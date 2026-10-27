package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/hints"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// hintsWidth is the fake pane's columns: every frame row is padded to it,
// as the emulator pads them.
const hintsWidth = 120

// padRows pads each row to the pane's width.
func padRows(rows ...string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r + strings.Repeat(" ", max(0, hintsWidth-len(r)))
	}
	return out
}

// hintsApp is the Panes page with w1 attached live, its screen the rows
// given (none: no frame yet), and the fake clipboard and opener.
func hintsApp(t *testing.T, rows []string) (*App, *fakeStream, *fakeSystem) {
	t.Helper()
	app, _, s, sys := hintsPanesApp(t, rows)
	return app, s, sys
}

// hintsPanesApp is hintsApp with the fake daemon too.
func hintsPanesApp(t *testing.T, rows []string) (*App, *fakePanes, *fakeStream, *fakeSystem) {
	t.Helper()
	app, f := panesApp(t)
	sys := &fakeSystem{}
	app.cx.sys = sys
	app.m.Width, app.m.Height = 140, 40
	settle(app, follow(app))
	plainView(app)
	cmd := key(t, app, "enter")
	s := f.streams[len(f.streams)-1]
	if rows != nil {
		s.frames <- ochazuke.TerminalFrame{Lines: rows, Cursor: ochazuke.Pos{X: 0, Y: 0}}
	}
	runOnce(app, cmd)
	live(t, app, "enter")
	return app, f, s, sys
}

// screenRows is a screen with a URL, a path, the same URL again and a URL
// soft-wrapped across two rows; it returns the path and the wrapped URL.
func screenRows(t *testing.T) ([]string, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	wrapped := "https://example.com/" + strings.Repeat("w", 110) + "/tail"
	lead := "fetch "
	first := lead + wrapped[:hintsWidth-len(lead)]
	rows := padRows(
		"open https://example.com/one now",
		"file "+path,
		"again https://example.com/one",
		first,
		wrapped[hintsWidth-len(lead):]+" done",
	)
	rows[3] = first // exactly the width: it continues on the next row
	return rows, path, wrapped
}

// labelOf is the label the session gave the item that reads raw.
func labelOf(t *testing.T, app *App, raw string) string {
	t.Helper()
	for i, s := range app.hinting.spans {
		if s.Raw == raw {
			return app.hinting.labels[i]
		}
	}
	t.Fatalf("no span %q in %v", raw, app.hinting.spans)
	return ""
}

// startHints presses ctrl+a f and checks the labels are up.
func startHints(t *testing.T, app *App, s *fakeStream) *ochazuke.TerminalOverlay {
	t.Helper()
	pressAll(t, app, "ctrl+a", "f")
	if app.r.Mode.Name != hintsMode || app.r.Focus != paneTermID || app.hinting == nil {
		t.Fatalf("ctrl+a f: mode %s focus %s", app.r.Mode.Name, app.r.Focus)
	}
	o := app.r.Terminal(paneTermID).Overlay()
	if o == nil || !o.Dim || len(o.Marks) == 0 {
		t.Fatalf("ctrl+a f: overlay %+v", o)
	}
	if len(s.sent) != 0 || len(s.pasted) != 0 {
		t.Fatalf("ctrl+a f reached the pane: sent %v pasted %v", s.sent, s.pasted)
	}
	return o
}

// cleared fails unless the labels are gone and the pane is live again.
func cleared(t *testing.T, app *App, after string) {
	t.Helper()
	if o := app.r.Terminal(paneTermID).Overlay(); o != nil || app.hinting != nil {
		t.Fatalf("%s: overlay %+v hinting %v", after, o, app.hinting != nil)
	}
	live(t, app, after)
}

