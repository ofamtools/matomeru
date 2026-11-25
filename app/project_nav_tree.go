package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

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

func (a *App) buildProjectNavChildren(ws projectWorkspaceData, secKey, focusID string) []views.ProjectNavNode {
	pc := a.projectCtrl()
	lockedFocus := ""
	if locked := pc.activeNavItem(ws.projectKey); locked != nil {
		lockedFocus = locked.FocusID
	}
	switch secKey {
	case "tasks":
		return a.projectTaskNavNodes(ws, focusID, lockedFocus)
	case "pages":
		return a.projectPageNavNodes(ws, focusID, lockedFocus)
	case "panes":
		return a.projectTreeNavNodes(ws.projectKey, "panes", data.KindPane, data.BuildTree(ws.panes, "parentPane", "", a.nav.showArchived), focusID, lockedFocus)
	case "lanes":
		return a.projectTreeNavNodes(ws.projectKey, "lanes", data.KindLane, data.BuildTree(ws.allLanes, "parentLane", "", a.nav.showArchived), focusID, lockedFocus)
	case "documents":
		return a.projectRecordNavNodes(ws.projectKey, "documents", data.KindDocument, ws.documents, focusID, lockedFocus)
	case "notes":
		return a.projectRecordNavNodes(ws.projectKey, "notes", data.KindNote, ws.notes, focusID, lockedFocus)
	case "questions":
		return a.projectRecordNavNodes(ws.projectKey, "questions", data.KindQuestion, ws.questions, focusID, lockedFocus)
	case "commands":
		return a.projectRecordNavNodes(ws.projectKey, "commands", data.KindCommand, ws.commands, focusID, lockedFocus)
	case "context":
		return a.projectContextNavNodes(ws.projectKey, "context", ws.contextItems, focusID, lockedFocus)
	case "pinned":
		return a.projectContextNavNodes(ws.projectKey, "pinned", ws.pinnedItems, focusID, lockedFocus)
	case "prs":
		return a.projectContextNavNodes(ws.projectKey, "prs", ws.prItems, focusID, lockedFocus)
	case "worktrees":
		return a.projectWorktreeNavNodes(ws.projectKey, ws.worktrees, focusID, lockedFocus)
	}
	return nil
}

func (a *App) projectTaskNavNodes(ws projectWorkspaceData, focusID, lockedFocus string) []views.ProjectNavNode {
	if a.Store == nil || ws.projectID == "" {
		return nil
	}
	allNodes, err := a.recordTreeNodes(data.KindTask)
	if err != nil {
		return nil
	}
	projNode, ok := findProjectTreeNode(allNodes, ws.projectID)
	if !ok || len(projNode.Children) == 0 {
		return nil
	}
	return a.projectTreeNavNodes(ws.projectKey, "tasks", data.KindTask, projNode.Children, focusID, lockedFocus)
}

func (a *App) projectTreeNavNodes(projKey, secKey, kind string, nodes []data.TreeNode, focusID, lockedFocus string) []views.ProjectNavNode {
	pc := a.projectCtrl()
	schema := a.schema(kind)
	styleOpts := a.styleOptions()
	var build func(ns []data.TreeNode, parentID, parentFocusID string) []views.ProjectNavNode
	build = func(ns []data.TreeNode, parentID, parentFocusID string) []views.ProjectNavNode {
		out := make([]views.ProjectNavNode, 0, len(ns))
		for _, nd := range ns {
			rec := nd.Record
			ref := rec.Ref()
			nodeFocusID := projectNavNodePrefix + secKey + "." + rec.ID
			expandable := len(nd.Children) > 0
			nodeKey := secKey + "." + rec.ID
			expanded := expandable && pc.isNavNodeExpanded(projKey, nodeKey)
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
			unread := rec.IsUnreadBy(schema.UnreadField)
			item := views.ProjectNavNode{
				ID:         rec.ID,
				Section:    secKey,
				ParentID:   parentID,
				Label:      title,
				Icon:       rs.Icon,
				Badge:      badge,
				Ref:        &ref,
				Unread:     unread,
				Active:     active,
				Expandable: expandable,
				Expanded:   expanded,
			}
			pc.registerNavTarget(projKey, projectNavTarget{
				FocusID:       nodeFocusID,
				Section:       secKey,
				NodeID:        rec.ID,
				ParentFocusID: parentFocusID,
				Label:         title,
				Ref:           &ref,
				Expandable:    expandable,
				Expanded:      expanded,
			})
			if expanded {
				item.Children = build(nd.Children, rec.ID, nodeFocusID)
			}
			out = append(out, item)
		}
		return out
	}
	return build(nodes, "", projectNavPrefix+secKey)
}

func (a *App) projectPageNavNodes(ws projectWorkspaceData, focusID, lockedFocus string) []views.ProjectNavNode {
	pc := a.projectCtrl()
	schema := a.schema(data.KindPage)
	openPage := pc.openPageID(ws.projectKey)
	parentFocus := projectNavPrefix + "pages"
	out := make([]views.ProjectNavNode, 0, len(ws.pages))
	for _, p := range ws.pages {
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
		unread := p.IsUnread()
		active := focusID == nodeFocusID || lockedFocus == nodeFocusID || openPage == p.ID
		out = append(out, views.ProjectNavNode{
			ID:       p.ID,
			Section:  "pages",
			ParentID: "",
			Label:    title,
			Icon:     icon,
			Badge:    p.Text("pageKind"),
			Ref:      &ref,
			Unread:   unread,
			Active:   active,
			Facts: map[string]any{
				"pageId":   p.ID,
				"pageKind": p.Text("pageKind"),
				"pinned":   p.Bool("pinned"),
			},
		})
		pc.registerNavTarget(ws.projectKey, projectNavTarget{
			FocusID:       nodeFocusID,
			Section:       "pages",
			NodeID:        p.ID,
			ParentFocusID: parentFocus,
			Label:         title,
			Ref:           &ref,
		})
	}
	return out
}

