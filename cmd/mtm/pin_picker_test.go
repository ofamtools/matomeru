package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
)

// openPicker presses P and checks the search input holds the focus in insert.
func openPicker(t *testing.T, a *App) *Chooser {
	t.Helper()
	press(t, a, "P")
	c := a.choosers.Get(pinPicker)
	if c == nil || a.r.TopModal() != pinPicker || a.r.Focus != pinPicker+"/query" || a.r.Mode.Name != "insert" {
		t.Fatalf("picker modal %q focus %q mode %q", a.r.TopModal(), a.r.Focus, a.r.Mode.Name)
	}
	return c
}

func labels(rows []ChooserRow) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.Label
	}
	return out
}

func TestPinPickerEnterPinsTopMatch(t *testing.T) {
	a := open(t)
	executeA(t, a, "project", "unpin", "work", nil)
	c := openPicker(t, a)
	if got := labels(c.Matches()); strings.Join(got, "|") != "◆ matomeru|◆ ochazuke|no pin (unpin)" {
		t.Fatalf("rows %q", got)
	}
	if row, ok := c.Highlighted(); !ok || row.Data != "matomeru" {
		t.Fatalf("top row not highlighted: %+v", row)
	}
	typeText(t, a, "chz")
	if got := labels(c.Matches()); len(got) != 1 || got[0] != "◆ ochazuke" {
		t.Fatalf("filtered %q", got)
	}
	press(t, a, "enter")
	if a.pin != "ochazuke" || a.r.TopModal() != "" {
		t.Fatalf("pin %q modal %q", a.pin, a.r.TopModal())
	}
}

func TestPinPickerMovedHighlightWins(t *testing.T) {
	a := open(t)
	executeA(t, a, "project", "unpin", "work", nil)
	c := openPicker(t, a)
	press(t, a, "down")
	if !c.Moved() {
		t.Fatal("down did not count as a move")
	}
	press(t, a, "ctrl+j", "ctrl+k")
	if row, _ := c.Highlighted(); row.Data != "ochazuke" {
		t.Fatalf("highlight %+v", row)
	}
	press(t, a, "enter")
	if a.pin != "ochazuke" {
		t.Fatalf("pin %q", a.pin)
	}
	// Most recently pinned leads the next time.
	c = openPicker(t, a)
	if row, _ := c.Highlighted(); row.Data != "ochazuke" {
		t.Fatalf("recent first %+v", labels(c.Matches()))
	}
	press(t, a, "esc")
	if a.pin != "ochazuke" || a.r.TopModal() != "" {
		t.Fatalf("esc changed pin %q or left modal %q", a.pin, a.r.TopModal())
	}
}

func TestPinPickerCreatesOnMissAndOnCtrlN(t *testing.T) {
	a := open(t)
	openPicker(t, a)
	typeText(t, a, "zebra")
	press(t, a, "enter")
	p, ok := a.project(a.pin)
	if !ok || p.Name != "zebra" || a.r.TopModal() == pinPicker {
		t.Fatalf("miss created %+v pin %q", p, a.pin)
	}
	c := openPicker(t, a)
	typeText(t, a, "mato")
	if len(c.Matches()) == 0 {
		t.Fatal("mato should match matomeru")
	}
	press(t, a, "ctrl+n")
	p, ok = a.project(a.pin)
	if !ok || p.Name != "mato" || p.ID == "matomeru" {
		t.Fatalf("ctrl+n created %+v pin %q", p, a.pin)
	}
}

func TestPinPickerEmptyQueryWithNoProjectsCreatesNameless(t *testing.T) {
	a := openAt(t, filepath.Join(t.TempDir(), "mtm.db"))
	if len(a.projects) != 0 {
		t.Fatalf("fresh database has projects %+v", a.projects)
	}
	c := openPicker(t, a)
	if _, ok := c.Highlighted(); ok {
		t.Fatal("the unpin row must not be the default with an empty query")
	}
	press(t, a, "enter")
	p, ok := a.project(a.pin)
	if !ok || p.Name != "" || !strings.HasPrefix(a.m.Title, "📌 untitled") {
		t.Fatalf("nameless %+v title %q", p, a.m.Title)
	}
}

func TestPinPickerUnpinRowIsLastAndClearsPinAcrossRestart(t *testing.T) {
	path := seeded(t)
	a := openAt(t, path)
	executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": "matomeru"})
	c := openPicker(t, a)
	rows := c.Matches()
	if last := rows[len(rows)-1]; last.ID != unpinRow {
		t.Fatalf("unpin not last: %q", labels(rows))
	}
	if row, _ := c.Highlighted(); row.ID == unpinRow {
		t.Fatal("unpin is the default")
	}
	typeText(t, a, "unpin")
	if row, ok := c.Highlighted(); !ok || row.ID != unpinRow {
		t.Fatalf("unpin query highlights %+v of %q", row, labels(c.Matches()))
	}
	press(t, a, "enter")
	if a.pin != "" || a.r.TopModal() != "" || a.m.Title != "Matomeru · no pin" {
		t.Fatalf("pin %q modal %q title %q", a.pin, a.r.TopModal(), a.m.Title)
	}
	b := openAt(t, path)
	if b.pin != "" {
		t.Fatalf("reopened pin %q", b.pin)
	}
	c = openPicker(t, b)
	typeText(t, b, "no pin")
	if got := labels(c.Matches()); len(got) != 1 || got[0] != "no pin (unpin)" {
		t.Fatalf("no pin query %q", got)
	}
}

func TestPinPickerRendersAndHints(t *testing.T) {
	a := titled(t)
	c := openPicker(t, a)
	body := strings.Split(c.View(60, 10).Text, "\n")
	if !strings.Contains(body[1], "\x1b[7m") || strings.Contains(body[2], "\x1b[7m") {
		t.Fatalf("only the top match is highlighted: %q", body)
	}
	for _, width := range []int{80, 120} {
		a.m.Width, a.m.Height = width, 24
		frame := ansi.Strip(a.View().Content)
		for _, want := range []string{"Pin project", "◆ matomeru  ~/proj/clients", "no pin (unpin)", "enter chooser.pick", "ctrl+n chooser.new", "esc chooser.cancel"} {
			if !strings.Contains(frame, want) {
				t.Fatalf("at %d missing %q:\n%s", width, want, frame)
			}
		}
		for i, line := range strings.Split(frame, "\n") {
			if w := ansi.StringWidth(line); w > width {
				t.Fatalf("at %d line %d is %d wide: %q", width, i, w, line)
			}
		}
		t.Logf("frame at %d:\n%s", width, frame)
	}
}
