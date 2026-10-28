package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/turns"
)

// Fixture session directories in the shapes Claude Code writes
// (internal/turns/agent.go): the parent's log, and its subagents/
// directory with metas and the agents' own logs. The notification is a
// real one's shape, its text replaced.

func logAgentCall(t *testing.T, uuid, ts, id, desc string, background bool) string {
	return logAssistant(t, uuid, ts, "tool_use", map[string]any{"type": "tool_use", "id": id, "name": "Agent", "input": map[string]any{
		"description": desc, "subagent_type": "general-purpose", "model": "opus", "run_in_background": background,
		"prompt": "BRIEF for " + desc + "\n\nRead the files, then report.",
	}})
}

func logLaunched(t *testing.T, uuid, ts, id, agent string) string {
	return logRecord(t, map[string]any{"type": "user", "uuid": uuid, "timestamp": ts,
		"toolUseResult": map[string]any{"isAsync": true, "status": "async_launched", "agentId": agent},
		"message": map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": id,
			"content": "Async agent launched successfully.\nagentId: " + agent + " (internal ID)\nThe agent is working in the background."}}}})
}

func logForeground(t *testing.T, uuid, ts, id, agent, answer string) string {
	return logRecord(t, map[string]any{"type": "user", "uuid": uuid, "timestamp": ts,
		"toolUseResult": map[string]any{"status": "completed", "agentId": agent, "totalDurationMs": 65000, "totalTokens": 3400, "totalToolUseCount": 4},
		"message": map[string]any{"role": "user", "content": []map[string]any{{"type": "tool_result", "tool_use_id": id,
			"content": answer + "\nagentId: " + agent + " (use SendMessage to continue)"}}}})
}

// realNotification is a task notification as Claude Code writes it.
func realNotification(task, call, status, summary, result string) string {
	return "<task-notification>\n<task-id>" + task + "</task-id>\n<tool-use-id>" + call + "</tool-use-id>\n" +
		"<output-file>/private/tmp/claude-501/-w-proj/s1/tasks/" + task + ".output</output-file>\n" +
		"<status>" + status + "</status>\n<summary>" + summary + "</summary>\n" +
		"<note>A task-notification fires each time this agent stops with no live background children of its own. The user can send it another message and resume it, so the same task-id may notify more than once.</note>\n" +
		"<result>" + result + "</result>\n" +
		"<usage><subagent_tokens>204501</subagent_tokens><tool_uses>94</tool_uses><duration_ms>1243026</duration_ms></usage>\n</task-notification>"
}

func logNotification(t *testing.T, uuid, ts, body string) string {
	return logRecord(t, map[string]any{"type": "user", "uuid": uuid, "timestamp": ts, "promptSource": "system",
		"origin": map[string]any{"kind": "task-notification"}, "message": map[string]any{"role": "user", "content": body}})
}

func sideLog(t *testing.T, agent, uuid, ts, typ string, m map[string]any) string {
	return logRecord(t, map[string]any{"type": typ, "uuid": uuid, "timestamp": ts, "isSidechain": true, "agentId": agent, "message": m})
}

func writeAgentFiles(t *testing.T, log, id, call, desc string, depth int, parent string, lines ...string) {
	t.Helper()
	dir := turns.SubagentDir(log)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := map[string]any{"agentType": "general-purpose", "description": desc, "toolUseId": call, "spawnDepth": depth, "requestShape": "background", "model": "opus", "finished": true}
	if parent != "" {
		meta["parentAgentId"], meta["agentType"] = parent, "Explore"
	}
	writeLog(t, filepath.Join(dir, "agent-"+id+".meta.json"), logRecord(t, meta))
	if len(lines) > 0 {
		writeLog(t, filepath.Join(dir, "agent-"+id+".jsonl"), lines...)
	}
}

