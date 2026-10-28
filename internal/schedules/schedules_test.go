package schedules

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unixsuperhero/matomeru/store"
)

// clock is a fake clock the tests move by hand.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// fake is a fake command: it counts runs, can block until released, and
// answers with out and err.
type fake struct {
	mu      sync.Mutex
	runs    int32
	argv    [][]string
	env     [][]string
	block   chan struct{}
	out     string
	err     error
	waitCtx bool // block until the context ends (a hung command)
}

func (f *fake) exec(ctx context.Context, argv []string, env []string) ([]byte, error) {
	atomic.AddInt32(&f.runs, 1)
	f.mu.Lock()
	f.argv, f.env = append(f.argv, argv), append(f.env, env)
	block, out, err, wait := f.block, f.out, f.err, f.waitCtx
	f.mu.Unlock()
	if wait {
		<-ctx.Done()
		return []byte("partial"), errors.New("signal: killed")
	}
	if block != nil {
		<-block
	}
	return []byte(out), err
}

func (f *fake) count() int { return int(atomic.LoadInt32(&f.runs)) }

func newRunner(t *testing.T, db *store.Store, c *clock, f *fake, owner string) *Runner {
	t.Helper()
	r, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	r.Now, r.Exec, r.Owner = c.now, f.exec, owner
	r.LookPath = func(name string) (string, error) {
		if name == "absent" {
			return "", errors.New("not found")
		}
		return "/bin/" + name, nil
	}
	return r
}

func openDB(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

func def(name string) Def {
	return Def{Name: name, Command: []string{"tool", "signals"}, Interval: Duration(5 * time.Minute), Enabled: On}
}

func start() *clock { return &clock{t: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)} }

// A schedule runs at once when it never ran, then again only after its
// interval; an off schedule never runs from Tick.
func TestTickRunsOnTheInterval(t *testing.T) {
	db, _ := openDB(t)
	c, f := start(), &fake{out: "2 signals"}
	r := newRunner(t, db, c, f, "a")
	defs := []Def{def("gh"), {Name: "off", Command: []string{"x"}, Interval: Duration(time.Minute), Enabled: Off}}
	if got := r.Tick(context.Background(), defs); len(got) != 1 || got[0] != "gh" {
		t.Fatalf("first tick started %v", got)
	}
	r.Wait()
	c.add(4 * time.Minute)
	if got := r.Tick(context.Background(), defs); len(got) != 0 {
		t.Fatalf("early tick started %v", got)
	}
	c.add(time.Minute)
	if got := r.Tick(context.Background(), defs); len(got) != 1 {
		t.Fatalf("due tick started %v", got)
	}
	r.Wait()
	if f.count() != 2 {
		t.Fatalf("runs %d", f.count())
	}
	runs, err := r.History("gh", 0)
	if err != nil || len(runs) != 2 || runs[0].Outcome != OutcomeOK || runs[0].Output != "2 signals" || runs[0].Started <= runs[1].Started {
		t.Fatalf("history %+v %v", runs, err)
	}
	states, _ := r.States()
	if s := states["gh"]; s.LastOutcome != OutcomeOK || s.RunningBy != "" || s.Next != s.Last+(5*time.Minute).Milliseconds() {
		t.Fatalf("state %+v", s)
	}
	if rec := <-r.Done; rec.ScheduleID != "gh" {
		t.Fatalf("done %+v", rec)
	}
}

// A run still going is never started again: not by the next tick, not by
// run-now (ErrRunning).
func TestNoOverlap(t *testing.T) {
	db, _ := openDB(t)
	c, f := start(), &fake{block: make(chan struct{})}
	r := newRunner(t, db, c, f, "a")
	d := def("gh")
	d.Interval = Duration(time.Minute)
	r.Tick(context.Background(), []Def{d})
	waitFor(t, func() bool { return f.count() == 1 })
	c.add(10 * time.Minute)
	if got := r.Tick(context.Background(), []Def{d}); len(got) != 0 {
		t.Fatalf("overlapping tick started %v", got)
	}
	if _, err := r.RunNow(context.Background(), d); !errors.Is(err, ErrRunning) {
		t.Fatalf("run-now while running: %v", err)
	}
	close(f.block)
	r.Wait()
	if f.count() != 1 {
		t.Fatalf("runs %d", f.count())
	}
}

