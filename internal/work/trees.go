package work

import (
	"errors"
	"path/filepath"

	"github.com/unixsuperhero/matomeru/store"
)

func (s *Store) Project(id string) (Project, error) {
	var p Project
	err := s.db.Get("projects", id, &p)
	return p, err
}

// AddTree attaches a checkout without claiming exclusive ownership of its path.
func (s *Store) AddTree(id string, tree Tree) (Project, error) {
	tree.Path = filepath.Clean(tree.Path)
	var p Project
	err := s.db.Tx([]string{"projects"}, func(tx *store.Tx) error {
		if err := tx.Get("projects", id, &p); err != nil {
			return err
		}
		for _, t := range p.Trees {
			if filepath.Clean(t.Path) == tree.Path {
				return nil
			}
		}
		p.Trees = append(p.Trees, tree)
		if p.DefaultTree == "" {
			p.DefaultTree = tree.Path
		}
		if p.Repo == "" {
			p.Repo = tree.Repo
		}
		return tx.Put("projects", id, p)
	})
	if err == nil {
		err = s.TouchDir(tree.Path)
	}
	return p, err
}

func (s *Store) SetDefaultTree(id, path string) (Project, error) {
	path = filepath.Clean(path)
	var p Project
	err := s.db.Tx([]string{"projects"}, func(tx *store.Tx) error {
		if err := tx.Get("projects", id, &p); err != nil {
			return err
		}
		for _, tree := range p.Trees {
			if filepath.Clean(tree.Path) == path {
				p.DefaultTree = path
				return tx.Put("projects", id, p)
			}
		}
		return errors.New("directory is not attached to this project")
	})
	return p, err
}

// RemoveTree detaches path from the project; the directory on disk is never
// touched. Removing the default promotes the next remaining directory (the
// first in attach order) and returns it as promoted; removing the last one
// leaves no default. A path the project does not have is an error.
func (s *Store) RemoveTree(id, path string) (p Project, promoted string, err error) {
	path = filepath.Clean(path)
	err = s.db.Tx([]string{"projects"}, func(tx *store.Tx) error {
		if err := tx.Get("projects", id, &p); err != nil {
			return err
		}
		kept := p.Trees[:0:0]
		found := false
		for _, t := range p.Trees {
			if filepath.Clean(t.Path) == path {
				found = true
				continue
			}
			kept = append(kept, t)
		}
		if !found {
			return errors.New("directory is not attached to this project")
		}
		p.Trees = kept
		if filepath.Clean(p.DefaultTree) == path {
			p.DefaultTree = ""
			if len(kept) > 0 {
				p.DefaultTree = filepath.Clean(kept[0].Path)
				promoted = p.DefaultTree
			}
		}
		return tx.Put("projects", id, p)
	})
	return p, promoted, err
}

// ReplaceTree points one of the project's directories at another path
// (tree, already inspected), keeping its place and its default mark. When
// the project already has the new path, the old entry is dropped instead.
func (s *Store) ReplaceTree(id, path string, tree Tree) (Project, error) {
	path, tree.Path = filepath.Clean(path), filepath.Clean(tree.Path)
	var p Project
	err := s.db.Tx([]string{"projects"}, func(tx *store.Tx) error {
		if err := tx.Get("projects", id, &p); err != nil {
			return err
		}
		at, dup := -1, false
		for i, t := range p.Trees {
			switch filepath.Clean(t.Path) {
			case path:
				at = i
			case tree.Path:
				dup = true
			}
		}
		if at < 0 {
			return errors.New("directory is not attached to this project")
		}
		if dup {
			p.Trees = append(p.Trees[:at:at], p.Trees[at+1:]...)
		} else {
			p.Trees[at] = tree
		}
		if filepath.Clean(p.DefaultTree) == path {
			p.DefaultTree = tree.Path
		}
		return tx.Put("projects", id, p)
	})
	if err == nil {
		err = s.TouchDir(tree.Path)
	}
	return p, err
}
