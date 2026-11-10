package main

import (
	"path/filepath"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// putProjectDirs writes the project and the directory relations of its
// Trees and DefaultTree (a project row holds no directories).
func putProjectDirs(w *work.Store, p work.Project) error {
	if err := w.PutProject(p); err != nil {
		return err
	}
	trees := append([]work.Tree(nil), p.Trees...)
	if p.DefaultTree != "" {
		found := false
		for _, t := range trees {
			found = found || filepath.Clean(t.Path) == filepath.Clean(p.DefaultTree)
		}
		if !found {
			trees = append(trees, work.Tree{Kind: "directory", Path: p.DefaultTree})
		}
	}
	for _, t := range trees {
		if err := w.PutDirectory(p.ID, t, p.DefaultTree != "" && filepath.Clean(t.Path) == filepath.Clean(p.DefaultTree), 0); err != nil {
			return err
		}
	}
	return nil
}

// addTree attaches a checkout through the runtime, as project directory does.
func (a *App) addTree(id string, t work.Tree) (work.Project, error) {
	return a.relateDirectory(ochazuke.Ctx{}, id, t)
}

// setDefaultTree is project default.
func (a *App) setDefaultTree(id, path string) (work.Project, error) {
	return a.defaultDirectory(ochazuke.Ctx{}, id, path)
}
