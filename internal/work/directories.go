package work

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"time"

	"ofam.dev/matomeru/internal/relations"
	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

// A project's directories are relations of kind DirectoryKind from the
// project ("project:ID") to the directory ("directory:PATH"): any number,
// ordered by position, each with free text and a data object. The data
// holds kind, repo and branch (what inspecting the directory found) and
// default (true on at most one: the kind is declared Exclusive), so the
// project's default directory is a flag in the relation, not a field of the
// project. The project row keeps no directory (version 2 of the projects
// table moved trees and default_tree here); Project.Trees and DefaultTree
// are loaded from the relations on every read.

const (
	// DirectoryKind is the relation kind of a project's directory.
	DirectoryKind = "directory"
	// DirectoryNoun is the noun of the To end.
	DirectoryNoun = "directory"
	// DefaultFlag is the data key that marks the default directory.
	DefaultFlag = "default"
)

// ProjectRef is the relation end of a project.
func ProjectRef(id string) string { return ochazuke.AnyRef("project", id) }

// DirectoryRef is the relation end of a directory (its cleaned path).
func DirectoryRef(path string) string {
	return ochazuke.AnyRef(DirectoryNoun, filepath.Clean(path))
}

// DirectoryData is the data a tree carries into its relation.
func DirectoryData(t Tree) map[string]any {
	data := map[string]any{}
	for k, v := range t.Data {
		data[k] = v
	}
	for k, v := range map[string]string{"kind": t.Kind, "repo": t.Repo, "branch": t.Branch} {
		if v != "" {
			data[k] = v
		}
	}
	if len(data) == 0 {
		return nil
	}
	return data
}

// treeOf reads a directory relation as a tree.
func treeOf(x ochazuke.Relation) Tree {
	_, path, _ := ochazuke.SplitAnyRef(x.To)
	str := func(key string) string { s, _ := x.Data[key].(string); return s }
	kind := str("kind")
	if kind == "" {
		kind = "directory" // a relation made by set directories+= has not been inspected
	}
	t := Tree{Kind: kind, Path: path, Repo: str("repo"), Branch: str("branch"), Text: x.Text}
	t.Data = maps.Clone(x.Data)
	return t
}

// setTrees sets the project's directories from its live directory
// relations (position order).
func (p *Project) setTrees(xs []ochazuke.Relation) {
	p.Trees, p.DefaultTree = nil, ""
	for _, x := range xs {
		t := treeOf(x)
		p.Trees = append(p.Trees, t)
		if x.Flag(DefaultFlag) && p.DefaultTree == "" {
			p.DefaultTree = t.Path
		}
	}
}

// projectTrees reads every project's live directory relations in one
// indexed query (the relations table's from index), by project id.
func (s *Store) projectTrees() (map[string][]ochazuke.Relation, error) {
	raws, err := s.db.Match(relations.Table, store.JSONMatch{Field: "from", Value: "project:", Prefix: true}, store.JSONMatch{Field: "kind", Value: DirectoryKind})
	if err != nil {
		return nil, err
	}
	out := map[string][]ochazuke.Relation{}
	for _, raw := range raws {
		var r relations.Row
		if json.Unmarshal(raw, &r) != nil || r.Archived || r.FormerFrom != "" || r.FormerTo != "" {
			continue
		}
		_, id, _ := ochazuke.SplitAnyRef(r.From)
		out[id] = append(out[id], r.Relation())
	}
	for id, xs := range out {
		sortByPosition(xs)
		out[id] = xs
	}
	return out, nil
}

func sortByPosition(xs []ochazuke.Relation) {
	// insertion sort: stable, and the lists are a handful of rows
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j].Position < xs[j-1].Position; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

// Project is the project with that id, its directories loaded.
func (s *Store) Project(id string) (Project, error) {
	var p Project
	if err := s.db.Get("projects", id, &p); err != nil {
		return p, err
	}
	raws, err := s.db.Match(relations.Table, store.JSONMatch{Field: "from", Value: ProjectRef(id)}, store.JSONMatch{Field: "kind", Value: DirectoryKind})
	if err != nil {
		return p, err
	}
	var xs []ochazuke.Relation
	for _, raw := range raws {
		var r relations.Row
		if json.Unmarshal(raw, &r) == nil && !r.Archived && r.FormerFrom == "" && r.FormerTo == "" {
			xs = append(xs, r.Relation())
		}
	}
	sortByPosition(xs)
	p.setTrees(xs)
	return p, nil
}

