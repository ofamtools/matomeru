package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/work"
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
	Harness           string            `json:"harness,omitempty"`
	StateAt           int64             `json:"state_at,omitempty"`
	NeedsYou          bool              `json:"needs_you,omitempty"`
	Model             string            `json:"model,omitempty"`          // from the launch recipe, when mtm started it
	LaunchHarness     string            `json:"launch_harness,omitempty"` // the recipe's harness, for the detail header
	LaunchFlags       []string          `json:"launch_flags,omitempty"`   // the recipe's argv after the command
	Meta              map[string]string `json:"meta,omitempty"`           // the agent's metadata in tuios (model, context, cost…)
	SideVim, SideTerm string
	ShowVim, ShowTerm bool
	SideFront         string
	Exited            bool   `json:"exited,omitempty"`  // the window's process ended; TUIOS still lists it
	Program           string `json:"program,omitempty"` // what mtm knows runs in a shell pane ("vim"), else ""
	Starred           bool   `json:"starred,omitempty"` // pane.star (pane_stars.go)
	daemon.Pane
}

// rowID is the pane's list row instance id; window ids are uuids, the
// prefix keeps them apart from project and task rows.
func (p pane) rowID() string { return "pane:" + p.Session + "/" + p.ID }

// label is the row text, the panel title and the {{label}} a confirm
// prompt reads. A uuid-named session shows its first 8 characters.
func (p pane) label() string {
	name := p.Name
	if name == "" || defaultWindowName.MatchString(name) {
		name = p.baseName()
	}
	if name == "" {
		name = p.ID
	}
	return shortSession(p.Session) + "/" + name
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
	exited     map[string]bool // windows seen exited, kept across plain listings
	listed     bool            // a listing has succeeded (the Vim page's state follows it)
}

