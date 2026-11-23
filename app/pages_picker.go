package app

import (
	"fmt"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
)

// --- Unified `'` Pages Picker ([app], [pane], [noun]) ----------------------------

const (
	pagePickAppPrefix    = "app:"
	pagePickNounPrefix   = "noun:"
	pagePickPagePrefix   = "page:"
	pagePickScreenPrefix = "screen:"
)

// coreNounOrder places the primary daily-driver global nouns first in the
// `[noun]` group of the unified `'` Pages picker.
var coreNounOrder = []string{
	data.KindTask,
	data.KindLane,
	data.KindDocument,
	data.KindNote,
	data.KindQuestion,
	data.KindCommand,
	data.KindContext,
	data.KindSchedule,
	data.KindSignal,
	data.KindPage,
}

func (a *App) schemaCount(kind string) int {
	if a.isExternalKind(kind) && a.ext.readCache != nil {
		if snap, ok := a.ext.readCache.Get(kind); ok {
			return len(snap.Items)
		}
	}
	if res, err := a.Store.Query(data.Query{Kind: kind, IncludeArchived: a.nav.showArchived, Limit: 1}); err == nil {
		return res.Total
	}
	return 0
}

// pagesPick opens the unified `'` Pages picker containing three clearly badged
// groups: [app] App pages, [pane] Pane site & MCP pages, and [noun] Global noun
// pages (including all mounted extension nouns). Never empty.
func (a *App) pagesPick(s core.State, _ core.Command) core.StepResult {
	var items []picker.Item

	// Group 1: [app] App pages (spaces + core overlays).
	appPages := []struct {
		id, title, subtitle, key string
		keywords                 []string
	}{
		{pagePickAppPrefix + "space:" + SpacePanes, "Panes", "Live terminals, pane sites, and project overviews", "1", []string{"app", "space", "panes", "terminals"}},
		{pagePickAppPrefix + "space:" + SpaceWork, "Work", "Tasks tree and global noun views", "2", []string{"app", "space", "work", "tasks"}},
		{pagePickAppPrefix + "space:" + SpaceRuns, "Runs", "Command runs and captured output", "3", []string{"app", "space", "runs"}},
		{pagePickAppPrefix + "space:" + SpaceExtensions, "Extensions", "Discovered o-* tools and setup", "4", []string{"app", "space", "extensions"}},
		{pagePickAppPrefix + "about", "About", "Build versions, daemon PIDs, storage paths, and harnesses", "g A", []string{"app", "about", "diagnostics", "version"}},
		{pagePickAppPrefix + "settings", "Settings", "Theme, layout, confirmations, and preferences", "S", []string{"app", "settings", "config"}},
		{pagePickAppPrefix + "bindings", "Bindings", "Browse, filter, rebind, and reset key bindings", "B", []string{"app", "bindings", "keys", "keymap"}},
		{pagePickAppPrefix + "outline", "Outline", "Full task hierarchy outline overlay", "T", []string{"app", "outline", "tree"}},
		{pagePickAppPrefix + "inspector", "Inspector", "Inspect registered actions, parameters, and modes", ": inspect", []string{"app", "inspector", "actions", "modes"}},
	}
	for _, ap := range appPages {
		badges := []ui.Badge{{Text: "[app]", Tone: "info"}}
		if ap.key != "" {
			badges = append(badges, ui.Badge{Text: ap.key, Tone: "muted"})
		}
		items = append(items, picker.Item{
			ID:       ap.id,
			Title:    ap.title,
			Subtitle: ap.subtitle,
			Badges:   badges,
			Keywords: ap.keywords,
		})
	}

	// Group 2: [pane] Pane site & MCP pages (unread/open questions first, plus each pane's Screen).
	if res, err := a.Router.ListPages(mcpui.ListPagesArgs{}); err == nil {
		for _, p := range res.Pages {
			ref := core.Ref{Kind: data.KindPage, ID: p.ID}
			sub := p.PageKind
			if p.PaneRef != nil {
				paneTitle := a.title(*p.PaneRef)
				if sub != "" {
					sub = paneTitle + " · " + sub
				} else {
					sub = paneTitle
				}
			}
			badges := []ui.Badge{{Text: "[pane]", Tone: "accent"}}
			open := 0
			for _, q := range p.Questions {
				if !q.Answered {
					open++
				}
			}
			if open > 0 {
				badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d open", open), Tone: "warning"})
			}
			items = append(items, picker.Item{
				ID:       pagePickPagePrefix + p.ID,
				Title:    p.Title,
				Subtitle: sub,
				Unread:   p.Unread,
				Ref:      &ref,
				Scope:    data.KindPage,
				Badges:   badges,
				Keywords: []string{"pane", p.Slug, p.PageKind, sub},
			})
		}
	}
	if panesRes, err := a.Store.Query(data.Query{Kind: data.KindPane, IncludeArchived: a.nav.showArchived}); err == nil {
		paneSchema := a.schema(data.KindPane)
		for _, pane := range panesRes.Items {
			ref := pane.Ref()
			paneTitle := views.Title(paneSchema, pane)
			items = append(items, picker.Item{
				ID:       pagePickScreenPrefix + pane.ID,
				Title:    "Screen · " + paneTitle,
				Subtitle: pane.Text("harness"),
				Ref:      &ref,
				Scope:    data.KindPane,
				Badges:   []ui.Badge{{Text: "[pane]", Tone: "accent"}, {Text: "screen", Tone: "muted"}},
				Keywords: []string{"pane", "screen", "terminal", "screen " + paneTitle, paneTitle, pane.Text("harness")},
			})
		}
	}

	// Group 3: [noun] Global noun pages (core daily nouns + every registered & mounted extension noun).
	schemas := a.Store.Registry().List()
	byKind := make(map[string]data.Schema, len(schemas))
	for _, sc := range schemas {
		byKind[sc.Kind] = sc
	}
	addedKind := map[string]bool{}
	appendNoun := func(sc data.Schema) {
		if addedKind[sc.Kind] {
			return
		}
		addedKind[sc.Kind] = true
		title := sc.Plural
		if title == "" {
			title = sc.Title
		}
		if title == "" {
			title = sc.Kind
		}
		n := a.schemaCount(sc.Kind)
		badges := []ui.Badge{{Text: "[noun]", Tone: "success"}, {Text: fmt.Sprint(n), Tone: "muted"}}
		sub := sc.Kind
		if a.isExternalKind(sc.Kind) {
			badges = append(badges, ui.Badge{Text: "ext", Tone: "info"})
			sub = sc.Kind + " · extension"
		}
		items = append(items, picker.Item{
			ID:       pagePickNounPrefix + sc.Kind,
			Title:    title,
			Subtitle: sub,
			Badges:   badges,
			Keywords: []string{"noun", sc.Kind, sc.Title, sc.Plural},
		})
	}
	for _, kind := range coreNounOrder {
		if sc, ok := byKind[kind]; ok {
			appendNoun(sc)
		}
	}
	for _, sc := range schemas {
		appendNoun(sc)
	}

	pm := picker.New("Pages", items)
	pm.Placeholder = "search [app], [pane], and [noun] pages"
	m := modal.NewPickerModal(pm, modal.PickerOptions{
		ID:        "pages",
		WidthPct:  76,
		HeightPct: 80,
		PageSize:  10,
		OnConfirm: func(picked picker.Item) modal.Result {
			id := picked.ID
			a.afterModal(func(st core.State) core.State {
				return a.jumpToUnifiedPage(st, id)
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	return core.Next(a.pushModal(s, m))
}

func (a *App) jumpToUnifiedPage(s core.State, pickedID string) core.State {
	switch {
	case strings.HasPrefix(pickedID, pagePickAppPrefix):
		target := strings.TrimPrefix(pickedID, pagePickAppPrefix)
		switch {
		case strings.HasPrefix(target, "space:"):
			return a.openSpace(s, strings.TrimPrefix(target, "space:"))
		case target == "about":
			return a.aboutOpen(s, core.Command{}).State
		case target == "settings":
			return a.settingsOpen(s, core.Command{}).State
		case target == "bindings":
			return a.bindingsOpen(s, core.Command{}).State
		case target == "outline":
			tm := a.trees.outlineModel()
			a.syncTreeModel(tm, data.KindTask, outlineTree)
			return a.pushModal(a.dropModals(s), a.newOutlineModal())
		case target == "inspector":
			return a.inspectOpen(s, core.Command{Args: map[string]any{"what": InspectActions}}).State
		}
	case strings.HasPrefix(pickedID, pagePickNounPrefix):
		return a.browse(s, strings.TrimPrefix(pickedID, pagePickNounPrefix))
	case strings.HasPrefix(pickedID, pagePickScreenPrefix):
		paneID := strings.TrimPrefix(pickedID, pagePickScreenPrefix)
		paneRef := core.Ref{Kind: data.KindPane, ID: paneID}
		rail := a.rail()
		if !selectRow(rail, data.RefText(paneRef)) {
			rail.Query = ""
			selectRow(rail, data.RefText(paneRef))
		}
		s = a.dropModals(s)
		s.ActivePage = SpacePanes
		a.nav.tocFilter = ""
		a.panes.activePage[paneID] = views.ScreenPageID
		return withPage(s, SpacePanes, tocPrefix+views.ScreenPageID, ModeNormal)
	case strings.HasPrefix(pickedID, pagePickPagePrefix):
		return a.jumpToPage(s, strings.TrimPrefix(pickedID, pagePickPagePrefix))
	default:
		return a.jumpToPage(s, pickedID)
	}
	return s
}
