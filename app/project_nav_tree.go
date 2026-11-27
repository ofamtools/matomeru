package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"ofam.dev/matomeru/contextitems"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
)

type projectNavTarget struct {
	FocusID       string
	Section       string
	NodeID        string
	ParentFocusID string
	Label         string
	Ref           *core.Ref
	ContextKey    string
	ContextItem   *CollectedContextItem
	Worktree      *data.ProjectWorktree
	Unread        bool
	Expandable    bool
	Expanded      bool
}

func (a *App) projectNavTreeEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetProjectsNavTree, true)
}

func projectNavFocusSection(focusID string) (secKey string, isNode bool) {
	if rest, ok := strings.CutPrefix(focusID, projectNavNodePrefix); ok && rest != "" {
		sec, _, _ := strings.Cut(rest, ".")
		return sec, true
	}
	if sec, ok := strings.CutPrefix(focusID, projectNavPrefix); ok && sec != "" {
		if strings.HasPrefix(sec, "group.") || sec == "empty" || sec == "scroll" || sec == "list" {
			return "", false
		}
		return sec, false
	}
	return "", false
}

// activeProjectWorkspaceKey returns the projectKey for the currently active
// Project workspace (either a 6+ Project Tab or a 1 Panes Project Header), or
// "" when not currently viewing a Project workspace.
func (a *App) activeProjectWorkspaceKey(s core.State) string {
	if a == nil {
		return ""
	}
	if isProjectTab(s.ActivePage) {
		return projectIDFromTab(s.ActivePage)
	}
	if s.ActivePage == SpacePanes && a.projectWorkspaceEnabled() {
		if groupName, projRef, onProj := a.selectedRailProject(); onProj {
			return projectKeyFor(groupName, projRef)
		}
	}
	return ""
}

// --- ProjectController nav-tree state helpers -----------------------------------

func (pc *ProjectController) isNavSectionExpanded(projKey, secKey string) bool {
	if pc == nil || pc.expandedSections == nil || projKey == "" || secKey == "" {
		return false
	}
	return pc.expandedSections[projKey][secKey]
}

func (pc *ProjectController) setNavSectionExpanded(projKey, secKey string, expanded bool) {
	if pc == nil || projKey == "" || secKey == "" {
		return
	}
	if pc.expandedSections == nil {
		pc.expandedSections = map[string]map[string]bool{}
	}
	if pc.expandedSections[projKey] == nil {
		pc.expandedSections[projKey] = map[string]bool{}
	}
	if !expanded {
		delete(pc.expandedSections[projKey], secKey)
		return
	}
	pc.expandedSections[projKey][secKey] = true
}

func (pc *ProjectController) isNavNodeExpanded(projKey, nodeKey string) bool {
	if pc == nil || pc.expandedNodes == nil || projKey == "" || nodeKey == "" {
		return false
	}
	return pc.expandedNodes[projKey][nodeKey]
}

func (pc *ProjectController) setNavNodeExpanded(projKey, nodeKey string, expanded bool) {
	if pc == nil || projKey == "" || nodeKey == "" {
		return
	}
	if pc.expandedNodes == nil {
		pc.expandedNodes = map[string]map[string]bool{}
	}
	if pc.expandedNodes[projKey] == nil {
		pc.expandedNodes[projKey] = map[string]bool{}
	}
	if !expanded {
		delete(pc.expandedNodes[projKey], nodeKey)
		return
	}
	pc.expandedNodes[projKey][nodeKey] = true
}

func (pc *ProjectController) activeNavItem(projKey string) *projectNavTarget {
	if pc == nil || pc.activeItemByProject == nil || projKey == "" {
		return nil
	}
	return pc.activeItemByProject[projKey]
}

func (pc *ProjectController) setActiveNavItem(projKey string, target *projectNavTarget) {
	if pc == nil || projKey == "" {
		return
	}
	if pc.activeItemByProject == nil {
		pc.activeItemByProject = map[string]*projectNavTarget{}
	}
	if target == nil {
		delete(pc.activeItemByProject, projKey)
		return
	}
	cp := *target
	pc.activeItemByProject[projKey] = &cp
}

func (pc *ProjectController) resetNavTargets(projKey string) {
	if pc == nil || projKey == "" {
		return
	}
	if pc.navTargets == nil {
		pc.navTargets = map[string]map[string]projectNavTarget{}
	}
	pc.navTargets[projKey] = map[string]projectNavTarget{}
}

