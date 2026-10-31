package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
)

// ? help over a live pane lists the leader once, then every leader key as
// the whole sequence with what it does, the words the leader modal
// (ctrl+a ?) shows; each sequence is listed once, under the leader, and
// the rows follow the terminal-leader setting (ochazuke R6.10).
func TestHelpOverALivePaneListsTheLeaderKeys(t *testing.T) {
	t.Parallel()
	app, _ := liveApp(t)
	check := func(leader string) {
		t.Helper()
		rows := ochazuke.BindingHelp(app.r, paneTermID)
		byKey := map[string]ochazuke.HelpRow{}
		for _, row := range rows {
			if _, dup := byKey[row.Key]; dup {
				t.Fatalf("%q listed twice", row.Key)
			}
			byKey[row.Key] = row
		}
		head, ok := byKey[leader]
		if !ok || !head.Group || head.Enters != "leader_pending" || head.Operation != "mode.enter" {
			t.Fatalf("leader row %+v", head)
		}
		bound := map[string]bool{}
		for _, b := range app.r.Noun("terminal").Bindings["leader_pending"] {
			if b.Keys != "" && !b.Hidden {
				bound[b.Keys] = true
			}
		}
		for _, w := range leaderWords {
			if !bound[w.key] {
				continue
			}
			row, ok := byKey[leader+" "+w.key]
			// The framework appends the operation's aliases ("· also: opt,
			// options" on settings.edit since ochazuke v0.50.0).
			if desc, _, _ := strings.Cut(row.Description, " · also: "); !ok || row.Under != leader || desc != w.text {
				t.Fatalf("%s %s: row %+v", leader, w.key, row)
			}
			if len(row.Steps) != 2 || row.Steps[0].Key != leader || row.Steps[0].Mode != "leader_pending" || row.Steps[1].Key != w.key {
				t.Fatalf("%s %s: steps %+v", leader, w.key, row.Steps)
			}
		}
	}
	check("ctrl+a")

	mono(t, app)
	app.m.Width, app.m.Height = 160, 200
	if _, err := app.r.Execute(ochazuke.Do("ochazuke.bindings.help").Request); err != nil {
		t.Fatal(err)
	}
	frame := ansi.Strip(app.m.View().Content)
	if strings.Count(frame, "▾ ctrl+a") != 1 || strings.Contains(frame, "▾ ctrl+a v") {
		t.Fatalf("the leader is not one open row:\n%s", frame)
	}
	for _, want := range []string{"▸ ctrl+a v", "side vim: open it, focus it", "▸ ctrl+a t", "side terminal: open it", "▸ ctrl+a d", "close the front side, then"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("help over the pane lacks %q:\n%s", want, frame)
		}
	}
	if err := app.r.Pop(); err != nil {
		t.Fatal(err)
	}

	if err := app.r.Settings().Set(ochazuke.TerminalLeaderSetting, "ctrl+b"); err != nil {
		t.Fatal(err)
	}
	check("ctrl+b")
}
