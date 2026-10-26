package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
)

// The panes page: the pane tree on the left (panes_tree.go) and the
// detail on the right (stacked under it when narrow), whose tabs are
// Screen, Activity, Transcript and Turns (panes_detail.go). The Screen tab
// follows the selection after a short pause: pane.preview captures the
// screen off the loop in the "preview" slot scoped to the list, so a newer
// preview of any row replaces the older. pane.attach (enter) adds a
// framework terminal in the detail panel fed by the daemon's stream-pane
// and focuses it; the terminal noun owns passthrough and the ctrl+a
// leader, to which mtm adds z (zoom the pane to the page body, off on every
// attach), j/k (the next or previous pane, live) and ? (the keys). The
// terminal is added with CloseOnDetach, so detach removes it, releases the
// stream's size lease and returns the focus to the row. The list reloads
// on daemon events and every few seconds while the page is open, off the
// loop, so no key waits on the daemon.

const (
	panesPage     = "panes"
	paneListID    = paneTreeID + "/list"
	paneTermID    = "panes/terminal"
	killConfirm   = "Kill pane {{label}}?"
	daemonTimeout = 5 * time.Second
)

// paneColumns are the pane noun's table (the catalog's columns, so a host
// mounting pane rows draws them too): the pane, its project, the agent
// state and the working directory; narrow, project drops first, then cwd,
// then state, and the pane keeps at least 16 cells. Session rows fill pane
// and project.
var paneColumns = []ochazuke.Column{
	{Title: "pane", Min: 16},
	{Title: "project", Priority: 3},
	{Title: "state", Priority: 1},
	{Title: "cwd", Min: 12, Priority: 2},
}

// paneCells is a pane row's cells in paneColumns order; home shortens the
// cwd to ~.
func paneCells(p pane, owner, home string) []string {
	return []string{p.label(), owner, p.AgentState, tildePath(p.Cwd, home)}
}

// sessionCells is a session row's cells in paneColumns order.
func sessionCells(p pane, owner string) []string {
	return []string{"session " + shortSession(p.Session), owner, "", ""}
}

// tildePath writes a path under home with ~ for home.
func tildePath(path, home string) string {
	if home == "" || path == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, strings.TrimSuffix(home, "/")+"/"); ok {
		return "~/" + rest
	}
	return path
}

// pane is one row: a window, the session it lives in and, when an agent
// runs in it, the agent (list-agents: harness, state time, needs you).
type pane struct {
	Session           string
	SessionID         string
	OwnerUnsaved      bool
	Harness           string `json:"harness,omitempty"`
	StateAt           int64  `json:"state_at,omitempty"`
	NeedsYou          bool   `json:"needs_you,omitempty"`
	SideVim, SideTerm string
	ShowVim, ShowTerm bool
	SideFront         string
	daemon.Pane
}

// rowID is the pane's list row instance id; window ids are uuids, the
// prefix keeps them apart from project and task rows.
func (p pane) rowID() string { return "pane:" + p.Session + "/" + p.ID }

