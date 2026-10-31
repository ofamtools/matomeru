package main

import (
	ocli "github.com/unixsuperhero/ochazuke/cli"
	"github.com/unixsuperhero/ochazuke/sysstat"
)

// mtm's status segments ride ochazuke's header rule: cpu, mtm's own CPU
// (proc, scaled by status.proc-scale), ram and the free space of the data
// directory's volume are on by default, each
// behind its own status.segments.<id> setting, and status.segments hides
// them all. A user adds or changes rows in segments.json beside
// settings.json; a mounted extension adds the segments its catalog
// declares (mountExtensionSegments).

// addSegments registers the system segments, on, the disk one reading the
// data directory.
func (a *App) addSegments() error {
	return sysstat.Register(a.r, sysstat.Options{Enabled: true, DiskPath: a.dataDir()})
}

// mountExtensionSegments adds an extension's catalog segments, ids
// prefixed with its name, each against the proxy verb mtm mounted for it;
// their settings are in the Extensions tab.
// A row that cannot be added is a notice, never a failed mount.
func (a *App) mountExtensionSegments(x *extension, noun func(string) string) {
	rows := ocli.CatalogSegments(x.doc, x.name, noun)
	if len(rows) == 0 {
		return
	}
	for i := range rows {
		rows[i].Owner = extensionsOwner // their settings: the Extensions tab's Status bar
	}
	if err := a.r.AddSegment(rows...); err != nil {
		a.r.UI().Notify(x.name + ": segments: " + err.Error())
	}
}
