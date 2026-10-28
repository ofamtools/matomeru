package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

const (
	rowW1     = "pane:matomeru/w1"
	rowURL    = "https://example.com/preview"
	rowShared = "https://example.com/again"
)

// rowHintsApp is the Panes page on w1's row, not attached, its Screen tab
// showing the captured screen: a URL, a path and a URL twice.
func rowHintsApp(t *testing.T) (*App, *fakePanes, *fakeSystem, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	app, f := panesApp(t)
	sys := &fakeSystem{}
	app.cx.sys = sys
	app.m.Width, app.m.Height = 140, 40
	f.screens["w1"] = strings.Join([]string{"$ open " + rowURL, "edit " + path, "see " + rowShared + " and " + rowShared, "$ "}, "\n")
	settle(app, follow(app))
	if view := plainView(app); !strings.Contains(view, rowURL) || app.r.Focus != rowW1 || app.r.Terminal(paneTermID) != nil {
		t.Fatalf("no preview on the row:\n%s", view)
	}
	return app, f, sys, path
}

// startRowHints presses f on the row and checks the labels are up over
// the preview with the focus still on the row.
func startRowHints(t *testing.T, app *App) string {
	t.Helper()
	pressAll(t, app, "f")
	if app.r.Mode.Name != hintsMode || app.r.Focus != rowW1 || app.hinting == nil || app.hinting.row != rowW1 {
		t.Fatalf("f on the row: mode %s focus %s", app.r.Mode.Name, app.r.Focus)
	}
	return plainView(app)
}

// rowCleared fails unless the labels are gone and the row is in normal
// mode with the focus.
func rowCleared(t *testing.T, app *App, after string) {
	t.Helper()
	if app.hinting != nil || app.r.Mode.Name != "normal" || app.r.Focus != rowW1 {
		t.Fatalf("%s: hinting %v mode %s focus %s", after, app.hinting != nil, app.r.Mode.Name, app.r.Focus)
	}
	if view := plainView(app); !strings.Contains(view, rowShared) {
		t.Fatalf("%s: preview still marked:\n%s", after, view)
	}
}

// #3 (owner, 2026-10-27): f on a pane row labels the row's captured screen
// in the Screen tab, drawn over the preview, the focus staying on the row;
// the URL twice shares one label.
func TestRowHintsLabelThePreview(t *testing.T) {
	app, f, _, path := rowHintsApp(t)
	view := startRowHints(t, app)
	if len(app.hinting.spans) != 4 || labelOf(t, app, rowShared) == "" {
		t.Fatalf("spans %v", app.hinting.spans)
	}
	for _, raw := range []string{rowURL, path, rowShared} {
		// The panel cuts the long path: its first cells are what is drawn.
		if l := labelOf(t, app, raw); !drawnLabel(view, l, raw[:min(len(raw), 30)]) {
			t.Fatalf("%s's label %q is not drawn:\n%s", raw, l, view)
		}
	}
	if !strings.Contains(view, "esc cancel") {
		t.Fatalf("footer:\n%s", view)
	}
	pressAll(t, app, "esc")
	rowCleared(t, app, "esc")
	if len(f.sentText) != 0 {
		t.Fatalf("esc typed %v", f.sentText)
	}
}

// A label copies, ctrl+label opens and a capital types the item into the
// row's pane through the daemon, without attaching it.
func TestRowHintsActions(t *testing.T) {
	app, f, sys, path := rowHintsApp(t)
	startRowHints(t, app)
	pressAll(t, app, labelOf(t, app, rowURL))
	if !slices.Equal(sys.copied, []string{rowURL}) {
		t.Fatalf("copy: %v", sys.copied)
	}
	rowCleared(t, app, "copy")

	startRowHints(t, app)
	pressAll(t, app, strings.ToUpper(labelOf(t, app, path)))
	if !slices.Equal(f.sentText, []string{"w1 " + path}) || app.r.Terminal(paneTermID) != nil {
		t.Fatalf("type: sent %v attached %v", f.sentText, app.r.Terminal(paneTermID) != nil)
	}
	rowCleared(t, app, "type")

	startRowHints(t, app)
	pressAll(t, app, "ctrl+"+labelOf(t, app, rowURL))
	if !slices.Equal(sys.opened, []string{rowURL}) {
		t.Fatalf("open: %v", sys.opened)
	}
	rowCleared(t, app, "open")
}