// Timers of the panes page.
const (
	followDelay  = 150 * time.Millisecond // the detail follows the selection after this pause
	refreshEvery = 3 * time.Second        // the list reloads this often while the page is open (refresh-interval's default)
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
		Verb("preview", a.previewPane, "Capture the pane's screen into the detail's Screen tab").Fetch().Quiet().
		Verb("kill", a.killPane, "Close the pane's window").Confirm(killConfirm).
		Verb("killBlocked", a.killBlocked, "Say which side panes must close before the pane can be killed").
		Verb("activity", a.paneActivity, "Read what the agent in the pane has been doing into the Activity tab").Fetch().Quiet().
		Verb("transcript", a.paneTranscript, "Read the agent's transcript into the Transcript tab").Fetch().Quiet().
		Mode("normal").
		Bind("enter", ochazuke.Do("pane.attach")).
		Bind("d d", ochazuke.Do("pane.kill").Guard("pane-no-sides")).
		Bind("d d", ochazuke.Do("pane.killBlocked").Hide()).
		Bind("m", ochazuke.Do("pane.associate"))
	// d d asks to kill only a pane with no side pane open; with one, it says
	// which to close instead of confirming and then refusing.
	a.r.DefineGuard("pane-no-sides", func(r *ochazuke.Runtime, ev ochazuke.KeyEvent) bool {
		p, err := a.paneAt(r.Instances[ev.Focus])
		return err != nil || p.SideVim == "" && p.SideTerm == ""
	})
	r.Define("session").
		Verb("attach", a.attachSession, "Attach the session's first pane").
		Verb("kill", a.killSession, "Close the session and every pane in it").Confirm("Kill {{label}} and its panes?").
		Mode("normal").
		Bind("enter", ochazuke.Do("session.attach").Guard("session-has-pane")).
		Bind("m", ochazuke.Do("session.associate")).
		Bind("d d", ochazuke.Do("session.kill"))
	// enter attaches a session only when it has a pane to attach.
	a.r.DefineGuard("session-has-pane", func(r *ochazuke.Runtime, ev ochazuke.KeyEvent) bool {
		return a.sessionPane(ev.Focus) != ""
	})
	r.Define("pane-project").Mode("normal").Bind("enter", ochazuke.Do("outline.fold"))
	// List reloads are bookkeeping: Quiet keeps them off the jobs ticker.
	r.Define("panes").
		Verb("refresh", a.refreshPanes, "Reload the pane list from the daemon").Fetch().Quiet().
		Verb("tab", a.paneTab, "Show the next (delta 1) or previous (-1) detail tab: Screen, Activity, Transcript, Turns").
		Verb("waiting", a.nextWaiting, "Select the next pane that needs input")
	a.parameters("panes.tab", map[string]ochazuke.Parameter{"delta": {Type: "integer", Required: true}})
	r.Define("daemon").Verb("ensure", a.ensureVerb, "Start mtm's TUIOS server when it is not running, then reload the pane list").Quiet()
	a.defineLiveKeys()
	a.definePaneStars()
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
		{"]", ochazuke.Do("panes.tab", ochazuke.Arguments{"delta": 1}).Hinted("next tab")},
		{"[", ochazuke.Do("panes.tab", ochazuke.Arguments{"delta": -1}).Hinted("previous tab")},
		// W: w saves a pane as a record (the key map).
		{"W", ochazuke.Do("panes.waiting")},
		// `: ' is page.pick; the backtick is Vim's jump to a mark.
		{"`", ochazuke.Do("panes.starred").Hinted("next starred")},
	} {
		b := row.req
		b.Instance, b.Mode, b.Keys = panesPage, "normal", row.keys
		if err := a.r.Bind(b); err != nil {
			return err
		}
	}
	// While the daemon does not answer (the list shows "daemon is not
	// running") e runs daemon.ensure; with the daemon up it is no key.
	a.r.DefineGuard("daemon-down", func(*ochazuke.Runtime, ochazuke.KeyEvent) bool { return a.paneErr != "" })
	ensure := ochazuke.Do("daemon.ensure").Guard("daemon-down")
	ensure.Instance, ensure.Mode, ensure.Keys = paneListID, "normal", "e"
	if err := a.r.Bind(ensure); err != nil {
		return err
	}
	if _, err = a.r.AddPage(panesPage, "Panes", a.renderPanes, ochazuke.Focus(paneListID)); err != nil {
		return err
	}
	// The detail is the selected row's tab; Screen, Activity and
	// Transcript have the newest at the bottom.
	a.detail.panes, err = a.addDetail(panesDetailID, panesPage, func() string {
		row, _ := a.selectedRow()
		return fmt.Sprintf("%s|%d", row, a.pv.tab)
	}, func() bool { return a.pv.tab != tabTurns })
	return err
}

// refreshPanes is panes.refresh: list the panes off the loop, then push them.
func (a *App) refreshPanes(ctx ochazuke.Ctx) ochazuke.Result {
	d, ws := a.panesd, a.work
	// The lock keeps the listing's link cleanup from racing a side launch.
	return ochazuke.Pending(a.withSideLock(func(c context.Context) (any, error) {
		return sideListing(c, d, ws)
	})).ReplacingAt(paneListID, "refresh").Then(func(_ ochazuke.Ctx, v any, err error) {
		panes, _ := v.([]pane)
		a.setPanes(panes, err)
	})
}

const agentChooser = "agent-chooser"

// chooseAgent is agent.choose (L on every page): a chooser of the
// harnesses with the pin's project's harness (else the default) first and
// highlighted, so L enter launches it; Enter runs agent.launch, which
// lands the new agent live on Panes.
func (a *App) chooseAgent(ochazuke.Ctx) ochazuke.Result {
	p, _ := a.project(a.pin)
	where := "no project"
	if p.ID != "" {
		where = projectName(p)
	}
	return resultError(a.chooseHarness("Launch an agent in "+where, p, ochazuke.Request{Verb: "launch", Noun: "agent"}))
}

