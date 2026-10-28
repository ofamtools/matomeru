package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/harness"
	"github.com/unixsuperhero/matomeru/internal/turns"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/matomeru/store"
	"github.com/unixsuperhero/ochazuke"
	"golang.org/x/sys/unix"
)

func (a *App) defineSides() {
	a.sides = make(map[string]sideState)
	a.companionPanes = make(map[string]pane)
	a.sideStarting = make(map[string]bool)
	a.r.Define("pane").Verb("side", a.sidePane, "Open or list this pane's side vim and terminal").Positional("kind")
	a.r.Define("panes").Verb("sides", a.chooseSide, "Choose any open side vim or terminal: attach its pane, show the side and focus it")
	a.parameters("pane.side", map[string]ochazuke.Parameter{
		"kind": {Type: "string", Required: true, Enum: []string{"vim", "term", "list"}},
		"pane": {Type: "string", Description: "The parent's window id (as pane list prints it), instead of --target pane:SESSION/WINDOW"},
	})
	a.r.Define("app").
		// Quiet: the wait for an opening side is one footer line, not a ticker row too.
		Verb("close", a.quitApp, "Exit; side panes keep running and come back on the next start").Quiet().
		Verb("quit", func(r *ochazuke.Runtime, _ ochazuke.Context, _ ochazuke.Arguments) (any, error) {
			return r.Execute(ochazuke.Request{Noun: "app", Verb: "close", Target: "app"})
		})
}

func (a *App) mergeCompanions(panes []pane) ([]pane, error) {
	live := make(map[string]bool, len(panes))
	for _, p := range panes {
		live[p.ID] = true
	}
	links, err := a.work.Companions()
	if err != nil {
		return panes, err
	}
	for id, state := range a.sides {
		state.SideVim, state.SideTerm = "", ""
		a.sides[id] = state
	}
	children := make(map[string]bool, len(links))
	for _, link := range links {
		if !live[link.Window] || !live[link.Parent] {
			continue
		}
		state := a.sides[link.Parent]
		if link.Kind == "vim" {
			state.SideVim = link.Window
		} else {
			state.SideTerm = link.Window
		}
		a.sides[link.Parent] = state
		children[link.Window] = true
	}
	a.companionPanes = make(map[string]pane, len(links))
	out := make([]pane, 0, len(panes))
	for _, p := range panes {
		if children[p.ID] {
			a.companionPanes[p.ID] = p
			continue
		}
		state := a.sides[p.ID]
		if state.SideVim == "" {
			state.ShowVim = false
		}
		if state.SideTerm == "" {
			state.ShowTerm = false
		}
		a.sides[p.ID] = state
		out = append(out, a.sideFields(p))
	}
	return out, nil
}

