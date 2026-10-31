package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/harness"
)

// SendKey records a key sent to a pane, "key window name", and runs the
// test's onKey (a harness redrawing its screen).
func (f *fakePanes) SendKey(_ context.Context, p pane, key string) error {
	f.prompts = append(f.prompts, "key "+p.ID+" "+key)
	if f.onKey != nil {
		f.onKey(p.ID, key)
	}
	return f.promptErr
}

// settingsLog is a Claude transcript's settings records in the shapes
// Claude Code 2.1.312 writes them: the session's permission mode and a
// reply with its model, effort, speed and usage.
func settingsLog(t *testing.T) []string {
	return []string{
		logRecord(t, map[string]any{"type": "permission-mode", "permissionMode": "bypassPermissions", "sessionId": "s1"}),
		logRecord(t, map[string]any{"type": "assistant", "uuid": "a9", "timestamp": "2026-10-27T10:00:07Z", "effort": "medium", "message": map[string]any{
			"role": "assistant", "model": "claude-opus-5-5", "content": []map[string]any{{"type": "text", "text": "done"}}, "stop_reason": "end_turn",
			"usage": map[string]any{"input_tokens": 2, "cache_creation_input_tokens": 1000, "cache_read_input_tokens": 319000, "speed": "standard"}}}),
		logRecord(t, map[string]any{"type": "cost-state", "sessionId": "s1", "totalCostUSD": 1.2}),
	}
}

// settingsApp is claudeTurnsApp with the settings records in the log,
// read into the Turns tab.
func settingsApp(t *testing.T) (*App, *fakePanes, string) {
	t.Helper()
	app, f, log := claudeTurnsApp(t)
	// The settings line is read off whole frame lines: the stacked lists
	// draw no borders in the left column (panes_panels_test.go has the
	// panels).
	setting(t, app, settingPanesLayout, layoutStacked)
	writeLog(t, log, settingsLog(t)...)
	readTurnsNow(t, app)
	return app, f, log
}

// readTurnsNow reads the shown pane's log again, as a listing does.
func readTurnsNow(t *testing.T, a *App) {
	t.Helper()
	if _, err := a.r.Execute(ochazuke.Request{Verb: "read-turns", Noun: "pane", Target: "pane:matomeru/w1"}); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
}

func fieldsOf(a *App, window string) map[string]settingField {
	p, _ := a.paneByWindow(window)
	out := map[string]settingField{}
	for _, f := range a.settingFields(p) {
		out[f.Name] = f
	}
	return out
}

// Every value comes from the transcript when it states one; tuios's
// metadata and the launch flags fill only what it does not, the launch
// flags marked "(at launch)".
func TestSettingsFieldSources(t *testing.T) {
	t.Parallel()
	app, f, _ := claudeTurnsApp(t)
	// Nothing read yet: tuios's metadata, then the recipe.
	f.panes[0].Meta = map[string]string{"model": "opus", "context": "12%", "cost": "$0.40"}
	f.panes[0].LaunchFlags = []string{"--effort", "high", "--dangerously-skip-permissions"}
	reload(t, app)
	got := fieldsOf(app, "w1")
	for name, want := range map[string][2]string{
		harness.SettingModel:      {"opus", sourceTuios},
		harness.SettingEffort:     {"effort high (at launch)", sourceLaunch},
		harness.SettingPermission: {"bypass (at launch)", sourceLaunch},
		fieldContext:              {"ctx 12%", sourceTuios},
		fieldCost:                 {"$0.40", sourceTuios},
		harness.SettingFast:       {"fast ?", ""},
	} {
		if got[name].Text != want[0] || got[name].Source != want[1] {
			t.Errorf("%s: %q from %q, want %q from %q", name, got[name].Text, got[name].Source, want[0], want[1])
		}
	}
	// The transcript wins: the launch-time bypass is stale once the
	// session switched to plan (the owner's open question).
	log := filepath.Join(app.claudeProjects, "-w-proj", "s1.jsonl")
	writeLog(t, log, settingsLog(t)...)
	writeLog(t, log, logRecord(t, map[string]any{"type": "permission-mode", "permissionMode": "plan", "sessionId": "s1"}))
	readTurnsNow(t, app)
	got = fieldsOf(app, "w1")
	for name, want := range map[string]string{
		harness.SettingModel:      "Opus 5.5",
		harness.SettingEffort:     "effort medium",
		harness.SettingFast:       "fast off",
		harness.SettingPermission: "plan",
		fieldContext:              "ctx 32%", // 320002 of Opus 5's 1M window (harness contextWindows)
		fieldCost:                 "$1.20",
	} {
		if got[name].Text != want || got[name].Source != sourceLog || got[name].From != "Claude transcript" {
			t.Errorf("%s: %+v, want %q from the log", name, got[name], want)
		}
	}
	// pane.settings prints them with their sources.
	res, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "settings", Target: "pane:matomeru/w1"})
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := res.([]settingField); len(list) != 6 || list[0].Name != harness.SettingModel || !list[0].Changeable || list[4].Changeable {
		t.Fatalf("pane.settings %+v", res)
	}
}

