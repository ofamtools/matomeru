package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// A project's own colour wins; one with none, or one that does not parse,
// gets the palette tone of its creation rank: the first projects are all
// distinct, and adding or archiving one leaves the others as they were.
func TestProjectHueByRank(t *testing.T) {
	t.Parallel()
	a := open(t)
	var ps []work.Project
	for i := range ochazuke.PaletteSize() {
		ps = append(ps, work.Project{ID: fmt.Sprintf("p%02d", i), Created: int64(100 + i)})
	}
	a.projects = ps
	a.setProjectHues()
	seen := map[string]bool{}
	before := map[string]string{}
	for _, p := range ps {
		h := a.projectHue(p.ID)
		if h == "" || seen[h] {
			t.Fatalf("%s hue %q missing or repeated", p.ID, h)
		}
		seen[h], before[p.ID] = true, h
	}
	// A later project repeats in rotation and moves nobody.
	a.projects = append(slices.Clone(ps), work.Project{ID: "late", Created: 999})
	a.setProjectHues()
	for id, h := range before {
		if a.projectHue(id) != h {
			t.Fatalf("adding a project recoloured %s", id)
		}
	}
	if a.projectHue("late") != before["p00"] {
		t.Fatalf("project %d does not rotate to the first tone", ochazuke.PaletteSize())
	}
	// Archiving keeps every colour.
	a.projects[3].Archived = true
	a.setProjectHues()
	for id, h := range before {
		if a.projectHue(id) != h {
			t.Fatalf("archiving a project recoloured %s", id)
		}
	}
	// An explicit colour wins; one that does not parse falls back.
	a.projects[1].Theme.Color = "#7FB685"
	a.projects[2].Theme.Color = "not-a-colour"
	a.setProjectHues()
	if a.projectHue("p01") != "#7FB685" {
		t.Fatalf("explicit colour = %q", a.projectHue("p01"))
	}
	if a.projectHue("p02") != before["p02"] {
		t.Fatalf("an unparsable colour gave %q, want the derived %q", a.projectHue("p02"), before["p02"])
	}
	if a.projectHue("") != "" || a.projectHue("nope") != "" {
		t.Fatal("no project has a hue")
	}
	if a.projects[5].Theme.Color != "" {
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
	a.setProjectHues()
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

// panes.status-border tints a pane's border by state; panel.hue-over-state
// (default true) says whether the project colour wins it: false lets a
// state tone that draws keep the border, the hue still colouring the title.
func TestPaneHueOverState(t *testing.T) {
	t.Parallel()
	a, panes := sectionsApp(t)
	a.projects = []work.Project{{ID: "ochazuke"}}
	a.setProjectHues()
	if err := a.work.SetPaneOwner(work.Assignment{Window: "wwait", Session: "s", ProjectID: "ochazuke"}); err != nil {
		t.Fatal(err)
	}
	a.setPanes(panes, nil)
	l := ochazuke.Layout{Styles: a.m.Styles()}
	p := panes[0] // needs input: its border has a state tone
	if p.ID != "wwait" {
		t.Fatalf("panes moved: %s", p.ID)
	}
	draw := func(layout ochazuke.Layout) string {
		return layout.PanelBlock("pane", ochazuke.Text("body"), 30, 5, true).Text
	}
	side := func(s string) string { return strings.Split(s, "\n")[1] }
	stateOnly, hueOnly := side(draw(a.paneBorderLayout(l, p))), side(draw(a.r.WithHue(l, a.paneHue(p))))
	for _, over := range []bool{true, false} {
		if err := a.r.Settings().Set(ochazuke.PanelHueOverStateSetting, over); err != nil {
			t.Fatal(err)
		}
		got := side(draw(a.panePanel(l, p)))
		want := map[bool]string{true: hueOnly, false: stateOnly}[over]
		if got != want {
			t.Errorf("over=%v: the border is not the %s's", over, map[bool]string{true: "hue", false: "state tone"}[over])
		}
	}
}
