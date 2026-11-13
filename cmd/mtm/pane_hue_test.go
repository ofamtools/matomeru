package main

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// A project's own colour wins; one with none gets a palette tone picked by
// its id, the same every time and different across ids; nothing is written.
func TestProjectHueDerivedAndExplicit(t *testing.T) {
	t.Parallel()
	a := open(t)
	a.projects = []work.Project{
		{ID: "own", Theme: work.Theme{Color: "#7FB685"}},
		{ID: "matomeru"}, {ID: "ochazuke"}, {ID: "oden"}, {ID: "nabe"},
	}
	if got := a.projectHue("own"); got != "#7FB685" {
		t.Fatalf("explicit colour = %q", got)
	}
	seen := map[string]bool{}
	for _, id := range []string{"matomeru", "ochazuke", "oden", "nabe"} {
		h := a.projectHue(id)
		if h != ochazuke.PaletteTone(id) || h != a.projectHue(id) {
			t.Fatalf("%s hue = %q, want its palette tone", id, h)
		}
		seen[h] = true
	}
	if len(seen) < 3 {
		t.Fatalf("four projects drew %d hues", len(seen))
	}
	if a.projectHue("") != "" {
		t.Fatal("no project has a hue")
	}
	if a.projects[1].Theme.Color != "" {
		t.Fatal("a derived colour was written to the project")
	}
}

// A pane's panel border and title take its project's hue; a pane with no
// project keeps today's panel; panel.hue says which parts; a focused panel
// still differs from an unfocused one.
func TestPanePanelHue(t *testing.T) {
	t.Parallel()
	a, panes := sectionsApp(t)
	a.projects = []work.Project{{ID: "ochazuke"}}
	if err := a.work.SetPaneOwner(work.Assignment{Window: "wnew", Session: "s", ProjectID: "ochazuke"}); err != nil {
		t.Fatal(err)
	}
	a.setPanes(panes, nil)
	l := ochazuke.Layout{Styles: a.m.Styles()}
	draw := func(p pane, focused bool) string {
		return a.panePanel(l, p).PanelBlock("pane", ochazuke.Text("body"), 30, 5, focused).Text
	}
	owned, free := panes[3], panes[5]
	if owned.ID != "wnew" || free.ID != "widle" {
		t.Fatalf("panes moved: %s %s", owned.ID, free.ID)
	}
	plain := func(p pane, focused bool) string {
		return a.r.WithHue(a.paneBorderLayout(l, p), "").PanelBlock("pane", ochazuke.Text("body"), 30, 5, focused).Text
	}
	top := func(s string) string { return strings.SplitN(s, "\n", 2)[0] }
	side := func(s string) string { return strings.Split(s, "\n")[1] }
	for _, c := range []struct {
		value         string
		border, title bool
	}{{"both", true, true}, {"border", true, false}, {"title", false, true}, {"off", false, false}} {
		if err := a.r.Settings().Set(ochazuke.PanelHueSetting, c.value); err != nil {
			t.Fatal(err)
		}
		if got := side(draw(owned, true)) != side(plain(owned, true)); got != c.border {
			t.Errorf("%s: border tinted = %v, want %v", c.value, got, c.border)
		}
		if got := top(draw(owned, true)) != top(plain(owned, true)); got != (c.border || c.title) {
			t.Errorf("%s: top row tinted = %v", c.value, got)
		}
		if draw(free, true) != plain(free, true) {
			t.Errorf("%s: a pane with no project changed", c.value)
		}
	}
	if err := a.r.Settings().Set(ochazuke.PanelHueSetting, "both"); err != nil {
		t.Fatal(err)
	}
	if draw(owned, true) == draw(owned, false) {
		t.Error("focused and unfocused tinted panels look alike")
	}
}
