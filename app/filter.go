package app

import (
	"strings"

	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/widgets/tree"
)

func (a *App) inPlaceTreeFilterEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetFilterInPlaceTree, true)
}

// tocFilterable adapts the pane TOC filter string into traits.Filterable.
type tocFilterable struct {
	a *App
}

var _ traits.Filterable = tocFilterable{}

func (f tocFilterable) Filter(query string, mode traits.MatchMode) bool {
	f.a.nav.tocFilter = query
	if mode != "" {
		f.a.nav.tocMatchMode = mode
	}
	return true
}

func (f tocFilterable) FilterQuery() string { return f.a.nav.tocFilter }
func (f tocFilterable) FilterMode() traits.MatchMode {
	if f.a.nav.tocMatchMode != "" {
		return f.a.nav.tocMatchMode
	}
	return f.a.matchMode()
}
func (f tocFilterable) FilterError() string {
	_, errMsg := traits.CompileMultiTerm(f.a.nav.tocFilter, f.FilterMode(), traits.FilterConfig{})
	return errMsg
}
func (f tocFilterable) IsFiltered() bool { return strings.TrimSpace(f.a.nav.tocFilter) != "" }

// projectNavFilterable adapts a Project Page's left-nav filter into traits.Filterable.
type projectNavFilterable struct {
	a       *App
	projKey string
}

var _ traits.Filterable = projectNavFilterable{}

func (f projectNavFilterable) Filter(query string, mode traits.MatchMode) bool {
	if f.a.nav.projectNavFilters == nil {
		f.a.nav.projectNavFilters = map[string]string{}
	}
	if query == "" {
		delete(f.a.nav.projectNavFilters, f.projKey)
	} else {
		f.a.nav.projectNavFilters[f.projKey] = query
	}
	if mode != "" {
		f.a.nav.projectNavMatchMode = mode
	}
	return true
}

func (f projectNavFilterable) FilterQuery() string {
	if f.a.nav == nil || f.a.nav.projectNavFilters == nil {
		return ""
	}
	return f.a.nav.projectNavFilters[f.projKey]
}

func (f projectNavFilterable) FilterMode() traits.MatchMode {
	if f.a.nav != nil && f.a.nav.projectNavMatchMode != "" {
		return f.a.nav.projectNavMatchMode
	}
	return f.a.matchMode()
}

func (f projectNavFilterable) FilterError() string {
	_, errMsg := traits.CompileMultiTerm(f.FilterQuery(), f.FilterMode(), traits.FilterConfig{})
	return errMsg
}

func (f projectNavFilterable) IsFiltered() bool {
	return strings.TrimSpace(f.FilterQuery()) != ""
}

// focusedTreeForFilter returns the tree.Model for targetID when targetID refers
// to a tree widget (Outline modal, Project Page outline/panes/lanes trees,
// Features capture tree, or 2 Work tree when filter.inPlaceTree is enabled).
func (a *App) focusedTreeForFilter(s core.State, targetID string) *tree.Model {
	if a.Modal() == "outline" && (targetID == "" || targetID == outlineTree) {
		tm := a.trees.outlineModel()
		if tm.MatchMode == "" {
			tm.MatchMode = a.matchMode()
		}
		return tm
	}
	switch targetID {
	case projectOutlineTree:
		if pid := a.activeProjectOutlineID(s); pid != "" {
			tm := a.trees.projectModel(pid)
			if tm.MatchMode == "" {
				tm.MatchMode = a.matchMode()
			}
			return tm
		}
	case projectPanesTree, projectLanesTree:
		if tm, ok := a.projectSectionTree(targetID); ok {
			if tm.MatchMode == "" {
				tm.MatchMode = a.matchMode()
			}
			return tm
		}
	case featuresCaptureTree:
		if wid := a.featuresWishlistTaskID(); wid != "" && a.inFeaturesCaptureTree(&s) {
			tm := a.trees.projectModel(wid)
			if tm.MatchMode == "" {
				tm.MatchMode = a.matchMode()
			}
			return tm
		}
	case workTable:
		if a.inWorkTree(s) && a.inPlaceTreeFilterEnabled() {
			tm := a.trees.workModel(a.workKind())
			if tm.MatchMode == "" {
				tm.MatchMode = a.matchMode()
			}
			return tm
		}
	}
	return nil
}

