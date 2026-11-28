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
	data.KindRepo,
	data.KindDocument,
	data.KindNote,
	data.KindCapture,
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

type appPageEntry struct {
	id, title, subtitle, key string
	keywords                 []string
}

var appSpacePickMeta = map[string]appPageEntry{
	SpacePanes:      {title: "Panes", subtitle: "Live terminals, pane sites, and project overviews", keywords: []string{"app", "space", "panes", "terminals"}},
	SpaceWork:       {title: "Work", subtitle: "Tasks tree and global noun views", keywords: []string{"app", "space", "work", "tasks"}},
	SpaceRuns:       {title: "Runs", subtitle: "Command runs and captured output", keywords: []string{"app", "space", "runs"}},
	SpaceExtensions: {title: "Extensions", subtitle: "Discovered o-* tools and setup", keywords: []string{"app", "space", "extensions"}},
	SpaceProjects:   {title: "Projects", subtitle: "All projects directory with live Home overview preview", keywords: []string{"app", "space", "projects", "directory"}},
	SpaceReminders:  {title: "Reminders", subtitle: "Daily check-offs, scheduled notifications, and history log", keywords: []string{"app", "space", "reminders", "daily", "notifications", "alerts", "schedule"}},
	SpaceFeatures:   {title: "Features", subtitle: "Self-hosted Matomeru features, noun verbs, organized groups, and wishlist capture", keywords: []string{"app", "space", "features", "wishlist", "self-hosted"}},
}

var appOverlayPickEntries = []appPageEntry{
	{id: pagePickAppPrefix + "space:" + SpaceReminders, title: "Reminders", subtitle: "Daily check-offs, scheduled notifications, and history log", key: "g M", keywords: []string{"app", "space", "reminders", "daily", "notifications", "alerts", "schedule"}},
	{id: pagePickAppPrefix + "space:" + SpaceCaptures, title: "Captures", subtitle: "Persistent dictation & composed prompt capture log", key: "g L", keywords: []string{"app", "space", "captures", "dictation", "compose", "log"}},
	{id: pagePickAppPrefix + "about", title: "About", subtitle: "Build versions, daemon PIDs, storage paths, and harnesses", key: "g A", keywords: []string{"app", "about", "diagnostics", "version"}},
	{id: pagePickAppPrefix + "settings", title: "Settings", subtitle: "Theme, layout, confirmations, and preferences", key: "S", keywords: []string{"app", "settings", "config"}},
	{id: pagePickAppPrefix + "bindings", title: "Bindings", subtitle: "Browse, filter, rebind, and reset key bindings", key: "B", keywords: []string{"app", "bindings", "keys", "keymap"}},
	{id: pagePickAppPrefix + "outline", title: "Outline", subtitle: "Full task hierarchy outline overlay", key: "T", keywords: []string{"app", "outline", "tree"}},
	{id: pagePickAppPrefix + "inspector", title: "Inspector", subtitle: "Inspect registered actions, parameters, and modes", key: ": inspect", keywords: []string{"app", "inspector", "actions", "modes"}},
}

func (a *App) pagesPickAppItems() []picker.Item {
	tabs := a.topTabs()
	items := make([]picker.Item, 0, len(tabs)+len(appOverlayPickEntries)+len(spaces))
	listed := make(map[string]bool, len(tabs))
	appendEntry := func(id, title, subtitle, badge, badgeTone string, keywords []string) {
		badges := []ui.Badge{{Text: "[app]", Tone: "info"}}
		if badge != "" {
			badges = append(badges, ui.Badge{Text: badge, Tone: badgeTone})
		}
		items = append(items, picker.Item{ID: id, Title: title, Subtitle: subtitle, Badges: badges, Keywords: keywords})
	}
	for i, tb := range tabs {
		listed[tb.id] = true
		if tb.dynamic {
			continue
		}
		meta := appSpacePickMeta[tb.id]
		appendEntry(pagePickAppPrefix+"space:"+tb.id, firstNonEmpty(meta.title, tb.title), meta.subtitle, fmt.Sprint(i+1), "muted", meta.keywords)
	}
	for _, ap := range appOverlayPickEntries {
		if listed[strings.TrimPrefix(ap.id, pagePickAppPrefix+"space:")] {
			continue
		}
		appendEntry(ap.id, ap.title, ap.subtitle, ap.key, "muted", ap.keywords)
	}
	for _, sp := range spaces {
		if listed[sp.id] {
			continue
		}
		meta := appSpacePickMeta[sp.id]
		appendEntry(pagePickAppPrefix+"space:"+sp.id, firstNonEmpty(meta.title, sp.title), meta.subtitle, "legacy", "muted", meta.keywords)
	}
	for i, tb := range tabs {
		if !tb.dynamic {
			continue
		}
		appendEntry(pagePickAppPrefix+"space:"+tb.id, tb.title, "Open Project Tab", fmt.Sprint(i+1), "accent", []string{"app", "project", "tab", tb.title, tb.projectID})
	}
	return items
}

// pagesPick opens the unified `'` Pages picker containing three clearly badged
// groups: [app] App pages, [pane] Pane site & MCP pages, and [noun] Global noun
// pages (including all mounted extension nouns). Never empty.
func (a *App) pagesPick(s core.State, _ core.Command) core.StepResult {
	items := a.pagesPickAppItems()

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
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:            "pages",
		StartInFilter: a.pickerStartInFilter(true),
		WidthPct:      76,
		HeightPct:     80,
		PageSize:      10,
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
		return a.jumpToPane(s, paneID, false)
	case strings.HasPrefix(pickedID, pagePickPagePrefix):
		return a.jumpToPage(s, strings.TrimPrefix(pickedID, pagePickPagePrefix))
	default:
		return a.jumpToPage(s, pickedID)
	}
	return s
}
