package work

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/unixsuperhero/matomeru/store"
)

func TestSavedRecipeSurvivesRestartAndForgetPreservesRecipeAndOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	recipe := PaneRecipe{Window: "original", Session: "mtm-only", Name: "scratch", Command: []string{"/bin/sh", "-c", "printf '%s' 'a b'; exec /bin/sh"}, Env: map[string]string{"CUSTOM": "with spaces"}, Cwd: t.TempDir(), Harness: "proof", Model: "exact-model", Flags: []string{"-c", "printf '%s' 'a b'; exec /bin/sh"}}
	if err := w.PutPaneRecipe(recipe); err != nil {
		t.Fatal(err)
	}
	if err := w.SetPaneOwner(Assignment{Window: recipe.Window, Session: recipe.Session}); err != nil {
		t.Fatal(err)
	}
	saved, err := w.PutSavedPane(SavedPane{PaneRecipe: recipe})
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" || saved.Created == 0 || saved.Updated == 0 {
		t.Fatalf("missing identity/timestamps: %+v", saved)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w, err = Open(db)
	if err != nil {
		t.Fatal(err)
	}
	got, err := w.SavedPane(saved.ID)
	if err != nil || !reflect.DeepEqual(got, saved) {
		t.Fatalf("restart: %+v, %v; want %+v", got, err, saved)
	}
	got.Name, got.FormerProjectID, got.FormerTaskID = "renamed", "deleted-project", "deleted-task"
	got, err = w.PutSavedPane(got)
	if err != nil {
		t.Fatal(err)
	}
	got.Name = "again"
	if _, err := w.PutSavedPane(got); err != nil {
		t.Fatal(err)
	}
	if got.ID != saved.ID || got.Created != saved.Created || got.FormerProjectID != "deleted-project" || got.FormerTaskID != "deleted-task" {
		t.Fatalf("edit lost fields: %+v", got)
	}
	if err := w.ForgetSavedPane(saved.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.SavedPane(saved.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("record still exists: %v", err)
	}
	kept, err := w.PaneRecipe(recipe.Window)
	if err != nil || !reflect.DeepEqual(kept.Command, recipe.Command) || !reflect.DeepEqual(kept.Env, recipe.Env) {
		t.Fatalf("forget lost recipe: %+v, %v", kept, err)
	}
	owner, err := w.PaneOwner(recipe.Window, "")
	if err != nil || owner.Window != recipe.Window {
		t.Fatalf("forget changed pane ownership: %+v, %v", owner, err)
	}
}
