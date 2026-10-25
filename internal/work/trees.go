package work

import (
	"errors"
	"path/filepath"

	"github.com/unixsuperhero/matomeru/internal/store"
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
