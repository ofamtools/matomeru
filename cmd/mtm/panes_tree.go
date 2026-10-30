package main

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/unixsuperhero/ochazuke"
)

// The panes page's left side is an ochazuke tree grouped by the
// panes.group setting (panes_group.go): project → pane by default, project
// → session → pane, session → pane or flat; unassociated last, and while
// pinned the project level is the pin. One row per window: a window
// running an agent is the agent's row (the old app's rule: a harness, or
// an agent state other than none), with the agent's state glyph, harness
// and age; a plain shell is a shell row. Every pane verb answers on
// either. Rows that need input sort first in their group, groups with one
// first, and the header counts them.

const (
	paneTreeID  = "panes/tree"
	outsideNode = "outside-pin"
)

// paneTreeColumns are the tree's table: the pane (indented by the tree,
// its marker gutter first), its short state and the compact status line
// (context used and cost), which is there only while some row has one
// (paneView.usage) and drops first when the tree is narrow. The
// harness, model, effort, permission mode, cwd and owner are the detail's
// header line (paneHeader), so the tree stays lean.
var paneTreeColumns = []ochazuke.Column{
	{Title: "pane", Min: 16},
	{Title: "state", Priority: 1},
	{Title: "use", Priority: 2},
}

// isAgent is the old app's rule: a pane with a harness, or one whose agent
// state is anything but none, is an agent's pane.
func (p pane) isAgent() bool {
	return p.Harness != "" || p.AgentState != "" && p.AgentState != "none"
}

// needsInput reports whether the pane waits on the person.
func (p pane) needsInput() bool {
	return !p.Exited && (p.words().waiting(p.AgentState) || p.NeedsYou)
}

// queuedMarker follows a pane's name while prompts wait in its delivery
// queue (list-agents' queued), with no count; it is gone once the queue
// empties. It marks waiting prompts, not needs input (that is the ? glyph,
// which the header's W next jumps to).
const queuedMarker = "W"

// stateGlyph is the agent's state as one cell; a shell has none.
func stateGlyph(p pane) string {
	if p.Exited {
		return "×"
	}
	if !p.isAgent() {
		return " "
	}
	switch {
	case p.needsInput():
		return "?"
	case p.working():
		return "●"
	case p.AgentState == "done":
		return "✓"
	case p.errored():
		return "✗"
	}
	return "○"
}

// The marker gutter leads every pane row, one cell per slot and a blank
// slot when its marker is off, so the slots line up down the list: the
// star (pane.star), side panes (a side vim or terminal is open: leader v,
// t, panes.sides) and the state glyph. ★ is the star the context list and
// the old badge used; ◧, a square with one side filled, is a window with
// a pane beside it. Queued prompts stay W after the name.
const (
	starMarker = "★"
	sideMarker = "◧"
)

// markerGutter is a pane row's gutter: star, side, state glyph.
func markerGutter(p pane) string {
	star, side := " ", " "
	if p.Starred {
		star = starMarker
	}
	if p.SideVim != "" || p.SideTerm != "" {
		side = sideMarker
	}
	return star + side + stateGlyph(p)
}

// stateTone is the theme tone of an agent's state (R5.14): the glyph and
// the short state column carry it, and themes.json "tones" colours it; a
// shell has none. The text still says the state, so mono reads the same.
func stateTone(p pane) string {
	switch {
	case p.Exited:
		return ochazuke.ToneExited
	case !p.isAgent():
		return ""
	case p.needsInput():
		return ochazuke.ToneNeedsInput
	case p.working():
		return ochazuke.ToneWorking
	case p.AgentState == "done":
		return ochazuke.ToneDone
	case p.errored():
		return ochazuke.ToneErrored
	}
	return ochazuke.ToneIdle
}

// toneSide is the side marker's tone. ochazuke's themes do not draw it, so
// it is plain until themes.json gives it a style.
const toneSide = "marker.side"

// paneTones are a pane row's tones over its cells: the gutter's star,
// side and state glyph (runes 0, 1, 2 of cell 0), W after the name, and
// the state column.
func paneTones(p pane, name string) []ochazuke.ToneSpan {
	var tones []ochazuke.ToneSpan
	if p.Starred {
		tones = append(tones, ochazuke.SpanTone(0, 0, 1, ochazuke.ToneStar))
	}
	if p.SideVim != "" || p.SideTerm != "" {
		tones = append(tones, ochazuke.SpanTone(0, 1, 2, toneSide))
	}
	state := stateTone(p)
	if state != "" {
		tones = append(tones, ochazuke.SpanTone(0, 2, 3, state))
	}
	if p.Queued > 0 {
		at := utf8.RuneCountInString(name) - utf8.RuneCountInString(queuedMarker)
		tones = append(tones, ochazuke.SpanTone(0, at, at+utf8.RuneCountInString(queuedMarker), ochazuke.ToneQueued))
	}
	if state != "" {
		tones = append(tones, ochazuke.CellTone(1, state))
	}
	return tones
}

