package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

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
	newAbout(t, a, ochazuke.Arguments{"text": "main.go needs a split", "about": "context-item:" + savedRowKey(a.r.Instances[item.ID])})
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
	if err := a.notesLoad.refresh(); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Select("notes/list/" + base.ID); err != nil {
		t.Fatal(err)
	}
	if fr := frame(a); strings.Contains(fr, "Notes (") {
		t.Fatalf("a note with no notes shows a section:\n%s", fr)
	}
	newAbout(t, a, ochazuke.Arguments{"text": "a remark on the base", "about": "note:" + base.ID})
	if err := a.notesLoad.refresh(); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Select("notes/list/" + base.ID); err != nil {
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

// A mounted extension page's detail shows the notes about its selected row,
// whatever the tool declared, and nothing for a row without notes.
func TestMountedPageDetailShowsTheRowsNotes(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	bin, _ := fakeExtension(t, strings.Replace(fakeCatalog, "VERSION", "1.0", 1), "")
	a := registered(t, path, "fake", bin)
	openExtPage(t, a, "ext/fake")
	list := a.r.List("ext/fake/item")
	if list == nil || list.Len() != 2 {
		t.Fatalf("rows not loaded: %v", list)
	}
	if err := a.r.SetFocus("ext/fake/item/i1"); err != nil {
		t.Fatal(err)
	}
	if fr := tallFrame(a); strings.Contains(fr, "Notes (") {
		t.Fatalf("a row with no notes shows a section:\n%s", fr)
	}
	newAbout(t, a, ochazuke.Arguments{"text": "one needs finishing", "about": "fake.item:i1"})
	if err := a.r.OpenPage("ext/fake"); err != nil { // note new opened the Notes page
		t.Fatal(err)
	}
	if err := a.r.SetFocus("ext/fake/item/i1"); err != nil {
		t.Fatal(err)
	}
	if fr := tallFrame(a); !strings.Contains(fr, "Notes (") || !strings.Contains(fr, "one needs finishing") {
		t.Fatalf("the row's note is not in its detail:\n%s", fr)
	}
	if err := a.r.SetFocus("ext/fake/item/i2"); err != nil {
		t.Fatal(err)
	}
	if fr := tallFrame(a); strings.Contains(fr, "one needs finishing") {
		t.Fatalf("the next row shows the note:\n%s", fr)
	}
	if err := a.r.SetFocus("ext/fake/item/i1"); err != nil {
		t.Fatal(err)
	}
	if err := a.r.Settings().Set(ochazuke.RelatedDetailSetting, false); err != nil {
		t.Fatal(err)
	}
	if fr := tallFrame(a); strings.Contains(fr, "one needs finishing") {
		t.Fatalf("detail.related off still shows the note:\n%s", fr)
	}
}

// tallFrame is the frame on a tall screen, so a mounted detail has room for
// its record and the stack under it.
func tallFrame(a *App) string {
	a.m.Width, a.m.Height = 120, 60
	return ansi.Strip(frameView(a).Content)
}
