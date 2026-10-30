package main

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/unixsuperhero/ochazuke"
)

// The Panes page's left side in the panels layout (panes.layout panels,
// the default): the queue ("Needs you") is a bordered panel at the top,
// Working a bordered panel floated to the bottom, and the Panes tree
// fills the lines between them. A panel is panes.panel-min lines tall at
// least and grows with its rows up to panes.panel-max, then scrolls, so a
// count changing by one does not move the rest of the column. An empty
// panel shows its empty-state line (panes.empty-panels show) or gives its
// lines to the Panes tree (hide). panes.layout stacked is the earlier
// look: three titled lists sized by panes.section-layout.
//
// The three lists stay focus stops in drawing order (j and k spill across
// them, ochazuke's visual motions); a focused panel draws the heavy
// accent border the terminal panel draws. panes.focus-section (g n, g w,
// g p on the page) moves the focus to a list.
//
// Which agent states put a pane in Working, the queue (waiting) or the
// errored category is data too: panes.working-states, panes.waiting-states
// and panes.errored-states list the state words (tuios's agent states:
// none, working, needs_input, idle, done, errored, unknown).

const (
	settingPanesLayout   = "panes.layout"
	settingPanelMin      = "panes.panel-min"
	settingPanelMax      = "panes.panel-max"
	settingEmptyPanels   = "panes.empty-panels"
	settingStickyRows    = "panes.sticky-rows"
	settingStableOrder   = "panes.stable-order"
	settingWorkingStates = "panes.working-states"
	settingWaitingStates = "panes.waiting-states"
	settingErroredStates = "panes.errored-states"

	layoutPanels  = "panels"
	layoutStacked = "stacked"
	emptyShow     = "show"
	emptyHide     = "hide"

	defaultPanelMin = 6
	defaultPanelMax = 10
	// smallestPanel is a panel's floor on a short page: its border and one
	// row.
	smallestPanel = 3
	// middleFloor is the Panes tree's floor before the panels shrink: its
	// header and two rows.
	middleFloor = 3
)

// panelText is a panel's title and the line an empty one shows, by
// section.
type panelText struct{ title, empty string }

var panelTexts = map[string]panelText{
	sectionQueue:   {title: "Needs you", empty: "Nothing needs you"},
	sectionPanes:   {title: "Panes", empty: "No panes"},
	sectionWorking: {title: "Working", empty: "No agent is working"},
}

// tuios's agent states (internal/session/agent_state.go): the words
// list-agents and list-windows report.
var (
	defaultWorkingStates = []string{"working"}
	defaultWaitingStates = []string{"needs_input"}
	defaultErroredStates = []string{"errored"}
)

