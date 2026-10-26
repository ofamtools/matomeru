package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/history"
)

// tickingClock replaces the journal's clock with one that advances a
// millisecond per read. The journal orders entries by millisecond, then by
// the turn's random id, so hooks that run inside one real millisecond (a
// Stop and the next prompt) would sort in a random order and merge turns.
func tickingClock(t *testing.T) {
	t.Helper()
	real := history.Now
	var ms atomic.Int64
	ms.Store(real())
	history.Now = func() int64 { return ms.Add(1) }
	t.Cleanup(func() { history.Now = real })
}

// paneEnv is the environment of a hook running in the claude pane.
func paneEnv(a *App) {
	env := map[string]string{"TUIOS_SESSION": "matomeru", "TUIOS_PANE_ID": "w1"}
	a.getenv = func(k string) string { return env[k] }
}

// The OMP extension's calls: a start with --json, its completion on stdin.
func TestCLICaptureTurn(t *testing.T) {
	app, _ := cliApp(t)
	mustCLI(t, app, "history", "capture-turn", "--json", `{"id":"t1","session":"matomeru","window":"w1","harness":"omp","conversation_id":"c1","prompt":"lift the store\nand test it","started_at":1790000000000}`)
	done := `{"id":"t1","session":"matomeru","window":"w1","harness":"omp","conversation_id":"c1","prompt":"lift the store\nand test it","started_at":1790000000000,"completed_at":1790000060000,"response":"Lifted.\nTests pass."}`
	if out, errOut, err := cli(app, done, "history", "capture-turn", "--stdin"); err != nil || !strings.Contains(out, `"id"`) {
		t.Fatalf("completion: %v %s %s", err, out, errOut)
	}
	if _, _, err := cli(app, "", "history", "capture-turn", "--json", `{"id":"t2","session":"s"}`); err == nil {
		t.Fatal("a turn without a window was accepted")
	}
	turns, _ := app.hist.Turns("w1")
	if len(turns) != 1 || turns[0].Prompt != "lift the store\nand test it" || turns[0].Response() != "Lifted.\nTests pass." || !turns[0].Done() {
		t.Fatalf("turns %+v", turns)
	}
}

// Claude Code's hooks: UserPromptSubmit starts the turn with the full
// prompt, Stop completes it with the full answer; a hook outside a TUIOS
// pane does nothing.
func TestCLICaptureHook(t *testing.T) {
	tickingClock(t)
	app, _ := cliApp(t)
	hook := func(payload string) string {
		t.Helper()
		out, errOut, err := cli(app, payload, "history", "capture-hook", "claude-code")
		if err != nil {
			t.Fatalf("hook %s: %v %s", payload, err, errOut)
		}
		return out
	}
	if out := hook(`{"hook_event_name":"UserPromptSubmit","session_id":"c1","prompt":"hello"}`); !strings.Contains(out, "not in a TUIOS pane") {
		t.Fatalf("outside a pane: %s", out)
	}
	paneEnv(app)
	hook(`{"hook_event_name":"UserPromptSubmit","session_id":"c1","prompt":"explain the store\nin detail"}`)
	hook(`{"hook_event_name":"Stop","session_id":"c1","last_assistant_message":"It keeps rows.\nLine two.\nLine three."}`)
	if out := hook(`{"hook_event_name":"Stop","session_id":"c1","last_assistant_message":"again"}`); !strings.Contains(out, "no open turn") {
		t.Fatalf("a second Stop: %s", out)
	}
	// Without last_assistant_message the answer is read from the transcript.
	path := filepath.Join(t.TempDir(), "session.jsonl")
	lines := []any{
		map[string]any{"type": "user", "message": map[string]any{"content": "old prompt"}},
		map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "old answer"}}}},
		map[string]any{"type": "user", "message": map[string]any{"content": "second prompt"}},
		map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "Reading."}, map[string]any{"type": "tool_use", "name": "Read"}}}},
		map[string]any{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "content": "file"}}}},
		map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": "Here it is."}}}},
	}
	var b strings.Builder
	for _, l := range lines {
		data, _ := json.Marshal(l)
		b.Write(append(data, '\n'))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	hook(`{"hook_event_name":"UserPromptSubmit","session_id":"c1","prompt":"second prompt","transcript_path":"` + path + `"}`)
	hook(`{"hook_event_name":"Stop","session_id":"c1"}`)
	turns, _ := app.hist.Turns("w1")
	if len(turns) != 2 {
		t.Fatalf("turns %+v", turns)
	}
	if turns[0].Prompt != "explain the store\nin detail" || turns[0].Response() != "It keeps rows.\nLine two.\nLine three." || turns[0].Harness != "claude-code" {
		t.Fatalf("first %+v", turns[0])
	}
	if got := turns[1].Response(); got != "Reading.\n\nHere it is." {
		t.Fatalf("from the transcript: %q", got)
	}
}
