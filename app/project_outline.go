package app

import (
	"maps"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/tree"
)

const (
	projectOutlineTree  = "project.tasks.tree"
	projectOutlinePanel = "project.tasks"
	projectOutlineTitle = "Tasks (Outline) · w agent · t thread · r reply · D dispatch"
)

// inProjectOutlineTree reports whether focus is currently in the embedded
// Project Page outline todo tree in 1 Panes or an open Project Tab with no modal open.
func (a *App) inProjectOutlineTree(s *core.State) bool {
	if a == nil || s == nil {
		return false
	}
	inSpace := s.ActivePage == SpacePanes || isProjectTab(s.ActivePage)
	return inSpace && s.ActiveFrame().FocusID == projectOutlineTree && a.Modal() == ""
}

// activeProjectOutlineID resolves the project task ID for the active embedded
// Project Outline tree, preferring an open Project Tab's project ID, then the
// project controller's active project ID, then the last rendered project ID,
// and falling back to the rail's active project task.
func (a *App) activeProjectOutlineID(s core.State) string {
	if id := strings.TrimSpace(projectIDFromTab(s.ActivePage)); id != "" {
		return id
	}
	if a != nil {
		if id := strings.TrimSpace(a.projectCtrl().activeProjectID); id != "" {
			return id
		}
	}
	if a != nil && a.trees != nil {
		if id := strings.TrimSpace(a.trees.activeProjectID); id != "" {
			return id
		}
	}
	if ref, ok := a.railActiveProjectTask(s); ok && ref.ID != "" {
		return ref.ID
	}
	return ""
}

func findProjectTreeNode(nodes []data.TreeNode, projectID string) (data.TreeNode, bool) {
	if projectID == "" {
		return data.TreeNode{}, false
	}
	for _, nd := range nodes {
		if nd.Record.ID == projectID {
			return nd, true
		}
		if found, ok := findProjectTreeNode(nd.Children, projectID); ok {
			return found, true
		}
	}
	return data.TreeNode{}, false
}

func (a *App) cachedEditingTree(tm *tree.Model, treeID string) ([]data.TreeNode, bool) {
	if tm == nil || !tm.Editing() || a.trees == nil {
		return nil, false
	}
	entry, ok := a.trees.syncCache[tm]
	if !ok {
		return nil, false
	}
	tm.ScrollOff, tm.PageSize = a.scrollOff(), a.treeViewportHeight(treeID)
	return entry.nodes, true
}

func (a *App) syncScopedTaskTree(tm *tree.Model, treeID string) (data.Schema, []data.TreeNode, bool) {
	if a.trees == nil {
		return data.Schema{}, nil, false
	}
	if treeID == featuresOutlineTree {
		a.resyncActiveFeaturesOutline()
		return a.schema(data.KindTask), nil, true
	}
	projID := a.trees.projectIDForModel(tm)
	if treeID == featuresCaptureTree || projID != "" {
		if wid := a.featuresWishlistTaskID(); wid != "" && (treeID == featuresCaptureTree || projID == wid) {
			s, n := a.syncFeaturesCaptureTree(tm, wid)
			return s, n, true
		}
		if projID != "" {
			s, n := a.syncProjectTaskTree(tm, projID, projectOutlineTree)
			return s, n, true
		}
	}
	if treeID == projectOutlineTree && a.trees.activeProjectID != "" {
		s, n := a.syncProjectTaskTree(tm, a.trees.activeProjectID, projectOutlineTree)
		return s, n, true
	}
	return data.Schema{}, nil, false
}