func (a *App) sidePane(ctx ochazuke.Ctx) ochazuke.Result {
	kind := ochazuke.StringArg(ctx.Args, "kind", "")
	if kind != "vim" && kind != "term" && kind != "list" {
		return ochazuke.Failed(errors.New("side kind must be vim, term or list"))
	}
	p := a.attached
	if ctx.Instance != nil && ctx.Instance.Noun == "pane" {
		var err error
		p, err = a.paneAt(ctx.Instance)
		if err != nil {
			return ochazuke.Failed(err)
		}
	}
	if id := ochazuke.StringArg(ctx.Args, "pane", ""); id != "" {
		// The job finds the parent by its window id in its own listing.
		p = pane{Pane: daemon.Pane{ID: id}}
	}
	if p.ID == "" {
		return ochazuke.Failed(errors.New("no parent pane selected: pass --pane WINDOW_ID or --target pane:SESSION/WINDOW"))
	}
	key := p.ID + "/" + kind
	if a.quitAfterSides {
		return ochazuke.Failed(errors.New("mtm is quitting"))
	}
	if a.sideStarting[key] {
		return ochazuke.Failed(errors.New("side pane is still opening"))
	}
	a.sideStarting[key] = true
	pd, ws, launcher, dir, server, projects, started := a.panesd, a.work, a.launcher, a.vimDir, a.server(), a.claudeProjects, a.agentStarted
	fit := a.sideFit()
	result := ochazuke.Pending(a.withSideLock(func(c context.Context) (any, error) {
		panes, err := sideListing(c, pd, ws, server)
		if err != nil {
			return nil, err
		}
		parent := pane{}
		live := make(map[string]bool, len(panes))
		for _, row := range panes {
			live[row.ID] = !row.Exited
			if row.ID == p.ID && !row.Exited {
				parent = row
			}
		}
		if parent.ID == "" {
			return nil, errors.New("parent pane has exited")
		}
		links, err := ws.ReconcileCompanions(server, live)
		if err != nil {
			return nil, err
		}
		var selected []work.Companion
		for _, link := range links {
			if link.Parent == parent.ID {
				selected = append(selected, link)
				if link.Kind == kind {
					return sideOpened{Parent: parent, Panes: panes, Links: selected}, nil
				}
			}
			if link.Window == parent.ID {
				return nil, errors.New("a side pane cannot have side panes")
			}
		}
		if kind == "list" {
			return sideOpened{Parent: parent, Panes: panes, Links: selected}, nil
		}
		assignment, err := ws.PaneOwner(parent.ID, parent.SessionID)
		if err != nil {
			return nil, err
		}
		owner := Owner{ProjectID: assignment.ProjectID, TaskID: assignment.TaskID}
		recipe, err := ws.PaneRecipe(parent.ID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
		cwd := sideCwd(agentCwd(c, ws, projects, parent, panes, started), parent, sideProjectDir(ws, owner.ProjectID), recipe)
		side := &sideDriver{Driver: launcher, work: ws, server: server, parent: parent, cwd: cwd, kind: kind, env: recordedEnvironment(recipe), fit: fit}
		driver := &recordingDriver{Driver: side, work: ws, owner: owner}
		side.note = driver.note
		if kind == "vim" {
			id := work.NewID()
			path, err := filepath.Abs(filepath.Join(dir, id+".vim"))
			if err != nil {
				return nil, err
			}
			session := work.VimSession{ID: id, Name: "side-vim", Path: path, OwnedPath: path, Session: parent.Session, Cwd: cwd, ProjectID: owner.ProjectID, TaskID: owner.TaskID, Created: work.Now()}
			value, err := launchVimJob(session, "", false, driver, pd, ws, dir, parent.Session)(c)
			if err != nil {
				return nil, err
			}
			done := value.(vimStarted)
			return sideOpened{Parent: parent, Panes: done.Launch.Panes, Note: done.Launch.Note}, nil
		}
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		_, err = driver.OpenWindow(c, harness.Window{Session: parent.Session, Workspace: parent.Workspace, Name: "side-term", Cwd: cwd, Command: []string{shell}})
		if err != nil {
			return nil, err
		}
		panes, err = pd.ListPanes(c)
		return sideOpened{Parent: parent, Panes: panes, Note: strings.Join(driver.notes, "; ")}, err
	})).Then(func(ctx ochazuke.Ctx, value any, err error) {
		a.sideSettled(key)
		if err != nil || a.r.Quit {
			return
		}
		done := value.(sideOpened)
		a.setPanes(done.Panes, nil)
		if done.Note != "" {
			ctx.Notify(done.Note)
		}
		if kind == "list" || a.cli {
			return
		}
		// A completion must not move someone who switched away during launch.
		if a.attached.ID != done.Parent.ID {
			return
		}
		if err := a.showSide(done.Parent, kind); err != nil {
			ctx.Notify(err.Error())
		}
	})
	if a.cli {
		return result
	}
	ticket, err := a.r.Launch(paneListID, "pane.side", result, ctx)
	if err != nil {
		a.sideSettled(key)
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(ticket)
}

type sideOpened struct {
	Parent pane             `json:"parent"`
	Panes  []pane           `json:"-"`
	Links  []work.Companion `json:"companions,omitempty"`
	Note   string           `json:"note,omitempty"`
}

// sideDriver opens a side pane in its parent's context: the parent's
// session and workspace, the folder the parent is in now (sideCwd) and the
// environment the parent was launched with.
type sideDriver struct {
	harness.Driver
	work   *work.Store
	server string
	parent pane
	cwd    string
	kind   string
	env    map[string]string
	// fit sizes the new window to the side's region (sideFit); nil when
	// the region is not known yet.
	fit  func(ctx context.Context, session, window string) error
	note func(error) // the launch's notes (recordingDriver.note)
}

func (d *sideDriver) OpenWindow(ctx context.Context, w harness.Window) (string, error) {
	w.Session, w.Workspace, w.Cwd = d.parent.Session, d.parent.Workspace, d.cwd
	w.Command = savedCommand(work.PaneRecipe{Command: w.Command, Env: d.env})
	id, err := d.Driver.OpenWindow(ctx, w)
	if err != nil {
		return id, err
	}
	if id == "" {
		return "", errors.New("side launch returned no pane id")
	}
	if err := d.work.PutCompanion(work.Companion{Window: id, Parent: d.parent.ID, Kind: d.kind, Server: d.server}); err != nil {
		return id, fmt.Errorf("side pane %s is open but its link could not be saved: %w", id, err)
	}
	if d.fit != nil {
		// A failed fit never fails the launch (as matomeru-old's fit): the
		// lease mtm takes once it shows the side sizes the pane as well.
		// It is noted on the launch instead.
		if err := d.fit(ctx, w.Session, id); err != nil && d.note != nil {
			d.note(fmt.Errorf("side pane resize: %w", err))
		}
	}
	return id, nil
}

// windowFitter is a daemon that can set a window's size (tuios's resize).
type windowFitter interface {
	FitWindow(ctx context.Context, p pane, cols, rows int) error
}

// windowOwner is a stream that asks the daemon for each size it is drawn
// at (liveStream.own); App.openStream makes every stream mtm draws one.
type windowOwner interface {
	own(fit func(ctx context.Context, cols, rows int) error)
}

// sideFit sizes a new side window to the side's region in the detail
// area the last frame drew. TUIOS creates a window at its session's size,
// the whole screen of the client that last sized it, and a side would
// start there until its stream's lease shrinks it.
func (a *App) sideFit() func(ctx context.Context, session, window string) error {
	fitter, ok := a.panesd.(windowFitter)
	cols, rows := a.sideContentSize()
	if !ok || cols <= 0 || rows <= 0 {
		return nil
	}
	return func(ctx context.Context, session, window string) error {
		return fitter.FitWindow(ctx, pane{Session: session, Pane: daemon.Pane{ID: window}}, cols, rows)
	}
}

func (a *App) checkCompanions(ctx context.Context, parent string) error {
	links, err := a.work.Companions()
	if err != nil {
		return err
	}
	if len(links) == 0 {
		return nil
	}
	panes, err := sideListing(ctx, a.panesd, a.work, a.server())
	if err != nil {
		return fmt.Errorf("cannot verify side panes: %w", err)
	}
	live := make(map[string]bool, len(panes))
	labels := make(map[string]string, len(panes))
	for _, p := range panes {
		live[p.ID] = !p.Exited
		labels[p.ID] = p.label()
	}
	links, err = a.work.ReconcileCompanions(a.server(), live)
	if err != nil {
		return err
	}
	var messages []string
	for _, link := range links {
		if parent != link.Parent {
			continue
		}
		// ctrl+a v / t only shows the side pane; the program inside closes it.
		how := fmt.Sprintf("side terminal of %s (%s t, then exit)", labels[link.Parent], a.r.TerminalLeader())
		if link.Kind == "vim" {
			how = fmt.Sprintf("side vim of %s (%s v, then :qa)", labels[link.Parent], a.r.TerminalLeader())
		}
		messages = append(messages, how)
	}
	if len(messages) > 0 {
		return errors.New("close the side panes first: " + strings.Join(messages, "; "))
	}
	return nil
}

// quitApp is app.close: mtm exits and the side panes keep running; their
// saved links reattach them to their parents on the next start. A side
// still opening may not have saved its link yet, so quit waits for it.
func (a *App) quitApp(ctx ochazuke.Ctx) ochazuke.Result {
	if len(a.sideStarting) > 0 {
		a.quitAfterSides = true
		ctx.Notify("quitting once the side pane finishes opening")
		return ochazuke.Done(false)
	}
	a.r.Quit = true
	return ochazuke.Done(true)
}

// sideSettled ends a side launch, and the quit that waited for it.
func (a *App) sideSettled(key string) {
	delete(a.sideStarting, key)
	if a.quitAfterSides && len(a.sideStarting) == 0 {
		a.r.Quit = true
	}
}

// The lock covers the daemon creation and the link write across CLI processes.
func (a *App) withSideLock(fn func(context.Context) (any, error)) func(context.Context) (any, error) {
	path := a.sideLock
	return func(ctx context.Context) (any, error) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
			return nil, err
		}
		defer unix.Flock(int(f.Fd()), unix.LOCK_UN)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return fn(ctx)
	}
}