// panesPanelSettings are the panels layout's settings, the row-holding
// rules and the state words.
func panesPanelSettings() []ochazuke.Setting {
	lines := func(name string, def int, what string) ochazuke.Setting {
		return ochazuke.Setting{Name: name, Type: ochazuke.SettingInteger, Default: def, Description: what,
			Validate: func(v any) error {
				if n, _ := v.(int); n < smallestPanel || n > 100 {
					return fmt.Errorf("lines from %d to 100", smallestPanel)
				}
				return nil
			}}
	}
	words := func(name string, def []string, what string) ochazuke.Setting {
		return ochazuke.Setting{Name: name, Type: ochazuke.SettingList, Default: def,
			Description: what + " (tuios reports none, working, needs_input, idle, done, errored, unknown)",
			Validate: func(v any) error {
				for _, w := range listValue(v) {
					if strings.TrimSpace(w) == "" {
						return fmt.Errorf("an empty state word")
					}
				}
				return nil
			}}
	}
	return []ochazuke.Setting{
		{Name: settingPanesLayout, Type: ochazuke.SettingString, Default: layoutPanels, Enum: []string{layoutPanels, layoutStacked},
			Description: "The Panes page's left side: panels (Needs you and Working as bordered panels at the top and bottom, the panes between) or stacked (three titled lists, sized by panes.section-layout)"},
		lines(settingPanelMin, defaultPanelMin, "Lines a panel of the panels layout takes at least, its border included"),
		lines(settingPanelMax, defaultPanelMax, "Lines a panel of the panels layout grows to with its rows before it scrolls, its border included"),
		{Name: settingEmptyPanels, Type: ochazuke.SettingString, Default: emptyShow, Enum: []string{emptyShow, emptyHide},
			Description: "An empty panel of the panels layout: show (its border and an empty-state line, so the column keeps its shape) or hide (its lines go to the panes)"},
		{Name: settingStickyRows, Type: ochazuke.SettingBoolean, Default: false,
			Description: "Keep the row under the cursor in its list when its pane changes section, until the cursor moves off it"},
		{Name: settingStableOrder, Type: ochazuke.SettingBoolean, Default: true,
			Description: "A row keeps the place it entered its list with until it leaves (off: each refresh sorts by the current state time)"},
		words(settingWorkingStates, defaultWorkingStates, "Agent states that put a pane in Working"),
		words(settingWaitingStates, defaultWaitingStates, "Agent states that mean the agent waits on you (needs input)"),
		words(settingErroredStates, defaultErroredStates, "Agent states that mean the agent stopped on an error"),
	}
}

// stateWords are the agent state words of each signal.
type stateWords struct{ work, wait, fail []string }

var defaultStateWords = &stateWords{work: defaultWorkingStates, wait: defaultWaitingStates, fail: defaultErroredStates}

func (w *stateWords) working(state string) bool { return slices.Contains(w.work, state) }
func (w *stateWords) waiting(state string) bool { return slices.Contains(w.wait, state) }
func (w *stateWords) errored(state string) bool { return slices.Contains(w.fail, state) }

// words are the state words the pane is read with.
func (p pane) words() *stateWords {
	if p.states != nil {
		return p.states
	}
	return defaultStateWords
}

// working reports whether the agent's state is a working one.
func (p pane) working() bool { return p.words().working(p.AgentState) }

// errored reports whether the agent's state is an errored one.
func (p pane) errored() bool { return p.words().errored(p.AgentState) }

// signalState is the agent's state in tuios's own words for the pane
// signals (internal/panesignals reads those): a state the settings list
// as waiting, errored or working reads as needs_input, errored or
// working; any other is itself.
func (p pane) signalState() string {
	w := p.words()
	switch {
	case w.waiting(p.AgentState):
		return defaultWaitingStates[0]
	case w.errored(p.AgentState):
		return defaultErroredStates[0]
	case w.working(p.AgentState):
		return defaultWorkingStates[0]
	}
	return p.AgentState
}

// paneStates are the state words of the settings.
func (a *App) paneStates() *stateWords {
	s := a.r.Settings()
	return &stateWords{work: s.Strings(settingWorkingStates), wait: s.Strings(settingWaitingStates), fail: s.Strings(settingErroredStates)}
}

// stampStates gives every pane the settings' state words.
func (a *App) stampStates(panes []pane) {
	if a.r == nil {
		return
	}
	st := a.paneStates()
	for i := range panes {
		panes[i].states = st
	}
	if a.attached.ID != "" {
		a.attached.states = st
	}
}

// observePanelSettings rebuilds the lists when a setting that sorts them
// changes; the layout settings are read when drawing.
func (a *App) observePanelSettings() {
	for _, name := range []string{settingStickyRows, settingStableOrder, settingWorkingStates, settingWaitingStates, settingErroredStates} {
		a.r.Settings().Observe(name, func(string, any) {
			if err := a.rebuildPanes(); err != nil {
				a.r.UI().Notify("panes: " + err.Error())
			}
		})
	}
}

