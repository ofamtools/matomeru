package app

import (
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/handlers"
	"ofam.dev/matomeru/harness"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/form"
)

const (
	harnessesFileName  = "harnesses.json"
	oaiDiscoverTimeout = 6 * time.Second
)

// --- Harness catalog + async o-ai discovery --------------------------------------

func defaultInstalledHarnesses() []harness.InstalledHarness {
	defs := harness.DefaultConfigs()
	out := make([]harness.InstalledHarness, 0, len(defs))
	for id := range defs {
		if id == "antigravity" {
			if _, hasAgy := defs["agy"]; hasAgy {
				continue
			}
		}
		out = append(out, harness.InstalledHarness{ID: id, Installed: true})
	}
	return out
}

func (a *App) loadHarnesses(configDir string) {
	var cfg harness.FileConfig
	if configDir != "" {
		loaded, err := harness.LoadFile(filepath.Join(configDir, harnessesFileName))
		if err != nil {
			a.warn("%s: %v", harnessesFileName, err)
		} else {
			cfg = loaded
		}
	}
	entries := harness.MergeInstalled(defaultInstalledHarnesses(), cfg)
	a.rt.harnessMu.Lock()
	a.rt.harnessCfg = cfg
	a.rt.harnessEntries = entries
	a.rt.harnessMu.Unlock()
}

// Harnesses returns a copy of the current merged harness catalog entries.
func (a *App) Harnesses() []harness.Entry {
	a.rt.harnessMu.RLock()
	defer a.rt.harnessMu.RUnlock()
	return slices.Clone(a.rt.harnessEntries)
}

// SetInstalledHarnesses merges installed inventory from o-ai with harnesses.json
// and updates the app's harness catalog.
func (a *App) SetInstalledHarnesses(installed []harness.InstalledHarness) []harness.Entry {
	a.rt.harnessMu.Lock()
	merged := harness.MergeInstalled(installed, a.rt.harnessCfg)
	a.rt.harnessEntries = merged
	a.rt.harnessMu.Unlock()
	if rf := a.activeRecordForm(); rf != nil && rf.kind == data.KindPane {
		a.applyHarnessChoices(rf)
	}
	a.viewMemo.gen++
	return slices.Clone(merged)
}

func (a *App) lookupHarness(name string) (harness.Entry, bool) {
	want := strings.ToLower(strings.TrimSpace(name))
	if want == "" {
		return harness.Entry{}, false
	}
	alias := harness.AliasID(want)
	a.rt.harnessMu.RLock()
	defer a.rt.harnessMu.RUnlock()
	for _, e := range a.rt.harnessEntries {
		if strings.EqualFold(e.ID, want) || strings.EqualFold(e.Name, want) {
			return e, true
		}
	}
	if alias != "" {
		for _, e := range a.rt.harnessEntries {
			if strings.EqualFold(e.ID, alias) || strings.EqualFold(e.Name, alias) {
				return e, true
			}
		}
	}
	return harness.Entry{}, false
}

func (a *App) harnessChoices(current string) []string {
	entries := a.Harnesses()
	choices := make([]string, 0, len(entries)+1)
	for _, e := range entries {
		if e.ID != "" && !slices.Contains(choices, e.ID) {
			choices = append(choices, e.ID)
		}
	}
	if cur := strings.TrimSpace(current); cur != "" && !slices.Contains(choices, cur) {
		choices = append(choices, cur)
	}
	return choices
}

func (a *App) defaultDirForTask(taskRef core.Ref) string {
	if a.Store == nil || taskRef.Kind != data.KindTask || taskRef.ID == "" {
		return ""
	}
	cur, err := a.Store.Get(taskRef)
	if err != nil || cur == nil {
		return ""
	}
	seen := map[string]bool{cur.ID: true}
	for range 32 {
		if dir := strings.TrimSpace(cur.Text("defaultDir")); dir != "" {
			return dir
		}
		parentRef, hasParent := cur.RefField("parent")
		if !hasParent || parentRef.Kind != data.KindTask || parentRef.ID == "" || seen[parentRef.ID] {
			break
		}
		parent, err := a.Store.Get(parentRef)
		if err != nil || parent == nil {
			break
		}
		seen[parent.ID] = true
		cur = parent
	}
	return ""
}

func (a *App) applyHarnessChoices(rf *recordFormModal) {
	if rf == nil {
		return
	}
	a.applyHarnessChoicesToForm(rf.kind, rf.ref, rf.Model())
}

