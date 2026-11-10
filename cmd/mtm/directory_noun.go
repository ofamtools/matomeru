package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

// A directory is a path with local metadata and project relations. Reading an
// unrecorded path still works; create and set record its description and archive
// state. These operations never change files on disk. The curated Dirs page
// keeps its filesystem scan; the noun's index is the runtime scaffold.

func directoryProps() []ochazuke.PropSpec {
	text := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "string", Description: desc} }
	return []ochazuke.PropSpec{
		{Name: "path", Key: true, CreateOnly: true, Parameter: ochazuke.Parameter{Type: "string", Required: true, Description: "The directory's cleaned path (its key); metadata operations never change files on disk"}},
		{Name: "name", Role: ochazuke.RoleLabel, ReadOnly: true, Parameter: ochazuke.Parameter{Type: ochazuke.PropPath, Description: "The directory's path"}},
		{Name: "description", Multiline: true, Parameter: text("Notes about this directory")},
		{Name: "archived", Role: ochazuke.RoleArchive, Default: false, Parameter: ochazuke.Parameter{Type: "boolean", Description: "Hide the directory record; files and project attachments are kept"}},
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
	t := ochazuke.Thing{"path": path, "name": path, "description": "", "archived": false, "projects": []string{}, "default-for": []string{}, "repo": ""}
	var meta ochazuke.Thing
	if err := a.db.Get("directories", path, &meta); err == nil {
		for k, v := range meta {
			t[k] = v
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
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
			records, err := a.db.All("directories")
			if err != nil {
				return nil, err
			}
			for _, raw := range records {
				var record struct {
					Path string `json:"path"`
				}
				if err := json.Unmarshal(raw, &record); err != nil {
					return nil, err
				}
				if !slices.Contains(paths, record.Path) {
					paths = append(paths, record.Path)
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
		Create: func(_ ochazuke.Ctx, t ochazuke.Thing) (ochazuke.Thing, error) {
			path := a.expandPath(fmt.Sprint(t["path"]))
			if err := a.writeDirectory(path, t); err != nil {
				return nil, err
			}
			return a.directoryThing(path)
		},
		Patch: func(_ ochazuke.Ctx, path string, changes ochazuke.Thing) (ochazuke.Thing, error) {
			path = a.expandPath(path)
			t, err := a.directoryThing(path)
			if err != nil {
				return nil, err
			}
			for k, v := range changes {
				t[k] = v
			}
			if err := a.writeDirectory(path, t); err != nil {
				return nil, err
			}
			return a.directoryThing(path)
		},
		Delete: func(_ ochazuke.Ctx, path string) error {
			return a.db.Tx([]string{"directories"}, func(tx *store.Tx) error { return tx.Delete("directories", a.expandPath(path)) })
		},
	}
}

func (a *App) writeDirectory(path string, t ochazuke.Thing) error {
	meta := ochazuke.Thing{"path": path, "name": path, "description": t["description"], "archived": t["archived"]}
	return a.db.Tx([]string{"directories"}, func(tx *store.Tx) error { return tx.Put("directories", path, meta) })
}

func (a *App) defineDirectoryNoun() {
	// The declared directory picker (pickers.go) is the one that chooses a
	// directory for a project; the noun keeps out of the derived pickers.
	a.r.Define(work.DirectoryNoun).Props(directoryProps()...).Store(a.docStore(work.DirectoryNoun, a.directoryStore())).NoPicker()
}