// holdsRows reports whether the row under the cursor stays in its list:
// panes.sticky-rows, while the cursor is on one of the lists. With the
// focus in the detail or a live pane there is no cursor on a row, so no
// row is held (a held row used to stay put for as long as its pane was
// attached).
func (a *App) holdsRows() bool {
	if !a.r.Settings().Bool(settingStickyRows) {
		return false
	}
	for _, s := range a.pv.sections {
		if s.tree.List().Focused() {
			return true
		}
	}
	return false
}

// followRow keeps the selection on row, the row the detail showed, when
// a rebuild moved it to another list (it changed section and is not
// held): that list becomes the active one, with the cursor on the row
// when the cursor was on one of the lists, so the detail and a live pane
// stay on the pane that moved.
func (a *App) followRow(row string) error {
	if row == "" || a.pv.held {
		return nil
	}
	i := a.sectionOf(row)
	if i < 0 {
		return nil
	}
	if cur, _ := a.selectedRow(); cur == row && i == a.pv.active {
		return nil
	}
	return a.selectPaneRow(row)
}

// panelsLayout reports whether the left side draws panels.
func (a *App) panelsLayout() bool {
	return len(a.pv.sections) == 3 && a.r.Settings().String(settingPanesLayout) != layoutStacked
}

// panelLimits are panes.panel-min and panes.panel-max, the max never under
// the min.
func (a *App) panelLimits() (int, int) {
	s := a.r.Settings()
	lo := max(smallestPanel, s.Int(settingPanelMin))
	return lo, max(lo, s.Int(settingPanelMax))
}

// panelContent is the lines a list's rows fill (Tree.Height: the
// breadcrumb and filter lines count), 0 for an empty list.
func panelContent(s *paneSection) int {
	if len(s.nodes) == 0 {
		return 0
	}
	return s.tree.Height()
}

// panelHeights splits height lines between the top panel, the Panes tree
// and the bottom panel. content is each list's lines (0 when empty; the
// middle's with its column header); a panel is its rows and border, from
// lo up to hi lines, or 0 when empty and hide; the middle takes the rest.
// On a short page the panels shrink (the larger first, down to
// smallestPanel) until the middle has a third of the height, at least
// middleFloor lines, or all it needs when that is less; then everything
// fits in height.
func panelHeights(height int, content [3]int, lo, hi int, hide bool) [3]int {
	var out [3]int
	for _, i := range []int{0, 2} {
		if content[i] == 0 && hide {
			continue
		}
		out[i] = min(hi, max(lo, content[i]+2))
	}
	shrink := func(floor int, fits func() bool) {
		for !fits() {
			i := 0
			if out[2] > out[0] {
				i = 2
			}
			if out[i] <= floor {
				if i = 2 - i; out[i] <= floor {
					return
				}
			}
			out[i]--
		}
	}
	floor := max(middleFloor, height/3)
	if content[1] < floor {
		floor = max(content[1], 1)
	}
	floor = min(floor, height)
	shrink(smallestPanel, func() bool { return height-out[0]-out[2] >= floor })
	shrink(0, func() bool { return out[0]+out[2] <= height })
	out[1] = max(0, height-out[0]-out[2])
	return out
}

// panelSizes are the three heights for height lines.
func (a *App) panelSizes(height int) [3]int {
	lo, hi := a.panelLimits()
	var content [3]int
	for i, s := range a.pv.sections {
		content[i] = panelContent(s)
	}
	if content[1] > 0 {
		content[1]++ // the column header
	}
	return panelHeights(height, content, lo, hi, a.r.Settings().String(settingEmptyPanels) == emptyHide)
}

// sectionsNeed is the lines the left side would fill, for the narrow
// layout's split: the panels at their size and the Panes tree's rows, or
// the stacked lists' titles and rows.
func (a *App) sectionsNeed() int {
	if !a.panelsLayout() {
		need := 0
		for _, sec := range a.pv.sections {
			need += a.sectionNeed(sec)
		}
		return need
	}
	sizes := a.panelSizes(1 << 20)
	return sizes[0] + sizes[2] + max(0, a.sectionNeed(a.pv.sections[1])-1)
}