func (pc *ProjectController) registerNavTarget(projKey string, t projectNavTarget) {
	if pc == nil || projKey == "" || t.FocusID == "" {
		return
	}
	if pc.navTargets == nil {
		pc.navTargets = map[string]map[string]projectNavTarget{}
	}
	if pc.navTargets[projKey] == nil {
		pc.navTargets[projKey] = map[string]projectNavTarget{}
	}
	pc.navTargets[projKey][t.FocusID] = t
}

func (pc *ProjectController) navTarget(projKey, focusID string) (projectNavTarget, bool) {
	if pc == nil || pc.navTargets == nil || projKey == "" || focusID == "" {
		return projectNavTarget{}, false
	}
	t, ok := pc.navTargets[projKey][focusID]
	return t, ok
}

func (pc *ProjectController) setOpenPageReturnFocus(projKey, focusID string) {
	if pc == nil || projKey == "" {
		return
	}
	if pc.openPageReturnFocus == nil {
		pc.openPageReturnFocus = map[string]string{}
	}
	if focusID == "" {
		delete(pc.openPageReturnFocus, projKey)
		return
	}
	pc.openPageReturnFocus[projKey] = focusID
}

func (pc *ProjectController) takeOpenPageReturnFocus(projKey string) string {
	if pc == nil || pc.openPageReturnFocus == nil || projKey == "" {
		return ""
	}
	ret := strings.TrimSpace(pc.openPageReturnFocus[projKey])
	delete(pc.openPageReturnFocus, projKey)
	return ret
}

// --- Lazy nav-tree child builders -----------------------------------------------

func (a *App) buildProjectNavChildren(ws projectWorkspaceData, secKey, focusID string, expanded, allowPeek bool) ([]views.ProjectNavNode, int) {
	pc := a.projectCtrl()
	lockedFocus := ""
	if locked := pc.activeNavItem(ws.projectKey); locked != nil {
		lockedFocus = locked.FocusID
	}
	switch secKey {
	case "tasks":
		return a.projectTaskNavNodes(ws, focusID, lockedFocus, expanded, allowPeek)
	case "pages":
		return a.projectPageNavNodes(ws, focusID, lockedFocus, expanded, allowPeek)
	case "panes":
		return a.projectTreeNavNodes(ws, "panes", data.KindPane, data.BuildTree(ws.panes, "parentPane", "", a.nav.showArchived), focusID, lockedFocus, expanded, allowPeek)
	case "lanes":
		return a.projectTreeNavNodes(ws, "lanes", data.KindLane, data.BuildTree(ws.allLanes, "parentLane", "", a.treeIncludesArchived(data.KindLane)), focusID, lockedFocus, expanded, allowPeek)
	case "documents":
		return a.projectRecordNavNodes(ws, "documents", data.KindDocument, ws.documents, focusID, lockedFocus, expanded, allowPeek)
	case "notes":
		return a.projectRecordNavNodes(ws, "notes", data.KindNote, ws.notes, focusID, lockedFocus, expanded, allowPeek)
	case "questions":
		return a.projectRecordNavNodes(ws, "questions", data.KindQuestion, ws.questions, focusID, lockedFocus, expanded, allowPeek)
	case "commands":
		return a.projectRecordNavNodes(ws, "commands", data.KindCommand, ws.commands, focusID, lockedFocus, expanded, allowPeek)
	case "context":
		return a.projectContextNavNodes(ws, "context", ws.contextItems, focusID, lockedFocus, expanded, allowPeek && a.projectNavSurfaceContextUnreadEnabled())
	case "pinned":
		return a.projectContextNavNodes(ws, "pinned", ws.pinnedItems, focusID, lockedFocus, expanded, allowPeek)
	case "prs":
		return a.projectContextNavNodes(ws, "prs", ws.prItems, focusID, lockedFocus, expanded, allowPeek)
	case "worktrees":
		if !expanded {
			return nil, 0
		}
		return a.projectWorktreeNavNodes(ws.projectKey, ws.worktrees, focusID, lockedFocus), 0
	}
	return nil, 0
}

func (a *App) projectTaskNavNodes(ws projectWorkspaceData, focusID, lockedFocus string, expanded, allowPeek bool) ([]views.ProjectNavNode, int) {
	return a.projectTreeNavNodes(ws, "tasks", data.KindTask, ws.taskTree, focusID, lockedFocus, expanded, allowPeek)
}

