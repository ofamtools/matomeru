package main

import (
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/work"
)

func starred(t *testing.T, app *App) map[string]work.PaneStar {
	t.Helper()
	stars, err := app.work.PaneStars()
	if err != nil {
		t.Fatal(err)
	}
	return stars
}

// * on a pane row stars it (★ in the row's gutter, written to the store), * again
// unstars it, --starred sets it; a listing without the window keeps its
// star (work.PaneStar says why), and the row reads ★ when it is back.
func TestStarAPaneRow(t *testing.T) {
	app, f := panesApp(t)
	settle(app, follow(app))
	if app.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("focus %s", app.r.Focus)
	}
	press(t, app, "*")
	if _, ok := starred(t, app)["w1"]; !ok || !strings.Contains(cells(app, "pane:matomeru/w1"), "★   claude") {
		t.Fatalf("after *: stars %+v row %q", starred(t, app), cells(app, "pane:matomeru/w1"))
	}
	if fr := frame(app); !strings.Contains(fr, "★   claude") {
		t.Fatalf("frame lacks the star:\n%s", fr)
	}
	press(t, app, "*")
	if len(starred(t, app)) != 0 || strings.Contains(cells(app, "pane:matomeru/w1"), "★") {
		t.Fatalf("after * *: %+v", starred(t, app))
	}
	for range 2 {
		if _, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "star", Target: "pane:ochazuke/w2", Args: ochazuke.Arguments{"value": true}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := starred(t, app)["w2"]; !ok {
		t.Fatalf("--value: %+v", starred(t, app))
	}
	// w2 is missing from a listing (another server's, or a partial one):
	// its star stays, and the row reads ★ when the window is listed again.
	f.panes = f.panes[:1]
	reload(t, app)
	if _, ok := starred(t, app)["w2"]; !ok {
		t.Fatalf("a listing without w2 dropped its star: %+v", starred(t, app))
	}
	f.panes = append(f.panes, pane{Session: "ochazuke", Pane: daemon.Pane{ID: "w2", Name: "shell"}})
	reload(t, app)
	if !strings.Contains(cells(app, "pane:ochazuke/w2"), "★") {
		t.Fatalf("w2 back without its star: %q", cells(app, "pane:ochazuke/w2"))
	}
}

// ` on the Panes page selects the next starred pane, wrapping; with none
// starred it says how to star one.
func TestBacktickSelectsTheNextStarredPane(t *testing.T) {
	app, _ := panesApp(t)
	settle(app, follow(app))
	app.Update(keyMsg("`"))
	if app.r.Focus != "pane:matomeru/w1" || !strings.Contains(app.m.Info, "no starred panes") {
		t.Fatalf("none starred: focus %s status %q", app.r.Focus, app.m.Info)
	}
	for _, row := range []string{"pane:matomeru/w1", "pane:ochazuke/w2"} {
		if _, err := app.r.Execute(ochazuke.Request{Noun: "pane", Verb: "star", Target: row}); err != nil {
			t.Fatal(err)
		}
	}
	press(t, app, "`")
	if app.r.Focus != "pane:ochazuke/w2" {
		t.Fatalf("`: focus %s", app.r.Focus)
	}
	press(t, app, "`")
	if app.r.Focus != "pane:matomeru/w1" {
		t.Fatalf("` wraps: focus %s", app.r.Focus)
	}
}
