package main

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"ofam.dev/ochazuke"
)

const settingExtAutoRefresh = "extensions.auto-refresh"

var extAutoRefreshSetting = ochazuke.Setting{Name: settingExtAutoRefresh, Owner: extensionsOwner, Section: ochazuke.SettingsGeneralSection,
	Type: ochazuke.SettingBoolean, Default: true,
	Description: "Refresh changed extension catalogs automatically on startup and page open; off keeps the cached catalog and marks it stale"}

// refreshStaleExtensions checks the catalogs off the loop at startup and
// when an extension tab opens. It uses discovery's exact stale mechanism,
// limited to registered tools. The old catalog remains usable throughout.
func (a *App) refreshStaleExtensions(names ...string) tea.Cmd {
	if a.cli {
		return nil
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
	return a.r.PendingCommands()
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
