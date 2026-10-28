package turns

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The id tuios reports wins when its transcript exists; a stale one
// (resume, restart) falls back to the one new transcript in the pane's
// folder, written after the agent started; anything unsure is no log.
func TestResolveClaudeLog(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	started := now.Add(-time.Hour)
	setup := func(t *testing.T, files map[string]time.Time) string {
		t.Helper()
		projects := filepath.Join(t.TempDir(), "projects")
		dir := filepath.Join(projects, ClaudeProjectDir("/w/proj"))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, mod := range files {
			p := filepath.Join(dir, name+".jsonl")
			if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(p, mod, mod); err != nil {
				t.Fatal(err)
			}
		}
		return projects
	}
	at := func() time.Time { return started }
	for _, tc := range []struct {
		name      string
		files     map[string]time.Time
		session   string
		started   func() time.Time
		claimed   []string
		contested bool
		want      string
	}{
		{"exact id wins over a newer file", map[string]time.Time{"old": now.Add(-5 * time.Hour), "new": now.Add(-time.Minute)}, "old", at, nil, false, "old"},
		{"stale id: the one new file", map[string]time.Time{"before": now.Add(-2 * time.Hour), "new": now.Add(-time.Minute)}, "gone", at, nil, false, "new"},
		{"stale id, start unknown", map[string]time.Time{"new": now.Add(-time.Minute)}, "gone", nil, nil, false, ""},
		{"stale id, start zero", map[string]time.Time{"new": now.Add(-time.Minute)}, "gone", func() time.Time { return time.Time{} }, nil, false, ""},
		{"stale id, written before the start", map[string]time.Time{"before": now.Add(-2 * time.Hour)}, "gone", at, nil, false, ""},
		{"stale id, not recent", map[string]time.Time{"new": now.Add(-7 * time.Hour)}, "gone", func() time.Time { return now.Add(-8 * time.Hour) }, nil, false, ""},
		{"stale id, another pane's file", map[string]time.Time{"theirs": now.Add(-time.Minute)}, "gone", at, []string{"theirs"}, false, ""},
		{"stale id, another pane's file left out", map[string]time.Time{"theirs": now.Add(-time.Minute), "mine": now.Add(-2 * time.Minute)}, "gone", at, []string{"theirs"}, false, "mine"},
		{"stale id, two candidates", map[string]time.Time{"a": now.Add(-time.Minute), "b": now.Add(-2 * time.Minute)}, "gone", at, nil, false, ""},
		{"stale id, contested folder", map[string]time.Time{"new": now.Add(-time.Minute)}, "gone", at, nil, true, ""},
		{"no id", map[string]time.Time{"new": now.Add(-time.Minute)}, "", at, nil, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projects := setup(t, tc.files)
			path, session, err := ResolveClaudeLog(ClaudeLookup{
				Projects: projects, Cwd: "/w/proj", Session: tc.session,
				Started: tc.started, Claimed: tc.claimed, Contested: tc.contested, Now: now,
			})
			if tc.want == "" {
				if err != ErrNoLog {
					t.Fatalf("got %s %s %v, want no log", path, session, err)
				}
				return
			}
			if err != nil || session != tc.want || filepath.Base(path) != tc.want+".jsonl" {
				t.Fatalf("got %s %s %v, want %s", path, session, err, tc.want)
			}
		})
	}
}