// agentsApp is the panes app on a Claude pane whose session started a
// foreground agent, a background agent that finishes two turns later (and
// that starts a depth-2 agent of its own), one that fails and one whose
// transcript is missing.
func agentsApp(t *testing.T) (*App, string) {
	t.Helper()
	app, f := panesApp(t)
	f.panes[0].Harness, f.panes[0].AgentState, f.panes[0].AgentSession, f.panes[0].Cwd = "claude-code", "idle", "s1", "/w/proj"
	dir := filepath.Join(app.claudeProjects, turns.ClaudeProjectDir("/w/proj"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "s1.jsonl")
	writeLog(t, log,
		logPrompt(t, "u1", "2026-10-28T10:00:00Z", "start the agents"),
		logAgentCall(t, "a1", "2026-10-28T10:00:01Z", "toolu_fg", "Audit the rail", false),
		logForeground(t, "r1", "2026-10-28T10:01:06Z", "toolu_fg", "af1", "The rail is fine."),
		logAgentCall(t, "a2", "2026-10-28T10:01:07Z", "toolu_bg", "One-line bindings export format", true),
		logLaunched(t, "r2", "2026-10-28T10:01:08Z", "toolu_bg", "ab1"),
		logAgentCall(t, "a3", "2026-10-28T10:01:09Z", "toolu_x", "L4 on fake server", true),
		logLaunched(t, "r3", "2026-10-28T10:01:10Z", "toolu_x", "ax1"),
		logAgentCall(t, "a4", "2026-10-28T10:01:11Z", "toolu_gone", "Gone agent", true),
		logLaunched(t, "r4", "2026-10-28T10:01:12Z", "toolu_gone", "ag1"),
		logAssistant(t, "a5", "2026-10-28T10:01:13Z", "end_turn", map[string]any{"type": "text", "text": "Four agents started."}),
		logDone(t, "d1", "2026-10-28T10:01:14Z"),
		logPrompt(t, "u2", "2026-10-28T10:02:00Z", "something else meanwhile"),
		logAssistant(t, "a6", "2026-10-28T10:02:01Z", "end_turn", map[string]any{"type": "text", "text": "Done meanwhile."}),
		logDone(t, "d2", "2026-10-28T10:02:02Z"),
		logNotification(t, "n1", "2026-10-28T10:22:00Z", realNotification("ab1", "toolu_bg", "completed", `Agent "One-line bindings export format" finished`,
			"# Report: bindings export\n\nThe export is one line per binding.\n\n"+strings.Repeat("A long report line.\n", 60))),
		logAssistant(t, "a7", "2026-10-28T10:22:01Z", "end_turn", map[string]any{"type": "text", "text": "The export agent finished; merging next."}),
		logDone(t, "d3", "2026-10-28T10:22:02Z"),
		logNotification(t, "n2", "2026-10-28T10:23:00Z", realNotification("ax1", "toolu_x", "failed", `Agent "L4 on fake server" failed: Agent terminated early due to an API error`, "")),
		logAssistant(t, "a8", "2026-10-28T10:23:01Z", "end_turn", map[string]any{"type": "text", "text": "It hit the limit."}),
		logDone(t, "d4", "2026-10-28T10:23:02Z"),
	)
	writeAgentFiles(t, log, "af1", "toolu_fg", "Audit the rail", 1, "",
		sideLog(t, "af1", "f1", "2026-10-28T10:00:01Z", "user", map[string]any{"role": "user", "content": "BRIEF for Audit the rail"}),
		sideLog(t, "af1", "f2", "2026-10-28T10:01:05Z", "assistant", map[string]any{"role": "assistant", "stop_reason": "end_turn", "content": []any{map[string]any{"type": "text", "text": "The rail is fine."}}}),
	)
	writeAgentFiles(t, log, "ab1", "toolu_bg", "One-line bindings export format", 1, "",
		sideLog(t, "ab1", "b1", "2026-10-28T10:01:08Z", "user", map[string]any{"role": "user", "content": "BRIEF for One-line bindings export format"}),
		sideLog(t, "ab1", "b2", "2026-10-28T10:01:09Z", "assistant", map[string]any{"role": "assistant", "stop_reason": "tool_use", "content": []any{map[string]any{"type": "tool_use", "id": "sb1", "name": "Bash", "input": map[string]any{"command": "mtm bindings export"}}}}),
		sideLog(t, "ab1", "b3", "2026-10-28T10:01:20Z", "user", map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "sb1", "content": "ok"}}}),
		sideLog(t, "ab1", "b4", "2026-10-28T10:01:21Z", "assistant", map[string]any{"role": "assistant", "stop_reason": "tool_use", "content": []any{map[string]any{"type": "tool_use", "id": "toolu_child", "name": "Agent", "input": map[string]any{"description": "Child audit", "subagent_type": "Explore", "prompt": "BRIEF for Child audit"}}}}),
		sideLog(t, "ab1", "b5", "2026-10-28T10:21:59Z", "assistant", map[string]any{"role": "assistant", "stop_reason": "end_turn", "content": []any{map[string]any{"type": "text", "text": "Export done (inner answer)."}}}),
	)
	writeAgentFiles(t, log, "ac1", "toolu_child", "Child audit", 2, "ab1",
		sideLog(t, "ac1", "c1", "2026-10-28T10:01:22Z", "user", map[string]any{"role": "user", "content": "BRIEF for Child audit"}),
	)
	writeAgentFiles(t, log, "ag1", "toolu_gone", "Gone agent", 1, "") // a meta, no transcript
	reload(t, app)
	settle(app, key(t, app, "[")) // Screen wraps back to Turns
	if app.pv.tab != tabTurns {
		t.Fatalf("tab %d", app.pv.tab)
	}
	return app, log
}

