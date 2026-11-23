package app

import (
	"context"
	"errors"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ext"
	"ofam.dev/ochazuke/core"
)

// MountCatalog registers an extension's manifest schemas on the store's
// registry and remembers binary as the live read-only source for those nouns.
// Browsing any mounted noun serves from the async ReadCache and lays local
// shadow overrides (star, archive, snooze, local edits) over what the tool prints.
func (a *App) MountCatalog(binary string, m ext.Manifest) error {
	if err := ext.MountCatalog(a.Store.Registry(), m); err != nil {
		return err
	}
	if a.extCatalogs == nil {
		a.extCatalogs = map[string]string{}
	}
	if a.readCache == nil {
		a.readCache = data.NewReadCache()
	}
	for _, s := range m.Schemas {
		a.extCatalogs[s.Kind] = binary
		a.readCache.MarkStale(s.Kind)
	}
	a.gen++
	return nil
}

// isExternalKind reports whether kind is served by a mounted extension catalog.
func (a *App) isExternalKind(kind string) bool {
	return a.extCatalogs[kind] != ""
}

// externalRecords returns the cached records of a mounted external noun with
// Matomeru's local overrides overlaid, kicking off a background refresh when
// the cache has no snapshot or the snapshot is stale.
func (a *App) externalRecords(kind string) ([]data.Record, bool) {
	binary := a.extCatalogs[kind]
	if binary == "" {
		return nil, false
	}
	if a.readCache == nil {
		a.readCache = data.NewReadCache()
	}
	snap, ok := a.readCache.Get(kind)
	if !ok || snap.Stale {
		a.fetchExternal(kind, binary)
	}
	items := make([]data.Record, 0, len(snap.Items))
	for _, raw := range snap.Items {
		rec := ext.Overlay(a.Store, raw)
		if !a.showArchived && rec.IsArchived() {
			continue
		}
		items = append(items, rec)
	}
	return items, true
}

func (a *App) fetchExternal(kind, binary string) {
	if a.extFetching == nil {
		a.extFetching = map[string]bool{}
	}
	if a.extFetching[kind] {
		return
	}
	a.extFetching[kind] = true
	store, cache := a.Store, a.readCache
	a.background(func(ctx context.Context) func(core.State) core.State {
		res, err := ext.QueryExternal(ctx, binary, kind, store)
		return func(s core.State) core.State {
			delete(a.extFetching, kind)
			if err != nil {
				a.fail("query "+kind, err)
				return s
			}
			cache.Put(kind, res.Items, res.Total)
			a.gen++
			return s
		}
	})
}

// cachedExternal looks up ref in the ReadCache and lays any local shadow over it.
func (a *App) cachedExternal(ref core.Ref) (data.Record, bool) {
	if a.readCache == nil || !a.isExternalKind(ref.Kind) {
		return data.Record{}, false
	}
	snap, ok := a.readCache.Get(ref.Kind)
	if !ok {
		return data.Record{}, false
	}
	for _, raw := range snap.Items {
		if raw.Ref() == ref {
			return ext.Overlay(a.Store, raw), true
		}
	}
	return data.Record{}, false
}

// ensureShadow materializes Matomeru's shadow record for a cached external
// record if one does not exist in the store yet, so star, archive, snooze,
// local edits, Show pages and notes work without ever writing to the tool.
func (a *App) ensureShadow(ref core.Ref) error {
	if !a.isExternalKind(ref.Kind) {
		return nil
	}
	if _, err := a.Store.Get(ref); err == nil {
		return nil
	} else if !errors.Is(err, data.ErrNotFound) {
		return err
	}
	rec, ok := a.cachedExternal(ref)
	if !ok {
		return nil
	}
	_, err := ext.Shadow(a.Store, rec, User)
	return err
}
