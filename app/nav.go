package app

import (
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/commandline"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/table"
)

type historyEntry struct {
	ref   core.Ref
	focus string // focus to restore when coming back to this page
}

type jumpEntry struct {
	page       string
	focus      string
	ref        core.Ref // non-empty when showing a Work Show page
	histPos    int      // -1 when not on a Work Show page
	browseKind string
}

const maxJumpHistory = 100

// NavController owns tables, presets, archived visibility, browsing state,
// Work history, global jump history, focus tracking, filtering, command line,
// registers, and macros.
type NavController struct {
	tables               map[string]*table.Model
	recCacheKeys         map[string]string
	presets              *table.PresetStore
	activePreset         map[string]string
	showCompact          bool
	showArchived         bool
	browseKind           string
	history              []historyEntry
	histPos              int
	showScrollRef        core.Ref
	showRelatedOff       *int // explicit offset while the user scrolls show.related; nil: the renderer follows focus
	showPropsOff         *int // explicit offset while the user scrolls show.props.scroll; nil: the renderer follows focus
	jumps                []jumpEntry
	jumpPos              int
	jumping              bool
	indexFocus           string
	lastFocus            map[string]string
	focusRegion          string
	tocFilter            string
	tocMatchMode         traits.MatchMode
	projectNavFilters    map[string]string
	projectNavMatchModes map[string]traits.MatchMode
	filters              *traits.FilterRegistry
	filterModes          traits.ModeStack
	filterTarget         string
	filterSpec           *ui.FilterSpec
	filterLine           input.Line
	cmdline              *commandline.Model
	lastFDSearch         map[string]any
	Registers            core.Registers
	Macros               core.MacroRecorder
	replaying            int
}

func newNavController() *NavController {
	return &NavController{
		tables:               map[string]*table.Model{},
		recCacheKeys:         map[string]string{},
		activePreset:         map[string]string{},
		lastFocus:            map[string]string{},
		projectNavMatchModes: map[string]traits.MatchMode{},
		filters:              traits.NewFilterRegistry(),
		histPos:              -1,
		jumpPos:              -1,
		indexFocus:           workTable,
	}
}

// --- focus ----------------------------------------------------------------------

// regionRoots are the nodes whose focusable descendants form one column: h
// and l move between regions, j and k walk every focusable in drawing order.
var regionRoots = []string{"panes.rail", "projects.dir", "projects.preview", "project.nav", "project.content", "project.overview", "features.nav", "features.content", "features.capture", "site.toc", "site.content", "site.side.panel", showHeader, "show.props", "show.related",
	SpaceWork, SpaceRuns, SpaceExtensions, "runs.detail", "extensions.detail"}

type fnode struct{ id, region string }

func collectFocus(n *ui.ViewNode, region string, out *[]fnode) {
	if n.Kind == ui.KindOverlay {
		return
	}
	if slices.Contains(regionRoots, n.ID) {
		region = n.ID
	}
	if n.Focusable && n.ID != "" {
		*out = append(*out, fnode{n.ID, region})
	}
	for i := range n.Children {
		collectFocus(&n.Children[i], region, out)
	}
}

func (a *App) focusables(s core.State) []fnode {
	root := a.View(s)
	var out []fnode
	collectFocus(&root, "", &out)
	return out
}

func (a *App) defaultFocus(s core.State) string {
	if s.ActivePage == SpaceWork && a.nav.histPos >= 0 {
		return showHeader
	}
	if s.ActivePage == SpaceProjects {
		return projectsDirTable
	}
	if s.ActivePage == SpaceFeatures {
		return views.FeaturesNavPrefix + a.featuresCtrl().activeItemKey()
	}
	if isProjectTab(s.ActivePage) {
		pid := projectIDFromTab(s.ActivePage)
		return projectNavPrefix + a.projectCtrl().activeSection(pid)
	}
	for _, sp := range spaces {
		if sp.id == s.ActivePage {
			return sp.table
		}
	}
	return ""
}