func applyLaneNavPresentation(nd data.TreeNode, showProgressBar bool, node *views.ProjectNavNode) {
	switch data.LaneProgressOf(nd.Record) {
	case data.LaneProgressDone:
		node.Icon = "✓"
		node.IconTone = "muted"
		node.Tone = "muted"
		node.Dim = true
	case data.LaneProgressExcluded:
		node.Icon = "×"
		node.IconTone = "muted"
		node.Tone = "muted"
		node.Dim = true
	}
	if showProgressBar {
		done, activeCnt, planned := views.LaneTreeRollupCounts(nd)
		node.ProgressBar = views.CompactLaneBarRuns(done, activeCnt, planned, 4)
	}
}

func (a *App) projectTreeNavNodes(ws projectWorkspaceData, secKey, kind string, nodes []data.TreeNode, focusID, lockedFocus string, sectionExpanded, allowPeek bool) ([]views.ProjectNavNode, int) {
	pc := a.projectCtrl()
	projKey := ws.projectKey
	schema := a.schema(kind)
	styleOpts := a.styleOptions()

	var build func(ns []data.TreeNode, parentID string, peekOnly bool) ([]views.ProjectNavNode, int)
	build = func(ns []data.TreeNode, parentID string, peekOnly bool) ([]views.ProjectNavNode, int) {
		out := make([]views.ProjectNavNode, 0, len(ns))
		totalUnread := 0
		for _, nd := range ns {
			rec := nd.Record
			ref := rec.Ref()
			nodeFocusID := projectNavNodePrefix + secKey + "." + rec.ID
			expandable := len(nd.Children) > 0
			nodeKey := secKey + "." + rec.ID
			nodeExpanded := expandable && pc.isNavNodeExpanded(projKey, nodeKey)
			selfUnread := a.isNavRecordUnread(kind, rec, &ws)
			if selfUnread {
				totalUnread++
			}

			var children []views.ProjectNavNode
			childUnread := 0
			if expandable {
				childPeekOnly := peekOnly || !nodeExpanded
				children, childUnread = build(nd.Children, rec.ID, childPeekOnly)
				totalUnread += childUnread
				if !nodeExpanded && !allowPeek {
					children = nil
				}
			}

			hasUnread := selfUnread || childUnread > 0
			if peekOnly && !hasUnread {
				continue
			}

			title := strings.TrimSpace(views.Title(schema, rec))
			if title == "" {
				title = rec.ID
			}
			rs := views.ResolveRecordStyle(schema, rec, styleOpts)
			badge := ""
			if expandable {
				badge = fmt.Sprintf("%d", len(nd.Children))
			} else if statusField, _, hasStatus := schema.PrimaryStatusField(); hasStatus {
				if st := strings.TrimSpace(rec.Text(statusField)); st != "" && secKey != "tasks" {
					badge = st
				}
			}
			active := focusID == nodeFocusID || lockedFocus == nodeFocusID
			if a.archiveAnimActive(ref) {
				children, expandable, nodeExpanded = nil, false, false
			}
			node := views.ProjectNavNode{
				ID:         rec.ID,
				Section:    secKey,
				ParentID:   parentID,
				Label:      title,
				Icon:       rs.Icon,
				IconTone:   rs.IconTone,
				Badge:      badge,
				Tone:       rs.RowTone,
				Ref:        &ref,
				Unread:     selfUnread,
				Peeked:     peekOnly,
				HasUnread:  hasUnread,
				Active:     active,
				Expandable: expandable,
				Expanded:   nodeExpanded,
				Children:   children,
				Facts:      a.projectNavRecordFacts(schema, rec),
			}
			if kind == data.KindLane {
				applyLaneNavPresentation(nd, a.projectNavLaneProgressBarEnabled(), &node)
			}
			out = append(out, node)
			a.applyArchiveAnimToNavNode(ref, &out[len(out)-1])
		}
		return out, totalUnread
	}

	roots, unreadTotal := build(nodes, "", !sectionExpanded)
	if !sectionExpanded && !allowPeek {
		return nil, unreadTotal
	}
	var register func(ns []views.ProjectNavNode, parentFocusID string)
	register = func(ns []views.ProjectNavNode, parentFocusID string) {
		for _, item := range ns {
			nodeFocusID := projectNavNodePrefix + secKey + "." + item.ID
			pc.registerNavTarget(projKey, projectNavTarget{
				FocusID:       nodeFocusID,
				Section:       secKey,
				NodeID:        item.ID,
				ParentFocusID: parentFocusID,
				Label:         item.Label,
				Ref:           item.Ref,
				Unread:        item.Unread,
				Expandable:    item.Expandable,
				Expanded:      item.Expanded,
			})
			if len(item.Children) > 0 {
				register(item.Children, nodeFocusID)
			}
		}
	}
	register(roots, projectNavPrefix+secKey)
	return roots, unreadTotal
}

