package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/turns"
)

// resumedID is a transcript's session, long enough for the notice to cut.
const resumedID = "7f96ea51-0c1d-4e2f-9a8b-1234567890ab"

// fakeClock is a settable time for the Turns tab's retries.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// writeTranscript writes session's transcript in the folder of w1's
// /w/proj, one prompt naming the session.
func writeTranscript(t *testing.T, app *App, session string) {
	t.Helper()
	dir := filepath.Join(app.claudeProjects, turns.ClaudeProjectDir("/w/proj"))
	writeLog(t, filepath.Join(dir, session+".jsonl"), logPrompt(t, "u-"+session, "2026-10-28T10:00:00Z", "prompt of "+session))
}

// onTheJournal is the stale pane w1 on the Turns tab with no transcript
// yet: the journal fallback.
func onTheJournal(t *testing.T, clock *fakeClock) *App {
	t.Helper()
	app, _ := staleClaudeApp(t, time.Now().Add(-time.Hour))
	if clock != nil {
		app.clock = clock.now
	}
	if fr := turnsText(t, app); strings.Contains(fr, "prompt of") {
		t.Fatalf("a transcript before any was written:\n%s", fr)
	}
	if pt := app.mturns["w1"]; pt == nil || !pt.fallback {
		t.Fatalf("w1 is not on the journal fallback: %+v", pt)
	}
	return app
}

func refreshTurns(t *testing.T, app *App, noun, target string) {
	t.Helper()
	if _, err := app.r.Execute(ochazuke.Request{Verb: "refresh-turns", Noun: noun, Target: target}); err != nil {
		t.Fatal(err)
	}
	settle(app, app.r.PendingCommands())
}

// R on the pane row while the Turns tab shows: a transcript written after
// the first read replaces the journal, and the status says which.
func TestRefreshTurnsSwitchesFromTheJournalToTheTranscript(t *testing.T) {
	app := onTheJournal(t, nil)
	writeTranscript(t, app, resumedID)
	settle(app, key(t, app, "R"))
	settle(app, app.r.PendingCommands())
	if fr := frame(app); !strings.Contains(fr, "prompt of "+resumedID) {
		t.Fatalf("the transcript is not shown after R:\n%s", fr)
	}
	if want := "turns: transcript 7f96ea51… (resumed session)"; app.m.Info != want {
		t.Fatalf("status %q, want %q", app.m.Info, want)
	}
	if pt := app.mturns["w1"]; pt.fallback || pt.cur.session != resumedID {
		t.Fatalf("after refresh: fallback %v, resolved %q", pt.fallback, pt.cur.session)
	}
}

// A refresh that still finds nothing says the journal stands.
func TestRefreshTurnsWithNoTranscriptSaysTheJournal(t *testing.T) {
	app := onTheJournal(t, nil)
	refreshTurns(t, app, "pane", app.pv.turnsFor)
	if want := "turns: no transcript, showing mtm's journal"; app.m.Info != want {
		t.Fatalf("status %q, want %q", app.m.Info, want)
	}
	if !app.mturns["w1"].fallback {
		t.Fatal("w1 left the journal with no transcript")
	}
}

// The journal fallback tries again after 10 s, 30 s, 1 min, then every
// 5 min, and switches to the transcript on the first try that finds it.
func TestTurnsRetryTheTranscriptOnABackoff(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	app := onTheJournal(t, clock)
	pt := app.mturns["w1"]
	start := clock.now()
	if !pt.cur.retryAt.Equal(start.Add(10 * time.Second)) {
		t.Fatalf("first retry at %v, want +10s", pt.cur.retryAt.Sub(start))
	}
	// Due: a miss moves the retry by the next wait.
	for _, want := range []time.Duration{30 * time.Second, time.Minute, 5 * time.Minute, 5 * time.Minute} {
		clock.t = pt.cur.retryAt
		reload(t, app)
		if got := pt.cur.retryAt.Sub(clock.now()); got != want {
			t.Fatalf("after a miss the next retry is in %v, want %v", got, want)
		}
	}
	// Not due yet: a new transcript waits for the retry.
	writeTranscript(t, app, resumedID)
	clock.add(time.Minute)
	reload(t, app)
	if fr := frame(app); strings.Contains(fr, "prompt of "+resumedID) {
		t.Fatalf("read before the retry was due:\n%s", fr)
	}
	clock.t = pt.cur.retryAt
	reload(t, app)
	if fr := frame(app); !strings.Contains(fr, "prompt of "+resumedID) {
		t.Fatalf("the retry did not pick up the transcript:\n%s", fr)
	}
	if pt.fallback || pt.cur.tries != 0 {
		t.Fatalf("after the find: fallback %v, tries %d", pt.fallback, pt.cur.tries)
	}
	if app.m.Status != "" && strings.HasPrefix(app.m.Status, "turns:") {
		t.Fatalf("an automatic retry put a notice up: %q", app.m.Status)
	}
}

