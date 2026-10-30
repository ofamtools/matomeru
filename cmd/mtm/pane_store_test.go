package main

import (
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// standardIs checks a noun's verbs: each present with its standard mark
// ("" for a named verb), and the absent ones absent.
func standardIs(t *testing.T, a *App, noun string, want map[string]string, absent ...string) {
	t.Helper()
	for verb, standard := range want {
		op, ok := a.r.Operation(noun + "." + verb)
		if !ok || op.Standard != standard {
			t.Errorf("%s.%s: ok %v standard %q, want %q", noun, verb, ok, op.Standard, standard)
		}
	}
	for _, verb := range absent {
		if _, ok := a.r.Operation(noun + "." + verb); ok {
			t.Errorf("%s.%s should not exist", noun, verb)
		}
	}
}

// The pane's verbs: get, list, set, watch and move are standard; no
// create or delete (kill is a real action); rename, associate and the
// agent's set-* verbs are named; star is an alias of set (starred,
// toggled) and unassociate a lasting alias of move.
func TestPaneVerbsAreStandard(t *testing.T) {
	a, _ := panesApp(t)
	standardIs(t, a, "pane", map[string]string{
		"get": "get", "list": "list", "set": "set", "watch": "watch", "move": "move", "star": "set",
		"rename": "", "associate": "", "choose-move": "", "kill": "",
		"set-model": "", "set-effort": "", "set-fast": "", "set-permission-mode": "", "toggle-fast": "",
	}, "create", "delete", "duplicate")
	var found bool
	for _, alias := range a.r.AliasesOf("pane.move") {
		if alias.Alias == "pane.unassociate" {
			found = !alias.Deprecated()
		}
	}
	if !found {
		t.Fatal("pane.unassociate is not a lasting alias of pane.move")
	}
}

// pane get answers its props from the listing; the agent's settings are
// read-only (set refuses them; set-model changes them in the session).
func TestPaneGetAndReadOnlySettings(t *testing.T) {
	a, _ := panesApp(t)
	thing, _ := executeA(t, a, "pane", "get", "", ochazuke.Arguments{"id": "w1"}).(map[string]any)
	if thing["name"] != "claude" || thing["session"] != "matomeru" || thing["display"] != "matomeru/claude" {
		t.Fatalf("get: %v", thing)
	}
	_, err := a.r.Execute(ochazuke.Request{Noun: "pane", Verb: "set", Args: ochazuke.Arguments{"id": "w1", "prop": "model", "value": "opus"}})
	if err == nil {
		t.Fatal("set model was written")
	}
	rows, _ := executeA(t, a, "pane", "list", "", ochazuke.Arguments{"all": true}).([]map[string]any)
	if len(rows) != 2 {
		t.Fatalf("list: %v", rows)
	}
}

// * on a pane row stars it through the standard set, and undo takes the
// star back.
func TestPaneStarKeyAndUndo(t *testing.T) {
	a, _ := panesApp(t)
	row := "pane:matomeru/w1"
	if err := a.selectPaneRow(row); err != nil {
		t.Fatal(err)
	}
	press(t, a, "*")
	if stars, _ := a.work.PaneStars(); len(stars) != 1 {
		t.Fatalf("* stars: %v", stars)
	}
	executeA(t, a, "history", "undo", "", nil)
	if stars, _ := a.work.PaneStars(); len(stars) != 0 {
		t.Fatalf("undo kept the star: %v", stars)
	}
}

// pane rename NAME is the standard set of name: the daemon renames the
// window in a job named pane.rename; undo renames it back.
func TestPaneRenameIsSetAndUndoes(t *testing.T) {
	a, f := panesApp(t)
	fr := &fakeRenamer{panes: f}
	a.renamer = fr
	row := "pane:matomeru/w1"
	executeA(t, a, "pane", "rename", row, ochazuke.Arguments{"name": "fixer"})
	settle(a, a.r.PendingCommands())
	if len(jobsOf(a, "pane.rename")) != 1 {
		t.Fatalf("jobs: %+v", a.r.Jobs())
	}
	executeA(t, a, "history", "undo", "", nil)
	settle(a, a.r.PendingCommands())
	if got := strings.Join(fr.calls, "; "); got != "window matomeru/w1 fixer; window matomeru/w1 claude" {
		t.Fatalf("calls %s", got)
	}
}

// m on a pane row opens the project chooser, whose choice is the standard
// move; unassociate moves it to no project.
func TestPaneMoveKeyAndUnassociate(t *testing.T) {
	a, _ := panesApp(t)
	row := "pane:matomeru/w1"
	if err := a.selectPaneRow(row); err != nil {
		t.Fatal(err)
	}
	press(t, a, "m")
	if a.r.Chooser(moveChooser) == nil {
		t.Fatalf("m opened %q", a.r.TopModal())
	}
	typeText(t, a, "ochaz")
	press(t, a, "enter")
	if o, _ := a.work.PaneOwner("w1", ""); o.ProjectID != "ochazuke" {
		t.Fatalf("m: %+v", o)
	}
	executeA(t, a, "pane", "unassociate", "", ochazuke.Arguments{"id": row})
	if o, _ := a.work.PaneOwner("w1", ""); o.ProjectID != "" || o.TaskID != "" {
		t.Fatalf("unassociate: %+v", o)
	}
}
