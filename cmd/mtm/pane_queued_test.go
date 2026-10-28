package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/daemon"
)

// daemonListed lists panes from a (fake) tuios daemon through mtm's own
// client, list-agents mapping included; the rest is fakePanes.
type daemonListed struct {
	*fakePanes
	d tuios
}

func (l daemonListed) ListPanes(ctx context.Context) ([]pane, error) { return l.d.ListPanes(ctx) }

// queuedDaemon is one session with an agent window and a shell window;
// list-agents reports the agent's queued count as it is when asked.
func queuedDaemon(t *testing.T, queued *atomic.Int64) string {
	t.Helper()
	return serveDaemon(t, func(verb string) (any, bool) {
		switch verb {
		case "list-sessions":
			return map[string]any{"sessions": []any{map[string]any{"id": "s1", "name": "work", "dir": "/tmp/w"}}}, true
		case "list-windows":
			return map[string]any{"windows": []any{
				map[string]any{"window_id": "w1", "display_name": "fixer", "agent_state": "working", "cwd": "/tmp/w"},
				map[string]any{"window_id": "w2", "display_name": "sh", "cwd": "/tmp/w"},
			}}, true
		case "list-agents":
			return map[string]any{"agents": []any{
				map[string]any{"window_id": "w1", "harness_id": "claude", "state": "working", "queued": queued.Load(), "meta": map[string]any{}},
				map[string]any{"window_id": "w2", "state": "none", "queued": 0, "meta": map[string]any{}},
			}}, true
		}
		return nil, false
	})
}

// A pane with prompts waiting in its delivery queue (list-agents' queued)
// reads W after its name, with no count; the refresh that reads the state
// reads it, and it is gone once the queue empties. A pane with none never
// shows it.
func TestQueuedPromptsMarkThePaneRow(t *testing.T) {
	var queued atomic.Int64
	queued.Store(2)
	app, f := panesApp(t)
	app.panesd = daemonListed{f, tuios{daemon.New(queuedDaemon(t, &queued))}}
	reload(t, app)
	agent, shell := "pane:work/w1", "pane:work/w2"
	if got := cells(app, agent); !strings.Contains(got, "● fixer W |") || strings.Contains(got, "2") {
		t.Fatalf("queued 2: agent row %q", got)
	}
	if got := cells(app, shell); strings.Contains(got, " W") {
		t.Fatalf("queued 0: shell row %q", got)
	}
	app.m.Width, app.m.Height = 100, 24
	got := plainView(app)
	file := filepath.Join("testdata", "pane-queued.golden")
	if *updateGolden {
		if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	} else if want, err := os.ReadFile(file); err != nil {
		t.Fatal(err)
	} else if got != string(want) {
		t.Fatalf("frame differs from %s:\n%s", file, got)
	}
	queued.Store(0)
	reload(t, app)
	if got := cells(app, agent); strings.Contains(got, " W") || !strings.Contains(got, "● fixer |") {
		t.Fatalf("queued 0: agent row %q", got)
	}
}
