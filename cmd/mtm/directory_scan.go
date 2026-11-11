package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

const (
	settingDirectoryProbe     = "directories.probe-defaults"
	settingDirectoryAddChoice = "directories.add-choice"
	settingDirectoryStatus    = "directories.show-status"
	settingDirectorySelect    = "directories.select-added"
	settingDirectoryTimeout   = "directories.scan-timeout"
)

func directorySettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingDirectoryProbe, Type: ochazuke.SettingBoolean, Default: false, Description: "Opt in to probing ~/proj, ~/projects, ~/src, ~/code and ~/dev when no scan bases are saved; existing users' legacy bases are imported once regardless"},
		{Name: settingDirectoryAddChoice, Type: ochazuke.SettingBoolean, Default: true, Description: "Ask what to add before the directory picker: an ordinary directory or a base to scan; off keeps the standard create form"},
		{Name: settingDirectoryStatus, Type: ochazuke.SettingBoolean, Default: true, Description: "Show scan progress and unavailable paths on directory rows"},
		{Name: settingDirectorySelect, Type: ochazuke.SettingBoolean, Default: true, Description: "Select a newly added directory on the Directories page"},
		{Name: settingDirectoryTimeout, Type: ochazuke.SettingDuration, Default: 750 * time.Millisecond, Description: "Maximum wait for one filesystem scan or directory inspection; a slow path is unavailable, never a startup dependency"},
	}
}

// Filesystem calls cannot be cancelled in Go. A bounded worker pool caps stuck
// mount calls, and a timed-out call retains its slot until the kernel returns.
// A later scan never waits for a slot. No goroutine writes runtime state.
type directoryIO struct{ slots chan struct{} }

func newDirectoryIO() *directoryIO { return &directoryIO{slots: make(chan struct{}, 4)} }
func (io *directoryIO) run(ctx context.Context, timeout time.Duration, work func()) error {
	select {
	case io.slots <- struct{}{}:
	default:
		return errors.New("filesystem workers busy")
	}
	done := make(chan struct{})
	go func() { defer func() { <-io.slots; close(done) }(); work() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("filesystem timed out")
	}
}

type directoryProbeResult struct {
	paths   []string
	missing []string
	key     string
}

func (a *App) scanDirectoryBases(ctx context.Context, bases []projectBase, listable func(string) bool, timeout time.Duration) ([]string, map[string]string, map[string]string) {
	paths := []string{}
	unavailable := map[string]string{}
	keys := map[string]string{}
	for _, base := range bases {
		if listable != nil && !listable(base.Path) {
			continue
		}
		// The worker owns its result until done closes. On timeout nobody reads it.
		result := &directoryProbeResult{}
		err := a.directoryIO.run(ctx, timeout, func() {
			result.paths, result.missing = scanBases([]projectBase{base}, listable)
			result.key = directoryKey(base.Path)
		})
		if err != nil {
			unavailable[base.Path] = err.Error()
			continue
		}
		keys[base.Path] = result.key
		if len(result.missing) > 0 {
			unavailable[base.Path] = "missing or unreadable"
			continue
		}
		paths = append(paths, result.paths...)
	}
	return paths, unavailable, keys
}

// probeDirectoryDefaults is shared by the one-time migration and the opt-in
// setting. Its only caller is the background scan; tests supply a temp HOME.
func (a *App) probeDirectoryDefaults(ctx context.Context, timeout time.Duration) []projectBase {
	var bases []projectBase
	for _, name := range []string{"proj", "projects", "src", "code", "dev"} {
		path := a.expandPath("~/" + name)
		var found bool
		if err := a.directoryIO.run(ctx, timeout, func() { info, err := os.Stat(path); found = err == nil && info.IsDir() }); err == nil && found {
			bases = append(bases, projectBase{Path: path, Depth: 1, Source: "default"})
		}
	}
	return bases
}

// migrateDirectoryProbe claims the migration in the same transaction as its
// writes. A user edit made while the probe was running wins; it is never replaced.
func (a *App) migrateDirectoryProbe(ctx context.Context, timeout time.Duration) (string, error) {
	var state directoryState
	if err := a.db.Get("directory_state", directoryStateID, &state); err != nil {
		return "", err
	}
	if !state.ProbePending {
		return "", nil
	}
	bases := a.probeDirectoryDefaults(ctx, timeout)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	imported := []string{}
	err := a.db.Tx([]string{"directories", "directory_state"}, func(tx *store.Tx) error {
		if err := tx.Get("directory_state", directoryStateID, &state); err != nil {
			return err
		}
		if !state.ProbePending {
			return nil
		}
		records, err := tx.All("directories")
		if err != nil {
			return err
		}
		if len(records) == 0 {
			for _, base := range bases {
				if err := tx.Put("directories", base.Path, directoryRecord{Path: base.Path, Depth: 1, Source: "migrated"}); err != nil {
					return err
				}
				imported = append(imported, base.Path)
			}
		}
		state.ProbePending = false
		return tx.Put("directory_state", state.ID, state)
	})
	if err != nil || len(imported) == 0 {
		return "", err
	}
	return "Imported legacy directory bases once: " + strings.Join(imported, ", ") + ". Remove or archive them on Directories; default probing is now off.", nil
}

