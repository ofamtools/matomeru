package work

import (
	"fmt"
	"slices"

	"github.com/unixsuperhero/matomeru/store"
)

// The work store's side of privacy.go.

// SetProjectPrivacy sets a project's privacy (project make-private and
// make-family).
func (s *Store) SetProjectPrivacy(id, privacy string) (Project, error) {
	if !ValidPrivacy(privacy) {
		return Project{}, fmt.Errorf("privacy %q is not one of private, family", privacy)
	}
	p, err := s.Project(id)
	if err != nil {
		return p, err
	}
	p.Settings.Privacy = privacy
	return p, s.PutProject(p)
}

// Exports is Exports over the store's tasks and projects.
func (s *Store) Exports() (map[string]bool, error) {
	tasks, err := s.Tasks("")
	if err != nil {
		return nil, err
	}
	projects, err := s.Projects()
	if err != nil {
		return nil, err
	}
	return Exports(tasks, projects), nil
}

// TaskExported is Exports for one task.
func (s *Store) TaskExported(id string) (bool, error) {
	all, err := s.Exports()
	if err != nil {
		return false, err
	}
	if _, ok := all[id]; !ok {
		return false, store.ErrNotFound
	}
	return all[id], nil
}

// UnsetPrivacy lists the projects that have no privacy setting: private
// by default, so not exported until the owner marks them.
func (s *Store) UnsetPrivacy() ([]Project, error) {
	projects, err := s.Projects()
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(projects, func(p Project) bool { return p.Settings.Privacy != "" }), nil
}