// Two runners on one database file (two TUIs) run each occurrence once:
// the claim is a write transaction, and a live claim from the other
// process blocks a run.
func TestTwoProcessesRunEachOccurrenceOnce(t *testing.T) {
	db1, path := openDB(t)
	db2, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	c, f := start(), &fake{block: make(chan struct{})}
	a, b := newRunner(t, db1, c, f, "a"), newRunner(t, db2, c, f, "b")
	d := def("gh")
	var wg sync.WaitGroup
	var started int32
	for _, r := range []*Runner{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			atomic.AddInt32(&started, int32(len(r.Tick(context.Background(), []Def{d}))))
		}()
	}
	wg.Wait()
	if started != 1 {
		t.Fatalf("started %d runs, want 1", started)
	}
	waitFor(t, func() bool { return f.count() == 1 })
	// The other process's run-now sees the live claim.
	other := b
	if s, _ := a.States(); s["gh"].RunningBy == "b" {
		other = a
	}
	if _, err := other.RunNow(context.Background(), d); !errors.Is(err, ErrRunning) {
		t.Fatalf("run-now across processes: %v", err)
	}
	close(f.block)
	a.Wait()
	b.Wait()
	if f.count() != 1 {
		t.Fatalf("runs %d", f.count())
	}
}

// A claim from a process that died lapses after the timeout and a grace.
func TestStaleClaimLapses(t *testing.T) {
	db, _ := openDB(t)
	c, f := start(), &fake{}
	r := newRunner(t, db, c, f, "a")
	d := def("gh")
	if err := db.Tx([]string{Table.Name}, func(tx *store.Tx) error {
		return tx.Put(Table.Name, "gh", State{ID: "gh", RunningBy: "dead", RunningUntil: c.now().Add(DefaultTimeout).UnixMilli()})
	}); err != nil {
		t.Fatal(err)
	}
	if got := r.Tick(context.Background(), []Def{d}); len(got) != 0 {
		t.Fatalf("ran over a live claim: %v", got)
	}
	c.add(DefaultTimeout + time.Second)
	if got := r.Tick(context.Background(), []Def{d}); len(got) != 1 {
		t.Fatalf("stale claim kept it: %v", got)
	}
	r.Wait()
}