// directoryScanOptions is copied on the UI loop before starting work. The job
// never reads Settings or calls a runtime path-policy method from a goroutine.
type directoryScanOptions struct {
	timeout  time.Duration
	probe    bool
	listable func(string) bool
}

func (a *App) directoryScanOptions() directoryScanOptions {
	prefixes := append([]string(nil), a.r.Settings().Strings("path.no-auto-list")...)
	return directoryScanOptions{
		timeout: positive(a.r.Settings().Duration(settingDirectoryTimeout), 750*time.Millisecond),
		probe:   a.r.Settings().Bool(settingDirectoryProbe),
		listable: func(path string) bool {
			path = filepath.Clean(path)
			for _, prefix := range prefixes {
				prefix = a.expandPath(prefix)
				if path == prefix || strings.HasPrefix(path, strings.TrimSuffix(prefix, "/")+"/") {
					return false
				}
			}
			return true
		},
	}
}

func directoryScanInputs(records []directoryRecord) ([]projectBase, []string, map[string]bool) {
	var bases []projectBase
	var paths []string
	archived := map[string]bool{}
	for _, row := range records {
		archived[row.Path] = row.Archived
		if row.Archived {
			continue
		}
		if row.Depth > 0 {
			bases = append(bases, projectBase{Path: row.Path, Depth: row.Depth, Source: row.Source})
		} else {
			paths = append(paths, row.Path)
		}
	}
	return bases, paths, archived
}

func (a *App) inspectKnownDirectory(ctx context.Context, path string, options directoryScanOptions) (knownDirectory, string) {
	row := knownDirectory{Tree: work.Tree{Kind: "directory", Path: a.expandPath(path)}}
	if !options.listable(path) {
		return row, ""
	}
	result := &knownDirectory{Tree: row.Tree}
	var missing bool
	err := a.directoryIO.run(ctx, options.timeout, func() {
		info, err := os.Stat(path)
		missing = err != nil || !info.IsDir()
		if !missing {
			result.Tree = inspectDirectory(path)
			result.Path = directoryKey(path)
			if result.Repo != "" {
				result.Common = gitCommonDir(path)
			}
		}
	})
	if err != nil {
		return row, err.Error()
	}
	if missing {
		return row, "missing or unreadable"
	}
	return *result, ""
}

// External sources enrich an explicitly populated list; they never seed a fresh
// app. This is the existing o-proj/TUIOS source, with no runtime access.
func (a *App) externalDirectoryPaths(ctx context.Context) ([]string, []string, error) {
	paths, manifestErr := a.manifestDirs(ctx)
	sessions, daemonErr := a.daemon.Sessions(ctx)
	seeds := map[string]int64{}
	for _, session := range sessions {
		last := session.LastActive
		if last > 0 && last < 1_000_000_000_000 {
			last *= 1000
		}
		if filepath.IsAbs(session.Dir) || strings.HasPrefix(session.Dir, "~/") {
			paths = append(paths, session.Dir)
			seeds[session.Dir] = max(seeds[session.Dir], last)
		}
		panes, err := a.daemon.Panes(ctx, session.Name)
		if err != nil {
			daemonErr = err
			continue
		}
		for _, pane := range panes {
			if pane.Cwd != "" {
				paths = append(paths, pane.Cwd)
				seeds[pane.Cwd] = max(seeds[pane.Cwd], last)
			}
		}
	}
	var rows []work.DirRecent
	for path, last := range seeds {
		rows = append(rows, work.DirRecent{Path: path, LastUsed: last})
	}
	var footer []string
	if daemonErr != nil {
		footer = append(footer, "TUIOS unavailable; its sessions were not scanned")
	}
	if manifestErr != nil {
		footer = append(footer, manifestErr.Error())
	}
	return paths, footer, a.work.SeedDirs(rows)
}

func directoryScanFooter(unavailable map[string]string, footer []string) string {
	if len(unavailable) > 0 {
		var paths []string
		for path := range unavailable {
			paths = append(paths, path)
		}
		slices.Sort(paths)
		footer = append(footer, "unavailable paths: "+strings.Join(paths, ", "))
	}
	return strings.Join(footer, "; ")
}
