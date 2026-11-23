package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/harness"
	"ofam.dev/oden"
)

const aboutDaemonTimeout = 2 * time.Second

var aboutConfigFileNames = []string{
	"settings.json",
	"bindings.json",
	"themes.json",
	"handlers.json",
	"harnesses.json",
	"actions.json",
	"drafts.json",
}

type aboutReport struct {
	Version     string         `json:"version"`
	GoVersion   string         `json:"goVersion"`
	Ochazuke    string         `json:"ochazukeVersion"`
	Oden        string         `json:"odenVersion"`
	Executable  string         `json:"executable,omitempty"`
	MTMHookPath string         `json:"mtmHookPath,omitempty"`
	Daemon      aboutDaemon    `json:"daemon"`
	Storage     aboutStorage   `json:"storage"`
	Config      aboutConfig    `json:"config"`
	Harnesses   []aboutHarness `json:"harnesses"`
}

type aboutPane struct {
	ID     string `json:"id"`
	PID    int    `json:"pid,omitempty"`
	Cmd    string `json:"cmd,omitempty"`
	Title  string `json:"title,omitempty"`
	Exited bool   `json:"exited,omitempty"`
}

type aboutDaemon struct {
	Running      bool        `json:"running"`
	PID          int         `json:"pid,omitempty"`
	Socket       string      `json:"socket"`
	MCPURL       string      `json:"mcpURL,omitempty"`
	PaneCount    int         `json:"paneCount"`
	Uptime       string      `json:"uptime,omitempty"`
	LivePanePIDs []int       `json:"livePanePIDs"`
	Panes        []aboutPane `json:"panes,omitempty"`
}

type aboutStorage struct {
	DBPath           string         `json:"dbPath"`
	DBExists         bool           `json:"dbExists"`
	DBBytes          int64          `json:"dbBytes"`
	TotalRecords     int            `json:"totalRecords"`
	RecordCounts     map[string]int `json:"recordCounts"`
	ManagedFilesDir  string         `json:"managedFilesDir,omitempty"`
	ManagedFileCount int            `json:"managedFileCount"`
}

type aboutConfigFile struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Bytes  int64  `json:"bytes,omitempty"`
}

type aboutConfig struct {
	Dir   string            `json:"dir"`
	Files []aboutConfigFile `json:"files"`
}

type aboutHarness struct {
	ID         string   `json:"id"`
	Label      string   `json:"label,omitempty"`
	Source     string   `json:"source,omitempty"`
	Command    []string `json:"command,omitempty"`
	Executable string   `json:"executable,omitempty"`
	Default    bool     `json:"default,omitempty"`
	Shell      bool     `json:"shell,omitempty"`
}

func mtmAboutCommand(g globalOpts) oden.Command {
	params := []oden.Parameter{
		{
			Name:        "socket",
			Type:        "string",
			Description: "Unix domain socket path (default: $MTM_SOCKET or ~/.local/share/matomeru/mtm.sock)",
		},
	}
	return oden.Command{
		Name:       "about",
		Summary:    "show mtm version, daemon, storage, config, and harness status",
		Category:   "Configuration",
		Usage:      "[--socket PATH] [--json]",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			if len(p.Positional()) != 0 {
				return errors.New("usage: mtm about [--socket PATH] [--json]")
			}
			rep, err := collectAbout(c.Context, g, p.String("socket"))
			if err != nil {
				return err
			}
			if p.Format() == "json" {
				return printJSON(c.Stdout, rep)
			}
			return writeAboutText(c.Stdout, rep)
		},
	}
}

func collectAbout(ctx context.Context, g globalOpts, socketFlag string) (aboutReport, error) {
	ochazukeVer, odenVer, goVer := buildModuleVersions()
	exe, _ := os.Executable()
	cfgDir := configDir()

	rep := aboutReport{
		Version:     mtmVersion,
		GoVersion:   goVer,
		Ochazuke:    ochazukeVer,
		Oden:        odenVer,
		Executable:  exe,
		MTMHookPath: findMTMHookPath(exe),
		Daemon:      collectAboutDaemon(ctx, socketFlag),
		Storage:     collectAboutStorage(g),
		Config:      collectAboutConfig(cfgDir),
		Harnesses:   collectAboutHarnesses(ctx, cfgDir),
	}
	return rep, nil
}

func buildModuleVersions() (ochazukeVer, odenVer, goVer string) {
	goVer = runtime.Version()
	ochazukeVer = "unknown"
	odenVer = "unknown"
	bi, ok := debug.ReadBuildInfo()
	if !ok || bi == nil {
		return ochazukeVer, odenVer, goVer
	}
	if bi.GoVersion != "" {
		goVer = bi.GoVersion
	}
	for _, dep := range bi.Deps {
		if dep == nil {
			continue
		}
		ver := dep.Version
		if dep.Replace != nil && dep.Replace.Version != "" {
			ver = dep.Replace.Version
		}
		switch dep.Path {
		case "ofam.dev/ochazuke":
			if ver != "" {
				ochazukeVer = ver
			}
		case "ofam.dev/oden":
			if ver != "" {
				odenVer = ver
			}
		}
	}
	return ochazukeVer, odenVer, goVer
}

