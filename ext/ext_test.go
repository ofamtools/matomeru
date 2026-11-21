package ext

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/runs"
	"ofam.dev/ochazuke/core"
)

func openStore(t *testing.T) *data.Store {
	t.Helper()
	t.Setenv("PATH", "/usr/bin:/bin") // keep the owner's real o-* tools out of reach
	s, err := data.Open(filepath.Join(t.TempDir(), "ext.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func script(t *testing.T, dir, name, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// described answers the capabilities convention; helpOnly lists commands in
// its help text.
const (
	described = `case "$1" in
--version) echo "o-git 1.2.3" ;;
capabilities) echo '{"summary":"git views","capabilities":["git.repo"],"commands":["status","log"]}' ;;
status) echo "clean tree" ;;
esac
`
	helpOnly = `case "$1" in
--help) printf 'usage: o-md\n\nCommands:\n  new     make a doc\n  show    show a doc\n\nFlags:\n  --json\n' ;;
capabilities) exit 2 ;;
esac
`
)

func byName(t *testing.T, results []Result, name string) Result {
	t.Helper()
	for _, r := range results {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no result for %s", name)
	return Result{}
}

func changeSets(t *testing.T, s *data.Store) int {
	t.Helper()
	h, err := s.History(nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return len(h)
}

func TestDiscover(t *testing.T) {
	s := openStore(t)
	dir := t.TempDir()
	gitBin := script(t, dir, "o-git", described, 0o755)
	script(t, dir, "o-md", helpOnly, 0o755)
	script(t, dir, "o-csv", "echo hi\n", 0o644)        // not executable
	script(t, dir, "o-lane", "exec sleep 10\n", 0o755) // never answers
	m := New(Options{Tools: []string{"o-git", "o-md", "o-csv", "o-lane", "o-zzz-missing"}, ProbeTimeout: time.Second})
	ctx := context.Background()

	results, err := m.Discover(ctx, []string{dir}, s)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	git := byName(t, results, "o-git")
	if rec := git.Record; rec == nil || rec.Text("runStatus") != StatusHealthy || rec.Text("binary") != gitBin ||
		rec.Text("version") != "o-git 1.2.3" || rec.Text("summary") != "git views" ||
		data.Text(rec.Fields["commands"]) != data.Text([]any{"status", "log"}) || data.Text(rec.Fields["capabilities"]) != data.Text([]any{"git.repo"}) {
		t.Fatalf("o-git = %+v", git.Record)
	}
	if md := byName(t, results, "o-md"); md.Status != StatusHealthy || !slices.Equal(md.Commands, []string{"new", "show"}) {
		t.Fatalf("o-md = %+v", md)
	}
	for _, name := range []string{"o-csv", "o-lane"} {
		if r := byName(t, results, name); r.Status != StatusOffline || r.Record == nil || r.Record.Text("runStatus") != StatusOffline {
			t.Fatalf("%s = %+v", name, r)
		}
	}
	if missing := byName(t, results, "o-zzz-missing"); missing.Status != StatusOffline || missing.Record != nil {
		t.Fatalf("missing = %+v", missing)
	}

	// Rediscovering an unchanged set writes nothing and keeps the IDs.
	sets := changeSets(t, s)
	again, err := m.Discover(ctx, []string{dir}, s)
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range again {
		if r.Changed || (r.Record == nil) != (results[i].Record == nil) || (r.Record != nil && r.Record.ID != results[i].Record.ID) {
			t.Fatalf("rediscover %s = %+v", r.Name, r)
		}
	}
	if n := changeSets(t, s); n != sets {
		t.Fatalf("change sets %d -> %d on an unchanged rediscover", sets, n)
	}
	if q, _ := s.Query(data.Query{Kind: data.KindExtension}); q.Total != 4 {
		t.Fatalf("extension records = %d, want 4", q.Total)
	}

	// A tool removed later goes offline and keeps what was learned.
	if err := os.Remove(gitBin); err != nil {
		t.Fatal(err)
	}
	later, err := m.Discover(ctx, []string{dir}, s)
	if err != nil {
		t.Fatal(err)
	}
	gone := byName(t, later, "o-git")
	if !gone.Changed || gone.Record.ID != git.Record.ID || gone.Record.Text("runStatus") != StatusOffline || gone.Record.Text("summary") != "git views" {
		t.Fatalf("removed o-git = %+v", gone.Record)
	}
}

func TestProbesHonorProcessCap(t *testing.T) {
	s := openStore(t)
	dir, live := t.TempDir(), t.TempDir()
	// Each probe marks itself live, records how many are live, then leaves.
	body := `touch "` + live + `/$$"; ls "` + live + `" | wc -l >> "` + live + `.log"; sleep 0.2; rm -f "` + live + `/$$"` + "\n"
	var tools []string
	for i := range 6 {
		name := "o-slow" + strconv.Itoa(i)
		script(t, dir, name, body, 0o755)
		tools = append(tools, name)
	}
	m := New(Options{Tools: tools, MaxProcesses: 2})
	if _, err := m.Discover(context.Background(), []string{dir}, s); err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(live + ".log")
	if err != nil {
		t.Fatal(err)
	}
	high := 0
	for _, f := range strings.Fields(string(log)) {
		n, _ := strconv.Atoi(f)
		high = max(high, n)
	}
	if high > 2 || high < 2 {
		t.Fatalf("concurrent probes high-water = %d, want the cap 2", high)
	}
}

func TestRunCommand(t *testing.T) {
	s := openStore(t)
	dir := t.TempDir()
	script(t, dir, "o-git", described, 0o755)
	m := New(Options{Tools: []string{"o-git"}})
	if _, err := m.Discover(context.Background(), []string{dir}, s); err != nil {
		t.Fatal(err)
	}
	runner := runs.New(s, runs.Options{})
	t.Cleanup(runner.Close)

	if _, err := m.RunCommand(context.Background(), "o-zzz-missing", nil, runner); err == nil {
		t.Fatal("RunCommand of an unknown extension did not refuse")
	}
	rec, err := m.RunCommand(context.Background(), "o-git", []string{"status"}, runner)
	if err != nil {
		t.Fatal(err)
	}
	<-runner.Done(rec.ID)
	rec, err = s.Get(core.Ref{Kind: data.KindRun, ID: rec.ID})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Text("title") != "o-git status" || rec.Text("runStatus") != runs.StatusDone || rec.Text("output") != "clean tree\n" {
		t.Fatalf("run = %v", rec.Fields)
	}
}