func (a *App) applyHarnessChoicesToForm(kind string, ref *core.Ref, fm *form.Model) {
	if fm == nil || kind != data.KindPane {
		return
	}
	wasDirty := fm.Dirty
	current := data.Text(fm.Values["harness"])
	choices := a.harnessChoices(current)
	if len(choices) > 0 {
		for i := range fm.Specs {
			if fm.Specs[i].Name != "harness" {
				continue
			}
			fm.Specs[i].Enum = choices
			if ref == nil && strings.TrimSpace(current) == "" {
				fm.Specs[i].Default = choices[0]
				fm.SetValue("harness", choices[0])
			}
			break
		}
	}
	// Pre-fill cwd on New Pane forms from the selected task/project's defaultDir,
	// falling back to os.Getwd() so the user always sees the launch directory.
	if ref == nil && strings.TrimSpace(data.Text(fm.Values["cwd"])) == "" {
		dir := ""
		if taskRef, ok := data.AsRef(fm.Values["task"]); ok {
			dir = a.defaultDirForTask(taskRef)
		}
		if dir == "" {
			if wd, err := os.Getwd(); err == nil {
				dir = wd
			}
		}
		if dir != "" {
			fm.SetValue("cwd", dir)
		}
	}
	fm.Dirty = wasDirty
}

func findOAIBinary(searchDirs []string, storeBinary string) string {
	if storeBinary != "" {
		if info, err := os.Stat(storeBinary); err == nil && !info.IsDir() {
			return storeBinary
		}
	}
	for _, dir := range searchDirs {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, "o-ai")
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath("o-ai"); err == nil {
		return p
	}
	return ""
}

func installedFromRecords(items []data.Record) []harness.InstalledHarness {
	out := make([]harness.InstalledHarness, 0, len(items))
	for _, rec := range items {
		id := rec.ID
		if v := rec.Text("id"); v != "" {
			id = v
		}
		out = append(out, harness.InstalledHarness{
			ID:            id,
			Installed:     rec.Bool("installed"),
			Configured:    rec.Bool("configured"),
			Executable:    rec.Text("executable"),
			ProcessStatus: rec.Text("processStatus"),
			CatalogStatus: rec.Text("catalogStatus"),
		})
	}
	return out
}

// startHarnessDiscovery runs one async background goroutine (tracked by
// a.live.bg, never blocking the UI loop) that discovers installed harnesses
// from cached ai.harness records or `o-ai query --kind ai.harness` and merges
// them with harnesses.json via harness.MergeInstalled.
func (a *App) startHarnessDiscovery() {
	dirs := append(slices.Clone(a.opts.ExtensionDirs), a.cfg.Settings.Strings("extensionDirs", nil)...)
	for i, d := range dirs {
		dirs[i] = expandHome(d)
	}
	storeBinary := ""
	if a.Store != nil {
		if rec, err := a.Store.GetByField(data.KindExtension, "name", "o-ai"); err == nil && rec != nil {
			storeBinary = rec.Text("binary")
		}
	}
	a.rt.harnessMu.RLock()
	cfg := a.rt.harnessCfg
	a.rt.harnessMu.RUnlock()
	cache := a.ext.readCache

	a.background(func(ctx context.Context) func(core.State) core.State {
		var installed []harness.InstalledHarness
		if cache != nil {
			if snap, ok := cache.Get("ai.harness"); ok && len(snap.Items) > 0 {
				installed = installedFromRecords(snap.Items)
			}
		}
		if len(installed) == 0 {
			bin := findOAIBinary(dirs, storeBinary)
			if bin == "" {
				return nil
			}
			pctx, cancel := context.WithTimeout(ctx, oaiDiscoverTimeout)
			defer cancel()
			out, err := exec.CommandContext(pctx, bin, "query", "--kind", "ai.harness").Output()
			if err != nil {
				return nil
			}
			parsed, err := harness.ParseOAIJSON(out)
			if err != nil || len(parsed) == 0 {
				return nil
			}
			installed = parsed
		}
		merged := harness.MergeInstalled(installed, cfg)
		return func(s core.State) core.State {
			a.rt.harnessMu.Lock()
			a.rt.harnessEntries = merged
			a.rt.harnessMu.Unlock()
			if rf := a.activeRecordForm(); rf != nil && rf.kind == data.KindPane {
				a.applyHarnessChoices(rf)
			}
			a.viewMemo.gen++
			return s
		}
	})
}

func shellJoin(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	parts := make([]string, len(argv))
	for i, arg := range argv {
		if arg == "" || strings.ContainsAny(arg, " \t\n\r'\"$`\\;&|<>()*?[]{}~#!") {
			parts[i] = handlers.Quote(arg)
		} else {
			parts[i] = arg
		}
	}
	return strings.Join(parts, " ")
}

// buildPaneLaunch resolves the command and extra environment variables for
// launching pane. If pane's harness matches a catalog entry, BuildLaunchCommand
// supplies the launch command (when pane.command is empty) and entry.Env is
// merged into the returned env map.
func (a *App) buildPaneLaunch(pane data.Record) (string, map[string]string) {
	cmd := pane.Text("command")
	env := map[string]string{}
	if entry, ok := a.lookupHarness(pane.Text("harness")); ok {
		argv, hEnv := harness.BuildLaunchCommand(entry, a.paneMCPURL(pane.ID), pane.ID)
		maps.Copy(env, hEnv)
		if cmd == "" && len(argv) > 0 {
			cmd = shellJoin(argv)
		}
	}
	return cmd, env
}
