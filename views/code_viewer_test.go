package views

import (
	"fmt"
	"strings"
	"testing"

	"ofam.dev/ochazuke/ui"
)

func sampleGoFile() string {
	var lines []string
	lines = append(lines,
		"package sample",
		"",
		"import \"fmt\"",
		"",
		"func alpha(x int) int {",
		"    total := x + 1",
		"    return total * 2",
		"}",
		"",
		"func beta(name string) string {",
		"    msg := fmt.Sprintf(\"hello %s\", name)",
		"    return msg",
		"}",
	)
	for i := len(lines) + 1; i <= 50; i++ {
		lines = append(lines, fmt.Sprintf("// filler line %02d alpha beta", i))
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestCodeViewerVimMotions(t *testing.T) {
	st := NewCodeViewerState("/repo/sample.go", "sample.go", sampleGoFile(), nil)
	st.ViewportH = 10

	if st.CursorLine != 1 || st.CursorCol != 0 || st.LineCount() != 50 {
		t.Fatalf("initial state: line=%d col=%d total=%d", st.CursorLine, st.CursorCol, st.LineCount())
	}

	// j / k / counts.
	st.MoveDown(4) // line 5: "func alpha(x int) int {"
	if st.CursorLine != 5 {
		t.Fatalf("after 4j: line=%d want 5", st.CursorLine)
	}
	st.MoveUp(2)
	if st.CursorLine != 3 {
		t.Fatalf("after 2k: line=%d want 3", st.CursorLine)
	}

	// h / l / 0 / ^ / $.
	st.MoveDown(3) // line 6: "    total := x + 1"
	st.MoveLineStart()
	if st.CursorCol != 0 {
		t.Fatalf("0 col=%d want 0", st.CursorCol)
	}
	st.MoveFirstNonBlank()
	if st.CursorCol != 4 {
		t.Fatalf("^ col=%d want 4", st.CursorCol)
	}
	st.MoveRight(2)
	if st.CursorCol != 6 {
		t.Fatalf("2l col=%d want 6", st.CursorCol)
	}
	st.MoveLeft(1)
	if st.CursorCol != 5 {
		t.Fatalf("h col=%d want 5", st.CursorCol)
	}
	st.MoveLineEnd()
	wantEnd := len([]rune(st.Lines[5])) - 1
	if st.CursorCol != wantEnd {
		t.Fatalf("$ col=%d want %d", st.CursorCol, wantEnd)
	}

	// w / e / b on line 5 ("func alpha(x int) int {").
	st.MoveTop(5)
	st.MoveLineStart()
	st.MoveWordForward(1) // -> "alpha" at col 5
	if st.CursorLine != 5 || st.CursorCol != 5 {
		t.Fatalf("w: got L%d:%d want L5:5", st.CursorLine, st.CursorCol)
	}
	st.MoveWordEnd(1) // -> end of "alpha" at col 9
	if st.CursorLine != 5 || st.CursorCol != 9 {
		t.Fatalf("e: got L%d:%d want L5:9", st.CursorLine, st.CursorCol)
	}
	st.MoveWordBackward(1) // -> start of "alpha" at col 5
	if st.CursorLine != 5 || st.CursorCol != 5 {
		t.Fatalf("b: got L%d:%d want L5:5", st.CursorLine, st.CursorCol)
	}

	// gg / G / ctrl+d / ctrl+u / ctrl+f / ctrl+b.
	st.MoveBottom(0)
	if st.CursorLine != 50 {
		t.Fatalf("G: line=%d want 50", st.CursorLine)
	}
	st.MoveTop(0)
	if st.CursorLine != 1 {
		t.Fatalf("gg: line=%d want 1", st.CursorLine)
	}
	st.HalfPageDown(1) // +5 -> line 6
	if st.CursorLine != 6 {
		t.Fatalf("ctrl+d: line=%d want 6", st.CursorLine)
	}
	st.HalfPageUp(1) // -5 -> line 1
	if st.CursorLine != 1 {
		t.Fatalf("ctrl+u: line=%d want 1", st.CursorLine)
	}
	st.PageDown(1) // +8 -> line 9
	if st.CursorLine != 9 {
		t.Fatalf("ctrl+f: line=%d want 9", st.CursorLine)
	}
	st.PageUp(1) // -8 -> line 1
	if st.CursorLine != 1 {
		t.Fatalf("ctrl+b: line=%d want 1", st.CursorLine)
	}
}

func TestCodeViewerSearchAndVisualSelection(t *testing.T) {
	st := NewCodeViewerState("/repo/sample.go", "sample.go", sampleGoFile(), nil)

	// Search forward and backward (/, n, N).
	st.StartSearch()
	if !st.SearchActive {
		t.Fatal("SearchActive should be true")
	}
	st.SearchAppend("betax")
	st.SearchBackspace()
	if st.SearchDraft != "beta" {
		t.Fatalf("SearchDraft=%q want beta", st.SearchDraft)
	}
	if !st.CommitSearch() {
		t.Fatal("CommitSearch should find beta")
	}
	if st.CursorLine != 10 {
		t.Fatalf("first match line=%d want 10", st.CursorLine)
	}
	if !st.FindNext(1) || st.CursorLine != 14 {
		t.Fatalf("next match line=%d want 14", st.CursorLine)
	}
	if !st.FindNext(-1) || st.CursorLine != 10 {
		t.Fatalf("prev match line=%d want 10", st.CursorLine)
	}

	// Visual line selection (V) forward & backward, and ClearVisual.
	st.MoveTop(6)
	st.ToggleVisual(CodeVisualLine)
	st.MoveDown(2) // anchor 6, cursor 8
	s, e, active := st.SelectedRange()
	if !active || s != 6 || e != 8 {
		t.Fatalf("visual range=(%d,%d,%v) want (6,8,true)", s, e, active)
	}
	st.MoveUp(4) // anchor 6, cursor 4
	s, e, active = st.SelectedRange()
	if !active || s != 4 || e != 6 {
		t.Fatalf("backward visual range=(%d,%d,%v) want (4,6,true)", s, e, active)
	}
	st.ClearVisual()
	if st.VisualActive() {
		t.Fatal("ClearVisual should exit visual mode")
	}
}

func TestCodeViewerDistinctHighlightColorsAndDumbView(t *testing.T) {
	sourceH := CodeHighlight{
		ID:        "src-1",
		Kind:      CodeHighlightSource,
		StartLine: 5,
		EndLine:   7,
		Label:     "Mentioned in previous entry",
	}
	annotH := CodeHighlight{
		ID:        "ann-1",
		Kind:      CodeHighlightAnnotation,
		StartLine: 10,
		EndLine:   12,
		Label:     "Refactor greeting",
		Note:      "Extract formatter helper",
	}
	if sourceH.EffectiveColor() == annotH.EffectiveColor() {
		t.Fatalf("source color %q and annotation color %q must be distinct", sourceH.EffectiveColor(), annotH.EffectiveColor())
	}
	if sourceH.Style().Bg == annotH.Style().Bg || sourceH.Style().Tone == annotH.Style().Tone {
		t.Fatalf("source style %+v and annotation style %+v must render with distinct colors", sourceH.Style(), annotH.Style())
	}

	st := NewCodeViewerState("/repo/sample.go", "sample.go", sampleGoFile(), []CodeHighlight{sourceH, annotH})
	// Cursor auto-lands on the source highlight start line.
	if st.CursorLine != 5 {
		t.Fatalf("auto-jump cursor line=%d want 5", st.CursorLine)
	}
	facts := CodeViewerFacts(st)
	if facts["hasHighlightAtCursor"] != true || facts["highlightKind"] != CodeHighlightSource {
		t.Fatalf("facts on source highlight = %+v", facts)
	}

	// Render view via Ochazuke renderer and verify both source (┃) and annotation (✎) render cleanly.
	node := CodeViewerView(st, 100, 24)
	if node.Scope != ScopeCodeView || !node.Focusable {
		t.Fatalf("root node scope=%q focusable=%v", node.Scope, node.Focusable)
	}
	frame, _ := ui.Render(node, ui.DefaultDarkTheme(), CodeViewerBodyID, 100, 24)
	if !strings.Contains(frame.ANSI, "┃") || !strings.Contains(frame.ANSI, "✎") {
		t.Fatalf("expected both source (┃) and annotation (✎) markers in rendered frame:\n%s", frame.ANSI)
	}
	if !strings.Contains(frame.ANSI, "SOURCE") || !strings.Contains(frame.ANSI, "Refactor greeting") {
		t.Fatalf("expected source inspector and inline annotation tag:\n%s", frame.ANSI)
	}

	// Moving onto the annotation switches inspector and facts to annotation kind.
	st.MoveTop(10)
	facts2 := CodeViewerFacts(st)
	if facts2["hasHighlightAtCursor"] != true || facts2["highlightKind"] != CodeHighlightAnnotation {
		t.Fatalf("facts on annotation highlight = %+v", facts2)
	}
	node2 := CodeViewerView(st, 100, 24)
	frame2, _ := ui.Render(node2, ui.DefaultDarkTheme(), CodeViewerBodyID, 100, 24)
	if !strings.Contains(frame2.ANSI, "ANNOTATION") || !strings.Contains(frame2.ANSI, "Extract formatter helper") {
		t.Fatalf("expected annotation inspector details:\n%s", frame2.ANSI)
	}

	// Moving off any highlight clears hasHighlightAtCursor.
	st.MoveTop(1)
	if CodeViewerFacts(st)["hasHighlightAtCursor"] != false {
		t.Fatalf("expected hasHighlightAtCursor=false on line 1")
	}
}
