package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

func numbered(prefix string, n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("%s %03d", prefix, i+1)
	}
	return strings.Join(lines, "\n")
}

// On Panes the Screen detail opens at its live end; K scrolls back a line,
// 20K twenty more, J forward, all while the pane row keeps the focus;
// another pane starts at its own end.
func TestPanesScreenDetailScrollsWithJK(t *testing.T) {
	app, f := panesApp(t)
	f.screens["w1"] = numbered("row", 100)
	settle(app, follow(app))
	focus := app.r.Focus
	d := app.detail.panes.d
	fr := frame(app)
	if !strings.Contains(fr, "row 100") || strings.Contains(fr, "row 001") || !strings.Contains(fr, "/ 100") {
		t.Fatalf("opened:\n%s", fr)
	}
	end := d.Offset()
	press(t, app, "K")
	frame(app)
	if d.Offset() != end-1 || app.r.Focus != focus {
		t.Fatalf("K: offset %d want %d, focus %s", d.Offset(), end-1, app.r.Focus)
	}
	press(t, app, "2", "0", "K")
	if fr = frame(app); d.Offset() != end-21 || strings.Contains(fr, "row 100") {
		t.Fatalf("20K: offset %d want %d\n%s", d.Offset(), end-21, fr)
	}
	// A refresh of the same pane keeps the place scrolled back to.
	settle(app, follow(app))
	if frame(app); d.Offset() != end-21 {
		t.Fatalf("refresh moved the detail: offset %d want %d", d.Offset(), end-21)
	}
	press(t, app, "J")
	if frame(app); d.Offset() != end-20 || app.r.Focus != focus {
		t.Fatalf("J: offset %d want %d, focus %s", d.Offset(), end-20, app.r.Focus)
	}
	press(t, app, "j", "j")
	f.screens["w2"] = numbered("shell", 60)
	settle(app, follow(app))
	if fr = frame(app); !strings.Contains(fr, "shell 060") {
		t.Fatalf("another pane does not start at its end:\n%s", fr)
	}
}

// The Activity text (newest at the bottom) follows its end while it is
// there.
func TestPanesTailDetailFollowsItsEnd(t *testing.T) {
	app, _ := panesApp(t)
	row, _ := app.selectedRow()
	app.pv.tab = tabActivity
	app.pv.text[fmt.Sprintf("%s|%d", row, tabActivity)] = numbered("event", 80)
	if fr := frame(app); !strings.Contains(fr, "event 080") {
		t.Fatalf("opened:\n%s", fr)
	}
	app.pv.text[fmt.Sprintf("%s|%d", row, tabActivity)] = numbered("event", 90)
	if fr := frame(app); !strings.Contains(fr, "event 090") {
		t.Fatalf("new lines did not show:\n%s", fr)
	}
}

// On Work, J and K scroll a long project detail while the projects list
// keeps the focus and its selection; another row starts at the top.
func TestWorkDetailScrollsWithJK(t *testing.T) {
	app := open(t)
	for i := 1; i <= 40; i++ {
		executeA(t, app, "note", "new", "", ochazuke.Arguments{"text": fmt.Sprintf("note %03d", i), "project": "matomeru"})
	}
	if err := app.r.OpenPage("work"); err != nil {
		t.Fatal(err)
	}
	if err := app.r.SetFocus("matomeru"); err != nil {
		t.Fatal(err)
	}
	frame(app)
	d := app.detail.work.d
	if !d.Overflows() {
		t.Fatalf("the detail fits:\n%s", frame(app))
	}
	press(t, app, "5", "J")
	if frame(app); d.Offset() != 5 || app.r.Focus != "matomeru" {
		t.Fatalf("5J: offset %d focus %s", d.Offset(), app.r.Focus)
	}
	press(t, app, "K")
	if frame(app); d.Offset() != 4 {
		t.Fatalf("K: offset %d", d.Offset())
	}
	press(t, app, "j")
	if frame(app); d.Offset() != 0 {
		t.Fatalf("a new row kept offset %d", d.Offset())
	}
}

// On Notes, J scrolls the selected note's body from the list; the list
// keeps the focus, and the body (enter) scrolls the same text with j.
func TestNotesDetailScrollsFromTheList(t *testing.T) {
	a := open(t)
	executeA(t, a, "note", "new", "", ochazuke.Arguments{"text": numbered("line", 100), "scratch": true})
	if err := a.r.OpenPage(notesPage); err != nil {
		t.Fatal(err)
	}
	if err := a.noteList.Focus(); err != nil {
		t.Fatal(err)
	}
	focus := a.r.Focus
	frame(a)
	press(t, a, "1", "0", "J")
	fr := frame(a)
	if a.r.Focus != focus || strings.Contains(fr, "line 010") || !strings.Contains(fr, "line 011") || !strings.Contains(fr, "11–") {
		t.Fatalf("10J from the list: focus %s\n%s", a.r.Focus, fr)
	}
	press(t, a, "enter", "j")
	if fr = frame(a); strings.Contains(fr, "line 011") || !strings.Contains(fr, "line 012") {
		t.Fatalf("j in the body did not continue from J:\n%s", fr)
	}
}

// Without an overflowing detail J and K do not answer (detail-scrolls),
// and the footer has no J/K detail hint; with one it has.
func TestJKAnswerOnlyWhileTheDetailOverflows(t *testing.T) {
	app, f := panesApp(t)
	settle(app, follow(app))
	frame(app)
	hinted := func() bool { return strings.Contains(ochazuke.BindingHints(app.r, app.r.Focus), "J/K detail") }
	if app.detail.panes.d.Overflows() || hinted() {
		t.Fatalf("a short screen overflows or hints J/K")
	}
	f.screens["w1"] = numbered("row", 100)
	settle(app, follow(app))
	frame(app)
	if !hinted() {
		t.Fatalf("no J/K detail hint for a long screen: %q", ochazuke.BindingHints(app.r, app.r.Focus))
	}
}

// On Review, the opened review's captured output opens at its end and K
// scrolls it back while the review row keeps the focus.
func TestReviewOutputScrollsWithJK(t *testing.T) {
	app, f, rv := reviewApp(t)
	f.screens[rv.Window] = numbered("out", 100)
	settle(app, key(t, app, "enter"))
	focus := app.r.Focus
	d := app.detail.reviews.d
	if fr := frame(app); !d.Overflows() || !strings.Contains(fr, "out 100") {
		t.Fatalf("opened (focus %s):\n%s", focus, fr)
	}
	end := d.Offset()
	press(t, app, "K")
	if fr := frame(app); d.Offset() != end-1 || app.r.Focus != focus || strings.Contains(fr, "out 100") {
		t.Fatalf("K: offset %d want %d focus %s\n%s", d.Offset(), end-1, app.r.Focus, fr)
	}
}