// resolveFilterable returns the traits.Filterable for targetID (or the focused
// component when targetID is empty) across trees, tables, project.nav, and site.toc.
func (a *App) resolveFilterable(s core.State, targetID string) (traits.Filterable, string) {
	if a.Modal() == "outline" {
		return a.focusedTreeForFilter(s, outlineTree), outlineTree
	}
	if a.Modal() != "" {
		return nil, ""
	}
	id := targetID
	if id == "" {
		id = s.ActiveFrame().FocusID
	}
	if id == "toc" || strings.HasPrefix(id, "site.") {
		return tocFilterable{a: a}, "toc"
	}
	if projKey, ok := strings.CutPrefix(id, "project.nav.filter:"); ok && projKey != "" {
		return projectNavFilterable{a: a, projKey: projKey}, id
	}
	if strings.HasPrefix(id, "project.nav") {
		if projKey := a.activeProjectWorkspaceKey(s); projKey != "" {
			return projectNavFilterable{a: a, projKey: projKey}, "project.nav.filter:" + projKey
		}
	}
	if tm := a.focusedTreeForFilter(s, id); tm != nil {
		return tm, id
	}
	if tbl := a.nav.tables[id]; tbl != nil {
		return tbl, id
	}
	switch id {
	case workTable:
		if s.ActivePage == SpaceWork && a.nav.histPos < 0 && a.Store != nil {
			return a.records(workTable, a.workKind()), id
		}
	case railTable:
		if s.ActivePage == SpacePanes && a.Store != nil {
			return a.rail(), id
		}
	}
	return nil, id
}

// activeFilterable returns the traits.Filterable that filter commands act on
// right now (either the in-progress filterTarget or the focused component).
func (a *App) activeFilterable(s core.State) (traits.Filterable, string) {
	if a.nav.filterTarget != "" {
		if f, id := a.resolveFilterable(s, a.nav.filterTarget); f != nil {
			return f, id
		}
	}
	return a.resolveFilterable(s, "")
}

func (a *App) cycleFilterableMode(f traits.Filterable) traits.MatchMode {
	nextMode := traits.CycleMatchMode(f.FilterMode(), traits.FilterConfig{})
	f.Filter(f.FilterQuery(), nextMode)
	return nextMode
}

// filterProjectNavItems recursively filters Project Page left-nav sections and
// child nodes using traits.CompileMultiTerm (keeping a section visible when its
// own label/key matches OR any descendant node matches).
func filterProjectNavItems(items []views.ProjectNavSection, query string, mode traits.MatchMode) []views.ProjectNavSection {
	if strings.TrimSpace(query) == "" {
		return items
	}
	mm, _ := traits.CompileMultiTerm(query, mode, traits.FilterConfig{})
	var out []views.ProjectNavSection
	for _, it := range items {
		visKids := filterProjectNavNodes(it.Children, mm)
		selfText := traits.FormatFilterableText("section", it.Key, "label", it.Label, "group", it.Group, "badge", it.Badge)
		if !mm.Match(selfText) && len(visKids) == 0 {
			continue
		}
		cp := it
		if len(visKids) > 0 {
			cp.Children = visKids
			cp.Expanded = true
		}
		out = append(out, cp)
	}
	return out
}

func filterProjectNavNodes(nodes []views.ProjectNavNode, mm traits.MultiMatcher) []views.ProjectNavNode {
	var out []views.ProjectNavNode
	for _, nd := range nodes {
		visKids := filterProjectNavNodes(nd.Children, mm)
		selfText := traits.FormatFilterableText("section", nd.Section, "label", nd.Label, "badge", nd.Badge, "id", nd.ID)
		if !mm.Match(selfText) && len(visKids) == 0 {
			continue
		}
		cp := nd
		if len(visKids) > 0 {
			cp.Children = visKids
			cp.Expanded = true
		}
		out = append(out, cp)
	}
	return out
}
