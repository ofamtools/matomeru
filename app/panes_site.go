package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

const childPaneBackHint = "enter live · ctrl+a d detach · esc/h back to project"

// activeChildPaneID returns the embedded child pane ID currently open inside
// an active Project Tab or 1 Panes Project Page workspace, or "" when none is open.
func (a *App) activeChildPaneID(s core.State) string {
	if a == nil || a.proj == nil || a.proj.childPaneByProject == nil {
		return ""
	}
	if isProjectTab(s.ActivePage) {
		projectID := projectIDFromTab(s.ActivePage)
		if projectID == "" {
			return ""
		}
		return strings.TrimSpace(a.proj.childPaneByProject[projectID])
	}
	if s.ActivePage == SpacePanes {
		if groupName, projRef, onProj := a.selectedRailProject(); onProj {
			return strings.TrimSpace(a.proj.childPaneByProject[projectKeyFor(groupName, projRef)])
		}
	}
	return ""
}

// selectedRailPaneID returns the focused rail pane ID without querying SQLite.
func (a *App) selectedRailPaneID() string {
	if a == nil || a.nav == nil {
		return ""
	}
	if tbl := a.nav.tables[railTable]; tbl != nil {
		if row, ok := tbl.FocusedRow(); ok && row.Ref != nil {
			return row.Ref.ID
		}
	}
	return ""
}

// activePaneID resolves the active pane ID without hitting SQLite.
func (a *App) activePaneID(s core.State) string {
	if paneID := a.activeChildPaneID(s); paneID != "" {
		return paneID
	}
	return a.selectedRailPaneID()
}

// activeSideSplitID returns the side-split pane ID for the active pane without
// hitting SQLite.
func (a *App) activeSideSplitID(s core.State) string {
	if a == nil || a.panes == nil || len(a.panes.sideSplit) == 0 {
		return ""
	}
	paneID := a.activePaneID(s)
	if paneID == "" {
		return ""
	}
	return a.panes.sideSplit[paneID]
}

// activePane resolves the pane currently targeted by pane site operations:
// when an open Project Tab or 1 Panes Project Page is showing an embedded child
// pane, it loads that pane from the store; otherwise it delegates to SelectedPane.
func (a *App) activePane(s core.State) (*data.Record, bool) {
	if paneID := a.activeChildPaneID(s); paneID != "" {
		if a.Store == nil {
			return nil, false
		}
		rec, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: paneID})
		return rec, err == nil && rec != nil
	}
	return a.SelectedPane()
}

// displayedSitePageIDFor resolves which page of paneID should be displayed in
// the pane site (supporting live TOC preview in both 1 Panes and Project Tabs).
func (a *App) displayedSitePageIDFor(s core.State, paneID string, pages []data.Record) (string, bool) {
	active := a.panes.activePage[paneID]
	if active == "" {
		active = views.ScreenPageID
	}
	if a.cfg != nil && a.cfg.Settings != nil && !a.cfg.Settings.Bool(SetTOCLivePreview, true) {
		return active, false
	}
	if s.ActivePage != SpacePanes && !isProjectTab(s.ActivePage) {
		return active, false
	}
	focus := s.Pages[s.ActivePage].FocusID
	pageID, ok := strings.CutPrefix(focus, tocPrefix)
	if !ok || pageID == "" || strings.HasPrefix(focus, views.TOCGroupPrefix) || strings.HasPrefix(focus, views.LaneTOCPrefix) {
		return active, false
	}
	if pageID == views.ScreenPageID {
		return views.ScreenPageID, active != views.ScreenPageID
	}
	for _, p := range pages {
		if p.ID == pageID {
			return pageID, pageID != active
		}
	}
	return active, false
}

