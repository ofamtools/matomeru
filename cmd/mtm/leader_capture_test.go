package main

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/work"
)

// liveApp is the Panes page with w1 attached, live.
func liveApp(t *testing.T) (*App, *fakePanes) {
	t.Helper()
	app, f := panesApp(t)
	app.m.Width, app.m.Height = 120, 40
	settle(app, follow(app))
	plainView(app)
	settle(app, key(t, app, "enter"))
	plainView(app)
	if app.r.Focus != paneTermID || app.r.Mode.Name != "terminal" {
		t.Fatalf("enter: focus %s mode %s", app.r.Focus, app.r.Mode.Name)
	}
	return app, f
}

// live fails unless the attached terminal has the focus in terminal mode.
func live(t *testing.T, app *App, after string) {
	t.Helper()
	if app.r.Focus != paneTermID || app.r.Mode.Name != "terminal" || app.r.TopModal() != "" || app.r.Page != panesPage {
		t.Fatalf("%s: focus %s mode %s modal %q page %s", after, app.r.Focus, app.r.Mode.Name, app.r.TopModal(), app.r.Page)
	}
}

// The leader's s attaches the next starred pane, wrapping, and stays live;
// with none starred it says so and the pane stays live.
func TestLeaderSCyclesTheStarredPanes(t *testing.T) {
	t.Parallel()
	app, _ := liveApp(t)
	app.Update(keyMsg("ctrl+a"))
	app.Update(keyMsg("s"))
	if !strings.Contains(app.m.Info, "no starred panes") || app.attached.ID != "w1" {
		t.Fatalf("none starred: status %q attached %s", app.m.Info, app.attached.ID)
	}
	live(t, app, "ctrl+a s, none starred")
	for _, w := range []string{"w1", "w2"} {
		if err := app.work.SetPaneStar(work.PaneStar{Window: w}, true); err != nil {
			t.Fatal(err)
		}
	}
	reload(t, app)
	settle(app, key(t, app, "ctrl+a"))
	settle(app, key(t, app, "s"))
	if app.attached.ID != "w2" {
		t.Fatalf("ctrl+a s: attached %s", app.attached.ID)
	}
	live(t, app, "ctrl+a s")
	settle(app, key(t, app, "ctrl+a"))
	settle(app, key(t, app, "s"))
	if app.attached.ID != "w1" {
		t.Fatalf("ctrl+a s wraps: attached %s", app.attached.ID)
	}
	live(t, app, "ctrl+a s again")
}

// The leader's N, T and n open the quick note, the todos outline and a new
// task's editor over the live pane; closing each gives the terminal its
// keys again. S toggles all projects and stays live.
func TestLeaderCaptureKeysReturnToTheLivePane(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		key, opened string
		close       []string
	}{
		{"N", quickNoteModal, []string{"esc", "esc"}},
		{"T", outlineModal, []string{"esc"}},
		{"n", recordEditorID + "/title", []string{"esc", "esc"}},
	} {
		app, _ := liveApp(t)
		settle(app, key(t, app, "ctrl+a"))
		settle(app, key(t, app, c.key))
		plainView(app)
		if app.r.TopModal() != c.opened && app.r.Focus != c.opened {
			t.Fatalf("ctrl+a %s: modal %q focus %s", c.key, app.r.TopModal(), app.r.Focus)
		}
		if c.key == "n" {
			app.Update(keyMsg("x")) // a title, written as typed
		}
		for _, k := range c.close {
			settle(app, key(t, app, k))
			plainView(app)
		}
		live(t, app, "closing ctrl+a "+c.key)
		if c.key == "n" {
			tasks, _ := app.work.Tasks("")
			if !strings.Contains(func() string {
				var s []string
				for _, t := range tasks {
					s = append(s, t.Title)
				}
				return strings.Join(s, "|")
			}(), "x") {
				t.Fatalf("ctrl+a n wrote no task: %+v", tasks)
			}
		}
	}
	app, _ := liveApp(t)
	all := app.all
	settle(app, key(t, app, "ctrl+a"))
	settle(app, key(t, app, "S"))
	if app.all == all {
		t.Fatal("ctrl+a S did not toggle all projects")
	}
	live(t, app, "ctrl+a S")
}

// The leader's hint and its keys modal (terminal.keys) name the new keys.
func TestLeaderHintAndKeysListTheNewKeys(t *testing.T) {
	t.Parallel()
	app, _ := liveApp(t)
	var hint string
	for _, b := range app.r.Noun("terminal").Bindings["terminal"] {
		if b.Keys == "ctrl+a" {
			hint = b.Hint
		}
	}
	for _, want := range []string{"s starred", "N note", "n task", "T todos", "S all"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("hint %q lacks %q", hint, want)
		}
	}
	// The leader's ? is the help tree now; terminal.keys (the palette's
	// terminal keys) still opens the leader modal.
	if _, err := app.r.Execute(ochazuke.Request{Verb: "keys", Noun: "terminal", Target: paneTermID}); err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, it := range app.r.List(leaderList).Items() {
		rows = append(rows, it.Label)
	}
	text := strings.Join(rows, "\n")
	for _, want := range []string{"ctrl+a s ", "ctrl+a N ", "ctrl+a n ", "ctrl+a T ", "ctrl+a S ", "quick note", "next starred pane"} {
		if !strings.Contains(text, want) {
			t.Fatalf("leader keys lack %q:\n%s", want, text)
		}
	}
}
