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

// queryFilter is the reusable traits.Filterable state for query-driven views
// (TOC, Project Nav, Features Nav, and dynamic ui.page nodes) stored directly
// inside traits.FilterRegistry with zero ad-hoc controller fields.
type queryFilter struct {
	query string
	mode  traits.MatchMode
	def   func() traits.MatchMode
}

var _ traits.Filterable = (*queryFilter)(nil)

func newQueryFilter(def func() traits.MatchMode) *queryFilter {
	return &queryFilter{def: def}
}

func (f *queryFilter) Filter(query string, mode traits.MatchMode) bool {
	f.query = query
	if mode != "" {
		f.mode = mode
	}
	return true
}

func (f *queryFilter) FilterQuery() string { return f.query }

func (f *queryFilter) FilterMode() traits.MatchMode {
	if f.mode != "" {
		return f.mode
	}
	if f.def != nil {
		return f.def()
	}
	return traits.MatchSubstring
}

func (f *queryFilter) FilterError() string {
	_, errMsg := traits.CompileMultiTerm(f.query, f.FilterMode(), traits.FilterConfig{})
	return errMsg
}

func (f *queryFilter) IsFiltered() bool {
	return strings.TrimSpace(f.query) != ""
}

func projectNavFilterID(projKey string) string {
	return "project.nav.filter:" + projKey
}

func (a *App) ensureQueryFilter(id string) *queryFilter {
	reg := a.filterRegistry()
	if reg == nil || id == "" {
		return nil
	}
	if existing, ok := reg.Get(id).(*queryFilter); ok && existing != nil {
		return existing
	}
	qf := newQueryFilter(a.matchMode)
	reg.Register(id, qf)
	return qf
}

func (a *App) filterQuery(id string) string {
	if reg := a.filterRegistry(); reg != nil && id != "" {
		if f := reg.Get(id); f != nil {
			return f.FilterQuery()
		}
	}
	return ""
}

func (a *App) filterMode(id string) traits.MatchMode {
	if reg := a.filterRegistry(); reg != nil && id != "" {
		if f := reg.Get(id); f != nil {
			return f.FilterMode()
		}
	}
	return a.matchMode()
}

func (a *App) clearFilter(id string) {
	if reg := a.filterRegistry(); reg != nil && id != "" {
		reg.ClearFilter(id)
	}
}

// tableFieldFilterable adapts a table.Model to traits.FieldFilterable so field
// display and cycling go through the trait interface instead of ID switches.
type tableFieldFilterable struct {
	a   *App
	id  string
	tbl *table.Model
}

var _ traits.FieldFilterable = tableFieldFilterable{}

func (f tableFieldFilterable) current() *table.Model {
	if f.a != nil && f.a.nav != nil && f.id != "" {
		if live := f.a.nav.tables[f.id]; live != nil {
			return live
		}
	}
	return f.tbl
}

func (f tableFieldFilterable) Filter(query string, mode traits.MatchMode) bool {
	if tbl := f.current(); tbl != nil {
		return tbl.Filter(query, mode)
	}
	return false
}
func (f tableFieldFilterable) FilterQuery() string {
	if tbl := f.current(); tbl != nil {
		return tbl.FilterQuery()
	}
	return ""
}
func (f tableFieldFilterable) FilterMode() traits.MatchMode {
	if tbl := f.current(); tbl != nil {
		return tbl.FilterMode()
	}
	return traits.MatchSubstring
}
func (f tableFieldFilterable) FilterError() string {
	if tbl := f.current(); tbl != nil {
		return tbl.FilterError()
	}
	return ""
}
func (f tableFieldFilterable) IsFiltered() bool {
	if tbl := f.current(); tbl != nil {
		return tbl.IsFiltered()
	}
	return false
}

func (f tableFieldFilterable) FilterFieldName() string {
	tbl := f.current()
	if tbl == nil || tbl.FilterField == views.AnyField {
		return ""
	}
	return tbl.FilterField
}

