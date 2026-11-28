package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"ofam.dev/matomeru/contextitems"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// --- Interactive ui.page & Right-Pane Preview / Active Body Builders ------------

func (a *App) projectOpenInteractivePage(s core.State, projKey string, page data.Record, navTarget *projectNavTarget, returnFocus string) core.StepResult {
	pc := a.projectCtrl()
	if page.IsUnread() && a.Router != nil {
		_, _ = a.Router.MarkViewed(page.ID, viewer)
	}
	pc.setOpenPageID(projKey, page.ID)
	pc.setOpenPageReturnFocus(projKey, returnFocus)
	if navTarget != nil {
		pc.setActiveNavItem(projKey, navTarget)
	} else {
		pc.setActiveNavItem(projKey, nil)
	}
	a.setPageScrollOffset(page.ID, 0)
	a.viewMemo.gen++

	if a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetPageLockScroll, true) {
		s = withMode(withFocus(s, views.PageScrollID), ModePage)
		a.viewMemo.gen++
		focus := views.PageScrollID
		if page.Text("pageKind") == "questions" {
			for _, n := range a.focusables(s) {
				if n.region == "site.content" && n.id != views.PageScrollID {
					focus = n.id
					break
				}
			}
		} else if first := a.firstCanvasWidgetID(s); first != "" {
			focus = first
		}
		if focus != views.PageScrollID && page.Text("pageKind") != "questions" {
			a.say("%s · j/k move · h/l links · space/x check · enter/o open · tab blocks · esc leaves", page.Text("title"))
		} else {
			a.say("%s · j/k/ctrl+d/ctrl+u scroll · tab/n/p controls · esc leaves", page.Text("title"))
		}
		return core.Next(withMode(withFocus(s, focus), ModePage))
	}

	s = withFocus(s, views.PageScrollID)
	a.viewMemo.gen++
	for _, n := range a.focusables(s) {
		if n.region == "site.content" && n.id != views.PageScrollID {
			a.say("%s · esc / h back", page.Text("title"))
			return core.Next(withFocus(s, n.id))
		}
	}
	a.say("%s · esc / h back", page.Text("title"))
	return core.Next(withFocus(s, views.PageScrollID))
}

func (a *App) buildProjectInteractivePageNode(s core.State, page data.Record, preview bool) ui.ViewNode {
	pageRef := page.Ref()
	title := views.Title(a.schema(data.KindPage), page)
	badges := make([]ui.Badge, 0, 3)
	if pk := strings.TrimSpace(page.Text("pageKind")); pk != "" {
		badges = append(badges, ui.Badge{Text: pk, Tone: "info"})
	}
	switch {
	case preview:
		badges = append(badges, ui.Badge{Text: "preview · enter opens", Tone: "muted"})
	case s.Pages[s.ActivePage].Mode == ModePage:
		badges = append(badges, ui.Badge{Text: "reading · esc leaves", Tone: "accent"})
	default:
		badges = append(badges, ui.Badge{Text: "esc / h back", Tone: "muted"})
	}

	content := ui.ViewNode{
		ID:     "site.content",
		Kind:   ui.KindPanel,
		Scope:  data.KindPage,
		Ref:    &pageRef,
		Title:  title,
		Border: true,
		Badges: badges,
		Box:    layout.Constraints{Flex: 1},
		Facts: map[string]any{
			"section":           "pages",
			"projectOpenPage":   true,
			"pageId":            page.ID,
			"pageKind":          page.Text("pageKind"),
			"screen":            false,
			"projectNavPreview": preview,
			"projectNavLocked":  !preview,
		},
	}

	root, err := mcpui.PageRoot(page)
	if err != nil {
		content.Children = []ui.ViewNode{text("project.page.err", "This page cannot be rendered: "+err.Error(), "error")}
	} else {
		content.Children = []ui.ViewNode{views.PageViewport(root, a.pageScrollOffset(page.ID))}
	}

	wrapper := ui.ViewNode{
		ID:       "project.page.wrapper",
		Kind:     ui.KindVStack,
		Children: []ui.ViewNode{content},
	}
	a.enhanceQuestionsSite(&wrapper, data.Record{}, nil)
	a.enhanceCanvasSite(&wrapper, page.ID)
	return wrapper.Children[0]
}

func findWorkspaceRecord(recs []data.Record, id string) (*data.Record, bool) {
	for i := range recs {
		if recs[i].ID == id {
			return &recs[i], true
		}
	}
	return nil, false
}

