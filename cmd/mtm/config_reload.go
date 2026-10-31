package main

import (
	"path/filepath"
	"slices"
	"strings"

	"ofam.dev/matomeru/internal/harness"
)

// mtm's own configuration files join ochazuke's config.reload (`mtm config
// reload`, the palette's config.reload): after themes.json, settings.json,
// actions.json and bindings.json, it reads harnesses.json, dirs.json and sides.json
// again. A file that does not read is a problem naming it; the old
// configuration stays. Not hooked: the extension registry (extensions/ in
// the data directory) is state the extension verbs write, synced already
// whenever a page opens; `mtm extension refresh` reads catalogs again.

// harnessOps are the verbs whose harness parameter picks from the registry.
var harnessOps = []string{"agent.launch", "agent.start", "project.launch", "task.launch"}

// defineConfigReload hooks mtm's files into config.reload.
func (a *App) defineConfigReload() {
	a.r.OnConfigReload(filepath.Base(a.opts.Harnesses), a.reloadHarnesses)
	a.r.OnConfigReload("dirs.json", a.reloadDirsConfig)
	a.r.OnConfigReload(sidesFile, a.reloadSides)
}

// reloadHarnesses reads the harness file again over the built-ins; the
// harness parameters' choices follow. A bad file keeps the registry.
// The default-harness setting's choices stay as they were declared:
// ochazuke has no way to change a declared setting's enum.
func (a *App) reloadHarnesses() []string {
	reg, err := harness.Load(a.opts.Harnesses)
	if err != nil {
		return []string{strings.TrimPrefix(err.Error(), a.opts.Harnesses+": ")}
	}
	a.harnesses = reg
	if a.locator != nil {
		a.locator.Set(reg.List())
	}
	a.setHarnessChoices()
	return nil
}

// setHarnessChoices points every harness parameter at the registry's names.
func (a *App) setHarnessChoices() {
	names := a.harnesses.Names()
	for _, key := range harnessOps {
		op, ok := a.r.Operations[a.r.CanonicalKey(key)]
		if !ok {
			continue
		}
		p, ok := op.Parameters["harness"]
		if !ok {
			continue
		}
		p.Enum = slices.Clone(names)
		op.Parameters["harness"] = p
		a.r.Operations[a.r.CanonicalKey(key)] = op
	}
}

// reloadDirsConfig checks dirs.json (the bases are read on every use) and,
// in the TUI, shows the bases again and rescans when a scan had run. A bad
// file leaves the lists and the last scan as they were.
func (a *App) reloadDirsConfig() []string {
	if _, err := a.projectBases(); err != nil {
		return []string{err.Error()}
	}
	if a.cli || a.baseList == nil {
		return nil
	}
	a.refreshBases()
	if !a.directories.Ready && !a.directories.Loading && len(a.directories.Rows) == 0 {
		return nil
	}
	a.directories.Ready = false
	if err := a.loadDirectories(); err != nil {
		return []string{err.Error()}
	}
	return nil
}