// settle keeps the focus on a node that exists after a command changed what
// is shown, and remembers each region's last focus.
func (a *App) settle(s core.State) core.State {
	if len(s.Modals) > 0 {
		return s
	}
	if !a.isValidTopTab(s.ActivePage) {
		s.ActivePage = SpacePanes
		a.viewMemo.gen++
	}
	nodes := a.focusables(s)
	cur := s.Pages[s.ActivePage].FocusID
	if i := slices.IndexFunc(nodes, func(n fnode) bool { return n.id == cur }); i >= 0 {
		a.nav.focusRegion, a.nav.lastFocus[nodes[i].region] = nodes[i].region, cur
		return s
	}
	pick := ""
	for _, n := range nodes {
		if n.region == a.nav.focusRegion {
			pick = n.id
			break
		}
	}
	if def := a.defaultFocus(s); pick == "" && slices.ContainsFunc(nodes, func(n fnode) bool { return n.id == def }) {
		pick = def
	}
	if pick == "" && len(nodes) > 0 {
		pick = nodes[0].id
	}
	a.viewMemo.gen++
	return withPage(s, s.ActivePage, pick, "")
}

// step moves the focus by delta nodes in drawing order. Entering a list from
// above lands on its first row, from below on its last.
func (a *App) step(s core.State, delta int) core.State {
	nodes := a.focusables(s)
	if len(nodes) == 0 {
		return s
	}
	cur := slices.IndexFunc(nodes, func(n fnode) bool { return n.id == s.ActiveFrame().FocusID })
	to := min(max(cur+delta, 0), len(nodes)-1)
	if cur < 0 {
		to = 0
	}
	if to == cur {
		return s
	}
	if tbl := a.nav.tables[nodes[to].id]; tbl != nil {
		tbl.SelectedIdx, tbl.Offset = 0, 0
		if delta < 0 {
			tbl.SelectedIdx = len(tbl.Items()) - 1
		}
		tbl.Focused()
	}
	return withFocus(s, nodes[to].id)
}

// column moves the focus to the next or previous region.
func (a *App) column(s core.State, delta int) core.State {
	nodes := a.focusables(s)
	var regions []string
	for _, n := range nodes {
		if !slices.Contains(regions, n.region) {
			regions = append(regions, n.region)
		}
	}
	cur := slices.IndexFunc(nodes, func(n fnode) bool { return n.id == s.ActiveFrame().FocusID })
	if cur < 0 || len(regions) < 2 {
		return s
	}
	at := slices.Index(regions, nodes[cur].region)
	to := min(max(at+delta, 0), len(regions)-1)
	if to == at {
		return s
	}
	// When stepping right from a live-previewed Project Nav pane node into the
	// previewed child pane's TOC or Screen, commit it as the open child pane so
	// focus stays anchored and enter/i attach cleanly.
	if nodes[cur].region == "project.nav" && (regions[to] == "site.toc" || regions[to] == "site.content") &&
		strings.HasPrefix(nodes[cur].id, projectNavNodePrefix) && a.projectChildPaneViewEnabled() {
		if projKey := a.activeProjectWorkspaceKey(s); projKey != "" {
			pc := a.projectCtrl()
			if target, ok := pc.navTarget(projKey, nodes[cur].id); ok && target.Section == "panes" && target.Ref != nil && target.Ref.Kind == data.KindPane {
				if pc.childPaneByProject == nil {
					pc.childPaneByProject = map[string]string{}
				}
				pc.childPaneByProject[projKey] = target.Ref.ID
				if pc.childPaneReturnFocus == nil {
					pc.childPaneReturnFocus = map[string]string{}
				}
				pc.childPaneReturnFocus[projKey] = nodes[cur].id
				pc.setActiveNavItem(projKey, nil)
				a.viewMemo.gen++
			}
		}
	}
	// When stepping right from a live-previewed middle-rail canvas into site.content,
	// commit the previewed canvas as the pane's active page so content stays aligned.
	if nodes[cur].region == "site.toc" && regions[to] == "site.content" &&
		(a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetTOCLivePreview, true)) {
		curID := nodes[cur].id
		if pageID, ok := strings.CutPrefix(curID, tocPrefix); ok && pageID != "" &&
			!strings.HasPrefix(curID, views.TOCGroupPrefix) && !strings.HasPrefix(curID, views.LaneTOCPrefix) {
			if pane, hasPane := a.activePane(s); hasPane && pane != nil {
				a.panes.activePage[pane.ID] = pageID
			}
		}
	}
	target := a.nav.lastFocus[regions[to]]
	if !slices.ContainsFunc(nodes, func(n fnode) bool { return n.id == target && n.region == regions[to] }) {
		idx := slices.IndexFunc(nodes, func(n fnode) bool {
			return n.region == regions[to] && (regions[to] != "site.content" || n.id != views.PageScrollID)
		})
		if idx < 0 {
			idx = slices.IndexFunc(nodes, func(n fnode) bool { return n.region == regions[to] })
		}
		target = nodes[idx].id
	}
	return withFocus(s, target)
}

