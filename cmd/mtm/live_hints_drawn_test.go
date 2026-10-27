package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// drawnLabel reports whether the rendered page shows label over the first
// cells of raw (a mark replaces the cells it covers).
func drawnLabel(view, label, raw string) bool {
	return strings.Contains(view, label+raw[len(label):])
}

// tallRows is a screen of rows lines: the first holds first, the last
// holds last, the rest are blank. A pane that has not taken the size mtm
// draws it at (another client holds it larger) sends such a frame.
func tallRows(rows int, first, last string) []string {
	lines := make([]string, rows)
	lines[0], lines[rows-1] = first, last
	return padRows(lines...)
}

// #1 (owner, 2026-10-27: "it only does the main"): hints labels only what
// is drawn. A pane's frame can be taller (and wider) than the region mtm
// draws it in, and Terminal.ViewBlock draws only the top left of it: an
// item below the drawn rows used to take a label that was drawn nowhere,
// so a pane whose items were all off its drawn rows (a vim side's
// statusline at the bottom of a taller screen) showed no label at all.
// Now every label is on a drawn cell, of both panes, in both layouts and
// from either pane, as the rendered page shows.
func TestHintsLabelOnlyWhatIsDrawn(t *testing.T) {
	const (
		mainHidden = "https://example.com/main-hidden"
		sideHidden = "https://example.com/side-hidden"
	)
	for _, layout := range []string{"below", "beside"} {
		for _, from := range []string{paneTermID, sideTermID} {
			name := layout + " from " + from
			app, main, side, _ := hintsSideApp(t, tallRows(60, "open "+mainURL, "x "+mainHidden), tallRows(60, "side "+sideURL, "x "+sideHidden))
			if err := app.r.Settings().Set("side-layout", layout); err != nil {
				t.Fatal(err)
			}
			if err := app.r.SetFocus(from); err != nil {
				t.Fatal(err)
			}
			plainView(app)
			if _, rows := app.r.Terminal(sideTermID).Size(); rows >= 60 || rows == 0 {
				t.Fatalf("%s: side drawn %d rows", name, rows)
			}
			pressAll(t, app, "ctrl+a", "f")
			if app.r.Mode.Name != hintsMode || len(app.hinting.screens) != 2 || len(app.hinting.spans) != 2 {
				t.Fatalf("%s: mode %s spans %v", name, app.r.Mode.Name, app.hinting.spans)
			}
			view := plainView(app)
			for _, raw := range []string{mainURL, sideURL} {
				if l := labelOf(t, app, raw); !drawnLabel(view, l, raw) {
					t.Fatalf("%s: %s's label %q is not drawn:\n%s", name, raw, l, view)
				}
			}
			for _, raw := range []string{mainHidden, sideHidden} {
				for _, s := range app.hinting.spans {
					if s.Raw == raw {
						t.Fatalf("%s: %s is not drawn but is labelled", name, raw)
					}
				}
			}
			pressAll(t, app, "esc")
			if app.hinting != nil || app.r.Focus != from || len(main.sent)+len(side.sent) != 0 {
				t.Fatalf("%s: esc: hinting %v focus %s", name, app.hinting != nil, app.r.Focus)
			}
		}
	}
}

// vimScreen is a side vim's screen: code, tildes past the end of the
// buffer, and a statusline naming the file relative to vim's folder.
func vimScreen(rows int, status string) []string {
	lines := []string{"package main", "", "import (", `	"github.com/unixsuperhero/ochazuke"`, ")", "", "func main() {}"}
	for len(lines) < rows-2 {
		lines = append(lines, "~")
	}
	return padRows(append(lines, status, "")...)
}