// ctrl+a f labels every item: one label per item (the same URL twice
// shares one), drawn at each occurrence's first cell, the wrapped URL as
// one item on its first row.
func TestHintsLabelTheScreen(t *testing.T) {
	rows, path, wrapped := screenRows(t)
	app, s, _ := hintsApp(t, rows)
	o := startHints(t, app, s)
	one := labelOf(t, app, "https://example.com/one")
	labelOf(t, app, path)
	long := labelOf(t, app, wrapped)
	at := map[string][]string{}
	for _, m := range o.Marks {
		at[m.Text] = append(at[m.Text], fmt.Sprintf("%d:%d", m.Row, m.Col))
	}
	if got := at[one]; !slices.Equal(got, []string{"0:5", "2:6"}) {
		t.Fatalf("the URL twice: marks %v (all %v)", got, at)
	}
	if got := at[long]; !slices.Equal(got, []string{"3:6"}) {
		t.Fatalf("the wrapped URL: marks %v", got)
	}
	if len(at) != 3 {
		t.Fatalf("labels %v", at)
	}
	// Footer hints come from the rows.
	footer := plainView(app)
	for _, want := range []string{"copy", "type", "open", "esc cancel"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer lacks %q:\n%s", want, footer)
		}
	}
}

// A label copies the item as it reads on the screen; ctrl and the label
// opens the resolved item; a capital or shift and the letter types it
// into the pane, exactly.
func TestHintActions(t *testing.T) {
	rows, path, wrapped := screenRows(t)
	for _, c := range []struct {
		name string
		key  func(label string) tea.KeyPressMsg
		raw  string
		want func(s *fakeStream, sys *fakeSystem) []string
	}{
		{"copy", func(l string) tea.KeyPressMsg { return keyMsg(l) }, wrapped, func(_ *fakeStream, sys *fakeSystem) []string { return sys.copied }},
		{"open", func(l string) tea.KeyPressMsg { return keyMsg("ctrl+" + l) }, path, func(_ *fakeStream, sys *fakeSystem) []string { return sys.opened }},
		{"type", func(l string) tea.KeyPressMsg { return keyMsg(strings.ToUpper(l)) }, "https://example.com/one", func(s *fakeStream, _ *fakeSystem) []string { return s.pasted }},
		{"shift", func(l string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: rune(l[0]), Mod: tea.ModShift} }, path, func(s *fakeStream, _ *fakeSystem) []string { return s.pasted }},
	} {
		app, s, sys := hintsApp(t, rows)
		startHints(t, app, s)
		_, cmd := app.Update(c.key(labelOf(t, app, c.raw)))
		settle(app, cmd)
		if app.m.Status != "" {
			t.Fatalf("%s: status %q", c.name, app.m.Status)
		}
		if got := c.want(s, sys); !slices.Equal(got, []string{c.raw}) {
			t.Fatalf("%s: got %q, want %q", c.name, got, c.raw)
		}
		if len(s.sent) != 0 {
			t.Fatalf("%s: keys reached the pane: %v", c.name, s.sent)
		}
		cleared(t, app, c.name)
	}
}

// With more items than letters, labels take two letters: the first
// narrows the marks to the labels it starts, backspace takes it back, and
// the second acts with the strongest modifier on either letter.
func TestHintsTwoLetterLabels(t *testing.T) {
	var lines []string
	for i := range 30 {
		lines = append(lines, fmt.Sprintf("https://example.com/item%d", i))
	}
	app, s, sys := hintsApp(t, padRows(lines...))
	o := startHints(t, app, s)
	all := len(o.Marks)
	label := ""
	for _, l := range app.hinting.labels {
		if len(l) == 2 {
			label = l
			break
		}
	}
	if all != 30 || label == "" {
		t.Fatalf("marks %d, labels %v", all, app.hinting.labels)
	}
	pressAll(t, app, label[:1])
	narrowed := app.r.Terminal(paneTermID).Overlay().Marks
	if len(narrowed) == 0 || len(narrowed) >= all || app.r.Mode.Name != hintsMode {
		t.Fatalf("first letter: %d of %d marks, mode %s", len(narrowed), all, app.r.Mode.Name)
	}
	for _, m := range narrowed {
		if !strings.HasPrefix(m.Text, label[:1]) {
			t.Fatalf("mark %q does not start with %q", m.Text, label[:1])
		}
	}
	app.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if n := len(app.r.Terminal(paneTermID).Overlay().Marks); n != all || app.hinting.matcher.Typed() != "" {
		t.Fatalf("backspace: %d marks, typed %q", n, app.hinting.matcher.Typed())
	}
	// Shift on the first letter, then the plain second: it types.
	raw := app.hinting.spans[slices.Index(app.hinting.labels, label)].Raw
	pressAll(t, app, strings.ToUpper(label[:1]), label[1:])
	if !slices.Equal(s.pasted, []string{raw}) || len(sys.copied) != 0 {
		t.Fatalf("two letters: pasted %q copied %q, want %q typed", s.pasted, sys.copied, raw)
	}
	cleared(t, app, "two letters")
}