func TestModelAndContextLabels(t *testing.T) {
	t.Parallel()
	for id, want := range map[string]string{
		"claude-opus-5-5":           "Opus 5.5",
		"claude-haiku-4-5-20251001": "Haiku 4.5",
		"claude-fable-5-1[1m]":      "Fable 5.1 1M",
		"openai-codex/gpt-6.1-sol":  "gpt-6.1-sol",
		"Fable 5.1":                 "Fable 5.1",
		"opus":                      "opus",
	} {
		if got := modelLabel(id); got != want {
			t.Errorf("%s: %q, want %q", id, got, want)
		}
	}
	for _, c := range []struct {
		tokens, window int64
		text, tone     string
	}{
		{320_000, 1_000_000, "ctx 32%", ""},
		{170_000, 200_000, "ctx 85%", ochazuke.ToneNeedsInput},
		{196_000, 200_000, "ctx 98%", ochazuke.ToneErrored},
		{455_000, 200_000, "ctx 455k", ""}, // a window too small for what was used is wrong: tokens alone
		{48_557, 0, "ctx 49k", ""},
	} {
		if text, tone := contextText(c.tokens, c.window); text != c.text || tone != c.tone {
			t.Errorf("%d/%d: %q %q, want %q %q", c.tokens, c.window, text, tone, c.text, c.tone)
		}
	}
}

// The line sits under the prompt box, one line; a narrow panel drops
// the lowest-priority fields first (cost, fast, effort), never the model.
func TestSettingsLineAt80And120Columns(t *testing.T) {
	t.Parallel()
	app, _, _ := settingsApp(t)
	line := func(width int) string {
		fr := frameAt(app, width)
		for _, l := range strings.Split(fr, "\n") {
			if strings.Contains(l, "Opus 5.5") {
				return strings.TrimSpace(strings.Trim(strings.TrimSpace(l), "┃│"))
			}
		}
		t.Fatalf("no settings line at %d:\n%s", width, fr)
		return ""
	}
	wide := line(120)
	if wide != "Opus 5.5 · effort medium · fast off · bypass · ctx 32% · $1.20" {
		t.Fatalf("120: %q", wide)
	}
	t.Logf("120 columns: %s", wide)
	narrow := line(80)
	t.Logf("80 columns: %s", narrow)
	if !strings.HasPrefix(narrow, "Opus 5.5") || !strings.Contains(narrow, "ctx 32%") {
		t.Fatalf("80: %q", narrow)
	}
	fr := frameAt(app, 80)
	box := strings.Index(fr, "write to the agent")
	if box < 0 || strings.Index(fr, "Opus 5.5 ·") < box {
		t.Fatalf("the line is not under the prompt box:\n%s", fr)
	}
	t.Logf("frame at 80:\n%s", fr)
	fields := app.settingFields(app.panes[0])
	for width, want := range map[int]string{
		40: "Opus 5.5 · bypass · ctx 32%",
		52: "Opus 5.5 · effort medium · bypass · ctx 32%",
		12: "Opus 5.5",
	} {
		if got := joinFields(fitFields(fields, width)); got != want {
			t.Errorf("fit %d: %q, want %q", width, got, want)
		}
	}
}