func (a *App) projectRecordNavNodes(projKey, secKey, kind string, recs []data.Record, focusID, lockedFocus string) []views.ProjectNavNode {
	pc := a.projectCtrl()
	schema := a.schema(kind)
	styleOpts := a.styleOptions()
	parentFocus := projectNavPrefix + secKey
	out := make([]views.ProjectNavNode, 0, len(recs))
	for _, rec := range recs {
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
		unread := rec.IsUnreadBy(schema.UnreadField)
		if kind == data.KindQuestion && rec.Text("status") == "open" {
			unread = true
		}
		active := focusID == nodeFocusID || lockedFocus == nodeFocusID
		out = append(out, views.ProjectNavNode{
			ID:       rec.ID,
			Section:  secKey,
			ParentID: "",
			Label:    title,
			Icon:     rs.Icon,
			Badge:    badge,
			Ref:      &ref,
			Unread:   unread,
			Active:   active,
		})
		pc.registerNavTarget(projKey, projectNavTarget{
			FocusID:       nodeFocusID,
			Section:       secKey,
			NodeID:        rec.ID,
			ParentFocusID: parentFocus,
			Label:         title,
			Ref:           &ref,
		})
	}
	return out
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

func (a *App) projectContextNavNodes(projKey, secKey string, items []CollectedContextItem, focusID, lockedFocus string) []views.ProjectNavNode {
	pc := a.projectCtrl()
	parentFocus := projectNavPrefix + secKey
	seen := map[string]int{}
	out := make([]views.ProjectNavNode, 0, len(items))
	for i, it := range items {
		nodeID := it.RecordID
		if nodeID == "" {
			nodeID = projectNavSafeID(it.CanonKey, i)
		}
		if n := seen[nodeID]; n > 0 {
			nodeID = fmt.Sprintf("%s_%d", nodeID, n)
		}
		seen[nodeID]++

		nodeFocusID := projectNavNodePrefix + secKey + "." + nodeID
		label := strings.TrimSpace(it.Label)
		if label == "" {
			label = it.Value
		}
		icon := ""
		tone := ""
		if it.Official {
			icon = "★"
			tone = "accent"
		}
		var ref *core.Ref
		if it.RecordID != "" {
			r := core.Ref{Kind: data.KindContext, ID: it.RecordID}
			ref = &r
		}
		active := focusID == nodeFocusID || lockedFocus == nodeFocusID
		itemCopy := it
		out = append(out, views.ProjectNavNode{
			ID:       nodeID,
			Section:  secKey,
			ParentID: "",
			Label:    clip(label, 40),
			Icon:     icon,
			Badge:    it.Kind,
			Tone:     tone,
			Ref:      ref,
			Active:   active,
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
		})
	}
	return out
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

func (a *App) projectNavToggle(s core.State, _ core.Command) core.StepResult {
	if !a.projectNavTreeEnabled() {
		return core.Next(s)
	}
	projKey := a.activeProjectWorkspaceKey(s)
	if projKey == "" {
		return core.Next(s)
	}
	focus := s.ActiveFrame().FocusID
	pc := a.projectCtrl()

	if strings.HasPrefix(focus, projectNavNodePrefix) {
		target, ok := pc.navTarget(projKey, focus)
		if !ok {
			_ = a.View(s)
			target, ok = pc.navTarget(projKey, focus)
		}
		if ok && target.Expandable {
			nodeKey := target.Section + "." + target.NodeID
			next := !pc.isNavNodeExpanded(projKey, nodeKey)
			pc.setNavNodeExpanded(projKey, nodeKey, next)
			a.viewMemo.gen++
			if next {
				a.say("expanded %s", target.Label)
			} else {
				a.say("collapsed %s", target.Label)
			}
		}
		return core.Next(s)
	}

	if secKey, isNode := projectNavFocusSection(focus); secKey != "" && !isNode {
		root := a.View(s)
		nd := root.Find(focus)
		if nd != nil && nd.Facts != nil && nd.Facts["navExpandable"] == true {
			next := !pc.isNavSectionExpanded(projKey, secKey)
			pc.setNavSectionExpanded(projKey, secKey, next)
			a.viewMemo.gen++
			if next {
				a.say("expanded %s", secKey)
			} else {
				a.say("collapsed %s", secKey)
			}
		}
	}
	return core.Next(s)
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
		if a.projectChildPaneViewEnabled() {
			return core.Next(a.openProjectChildPaneInPlace(s, projKey, target.Ref.ID, target.FocusID, false))
		}
		return core.Next(a.jumpToPane(s, target.Ref.ID, true))
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
	fmt.Fprintf(&b, "|pnt:%t", a.projectNavTreeEnabled())
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
