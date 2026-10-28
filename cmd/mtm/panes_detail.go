package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

// The panes page's detail: tabs, the follow and refresh timers, the live
// leader keys, and the rename, move and session verbs.

// Detail tabs, in the order the tab row draws them and [ and ] cycle:
// Turns first, the default tab.
const (
	tabTurns = iota
	tabScreen
	tabActivity
	tabTranscript
)

var tabNames = []string{"Turns", "Screen", "Activity", "Transcript"}

// sideBySide is the width from which the detail sits right of the tree.
const sideBySide = 100

// treeWidth is the tree's share of a side-by-side page: about 30%, never
// under 32 cells, so a pane's glyph, name and short state fit.
func treeWidth(width int) int { return max(32, width*3/10) }

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
		if a.pv.liveSaved {
			// Back to the tab the attach left (the Turns tab, where the
			// row, a turn or the box asked for live).
			a.pv.tab, a.pv.liveSaved = a.pv.liveFrom, false
			if err := a.leaveHiddenTurns(); err != nil {
				a.paneErr = err.Error()
			}
			a.loadDetail()
		}
	}
	if err := a.keepHeld(); err != nil {
		a.paneErr = err.Error()
	}
	if a.r.Page == panesPage && !a.pv.armed && a.pv.after != nil {
		a.pv.armed = true
		cmds = append(cmds, a.pv.after(a.refreshInterval(), refreshDue{}))
	}
	row, _ := a.selectedRow()
	if key := fmt.Sprintf("%s|%d", row, a.pv.tab); row != "" && key != a.pv.followed && !a.liveShown() {
		a.pv.followed = key
		a.pv.seq++
		if a.pv.after != nil {
			cmds = append(cmds, a.pv.after(followDelay, followDue{seq: a.pv.seq}))
		}
	}
	a.copyUpdate(msg)
	a.syncOpenedCards()
	a.noteTurnsEntered()
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
		a.r.UI().Status(err.Error())
	}
}

// paneTab is panes.tab: the next or previous detail tab, loaded at once.
func (a *App) paneTab(ctx ochazuke.Ctx) ochazuke.Result {
	a.pv.tab = (a.pv.tab + ochazuke.IntArg(ctx.Args, "delta", 1) + len(tabNames)) % len(tabNames)
	row, _ := a.selectedRow()
	a.pv.followed = fmt.Sprintf("%s|%d", row, a.pv.tab)
	if err := a.leaveHiddenTurns(); err != nil {
		return ochazuke.Failed(err)
	}
	a.loadDetail()
	return ochazuke.Done(tabNames[a.pv.tab])
}

