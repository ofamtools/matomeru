package main

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/unixsuperhero/ochazuke"
)

// The panes page's left side is an ochazuke tree: project → session →
// pane in the all view (unassociated last), session → pane while pinned.
// One row per window: a window running an agent is the agent's row (the
// old app's rule: a harness, or an agent state other than none), with the
// agent's state glyph, harness and age; a plain shell is a shell row. Every
// pane verb answers on either. Rows that need input sort first in their
// session, sessions and projects with one sort first, and the header
// counts them.

const (
	paneTreeID  = "panes/tree"
	outsideNode = "outside-pin"
)

// paneTreeColumns are the tree's table: the pane (indented by the tree,
// its state glyph first), its short state and the compact status line
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
	return !p.Exited && (p.AgentState == "needs_input" || p.NeedsYou)
}

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
	case p.AgentState == "working":
		return "●"
	case p.AgentState == "done":
		return "✓"
	case p.AgentState == "errored":
		return "✗"
	}
	return "○"
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
// attached pane outside the pin as its own labelled group.
func (a *App) buildPaneTree(panes []pane, now time.Time) paneView {
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
				group(sessionOwner, sessionKey, p)
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
		key := sessionKey
		if owner != sessionOwner {
			key += "@" + owner
		}
		g := group(owner, key, p)
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
	tree := a.pin == "" || a.all
	for _, id := range order {
		proj := projects[id]
		slices.SortStableFunc(proj.sessions, func(x, y *paneGroup) int {
			return -cmp.Compare(btoi(x.needsInput()), btoi(y.needsInput()))
		})
		n, waiting := proj.count()
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
		for _, g := range proj.sessions {
			slices.SortStableFunc(g.panes, func(x, y pane) int { return -cmp.Compare(btoi(x.needsInput()), btoi(y.needsInput())) })
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
				name := stateGlyph(p) + " " + names[p.ID]
				if p.SideVim != "" {
					name += " v"
				}
				if p.SideTerm != "" {
					name += " t"
				}
				use := p.usageCell()
				view.usage = view.usage || use != ""
				view.nodes = append(view.nodes, ochazuke.TreeNode{ID: p.rowID(), Parent: g.id, Label: p.label(), Noun: "pane", Data: p, Cells: []string{name, p.stateCell(now), use}})
				view.order = append(view.order, p.rowID())
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