func (a *App) projectPageNavNodes(ws projectWorkspaceData, focusID, lockedFocus string, expanded, allowPeek bool) ([]views.ProjectNavNode, int) {
	pc := a.projectCtrl()
	schema := a.schema(data.KindPage)
	openPage := pc.openPageID(ws.projectKey)
	parentFocus := projectNavPrefix + "pages"
	out := make([]views.ProjectNavNode, 0, len(ws.pages))
	unreadCount := 0
	for _, p := range ws.pages {
		unread := a.isNavRecordUnread(data.KindPage, p, &ws)
		if unread {
			unreadCount++
		}
		if !expanded && (!allowPeek || !unread) {
			continue
		}
		ref := p.Ref()
		nodeFocusID := projectNavNodePrefix + "pages." + p.ID
		title := strings.TrimSpace(views.Title(schema, p))
		if title == "" {
			title = p.ID
		}
		icon := "≡"
		switch {
		case p.Text("pageKind") == "questions":
			icon = "?"
		case p.Bool("pinned"):
			icon = "▪"
		}
		active := focusID == nodeFocusID || lockedFocus == nodeFocusID || openPage == p.ID
		facts := a.projectNavRecordFacts(schema, p)
		facts["pageId"] = p.ID
		facts["pageKind"] = p.Text("pageKind")
		facts["pinned"] = p.Bool("pinned")
		out = append(out, views.ProjectNavNode{
			ID:        p.ID,
			Section:   "pages",
			ParentID:  "",
			Label:     title,
			Icon:      icon,
			Badge:     p.Text("pageKind"),
			Ref:       &ref,
			Unread:    unread,
			Peeked:    !expanded && unread,
			HasUnread: unread,
			Active:    active,
			Facts:     facts,
		})
		pc.registerNavTarget(ws.projectKey, projectNavTarget{
			FocusID:       nodeFocusID,
			Section:       "pages",
			NodeID:        p.ID,
			ParentFocusID: parentFocus,
			Label:         title,
			Ref:           &ref,
			Unread:        unread,
		})
	}
	return out, unreadCount
}

func (a *App) projectRecordNavNodes(ws projectWorkspaceData, secKey, kind string, recs []data.Record, focusID, lockedFocus string, expanded, allowPeek bool) ([]views.ProjectNavNode, int) {
	pc := a.projectCtrl()
	projKey := ws.projectKey
	schema := a.schema(kind)
	styleOpts := a.styleOptions()
	parentFocus := projectNavPrefix + secKey
	out := make([]views.ProjectNavNode, 0, len(recs))
	unreadCount := 0
	for _, rec := range recs {
		unread := a.isNavRecordUnread(kind, rec, &ws)
		if unread {
			unreadCount++
		}
		if !expanded && (!allowPeek || !unread) {
			continue
		}
		ref := rec.Ref()
		nodeFocusID := projectNavNodePrefix + secKey + "." + rec.ID
		title := strings.TrimSpace(views.Title(schema, rec))
		if title == "" {
			title = rec.ID
		}
		rs := views.ResolveRecordStyle(schema, rec, styleOpts)
		badge := ""
		if statusField, _, hasStatus := schema.PrimaryStatusField(); hasStatus {
			badge = strings.TrimSpace(rec.Text(statusField))
		}
		active := focusID == nodeFocusID || lockedFocus == nodeFocusID
		out = append(out, views.ProjectNavNode{
			ID:        rec.ID,
			Section:   secKey,
			ParentID:  "",
			Label:     title,
			Icon:      rs.Icon,
			IconTone:  rs.IconTone,
			Badge:     badge,
			Tone:      rs.RowTone,
			Ref:       &ref,
			Unread:    unread,
			Peeked:    !expanded && unread,
			HasUnread: unread,
			Active:    active,
			Facts:     a.projectNavRecordFacts(schema, rec),
		})
		pc.registerNavTarget(projKey, projectNavTarget{
			FocusID:       nodeFocusID,
			Section:       secKey,
			NodeID:        rec.ID,
			ParentFocusID: parentFocus,
			Label:         title,
			Ref:           &ref,
			Unread:        unread,
		})
	}
	if secKey == "context" && !a.projectNavSurfaceContextUnreadEnabled() {
		unreadCount = 0
	}
	return out, unreadCount
}