// j on the box's last line goes down to the line under it, h and l walk
// its fields, k goes back up to the box, esc to the pane row; enter opens
// the field's chooser.
func TestSettingsLineIsAStopAndItsFieldsAreRows(t *testing.T) {
	t.Parallel()
	app, _, _ := settingsApp(t)
	frame(app)
	press(t, app, "}", "}") // the turns, then the box
	if app.r.Focus != promptID {
		t.Fatalf("focus %s, want the box", app.r.Focus)
	}
	press(t, app, "j") // the box's last line: down to the line under it
	if want := settingRowID("pane:matomeru/w1", harness.SettingModel); app.r.Focus != want {
		t.Fatalf("} after the box: %s, want %s", app.r.Focus, want)
	}
	press(t, app, "l", "l")
	if want := settingRowID("pane:matomeru/w1", harness.SettingFast); app.r.Focus != want {
		t.Fatalf("l l: %s", app.r.Focus)
	}
	press(t, app, "l", "l", "l", "l") // clamps at the last field
	if want := settingRowID("pane:matomeru/w1", fieldCost); app.r.Focus != want {
		t.Fatalf("past the end: %s", app.r.Focus)
	}
	press(t, app, "h", "h", "h", "h") // back to effort
	press(t, app, "enter")
	c := app.r.Chooser("pane-setting-" + harness.SettingEffort)
	if c == nil {
		t.Fatalf("enter on effort opened no chooser; focus %s", app.r.Focus)
	}
	press(t, app, "esc")
	press(t, app, "esc")
	if app.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("esc: %s", app.r.Focus)
	}
	press(t, app, "}", "}", "j", "k")
	if app.r.Focus != promptID {
		t.Fatalf("k from the line: %s, want the box", app.r.Focus)
	}
}

// A chooser lists the values the harness profile gives, the current one
// marked; a harnesses.json row replaces them.
func TestSettingChoosersListTheConfiguredValues(t *testing.T) {
	t.Parallel()
	app, _, _ := settingsApp(t)
	open := func(verb string) []ochazuke.ChooserRow {
		t.Helper()
		if _, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: verb, Target: "pane:matomeru/w1"}); err != nil {
			t.Fatal(err)
		}
		name := strings.TrimPrefix(verb, "set-")
		c := app.r.Chooser("pane-setting-" + name)
		if c == nil {
			t.Fatalf("%s opened no chooser", verb)
		}
		rows := c.Matches()
		if _, err := app.r.Execute(ochazuke.Request{Noun: "ochazuke.modal", Verb: "pop"}); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	marked := func(rows []ochazuke.ChooserRow) (vals []string, cur string) {
		for _, r := range rows {
			vals = append(vals, r.ID)
			if r.Cells[0] == "●" {
				cur = r.ID
			}
		}
		return vals, cur
	}
	if vals, cur := marked(open("set-effort")); !slices.Equal(vals, []string{"low", "medium", "high", "xhigh", "max", "auto"}) || cur != "medium" {
		t.Fatalf("effort %v current %q", vals, cur)
	}
	if vals, cur := marked(open("set-model")); vals[0] != "opus" || cur != "opus" {
		t.Fatalf("model %v current %q", vals, cur)
	}
	if vals, cur := marked(open("set-permission-mode")); !slices.Contains(vals, "plan") || cur != "bypassPermissions" {
		t.Fatalf("perms %v current %q", vals, cur)
	}
	merged, err := harness.Merge(harness.Builtins(), []byte(`[{"name": "claude", "settings": {"effort": {"values": ["low", "ultra"], "command": "/think {value}"}}}]`))
	if err != nil {
		t.Fatal(err)
	}
	if app.harnesses, err = harness.NewRegistry(merged); err != nil {
		t.Fatal(err)
	}
	if vals, _ := marked(open("set-effort")); !slices.Equal(vals, []string{"low", "ultra"}) {
		t.Fatalf("configured effort %v", vals)
	}
}