// edge moves the focus to the first or last node of its region.
func (a *App) edge(s core.State, last bool) core.State {
	nodes := a.focusables(s)
	cur := slices.IndexFunc(nodes, func(n fnode) bool { return n.id == s.ActiveFrame().FocusID })
	if cur < 0 {
		return s
	}
	target := ""
	for _, n := range nodes {
		if n.region == nodes[cur].region && (target == "" || last) {
			target = n.id
		}
	}
	return withFocus(s, target)
}

func (a *App) edgeFocusSpillEnabled() bool {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return false
	}
	return a.cfg.Settings.Bool(SetEdgeFocusSpill, false)
}

func samePaneRegion(a, b string) bool {
	if a == b {
		return true
	}
	// On Work Show pages, showHeader / show.props / show.related form one vertical document.
	isShow := func(r string) bool {
		return r == showHeader || r == "show.props" || r == "show.related"
	}
	return isShow(a) && isShow(b)
}

func (a *App) stepBounded(s core.State, delta int, allowSpill bool) core.State {
	if allowSpill {
		return a.step(s, delta)
	}
	nodes := a.focusables(s)
	if len(nodes) == 0 {
		return s
	}
	cur := slices.IndexFunc(nodes, func(n fnode) bool { return n.id == s.ActiveFrame().FocusID })
	if cur < 0 {
		return a.step(s, delta)
	}
	curRegion := nodes[cur].region
	var scoped []fnode
	curScoped := -1
	for _, n := range nodes {
		if samePaneRegion(n.region, curRegion) {
			if n.id == nodes[cur].id {
				curScoped = len(scoped)
			}
			scoped = append(scoped, n)
		}
	}
	if len(scoped) == 0 || curScoped < 0 {
		return s
	}
	to := min(max(curScoped+delta, 0), len(scoped)-1)
	if to == curScoped {
		return s
	}
	if tbl := a.nav.tables[scoped[to].id]; tbl != nil {
		tbl.SelectedIdx, tbl.Offset = 0, 0
		if delta < 0 {
			tbl.SelectedIdx = len(tbl.Items()) - 1
		}
		tbl.Focused()
	}
	return withFocus(s, scoped[to].id)
}

func (a *App) activeShowRef(s core.State) (core.Ref, bool) {
	if s.ActivePage == SpaceWork {
		return a.Showing()
	}
	if isProjectTab(s.ActivePage) {
		focus := s.ActiveFrame().FocusID
		if focus == showHeader || strings.HasPrefix(focus, "show.") {
			if projKey := a.activeProjectWorkspaceKey(s); projKey != "" {
				if locked := a.projectCtrl().activeNavItem(projKey); locked != nil && locked.Ref != nil {
					return *locked.Ref, true
				}
			}
		}
	}
	return core.Ref{}, false
}

