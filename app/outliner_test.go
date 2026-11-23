package app

import (
	"fmt"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/tree"
)

func TestOutlineInPlaceVimAndScrollOff(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	seed := core.Actor{Kind: "system", ID: "seed"}
	var roots []*data.Record
	for i := 1; i <= 25; i++ {
		rec, _, err := store.Create(data.CreateInput{
			Kind: data.KindTask,
			Fields: map[string]any{
				"title":      fmt.Sprintf("Root %02d", i),
				"workStatus": "todo",
				"order":      int64(i),
			},
		}, seed)
		if err != nil {
			t.Fatal(err)
		}
		roots = append(roots, rec)
	}

	a, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })

	// 1. CheckBindings must report zero conflicts or errors for the new Outline/Tree bindings.
	if report, err := CheckBindings(""); err != nil || len(report.Errors) != 0 {
		t.Fatalf("CheckBindings errors=%v err=%v", report.Errors, err)
	}

	d := NewDriver(a, 100, 24)
	d.Keys("T")
	if a.Modal() != "outline" || a.Mode() != ModeOutline {
		t.Fatalf("T did not open outline: modal=%q mode=%q", a.Modal(), a.Mode())
	}

	// 2. One blank line between top-level items (gapTopLevel: true).
	frame := d.Frame()
	lines := strings.Split(frame, "\n")
	r1, r2 := -1, -1
	for idx, ln := range lines {
		if strings.Contains(ln, "Root 01") {
			r1 = idx
		}
		if strings.Contains(ln, "Root 02") {
			r2 = idx
		}
	}
	if r1 < 0 || r2 != r1+2 {
		t.Fatalf("expected 1 blank spacer row between Root 01 (line %d) and Root 02 (line %d):\n%s", r1, r2, frame)
	}

	// 3. Viewport scrolloff (default 5): jump to bottom with G, then move up with k.
	// Cursor MUST move freely upward without dragging the viewport until it enters the 5-row top padding zone.
	d.Keys("G")
	om := a.trees.outlineModel()
	offAtBottom := om.Offset
	if offAtBottom <= 0 {
		t.Fatalf("expected positive outline Offset at bottom of 25-item list, got %d", offAtBottom)
	}
	d.Keys("k")
	if om.SelectedID != roots[23].ID {
		t.Fatalf("after G then k, outline SelectedID = %q, want %q", om.SelectedID, roots[23].ID)
	}
	if om.Offset != offAtBottom {
		t.Fatalf("cursor glued to bottom! after G then k, outline Offset changed from %d to %d", offAtBottom, om.Offset)
	}
	// Moving up repeatedly eventually scrolls upward and releases cleanly to hit offset 0 at top.
	for range 24 {
		d.Keys("k")
	}
	if om.SelectedID != roots[0].ID || om.Offset != 0 {
		t.Fatalf("at top: sel=%q (want %q), offset=%d (want 0)", om.SelectedID, roots[0].ID, om.Offset)
	}

	// 4. ctrl+e / ctrl+y scroll the viewport line by line.
	d.Keys("ctrl+e", "ctrl+e")
	if om.Offset != 2 {
		t.Fatalf("after 2x ctrl+e, outline Offset = %d, want 2", om.Offset)
	}
	d.Keys("ctrl+y")
	if om.Offset != 1 {
		t.Fatalf("after ctrl+y, outline Offset = %d, want 1", om.Offset)
	}
	d.Keys("g", "g")

	// 5. `i` places cursor at beginning (0); `a` places cursor at end (len).
	d.Keys("i")
	if ed := a.trees.activeEdit(); a.Mode() != ModeOutlineEdit || ed == nil || ed.Cursor() != 0 {
		t.Fatalf("i: mode=%q edit=%+v, want cursor 0", a.Mode(), ed)
	}
	d.Type("Pre ")
	d.Keys("enter")
	if rec, _ := store.Get(roots[0].Ref()); rec.Text("title") != "Pre Root 01" {
		t.Fatalf("after i + Type + enter: title = %q, want %q", rec.Text("title"), "Pre Root 01")
	}

	d.Keys("a")
	if ed := a.trees.activeEdit(); a.Mode() != ModeOutlineEdit || ed == nil || ed.Cursor() != len([]rune("Pre Root 01")) {
		t.Fatalf("a: mode=%q edit=%+v, want cursor at end", a.Mode(), ed)
	}
	d.Type(" Post")
	d.Keys("enter")
	if rec, _ := store.Get(roots[0].Ref()); rec.Text("title") != "Pre Root 01 Post" {
		t.Fatalf("after a + Type + enter: title = %q, want %q", rec.Text("title"), "Pre Root 01 Post")
	}

	// 6. `o` inserts below at SAME indent level (root sibling, NOT child).
	d.Keys("o")
	if ed := a.trees.activeEdit(); a.Mode() != ModeOutlineEdit || ed == nil || ed.ParentID != "" {
		t.Fatalf("o on root: mode=%q edit=%+v, want empty ParentID", a.Mode(), ed)
	}
	d.Type("Sibling Below")
	d.Keys("enter")
	sibBelow, err := store.GetByField(data.KindTask, "title", "Sibling Below")
	if err != nil {
		t.Fatalf("Sibling Below not created: %v", err)
	}
	if _, hasParent := sibBelow.RefField("parent"); hasParent {
		t.Fatalf("Sibling Below unexpectedly got a parent")
	}

	// 7. `o` then `Tab` indents under the focused item; `Shift+Tab` outdents back to root; `Tab` indents again.
	d.Keys("o", "tab")
	if ed := a.trees.activeEdit(); ed == nil || ed.ParentID != sibBelow.ID {
		t.Fatalf("o+tab edit = %+v, want ParentID %q", ed, sibBelow.ID)
	}
	d.Keys("shift+tab")
	if ed := a.trees.activeEdit(); ed == nil || ed.ParentID != "" {
		t.Fatalf("o+tab+shift+tab edit = %+v, want empty ParentID", ed)
	}
	d.Keys("tab")
	d.Type("Child Via Tab")
	d.Keys("enter")
	childRec, err := store.GetByField(data.KindTask, "title", "Child Via Tab")
	if err != nil {
		t.Fatalf("Child Via Tab not created: %v", err)
	}
	if p, ok := childRec.RefField("parent"); !ok || p != sibBelow.Ref() {
		t.Fatalf("Child Via Tab parent = %v, want %v", p, sibBelow.Ref())
	}

	// 8. `O` on a child inserts ABOVE at the SAME child indent level (under sibBelow).
	d.Keys("O")
	if ed := a.trees.activeEdit(); ed == nil || ed.Kind != tree.EditAddAbove || ed.ParentID != sibBelow.ID {
		t.Fatalf("O on child: edit=%+v, want add_above under %q", ed, sibBelow.ID)
	}
	d.Type("Child Above")
	d.Keys("enter")
	childAbove, err := store.GetByField(data.KindTask, "title", "Child Above")
	if err != nil {
		t.Fatalf("Child Above not created: %v", err)
	}
	if p, ok := childAbove.RefField("parent"); !ok || p != sibBelow.Ref() {
		t.Fatalf("Child Above parent = %v, want %v", p, sibBelow.Ref())
	}

	// 9. Normal-mode Shift+Tab outdents Child Above to top-level (clears parent), and Tab indents it back.
	d.Keys("shift+tab")
	if rec, _ := store.Get(childAbove.Ref()); rec.Text("parent") != "" {
		t.Fatalf("after normal-mode shift+tab, parent not cleared: %+v", rec.Fields["parent"])
	}
	d.Keys("tab")
	if rec, _ := store.Get(childAbove.Ref()); rec.Fields["parent"] == nil {
		t.Fatalf("after normal-mode tab, expected parent to be set")
	}

	// 10. `e` opens the full metadata form when explicitly wanted.
	d.Keys("e")
	if a.Modal() != "form" || a.Mode() != ModeForm {
		t.Fatalf("after e in outline: modal=%q mode=%q, want form/form", a.Modal(), a.Mode())
	}
	d.Keys("esc")
}
