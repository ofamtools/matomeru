package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestTurnTreeSearchDoesNotRedrawSourceLines(t *testing.T) {
	t.Parallel()
	for _, source := range []struct {
		name string
		open func(*testing.T) *App
	}{
		{"journal", func(t *testing.T) *App {
			return wrapApp(t, "alpha beta gamma delta epsilon ENDWORD")
		}},
		{"session", func(t *testing.T) *App {
			a, _, log := claudeTurnsApp(t)
			useTurnStyle(t, a, turnsStyleTree)
			writeLog(t, log, logPrompt(t, "u2", "2026-10-27T10:01:00Z", "alpha beta gamma delta epsilon ENDWORD"))
			reload(t, a)
			if err := a.pv.turns.Select("turn:u2"); err != nil {
				t.Fatal(err)
			}
			if err := a.pv.turns.Focus(); err != nil {
				t.Fatal(err)
			}
			settle(a, key(t, a, "l"))
			return a
		}},
	} {
		t.Run(source.name, func(t *testing.T) {
			t.Parallel()
			a := source.open(t)
			tree := a.pv.turns
			a.wrapTurns(tree, 24)
			want := []string{
				"      alpha beta gamma",
				"      delta epsilon",
				"      ENDWORD",
			}
			for _, filtered := range []bool{false, true} {
				if filtered {
					// This phrase crosses the first two pieces. Search must
					// retain every piece without drawing the whole source line.
					tree.List().SetFilter("gamma delta")
				}
				f := ansi.Strip(tree.Block(24, 40).Text)
				t.Logf("%s source, filtered=%v:\n%s", source.name, filtered, f)
				name := "turn-source-" + source.name
				if filtered {
					name += "-filtered"
				}
				writeFrame(t, name+".txt", f)
				lines := strings.Split(f, "\n")
				for i := range lines {
					lines[i] = strings.TrimRight(lines[i], " ")
				}
				at := slices.Index(lines, want[0])
				if at < 0 || at+len(want) > len(lines) || !slices.Equal(lines[at:at+len(want)], want) {
					t.Fatalf("source pieces must render once, including their ending; want %q:\n%s", want, f)
				}
			}
		})
	}
}
