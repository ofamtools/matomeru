package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// firstColor makes colour picking deterministic: always the first of the
// candidates, so a test names the tones it expects.
func firstColor(a *App) { a.colorRand = func(int) int { return 0 } }

func storedColor(t *testing.T, a *App, id string) string {
	t.Helper()
	p, err := a.work.Project(id)
	if err != nil {
		t.Fatal(err)
	}
	return p.Theme.Color
}

// A new project with no colour gets a palette tone no live project holds,
// whichever way it is created (the store's PutProject); an explicit colour
// wins; when every tone is held a least-used one is picked; archiving frees
// a tone, and a stored project is never recoloured by a later write.
func TestNewProjectColorPicked(t *testing.T) {
	t.Parallel()
	a := open(t)
	firstColor(a)
	put := func(id, color string) {
		t.Helper()
		if err := a.work.PutProject(work.Project{ID: id, Status: "open", Created: work.Now(), Theme: work.Theme{Color: color}}); err != nil {
			t.Fatal(err)
		}
	}
	held := func() map[string]int {
		ps, err := a.work.Projects()
		if err != nil {
			t.Fatal(err)
		}
		n := map[string]int{}
		for _, p := range ps {
			if !p.Archived {
				n[p.Theme.Color]++
			}
		}
		return n
	}
	size := ochazuke.PaletteSize()
	for i := 0; ; i++ {
		before := held()
		unheld := 0
		for j := range size {
			if before[ochazuke.PaletteToneAt(j)] == 0 {
				unheld++
			}
		}
		if unheld == 0 {
			break
		}
		id := fmt.Sprintf("p%02d", i)
		put(id, "")
		if got := storedColor(t, a, id); !ochazuke.ValidHue(got) || before[got] != 0 {
			t.Fatalf("%s got %q, a tone already held", id, got)
		}
	}
	put("own", "#7FB685")
	if got := storedColor(t, a, "own"); got != "#7FB685" {
		t.Fatalf("explicit colour = %q", got)
	}
	// Every tone is held: the next takes a least-used one.
	before := held()
	least := before[ochazuke.PaletteToneAt(0)]
	for i := range size {
		least = min(least, before[ochazuke.PaletteToneAt(i)])
	}
	put("again", "")
	if got := storedColor(t, a, "again"); before[got] != least {
		t.Fatalf("all tones held: got %s held %d times, least is %d", got, before[got], least)
	}
	// Archiving frees a tone for the next project.
	freedTone := storedColor(t, a, "p01")
	p, _ := a.work.Project("p01")
	p.Archived = true
	if err := a.work.PutProject(p); err != nil {
		t.Fatal(err)
	}
	if held()[freedTone] >= least+1 {
		t.Fatalf("archiving did not free %s", freedTone)
	}
	put("freed", "")
	// Restoring keeps its stored colour; a rewrite never recolours.
	p.Archived = false
	if err := a.work.PutProject(p); err != nil {
		t.Fatal(err)
	}
	if got := storedColor(t, a, "p01"); got != freedTone {
		t.Fatalf("restore changed the colour: %s", got)
	}
	p.Theme.Color = ""
	if err := a.work.PutProject(p); err != nil {
		t.Fatal(err)
	}
	if got := storedColor(t, a, "p01"); got != "" {
		t.Fatalf("a rewrite of a stored project picked a colour: %q", got)
	}
}

// Loading assigns a colour to the projects that have none, oldest first,
// and only to those: one with any colour value, parsable or not, is left,
// and a second load changes nothing. Each is a recorded project set.
func TestAssignProjectColorsOnLoad(t *testing.T) {
	t.Parallel()
	a := open(t)
	a.work.SetColorPicker(nil) // projects written before colours were assigned
	firstColor(a)
	for i, c := range []string{"", "#7FB685", "", "garbage"} {
		if err := a.work.PutProject(work.Project{ID: fmt.Sprintf("q%d", i), Status: "open", Created: int64(10 + i), Theme: work.Theme{Color: c}}); err != nil {
			t.Fatal(err)
		}
	}
	a.work.SetColorPicker(a.pickProjectColor)
	if err := a.assignProjectColors(); err != nil {
		t.Fatal(err)
	}
	got := []string{storedColor(t, a, "q0"), storedColor(t, a, "q1"), storedColor(t, a, "q2"), storedColor(t, a, "q3")}
	want := []string{ochazuke.PaletteToneAt(0), "#7FB685", ochazuke.PaletteToneAt(1), "garbage"}
	if !slices.Equal(got, want) {
		t.Fatalf("colours = %v, want %v", got, want)
	}
	if err := a.assignProjectColors(); err != nil {
		t.Fatal(err)
	}
	if again := []string{storedColor(t, a, "q0"), storedColor(t, a, "q1"), storedColor(t, a, "q2"), storedColor(t, a, "q3")}; !slices.Equal(again, want) {
		t.Fatalf("a second load changed colours: %v", again)
	}
}

// A stored palette tone or colour is the hue; one that does not parse draws
// the tone its id hashes to, never none.
func TestProjectHueStoredAndFallback(t *testing.T) {
	t.Parallel()
	a := open(t)
	a.projects = []work.Project{
		{ID: "tone", Theme: work.Theme{Color: ochazuke.PaletteToneAt(3)}},
		{ID: "hex", Theme: work.Theme{Color: "#7FB685"}},
		{ID: "bad", Theme: work.Theme{Color: "not-a-colour"}},
		{ID: "none"},
	}
	a.setProjectHues()
	if a.projectHue("tone") != ochazuke.PaletteToneAt(3) || a.projectHue("hex") != "#7FB685" {
		t.Fatalf("stored hues = %q %q", a.projectHue("tone"), a.projectHue("hex"))
	}
	for _, id := range []string{"bad", "none"} {
		if h := a.projectHue(id); !ochazuke.ValidHue(h) || h == "" {
			t.Fatalf("%s fell back to %q", id, h)
		}
	}
	if a.projectHue("") != "" || a.projectHue("nope") != "" {
		t.Fatal("no project has a hue")
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
