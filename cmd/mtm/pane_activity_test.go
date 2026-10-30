package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/turns"
)

// subagentsApp is the panes app on a working Claude pane whose session
// started five agents: a background one running, a foreground one
// running, one that failed and one killed ten seconds ago, and one that
// finished.
func subagentsApp(t *testing.T) *App {
	t.Helper()
	app, f := panesApp(t)
	groupBy(t, app, groupProjectSessionPane)
	f.panes[0].Harness, f.panes[0].AgentState, f.panes[0].AgentSession, f.panes[0].Cwd = "claude-code", "working", "s1", "/w/proj"
	dir := filepath.Join(app.claudeProjects, turns.ClaudeProjectDir("/w/proj"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ago := func(d time.Duration) string { return time.Now().Add(-d).UTC().Format(time.RFC3339) }
	writeLog(t, filepath.Join(dir, "s1.jsonl"),
		logPrompt(t, "u1", ago(5*time.Minute), "start the agents"),
		logAgentCall(t, "a1", ago(4*time.Minute), "toolu_bg", "Export bindings", true),
		logLaunched(t, "r1", ago(4*time.Minute), "toolu_bg", "ab1"),
		logAgentCall(t, "a2", ago(4*time.Minute), "toolu_x", "L4 on fake server", true),
		logLaunched(t, "r2", ago(4*time.Minute), "toolu_x", "ax1"),
		logAgentCall(t, "a3", ago(4*time.Minute), "toolu_k", "Stale spike", true),
		logLaunched(t, "r3", ago(4*time.Minute), "toolu_k", "ak1"),
		logAgentCall(t, "a4", ago(4*time.Minute), "toolu_d", "Done review", true),
		logLaunched(t, "r4", ago(4*time.Minute), "toolu_d", "ad1"),
		logAssistant(t, "a9", ago(4*time.Minute), "end_turn", map[string]any{"type": "text", "text": "Four started."}),
		logDone(t, "d1", ago(4*time.Minute)),
		logNotification(t, "n1", ago(3*time.Minute), realNotification("ad1", "toolu_d", "completed", `Agent "Done review" finished`, "ok")),
		logNotification(t, "n2", ago(10*time.Second), realNotification("ax1", "toolu_x", "failed", `Agent "L4 on fake server" failed: API error`, "")),
		logNotification(t, "n3", ago(10*time.Second), realNotification("ak1", "toolu_k", "killed", `Agent "Stale spike" was stopped by Claude`, "")),
		logPrompt(t, "u2", ago(2*time.Minute), "audit now"),
		logAgentCall(t, "a5", ago(2*time.Minute), "toolu_fg", "Audit the rail", false),
	)
	reload(t, app)
	return app
}

func activityRows(app *App) map[string]string {
	out := map[string]string{}
	for _, it := range sectionItems(app) {
		if it.Noun == activityNoun {
			out[it.ID] = strings.TrimSpace(strings.Join(it.Cells, " | ")) // the tree's indent off
		}
	}
	return out
}

// An agent pane's running subagents are rows under its row: the
// description and type, the status word and its age, toned by its class;
// a finished one is not there, a failed or killed one stays
// panes.subagents-linger; panes.subagents off takes every row away. The
// rows follow the pane's row: in the Working list while it works, in the
// tree under its session once idle.
func TestPaneListShowsActiveSubagentsUnderTheirPane(t *testing.T) {
	app := subagentsApp(t)
	rows := activityRows(app)
	parent := "pane:matomeru/w1"
	want := map[string]string{
		"child:w1/toolu_bg": "● Export bindings · general-purpose | running 4m",
		"child:w1/toolu_fg": "● Audit the rail · general-purpose | running 2m",
		"child:w1/toolu_x":  "✗ L4 on fake server · general-purpose | failed 1",
		"child:w1/toolu_k":  "⊘ Stale spike · general-purpose | killed 1",
	}
	if len(rows) != len(want) {
		t.Fatalf("rows %v\n%s", rows, frameWH(app, 140, 40))
	}
	for id, w := range want {
		if !strings.HasPrefix(rows[id], w) { // the age's last digit may move on a slow run
			t.Fatalf("%s: %q, want %q…", id, rows[id], w)
		}
	}
	var nodes []ochazuke.TreeNode
	for _, sec := range app.pv.sections {
		nodes = append(nodes, sec.tree.Nodes()...)
	}
	for _, n := range nodes {
		if n.Noun != activityNoun {
			continue
		}
		if n.Parent != parent {
			t.Fatalf("%s under %q", n.ID, n.Parent)
		}
		tone := ochazuke.ToneWorking
		if strings.HasSuffix(n.ID, "toolu_x") || strings.HasSuffix(n.ID, "toolu_k") {
			tone = ochazuke.ToneErrored
		}
		if len(n.Tones) != 2 || n.Tones[0].Tone != tone || n.Tones[1].Tone != tone {
			t.Fatalf("%s tones %+v", n.ID, n.Tones)
		}
	}
	fr := frameWH(app, 140, 40)
	for _, s := range []string{"● Export binding", "running 4m", "✗ L4 on fake", "failed 1"} {
		if !strings.Contains(fr, s) {
			t.Fatalf("frame lacks %q:\n%s", s, fr)
		}
	}
	if os.Getenv("MTM_FRAMES") != "" {
		t.Logf("frame:\n%s", fr)
	}
	// The bad ends drop after the linger.
	setting(t, app, settingPanesSubagentsLinger, "5s")
	reload(t, app)
	if rows := activityRows(app); len(rows) != 2 || rows["child:w1/toolu_x"] != "" {
		t.Fatalf("after the linger: %v", rows)
	}
	// A foreground agent runs only while its pane's agent works.
	f := app.panesd.(*fakePanes)
	f.panes[0].AgentState = "idle"
	reload(t, app)
	if app.paneErr != "" {
		t.Fatal(app.paneErr)
	}
	if rows := activityRows(app); len(rows) != 1 || rows["child:w1/toolu_bg"] == "" {
		t.Fatalf("idle pane: %v", rows)
	}
	setting(t, app, settingPanesSubagents, subagentsOff)
	reload(t, app)
	if rows := activityRows(app); len(rows) != 0 {
		t.Fatalf("off: %v", rows)
	}
}

// j and k pass through the child rows (drawing order); a selected child
// row shows its pane's detail; enter (pane-child.open) puts the detail on
// the Turns tab with the card of the turn that started the subagent
// selected, the focus kept on the list; a child row that drops off leaves
// the cursor on its pane.
func TestSubagentRowsAreSelectableAndOpenTheirCard(t *testing.T) {
	app := subagentsApp(t)
	parent := "pane:matomeru/w1"
	if err := app.selectPaneRow(parent); err != nil {
		t.Fatal(err)
	}
	settle(app, key(t, app, "j"))
	row, inst := app.selectedRow()
	if !strings.HasPrefix(row, "child:w1/") {
		t.Fatalf("j from the pane: %s", row)
	}
	if p, err := app.paneAt(inst); err != nil || p.ID != "w1" {
		t.Fatalf("the child row's pane: %v %v", p.ID, err)
	}
	if err := app.selectPaneRow("child:w1/toolu_fg"); err != nil {
		t.Fatal(err)
	}
	focus := app.r.Focus
	settle(app, key(t, app, "enter"))
	settle(app, follow(app))
	if app.pv.tab != tabTurns {
		t.Fatalf("tab %d", app.pv.tab)
	}
	if app.r.Focus != focus {
		t.Fatalf("focus moved: %s → %s", focus, app.r.Focus)
	}
	tl := app.pv.timelines["w1"]
	if tl == nil {
		t.Fatalf("no timeline:\n%s", frameWH(app, 140, 40))
	}
	if sel := tl.SelectedID(); sel != turnCardID("w1", "u2") {
		t.Fatalf("selected card %q, want the audit turn's:\n%s", sel, frameWH(app, 140, 40))
	}
	// The killed agent's row is selected when the linger ends.
	if err := app.selectPaneRow("child:w1/toolu_k"); err != nil {
		t.Fatal(err)
	}
	setting(t, app, settingPanesSubagentsLinger, "5s")
	reload(t, app)
	if row, _ := app.selectedRow(); row != parent {
		t.Fatalf("after its row dropped: %s", row)
	}
}