func (a *App) resetShowScroll(ref core.Ref) {
	if a.nav == nil {
		return
	}
	if cleared := a.clearShowScroll(); cleared || a.nav.showScrollRef != ref {
		a.nav.showScrollRef = ref
		a.viewMemo.gen++
	}
}

func (a *App) showScrollMotion(s core.State, c core.Command) (core.StepResult, bool) {
	ref, ok := a.activeShowRef(s)
	if !ok {
		return core.StepResult{}, false
	}
	focus := s.ActiveFrame().FocusID
	inProps := focus == "show.props" || focus == "show.props.scroll" || strings.HasPrefix(focus, "show.field.")
	scrollID := "show.related"
	if inProps {
		scrollID = "show.props.scroll"
	}
	slot := a.showScrollSlot(scrollID)
	if a.nav.showScrollRef != ref {
		a.nav.showScrollRef = ref
		a.clearShowScroll()
	}
	info, hasInfo := s.Layout.Scrolls[scrollID]
	explicitScroll := c.ID == "motion.scroll.down" || c.ID == "motion.scroll.up"
	pageScroll := c.ID == "motion.half.down" || c.ID == "motion.half.up" || c.ID == "motion.page.down" || c.ID == "motion.page.up"
	if !explicitScroll {
		if !pageScroll {
			return core.StepResult{}, false
		}
		if (!inProps && focus != showHeader) || !hasInfo || info.MaxOff <= 0 {
			return core.StepResult{}, false
		}
	}
	// Step from the explicit offset while scrolling, else from where the
	// renderer last drew the column.
	cur := 0
	if *slot != nil {
		cur = **slot
	} else if hasInfo {
		cur = info.Offset
	}
	next, _, _ := traits.ScrollStep(scrollStateFromInfo(cur, info, hasInfo), a.scrollConfig(), c)
	if *slot == nil || **slot != next.OffsetY {
		off := next.OffsetY
		*slot = &off
		a.viewMemo.gen++
	}
	return core.Next(s), true
}

func (a *App) motion(s core.State, c core.Command) core.StepResult {
	if a.Modal() != "" {
		return a.handleModal(s, c)
	}
	if a.Engine.Mode(s) == ModePage {
		return a.pageScrollMotion(s, c)
	}
	spill := a.edgeFocusSpillEnabled()
	focus := s.ActiveFrame().FocusID
	if focus == workTable && a.inWorkTree(s) && c.ID != "motion.left" && c.ID != "motion.right" {
		a.workTreeMove(c)
		a.syncVisualStatus(s)
		return core.Next(s)
	}
	if a.inProjectOutlineTree(&s) && c.ID != "motion.left" && c.ID != "motion.right" {
		a.projectOutlineTreeMove(s, c)
		a.syncVisualStatus(s)
		return core.Next(s)
	}
	if a.inFeaturesCaptureTree(&s) && c.ID != "motion.left" && c.ID != "motion.right" {
		a.featuresCaptureTreeMove(s, c)
		a.syncVisualStatus(s)
		return core.Next(s)
	}
	if c.ID != "motion.left" && c.ID != "motion.right" && a.handleProjectTreeMotion(s, c) {
		a.syncVisualStatus(s)
		return core.Next(s)
	}
	if tbl := a.nav.tables[focus]; tbl != nil && c.ID != "motion.left" && c.ID != "motion.right" {
		before := tbl.SelectedIdx
		tbl.HandleCommand(c)
		if tbl.SelectedIdx != before || (c.ID != "motion.down" && c.ID != "motion.up") || !spill {
			a.syncVisualStatus(s)
			return core.Next(s)
		}
		// At the list's edge with nav.edgeFocusSpill=true, spill into the next thing drawn.
	}
	if res, handled := a.showScrollMotion(s, c); handled {
		return res
	}
	switch c.ID {
	case "motion.down":
		s = a.stepBounded(s, c.Times(), spill)
	case "motion.up":
		s = a.stepBounded(s, -c.Times(), spill)
	case "motion.half.down", "motion.block.next":
		s = a.stepBounded(s, 5*c.Times(), spill)
	case "motion.half.up", "motion.block.prev":
		s = a.stepBounded(s, -5*c.Times(), spill)
	case "motion.page.down":
		s = a.stepBounded(s, 10*c.Times(), spill)
	case "motion.page.up":
		s = a.stepBounded(s, -10*c.Times(), spill)
	case "motion.right":
		if next, handled := a.smartGroupHL(s, 1); handled {
			return core.Next(next)
		}
		s = a.column(s, 1)
	case "motion.left":
		if next, handled := a.smartGroupHL(s, -1); handled {
			return core.Next(next)
		}
		s = a.column(s, -1)
	case "motion.top":
		s = a.edge(s, false)
	case "motion.bottom":
		s = a.edge(s, true)
	}
	// A focus move ends explicit Show scrolling: the renderer continues from
	// the drawn offset and reveals the new focus with ui.scrolloff padding.
	if s.ActiveFrame().FocusID != focus && a.nav != nil && a.clearShowScroll() {
		a.viewMemo.gen++
	}
	return core.Next(s)
}