// buildPaneSiteNode builds the full interactive pane site (live Screen + TOC +
// ui.page canvases + questions + approval/header strips + side split) for both
// 1 Panes (embedded=false) and an open Project Tab's embedded child pane view
// (embedded=true).
func (a *App) buildPaneSiteNode(s core.State, pane data.Record, embedded bool) ui.ViewNode {
	frame := s.Pages[SpacePanes]
	if isProjectTab(s.ActivePage) {
		frame = s.Pages[s.ActivePage]
	}
	ref := pane.Ref()
	res, err := a.Store.Query(data.Query{Kind: data.KindPage, Parent: &ref, ParentField: "pane", IncludeArchived: a.nav.showArchived})
	if err != nil {
		a.fail("read pages", err)
	}
	shownPageID, previewing := a.displayedSitePageIDFor(s, pane.ID, res.Items)
	compactTop := a.panesCompactTopAreaEnabled()
	verboseHook := a.cfg != nil && a.cfg.Settings != nil && a.cfg.Settings.Bool(SetVerboseHookBar, false)
	showLaneBar := a.cfg.Settings.Bool(SetLaneBar, false)
	hasPendingApproval := len(a.pendingApprovalsFor(pane.ID)) > 0
	laneSuffix, laneSuffixTone := "", ""
	foldHookIntoLane := false
	if compactTop && !verboseHook && showLaneBar && !hasPendingApproval {
		if badge, tone, ok := a.compactHookBadge(pane); ok {
			laneSuffix, laneSuffixTone = badge, tone
			foldHookIntoLane = true
		}
	}
	laneInTOC := a.cfg.Settings.Bool(SetLaneInTOC, true)
	var laneBars []data.LaneBarEntry
	if showLaneBar || laneInTOC {
		// Finished lane trees stay stacked above the active bar until dismissed.
		// A failed read shows no lane, as a pane with none does.
		laneBars, _ = a.Store.PaneLaneBars(pane, showLaneBar && a.keepCompletedLaneBarsEnabled())
	}
	opts := views.SiteOptions{
		LaneBars:          laneBars,
		ShowLaneBar:       showLaneBar,
		DismissedLaneBars: a.laneBarCtrl().dismissedFor(pane.ID),
		CompactTopArea:    compactTop,
		LaneSuffix:        laneSuffix,
		LaneSuffixTone:    laneSuffixTone,
		LaneInTOC:         laneInTOC,
		CompactTOC:        a.cfg.Settings.Bool(SetCompactTOC, true),
		TOCStyle:          a.tocStyleSetting(),
		TOCGrouped:        a.cfg.Settings.Bool(SetTOCGrouped, true),
		TOCCollapsed:      a.panes.tocCollapsed,
		MatchMode:         a.filterMode("toc"),
		PageScrollOffset:  a.pageScrollOffset(shownPageID),
		PageScrollLocked:  frame.Mode == ModePage,
		WrapPageScroll:    true,
		HideTOC:           !a.cfg.Settings.Bool(SetShowTOC, true),
		LaneHint:          laneBarHint,
	}
	if a.Shell != nil {
		opts.Width = a.Shell.State.Layout.Rects["site"].W // 0 until the site was drawn once
	}
	site := views.PaneSiteViewWith(pane, res.Items, shownPageID, a.filterQuery("toc"), opts)
	a.hydratePageRefCards(&site)
	a.enhanceQuestionsSite(&site, pane, res.Items)
	a.enhanceCanvasSite(&site, shownPageID)
	if previewing && shownPageID != views.ScreenPageID {
		if content := site.Find("site.content"); content != nil {
			content.Badges = append(slices.Clone(content.Badges), ui.Badge{Text: "preview · enter opens", Tone: "muted"})
		}
	}
	var extraTop []ui.ViewNode
	hookFolded := foldHookIntoLane && site.Find(views.LaneBarID) != nil
	if !hookFolded {
		if strip, ok := a.paneHeaderStrip(pane); ok {
			extraTop = append(extraTop, strip)
		}
	}
	if banner, ok := a.paneApprovalBanner(pane); ok {
		extraTop = append(extraTop, banner)
		if site.Facts == nil {
			site.Facts = map[string]any{}
		}
		site.Facts["hasPendingApproval"] = true
	}
	if len(extraTop) > 0 {
		site.Children = append(extraTop, site.Children...)
	}
	a.rt.live.shown = ""
	a.rt.live.shownSide = ""
	if screen := site.Find(siteScreen); screen != nil {
		var shownLines []string
		if lines, cursor, badges, ok := a.liveScreen(pane, siteScreen); ok {
			shownLines = lines
			screen.Props["lines"], screen.Props["cursor"] = lines, cursor
			content := site.Find("site.content")
			content.Badges = badges
			if f := a.rt.live.frames[pane.ID]; f.PID > 0 && !f.Exited {
				content.Title = fmt.Sprintf("Screen · pid %d", f.PID)
			}
		} else {
			typing := frame.Mode == ModeTerminal && frame.FocusID != sideScreen
			shownLines = a.termLines(pane, typing)
			screen.Props["lines"] = shownLines
			site.Find("site.content").Badges = []ui.Badge{{Text: "not attached", Tone: "muted"}}
		}
		if a.panes.hints != nil && a.panes.hints.session != nil && a.panes.hints.session.Active() && a.panes.hints.paneID == pane.ID {
			w, h := a.screenSize()
			screen.Props["lines"] = a.hintsRenderLines(shownLines, w, h)
			delete(screen.Props, "cursor")
			if content := site.Find("site.content"); content != nil {
				content.Badges = append(slices.Clone(content.Badges),
					ui.Badge{Text: fmt.Sprintf("hints %d", len(a.panes.hints.session.Remaining())), Tone: "accent"})
			}
			site.Children = append(site.Children, a.panes.hints.session.View("site.hints.bar"))
		}
		a.applySideSplit(&site, pane, s)
	}
	if !embedded {
		return site
	}
	if site.Facts == nil {
		site.Facts = map[string]any{}
	}
	site.Facts["inChildPaneView"] = true
	site.Box.Flex = 1
	paneTitle := views.Title(a.schema(data.KindPane), pane)
	banner := views.ChildPaneBannerViewWith(paneTitle, childPaneBackHint, compactTop)
	return ui.ViewNode{
		ID:       "project.child_pane.view",
		Kind:     ui.KindVStack,
		Box:      layout.Constraints{Flex: 1},
		Facts:    map[string]any{"inChildPaneView": true, "paneId": pane.ID},
		Children: []ui.ViewNode{banner, site},
	}
}

