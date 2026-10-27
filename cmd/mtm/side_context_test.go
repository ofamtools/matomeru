package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/harness"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

// windowRecorder keeps every window a launch asks its driver for.
type windowRecorder struct {
	harness.Driver
	windows []harness.Window
}

func (d *windowRecorder) OpenWindow(ctx context.Context, w harness.Window) (string, error) {
	d.windows = append(d.windows, w)
	return d.Driver.OpenWindow(ctx, w)
}

// sideContextApp is shellApp whose first pane, the parent, sits on
// workspace ws in folder cwd, launched (by mtm) in /launched; every window
// the side launch opens is recorded.
func sideContextApp(t *testing.T, ws int, cwd string) (*App, pane, *windowRecorder) {
	t.Helper()
	a, driver := shellApp(t)
	a.cli = true
	f := driver.panes
	f.panes[0].Workspace, f.panes[0].Cwd = ws, cwd
	launched := t.TempDir()
	if err := a.work.PutPaneRecipe(work.PaneRecipe{Window: f.panes[0].ID, Cwd: launched}); err != nil {
		t.Fatal(err)
	}
	rec := &windowRecorder{Driver: &sideLauncher{fakeDaemon: driver, vim: &vimProcessDriver{t: t, panes: f}}}
	a.launcher = rec
	return a, f.panes[0], rec
}

func openSide(t *testing.T, a *App, p pane, kind string) {
	t.Helper()
	if _, err := a.r.Execute(ochazuke.Request{Noun: "pane", Verb: "side", Target: p.rowID(), Args: ochazuke.Arguments{"kind": kind}}); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	for _, job := range jobsOf(a, "pane.side") {
		if job.Error != "" {
			t.Fatal(job.Error)
		}
	}
}

// A side vim or terminal opens in its parent's context (owner,
// 2026-10-27): the parent's session, its workspace (not the one TUIOS
// shows or an empty one) and the folder the parent is in now.
func TestSideOpensOnTheParentsWorkspaceInItsCurrentFolder(t *testing.T) {
	for _, kind := range []string{"term", "vim"} {
		t.Run(kind, func(t *testing.T) {
			if _, err := exec.LookPath("nvim"); kind == "vim" && err != nil {
				t.Skip("nvim not installed; the side vim launch needs it")
			}
			now := t.TempDir()
			a, parent, rec := sideContextApp(t, 4, now)
			openSide(t, a, parent, kind)
			if len(rec.windows) != 1 {
				t.Fatalf("windows %+v", rec.windows)
			}
			got := rec.windows[0]
			if got.Session != parent.Session || got.Workspace != 4 || got.Cwd != now {
				t.Fatalf("side %s window %+v, want session %s workspace 4 cwd %s", kind, got, parent.Session, now)
			}
			if kind != "vim" {
				return
			}
			sessions, err := a.work.VimSessions()
			if err != nil || len(sessions) != 1 || sessions[0].Cwd != now || sessions[0].Session != parent.Session {
				t.Fatalf("vim sessions %+v %v", sessions, err)
			}
		})
	}
}

// A parent whose workspace and folder TUIOS does not report: the driver
// picks the workspace (as for any new pane) and the side starts where mtm
// launched the parent.
func TestSideFallsBackWhenTheParentsContextIsUnknown(t *testing.T) {
	a, parent, rec := sideContextApp(t, 0, "")
	recipe, err := a.work.PaneRecipe(parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	openSide(t, a, parent, "term")
	if len(rec.windows) != 1 || rec.windows[0].Workspace != 0 || rec.windows[0].Cwd != recipe.Cwd {
		t.Fatalf("windows %+v, want workspace 0 cwd %s", rec.windows, recipe.Cwd)
	}
}

// verbDaemon answers the launch verbs the way TUIOS does (an attached
// session on workspace 1 holding one short window, so LaunchWorkspace
// chooses empty workspace 2) and records each request.
func verbDaemon(t *testing.T) (string, func() []string, func() map[string]any) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mtmv")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	results := map[string]any{
		"session-info":    map[string]any{"current_workspace": 1, "tui_attached": true, "tiling_mode": "tiling", "layout_mode": "scrolling", "height": 59},
		"list-windows":    map[string]any{"windows": []any{map[string]any{"window_id": "p", "workspace": 1, "height": 20}}},
		"list-workspaces": map[string]any{"workspaces": []any{map[string]any{"workspace": 1, "window_count": 1}, map[string]any{"workspace": 2, "window_count": 0}}},
		"new-window":      map[string]any{"window_id": "side", "workspace": 0},
	}
	var mu sync.Mutex
	var verbs []string
	var last map[string]any
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadBytes('\n')
					if err != nil {
						return
					}
					var req struct {
						ID     string         `json:"id"`
						Verb   string         `json:"verb"`
						Params map[string]any `json:"params"`
					}
					if json.Unmarshal(line, &req) != nil {
						return
					}
					mu.Lock()
					verbs = append(verbs, req.Verb)
					if req.Verb == "new-window" {
						last = req.Params
					}
					mu.Unlock()
					reply := map[string]any{"id": req.ID}
					if res, ok := results[req.Verb]; ok {
						reply["result"] = res
					} else {
						reply["error"] = map[string]any{"code": "unknown_verb", "message": req.Verb}
					}
					out, _ := json.Marshal(reply)
					if _, err := c.Write(append(out, '\n')); err != nil {
						return
					}
				}
			}(c)
		}
	}()
	return sock, func() []string {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(verbs)
		}, func() map[string]any {
			mu.Lock()
			defer mu.Unlock()
			return last
		}
}

// The TUIOS driver opens a window on the workspace it is given without
// choosing one; given none, it chooses as before (here an empty one).
func TestTheDriverOpensOnTheNamedWorkspace(t *testing.T) {
	for _, c := range []struct {
		workspace int
		verbs     []string
		want      float64
	}{
		{4, []string{"new-window"}, 4},
		{0, []string{"session-info", "list-windows", "list-workspaces", "new-window"}, 2},
	} {
		sock, verbs, last := verbDaemon(t)
		id, err := tuios{daemon.New(sock)}.OpenWindow(context.Background(), harness.Window{Session: "work", Workspace: c.workspace, Cwd: "/tmp", Command: []string{"/bin/sh"}})
		if err != nil || id != "side" {
			t.Fatalf("workspace %d: %q %v", c.workspace, id, err)
		}
		if got := verbs(); !slices.Equal(got, c.verbs) {
			t.Fatalf("workspace %d: verbs %v, want %v", c.workspace, got, c.verbs)
		}
		if got := last()["workspace"]; got != c.want {
			t.Fatalf("workspace %d: new-window workspace %v, want %v", c.workspace, got, c.want)
		}
	}
}