// At rest a change is the harness's command typed into the pane, and the
// log read after it gives the notice.
func TestSettingChangeTypesTheCommandWhenIdle(t *testing.T) {
	t.Parallel()
	app, f, log := settingsApp(t)
	rec := &ochazuke.RecordingSink{}
	prev := app.r.UI().Attach(rec)
	defer app.r.UI().Attach(prev)
	// The harness's reply is in the log by the time mtm reads it again.
	writeLog(t, log, logRecord(t, map[string]any{"type": "system", "subtype": "local_command", "timestamp": "2026-10-27T10:01:00Z", "content": "<local-command-stdout>Effort level set to high</local-command-stdout>"}))
	if _, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "set-effort", Target: "pane:matomeru/w1", Args: ochazuke.Arguments{"level": "high"}}); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	if strings.Join(f.prompts, "|") != "submit w1 /effort high" {
		t.Fatalf("sent %q", f.prompts)
	}
	if got := rec.Notices(); len(got) != 1 || got[0] != "effort now effort high (Claude transcript)" {
		t.Fatalf("notices %q", got)
	}
	// A value the harness does not take is refused, nothing sent.
	f.prompts = nil
	if _, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "set-effort", Target: "pane:matomeru/w1", Args: ochazuke.Arguments{"level": "turbo"}}); err == nil || !strings.Contains(err.Error(), "claude takes effort: low, medium") {
		t.Fatalf("bad value: %v", err)
	}
	// toggle-fast flips what the log says (off → on).
	if _, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "toggle-fast", Target: "pane:matomeru/w1"}); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	if strings.Join(f.prompts, "|") != "submit w1 /fast on" {
		t.Fatalf("toggle sent %q", f.prompts)
	}
	// Model: the reply names the new model before the next answer does.
	f.prompts = nil
	writeLog(t, log, logRecord(t, map[string]any{"type": "user", "uuid": "u8", "timestamp": "2026-10-27T10:02:00Z", "message": map[string]any{"role": "user", "content": "<local-command-stdout>Set model to `Fable 5.1` and saved as your default for new sessions</local-command-stdout>"}}))
	if _, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "set-model", Target: "pane:matomeru/w1", Args: ochazuke.Arguments{"model": "fable"}}); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	if got := rec.Notices(); got[len(got)-1] != "model now Fable 5.1 (Claude transcript)" {
		t.Fatalf("model notice %q", got)
	}
}

// While the agent works a typed command waits in tuios's delivery queue;
// a stepped one is refused; while it waits on the person nothing goes.
func TestSettingChangeQueuesOrRefusesWhenBusy(t *testing.T) {
	t.Parallel()
	app, f, _ := settingsApp(t)
	rec := &ochazuke.RecordingSink{}
	prev := app.r.UI().Attach(rec)
	defer app.r.UI().Attach(prev)
	f.panes[0].AgentState = "working"
	reload(t, app)
	if _, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "set-effort", Target: "pane:matomeru/w1", Args: ochazuke.Arguments{"level": "max"}}); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	if strings.Join(f.prompts, "|") != "queue w1 /effort max" {
		t.Fatalf("working: %q", f.prompts)
	}
	if got := rec.Notices(); len(got) == 0 || !strings.Contains(got[len(got)-1], "queued /effort max") {
		t.Fatalf("notices %q", got)
	}
	f.prompts = nil
	if _, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "set-permission-mode", Target: "pane:matomeru/w1", Args: ochazuke.Arguments{"mode": "plan"}}); err == nil || !strings.Contains(err.Error(), "the agent is working") {
		t.Fatalf("a key while working: %v", err)
	}
	f.panes[0].AgentState = "needs_input"
	reload(t, app)
	if _, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "set-effort", Target: "pane:matomeru/w1", Args: ochazuke.Arguments{"level": "max"}}); err == nil || !strings.Contains(err.Error(), "waits on you") {
		t.Fatalf("needs input: %v", err)
	}
	if len(f.prompts) != 0 {
		t.Fatalf("sent while busy: %q", f.prompts)
	}
}

// A draft in the prompt box for the pane asks first; y sends and the
// draft stays.
func TestSettingChangeAsksOverADraft(t *testing.T) {
	t.Parallel()
	app, f, _ := settingsApp(t)
	press(t, app, "i")
	typeText(t, app, "half a thought")
	press(t, app, "esc")
	if _, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "set-effort", Target: "pane:matomeru/w1", Args: ochazuke.Arguments{"level": "low"}}); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	if len(f.prompts) != 0 || !strings.Contains(frame(app), "holds a draft for this pane") {
		t.Fatalf("sent over a draft %q:\n%s", f.prompts, frame(app))
	}
	pressSettle(t, app, "y")
	if strings.Join(f.prompts, "|") != "submit w1 /effort low" || string(app.pb.input.Text) != "half a thought" {
		t.Fatalf("after y: %q draft %q", f.prompts, string(app.pb.input.Text))
	}
}

