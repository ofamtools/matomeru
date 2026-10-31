package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// savedRecord stores a record and opens the Saved page on it.
func savedRecord(t *testing.T, a *App) work.SavedPane {
	t.Helper()
	row, err := a.work.PutSavedPane(work.SavedPane{PaneRecipe: work.PaneRecipe{Window: "w2", Session: "ochazuke", Name: "tests",
		Command: []string{"go", "test"}, Env: map[string]string{"A": "1"}, Cwd: "/tmp/x"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.refreshSaved(); err != nil {
		t.Fatal(err)
	}
	return row
}

// The saved verbs: the six standard ones with move and duplicate; forget a
// lasting alias of delete; rename, save, reopen and associate named.
func TestSavedVerbsAreStandard(t *testing.T) {
	t.Parallel()
	a := open(t)
	standardIs(t, a, "saved", map[string]string{
		"get": "get", "list": "list", "create": "create", "set": "set", "delete": "delete", "watch": "watch",
		"move": "move", "duplicate": "duplicate",
		"rename": "", "save": "", "reopen": "", "associate": "", "choose-move": "",
	})
	var forget *ochazuke.ActionAlias
	for _, alias := range a.r.AliasesOf("saved.delete") {
		if alias.Alias == "saved.forget" {
			forget = &alias
		}
	}
	if forget == nil || forget.Deprecated() {
		t.Fatalf("forget: %+v", forget)
	}
}

// d d on a saved row asks "Forget ...? Its pane keeps running." and
// deletes the record; undo puts it back exactly as it was.
func TestSavedForgetKeyAndUndo(t *testing.T) {
	t.Parallel()
	a := open(t)
	row := savedRecord(t, a)
	if err := a.r.OpenPage(savedPage); err != nil {
		t.Fatal(err)
	}
	if err := a.savedList.Select(row.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.savedList.Focus(); err != nil {
		t.Fatal(err)
	}
	press(t, a, "d", "d")
	if fr := frame(a); !strings.Contains(fr, "Forget tests? Its pane keeps running.") {
		t.Fatalf("d d:\n%s", fr)
	}
	press(t, a, "y")
	if _, err := a.work.SavedPane(row.ID); err == nil {
		t.Fatal("y kept the record")
	}
	executeA(t, a, "history", "undo", "", nil)
	back, err := a.work.SavedPane(row.ID)
	back.Updated, row.Updated = 0, 0
	if err != nil || !reflect.DeepEqual(back, row) {
		t.Fatalf("undo: %+v %v, want %+v", back, err, row)
	}
}

// A move of a record moves its live pane with it; rename NAME is the
// standard set; create makes a recipe by hand (no command: a daemon
// shell), never refused.
func TestSavedMoveRenameCreate(t *testing.T) {
	t.Parallel()
	a := open(t)
	row := savedRecord(t, a)
	executeA(t, a, "saved", "move", "", ochazuke.Arguments{"id": row.ID, "to": "matomeru"})
	if o, _ := a.work.PaneOwner("w2", ""); o.ProjectID != "matomeru" {
		t.Fatalf("the pane did not follow: %+v", o)
	}
	executeA(t, a, "saved", "rename", "", ochazuke.Arguments{"id": row.ID, "name": "unit"})
	if got, _ := a.work.SavedPane(row.ID); got.Name != "unit" || got.ProjectID != "matomeru" {
		t.Fatalf("rename: %+v", got)
	}
	made, _ := executeA(t, a, "saved", "create", "", ochazuke.Arguments{"name": "by hand", "cwd": "/tmp"}).(map[string]any)
	id, _ := made["id"].(string)
	if got, err := a.work.SavedPane(id); err != nil || got.Name != "by hand" || got.Cwd != "/tmp" || len(got.Command) != 0 {
		t.Fatalf("create: %+v %v", got, err)
	}
}