// A run past its timeout is killed and recorded as timeout, with the
// output it wrote.
func TestTimeout(t *testing.T) {
	db, _ := openDB(t)
	c, f := start(), &fake{waitCtx: true}
	r := newRunner(t, db, c, f, "a")
	d := def("gh")
	d.Timeout = Duration(20 * time.Millisecond)
	rec, err := r.RunNow(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Outcome != OutcomeTimeout || !strings.Contains(rec.Error, "timed out after 20ms") || rec.Output != "partial" || !rec.Manual {
		t.Fatalf("record %+v", rec)
	}
	states, _ := r.States()
	if s := states["gh"]; s.LastOutcome != OutcomeTimeout || s.Failures != 1 || s.RunningBy != "" {
		t.Fatalf("state %+v", s)
	}
}

// Failures back off: each one doubles the wait (at most an hour for a
// short interval), and an ok run resets it. A command that is not
// installed is recorded as missing and backs off the same way.
func TestBackoff(t *testing.T) {
	db, _ := openDB(t)
	c, f := start(), &fake{err: errors.New("exit status 1"), out: "line one\ngh: not logged in"}
	r := newRunner(t, db, c, f, "a")
	d := def("gh")
	tick := func() int { n := len(r.Tick(context.Background(), []Def{d})); r.Wait(); return n }
	tick()
	states, _ := r.States()
	if s := states["gh"]; s.Failures != 1 || s.LastError != "exit status 1: gh: not logged in" {
		t.Fatalf("after one failure %+v", s)
	}
	c.add(5 * time.Minute)
	if tick() != 0 {
		t.Fatal("ran at the plain interval after a failure")
	}
	c.add(5 * time.Minute) // 10m: interval << 1
	if tick() != 1 {
		t.Fatal("backed-off run did not start")
	}
	for _, want := range []time.Duration{20 * time.Minute, 40 * time.Minute, time.Hour, time.Hour} {
		c.add(want - time.Second)
		if tick() != 0 {
			t.Fatalf("ran before %s", want)
		}
		c.add(time.Second)
		if tick() != 1 {
			t.Fatalf("did not run at %s", want)
		}
	}
	f.mu.Lock()
	f.err = nil
	f.mu.Unlock()
	c.add(time.Hour)
	tick()
	c.add(5 * time.Minute)
	if tick() != 1 {
		t.Fatal("an ok run did not reset the backoff")
	}

	missing := Def{Name: "slack", Command: []string{"absent", "signals"}, Interval: Duration(5 * time.Minute), Enabled: On}
	rec, err := r.RunNow(context.Background(), missing)
	if err != nil || rec.Outcome != OutcomeMissing || rec.Error != "absent not on PATH" {
		t.Fatalf("missing: %+v %v", rec, err)
	}
	states, _ = r.States()
	if s := states["slack"]; s.Failures != 1 || s.LastOutcome != OutcomeMissing {
		t.Fatalf("missing state %+v", s)
	}
}

func TestDelay(t *testing.T) {
	for _, tc := range []struct {
		interval time.Duration
		failures int
		want     time.Duration
	}{
		{5 * time.Minute, 0, 5 * time.Minute},
		{5 * time.Minute, 1, 10 * time.Minute},
		{5 * time.Minute, 3, 40 * time.Minute},
		{5 * time.Minute, 9, time.Hour},
		{2 * time.Hour, 3, 2 * time.Hour},
	} {
		if got := Delay(tc.interval, tc.failures); got != tc.want {
			t.Errorf("Delay(%s, %d) = %s, want %s", tc.interval, tc.failures, got, tc.want)
		}
	}
}

// The history keeps Keep runs per schedule, newest first.
func TestHistoryIsShort(t *testing.T) {
	db, _ := openDB(t)
	c, f := start(), &fake{}
	r := newRunner(t, db, c, f, "a")
	r.Keep = 3
	for range 5 {
		if _, err := r.RunNow(context.Background(), def("gh")); err != nil {
			t.Fatal(err)
		}
		c.add(time.Second)
	}
	if _, err := r.RunNow(context.Background(), def("other")); err != nil {
		t.Fatal(err)
	}
	runs, _ := r.History("gh", 0)
	if len(runs) != 3 || runs[0].Started != c.now().Add(-time.Second).UnixMilli() {
		t.Fatalf("history %+v", runs)
	}
	if other, _ := r.History("other", 0); len(other) != 1 {
		t.Fatalf("other's history %d", len(other))
	}
}

// Auto runs a schedule only where its command is installed and every
// check exits 0; the answer is cached.
func TestAuto(t *testing.T) {
	db, _ := openDB(t)
	c, f := start(), &fake{}
	r := newRunner(t, db, c, f, "a")
	d := Def{Name: "gh", Command: []string{"tool"}, Interval: Duration(5 * time.Minute), Enabled: Auto, Requires: [][]string{{"gh", "auth", "status", "--active"}}}
	if on, why := r.Active(context.Background(), d); !on || why != "auto: checks pass" {
		t.Fatalf("auto with passing checks: %v %q", on, why)
	}
	if f.count() != 1 || strings.Join(f.argv[0], " ") != "gh auth status --active" {
		t.Fatalf("checks %v", f.argv)
	}
	r.Active(context.Background(), d)
	if f.count() != 1 {
		t.Fatal("the check was not cached")
	}
	c.add(probeEvery)
	f.mu.Lock()
	f.err = errors.New("exit status 1")
	f.mu.Unlock()
	if on, why := r.Active(context.Background(), d); on || why != "auto: gh auth status --active failed" {
		t.Fatalf("auto with a failing check: %v %q", on, why)
	}
	d.Command = []string{"absent"}
	r.probes = map[string]probe{}
	if on, why := r.Active(context.Background(), d); on || why != "auto: absent not on PATH" {
		t.Fatalf("auto without the command: %v %q", on, why)
	}
	if on, why := r.Active(context.Background(), Def{Name: "x", Command: []string{"x"}, Enabled: Off}); on || why != Off {
		t.Fatalf("off: %v %q", on, why)
	}
}

// Placeholders expand in the command and the env given to it.
func TestExpand(t *testing.T) {
	db, _ := openDB(t)
	c, f := start(), &fake{}
	r := newRunner(t, db, c, f, "a")
	r.Expand = func(s string) string { return strings.ReplaceAll(s, "{mtm}", "/usr/bin/mtm") }
	d := def("gh")
	d.Command = []string{"tool", "{mtm}"}
	d.Env = map[string]string{"O_GH_MTM": "{mtm}", "A": "b"}
	if _, err := r.RunNow(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.argv[0], " "); got != "tool /usr/bin/mtm" {
		t.Fatalf("argv %q", got)
	}
	if got := strings.Join(f.env[0], " "); got != "A=b O_GH_MTM=/usr/bin/mtm" {
		t.Fatalf("env %q", got)
	}
}

// The file merges over the defaults by name: a default's row changes only
// what it gives; a new row is added, on unless it says otherwise; a bad
// row names itself.
func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if defs, err := Load(filepath.Join(dir, "none.json")); err != nil || len(defs) != 2 {
		t.Fatalf("missing file: %v %v", defs, err)
	}
	path := filepath.Join(dir, "schedules.json")
	write := func(s string) {
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`[{"name": "gh-signals", "interval": "10m"}, {"name": "todo-sync", "command": ["o-todo", "sync"], "interval": "1h"}]`)
	defs, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 3 || defs[0].Every() != 10*time.Minute || defs[0].Enabled != Auto || defs[0].Command[0] != "o-gh" {
		t.Fatalf("merged gh-signals %+v", defs[0])
	}
	if defs[2].Name != "todo-sync" || defs[2].Enabled != On || defs[2].Every() != time.Hour {
		t.Fatalf("added %+v", defs[2])
	}
	for _, bad := range []string{`{}`, `[{"name": "x y"}]`, `[{"name": "n", "command": ["a"], "interval": "10s"}]`, `[{"name": "n", "command": ["a"], "interval": "5m", "enabled": "maybe"}]`, `[{"name": "n", "interval": "5m"}]`, `[{"name": "n", "command": ["a"], "interval": "5m", "bogus": 1}]`} {
		write(bad)
		if _, err := Load(path); err == nil {
			t.Errorf("%s loaded", bad)
		}
	}
	if d := Defaults(); d[0].Enabled != Auto || d[1].Enabled != Off {
		t.Fatalf("defaults %+v", d)
	}
}

