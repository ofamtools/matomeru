package main

import (
	"testing"

	"ofam.dev/matomeru/internal/contextitems"
)

// N on an outline task row writes a note about that task, and C chooses a
// context item and attaches it (the item's task becomes the row's).
func TestOutlineRowNoteAndContext(t *testing.T) {
	t.Parallel()
	a := open(t)
	if err := a.contexts.Put(contextitems.Item{ID: "ci-1", Kind: "link", Text: "https://example.com/spec", ProjectID: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	press(t, a, "j", "O")
	focusRow(t, a, outlineNode("t2"))
	press(t, a, "N")
	typeText(t, a, "from the outline")
	press(t, a, "esc", "N")
	var got int
	for _, n := range notesOf(t, a) {
		if n.TaskID == "t2" && n.Text == "from the outline" {
			got++
		}
	}
	if got != 1 || a.r.TopModal() != outlineModal {
		t.Fatalf("%d notes on t2, modal %q", got, a.r.TopModal())
	}
	press(t, a, "C")
	if a.r.Chooser(taskContextChooser) == nil {
		t.Fatalf("C opened %q\n%s", a.r.TopModal(), frame(a))
	}
	typeText(t, a, "spec")
	press(t, a, "enter")
	it, err := a.contexts.Get("ci-1")
	if err != nil || it.TaskID != "t2" {
		t.Fatalf("item %+v %v, want task t2", it, err)
	}
}