// saveFrame keeps a frame for the report when MTM_TEST_FRAMES names a
// directory.
func saveFrame(t *testing.T, name, fr string) {
	if dir := os.Getenv("MTM_TEST_FRAMES"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, name+".txt"), []byte(fr), 0o644)
	}
}

// stepTo walks the open card's steps with j until the current step's
// text starts with prefix.
func stepTo(t *testing.T, a *App, prefix string) {
	t.Helper()
	for i := 0; i < 80; i++ {
		if text, ok := a.pv.cards.StepText(); ok && strings.Contains(text, prefix) {
			return
		}
		settle(a, appKey(t, a, "j"))
		frame(a)
	}
	text, _ := a.pv.cards.StepText()
	t.Fatalf("no step %q (at %q): %s\n%s", prefix, text, where(a), frame(a))
}

// frameWH is the frame width by height cells, unstyled.
func frameWH(a *App, width, height int) string {
	a.m.Width, a.m.Height = width, height
	return ansi.Strip(a.m.View().Content)
}

// stepTitles are the card's steps' title lines, "title · summary".
func stepTitles(a *App, card string) string {
	for _, c := range a.pv.cards.Cards() {
		if c.ID != card {
			continue
		}
		var out []string
		for _, s := range c.Steps {
			out = append(out, joinNonEmpty(s.Title, s.Summary))
		}
		return strings.Join(out, "\n")
	}
	return ""
}