// leaveHiddenTurns moves the focus off the turns and the prompt box to
// the pane row they belong to when the tab no longer draws them, so the
// footer and the next key are the row's, never a stop the frame left out.
func (a *App) leaveHiddenTurns() error {
	if a.pv.tab == tabTurns || a.r.Instances[a.pv.turnsFor] == nil {
		return nil
	}
	views := []string{promptID, turnsTreeID}
	for _, tl := range a.pv.timelines {
		views = append(views, tl.ID(), tl.ID()+"/list")
	}
	for _, id := range a.r.Ancestry(a.r.Focus) {
		if slices.Contains(views, id) {
			return a.r.SetFocus(a.pv.turnsFor)
		}
	}
	return nil
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
// What it reads yields context items for the pane's owner now
// (transcriptContext), so a transcript the person reads is extracted
// without a read of its own.
func (a *App) paneTranscript(ctx ochazuke.Ctx) ochazuke.Result {
	extract := a.transcriptContext(ctx.Instance)
	return a.paneText(ctx, tabTranscript, func(c context.Context, p pane) (string, error) {
		text, err := a.panesd.Transcript(c, p)
		if err == nil {
			extract(p, text)
		}
		return text, err
	})
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
		var plain explained
		switch {
		case errors.As(err, &plain):
			return plain.text, nil
		case err != nil:
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
	}).Then(func(ctx ochazuke.Ctx, v any, err error) {
		if err == nil {
			if a.attached.Session == s.Session {
				if err := a.closeTerminal(); err != nil {
					ctx.Notify(err.Error())
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
	id := a.sessionPane(ctx.Instance.ID)
	if id == "" {
		return ochazuke.Failed(errors.New("the session has no pane; n opens a shell"))
	}
	if _, err := a.r.Execute(ochazuke.Request{Verb: "attach", Noun: "pane", Target: id}); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(id)
}

// sessionPane is the first pane row under a session row, "" for none.
func (a *App) sessionPane(session string) string {
	for _, n := range a.pv.tree.Nodes() {
		if n.Parent == session && strings.HasPrefix(n.ID, "pane:") {
			return n.ID
		}
	}
	return ""
}

// moving reports an associate call that names no owner from a key: it
// asks with the project chooser (moveTo) instead of unassociating.
func (a *App) moving(ctx ochazuke.Ctx) bool {
	if a.cli || ctx.Instance == nil {
		return false
	}
	for _, k := range []string{"project", "task", "scratch"} {
		if _, ok := ctx.Args[k]; ok {
			return false
		}
	}
	return true
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
	current, hasOwner := a.moveOwner(ctx.Instance, noun)
	choices := make([]ochazuke.ChooserRow, 0, len(a.projects)+1)
	for _, p := range a.projectsByRecency() {
		if hasOwner && p.ID == current {
			continue // moving to where it already is does nothing
		}
		choices = append(choices, ochazuke.ChooserRow{ID: p.ID, Label: projectName(p), Data: p.ID})
	}
	if !hasOwner || current != "" {
		choices = append(choices, ochazuke.ChooserRow{ID: "scratch", Label: "unassociated", Trailing: true})
	}
	_, err := a.r.OpenChooser(ochazuke.ChooserSpec{
		Kind:  moveChooser,
		Title: "Move " + ctx.Instance.Label + " to",
		Rows:  choices,
		Filter: func(query string, row ochazuke.ChooserRow) bool {
			if row.Trailing {
				return strings.Contains(row.Label, strings.ToLower(query))
			}
			return fuzzy(query, row.Label)
		},
		Choose: func(c ochazuke.ChooserRow) ochazuke.Result {
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

// moveOwner is the project a pane or session row belongs to now ("" for
// unassociated); ok is false when it cannot be told.
func (a *App) moveOwner(inst *ochazuke.Instance, noun string) (string, bool) {
	switch noun {
	case "task":
		t, err := a.task(ochazuke.Ctx{Instance: inst})
		return t.ProjectID, err == nil
	case "note":
		n, err := a.selectedNote(ochazuke.Ctx{Instance: inst})
		return n.ProjectID, err == nil
	case "saved":
		row, err := a.savedAt(ochazuke.Ctx{Instance: inst})
		return row.ProjectID, err == nil
	case actionItemNoun:
		it, err := a.actionItemAt(ochazuke.Ctx{Instance: inst})
		return cmp.Or(it.ProjectID, a.taskProject(it.TaskID)), err == nil
	}
	if noun == "pane" {
		p, err := a.paneAt(inst)
		if err != nil {
			return "", false
		}
		owner, ok := a.pv.view.owners[p.ID]
		return owner, ok
	}
	s, err := a.sessionAt(inst)
	if err != nil || s.SessionID == "" {
		return "", false
	}
	owner, err := a.work.SessionOwner(s.SessionID)
	return owner.ProjectID, err == nil
}

// projectsByRecency is the projects, the one whose directories were used
// last first; projects never used keep their order after them.
func (a *App) projectsByRecency() []work.Project {
	last := map[string]int64{}
	if recent, err := a.work.RecentDirs(); err == nil {
		for _, r := range recent {
			last[r.Path] = max(last[r.Path], r.LastUsed)
		}
	}
	used := func(p work.Project) int64 {
		var at int64
		for _, t := range p.Trees {
			at = max(at, last[t.Path])
		}
		return at
	}
	out := slices.Clone(a.projects)
	slices.SortStableFunc(out, func(x, y work.Project) int { return cmp.Compare(used(y), used(x)) })
	return out
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
		Verb("cycle", a.cyclePane, "Attach the next (delta 1) or previous (-1) pane of the list; outside live, select it").
		Verb("side", a.sidePane, "Open or focus this pane's side vim or terminal").
		Verb("sideDetach", a.sideDetach, "Hide the front side, fall back to the previous side, or detach").
		Verb("other", a.focusOther, "Move the focus to the other live terminal: the pane, then its side, wrapping").
		Verb("layout", a.flipLayout, "Flip the side pane between beside and below its parent (the side-layout setting)").
		Verb("keys", a.leaderKeys, "Show the leader keys (the terminal-leader setting, then one key) in a small modal").
		Mode("terminal").
		Bind("ctrl+a", ochazuke.Enter("leader_pending", ochazuke.Payload{Prefix: "ctrl+a", Request: &leader}).Hinted("leader: v vim · t term · d close/detach · z zoom · o other · R layout · w sides · j/k panes · p pick · s starred · [ copy · c context · f hints · N note · n task · T todos · S all · ? keys")).
		Mode("leader_pending").
		Bind("v", ochazuke.Do("terminal.side", ochazuke.Arguments{"kind": "vim"}).Hinted("side vim")).
		Bind("t", ochazuke.Do("terminal.side", ochazuke.Arguments{"kind": "term"}).Hinted("side terminal")).
		Bind("d", ochazuke.Do("terminal.sideDetach")).
		Bind("z", ochazuke.Do("terminal.zoom")).
		Bind("o", ochazuke.Do("terminal.other").Hinted("other pane")).
		Bind("R", ochazuke.Do("terminal.layout").Hinted("side layout")).
		Bind("w", ochazuke.Do("panes.sides").Hinted("sides")).
		Bind("j", ochazuke.Do("terminal.cycle", ochazuke.Arguments{"delta": 1}).Hinted("next pane")).
		Bind("k", ochazuke.Do("terminal.cycle", ochazuke.Arguments{"delta": -1}).Hinted("previous pane")).
		Bind("c", ochazuke.Do(contextNoun+".pick")).
		// The app's own capture keys, without leaving live: each opens over
		// the terminal and closing it gives the terminal its keys again.
		Bind("s", ochazuke.Do("terminal.starred").Hinted("next starred")).
		Bind("N", ochazuke.Do(quickNoteModal+".open").Hinted("quick note")).
		Bind("n", ochazuke.Do("task.new").Hinted("new task")).
		Bind("T", ochazuke.Do("todos.open").Hinted("todos")).
		Bind("S", ochazuke.Do("scope.all").Hinted("all projects")).
		Bind("?", ochazuke.Do("terminal.keys"))
	a.defineLiveHints()
	a.leaderView()
	a.parameters("terminal.cycle", map[string]ochazuke.Parameter{"delta": {Type: "integer", Required: true}})
	a.parameters("terminal.side", map[string]ochazuke.Parameter{"kind": {Type: "string", Required: true, Enum: []string{"vim", "term"}}})
}

const (
	leaderModal = "leader-keys"
	leaderList  = leaderModal + "/list"
)

// leaderWords say what each leader key does, in the order the modal
// lists them; a leader row not named here shows its operation's
// description.
var leaderWords = []struct{ key, text string }{
	{"v", "side vim: open it, focus it, or back to the pane"},
	{"t", "side terminal: open it, focus it, or back to the pane"},
	{"d", "close the front side, then detach"},
	{"z", "zoom the pane to the page, or back"},
	{"o", "the other pane: the parent, then its side (tmux's prefix o)"},
	{"R", "side layout: beside or below the pane (the side-layout setting)"},
	{"w", "sides: jump to any open side vim or terminal"},
	{"j", "next pane, live"},
	{"k", "previous pane, live"},
	{"p", "pick a pane: the ones attached before first, then attach it live"},
	{"s", "next starred pane, live (* on a pane row stars it)"},
	{"c", "context: copy, open or reveal a path, URL, PR, branch or commit"},
	{"f", "hints: label the URLs, paths, PRs, branches and commits drawn on the pane and its side when both show; a label copies, ctrl+label opens, a capital types it into its pane, esc or q cancels (f on a pane row labels its Screen tab)"},
	{"N", "quick note for the selected task, else the pin"},
	{"n", "new task under the pin"},
	{"T", "todos: the pin's tasks as an outline"},
	{"S", "all projects, or back to the pin's"},
	{"[", "copy mode: scroll back, search, select and yank; i goes live again"},
	{"?", "these keys"},
}

// leaderHelp gives each leader_pending row on the terminal noun its words
// (leaderWords) as its Help, rebinding it with them, so ? help over a live
// pane, which lists the leader's keys under the leader (ochazuke R6.10),
// says what each does as the leader modal does. A row with a Help of its
// own keeps it. Run once every leader row is bound.
func (a *App) leaderHelp() error {
	n := a.r.Noun("terminal")
	if n == nil {
		return nil
	}
	words := map[string]string{}
	for _, w := range leaderWords {
		words[w.key] = w.text
	}
	var rows []ochazuke.Binding
	for _, b := range n.Bindings["leader_pending"] {
		if text, ok := words[b.Keys]; ok && b.Help == "" && !b.Unbind {
			b.Help = text
			rows = append(rows, b)
		}
	}
	return a.r.Bind(rows...)
}

// leaderColumns are the leader modal's table, as ? help draws a key
// sequence: the leader (a Group column, so it reads once, on the first
// row), the key after it, and what it does, wrapped inside its column.
// No titles, so no header line.
var leaderColumns = []ochazuke.Column{
	{Group: true, Pinned: true},
	{Pinned: true},
	{Wrap: true},
}

// leaderKeys is terminal.keys (leader ?): the leader table's rows in a
// small modal, leader, key and what it does; esc closes it and the pane
// stays live. The rows come from the leader_pending bindings, so a rebound key
// shows as bound, and every row names the leader as the terminal-leader
// setting has it (r.TerminalLeader()).
func (a *App) leaderKeys(ochazuke.Ctx) ochazuke.Result {
	leader := a.r.TerminalLeader()
	words := append(slices.Clone(leaderWords), struct{ key, text string }{leader, "send " + leader + " to the pane"})
	bound := map[string]ochazuke.Binding{}
	var order []string
	if n := a.r.Noun("terminal"); n != nil {
		for _, b := range n.Bindings["leader_pending"] {
			if b.Keys == "" || b.Hidden || b.Unbind {
				continue
			}
			if _, seen := bound[b.Keys]; !seen {
				order = append(order, b.Keys)
			}
			bound[b.Keys] = b
		}
	}
	var items []ochazuke.ListItem
	add := func(key, text string) {
		items = append(items, ochazuke.ListItem{ID: leaderModal + "/" + key, Noun: "binding", Label: leader + " " + key + "  " + text, Cells: []string{leader, key, text}})
		delete(bound, key)
	}
	for _, w := range words {
		if _, ok := bound[w.key]; ok {
			add(w.key, w.text)
		}
	}
	for _, key := range order {
		if b, ok := bound[key]; ok {
			add(key, a.r.Operations[a.r.CanonicalKey(b.Request.Key())].Description)
		}
	}
	if _, err := a.r.OpenModal(leaderModal, ochazuke.Payload{}); err != nil {
		return ochazuke.Failed(err)
	}
	list, err := a.r.AddList(leaderList, items, ochazuke.Parent(leaderModal), ochazuke.Columns(leaderColumns...))
	if err != nil {
		_ = a.r.Pop()
		return ochazuke.Failed(err)
	}
	return resultError(list.Focus())
}

// leaderView draws the leader keys modal.
func (a *App) leaderView() {
	a.m.ModalViews[leaderModal] = ochazuke.ModalView{Title: "Leader keys · esc closes", Render: func(r *ochazuke.Runtime, _ *ochazuke.Instance, w, h int) ochazuke.Block {
		if l := r.List(leaderList); l != nil {
			return ochazuke.Text(l.View(w, h))
		}
		return ochazuke.Text("")
	}}
}

// toggleZoom is terminal.zoom.
func (a *App) toggleZoom(ochazuke.Ctx) ochazuke.Result {
	if a.r.Terminal(paneTermID) == nil {
		return ochazuke.Failed(errors.New("no pane is attached"))
	}
	a.pv.zoom = !a.pv.zoom
	return ochazuke.Done(a.pv.zoom)
}

// cyclePane is terminal.cycle: attach the neighbouring pane row, live;
// outside live, select it (panes_leader.go).
func (a *App) cyclePane(ctx ochazuke.Ctx) ochazuke.Result {
	if !a.inLive() {
		row, _ := a.selectedRow()
		return a.selectPane(a.paneNear(row, ochazuke.IntArg(ctx.Args, "delta", 1)))
	}
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
	if a.liveShown() && a.pv.zoom {
		p := a.attached
		if side := a.r.Terminal(sideTermID); side != nil && side.Focused() {
			term = side
			p, _ = a.r.Instances[sideTermID].Data.(pane)
		}
		title := a.streamTitle(term.ID(), p.label()) + " · zoomed · " + a.r.TerminalLeader() + " z restores"
		cw, ch := ochazuke.PanelContentSize(width, height)
		if a.copy.active {
			return layout.PanelBlock(a.copyTitle()+" · zoomed", a.copyBlock(cw, ch), width, height, true)
		}
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
		listW = treeWidth(width)
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
		head += fmt.Sprintf(" · %d need input", v.waiting)
		if k := a.keyFor(a.r.Focus, "panes.waiting"); k != "" {
			head += " · " + k + " next"
		}
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
		return ochazuke.Text(a.fill("no panes\n"+a.keySentence(paneListID, keyAction{"pane.new", "opens a shell"}, keyAction{"agent.choose", "launches an agent"}), height, width))
	}
	block := a.pv.tree.Block(width, height)
	lines := strings.Split(block.Text, "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	block.Text = strings.Join(lines[:height], "\n")
	return block
}

// liveShown reports whether the detail draws the attached terminal: on
// the Screen tab, or while the focus is in it or its side. On the
// Activity, Transcript and Turns tabs an attached terminal stays attached
// but is not drawn, so it is no focus stop (ochazuke skips a stop the
// last frame did not place) and moving into the detail reaches the
// tab's content, never the hidden live pane.
func (a *App) liveShown() bool {
	term := a.r.Terminal(paneTermID)
	if term == nil {
		return false
	}
	if a.pv.tab == tabScreen || term.Focused() {
		return true
	}
	side := a.r.Terminal(sideTermID)
	return side != nil && side.Focused()
}

// parentDetailBlock draws the parent's tabs and live stream or selected tab.
func (a *App) parentDetailBlock(layout ochazuke.Layout, width, height int) ochazuke.Block {
	term := a.r.Terminal(paneTermID)
	live := a.liveShown()
	row, inst := a.selectedRow()
	cw, ch := ochazuke.PanelContentSize(width, height)
	ch = max(1, ch-2) // the header and tab lines
	title := "detail"
	header := ""
	var body ochazuke.Block
	focused := false
	if live || a.copyShown(paneTermID) || inst == nil || inst.Noun != "pane" {
		a.detail.panes.hide() // tabBody windows the detail when it draws
	}
	switch {
	case a.copyShown(paneTermID):
		title = a.copyTitle()
		header = a.paneHeader(a.copy.pane)
		body = a.copyBlock(cw, ch)
		focused = true
	case live:
		title = a.streamTitle(paneTermID, a.attached.label())
		header = a.paneHeader(a.attached)
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
		title = p.label()
		header = a.paneHeader(p)
		if a.pv.tab == tabScreen {
			a.r.Placed(copyID) // l, } and tab reach copy mode from the list
		}
		body = a.tabBody(row, cw, ch)
		// The turns or the prompt box below them: both are the panel's.
		focused = a.pv.tab == tabTurns && (a.turnsList().Focused() || a.r.Focus == promptID)
	default:
		title, body = a.groupSummary(inst)
	}
	tabs := a.tabLine(live, a.r.Instances[a.r.Focus] != nil && a.r.Instances[a.r.Focus].Noun == "pane")
	if a.copyShown(paneTermID) {
		tabs = a.copyTabLine()
	}
	switch {
	case header != "":
		header = layout.Styles.Muted.Render(ansi.Truncate(header, cw, "…"))
		body = body.Offset(0, 2).WithText(header + "\n" + tabs + "\n" + body.Text)
	case live || inst == nil || inst.Noun == "pane":
		body = body.Offset(0, 1).WithText(tabs + "\n" + body.Text)
	}
	return layout.PanelBlock(ansi.Truncate(title, max(1, width-6), "…"), body, width, height, focused)
}

// tabLine names the tabs, the shown one marked; "enter live" only while
// the focus is a pane row, where enter attaches it (in the Turns tree
// enter opens a turn).
func (a *App) tabLine(live, onPane bool) string {
	parts := make([]string, len(tabNames))
	for i, name := range tabNames {
		if i == a.pv.tab {
			name = "[" + name + "]"
		}
		parts[i] = name
	}
	line := strings.Join(parts, " ")
	if live {
		return line + " · live · " + a.r.TerminalLeader() + " ? keys"
	}
	if onPane {
		return line + " · [ ] tabs · enter live"
	}
	return line + " · [ ] tabs"
}

// paneHeader is the detail's first line, the pane's facts. A shell's is
// shell · cwd. An agent's doubles as its status line: harness · model ·
// effort · permission mode · context used · cost · cwd · state · age, each
// from what mtm can know (the agent's tuios metadata first, then the
// launch recipe) and left out when nothing says it. The owner follows when
// the pin leaves the pane out. Every tab shows it; none repeats it.
func (a *App) paneHeader(p pane) string {
	home, _ := a.home()
	harness := p.kind()
	if p.Harness == "" && p.LaunchHarness != "" {
		harness = p.LaunchHarness
	}
	parts := []string{harness}
	if p.isAgent() || p.LaunchHarness != "" {
		parts = append(parts, p.statusFields()...)
	}
	if p.Cwd != "" {
		parts = append(parts, tildePath(p.Cwd, home))
	}
	switch {
	case p.Exited:
		parts = append(parts, "exited")
		if k := a.keyFor(p.rowID(), "pane.kill"); k != "" {
			parts[len(parts)-1] += " · " + k + " closes it"
		}
	case p.isAgent():
		parts = append(parts, p.stateWord())
		if p.StateAt > 0 {
			parts = append(parts, age(time.Since(time.UnixMilli(p.StateAt))))
		}
	}
	if owner, ok := a.pv.view.owners[p.ID]; ok && !a.inScope(owner) {
		parts = append(parts, "owner "+a.ownerLabel(owner))
	}
	return strings.Join(parts, " · ")
}

// statusFields are an agent's status-line facts in order: model, effort,
// permission mode, context used and cost, each only when something states
// it. The daemon reports model, context and cost (tuios metadata, fed by
// the harness's status line or a protocol pane); no feed reports effort or
// the permission mode, so those come only from the launch recipe, the
// flags mtm started the agent with, as does the model when the daemon has
// none.
func (p pane) statusFields() []string {
	var out []string
	if model := cmp.Or(p.Meta["model"], p.Model); model != "" {
		out = append(out, model)
	}
	if effort := launchFlag(p.LaunchFlags, "--effort"); effort != "" {
		out = append(out, "effort "+effort)
	}
	perm := launchFlag(p.LaunchFlags, "--permission-mode")
	if perm == "" && slices.Contains(p.LaunchFlags, "--dangerously-skip-permissions") {
		perm = "bypassPermissions"
	}
	if perm != "" {
		out = append(out, "perms "+perm)
	}
	if ctx := p.Meta["context"]; ctx != "" {
		out = append(out, "ctx "+ctx)
	}
	if cost := p.costText(); cost != "" {
		out = append(out, cost)
	}
	return out
}

// costText is the daemon's cost as written ("$1.20"), "cost 1.2" when a
// feed wrote a bare number; "" when none reported it.
func (p pane) costText() string {
	cost := p.Meta["cost"]
	if cost != "" && !strings.HasPrefix(cost, "$") {
		cost = "cost " + cost
	}
	return cost
}

// usageCell is the status line's compact form for the tree: context used
// and cost, as the daemon reports them ("42% $1.20"); "" for a shell or
// an agent nothing has reported on.
func (p pane) usageCell() string {
	if !p.isAgent() || p.Exited {
		return ""
	}
	var parts []string
	if ctx := p.Meta["context"]; ctx != "" {
		parts = append(parts, ctx)
	}
	if cost := p.costText(); cost != "" {
		parts = append(parts, cost)
	}
	return strings.Join(parts, " ")
}

// launchFlag is the value of flag in argv, given as "--flag v" or
// "--flag=v"; "" when absent.
func launchFlag(argv []string, flag string) string {
	for i, arg := range argv {
		if v, ok := strings.CutPrefix(arg, flag+"="); ok {
			return v
		}
		if arg == flag && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

// screenWindow is what the Screen tab just drew of row's preview text:
// which lines (the detail's offset, and every drawn line but a position
// marker) at what width.
func (a *App) screenWindow(row, text, drawn string, width, height int) screenWindow {
	d := a.detail.panes.d
	shown := min(height, len(strings.Split(text, "\n")))
	if text == "" {
		shown = 0
	}
	if d.Overflows() {
		shown = height - 1
	}
	return screenWindow{row: row, text: drawn, top: d.Offset(), shown: max(0, shown), width: width, previewText: text}
}

// tabBody is the selected pane's tab.
func (a *App) tabBody(row string, width, height int) ochazuke.Block {
	switch a.pv.tab {
	case tabScreen:
		if a.preview.Pane == row {
			// The screen is not wrapped: its lines are cut to the panel,
			// and it opens on its last line holding text.
			text := strings.ReplaceAll(trimBlankTail(a.preview.Text), "\t", "    ")
			drawn := a.detail.panes.lines(text, 0, width, height)
			if sc := a.previewHints(row); sc != nil {
				// While hints are up the window they labelled stays put.
				return ochazuke.Text(overlayText(sc.win.text, sc.win.width, sc.win.shown, sc.overlay, ochazuke.NewStyles(*a.m.Appearance).Backdrop))
			}
			a.pv.screen = a.screenWindow(row, text, drawn, width, height)
			return ochazuke.Text(drawn)
		}
		a.detail.panes.hide()
		return ochazuke.Text("loading…")
	case tabTurns:
		if a.pv.turnsFor != row {
			a.detail.panes.hide()
			return ochazuke.Text("loading…")
		}
		// An agent's turns end in the prompt box (prompt_box.go), pinned
		// to the bottom, drawn after the tree so } and tab reach it next.
		if p := a.promptPane(); p != nil && height > 3 {
			boxH := a.promptBoxHeight()
			turns := a.turnsBody(width, height-boxH)
			return stackBlocks(turns, height-boxH, a.promptBoxBlock(*p, width))
		}
		return a.turnsBody(width, height)
	}
	text, ok := a.pv.text[fmt.Sprintf("%s|%d", row, a.pv.tab)]
	if !ok {
		a.detail.panes.hide()
		return ochazuke.Text("loading…")
	}
	// Long lines wrap to the panel, so an explanation or a reply reads
	// whole; the newest stay at the bottom until K scrolls back.
	return ochazuke.Text(a.detail.panes.lines(text, max(1, width), width, height))
}

// turnsBody is the Turns tab's tree at width x height.
func (a *App) turnsBody(width, height int) ochazuke.Block {
	if a.turnCards() {
		return a.cardsBody(width, height)
	}
	a.wrapTurns(a.pv.turns, width)
	list := a.pv.turns.List()
	st := a.turnFillOf(a.pv.turns)
	if list.Len() == 0 {
		a.detail.panes.hide()
		a.r.Placed(turnsTreeID + "/list")
		return ochazuke.Text(a.noTurnsText(st))
	}
	if list.Focused() || a.r.Focus == promptID {
		// In the tree the selection moves and the tree keeps it in
		// view; J and K scroll it only from the pane list. The prompt
		// box below keeps the tree's window too: j from the last turn
		// into the box must not jump the turns back to their top.
		a.detail.panes.hide()
		block := a.pv.turns.Block(width, height)
		if st.model {
			block = block.WithText(a.styleTurnRows(block.Text))
		}
		return block
	}
	text := a.pv.turns.Block(width, list.Len()).Text
	if st.model {
		text = a.styleTurnRows(text)
	}
	return ochazuke.Text(a.detail.panes.lines(text, 0, width, height))
}

// stackBlocks puts bottom under top, top cut or padded to height lines;
// the cursor is whichever block holds one, moved to where it lands.
func stackBlocks(top ochazuke.Block, height int, bottom ochazuke.Block) ochazuke.Block {
	lines := strings.Split(strings.TrimRight(top.Text, "\n"), "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	lines = lines[:height]
	out := ochazuke.Block{Text: strings.Join(lines, "\n") + "\n" + bottom.Text}
	switch {
	case top.Cursor != nil && top.Cursor.Y < height:
		out.Cursor = top.Cursor
	case bottom.Cursor != nil:
		out.Cursor = bottom.Offset(0, height).Cursor
	}
	return out
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
		head := plural(len(lines), "pane") + " · " + a.ownerLabel(owner.ProjectID)
		keys := a.keySentence(inst.ID, keyAction{"session.attach", "attaches the first pane"}, keyAction{"session.move", "moves"}, keyAction{"session.rename", "renames"}, keyAction{"session.kill", "kills"})
		if len(lines) == 0 {
			keys = "no panes · " + a.keySentence(inst.ID, keyAction{"pane.new", "opens a shell"}, keyAction{"agent.choose", "launches an agent"}, keyAction{"session.move", "moves"}, keyAction{"session.rename", "renames"}, keyAction{"session.kill", "kills"})
		}
		return "session " + s.Session, ochazuke.Text(head + "\n\n" + strings.Join(lines, "\n") + "\n\n" + keys)
	}
	return inst.Label, ochazuke.Text(plural(len(lines), "session") + "\n\n" + strings.Join(lines, "\n"))
}

// plural is "1 pane", "2 panes".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
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
		types := []string{"window-created", "window-closed", "window-exit", "window-retitled", "agent-state", "session-created", "session-closed", "command-started", "command-finished"}
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