func findMTMHookPath(exe string) string {
	if p, err := exec.LookPath("mtm-hook"); err == nil {
		return p
	}
	if exe != "" {
		sibling := filepath.Join(filepath.Dir(exe), "mtm-hook")
		if fi, err := os.Stat(sibling); err == nil && !fi.IsDir() {
			return sibling
		}
	}
	return ""
}

func collectAboutDaemon(ctx context.Context, socketFlag string) aboutDaemon {
	sock := daemon.ResolveSocketPath(socketFlag)
	out := aboutDaemon{
		Running:      false,
		Socket:       sock,
		LivePanePIDs: []int{},
	}
	client := daemon.NewClient(sock)
	client.Timeout = aboutDaemonTimeout

	dCtx, cancel := context.WithTimeout(ctx, aboutDaemonTimeout)
	defer cancel()

	st, err := client.Status(dCtx)
	if err != nil {
		return out
	}
	out.Running = true
	out.PID = st.PID
	if st.Socket != "" {
		out.Socket = st.Socket
	}
	out.MCPURL = st.MCPURL
	out.PaneCount = st.PaneCount
	out.Uptime = st.Uptime

	panes, err := client.List(dCtx)
	if err == nil && len(panes) > 0 {
		out.Panes = make([]aboutPane, 0, len(panes))
		for _, pn := range panes {
			out.Panes = append(out.Panes, aboutPane{
				ID:     pn.ID,
				PID:    pn.PID,
				Cmd:    pn.Cmd,
				Title:  pn.Title,
				Exited: pn.Exited,
			})
			if !pn.Exited && pn.PID > 0 {
				out.LivePanePIDs = append(out.LivePanePIDs, pn.PID)
			}
		}
	}
	return out
}

func collectAboutStorage(g globalOpts) aboutStorage {
	dbPath := g.db
	if dbPath == "" {
		dbPath, _ = defaultDB()
	}
	out := aboutStorage{
		DBPath:          dbPath,
		RecordCounts:    map[string]int{},
		ManagedFilesDir: data.FilesRoot(dbPath),
	}
	isMemory := dbPath == ":memory:" || strings.HasPrefix(dbPath, "file:")
	if !isMemory && dbPath != "" {
		if fi, err := os.Stat(dbPath); err == nil && !fi.IsDir() {
			out.DBExists = true
			out.DBBytes = fi.Size()
		}
	}
	if out.DBExists || isMemory || g.seed {
		store, err := openStoreWithSeed(g)
		if err == nil {
			defer store.Close()
			if res, qerr := store.Query(data.Query{IncludeArchived: true}); qerr == nil {
				for _, rec := range res.Items {
					out.RecordCounts[rec.Kind]++
					out.TotalRecords++
				}
			}
		}
	}
	if out.ManagedFilesDir != "" {
		out.ManagedFileCount = countRegularFiles(out.ManagedFilesDir)
	}
	return out
}

func countRegularFiles(root string) int {
	n := 0
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		n++
		return nil
	})
	return n
}

func collectAboutConfig(cfgDir string) aboutConfig {
	out := aboutConfig{
		Dir:   cfgDir,
		Files: make([]aboutConfigFile, 0, len(aboutConfigFileNames)),
	}
	for _, name := range aboutConfigFileNames {
		p := filepath.Join(cfgDir, name)
		item := aboutConfigFile{Name: name, Path: p}
		if cfgDir != "" {
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				item.Exists = true
				item.Bytes = fi.Size()
			}
		}
		out.Files = append(out.Files, item)
	}
	return out
}

func collectAboutHarnesses(ctx context.Context, cfgDir string) []aboutHarness {
	var cfg harness.FileConfig
	if cfgDir != "" {
		if loaded, err := harness.LoadFile(filepath.Join(cfgDir, "harnesses.json")); err == nil {
			cfg = loaded
		}
	}
	installed := probeOAIHarnesses(ctx)
	entries := harness.MergeInstalled(installed, cfg)
	out := make([]aboutHarness, 0, len(entries))
	defID := strings.ToLower(strings.TrimSpace(cfg.Default))
	for _, e := range entries {
		argv, _ := harness.BuildLaunchCommand(e, "", "")
		if len(e.MCPFlag) > 0 {
			argv = append(argv, e.MCPFlag...)
		}
		label := e.Label
		if label == "" {
			label = e.Name
		}
		out = append(out, aboutHarness{
			ID:         e.ID,
			Label:      label,
			Source:     e.Source,
			Command:    argv,
			Executable: e.Executable,
			Default:    defID != "" && e.ID == defID,
			Shell:      e.Shell,
		})
	}
	return out
}