func projectNavSafeID(raw string, idx int) string {
	s := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, strings.TrimSpace(raw))
	if s == "" {
		return fmt.Sprintf("item_%d", idx)
	}
	if len(s) > 48 {
		s = s[:48]
	}
	return s
}

const (
	projectNavContextMaxItems = 40
	projectNavOverflowNodeID  = "_more"
)

func projectNavSectionDisplayTitle(secKey string) string {
	switch secKey {
	case "context":
		return "Context"
	case "pinned":
		return "Pinned"
	case "prs":
		return "PRs"
	default:
		return secKey
	}
}

func skipCollapsedContextNav(ws projectWorkspaceData, expanded bool) bool {
	return !expanded && (len(ws.contextRecords) == 0 || len(ws.reviewContext) == 0)
}

func (a *App) projectContextNavNodes(ws projectWorkspaceData, secKey string, items []CollectedContextItem, focusID, lockedFocus string, expanded, allowPeek bool) ([]views.ProjectNavNode, int) {
	if skipCollapsedContextNav(ws, expanded) {
		return nil, 0
	}
	pc := a.projectCtrl()
	projKey := ws.projectKey
	parentFocus := projectNavPrefix + secKey
	var seen map[string]int
	var out []views.ProjectNavNode
	unreadCount := 0
	overflow := 0
	reviewInSection := a.projectContextReviewEnabled() && !a.projectNavSurfaceContextUnreadEnabled()
	for i, it := range items {
		rec, persisted := ws.contextRecords[it.RecordID]
		if reviewInSection && persisted && a.isProjectContextUnread(rec) {
			continue
		}
		unread := persisted && a.isNavRecordUnread(data.KindContext, rec, &ws)
		if unread {
			unreadCount++
		}
		if !expanded && (!allowPeek || !unread) {
			continue
		}
		if len(out) >= projectNavContextMaxItems {
			overflow++
			continue
		}
		if seen == nil {
			capHint := min(len(items)-i, projectNavContextMaxItems)
			seen = make(map[string]int, capHint)
			out = make([]views.ProjectNavNode, 0, capHint+1)
		}
		nodeID := it.RecordID
		if nodeID == "" {
			nodeID = projectNavSafeID(it.CanonKey, i)
		}
		if n := seen[nodeID]; n > 0 {
			nodeID = fmt.Sprintf("%s_%d", nodeID, n)
		}
		seen[nodeID]++

		nodeFocusID := projectNavNodePrefix + secKey + "." + nodeID
		label := singleLineContextText(it.Label)
		if label == "" {
			if it.Kind == contextitems.KindCode {
				label = data.CodeContextTitle(it.Value, it.Language)
			} else {
				label = singleLineContextText(it.Value)
			}
		}
		icon := ""
		tone := ""
		if it.Official || it.Starred {
			icon = "★"
			if !unread && it.Official {
				tone = "accent"
			}
		}
		var ref *core.Ref
		if it.RecordID != "" {
			r := core.Ref{Kind: data.KindContext, ID: it.RecordID}
			ref = &r
		}
		active := focusID == nodeFocusID || lockedFocus == nodeFocusID
		itemCopy := it
		out = append(out, views.ProjectNavNode{
			ID:        nodeID,
			Section:   secKey,
			ParentID:  "",
			Label:     clip(label, 40),
			Icon:      icon,
			Badge:     it.Kind,
			Tone:      tone,
			Ref:       ref,
			Unread:    unread,
			Peeked:    !expanded && unread,
			HasUnread: unread,
			Active:    active,
			Facts: map[string]any{
				"contextItem": true,
				"contextKey":  it.Key,
				"official":    it.Official,
			},
		})
		pc.registerNavTarget(projKey, projectNavTarget{
			FocusID:       nodeFocusID,
			Section:       secKey,
			NodeID:        nodeID,
			ParentFocusID: parentFocus,
			Label:         label,
			Ref:           ref,
			ContextKey:    it.Key,
			ContextItem:   &itemCopy,
			Unread:        unread,
		})
	}
	if overflow > 0 {
		moreFocusID := projectNavNodePrefix + secKey + "." + projectNavOverflowNodeID
		label := fmt.Sprintf("+%d more — open %s section", overflow, projectNavSectionDisplayTitle(secKey))
		out = append(out, views.ProjectNavNode{
			ID:      projectNavOverflowNodeID,
			Section: secKey,
			Label:   label,
			Tone:    "muted",
			Active:  focusID == moreFocusID,
			Facts: map[string]any{
				"navOverflow":   true,
				"overflowCount": overflow,
			},
		})
		pc.registerNavTarget(projKey, projectNavTarget{
			FocusID:       moreFocusID,
			Section:       secKey,
			NodeID:        projectNavOverflowNodeID,
			ParentFocusID: parentFocus,
			Label:         label,
		})
	}
	if secKey == "context" && !a.projectNavSurfaceContextUnreadEnabled() {
		unreadCount = 0
	}
	return out, unreadCount
}

