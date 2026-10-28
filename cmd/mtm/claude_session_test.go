package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unixsuperhero/matomeru/internal/turns"
)

// staleClaudeApp is the panes app with w1 a Claude Code pane in /w/proj
// whose reported session ("gone") has no transcript, as after a resume;
// files are the transcripts in its folder, by session, each with one
// prompt naming its session.
func staleClaudeApp(t *testing.T, started time.Time, files ...string) (*App, *fakePanes) {
	t.Helper()
	app, f := panesApp(t)
	f.panes[0].Harness, f.panes[0].AgentState, f.panes[0].AgentSession, f.panes[0].Cwd = "claude-code", "idle", "gone", "/w/proj"
	app.agentStarted = func(pane) time.Time { return started }
	dir := filepath.Join(app.claudeProjects, turns.ClaudeProjectDir("/w/proj"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, s := range files {
		writeLog(t, filepath.Join(dir, s+".jsonl"), logPrompt(t, "u-"+s, "2026-10-28T10:00:00Z", "prompt of "+s))
	}
	return app, f
}

func turnsText(t *testing.T, app *App) string {
	t.Helper()
	reload(t, app)
	settle(app, key(t, app, "[")) // Screen wraps back to Turns
	return frame(app)
}

// A stale session id: the Turns tab reads the one transcript written in
// the pane's folder since its agent started.
func TestTurnsFollowAStaleSessionToItsNewTranscript(t *testing.T) {
	app, _ := staleClaudeApp(t, time.Now().Add(-time.Hour), "resumed")
	if fr := turnsText(t, app); !strings.Contains(fr, "prompt of resumed") {
		t.Fatalf("the resumed transcript is not shown:\n%s", fr)
	}
}

// Unsure is nothing: no start time, a transcript older than the agent,
// another pane's transcript, or two candidates.
func TestTurnsNeverGuessAStaleSession(t *testing.T) {
	for _, tc := range []struct {
		name    string
		started time.Time
		files   []string
		other   string // w2 becomes a Claude pane in the same folder on this session
	}{
		{"start unknown", time.Time{}, []string{"resumed"}, ""},
		{"written before the agent started", time.Now().Add(time.Minute), []string{"resumed"}, ""},
		{"another pane's transcript", time.Now().Add(-time.Hour), []string{"theirs"}, "theirs"},
		{"another pane in the folder is stale too", time.Now().Add(-time.Hour), []string{"resumed"}, "also-gone"},
		{"two candidates", time.Now().Add(-time.Hour), []string{"one", "two"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, f := staleClaudeApp(t, tc.started, tc.files...)
			if tc.other != "" {
				f.panes[1].Harness, f.panes[1].AgentState, f.panes[1].AgentSession, f.panes[1].Cwd = "claude-code", "idle", tc.other, "/w/proj"
			}
			fr := turnsText(t, app)
			for _, s := range tc.files {
				if strings.Contains(fr, "prompt of "+s) {
					t.Fatalf("guessed %s:\n%s", s, fr)
				}
			}
		})
	}
}

// The side's folder follows the same rule: a stale id's new transcript
// names where the agent is.
func TestSideFollowsAStaleSessionsTranscript(t *testing.T) {
	started, agent := t.TempDir(), t.TempDir()
	a, parent, rec := sideContextApp(t, 4, started)
	ownSideParent(t, a, parent, t.TempDir())
	a.panesd.(*fakePanes).panes[0].Harness = "claude-code"
	a.panesd.(*fakePanes).panes[0].AgentSession = "gone"
	a.claudeProjects = t.TempDir()
	a.agentStarted = func(pane) time.Time { return time.Now().Add(-time.Hour) }
	dir := filepath.Join(a.claudeProjects, turns.ClaudeProjectDir(started))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","uuid":"u1","sessionId":"resumed","cwd":"` + agent + `","message":{"role":"user","content":"hi"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "resumed.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	openSide(t, a, parent, "term")
	if len(rec.windows) != 1 || rec.windows[0].Cwd != agent {
		t.Fatalf("windows %+v, want cwd %s (from the resumed transcript)", rec.windows, agent)
	}
}

// The agent's start is its claude process's, from ps: the pane's own
// process, else its one outermost claude descendant.
func TestAgentStartFromThePsTable(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	table := `
  100     1 1-02:03:04 /bin/zsh
  101   100    05:00 claude
  102   101    04:00 claude
  103   101    03:00 node
  200     1    10:00 /bin/zsh
  201   200    09:00 /usr/local/bin/claude
  202   200    08:00 claude
  300     1    01:02:03 claude
  400     1    00:10 /bin/zsh
`
	for _, tc := range []struct {
		root int
		want time.Duration // ago; 0 is none
	}{
		{100, 5 * time.Minute},
		{200, 0}, // two agents: unsure
		{300, time.Hour + 2*time.Minute + 3*time.Second},
		{400, 0},
		{999, 0},
	} {
		got := agentStartFrom(table, tc.root, now)
		if tc.want == 0 {
			if !got.IsZero() {
				t.Fatalf("root %d: %v, want none", tc.root, got)
			}
			continue
		}
		if !got.Equal(now.Add(-tc.want)) {
			t.Fatalf("root %d: %v, want %v ago", tc.root, now.Sub(got), tc.want)
		}
	}
	if d, ok := parseEtime("1-02:03:04"); !ok || d != 26*time.Hour+3*time.Minute+4*time.Second {
		t.Fatalf("etime %v %v", d, ok)
	}
	for _, bad := range []string{"", "5", "a:b", "1-x"} {
		if _, ok := parseEtime(bad); ok {
			t.Fatalf("etime %q read", bad)
		}
	}
}