func writeAboutText(w io.Writer, rep aboutReport) error {
	hook := rep.MTMHookPath
	if hook == "" {
		hook = "(not found)"
	}
	exe := rep.Executable
	if exe == "" {
		exe = "(unknown)"
	}

	fmt.Fprintln(w, "Matomeru (mtm)")
	fmt.Fprintf(w, "  version:      %s\n", rep.Version)
	fmt.Fprintf(w, "  go:           %s\n", rep.GoVersion)
	fmt.Fprintf(w, "  ochazuke:     %s\n", rep.Ochazuke)
	fmt.Fprintf(w, "  oden:         %s\n", rep.Oden)
	fmt.Fprintf(w, "  executable:   %s\n", exe)
	fmt.Fprintf(w, "  mtm-hook:     %s\n", hook)

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Daemon")
	if rep.Daemon.Running {
		statusLine := fmt.Sprintf("running (pid %d", rep.Daemon.PID)
		if rep.Daemon.Uptime != "" {
			statusLine += ", uptime " + rep.Daemon.Uptime
		}
		statusLine += ")"
		fmt.Fprintf(w, "  status:       %s\n", statusLine)
		fmt.Fprintf(w, "  socket:       %s\n", rep.Daemon.Socket)
		mcpURL := rep.Daemon.MCPURL
		if mcpURL == "" {
			mcpURL = "(disabled)"
		}
		fmt.Fprintf(w, "  mcpURL:       %s\n", mcpURL)
		if len(rep.Daemon.LivePanePIDs) > 0 {
			pids := make([]string, len(rep.Daemon.LivePanePIDs))
			for i, pid := range rep.Daemon.LivePanePIDs {
				pids[i] = strconv.Itoa(pid)
			}
			fmt.Fprintf(w, "  panes:        %d (live PIDs: %s)\n", rep.Daemon.PaneCount, strings.Join(pids, ", "))
		} else {
			fmt.Fprintf(w, "  panes:        %d\n", rep.Daemon.PaneCount)
		}
	} else {
		fmt.Fprintln(w, "  status:       stopped")
		fmt.Fprintf(w, "  socket:       %s\n", rep.Daemon.Socket)
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Storage")
	switch {
	case rep.Storage.DBExists:
		fmt.Fprintf(w, "  db:           %s (%d bytes)\n", rep.Storage.DBPath, rep.Storage.DBBytes)
	case rep.Storage.DBPath == ":memory:" || strings.HasPrefix(rep.Storage.DBPath, "file:"):
		fmt.Fprintf(w, "  db:           %s\n", rep.Storage.DBPath)
	default:
		fmt.Fprintf(w, "  db:           %s (not created)\n", rep.Storage.DBPath)
	}
	if len(rep.Storage.RecordCounts) == 0 {
		fmt.Fprintf(w, "  records:      %d total\n", rep.Storage.TotalRecords)
	} else {
		kinds := make([]string, 0, len(rep.Storage.RecordCounts))
		for k := range rep.Storage.RecordCounts {
			kinds = append(kinds, k)
		}
		slices.Sort(kinds)
		parts := make([]string, len(kinds))
		for i, k := range kinds {
			parts[i] = fmt.Sprintf("%s=%d", k, rep.Storage.RecordCounts[k])
		}
		fmt.Fprintf(w, "  records:      %d total (%s)\n", rep.Storage.TotalRecords, strings.Join(parts, ", "))
	}
	if rep.Storage.ManagedFilesDir != "" {
		fmt.Fprintf(w, "  managedDir:   %s (%d files)\n", rep.Storage.ManagedFilesDir, rep.Storage.ManagedFileCount)
	} else {
		fmt.Fprintln(w, "  managedDir:   (none)")
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Config")
	fmt.Fprintf(w, "  dir:          %s\n", rep.Config.Dir)
	twCfg := tabwriter.NewWriter(w, 2, 8, 2, ' ', 0)
	for _, f := range rep.Config.Files {
		state := "missing"
		if f.Exists {
			state = fmt.Sprintf("present (%d bytes)", f.Bytes)
		}
		fmt.Fprintf(twCfg, "  %s\t%s\n", f.Name, state)
	}
	if err := twCfg.Flush(); err != nil {
		return err
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Harnesses")
	twH := tabwriter.NewWriter(w, 2, 8, 2, ' ', 0)
	fmt.Fprintln(twH, "  ID\tLABEL\tSOURCE\tCOMMAND")
	for _, h := range rep.Harnesses {
		id := h.ID
		if h.Default {
			id += "*"
		}
		fmt.Fprintf(twH, "  %s\t%s\t%s\t%s\n", id, h.Label, h.Source, strings.Join(h.Command, " "))
	}
	return twH.Flush()
}