// legacyProject is the part of a version 1 project row the migration reads.
type legacyProject struct {
	ID          string `json:"id"`
	Trees       []Tree `json:"trees"`
	DefaultTree string `json:"default_tree"`
}

// migrateDirectories moves version 1 projects' trees and default_tree into
// directory relations (projects 1 -> 2), once, in one transaction: a
// relation per distinct cleaned path in the stored order (positions in
// steps of 1024), data kind, repo and branch as stored, the default flag on
// the path default_tree names (a default_tree no tree lists is kept as a
// relation of its own: no row is lost), then the row without trees and
// default_tree. A file about to migrate is copied beside itself first
// (mtm.db.bak-<time>); a fresh file and a migrated one do nothing.
func migrateDirectories(db *store.Store) error {
	if v, ok := db.TableVersion("projects"); !ok || v != 1 {
		return nil
	}
	if path := db.Path(); path != "" && path != ":memory:" {
		if err := db.Backup(path + ".bak-" + time.Now().UTC().Format("20060102T150405Z")); err != nil {
			return fmt.Errorf("projects 1 -> 2: backup before moving directories to relations: %w", err)
		}
	}
	return db.Migrate("projects", 1, 2, func(tx *store.Tx, row json.RawMessage) (any, error) {
		var old legacyProject
		if err := json.Unmarshal(row, &old); err != nil {
			return nil, err
		}
		fields := map[string]json.RawMessage{}
		if err := json.Unmarshal(row, &fields); err != nil {
			return nil, err
		}
		trees := old.Trees
		def := filepath.Clean(old.DefaultTree)
		if old.DefaultTree != "" {
			found := false
			for _, t := range trees {
				found = found || filepath.Clean(t.Path) == def
			}
			if !found {
				trees = append(append([]Tree(nil), trees...), Tree{Kind: "directory", Path: old.DefaultTree})
			}
		}
		seen := map[string]bool{}
		now := time.Now().UnixMilli()
		for _, t := range trees {
			if strings.TrimSpace(t.Path) == "" || seen[filepath.Clean(t.Path)] {
				continue
			}
			seen[filepath.Clean(t.Path)] = true
			data := DirectoryData(t)
			if old.DefaultTree != "" && filepath.Clean(t.Path) == def {
				if data == nil {
					data = map[string]any{}
				}
				data[DefaultFlag] = true
			}
			x := ochazuke.Relation{Kind: DirectoryKind, From: ProjectRef(old.ID), To: DirectoryRef(t.Path), Text: t.Text, Data: data, Position: len(seen) * 1024}
			if err := relations.PutTx(tx, x, now); err != nil {
				return nil, err
			}
		}
		delete(fields, "trees")
		delete(fields, "default_tree")
		return fields, nil
	})
}

// ErrNotAttached is the error of a directory the project does not have.
var ErrNotAttached = errors.New("directory is not attached to this project")

// PutDirectory writes the directory relation of a tree straight to the
// table: no rules, no history, an existing one replaced in place. It is for
// what builds rows outside a request (seeds, conversions, tests); the
// verbs use ochazuke's Relate (cmd/mtm/project_directories.go). position 0
// puts it after the project's last.
func (s *Store) PutDirectory(projectID string, t Tree, isDefault bool, position int) error {
	return s.db.Tx([]string{relations.Table}, func(tx *store.Tx) error {
		if position == 0 {
			raws, err := tx.Match(relations.Table, store.JSONMatch{Field: "from", Value: ProjectRef(projectID)}, store.JSONMatch{Field: "kind", Value: DirectoryKind})
			if err != nil {
				return err
			}
			position = (len(raws) + 1) * 1024
			for _, raw := range raws {
				var r relations.Row
				if json.Unmarshal(raw, &r) == nil && r.Position >= position {
					position = r.Position + 1024
				}
			}
		}
		data := DirectoryData(t)
		if isDefault {
			if data == nil {
				data = map[string]any{}
			}
			data[DefaultFlag] = true
		}
		return relations.PutTx(tx, ochazuke.Relation{Kind: DirectoryKind, From: ProjectRef(projectID), To: DirectoryRef(t.Path), Text: t.Text, Data: data, Position: position}, Now())
	})
}