func (a *App) projectWorktreeNavNodes(projKey string, items []data.ProjectWorktree, focusID, lockedFocus string) []views.ProjectNavNode {
	pc := a.projectCtrl()
	parentFocus := projectNavPrefix + "worktrees"
	seen := map[string]int{}
	out := make([]views.ProjectNavNode, 0, len(items))
	for i, wt := range items {
		nodeID := projectNavSafeID(wt.Path, i)
		if n := seen[nodeID]; n > 0 {
			nodeID = fmt.Sprintf("%s_%d", nodeID, n)
		}
		seen[nodeID]++

		nodeFocusID := projectNavNodePrefix + "worktrees." + nodeID
		label := strings.TrimSpace(wt.Branch)
		if label == "" {
			label = filepath.Base(wt.Path)
		}
		if label == "" || label == "." {
			label = wt.Path
		}
		var ref *core.Ref
		if wt.Ref.ID != "" && wt.Ref.Kind != "" {
			r := wt.Ref
			ref = &r
		}
		wtCopy := wt
		active := focusID == nodeFocusID || lockedFocus == nodeFocusID
		out = append(out, views.ProjectNavNode{
			ID:       nodeID,
			Section:  "worktrees",
			ParentID: "",
			Label:    label,
			Badge:    wt.Source,
			Ref:      ref,
			Active:   active,
			Facts:    map[string]any{"path": wt.Path},
		})
		pc.registerNavTarget(projKey, projectNavTarget{
			FocusID:       nodeFocusID,
			Section:       "worktrees",
			NodeID:        nodeID,
			ParentFocusID: parentFocus,
			Label:         label,
			Ref:           ref,
			Worktree:      &wtCopy,
		})
	}
	return out
}

// --- Motion & toggle commands (l / h / space / enter) ---------------------------

func (a *App) projectNavNodeHL(s core.State, projKey, focus string, delta int) (core.State, bool) {
	pc := a.projectCtrl()
	target, ok := pc.navTarget(projKey, focus)
	if !ok {
		_ = a.View(s)
		target, ok = pc.navTarget(projKey, focus)
	}
	if !ok {
		return s, false
	}
	nodeKey := target.Section + "." + target.NodeID
	if delta > 0 {
		if target.Expandable && !pc.isNavNodeExpanded(projKey, nodeKey) {
			pc.setNavNodeExpanded(projKey, nodeKey, true)
			a.viewMemo.gen++
			a.say("expanded %s", target.Label)
			return s, true
		}
		// When a pane site with a middle TOC column is rendered in the right
		// area, let column(+1) step into the TOC instead of jumping straight
		// into the terminal screen.
		if hasNodeWithScope(a.View(s), views.ScopeTOC) {
			return s, false
		}
		res := a.projectNavNodeOpen(s, focus)
		return res.State, true
	}
	if target.Expandable && pc.isNavNodeExpanded(projKey, nodeKey) {
		pc.setNavNodeExpanded(projKey, nodeKey, false)
		a.viewMemo.gen++
		a.say("collapsed %s", target.Label)
		return s, true
	}
	parentFocus := target.ParentFocusID
	if parentFocus == "" {
		parentFocus = projectNavPrefix + target.Section
	}
	a.viewMemo.gen++
	return withFocus(s, parentFocus), true
}

