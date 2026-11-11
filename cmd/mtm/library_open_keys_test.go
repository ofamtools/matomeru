package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"
)

// g o on a task row opens the library's Open line.
func TestOpenLineKeyOpensFromATaskRow(t *testing.T) {
	t.Parallel()
	a := open(t)
	a.m.Width, a.m.Height = 120, 40
	if err := a.reveal("t1"); err != nil {
		t.Fatal(err)
	}
	press(t, a, "g", "o")
	if a.r.RecordEditor(ochazuke.OpenModalID) == nil || a.r.TopModal() != ochazuke.OpenModalID {
		t.Fatalf("g o opened %q", a.r.TopModal())
	}
	if frame := ansi.Strip(frameView(a).Content); !strings.Contains(frame, "a link, a path, a file, noun:id") {
		t.Fatalf("Open line missing from the frame:\n%s", frame)
	}
}

// g . on a task row lists the actions of every level of its key walk (the
// task, its collection, the page, the app) under full noun.verb names.
func TestFocusedActionsKeyListsEveryLevelOfATaskRow(t *testing.T) {
	t.Parallel()
	a := open(t)
	a.m.Width, a.m.Height = 120, 40
	if err := a.reveal("t1"); err != nil {
		t.Fatal(err)
	}
	_, choices := a.r.FocusedActions(a.r.FocusID())
	names := map[string][]string{}
	for _, c := range choices {
		if !strings.Contains(c.ID, ".") {
			t.Fatalf("action %q under %q is not a full noun.verb name", c.ID, c.Noun)
		}
		names[c.Noun] = append(names[c.Noun], c.ID)
	}
	for origin, want := range map[string]string{"task": "task.archive", "ochazuke.collection": "ochazuke.collection.filter-open", "ochazuke.page": "ochazuke.page.next", "app": "app.quit"} {
		if !slices.Contains(names[origin], want) {
			t.Fatalf("level %s lacks %s: %v", origin, want, names[origin])
		}
	}
	press(t, a, "g", ".")
	if a.r.TopModal() != ochazuke.FocusActionsKind {
		t.Fatalf("g . opened %q", a.r.TopModal())
	}
	frame := ansi.Strip(frameView(a).Content)
	for _, want := range []string{"Actions · task", "task.archive", "task.choose-status"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("%q missing from the actions list:\n%s", want, frame)
		}
	}
	typeText(t, a, "app.quit")
	if frame := ansi.Strip(frameView(a).Content); !strings.Contains(frame, "app.quit") || strings.Contains(frame, "task.archive") {
		t.Fatalf("filtering to an app-level action:\n%s", frame)
	}
}

// g F on a Directories row opens its path through the opener table: the
// shipped row reaches the OS opener, and an open.openers row replaces it.
func TestOpenPathKeyUsesOpenerTableOnADirectoryRow(t *testing.T) {
	t.Parallel()
	a := open(t)
	var runs []string
	a.r.SetOpener(ochazuke.Opener{GOOS: "darwin", Start: func(name string, args ...string) error {
		runs = append(runs, name+" "+strings.Join(args, " "))
		return nil
	}})
	root := knownRepos(t, a, "alpha")
	alpha := directoryKey(filepath.Join(root, "alpha"))
	openDirectories(t, a)
	if a.r.FocusID() != "directory:"+alpha {
		t.Fatalf("focus %s", a.r.FocusID())
	}
	settle(a, key(t, a, "g"))
	settle(a, key(t, a, "F"))
	if !slices.Equal(runs, []string{"open -- " + alpha}) || a.r.Page != directoriesPage {
		t.Fatalf("g F ran %v on page %s", runs, a.r.Page)
	}
	if err := a.r.Settings().Set(ochazuke.OpenOpenersSetting, `[{"prefix":"`+directoryKey(root)+`","action":"page","to":"work"}]`); err != nil {
		t.Fatal(err)
	}
	settle(a, key(t, a, "g"))
	settle(a, key(t, a, "F"))
	if len(runs) != 1 || a.r.Page != "work" {
		t.Fatalf("an opener row must replace the OS open: ran %v, page %s", runs, a.r.Page)
	}
}