// panelsBlock draws the panels layout in width x height: the top panel,
// the Panes tree filling the middle, the bottom panel on the last lines.
func (a *App) panelsBlock(width, height int) ochazuke.Block {
	st := ochazuke.NewStyles(*a.m.Appearance)
	layout := ochazuke.Layout{Width: width, Height: height, Styles: st}
	sizes := a.panelSizes(height)
	var lines []string
	var cursor *ochazuke.Pos
	for i, sec := range a.pv.sections {
		h := sizes[i]
		if h <= 0 {
			continue
		}
		var block ochazuke.Block
		if i == 1 {
			block = sec.tree.Block(width, h)
		} else {
			block = a.panelBlock(layout, st, sec, width, h)
		}
		if block.Cursor != nil && cursor == nil {
			cursor = &ochazuke.Pos{X: block.Cursor.X, Y: block.Cursor.Y + len(lines)}
		}
		body := strings.Split(block.Text, "\n")
		for len(body) < h {
			body = append(body, "")
		}
		lines = append(lines, body[:h]...)
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	out := ochazuke.Text(strings.Join(lines[:height], "\n"))
	out.Cursor = cursor
	return out
}

// panelBlock is one outer list as a bordered panel of width x height: its
// title and count (and how many rows its window hides), its rows or its
// empty-state line; the border is the focused one while the list holds
// the focus.
func (a *App) panelBlock(layout ochazuke.Layout, st ochazuke.Styles, sec *paneSection, width, height int) ochazuke.Block {
	text := panelTexts[sec.name]
	cw, ch := ochazuke.PanelContentSize(width, height)
	title := fmt.Sprintf("%s (%d)", cmp.Or(text.title, sec.title), sec.rows)
	var body ochazuke.Block
	if len(sec.nodes) == 0 {
		body = ochazuke.Text(st.Muted.Render(text.empty))
	} else {
		body = sec.tree.Block(cw, max(1, ch))
		if need := sec.tree.Height(); need > ch && ch > 0 {
			title += fmt.Sprintf(" · +%d more", need-ch)
		}
	}
	return layout.PanelBlock(title, body, width, height, sec.tree.List().Focused())
}

// focusSection is panes.focus-section: the focus on one of the page's
// lists (queue, panes, working), on its selected row, else its first; the
// Panes page opens first when another page is showing. An empty list is
// no stop, so the verb says so and the focus stays.
func (a *App) focusSection(ctx ochazuke.Ctx) ochazuke.Result {
	name := ochazuke.StringArg(ctx.Args, "section", "")
	i := slices.IndexFunc(a.pv.sections, func(s *paneSection) bool { return s.name == name })
	if i < 0 {
		return ochazuke.Failed(fmt.Errorf("section %q: one of %s", name, strings.Join(sectionNames, ", ")))
	}
	if a.r.Page != panesPage {
		if err := a.r.OpenPage(panesPage); err != nil {
			return ochazuke.Failed(err)
		}
	}
	s := a.pv.sections[i]
	if len(s.nodes) == 0 {
		return ochazuke.Done(panelTexts[s.name].empty)
	}
	target := ""
	if n, ok := s.tree.Selected(); ok {
		target = n.ID
	}
	if i == 1 && !strings.HasPrefix(target, "pane:") {
		target = cmp.Or(a.firstPaneRow(), target)
	}
	if target == "" {
		target = s.nodes[0].ID
	}
	if a.r.Instances[target] == nil {
		return ochazuke.Failed(fmt.Errorf("unknown row %q", target))
	}
	a.pv.active = i
	if a.r.Focus != target {
		if err := a.r.SetFocus(target); err != nil {
			return ochazuke.Failed(err)
		}
	}
	return ochazuke.Done(target)
}