// label is the row text, the panel title and the {{label}} a confirm
// prompt reads. A uuid-named session shows its first 8 characters.
func (p pane) label() string {
	name := p.Name
	badges := ""
	if p.SideVim != "" {
		badges += " v"
	}
	if p.SideTerm != "" {
		badges += " t"
	}
	if name == "" || defaultWindowName.MatchString(name) {
		name = p.baseName()
	}
	if name == "" {
		name = p.ID
	}
	return shortSession(p.Session) + "/" + name + badges
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// shortSession is a session name for display: a uuid's first 8
// characters, any other name in full.
func shortSession(name string) string {
	if uuidPattern.MatchString(name) {
		return name[:8]
	}
	return name
}

// paneDaemon is the part of the daemon the panes page needs; tests replace it.
type paneDaemon interface {
	ListPanes(ctx context.Context) ([]pane, error)
	CapturePane(ctx context.Context, p pane) (string, error)
	KillPane(ctx context.Context, p pane) error
	KillSession(ctx context.Context, session string) error
	Activity(ctx context.Context, p pane) (string, error)
	Transcript(ctx context.Context, p pane) (string, error)
	OpenStream(p pane) paneStream
}

// paneStream is a terminal stream the terminal releases when it closes:
// Release drops the size lease and closes the connection.
type paneStream interface {
	ochazuke.TerminalStream
	ochazuke.TerminalReleaser
}

// preview is the last captured screen and the pane it came from.
type preview struct {
	Pane string
	Text string
}

// paneUI is the panes page's own state (the App holds one).
type paneUI struct {
	tree     *ochazuke.Tree
	view     paneView
	tab      int    // the detail tab: tabScreen, tabActivity, tabTranscript, tabTurns
	zoom     bool   // the attached pane fills the page body; off on every attach
	followed string // the row the detail last followed
	seq      uint64 // the follow timer's generation
	armed    bool   // the refresh timer is set
	// after schedules msg after d; nil (tests) schedules nothing.
	after      func(d time.Duration, msg tea.Msg) tea.Cmd
	text       map[string]string // row + tab → the Activity or Transcript text
	lastState  map[string]string // window → the agent state at the last listing
	turns      *ochazuke.Tree
	turnsFor   string // the pane row the Turns tree shows
	events     chan struct{}
	subscribed bool
}

// Timers of the panes page.
const (
	followDelay  = 150 * time.Millisecond // the detail follows the selection after this pause
	refreshEvery = 3 * time.Second        // the list reloads this often while the page is open
)

// followDue is the follow timer: when seq is still current, the detail
// loads the selected row's tab.
type followDue struct{ seq uint64 }

// refreshDue is the reload timer of the open panes page.
type refreshDue struct{}

// daemonChanged is a daemon event about windows, sessions or agent state.
type daemonChanged struct{}

// definePanes adds the pane noun, the session rows' verbs and the panes
// noun (refresh).
func (a *App) definePanes() {
	r := a.r
	a.pv = &paneUI{text: map[string]string{}, lastState: map[string]string{}, after: func(d time.Duration, msg tea.Msg) tea.Cmd {
		return tea.Tick(d, func(time.Time) tea.Msg { return msg })
	}}
	r.Define("pane").
		Columns(paneColumns...).
		Verb("attach", a.attachPane, "Attach the pane live in the detail panel; ctrl+a d detaches, ctrl+a z zooms").
		Verb("preview", a.previewPane, "Capture the pane's screen into the detail's Screen tab").Quiet().
		Verb("kill", a.killPane, "Close the pane's window").Confirm(killConfirm).
		Verb("move", a.movePane, "Move the pane to a project (a chooser; unassociated last)").
		Verb("activity", a.paneActivity, "Read what the agent in the pane has been doing into the Activity tab").Quiet().
		Verb("transcript", a.paneTranscript, "Read the agent's transcript into the Transcript tab").Quiet().
		Mode("normal").
		Bind("enter", ochazuke.Do("pane.attach")).
		Bind("x", ochazuke.Do("pane.kill")).
		Bind("m", ochazuke.Do("pane.move"))
	r.Define("session").
		Verb("attach", a.attachSession, "Attach the session's first pane").
		Verb("move", a.moveSession, "Move the session to a project (a chooser; unassociated last)").
		Verb("kill", a.killSession, "Close the session and every pane in it").Confirm("Kill {{label}} and its panes?").
		Mode("normal").
		Bind("enter", ochazuke.Do("session.attach")).
		Bind("m", ochazuke.Do("session.move")).
		Bind("x", ochazuke.Do("session.kill"))
	r.Define("pane-project").Mode("normal").Bind("enter", ochazuke.Do("outline.fold"))
	// List reloads are bookkeeping: Quiet keeps them off the jobs ticker.
	r.Define("panes").
		Verb("refresh", a.refreshPanes, "Reload the pane list from the daemon").Quiet().
		Verb("tab", a.paneTab, "Show the next (delta 1) or previous (-1) detail tab: Screen, Activity, Transcript, Turns").
		Verb("waiting", a.nextWaiting, "Select the next pane that needs input")
	a.parameters("panes.tab", map[string]ochazuke.Parameter{"delta": {Type: "integer", Required: true}})
	a.parameters("pane.move", map[string]ochazuke.Parameter{"project": {Type: "string", Source: "projects.list", Description: "The project id; omitted, a chooser asks"}, "scratch": {Type: "boolean", Description: "Unassociate it"}})
	a.parameters("session.move", map[string]ochazuke.Parameter{"project": {Type: "string", Source: "projects.list", Description: "The project id; omitted, a chooser asks"}, "scratch": {Type: "boolean", Description: "Unassociate it"}})
	r.Define("daemon").Verb("ensure", a.ensureVerb, "Start mtm's TUIOS server when it is not running, then reload the pane list").Quiet()
	a.defineLiveKeys()
	a.ownedBy("panes", paneListID)
	a.ownedBy("daemon", paneListID)
}

// ownedBy records that noun has no instance of its own and its verbs act
// for instance (their jobs are scoped to it), so a CLI command of that noun
// without --target targets it.
func (a *App) ownedBy(noun, instance string) { a.owners[noun] = instance }

// addPanesPage builds the page: the tree first, so the page starts on it,
// then the Turns tree of the detail. The page's own rows switch the tabs
// and jump to a pane that needs input from any stop on it.
func (a *App) addPanesPage() error {
	a.r.Instance(panesPage, ochazuke.Kind("page"))
	tree, err := a.r.AddTree(paneTreeID, nil, ochazuke.Parent(panesPage), ochazuke.Columns(paneTreeColumns...))
	if err != nil {
		return err
	}
	a.pv.tree = tree
	a.paneList = tree.List()
	if err := a.addTurns(); err != nil {
		return err
	}
	for _, row := range []struct {
		keys string
		req  ochazuke.Binding
	}{
		{"]", ochazuke.Do("panes.tab", ochazuke.Arguments{"delta": 1})},
		{"[", ochazuke.Do("panes.tab", ochazuke.Arguments{"delta": -1})},
		{"w", ochazuke.Do("panes.waiting")},
	} {
		b := row.req
		b.Instance, b.Mode, b.Keys = panesPage, "normal", row.keys
		if err := a.r.Bind(b); err != nil {
			return err
		}
	}
	// An empty list (no panes, or the daemon is down) answers e with
	// daemon.ensure; the rows keep their own keys.
	a.r.DefineGuard("no-panes", func(*ochazuke.Runtime, ochazuke.KeyEvent) bool { return a.paneList.Len() == 0 })
	ensure := ochazuke.Do("daemon.ensure").Guard("no-panes")
	ensure.Instance, ensure.Mode, ensure.Keys = paneListID, "normal", "e"
	if err := a.r.Bind(ensure); err != nil {
		return err
	}
	_, err = a.r.AddPage(panesPage, "Panes", a.renderPanes, ochazuke.Focus(paneListID))
	return err
}

// refreshPanes is panes.refresh: list the panes off the loop, then push them.
func (a *App) refreshPanes(ctx ochazuke.Ctx) ochazuke.Result {
	d := a.panesd
	return ochazuke.Pending(func(c context.Context) (any, error) {
		return sideListing(c, d, a.work)
	}).ReplacingAt(paneListID, "refresh").Then(func(_ ochazuke.Ctx, v any, err error) {
		panes, _ := v.([]pane)
		a.setPanes(panes, err)
	})
}

// ensureVerb is daemon.ensure: make the daemon answer off the loop (Ensure
// starts mtm's server when missing), then reload the pane list. A failure
// fails the job (the footer shows it) and is the panes page's hint.
func (a *App) ensureVerb(ochazuke.Ctx) ochazuke.Result {
	ensure := a.ensure
	return ochazuke.Pending(func(c context.Context) (any, error) {
		return nil, ensure(c)
	}).Then(func(_ ochazuke.Ctx, _ any, err error) {
		if err != nil {
			a.setPanes(nil, err)
			return
		}
		if err := a.reloadPanes(); err != nil {
			a.paneErr = err.Error()
		}
	})
}

// ensureDaemon runs daemon.ensure on the pane list.
func (a *App) ensureDaemon() error {
	_, err := a.r.Execute(ochazuke.Request{Verb: "ensure", Noun: "daemon", Target: paneListID})
	return err
}

// reloadPanes runs panes.refresh on the list.
func (a *App) reloadPanes() error {
	_, err := a.r.Execute(ochazuke.Request{Verb: "refresh", Noun: "panes", Target: paneListID})
	return err
}

// setPanes stores a listing and pushes it to the tree; a failed listing
// empties the tree and keeps the error for the hint. Agents that started
// or finished a turn since the last listing are recorded (turns.go).
func (a *App) setPanes(panes []pane, err error) {
	was := a.paneErr
	a.paneErr = ""
	if err != nil {
		panes, a.paneErr = nil, err.Error()
		if was == "" {
			// The page says the daemon is down in two lines; the whole
			// error is a notice (notice.list), once per outage.
			a.m.Notify("panes: " + a.paneErr)
		}
	}
	if err == nil {
		var mergeErr error
		panes, mergeErr = a.mergeCompanions(panes)
		if mergeErr != nil {
			a.paneErr = mergeErr.Error()
		}
	}
	a.panes = panes
	a.recordTurns(panes)
	a.pv.view = a.buildPaneTree(panes, time.Now())
	before, hadRow := a.pv.tree.Selected()
	if err := a.pv.tree.SetNodes(a.pv.view.nodes); err != nil {
		a.paneErr = err.Error()
	}
	// The page starts on its first pane (the one that most needs you), not
	// on the group rows above it.
	if after, ok := a.pv.tree.Selected(); ok && (!hadRow || before.ID != after.ID) && !strings.HasPrefix(after.ID, "pane:") && len(a.pv.view.order) > 0 {
		if err := a.pv.tree.Select(a.pv.view.order[0]); err != nil {
			a.paneErr = err.Error()
		}
	}
	if err := a.syncSideView(); err != nil {
		a.paneErr = err.Error()
	}
}

// landPane is the one completion of agent.start and pane.start (the owner
// rule in docs/BRIEF.md: a launch ends with the pane on screen). The job
// listed the panes after creating p; the list shows them, the panes page
// opens (page.open), the new pane's row becomes the list's selection and
// takes the focus (so a later } from the page lands on it), and pane.attach
// runs on it through the registry, which moves the focus into the
// terminal. A step that cannot run (a modal is open, the listing lacks
// the pane) stops the landing and shows why in the page's header.
func (a *App) landPane(p pane, panes []pane) {
	a.setPanes(panes, nil)
	a.landErr = ""
	if err := a.land(p.rowID()); err != nil {
		a.landErr = "show the new pane: " + err.Error()
	}
}

func (a *App) land(row string) error {
	if _, err := a.r.Execute(ochazuke.Request{Verb: "open", Noun: "page", Args: ochazuke.Arguments{"id": panesPage}}); err != nil {
		return err
	}
	if a.r.Instances[row] == nil {
		return fmt.Errorf("the pane list has no %s", row)
	}
	if err := a.pv.tree.Select(row); err != nil {
		return err
	}
	if err := a.paneList.Focus(); err != nil {
		return err
	}
	_, err := a.r.Execute(ochazuke.Request{Verb: "attach", Noun: "pane", Target: row})
	return err
}

// paneAt is the pane a row instance fronts.
func (a *App) paneAt(inst *ochazuke.Instance) (pane, error) {
	if inst != nil {
		for _, p := range a.panes {
			if p.rowID() == inst.ID {
				return p, nil
			}
		}
		return pane{}, fmt.Errorf("no pane at %s", inst.ID)
	}
	return pane{}, errors.New("no pane selected")
}

// previewPane is pane.preview: capture the screen in the preview slot
// scoped to the list; a newer preview of any row cancels the older one.
func (a *App) previewPane(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.paneAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	d := a.panesd
	version := a.pinVersion
	return ochazuke.Pending(func(c context.Context) (any, error) {
		text, err := d.CapturePane(c, p)
		if err != nil {
			return nil, fmt.Errorf("preview %s: %w", p.label(), err)
		}
		return preview{Pane: p.rowID(), Text: text}, nil
	}).ReplacingAt(paneListID, "preview").Then(func(_ ochazuke.Ctx, v any, err error) {
		if got, ok := v.(preview); ok && err == nil && version == a.pinVersion {
			a.preview = got
		}
	})
}

// attachPane is pane.attach: replace any attached terminal with one fed by
// the pane's stream and focus it. CloseOnDetach makes the leader's d remove
// the terminal, which releases the stream, and FocusOnClose lands focus back
// on the row the attach came from, so enter re-attaches at once.
func (a *App) attachPane(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.paneAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.r.OpenPage(panesPage); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.attachLive(p, ctx.Instance.ID); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(p.label())
}

func (a *App) attachLive(p pane, returnTo string) error {
	if err := a.work.TouchDir(p.Cwd); err != nil {
		a.m.Notify("directory recency: " + err.Error())
	}
	if err := a.closeTerminal(); err != nil {
		return err
	}
	stream := a.panesd.OpenStream(p)
	if _, err := a.r.AddTerminal(paneTermID, stream, ochazuke.Parent(panesPage), ochazuke.CloseOnDetach(), ochazuke.FocusOnClose(returnTo)); err != nil {
		stream.Release() // never added, so no Terminal.Close releases it
		return err
	}
	a.attached = p
	a.pv.zoom = false
	a.pv.tab = tabScreen
	if a.r.Instances[p.rowID()] == nil && len(a.panes) > 0 {
		// A pane outside the pin stays reachable as a labelled row while
		// it is attached.
		a.pv.view = a.buildPaneTree(a.panes, time.Now())
		if err := a.pv.tree.SetNodes(a.pv.view.nodes); err != nil {
			return err
		}
	}
	if err := a.r.SetFocus(paneTermID); err != nil {
		return err
	}
	return a.syncSideView()
}

// closeTerminal removes the attached terminal; Terminal.Close releases
// its stream.
func (a *App) closeTerminal() error {
	if err := a.closeSideTerminal(); err != nil {
		return err
	}
	a.attached = pane{}
	a.pv.zoom = false
	if a.r.Instances[paneTermID] == nil {
		return nil
	}
	return a.r.RemoveInstance(paneTermID)
}

// killPane is pane.kill, after its confirm: close the window off the loop
// (detaching first when it is the attached pane), then reload the list.
func (a *App) killPane(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.paneAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if a.sideStarting[p.ID+"/vim"] || a.sideStarting[p.ID+"/term"] {
		return ochazuke.Failed(errors.New("wait for side panes to finish opening before killing the parent"))
	}
	d := a.panesd
	return ochazuke.Pending(func(c context.Context) (any, error) {
		if err := a.checkCompanions(c, p.ID); err != nil {
			return nil, err
		}
		if err := d.KillPane(c, p); err != nil {
			return nil, fmt.Errorf("kill %s: %w", p.label(), err)
		}
		return d.ListPanes(c)
	}).Then(func(_ ochazuke.Ctx, v any, err error) {
		if err == nil {
			if a.attached.ID == p.ID {
				if closeErr := a.closeTerminal(); closeErr != nil {
					a.m.Notify(closeErr.Error())
				}
			}
			panes, _ := v.([]pane)
			a.setPanes(panes, nil)
		}
	})
}

// fill fits text into exactly height lines, each cut to width when given.
func (a *App) fill(text string, height int, width ...int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	lines = lines[:height]
	if len(width) > 0 {
		for i, line := range lines {
			lines[i] = ansi.Truncate(line, width[0], "")
		}
	}
	return strings.Join(lines, "\n")
}

// agentRow is one row of list-agents: the agent a window runs, if any.
type agentRow struct {
	Window   string `json:"window_id"`
	Harness  string `json:"harness_id"`
	State    string `json:"state"`
	StateAt  int64  `json:"agent_state_at"`
	NeedsYou bool   `json:"needs_you"`
}

// agents reads list-agents for every window of every session; a daemon
// without it answers with no agents.
func (t tuios) agents(ctx context.Context) map[string]agentRow {
	var out struct {
		Agents []agentRow `json:"agents"`
	}
	if err := t.Call(ctx, "list-agents", map[string]any{"all": true, "all_sessions": true}, &out); err != nil {
		return nil
	}
	rows := map[string]agentRow{}
	for _, a := range out.Agents {
		rows[a.Window] = a
	}
	return rows
}

// KillSession closes a session and its windows.
func (t tuios) KillSession(ctx context.Context, session string) error {
	ctx, cancel := context.WithTimeout(ctx, daemonTimeout)
	defer cancel()
	return t.Call(ctx, "kill-session", map[string]any{"session": session}, nil)
}

// Activity is the agent's recent activity, one line per entry, newest last.
func (t tuios) Activity(ctx context.Context, p pane) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, daemonTimeout)
	defer cancel()
	got, err := t.Client.Activity(ctx, daemon.ActivityParams{Session: p.Session, Window: p.ID, Limit: 200})
	if err != nil {
		return "", err
	}
	var lines []string
	for _, e := range got.Entries {
		line := time.UnixMilli(e.At).Format("15:04:05") + "  " + e.Kind
		for _, part := range []string{e.Tool, e.Target, oneLine(e.Text)} {
			if part != "" {
				line += "  " + part
			}
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}

// Transcript is the agent's conversation, one entry per paragraph.
func (t tuios) Transcript(ctx context.Context, p pane) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, daemonTimeout)
	defer cancel()
	got, err := t.Client.Transcript(ctx, daemon.TranscriptParams{Session: p.Session, Window: p.ID, Limit: 100})
	if err != nil {
		return "", err
	}
	var parts []string
	for _, e := range got.Entries {
		head := e.Role
		if e.Tool != "" {
			head += " · " + e.Tool
		}
		parts = append(parts, head+"\n"+strings.TrimSpace(e.Text))
	}
	return strings.Join(parts, "\n\n"), nil
}