// chooseHarness opens the harness chooser, p's harness (else the default)
// first and highlighted; Enter runs launch with the chosen harness.
func (a *App) chooseHarness(title string, p work.Project, launch ochazuke.Request) error {
	first := cmp.Or(p.Settings.Harness, a.launchHarness())
	names := a.harnesses.Names()
	slices.SortStableFunc(names, func(x, y string) int { return -cmp.Compare(btoi(x == first), btoi(y == first)) })
	rows := make([]ochazuke.ChooserRow, 0, len(names))
	for _, name := range names {
		rows = append(rows, ochazuke.ChooserRow{ID: name, Label: name})
	}
	_, err := a.r.OpenChooser(ochazuke.ChooserSpec{
		Kind:  agentChooser,
		Title: title,
		Rows:  rows,
		Choose: func(c ochazuke.ChooserRow) ochazuke.Result {
			req := launch
			req.Args = ochazuke.Arguments{}
			for k, v := range launch.Args {
				req.Args[k] = v
			}
			req.Args["harness"] = c.ID
			out, err := a.r.Execute(req)
			if err != nil {
				return ochazuke.Failed(err)
			}
			return ochazuke.Done(out)
		},
	})
	return err
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
			a.r.UI().Notify("panes: " + a.paneErr)
		}
	}
	if err == nil {
		a.pv.listed = true
		a.markPanes(panes)
		var mergeErr error
		panes, mergeErr = a.mergeCompanions(panes)
		if mergeErr != nil {
			a.paneErr = mergeErr.Error()
		}
	}
	for i := range panes {
		if panes[i].ID == "" {
			continue
		}
		if r, err := a.work.PaneRecipe(panes[i].ID); err == nil {
			panes[i].Model, panes[i].LaunchHarness, panes[i].LaunchFlags = r.Model, r.Harness, r.Flags
		}
	}
	a.markStars(panes, err == nil)
	a.panes = panes
	if err == nil && a.attached.ID != "" {
		live := slices.ContainsFunc(panes, func(p pane) bool {
			return p.ID == a.attached.ID && !p.Exited
		})
		if !live {
			if err := a.closeTerminal(); err != nil {
				a.paneErr = err.Error()
			}
		}
	}
	a.recordTurns(panes)
	a.feed.setPanes(panes)
	a.pv.view = a.buildPaneTree(panes, time.Now())
	before, hadRow := a.pv.tree.Selected()
	a.paneList.SetColumns(a.pv.view.columns()...)
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
	if err := a.refreshVim(); err != nil {
		a.paneErr = err.Error()
	}
}

// markPanes keeps what the listing alone does not say: a window seen
// exited stays exited (a plain listing after a launch or kill does not
// probe), and a window mtm opened Neovim in is a vim pane, not a shell.
func (a *App) markPanes(panes []pane) {
	if a.pv.exited == nil {
		a.pv.exited = map[string]bool{}
	}
	vim := map[string]bool{}
	if sessions, err := a.work.VimSessions(); err == nil {
		for _, s := range sessions {
			if s.WindowID != "" {
				vim[s.WindowID] = true
			}
		}
	}
	if links, err := a.work.Companions(); err == nil {
		for _, link := range links {
			if link.Kind == "vim" {
				vim[link.Window] = true
			}
		}
	}
	seen := make(map[string]bool, len(panes))
	for i := range panes {
		id := panes[i].ID
		if id == "" {
			continue
		}
		seen[id] = true
		if panes[i].Exited {
			a.pv.exited[id] = true
		}
		panes[i].Exited = a.pv.exited[id]
		if vim[id] {
			panes[i].Program = "vim"
		}
	}
	for id := range a.pv.exited {
		if !seen[id] {
			delete(a.pv.exited, id)
		}
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
		a.r.UI().Notify("directory recency: " + err.Error())
	}
	if err := a.closeTerminal(); err != nil {
		return err
	}
	stream := a.panesd.OpenStream(p)
	a.trackStream(paneTermID, stream)
	if h, ok := stream.(holdable); ok {
		// Detach parks the stream, so the pane keeps the size it was
		// shown at while its preview is on screen (dropHeld gives it back).
		h.hold()
		a.held = heldStream{pane: p, stream: h}
	}
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
	var err error
	if a.r.Instances[paneTermID] != nil {
		err = a.r.RemoveInstance(paneTermID)
	}
	return errors.Join(err, a.dropHeld())
}

// holdable is a stream whose Release can park it (liveStream).
type holdable interface {
	hold()
	drop() error
}

