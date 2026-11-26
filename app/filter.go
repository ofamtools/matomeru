package app

import (
	"slices"
	"strings"

	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/tree"
)

func (a *App) inPlaceTreeFilterEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetFilterInPlaceTree, true)
}

func (a *App) filterBarBottomLeftEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetFilterBarBottomLeft, true)
}

// tableFieldFilterable adapts a table.Model to traits.FieldFilterable so field
// display and cycling go through the trait interface instead of ID switches.
type tableFieldFilterable struct {
	a   *App
	id  string
	tbl *table.Model
}

var _ traits.FieldFilterable = tableFieldFilterable{}

func (f tableFieldFilterable) Filter(query string, mode traits.MatchMode) bool {
	return f.tbl.Filter(query, mode)
}
func (f tableFieldFilterable) FilterQuery() string          { return f.tbl.FilterQuery() }
func (f tableFieldFilterable) FilterMode() traits.MatchMode { return f.tbl.FilterMode() }
func (f tableFieldFilterable) FilterError() string          { return f.tbl.FilterError() }
func (f tableFieldFilterable) IsFiltered() bool             { return f.tbl.IsFiltered() }

func (f tableFieldFilterable) FilterFieldName() string {
	if f.tbl == nil || f.tbl.FilterField == views.AnyField {
		return ""
	}
	return f.tbl.FilterField
}

func (f tableFieldFilterable) CycleFilterField() string {
	if f.tbl == nil || f.a == nil {
		return ""
	}
	keys := f.a.tableKeys(f.id, f.tbl)
	fields := append([]string{views.AnyField}, keys...)
	i := slices.Index(fields, f.tbl.FilterField)
	f.tbl.FilterField = fields[(i+1)%len(fields)]
	f.tbl.Filter(f.tbl.FilterQuery(), f.tbl.FilterMode())
	if f.tbl.FilterField == views.AnyField {
		return ""
	}
	return f.tbl.FilterField
}

func (a *App) filterRegistry() *traits.FilterRegistry {
	if a == nil || a.nav == nil {
		return nil
	}
	if a.nav.filters == nil {
		a.nav.filters = traits.NewFilterRegistry()
	}
	return a.nav.filters
}

func (a *App) registerFilterable(id string, f traits.Filterable) {
	if reg := a.filterRegistry(); reg != nil && id != "" {
		reg.Register(id, f)
	}
}

func (a *App) registerTreeFilterable(id string, tm *tree.Model) {
	if tm == nil || id == "" {
		return
	}
	if tm.MatchMode == "" {
		tm.MatchMode = a.matchMode()
	}
	a.registerFilterable(id, tm)
}

func (a *App) registerTableFilterable(id string, tbl *table.Model) {
	if tbl == nil || id == "" {
		return
	}
	a.registerFilterable(id, tableFieldFilterable{a: a, id: id, tbl: tbl})
}

// syncFilterRegistry registers all live filterable surfaces currently known to
// the app into a.nav.filters so declarative ViewNode.Filter IDs resolve
// without hardcoded ID switches.
func (a *App) syncFilterRegistry(s core.State) {
	if a == nil || a.nav == nil {
		return
	}
	a.registerFilterable("toc", tocFilterable{a: a})
	if s.ActivePage == SpaceJobs && jobsTabs[a.jobs.tab] != views.JobsTabAll {
		a.registerFilterable(views.JobsListID, jobsFilterable{a: a})
	} else {
		a.filterRegistry().Unregister(views.JobsListID)
	}
	for id, tbl := range a.nav.tables {
		if tbl != nil {
			a.registerTableFilterable(id, tbl)
		}
	}
	if a.Store != nil {
		if s.ActivePage == SpaceWork && a.nav.histPos < 0 {
			if a.inWorkTree(s) && a.inPlaceTreeFilterEnabled() {
				a.registerTreeFilterable(workTable, a.trees.workModel(a.workKind()))
			} else if tbl := a.records(workTable, a.workKind()); tbl != nil {
				a.registerTableFilterable(workTable, tbl)
			}
		}
		if s.ActivePage == SpacePanes {
			if tbl := a.rail(); tbl != nil {
				a.registerTableFilterable(railTable, tbl)
			}
		}
	}
	if a.Modal() == "outline" {
		a.registerTreeFilterable(outlineTree, a.trees.outlineModel())
	}
	if projKey := a.activeProjectWorkspaceKey(s); projKey != "" {
		a.registerFilterable("project.nav.filter:"+projKey, projectNavFilterable{a: a, projKey: projKey})
	}
	if pid := a.activeProjectOutlineID(s); pid != "" {
		a.registerTreeFilterable(projectOutlineTree, a.trees.projectModel(pid))
	}
	for _, tid := range []string{projectPanesTree, projectLanesTree} {
		if tm, ok := a.projectSectionTree(tid); ok {
			a.registerTreeFilterable(tid, tm)
		}
	}
	if wid := a.featuresWishlistTaskID(); wid != "" && a.inFeaturesCaptureTree(&s) {
		a.registerTreeFilterable(featuresCaptureTree, a.trees.projectModel(wid))
	}
}