func (a *App) workspaceOrStoredRecord(recs []data.Record, ref core.Ref) (*data.Record, bool) {
	if rec, ok := findWorkspaceRecord(recs, ref.ID); ok {
		return rec, true
	}
	if a.Store == nil {
		return nil, false
	}
	rec, err := a.Store.Get(ref)
	return rec, err == nil && rec != nil
}

func (a *App) cachedShowSnapshot(ref core.Ref, rev int64) (data.ShowSnapshot, error) {
	pc := a.projectCtrl()
	if pc.showSnapCache != nil {
		if cached, ok := pc.showSnapCache[ref]; ok && cached.rev == rev {
			return cached.snap, nil
		}
	}
	snap, err := a.Store.LoadShowSnapshot(ref)
	if err != nil {
		return data.ShowSnapshot{}, err
	}
	if pc.showSnapCache == nil {
		pc.showSnapCache = map[core.Ref]cachedShowSnap{}
	}
	pc.showSnapCache[ref] = cachedShowSnap{rev: rev, snap: snap}
	return snap, nil
}

func (a *App) buildProjectNavItemBody(s core.State, ws projectWorkspaceData, target projectNavTarget, preview bool) ui.ViewNode {
	// 1. ui.page
	if target.Section == "pages" && target.Ref != nil && target.Ref.Kind == data.KindPage {
		if page, ok := a.workspaceOrStoredRecord(ws.pages, *target.Ref); ok {
			return a.buildProjectInteractivePageNode(s, *page, preview)
		}
	}

	// 2. Pane preview
	if target.Section == "panes" && target.Ref != nil && target.Ref.Kind == data.KindPane {
		if pane, ok := a.workspaceOrStoredRecord(ws.panes, *target.Ref); ok {
			site := a.buildPaneSiteNode(s, *pane, true)
			if preview {
				if content := site.Find("site.content"); content != nil {
					content.Badges = append(slices.Clone(content.Badges), ui.Badge{Text: "preview · enter opens", Tone: "muted"})
				}
				if site.Facts == nil {
					site.Facts = map[string]any{}
				}
				site.Facts["projectNavPreview"] = true
			}
			return site
		}
	}

	// 3. Document / Note with Markdown body or readable .md file
	if target.Ref != nil && (target.Ref.Kind == data.KindDocument || target.Ref.Kind == data.KindNote) {
		pool := ws.documents
		if target.Section == "notes" {
			pool = ws.notes
		}
		if rec, ok := a.workspaceOrStoredRecord(pool, *target.Ref); ok {
			if md, hasMD := a.recordMarkdownBody(*rec); hasMD && md != "" {
				return a.buildProjectMarkdownPreviewNode(s, *rec, target.Section, md, preview)
			}
		}
	}

	// 4. Persisted record (tasks, lanes, questions, commands, persisted context, fallback doc/note)
	if target.Ref != nil && target.Ref.ID != "" && target.Ref.Kind != "" && target.Ref.Kind != "directory" {
		opts := a.showOptionsFor(*target.Ref)
		if preview {
			opts.ExtraBadges = append(opts.ExtraBadges, ui.Badge{Text: "preview · enter opens", Tone: "muted"})
		} else {
			opts.ExtraBadges = append(opts.ExtraBadges, ui.Badge{Text: "esc back to nav", Tone: "muted"})
		}
		if snap, err := a.cachedShowSnapshot(*target.Ref, ws.cacheRev); err == nil {
			node := views.ShowViewWith(snap, s.ActiveFrame().FocusID, opts)
			if node.Facts == nil {
				node.Facts = map[string]any{}
			}
			node.Facts["section"] = target.Section
			node.Facts["projectNavPreview"] = preview
			node.Facts["projectNavLocked"] = !preview
			return node
		}
	}

	// 5. Unpersisted Context / Pinned / PR detail card
	if target.ContextItem != nil {
		return a.buildProjectContextDetailCard(*target.ContextItem, target.Section, preview)
	}

	// 6. Worktree detail card
	if target.Worktree != nil {
		return a.buildProjectWorktreeDetailCard(*target.Worktree, preview)
	}

	return a.buildProjectSectionBody(s, ws, target.Section)
}