// kind is what runs in the pane: the harness, "agent" for an agent of no
// known harness, the program mtm started in it ("vim"), else "shell".
func (p pane) kind() string {
	switch {
	case p.Harness != "":
		return p.Harness
	case p.isAgent():
		return "agent"
	case p.Program != "":
		return p.Program
	}
	return "shell"
}

var defaultWindowName = regexp.MustCompile(`^Terminal [0-9a-fA-F]{8}$`)

// baseName is the pane's name before it is made unique in its session: the
// name someone gave the window, else what runs in it (the harness or
// shell) rather than the daemon's "Terminal 1a2b3c4d".
func (p pane) baseName() string {
	for _, name := range []string{p.Name, p.Title} {
		if name != "" && !defaultWindowName.MatchString(name) {
			return name
		}
	}
	return p.kind()
}

// stateCell is the tree's short state: "working 2m", "needs input",
// "shell". The harness is the detail header's.
func (p pane) stateCell(now time.Time) string {
	switch {
	case p.Exited:
		return "exited"
	case !p.isAgent():
		return p.kind()
	}
	cell := p.stateWord()
	if p.StateAt > 0 {
		cell += " " + age(now.Sub(time.UnixMilli(p.StateAt)))
	}
	return cell
}

// stateWord is an agent's state in words: "working", "needs input",
// "agent" for an agent with no state.
func (p pane) stateWord() string {
	if p.Exited {
		return "exited"
	}
	state := p.AgentState
	if state == "" || state == "none" {
		state = "agent"
	}
	if p.needsInput() {
		state = "needs input"
	}
	return strings.ReplaceAll(state, "_", " ")
}

// age is a duration in one short unit: 40s, 3m, 2h, 5d.
func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(0, int(d.Seconds())))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// uniqueNames names each pane of a session once: a second claude is
// claude-2, a second shell shell-2.
func uniqueNames(panes []pane) map[string]string {
	out := map[string]string{}
	count := map[string]int{}
	for _, p := range panes {
		if p.ID == "" {
			continue
		}
		key := p.Session + "\x00" + p.baseName()
		count[key]++
		name := p.baseName()
		if n := count[key]; n > 1 {
			name = fmt.Sprintf("%s-%d", name, n)
		}
		out[p.ID] = name
	}
	return out
}

// paneGroup is one session's panes under one owner.
type paneGroup struct {
	session pane // the session's first listing (Session, SessionID)
	id      string
	panes   []pane
}

func (g paneGroup) needsInput() bool { return slices.ContainsFunc(g.panes, pane.needsInput) }

// paneProject is one owner's sessions; id "" is unassociated.
type paneProject struct {
	id       string
	label    string
	sessions []*paneGroup
}

func (p paneProject) count() (panes, waiting int) {
	for _, g := range p.sessions {
		panes += len(g.panes)
		for _, q := range g.panes {
			if q.needsInput() {
				waiting++
			}
		}
	}
	return panes, waiting
}

// paneView is what the tree shows: the rows, how many panes it holds, how
// many need input and how many the pin hides.
type paneView struct {
	nodes   []ochazuke.TreeNode
	panes   int
	waiting int
	hidden  int
	order   []string          // pane row ids in display order (j/k inside live)
	owners  map[string]string // window → its owning project ("" unassociated)
	usage   bool              // some pane row has a usage cell (the "use" column)
	queued  int               // rows of the top list (waiting, review, outside items)
	working int               // rows of the bottom list
}

// columns are the tree's columns for this view: the "use" column only
// while some row fills it, so an empty one never takes the pane's room.
func (v paneView) columns() []ochazuke.Column {
	if v.usage {
		return paneTreeColumns
	}
	return paneTreeColumns[:2]
}