// --- spaces and history -----------------------------------------------------------

func (a *App) dropModals(s core.State) core.State {
	if a.modals != nil {
		s = a.modals.DropAll(s)
	}
	a.panes.hints = nil
	a.trees.clearEdit()
	return s
}

func sameJumpLocation(a, b jumpEntry) bool {
	if a.page != b.page || a.ref != b.ref {
		return false
	}
	if a.page == SpaceWork && a.ref.ID == "" {
		return a.browseKind == b.browseKind
	}
	return true
}

func (a *App) currentJump(s core.State) jumpEntry {
	page := s.ActivePage
	focus := s.Pages[page].FocusID
	var ref core.Ref
	histPos := -1
	if page == SpaceWork {
		if r, ok := a.Showing(); ok {
			ref = r
			histPos = a.nav.histPos
		}
	}
	return jumpEntry{
		page:       page,
		focus:      focus,
		ref:        ref,
		histPos:    histPos,
		browseKind: a.nav.browseKind,
	}
}

func (a *App) recordJump(from, to jumpEntry) {
	if a.nav == nil || a.nav.jumping || sameJumpLocation(from, to) {
		return
	}
	if len(a.nav.jumps) == 0 || a.nav.jumpPos < 0 || a.nav.jumpPos >= len(a.nav.jumps) {
		a.nav.jumps = []jumpEntry{from, to}
		a.nav.jumpPos = 1
		return
	}
	a.nav.jumps[a.nav.jumpPos].focus = from.focus
	a.nav.jumps = append(a.nav.jumps[:a.nav.jumpPos+1], to)
	if len(a.nav.jumps) > maxJumpHistory {
		drop := len(a.nav.jumps) - maxJumpHistory
		a.nav.jumps = slices.Clone(a.nav.jumps[drop:])
	}
	a.nav.jumpPos = len(a.nav.jumps) - 1
}