// A background agent's notification is a compact, muted event card,
// closed, titled by the agent's description and how it stands, with the
// usage; never the notification's XML (which used to fill the screen).
func TestNotificationIsACompactEventCard(t *testing.T) {
	for _, width := range []int{80, 120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			app, _ := agentsApp(t)
			fr := frameWH(app, width, 40)
			saveFrame(t, fmt.Sprintf("notification-cards-%d", width), fr)
			for _, want := range []string{"⤷ One-line bindings export format · finished", "20m43s · 205k tokens · 94 tool uses", "⤷ L4 on fake server · failed"} {
				if !strings.Contains(fr, want) {
					t.Fatalf("width %d lacks %q:\n%s", width, want, fr)
				}
			}
			for _, raw := range []string{"task-notification", "task-id", "output-file", "<result>", "A long report line"} {
				if strings.Contains(fr, raw) {
					t.Fatalf("width %d shows %q:\n%s", width, raw, fr)
				}
			}
			cards := app.pv.cards.Cards()
			var notice ochazuke.Card
			for _, c := range cards {
				if strings.HasPrefix(c.Header, "⤷ One-line") {
					notice = c
				}
			}
			if notice.ID == "" || !notice.Muted || app.pv.cards.IsOpen(notice.ID) || !strings.Contains(notice.Meta, "from “start the agents”") {
				t.Fatalf("notice card %+v open %v", notice, app.pv.cards.IsOpen(notice.ID))
			}
		})
	}
}

// Opened, a notification card shows the agent's report as steps and then
// what the agent did about it.
func TestNotificationCardOpensToTheResult(t *testing.T) {
	app, _ := agentsApp(t)
	app.m.Width, app.m.Height = 120, 60
	frame(app)
	var id string
	for _, c := range app.pv.cards.Cards() {
		if strings.HasPrefix(c.Header, "⤷ One-line") {
			id = c.ID
		}
	}
	press(t, app, "}", "k")
	frame(app)
	if app.r.Focus != id {
		t.Fatalf("focus %s, want %s", where(app), id)
	}
	settle(app, key(t, app, "l"))
	press(t, app, "l")
	frame(app)
	stepTo(t, app, "Report: bindings export")
	press(t, app, "j")
	if got := stepText(t, app); got != "The export is one line per binding." {
		t.Fatalf("the report walks by paragraph: %q", got)
	}
	stepTo(t, app, "The export agent finished")
	fr := frameWH(app, 120, 50)
	saveFrame(t, "notification-open-120", fr)
	if strings.Contains(fr, "task-notification") {
		t.Fatalf("raw XML:\n%s", fr)
	}
}

// The spawning turn's agent steps say each agent's state; nothing reads a
// subagent's log until its step is opened.
func TestAgentStepsShowStateAndReadLazily(t *testing.T) {
	for _, width := range []int{80, 120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			app, _ := agentsApp(t)
			app.m.Width, app.m.Height = width, 60
			frame(app)
			first := app.pv.cards.Cards()[0].ID
			if err := app.pv.cards.Select(first); err != nil {
				t.Fatal(err)
			}
			if err := app.pv.cards.Focus(); err != nil {
				t.Fatal(err)
			}
			settle(app, key(t, app, "l"))
			saveFrame(t, fmt.Sprintf("agent-steps-%d", width), frameWH(app, width, 50))
			fr := stepTitles(app, first)
			for _, want := range []string{
				"agent general-purpose · Audit the rail · done · foreground · 1m05s · 3.4k tokens",
				"agent general-purpose · One-line bindings export format · done · background · 20m43s",
				"agent general-purpose · L4 on fake server · failed · background",
				"agent general-purpose · Gone agent · done · background · transcript missing",
			} {
				if !strings.Contains(fr, want) {
					t.Fatalf("width %d lacks %q:\n%s", width, want, fr)
				}
			}
			if n := app.subs.logs.Reads(); n != 0 {
				t.Fatalf("%d subagent logs read before any step opened", n)
			}
		})
	}
}

