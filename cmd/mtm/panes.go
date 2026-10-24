package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
)

// The panes page: two stops, the pane list (rows of the pane noun, every
// window the daemon reports across its sessions) and the terminal detail.
// pane.preview captures the pane's screen off the loop in the "preview"
// slot scoped to the list, so a newer preview of any row replaces the
// older; the detail shows it while no terminal is attached. pane.attach
// adds a framework terminal under the page fed by the daemon's stream-pane
// and focuses it; the terminal noun owns passthrough and the ctrl+a leader,
// whose d runs terminal.detach. The terminal is added with CloseOnDetach,
// so detach removes it, Terminal.Close releases the stream's size lease,
// and focus returns to the page. pane.kill confirms with the row's label,
// closes the window and reloads the list.

const (
	panesPage     = "panes"
	paneListID    = "panes/list"
	paneTermID    = "panes/terminal"
	killConfirm   = "Kill pane {{label}}?"
	daemonTimeout = 5 * time.Second
)

// pane is one row: a window and the session it lives in.
type pane struct {
	Session string
	daemon.Pane
}

// rowID is the pane's list row instance id; window ids are uuids, the
// prefix keeps them apart from project and task rows.
func (p pane) rowID() string { return "pane:" + p.Session + "/" + p.ID }

// label is the row text and the {{label}} a confirm prompt reads.
func (p pane) label() string {
	name := p.Name
	if name == "" {
		name = p.Title
	}
	if name == "" {
		name = p.ID
	}
	return p.Session + "/" + name
}