// esc and ctrl+c cancel; every other key, a paste and a letter that
// starts no label are swallowed while the labels are up.
func TestHintsCancelAndSwallow(t *testing.T) {
	rows, _, _ := screenRows(t)
	for _, cancel := range []string{"esc", "ctrl+c"} {
		app, s, sys := hintsApp(t, rows)
		startHints(t, app, s)
		for _, k := range []string{"enter", "1", "tab", "up", "z"} {
			pressAll(t, app, k)
			if app.r.Mode.Name != hintsMode {
				t.Fatalf("%q left hints: mode %s sent %v", k, app.r.Mode.Name, s.sent)
			}
		}
		app.Update(tea.KeyPressMsg{Code: tea.KeySpace, Mod: tea.ModCtrl})
		app.Update(tea.PasteMsg{Content: "pasted"})
		if len(s.sent) != 0 || len(s.pasted) != 0 || len(sys.copied) != 0 || app.r.Mode.Name != hintsMode || app.hinting == nil {
			t.Fatalf("swallowed keys: sent %v pasted %v copied %v mode %s", s.sent, s.pasted, sys.copied, app.r.Mode.Name)
		}
		pressAll(t, app, cancel)
		cleared(t, app, cancel)
		if len(s.sent) != 0 || app.r.Quit {
			t.Fatalf("%s: sent %v quit %v", cancel, s.sent, app.r.Quit)
		}
		// Live again: keys reach the pane.
		pressAll(t, app, "x")
		if !slices.EqualFunc(s.sent, [][]string{{"x"}}, slices.Equal) {
			t.Fatalf("after %s: sent %v", cancel, s.sent)
		}
	}
}

// A focus change while the labels are up (here from outside the keys)
// clears them; a detach removes the terminal and the session with it.
func TestHintsClearOnFocusChangeAndDetach(t *testing.T) {
	rows, _, _ := screenRows(t)
	app, s, _ := hintsApp(t, rows)
	startHints(t, app, s)
	if err := app.r.SetFocus("pane:matomeru/w1"); err != nil {
		t.Fatal(err)
	}
	app.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	if o := app.r.Terminal(paneTermID).Overlay(); o != nil || app.hinting != nil || app.r.Mode.Name == hintsMode {
		t.Fatalf("focus change: overlay %+v mode %s", o, app.r.Mode.Name)
	}

	app, s, _ = hintsApp(t, rows)
	startHints(t, app, s)
	if _, err := app.r.Execute(ochazuke.Request{Noun: "terminal", Verb: "detach", Args: ochazuke.Arguments{"id": paneTermID}}); err != nil {
		t.Fatal(err)
	}
	app.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	if app.hinting != nil || app.r.Mode.Name == hintsMode {
		t.Fatalf("detach: hinting %v mode %s", app.hinting != nil, app.r.Mode.Name)
	}
}