// oneLine is text on one line, cut at 200 runes.
func oneLine(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > 200 {
		return string(r[:200]) + "…"
	}
	return text
}

// ListPanes lists every window of every session the daemon reports.
func (t tuios) ListPanes(ctx context.Context) ([]pane, error) {
	ctx, cancel := context.WithTimeout(ctx, daemonTimeout)
	defer cancel()
	sessions, err := t.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	agents := t.agents(ctx)
	var out []pane
	for _, s := range sessions {
		windows, err := t.Panes(ctx, s.Name)
		if err != nil {
			return nil, fmt.Errorf("list windows of %s: %w", s.Name, err)
		}
		if len(windows) == 0 {
			out = append(out, pane{Session: s.Name, SessionID: s.ID, Pane: daemon.Pane{Cwd: s.Dir}})
		}
		for _, w := range windows {
			p := pane{Session: s.Name, SessionID: s.ID, Pane: w}
			if ag, ok := agents[w.ID]; ok {
				p.Harness, p.StateAt, p.NeedsYou = ag.Harness, ag.StateAt/int64(time.Millisecond), ag.NeedsYou
				if ag.State != "" {
					p.AgentState = ag.State
				}
			}
			out = append(out, p)
		}
	}
	return out, nil
}

// CapturePane is the pane's visible screen with its colours.
func (t tuios) CapturePane(ctx context.Context, p pane) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, daemonTimeout)
	defer cancel()
	got, err := t.Capture(ctx, daemon.CaptureParams{Session: p.Session, Window: p.ID, Source: "visible", ANSI: true})
	return got.Content, err
}

