package main

import (
	"path/filepath"
	"slices"
	"strings"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// The directory noun: a directory path, the To end of a project's directory
// relation. Its key is the cleaned path. It is read-only (get, list); what
// changes about a directory is its relations (project.directory,
// directory.relate, relation.set), and the Dirs page keeps its own rows. A
// directory needs no row to be related to (record first): get answers any
// path.

func directoryProps() []ochazuke.PropSpec {
	text := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "string", Description: desc} }
	return []ochazuke.PropSpec{
		{Name: "path", Key: true, Role: ochazuke.RoleLabel, Parameter: text("The directory's path, cleaned (its key)")},
		{Name: "projects", ReadOnly: true, Parameter: ochazuke.Parameter{Type: ochazuke.SettingList, Items: &ochazuke.Parameter{Type: "string", Noun: "project"},
			Description: "The projects that have this directory (the inverse of a project's directories)"}},
		{Name: "default-for", ReadOnly: true, Parameter: ochazuke.Parameter{Type: ochazuke.SettingList, Items: &ochazuke.Parameter{Type: "string", Noun: "project"},
			Description: "The projects this directory is the default of"}},
		{Name: "repo", ReadOnly: true, Parameter: text("The repository the directory is in, from the data of its relations")},
	}
}

// directoryThing is the directory at path with the projects that hold it,
// read from its relations (one indexed read).
func (a *App) directoryThing(path string) (ochazuke.Thing, error) {
	path = filepath.Clean(path)
	xs, err := a.r.RelationsOf(ochazuke.RelationQuery{Kind: work.DirectoryKind, To: work.DirectoryRef(path)})
	if err != nil {
		return nil, err
	}
	t := ochazuke.Thing{"path": path, "projects": []string{}, "default-for": []string{}, "repo": ""}
	for _, x := range xs {
		_, id, _ := ochazuke.SplitAnyRef(x.From)
		t["projects"] = append(t["projects"].([]string), id)
		if x.Flag(work.DefaultFlag) {
			t["default-for"] = append(t["default-for"].([]string), id)
		}
		if repo, _ := x.Data["repo"].(string); repo != "" && t["repo"] == "" {
			t["repo"] = repo
		}
	}
	return t, nil
}

// directoryStore is the directory noun's store: get answers any path, list
// the directories the projects hold, in path order.
func (a *App) directoryStore() ochazuke.Store {
	return ochazuke.Store{
		Kind: ochazuke.KindCollection,
		Get:  func(_ ochazuke.Ctx, key string) (ochazuke.Thing, error) { return a.directoryThing(a.expandPath(key)) },
		List: func(ochazuke.Ctx, ochazuke.ListQuery) ([]ochazuke.Thing, error) {
			xs, err := a.r.RelationsOf(ochazuke.RelationQuery{Kind: work.DirectoryKind})
			if err != nil {
				return nil, err
			}
			var paths []string
			for _, x := range xs {
				if _, path, ok := ochazuke.SplitAnyRef(x.To); ok && !slices.Contains(paths, path) {
					paths = append(paths, path)
				}
			}
			slices.SortFunc(paths, strings.Compare)
			out := make([]ochazuke.Thing, 0, len(paths))
			for _, p := range paths {
				t, err := a.directoryThing(p)
				if err != nil {
					return nil, err
				}
				out = append(out, t)
			}
			return out, nil
		},
	}
}

func (a *App) defineDirectoryNoun() {
	// The declared directory picker (pickers.go) is the one that chooses a
	// directory for a project; the noun keeps out of the derived pickers.
	a.r.Define(work.DirectoryNoun).Props(directoryProps()...).Store(a.directoryStore()).NoPicker()
}