// Opening a background agent's step reads its log and nests its turns:
// the brief, its tool calls, its depth-2 agent (whose own turns are past
// the nesting cap) and its answer. o, copy-brief and copy-result act on it.
func TestAgentStepNestsItsTranscript(t *testing.T) {
	app, _ := agentsApp(t)
	app.m.Width, app.m.Height = 120, 80
	frame(app)
	first := app.pv.cards.Cards()[0].ID
	_ = app.pv.cards.Select(first)
	_ = app.pv.cards.Focus()
	settle(app, key(t, app, "l"))
	press(t, app, "l")
	frame(app)
	stepTo(t, app, "agent general-purpose · One-line bindings export format")
	settle(app, appKey(t, app, "l")) // opens the step: reads its log
	frame(app)
	settle(app, app.r.PendingCommands())
	saveFrame(t, "agent-nested-120", frameWH(app, 120, 70))
	fr := stepTitles(app, first)
	for _, want := range []string{"│ brief · 1 line", "│ tool Bash · mtm bindings export", "│ agent Explore · Child audit · done · foreground", "│ answer"} {
		if !strings.Contains(fr, want) {
			t.Fatalf("nested steps lack %q:\n%s", want, fr)
		}
	}
	// Read: the agent stepped over (Audit the rail) and the one opened,
	// not the others.
	if n := app.subs.logs.Reads(); n != 2 {
		t.Fatalf("%d logs read, want the two stepped onto", n)
	}
	sys := &fakeSystem{}
	app.cx.sys = sys
	for _, verb := range []string{"copy-brief", "copy-result", "open-transcript"} {
		res, err := app.r.Execute(ochazuke.Request{Noun: ochazuke.TimelineBodyNoun, Verb: verb, Target: app.r.Focus})
		if err != nil {
			t.Fatalf("%s: %v", verb, err)
		}
		_ = res
		settle(app, app.r.PendingCommands())
	}
	if len(sys.copied) != 2 || !strings.HasPrefix(sys.copied[0], "BRIEF for One-line bindings export format") || !strings.HasPrefix(sys.copied[1], "# Report: bindings export") {
		t.Fatalf("copied %q", sys.copied)
	}
	if len(sys.opened) != 1 || !strings.HasSuffix(sys.opened[0], "subagents/agent-ab1.jsonl") {
		t.Fatalf("opened %q", sys.opened)
	}
	// The depth-2 agent's step, opened, stops at the cap.
	stepTo(t, app, "│ agent Explore · Child audit")
	settle(app, appKey(t, app, "l"))
	frame(app)
	settle(app, app.r.PendingCommands())
	if text, _ := app.pv.cards.StepText(); !strings.Contains(text, "nesting stops at 2 levels") || strings.Contains(stepTitles(app, first), "│ │ brief") {
		t.Fatalf("depth 2:\n%s", fr)
	}
}

// The tree style shows the same: the notification as one compact row,
// agent steps as rows, an opened one's transcript as child rows.
func TestAgentsInTheTree(t *testing.T) {
	for _, width := range []int{80, 120} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			app, _ := agentsApp(t)
			useTurnStyle(t, app, turnsStyleTree)
			app.m.Width, app.m.Height = width, 60
			rows := strings.Join(turnRows(app), "\n")
			if !strings.Contains(rows, "⤷ One-line bindings export format · finished") || strings.Contains(rows, "task-notification") {
				t.Fatalf("rows:\n%s", rows)
			}
			press(t, app, "}")
			for app.r.Focus != "turn:u1" {
				press(t, app, "k")
			}
			settle(app, key(t, app, "l"))
			rows = strings.Join(turnRows(app), "\n")
			if !strings.Contains(rows, "agent general-purpose: One-line bindings export format · done · background") {
				t.Fatalf("agent row:\n%s", rows)
			}
			id := "turn:u1/step/1"
			if err := app.pv.turns.SetCollapsed(id, false); err != nil {
				t.Fatal(err)
			}
			frame(app)
			app.wantAgents()
			settle(app, app.r.PendingCommands())
			saveFrame(t, fmt.Sprintf("tree-agents-%d", width), frameWH(app, width, 60))
			rows = strings.Join(turnRows(app), "\n")
			if !strings.Contains(rows, "│ brief: BRIEF for One-line bindings export format") || !strings.Contains(rows, "│ ▸ tool Bash: mtm bindings export") && !strings.Contains(rows, "│ tool Bash: mtm bindings export") {
				t.Fatalf("child rows:\n%s", rows)
			}
		})
	}
}

