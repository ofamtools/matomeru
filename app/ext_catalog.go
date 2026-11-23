package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/actions"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ext"
	"ofam.dev/matomeru/runs"
	"ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/toast"
)

// ExtensionController owns the extension manager, async external-record cache,
// mounted catalogs, in-flight fetch tracking, and declarative record actions.
type ExtensionController struct {
	Extensions    *ext.Manager
	readCache     *data.ReadCache
	extCatalogs   map[string]string
	extFetching   map[string]bool
	recordActions map[string]actions.RecordActionSpec
}

func newExtensionController() *ExtensionController {
	return &ExtensionController{
		readCache:     data.NewReadCache(),
		extCatalogs:   map[string]string{},
		extFetching:   map[string]bool{},
		recordActions: map[string]actions.RecordActionSpec{},
	}
}

// MountCatalog registers an extension's manifest schemas, declarative record
// action commands, and extension-scoped bindings, then rebuilds the live keymap
// so user bindings.json always overrides extension bindings.
func (a *App) MountCatalog(binary string, m ext.Manifest) error {
	if err := ext.MountCatalog(a.Store.Registry(), m); err != nil {
		return err
	}
	if a.ext.extCatalogs == nil {
		a.ext.extCatalogs = map[string]string{}
	}
	if a.ext.readCache == nil {
		a.ext.readCache = data.NewReadCache()
	}
	for _, s := range m.Schemas {
		a.ext.extCatalogs[s.Kind] = binary
		a.ext.readCache.MarkStale(s.Kind)
	}

	var addedBindings []core.Binding
	for _, cmd := range m.Commands {
		spec, ok := cmd.ToRecordActionSpec(binary, m.Name)
		if !ok {
			continue
		}
		a.registerRecordAction(spec)
		if spec.Key != "" {
			keySeq := strings.Fields(spec.Key)
			if len(keySeq) > 0 {
				hint := spec.KeyHint
				if hint == "" {
					hint = spec.Description
				}
				if hint == "" {
					hint = spec.ID
				}
				scopes := spec.Kinds
				if len(scopes) == 0 {
					scopes = []string{""}
				}
				for _, sc := range scopes {
					addedBindings = append(addedBindings, core.Binding{
						Mode:    ModeNormal,
						Scope:   sc,
						Keys:    keySeq,
						Command: spec.ID,
						Hint:    hint,
					})
				}
			}
		}
	}
	addedBindings = append(addedBindings, m.Bindings...)

	for _, b := range addedBindings {
		if b.Mode == "" {
			b.Mode = ModeNormal
		}
		idx := slices.IndexFunc(a.bind.extBindings, func(existing core.Binding) bool {
			return existing.Mode == b.Mode && existing.Scope == b.Scope && existing.Command == b.Command && slices.Equal(existing.Keys, b.Keys)
		})
		if idx >= 0 {
			a.bind.extBindings[idx] = b
		} else {
			a.bind.extBindings = append(a.bind.extBindings, b)
		}
	}

	// Re-validate raw user bindings now that mounted extension commands are in a.specs.
	if len(a.bind.userBindingsRaw) > 0 {
		a.bind.userBindings = filterValidUserBindings(a.bind.userBindingsRaw, a.bind.specs)
	}
	a.bind.bindings = config.MergeBindings(a.baseBindings(), a.bind.userBindings)
	a.bind.bindEd = nil
	if a.Engine != nil {
		if err := a.rebuildKeymap(); err != nil {
			a.warn("ext bindings (%s): %v", m.Name, err)
		}
	}
	a.viewMemo.gen++
	return nil
}

// isExternalKind reports whether kind is served by a mounted extension catalog.
func (a *App) isExternalKind(kind string) bool {
	return a.ext.extCatalogs[kind] != ""
}

