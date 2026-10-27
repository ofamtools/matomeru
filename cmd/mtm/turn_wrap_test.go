package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	xansi "github.com/charmbracelet/x/ansi"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden frames in testdata")

func pieceTexts(ps []turnPiece) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.text
	}
	return out
}

// Prose breaks at spaces, dropping them; every piece fits; the offsets
// count runes into the source line.
func TestWrapTurnLineBreaksProseAtSpaces(t *testing.T) {
	line := "  the quick brown fox jumps over the lazy dog"
	got := wrapTurnLine(line, 16)
	want := []string{"  the quick", "  brown fox", "  jumps over the", "  lazy dog"}
	if strings.Join(pieceTexts(got), "|") != strings.Join(want, "|") {
		t.Fatalf("pieces %q", pieceTexts(got))
	}
	for _, p := range got {
		if !strings.HasPrefix(strings.TrimLeft(string([]rune(line)[p.offset:]), " "), strings.TrimLeft(p.text, " ")) {
			t.Fatalf("piece %q at offset %d: line has %q there", p.text, p.offset, string([]rune(line)[p.offset:]))
		}
	}
	if got := wrapTurnLine(line, 0); len(got) != 1 || got[0].text != line {
		t.Fatalf("width 0: %q", pieceTexts(got))
	}
	if got := wrapTurnLine("  ", 10); len(got) != 1 || got[0].text != "  " {
		t.Fatalf("blank line: %q", pieceTexts(got))
	}
}

// A continuation keeps its line's indentation, so code stays under code.
func TestWrapTurnLineKeepsTheIndentation(t *testing.T) {
	got := pieceTexts(wrapTurnLine("      call(alpha, beta, gamma)", 20))
	want := []string{"      call(alpha,", "      beta, gamma)"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("pieces %q", got)
	}
}

// A URL longer than a row starts where it is and breaks at the row's end.
func TestWrapTurnLineHardBreaksALongURL(t *testing.T) {
	url := "https://example.com/a/very/long/path/without/any/spaces/at/all.html"
	got := wrapTurnLine("  See "+url, 30)
	if len(got) < 3 || !strings.HasPrefix(got[0].text, "  See https://") {
		t.Fatalf("pieces %q", pieceTexts(got))
	}
	var joined string
	for i, p := range got {
		if w := xansi.StringWidth(p.text); w > 30 {
			t.Fatalf("piece %d is %d wide: %q", i, w, p.text)
		}
		if i > 0 && !strings.HasPrefix(p.text, "  ") {
			t.Fatalf("piece %d lost the indent: %q", i, p.text)
		}
		joined += strings.TrimPrefix(p.text, "  ")
	}
	if joined != "See "+url {
		t.Fatalf("joined %q", joined)
	}
}

// Wide characters take two cells: no piece is wider than the row, and a
// row ends early rather than splitting one.
func TestWrapTurnLineMeasuresCellWidth(t *testing.T) {
	line := "  日本語の文章も正しく折り返されるべきです。🙂👍🏽 done"
	for _, width := range []int{9, 10, 11, 20} {
		got := wrapTurnLine(line, width)
		var joined string
		for i, p := range got {
			if w := xansi.StringWidth(p.text); w > width {
				t.Fatalf("width %d: piece %d is %d wide: %q", width, i, w, p.text)
			}
			joined += strings.TrimPrefix(p.text, "  ")
		}
		if strings.ReplaceAll(joined, " ", "") != strings.ReplaceAll(strings.TrimSpace(line), " ", "") {
			t.Fatalf("width %d lost text: %q", width, pieceTexts(got))
		}
	}
	if got := wrapTurnLine("  日本語の文章", 8); len(got) != 2 || got[0].text != "  日本語" || got[1].text != "  の文章" || got[1].offset != 5 {
		t.Fatalf("pieces %q %+v", pieceTexts(got), got)
	}
}

// wrapApp is the Panes page on the Turns tab with the first turn's long
// answer opened in the tree.
func wrapApp(t *testing.T, answer string) *App {
	t.Helper()
	path := seeded(t)
	capturedTurns(t, path, answer, "second answer")
	app := openWith(t, Options{Data: path})
	f := app.panesd.(*fakePanes)
	f.panes[0].Harness, f.panes[0].AgentState = "claude-code", "idle"
	reload(t, app)
	settle(app, key(t, app, "[")) // Screen wraps back to Turns
	app.m.Width, app.m.Height = 120, 40
	plainView(app)
	press(t, app, "}", "l")
	plainView(app)
	return app
}

// answerRows are the tree's rows of the opened turn's answer line, in order.
func answerRows(app *App, word string) []string {
	var out []string
	for _, it := range app.pv.turns.List().Items() {
		if it.Noun == "turn-line" && (strings.Contains(it.Label, word) || len(out) > 0 && len(it.Cells) > 0 && strings.Contains(it.Cells[0], word)) {
			out = append(out, it.Label)
		}
	}
	return out
}

