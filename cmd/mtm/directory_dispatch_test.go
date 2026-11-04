package main

import (
	"path/filepath"
	"testing"

	"ofam.dev/ochazuke"
)

// project.directory with no path (d) opens the directory chooser over the
// current page and loads the known directories behind it (directory-chooser
// known: the path chooser on its known source), and a typed path that
// does not exist is still attached (record first).
func TestProjectDirectoryChooserDeliversAcrossPages(t *testing.T) {
	t.Parallel()
	a := open(t)
	testEnv(a, "MTM_PROJECT_BASE_DIRS", "")
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
	if got := labels(c.Matches()); len(got) != 0 {
		t.Fatalf("expected no known directories: %q", got)
	}
	// A typed path that is not there is still attached (record first).
	typePath(t, a, "/missing-attachment")
	pressSettle(t, a, "enter")
	p, err := a.work.Project("matomeru")
	if err != nil {
		t.Fatal(err)
	}
	if p.DefaultTree != "/missing-attachment" || len(p.Trees) != 1 {
		t.Fatalf("missing path not attached: %+v", p)
	}
	if a.r.TopModal() != "" || a.r.Page != "work" {
		t.Fatalf("chooser did not close: page %s modal %q", a.r.Page, a.r.TopModal())
	}
}
