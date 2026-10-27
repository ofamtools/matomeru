package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/contextitems"
	"github.com/unixsuperhero/matomeru/internal/daemon"
)

// promptApp is claudeTurnsApp with the prompt box focused through i on
// the pane row, in insert mode.
func promptApp(t *testing.T) (*App, *fakePanes) {
	t.Helper()
	app, f, _ := claudeTurnsApp(t)
	if !strings.Contains(frame(app), "write to the agent") {
		t.Fatalf("no prompt box under the turns:\n%s", frame(app))
	}
	press(t, app, "i")
	if app.r.Focus != promptID || app.r.BaseMode() != "insert" {
		t.Fatalf("i focused %s in %s", app.r.Focus, app.r.BaseMode())
	}
	return app, f
}

// boxRule is the box's rule line as drawn.
func boxRule(t *testing.T, a *App) string {
	t.Helper()
	for _, l := range strings.Split(frame(a), "\n") {
		if i := strings.Index(l, "── ["); i >= 0 {
			return l[i:]
		}
	}
	t.Fatalf("no prompt box rule:\n%s", frame(a))
	return ""
}

func TestPromptBoxModesCycleAndShow(t *testing.T) {
	app, f := promptApp(t)
	if r := boxRule(t, app); !strings.Contains(r, "[send] claude-code") || !strings.Contains(r, "enter sends") || !strings.Contains(r, "ctrl+t mode") {
		t.Fatalf("rule %q", r)
	}
	for _, want := range []string{"[queue]", "[interrupt]", "[send]"} {
		press(t, app, "ctrl+t")
		if r := boxRule(t, app); !strings.Contains(r, want) {
			t.Fatalf("after ctrl+t: %q, want %s", r, want)
		}
	}
	// A send while the agent works is a steer, and the box says so.
	f.panes[0].AgentState = "working"
	reload(t, app)
	if r := boxRule(t, app); !strings.Contains(r, "[steer]") {
		t.Fatalf("working: %q", r)
	}
	// Each mode has its own colour (mono tells them apart by the label).
	app.m.Appearance.Theme = "dark"
	s := ochazuke.NewStyles(*app.m.Appearance)
	seen := map[string]int{}
	for mode := range promptModes {
		app.pb.mode = mode
		seen[app.promptModeStyle(s).Render("x")] = mode
	}
	if len(seen) != len(promptModes) {
		t.Fatalf("modes share a colour: %v", seen)
	}
	app.pb.mode = promptSend
	// The palette spelling names a mode.
	if _, err := app.r.Execute(ochazuke.Request{Verb: "mode", Noun: promptNoun, Args: ochazuke.Arguments{"mode": "queue"}}); err != nil || app.pb.mode != promptQueue {
		t.Fatalf("mode queue: %v %d", err, app.pb.mode)
	}
}

// enter sends (a paste then the harness's Enter, through tuios's
// send-text); ctrl+j breaks the line; the box clears once it went.
func TestPromptBoxSendsAndClears(t *testing.T) {
	app, f := promptApp(t)
	typeText(t, app, "fix it")
	press(t, app, "ctrl+j")
	typeText(t, app, "then test")
	if got := string(app.pb.input.Text); got != "fix it\nthen test" {
		t.Fatalf("text %q", got)
	}
	if strings.Count(frame(app), "› fix it") != 1 || !strings.Contains(frame(app), "  then test") {
		t.Fatalf("the box grows to its lines:\n%s", frame(app))
	}
	pressSettle(t, app, "enter")
	if len(f.prompts) != 1 || f.prompts[0] != "submit w1 fix it\nthen test" {
		t.Fatalf("calls %q", f.prompts)
	}
	if len(app.pb.input.Text) != 0 || app.r.Focus != promptID || app.r.BaseMode() != "insert" {
		t.Fatalf("after send: %q focus %s %s", string(app.pb.input.Text), app.r.Focus, app.r.BaseMode())
	}
	// Nothing to send is refused, and nothing reaches the pane.
	app.Update(keyMsg("enter"))
	if !strings.Contains(app.m.Status, "nothing to send") || len(f.prompts) != 1 {
		t.Fatalf("empty: %q %q", app.m.Status, f.prompts)
	}
}