// While the labels are up every other key is swallowed: the selection
// does not move, nothing attaches, q cancels.
func TestRowHintsSwallowAndCancel(t *testing.T) {
	app, f, sys, _ := rowHintsApp(t)
	startRowHints(t, app)
	for _, k := range []string{"j", "enter", "tab", "1", "]"} {
		pressAll(t, app, k)
		if app.r.Mode.Name != hintsMode || app.r.Focus != rowW1 || app.r.Terminal(paneTermID) != nil || app.pv.tab != tabScreen {
			t.Fatalf("%q: mode %s focus %s", k, app.r.Mode.Name, app.r.Focus)
		}
	}
	app.Update(tea.PasteMsg{Content: "pasted"})
	if app.hinting == nil || len(f.sentText)+len(sys.copied) != 0 {
		t.Fatalf("swallowed: sent %v copied %v", f.sentText, sys.copied)
	}
	for _, cancel := range []string{"q", "ctrl+c"} {
		if app.hinting == nil {
			startRowHints(t, app)
		}
		pressAll(t, app, cancel)
		rowCleared(t, app, cancel)
		if app.r.Quit {
			t.Fatalf("%s quit", cancel)
		}
	}
	// A focus change from outside the keys ends them too.
	startRowHints(t, app)
	if err := app.r.SetFocus("pane:ochazuke/w2"); err != nil {
		t.Fatal(err)
	}
	app.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	if app.hinting != nil || app.r.Mode.Name == hintsMode {
		t.Fatalf("focus change: hinting %v mode %s", app.hinting != nil, app.r.Mode.Name)
	}
}

// A row whose screen is not captured yet says so and stays put; off the
// Screen tab f does not answer.
func TestRowHintsWithNoScreen(t *testing.T) {
	app, _, _, _ := rowHintsApp(t)
	// This test reads the session rows (project → session → pane).
	groupBy(t, app, groupProjectSessionPane)
	pressAll(t, app, "j", "j") // the ochazuke session's shell; no follow yet
	if app.r.Focus != "pane:ochazuke/w2" {
		t.Fatalf("focus %s", app.r.Focus)
	}
	plainView(app)
	app.Update(keyMsg("f"))
	if !strings.Contains(app.m.Info, "no screen for this pane yet") || app.r.Mode.Name != "normal" || app.hinting != nil {
		t.Fatalf("no screen: status %q mode %s", app.m.Info, app.r.Mode.Name)
	}

	app, _, _, _ = rowHintsApp(t)
	pressAll(t, app, "]")
	plainView(app)
	app.Update(keyMsg("f"))
	if app.hinting != nil || app.r.Mode.Name != "normal" {
		t.Fatalf("Activity tab: mode %s", app.r.Mode.Name)
	}
}

// With the live panes drawn while the focus is on the row (the Screen
// tab shows them), f labels both, the focus staying on the row; a capital
// on an item on both types into the main pane.
func TestRowHintsLabelTheLivePanes(t *testing.T) {
	mainRows, sideRows := sideScreens()
	app, main, side, _ := hintsSideApp(t, mainRows, sideRows)
	row := app.attached.rowID()
	if err := app.r.SetFocus(row); err != nil {
		t.Fatal(err)
	}
	plainView(app)
	if !app.liveShown() {
		t.Fatal("the live panes are not drawn from the row")
	}
	pressAll(t, app, "f")
	if app.r.Mode.Name != hintsMode || app.r.Focus != row || len(app.hinting.screens) != 2 {
		t.Fatalf("f: mode %s focus %s", app.r.Mode.Name, app.r.Focus)
	}
	view := plainView(app)
	for _, raw := range []string{mainURL, sideURL} {
		if l := labelOf(t, app, raw); !drawnLabel(view, l, raw) {
			t.Fatalf("%s's label %q is not drawn:\n%s", raw, l, view)
		}
	}
	pressAll(t, app, strings.ToUpper(labelOf(t, app, sharedURL)))
	if !slices.Equal(main.pasted, []string{sharedURL}) || len(side.pasted) != 0 || len(main.sent)+len(side.sent) != 0 {
		t.Fatalf("type shared: main %v side %v", main.pasted, side.pasted)
	}
	if app.r.Focus != row || app.r.Mode.Name != "normal" || app.r.Terminal(paneTermID).Overlay() != nil || app.r.Terminal(sideTermID).Overlay() != nil {
		t.Fatalf("after type: focus %s mode %s", app.r.Focus, app.r.Mode.Name)
	}
}

// A captured screen longer than the panel shows its end (and a position
// marker): only the lines drawn are labelled, each label on its own row.
func TestRowHintsLabelTheDrawnWindow(t *testing.T) {
	app, f := panesApp(t)
	app.cx.sys = &fakeSystem{}
	app.m.Width, app.m.Height = 140, 40
	lines := []string{"top " + rowURL}
	for i := range 80 {
		lines = append(lines, "line "+string(rune('a'+i%26)))
	}
	lines = append(lines, "end "+rowShared, "$ ")
	f.screens["w1"] = strings.Join(lines, "\n")
	settle(app, follow(app))
	if view := plainView(app); strings.Contains(view, rowURL) || !strings.Contains(view, rowShared) {
		t.Fatalf("the preview does not show its end:\n%s", view)
	}
	view := startRowHints(t, app)
	if len(app.hinting.spans) != 1 || app.hinting.spans[0].Raw != rowShared {
		t.Fatalf("spans %v", app.hinting.spans)
	}
	if !drawnLabel(view, labelOf(t, app, rowShared), rowShared) {
		t.Fatalf("label not drawn:\n%s", view)
	}
	pressAll(t, app, "esc")
	rowCleared(t, app, "esc")
}