func (a *App) openSpace(s core.State, id string) core.State {
	switch {
	case slices.ContainsFunc(spaces, func(sp spaceDef) bool { return sp.id == id }):
	case id == SpaceProjects:
		if !a.projectsTopNavTabEnabled() {
			a.say("Projects tab is disabled (projects.topNavTab.enabled=false)")
			return s
		}
	case id == SpaceFeatures:
		if !a.featuresPageActive() {
			a.say("Features page is inactive (requires local Matomeru checkout or selfHosted.featuresPage.enabled=on)")
			return s
		}
	case isProjectTab(id):
		if !a.projectsDynamicTabsEnabled() {
			a.say("Project tabs are disabled (projects.dynamicTabs.enabled=false)")
			return s
		}
		pid := projectIDFromTab(id)
		if a.Store == nil {
			a.say("no project %q", pid)
			return s
		}
		if rec, err := a.Store.Get(core.Ref{Kind: data.KindTask, ID: pid}); err != nil || rec == nil {
			a.say("project %q not found", pid)
			return s
		}
		a.ensureProjectTabOpen(pid)
	default:
		a.say("no space %q", id)
		return s
	}
	from := a.currentJump(s)
	s = a.dropModals(s)
	s.ActivePage = id
	if isProjectTab(id) && s.Pages[id].ID == "" {
		pid := projectIDFromTab(id)
		s = withPage(s, id, projectNavPrefix+a.projectCtrl().activeSection(pid), ModeNormal)
	}
	if id == SpaceFeatures && s.Pages[id].ID == "" {
		s = withPage(s, id, views.FeaturesNavPrefix+a.featuresCtrl().activeItemKey(), ModeNormal)
	}
	a.recordJump(from, a.currentJump(s))
	return s
}

func (a *App) cycleSpace(s core.State, delta int) core.State {
	tabs := a.topTabs()
	if len(tabs) == 0 {
		return s
	}
	i := slices.IndexFunc(tabs, func(tb topTabDef) bool { return tb.id == s.ActivePage })
	if i < 0 {
		i = 0
	}
	n := len(tabs)
	return a.openSpace(s, tabs[((i+delta)%n+n)%n].id)
}

// leave remembers the Work space's focus on the page it is leaving.
func (a *App) leave(s core.State) {
	focus := s.Pages[SpaceWork].FocusID
	if a.nav.histPos >= 0 && a.nav.histPos < len(a.nav.history) {
		a.nav.history[a.nav.histPos].focus = focus
	} else {
		a.nav.indexFocus = focus
	}
}

func (a *App) arrive(s core.State) core.State {
	focus := a.nav.indexFocus
	if a.nav.histPos >= 0 {
		focus = a.nav.history[a.nav.histPos].focus
	}
	return withPage(s, SpaceWork, focus, ModeNormal)
}

// show pushes a record's Show page onto the Work space's history, as a
// browser does: what was ahead of the current page is dropped.
func (a *App) show(s core.State, ref core.Ref) core.State {
	_ = a.ensureShadow(ref)
	if _, err := a.Store.Get(ref); err != nil {
		a.fail("open "+data.RefText(ref), err)
		return s
	}
	from := a.currentJump(s)
	s = a.dropModals(s)
	if cur, ok := a.Showing(); !ok || cur != ref {
		a.leave(s)
		a.nav.history = append(a.nav.history[:a.nav.histPos+1], historyEntry{ref: ref, focus: showHeader})
		a.nav.histPos = len(a.nav.history) - 1
		a.resetShowScroll(ref)
	}
	s.ActivePage = SpaceWork
	a.say("%s", a.title(ref))
	next := a.arrive(s)
	a.recordJump(from, a.currentJump(next))
	return next
}

func (a *App) historyTravel(s core.State, c core.Command, delta int) core.StepResult {
	if c.Arg("scope") == "work" {
		return core.Next(a.travel(s, delta))
	}
	return core.Next(a.jumpTravel(s, delta))
}