// No frame yet, or a screen with nothing on it, says so and stays live.
func TestHintsWithNothingToLabel(t *testing.T) {
	for _, c := range []struct {
		name, status string
		rows         []string
	}{
		{"no frame", "drawn nothing", nil},
		{"empty screen", "nothing to label", padRows("$ ", "")},
	} {
		app, s, _ := hintsApp(t, c.rows)
		app.Update(keyMsg("ctrl+a"))
		app.Update(keyMsg("f"))
		if !strings.Contains(app.m.Status, c.status) {
			t.Fatalf("%s: status %q", c.name, app.m.Status)
		}
		if app.hinting != nil || app.r.Terminal(paneTermID).Overlay() != nil || app.r.Mode.Name != "terminal" || len(s.sent) != 0 {
			t.Fatalf("%s: hinting %v mode %s sent %v", c.name, app.hinting != nil, app.r.Mode.Name, s.sent)
		}
	}
}

// Both live panes: the main pane and its side, each with its own items
// and one URL on both.
const (
	mainURL   = "https://example.com/main"
	sharedURL = "https://example.com/shared"
	sideURL   = "https://example.com/side"
)

// hintsSideApp is hintsApp with the side vim open beside the main pane,
// its screen sideRows, and the focus back on the main pane, live; both are
// drawn.
func hintsSideApp(t *testing.T, mainRows, sideRows []string) (*App, *fakeStream, *fakeStream, *fakeSystem) {
	t.Helper()
	return hintsSideAppIn(t, mainRows, sideRows, "")
}

// hintsSideAppIn is hintsSideApp with the side pane's folder cwd, which
// its relative paths resolve against.
func hintsSideAppIn(t *testing.T, mainRows, sideRows []string, cwd string) (*App, *fakeStream, *fakeStream, *fakeSystem) {
	t.Helper()
	app, f, main, sys := hintsPanesApp(t, mainRows)
	vim := pane{Session: app.attached.Session, Pane: daemon.Pane{ID: "side-vim", Name: "editor", Cwd: cwd}}
	f.panes = append(f.panes, vim)
	if err := app.work.PutCompanion(work.Companion{Window: vim.ID, Parent: app.attached.ID, Kind: "vim"}); err != nil {
		t.Fatal(err)
	}
	app.setPanes(f.panes, nil)
	if err := app.showSide(app.attached, "vim"); err != nil {
		t.Fatal(err)
	}
	side := f.streams[len(f.streams)-1]
	if side == main {
		t.Fatal("no side stream")
	}
	side.frames <- ochazuke.TerminalFrame{Lines: sideRows, Cursor: ochazuke.Pos{X: 0, Y: 0}}
	runOnce(app, app.r.PendingCommands())
	if err := app.r.SetFocus(paneTermID); err != nil {
		t.Fatal(err)
	}
	if fr := plainView(app); !strings.Contains(fr, "side vim") || !strings.Contains(fr, mainRows[0][:20]) {
		t.Fatalf("both panes are not drawn:\n%s", fr)
	}
	live(t, app, "side open")
	return app, main, side, sys
}

func sideScreens() (mainRows, sideRows []string) {
	return padRows("open "+mainURL, "shared "+sharedURL), padRows("side "+sideURL, "also "+sharedURL)
}

// marksOf is a terminal's overlay marks by label.
func marksOf(t *testing.T, app *App, id string) map[string][]string {
	t.Helper()
	o := app.r.Terminal(id).Overlay()
	if o == nil || !o.Dim {
		t.Fatalf("%s: overlay %+v", id, o)
	}
	at := map[string][]string{}
	for _, m := range o.Marks {
		at[m.Text] = append(at[m.Text], fmt.Sprintf("%d:%d", m.Row, m.Col))
	}
	return at
}