// KillPane closes the pane's window.
func (t tuios) KillPane(ctx context.Context, p pane) error {
	ctx, cancel := context.WithTimeout(ctx, daemonTimeout)
	defer cancel()
	return t.Call(ctx, "close-window", map[string]any{"session": p.Session, "window": p.ID}, nil)
}

// OpenStream is a stream that dials stream-pane on its first read.
func (t tuios) OpenStream(p pane) paneStream {
	ctx, cancel := context.WithCancel(context.Background())
	return &liveStream{client: t.Client, pane: p, ctx: ctx, cancel: cancel}
}

// liveStream adapts the daemon's stream-pane to the framework terminal: its
// frames only say the pane changed, and each change is read back as the
// pane's visible screen through capture-pane (the old app's approach, so
// the app holds no terminal emulator). Keys go through send-keys, pastes
// through send-text, and Resize holds a size lease that Release drops.
type liveStream struct {
	client *daemon.Client
	pane   pane
	ctx    context.Context // the stream's life; Release cancels it
	cancel context.CancelFunc

	mu         sync.Mutex
	conn       *daemon.PaneStream
	frames     chan streamFrame
	held       *streamFrame // an ending frame met while coalescing, read next
	cols, rows int
	released   bool
}

type streamFrame struct {
	frame daemon.Frame
	err   error
}