func (a *App) jumpTravel(s core.State, delta int) core.State {
	if len(a.nav.jumps) == 0 || a.nav.jumpPos < 0 {
		if s.ActivePage == SpaceWork {
			return a.travel(s, delta)
		}
		if delta < 0 {
			a.say("this is the first page")
		} else {
			a.say("nothing to go forward to")
		}
		return s
	}
	to := a.nav.jumpPos + delta
	if to < 0 || to >= len(a.nav.jumps) {
		if delta < 0 {
			a.say("this is the first page")
		} else {
			a.say("nothing to go forward to")
		}
		return s
	}
	a.nav.jumps[a.nav.jumpPos].focus = s.Pages[s.ActivePage].FocusID
	if s.ActivePage == SpaceWork {
		a.leave(s)
	}
	a.nav.jumpPos = to
	entry := a.nav.jumps[to]

	a.nav.jumping = true
	defer func() { a.nav.jumping = false }()

	s = a.dropModals(s)
	targetPage := entry.page
	if targetPage == SpaceProjects && !a.projectsTopNavTabEnabled() {
		targetPage = SpacePanes
	} else if targetPage == SpaceFeatures && !a.featuresPageActive() {
		targetPage = SpacePanes
	} else if isProjectTab(targetPage) {
		pid := projectIDFromTab(targetPage)
		if !a.projectsDynamicTabsEnabled() {
			if a.projectsTopNavTabEnabled() {
				targetPage = SpaceProjects
			} else {
				targetPage = SpacePanes
			}
		} else if a.Store == nil {
			targetPage = SpacePanes
		} else if rec, err := a.Store.Get(core.Ref{Kind: data.KindTask, ID: pid}); err != nil || rec == nil {
			if a.projectsTopNavTabEnabled() {
				targetPage = SpaceProjects
			} else {
				targetPage = SpacePanes
			}
		} else {
			a.ensureProjectTabOpen(pid)
		}
	}
	s.ActivePage = targetPage
	if targetPage == SpaceWork {
		if entry.ref.ID != "" {
			if entry.histPos >= 0 && entry.histPos < len(a.nav.history) && a.nav.history[entry.histPos].ref == entry.ref {
				a.nav.histPos = entry.histPos
			} else if idx := slices.IndexFunc(a.nav.history, func(h historyEntry) bool { return h.ref == entry.ref }); idx >= 0 {
				a.nav.histPos = idx
			} else {
				a.nav.history = append(a.nav.history, historyEntry{ref: entry.ref, focus: showHeader})
				a.nav.histPos = len(a.nav.history) - 1
			}
			focus := entry.focus
			if focus == "" {
				focus = showHeader
			}
			a.nav.history[a.nav.histPos].focus = focus
			a.resetShowScroll(entry.ref)
			a.say("%s", a.title(entry.ref))
			return withPage(s, SpaceWork, focus, ModeNormal)
		}
		a.nav.histPos = -1
		if entry.browseKind != a.nav.browseKind {
			a.nav.browseKind = entry.browseKind
			delete(a.nav.tables, workTable)
			a.setActivePreset(workTable, "")
		}
		focus := entry.focus
		if focus == "" {
			focus = workTable
		}
		a.nav.indexFocus = focus
		a.say("%s", strings.ToLower(a.schema(a.workKind()).Plural))
		return withPage(s, SpaceWork, focus, ModeNormal)
	}
	focus := entry.focus
	if targetPage != entry.page || focus == "" {
		focus = a.defaultFocus(s)
	}
	mode := s.Pages[targetPage].Mode
	if mode == "" {
		mode = ModeNormal
	}
	if isProjectTab(targetPage) {
		a.say("%s", a.title(core.Ref{Kind: data.KindTask, ID: projectIDFromTab(targetPage)}))
	} else {
		a.say("%s", targetPage)
	}
	return withPage(s, targetPage, focus, mode)
}

func (a *App) travel(s core.State, delta int) core.State {
	to := a.nav.histPos + delta
	if to < -1 && a.nav.browseKind != "" {
		return a.browse(s, data.KindTask) // back from another noun's index: tasks
	}
	if to < -1 || to >= len(a.nav.history) {
		if delta < 0 {
			a.say("this is the first page")
		} else {
			a.say("nothing to go forward to")
		}
		return s
	}
	a.leave(s)
	a.nav.histPos = to
	if ref, ok := a.Showing(); ok {
		a.resetShowScroll(ref)
		a.say("%s", a.title(ref))
	} else {
		a.resetShowScroll(core.Ref{})
		a.say("%s", strings.ToLower(a.schema(a.workKind()).Plural))
	}
	return a.arrive(s)
}