func TestTurnsRetryWaits(t *testing.T) {
	for tries, want := range map[int]time.Duration{0: 10 * time.Second, 1: 10 * time.Second, 2: 30 * time.Second, 3: time.Minute, 4: 5 * time.Minute, 9: 5 * time.Minute} {
		if got := turnsRetryAfter(tries); got != want {
			t.Fatalf("after %d misses %v, want %v", tries, got, want)
		}
	}
}

// A refresh never takes the transcript of another pane: one the other
// pane's reported id names, or one recorded for it.
func TestRefreshTurnsNeverTakesAnotherPanesTranscript(t *testing.T) {
	for _, tc := range []struct {
		name     string
		reported bool // w2 reports the session; else it is only recorded for w2
	}{{"reported", true}, {"recorded", false}} {
		t.Run(tc.name, func(t *testing.T) {
			app, f := staleClaudeApp(t, time.Now().Add(-time.Hour))
			f.panes[1].Harness, f.panes[1].AgentState, f.panes[1].Cwd = "claude-code", "idle", "/w/proj"
			if tc.reported {
				f.panes[1].AgentSession = resumedID
			} else if _, err := app.work.AddPaneSession("w2", "claude-code", resumedID); err != nil {
				t.Fatal(err)
			}
			turnsText(t, app)
			writeTranscript(t, app, resumedID)
			refreshTurns(t, app, "pane", app.pv.turnsFor)
			if fr := frame(app); strings.Contains(fr, "prompt of "+resumedID) {
				t.Fatalf("took w2's transcript:\n%s", fr)
			}
			if want := "turns: no transcript, showing mtm's journal"; app.m.Info != want {
				t.Fatalf("status %q, want %q", app.m.Info, want)
			}
		})
	}
}

// The turn rows' R refreshes the pane the turns are of; an id tuios
// reports whose transcript exists is not marked resumed.
func TestRefreshTurnsFromATurnRow(t *testing.T) {
	app, f := staleClaudeApp(t, time.Now().Add(-time.Hour))
	useTurnStyle(t, app, turnsStyleTree)
	f.panes[0].AgentSession = resumedID
	writeTranscript(t, app, resumedID)
	if fr := turnsText(t, app); !strings.Contains(fr, "prompt of "+resumedID) {
		t.Fatalf("not shown:\n%s", fr)
	}
	turn := ""
	for id, in := range app.r.Instances {
		if in.Noun == "turn" {
			turn = id
		}
	}
	if turn == "" {
		t.Fatal("no turn row")
	}
	refreshTurns(t, app, "turn", turn)
	if want := "turns: transcript 7f96ea51…"; app.m.Info != want {
		t.Fatalf("status %q, want %q", app.m.Info, want)
	}
	if fr := frame(app); !strings.Contains(fr, "prompt of "+resumedID) {
		t.Fatalf("not shown after the refresh:\n%s", fr)
	}
}

func TestTurnsNotice(t *testing.T) {
	for _, tc := range []struct {
		got  claudeRead
		want string
	}{
		{claudeRead{NoLog: true}, "turns: no transcript, showing mtm's journal"},
		{claudeRead{Session: "gone"}, "turns: transcript gone"},
		{claudeRead{Session: resumedID}, "turns: transcript 7f96ea51… (resumed session)"},
		{claudeRead{Turns: []turns.Turn{{}}}, "turns: no transcript of session gone, showing earlier sessions"},
	} {
		if got := turnsNotice("gone", tc.got); got != tc.want {
			t.Fatalf("%+v: %q, want %q", tc.got, got, tc.want)
		}
	}
}