// heldStream is the attached pane's stream; after a detach it stays parked,
// holding the pane's size lease, until the preview leaves that pane.
type heldStream struct {
	pane   pane
	stream holdable
}

// dropHeld releases the held stream, if any.
func (a *App) dropHeld() error {
	h := a.held
	a.held = heldStream{}
	if h.stream == nil {
		return nil
	}
	return h.stream.drop()
}

// keepHeld drops a parked stream once its pane is no longer the one the
// Screen preview shows: another row, another page, or no detach yet.
func (a *App) keepHeld() error {
	if a.held.stream == nil || a.r.Terminal(paneTermID) != nil {
		return nil
	}
	row, _ := a.selectedRow()
	if a.r.Page == panesPage && row == a.held.pane.rowID() {
		return nil
	}
	return a.dropHeld()
}

// killBlocked is pane.killBlocked, x on a pane whose side panes are open:
// it names them and the leader keys that reach them.
func (a *App) killBlocked(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.paneAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	var open []string
	if p.SideVim != "" {
		open = append(open, "side vim ("+a.r.TerminalLeader()+" v)")
	}
	if p.SideTerm != "" {
		open = append(open, "side terminal ("+a.r.TerminalLeader()+" t)")
	}
	return ochazuke.Failed(fmt.Errorf("close the %s of %s before killing it", strings.Join(open, " and "), p.label()))
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
	}).Then(func(ctx ochazuke.Ctx, v any, err error) {
		if err == nil {
			if a.attached.ID == p.ID {
				if closeErr := a.closeTerminal(); closeErr != nil {
					ctx.Notify(closeErr.Error())
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
	Window   string         `json:"window_id"`
	Harness  string         `json:"harness_id"`
	State    string         `json:"state"`
	StateAt  int64          `json:"agent_state_at"`
	NeedsYou bool           `json:"needs_you"`
	Meta     map[string]any `json:"meta"`
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

// Transcript is the agent's conversation, one entry per paragraph. Only
// the person may read it: the call carries the presence nonce mtm holds
// (a refused nonce, after a daemon restart, is renewed once).
func (t tuios) Transcript(ctx context.Context, p pane) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, daemonTimeout)
	defer cancel()
	read := func() (daemon.TranscriptResult, error) {
		nonce, err := t.HumanNonce(ctx)
		if err != nil {
			return daemon.TranscriptResult{}, err
		}
		return t.Client.Transcript(ctx, daemon.TranscriptParams{Session: p.Session, Window: p.ID, HumanNonce: nonce, Limit: 100})
	}
	got, err := read()
	if daemonCode(err) == "not_human" {
		t.DropPresence()
		got, err = read()
	}
	if err != nil {
		return "", transcriptError(err, t.Server)
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

// daemonCode is a daemon error's code, "" for any other error.
func daemonCode(err error) string {
	var remote *daemon.Error
	if errors.As(err, &remote) {
		return remote.Code
	}
	return ""
}

// explained is an error whose message is meant for the person as it is:
// what happened and what makes it work.
type explained struct{ text string }

func (e explained) Error() string { return e.text }

// transcriptError says in plain words why a transcript cannot be read.
func transcriptError(err error, server string) error {
	switch daemonCode(err) {
	case "forbidden", "not_human":
		where := "this daemon"
		if server != "" {
			where = "TUIOS server " + server
		}
		return explained{"the transcript is only for the person: TUIOS gives mtm your presence only when mtm runs outside the panes of " + where + ". Run mtm from a terminal that is not one of its panes; the Screen tab shows the pane meanwhile."}
	case "no_transcript":
		return explained{"no transcript yet: this pane is not joined to an agent transcript (the agent's hooks join it once its session starts). The Screen tab shows the pane."}
	case "unsupported_harness":
		return explained{"this agent's harness keeps no transcript TUIOS reads. The Screen tab shows the pane."}
	}
	return err
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
				for k, v := range ag.Meta {
					if s := strings.TrimSpace(fmt.Sprint(v)); v != nil && s != "" {
						if p.Meta == nil {
							p.Meta = map[string]string{}
						}
						p.Meta[k] = s
					}
				}
				if ag.State != "" {
					p.AgentState = ag.State
				}
			}
			out = append(out, p)
		}
	}
	return out, nil
}

// Exited reports whether the window's process has ended: wait-for
// window-exit answers at once for an exited window and times out after a
// millisecond for a running one.
func (t tuios) Exited(ctx context.Context, p pane) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, daemonTimeout)
	defer cancel()
	err := t.WaitFor(ctx, daemon.WaitForParams{Session: p.Session, Window: p.ID, Condition: "window-exit", Timeout: 1})
	var remote *daemon.Error
	if errors.As(err, &remote) && remote.Code == "timeout" {
		return false, nil
	}
	return err == nil, err
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
	return &liveStream{client: t.Client, pane: p, ctx: ctx, cancel: cancel, dial: dialDaemon(t.Client), backoff: reconnectBackoff}
}