// Claude Code's permission mode steps with shift+tab: mtm presses it and
// reads the screen after each press until the footer shows the mode,
// skipping a mode the session does not offer.
func TestPermissionModeStepsUntilTheScreenShowsIt(t *testing.T) {
	t.Parallel()
	app, f, _ := settingsApp(t)
	rec := &ochazuke.RecordingSink{}
	prev := app.r.UI().Attach(rec)
	defer app.r.UI().Attach(prev)
	// This session offers no auto mode: bypass → default → accept → plan.
	offered := []string{"⏵⏵ bypass permissions on", "? for shortcuts", "⏵⏵ accept edits on", "⏸ plan mode on"}
	at := 0
	f.screens = map[string]string{"w1": "> \n" + offered[at]}
	f.onKey = func(window, key string) {
		at = (at + 1) % len(offered)
		f.screens[window] = "> \n" + offered[at]
	}
	if _, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "set-permission-mode", Target: "pane:matomeru/w1", Args: ochazuke.Arguments{"mode": "plan"}}); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	if strings.Join(f.prompts, "|") != "key w1 BTab|key w1 BTab|key w1 BTab" || at != 3 {
		t.Fatalf("keys %q at %d", f.prompts, at)
	}
	if got := rec.Notices(); len(got) == 0 || got[0] != "permission-mode now plan (the pane's screen, after 3 BTab presses)" {
		t.Fatalf("notices %q", got)
	}
	// A mode the session never shows stops after one round.
	f.prompts = nil
	app.r.UI().Attach(prev)
	_, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "set-permission-mode", Target: "pane:matomeru/w1", Args: ochazuke.Arguments{"mode": "auto"}})
	if err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	if n := len(f.prompts); n != 5 || !strings.Contains(app.m.Status, "does not offer it") {
		t.Fatalf("auto: %d presses, status %q", n, app.m.Status)
	}
}

// omp has no permission modes and Codex takes no in-session argument:
// their verbs say so rather than type anything.
func TestSettingWithoutARowIsRefused(t *testing.T) {
	t.Parallel()
	app, f, _ := settingsApp(t)
	f.panes[0].Harness = "codex"
	reload(t, app)
	_, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "set-model", Target: "pane:matomeru/w1", Args: ochazuke.Arguments{"model": "gpt-6"}})
	if err == nil || !strings.Contains(err.Error(), "codex has no in-session command for model") {
		t.Fatalf("codex: %v", err)
	}
	if len(f.prompts) != 0 {
		t.Fatalf("sent %q", f.prompts)
	}
}

// A Codex pane's line reads its rollout's end.
func TestCodexSettingsFromItsRollout(t *testing.T) {
	t.Parallel()
	app, f := panesApp(t)
	f.panes[0].Harness, f.panes[0].AgentState, f.panes[0].AgentSession = "codex", "idle", "c1"
	day := filepath.Join(app.codexSessions, "2026", "10", "28")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	rollout := `{"timestamp":"2026-10-28T06:40:00Z","type":"turn_context","payload":{"model":"gpt-6.1-sol","effort":"low","approval_policy":"never","sandbox_policy":{"type":"danger-full-access"},"service_tier":null}}
{"timestamp":"2026-10-28T06:40:05Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":237500},"model_context_window":475000}}}
`
	if err := os.WriteFile(filepath.Join(day, "rollout-2026-10-28T02-39-47-c1.jsonl"), []byte(rollout), 0o644); err != nil {
		t.Fatal(err)
	}
	reload(t, app)
	if _, err := app.r.Execute(ochazuke.Request{Verb: "read-settings", Noun: "pane", Target: "pane:matomeru/w1"}); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
	got := joinFields(app.settingFields(app.panes[0]))
	if got != "gpt-6.1-sol · effort low · fast off · approve never · full-access · ctx 50%" {
		t.Fatalf("codex line %q", got)
	}
}
