package main

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/contextitems"
	"github.com/unixsuperhero/matomeru/internal/history"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// itemsByText is every stored context item, by its text.
func itemsByText(t *testing.T, a *App) map[string]contextitems.Item {
	t.Helper()
	items, err := a.contexts.All()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]contextitems.Item{}
	for _, it := range items {
		if _, dup := out[it.Text]; dup {
			t.Fatalf("%q stored twice", it.Text)
		}
		out[it.Text] = it
	}
	return out
}

// activity is a daemon activity entry of pane w1 under boot1.
func activity(seq uint64, at int64, kind, text string) history.Entry {
	e, ok := history.FromActivity("boot1", history.Pane{Session: "matomeru", Window: "w1", Name: "claude", Harness: "claude-code", Cwd: "/"},
		history.Activity{Seq: seq, At: at * ns, Kind: kind, Text: text})
	if !ok {
		panic("dropped")
	}
	return e
}

// The daemon's activity, as the journal reader journals it: the prompt
// and the answer yield their items, owned by the pane's owner when the
// prompt went in, even when the pane moved before the answer; a reread of
// the ring writes nothing again, and the loop is told to reload.
func TestJournaledTurnsYieldContextItems(t *testing.T) {
	app, _ := panesApp(t)
	at := int64(1_790_000_000_000)
	if err := app.work.SetPaneOwner(work.Assignment{Window: "w1", ProjectID: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	prompt := activity(1, at, "prompt", "review https://github.com/unixsuperhero/matomeru/pull/42 please")
	app.journal(prompt)
	if !app.feed.contextAdded.Load() {
		t.Fatal("a turn with items did not tell the loop")
	}
	if err := app.work.SetPaneOwner(work.Assignment{Window: "w1", ProjectID: "ochazuke"}); err != nil {
		t.Fatal(err)
	}
	answer := activity(2, at+2000, "turn_end", "Done on branch feat/turn-context, see https://example.com/notes and commit 1ba025f.")
	app.journal(answer)
	app.journal(prompt, answer) // a ring read of the same entries
	got := itemsByText(t, app)
	want := map[string]string{
		"https://github.com/unixsuperhero/matomeru/pull/42": "pr",
		"https://example.com/notes":                         "link",
		"1ba025f":                                           "commit",
		"feat/turn-context":                                 "branch",
	}
	for text, kind := range want {
		it, ok := got[text]
		if !ok || it.Kind != kind || it.ProjectID != "matomeru" || it.Provenance.TurnID != prompt.ID {
			t.Fatalf("%s: %+v (all %v)", text, it, slices.Sorted(maps.Keys(got)))
		}
		if len(it.Sources) != 1 || it.Sources[0].Kind != turnContextSource || it.Sources[0].Window != "w1" {
			t.Fatalf("%s sources %+v", text, it.Sources)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("items %v", slices.Sorted(maps.Keys(got)))
	}
	// The views reload the Context page with the journal's burst.
	app.historyChanged()
	if app.feed.contextAdded.Load() {
		t.Fatal("the burst left the flag set")
	}
}

// A prompt typed while the agent works continues the turn: its own text
// is read, for the turn's owner. The daemon's summary of a captured
// prompt is not read (the capture has the whole prompt), and an answer
// the capture replaces is not either.
func TestTurnTextFollowsTheProjection(t *testing.T) {
	app, _ := panesApp(t)
	at := int64(1_790_000_000_000)
	if err := app.work.SetPaneOwner(work.Assignment{Window: "w1", ProjectID: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	app.journal(activity(1, at, "prompt", "look at https://example.com/first"))
	if err := app.work.SetPaneOwner(work.Assignment{Window: "w1", ProjectID: "ochazuke"}); err != nil {
		t.Fatal(err)
	}
	app.journal(activity(2, at+1000, "prompt", "and https://example.com/second"))
	if it := itemsByText(t, app)["https://example.com/second"]; it.ProjectID != "matomeru" {
		t.Fatalf("continuation owned by %+v", it)
	}

	// A captured turn in w1 and the daemon's summary of its prompt.
	later := at + 60_000
	if _, err := app.hist.Capture(history.Turn{ID: "t1", Session: "matomeru", Window: "w1", Harness: "claude-code", ConversationID: "c1",
		Prompt: "open https://example.com/captured/whole/url", StartedAt: later}); err != nil {
		t.Fatal(err)
	}
	app.journal(activity(3, later+200, "prompt", "open https://example.com/captured/wh"))
	app.journal(activity(4, later+5000, "turn_end", "summary https://example.com/summary-answer"))
	got := itemsByText(t, app)
	for _, text := range []string{"https://example.com/captured/wh", "https://example.com/summary-answer"} {
		if _, ok := got[text]; ok {
			t.Fatalf("%s was extracted from a summary", text)
		}
	}
}

// The capture hooks, through the CLI: the start's prompt and the
// completion's answer yield items before the command returns, owned by
// the turn's owner; the command still prints the entry id, and capturing
// the same completion again adds nothing.
func TestCapturedTurnsYieldContextItems(t *testing.T) {
	app, _ := cliApp(t)
	if err := app.work.SetPaneOwner(work.Assignment{Window: "w1", ProjectID: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, app, "history", "capture-turn", "--json", `{"id":"t1","session":"matomeru","window":"w1","harness":"omp","conversation_id":"c1","prompt":"fix unixsuperhero/ochazuke#12","started_at":1790000000000}`)
	done := `{"id":"t1","session":"matomeru","window":"w1","harness":"omp","conversation_id":"c1","prompt":"fix unixsuperhero/ochazuke#12","started_at":1790000000000,"completed_at":1790000060000,"response":"Switched to a new branch 'fix/12'\nhttps://example.com/done"}`
	for range 2 {
		out, errOut, err := cli(app, done, "history", "capture-turn", "--stdin")
		var res map[string]string
		if err != nil || json.Unmarshal([]byte(out), &res) != nil || !strings.HasSuffix(res["id"], ":response") {
			t.Fatalf("completion: %v %q %s", err, out, errOut)
		}
	}
	got := itemsByText(t, app)
	for text, kind := range map[string]string{"unixsuperhero/ochazuke#12": "pr", "fix/12": "branch", "https://example.com/done": "link"} {
		if it, ok := got[text]; !ok || it.Kind != kind || it.ProjectID != "matomeru" {
			t.Fatalf("%s: %+v", text, it)
		}
	}
	if len(got) != 3 {
		t.Fatalf("items %v", slices.Sorted(maps.Keys(got)))
	}
}

// context-item turns reads every journaled turn once (turns journaled
// before extraction from turns existed); a second run adds nothing.
func TestContextTurnsBackfill(t *testing.T) {
	app, _ := cliApp(t)
	at := int64(1_790_000_000_000)
	if _, err := app.hist.Append(activity(1, at, "prompt", "read https://example.com/old"), activity(2, at+1000, "turn_end", "ok 1ba025f")); err != nil {
		t.Fatal(err)
	}
	var res turnsExtracted
	if err := json.Unmarshal([]byte(mustCLI(t, app, "context-item", "turns")), &res); err != nil || res.New != 2 || res.Entries != 2 {
		t.Fatalf("first run %+v %v", res, err)
	}
	if err := json.Unmarshal([]byte(mustCLI(t, app, "context-item", "turns")), &res); err != nil || res.New != 0 {
		t.Fatalf("second run %+v %v", res, err)
	}
	if got := itemsByText(t, app); len(got) != 2 || got["1ba025f"].Kind != "commit" {
		t.Fatalf("items %v", slices.Sorted(maps.Keys(got)))
	}
}

// Reading an agent's transcript (the Transcript tab) extracts its items
// for the pane's owner, and tells the loop to reload.
func TestTranscriptReadYieldsContextItems(t *testing.T) {
	app, f := panesApp(t)
	f.panes[0].Harness = "claude"
	f.transcript = "assistant\nopened https://github.com/unixsuperhero/ochazuke/pull/7"
	if err := app.work.SetPaneOwner(work.Assignment{Window: f.panes[0].ID, ProjectID: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	reload(t, app)
	settle(app, key(t, app, "]"))
	settle(app, key(t, app, "]"))
	it, ok := itemsByText(t, app)["https://github.com/unixsuperhero/ochazuke/pull/7"]
	if !ok || it.Kind != "pr" || it.ProjectID != "matomeru" || it.Sources[0].Kind != "transcript" {
		t.Fatalf("item %+v", it)
	}
	if !app.feed.contextAdded.Load() {
		t.Fatal("the loop was not told")
	}
}

// The daemon cuts a long text with "..."; the cut word names nothing.
func TestUntruncated(t *testing.T) {
	for in, want := range map[string]string{
		"saved at /tmp/proj/notes/todo.md and /Users/me/pr...": "saved at /tmp/proj/notes/todo.md and",
		"whole text":       "whole text",
		"one…":             "",
		"ends here.":       "ends here.",
		"cut mid word… \n": "cut mid",
	} {
		if got := untruncated(in); got != want {
			t.Errorf("untruncated(%q) = %q, want %q", in, got, want)
		}
	}
}