func (a *App) projectNavHL(s core.State, delta int) (core.State, bool) {
	if !a.projectNavTreeEnabled() {
		return s, false
	}
	projKey := a.activeProjectWorkspaceKey(s)
	if projKey == "" {
		return s, false
	}
	focus := s.ActiveFrame().FocusID
	pc := a.projectCtrl()

	if strings.HasPrefix(focus, projectNavNodePrefix) {
		return a.projectNavNodeHL(s, projKey, focus, delta)
	}

	if secKey, isNode := projectNavFocusSection(focus); secKey != "" && !isNode {
		root := a.View(s)
		nd := root.Find(focus)
		expandable := nd != nil && nd.Facts != nil && nd.Facts["navExpandable"] == true
		if delta > 0 {
			if expandable && !pc.isNavSectionExpanded(projKey, secKey) {
				pc.setNavSectionExpanded(projKey, secKey, true)
				a.viewMemo.gen++
				a.say("expanded %s", secKey)
				return s, true
			}
			return s, false
		}
		if pc.isNavSectionExpanded(projKey, secKey) {
			pc.setNavSectionExpanded(projKey, secKey, false)
			a.viewMemo.gen++
			a.say("collapsed %s", secKey)
			return s, true
		}
		return s, false
	}

	return s, false
}

func (a *App) foldProjectNavNode(s core.State, projKey, nodeTarget, action string) (core.StepResult, bool) {
	pc := a.projectCtrl()
	target, ok := pc.navTarget(projKey, nodeTarget)
	if !ok {
		_ = a.View(s)
		target, ok = pc.navTarget(projKey, nodeTarget)
	}
	if !ok || !target.Expandable {
		return core.Next(s), ok
	}
	nodeKey := target.Section + "." + target.NodeID
	next := resolveFoldNext(pc.isNavNodeExpanded(projKey, nodeKey), action)
	pc.setNavNodeExpanded(projKey, nodeKey, next)
	a.viewMemo.gen++
	if next {
		a.say("expanded %s", target.Label)
	} else {
		a.say("collapsed %s", target.Label)
	}
	return core.Next(s), true
}

func (a *App) foldProjectNavSection(s core.State, projKey, nodeTarget, secKey, action string) (core.StepResult, bool) {
	root := a.View(s)
	nd := root.ByID(nodeTarget)
	if nd == nil || nd.Facts == nil || nd.Facts["navExpandable"] != true {
		return core.Next(s), false
	}
	pc := a.projectCtrl()
	next := resolveFoldNext(pc.isNavSectionExpanded(projKey, secKey), action)
	pc.setNavSectionExpanded(projKey, secKey, next)
	a.viewMemo.gen++
	if next {
		a.say("expanded %s", secKey)
	} else {
		a.say("collapsed %s", secKey)
	}
	return core.Next(s), true
}

func (a *App) foldProjectNavAt(s core.State, nodeTarget, action string) (core.StepResult, bool) {
	if !a.projectNavTreeEnabled() {
		return core.Next(s), false
	}
	projKey := a.activeProjectWorkspaceKey(s)
	if projKey == "" {
		return core.Next(s), false
	}
	if strings.HasPrefix(nodeTarget, projectNavNodePrefix) {
		return a.foldProjectNavNode(s, projKey, nodeTarget, action)
	}
	if secKey, isNode := projectNavFocusSection(nodeTarget); secKey != "" && !isNode {
		return a.foldProjectNavSection(s, projKey, nodeTarget, secKey, action)
	}
	return core.Next(s), false
}

func (a *App) projectNavToggle(s core.State, _ core.Command) core.StepResult {
	res, _ := a.foldProjectNavAt(s, s.ActiveFrame().FocusID, "toggle")
	return res
}

