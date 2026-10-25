package main

import (
	"path/filepath"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

func TestProjectDirectoryChooserDeliversAcrossPages(t *testing.T) {
	t.Setenv("MTM_PROJECT_BASE_DIRS", "")
	a := open(t)
	a.daemon = Options{Socket: filepath.Join(t.TempDir(), "absent.sock")}.client()
	if _, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "directory", Target: "matomeru"}); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	if a.r.Page != "directories" || a.directories.ProjectID != "matomeru" || !a.directories.Ready {
		t.Fatalf("chooser did not load: page=%s cache=%+v", a.r.Page, a.directories)
	}
	if a.directoryList.Len() != 1 {
		t.Fatalf("expected only browse: %+v", a.directoryList.Items())
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "directory-browse", Verb: "open", Target: "directory:browse"}); err != nil {
		t.Fatal(err)
	}
	if a.r.TopModal() != "picker" {
		t.Fatal("browse did not open framework picker")
	}
	// Cancel browsing, then type an absent path; attachment must still record it.
	if _, err := a.r.Execute(ochazuke.Request{Noun: "picker", Verb: "close"}); err != nil {
		t.Fatal(err)
	}
	a.r.Input("directories/path").Text = []rune("/missing-attachment")
	if _, err := a.r.Execute(ochazuke.Request{Noun: "directory-path", Verb: "attach", Target: "directories/path"}); err != nil {
		t.Fatal(err)
	}
	p, err := a.work.Project("matomeru")
	if err != nil {
		t.Fatal(err)
	}
	if p.DefaultTree != "/missing-attachment" || len(p.Trees) != 1 {
		t.Fatalf("missing path not attached: %+v", p)
	}
}
