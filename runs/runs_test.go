package runs

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

var owner = core.Actor{Kind: "user", ID: "owner"}

func openStore(t *testing.T) *data.Store {
	t.Helper()
	s, err := data.Open(filepath.Join(t.TempDir(), "runs.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func newRunner(t *testing.T, opts Options) (*Runner, *data.Store) {
	t.Helper()
	s := openStore(t)
	r := New(s, opts)
	t.Cleanup(r.Close)
	return r, s
}

// finished starts spec, waits for it and re-reads the run from the store.
func finished(t *testing.T, r *Runner, spec StartSpec) *data.Record {
	t.Helper()
	rec, err := r.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if rec.Text("runStatus") != StatusRunning {
		t.Fatalf("started run state = %q, want running", rec.Text("runStatus"))
	}
	return wait(t, r, rec.ID)
}

func wait(t *testing.T, r *Runner, id string) *data.Record {
	t.Helper()
	select {
	case <-r.Done(id):
	case <-time.After(10 * time.Second):
		t.Fatalf("run %s did not finish", id)
	}
	rec, err := r.Store().Get(core.Ref{Kind: data.KindRun, ID: id})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return rec
}

func TestRunDone(t *testing.T) {
	r, s := newRunner(t, Options{})
	task, _, err := s.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "t"}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	rec := finished(t, r, StartSpec{Title: "greet", Command: "echo hi && exit 0", Dir: dir, TaskRef: task.Ref(), Actor: owner})
	if rec.Text("runStatus") != StatusDone || rec.Text("exitCode") != "0" || strings.TrimSpace(rec.Text("output")) != "hi" {
		t.Fatalf("run = %v", rec.Fields)
	}
	if ref, ok := rec.RefField("task"); !ok || ref != task.Ref() {
		t.Fatalf("task = %v", rec.Fields["task"])
	}
	if rec.Text("cwd") != dir || rec.Text("title") != "greet" || rec.Text("command") != "echo hi && exit 0" {
		t.Fatalf("run = %v", rec.Fields)
	}
	if _, ok := rec.Value("durationMs"); !ok {
		t.Fatalf("no durationMs: %v", rec.Fields)
	}
}

func TestRunFailed(t *testing.T) {
	r, _ := newRunner(t, Options{})
	rec := finished(t, r, StartSpec{Command: "echo oops >&2; exit 7", Actor: owner})
	if rec.Text("runStatus") != StatusFailed || rec.Text("exitCode") != "7" || strings.TrimSpace(rec.Text("output")) != "oops" {
		t.Fatalf("run = %v", rec.Fields)
	}
}

func TestSpawnFailureLeavesFailedRun(t *testing.T) {
	r, _ := newRunner(t, Options{})
	rec, err := r.Start(context.Background(), StartSpec{Args: []string{filepath.Join(t.TempDir(), "missing")}, Actor: owner})
	if err == nil || rec == nil {
		t.Fatalf("Start = %v, %v; want a failed record and an error", rec, err)
	}
	rec = wait(t, r, rec.ID)
	if rec.Text("runStatus") != StatusFailed || !strings.Contains(rec.Text("output"), "missing") {
		t.Fatalf("run = %v", rec.Fields)
	}
}

func TestCancelKillsProcessGroup(t *testing.T) {
	r, _ := newRunner(t, Options{})
	// The shell forks sleep as a child: only a group kill ends both promptly.
	rec, err := r.Start(context.Background(), StartSpec{Command: "sleep 5; echo late", Actor: owner})
	if err != nil {
		t.Fatal(err)
	}
	begin := time.Now()
	if !r.Cancel(rec.ID) {
		t.Fatal("Cancel found no active run")
	}
	<-r.Done(rec.ID)
	if took := time.Since(begin); took > 200*time.Millisecond {
		t.Fatalf("cancel took %v", took)
	}
	rec = wait(t, r, rec.ID)
	if rec.Text("runStatus") != StatusCancelled || strings.Contains(rec.Text("output"), "late") {
		t.Fatalf("run = %v", rec.Fields)
	}
	if r.Cancel(rec.ID) {
		t.Fatal("Cancel of a finished run reported active")
	}
}

func TestCallerContextCancelIsCancelled(t *testing.T) {
	r, _ := newRunner(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	rec, err := r.Start(ctx, StartSpec{Command: "sleep 5", Actor: owner})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if rec = wait(t, r, rec.ID); rec.Text("runStatus") != StatusCancelled {
		t.Fatalf("run = %v", rec.Fields)
	}
}

func TestRerun(t *testing.T) {
	r, s := newRunner(t, Options{})
	first := finished(t, r, StartSpec{Title: "argv", Args: []string{"/bin/echo", "a b"}, Actor: owner})
	again, err := r.Rerun(context.Background(), first.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID == first.ID {
		t.Fatal("rerun reused the old record")
	}
	again = wait(t, r, again.ID)
	if again.Text("runStatus") != StatusDone || again.Text("output") != "a b\n" || again.Text("title") != "argv" || again.Text("command") != "/bin/echo 'a b'" {
		t.Fatalf("rerun = %v", again.Fields)
	}
	rels, err := s.Related(again.Ref(), RelRerunOf, data.Outgoing)
	if err != nil || len(rels) != 1 || rels[0].To != first.Ref() {
		t.Fatalf("rerunOf = %v, %v", rels, err)
	}
}

func TestOutputCapKeepsTail(t *testing.T) {
	r, _ := newRunner(t, Options{MaxOutputBytes: 100})
	rec := finished(t, r, StartSpec{Command: "i=0; while [ $i -lt 300 ]; do echo line$i; i=$((i+1)); done", Actor: owner})
	out := rec.Text("output")
	if !rec.Bool("outputTruncated") || !strings.HasPrefix(out, "[... ") || !strings.HasSuffix(out, "line299\n") || strings.Contains(out, "line0\n") {
		t.Fatalf("output = %q (truncated %v)", out, rec.Fields["outputTruncated"])
	}
	if body := out[strings.Index(out, "\n")+1:]; len(body) > 100 {
		t.Fatalf("kept %d bytes, cap 100", len(body))
	}
}

func TestCloseCancelsAndLeaksNothing(t *testing.T) {
	s := openStore(t)
	base := runtime.NumGoroutine()
	r := New(s, Options{})
	var ids []string
	for range 3 {
		rec, err := r.Start(context.Background(), StartSpec{Command: "sleep 5", Actor: owner})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, rec.ID)
	}
	r.Close()
	if len(r.Active()) != 0 {
		t.Fatalf("active after Close: %v", r.Active())
	}
	for _, id := range ids {
		if rec := wait(t, r, id); rec.Text("runStatus") != StatusCancelled {
			t.Fatalf("run = %v", rec.Fields)
		}
	}
	if _, err := r.Start(context.Background(), StartSpec{Command: "true"}); err != ErrClosed {
		t.Fatalf("Start after Close = %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > base {
		t.Fatalf("goroutines = %d after Close, %d before", n, base)
	}
}
