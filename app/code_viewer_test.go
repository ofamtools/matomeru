package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
)

func setupCodeViewerApp(t *testing.T) (*App, *Driver, string) {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := SeedDemo(store); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	srcFile := filepath.Join(dir, "engine.go")
	var lines []string
	lines = append(lines,
		"package engine",
		"",
		"// Process handles incoming events.",
		"func Process(input string) string {",
		"    trimmed := clean(input)",
		"    if trimmed == \"\" {",
		"        return \"empty\"",
		"    }",
		"    return \"ok:\" + trimmed",
		"}",
		"",
		"func clean(s string) string {",
		"    return s",
		"}",
	)
	for i := len(lines) + 1; i <= 45; i++ {
		lines = append(lines, fmt.Sprintf("// trailing comment %02d clean target", i))
	}
	if err := os.WriteFile(srcFile, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	return a, NewDriver(a, 120, 36), srcFile
}

func runCodeOpen(t *testing.T, a *App, d *Driver, args map[string]any) {
	t.Helper()
	s := a.State()
	step := a.Engine.Step(s, nil, core.CommandEvent{Command: core.Command{
		ID:     CmdCodeOpen,
		Args:   args,
		Source: "test",
		Actor:  User,
	}})
	a.Shell.State = step.State
	_ = d.Frame()
}

func TestCodeViewerCheckBindingsZeroErrors(t *testing.T) {
	rep, err := CheckBindings("")
	if err != nil {
		t.Fatalf("CheckBindings error: %v", err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings(\"\") reported %d errors: %+v", len(rep.Errors), rep.Errors)
	}
}

func TestCodeViewerVimMotionsAndSearchIntegration(t *testing.T) {
	a, d, srcFile := setupCodeViewerApp(t)
	d.Keys("2") // start on Work space

	runCodeOpen(t, a, d, map[string]any{
		"file":      srcFile,
		"startLine": 5,
		"endLine":   8,
		"label":     "Previous entry match",
	})
	if a.Mode() != ModeCodeView || a.activeCodeViewer() == nil {
		t.Fatalf("expected ModeCodeView, got mode=%q modal=%q", a.Mode(), a.Modal())
	}
	st := a.activeCodeViewer().state
	if st.CursorLine != 5 {
		t.Fatalf("initial cursor line=%d want 5", st.CursorLine)
	}

	// Test j/k/h/l/w/b/e/0/$/gg/G/ctrl+d/ctrl+u.
	d.Keys("g", "g")
	if st.CursorLine != 1 {
		t.Fatalf("gg -> line %d want 1", st.CursorLine)
	}
	d.Keys("G")
	if st.CursorLine != 45 {
		t.Fatalf("G -> line %d want 45", st.CursorLine)
	}
	d.Keys("g", "g", "j", "j", "j") // line 4: "func Process(input string) string {"
	if st.CursorLine != 4 {
		t.Fatalf("3j -> line %d want 4", st.CursorLine)
	}
	d.Keys("w") // "Process" col 5
	if st.CursorCol != 5 {
		t.Fatalf("w -> col %d want 5", st.CursorCol)
	}
	d.Keys("e") // end of "Process" col 11
	if st.CursorCol != 11 {
		t.Fatalf("e -> col %d want 11", st.CursorCol)
	}
	d.Keys("b") // back to "Process" col 5
	if st.CursorCol != 5 {
		t.Fatalf("b -> col %d want 5", st.CursorCol)
	}
	d.Keys("$")
	if st.CursorCol != len([]rune(st.Lines[3]))-1 {
		t.Fatalf("$ -> col %d want %d", st.CursorCol, len([]rune(st.Lines[3]))-1)
	}
	d.Keys("0")
	if st.CursorCol != 0 {
		t.Fatalf("0 -> col %d want 0", st.CursorCol)
	}
	d.Keys("ctrl+d")
	afterHalfDown := st.CursorLine
	if afterHalfDown <= 4 {
		t.Fatalf("ctrl+d did not advance cursor: line=%d", afterHalfDown)
	}
	d.Keys("ctrl+u")
	if st.CursorLine >= afterHalfDown {
		t.Fatalf("ctrl+u did not move cursor up: line=%d vs %d", st.CursorLine, afterHalfDown)
	}

	// Vim search /, n, N.
	d.Keys("g", "g", "/")
	if a.Mode() != ModeCodeViewSearch {
		t.Fatalf("/ did not enter ModeCodeViewSearch, mode=%q", a.Mode())
	}
	d.Type("clean")
	d.Keys("enter")
	if a.Mode() != ModeCodeView || st.CursorLine != 5 {
		t.Fatalf("search commit: mode=%q line=%d want 5", a.Mode(), st.CursorLine)
	}
	d.Keys("n")
	if st.CursorLine != 12 {
		t.Fatalf("n -> line=%d want 12", st.CursorLine)
	}
	d.Keys("N")
	if st.CursorLine != 5 {
		t.Fatalf("N -> line=%d want 5", st.CursorLine)
	}
}

func TestCodeViewerAnnotateCustomizePersistAndPromoteToTask(t *testing.T) {
	a, d, srcFile := setupCodeViewerApp(t)
	d.Keys("2")

	runCodeOpen(t, a, d, map[string]any{
		"file":      srcFile,
		"startLine": 5,
		"endLine":   7,
		"label":     "Caller hunk",
	})
	st := a.activeCodeViewer().state
	srcH, ok := st.HighlightAt(5)
	if !ok || srcH.Kind != views.CodeHighlightSource {
		t.Fatalf("expected source highlight at line 5, got %+v ok=%v", srcH, ok)
	}

	// Visual-select lines 12-14 with V + j + j, press esc to confirm esc clears visual without closing viewer.
	d.Keys("g", "g")
	for i := 1; i < 12; i++ {
		d.Keys("j")
	}
	d.Keys("V", "j", "j")
	if !st.VisualActive() {
		t.Fatal("expected visual selection active after V j j")
	}
	d.Keys("esc")
	if st.VisualActive() || a.Mode() != ModeCodeView {
		t.Fatalf("esc should clear visual selection and keep viewer open: visual=%v mode=%q", st.VisualActive(), a.Mode())
	}

	// Re-select lines 12-14 and annotate with 'a'.
	d.Keys("g", "g")
	for i := 1; i < 12; i++ {
		d.Keys("j")
	}
	d.Keys("V", "j", "j", "a")
	if a.Modal() != codeAnnotateModalID || a.Mode() != ModeForm {
		t.Fatalf("a did not open annotate form: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	// Field 0: label, Field 1: note, Field 2: color (defaults to amber).
	d.Keys("i")
	d.Type("Trim whitespace")
	d.Keys("esc", "j", "i")
	d.Type("Use strings.TrimSpace instead of noop clean")
	d.Keys("ctrl+s")

	if a.Modal() != codeViewerModalID || a.Mode() != ModeCodeView {
		t.Fatalf("after annotate submit expected code viewer: modal=%q mode=%q status=%q", a.Modal(), a.Mode(), a.Status())
	}
	annH, ok := st.HighlightAt(12)
	if !ok || annH.Kind != views.CodeHighlightAnnotation {
		t.Fatalf("expected annotation highlight at line 12, got %+v ok=%v", annH, ok)
	}
	if annH.StartLine != 12 || annH.EndLine != 14 {
		t.Fatalf("annotation bounds=%d-%d want 12-14", annH.StartLine, annH.EndLine)
	}
	if annH.EffectiveColor() == srcH.EffectiveColor() {
		t.Fatalf("annotation color %q must differ from source color %q", annH.EffectiveColor(), srcH.EffectiveColor())
	}

	// Hover over the annotation hunk -> press 'c' to customize color (amber -> cyan -> blue -> green -> magenta).
	d.Keys("c")
	if a.Modal() != codeCustomizeModalID || a.Mode() != ModeForm {
		t.Fatalf("c did not open customize modal: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	// Field 0 is color enum [amber, cyan, blue, green, magenta, rose]; step 4x right -> magenta.
	d.Keys("l", "l", "l", "l")
	// Field 2 is label, Field 3 is note -> submit with ctrl+s.
	d.Keys("ctrl+s")
	if a.Modal() != codeViewerModalID || a.Mode() != ModeCodeView {
		t.Fatalf("after customize submit expected code viewer: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	updatedAnn, ok := st.HighlightAt(12)
	if !ok || updatedAnn.EffectiveColor() != views.CodeColorMagenta {
		t.Fatalf("customized annotation color=%q want magenta", updatedAnn.EffectiveColor())
	}

	// Promote the hovered annotation hunk to a KindTask to-do via 'T'.
	d.Keys("T")
	tasks, err := a.Store.Query(data.Query{Kind: data.KindTask})
	if err != nil {
		t.Fatal(err)
	}
	var promoted *data.Record
	for i := range tasks.Items {
		if tasks.Items[i].Text("title") == "Trim whitespace" {
			promoted = &tasks.Items[i]
			break
		}
	}
	if promoted == nil {
		t.Fatalf("T did not create promoted task 'Trim whitespace'; status=%q", a.Status())
	}
	desc := promoted.Text("description")
	if !strings.Contains(desc, fmt.Sprintf("%s:12-14", srcFile)) || !strings.Contains(desc, "Use strings.TrimSpace") || !strings.Contains(desc, "func clean") {
		t.Fatalf("promoted task description missing file:range, note, or snippet:\n%s", desc)
	}

	// Close viewer and reopen the same file -> verify saved annotation and customized color persist!
	d.Keys("q")
	if a.Modal() != "" {
		t.Fatalf("q did not close code viewer: modal=%q", a.Modal())
	}
	runCodeOpen(t, a, d, map[string]any{
		"file":      srcFile,
		"startLine": 5,
		"endLine":   7,
	})
	reopened := a.activeCodeViewer().state
	restored, ok := reopened.HighlightAt(12)
	if !ok || restored.Kind != views.CodeHighlightAnnotation || restored.EffectiveColor() != views.CodeColorMagenta || restored.Label != "Trim whitespace" {
		t.Fatalf("reopened annotation did not restore customized state: %+v ok=%v", restored, ok)
	}
	if frame := d.Frame(); !strings.Contains(frame, "Trim whitespace") {
		t.Fatalf("reopened frame missing restored annotation label:\n%s", frame)
	}
}

func TestCodeViewerBackNavigationBindings(t *testing.T) {
	a, d, srcFile := setupCodeViewerApp(t)
	d.Keys("2")
	origSpace := a.Space()
	origFocus := a.Focus()
	origMode := a.Mode()

	for _, backKey := range []string{"ctrl+o", "q", "backspace", "esc"} {
		runCodeOpen(t, a, d, map[string]any{"file": srcFile, "startLine": 4})
		if a.Mode() != ModeCodeView || a.Modal() != codeViewerModalID {
			t.Fatalf("before %q: expected code viewer open, mode=%q modal=%q", backKey, a.Mode(), a.Modal())
		}
		d.Keys(backKey)
		if a.Modal() != "" || a.Space() != origSpace || a.Focus() != origFocus || a.Mode() != origMode {
			t.Fatalf("back key %q did not restore previous view: modal=%q space=%q/%q focus=%q/%q mode=%q/%q",
				backKey, a.Modal(), a.Space(), origSpace, a.Focus(), origFocus, a.Mode(), origMode)
		}
	}
}