// The agent's start is looked up once per pane process and session; a
// new process, a new session or a refresh asks again, and "none found" is
// asked again after agentStartUnknownFor.
func TestAgentStartIsCachedPerPaneProcess(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	calls := 0
	result := clock.now().Add(-time.Hour)
	lookup := func(pane) time.Time { calls++; return result }
	c := &agentStarts{now: clock.now}
	p := pane{AgentSession: "s1"}
	p.ID, p.PID = "w1", 100
	for range 3 {
		c.started(p, lookup)
	}
	if calls != 1 {
		t.Fatalf("%d lookups for one process, want 1", calls)
	}
	p.PID = 101
	c.started(p, lookup)
	p.AgentSession = "s2"
	c.started(p, lookup)
	c.forget("w1")
	c.started(p, lookup)
	if calls != 4 {
		t.Fatalf("%d lookups, want 4 (new pid, new session, forget)", calls)
	}
	other := p
	other.ID = "w2"
	c.started(other, lookup)
	if calls != 5 {
		t.Fatalf("another pane shared w1's entry")
	}
	result = time.Time{}
	c.forget("w1")
	c.started(p, lookup)
	c.started(p, lookup)
	if calls != 6 {
		t.Fatalf("none found was not cached: %d", calls)
	}
	clock.add(agentStartUnknownFor)
	c.started(p, lookup)
	if calls != 7 {
		t.Fatalf("none found was not asked again after %v: %d", agentStartUnknownFor, calls)
	}
}

// The Turns tab's polls ask ps once for a pane, and a refresh asks again.
func TestTurnsPollsDoNotRunPsEachTime(t *testing.T) {
	clock := &fakeClock{t: time.Now()}
	app, _ := staleClaudeApp(t, time.Now().Add(-time.Hour))
	app.clock = clock.now
	app.starts.now = clock.now
	var mu sync.Mutex
	calls := 0
	app.agentStarted = func(pane) time.Time { mu.Lock(); calls++; mu.Unlock(); return time.Now().Add(-time.Hour) }
	// Two candidates: the fallback asks the start, then refuses to guess,
	// so every poll resolves again.
	writeTranscript(t, app, "one")
	writeTranscript(t, app, "two")
	turnsText(t, app)
	for range 4 {
		clock.add(5 * time.Minute)
		reload(t, app)
	}
	if calls != 1 {
		t.Fatalf("%d ps lookups over the polls, want 1", calls)
	}
	refreshTurns(t, app, "pane", app.pv.turnsFor)
	if calls != 2 {
		t.Fatalf("%d ps lookups after a refresh, want 2", calls)
	}
}

// A side opened after a refresh follows the tab's resolution, even where
// the fallback could not tell on its own (no start time).
func TestSideFollowsTheTurnsTabsResolution(t *testing.T) {
	app := open(t)
	projects, agent := t.TempDir(), t.TempDir()
	dir := filepath.Join(projects, turns.ClaudeProjectDir("/w/proj"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","uuid":"u1","sessionId":"` + resumedID + `","cwd":"` + agent + `","message":{"role":"user","content":"hi"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, resumedID+".jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	parent := pane{Harness: "claude-code", AgentSession: "gone"}
	parent.ID, parent.Cwd = "w1", "/w/proj"
	if got := agentCwd(context.Background(), app.work, projects, parent, nil, nil, ""); got != "" {
		t.Fatalf("with no resolution and no start time the side guessed %q", got)
	}
	r := resolutions{"w1": {reported: "gone", session: resumedID}}
	if got := agentCwd(context.Background(), app.work, projects, parent, nil, nil, r.of(parent)); got != agent {
		t.Fatalf("side cwd %q, want %q (the tab's resolution)", got, agent)
	}
	parent.AgentSession = "newer"
	if got := r.of(parent); got != "" {
		t.Fatalf("a resolution for another reported session was used: %q", got)
	}
}

// What a refresh found is a plain message, never the error line: the
// shell's commands and the journal fallback read before the hints, with
// no "Error:" (they were drawn as errors while posted through Status).
func TestRefreshTurnsNoticesAreInfoNotErrors(t *testing.T) {
	for _, c := range []struct {
		name string
		open func(t *testing.T) *App
		want string
	}{
		{"shell", func(t *testing.T) *App { app, _ := shellTurnsApp(t, 1); return app }, "turns: the shell's commands"},
		{"journal", func(t *testing.T) *App { return onTheJournal(t, nil) }, "turns: " + journalNotice},
	} {
		t.Run(c.name, func(t *testing.T) {
			app := c.open(t)
			app.m.Width, app.m.Height = 160, 40
			refreshTurns(t, app, "pane", app.pv.turnsFor)
			if app.m.Info != c.want || app.m.Status != "" {
				t.Fatalf("info %q status %q, want info %q", app.m.Info, app.m.Status, c.want)
			}
			if fr := frame(app); strings.Contains(fr, "Error:") || !strings.Contains(fr, c.want) {
				t.Fatalf("frame:\n%s", fr)
			}
		})
	}
}