func (f tableFieldFilterable) CycleFilterField() string {
	tbl := f.current()
	if tbl == nil || f.a == nil {
		return ""
	}
	keys := f.a.tableKeys(f.id, tbl)
	fields := append([]string{views.AnyField}, keys...)
	i := slices.Index(fields, tbl.FilterField)
	tbl.FilterField = fields[(i+1)%len(fields)]
	tbl.Filter(tbl.FilterQuery(), tbl.FilterMode())
	if tbl.FilterField == views.AnyField {
		return ""
	}
	return tbl.FilterField
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

func (a *App) registerWorkTreeFilterable(tm *tree.Model) {
	if a.inPlaceTreeFilterEnabled() {
		a.registerTreeFilterable(workTable, tm)
		return
	}
	a.registerFilterable(workTable, lazyTableFilterable{
		a:       a,
		id:      workTable,
		resolve: func() *table.Model { return a.records(workTable, a.workKind()) },
	})
}

// lazyTableFilterable adapts a lazily built table.Model (used when a user
// explicitly opts out of in-place tree filtering via filter.inPlaceTree=false).
type lazyTableFilterable struct {
	a       *App
	id      string
	resolve func() *table.Model
}

var _ traits.FieldFilterable = lazyTableFilterable{}

func (f lazyTableFilterable) existing() *table.Model {
	if f.a == nil || f.a.nav == nil {
		return nil
	}
	return f.a.nav.tables[f.id]
}

func (f lazyTableFilterable) Filter(query string, mode traits.MatchMode) bool {
	tbl := f.existing()
	if tbl == nil && f.resolve != nil {
		tbl = f.resolve()
	}
	if tbl == nil {
		return false
	}
	return tbl.Filter(query, mode)
}

func (f lazyTableFilterable) FilterQuery() string {
	if tbl := f.existing(); tbl != nil {
		return tbl.FilterQuery()
	}
	return ""
}

func (f lazyTableFilterable) FilterMode() traits.MatchMode {
	if tbl := f.existing(); tbl != nil {
		return tbl.FilterMode()
	}
	if f.a != nil {
		return f.a.matchMode()
	}
	return traits.MatchSubstring
}

func (f lazyTableFilterable) FilterError() string {
	if tbl := f.existing(); tbl != nil {
		return tbl.FilterError()
	}
	return ""
}

func (f lazyTableFilterable) IsFiltered() bool {
	if tbl := f.existing(); tbl != nil {
		return tbl.IsFiltered()
	}
	return false
}

func (f lazyTableFilterable) FilterFieldName() string {
	if tbl := f.existing(); tbl != nil {
		return (tableFieldFilterable{a: f.a, id: f.id, tbl: tbl}).FilterFieldName()
	}
	return ""
}

func (f lazyTableFilterable) CycleFilterField() string {
	tbl := f.existing()
	if tbl == nil && f.resolve != nil {
		tbl = f.resolve()
	}
	if tbl == nil {
		return ""
	}
	return (tableFieldFilterable{a: f.a, id: f.id, tbl: tbl}).CycleFilterField()
}

// initFilters registers the singleton page-owned filter adapters once at startup.
// All dynamic table, tree, project-nav, and ui.page filterables mount themselves
// when their views render or resolve lazily from declarative ViewNode.Filter specs.
func (a *App) initFilters() {
	a.ensureQueryFilter("toc")
	a.ensureQueryFilter(views.FeaturesNavFilterID)
	a.registerFilterable(views.JobsListID, jobsFilterable{a: a})
}

func (a *App) filterableForID(id string) traits.Filterable {
	if a == nil || a.nav == nil || id == "" {
		return nil
	}
	if f := a.filterRegistry().Get(id); f != nil {
		return f
	}
	if tbl := a.nav.tables[id]; tbl != nil {
		a.registerTableFilterable(id, tbl)
		return a.filterRegistry().Get(id)
	}
	return nil
}

func (a *App) filterableForSpec(spec *ui.FilterSpec, id string) traits.Filterable {
	if id == "" && spec != nil {
		id = spec.ID
	}
	if id == "" {
		return nil
	}
	if f := a.filterableForID(id); f != nil {
		return f
	}
	return a.ensureDynamicFilter(id)
}

// resolveNodeFilterSpec walks root.Ancestors(focusID) innermost-first and
// returns the nearest declarative FilterSpec (or TraitFilter node ID).
func resolveNodeFilterSpec(root ui.ViewNode, focusID string) (*ui.FilterSpec, string) {
	if spec, id := ui.ResolveFilterSpec(root, focusID); spec != nil {
		return spec, id
	}
	for _, n := range root.Ancestors(focusID) {
		if n.ID != "" && slices.Contains(n.Traits, traits.TraitFilter) {
			return &ui.FilterSpec{ID: n.ID}, n.ID
		}
	}
	return nil, ""
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

func hasFoldPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

func formatFilterErrorLabel(errMsg string) string {
	if hasFoldPrefix(errMsg, "invalid regex") {
		return errMsg
	}
	return "invalid regex: " + errMsg
}

func filterBarHints(f traits.Filterable) string {
	hints := "    enter keep · esc cancel · ctrl+x clear · tab mode"
	if _, hasFields := f.(traits.FieldFilterable); hasFields {
		hints += " · ctrl+t field"
	}
	return hints
}

func clampToneSpans(spans []ui.ToneSpan, maxRunes int) []ui.ToneSpan {
	var clamped []ui.ToneSpan
	for _, sp := range spans {
		from := max(sp.From, 0)
		to := min(sp.To, maxRunes)
		if from < to {
			clamped = append(clamped, ui.ToneSpan{From: from, To: to, Tone: sp.Tone})
		}
	}
	return clamped
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
	write(activeFilterPrefix(f, spec), "muted")
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
		write("  "+formatFilterErrorLabel(errMsg), "error")
	} else {
		write(filterBarHints(f), "muted")
	}

	clipped := clip(sb.String(), w)
	props := map[string]any{"text": clipped}
	if clamped := clampToneSpans(spans, len([]rune(clipped))); len(clamped) > 0 {
		props["spans"] = clamped
	}
	return ui.ViewNode{
		ID:    "hints",
		Kind:  ui.KindText,
		Box:   layout.Constraints{FixedH: 1},
		Props: props,
	}, true
}

func (a *App) filterLookupRoot(s core.State, root *ui.ViewNode, allowBuild bool) (ui.ViewNode, bool) {
	switch {
	case root != nil:
		return *root, true
	case allowBuild:
		// View(s) is memoized by viewKey(s) and mounts every rendered surface's
		// Filterable when the view rebuilds.
		return a.View(s), true
	case a.viewMemo.memo.ID != "":
		return a.viewMemo.memo, true
	}
	return ui.ViewNode{}, false
}

func (a *App) resolveExplicitFilterTarget(treeRoot ui.ViewNode, hasRoot bool, targetID string) (traits.Filterable, *ui.FilterSpec, string) {
	if hasRoot {
		if owner := findFilterOwner(treeRoot, targetID); owner != nil && owner.Filter != nil {
			spec := *owner.Filter
			if spec.ID == "" {
				spec.ID = targetID
			}
			return a.filterableForSpec(&spec, targetID), &spec, targetID
		}
	}
	if f := a.filterableForID(targetID); f != nil {
		spec := a.nav.filterSpec
		if spec == nil || spec.ID != targetID {
			spec = &ui.FilterSpec{ID: targetID}
		}
		return f, spec, targetID
	}
	if hasRoot {
		if spec, id := resolveNodeFilterSpec(treeRoot, targetID); spec != nil {
			if f := a.filterableForSpec(spec, id); f != nil {
				return f, spec, id
			}
		}
	}
	return nil, nil, targetID
}

func (a *App) resolveFocusedFilterTarget(treeRoot ui.ViewNode, hasRoot bool, focusID string) (traits.Filterable, *ui.FilterSpec, string) {
	if !hasRoot {
		if f := a.filterableForID(focusID); f != nil {
			return f, &ui.FilterSpec{ID: focusID}, focusID
		}
		return nil, nil, focusID
	}
	for probe := focusID; probe != ""; {
		if spec, id := resolveNodeFilterSpec(treeRoot, probe); spec != nil {
			if f := a.filterableForSpec(spec, id); f != nil {
				return f, spec, id
			}
		}
		// If a kept filter hid the previously focused child item (e.g.
		// "project.nav.home" filtered out by "/lanes"), walk dotted parent IDs
		// to find the surviving container's declarative FilterSpec.
		dot := strings.LastIndexByte(probe, '.')
		if dot <= 0 {
			break
		}
		probe = probe[:dot]
	}
	return nil, nil, focusID
}

// resolveFilterTarget resolves the nearest filterable surface by walking
// declarative ViewNode.Filter (or TraitFilter) up root.Ancestors(focusID) and
// resolving its ID via mounted components or a generic dynamic queryFilter.
// Works uniformly across pages, trees, tables, dynamic ui.pages, and modals.
func (a *App) resolveFilterTarget(s core.State, root *ui.ViewNode, targetID string, allowBuild bool) (traits.Filterable, *ui.FilterSpec, string) {
	if a.filterRegistry() == nil {
		return nil, nil, ""
	}
	treeRoot, hasRoot := a.filterLookupRoot(s, root, allowBuild)
	if targetID != "" {
		return a.resolveExplicitFilterTarget(treeRoot, hasRoot, targetID)
	}
	return a.resolveFocusedFilterTarget(treeRoot, hasRoot, s.ActiveFrame().FocusID)
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

func (a *App) isSelfContainedFilterWidget(filterID string) bool {
	if filterID == "" {
		return false
	}
	if a.nav != nil && a.nav.tables[filterID] != nil {
		return true
	}
	if a.proj != nil && a.proj.trees[filterID] != nil {
		return true
	}
	return filterID == outlineTree || filterID == projectOutlineTree || filterID == workTable
}

// repairFilterFocus keeps focus inside the container declaring filterID when
// filtering hides the previously focused child item, preventing generic focus
// repair from ejecting focus to an unrelated earlier widget on the page.
func (a *App) repairFilterFocus(s core.State, filterID string) core.State {
	if filterID == "" {
		return s
	}
	focusID := s.ActiveFrame().FocusID
	if focusID == filterID && a.isSelfContainedFilterWidget(filterID) {
		return s
	}
	root := a.View(s)
	if focusID != "" {
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