// ctrl+a f labels both live panes as one set: each gets its own dimmed
// overlay, the URL on both shares one label, and the terminal it ran in
// takes the shortest labels first (from either pane).
func TestHintsLabelBothPanes(t *testing.T) {
	mainRows, sideRows := sideScreens()
	first := hints.Labels(3, hintsAlphabet)
	for _, from := range []string{paneTermID, sideTermID} {
		app, main, side, _ := hintsSideApp(t, mainRows, sideRows)
		own, other := mainURL, sideURL
		if from == sideTermID {
			if err := app.r.SetFocus(sideTermID); err != nil {
				t.Fatal(err)
			}
			own, other = sideURL, mainURL
		}
		pressAll(t, app, "ctrl+a", "f")
		if app.r.Mode.Name != hintsMode || app.r.Focus != from || app.hinting == nil || len(app.hinting.screens) != 2 {
			t.Fatalf("from %s: mode %s focus %s", from, app.r.Mode.Name, app.r.Focus)
		}
		if got := []string{labelOf(t, app, own), labelOf(t, app, sharedURL), labelOf(t, app, other)}; !slices.Equal(got, first) {
			t.Fatalf("from %s: labels %v, want %v", from, got, first)
		}
		shared := labelOf(t, app, sharedURL)
		m, s := marksOf(t, app, paneTermID), marksOf(t, app, sideTermID)
		if len(m) != 2 || len(s) != 2 || !slices.Equal(m[shared], []string{"1:7"}) || !slices.Equal(s[shared], []string{"1:5"}) {
			t.Fatalf("from %s: main marks %v side marks %v", from, m, s)
		}
		if len(m[labelOf(t, app, mainURL)]) != 1 || len(s[labelOf(t, app, sideURL)]) != 1 {
			t.Fatalf("from %s: main marks %v side marks %v", from, m, s)
		}
		if len(main.sent)+len(main.pasted)+len(side.sent)+len(side.pasted) != 0 {
			t.Fatalf("from %s: keys reached a pane", from)
		}
		pressAll(t, app, "esc")
		for _, id := range []string{paneTermID, sideTermID} {
			if o := app.r.Terminal(id).Overlay(); o != nil {
				t.Fatalf("from %s: esc left %s's overlay", from, id)
			}
		}
		if app.hinting != nil || app.r.Mode.Name != "terminal" || app.r.Focus != from {
			t.Fatalf("from %s: esc: mode %s focus %s", from, app.r.Mode.Name, app.r.Focus)
		}
	}
}

// A capital types the item into the pane it is on; an item on both goes
// to the pane hints ran in. Copy works on either pane's items.
func TestHintsTypeIntoItsPane(t *testing.T) {
	mainRows, sideRows := sideScreens()
	for _, c := range []struct {
		raw        string
		main, side []string
	}{
		{sideURL, nil, []string{sideURL}},
		{sharedURL, []string{sharedURL}, nil},
		{mainURL, []string{mainURL}, nil},
	} {
		app, main, side, sys := hintsSideApp(t, mainRows, sideRows)
		startHints(t, app, main)
		pressAll(t, app, strings.ToUpper(labelOf(t, app, c.raw)))
		if !slices.Equal(main.pasted, c.main) || !slices.Equal(side.pasted, c.side) || len(sys.copied) != 0 {
			t.Fatalf("type %s: main %q side %q copied %q", c.raw, main.pasted, side.pasted, sys.copied)
		}
		if len(main.sent) != 0 || len(side.sent) != 0 || app.r.Terminal(sideTermID).Overlay() != nil {
			t.Fatalf("type %s: sent %v %v", c.raw, main.sent, side.sent)
		}
		cleared(t, app, "type "+c.raw)
	}
	app, main, side, sys := hintsSideApp(t, mainRows, sideRows)
	startHints(t, app, main)
	pressAll(t, app, labelOf(t, app, sideURL))
	if !slices.Equal(sys.copied, []string{sideURL}) || len(side.pasted) != 0 {
		t.Fatalf("copy from the side: copied %q", sys.copied)
	}
	cleared(t, app, "copy from the side")
}

