package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
)

// The panes page's detail: tabs, the follow and refresh timers, the live
// leader keys, and the rename, move and session verbs.

// Detail tabs.
const (
	tabScreen = iota
	tabActivity
	tabTranscript
	tabTurns
)

var tabNames = []string{"Screen", "Activity", "Transcript", "Turns"}

// sideBySide is the width from which the detail sits right of the tree.
const sideBySide = 100

// selectedRow is the tree's selected row and its instance.
func (a *App) selectedRow() (string, *ochazuke.Instance) {
	n, ok := a.pv.tree.Selected()
	if !ok {
		return "", nil
	}
	return n.ID, a.r.Instances[n.ID]
}

// panesUpdate runs after every message: it notices a detached terminal,
// arms the refresh timer while the page is open, and sets the follow timer
// when the selection or tab changed, so the detail loads after a pause and
// never on the key itself.
func (a *App) panesUpdate(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	switch v := msg.(type) {
	case followDue:
		if v.seq == a.pv.seq {
			a.loadDetail()
		}
	case refreshDue:
		a.pv.armed = false
		if a.r.Page == panesPage {
			if err := a.reloadPanes(); err != nil {
				a.paneErr = err.Error()
			}
			if a.pv.tab == tabScreen && a.r.Terminal(paneTermID) == nil {
				a.loadDetail()
			}
		}
	case daemonChanged:
		if err := a.reloadPanes(); err != nil {
			a.paneErr = err.Error()
		}
		cmds = append(cmds, a.waitEvent())
	}
	if a.attached.ID != "" && a.r.Terminal(paneTermID) == nil {
		// A direct terminal.detach also releases the secondary stream.
		if err := a.closeSideTerminal(); err != nil {
			a.paneErr = err.Error()
		}
		a.attached, a.pv.zoom, a.pv.followed = pane{}, false, ""
	}
	if a.r.Page == panesPage && !a.pv.armed && a.pv.after != nil {
		a.pv.armed = true
		cmds = append(cmds, a.pv.after(refreshEvery, refreshDue{}))
	}
	row, _ := a.selectedRow()
	if key := fmt.Sprintf("%s|%d", row, a.pv.tab); row != "" && key != a.pv.followed && a.r.Terminal(paneTermID) == nil {
		a.pv.followed = key
		a.pv.seq++
		if a.pv.after != nil {
			cmds = append(cmds, a.pv.after(followDelay, followDue{seq: a.pv.seq}))
		}
	}
	if pending := a.r.PendingCommands(); pending != nil {
		cmds = append(cmds, pending)
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// loadDetail fills the selected row's tab: a capture, the activity, the
// transcript or the turns.
func (a *App) loadDetail() {
	row, inst := a.selectedRow()
	if inst == nil || inst.Noun != "pane" {
		return
	}
	verb := map[int]string{tabScreen: "preview", tabActivity: "activity", tabTranscript: "transcript"}[a.pv.tab]
	if a.pv.tab == tabTurns {
		if err := a.showTurns(row); err != nil {
			a.paneErr = err.Error()
		}
		return
	}
	if _, err := a.r.Execute(ochazuke.Request{Verb: verb, Noun: "pane", Target: row}); err != nil {
		a.m.Status = err.Error()
	}
}

// paneTab is panes.tab: the next or previous detail tab, loaded at once.
func (a *App) paneTab(ctx ochazuke.Ctx) ochazuke.Result {
	a.pv.tab = (a.pv.tab + ochazuke.IntArg(ctx.Args, "delta", 1) + len(tabNames)) % len(tabNames)
	a.r.Instances[turnsTreeID+"/list"].Unfocusable = a.pv.tab != tabTurns
	row, _ := a.selectedRow()
	a.pv.followed = fmt.Sprintf("%s|%d", row, a.pv.tab)
	a.loadDetail()
	return ochazuke.Done(tabNames[a.pv.tab])
}

// nextWaiting is panes.waiting: select the next pane that needs input,
// after the selected one, wrapping once.
func (a *App) nextWaiting(ochazuke.Ctx) ochazuke.Result {
	row, _ := a.selectedRow()
	order := a.pv.view.order
	start := slices.Index(order, row)
	for i := 1; i <= len(order); i++ {
		id := order[(start+i+len(order))%len(order)]
		if p, err := a.paneAt(a.r.Instances[id]); err == nil && p.needsInput() {
			if err := a.pv.tree.Select(id); err != nil {
				return ochazuke.Failed(err)
			}
			return ochazuke.Done(id)
		}
	}
	return ochazuke.Done(nil)
}

// paneActivity is pane.activity: the agent's activity off the loop.
func (a *App) paneActivity(ctx ochazuke.Ctx) ochazuke.Result {
	return a.paneText(ctx, tabActivity, func(c context.Context, p pane) (string, error) { return a.panesd.Activity(c, p) })
}

// paneTranscript is pane.transcript: the agent's transcript off the loop.
func (a *App) paneTranscript(ctx ochazuke.Ctx) ochazuke.Result {
	return a.paneText(ctx, tabTranscript, func(c context.Context, p pane) (string, error) { return a.panesd.Transcript(c, p) })
}

func (a *App) paneText(ctx ochazuke.Ctx, tab int, read func(context.Context, pane) (string, error)) ochazuke.Result {
	p, err := a.paneAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	key := fmt.Sprintf("%s|%d", p.rowID(), tab)
	return ochazuke.Pending(func(c context.Context) (any, error) {
		if !p.isAgent() {
			return "a shell has no agent " + strings.ToLower(tabNames[tab]), nil
		}
		text, err := read(c, p)
		if err != nil {
			return "unavailable: " + err.Error(), nil
		}
		if strings.TrimSpace(text) == "" {
			text = "nothing yet"
		}
		return text, nil
	}).ReplacingAt(paneListID, "detail").Then(func(_ ochazuke.Ctx, v any, err error) {
		if text, ok := v.(string); ok && err == nil {
			a.pv.text[key] = text
		}
	})
}

// killSession is session.kill, after its confirm.
func (a *App) killSession(ctx ochazuke.Ctx) ochazuke.Result {
	s, err := a.sessionAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	d := a.panesd
	return ochazuke.Pending(func(c context.Context) (any, error) {
		panes, err := d.ListPanes(c)
		if err != nil {
			return nil, err
		}
		for _, p := range panes {
			if p.Session == s.Session {
				if err := a.checkCompanions(c, p.ID); err != nil {
					return nil, err
				}
			}
		}
		if err := d.KillSession(c, s.Session); err != nil {
			return nil, fmt.Errorf("kill session %s: %w", s.Session, err)
		}
		return d.ListPanes(c)
	}).Then(func(_ ochazuke.Ctx, v any, err error) {
		if err == nil {
			if a.attached.Session == s.Session {
				if err := a.closeTerminal(); err != nil {
					a.m.Notify(err.Error())
				}
			}
			panes, _ := v.([]pane)
			a.setPanes(panes, nil)
		}
	})
}

// attachSession is session.attach: its first pane, live.
func (a *App) attachSession(ctx ochazuke.Ctx) ochazuke.Result {
	if ctx.Instance == nil {
		return ochazuke.Failed(errors.New("select a session"))
	}
	for _, n := range a.pv.tree.Nodes() {
		if n.Parent == ctx.Instance.ID && strings.HasPrefix(n.ID, "pane:") {
			_, err := a.r.Execute(ochazuke.Request{Verb: "attach", Noun: "pane", Target: n.ID})
			if err != nil {
				return ochazuke.Failed(err)
			}
			return ochazuke.Done(n.ID)
		}
	}
	return ochazuke.Failed(errors.New("the session has no pane; n opens a shell"))
}

// movePane is pane.move: with project or scratch it associates the pane at
// once; without, a chooser of projects asks (unassociated last).
func (a *App) movePane(ctx ochazuke.Ctx) ochazuke.Result {
	return a.moveTo(ctx, "pane")
}

// moveSession is session.move, the same for a session.
func (a *App) moveSession(ctx ochazuke.Ctx) ochazuke.Result {
	return a.moveTo(ctx, "session")
}

const moveChooser = "move-chooser"

func (a *App) moveTo(ctx ochazuke.Ctx, noun string) ochazuke.Result {
	if ctx.Instance == nil {
		return ochazuke.Failed(fmt.Errorf("select a %s", noun))
	}
	row := ctx.Instance.ID
	associate := func(args ochazuke.Arguments) ochazuke.Result {
		out, err := a.r.Execute(ochazuke.Request{Verb: "associate", Noun: noun, Target: row, Args: args})
		if err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(out)
	}
	_, hasProject := ctx.Args["project"]
	if scratch, _ := ctx.Args["scratch"].(bool); scratch || hasProject {
		return associate(ctx.Args)
	}
	choices := make([]ChooserRow, 0, len(a.projects)+1)
	for _, p := range a.projects {
		choices = append(choices, ChooserRow{ID: p.ID, Label: projectName(p), Data: p.ID})
	}
	choices = append(choices, ChooserRow{ID: "scratch", Label: "unassociated", Trailing: true})
	_, err := a.choosers.Open(ChooserSpec{
		Kind:  moveChooser,
		Title: "Move " + ctx.Instance.Label + " to",
		Rows:  choices,
		Filter: func(query string, row ChooserRow) bool {
			if row.Trailing {
				return strings.Contains(row.Label, strings.ToLower(query))
			}
			return fuzzy(query, row.Label)
		},
		Choose: func(c ChooserRow) ochazuke.Result {
			if c.ID == "scratch" {
				return associate(ochazuke.Arguments{"scratch": true})
			}
			return associate(ochazuke.Arguments{"project": c.ID})
		},
	})
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(nil)
}

// fuzzy reports whether every rune of query appears in label in order,
// ignoring case.
func fuzzy(query, label string) bool {
	label = strings.ToLower(label)
	for _, r := range strings.ToLower(query) {
		i := strings.IndexRune(label, r)
		if i < 0 {
			return false
		}
		label = label[i+len(string(r)):]
	}
	return true
}

// defineLiveKeys adds the app's live-pane actions to the terminal leader.
func (a *App) defineLiveKeys() {
	leader := ochazuke.Request{Verb: "send", Noun: "terminal", Args: ochazuke.Arguments{"keys": "ctrl+a"}}
	a.r.Define("terminal").
		Verb("zoom", a.toggleZoom, "Zoom the attached pane to the page body, or back into the detail panel").
		Verb("cycle", a.cyclePane, "Attach the next (delta 1) or previous (-1) pane of the list").
		Verb("side", a.sidePane, "Open or focus this pane's side vim or terminal").
		Verb("sideDetach", a.sideDetach, "Hide the front side, fall back to the previous side, or detach").
		Mode("terminal").
		Bind("ctrl+a", ochazuke.Enter("leader_pending", ochazuke.Payload{Prefix: "ctrl+a", Request: &leader}).Hinted("leader: v vim · t term · d close/detach · z zoom · j/k panes · ? keys")).
		Mode("leader_pending").
		Bind("v", ochazuke.Do("terminal.side", ochazuke.Arguments{"kind": "vim"})).
		Bind("t", ochazuke.Do("terminal.side", ochazuke.Arguments{"kind": "term"})).
		Bind("d", ochazuke.Do("terminal.sideDetach")).
		Bind("z", ochazuke.Do("terminal.zoom")).
		Bind("j", ochazuke.Do("terminal.cycle", ochazuke.Arguments{"delta": 1})).
		Bind("k", ochazuke.Do("terminal.cycle", ochazuke.Arguments{"delta": -1})).
		Bind("?", ochazuke.Do("bindings.help"))
	a.parameters("terminal.cycle", map[string]ochazuke.Parameter{"delta": {Type: "integer", Required: true}})
	a.parameters("terminal.side", map[string]ochazuke.Parameter{"kind": {Type: "string", Required: true, Enum: []string{"vim", "term"}}})
}

// toggleZoom is terminal.zoom.
func (a *App) toggleZoom(ochazuke.Ctx) ochazuke.Result {
	if a.r.Terminal(paneTermID) == nil {
		return ochazuke.Failed(errors.New("no pane is attached"))
	}
	a.pv.zoom = !a.pv.zoom
	return ochazuke.Done(a.pv.zoom)
}

// cyclePane is terminal.cycle: attach the neighbouring pane row, live.
func (a *App) cyclePane(ctx ochazuke.Ctx) ochazuke.Result {
	order := a.pv.view.order
	i := slices.Index(order, a.attached.rowID())
	next := i + ochazuke.IntArg(ctx.Args, "delta", 1)
	if i < 0 || next < 0 || next >= len(order) {
		return ochazuke.Done(nil) // a quiet edge
	}
	row := order[next]
	p, err := a.paneAt(a.r.Instances[row])
	if err != nil {
		return ochazuke.Failed(err)
	}
	zoom := a.pv.zoom
	if err := a.pv.tree.Select(row); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.attachLive(p, row); err != nil {
		return ochazuke.Failed(err)
	}
	a.pv.zoom = zoom // moving between panes keeps the zoom the user chose
	return ochazuke.Done(p.label())
}

// renderPanes draws the tree beside (or above, when narrow) the detail
// panel: its tabs and the selected row's tab, the attached terminal in the
// Screen tab, or, zoomed, the terminal over the whole body.
func (a *App) renderPanes(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
	s := ochazuke.NewStyles(*a.m.Appearance)
	layout := ochazuke.Layout{Width: width, Height: height, Styles: s}
	term := a.r.Terminal(paneTermID)
	if term != nil && a.pv.zoom {
		p := a.attached
		if side := a.r.Terminal(sideTermID); side != nil && side.Focused() {
			term = side
			p, _ = a.r.Instances[sideTermID].Data.(pane)
		}
		title := p.label() + " · zoomed · ctrl+a z restores"
		cw, ch := ochazuke.PanelContentSize(width, height)
		body := term.ViewBlock(cw, ch)
		if !term.Focused() {
			body.Cursor = nil
		}
		return layout.PanelBlock(title, body, width, height, term.Focused())
	}
	head := a.panesHead()
	listW, listH, detailW, detailH := width, max(1, (height-1)/3), width, 0
	side := width >= sideBySide
	if side {
		listW = max(40, width*9/20)
		detailW = width - listW - 1
		listH = height - 1
		detailH = height
	} else {
		need := max(3, len(a.pv.tree.Nodes())+1)
		listH = min(need, max(3, (height-1)/2))
		detailH = height - 1 - listH
	}
	left := a.paneListBlock(listW, listH)
	detail := a.detailBlock(layout, detailW, max(3, detailH))
	headLine := s.Muted.Render(ansi.Truncate(head, listW, "…"))
	if side {
		leftText := headLine + "\n" + left.Text
		lines := strings.Split(leftText, "\n")
		for len(lines) < height {
			lines = append(lines, "")
		}
		right := strings.Split(detail.Text, "\n")
		out := make([]string, height)
		for i := range height {
			l := lines[i]
			l += strings.Repeat(" ", max(0, listW-ansi.StringWidth(l)))
			r := ""
			if i < len(right) {
				r = right[i]
			}
			out[i] = ansi.Truncate(l, listW, "") + " " + r
		}
		block := ochazuke.Text(strings.Join(out, "\n"))
		switch {
		case left.Cursor != nil:
			block.Cursor = &ochazuke.Pos{X: left.Cursor.X, Y: left.Cursor.Y + 1}
		case detail.Cursor != nil:
			block.Cursor = &ochazuke.Pos{X: detail.Cursor.X + listW + 1, Y: detail.Cursor.Y}
		}
		return block
	}
	above := 1 + listH
	text := headLine + "\n" + left.Text + "\n" + detail.Text
	block := ochazuke.Text(text)
	switch {
	case left.Cursor != nil:
		block.Cursor = &ochazuke.Pos{X: left.Cursor.X, Y: left.Cursor.Y + 1}
	case detail.Cursor != nil:
		block.Cursor = &ochazuke.Pos{X: detail.Cursor.X, Y: detail.Cursor.Y + above}
	}
	return block
}

// panesHead is the line over the tree: the counts, a launch or listing
// problem, and what the pin hides.
func (a *App) panesHead() string {
	v := a.pv.view
	head := fmt.Sprintf("panes (%d)", v.panes)
	if v.waiting > 0 {
		head += fmt.Sprintf(" · %d need input · w next", v.waiting)
	}
	if v.hidden > 0 {
		head += fmt.Sprintf(" · %d outside the pin · S shows all", v.hidden)
	}
	if a.landErr != "" {
		head += " · " + a.landErr
	}
	return head
}

// paneListBlock is the tree, or the empty and daemon-down states, each an
// action in two short lines.
func (a *App) paneListBlock(width, height int) ochazuke.Block {
	switch {
	case a.paneErr != "":
		a.r.Placed(paneListID)
		name := "mtm's"
		if a.daemon != nil {
			name = a.daemon.Label()
		}
		return ochazuke.Text(a.fill("daemon is not running\ne starts it (daemon.ensure)\n"+name, height, width))
	case a.paneList.Len() == 0:
		a.r.Placed(paneListID)
		return ochazuke.Text(a.fill("no panes\nn opens a shell · a launches an agent from Work", height, width))
	}
	block := a.pv.tree.Block(width, height)
	lines := strings.Split(block.Text, "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	block.Text = strings.Join(lines[:height], "\n")
	return block
}

// parentDetailBlock draws the parent's tabs and live stream or selected tab.
func (a *App) parentDetailBlock(layout ochazuke.Layout, width, height int) ochazuke.Block {
	term := a.r.Terminal(paneTermID)
	row, inst := a.selectedRow()
	// The Turns tree is a stop only while it is drawn.
	if turns := a.r.Instances[turnsTreeID+"/list"]; turns != nil {
		turns.Unfocusable = term != nil || a.pv.tab != tabTurns || inst == nil || inst.Noun != "pane"
	}
	cw, ch := ochazuke.PanelContentSize(width, height)
	ch = max(1, ch-1) // the tab line
	title := "detail"
	var body ochazuke.Block
	focused := false
	switch {
	case term != nil:
		title = a.paneTitle(a.attached)
		if e := term.Err(); e != "" {
			title += ": " + e
		}
		body = term.ViewBlock(cw, ch)
		focused = term.Focused()
		if !focused {
			body.Cursor = nil
		}
	case inst == nil:
		body = ochazuke.Text("select a pane")
	case inst.Noun == "pane":
		p, _ := a.paneAt(inst)
		title = a.paneTitle(p)
		body = a.tabBody(row, cw, ch)
		focused = a.pv.tab == tabTurns && a.pv.turns.List().Focused()
	default:
		title, body = a.groupSummary(inst)
	}
	tabs := a.tabLine(term != nil)
	if term != nil || inst == nil || inst.Noun == "pane" {
		body = body.Offset(0, 1).WithText(tabs + "\n" + body.Text)
	}
	return layout.PanelBlock(ansi.Truncate(title, max(1, width-6), "…"), body, width, height, focused)
}

// tabLine names the tabs, the shown one marked.
func (a *App) tabLine(live bool) string {
	parts := make([]string, len(tabNames))
	for i, name := range tabNames {
		if i == a.pv.tab {
			name = "[" + name + "]"
		}
		parts[i] = name
	}
	line := strings.Join(parts, " ")
	if live {
		return line + " · live · ctrl+a ? keys"
	}
	return line + " · [ ] tabs · enter live"
}

// paneTitle is harness · directory · state for an agent, shell · directory
// for a shell.
func (a *App) paneTitle(p pane) string {
	home, _ := a.home()
	parts := []string{p.label(), p.kind()}
	if p.Cwd != "" {
		parts = append(parts, tildePath(p.Cwd, home))
	}
	if p.isAgent() && p.AgentState != "" && p.AgentState != "none" {
		parts = append(parts, strings.ReplaceAll(p.AgentState, "_", " "))
	}
	return strings.Join(parts, " · ")
}

// tabBody is the selected pane's tab.
func (a *App) tabBody(row string, width, height int) ochazuke.Block {
	switch a.pv.tab {
	case tabScreen:
		if a.preview.Pane == row {
			return ochazuke.Text(a.fill(a.preview.Text, height, width))
		}
		return ochazuke.Text("loading…")
	case tabTurns:
		if a.pv.turnsFor != row {
			return ochazuke.Text("loading…")
		}
		if a.pv.turns.List().Len() == 0 {
			a.r.Placed(turnsTreeID + "/list")
			return ochazuke.Text("no turns recorded yet")
		}
		return a.pv.turns.Block(width, height)
	}
	text, ok := a.pv.text[fmt.Sprintf("%s|%d", row, a.pv.tab)]
	if !ok {
		return ochazuke.Text("loading…")
	}
	lines := strings.Split(text, "\n")
	if len(lines) > height {
		lines = lines[len(lines)-height:] // the newest at the bottom
	}
	return ochazuke.Text(a.fill(strings.Join(lines, "\n"), height, width))
}

// groupSummary describes a session or project row: its panes and owner.
func (a *App) groupSummary(inst *ochazuke.Instance) (string, ochazuke.Block) {
	var lines []string
	for _, n := range a.pv.tree.Nodes() {
		if n.Parent == inst.ID {
			lines = append(lines, n.Label)
		}
	}
	switch inst.Noun {
	case "session":
		s, _ := inst.Data.(pane)
		owner, _ := a.work.SessionOwner(s.SessionID)
		head := fmt.Sprintf("%d panes · %s", len(lines), a.ownerLabel(owner.ProjectID))
		return "session " + s.Session, ochazuke.Text(head + "\n\n" + strings.Join(lines, "\n") + "\n\nenter attaches the first pane · m moves · r renames · x kills")
	}
	return inst.Label, ochazuke.Text(fmt.Sprintf("%d sessions\n\n%s", len(lines), strings.Join(lines, "\n")))
}

// events subscribes to the daemon's window, session and agent-state
// events in the background; each burst becomes one daemonChanged message
// (waitEvent), so the list follows the daemon without polling. Without a
// daemon it retries quietly.
func (a *App) subscribeEvents() tea.Cmd {
	if a.pv.subscribed || a.daemon == nil {
		return nil
	}
	a.pv.subscribed = true
	a.pv.events = make(chan struct{}, 1)
	go func() {
		types := []string{"window-created", "window-closed", "window-retitled", "agent-state", "session-created", "session-closed"}
		for {
			events, failures := a.daemon.Subscribe(context.Background(), types, 0, "")
			for range events {
				select {
				case a.pv.events <- struct{}{}:
				default:
				}
			}
			<-failures
			time.Sleep(5 * time.Second)
		}
	}()
	return a.waitEvent()
}

// waitEvent waits for the next daemon event burst.
func (a *App) waitEvent() tea.Cmd {
	if a.pv.events == nil {
		return nil
	}
	ch := a.pv.events
	return func() tea.Msg {
		<-ch
		return daemonChanged{}
	}
}