func TestPromptBoxInterruptAndQueue(t *testing.T) {
	app, f := promptApp(t)
	f.panes[0].AgentState = "working"
	reload(t, app)
	press(t, app, "ctrl+t", "ctrl+t") // interrupt
	typeText(t, app, "stop, use the cache")
	pressSettle(t, app, "enter")
	if strings.Join(f.prompts, "|") != "interrupt w1|submit w1 stop, use the cache" {
		t.Fatalf("interrupt: %q", f.prompts)
	}
	if app.pb.mode != promptSend {
		t.Fatalf("an interrupt goes back to send, mode %d", app.pb.mode)
	}
	f.prompts = nil
	press(t, app, "ctrl+t") // queue
	f.queued = daemon.QueuePromptResult{ID: "q1", Position: 2, Queued: 2}
	typeText(t, app, "then commit")
	pressSettle(t, app, "enter")
	if strings.Join(f.prompts, "|") != "queue w1 then commit" || !strings.Contains(app.m.Status, "queued (2 waiting)") || len(app.pb.input.Text) != 0 {
		t.Fatalf("queue: %q %q %q", f.prompts, app.m.Status, string(app.pb.input.Text))
	}
	// A full queue says why and keeps the text.
	f.prompts, f.queueErr = nil, &daemon.Error{Code: daemon.CodeQueueFull, Message: "full"}
	app.m.Status = ""
	typeText(t, app, "more")
	_, cmd := app.Update(keyMsg("enter"))
	settle(app, cmd)
	if !strings.Contains(app.m.Status, "queue is full (tuios keeps 8 prompts a pane)") || string(app.pb.input.Text) != "more" {
		t.Fatalf("full: %q %q", app.m.Status, string(app.pb.input.Text))
	}
	// A prompt over the queue's 16 KiB is refused before tuios sees it.
	f.prompts, app.m.Status = nil, ""
	if err := app.pb.input.Replace(0, len(app.pb.input.Text), strings.Repeat("x", daemon.QueueMaxText+1)); err != nil {
		t.Fatal(err)
	}
	app.Update(keyMsg("enter"))
	if !strings.Contains(app.m.Status, "at most 16 KiB") || len(f.prompts) != 0 {
		t.Fatalf("too long: %q %q", app.m.Status, f.prompts)
	}
}

// While the agent waits on the person, send and interrupt refuse: tuios
// would type into the prompt it shows. A queue waits for its rest.
func TestPromptBoxNeverTypesOverAWaitingPrompt(t *testing.T) {
	app, f := promptApp(t)
	f.panes[0].AgentState = "needs_input"
	reload(t, app)
	if r := boxRule(t, app); !strings.Contains(r, "waits on you") {
		t.Fatalf("rule %q", r)
	}
	typeText(t, app, "yes")
	app.Update(keyMsg("enter"))
	if !strings.Contains(app.m.Status, "waits on you") || len(f.prompts) != 0 {
		t.Fatalf("status %q calls %q", app.m.Status, f.prompts)
	}
	app.m.Status = ""
	press(t, app, "ctrl+t")
	pressSettle(t, app, "enter")
	if len(f.prompts) != 1 || f.prompts[0] != "queue w1 yes" {
		t.Fatalf("queue: %q", f.prompts)
	}
}

// esc leaves insert, then goes back to the turns; } from the turns comes
// back to the box; a shell pane has no box.
func TestPromptBoxIsAStopAfterTheTurns(t *testing.T) {
	app, _ := promptApp(t)
	press(t, app, "esc", "esc")
	if app.r.Instances[app.r.Focus].Noun != "turn" {
		t.Fatalf("esc esc focused %s", app.r.Focus)
	}
	press(t, app, "}")
	if app.r.Focus != promptID {
		t.Fatalf("} from the turns focused %s", app.r.Focus)
	}
	if _, err := app.r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
	shell, _ := panesApp(t)
	shell.pv.tab = tabTurns
	frameText := frame(shell)
	if strings.Contains(frameText, "write to the agent") {
		t.Fatalf("a shell pane drew the box:\n%s", frameText)
	}
}