// liveStream adapts the daemon's stream-pane to the framework terminal: its
// frames replay into an emulator (liveScreen), which gives the screen and
// the cursor; before the first snapshot a change is read back through
// capture-pane. Keys go through send-keys, pastes through send-text, and
// Resize holds a size lease that Release drops, unless the stream is held
// (hold), when Release parks it and drop gives the lease back.
type liveStream struct {
	client *daemon.Client
	pane   pane
	ctx    context.Context // the stream's life; Release cancels it
	cancel context.CancelFunc

	// dial opens stream-pane (dialDaemon; a fake in tests) and backoff is
	// the wait before reconnect attempt n (stream_reconnect.go).
	dial    func(ctx context.Context, params daemon.StreamParams) (paneConn, daemon.StreamInfo, error)
	backoff func(attempt int) time.Duration

	mu         sync.Mutex
	conn       paneConn
	frames     chan streamFrame
	connDone   chan struct{} // closed when resnap drops conn
	held       *streamFrame  // an ending frame met while coalescing, read next
	cols, rows int
	released   bool
	keep       bool // hold: Release parks the stream instead of dropping it
	parked     bool
	ended      bool // the program ended normally; reads wait for the close
	screen     liveScreen
	// The resume point and the connection's state (stream_reconnect.go):
	// seq is the last stream position written into the screen, boot the
	// daemon boot it came from, state "" while connected, else
	// reconnecting or gone.
	seq   uint64
	boot  string
	state string
}

type streamFrame struct {
	frame daemon.Frame
	err   error
}