// A long answer wraps to the detail's width and re-wraps when the width
// changes, the turn still open, the selection on the same part of the
// same line and the turn still reviewed; j and k move over the pieces;
// the filter finds a phrase split across pieces.
func TestALongAnswerWrapsAndRewrapsAtTheDetailWidth(t *testing.T) {
	words := strings.Repeat("lorem ipsum dolor sit amet ", 12) + "ENDWORD"
	app := wrapApp(t, words)
	at120 := answerRows(app, "lorem")
	if len(at120) < 3 {
		t.Fatalf("120 columns: %d rows %q", len(at120), at120)
	}
	fr := plainView(app)
	if !strings.Contains(fr, "ENDWORD") || strings.Contains(fr, "lorem…") {
		t.Fatalf("120 columns cut the answer:\n%s", fr)
	}
	// Select the answer's last piece.
	var last string
	for _, it := range app.pv.turns.List().Items() {
		if strings.Contains(it.Label, "ENDWORD") {
			last = it.ID
		}
	}
	if err := app.pv.turns.Select(last); err != nil {
		t.Fatal(err)
	}
	if err := app.pv.turns.Focus(); err != nil {
		t.Fatal(err)
	}
	before, _ := app.pv.turns.Selected()
	was := before.Data.(turnRef)
	app.m.Width = 80 // the detail stacks under the list, narrower
	fr = plainView(app)
	at80 := answerRows(app, "lorem")
	if len(at80) <= len(at120) {
		t.Fatalf("80 columns: %d rows, 120: %d", len(at80), len(at120))
	}
	for _, row := range at80 {
		if w := xansi.StringWidth(row); w > 80-6 { // the panel's inner width; a row holds the tree's indent
			t.Fatalf("row %q is %d wide", row, w)
		}
	}
	// The selection is the piece holding where the selected one started.
	sel, ok := app.pv.turns.Selected()
	now, _ := sel.Data.(turnRef)
	if !ok || app.r.Focus != sel.ID || now.Line != was.Line || now.Offset > was.Offset {
		t.Fatalf("selection after re-wrap: %+v (was %+v) focus %s", sel, was, app.r.Focus)
	}
	for _, it := range app.pv.turns.List().Items() {
		if ref, _ := it.Data.(turnRef); it.Noun == "turn-line" && ref.Line == was.Line && ref.Offset > now.Offset && ref.Offset <= was.Offset {
			t.Fatalf("piece %+v holds offset %d, not the selected %+v", ref, was.Offset, now)
		}
	}
	if !strings.Contains(fr, "ENDWORD") || strings.Contains(fr, "lorem…") {
		t.Fatalf("80 columns:\n%s", fr)
	}
	turns, _ := app.hist.Turns("w1")
	if app.pv.turns.Collapsed("turn:"+turns[0].ID) || turns[0].ReviewedAt == 0 {
		t.Fatalf("the turn closed or lost its review mark: %+v", turns[0])
	}
	// k walks back up the pieces, one row each.
	press(t, app, "k")
	if s, _ := app.pv.turns.Selected(); s.ID == sel.ID || s.Noun != "turn-line" {
		t.Fatalf("k: %+v", s)
	}
	app.m.Width = 120
	plainView(app)
	if got := answerRows(app, "lorem"); len(got) != len(at120) {
		t.Fatalf("back at 120: %d rows, want %d", len(got), len(at120))
	}
	// The filter matches the source line whole: a phrase split across two
	// pieces shows every piece of its line.
	var split string
	rows := answerRows(app, "lorem")
	for i := 0; i+1 < len(rows); i++ {
		a, b := strings.Fields(rows[i]), strings.Fields(rows[i+1])
		split = a[len(a)-1] + " " + b[0]
		break
	}
	app.pv.turns.List().SetFilter(split)
	if n := app.pv.turns.List().Shown(); n != len(rows) {
		t.Fatalf("filter %q shows %d rows, want the line's %d", split, n, len(rows))
	}
}

// The Review page's turns tree wraps at its width too.
func TestTheReviewTurnsWrap(t *testing.T) {
	app := wrapApp(t, strings.Repeat("lorem ipsum dolor sit amet ", 12)+"ENDWORD")
	st := app.turnFillOf(app.pv.turns)
	if st.width <= 0 {
		t.Fatalf("panes fill width %d", st.width)
	}
	if got := app.turnFillOf(app.reviewTurns).width; got != 0 {
		t.Fatalf("review fill width before it is drawn: %d", got)
	}
	app.wrapTurns(app.reviewTurns, 50)
	if got := app.turnFillOf(app.reviewTurns).width; got != 50-turnLineIndent {
		t.Fatalf("review fill width %d", got)
	}
}

// Golden frames of an opened turn whose answer holds prose, an indented
// code line, a blank line, a long URL and Japanese, at 80 and 120
// columns. go test -run TestOpenedTurnFrames -update rewrites them.
func TestOpenedTurnFrames(t *testing.T) {
	answer := "This answer is a long paragraph of prose that the agent wrote, which should wrap onto several rows at the panel width instead of being cut off with an ellipsis at the edge.\n" +
		"    func indented() { return someLongFunctionName(withArguments, andMoreArguments, andEvenMoreArguments) }\n" +
		"\n" +
		"See https://example.com/a/very/long/path/that/does/not/have/any/spaces/in/it/at/all/so/it/must/hard/break/somewhere.html\n" +
		"日本語の文章も正しく折り返されるべきです。全角文字は二つのセルを占めるので、幅の計算に注意が必要です。"
	app := wrapApp(t, answer)
	app.loc = time.UTC // this app's zone, never the process-global time.Local
	for _, width := range []int{80, 120} {
		app.m.Width, app.m.Height = width, 34
		got := plainView(app)
		file := filepath.Join("testdata", "turn-wrap-"+map[int]string{80: "80", 120: "120"}[width]+".golden")
		if *updateGolden {
			if err := os.MkdirAll("testdata", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if got != string(want) {
			t.Fatalf("%d columns differ from %s:\n%s", width, file, got)
		}
	}
}