// openProjectChildPane opens paneID as an embedded child pane site inside the
// Project Tab for projectID, optionally attaching straight into live terminal mode.
func (a *App) openProjectChildPane(s core.State, projectID, paneID string, enterLive bool) core.State {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return s
	}
	s.ActivePage = ProjectTabPrefix + projectID
	return a.openProjectChildPaneInPlace(s, projectID, paneID, "", enterLive)
}

// openProjectChildPaneInPlace opens paneID as an embedded child pane site
// inside the active space (1 Panes Project Page or 6+ Project Tab) and records
// returnFocus to restore on close.
func (a *App) openProjectChildPaneInPlace(s core.State, projKey, paneID, returnFocus string, enterLive bool) core.State {
	projKey = strings.TrimSpace(projKey)
	paneID = strings.TrimSpace(paneID)
	if projKey == "" || paneID == "" || a.Store == nil {
		return s
	}
	pane, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: paneID})
	if err != nil || pane == nil {
		if err != nil {
			a.fail("open pane", err)
		}
		return s
	}
	pc := a.projectCtrl()
	if pc.childPaneByProject == nil {
		pc.childPaneByProject = map[string]string{}
	}
	pc.childPaneByProject[projKey] = paneID
	if pc.childPaneReturnFocus == nil {
		pc.childPaneReturnFocus = map[string]string{}
	}
	if returnFocus != "" {
		pc.childPaneReturnFocus[projKey] = returnFocus
	} else {
		delete(pc.childPaneReturnFocus, projKey)
	}
	pc.setActiveNavItem(projKey, nil)
	a.clearFilter("toc")
	if a.panes != nil {
		if a.panes.activePage == nil {
			a.panes.activePage = map[string]string{}
		}
		a.panes.activePage[paneID] = views.ScreenPageID
	}
	a.viewMemo.gen++
	s = a.dropModals(s)
	s = withPage(s, s.ActivePage, siteScreen, ModeNormal)
	if enterLive {
		return a.enterScreen(s, *pane)
	}
	if a.panesCompactTopAreaEnabled() {
		a.say("%s · esc back", views.Title(a.schema(data.KindPane), *pane))
	} else {
		a.say("%s · %s", views.Title(a.schema(data.KindPane), *pane), childPaneBackHint)
	}
	return s
}

// closeProjectChildPane exits the embedded child pane view inside the active
// Project Tab or 1 Panes Project Page and restores focus to the originating nav
// node or project.panes.tree in ModeNormal.
func (a *App) closeProjectChildPane(s core.State, _ core.Command) core.StepResult {
	projKey := a.activeProjectWorkspaceKey(s)
	if projKey == "" {
		return core.Next(s)
	}
	ret := projectPanesTree
	if a.proj != nil {
		if a.proj.childPaneByProject != nil {
			delete(a.proj.childPaneByProject, projKey)
		}
		if a.proj.childPaneReturnFocus != nil {
			if saved := strings.TrimSpace(a.proj.childPaneReturnFocus[projKey]); saved != "" {
				ret = saved
			}
			delete(a.proj.childPaneReturnFocus, projKey)
		}
		a.proj.setActiveNavItem(projKey, nil)
	}
	if a.panes != nil && a.panes.zoom {
		a.panes.zoom = false
	}
	a.viewMemo.gen++
	a.say("returned to project panes")
	return core.Next(withPage(s, s.ActivePage, ret, ModeNormal))
}
