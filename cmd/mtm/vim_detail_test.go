package main

import (
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// openVimPage opens the Vim page on the session id.
func openVimPage(t *testing.T, a *App, id string) {
	t.Helper()
	if err := a.r.OpenPage(vimPage); err != nil {
		t.Fatal(err)
	}
	a.Update(nil)
	if err := a.vimList.Select(id); err != nil {
		t.Fatal(err)
	}
	if err := a.vimList.Focus(); err != nil {
		t.Fatal(err)
	}
	a.Update(nil)
}

// The Vim detail shows the session file, project, directory and window,
// and links the project, the live pane and the directory; a side Vim
// links the pane it sits beside.
func TestVimDetailShowsTheSessionAndItsLinks(t *testing.T) {
	l := linkedFixture(t)
	a := l.a
	openVimPage(t, a, l.vimID)
	fr := sizedFrame(a, 140, 30)
	t.Logf("Vim:\n%s", fr)
	for _, want := range []string{"notes", "session file  ~/.local/v1.vim", "state         running", "window        w2 (running)", "links (3)", "project ◆ matomeru", "pane    ochazuke/shell", "dir     " + pathCell(l.alpha, "/home/test")} {
		if !strings.Contains(fr, want) {
			t.Fatalf("frame lacks %q:\n%s", want, fr)
		}
	}
	a.panes[0].SideVim = "side-vim"
	if got, ok := a.paneLink("side-vim"); !ok || got.To.ID != l.paneRow || !strings.Contains(got.Text, "side pane") {
		t.Fatalf("side Vim link %+v %v", got, ok)
	}
}

// enter on a Vim link lands on the project's Work row, the pane's Panes
// row and the directory's Directories row.
func TestVimLinksJumpToTheirRows(t *testing.T) {
	l := linkedFixture(t)
	a := l.a
	for _, c := range []struct{ kind, id, page, focus string }{
		{linkProject, "matomeru", "work", "matomeru"},
		{linkPane, l.vimPaneRw, panesPage, l.vimPaneRw},
		{linkDirectory, l.alpha, directoriesPage, "directory:" + l.alpha},
	} {
		openVimPage(t, a, l.vimID)
		frame(a)
		toLinks(t, a, a.detail.vim)
		// A link answers enter; it takes no marks.
		if hints := ochazuke.BindingHints(a.r, a.r.Focus); !strings.Contains(hints, "enter detail-link.follow") || strings.Contains(hints, "list.mark") {
			t.Fatalf("link hints %q", hints)
		}
		followLinkTo(t, a, a.detail.vim, c.kind, c.id)
		if a.r.Page != c.page || a.r.Focus != c.focus {
			t.Fatalf("%s link: page %s focus %s, want %s on %s", c.kind, a.r.Page, a.r.Focus, c.focus, c.page)
		}
	}
}

// J and K scroll the Vim detail from the list on a short screen; the row
// keeps the focus.
func TestVimDetailScrollsWithJK(t *testing.T) {
	l := linkedFixture(t)
	a := l.a
	openVimPage(t, a, l.vimID)
	sizedFrame(a, 140, 14)
	d := a.detail.vim.d
	if !d.Overflows() {
		t.Fatalf("the detail fits:\n%s", sizedFrame(a, 140, 14))
	}
	press(t, a, "2", "J")
	if sizedFrame(a, 140, 14); d.Offset() != 2 || a.r.Focus != l.vimID {
		t.Fatalf("2J: offset %d focus %s", d.Offset(), a.r.Focus)
	}
	press(t, a, "K")
	if sizedFrame(a, 140, 14); d.Offset() != 1 {
		t.Fatalf("K: offset %d", d.Offset())
	}
}
