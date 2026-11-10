package main

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

// Every detail shows the notes about its thing when it has some and nothing
// when it has none: here the Context page, which had no detail.
func TestContextShowsTheFocusedItemsNotes(t *testing.T) {
	t.Parallel()
	a, _ := contextApp(t)
	selectContext(t, a, "/tmp/matomeru/main.go")
	if fr := frame(a); strings.Contains(fr, "Notes (") {
		t.Fatalf("an item with no notes shows a section:\n%s", fr)
	}
	item, _ := a.cx.list.Selected()
	newAbout(t, a, ochazuke.Arguments{"text": "main.go needs a split", "about": "context-item:" + item.ID})
	if err := a.r.OpenPage(contextPage); err != nil {
		t.Fatal(err)
	}
	if fr := frame(a); !strings.Contains(fr, "main.go needs a split") || !strings.Contains(fr, "Notes (") {
		t.Fatalf("the item's note is not shown:\n%s", fr)
	}
	// Another item shows none; the setting turns the region off.
	selectContext(t, a, "1ba025f")
	if fr := frame(a); strings.Contains(fr, "main.go needs a split") {
		t.Fatalf("another item shows the note:\n%s", fr)
	}
	selectContext(t, a, "/tmp/matomeru/main.go")
	if err := a.r.Settings().Set(settingContextNotes, false); err != nil {
		t.Fatal(err)
	}
	if fr := frame(a); strings.Contains(fr, "main.go needs a split") {
		t.Fatalf("context.notes off still shows the note:\n%s", fr)
	}
}

// The Notes page's detail shows the notes about the selected note, and
// nothing while it has none.
func TestNotesPageShowsNotesAboutTheSelectedNote(t *testing.T) {
	t.Parallel()
	a := open(t)
	base := newAbout(t, a, ochazuke.Arguments{"text": "the base note", "about": "task:t1"})
	if err := a.r.OpenPage(notesPage); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshNotes(); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Select(base.ID); err != nil {
		t.Fatal(err)
	}
	if fr := frame(a); strings.Contains(fr, "Notes (") {
		t.Fatalf("a note with no notes shows a section:\n%s", fr)
	}
	newAbout(t, a, ochazuke.Arguments{"text": "a remark on the base", "about": "note:" + base.ID})
	if err := a.refreshNotes(); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Select(base.ID); err != nil {
		t.Fatal(err)
	}
	if fr := frame(a); !strings.Contains(fr, "Notes (") || !strings.Contains(fr, "a remark on the base") {
		t.Fatalf("the remark is not under the note:\n%s", fr)
	}
}

// The Extensions page's detail shows the notes about the selected tool.
func TestExtensionsPageShowsNotesAboutTheSelectedTool(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fakeTool(t, dir, "alpha", toolCatalog("alpha", "1.0"))
	path := seeded(t)
	a := extPageApp(t, path, dir)
	if err := a.r.SetFocus(extListID + "/alpha"); err != nil {
		t.Fatal(err)
	}
	if fr := frame(a); strings.Contains(fr, "Notes (") {
		t.Fatalf("a tool with no notes shows a section:\n%s", fr)
	}
	newAbout(t, a, ochazuke.Arguments{"text": "alpha needs a newer catalog", "about": "extension:alpha"})
	if fr := frame(a); !strings.Contains(fr, "alpha needs a newer catalog") {
		t.Fatalf("the tool's note is not shown:\n%s", fr)
	}
}
