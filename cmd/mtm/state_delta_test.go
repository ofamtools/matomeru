package main

import (
	"testing"

	ocli "github.com/unixsuperhero/ochazuke/cli"

	"github.com/unixsuperhero/matomeru/internal/daemon"
)

// projects state speaks ochazuke's delta protocol through the shell: the
// first call (--since=) answers every list in full with hashes, the next
// one from those hashes answers every list unchanged, and a pane added on
// the daemon answers the pane list as a delta or full, the rest unchanged.
// Its cache is scoped by the database and the daemon socket: another
// database or another socket is another cache id.
func TestProjectsStateAnswersDeltasScopedByDataAndSocket(t *testing.T) {
	t.Parallel()
	app, _ := cliApp(t)
	state := func(a *App, flags ...string) ocli.StateAnswer {
		t.Helper()
		out := mustCLI(t, a, append([]string{"projects", "state"}, flags...)...)
		answer, err := ocli.DecodeState([]byte(out))
		if err != nil || !answer.Delta {
			t.Fatalf("answer %+v %v:\n%s", answer, err, out)
		}
		return answer
	}
	hashes := func(a ocli.StateAnswer) map[string]string {
		out := map[string]string{}
		for _, l := range a.Lists {
			out[l.List] = l.Hash
		}
		return out
	}
	first := state(app, "--since=")
	if len(first.Lists) < 3 || first.Cache == "" {
		t.Fatalf("first answer lists %+v", first.Lists)
	}
	for _, l := range first.Lists {
		if l.Answer != ocli.AnswerFull {
			t.Fatalf("first answer %s is %s, want full", l.List, l.Answer)
		}
	}
	again := state(app, ocli.StateFlags(first.Cache, hashes(first))...)
	for _, l := range again.Lists {
		if l.Answer != ocli.AnswerUnchanged {
			t.Fatalf("unchanged poll answers %s %s", l.List, l.Answer)
		}
	}
	if len(again.Rows) != 0 {
		t.Fatalf("an unchanged poll printed %d rows", len(again.Rows))
	}
	fp := app.panesd.(*fakePanes)
	added := fp.panes[0]
	added.ID, added.Title = "p-added", "added"
	fp.panes = append(fp.panes, added)
	changed := state(app, ocli.StateFlags(first.Cache, hashes(first))...)
	for _, l := range changed.Lists {
		want := l.List == "pane"
		if got := l.Answer != ocli.AnswerUnchanged; got != want {
			t.Fatalf("after a pane was added %s answers %s", l.List, l.Answer)
		}
	}

	other := openAt(t, seeded(t))
	other.panesd = app.panesd
	if c := state(other, "--since=").Cache; c == first.Cache {
		t.Fatalf("another database shares the cache id %s", c)
	}
	app.daemon = daemon.New("/nowhere/else.sock")
	if c := state(app, "--since=").Cache; c == first.Cache {
		t.Fatalf("another socket shares the cache id %s", c)
	}
}
