package main

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/harness"
)

const launchRowsFixture = `{"noun":"model","data":{"harness":"claude","cliSlug":"claude-fable-5","slug":"claude-fable-5","name":"Fable 5","thinking":[{"slug":"low"},{"slug":"high"},{"slug":"nosuch"}]}}
{"noun":"model","data":{"harness":"claude","cliSlug":"claude-haiku-4-5","slug":"claude-haiku-4-5","name":"Haiku 4.5","thinking":[]}}
{"noun":"model","data":{"harness":"codex","cliSlug":"gpt-test","slug":"gpt-test","name":"GPT Test","thinking":[{"slug":"high"}]}}
`

// withLaunchRows gives a the snapshot the fixture rows make.
func withLaunchRows(t *testing.T, a *App) {
	t.Helper()
	a.setLaunchOptions(&launchOptions{Harnesses: parseLaunchOptions([]byte(launchRowsFixture))})
}

// checkOptionsNow runs harness.refresh to its end.
func checkOptionsNow(t *testing.T, a *App) {
	t.Helper()
	if _, err := a.r.Execute(ochazuke.Request{Noun: "harness", Verb: "refresh"}); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
}

func choiceIDs(t *testing.T, a *App, verb string, args ochazuke.Arguments) []string {
	t.Helper()
	v, err := a.r.Execute(ochazuke.Request{Noun: "harness", Verb: verb, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	choices, err := ochazuke.ParseChoices(v)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(choices))
	for i, c := range choices {
		out[i] = c.ID
	}
	return out
}

// Forms read the snapshot; a check replaces it, a failed check keeps it, and
// a harness with no snapshot rows offers its registry values.
func TestLaunchOptionsSnapshotCheckAndFallback(t *testing.T) {
	t.Parallel()
	a := open(t)
	claude, err := a.harnesses.Lookup("claude")
	if err != nil {
		t.Fatal(err)
	}
	if got := choiceIDs(t, a, "models", ochazuke.Arguments{"harness": "claude"}); !slices.Equal(got, claude.Models) {
		t.Fatalf("no snapshot: models %v, want the registry's %v", got, claude.Models)
	}
	a.launchRows = func(context.Context) ([]byte, error) { return []byte(launchRowsFixture), nil }
	checkOptionsNow(t, a)
	if got := choiceIDs(t, a, "models", ochazuke.Arguments{"harness": "claude"}); !slices.Contains(got, "claude-fable-5") || !slices.Contains(got, "opus") {
		t.Fatalf("models after a check: %v", got)
	}
	if _, err := os.Stat(a.launchOptionsPath()); err != nil {
		t.Fatal(err)
	}
	// A model the snapshot lists launches.
	h, _ := a.harnesses.Lookup("claude")
	if got, err := h.Model("claude-fable-5"); err != nil || got != "claude-fable-5" {
		t.Fatalf("Model: %q, %v", got, err)
	}
	a.launchRows = func(context.Context) ([]byte, error) { return nil, errors.New("offline") }
	checkOptionsNow(t, a)
	if got := choiceIDs(t, a, "models", ochazuke.Arguments{"harness": "claude"}); !slices.Contains(got, "claude-fable-5") {
		t.Fatalf("a failed check dropped the snapshot: %v", got)
	}
	if a.launchOpts.Tried.IsZero() || !a.launchOpts.Tried.After(a.launchOpts.Checked) {
		t.Fatalf("the failed try is not recorded: %+v", a.launchOpts)
	}
	now := time.Now()
	if got := launchCheckWait(&launchOptions{Tried: now.Add(-time.Hour)}, now, 24*time.Hour); got != 23*time.Hour {
		t.Fatalf("next check in %v, want 23h", got)
	}
}

// Effort follows the model: only what both the model and the harness's row
// take, none for a model that reports none, the registry's for an alias.
func TestLaunchChoicesFollowHarnessAndModel(t *testing.T) {
	t.Parallel()
	a := open(t)
	withLaunchRows(t, a)
	claude, _ := a.harnesses.Lookup("claude")
	for model, want := range map[string][]string{
		"claude-fable-5":   {"low", "high"},
		"claude-haiku-4-5": {},
		"opus":             claude.Choices(harness.SettingEffort),
	} {
		got := choiceIDs(t, a, "choices", ochazuke.Arguments{"setting": harness.SettingEffort, "harness": "claude", "model": model})
		if !slices.Equal(got, want) {
			t.Fatalf("effort of %s: %v, want %v", model, got, want)
		}
	}
}

// pickIn opens the focused choice field's picker, filters it and picks.
func pickIn(t *testing.T, a *App, field, query string) {
	t.Helper()
	focusAskField(t, a, field)
	press(t, a, "/")
	typeText(t, a, query)
	press(t, a, "enter")
}

func focusAskField(t *testing.T, a *App, field string) {
	t.Helper()
	for range len(askFields) + 1 {
		if a.r.FocusID() == askFormID+"/"+field {
			return
		}
		press(t, a, "tab")
	}
	t.Fatalf("focus never reached %s: %s", field, a.r.FocusID())
}

// Changing the harness or the model empties what the new choice does not
// take, with no error; what it still takes stays.
func TestAskFormClearsWhatTheNewChoiceDoesNotTake(t *testing.T) {
	t.Parallel()
	a := open(t)
	withLaunchRows(t, a)
	press(t, a, "g", "q")
	typeText(t, a, "why")
	e := a.r.RecordEditor(askFormID)
	pickIn(t, a, "harness", "claude")
	pickIn(t, a, "model", "claude-fable-5")
	pickIn(t, a, "effort", "low")
	if got := e.Values(); got["model"] != "claude-fable-5" || got["effort"] != "low" {
		t.Fatalf("picked %v", got)
	}
	pickIn(t, a, "model", "claude-haiku-4-5")
	if got := e.Values(); got["model"] != "claude-haiku-4-5" || got["effort"] != "" || e.Problem("effort") != "" {
		t.Fatalf("after the model changed: %v, problem %q", got, e.Problem("effort"))
	}
	pickIn(t, a, "harness", "codex")
	if got := e.Values(); got["model"] != "" || e.Problem("model") != "" {
		t.Fatalf("after the harness changed: %v, problem %q", got, e.Problem("model"))
	}
}

// A letter typed at a launch field filters its picker and never becomes the
// value; with the setting off the field takes the text.
func TestStrayLetterNeverBecomesALaunchValue(t *testing.T) {
	t.Parallel()
	for _, on := range []bool{true, false} {
		a := open(t)
		if !on {
			if err := a.r.Settings().Set(settingLaunchPickers, "false"); err != nil {
				t.Fatal(err)
			}
		}
		press(t, a, "g", "q")
		typeText(t, a, "why")
		focusAskField(t, a, "fast")
		press(t, a, "i")
		typeText(t, a, "b")
		press(t, a, "esc")
		if a.r.TopModal() != askFormID {
			press(t, a, "esc")
		}
		want := map[bool]string{true: "", false: "b"}[on]
		if got := a.r.RecordEditor(askFormID).Values()["fast"]; got != want {
			t.Fatalf("pickers %v: fast holds %q, want %q", on, got, want)
		}
	}
}

// askFrame is the frame on a screen the whole form fits on.
func askFrame(a *App) string {
	a.m.Width, a.m.Height = 120, 50
	return ansi.Strip(frameView(a).Content)
}

// labelRows are the rows the form's launch field labels are drawn on.
func labelRows(t *testing.T, a *App) map[string]int {
	t.Helper()
	lines := strings.Split(askFrame(a), "\n")
	out := map[string]int{"lines": len(lines)}
	for _, name := range askFields[1:7] {
		out[name] = slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, "│ "+name+" ") })
		if out[name] < 0 {
			t.Fatalf("no label %s in\n%s", name, askFrame(a))
		}
	}
	return out
}

