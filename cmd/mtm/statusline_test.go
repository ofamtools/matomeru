package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/daemon"
)

// statuslineDaemon answers list-sessions, list-windows and list-agents the
// way tuios does: one session, an agent window whose metadata the status
// line feed wrote (model, context, cost) plus a key no feed writes, and a
// shell window.
func statuslineDaemon(t *testing.T) string {
	t.Helper()
	// Short: unix socket paths are capped near 104 bytes.
	dir, err := os.MkdirTemp("/tmp", "mtms")
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
		"list-sessions": map[string]any{"sessions": []any{map[string]any{"id": "s1", "name": "work", "dir": "/tmp/w"}}},
		"list-windows": map[string]any{"windows": []any{
			map[string]any{"window_id": "w1", "display_name": "fixer", "agent_state": "working", "cwd": "/tmp/w"},
			map[string]any{"window_id": "w2", "display_name": "sh", "cwd": "/tmp/w"},
		}},
		"list-agents": map[string]any{"agents": []any{
			map[string]any{"window_id": "w1", "harness_id": "claude", "state": "working",
				"meta": map[string]any{"model": "Opus 4.7", "context": "42%", "cost": "$1.20", "effort": "max"}},
			map[string]any{"window_id": "w2", "state": "none", "meta": map[string]any{}},
		}},
	}
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
						ID   string `json:"id"`
						Verb string `json:"verb"`
					}
					if json.Unmarshal(line, &req) != nil {
						return
					}
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
	return sock
}

// The status line shows what the daemon reports and nothing it does not:
// list-agents' metadata gives the model, context and cost; a metadata key
// no feed writes (effort) is not shown; effort and the permission mode come
// only from the flags mtm launched the agent with.
func TestStatusLineShowsOnlyWhatTheDaemonReports(t *testing.T) {
	panes, err := tuios{daemon.New(statuslineDaemon(t))}.ListPanes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(panes) != 2 || panes[0].Meta["context"] != "42%" || panes[0].Meta["cost"] != "$1.20" {
		t.Fatalf("panes %+v", panes)
	}
	agent, shell := panes[0], panes[1]
	if got := strings.Join(agent.statusFields(), " · "); got != "Opus 4.7 · ctx 42% · $1.20" {
		t.Fatalf("status fields %q", got)
	}
	if got := agent.usageCell(); got != "42% $1.20" {
		t.Fatalf("usage cell %q", got)
	}
	if got := shell.usageCell(); got != "" {
		t.Fatalf("shell usage cell %q", got)
	}
	agent.LaunchFlags = []string{"--effort", "high", "--dangerously-skip-permissions"}
	if got := strings.Join(agent.statusFields(), " · "); got != "Opus 4.7 · effort high · perms bypassPermissions · ctx 42% · $1.20" {
		t.Fatalf("with recipe %q", got)
	}
	agent.Meta = nil
	if got := agent.usageCell(); got != "" {
		t.Fatalf("nothing reported, usage cell %q", got)
	}
	if got := agent.statusFields(); len(got) != 2 {
		t.Fatalf("nothing reported, fields %q", got)
	}
}

// The detail header carries the full status line and the tree row its
// compact form, both from the daemon's metadata.
func TestStatusLineInTheHeaderAndTheTreeRow(t *testing.T) {
	app, f := panesApp(t)
	f.panes[0].Harness, f.panes[0].AgentState = "claude", "working"
	f.panes[0].Meta = map[string]string{"model": "opus", "context": "42%", "cost": "$1.20"}
	reload(t, app)
	row := "pane:matomeru/w1"
	if got := cells(app, row); !strings.HasSuffix(got, "| 42% $1.20") {
		t.Fatalf("tree row cells %q", got)
	}
	fr := frameAt(app, 160)
	for _, want := range []string{"claude · opus · ctx 42% · $1.20", "42% $1.20"} {
		if !strings.Contains(fr, want) {
			t.Fatalf("frame lacks %q:\n%s", want, fr)
		}
	}
	t.Log("\n" + fr)
}
