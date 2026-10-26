package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/harness"
	"github.com/unixsuperhero/matomeru/internal/store"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
	"golang.org/x/sys/unix"
)

func (a *App) defineSides() {
	a.sides = make(map[string]sideState)
	a.companionPanes = make(map[string]pane)
	a.sideStarting = make(map[string]bool)
	a.r.Define("pane").Verb("side", a.sidePane, "Open or list this pane's side vim and terminal").Positional("kind")
	a.parameters("pane.side", map[string]ochazuke.Parameter{
		"kind": {Type: "string", Required: true, Enum: []string{"vim", "term", "list"}},
		"pane": {Type: "string", Description: "The parent's window id (as pane list prints it), instead of --target pane:SESSION/WINDOW"},
	})
	a.r.Define("app").
		Verb("close", a.quitWithSides, "Exit after closing side panes").
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
	if a.sideStarting[key] {
		return ochazuke.Failed(errors.New("side pane is still opening"))
	}
	a.sideStarting[key] = true
	pd, ws, launcher, dir := a.panesd, a.work, a.launcher, a.vimDir
	result := ochazuke.Pending(a.withSideLock(func(c context.Context) (any, error) {
		panes, err := sideListing(c, pd, ws)
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
		links, err := ws.ReconcileCompanions(live)
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
		driver := &recordingDriver{Driver: &sideDriver{Driver: launcher, work: ws, parent: parent, kind: kind, env: recordedEnvironment(recipe)}, work: ws, owner: owner}
		if kind == "vim" {
			id := work.NewID()
			path, err := filepath.Abs(filepath.Join(dir, id+".vim"))
			if err != nil {
				return nil, err
			}
			session := work.VimSession{ID: id, Name: "side-vim", Path: path, OwnedPath: path, Session: parent.Session, Cwd: parent.Cwd, ProjectID: owner.ProjectID, TaskID: owner.TaskID, Created: work.Now()}
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
		_, err = driver.OpenWindow(c, harness.Window{Session: parent.Session, Name: "side-term", Cwd: parent.Cwd, Command: []string{shell}})
		if err != nil {
			return nil, err
		}
		panes, err = pd.ListPanes(c)
		return sideOpened{Parent: parent, Panes: panes, Note: strings.Join(driver.notes, "; ")}, err
	})).Then(func(_ ochazuke.Ctx, value any, err error) {
		delete(a.sideStarting, key)
		if err != nil {
			return
		}
		done := value.(sideOpened)
		a.setPanes(done.Panes, nil)
		if done.Note != "" {
			a.m.Notify(done.Note)
		}
		if kind == "list" || a.cli {
			return
		}
		// A completion must not move someone who switched away during launch.
		if a.attached.ID != done.Parent.ID {
			return
		}
		if err := a.showSide(done.Parent, kind); err != nil {
			a.m.Notify(err.Error())
		}
	})
	if a.cli {
		return result
	}
	ticket, err := a.r.Launch(paneListID, "pane.side", result, ctx)
	if err != nil {
		delete(a.sideStarting, key)
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

type sideDriver struct {
	harness.Driver
	work   *work.Store
	parent pane
	kind   string
	env    map[string]string
}

func (d *sideDriver) OpenWindow(ctx context.Context, w harness.Window) (string, error) {
	w.Session, w.Cwd = d.parent.Session, d.parent.Cwd
	w.Command = savedCommand(work.PaneRecipe{Command: w.Command, Env: d.env})
	id, err := d.Driver.OpenWindow(ctx, w)
	if err != nil {
		return id, err
	}
	if id == "" {
		return "", errors.New("side launch returned no pane id")
	}
	if err := d.work.PutCompanion(work.Companion{Window: id, Parent: d.parent.ID, Kind: d.kind}); err != nil {
		return id, fmt.Errorf("side pane %s is open but its link could not be saved: %w", id, err)
	}
	return id, nil
}

func (a *App) checkCompanions(ctx context.Context, parent string) error {
	links, err := a.work.Companions()
	if err != nil {
		return err
	}
	if len(links) == 0 {
		return nil
	}
	panes, err := sideListing(ctx, a.panesd, a.work)
	if err != nil {
		return fmt.Errorf("cannot verify side panes: %w", err)
	}
	live := make(map[string]bool, len(panes))
	labels := make(map[string]string, len(panes))
	for _, p := range panes {
		live[p.ID] = !p.Exited
		labels[p.ID] = p.label()
	}
	links, err = a.work.ReconcileCompanions(live)
	if err != nil {
		return err
	}
	var messages []string
	for _, link := range links {
		if parent != "" && parent != link.Parent {
			continue
		}
		kind, key := "terminal", "t"
		if link.Kind == "vim" {
			kind, key = "vim", "v"
		}
		messages = append(messages, fmt.Sprintf("close side %s of %s first (ctrl+a %s)", kind, labels[link.Parent], key))
	}
	if len(messages) > 0 {
		return errors.New(strings.Join(messages, "; "))
	}
	return nil
}

func (a *App) quitWithSides(ochazuke.Ctx) ochazuke.Result {
	if len(a.sideStarting) > 0 {
		return ochazuke.Failed(errors.New("wait for side panes to finish opening before quitting"))
	}
	links, err := a.work.Companions()
	if err != nil {
		return ochazuke.Failed(err)
	}
	if len(links) == 0 {
		a.r.Quit = true
		return ochazuke.Done(true)
	}
	return ochazuke.Pending(func(c context.Context) (any, error) { return true, a.checkCompanions(c, "") }).Then(func(_ ochazuke.Ctx, _ any, err error) {
		if err == nil {
			a.r.Quit = true
		}
	})
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

// sideListing lists the panes and marks the windows whose process exited.
// An exited side pane is closed and its link dropped (vim and terminal
// alike), so it never comes back as a plain row; any other exited window
// stays listed, marked Exited, until someone kills it. Links whose window
// or parent is gone are dropped too; a side pane whose parent exited
// becomes an ordinary pane. Callers that may race a side launch hold the
// side lock.
func sideListing(ctx context.Context, pd paneDaemon, ws *work.Store) ([]pane, error) {
	panes, err := pd.ListPanes(ctx)
	if err != nil {
		return nil, err
	}
	links, err := ws.Companions()
	if err != nil {
		return nil, err
	}
	exited, gone := probeExits(ctx, pd, panes)
	side := make(map[string]bool, len(links))
	for _, link := range links {
		side[link.Window] = true
	}
	out := panes[:0]
	live := make(map[string]bool, len(panes))
	for _, p := range panes {
		if gone[p.ID] {
			continue
		}
		if exited[p.ID] {
			if side[p.ID] {
				// The side pane's program ended (exit, :qa!): close the
				// window mtm opened rather than leave a dead row behind.
				if err := pd.KillPane(ctx, p); err != nil {
					var remote *daemon.Error
					if !errors.As(err, &remote) || remote.Code != "window_not_found" {
						return nil, fmt.Errorf("close exited side pane %s: %w", p.label(), err)
					}
				}
				continue
			}
			p.Exited = true
		}
		if p.ID != "" && !p.Exited {
			live[p.ID] = true
		}
		out = append(out, p)
	}
	if len(links) > 0 {
		if _, err := ws.ReconcileCompanions(live); err != nil {
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
