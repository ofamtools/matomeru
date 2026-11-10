package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"
)

func TestScaffoldSessionArchiveSurvivesListing(t *testing.T) {
	t.Parallel()
	a, f := sessionsApp(t)
	id := "id-matomeru"
	if err := a.r.OpenIndex("session", id); err != nil {
		t.Fatal(err)
	}
	// A legacy copy has no archive field: it must still appear in the index.
	if got := ansi.Strip(a.View().Content); !strings.Contains(got, "matomeru") {
		t.Fatal(got)
	}
	executeA(t, a, "session", "archive", "", ochazuke.Arguments{"id": id})
	if err := a.reloadPanes(); err != nil {
		t.Fatal(err)
	}
	settle(a, a.r.PendingCommands())
	got := executeA(t, a, "session", "get", "", ochazuke.Arguments{"id": id}).(map[string]any)
	if got["archived"] != true || len(f.panes) != 2 {
		t.Fatalf("archive changed daemon state: %v, panes %+v", got, f.panes)
	}
	var rows []map[string]any
	cliRows(t, a, &rows, "session", "list", "--all")
	for _, row := range rows {
		if row["id"] == id {
			t.Fatalf("archive still listed: %v", rows)
		}
	}
	executeA(t, a, "session", "set", "", ochazuke.Arguments{"id": id, "props": map[string]any{"@archive": false}})
	cliRows(t, a, &rows, "session", "list", "--all")
	found := false
	for _, row := range rows {
		if row["id"] == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("restored copy absent: %v", rows)
	}
}

func TestScaffoldDirectoryArchiveKeepsFilesAndRelations(t *testing.T) {
	t.Parallel()
	path := t.TempDir()
	file := filepath.Join(path, "keep.txt")
	if err := os.WriteFile(file, []byte("keep this file"), 0600); err != nil {
		t.Fatal(err)
	}
	dbPath := seeded(t)
	a := openAt(t, dbPath)
	_, err := a.r.Relate(ochazuke.Ctx{Runtime: a.r}, ochazuke.Relation{Kind: "directory", From: "project:matomeru", To: "directory:" + path})
	if err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "directory", "set", "", ochazuke.Arguments{"path": path, "props": map[string]any{"description": "working checkout"}})
	executeA(t, a, "directory", "archive", "", ochazuke.Arguments{"path": path})
	other := openAt(t, dbPath)
	got := executeA(t, other, "directory", "get", "", ochazuke.Arguments{"path": path}).(map[string]any)
	if got["archived"] != true || got["description"] != "working checkout" {
		t.Fatalf("reopened metadata: %v", got)
	}
	p, err := other.work.Project("matomeru")
	if err != nil || len(p.Trees) != 1 || p.Trees[0].Path != path {
		t.Fatalf("archive detached directory: %+v, %v", p, err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "keep this file" {
		t.Fatalf("archive touched disk: %q %v", data, err)
	}
	executeA(t, other, "directory", "set", "", ochazuke.Arguments{"path": path, "props": map[string]any{"@archive": false}})
	var rows []map[string]any
	cliRows(t, other, &rows, "directory", "list")
	if len(rows) != 1 || rows[0]["path"] != path {
		t.Fatalf("restored directory: %v", rows)
	}
}

func TestScaffoldReviewArchivePreservesAnswerAndTurns(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	capturedTurns(t, path, "the journal answer")
	a := openAt(t, path)
	id := paneReviewID("matomeru", "w1")
	executeA(t, a, "review", "set", "", ochazuke.Arguments{"id": id, "props": map[string]any{"answer": "keep the review"}})
	executeA(t, a, "review", "archive", "", ochazuke.Arguments{"id": id})
	var rows []map[string]any
	cliRows(t, a, &rows, "review", "list", "--all")
	if len(rows) != 0 {
		t.Fatalf("archived review visible: %v", rows)
	}
	other := openAt(t, path)
	got := executeA(t, other, "review", "get", "", ochazuke.Arguments{"id": id}).(map[string]any)
	if got["answer"] != "keep the review" || got["archived"] != true || got["turns"] != 1 {
		t.Fatalf("archive lost review data: %v", got)
	}
	executeA(t, other, "review", "set", "", ochazuke.Arguments{"id": id, "props": map[string]any{"@archive": false}})
	cliRows(t, other, &rows, "review", "list", "--all")
	if len(rows) != 1 || rows[0]["id"] != id || rows[0]["answer"] != "keep the review" {
		t.Fatalf("restored review: %v", rows)
	}
}

func TestScaffoldReviewDeleteKeepsJournalWithoutRecreatingReview(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	capturedTurns(t, path, "journal survives deletion")
	a := openAt(t, path)
	id := paneReviewID("matomeru", "w1")
	executeA(t, a, "review", "delete", "", ochazuke.Arguments{"id": id})
	other := openAt(t, path)
	var rows []map[string]any
	cliRows(t, other, &rows, "review", "list", "--all", "--archived")
	if len(rows) != 0 {
		t.Fatalf("journal recreated deleted review: %v", rows)
	}
	turns, err := other.hist.Turns("")
	if err != nil || len(turns) != 1 || turns[0].Window != "w1" {
		t.Fatalf("review deletion lost journal: %+v %v", turns, err)
	}
}