// syncProjectTaskTree synchronizes tm with the task subtree rooted at
// projectID, sharing fold state and inline note threads with 2 Work and T
// Outline while pinning the base hoist to projectID so inline adds (`o`/`O`)
// and outdents stay scoped to the project.
func (a *App) syncProjectTaskTree(tm *tree.Model, projectID, treeID string) (data.Schema, []data.TreeNode) {
	projectID = strings.TrimSpace(projectID)
	schema := a.schema(data.KindTask)
	cs := a.activeCleanSlate(data.KindTask, treeID)
	syncKey := ""
	if a.Store != nil && a.trees != nil {
		rev := a.storeRev()
		syncKey = a.treeSyncKey(rev, data.KindTask, treeID, cs) + "|proj:" + projectID
		if entry, ok := a.trees.syncCache[tm]; ok && entry.key == syncKey && (a.trees.foldInitialized[data.KindTask] || len(entry.nodes) == 0) {
			tm.Folded = maps.Clone(a.trees.folded[data.KindTask])
			if tm.Folded == nil {
				tm.Folded = map[string]bool{}
			}
			if projectID != "" {
				delete(tm.Folded, projectID)
			}
			tm.ScrollOff = a.scrollOff()
			tm.PageSize = a.treeViewportHeight(treeID)
			if len(tm.Nodes) > 0 && projectID != "" {
				if len(tm.HoistStack) == 0 || tm.HoistStack[0] != projectID {
					tm.HoistStack = []string{projectID}
				}
				if tm.SelectedID == "" && len(tm.Nodes[0].Children) > 0 {
					tm.SelectedID = tm.Nodes[0].Children[0].ID
				}
			} else {
				tm.HoistStack = nil
			}
			return schema, entry.nodes
		}
	}
	allNodes, err := a.recordTreeNodes(data.KindTask)
	if err != nil {
		a.fail(data.KindTask, err)
	}
	a.initTreeFoldState(data.KindTask, allNodes)

	var nodes []data.TreeNode
	if projNode, ok := findProjectTreeNode(allNodes, projectID); ok {
		nodes = a.applyOutlineViewToProjectRoot([]data.TreeNode{projNode}, data.KindTask, treeID)
	}

	builtNodes, opts := a.buildTreeNodes(schema, data.KindTask, nodes)

	prevSelected := tm.SelectedID
	tm.DefaultFolded = false
	tm.Folded = maps.Clone(a.trees.folded[data.KindTask])
	if tm.Folded == nil {
		tm.Folded = map[string]bool{}
	}
	if projectID != "" {
		delete(tm.Folded, projectID)
	}
	tm.GapTopLevel = opts.GapTopLevel
	tm.Guides = opts.Guides
	tm.ShowFoldCount = opts.ShowFoldCount
	tm.ShowGutter = opts.ShowGutter && len(schema.GutterRules) > 0
	tm.Wrap = a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetWrapOutline, true)
	tm.ScrollOff = a.scrollOff()
	tm.PageSize = a.treeViewportHeight(treeID)

	if len(builtNodes) > 0 && projectID != "" {
		if len(tm.HoistStack) == 0 || tm.HoistStack[0] != projectID {
			tm.HoistStack = []string{projectID}
		}
		if prevSelected == "" && len(builtNodes[0].Children) > 0 {
			tm.SelectedID = builtNodes[0].Children[0].ID
		}
	} else {
		tm.HoistStack = nil
	}

	tm.SetNodes(builtNodes)
	a.saveTreeFolded(data.KindTask, treeID, tm)
	if syncKey != "" && a.trees != nil {
		if a.trees.syncCache == nil {
			a.trees.syncCache = map[*tree.Model]treeSyncEntry{}
		}
		a.trees.syncCache[tm] = treeSyncEntry{key: syncKey, nodes: nodes}
	}
	return schema, nodes
}