// The tables are matomeru-old's: a database that declared them opens.
func TestTablesAreTheOldDeclarations(t *testing.T) {
	db, _ := openDB(t)
	for _, tb := range []store.Table{{Name: "schedules", Owner: "schedules", Key: "id", Version: 1}, {Name: "schedule_runs", Owner: "schedules", Key: "id", Version: 1}} {
		if err := db.Declare(tb); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := New(db); err != nil {
		t.Fatal(err)
	}
}

// RealExec runs a real command, killed with its group on timeout.
func TestRealExec(t *testing.T) {
	out, err := RealExec(context.Background(), []string{"sh", "-c", "echo hi; echo err >&2"}, []string{"X=1"})
	if err != nil || !strings.Contains(string(out), "hi") || !strings.Contains(string(out), "err") {
		t.Fatalf("%q %v", out, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	began := time.Now()
	if _, err := RealExec(ctx, []string{"sh", "-c", "sleep 30 & sleep 30"}, nil); err == nil {
		t.Fatal("a killed command succeeded")
	}
	if time.Since(began) > 5*time.Second {
		t.Fatalf("kill took %s", time.Since(began))
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting")
		}
		time.Sleep(time.Millisecond)
	}
}

// The built-in gh-signals checks only the active gh account: plain
// `gh auth status` fails when any configured account is broken, even an
// inactive one.
func TestDefaultGhCheckIsActiveAccount(t *testing.T) {
	for _, d := range Defaults() {
		if d.Name != "gh-signals" {
			continue
		}
		if len(d.Requires) != 1 || strings.Join(d.Requires[0], " ") != "gh auth status --active" {
			t.Fatalf("gh-signals requires %v", d.Requires)
		}
		return
	}
	t.Fatal("no gh-signals default")
}