// sideCwd is where a side pane of parent starts: the folder the agent in
// it works in (agent, agentCwd), else the folder TUIOS reports for the
// parent now (its shell's OSC 7, else its process's folder, so a cd in a
// shell moves it), else its project's folder (projectDir), else the folder
// mtm launched the parent in.
//
// An agent that changes folder inside its own process (Claude Code's Bash
// cd) does not move what TUIOS reports: the pane's process stays where it
// started. Its transcript says where it is; TUIOS hears the same folder in
// its hook reports (agent_cwd) but does not publish it.
func sideCwd(agent string, parent pane, projectDir string, recipe work.PaneRecipe) string {
	for _, dir := range []string{agent, parent.Cwd, projectDir, recipe.Cwd} {
		if dir != "" {
			return dir
		}
	}
	return ""
}

// agentCwdWait bounds agentCwd: a side never waits on a transcript.
const agentCwdWait = 300 * time.Millisecond

// agentCwd is the folder a Claude Code parent's agent works in, from the
// end of its newest session's transcript (the log the Turns tab reads,
// turns.ClaudeLogCwd; the current one found as the tab finds it,
// claudeLookup, against the listing panes); "" for another harness, a
// transcript not found or not read within agentCwdWait. omp's log names
// only the folder it started in, so it is not read for this.
func agentCwd(ctx context.Context, ws *work.Store, projects string, parent pane, panes []pane, started func(pane) time.Time) string {
	if !isClaude(parent) || projects == "" {
		return ""
	}
	var sessions []string
	if rec, err := ws.PaneSessions(parent.ID); err == nil {
		for i := len(rec.Sessions) - 1; i >= 0; i-- {
			sessions = append(sessions, rec.Sessions[i])
		}
	}
	got := make(chan string, 1)
	go func() {
		if path, _, err := turns.ResolveClaudeLog(claudeLookup(projects, ws, parent, panes, started)); err == nil {
			got <- turns.ClaudeLogCwd(path)
			return
		}
		for _, s := range sessions {
			if path, err := turns.FindClaudeLog(projects, parent.Cwd, s); err == nil {
				got <- turns.ClaudeLogCwd(path)
				return
			}
		}
		got <- ""
	}()
	ctx, cancel := context.WithTimeout(ctx, agentCwdWait)
	defer cancel()
	select {
	case cwd := <-got:
		return cwd
	case <-ctx.Done():
		return ""
	}
}

