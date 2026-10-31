package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/matomeru/internal/harness"
	"ofam.dev/matomeru/internal/locate"
)

// procTable is a fake process table for the locator.
type procTable struct {
	procs map[int]locate.Proc
	env   map[int]map[string]string
}

func (f *procTable) Procs(pids []int, ttys []string) map[int]locate.Proc {
	out := map[int]locate.Proc{}
	for pid, p := range f.procs {
		out[pid] = p
	}
	return out
}

func (f *procTable) Env(pids []int, _ []string) map[int]map[string]string {
	out := map[int]map[string]string{}
	for _, pid := range pids {
		out[pid] = f.env[pid]
	}
	return out
}

const procStart = "Wed Oct 28 08:01:05 2026"

// claudeRecords points the fake daemon's listing at a locator whose
// claude source reads records from a temp sessions folder and transcripts
// from app.claudeProjects.
func claudeRecords(t *testing.T, app *App, f *fakePanes) (string, *procTable) {
	t.Helper()
	sessions := t.TempDir()
	h, _ := app.harnesses.Get("claude")
	src := *h.Session
	src.Files = filepath.Join(sessions, "*.json")
	src.Transcript = filepath.Join(app.claudeProjects, "{cwd_slug}", "{session}.jsonl")
	h.Session = &src
	sys := &procTable{procs: map[int]locate.Proc{}, env: map[int]map[string]string{}}
	f.locate = locate.New([]harness.Harness{h}, sys, func(string) string { return "" }, t.TempDir())
	return sessions, sys
}

// writeRecord writes a running claude's sessions file: pid, the session it
// writes now, its folder, and the window its environment names.
func writeRecord(t *testing.T, dir string, sys *procTable, pid int, session, cwd, window string) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"pid": pid, "sessionId": session, "cwd": cwd, "kind": "bg", "procStart": procStart})
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(pid)+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	sys.procs[pid] = locate.Proc{Start: procStart}
	sys.env[pid] = map[string]string{"TUIOS_WINDOW_ID": window}
}

// tuios reports a stale id; the pane's claude process's record names the
// session it writes. The Turns tab reads that one, and when the process
// moves to a new session (a /clear) the next listing follows it, no R.
func TestTurnsFollowThePanesLiveSessionRecord(t *testing.T) {
	t.Parallel()
	app, f := staleClaudeApp(t, time.Time{}) // no start time: the old fallback cannot help
	dir, sys := claudeRecords(t, app, f)
	writeTranscript(t, app, "first")
	writeRecord(t, dir, sys, 10, "first", "/w/proj", "w1")
	if fr := turnsText(t, app); !strings.Contains(fr, "prompt of first") {
		t.Fatalf("the live session's transcript is not shown:\n%s", fr)
	}
	if p := app.panes[0]; p.AgentSession != "first" || p.ReportedSession != "gone" {
		t.Fatalf("pane session %q (reported %q)", p.AgentSession, p.ReportedSession)
	}
	writeTranscript(t, app, "second")
	writeRecord(t, dir, sys, 10, "second", "/w/proj", "w1")
	reload(t, app)
	fr := frame(app)
	if !strings.Contains(fr, "prompt of second") || !strings.Contains(fr, "prompt of first") {
		t.Fatalf("the tab did not follow the new session (chained after the first):\n%s", fr)
	}
}

// A pane whose claude only attaches to a session the background daemon
// runs: its own record has no transcript, and the folder's one running
// session no pane holds is taken. Two of them are a notice, not a guess.
func TestTurnsOfAnAttachingClaudePane(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		bgs  []string
	}{{"one background session", []string{"bg1"}}, {"two background sessions", []string{"bg1", "bg2"}}} {
		t.Run(tc.name, func(t *testing.T) {
			app, f := staleClaudeApp(t, time.Time{})
			dir, sys := claudeRecords(t, app, f)
			writeRecord(t, dir, sys, 10, "client", "/w/proj", "w1")
			for i, s := range tc.bgs {
				writeTranscript(t, app, s)
				writeRecord(t, dir, sys, 20+i, s, "/w/proj", "a-window-tuios-no-longer-lists")
			}
			fr := turnsText(t, app)
			if len(tc.bgs) == 1 {
				if !strings.Contains(fr, "prompt of bg1") {
					t.Fatalf("the background session is not shown:\n%s", fr)
				}
				return
			}
			for _, s := range tc.bgs {
				if strings.Contains(fr, "prompt of "+s) {
					t.Fatalf("guessed %s:\n%s", s, fr)
				}
			}
			if !strings.HasPrefix(app.m.Info, "turns: 2 live sessions in /w/proj could be this pane's") {
				t.Fatalf("notice %q", app.m.Info)
			}
		})
	}
}

// A pane tuios reports no harness for takes the harness of the record it
// holds; with no locator the listing stands.
func TestLocatePanesTakesTheRecordsHarness(t *testing.T) {
	t.Parallel()
	app, f := staleClaudeApp(t, time.Time{})
	dir, sys := claudeRecords(t, app, f)
	writeTranscript(t, app, "live")
	writeRecord(t, dir, sys, 10, "live", "/w/proj", "w9")
	panes := []pane{{Pane: daemon.Pane{ID: "w9", Cwd: "/w/proj"}}}
	if got := locatePanes(panes, nil); got[0].Harness != "" || got[0].AgentSession != "" {
		t.Fatalf("no locator changed the pane: %+v", got[0])
	}
	got := locatePanes(panes, f.locate)
	if got[0].Harness != "claude" || got[0].AgentSession != "live" || !isClaude(got[0]) {
		t.Fatalf("pane %+v", got[0])
	}
}
