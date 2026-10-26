package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// openPicker presses P and checks the search input holds the focus in insert.
func openPicker(t *testing.T, a *App) *ochazuke.Chooser {
	t.Helper()
	press(t, a, "P")
	c := a.r.Chooser(pinPicker)
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

func labels(rows []ochazuke.ChooserRow) []string {
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
	if got := labels(c.Matches()); strings.Join(got, "|") != `◆ ochazuke|+ new project "chz"|no pin (unpin)` {
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

// projectCount is how many projects the store holds.
func projectCount(t *testing.T, a *App) int {
	t.Helper()
	ps, err := a.work.Projects()
	if err != nil {
		t.Fatal(err)
	}
	return len(ps)
}

// With nothing matching, the create row is the only actionable row and is
// highlighted: Enter records the project before the directory chooser.
func TestPinPickerEnterOnNoMatchCreates(t *testing.T) {
	a := open(t)
	before := projectCount(t, a)
	c := openPicker(t, a)
	typeText(t, a, "zebra")
	if row, ok := c.Highlighted(); !ok || row.ID != "chooser:create" || row.Label != `+ new project "zebra"` {
		t.Fatalf("highlight %+v", row)
	}
	press(t, a, "enter")
	p, ok := a.project(a.pin)
	if !ok || p.Name != "zebra" || a.r.TopModal() == pinPicker || projectCount(t, a) != before+1 {
		t.Fatalf("miss created %+v pin %q", p, a.pin)
	}
	leaveDirectoryChooser(t, a)
}

// ctrl+n and ctrl+p move like down and up; no key creates a project.
func TestPinPickerCtrlNMovesAndNeverCreates(t *testing.T) {
	a := open(t)
	executeA(t, a, "project", "unpin", "work", nil)
	before := projectCount(t, a)
	c := openPicker(t, a)
	typeText(t, a, "o")
	if got := strings.Join(labels(c.Matches()), "|"); got != `◆ matomeru|◆ ochazuke|+ new project "o"|no pin (unpin)` {
		t.Fatalf("rows %q", got)
	}
	press(t, a, "ctrl+n")
	if row, _ := c.Highlighted(); row.Data != "ochazuke" || !c.Moved() {
		t.Fatalf("ctrl+n highlight %+v", row)
	}
	press(t, a, "ctrl+n", "ctrl+n", "ctrl+n")
	if row, _ := c.Highlighted(); row.ID != unpinRow {
		t.Fatalf("ctrl+n clamps on the unpin row, got %+v", row)
	}
	press(t, a, "ctrl+p", "ctrl+p", "ctrl+p")
	if row, _ := c.Highlighted(); row.Data != "matomeru" {
		t.Fatalf("ctrl+p highlight %+v", row)
	}
	if n := projectCount(t, a); n != before || a.r.TopModal() != pinPicker || a.pin != "" {
		t.Fatalf("projects %d want %d, modal %q pin %q", n, before, a.r.TopModal(), a.pin)
	}
	press(t, a, "enter")
	if a.pin != "matomeru" || projectCount(t, a) != before {
		t.Fatalf("pin %q projects %d", a.pin, projectCount(t, a))
	}
	leaveDirectoryChooser(t, a)
}

// A query that matches keeps the top match as Enter's target; moving onto
// the create row and pressing Enter creates and pins.
func TestPinPickerCreateRowCreatesAndPins(t *testing.T) {
	a := open(t)
	executeA(t, a, "project", "unpin", "work", nil)
	before := projectCount(t, a)
	c := openPicker(t, a)
	typeText(t, a, "mato")
	if row, _ := c.Highlighted(); row.Data != "matomeru" {
		t.Fatalf("default %+v", row)
	}
	press(t, a, "ctrl+n")
	if row, _ := c.Highlighted(); row.ID != "chooser:create" {
		t.Fatalf("ctrl+n onto %+v of %q", row, labels(c.Matches()))
	}
	press(t, a, "enter")
	p, ok := a.project(a.pin)
	if !ok || p.Name != "mato" || p.ID == "matomeru" || projectCount(t, a) != before+1 {
		t.Fatalf("created %+v pin %q", p, a.pin)
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

func TestPinPickerUnpinRowIsFixedAndClearsPinAcrossRestart(t *testing.T) {
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
	typeText(t, a, "chz")
	if got := strings.Join(labels(c.Matches()), "|"); got != `◆ ochazuke|+ new project "chz"|no pin (unpin)` {
		t.Fatalf("chz rows %q", got)
	}
	press(t, a, "down", "down")
	if row, _ := c.Highlighted(); row.ID != unpinRow {
		t.Fatalf("down from the top match lands on %+v", row)
	}
	press(t, a, "enter")
	if a.pin != "" || a.r.TopModal() != "" || a.m.Title != "Matomeru · no pin" {
		t.Fatalf("pin %q modal %q title %q", a.pin, a.r.TopModal(), a.m.Title)
	}
	b := openAt(t, path)
	if b.pin != "" {
		t.Fatalf("reopened pin %q", b.pin)
	}
	// With no query, moving to the bottom reaches it too.
	executeA(t, b, "project", "pin", "work", ochazuke.Arguments{"project": "ochazuke"})
	c = openPicker(t, b)
	press(t, b, "down", "down", "down")
	if row, _ := c.Highlighted(); row.ID != unpinRow {
		t.Fatalf("bottom row %+v", row)
	}
	press(t, b, "enter")
	if b.pin != "" {
		t.Fatalf("pin %q", b.pin)
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
		for _, want := range []string{"Pin project", "◆ matomeru  2 open", "~/proj/clients", "no pin (unpin)", "enter chooser.pick", "esc chooser.cancel"} {
			if !strings.Contains(frame, want) {
				t.Fatalf("at %d missing %q:\n%s", width, want, frame)
			}
		}
		if strings.Contains(frame, "chooser.new") {
			t.Fatalf("at %d a key still creates:\n%s", width, frame)
		}
		for i, line := range strings.Split(frame, "\n") {
			if w := ansi.StringWidth(line); w > width {
				t.Fatalf("at %d line %d is %d wide: %q", width, i, w, line)
			}
		}
		t.Logf("frame at %d:\n%s", width, frame)
	}
	// With a query, the create row sits above the unpin row, which stays
	// at the bottom; with no match it is the highlighted row, and there is
	// no separate "enter:" hint. The last frame has the highlight moved
	// onto the create row while a project matches.
	a.m.Width, a.m.Height = 80, 24
	for _, query := range []string{"och", "zz", "och+move"} {
		press(t, a, "ctrl+u")
		typeText(t, a, strings.TrimSuffix(query, "+move"))
		if query == "och+move" { // past ochazuke and matomeru
			press(t, a, "ctrl+n", "ctrl+n")
		}
		frame := ansi.Strip(a.View().Content)
		want := []string{"◆ ochazuke", `+ new project "och"`, "no pin (unpin)"}
		if query == "zz" {
			want = []string{"no matches", `+ new project "zz"`, "no pin (unpin)"}
		}
		if strings.Contains(frame, "enter:") {
			t.Fatalf("query %q shows a create hint beside the row:\n%s", query, frame)
		}
		body := strings.Split(c.View(60, 10).Text, "\n")
		lit := "+ new project"
		if query == "och" {
			lit = "◆ ochazuke"
		}
		for _, line := range body {
			if strings.Contains(line, "\x1b[7m") != strings.Contains(line, lit) {
				t.Fatalf("query %q highlight on %q", query, body)
			}
		}
		at := 0
		for _, w := range want {
			i := strings.Index(frame[at:], w)
			if i < 0 {
				t.Fatalf("query %q: %q missing or out of order:\n%s", query, w, frame)
			}
			at += i + len(w)
		}
		for i, line := range strings.Split(frame, "\n") {
			if w := ansi.StringWidth(line); w > 80 {
				t.Fatalf("query %q line %d is %d wide", query, i, w)
			}
		}
		t.Logf("frame at 80 with %q:\n%s\nhighlight: %q", query, frame, ansi.Strip(body[slices.IndexFunc(body, func(l string) bool { return strings.Contains(l, "\x1b[7m") })]))
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
	c := a.r.Chooser(dirChooser)
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
	c := a.r.Chooser(dirChooser)
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

// Typing the unpin row's words reaches it even when a project fuzzily
// matches ("unp" in "unixsuperhero"), and never the create row (UX review 2
// F1, replacing f55ecf6's "typing never reaches unpin").
func TestPinPickerTypingReachesUnpin(t *testing.T) {
	path := seeded(t)
	a := openAt(t, path)
	for _, name := range []string{"tuios-inbox", "unixsuperhero"} {
		if _, err := a.work.AddProject(name); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": "matomeru"})
	c := openPicker(t, a)
	typeText(t, a, "unp")
	if row, ok := c.Highlighted(); !ok || row.ID != unpinRow {
		t.Fatalf("unp highlights %+v of %q", row, labels(c.Matches()))
	}
	press(t, a, "esc")
	for _, query := range []string{"no pin", "unpin"} {
		c = openPicker(t, a)
		typeText(t, a, query)
		if row, ok := c.Highlighted(); !ok || row.ID != unpinRow {
			t.Fatalf("%q highlights %+v", query, row)
		}
		if got := labels(c.Matches()); len(got) != 2 || got[1] != "no pin (unpin)" {
			t.Fatalf("%q rows %q", query, got)
		}
		press(t, a, "esc")
	}
	before := projectCount(t, a)
	openPicker(t, a)
	typeText(t, a, "unpin")
	press(t, a, "enter")
	if a.pin != "" || projectCount(t, a) != before {
		t.Fatalf("unpin enter: pin %q projects %d want %d", a.pin, projectCount(t, a), before)
	}
	if b := openAt(t, path); b.pin != "" {
		t.Fatalf("unpin not recorded: pin %q", b.pin)
	}
}

// ctrl+u in the pin picker clears the query like any other input.
func TestPinPickerCtrlUClearsQuery(t *testing.T) {
	a := open(t)
	executeA(t, a, "project", "pin", "work", ochazuke.Arguments{"project": "matomeru"})
	c := openPicker(t, a)
	typeText(t, a, "chz")
	press(t, a, "ctrl+u")
	if c.Query() != "" || a.r.TopModal() != pinPicker || a.pin != "matomeru" {
		t.Fatalf("query %q modal %q pin %q", c.Query(), a.r.TopModal(), a.pin)
	}
	press(t, a, "esc")
}

// Test pass R3 and W6: at 80x24 with many projects the "no pin (unpin)"
// row stays on screen at the bottom (the matches shrink, and scroll as the
// highlight moves), and "unpin" matching only across a row's name and
// directory makes the create row Enter's target, not that project.
func TestPinPickerKeepsFixedRowsAndWantsAStrongMatch(t *testing.T) {
	a := open(t)
	for i := range 25 {
		if _, err := a.work.AddProject("extra-" + string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	inbox, err := a.work.AddProject("tuios-inbox")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.work.AddTree(inbox.ID, work.Tree{Path: "/x/proj/tuios-inbox"}); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	c := openPicker(t, a)
	if f := frameAt(a, 80); !strings.Contains(f, "no pin (unpin)") {
		t.Fatalf("no pin row scrolled off at 80x24:\n%s", f)
	}
	for range 20 {
		press(t, a, "ctrl+n")
	}
	f := frameAt(a, 80)
	row, _ := c.Highlighted()
	if !strings.Contains(f, "no pin (unpin)") || !strings.Contains(f, strings.TrimPrefix(row.Label, "◆ ")) {
		t.Fatalf("after 20 ctrl+n (on %q) the highlight or no pin is off screen:\n%s", row.Label, f)
	}
	press(t, a, "esc")
	c = openPicker(t, a)
	typeText(t, a, "unpin")
	if got := labels(c.Matches()); !slices.ContainsFunc(got, func(l string) bool { return strings.Contains(l, "tuios-inbox") }) {
		t.Fatalf("tuios-inbox should still be listed: %q", got)
	}
	if row, ok := c.Highlighted(); !ok || row.ID != unpinRow {
		t.Fatalf("unpin highlights %+v", row)
	}
	press(t, a, "esc")
	c = openPicker(t, a)
	typeText(t, a, "inbox")
	if row, _ := c.Highlighted(); row.Data != inbox.ID {
		t.Fatalf("inbox highlights %+v", row)
	}
}

// Test pass R4: a chooser waiting for its rows is as wide as it will be
// with them, and a wide column (a long project list) narrows so the path
// column stays in view.
func TestChooserColumnsFitAndLoadingKeepsWidth(t *testing.T) {
	a := open(t)
	c, err := a.r.OpenChooser(ochazuke.ChooserSpec{Kind: "fit-test", Title: "Fit", Empty: "loading…"})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(ansi.Strip(c.View(74, 10).Text), "\n")[1:] {
		if ansi.StringWidth(line) != 74 {
			t.Fatalf("loading line %q is %d wide, want 74", line, ansi.StringWidth(line))
		}
	}
	c.SetRows([]ochazuke.ChooserRow{{ID: "r", Label: "repo", Data: "/p/repo", Cells: []string{"repo", "~/proj/some/repo", "main", "never", "[" + strings.Repeat("project, ", 12) + "]"}}})
	view := ansi.Strip(c.View(74, 10).Text)
	if !strings.Contains(view, "~/proj/some/repo") || !strings.Contains(view, "never") {
		t.Fatalf("path or age cut:\n%s", view)
	}
}

// Typing the unpin row's words highlights it, not the create row, so
// Enter unpins instead of making a project named "no pin" (UX review 2 F1).
func TestPinPickerQuerySpellingUnpinHighlightsIt(t *testing.T) {
	for _, query := range []string{"unp", "unpin", "no pin"} {
		t.Run(query, func(t *testing.T) {
			a := open(t)
			before := projectCount(t, a)
			c := openPicker(t, a)
			typeText(t, a, query)
			if row, _ := c.Highlighted(); row.ID != unpinRow {
				t.Fatalf("highlight %+v, rows %q", row, labels(c.Matches()))
			}
			press(t, a, "enter")
			if a.pin != "" || projectCount(t, a) != before {
				t.Fatalf("pin %q projects %d want %d", a.pin, projectCount(t, a), before)
			}
		})
	}
}

// UX review 2 F22: a project left without a directory (esc in the
// directory chooser) is not asked again on the next pin.
func TestADirectorylessProjectIsAskedOnce(t *testing.T) {
	a := open(t)
	knownRepos(t, a, "alpha")
	executeA(t, a, "project", "unpin", "work", nil)
	openPicker(t, a)
	typeText(t, a, "matomeru")
	pressSettle(t, a, "enter")
	if a.r.Chooser(dirChooser) == nil {
		t.Fatalf("first pin: modal %q", a.r.TopModal())
	}
	press(t, a, "esc")
	executeA(t, a, "project", "unpin", "work", nil)
	openPicker(t, a)
	typeText(t, a, "matomeru")
	pressSettle(t, a, "enter")
	if a.pin != "matomeru" || a.r.TopModal() != "" {
		t.Fatalf("second pin: pin %q modal %q", a.pin, a.r.TopModal())
	}
}

// UX review 2 F22: the directory chooser cuts the branch and projects
// columns before the path. A known gap since mtm draws its choosers with
// ochazuke's: its ChooserSpec has no Keep, so the widest column (the
// path) narrows first.
func TestDirectoryChooserCutsThePathLast(t *testing.T) {
	t.Skip("gap: ochazuke ChooserSpec has no Keep (UX review 2 F22); the path narrows first")
	r := ochazuke.NewRuntime()
	path := "~/proj/some/deep/repo-ab" // 24 cells
	c, err := r.OpenChooser(ochazuke.ChooserSpec{Kind: "keep-test", Rows: []ochazuke.ChooserRow{{ID: "r", Label: "repo", Cells: []string{"repo-name1", path, "feature/branch-abc", "3d ago", "[alpha, beta, ga]"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if view := ansi.Strip(c.View(72, 6).Text); !strings.Contains(view, path) {
		t.Fatalf("path cut:\n%s", view)
	}
}
