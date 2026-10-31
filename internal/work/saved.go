package work

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/unixsuperhero/matomeru/store"
)

// PaneRecipe records the request confirmed by the daemon, not a foreground
// process guessed later. Empty Command means the daemon's default shell.
type PaneRecipe struct {
	Window          string            `json:"window"`
	Session         string            `json:"session"`
	SessionID       string            `json:"session_id,omitempty"`
	Name            string            `json:"name"`
	Command         []string          `json:"command"`
	Env             map[string]string `json:"env,omitempty"`
	Cwd             string            `json:"cwd"`
	Harness         string            `json:"harness,omitempty"`
	Model           string            `json:"model,omitempty"`
	Flags           []string          `json:"flags,omitempty"`
	ProjectID       string            `json:"project_id"`
	TaskID          string            `json:"task_id"`
	FormerProjectID string            `json:"former_project_id,omitempty"`
	FormerTaskID    string            `json:"former_task_id,omitempty"`
	Created         int64             `json:"created"`
	Updated         int64             `json:"updated"`
	// LaunchedBy is the pane of the agent that launched this one (empty
	// for the owner): a child pane its agent may associate (actor.go).
	LaunchedBy string `json:"launched_by,omitempty"`
}

type SavedPane struct {
	ID string `json:"id"`
	PaneRecipe
}

func (s *Store) PaneRecipe(window string) (PaneRecipe, error) {
	var recipe PaneRecipe
	err := s.db.Get("pane_recipes", window, &recipe)
	return recipe, err
}

func (s *Store) PutPaneRecipe(recipe PaneRecipe) error {
	if recipe.Window == "" {
		return errors.New("recipe requires a pane id")
	}
	if recipe.Created == 0 {
		recipe.Created = Now()
	}
	recipe.Updated = Now()
	if me := s.Actor(); recipe.LaunchedBy == "" && me.IsAgent() && me.Pane != recipe.Window {
		recipe.LaunchedBy = me.Pane
	}
	return s.db.Tx([]string{"pane_recipes"}, func(tx *store.Tx) error { return tx.Put("pane_recipes", recipe.Window, recipe) })
}

func (s *Store) SavedPanes() ([]SavedPane, error) {
	rows, err := all[SavedPane](s.db, "saved_panes")
	slices.SortFunc(rows, func(a, b SavedPane) int { return cmp.Or(cmp.Compare(a.Created, b.Created), cmp.Compare(a.ID, b.ID)) })
	return rows, err
}

func (s *Store) SavedPane(id string) (SavedPane, error) {
	var row SavedPane
	err := s.db.Get("saved_panes", id, &row)
	return row, err
}

func (s *Store) PutSavedPane(row SavedPane) (SavedPane, error) {
	if row.ID == "" {
		row.ID = NewID()
	}
	if row.Created == 0 {
		row.Created = Now()
	}
	row.Updated = Now()
	err := s.db.Tx([]string{"saved_panes"}, func(tx *store.Tx) error { return tx.Put("saved_panes", row.ID, row) })
	return row, err
}

func (s *Store) ForgetSavedPane(id string) error {
	return s.db.Tx([]string{"saved_panes"}, func(tx *store.Tx) error {
		var row SavedPane
		if err := tx.Get("saved_panes", id, &row); err != nil {
			return fmt.Errorf("saved pane %s: %w", id, err)
		}
		return tx.Delete("saved_panes", id)
	})
}