func (a *App) recordMarkdownBody(rec data.Record) (string, bool) {
	if body := strings.TrimSpace(rec.Text("body")); body != "" {
		return body, true
	}
	for _, field := range []string{"filePath", "path"} {
		fp := strings.TrimSpace(rec.Text(field))
		if fp == "" {
			continue
		}
		low := strings.ToLower(fp)
		if !strings.HasSuffix(low, ".md") && !strings.HasSuffix(low, ".markdown") {
			continue
		}
		if text, err := a.readFileCached(expandHome(fp)); err == nil && strings.TrimSpace(text) != "" {
			return text, true
		}
	}
	return "", false
}

func (a *App) buildProjectMarkdownPreviewNode(s core.State, rec data.Record, section, md string, preview bool) ui.ViewNode {
	ref := rec.Ref()
	schema := a.schema(rec.Kind)
	title := views.Title(schema, rec)
	badges := views.StatusBadges(schema, rec)
	switch {
	case preview:
		badges = append(badges, ui.Badge{Text: "preview · enter reads", Tone: "muted"})
	case s.Pages[s.ActivePage].Mode == ModePage:
		badges = append(badges, ui.Badge{Text: "reading · esc leaves", Tone: "accent"})
	default:
		badges = append(badges, ui.Badge{Text: "esc back to nav", Tone: "muted"})
	}
	mdNode := ui.ViewNode{
		ID:    "project.nav.md." + rec.ID,
		Kind:  ui.KindMarkdown,
		Props: map[string]any{"markdown": md},
	}
	content := ui.ViewNode{
		ID:     "site.content",
		Kind:   ui.KindPanel,
		Scope:  rec.Kind,
		Ref:    &ref,
		Title:  title,
		Border: true,
		Badges: badges,
		Box:    layout.Constraints{Flex: 1},
		Facts: map[string]any{
			"section":           section,
			"projectOpenPage":   true,
			"projectNavPreview": preview,
			"projectNavLocked":  !preview,
			"pageId":            rec.ID,
		},
		Children: []ui.ViewNode{{
			ID:        views.PageScrollID,
			Kind:      ui.KindScroll,
			Scope:     rec.Kind,
			Focusable: true,
			Props:     map[string]any{"offset": a.pageScrollOffset(rec.ID)},
			Children:  []ui.ViewNode{mdNode},
		}},
	}
	wrapper := ui.ViewNode{
		ID:       "project.md.wrapper",
		Kind:     ui.KindVStack,
		Children: []ui.ViewNode{content},
	}
	a.enhanceCanvasSite(&wrapper, rec.ID)
	return wrapper.Children[0]
}

func (a *App) buildProjectContextDetailCard(it CollectedContextItem, section string, preview bool) ui.ViewNode {
	title := singleLineContextText(it.Label)
	if title == "" {
		if it.Kind == contextitems.KindCode {
			title = data.CodeContextTitle(it.Value, it.Language)
		} else {
			title = singleLineContextText(it.Value)
		}
	}
	kindMeta := it.Kind
	if it.Language != "" {
		kindMeta += " (" + it.Language + ")"
	}
	var rows []ui.ViewNode
	rows = append(rows, line("project.ctx.detail.kind", fmt.Sprintf(" Kind: %s · Source: %s", kindMeta, it.Source), "muted"))
	if it.Kind == contextitems.KindCode {
		rows = append(rows, ui.ViewNode{
			ID:    "project.ctx.detail.code",
			Kind:  ui.KindMarkdown,
			Props: map[string]any{"markdown": data.FormatCodeMarkdownBlock(it.Value, it.Language), "wrap": true},
		})
	} else {
		rows = append(rows, ui.ViewNode{ID: "project.ctx.detail.val", Kind: ui.KindText, Props: map[string]any{"text": " " + it.Value, "wrap": true}})
	}
	if it.PaneTitle != "" {
		rows = append(rows, line("project.ctx.detail.pane", " Pane: "+it.PaneTitle, "muted"))
	}
	if strings.TrimSpace(it.Notes) != "" {
		rows = append(rows, ui.ViewNode{ID: "project.ctx.detail.notes", Kind: ui.KindText, Props: map[string]any{"text": " Notes: " + it.Notes, "wrap": true}})
	}
	badges := []ui.Badge{{Text: it.Kind, Tone: "info"}}
	if it.Official {
		badges = append(badges, ui.Badge{Text: "★ official · auto", Tone: "accent"})
	}
	if preview {
		badges = append(badges, ui.Badge{Text: "preview · enter focuses", Tone: "muted"})
	} else {
		badges = append(badges, ui.Badge{Text: "esc back to nav", Tone: "muted"})
	}
	facts := map[string]any{
		"section":           section,
		"contextItem":       true,
		"contextKey":        it.Key,
		"official":          it.Official,
		"projectNavPreview": preview,
		"projectNavLocked":  !preview,
	}
	return ui.ViewNode{
		ID: "project.ctx.detail", Kind: ui.KindPanel, Scope: ScopeProjectContext,
		Title: title, Border: true, Badges: badges, Facts: facts,
		Box: layout.Constraints{Flex: 1},
		Children: []ui.ViewNode{{
			ID: "project.ctx.detail.body", Kind: ui.KindVStack, Scope: ScopeProjectContext,
			Focusable: true, Facts: facts, Children: rows,
		}},
	}
}

