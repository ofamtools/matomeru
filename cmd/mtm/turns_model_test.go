package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/turns"
)

// Synthetic Claude Code log records, in the shapes internal/turns reads;
// no real session's content.

func logRecord(t *testing.T, fields map[string]any) string {
	t.Helper()
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func logPrompt(t *testing.T, uuid, ts, text string) string {
	return logRecord(t, map[string]any{"type": "user", "uuid": uuid, "timestamp": ts, "promptSource": "typed", "message": map[string]any{"role": "user", "content": text}})
}

func logAssistant(t *testing.T, uuid, ts, stop string, blocks ...map[string]any) string {
	return logRecord(t, map[string]any{"type": "assistant", "uuid": uuid, "timestamp": ts, "message": map[string]any{"role": "assistant", "content": blocks, "stop_reason": stop}})
}

func logSteer(t *testing.T, uuid, ts, text string) string {
	return logRecord(t, map[string]any{"type": "attachment", "uuid": uuid, "timestamp": ts, "attachment": map[string]any{"type": "queued_command", "prompt": text}})
}

func logResult(t *testing.T, uuid, ts, id, text string) string {
	return logRecord(t, map[string]any{"type": "user", "uuid": uuid, "timestamp": ts, "message": map[string]any{"role": "user", "content": []map[string]any{
		{"type": "tool_result", "tool_use_id": id, "content": text}}}})
}

func logDone(t *testing.T, uuid, ts string) string {
	return logRecord(t, map[string]any{"type": "system", "subtype": "turn_duration", "uuid": uuid, "timestamp": ts, "durationMs": 4000})
}

// claudeTurnsApp is the panes app with w1 a Claude Code pane in session
// s1 whose log (one finished turn with thinking, a tool call and a steer)
// is under the app's Claude projects directory, on the Turns tab.
func claudeTurnsApp(t *testing.T) (*App, *fakePanes, string) {
	t.Helper()
	app, f := panesApp(t)
	f.panes[0].Harness, f.panes[0].AgentState, f.panes[0].AgentSession, f.panes[0].Cwd = "claude-code", "idle", "s1", "/w/proj"
	dir := filepath.Join(app.claudeProjects, turns.ClaudeProjectDir("/w/proj"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "s1.jsonl")
	writeLog(t, log,
		logPrompt(t, "u1", "2026-10-27T10:00:00Z", "fix the flaky test"),
		logAssistant(t, "a1", "2026-10-27T10:00:01Z", "tool_use", map[string]any{"type": "thinking", "thinking": "the test races on the clock"}),
		logAssistant(t, "a2", "2026-10-27T10:00:02Z", "tool_use", map[string]any{"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{"command": "go test -race ./..."}}),
		logSteer(t, "q1", "2026-10-27T10:00:03Z", "only the cmd/mtm package"),
		logResult(t, "r1", "2026-10-27T10:00:04Z", "t1", "ok cmd/mtm"),
		logAssistant(t, "a3", "2026-10-27T10:00:05Z", "end_turn", map[string]any{"type": "text", "text": "Fixed: the test now **fakes** the clock."}),
		logDone(t, "d1", "2026-10-27T10:00:06Z"),
	)
	// These turns tests are about the turns and the send; the task picker
	// a send opens (prompt.after-submit) has its own (pane_task_test.go).
	if err := app.r.Settings().Set(settingAfterSubmit, afterSubmitNone); err != nil {
		t.Fatal(err)
	}
	reload(t, app)
	settle(app, key(t, app, "[")) // Screen wraps back to Turns
	if app.pv.tab != tabTurns {
		t.Fatalf("tab %d", app.pv.tab)
	}
	return app, f, log
}

func writeLog(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "")); err != nil {
		t.Fatal(err)
	}
}

// useTurnStyle sets turns.style (cards or tree) as the settings verb does.
func useTurnStyle(t *testing.T, a *App, style string) {
	t.Helper()
	if _, err := a.r.Execute(ochazuke.Request{Noun: "settings", Verb: "set", Args: ochazuke.Arguments{"name": settingTurnsStyle, "value": style}}); err != nil {
		t.Fatal(err)
	}
}

// turnRows are the Turns tree's rows' labels.
func turnRows(a *App) []string {
	var out []string
	for _, it := range a.pv.turns.List().Items() {
		out = append(out, it.Label)
	}
	return out
}

func TestTurnsIsTheDefaultTab(t *testing.T) {
	t.Parallel()
	app, err := New(Options{Data: seeded(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if app.pv.tab != tabTurns {
		t.Fatalf("the detail opens on %s", tabNames[app.pv.tab])
	}
}

// A Claude pane's turns come from its session log: closed, labelled by the
// prompt; opened, the prompt, the steer folded in under it, the thinking
// and the tool call folded (the settings' defaults) and the answer.
func TestClaudeTurnsFromTheSessionLog(t *testing.T) {
	t.Parallel()
	app, _, _ := claudeTurnsApp(t)
	useTurnStyle(t, app, turnsStyleTree)
	rows := turnRows(app)
	if len(rows) != 1 || !strings.Contains(rows[0], "fix the flaky test · 1 steer · 1 tool call · 4.0s") || strings.Contains(rows[0], activeTurnMarker) {
		t.Fatalf("rows %q", rows)
	}
	if !app.pv.turns.Collapsed("turn:u1") {
		t.Fatal("a turn starts closed")
	}
	press(t, app, "}")
	settle(app, key(t, app, "l"))
	got := strings.Join(turnRows(app), "\n")
	for _, want := range []string{"prompt:", "fix the flaky test", "steer · ", "only the cmd/mtm package", "▸ thinking · 1 line", "▸ tool Bash: go test -race ./...", "answer:", "Fixed: the test now fakes the clock."} {
		if !strings.Contains(got, want) {
			t.Fatalf("opened turn lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "the test races on the clock") || strings.Contains(got, "input:") {
		t.Fatalf("folded steps show their lines:\n%s", got)
	}
	if strings.Index(got, "only the cmd/mtm package") > strings.Index(got, "answer:") {
		t.Fatalf("the steer is not under the prompt:\n%s", got)
	}
	// The settings: thinking hidden, tool calls open.
	if _, err := app.r.Execute(ochazuke.Request{Noun: "settings", Verb: "set", Args: ochazuke.Arguments{"name": settingTurnsThinking, "value": "false"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.r.Execute(ochazuke.Request{Noun: "settings", Verb: "set", Args: ochazuke.Arguments{"name": settingTurnsToolsOpen, "value": "true"}}); err != nil {
		t.Fatal(err)
	}
	got = strings.Join(turnRows(app), "\n")
	if strings.Contains(got, "thinking") || !strings.Contains(got, "▾ tool Bash") || !strings.Contains(got, "input:") || !strings.Contains(got, "ok cmd/mtm") {
		t.Fatalf("after the settings:\n%s", got)
	}
	if _, err := app.r.Execute(ochazuke.Request{Noun: "settings", Verb: "set", Args: ochazuke.Arguments{"name": settingTurnsTools, "value": "false"}}); err != nil {
		t.Fatal(err)
	}
	if got = strings.Join(turnRows(app), "\n"); strings.Contains(got, "tool Bash") {
		t.Fatalf("tools hidden:\n%s", got)
	}
}

// A prompt the agent is working on is a new turn, closed like the rest,
// its row marked; the older rows are muted and the running one bold.
func TestTheRunningTurnIsClosedAndBrighter(t *testing.T) {
	t.Parallel()
	app, f, log := claudeTurnsApp(t)
	useTurnStyle(t, app, turnsStyleTree)
	writeLog(t, log,
		logPrompt(t, "u2", "2026-10-27T10:01:00Z", "now the docs"),
		logAssistant(t, "a4", "2026-10-27T10:01:01Z", "tool_use", map[string]any{"type": "text", "text": "Reading."}),
	)
	f.panes[0].AgentState = "working"
	reload(t, app)
	rows := turnRows(app)
	if len(rows) != 2 || !strings.Contains(rows[1], activeTurnMarker+" now the docs · working") || strings.Contains(rows[0], activeTurnMarker) {
		t.Fatalf("rows %q", rows)
	}
	for _, id := range []string{"turn:u1", "turn:u2"} {
		if !app.pv.turns.Collapsed(id) {
			t.Fatalf("%s is open", id)
		}
	}
	app.m.Appearance.Theme = "mono"
	styles := ochazuke.NewStyles(*app.m.Appearance)
	old, active := rows[0], rows[1] // the tree draws its marker in the label
	got := app.styleTurnRows(old + "\n" + active)
	if want := styles.Muted.Render(old) + "\n" + styles.Title.Bold(true).Render(active); got != want {
		t.Fatalf("styled\n%q\nwant\n%q", got, want)
	}
	// The frame draws the marker, so mono shows the running turn too.
	if fr := frame(app); !strings.Contains(fr, activeTurnMarker+" now the docs") {
		t.Fatalf("frame:\n%s", fr)
	}
}

// /clear starts a new session: the pane's turns chain both, a separator
// row above the new session's first turn.
func TestClaudeSessionsChainAcrossClear(t *testing.T) {
	t.Parallel()
	app, f, _ := claudeTurnsApp(t)
	useTurnStyle(t, app, turnsStyleTree)
	elsewhere := filepath.Join(app.claudeProjects, "-somewhere-else")
	os.MkdirAll(elsewhere, 0o755)
	writeLog(t, filepath.Join(elsewhere, "s2.jsonl"), logPrompt(t, "v1", "2026-10-27T11:00:00Z", "fresh start"))
	f.panes[0].AgentSession = "s2"
	reload(t, app)
	rows := turnRows(app)
	if len(rows) != 3 || !strings.Contains(rows[1], "── new session") || !strings.Contains(rows[2], "fresh start") {
		t.Fatalf("rows %q", rows)
	}
	if rec, _ := app.work.PaneSessions("w1"); strings.Join(rec.Sessions, ",") != "s1,s2" {
		t.Fatalf("recorded sessions %v", rec.Sessions)
	}
}

// A Claude pane whose log is not found keeps the journal's turns.
func TestAClaudePaneWithNoLogFallsBackToTheJournal(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	capturedTurns(t, path, "first answer", "second answer")
	app := openWith(t, Options{Data: path})
	f := app.panesd.(*fakePanes)
	f.panes[0].Harness, f.panes[0].AgentState, f.panes[0].AgentSession = "claude-code", "idle", "missing"
	useTurnStyle(t, app, turnsStyleTree)
	reload(t, app)
	settle(app, key(t, app, "["))
	if rows := strings.Join(turnRows(app), "\n"); !strings.Contains(rows, "prompt 1 · 1 answer") {
		t.Fatalf("rows:\n%s", rows)
	}
}

// fakeTurnPanes is the fake daemon with shell command history and a
// buffer per window (rows are lines: nothing wraps).
type fakeTurnPanes struct {
	*fakePanes
	mu       sync.Mutex
	commands map[string][]daemon.CommandEntry
	buffers  map[string][]string
	captures int
	pagers   []string
}

func withTurnPanes(a *App) *fakeTurnPanes {
	f := &fakeTurnPanes{fakePanes: a.panesd.(*fakePanes), commands: map[string][]daemon.CommandEntry{}, buffers: map[string][]string{}}
	a.panesd = f
	return f
}

func (f *fakeTurnPanes) Commands(_ context.Context, p pane) ([]daemon.CommandEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.commands[p.ID], nil
}

func (f *fakeTurnPanes) CaptureRows(_ context.Context, p pane, start, end int, _ bool) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.captures++
	all := f.buffers[p.ID]
	if start > len(all) {
		return nil, nil
	}
	return append([]string(nil), all[start-1:min(end, len(all))]...), nil
}

func (f *fakeTurnPanes) OpenPager(_ context.Context, p pane, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pagers = append(f.pagers, p.ID+" "+path)
	return nil
}

// shellTurnsApp is the panes app with w2 a marked shell that ran three
// commands, one of them long gone from the history, on the Turns tab.
func shellTurnsApp(t *testing.T, big int) (*App, *fakeTurnPanes) {
	t.Helper()
	app, _ := panesApp(t)
	f := withTurnPanes(app)
	f.panes[1].MarksCommands, f.panes[1].CommandSeq, f.panes[1].AtPrompt = true, 3, true
	zero, one := 0, 1
	buf := []string{"$ ls", "a.txt", "b.txt", "$ cat big.log"}
	for i := 1; i <= big; i++ {
		buf = append(buf, fmt.Sprintf("log line %d %s", i, strings.Repeat("x", 40)))
	}
	buf = append(buf, "$ ")
	f.buffers["w2"] = buf
	f.commands["w2"] = []daemon.CommandEntry{
		{Seq: 3, Cmdline: "cat big.log", Exit: &zero, Row: 3, DurationMS: 120, At: 3000},
		{Seq: 2, Cmdline: "ls", Exit: &zero, Row: 0, DurationMS: 5, At: 2000},
		{Seq: 1, Cmdline: "make old", Exit: &one, Row: -1, At: 1000},
	}
	app.cx.sys = &fakeSystem{}
	reload(t, app)
	for i := 0; i < 4 && app.r.Focus != "pane:ochazuke/w2"; i++ {
		press(t, app, "j") // down to the shell's row
	}
	if app.r.Focus != "pane:ochazuke/w2" {
		t.Fatalf("focus %s", app.r.Focus)
	}
	settle(app, key(t, app, "["))
	return app, f
}

// A marked shell's turns are its commands; opening one reads its output
// then, and a command whose prompt left the history says so.
func TestShellTurnsFromCommandHistory(t *testing.T) {
	t.Parallel()
	app, f := shellTurnsApp(t, 3)
	useTurnStyle(t, app, turnsStyleTree)
	rows := turnRows(app)
	if len(rows) != 3 || !strings.Contains(rows[0], "make old · exit 1") || !strings.Contains(rows[1], "ls · exit 0 · 0.0s") {
		t.Fatalf("rows %q", rows)
	}
	if f.captures != 0 {
		t.Fatalf("output read before a turn opened: %d captures", f.captures)
	}
	press(t, app, "}", "k") // } lands on the newest, cat big.log (F9)
	settle(app, key(t, app, "l"))
	got := strings.Join(turnRows(app), "\n")
	start, end := strings.Index(got, "output:"), strings.Index(got, "cat big.log")
	if start < 0 || end < start || !strings.Contains(got, "command:") {
		t.Fatalf("opened ls:\n%s", got)
	}
	if out := strings.Fields(got[start+len("output:") : end]); strings.Join(out, " ") != "a.txt b.txt ▸" {
		t.Fatalf("ls output %q:\n%s", out, got)
	}
	press(t, app, "esc", "}", "k")
	settle(app, key(t, app, "l"))
	if got := strings.Join(turnRows(app), "\n"); !strings.Contains(got, "output left the history") {
		t.Fatalf("make old:\n%s", got)
	}
	// A new command (the window's command_seq) reads the history again.
	f.commands["w2"] = append([]daemon.CommandEntry{{Seq: 4, Cmdline: "git status", Row: 9, At: 4000}}, f.commands["w2"]...)
	f.panes[1].CommandSeq = 4
	reload(t, app)
	if rows := turnRows(app); !strings.Contains(strings.Join(rows, "\n"), "git status") {
		t.Fatalf("after a new command %q", rows)
	}
}

// y copies a turn's response: a Claude turn's answer onto the clipboard; a
// shell output over 100 KB is offered as a file or a pager instead.
func TestCopyATurn(t *testing.T) {
	t.Parallel()
	app, _, _ := claudeTurnsApp(t)
	useTurnStyle(t, app, turnsStyleTree)
	sys := &fakeSystem{}
	app.cx.sys = sys
	press(t, app, "}")
	settle(app, key(t, app, "y"))
	settle(app, key(t, app, "Y"))
	if len(sys.copied) != 2 || sys.copied[0] != "Fixed: the test now **fakes** the clock." ||
		!strings.HasPrefix(sys.copied[1], "fix the flaky test\n\nonly the cmd/mtm package\n\nFixed:") {
		t.Fatalf("copied %q", sys.copied)
	}
}

func TestCopyingAHugeShellOutputOffersAFile(t *testing.T) {
	t.Parallel()
	app, f := shellTurnsApp(t, 3000) // about 160 KB
	useTurnStyle(t, app, turnsStyleTree)
	sys := app.cx.sys.(*fakeSystem)
	press(t, app, "}") // } lands on the newest, cat big.log (F9)
	settle(app, key(t, app, "y"))
	if len(sys.copied) != 0 {
		t.Fatalf("a huge output reached the clipboard: %d bytes", len(sys.copied[0]))
	}
	ch := app.r.Chooser(turnSaveChooser)
	if ch == nil {
		t.Fatalf("no save chooser; focus %s status %q", app.r.Focus, app.m.Status)
	}
	if f.captures < 2 {
		t.Fatalf("captures %d", f.captures)
	}
	settle(app, key(t, app, "enter")) // Save to …
	matches, _ := filepath.Glob(filepath.Join(app.r.Config().StateDirectory(), "turns", "*.txt"))
	if len(matches) != 1 {
		t.Fatalf("saved %v", matches)
	}
	data, _ := os.ReadFile(matches[0])
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 3000 || !strings.HasPrefix(lines[0], "log line 1 ") || !strings.HasPrefix(lines[2999], "log line 3000 ") {
		t.Fatalf("saved %d lines: %q … %q", len(lines), lines[0], lines[len(lines)-1])
	}
	// The pager row writes the file and opens it in a new pane.
	settle(app, key(t, app, "y"))
	settle(app, key(t, app, "ctrl+n"))
	settle(app, key(t, app, "enter"))
	if len(f.pagers) != 1 || !strings.HasPrefix(f.pagers[0], "w2 ") {
		t.Fatalf("pagers %q", f.pagers)
	}
}

// ompRecord is one synthetic omp session entry (internal/turns/omp.go's
// shapes); no real session's content.
func ompRecord(t *testing.T, typ, id, parent, ts string, message map[string]any) string {
	t.Helper()
	r := map[string]any{"type": typ, "id": id, "parentId": parent, "timestamp": ts}
	if parent == "" {
		r["parentId"] = nil
	}
	if message != nil {
		r["message"] = message
	}
	return logRecord(t, r)
}

// An omp pane's turns come from its session file, found by the session id
// tuios reports, following the file's current branch; a steer folds in.
func TestOmpTurnsFromTheSessionFile(t *testing.T) {
	t.Parallel()
	app, f := panesApp(t)
	f.panes[0].Harness, f.panes[0].AgentState, f.panes[0].AgentSession, f.panes[0].Cwd = "omp", "idle", "01a1-s1", "/w/proj"
	dir := filepath.Join(app.ompSessions, "-w-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	user := func(text string, steer bool) map[string]any {
		m := map[string]any{"role": "user", "content": []map[string]any{{"type": "text", "text": text}}}
		if steer {
			m["steering"] = true
		}
		return m
	}
	answer := func(text, stop string) map[string]any {
		return map[string]any{"role": "assistant", "content": []map[string]any{{"type": "text", "text": text}}, "stopReason": stop}
	}
	writeLog(t, filepath.Join(dir, "2026-10-27T10-00-00-000Z_01a1-s1.jsonl"),
		logRecord(t, map[string]any{"type": "session", "version": 3, "id": "01a1-s1", "cwd": "/w/proj"}),
		ompRecord(t, "message", "u1", "", "2026-10-27T10:00:01Z", user("plan the release", false)),
		ompRecord(t, "message", "s1", "u1", "2026-10-27T10:00:02Z", user("skip the changelog", true)),
		ompRecord(t, "message", "a1", "s1", "2026-10-27T10:00:03Z", answer("Planned.", "stop")),
		ompRecord(t, "message", "u2", "a1", "2026-10-27T10:00:04Z", user("abandoned", false)),
		ompRecord(t, "message", "u3", "a1", "2026-10-27T10:00:05Z", user("ship it", false)),
	)
	useTurnStyle(t, app, turnsStyleTree)
	reload(t, app)
	settle(app, key(t, app, "["))
	if app.turnKind(app.panes[0]) != turnsOmp {
		t.Fatalf("kind %d", app.turnKind(app.panes[0]))
	}
	rows := turnRows(app)
	if len(rows) != 2 || !strings.Contains(rows[0], "plan the release · 1 steer") || !strings.Contains(rows[1], "ship it") {
		t.Fatalf("rows %q", rows)
	}
	if rec, _ := app.work.PaneSessions("w1"); len(rec.Sessions) != 1 || rec.Sessions[0] != "01a1-s1" {
		t.Fatalf("recorded %+v", rec)
	}
}

// onTurn reports whether the focus is a turn of the Turns tab, a card or a
// tree row.
func onTurn(a *App) bool {
	inst := a.r.Instances[a.r.Focus]
	return inst != nil && strings.HasPrefix(inst.Noun, "turn") && inst.Noun != promptNoun
}
