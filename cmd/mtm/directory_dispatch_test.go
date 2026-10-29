package main

import (
	"path/filepath"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// project.directory with no path (d) opens the directory chooser over the
// current page and loads the known directories behind it (directory-chooser
// known: the classic chooser; path_sources_test.go has the path chooser); browse… goes to
// the Directories page with the framework picker, and a typed path that
// does not exist is still attached (record first).
func TestProjectDirectoryChooserDeliversAcrossPages(t *testing.T) {
	t.Setenv("MTM_PROJECT_BASE_DIRS", "")
	a := open(t)
	knownDirectoryChooser(t, a)
	a.daemon = Options{Socket: filepath.Join(t.TempDir(), "absent.sock")}.client()
	if _, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "directory", Target: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	c := a.r.Chooser(dirChooser)
	if a.r.Page != "work" || c == nil || !a.directories.Ready {
		t.Fatalf("chooser did not load: page=%s modal=%q cache=%+v", a.r.Page, a.r.TopModal(), a.directories)
	}
	if got := labels(c.Matches()); len(got) != 1 || got[0] != "browse…" {
		t.Fatalf("expected only browse: %q", got)
	}
	press(t, a, "enter")
	if a.r.Page != "directories" || a.r.TopModal() != "picker" || a.directories.ProjectID != "matomeru" {
		t.Fatalf("browse: page %s modal %q project %q", a.r.Page, a.r.TopModal(), a.directories.ProjectID)
	}
	// Cancel browsing, then type an absent path; attachment must still record it.
	if _, err := a.r.Execute(ochazuke.Request{Noun: "picker", Verb: "close"}); err != nil {
		t.Fatal(err)
	}
	a.r.Input(directoryPathID).Text = []rune("/missing-attachment")
	if _, err := a.r.Execute(ochazuke.Request{Noun: "directory-path", Verb: "attach", Target: directoryPathID}); err != nil {
		t.Fatal(err)
	}
	p, err := a.work.Project("matomeru")
	if err != nil {
		t.Fatal(err)
	}
	if p.DefaultTree != "/missing-attachment" || len(p.Trees) != 1 {
		t.Fatalf("missing path not attached: %+v", p)
	}
	if a.r.Input(directoryPathID) != nil || a.directories.Browse != "" || a.r.Page != "directories" {
		t.Fatalf("browse did not close: page %s browse %q", a.r.Page, a.directories.Browse)
	}
	if a.projectDirList.Len() != 1 {
		t.Fatalf("project directories %+v", a.projectDirList.Items())
	}
}
