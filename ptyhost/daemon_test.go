package ptyhost

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeDaemon answers the daemon's command line from memory.
type fakeDaemon struct {
	mu     sync.Mutex
	calls  []string
	screen map[string]string // pane id -> what capture-pane prints
	fail   string            // a command that fails
}

func (d *fakeDaemon) exec(_ context.Context, name string, args ...string) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	line := name + " " + strings.Join(args, " ")
	d.calls = append(d.calls, line)
	i := slices.IndexFunc(args, func(a string) bool {
		return strings.HasSuffix(a, "-pane") || strings.HasSuffix(a, "-panes") || a == "send-keys"
	})
	if i < 0 {
		return nil, errors.New("no command")
	}
	cmd, id := args[i], ""
	if j := slices.Index(args, "--id"); j >= 0 && j+1 < len(args) {
		id = args[j+1]
	}
	if cmd == d.fail {
		return nil, errors.New("daemon is gone")
	}
	switch cmd {
	case "new-pane":
		if d.screen == nil {
			d.screen = map[string]string{}
		}
		d.screen[id] = `{"lines": ["$ "], "cursor": {"x": 2, "y": 0}, "cols": 80, "rows": 24, "pid": 4242}`
	case "capture-pane":
		out, ok := d.screen[id]
		if !ok {
			return nil, errors.New("no pane " + id)
		}
		return []byte(out), nil
	case "kill-pane":
		delete(d.screen, id)
	case "list-panes":
		return []byte(`[{"id": "w1", "title": "claude", "pid": 4242, "cwd": "/work"}, {"id": "old", "exited": true}]`), nil
	}
	return nil, nil
}

func (d *fakeDaemon) last() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls[len(d.calls)-1]
}

func TestDaemonHostCommands(t *testing.T) {
	d := &fakeDaemon{}
	var host Host = &DaemonHost{Binary: "mtm __mtmd", Server: "work", Exec: d.exec}
	h := host.(*DaemonHost)
	if _, isEnv := host.(EnvOpener); !isEnv {
		t.Fatal("DaemonHost cannot open with an environment")
	}

	if err := h.OpenWithEnv("w1", "claude --resume", "/work", 80, 24, map[string]string{"MTM_TASK_ID": "t1", "MTM_PANE_ID": "w1"}); err != nil {
		t.Fatal(err)
	}
	want := "mtm __mtmd --server work new-pane --id w1 --cols 80 --rows 24 --cwd /work --env MTM_PANE_ID=w1 --env MTM_TASK_ID=t1 -- claude --resume"
	if d.last() != want {
		t.Fatalf("open ran\n%s\nwant\n%s", d.last(), want)
	}
	f, ok := h.Snapshot("w1")
	if !ok || f.ID != "w1" || f.PID != 4242 || f.Cols != 80 || f.Rows != 24 || f.Cursor.X != 2 || !slices.Equal(f.Plain, []string{"$"}) || f.Exited {
		t.Fatalf("snapshot = %+v (%v)", f, ok)
	}
	if !strings.HasSuffix(d.last(), "capture-pane --id w1 --json") {
		t.Fatalf("snapshot ran %s", d.last())
	}
	// The revision moves only when what the daemon shows changed.
	if again, _ := h.Snapshot("w1"); again.Revision != f.Revision {
		t.Fatalf("an unchanged screen moved the revision: %d -> %d", f.Revision, again.Revision)
	}
	if err := h.Write("w1", []byte("ls\r\x1b[A")); err != nil || !strings.HasSuffix(d.last(), "send-keys --id w1 --hex 6c730d1b5b41") {
		t.Fatalf("write ran %s (%v)", d.last(), err)
	}
	d.mu.Lock()
	d.screen["w1"] = "$ ls\nREADME.md   \n$ \n"
	d.mu.Unlock()
	moved, _ := h.Snapshot("w1")
	if moved.Revision <= f.Revision || !slices.Equal(moved.Plain, []string{"$ ls", "README.md", "$"}) || moved.Cols != 80 {
		t.Fatalf("plain capture = %+v", moved)
	}
	if err := h.Resize("w1", 120, 40); err != nil || !strings.HasSuffix(d.last(), "resize-pane --id w1 --cols 120 --rows 40") {
		t.Fatalf("resize ran %s (%v)", d.last(), err)
	}
	panes, err := h.ListRemotePanes()
	if err != nil || len(panes) != 2 || panes[0].ID != "w1" || panes[0].Cwd != "/work" || !panes[1].Exited {
		t.Fatalf("panes = %+v, %v", panes, err)
	}
	if err := h.Close("w1"); err != nil || !strings.HasSuffix(d.last(), "kill-pane --id w1") {
		t.Fatalf("close ran %s (%v)", d.last(), err)
	}
	if _, ok := h.Snapshot("w1"); ok {
		t.Fatal("a closed pane still has a screen")
	}
	if err := h.Open("", "", "", 80, 24); err == nil {
		t.Fatal("a pane with no id was opened")
	}

	// No server name: the daemon's default. An empty binary is mtmd.
	bare := &DaemonHost{Exec: d.exec}
	if err := bare.Open("w2", "", "", 80, 24); err != nil || d.last() != "mtmd new-pane --id w2 --cols 80 --rows 24" {
		t.Fatalf("bare open ran %s (%v)", d.last(), err)
	}
	bare.CloseAll()
	if _, ok := bare.Snapshot("w2"); !ok {
		t.Fatal("CloseAll ended a daemon pane: it should outlive the app")
	}
	d.fail = "send-keys"
	if err := bare.Write("w2", []byte("x")); err == nil || !strings.Contains(err.Error(), "daemon is gone") {
		t.Fatalf("a failed command: %v", err)
	}
}

func TestAutoHost(t *testing.T) {
	d := &fakeDaemon{}
	found := func(names ...string) func(string) (string, error) {
		return func(name string) (string, error) {
			if slices.Contains(names, name) {
				return "/usr/local/bin/" + name, nil
			}
			return "", errors.New("not found")
		}
	}
	noSocket := func(string) bool { return false }
	if _, local := autoHost(false, found("mtmd"), "", noSocket, d.exec).(*LocalHost); !local {
		t.Fatal("not preferring the daemon did not give a local host")
	}
	if _, local := autoHost(true, found(), "", noSocket, d.exec).(*LocalHost); !local {
		t.Fatal("no daemon binary did not fall back to a local host")
	}
	h, isDaemon := autoHost(true, found("tuios"), "", noSocket, d.exec).(*DaemonHost)
	if !isDaemon || h.Binary != "tuios" {
		t.Fatalf("a reachable daemon was not picked: %+v", h)
	}
	d.fail = "list-panes"
	if _, local := autoHost(true, found("mtmd"), "", noSocket, d.exec).(*LocalHost); !local {
		t.Fatal("a daemon that does not answer did not fall back to a local host")
	}
	if h, isDaemon := autoHost(true, found("mtmd"), "/run/mtmd.sock", func(p string) bool { return p == "/run/mtmd.sock" }, d.exec).(*DaemonHost); !isDaemon || h.Binary != "mtmd" {
		t.Fatal("an existing socket was not taken as a reachable daemon")
	}
}
