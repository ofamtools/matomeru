package main

import (
	tea "charm.land/bubbletea/v2"
	"github.com/unixsuperhero/ochazuke"
)

// A registered extension's catalog is read when it is registered or
// refreshed, with its bin's stamp (size and mtime, links resolved:
// extBinStamp). A rebuilt bin answers another catalog (new verbs, new
// keys), so the TUI checks the stamps when it starts and when an
// extension's page is opened, and refreshes each extension whose bin
// changed through extension.refresh, the same verb as `mtm extension
// refresh NAME`. The refresh runs off the loop: the old catalog stays
// mounted until the new one lands (the sync re-mounts it), and a failed
// refresh keeps it and says why (the failed job's error line, as for a
// refresh run from the palette). A bin that no longer stats is left to
// discovery (missing). Size and mtime are enough: a rebuild changes the
// mtime, and discovery compares the same stamp.

// staleExtensions are the named extensions (every registered one when
// none is named) whose bin's stamp is not the one stored with their
// catalog, and that this mtm did not already try to refresh at this stamp
// or is refreshing now. A registry file that does not read is skipped
// (mounting reports it).
func (a *App) staleExtensions(names ...string) []string {
	reg := a.extRegistry()
	if len(names) == 0 {
		var err error
		if names, err = reg.Names(); err != nil {
			return nil
		}
	}
	var out []string
	for _, name := range names {
		e, err := reg.Read(name)
		if err != nil {
			continue
		}
		stamp := binStamp(e.Bin)
		if stamp == nil || e.BinStamp != nil && *e.BinStamp == *stamp {
			continue
		}
		if tried, ok := a.extAutoTried[name]; ok && tried == *stamp || a.extPage.fetching[name] > 0 {
			continue
		}
		if a.extAutoTried == nil {
			a.extAutoTried = map[string]extBinStamp{}
		}
		a.extAutoTried[name] = *stamp
		out = append(out, name)
	}
	return out
}

// refreshStaleExtensions runs extension.refresh for each stale extension
// (staleExtensions) and returns the commands it scheduled. A refresh that
// cannot start is an error line, never a failed start.
func (a *App) refreshStaleExtensions(names ...string) tea.Cmd {
	if a.cli {
		return nil
	}
	stale := a.staleExtensions(names...)
	for _, name := range stale {
		if _, err := a.r.Execute(ochazuke.Request{Noun: "extension", Verb: "refresh", Args: ochazuke.Arguments{"name": name}}); err != nil {
			a.r.UI().Error("extension " + name + ": refresh: " + err.Error())
		}
	}
	if len(stale) == 0 {
		return nil
	}
	return a.r.PendingCommands()
}