func (a *App) projectNavNodeOpen(s core.State, focusID string) core.StepResult {
	projKey := a.activeProjectWorkspaceKey(s)
	pc := a.projectCtrl()
	if projKey == "" {
		projKey = pc.activeProjectKey
	}
	target, ok := pc.navTarget(projKey, focusID)
	if !ok {
		_ = a.View(s)
		target, ok = pc.navTarget(projKey, focusID)
	}
	if !ok {
		return core.Next(a.column(s, 1))
	}

	if target.Unread {
		a.markProjectNavTargetRead(target)
	}
	if target.NodeID == projectNavOverflowNodeID {
		return a.projectSectionSelect(s, core.Command{ID: CmdProjectSectionSelect, Args: map[string]any{"section": target.Section}})
	}

	// 1. Pages: full interactive ModePage matching 1 Panes.
	if target.Section == "pages" && target.Ref != nil && target.Ref.Kind == data.KindPage {
		page, err := a.Store.Get(*target.Ref)
		if err != nil {
			a.fail("open page", err)
			return core.Next(s)
		}
		return a.projectOpenInteractivePage(s, projKey, *page, &target, target.FocusID)
	}

	// 2. Panes: open inline child pane (or jump to 1 Panes when opted out).
	if target.Section == "panes" && target.Ref != nil && target.Ref.Kind == data.KindPane {
		enterLive := a.railEnterLiveEnabled()
		if a.projectChildPaneViewEnabled() {
			return core.Next(a.openProjectChildPaneInPlace(s, projKey, target.Ref.ID, target.FocusID, enterLive))
		}
		return core.Next(a.jumpToPane(s, target.Ref.ID, enterLive))
	}

	// 3. Documents & Notes with Markdown body or readable .md file: scrollable Markdown reader.
	if target.Ref != nil && (target.Ref.Kind == data.KindDocument || target.Ref.Kind == data.KindNote) {
		if rec, err := a.Store.Get(*target.Ref); err == nil && rec != nil {
			if md, hasMD := a.recordMarkdownBody(*rec); hasMD && md != "" {
				pc.setActiveNavItem(projKey, &target)
				a.setPageScrollOffset(rec.ID, 0)
				a.viewMemo.gen++
				if a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetPageLockScroll, true) {
					s = withMode(withFocus(s, views.PageScrollID), ModePage)
					a.viewMemo.gen++
					focus := views.PageScrollID
					if first := a.firstCanvasWidgetID(s); first != "" {
						focus = first
					}
					a.say("%s · j/k scroll · [ / ] headings · esc back", views.Title(a.schema(rec.Kind), *rec))
					return core.Next(withMode(withFocus(s, focus), ModePage))
				}
				return core.Next(withFocus(s, views.PageScrollID))
			}
		}
	}

	// 4. All other records (tasks, lanes, questions, commands, persisted context) and detail cards.
	pc.setActiveNavItem(projKey, &target)
	a.viewMemo.gen++
	s = withFocus(s, "project.content")
	for _, n := range a.focusables(s) {
		if n.id == showHeader {
			return core.Next(withFocus(s, showHeader))
		}
	}
	for _, n := range a.focusables(s) {
		if n.region == "project.content" || n.region == "site.content" || n.region == showHeader {
			return core.Next(withFocus(s, n.id))
		}
	}
	return core.Next(a.column(s, 1))
}

func (a *App) projectNavViewKey(s core.State) string {
	if a == nil || a.proj == nil {
		return ""
	}
	projKey := a.activeProjectWorkspaceKey(s)
	if projKey == "" {
		return ""
	}
	pc := a.proj
	var b strings.Builder
	fmt.Fprintf(&b, "|pnt:%t:%t:%t:%t:%s", a.projectNavTreeEnabled(), a.projectNavSurfaceUnreadEnabled(), a.projectNavShowNewItemsEnabled(), a.projectNavCompactEnabled(), a.projectNavMode(projKey))
	if secs := pc.expandedSections[projKey]; len(secs) > 0 {
		keys := make([]string, 0, len(secs))
		for k, v := range secs {
			if v {
				keys = append(keys, k)
			}
		}
		slices.Sort(keys)
		b.WriteString("|pns:" + strings.Join(keys, ","))
	}
	if nds := pc.expandedNodes[projKey]; len(nds) > 0 {
		keys := make([]string, 0, len(nds))
		for k, v := range nds {
			if v {
				keys = append(keys, k)
			}
		}
		slices.Sort(keys)
		b.WriteString("|pnn:" + strings.Join(keys, ","))
	}
	if locked := pc.activeNavItem(projKey); locked != nil {
		b.WriteString("|pnl:" + locked.FocusID)
	}
	if pid := a.activeProjectPageID(s); pid != "" {
		fmt.Fprintf(&b, "|ppg:%s:%d", pid, a.pageScrollOffset(pid))
		if ck := a.canvasViewKey(pid); ck != "" {
			b.WriteString(ck)
		}
	}
	return b.String()
}
