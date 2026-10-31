package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// vimSession stores a closed session with an mtm-owned session file.
func vimSession(t *testing.T, a *App) work.VimSession {
	t.Helper()
	if err := os.MkdirAll(a.vimDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.Abs(a.vimDir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "vim-a.vim")
	if err := os.WriteFile(path, []byte("\" session\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	row := work.VimSession{ID: "vim-a", Name: "notes", ProjectID: "matomeru", State: "closed", Path: path, OwnedPath: path, Session: "matomeru", WindowID: "w9"}
	if err := a.work.PutVimSession(row); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshVim(); err != nil {
		t.Fatal(err)
	}
	return row
}

// The Vim verbs: get, list, set, delete, watch, rename and move standard;
// no create (a session is opened, a launch); forget a lasting alias of
// delete; open, save, reopen and choose-move named.
func TestVimVerbsAreStandard(t *testing.T) {
	t.Parallel()
	a := open(t)
	standardIs(t, a, "vim", map[string]string{
		"get": "get", "list": "list", "set": "set", "delete": "delete", "watch": "watch", "rename": "rename", "move": "move",
		"open": "", "save": "", "reopen": "", "choose-move": "",
	}, "create", "duplicate")
	if forget := aliasOf(a, "vim.delete", "vim.forget"); forget == nil || forget.Deprecated() {
		t.Fatalf("forget: %+v", forget)
	}
}

// d d on a session asks "Forget ...?" and removes the record and mtm's
// session file; rename and move are the standard writes, and undo reverts
// them; m opens the project chooser.
func TestVimKeysAndWrites(t *testing.T) {
	t.Parallel()
	a := open(t)
	row := vimSession(t, a)
	executeA(t, a, "vim", "rename", "", ochazuke.Arguments{"id": row.ID, "name": "plans"})
	executeA(t, a, "vim", "move", "", ochazuke.Arguments{"id": row.ID, "to": "ochazuke"})
	if got, _ := a.work.VimSession(row.ID); got.Name != "plans" || got.ProjectID != "ochazuke" {
		t.Fatalf("rename, move: %+v", got)
	}
	if o, err := a.work.PaneOwner("w9", ""); err == nil && o.ProjectID == "ochazuke" {
		t.Fatal("a closed session's stale window was given the owner")
	}
	executeA(t, a, "history", "undo", "", nil)
	if got, _ := a.work.VimSession(row.ID); got.ProjectID != "matomeru" || got.Name != "plans" {
		t.Fatalf("undo of move: %+v", got)
	}
	a.all = true
	openVimPage(t, a, row.ID)
	press(t, a, "m")
	if a.r.TopModal() == "" {
		t.Fatal("m did not ask")
	}
	press(t, a, "esc")
	press(t, a, "d", "d")
	if fr := frame(a); !strings.Contains(fr, "Forget plans? Edited files and the live pane stay.") {
		t.Fatalf("d d:\n%s", fr)
	}
	press(t, a, "y")
	if _, err := a.work.VimSession(row.ID); err == nil {
		t.Fatal("y kept the record")
	}
	if _, err := os.Stat(row.Path); !os.IsNotExist(err) {
		t.Fatalf("the owned session file stayed: %v", err)
	}
}

// vim list answers the sessions in scope as things; --all widens it
// without changing the page.
func TestVimListIsStandard(t *testing.T) {
	t.Parallel()
	a := open(t)
	vimSession(t, a)
	if err := a.work.PutVimSession(work.VimSession{ID: "vim-b", Name: "other", ProjectID: "ochazuke", State: "closed"}); err != nil {
		t.Fatal(err)
	}
	a.pin = "matomeru"
	var rows []map[string]any
	cliJSON(t, a, &rows, "vim", "list")
	if len(rows) != 1 || rows[0]["id"] != "vim-a" || rows[0]["name"] != "notes" || rows[0]["project"] != "matomeru" {
		t.Fatalf("list: %v", rows)
	}
	cliJSON(t, a, &rows, "vim", "list", "--all")
	if len(rows) != 2 {
		t.Fatalf("list --all: %v", rows)
	}
}
