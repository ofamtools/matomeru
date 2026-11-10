package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/relations"
	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// dirsOf are the project's live directory paths in order and its default.
func dirsOf(t *testing.T, a *App, id string) ([]string, string) {
	t.Helper()
	p, err := a.work.Project(id)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tr := range p.Trees {
		out = append(out, tr.Path)
	}
	return out, p.DefaultTree
}

func relationsOf(t *testing.T, a *App, q ochazuke.RelationQuery) []ochazuke.Relation {
	t.Helper()
	xs, err := a.r.RelationsOf(q)
	if err != nil {
		t.Fatal(err)
	}
	return xs
}

func dirsMk(t *testing.T, names ...string) []string {
	t.Helper()
	base := t.TempDir()
	var out []string
	for _, n := range names {
		d := filepath.Join(base, n)
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	return out
}

// Any number of directories, each with text and data: relate carries both,
// the default is a flag in the data of one relation, and a project row
// holds none of it.
func TestProjectDirectoriesAreOrderedRelationsWithTextAndData(t *testing.T) {
	t.Parallel()
	a := open(t)
	d := dirsMk(t, "one", "two", "three")
	for i, path := range d {
		args := ochazuke.Arguments{"path": path}
		if i == 1 {
			args["text"], args["data"] = "docs checkout", map[string]any{"role": "docs"}
		}
		executeA(t, a, "project", "directory", "matomeru", args)
	}
	paths, def := dirsOf(t, a, "matomeru")
	if !slices.Equal(paths, d) || def != d[0] {
		t.Fatalf("directories %v default %q, want %v default %q", paths, def, d, d[0])
	}
	rs := relationsOf(t, a, ochazuke.RelationQuery{Kind: work.DirectoryKind, From: "project:matomeru"})
	if len(rs) != 3 || rs[1].Text != "docs checkout" || rs[1].Data["role"] != "docs" || !rs[0].Flag("default") || rs[1].Flag("default") {
		t.Fatalf("relations %+v", rs)
	}
	if !(rs[0].Position < rs[1].Position && rs[1].Position < rs[2].Position) {
		t.Fatalf("positions %v %v %v", rs[0].Position, rs[1].Position, rs[2].Position)
	}
	// The flag moves with project default, and only one holds it.
	executeA(t, a, "project", "default", "matomeru", ochazuke.Arguments{"path": d[2]})
	if _, def := dirsOf(t, a, "matomeru"); def != d[2] {
		t.Fatalf("default %q", def)
	}
	held := 0
	for _, x := range relationsOf(t, a, ochazuke.RelationQuery{Kind: work.DirectoryKind, From: "project:matomeru"}) {
		if x.Flag("default") {
			held++
		}
	}
	if held != 1 {
		t.Fatalf("%d relations hold the default", held)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "default", Target: "matomeru", Args: ochazuke.Arguments{"path": "/not/attached"}}); err == nil {
		t.Fatal("a directory the project lacks became its default")
	}
}

// project set directories+=/path adds one, -= removes one, = writes the list
// (order included).
func TestProjectDirectoriesPropListEdits(t *testing.T) {
	t.Parallel()
	a := open(t)
	d := dirsMk(t, "a", "b", "c")
	set := func(props map[string]any) {
		t.Helper()
		if _, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "set", Args: ochazuke.Arguments{"id": "matomeru", "props": props}}); err != nil {
			t.Fatal(err)
		}
	}
	set(map[string]any{"directories+": d[0]})
	set(map[string]any{"directories+": d[1]})
	set(map[string]any{"directories+": d[2]})
	if paths, def := dirsOf(t, a, "matomeru"); !slices.Equal(paths, d) || def != d[0] {
		t.Fatalf("after += : %v default %q", paths, def)
	}
	set(map[string]any{"directories-": d[0]})
	if paths, def := dirsOf(t, a, "matomeru"); !slices.Equal(paths, d[1:]) || def != d[1] {
		t.Fatalf("after -= : %v default %q (the next takes the default)", paths, def)
	}
	set(map[string]any{"directories": []string{d[2], d[1], d[0]}})
	if paths, _ := dirsOf(t, a, "matomeru"); !slices.Equal(paths, []string{d[2], d[1], d[0]}) {
		t.Fatalf("after = : %v", paths)
	}
	out, err := a.r.Execute(ochazuke.Request{Noun: "project", Verb: "get", Target: "matomeru"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), d[2]) {
		t.Fatalf("project get answers directories: %s", raw)
	}
}

