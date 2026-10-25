package main

import (
	"os"
	"os/exec"
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

// leaveDirectoryChooser checks a directoryless pin landed in the directory
// chooser and escapes it, keeping the pin.
func leaveDirectoryChooser(t *testing.T, a *App) {
	t.Helper()
	pin := a.pin
	if a.r.TopModal() != dirChooser || a.r.Focus != dirChooser+"/query" || a.r.Mode.Name != "insert" {
		t.Fatalf("expected the directory chooser, modal %q focus %q", a.r.TopModal(), a.r.Focus)
	}
	press(t, a, "esc")
	if a.r.TopModal() != "" || a.pin != pin {
		t.Fatalf("esc: modal %q pin %q want %q", a.r.TopModal(), a.pin, pin)
	}
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
	if a.pin != "ochazuke" || a.r.TopModal() == pinPicker {
		t.Fatalf("pin %q modal %q", a.pin, a.r.TopModal())
	}
	leaveDirectoryChooser(t, a)
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
	leaveDirectoryChooser(t, a)
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
	leaveDirectoryChooser(t, a)
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
	leaveDirectoryChooser(t, a)
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
	leaveDirectoryChooser(t, a)
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

// pressSettle presses key and runs the background work it started.
func pressSettle(t *testing.T, a *App, key string) {
	t.Helper()
	_, cmd := a.Update(keyMsg(key))
	if a.m.Status != "" {
		t.Fatalf("key %q: %s", key, a.m.Status)
	}
	settle(a, cmd)
}

// knownRepos makes git checkouts under a fresh base and points the
// directory scan at it, with no daemon.
func knownRepos(t *testing.T, a *App, names ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range names {
		path := filepath.Join(root, name)
		if out, err := exec.Command("git", "init", "-q", path).CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", out, err)
		}
	}
	t.Setenv("MTM_PROJECT_BASE_DIRS", root)
	a.daemon = Options{Socket: filepath.Join(t.TempDir(), "missing.sock")}.client()
	return root
}

func TestNewProjectChoosesDirectoryWhichBecomesDefault(t *testing.T) {
	a := open(t)
	root := knownRepos(t, a, "alpha", "beta")
	page := a.r.Page
	openPicker(t, a)
	typeText(t, a, "fresh")
	pressSettle(t, a, "enter")
	c := a.choosers.Get(dirChooser)
	if c == nil {
		t.Fatalf("no directory chooser, modal %q", a.r.TopModal())
	}
	if got := labels(c.Matches()); len(got) != 3 || got[2] != "browse…" {
		t.Fatalf("directory rows %q", got)
	}
	typeText(t, a, "beta")
	press(t, a, "enter")
	p, _ := a.project(a.pin)
	want := directoryKey(filepath.Join(root, "beta"))
	if p.Name != "fresh" || p.DefaultTree != want || len(p.Trees) != 1 || a.r.TopModal() != "" || a.r.Page != page {
		t.Fatalf("project %+v want default %s modal %q page %s", p, want, a.r.TopModal(), a.r.Page)
	}
	if !strings.HasPrefix(a.m.Title, "📌 fresh") {
		t.Fatalf("title %q", a.m.Title)
	}
}

func TestPinningDirectorylessProjectAsksAndMovedRowWins(t *testing.T) {
	a := open(t)
	root := knownRepos(t, a, "alpha", "beta")
	executeA(t, a, "project", "unpin", "work", nil)
	openPicker(t, a)
	typeText(t, a, "matomeru")
	pressSettle(t, a, "enter")
	c := a.choosers.Get(dirChooser)
	if c == nil || a.pin != "matomeru" {
		t.Fatalf("pin %q modal %q", a.pin, a.r.TopModal())
	}
	press(t, a, "down", "enter")
	p, _ := a.project("matomeru")
	if want := directoryKey(filepath.Join(root, "beta")); p.DefaultTree != want {
		t.Fatalf("default %q want %q", p.DefaultTree, want)
	}
	// With a default directory, pinning asks nothing.
	executeA(t, a, "project", "unpin", "work", nil)
	openPicker(t, a)
	typeText(t, a, "matomeru")
	press(t, a, "enter")
	if a.pin != "matomeru" || a.r.TopModal() != "" {
		t.Fatalf("pin %q modal %q", a.pin, a.r.TopModal())
	}
}

func TestDirectoryChooserTypedPathAttachesAndBrowseOpensPicker(t *testing.T) {
	a := open(t)
	knownRepos(t, a)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "plain"), 0700); err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "project", "unpin", "work", nil)
	openPicker(t, a)
	typeText(t, a, "ochazuke")
	pressSettle(t, a, "enter")
	typeText(t, a, filepath.Join(dir, "plain"))
	press(t, a, "enter")
	if p, _ := a.project("ochazuke"); p.DefaultTree != filepath.Join(dir, "plain") {
		t.Fatalf("typed path default %q", p.DefaultTree)
	}
	openPicker(t, a)
	typeText(t, a, "matomeru")
	press(t, a, "enter")
	typeText(t, a, "browse")
	press(t, a, "enter")
	if a.r.Page != "directories" || a.r.TopModal() != "picker" || a.directories.ProjectID != "matomeru" {
		t.Fatalf("browse: page %s modal %q project %q", a.r.Page, a.r.TopModal(), a.directories.ProjectID)
	}
}