// A session with 70 subagents (and their notifications) builds and draws
// fast and reads no subagent log until one is opened.
func TestSeventySubagentsStayFast(t *testing.T) {
	app, f := panesApp(t)
	f.panes[0].Harness, f.panes[0].AgentState, f.panes[0].AgentSession, f.panes[0].Cwd = "claude-code", "idle", "s1", "/w/proj"
	dir := filepath.Join(app.claudeProjects, turns.ClaudeProjectDir("/w/proj"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "s1.jsonl")
	lines := []string{logPrompt(t, "u1", "2026-10-28T10:00:00Z", "start seventy")}
	for i := range 70 {
		call, agent := fmt.Sprintf("toolu_%02d", i), fmt.Sprintf("a%02d", i)
		lines = append(lines, logAgentCall(t, fmt.Sprintf("c%02d", i), "2026-10-28T10:00:01Z", call, fmt.Sprintf("Lane %02d", i), true), logLaunched(t, fmt.Sprintf("l%02d", i), "2026-10-28T10:00:02Z", call, agent))
		writeAgentFiles(t, log, agent, call, fmt.Sprintf("Lane %02d", i), 1, "", sideLog(t, agent, "x"+agent, "2026-10-28T10:00:02Z", "user", map[string]any{"role": "user", "content": "brief"}))
	}
	lines = append(lines, logAssistant(t, "e1", "2026-10-28T10:00:03Z", "end_turn", map[string]any{"type": "text", "text": "Started."}), logDone(t, "d1", "2026-10-28T10:00:04Z"))
	for i := range 70 {
		lines = append(lines, logNotification(t, fmt.Sprintf("n%02d", i), fmt.Sprintf("2026-10-28T11:%02d:00Z", i%60), realNotification(fmt.Sprintf("a%02d", i), fmt.Sprintf("toolu_%02d", i), "completed", fmt.Sprintf(`Agent "Lane %02d" finished`, i), "report")),
			logAssistant(t, fmt.Sprintf("r%02d", i), fmt.Sprintf("2026-10-28T11:%02d:01Z", i%60), "end_turn", map[string]any{"type": "text", "text": "ok"}),
			logDone(t, fmt.Sprintf("dn%02d", i), fmt.Sprintf("2026-10-28T11:%02d:02Z", i%60)))
	}
	writeLog(t, log, lines...)
	reload(t, app)
	settle(app, key(t, app, "["))
	app.m.Width, app.m.Height = 120, 50
	start := time.Now()
	for range 10 {
		if err := app.showTurns(app.pv.turnsFor); err != nil {
			t.Fatal(err)
		}
		frame(app)
		app.wantAgents()
	}
	per := time.Since(start) / 10
	if n := len(app.pv.cards.Cards()); n != 71 {
		t.Fatalf("%d cards, want the turn and 70 notices", n)
	}
	if n := app.subs.logs.Reads(); n != 0 {
		t.Fatalf("%d subagent logs read with none opened", n)
	}
	// Opening the turn draws its 70 agent steps, still without a read.
	first := app.pv.cards.Cards()[0].ID
	_ = app.pv.cards.Select(first)
	_ = app.pv.cards.SetOpen(first, true)
	start = time.Now()
	if err := app.showTurns(app.pv.turnsFor); err != nil {
		t.Fatal(err)
	}
	fr := frame(app)
	open := time.Since(start)
	if n := app.subs.logs.Reads(); n != 0 || !strings.Contains(fr, "Lane") {
		t.Fatalf("%d reads", n)
	}
	t.Logf("70 subagents: fill+draw %v per pass, open turn %v", per, open)
	if per > 250*time.Millisecond || open > 250*time.Millisecond {
		t.Fatalf("too slow: %v per pass, %v to open", per, open)
	}
}