// open dials stream-pane with the current lease and starts the reader.
func (s *liveStream) open(ctx context.Context) (chan streamFrame, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return nil, context.Canceled
	}
	if s.conn != nil {
		return s.frames, nil
	}
	dial, stop := context.WithTimeout(ctx, daemonTimeout)
	defer stop()
	params := daemon.StreamParams{Session: s.pane.Session, Window: s.pane.ID, Fit: true}
	if s.cols > 0 && s.rows > 0 {
		params.LeaseCols, params.LeaseRows = uint16(s.cols), uint16(s.rows)
	}
	// The stream outlives this read: it ends with Release, not with ctx.
	conn, err := s.client.StreamPane(s.ctx, params)
	if err == nil && dial.Err() != nil {
		conn.Close()
		err = dial.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("attach %s: %w", s.pane.label(), err)
	}
	s.conn, s.frames = conn, make(chan streamFrame, 8)
	go func(conn *daemon.PaneStream, out chan streamFrame) {
		for {
			frame, err := conn.ReadFrame()
			select {
			case out <- streamFrame{frame, err}:
			case <-s.ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}(conn, s.frames)
	return s.frames, nil
}

// Next waits for the pane to change, then returns its visible screen.
func (s *liveStream) Next(ctx context.Context) (ochazuke.TerminalFrame, error) {
	frames, err := s.open(ctx)
	if err != nil {
		return ochazuke.TerminalFrame{}, err
	}
	var item streamFrame
	if s.held != nil {
		item, s.held = *s.held, nil
	} else {
		select {
		case <-ctx.Done():
			return ochazuke.TerminalFrame{}, ctx.Err()
		case item = <-frames:
		}
	}
	if err := frameErr(item); err != nil {
		return ochazuke.TerminalFrame{}, err
	}
	// Coalesce the frames already waiting into one capture; an ending
	// frame among them is held for the next read.
	for drained := false; !drained; {
		select {
		case next := <-frames:
			if frameErr(next) != nil {
				s.held, drained = &next, true
			}
		default:
			drained = true
		}
	}
	capture, cancel := context.WithTimeout(ctx, daemonTimeout)
	defer cancel()
	got, err := s.client.Capture(capture, daemon.CaptureParams{Session: s.pane.Session, Window: s.pane.ID, Source: "visible", ANSI: true})
	if err != nil {
		return ochazuke.TerminalFrame{}, fmt.Errorf("capture %s: %w", s.pane.label(), err)
	}
	return ochazuke.TerminalFrame{Lines: strings.Split(strings.TrimRight(got.Content, "\n"), "\n"), Hidden: true}, nil
}

// frameErr is the error a frame ends the stream with, nil for a change.
func frameErr(item streamFrame) error {
	switch {
	case item.err != nil:
		return item.err
	case item.frame.Type == 'E' && item.frame.Error != nil:
		return item.frame.Error
	case item.frame.Type == 'E':
		return errors.New("pane stream rejected a frame")
	case item.frame.Type == 'X':
		return fmt.Errorf("pane stream ended: %s", item.frame.Data)
	}
	return nil
}

// Send forwards key names through send-keys, in order, in one call.
func (s *liveStream) Send(keys ...string) error {
	names := make([]string, 0, len(keys))
	for _, k := range keys {
		names = append(names, daemonKey(k))
	}
	ctx, cancel := context.WithTimeout(context.Background(), daemonTimeout)
	defer cancel()
	return s.client.Call(ctx, "send-keys", map[string]any{"session": s.pane.Session, "window": s.pane.ID, "keys": strings.Join(names, " ")}, nil)
}

// daemonKey spells a binding key name the way send-keys parses it: the
// two separators it splits on and escape get their names.
func daemonKey(k string) string {
	switch k {
	case " ", "space":
		return "Space"
	case ",":
		return "Comma"
	case "esc":
		return "Escape"
	}
	return k
}

// Paste types text into the pane as a bracketed paste.
func (s *liveStream) Paste(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), daemonTimeout)
	defer cancel()
	return s.client.SendText(ctx, daemon.SendTextParams{Session: s.pane.Session, Window: s.pane.ID, Text: text, Paste: true})
}

// Resize holds the pane at most at the area the detail stop gives it.
func (s *liveStream) Resize(cols, rows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cols, s.rows = cols, rows
	if s.conn == nil || s.released {
		return nil
	}
	return s.conn.Lease(uint16(cols), uint16(rows))
}

// Release drops the size lease and closes the stream; it is idempotent.
// Terminal.Close calls it (ochazuke.TerminalReleaser).
func (s *liveStream) Release() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return nil
	}
	s.released = true
	var err error
	if s.conn != nil {
		err = errors.Join(s.conn.Lease(0, 0), s.conn.Close())
	}
	s.cancel()
	return err
}