// externalRecords returns the cached records of a mounted external noun with
// Matomeru's local overrides overlaid, kicking off a background refresh when
// the cache has no snapshot or the snapshot is stale.
func (a *App) externalRecords(kind string) ([]data.Record, bool) {
	binary := a.ext.extCatalogs[kind]
	if binary == "" {
		return nil, false
	}
	if a.ext.readCache == nil {
		a.ext.readCache = data.NewReadCache()
	}
	snap, ok := a.ext.readCache.Get(kind)
	if !ok || snap.Stale {
		a.fetchExternal(kind, binary)
	}
	items := make([]data.Record, 0, len(snap.Items))
	for _, raw := range snap.Items {
		rec := ext.Overlay(a.Store, raw)
		if !a.nav.showArchived && rec.IsArchived() {
			continue
		}
		items = append(items, rec)
	}
	return items, true
}

func (a *App) fetchExternal(kind, binary string) {
	if a.ext.extFetching == nil {
		a.ext.extFetching = map[string]bool{}
	}
	if a.ext.extFetching[kind] {
		return
	}
	a.ext.extFetching[kind] = true
	store, cache := a.Store, a.ext.readCache
	a.background(func(ctx context.Context) func(core.State) core.State {
		res, err := ext.QueryExternal(ctx, binary, kind, store)
		return func(s core.State) core.State {
			delete(a.ext.extFetching, kind)
			if err != nil {
				a.fail("query "+kind, err)
				return s
			}
			cache.Put(kind, res.Items, res.Total)
			a.viewMemo.gen++
			return s
		}
	})
}

// cachedExternal looks up ref in the ReadCache and lays any local shadow over it.
func (a *App) cachedExternal(ref core.Ref) (data.Record, bool) {
	if a.ext.readCache == nil || !a.isExternalKind(ref.Kind) {
		return data.Record{}, false
	}
	snap, ok := a.ext.readCache.Get(ref.Kind)
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

// --- extension.setup binding on 4 Extensions -------------------------------------

func extensionAdvertisesSetup(rec data.Record) bool {
	for _, field := range []string{"commands", "capabilities"} {
		raw, ok := rec.Value(field)
		if !ok || raw == nil {
			continue
		}
		switch list := raw.(type) {
		case []string:
			for _, item := range list {
				s := strings.ToLower(strings.TrimSpace(item))
				if s == "setup" || strings.Contains(s, "setup") {
					return true
				}
			}
		case []any:
			for _, item := range list {
				s := strings.ToLower(strings.TrimSpace(data.Text(item)))
				if s == "setup" || strings.Contains(s, "setup") {
					return true
				}
			}
		}
	}
	return false
}

func (a *App) extSetup(s core.State, c core.Command) core.StepResult {
	ref, ok := a.subject(s, c)
	if !ok || ref.Kind != data.KindExtension {
		a.say("no extension here to set up")
		return core.Next(s)
	}
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("extension setup", err)
		return core.Next(s)
	}
	name := rec.Text("name")
	if !extensionAdvertisesSetup(*rec) {
		msg := fmt.Sprintf("%s needs no setup (no setup command advertised)", name)
		a.say("%s", msg)
		a.pushToast(msg, toast.Info)
		return core.Next(s)
	}
	bin := strings.TrimSpace(rec.Text("binary"))
	if rec.Text("runStatus") == ext.StatusOffline || bin == "" {
		msg := fmt.Sprintf("%s is offline · R looks again", name)
		a.say("%s", msg)
		a.pushToast(msg, toast.Warn)
		return core.Next(s)
	}
	title := name + " setup"
	argv := []string{bin, "setup"}
	cmdLine := shellJoin(argv)
	if a.rt.Runner != nil {
		runRec, err := a.rt.Runner.Start(context.Background(), runs.StartSpec{
			Title:   title,
			Command: cmdLine,
			Args:    argv,
			Actor:   runActor,
		})
		s = a.started(s, "ran "+title, runRec, err)
		if err != nil {
			a.pushToast(title+" failed: "+err.Error(), toast.Err)
		} else {
			a.pushToast("Running "+title, toast.Ok)
		}
		return core.Next(s)
	}
	s = a.startShell(s, title, cmdLine, "")
	a.pushToast("Queued "+title, toast.Ok)
	return core.Next(s)
}