// A first letter narrows the marks on both panes; backspace restores
// both. With 48 items (two to a row, 12 rows fit the drawn part of either
// pane) the main pane's last labels and the side's first share their
// first letter.
func TestHintsPrefixNarrowsBothPanes(t *testing.T) {
	var mainLines, sideLines []string
	for i := range 12 {
		mainLines = append(mainLines, fmt.Sprintf("https://example.com/m%d https://example.com/m%d-b", i, i))
		sideLines = append(sideLines, fmt.Sprintf("https://example.com/s%d https://example.com/s%d-b", i, i))
	}
	app, main, _, _ := hintsSideApp(t, padRows(mainLines...), padRows(sideLines...))
	startHints(t, app, main)
	count := func(id string) int { return len(app.r.Terminal(id).Overlay().Marks) }
	if count(paneTermID) != 24 || count(sideTermID) != 24 {
		t.Fatalf("marks %d %d", count(paneTermID), count(sideTermID))
	}
	// A two-letter label on the side whose first letter starts labels on
	// the main pane too.
	prefix := ""
	for i, l := range app.hinting.labels {
		if len(l) == 2 && app.hinting.in[i] == 1 {
			for j, m := range app.hinting.labels {
				if app.hinting.in[j] == 0 && strings.HasPrefix(m, l[:1]) {
					prefix = l[:1]
				}
			}
		}
	}
	if prefix == "" {
		t.Fatalf("no prefix shared by both panes: %v", app.hinting.labels)
	}
	pressAll(t, app, prefix)
	for _, id := range []string{paneTermID, sideTermID} {
		marks := app.r.Terminal(id).Overlay().Marks
		if len(marks) == 0 || len(marks) >= 24 {
			t.Fatalf("%s after %q: %d marks", id, prefix, len(marks))
		}
		for _, m := range marks {
			if !strings.HasPrefix(m.Text, prefix) {
				t.Fatalf("%s: mark %q after %q", id, m.Text, prefix)
			}
		}
	}
	app.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if count(paneTermID) != 24 || count(sideTermID) != 24 {
		t.Fatalf("backspace: marks %d %d", count(paneTermID), count(sideTermID))
	}
	pressAll(t, app, "ctrl+c")
	if app.r.Terminal(sideTermID).Overlay() != nil {
		t.Fatal("ctrl+c left the side's overlay")
	}
	cleared(t, app, "ctrl+c")
}

// Zoomed, only the zoomed pane is drawn, so only it is labelled; with the
// side closed, only the main pane.
func TestHintsLabelOnlyTheDrawnPanes(t *testing.T) {
	mainRows, sideRows := sideScreens()
	app, main, _, _ := hintsSideApp(t, mainRows, sideRows)
	pressAll(t, app, "ctrl+a", "z")
	if !app.pv.zoom {
		t.Fatal("ctrl+a z did not zoom")
	}
	live(t, app, "zoom")
	startHints(t, app, main)
	if len(app.hinting.screens) != 1 || app.r.Terminal(sideTermID).Overlay() != nil {
		t.Fatalf("zoomed: screens %d side overlay %+v", len(app.hinting.screens), app.r.Terminal(sideTermID).Overlay())
	}
	if got := marksOf(t, app, paneTermID); len(got) != 2 {
		t.Fatalf("zoomed: main marks %v", got)
	}
	pressAll(t, app, "esc")
	cleared(t, app, "zoomed esc")

	app, main, _, _ = hintsSideApp(t, mainRows, sideRows)
	if err := app.closeSideTerminal(); err != nil {
		t.Fatal(err)
	}
	plainView(app)
	live(t, app, "side closed")
	startHints(t, app, main)
	if len(app.hinting.screens) != 1 || len(app.hinting.spans) != 2 {
		t.Fatalf("side closed: screens %d spans %v", len(app.hinting.screens), app.hinting.spans)
	}
	pressAll(t, app, "esc")
	cleared(t, app, "side closed esc")
}

// The side going while the labels are up ends the session: the main
// pane's overlay is cleared and it is live again.
func TestHintsEndWhenTheSideGoes(t *testing.T) {
	mainRows, sideRows := sideScreens()
	app, main, _, _ := hintsSideApp(t, mainRows, sideRows)
	startHints(t, app, main)
	if err := app.closeSideTerminal(); err != nil {
		t.Fatal(err)
	}
	app.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	cleared(t, app, "side closed mid-hints")
	pressAll(t, app, "x")
	if !slices.EqualFunc(main.sent, [][]string{{"x"}}, slices.Equal) {
		t.Fatalf("after the side went: sent %v", main.sent)
	}
}