// Under delete.archive, remove-directory archives the relation (the project
// stops listing the directory, the relation stays); removing it again
// removes the row; with the setting off it is removed at once. History undo
// brings it back.
func TestRemoveDirectoryArchivesFirstThenRemoves(t *testing.T) {
	t.Parallel()
	a := open(t)
	d := dirsMk(t, "a", "b")
	for _, p := range d {
		executeA(t, a, "project", "directory", "matomeru", ochazuke.Arguments{"path": p})
	}
	if err := a.r.Settings().Set(ochazuke.DeleteArchiveSetting, true); err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "project", "remove-directory", "matomeru", ochazuke.Arguments{"path": d[0]})
	if paths, def := dirsOf(t, a, "matomeru"); !slices.Equal(paths, d[1:]) || def != d[1] {
		t.Fatalf("archived: %v default %q", paths, def)
	}
	arch := relationsOf(t, a, ochazuke.RelationQuery{Kind: work.DirectoryKind, From: "project:matomeru", Archived: true})
	if len(arch) != 2 || !arch[0].Archived || arch[0].Flag("default") {
		t.Fatalf("the relation stays archived, its flag moved on: %+v", arch)
	}
	executeA(t, a, "history-entry", "undo", "", nil)
	if paths, _ := dirsOf(t, a, "matomeru"); !slices.Equal(paths, d) {
		t.Fatalf("undo: %v", paths)
	}
	executeA(t, a, "project", "remove-directory", "matomeru", ochazuke.Arguments{"path": d[0]})
	executeA(t, a, "project", "remove-directory", "matomeru", ochazuke.Arguments{"path": d[0]})
	if _, err := os.Stat(d[0]); err != nil {
		t.Fatalf("remove touched disk: %v", err)
	}
	if got := relationsOf(t, a, ochazuke.RelationQuery{Kind: work.DirectoryKind, From: "project:matomeru", Archived: true}); len(got) != 1 {
		t.Fatalf("the second removal deletes the archived relation: %+v", got)
	}
}

// relation set changes text and data and cannot drop the default flag by
// replacing the data; move reorders; delete detaches nothing it should not.
func TestRelationNounSetMoveAndFlag(t *testing.T) {
	t.Parallel()
	a := open(t)
	d := dirsMk(t, "a", "b")
	for _, p := range d {
		executeA(t, a, "project", "directory", "matomeru", ochazuke.Arguments{"path": p})
	}
	rs := relationsOf(t, a, ochazuke.RelationQuery{Kind: work.DirectoryKind, From: "project:matomeru"})
	key := relations.KeyOf
	executeA(t, a, "relation", "set", key(rs[0]), ochazuke.Arguments{"props": map[string]any{"text": "primary", "data": map[string]any{"role": "main"}}})
	rs = relationsOf(t, a, ochazuke.RelationQuery{Kind: work.DirectoryKind, From: "project:matomeru"})
	if rs[0].Text != "primary" || rs[0].Data["role"] != "main" || !rs[0].Flag("default") {
		t.Fatalf("set text and data kept the default flag: %+v", rs[0])
	}
	executeA(t, a, "relation", "move", "", ochazuke.Arguments{"id": key(rs[1]), "to": "first"})
	if paths, _ := dirsOf(t, a, "matomeru"); !slices.Equal(paths, []string{d[1], d[0]}) {
		t.Fatalf("move first: %v", paths)
	}
	executeA(t, a, "relation", "move", "", ochazuke.Arguments{"id": key(rs[1]), "to": "last"})
	if paths, _ := dirsOf(t, a, "matomeru"); !slices.Equal(paths, d) {
		t.Fatalf("move last: %v", paths)
	}
	// Any word is a kind: relate a project to a pane with no declared kind.
	executeA(t, a, "project", "relate", "matomeru", ochazuke.Arguments{"to": "pane:W1", "kind": "see-also", "text": "scratch", "data": map[string]any{"n": 1}})
	got := relationsOf(t, a, ochazuke.RelationQuery{Kind: "see-also", Thing: "pane:W1"})
	if len(got) != 1 || got[0].Text != "scratch" || got[0].From != "project:matomeru" {
		t.Fatalf("undeclared kind: %+v", got)
	}
	// The inverse: the directory noun lists the projects that have it.
	out, err := a.r.Execute(ochazuke.Request{Noun: "directory", Verb: "get", Target: d[0]})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), `"projects":["matomeru"]`) || !strings.Contains(string(raw), `"default-for":["matomeru"]`) {
		t.Fatalf("directory get: %s", raw)
	}
}

// Deleting a project keeps its directory relations: the end becomes the
// former end (record first), and undo of the delete puts it back.
func TestDeleteProjectDetachesItsRelations(t *testing.T) {
	t.Parallel()
	a := open(t)
	d := dirsMk(t, "a")
	executeA(t, a, "project", "directory", "ochazuke", ochazuke.Arguments{"path": d[0]})
	if _, err := a.deleteThing(ochazuke.Ctx{Runtime: a.r}, "project", "ochazuke"); err != nil {
		t.Fatal(err)
	}
	detached := relationsOf(t, a, ochazuke.RelationQuery{Detached: true})
	if len(detached) != 1 || detached[0].FormerFrom != "project:ochazuke" || detached[0].From != "" || detached[0].To != work.DirectoryRef(d[0]) {
		t.Fatalf("detached %+v", detached)
	}
}

// edit-directory points a directory at another path keeping its place, text
// and default mark.
func TestEditDirectoryKeepsPlaceTextAndDefault(t *testing.T) {
	t.Parallel()
	a := open(t)
	d := dirsMk(t, "a", "b", "c")
	executeA(t, a, "project", "directory", "matomeru", ochazuke.Arguments{"path": d[0], "text": "keep me"})
	executeA(t, a, "project", "directory", "matomeru", ochazuke.Arguments{"path": d[1]})
	executeA(t, a, "project", "edit-directory", "matomeru", ochazuke.Arguments{"path": d[0], "to": d[2]})
	paths, def := dirsOf(t, a, "matomeru")
	if !slices.Equal(paths, []string{d[2], d[1]}) || def != d[2] {
		t.Fatalf("%v default %q", paths, def)
	}
	p, _ := a.work.Project("matomeru")
	if p.Trees[0].Text != "keep me" {
		t.Fatalf("text lost: %+v", p.Trees[0])
	}
}