// projectOutlineView builds the first-class embedded Project Page outline todo
// view for projectID, reusing the full schema-driven tree, note indicators,
// inline note threads, and task-tree keybindings of 2 Work and T Outline.
func (a *App) projectOutlineView(s *core.State, projectID string) ui.ViewNode {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" && s != nil {
		projectID = a.activeProjectOutlineID(*s)
	}
	if a.trees != nil && projectID != "" {
		a.trees.activeProjectID = projectID
	}
	tm := a.trees.projectModel(projectID)
	schema, _ := a.syncProjectTaskTree(tm, projectID, projectOutlineTree)
	a.registerTreeFilterable(projectOutlineTree, tm)

	extra := a.outlineViewBadges(projectOutlineTree)
	if a.nav.showArchived {
		extra = append(extra, ui.Badge{Text: "archived", Tone: "muted"})
	}
	panel := views.TreeViewWith(schema, tm, projectOutlinePanel, projectOutlineTree, views.ScopeIndex, a.stickyTreeBreadcrumbsEnabled(), extra...)
	panel.Title = projectOutlineTitle
	a.applyOutlineViewFacts(&panel, projectOutlineTree)

	subHoisted := len(tm.HoistStack) > 1 || (tm.Hoisted() && tm.HoistID() != projectID)
	subHoistDepth := 0
	if subHoisted {
		subHoistDepth = max(1, len(tm.HoistStack)-1)
	}

	// Trim the base project hoist / root-count badges so the panel header stays
	// concise unless the user explicitly sub-hoisted into a child task.
	filteredBadges := make([]ui.Badge, 0, len(panel.Badges))
	for _, b := range panel.Badges {
		if strings.HasPrefix(b.Text, "hoist: ") {
			if !subHoisted {
				continue
			}
			if _, rest, ok := strings.Cut(strings.TrimPrefix(b.Text, "hoist: "), " › "); ok && rest != "" {
				b.Text = "hoist: " + rest
			}
		}
		if b.Text == "1 project" || b.Text == "0 projects" {
			continue
		}
		filteredBadges = append(filteredBadges, b)
	}
	panel.Badges = filteredBadges

	if panel.Facts == nil {
		panel.Facts = map[string]any{}
	}
	panel.Facts["isTree"] = true
	panel.Facts["projectOutline"] = true
	panel.Facts["projectID"] = projectID
	panel.Facts["hoisted"] = subHoisted
	panel.Facts["hoistDepth"] = subHoistDepth

	if len(panel.Children) > 0 {
		if panel.Children[0].Props == nil {
			panel.Children[0].Props = map[string]any{}
		}
		panel.Children[0].Props["scrollKey"] = projectID + "|" + tm.HoistID()
		if panel.Children[0].Facts == nil {
			panel.Children[0].Facts = map[string]any{}
		}
		panel.Children[0].Facts["isTree"] = true
		panel.Children[0].Facts["projectOutline"] = true
		panel.Children[0].Facts["projectID"] = projectID
		panel.Children[0].Facts["hoisted"] = subHoisted
		panel.Children[0].Facts["hoistDepth"] = subHoistDepth
	}
	views.ApplyTreeScope(&panel, tm, projectOutlineTree, projectID)
	a.applyProjectOutlineTreeNodeFacts(&panel, projectID)
	a.attachOutlineSuggestions(&panel, tm, data.KindTask, projectOutlineTree)
	return panel
}

func (a *App) applyProjectOutlineTreeNodeFacts(panel *ui.ViewNode, projectID string) {
	treeNode := panel.Find(projectOutlineTree)
	if treeNode == nil {
		return
	}
	if treeNode.Facts == nil {
		treeNode.Facts = map[string]any{}
	}
	treeNode.Facts["isTree"] = true
	treeNode.Facts["projectOutline"] = true
	treeNode.Facts["projectID"] = projectID
}

// projectOutlineTreeMove handles tree navigation motions when focus is in the
// embedded Project Page outline todo tree.
func (a *App) projectOutlineTreeMove(s core.State, c core.Command) {
	projectID := a.activeProjectOutlineID(s)
	if projectID == "" {
		return
	}
	tm := a.trees.projectModel(projectID)
	a.syncProjectTaskTree(tm, projectID, projectOutlineTree)
	tm.HandleCommand(c)
	a.saveTreeFolded(data.KindTask, projectOutlineTree, tm)
}