// buildPaneTree groups the panes by owner and session, in scope, with the
// attached pane outside the pin as its own labelled group, and draws the
// levels panes.group shows.
func (a *App) buildPaneTree(panes []pane, now time.Time) paneView {
	grouping := a.paneGrouping()
	byProject := grouping == groupProjectPane || grouping == groupProjectSessionPane
	showSessions := grouping == groupProjectSessionPane || grouping == groupSessionPane
	// bucket is the group a pane's owner falls in: the owner when projects
	// show, else one group for all (the outside pin keeps its own).
	bucket := func(owner string) string {
		if byProject || owner == outsideNode {
			return owner
		}
		return ""
	}
	names := uniqueNames(panes)
	projects := map[string]*paneProject{}
	var order []string
	group := func(owner, sessionKey string, session pane) *paneGroup {
		proj := projects[owner]
		if proj == nil {
			label := a.ownerLabel(owner)
			if owner == outsideNode {
				label = "outside pin"
			}
			proj = &paneProject{id: owner, label: label}
			projects[owner] = proj
			order = append(order, owner)
		}
		for _, g := range proj.sessions {
			if g.id == sessionKey {
				return g
			}
		}
		g := &paneGroup{session: session, id: sessionKey}
		proj.sessions = append(proj.sessions, g)
		return g
	}
	view := paneView{owners: map[string]string{}}
	for _, p := range panes {
		sessionOwner := ""
		if p.SessionID != "" {
			o, err := a.work.SessionOwner(p.SessionID)
			if err != nil {
				a.paneErr = err.Error()
			}
			sessionOwner = o.ProjectID
		}
		sessionKey := "session:" + cmp.Or(p.SessionID, p.Session)
		if p.ID == "" {
			if a.inScope(sessionOwner) {
				group(bucket(sessionOwner), sessionKey, p)
			}
			continue
		}
		owner := a.paneOwner(p).ProjectID
		view.owners[p.ID] = owner
		switch {
		case a.inScope(owner) || p.OwnerUnsaved:
		case a.attached.ID == p.ID:
			owner = outsideNode
		default:
			view.hidden++
			continue
		}
		// A session split between owners shows under each when projects
		// show; the outside pin's copy of a session is its own row.
		key := sessionKey
		if byProject && owner != sessionOwner || !byProject && owner == outsideNode {
			key += "@" + owner
		}
		g := group(bucket(owner), key, p)
		g.panes = append(g.panes, p)
	}
	// Needs input first; then name order, unassociated and outside last.
	rank := func(id string) int {
		switch id {
		case outsideNode:
			return 2
		case "":
			return 1
		}
		return 0
	}
	slices.SortStableFunc(order, func(x, y string) int {
		px, py := projects[x], projects[y]
		_, wx := px.count()
		_, wy := py.count()
		return cmp.Or(cmp.Compare(rank(x), rank(y)), -cmp.Compare(min(wx, 1), min(wy, 1)), cmp.Compare(px.label, py.label))
	})
	needsInputFirst := func(x, y pane) int { return -cmp.Compare(btoi(x.needsInput()), btoi(y.needsInput())) }
	addPane := func(p pane, parent string) {
		name := markerGutter(p) + " " + names[p.ID]
		if p.Queued > 0 {
			name += " " + queuedMarker
		}
		use := p.usageCell()
		view.usage = view.usage || use != ""
		view.nodes = append(view.nodes, ochazuke.TreeNode{ID: p.rowID(), Parent: parent, Label: p.label(), Noun: "pane", Data: p, Cells: []string{name, p.stateCell(now), use}, Tones: paneTones(p, name)})
		view.order = append(view.order, p.rowID())
	}
	tree := byProject && (a.pin == "" || a.all)
	for _, id := range order {
		proj := projects[id]
		slices.SortStableFunc(proj.sessions, func(x, y *paneGroup) int {
			return -cmp.Compare(btoi(x.needsInput()), btoi(y.needsInput()))
		})
		n, waiting := proj.count()
		if n == 0 && !showSessions {
			continue // only empty sessions, and sessions do not show
		}
		view.panes += n
		view.waiting += waiting
		parent := ""
		if tree || id == outsideNode {
			parent = "project:" + id
			if id == outsideNode {
				parent = outsideNode
			}
			view.nodes = append(view.nodes, ochazuke.TreeNode{ID: parent, Label: fmt.Sprintf("%s (%d)", proj.label, n), Noun: "pane-project", Data: id, Cells: []string{fmt.Sprintf("%s (%d)", proj.label, n), waitingCell(waiting)}})
		}
		if !showSessions {
			var all []pane
			for _, g := range proj.sessions {
				all = append(all, g.panes...)
			}
			slices.SortStableFunc(all, needsInputFirst)
			for _, p := range all {
				addPane(p, parent)
			}
			continue
		}
		for _, g := range proj.sessions {
			slices.SortStableFunc(g.panes, needsInputFirst)
			label := fmt.Sprintf("%s (%d)", shortSession(g.session.Session), len(g.panes))
			_, w := (&paneProject{sessions: []*paneGroup{g}}).count()
			cell := label
			if parent != "" {
				// Under its project a session says so: a session named
				// after its project read as the project twice (F22).
				cell = "session " + label
			}
			view.nodes = append(view.nodes, ochazuke.TreeNode{ID: g.id, Parent: parent, Label: "session " + label, Noun: "session", Data: g.session, Cells: []string{cell, waitingCell(w)}})
			for _, p := range g.panes {
				addPane(p, g.id)
			}
		}
	}
	return view
}

// countCell says how many panes of a group wait on the person.
func waitingCell(waiting int) string {
	if waiting == 0 {
		return ""
	}
	return fmt.Sprintf("%d need input", waiting)
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
