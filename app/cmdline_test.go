package app

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func TestShellIORedirectionCommandLine(t *testing.T) {
	a, d := demo(t)
	task := mkRec(t, a, data.KindTask, map[string]any{"title": "ship v2", "defaultDir": t.TempDir()})
	browseTo(t, a, d, task)

	// 1. ! opens commandline with $REF/$TITLE/$DIR expansion, ctrl+r % register paste, and Readline editing.
	d.Keys("!")
	if a.Modal() != "commandline" || a.Mode() != ModeCommandLine {
		t.Fatalf("! did not open commandline (modal=%q mode=%q)", a.Modal(), a.Mode())
	}
	d.Type("echo $TITLE typo")
	d.Keys("ctrl+w") // kills "typo"
	d.Keys("ctrl+r", "%")
	d.Keys("enter")
	if a.Modal() != "" {
		t.Fatalf("enter did not close commandline (modal=%q)", a.Modal())
	}
	runsRes, err := a.Store.Query(data.Query{Kind: data.KindRun})
	if err != nil {
		t.Fatal(err)
	}
	foundShell := false
	for _, r := range runsRes.Items {
		cmd := r.Text("command")
		if strings.Contains(cmd, "'ship v2'") && strings.Contains(cmd, data.RefText(task.Ref())) {
			foundShell = true
		}
	}
	if !foundShell {
		t.Fatalf("expected shell run with expanded $TITLE and ctrl+r %% register; runs=%+v", runsRes.Items)
	}

	// 2. | pipes the focused record's JSON into a shell command, captures output in a run and yanks it.
	browseTo(t, a, d, task)
	d.Keys("|")
	if a.Modal() != "commandline" || a.cmdline.Prefix != "|" {
		t.Fatalf("| did not open pipe commandline")
	}
	d.Type("tr a-z A-Z")
	d.Keys("enter")
	a.WaitBackground()
	a.Shell.Update(wakeMsg())
	if !strings.Contains(a.Register(core.RegUnnamed), "SHIP V2") {
		t.Fatalf("pipe stdout not yanked into unnamed register: %q", a.Register(core.RegUnnamed))
	}

	// 3. g ! (r!) reads shell output into a new note attached via about to the focused record.
	d.Keys("g", "!")
	if a.Modal() != "commandline" || a.cmdline.Prefix != "r!" {
		t.Fatalf("g ! did not open r! commandline")
	}
	d.Type("printf 'captured from shell'")
	d.Keys("enter")
	a.WaitBackground()
	a.Shell.Update(wakeMsg())
	rels, err := a.Store.Related(task.Ref(), data.RelAbout, data.Incoming)
	if err != nil {
		t.Fatal(err)
	}
	foundNote := false
	for _, rel := range rels {
		other := rel.Other(task.Ref())
		if note, err := a.Store.Get(other); err == nil && note.Text("body") == "captured from shell" {
			foundNote = true
		}
	}
	if !foundNote {
		t.Fatalf("r! did not create an about-linked note with shell stdout (rels=%+v)", rels)
	}
}