// home is the prompt sources' test home.
func promptHome(a *App) string { return a.pb.sources.Env.Home }

func writeTextFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func suggestionTexts(s []ochazuke.Suggestion) []string {
	var out []string
	for _, x := range s {
		out = append(out, x.Text)
	}
	return out
}

func TestPromptCompletionsFilesContextCommandsSkills(t *testing.T) {
	app, f, _ := claudeTurnsApp(t)
	cwd := t.TempDir()
	writeTextFile(t, filepath.Join(cwd, "internal", "turns", "omp.go"), "package turns\n")
	writeTextFile(t, filepath.Join(cwd, "README.md"), "# x\n")
	f.panes[0].Cwd = cwd
	reload(t, app)
	if _, _, err := app.contexts.Merge([]contextitems.Item{{ID: "c1", Kind: "link", Text: "https://example.test/omp-notes"}}, contextitems.Owner{}, 1); err != nil {
		t.Fatal(err)
	}
	home := promptHome(app)
	writeTextFile(t, filepath.Join(home, ".claude", "commands", "cr.md"), "---\ndescription: review\n---\n")
	writeTextFile(t, filepath.Join(home, ".claude", "skills", "deploy", "SKILL.md"), "---\nname: deploy\ndescription: Ship it\n---\n")
	press(t, app, "i")
	ctx := context.Background()
	got, _ := app.searchMentions(ctx, "omp")
	if texts := suggestionTexts(got); len(texts) != 2 || texts[0] != "https://example.test/omp-notes" || texts[1] != "internal/turns/omp.go" {
		t.Fatalf("@omp: %q", texts)
	}
	got, _ = app.searchCommands(ctx, "cr")
	if texts := suggestionTexts(got); len(texts) == 0 || texts[0] != "cr" {
		t.Fatalf("/cr: %q", texts)
	}
	got, _ = app.searchCommands(ctx, "deploy")
	if texts := suggestionTexts(got); len(texts) != 1 || texts[0] != "deploy" {
		t.Fatalf("/deploy (a Claude skill is a command): %q", texts)
	}
	got, _ = app.searchSkills(ctx, "dep")
	if len(got) != 1 || got[0].Text != "/deploy" || got[0].Label != "deploy" || got[0].Detail != "Ship it" {
		t.Fatalf("$dep: %+v", got)
	}
}

// Accepting a skill from the $ list writes the harness's syntax: Claude
// Code's /name, omp's /skill:name, mid-text too.
func TestPromptAcceptingASkillWritesTheHarnessSyntax(t *testing.T) {
	app, _ := promptApp(t)
	writeTextFile(t, filepath.Join(promptHome(app), ".claude", "skills", "deploy", "SKILL.md"), "---\nname: deploy\n---\n")
	acceptSkill(t, app, "$dep")
	if got := string(app.pb.input.Text); got != "/deploy " {
		t.Fatalf("claude: %q", got)
	}

	omp, f := panesApp(t)
	f.panes[0].Harness, f.panes[0].AgentState, f.panes[0].AgentSession = "omp", "idle", "s9"
	reload(t, omp)
	settle(omp, key(t, omp, "["))
	writeTextFile(t, filepath.Join(promptHome(omp), ".omp", "agent", "skills", "deploy", "SKILL.md"), "---\nname: deploy\n---\n")
	press(t, omp, "i")
	typeText(t, omp, "use ")
	acceptSkill(t, omp, "$dep")
	typeText(t, omp, "now")
	if got := string(omp.pb.input.Text); got != "use /skill:deploy now" {
		t.Fatalf("omp: %q", got)
	}
}

// acceptSkill types a $ token, opens its list with tab and accepts the
// first suggestion with tab.
func acceptSkill(t *testing.T, a *App, token string) {
	t.Helper()
	typeText(t, a, token)
	pressSettle(t, a, "tab")
	c := a.r.Completer()
	if !c.Open() || len(c.Suggestions()) == 0 {
		t.Fatalf("tab opened %v %+v (%v)", c.Open(), c.Suggestions(), c.Err())
	}
	press(t, a, "tab")
	if a.r.Completer().Open() {
		t.Fatal("the list stayed open after the accept")
	}
}
