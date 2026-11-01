package main

import (
	"io"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/work"
)

func TestProjectPinCompletesAndAcceptsNames(t *testing.T) {
	t.Parallel()
	a := open(t)
	if err := a.work.PutProject(work.Project{ID: "opaque-id", Name: "Readable project"}); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	sh := a.shell(strings.NewReader(""), io.Discard, io.Discard)
	choices, err := sh.Complete([]string{"project", "pin", "Readable"})
	if err != nil || len(choices) != 1 || choices[0].ID != "Readable project" {
		t.Fatalf("choices=%+v err=%v", choices, err)
	}
	flagged, err := sh.Complete([]string{"project", "pin", "--project=Readable"})
	if err != nil || len(flagged) != 1 || flagged[0].ID != "--project=Readable project" {
		t.Fatalf("flag choices=%+v err=%v", flagged, err)
	}
	for _, ch := range a.projectChoices() {
		if ch.ID == "Readable project" {
			t.Fatal("id-based project sources changed")
		}
	}
	for _, name := range []string{"Readable project", "readable PROJECT", "opaque-id"} {
		mustCLI(t, a, "project", "pin", name)
		if a.pin != "opaque-id" {
			t.Fatalf("pin %q=%s", name, a.pin)
		}
	}
	mustCLI(t, a, "project", "pin", "")
	if a.pin != "" {
		t.Fatalf("empty project did not clear pin: %s", a.pin)
	}
}