func (a *App) buildProjectWorktreeDetailCard(wt data.ProjectWorktree, preview bool) ui.ViewNode {
	title := strings.TrimSpace(wt.Branch)
	if title == "" {
		title = filepath.Base(wt.Path)
	}
	rows := []ui.ViewNode{
		line("project.wt.detail.path", " Path:   "+wt.Path, ""),
		line("project.wt.detail.branch", " Branch: "+wt.Branch, ""),
		line("project.wt.detail.repo", " Repo:   "+wt.Repo, "muted"),
		line("project.wt.detail.source", " Source: "+wt.Source, "muted"),
	}
	badges := []ui.Badge{{Text: wt.Source, Tone: "info"}}
	if preview {
		badges = append(badges, ui.Badge{Text: "preview · enter focuses", Tone: "muted"})
	} else {
		badges = append(badges, ui.Badge{Text: "esc back to nav", Tone: "muted"})
	}
	facts := map[string]any{
		"section":           "worktrees",
		"path":              wt.Path,
		"projectNavPreview": preview,
		"projectNavLocked":  !preview,
	}
	return ui.ViewNode{
		ID: "project.wt.detail", Kind: ui.KindPanel, Scope: scopeProjectWorktrees,
		Title: title, Border: true, Badges: badges, Facts: facts,
		Box: layout.Constraints{Flex: 1},
		Children: []ui.ViewNode{{
			ID: "project.wt.detail.body", Kind: ui.KindVStack, Scope: scopeProjectWorktrees,
			Focusable: true, Facts: facts, Children: rows,
		}},
	}
}

// --- ModePage integration -------------------------------------------------------

func (a *App) activeProjectPageID(s core.State) string {
	projKey := a.activeProjectWorkspaceKey(s)
	if projKey == "" || a.activeChildPaneID(s) != "" {
		return ""
	}
	pc := a.projectCtrl()
	if openID := pc.openPageID(projKey); openID != "" {
		return openID
	}
	if locked := pc.activeNavItem(projKey); locked != nil && locked.Ref != nil {
		switch locked.Ref.Kind {
		case data.KindPage, data.KindDocument, data.KindNote:
			return locked.Ref.ID
		}
	}
	focus := s.Pages[s.ActivePage].FocusID
	if strings.HasPrefix(focus, projectNavNodePrefix) {
		if target, ok := pc.navTarget(projKey, focus); ok && target.Ref != nil {
			switch target.Ref.Kind {
			case data.KindPage, data.KindDocument, data.KindNote:
				return target.Ref.ID
			}
		}
	}
	return ""
}

func (a *App) leaveProjectPage(s core.State) (core.State, bool) {
	projKey := a.activeProjectWorkspaceKey(s)
	if projKey == "" || a.activeChildPaneID(s) != "" {
		return s, false
	}
	pc := a.projectCtrl()
	if pc.openPageID(projKey) != "" {
		pc.setOpenPageID(projKey, "")
		pc.setActiveNavItem(projKey, nil)
		ret := pc.takeOpenPageReturnFocus(projKey)
		if ret == "" {
			ret = projectPagesTable
		}
		a.viewMemo.gen++
		ret = a.resolveProjectNavReturnFocus(s, ret)
		a.say("left page")
		return withMode(withFocus(s, ret), ModeNormal), true
	}
	if locked := pc.activeNavItem(projKey); locked != nil {
		ret := locked.FocusID
		pc.setActiveNavItem(projKey, nil)
		a.viewMemo.gen++
		ret = a.resolveProjectNavReturnFocus(s, ret)
		a.say("left preview")
		return withMode(withFocus(s, ret), ModeNormal), true
	}
	return s, false
}