// sideProjectDir is the folder of the project id names, or "" when the pane
// has no project or it cannot be read: a side never waits on its owner.
func sideProjectDir(ws *work.Store, id string) string {
	if id == "" {
		return ""
	}
	p, err := ws.Project(id)
	if err != nil {
		return ""
	}
	return projectDir(p)
}

func recordedEnvironment(recipe work.PaneRecipe) map[string]string {
	env := make(map[string]string, len(recipe.Env))
	for key, value := range recipe.Env {
		env[key] = value
	}
	if len(recipe.Command) > 0 && recipe.Command[0] == "env" {
		for _, arg := range recipe.Command[1:] {
			key, value, ok := strings.Cut(arg, "=")
			if !ok || key == "" {
				break
			}
			env[key] = value
		}
	}
	return env
}

// exitProber is a daemon that can tell whether a window's process has
// exited: TUIOS keeps a detached window listed after its program ends.
type exitProber interface {
	Exited(ctx context.Context, p pane) (bool, error)
}

// probeLimit is how many exit probes run at once.
const probeLimit = 8

// server is the TUIOS daemon socket this mtm lists, which a companion
// link records (work.Companion).
func (a *App) server() string {
	if a.daemon == nil || a.daemon.Socket == "" {
		return ""
	}
	return filepath.Clean(a.daemon.Socket)
}

// sideListing closes exited windows and reconciles companion links.
// Surviving companions whose parent exited become ordinary panes.
// Callers that may race a side launch hold the side lock.
func sideListing(ctx context.Context, pd paneDaemon, ws *work.Store, server string) ([]pane, error) {
	panes, err := pd.ListPanes(ctx)
	if err != nil {
		return nil, err
	}
	links, err := ws.Companions()
	if err != nil {
		return nil, err
	}
	exited, gone := probeExits(ctx, pd, panes)
	out := panes[:0]
	live := make(map[string]bool, len(panes))
	for _, p := range panes {
		if gone[p.ID] {
			continue
		}
		if exited[p.ID] {
			if err := pd.KillPane(ctx, p); err != nil {
				var remote *daemon.Error
				if !errors.As(err, &remote) || remote.Code != "window_not_found" {
					return nil, fmt.Errorf("close exited pane %s: %w", p.label(), err)
				}
			}
			continue
		}
		if p.ID != "" && !p.Exited {
			live[p.ID] = true
		}
		out = append(out, p)
	}
	if len(links) > 0 {
		if _, err := ws.ReconcileCompanions(server, live); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// probeExits asks the daemon, a few windows at a time, which windows'
// processes have exited and which windows are gone. A daemon that cannot
// tell (a test fake) reports none; a probe that fails leaves its window
// counted as running.
func probeExits(ctx context.Context, pd paneDaemon, panes []pane) (exited, gone map[string]bool) {
	exited, gone = map[string]bool{}, map[string]bool{}
	prober, ok := pd.(exitProber)
	if !ok {
		return exited, gone
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, probeLimit)
	for _, p := range panes {
		if p.ID == "" {
			continue
		}
		wg.Add(1)
		slots <- struct{}{}
		go func(p pane) {
			defer func() { <-slots; wg.Done() }()
			dead, err := prober.Exited(ctx, p)
			var remote *daemon.Error
			mu.Lock()
			defer mu.Unlock()
			switch {
			case errors.As(err, &remote) && remote.Code == "window_not_found":
				gone[p.ID] = true
			case err == nil && dead:
				exited[p.ID] = true
			}
		}(p)
	}
	wg.Wait()
	return exited, gone
}