// activeFilterPrefix derives the filter bar prefix purely from declarative
// ViewNode.Filter data and traits.FieldFilterable — zero ID switches.
func activeFilterPrefix(f traits.Filterable, spec *ui.FilterSpec) string {
	if ff, ok := f.(traits.FieldFilterable); ok {
		if field := ff.FilterFieldName(); field != "" {
			return field + " "
		}
	}
	if spec != nil && spec.Prefix != "" {
		return spec.Prefix
	}
	return ""
}

// filterBarNode builds the fixed bottom-left Filter Bar anchored at column 0
// in the bottom footer row while ModeFilter is active.
func (a *App) filterBarNode(s core.State, root ui.ViewNode, w int) (ui.ViewNode, bool) {
	if !a.filterBarBottomLeftEnabled() || a.Engine.Mode(s) != ModeFilter {
		return ui.ViewNode{}, false
	}
	f, spec, _ := a.activeFilterTarget(s, &root)
	if f == nil {
		return ui.ViewNode{}, false
	}

	var (
		sb    strings.Builder
		runes int
		spans []ui.ToneSpan
	)
	write := func(seg, tone string) {
		if seg == "" {
			return
		}
		n := len([]rune(seg))
		if tone != "" {
			spans = append(spans, ui.ToneSpan{From: runes, To: runes + n, Tone: tone})
		}
		sb.WriteString(seg)
		runes += n
	}

	write(" ", "")
	if prefix := activeFilterPrefix(f, spec); prefix != "" {
		write(prefix, "muted")
	}
	write("/", "accent")

	q := f.FilterQuery()
	a.nav.filterLine.Sync(q)
	rs := []rune(q)
	ci := min(max(a.nav.filterLine.Cursor, 0), len(rs))
	shownQuery := string(rs[:ci]) + "█" + string(rs[ci:])

	errMsg := f.FilterError()
	queryTone := ""
	if errMsg != "" {
		queryTone = "error"
	}
	write(shownQuery, queryTone)
	write("  ["+string(f.FilterMode())+"]", "accent")

	if errMsg != "" {
		label := errMsg
		if !strings.Contains(strings.ToLower(label), "invalid regex") {
			label = "invalid regex: " + label
		}
		write("  "+label, "error")
	} else {
		hints := "    enter keep · esc cancel · ctrl+x clear · tab mode"
		if _, hasFields := f.(traits.FieldFilterable); hasFields {
			hints += " · ctrl+t field"
		}
		write(hints, "muted")
	}

	clipped := clip(sb.String(), w)
	maxRunes := len([]rune(clipped))
	var clamped []ui.ToneSpan
	for _, sp := range spans {
		from := max(sp.From, 0)
		to := min(sp.To, maxRunes)
		if from < to {
			clamped = append(clamped, ui.ToneSpan{From: from, To: to, Tone: sp.Tone})
		}
	}

	props := map[string]any{"text": clipped}
	if len(clamped) > 0 {
		props["spans"] = clamped
	}
	return ui.ViewNode{
		ID:    "hints",
		Kind:  ui.KindText,
		Box:   layout.Constraints{FixedH: 1},
		Props: props,
	}, true
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

// projectNavFilterable adapts a Project Page's left-nav filter into traits.Filterable
// with per-project query and per-project match mode.
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
		if f.a.nav.projectNavMatchModes == nil {
			f.a.nav.projectNavMatchModes = map[string]traits.MatchMode{}
		}
		f.a.nav.projectNavMatchModes[f.projKey] = mode
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
	if f.a.nav != nil && f.a.nav.projectNavMatchModes != nil {
		if m := f.a.nav.projectNavMatchModes[f.projKey]; m != "" {
			return m
		}
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

// resolveFilterTarget resolves the nearest filterable surface by walking
// declarative ViewNode.Filter up root.Ancestors(focusID) and looking up its ID
// in a.nav.filters. Works uniformly across pages, trees, tables, and modals.
func (a *App) resolveFilterTarget(s core.State, root *ui.ViewNode, targetID string, allowBuild bool) (traits.Filterable, *ui.FilterSpec, string) {
	a.syncFilterRegistry(s)
	reg := a.filterRegistry()
	if reg == nil {
		return nil, nil, ""
	}
	if targetID != "" {
		if f := reg.Get(targetID); f != nil {
			spec := a.nav.filterSpec
			if spec == nil || spec.ID != targetID {
				spec = &ui.FilterSpec{ID: targetID}
			}
			return f, spec, targetID
		}
	}
	focusID := targetID
	if focusID == "" {
		focusID = s.ActiveFrame().FocusID
	}
	var treeRoot ui.ViewNode
	hasRoot := false
	if root != nil {
		treeRoot, hasRoot = *root, true
	} else if a.viewMemo.memo.ID != "" && (focusID == "" || a.viewMemo.memo.Find(focusID) != nil) {
		treeRoot, hasRoot = a.viewMemo.memo, true
	} else if allowBuild {
		treeRoot, hasRoot = a.View(s), true
	}
	if hasRoot {
		if spec, id := ui.ResolveFilterSpec(treeRoot, focusID); spec != nil {
			if f := reg.Get(id); f != nil {
				return f, spec, id
			}
		}
		// If a kept filter hid the previously focused child item (e.g.
		// "project.nav.home" filtered out by "/lanes"), walk dotted parent IDs
		// to find the surviving container's declarative FilterSpec.
		for parentID := focusID; parentID != ""; {
			dot := strings.LastIndexByte(parentID, '.')
			if dot <= 0 {
				break
			}
			parentID = parentID[:dot]
			if spec, id := ui.ResolveFilterSpec(treeRoot, parentID); spec != nil {
				if f := reg.Get(id); f != nil {
					return f, spec, id
				}
			}
		}
	}
	if focusID != "" {
		if f := reg.Get(focusID); f != nil {
			return f, &ui.FilterSpec{ID: focusID}, focusID
		}
	}
	return nil, nil, focusID
}

// activeFilterTarget returns the Filterable + FilterSpec currently targeted by
// filter commands (the active ModeFilter target, or the focused node's nearest
// declarative FilterSpec).
func (a *App) activeFilterTarget(s core.State, root *ui.ViewNode) (traits.Filterable, *ui.FilterSpec, string) {
	if a.nav != nil && a.nav.filterTarget != "" {
		if f, spec, id := a.resolveFilterTarget(s, root, a.nav.filterTarget, root == nil); f != nil {
			return f, spec, id
		}
	}
	return a.resolveFilterTarget(s, root, "", root == nil)
}

// activeFilterableNoBuild resolves the active Filterable without triggering
// View(s) rebuilds (safe for use inside viewKey).
func (a *App) activeFilterableNoBuild(s core.State) (traits.Filterable, string) {
	if a.nav != nil && a.nav.filterTarget != "" {
		if f, _, id := a.resolveFilterTarget(s, nil, a.nav.filterTarget, false); f != nil {
			return f, id
		}
	}
	f, _, id := a.resolveFilterTarget(s, nil, "", false)
	return f, id
}

func findFilterOwner(n ui.ViewNode, filterID string) *ui.ViewNode {
	if n.Filter != nil {
		id := n.Filter.ID
		if id == "" {
			id = n.ID
		}
		if id == filterID {
			cp := n
			return &cp
		}
	}
	for _, ch := range n.Children {
		if found := findFilterOwner(ch, filterID); found != nil {
			return found
		}
	}
	return nil
}

func firstFocusableID(n ui.ViewNode) string {
	if n.Focusable && n.ID != "" {
		return n.ID
	}
	for _, ch := range n.Children {
		if id := firstFocusableID(ch); id != "" {
			return id
		}
	}
	return ""
}

// repairFilterFocus keeps focus inside the container declaring filterID when
// filtering hides the previously focused child item, preventing generic focus
// repair from ejecting focus to an unrelated earlier widget on the page.
func (a *App) repairFilterFocus(s core.State, filterID string) core.State {
	if filterID == "" {
		return s
	}
	root := a.View(s)
	if focusID := s.ActiveFrame().FocusID; focusID != "" {
		if spec, id := ui.ResolveFilterSpec(root, focusID); spec != nil && id == filterID {
			return s
		}
	}
	if container := findFilterOwner(root, filterID); container != nil {
		if first := firstFocusableID(*container); first != "" {
			return withFocus(s, first)
		}
	}
	return s
}

// activeFilterable returns the traits.Filterable that filter commands act on.
func (a *App) activeFilterable(s core.State) (traits.Filterable, string) {
	f, _, id := a.activeFilterTarget(s, nil)
	return f, id
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
