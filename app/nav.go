package app

import (
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/commandline"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/table"
)

type historyEntry struct {
	ref   core.Ref
	focus string // focus to restore when coming back to this page
}

// NavController owns tables, presets, archived visibility, browsing state,
// Work history, focus tracking, filtering, command line, registers, and macros.
type NavController struct {
	tables       map[string]*table.Model
	recCacheKeys map[string]string
	presets      *table.PresetStore
	activePreset map[string]string
	showCompact  bool
	showArchived bool
	browseKind   string
	history      []historyEntry
	histPos      int
	indexFocus   string
	lastFocus    map[string]string
	focusRegion  string
	tocFilter    string
	filterTarget string
	filterLine   input.Line
	cmdline      *commandline.Model
	Registers    core.Registers
	Macros       core.MacroRecorder
	replaying    int
}

func newNavController() *NavController {
	return &NavController{
		tables:       map[string]*table.Model{},
		recCacheKeys: map[string]string{},
		activePreset: map[string]string{},
		lastFocus:    map[string]string{},
		histPos:      -1,
		indexFocus:   workTable,
	}
}

// --- focus ----------------------------------------------------------------------

// regionRoots are the nodes whose focusable descendants form one column: h
// and l move between regions, j and k walk every focusable in drawing order.
var regionRoots = []string{"panes.rail", "project.overview", "site.toc", "site.content", showHeader, "show.props", "show.related",
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
	target := a.nav.lastFocus[regions[to]]
	if !slices.ContainsFunc(nodes, func(n fnode) bool { return n.id == target && n.region == regions[to] }) {
		target = nodes[slices.IndexFunc(nodes, func(n fnode) bool { return n.region == regions[to] })].id
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

func (a *App) motion(s core.State, c core.Command) core.StepResult {
	if a.Modal() != "" {
		return a.handleModal(s, c)
	}
	focus := s.ActiveFrame().FocusID
	if focus == workTable && a.inWorkTree(s) && c.ID != "motion.left" && c.ID != "motion.right" {
		a.workTreeMove(c)
		return core.Next(s)
	}
	if tbl := a.nav.tables[focus]; tbl != nil && c.ID != "motion.left" && c.ID != "motion.right" {
		before := tbl.SelectedIdx
		tbl.HandleCommand(c)
		if tbl.SelectedIdx != before || (c.ID != "motion.down" && c.ID != "motion.up") {
			return core.Next(s)
		}
		// At the list's edge the motion spills into the next thing drawn.
	}
	switch c.ID {
	case "motion.down":
		s = a.step(s, c.Times())
	case "motion.up":
		s = a.step(s, -c.Times())
	case "motion.half.down", "motion.block.next":
		s = a.step(s, 5*c.Times())
	case "motion.half.up", "motion.block.prev":
		s = a.step(s, -5*c.Times())
	case "motion.page.down":
		s = a.step(s, 10*c.Times())
	case "motion.page.up":
		s = a.step(s, -10*c.Times())
	case "motion.right":
		s = a.column(s, 1)
	case "motion.left":
		s = a.column(s, -1)
	case "motion.top":
		s = a.edge(s, false)
	case "motion.bottom":
		s = a.edge(s, true)
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

func (a *App) openSpace(s core.State, id string) core.State {
	if !slices.ContainsFunc(spaces, func(sp spaceDef) bool { return sp.id == id }) {
		a.say("no space %q", id)
		return s
	}
	s = a.dropModals(s)
	s.ActivePage = id
	return s
}

func (a *App) cycleSpace(s core.State, delta int) core.State {
	i := slices.IndexFunc(spaces, func(sp spaceDef) bool { return sp.id == s.ActivePage })
	n := len(spaces)
	return a.openSpace(s, spaces[((i+delta)%n+n)%n].id)
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
	s = a.dropModals(s)
	if cur, ok := a.Showing(); !ok || cur != ref {
		a.leave(s)
		a.nav.history = append(a.nav.history[:a.nav.histPos+1], historyEntry{ref: ref, focus: showHeader})
		a.nav.histPos = len(a.nav.history) - 1
	}
	s.ActivePage = SpaceWork
	a.say("%s", a.title(ref))
	return a.arrive(s)
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
		a.say("%s", a.title(ref))
	} else {
		a.say("%s", strings.ToLower(a.schema(a.workKind()).Plural))
	}
	return a.arrive(s)
}