// paneDaemon is the part of the daemon the panes page needs; tests replace it.
type paneDaemon interface {
	ListPanes(ctx context.Context) ([]pane, error)
	CapturePane(ctx context.Context, p pane) (string, error)
	KillPane(ctx context.Context, p pane) error
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

// definePanes adds the pane noun and the panes noun (refresh).
func (a *App) definePanes() {
	r := a.r
	r.Define("pane").
		Verb("attach", a.attachPane, "Attach the pane live in the detail stop; ctrl+a d detaches").
		Verb("preview", a.previewPane, "Capture the pane's screen into the detail stop").
		Verb("kill", a.killPane, "Close the pane's window").Confirm(killConfirm).
		Mode("normal").
		Bind("enter", ochazuke.Do("pane.attach")).
		Bind("p", ochazuke.Do("pane.preview")).
		Bind("K", ochazuke.Do("pane.kill"))
	r.Define("panes").Verb("refresh", a.refreshPanes, "Reload the pane list from the daemon")
}

// addPanesPage builds the page: the list first, so the page starts on it.
func (a *App) addPanesPage() error {
	a.r.Instance(panesPage, ochazuke.Kind("page"))
	list, err := a.r.AddList(paneListID, nil, ochazuke.Parent(panesPage))
	if err != nil {
		return err
	}
	a.paneList = list
	_, err = a.r.AddPage(panesPage, "Panes", a.renderPanes, ochazuke.Focus(paneListID))
	return err
}

// refreshPanes is panes.refresh: list the panes off the loop, then push them.
func (a *App) refreshPanes(ctx ochazuke.Ctx) ochazuke.Result {
	d := a.panesd
	return ochazuke.Pending(func(c context.Context) (any, error) {
		return d.ListPanes(c)
	}).ReplacingAt(paneListID, "refresh").Then(func(_ ochazuke.Ctx, v any, err error) {
		panes, _ := v.([]pane)
		a.setPanes(panes, err)
	})
}

// reloadPanes runs panes.refresh on the list.
func (a *App) reloadPanes() error {
	_, err := a.r.Execute(ochazuke.Request{Verb: "refresh", Noun: "panes", Target: paneListID})
	return err
}

// setPanes stores a listing and pushes it to the list; a failed listing
// empties the list and keeps the error for the hint.
func (a *App) setPanes(panes []pane, err error) {
	a.paneErr = ""
	if err != nil {
		panes, a.paneErr = nil, err.Error()
	}
	a.panes = panes
	items := make([]ochazuke.ListItem, 0, len(panes))
	for _, p := range panes {
		items = append(items, ochazuke.ListItem{ID: p.rowID(), Label: p.label(), Noun: "pane"})
	}
	if err := a.paneList.SetItems(items); err != nil {
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
	if err := a.paneList.Select(row); err != nil {
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
	return ochazuke.Pending(func(c context.Context) (any, error) {
		text, err := d.CapturePane(c, p)
		if err != nil {
			return nil, fmt.Errorf("preview %s: %w", p.label(), err)
		}
		return preview{Pane: p.rowID(), Text: text}, nil
	}).ReplacingAt(paneListID, "preview").Then(func(_ ochazuke.Ctx, v any, err error) {
		if got, ok := v.(preview); ok && err == nil {
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
	if err := a.closeTerminal(); err != nil {
		return ochazuke.Failed(err)
	}
	stream := a.panesd.OpenStream(p)
	if _, err := ctx.Runtime.AddTerminal(paneTermID, stream, ochazuke.Parent(panesPage), ochazuke.CloseOnDetach(), ochazuke.FocusOnClose(ctx.Instance.ID)); err != nil {
		stream.Release() // never added, so no Terminal.Close releases it
		return ochazuke.Failed(err)
	}
	a.attached = p
	if err := ctx.Runtime.SetFocus(paneTermID); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(p.label())
}

// closeTerminal removes the attached terminal; Terminal.Close releases
// its stream.
func (a *App) closeTerminal() error {
	a.attached = pane{}
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
	if a.r.Terminal(paneTermID) != nil && a.attached.rowID() == p.rowID() {
		if err := a.closeTerminal(); err != nil {
			return ochazuke.Failed(err)
		}
	}
	d := a.panesd
	return ochazuke.Pending(func(c context.Context) (any, error) {
		if err := d.KillPane(c, p); err != nil {
			return nil, fmt.Errorf("kill %s: %w", p.label(), err)
		}
		return d.ListPanes(c)
	}).Then(func(_ ochazuke.Ctx, v any, err error) {
		if err == nil {
			panes, _ := v.([]pane)
			a.setPanes(panes, nil)
		}
	})
}

// renderPanes draws the pane list over the detail: a Layout.Panel titled
// with the pane's session/name holding the attached terminal (focused
// style while keys go to it, and its cursor) or the last preview. The
// terminal is given the panel's inner cells (PanelContentSize), so the
// stream's size lease matches what is shown.
func (a *App) renderPanes(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
	s := ochazuke.NewStyles(*a.m.Appearance)
	layout := ochazuke.Layout{Width: width, Height: height, Styles: s}
	top := max(1, (height-3)/3)
	head := fmt.Sprintf("panes (%d)", len(a.panes))
	list := a.view(a.paneList, width, top)
	switch {
	case a.paneErr != "":
		head = "panes: daemon not reachable"
		list = a.fill(ansi.Truncate(a.paneErr, width, "…"), top)
	case len(a.panes) == 0:
		list = a.fill("no panes; n opens a shell, or launch an agent from the work page", top)
	}
	if a.landErr != "" {
		head += ": " + a.landErr
	}
	rows := max(1, height-2-top)
	cw, ch := ochazuke.PanelContentSize(width, rows)
	var detail ochazuke.Block
	if t := a.r.Terminal(paneTermID); t != nil {
		title := a.attached.label()
		if e := t.Err(); e != "" {
			title += ": " + e
		}
		body := ochazuke.Text(t.View(cw, ch))
		if t.Focused() {
			body = t.ViewBlock(cw, ch)
		}
		detail = layout.PanelBlock(title, body, width, rows, t.Focused())
	} else if a.preview.Pane != "" {
		title := a.preview.Pane
		for _, p := range a.panes {
			if p.rowID() == a.preview.Pane {
				title = p.label()
			}
		}
		detail = ochazuke.Text(layout.Panel(title+" · preview", a.fill(a.preview.Text, ch, cw), width, rows, false))
	} else {
		detail = ochazuke.Text(layout.Panel("detail", "p previews, enter attaches", width, rows, false))
	}
	above := 1 + top // the head row and the list
	return detail.Offset(0, above).WithText(s.Muted.Render(head) + "\n" + list + "\n" + detail.Text + "\n" + s.Muted.Render(ansi.Truncate(a.rail(), width, "…")))
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

// ListPanes lists every window of every session the daemon reports.
func (t tuios) ListPanes(ctx context.Context) ([]pane, error) {
	ctx, cancel := context.WithTimeout(ctx, daemonTimeout)
	defer cancel()
	sessions, err := t.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	var out []pane
	for _, s := range sessions {
		windows, err := t.Panes(ctx, s.Name)
		if err != nil {
			return nil, fmt.Errorf("list windows of %s: %w", s.Name, err)
		}
		for _, w := range windows {
			out = append(out, pane{Session: s.Name, Pane: w})
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
