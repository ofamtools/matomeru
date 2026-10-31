package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// selectedNotLive fails unless the app is in normal mode on the tree with nothing
// attached.
func selectedNotLive(t *testing.T, app *App, step, focus string) {
	t.Helper()
	if app.r.Focus != focus || app.r.Mode.Name != "normal" || app.attached.ID != "" || app.r.Instances[paneTermID] != nil {
		t.Fatalf("%s: focus %s mode %s attached %q terminal %v", step, app.r.Focus, app.r.Mode.Name, app.attached.ID, app.r.Instances[paneTermID] != nil)
	}
}

// Outside live, ctrl+a j and k select the next and previous pane row,
// skipping the session rows between them, without attaching; past either
// end they do nothing. From a session row, j is the first pane below it
// and k the last above it.
func TestLeaderCyclesPanesOutsideLive(t *testing.T) {
	t.Parallel()
	app, f := panesApp(t)
	// This test reads the session rows (project → session → pane).
	groupBy(t, app, groupProjectSessionPane)
	settle(app, follow(app))
	selectedNotLive(t, app, "start", "pane:matomeru/w1")
	press(t, app, "ctrl+a", "k") // the first pane: a quiet edge
	selectedNotLive(t, app, "ctrl+a k at the start", "pane:matomeru/w1")
	press(t, app, "ctrl+a", "j")
	selectedNotLive(t, app, "ctrl+a j", "pane:ochazuke/w2")
	if len(f.streams) != 0 || app.pv.tab != tabScreen {
		t.Fatalf("ctrl+a j: streams %d tab %d", len(f.streams), app.pv.tab)
	}
	press(t, app, "ctrl+a", "j") // the last pane: a quiet edge
	selectedNotLive(t, app, "ctrl+a j at the end", "pane:ochazuke/w2")
	press(t, app, "ctrl+a", "k")
	selectedNotLive(t, app, "ctrl+a k", "pane:matomeru/w1")

	// The tab stays as it was.
	press(t, app, "]")
	tab := app.pv.tab
	press(t, app, "ctrl+a", "j")
	if app.pv.tab != tab || tab == tabScreen {
		t.Fatalf("tab %d after ctrl+a j, was %d", app.pv.tab, tab)
	}
	press(t, app, "[")

	// From the session row between the panes.
	press(t, app, "k")
	if !strings.HasPrefix(app.r.Focus, "session") && app.r.Instances[app.r.Focus].Noun != "session" {
		t.Fatalf("k from w2: focus %s", app.r.Focus)
	}
	session := app.r.Focus
	press(t, app, "ctrl+a", "j")
	selectedNotLive(t, app, "ctrl+a j from a session", "pane:ochazuke/w2")
	press(t, app, "k")
	if app.r.Focus != session {
		t.Fatalf("k back: focus %s", app.r.Focus)
	}
	press(t, app, "ctrl+a", "k")
	selectedNotLive(t, app, "ctrl+a k from a session", "pane:matomeru/w1")

	// From the detail, the selection moves and the detail keeps the focus.
	if err := app.r.SetFocus(panesDetailID); err != nil {
		t.Fatal(err)
	}
	press(t, app, "ctrl+a", "j")
	if row, _ := app.selectedRow(); row != "pane:ochazuke/w2" {
		t.Fatalf("ctrl+a j from the detail: row %s", row)
	}
	selectedNotLive(t, app, "ctrl+a j from the detail", panesDetailID)
}

// Outside live, ctrl+a s selects the next starred pane (wrapping); with
// none starred it says how to star one.
func TestLeaderSelectsStarredOutsideLive(t *testing.T) {
	t.Parallel()
	app, f := panesApp(t)
	settle(app, follow(app))
	app.Update(keyMsg("ctrl+a"))
	app.Update(keyMsg("s"))
	if !strings.Contains(app.m.Info, noStarredPanes) {
		t.Fatalf("none starred: status %q", app.m.Info)
	}
	selectedNotLive(t, app, "none starred", "pane:matomeru/w1")
	app.m.Info = ""
	for _, row := range []string{"pane:matomeru/w1", "pane:ochazuke/w2"} {
		if _, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "star", Target: row}); err != nil {
			t.Fatal(err)
		}
	}
	press(t, app, "ctrl+a", "s")
	selectedNotLive(t, app, "ctrl+a s", "pane:ochazuke/w2")
	press(t, app, "ctrl+a", "s")
	selectedNotLive(t, app, "ctrl+a s wraps", "pane:matomeru/w1")
	if len(f.streams) != 0 {
		t.Fatalf("streams %d", len(f.streams))
	}
	// Live, it still attaches.
	key(t, app, "enter")
	press(t, app, "ctrl+a", "s")
	if app.attached.ID != "w2" || app.r.Focus != paneTermID {
		t.Fatalf("live ctrl+a s: attached %s focus %s", app.attached.ID, app.r.Focus)
	}
}

// The rows are the page's normal-mode rows, listed for the tree's stops,
// and nothing else binds the leader there but ochazuke's ctrl+a o; they
// follow the terminal-leader setting.
func TestPageLeaderRowsFollowTheSetting(t *testing.T) {
	t.Parallel()
	app, _ := panesApp(t)
	if hints := ochazuke.BindingHints(app.r, app.r.Focus); !strings.Contains(hints, "next pane") {
		t.Errorf("hints %q", hints)
	}
	if err := app.r.Settings().Set(ochazuke.TerminalLeaderSetting, "ctrl+b"); err != nil {
		t.Fatal(err)
	}
	press(t, app, "ctrl+b", "j")
	selectedNotLive(t, app, "ctrl+b j", "pane:ochazuke/w2")
	for _, b := range app.r.Instances[panesPage].Bindings {
		if strings.HasPrefix(b.Keys, "ctrl+a") {
			t.Errorf("old leader row %q left", b.Keys)
		}
	}
	if err := app.r.Settings().Set(ochazuke.TerminalLeaderSetting, "ctrl+a"); err != nil {
		t.Fatal(err)
	}
	// Every stop of the page lists the rows, and no other normal-mode (or
	// every-mode) row starts with the leader.
	for _, target := range []string{"pane:matomeru/w1", paneListID, panesDetailID} {
		var rows []ochazuke.Binding
		if err := json.Unmarshal([]byte(mustCLI(t, app, "bindings", "list", "--target", target)), &rows); err != nil {
			t.Fatal(err)
		}
		listed := map[string]string{}
		for _, b := range rows {
			if (b.Mode == "normal" || b.Mode == "") && strings.HasPrefix(b.Keys, "ctrl+a") {
				listed[b.Keys] = app.r.DisplayKey(b.Request.Key())
			}
		}
		// ctrl+a o is ochazuke's own row (bindings/panes.json, v0.61,
		// UX-RULES R3.15): ochazuke.focus.next, the next focus stop, like
		// ctrl+w l. mtm binds no normal-mode leader o, so the library row
		// answers here; inside a live terminal mtm's leader o
		// (terminal.other) answers first.
		want := map[string]string{"ctrl+a j": "terminal.cycle", "ctrl+a k": "terminal.cycle", "ctrl+a o": "focus.next", "ctrl+a p": "pane.pick", "ctrl+a s": "terminal.starred", "ctrl+a w": "panes.sides", "ctrl+a esc": "mode.leave"}
		for k, v := range want {
			if listed[k] != v {
				t.Errorf("%s: %s is %q, want %q", target, k, listed[k], v)
			}
		}
		if len(listed) != len(want) {
			t.Errorf("%s: leader rows %v", target, listed)
		}
	}
	if _, err := app.r.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
}
