package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"
	"ofam.dev/ochazuke/markdown"

	"ofam.dev/matomeru/internal/work"
)

const checklistNote = "# Plan\n\nSome text.\n\n- [ ] one\n- [x] two\n- [ ] three\n"

// notesDocApp opens the Notes page on a note with a checklist, the list
// focused.
func notesDocApp(t *testing.T) (*App, work.Note) {
	t.Helper()
	a := open(t)
	mono(t, a)
	note, err := a.work.AddNote("matomeru", "", checklistNote)
	if err != nil {
		t.Fatal(err)
	}
	a.all = true
	if err := a.refreshNotes(); err != nil {
		t.Fatal(err)
	}
	if err := a.r.OpenPage(notesPage); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Select(note.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Focus(); err != nil {
		t.Fatal(err)
	}
	a.Update(nil)
	return a, note
}

func noteText(t *testing.T, a *App, id string) string {
	t.Helper()
	note, err := a.work.Note(id)
	if err != nil {
		t.Fatal(err)
	}
	return note.Text
}

// enter on a note lands on the Doc's first line; ]x moves to the first
// item, j to the next line, x toggles it by rewriting only the box in the
// stored note, u puts it back and ctrl+r writes it again.
func TestNoteChecklistTogglesInTheStore(t *testing.T) {
	t.Parallel()
	a, note := notesDocApp(t)
	frame(a)
	press(t, a, "enter")
	if !a.noteDocFocused() {
		t.Fatalf("enter focus %s", a.r.Focus)
	}
	if _, off := a.noteDoc.Cursor(); off != 0 {
		t.Fatalf("enter cursor line %d", off)
	}
	press(t, a, "]", "x")
	if task, _ := a.noteDoc.Selected(); task.Text != "one" {
		t.Fatalf("selected %+v", task)
	}
	press(t, a, "j", "j", "x")
	want := strings.Replace(checklistNote, "- [ ] three", "- [x] three", 1)
	if got := noteText(t, a, note.ID); got != want {
		t.Fatalf("x wrote %q, want %q", got, want)
	}
	press(t, a, "k", "x")
	want = strings.Replace(want, "- [x] two", "- [ ] two", 1)
	if got := noteText(t, a, note.ID); got != want {
		t.Fatalf("x on a checked item wrote %q", got)
	}
	press(t, a, "u", "u")
	if got := noteText(t, a, note.ID); got != checklistNote {
		t.Fatalf("u u left %q", got)
	}
	press(t, a, "ctrl+r")
	if got := noteText(t, a, note.ID); got != strings.Replace(checklistNote, "- [ ] three", "- [x] three", 1) {
		t.Fatalf("ctrl+r left %q", got)
	}
	if f := frame(a); !strings.Contains(f, "Some text.") || strings.Contains(f, "# Plan\n\nSome") {
		t.Fatalf("body not rendered as Markdown:\n%s", f)
	}
}

// An item changed in the store since the Doc read the note is refused:
// the key's error is in the footer and nothing is written.
func TestNoteChecklistRefusesAChangedItem(t *testing.T) {
	t.Parallel()
	a, note := notesDocApp(t)
	press(t, a, "enter", "]", "x")
	changed := strings.Replace(checklistNote, "- [ ] one", "- [ ] one, renamed", 1)
	n, _ := a.work.Note(note.ID)
	n.Text = changed
	if err := a.work.PutNote(n); err != nil {
		t.Fatal(err)
	}
	a.Update(keyMsg("x"))
	if !strings.Contains(a.m.Status, "not toggled") {
		t.Fatalf("status %q", a.m.Status)
	}
	if got := noteText(t, a, note.ID); got != changed {
		t.Fatalf("refused toggle wrote %q", got)
	}
}

// i on the Doc edits the block under the cursor in place (mtm binds no i
// on the markdown noun; note.edit keeps its verb); esc goes back to the
// list. A note without tasks is a Doc stop too.
func TestNoteChecklistEditAndBack(t *testing.T) {
	t.Parallel()
	a, note := notesDocApp(t)
	press(t, a, "enter", "esc")
	if a.r.Focus != note.ID {
		t.Fatalf("esc focus %s", a.r.Focus)
	}
	press(t, a, "enter", "i")
	if !a.noteDoc.Editing() || a.editor != nil {
		t.Fatalf("i did not edit the block in place: editing %v editor %+v", a.noteDoc.Editing(), a.editor)
	}
	press(t, a, "esc")
	if _, ok := a.r.Operations[a.r.CanonicalKey("note.edit")]; !ok {
		t.Fatal("note.edit is gone")
	}
	plain, err := a.work.AddNote("matomeru", "", "no tasks here")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.leaveEditor(); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshNotes(); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Select(plain.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "enter")
	if !a.noteDocFocused() {
		t.Fatalf("plain note enter focus %s", a.r.Focus)
	}
}

// Notes are plain lines: every newline is a line break in the view, not a
// paragraph join.
func TestNoteLinesRenderOneLinePerSourceLine(t *testing.T) {
	t.Parallel()
	a, _ := notesDocApp(t)
	if _, err := a.work.AddNote("matomeru", "", "first line\nsecond line\nthird line"); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshNotes(); err != nil {
		t.Fatal(err)
	}
	for _, item := range a.noteList.Items() {
		if strings.HasPrefix(item.Label, "first line") {
			if err := a.noteList.Select(item.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	a.Update(nil)
	f := frame(a)
	lines := strings.Split(f, "\n")
	second := -1
	for i, line := range lines {
		if strings.Contains(line, "second line") {
			second = i
		}
	}
	if second < 1 || second+1 >= len(lines) || !strings.Contains(lines[second-1], "first line") || !strings.Contains(lines[second+1], "third line") || strings.Contains(lines[second], "third") {
		t.Fatalf("not one line per source line:\n%s", f)
	}
}

// The view's line breaks never reach the store: in a note of plain lines
// (LF or CRLF, a code fence among them) a toggle writes only the box and
// u puts the very bytes back.
func TestNoteHardBreaksStayOutOfTheStore(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		"Plan\nsecond line\n- [ ] one\n- [ ] two\n",
		"crlf\r\nlines\r\n\r\n- [ ] one\r\n- [ ] two\r\n",
		"```\ncode\nstays\n```\nafter\nfence\n- [ ] one\n- [ ] two",
	} {
		a := open(t)
		note, err := a.work.AddNote("matomeru", "", text)
		if err != nil {
			t.Fatal(err)
		}
		a.all = true
		if err := a.refreshNotes(); err != nil {
			t.Fatal(err)
		}
		if err := a.r.OpenPage(notesPage); err != nil {
			t.Fatal(err)
		}
		if err := a.noteList.Select(note.ID); err != nil {
			t.Fatal(err)
		}
		if err := a.noteList.Focus(); err != nil {
			t.Fatal(err)
		}
		a.Update(nil)
		press(t, a, "enter", "]", "x", "j", "x")
		if got := noteText(t, a, note.ID); got != strings.Replace(text, "- [ ] two", "- [x] two", 1) {
			t.Fatalf("%q: toggle wrote %q", text, got)
		}
		press(t, a, "u")
		if got := noteText(t, a, note.ID); got != text {
			t.Fatalf("%q: undo wrote %q", text, got)
		}
	}
}

// esc from a plain note's body lands on the note's row, so enter reads
// it again.
func TestNoteBodyEscLandsOnTheRow(t *testing.T) {
	t.Parallel()
	a := open(t)
	note, err := a.work.AddNote("matomeru", "", "no tasks\nhere")
	if err != nil {
		t.Fatal(err)
	}
	a.all = true
	if err := a.refreshNotes(); err != nil {
		t.Fatal(err)
	}
	if err := a.r.OpenPage(notesPage); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Select(note.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "enter", "esc")
	if a.r.Focus != note.ID {
		t.Fatalf("esc focus %s, want the row %s", a.r.Focus, note.ID)
	}
	press(t, a, "enter")
	if !a.noteDocFocused() {
		t.Fatalf("enter after esc: focus %s", a.r.Focus)
	}
}

// With the focus on the items the Doc draws the body: a note of plain
// lines keeps one line per source line, and only the selected item is
// the reverse highlight.
func TestNoteChecklistDrawsTheDoc(t *testing.T) {
	t.Parallel()
	a := open(t)
	mono(t, a)
	note, err := a.work.AddNote("matomeru", "", "Plan\nsecond line\n- [ ] one\n- [ ] two\n")
	if err != nil {
		t.Fatal(err)
	}
	a.all = true
	if err := a.refreshNotes(); err != nil {
		t.Fatal(err)
	}
	if err := a.r.OpenPage(notesPage); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Select(note.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Focus(); err != nil {
		t.Fatal(err)
	}
	a.Update(nil)
	press(t, a, "enter", "]", "x", "j")
	lines := strings.Split(rawFrame(a), "\n")
	plan, items := -1, []string{}
	for i, line := range lines {
		plain := ansi.Strip(line)
		if strings.Contains(plain, "Plan") && !strings.Contains(plain, "second") {
			plan = i
		}
		if strings.Contains(line, "\x1b[7m") && strings.Contains(plain, "[ ]") {
			items = append(items, strings.TrimSpace(plain[strings.Index(plain, "[ ]"):]))
		}
	}
	if plan < 0 || !strings.Contains(lines[plan+1], "second line") {
		t.Fatalf("not one line per source line:\n%s", frame(a))
	}
	if len(items) != 1 || items[0] != "[ ] two" {
		t.Fatalf("highlighted items %q:\n%s", items, frame(a))
	}
}

// A markdown row bound while d of d d is pending keeps the sequence
// (ochazuke's Bind leaves a prefix its rows are not on), so mtm's rows on
// the markdown noun are safe to bind at any time, not only at setup.
func TestMarkdownRowBoundMidSequenceKeepsDD(t *testing.T) {
	t.Parallel()
	a, _ := notesDocApp(t)
	a.Update(keyMsg("d"))
	if a.r.Pending != "d" {
		t.Fatalf("pending %q", a.r.Pending)
	}
	a.r.Define(markdown.DocNoun).Mode("normal").Bind("I", ochazuke.Do("note.edit"))
	a.Update(keyMsg("d"))
	if a.r.TopModal() == "" {
		t.Fatalf("d d after a Bind did not ask to delete: pending %q status %q", a.r.Pending, a.m.Status)
	}
}