// open dials stream-pane with the current lease and starts the reader;
// resume asks the daemon to continue from the last seq (a reconnect).
func (s *liveStream) open(ctx context.Context, resume bool) (chan streamFrame, error) {
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
	if resume && s.seq > 0 {
		params.FromSeq, params.BootID = s.seq, s.boot
	}
	dialer := s.dial
	if dialer == nil {
		dialer = dialDaemon(s.client)
	}
	// The stream outlives this read: it ends with Release, not with ctx.
	conn, info, err := dialer(s.ctx, params)
	if err == nil && dial.Err() != nil {
		conn.Close()
		err = dial.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("attach %s: %w", s.pane.label(), err)
	}
	s.boot = info.BootID
	if resume && info.Mode == "resume" && info.Cols > 0 && info.Rows > 0 {
		// Output continues the screen we kept, at the pane's size now.
		s.screen.apply(daemon.Frame{Type: 'R', Cols: info.Cols, Rows: info.Rows})
	}
	s.conn, s.frames, s.connDone = conn, make(chan streamFrame, 8), make(chan struct{})
	go func(conn paneConn, out chan streamFrame, done chan struct{}) {
		for {
			frame, err := conn.ReadFrame()
			select {
			case out <- streamFrame{frame, err}:
			case <-s.ctx.Done():
				return
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}(conn, s.frames, s.connDone)
	return s.frames, nil
}

// resnap reconnects for a fresh snapshot at the pane's new size. The
// daemon's emulator reflows history into a grown screen; replaying an R
// frame into ours would not, and the screens would drift. The next read
// dials again with the current lease.
func (s *liveStream) resnap() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil || s.released {
		return
	}
	close(s.connDone)
	s.conn.Close()
	s.conn, s.frames = nil, nil
	s.screen.close()
}

// Next waits for the pane to change, then returns its visible screen.
func (s *liveStream) Next(ctx context.Context) (ochazuke.TerminalFrame, error) {
	if s.ended || s.connState() == streamGone {
		// The program ended normally, or its window is gone: the last
		// screen stays up, quietly, until the listing closes the window
		// and the terminal.
		<-ctx.Done()
		return ochazuke.TerminalFrame{}, ctx.Err()
	}
	if s.connState() == streamReconnecting {
		if err := s.reconnect(ctx); err != nil {
			return ochazuke.TerminalFrame{}, err
		}
		// The title changes either way (back, or "pane gone"): draw now,
		// even when the resumed pane has nothing new to say.
		return s.lastFrame(ctx)
	}
	frames, err := s.open(ctx, false)
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
	if endedNormally(item) {
		s.ended = true
		return s.Next(ctx)
	}
	if s.dropped(ctx, item) {
		// The connection went, not the program: keep the screen, say
		// reconnecting, and dial again on the next read.
		s.lose()
		return s.lastFrame(ctx)
	}
	if err := frameErr(item); err != nil {
		return ochazuke.TerminalFrame{}, err
	}
	// Coalesce the frames already waiting into one screen; an ending frame
	// among them is held for the next read. A resize asks for a new
	// snapshot instead (resnap).
	resized := item.frame.Type == 'R'
	s.applyFrame(item.frame)
	for drained := false; !drained && !resized; {
		select {
		case next := <-frames:
			if frameErr(next) != nil {
				s.held, drained = &next, true
			} else {
				resized = next.frame.Type == 'R'
				s.applyFrame(next.frame)
			}
		default:
			drained = true
		}
	}
	if resized {
		s.resnap()
		return s.Next(ctx)
	}
	if frame, ok := s.screen.frame(); ok {
		return frame, nil
	}
	capture, cancel := context.WithTimeout(ctx, daemonTimeout)
	defer cancel()
	got, err := s.client.Capture(capture, daemon.CaptureParams{Session: s.pane.Session, Window: s.pane.ID, Source: "visible", ANSI: true})
	if err != nil {
		return ochazuke.TerminalFrame{}, fmt.Errorf("capture %s: %w", s.pane.label(), err)
	}
	return ochazuke.TerminalFrame{Lines: strings.Split(strings.TrimRight(got.Content, "\n"), "\n"), Hidden: true}, nil
}

// endedNormally reports a stream that ended because its program exited
// with status 0 or its window closed: what quitting vim or typing exit in
// a side terminal does. That is not an error; a kill, a failure status or
// a refusal still is.
func endedNormally(item streamFrame) bool {
	if item.err != nil || item.frame.Type != 'X' {
		return false
	}
	reason := string(item.frame.Data)
	return reason == "closed" || reason == "exited 0"
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
// Terminal.Close calls it (ochazuke.TerminalReleaser). A held stream is
// parked instead: the lease and the connection stay, its frames are
// drained, and drop releases it.
func (s *liveStream) Release() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released || s.parked {
		return nil
	}
	if s.keep {
		s.parked = true
		if s.frames != nil {
			go drainFrames(s.ctx, s.frames)
		}
		return nil
	}
	return s.releaseLocked()
}

// hold makes the next Release park the stream, so a detached pane keeps
// the size it was shown at while its preview is on screen.
func (s *liveStream) hold() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keep = true
}

// drop releases a held stream, parked or not.
func (s *liveStream) drop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released {
		return nil
	}
	return s.releaseLocked()
}

// drainFrames reads a parked stream's frames until its context ends, so the
// reader never blocks on a full channel.
func drainFrames(ctx context.Context, frames chan streamFrame) {
	for {
		select {
		case <-ctx.Done():
			return
		case f := <-frames:
			if f.err != nil {
				return
			}
		}
	}
}

func (s *liveStream) releaseLocked() error {
	s.released = true
	s.screen.close()
	var err error
	if s.conn != nil {
		err = errors.Join(s.conn.Lease(0, 0), s.conn.Close())
	}
	s.cancel()
	return err
}