// A vim side (ctrl+a v): its statusline path is labelled when it names a
// file in the side pane's folder, and drawn over the statusline, beside
// the main pane's labels; a vim screen with nothing to label (no path on
// it) says so beside the main pane's labels.
func TestHintsLabelAVimSide(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "cmd", "mtm"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmd", "mtm", "live_hints.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	const status = "cmd/mtm/live_hints.go [+]                       12,3           Top"
	mainRows, _ := sideScreens()
	app, _, _, _ := hintsSideAppIn(t, mainRows, vimScreen(15, status), dir)
	pressAll(t, app, "ctrl+a", "f")
	label := labelOf(t, app, "cmd/mtm/live_hints.go")
	if view := plainView(app); !drawnLabel(view, label, "cmd/mtm/live_hints.go [+]") || !drawnLabel(view, labelOf(t, app, mainURL), mainURL) {
		t.Fatalf("vim side: labels not drawn:\n%s", view)
	}
	for i, s := range app.hinting.spans {
		if s.Raw == "cmd/mtm/live_hints.go" && (app.hinting.in[i] != 1 || s.Item.Text != filepath.Join(dir, "cmd/mtm/live_hints.go")) {
			t.Fatalf("statusline path: screen %d item %+v", app.hinting.in[i], s.Item)
		}
	}
	pressAll(t, app, "esc")

	// The same screen in a folder without the file: nothing on the side.
	app, _, _, _ = hintsSideAppIn(t, mainRows, vimScreen(15, status), t.TempDir())
	app.Update(keyMsg("ctrl+a"))
	app.Update(keyMsg("f"))
	if app.r.Mode.Name != hintsMode || len(app.hinting.screens) != 1 || !strings.Contains(app.m.Status, "nothing to label on the side") {
		t.Fatalf("vim side with nothing: mode %s status %q", app.r.Mode.Name, app.m.Status)
	}
}

// #2 (owner, 2026-10-27: "q doesn't work when called from a side pane"):
// q closes and goes back everywhere in mtm (keys.go), but in hints it was
// a label letter, so q did nothing and the labels stayed up. Now q
// cancels, from either pane, and no label holds a q (ctrl+q and Q do not
// open or type either).
func TestHintsQCancels(t *testing.T) {
	var mainLines, sideLines []string
	for i := range 12 {
		mainLines = append(mainLines, "https://example.com/m"+string(rune('a'+i))+" https://example.com/n"+string(rune('a'+i)))
		sideLines = append(sideLines, "https://example.com/s"+string(rune('a'+i)))
	}
	for _, from := range []string{sideTermID, paneTermID} {
		app, main, side, sys := hintsSideApp(t, padRows(mainLines...), padRows(sideLines...))
		if err := app.r.SetFocus(from); err != nil {
			t.Fatal(err)
		}
		pressAll(t, app, "ctrl+a", "f")
		if len(app.hinting.labels) != 36 {
			t.Fatalf("from %s: %d labels", from, len(app.hinting.labels))
		}
		for _, l := range app.hinting.labels {
			if strings.Contains(l, "q") {
				t.Fatalf("from %s: label %q holds q", from, l)
			}
		}
		pressAll(t, app, "ctrl+q", "Q")
		if app.hinting == nil || len(sys.copied)+len(main.pasted)+len(side.pasted) != 0 {
			t.Fatalf("from %s: ctrl+q or Q acted", from)
		}
		pressAll(t, app, "q")
		if app.hinting != nil || app.r.Mode.Name != "terminal" || app.r.Focus != from {
			t.Fatalf("from %s: q: hinting %v mode %s focus %s", from, app.hinting != nil, app.r.Mode.Name, app.r.Focus)
		}
		for _, id := range []string{paneTermID, sideTermID} {
			if app.r.Terminal(id).Overlay() != nil {
				t.Fatalf("from %s: q left %s's overlay", from, id)
			}
		}
		if len(main.sent)+len(side.sent)+len(sys.copied) != 0 {
			t.Fatalf("from %s: q reached a pane: %v %v", from, main.sent, side.sent)
		}
		// Live again: q reaches the pane.
		pressAll(t, app, "q")
		got := main.sent
		if from == sideTermID {
			got = side.sent
		}
		if !slices.EqualFunc(got, [][]string{{"q"}}, slices.Equal) {
			t.Fatalf("from %s: after: sent %v", from, got)
		}
	}
}