// chooserBox is the rows the open picker's frame spans.
func chooserBox(a *App) [2]int {
	lines := strings.Split(askFrame(a), "\n")
	var marked []int
	for i, l := range lines {
		if strings.ContainsAny(l, "╭╰") {
			marked = append(marked, i)
		}
	}
	if len(marked) < 4 { // the form's frame around the picker's
		return [2]int{-1, -1}
	}
	return [2]int{marked[1], marked[2]}
}

// Nothing moves: the form's fields keep their rows while focusing, picking
// and after a dependent field empties, and the picker keeps its size while
// filtering.
func TestAskFormDoesNotMove(t *testing.T) {
	t.Parallel()
	a := open(t)
	withLaunchRows(t, a)
	press(t, a, "g", "q")
	typeText(t, a, "why")
	press(t, a, "esc")
	base := labelRows(t, a)
	same := func(step string) {
		t.Helper()
		if got := labelRows(t, a); !mapsEqual(got, base) {
			t.Fatalf("%s moved the form: %v, was %v\n%s", step, got, base, askFrame(a))
		}
	}
	for _, field := range askFields[1:5] {
		focusAskField(t, a, field)
		same("focusing " + field)
	}
	pickIn(t, a, "model", "claude-fable-5")
	same("picking a model")
	pickIn(t, a, "effort", "low")
	pickIn(t, a, "harness", "codex")
	same("a dependent field emptying")

	pickIn(t, a, "harness", "claude")
	focusAskField(t, a, "model")
	press(t, a, "i")
	box := chooserBox(a)
	if box[0] < 0 {
		t.Fatalf("no picker frame:\n%s", askFrame(a))
	}
	for _, letter := range strings.Split("fable", "") {
		typeText(t, a, letter)
		if got := chooserBox(a); got != box {
			t.Fatalf("filtering by %q moved the picker: %v, was %v\n%s", letter, got, box, askFrame(a))
		}
	}
}

func mapsEqual(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
