package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

const titlePrompt = "Hey, can you please fix the flaky queue dispatch test in the daemon package and then rerun the whole suite?"

// titleServer starts a daemon over an in-memory store. Set the summarizer
// environment with t.Setenv before calling it.
func titleServer(t *testing.T) (*Server, *Client, *data.Store) {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	srv, client := startTestServer(t, Config{Store: store, DisableMCP: true})
	return srv, client, store
}

func newTitlePane(t *testing.T, store *data.Store, title, harnessID string) string {
	t.Helper()
	rec, _, err := store.Create(data.CreateInput{
		Kind:   data.KindPane,
		Fields: map[string]any{"title": title, "harness": harnessID},
	}, daemonActor)
	if err != nil {
		t.Fatal(err)
	}
	return rec.ID
}

// sendPrompt delivers one prompt hook event and waits for any summarizer it
// started to finish.
func sendPrompt(t *testing.T, srv *Server, client *Client, paneID, prompt string) {
	t.Helper()
	ev := HookEvent{PaneID: paneID, Harness: "claude", Kind: HookKindState, State: StateRunning, Prompt: prompt}
	if _, err := client.HookEvent(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	srv.titles.inflight.Wait()
}

func paneRecord(t *testing.T, store *data.Store, paneID string) *data.Record {
	t.Helper()
	rec, err := store.Get(core.Ref{Kind: data.KindPane, ID: paneID})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// fakeSummarizer writes a script that logs one line per run to the returned
// file and then executes body.
func fakeSummarizer(t *testing.T, body string) (command, runsPath string) {
	t.Helper()
	dir := t.TempDir()
	runsPath = filepath.Join(dir, "runs")
	script := filepath.Join(dir, "summarize.sh")
	text := "#!/bin/sh\necho run >> '" + runsPath + "'\n" + body + "\n"
	if err := os.WriteFile(script, []byte(text), 0o755); err != nil {
		t.Fatal(err)
	}
	return "'" + script + "'", runsPath
}

func runCount(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.Count(string(raw), "run\n")
}

func TestPaneTitleCommandTitlesGenericPaneOnceAndSticks(t *testing.T) {
	// The script proves it got the prompt on stdin by requiring a word of it.
	command, runs := fakeSummarizer(t, `grep -q "flaky queue" && printf '"Fix flaky queue dispatch test"\nsecond line\n'`)
	t.Setenv(EnvTitleSummarizerCmd, command)
	srv, client, store := titleServer(t)
	paneID := newTitlePane(t, store, "claude", "claude")

	sendPrompt(t, srv, client, paneID, titlePrompt)
	pane := paneRecord(t, store, paneID)
	if got := pane.Text("title"); got != "Fix flaky queue dispatch test" {
		t.Fatalf("title = %q", got)
	}
	if pane.Text(paneTitleSummarizedField) == "" {
		t.Fatal("pane not marked summarized")
	}

	// Later prompts never re-title it, and never run the command again.
	sendPrompt(t, srv, client, paneID, "Now rewrite the scheduler tick loop to use a heap")
	sendPrompt(t, srv, client, paneID, "Add a settings page for the scheduler interval")
	if got := paneRecord(t, store, paneID).Text("title"); got != "Fix flaky queue dispatch test" {
		t.Fatalf("re-titled to %q", got)
	}
	if n := runCount(t, runs); n != 1 {
		t.Fatalf("summarizer ran %d times, want 1", n)
	}
}

func TestPaneTitleMarkerSurvivesDaemonRestart(t *testing.T) {
	command, runs := fakeSummarizer(t, `echo "Should never appear"`)
	t.Setenv(EnvTitleSummarizerCmd, command)
	srv, client, store := titleServer(t)
	// A summarized pane whose title was later set back to a placeholder: a
	// fresh daemon has no in-memory claim, so only the marker protects it.
	paneID := newTitlePane(t, store, "claude", "claude")
	if _, _, err := store.Patch(data.PatchInput{
		Ref:    core.Ref{Kind: data.KindPane, ID: paneID},
		Fields: map[string]any{paneTitleSummarizedField: "true"},
	}, daemonActor); err != nil {
		t.Fatal(err)
	}

	sendPrompt(t, srv, client, paneID, titlePrompt)
	if got := paneRecord(t, store, paneID).Text("title"); got != "claude" {
		t.Fatalf("re-titled to %q", got)
	}
	if n := runCount(t, runs); n != 0 {
		t.Fatalf("summarizer ran %d times, want 0", n)
	}
}

func TestPaneTitleNeverOverwritesChosenTitle(t *testing.T) {
	command, runs := fakeSummarizer(t, `echo "Should never appear"`)
	t.Setenv(EnvTitleSummarizerCmd, command)
	srv, client, store := titleServer(t)

	for _, title := range []string{"D2 (Fable 5.2): Features Props", "claude-fable: W11F sticky pane title", "my notes"} {
		paneID := newTitlePane(t, store, title, "claude-fable")
		sendPrompt(t, srv, client, paneID, titlePrompt)
		sendPrompt(t, srv, client, paneID, titlePrompt)
		pane := paneRecord(t, store, paneID)
		if got := pane.Text("title"); got != title {
			t.Fatalf("title %q overwritten with %q", title, got)
		}
		if pane.Text(paneTitleSummarizedField) != "" {
			t.Fatalf("pane %q marked summarized", title)
		}
	}
	if n := runCount(t, runs); n != 0 {
		t.Fatalf("summarizer ran %d times, want 0", n)
	}
}

func TestPaneTitleLeavesRenameDuringSummarizeAlone(t *testing.T) {
	// The script signals that it started, then waits for the rename.
	dir := t.TempDir()
	started, renamed := filepath.Join(dir, "started"), filepath.Join(dir, "renamed")
	command, _ := fakeSummarizer(t, "touch '"+started+"'\nwhile [ ! -e '"+renamed+"' ]; do sleep 0.02; done\necho 'Late machine title'")
	t.Setenv(EnvTitleSummarizerCmd, command)
	srv, client, store := titleServer(t)
	paneID := newTitlePane(t, store, "zsh", "shell")

	ev := HookEvent{PaneID: paneID, Kind: HookKindState, State: StateRunning, Prompt: titlePrompt}
	if _, err := client.HookEvent(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, started)
	if _, _, err := store.Patch(data.PatchInput{
		Ref:    core.Ref{Kind: data.KindPane, ID: paneID},
		Fields: map[string]any{"title": "release notes"},
	}, daemonActor); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(renamed, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	srv.titles.inflight.Wait()
	if got := paneRecord(t, store, paneID).Text("title"); got != "release notes" {
		t.Fatalf("rename overwritten with %q", got)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
}

func TestPaneTitleFallsBackWhenCommandFailsOrTimesOut(t *testing.T) {
	want := SummarizePromptTitle(titlePrompt, PaneTitleMaxChars)
	cases := map[string]string{
		"exit status": `echo "Ignored title"; exit 3`,
		"no output":   `true`,
		"timeout":     `sleep 30; echo "Too late"`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			command, runs := fakeSummarizer(t, body)
			t.Setenv(EnvTitleSummarizerCmd, command)
			t.Setenv(EnvTitleSummarizerTimeout, "1s")
			srv, client, store := titleServer(t)
			paneID := newTitlePane(t, store, "", "codex")

			start := time.Now()
			ev := HookEvent{PaneID: paneID, Kind: HookKindState, State: StateRunning, Prompt: titlePrompt}
			if _, err := client.HookEvent(context.Background(), ev); err != nil {
				t.Fatal(err)
			}
			if d := time.Since(start); d > 900*time.Millisecond {
				t.Fatalf("hook response waited %s for the summarizer", d)
			}
			srv.titles.inflight.Wait()
			if d := time.Since(start); d > 5*time.Second {
				t.Fatalf("summarizer took %s past its timeout", d)
			}
			if got := paneRecord(t, store, paneID).Text("title"); got != want {
				t.Fatalf("title = %q, want fallback %q", got, want)
			}
			if n := runCount(t, runs); n != 1 {
				t.Fatalf("summarizer ran %d times, want 1", n)
			}
		})
	}
}

func TestPaneTitleConcurrentPromptsRunOneSummarizer(t *testing.T) {
	command, runs := fakeSummarizer(t, `sleep 0.2; echo "Fix flaky queue dispatch test"`)
	t.Setenv(EnvTitleSummarizerCmd, command)
	srv, client, store := titleServer(t)
	paneID := newTitlePane(t, store, "agy", "agy")

	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ev := HookEvent{PaneID: paneID, Kind: HookKindState, State: StateRunning, Prompt: titlePrompt}
			if _, err := client.HookEvent(context.Background(), ev); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	srv.titles.inflight.Wait()
	if n := runCount(t, runs); n != 1 {
		t.Fatalf("summarizer ran %d times, want 1", n)
	}
	if got := paneRecord(t, store, paneID).Text("title"); got != "Fix flaky queue dispatch test" {
		t.Fatalf("title = %q", got)
	}
}

func TestPaneTitleSkipsWithoutSpendingTheOneShot(t *testing.T) {
	srv, client, store := titleServer(t)
	paneID := newTitlePane(t, store, "bash", "shell")

	// Not worth a title, a subagent's prompt, and a pane with no record.
	for _, prompt := range []string{"", "y", "continue", "/clear", "  /compact now please  "} {
		sendPrompt(t, srv, client, paneID, prompt)
	}
	sub := HookEvent{PaneID: paneID, Kind: HookKindSubagent, SubagentID: "s1", Prompt: titlePrompt}
	if _, err := client.HookEvent(context.Background(), sub); err != nil {
		t.Fatal(err)
	}
	sendPrompt(t, srv, client, "no-such-pane", titlePrompt)
	if got := paneRecord(t, store, paneID).Text("title"); got != "bash" {
		t.Fatalf("title = %q", got)
	}

	// The first real prompt still gets the one title, from the fallback.
	sendPrompt(t, srv, client, paneID, titlePrompt)
	if got := paneRecord(t, store, paneID).Text("title"); got != SummarizePromptTitle(titlePrompt, PaneTitleMaxChars) {
		t.Fatalf("title = %q", got)
	}
}

func TestPaneTitleOptOut(t *testing.T) {
	for value, wantTitled := range map[string]bool{"1": false, "true": false, "0": true, "": true} {
		t.Run("disable="+value, func(t *testing.T) {
			t.Setenv(EnvDisableTitleSummarizer, value)
			srv, client, store := titleServer(t)
			paneID := newTitlePane(t, store, "claude", "claude")
			sendPrompt(t, srv, client, paneID, titlePrompt)
			if titled := paneRecord(t, store, paneID).Text("title") != "claude"; titled != wantTitled {
				t.Fatalf("titled = %v, want %v", titled, wantTitled)
			}
		})
	}
}

func TestIsPlaceholderPaneTitle(t *testing.T) {
	placeholders := []string{"", "  ", "claude", "Codex", "agy", "zsh", "bash", "pane", strings.Repeat("raw prompt text ", 5)}
	for _, title := range placeholders {
		if !isPlaceholderPaneTitle(title, "") {
			t.Errorf("%q should be a placeholder", title)
		}
	}
	if !isPlaceholderPaneTitle("claude-fable", "claude-fable") {
		t.Error("the pane's own harness name should be a placeholder")
	}
	for _, title := range []string{"D2 (Fable 5.2): Features Props", "claude: W11F lane", "scratch", "Fix flaky queue dispatch test"} {
		if isPlaceholderPaneTitle(title, "claude") {
			t.Errorf("%q is a chosen title", title)
		}
	}
}

func TestSummarizePromptTitle(t *testing.T) {
	cases := []struct{ raw, want string }{
		{titlePrompt, "Fix the flaky queue dispatch test in the daemon"},
		{"please   add\ta dark theme\n\nwith tokens from themes.json", "Add a dark theme"},
		{"Ok so, could you refactor data/store.go into smaller files. Keep the tests green.", "Refactor data/store.go into smaller files"},
		{"\n\n```\n## Goal: ship the pane title summarizer\n", "Goal: ship the pane title summarizer"},
		{"source maps are broken in the web build", "Source maps are broken in the web build"},
		{"Bump to v1.54.0 and tag it", "Bump to v1.54.0 and tag it"},
		{"please", "Please"},
		{"   \n\t ", ""},
		{strings.Repeat("x", 80), "X" + strings.Repeat("x", 51)},
		{"Résumé parsing für die Köln office échoue après la mise à jour de la bibliothèque", "Résumé parsing für die Köln office échoue après la"},
	}
	for _, c := range cases {
		got := SummarizePromptTitle(c.raw, PaneTitleMaxChars)
		if got != c.want {
			t.Errorf("SummarizePromptTitle(%q) = %q, want %q", c.raw, got, c.want)
		}
		if n := utf8.RuneCountInString(got); n > PaneTitleMaxChars {
			t.Errorf("%q is %d chars", got, n)
		}
		if got != SummarizePromptTitle(c.raw, PaneTitleMaxChars) {
			t.Errorf("not deterministic for %q", c.raw)
		}
	}
	if got := SummarizePromptTitle(titlePrompt, 10); got != "Fix" {
		t.Errorf("maxChars 10 = %q", got)
	}
	if got := SummarizePromptTitle(titlePrompt, 0); got != "" {
		t.Errorf("maxChars 0 = %q", got)
	}
}

func TestCleanSummarizerTitle(t *testing.T) {
	cases := map[string]string{
		"\n  \"Fix queue dispatch\"\nmore\n":                                "Fix queue dispatch",
		"**Title:** tidy   the\tdaemon":                                     "Title:** tidy the daemon",
		"a title that goes on and on well past the fifty-two character cap": "a title that goes on and on well past the fifty-two",
		"\x1b[1mBold\x1b[0m":                                                "[1mBold [0m",
		"":                                                                  "",
	}
	for out, want := range cases {
		if got := cleanSummarizerTitle(out, PaneTitleMaxChars); got != want {
			t.Errorf("cleanSummarizerTitle(%q) = %q, want %q", out, got, want)
		}
	}
}
