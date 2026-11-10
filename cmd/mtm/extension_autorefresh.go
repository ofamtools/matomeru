package main

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

const settingExtAutoRefresh = "extensions.auto-refresh"

var extAutoRefreshSetting = ochazuke.Setting{Name: settingExtAutoRefresh, Owner: extensionsOwner, Section: ochazuke.SettingsGeneralSection,
	Type: ochazuke.SettingBoolean, Default: true,
	Description: "Refresh changed extension catalogs automatically on startup and page open; off keeps the cached catalog and marks it stale"}

// refreshStaleExtensions checks the catalogs off the loop at startup and
// when an extension tab opens. It uses discovery's exact stale mechanism,
// limited to registered tools. The old catalog remains usable throughout.
func (a *App) refreshStaleExtensions(names ...string) tea.Cmd {
	a.checkStaleExtensions(names...)
	return a.r.PendingCommands()
}

// checkStaleExtensions launches the check; its jobs start with the next
// PendingCommands.
func (a *App) checkStaleExtensions(names ...string) {
	if a.cli {
		return
	}
	d := a.discovery()
	d.registeredOnly, d.names = true, names
	res := ochazuke.Pending(func(ctx context.Context) (any, error) { return d.run(ctx) }).
		Replacing("extension-catalog-check:" + strings.Join(names, ",")).
		Then(func(ctx ochazuke.Ctx, v any, err error) {
			if err != nil {
				ctx.Notify("extension catalog check: " + err.Error())
				return
			}
			a.autoRefreshCatalogs(v.([]extCandidate))
		})
	ctx := ochazuke.Ctx{Runtime: a.r, Context: a.r.Context(), Instance: a.r.Instances["app"]}
	if _, err := a.r.Launch("app", "extension.available", res, ctx); err != nil {
		a.r.UI().Notify("extension catalog check: " + err.Error())
	}
}

// autoRefreshCatalogs adopts successful stale answers without executing a
// second catalog request. Failures keep the cache; the next open probes
// again, so recovery is detectable even if the bin stamp never changes.
// Completion runs on the loop, and never overwrites a newer registration.
func (a *App) autoRefreshCatalogs(rows []extCandidate) {
	if !a.r.Settings().Bool(settingExtAutoRefresh) {
		return
	}
	reg := a.extRegistry()
	for _, c := range rows {
		if c.Status != statusStale || c.stored == nil {
			continue
		}
		e, err := reg.Read(c.Name)
		if err != nil || e.Bin != c.Bin || !e.RegisteredAt.Equal(c.RegisteredAt) || !catalogEqual(e.Catalog, *c.stored) {
			continue
		}
		if c.catalogErr != nil {
			a.extFailed(c.Name, c.catalogErr.Error())
			a.r.UI().Notify("extension " + c.Name + ": catalog refresh failed: " + c.catalogErr.Error())
			continue
		}
		if err := a.storeExtensionCatalog(reg, c.Name, e, c.catalog, c.stamp); err != nil {
			a.r.UI().Notify("extension " + c.Name + ": catalog refresh failed: " + err.Error())
			continue
		}
		a.r.UI().Notify("extension " + c.Name + ": catalog refreshed")
	}
}

// extStampRefreshTimeout bounds the catalog read of a mount-time refresh,
// which runs on the loop.
const extStampRefreshTimeout = 10 * time.Second

// refreshChangedBins reads again the catalog of every registered tool whose
// bin changed (refreshChangedBin) and remounts what that changed. The CLI
// and the MCP gateway call it before their commands are built; the TUI has
// the background check above.
func (a *App) refreshChangedBins() {
	reg := a.extRegistry()
	// The registry mounted a moment ago (mountExtensions) is not read again.
	snap := a.startRegistry
	a.startRegistry = nil
	var names []string
	var entries map[string]extEntry
	if snap != nil {
		names, entries = snap.names, snap.entries
	} else {
		names, entries, _ = a.orderedForMount()
	}
	changed := false
	for _, name := range names {
		if e := a.refreshChangedBin(reg, name, entries[name]); e.BinStamp != entries[name].BinStamp {
			changed = true
		}
	}
	if changed {
		a.syncExtensions()
	}
}

// refreshChangedBin is the entry as it should mount: when the registered
// bin's file differs from the stamp its catalog was read at (an entry with
// no stamp is left to the background check), the bin is asked its catalog again and the entry is written
// with it, so a reinstalled tool is mounted with the names it declares now
// whether mtm runs as the TUI, a CLI command or the MCP gateway (the
// background check above runs in the TUI only). A bin that does not
// answer, or the setting off, keeps the cached catalog.
func (a *App) refreshChangedBin(reg extRegistry, name string, e extEntry) extEntry {
	if !a.r.Settings().Bool(settingExtAutoRefresh) {
		return e
	}
	now := binStamp(e.Bin)
	if now == nil || e.BinStamp == nil || *e.BinStamp == *now {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), extStampRefreshTimeout)
	defer cancel()
	out, err := execExt(ctx, e.Bin, a.extEnv(name), "catalog")
	var doc ocli.CatalogDoc
	if err == nil {
		doc, err = parseExtCatalog(filepath.Base(e.Bin), out)
	}
	if err != nil {
		a.r.UI().Notify("extension " + name + ": catalog refresh failed: " + err.Error())
		return e
	}
	e.Catalog, e.BinStamp = doc, now
	if err := reg.write(name, e); err != nil {
		a.r.UI().Notify("extension " + name + ": catalog refresh failed: " + err.Error())
	}
	return e
}
