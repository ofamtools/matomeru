package main

import (
	"errors"
	"fmt"
	"path/filepath"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

// Paths and scan bases are the same stored noun. Metadata never changes files
// on disk. The runtime supplies CRUD, forms, archive, open/reveal and relations.
func directoryProps() []ochazuke.PropSpec {
	text := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "string", Description: desc} }
	path := text("The directory's cleaned path; a missing path is recorded without creating it")
	path.Required = true
	return []ochazuke.PropSpec{
		{Name: "path", Key: true, CreateOnly: true, Parameter: path},
		{Name: "name", ReadOnly: true, Parameter: ochazuke.Parameter{Type: ochazuke.PropPath, Path: ochazuke.PathDir, Description: "The directory's path"}},
		{Name: "label", Role: ochazuke.RoleLabel, Parameter: text("An optional label; empty shows the path")},
		{Name: "location", Parameter: dirParam("Retarget this record and its project associations; never moves files", true)},
		{Name: "depth", Default: 0, Parameter: ochazuke.Parameter{Type: "integer", Description: "Zero: ordinary directory; positive: scan this base to that depth"}},
		{Name: "description", Multiline: true, Parameter: text("Notes about this directory")},
		{Name: "archived", Role: ochazuke.RoleArchive, Default: false, Parameter: ochazuke.Parameter{Type: "boolean", Description: "Hide the directory and stop scanning it; keep files and project attachments"}},
		{Name: "source", ReadOnly: true, Parameter: text("saved, scan, file (one-time dirs.json import), migrated, project or variable (MTM_PROJECT_BASE_DIRS override)")},
		{Name: "projects", ReadOnly: true, Parameter: ochazuke.Parameter{Type: ochazuke.SettingList, Items: &ochazuke.Parameter{Type: "string", Noun: "project"}, Description: "Projects associated with this directory"}},
		{Name: "default-for", ReadOnly: true, Parameter: ochazuke.Parameter{Type: ochazuke.SettingList, Items: &ochazuke.Parameter{Type: "string", Noun: "project"}, Description: "Projects using this directory as their default"}},
		{Name: "repo", ReadOnly: true, Parameter: text("Repository from the directory's relations")},
		{Name: "candidate", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "boolean", Description: "A scan found a .git entry in this directory; directory.make-project promotes it"}},
	}
}

func (a *App) directoryThing(path string) (ochazuke.Thing, error) {
	path = a.expandPath(path)
	xs, err := a.r.RelationsOf(ochazuke.RelationQuery{Kind: work.DirectoryKind, To: work.DirectoryRef(path)})
	if err != nil {
		return nil, err
	}
	t := ochazuke.Thing{"path": path, "name": path, "label": "", "location": path, "depth": 0, "description": "", "archived": false, "source": "saved", "projects": []string{}, "default-for": []string{}, "repo": "", "candidate": false}
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

func (a *App) directoryStore() ochazuke.Store {
	return ochazuke.Store{
		Kind: ochazuke.KindCollection,
		Get:  func(_ ochazuke.Ctx, key string) (ochazuke.Thing, error) { return a.directoryThing(key) },
		List: func(ochazuke.Ctx, ochazuke.ListQuery) ([]ochazuke.Thing, error) {
			rows, err := a.userDirectories()
			if err != nil {
				return nil, err
			}
			out := make([]ochazuke.Thing, 0, len(rows))
			for _, row := range rows {
				t, err := a.directoryThing(row.Path)
				if err != nil {
					return nil, err
				}
				t["depth"], t["source"], t["archived"] = row.Depth, row.Source, row.Archived
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
			location, _ := t["location"].(string)
			if location != "" && a.expandPath(location) != path {
				next := a.expandPath(location)
				if err := a.retargetDirectory(path, next, t); err != nil {
					return nil, err
				}
				return a.directoryThing(next)
			}
			if err := a.writeDirectory(path, t); err != nil {
				return nil, err
			}
			return a.directoryThing(path)
		},
		Delete: func(_ ochazuke.Ctx, path string) error {
			path = a.expandPath(path)
			err := a.db.Tx([]string{"directories"}, func(tx *store.Tx) error { return tx.Delete("directories", path) })
			if err == nil {
				a.directoryRecordChanged(path)
			}
			return err
		},
	}
}

func (a *App) writeDirectory(path string, t ochazuke.Thing) error {
	depth := ochazuke.IntArg(ochazuke.Arguments(t), "depth", 0)
	if depth < 0 {
		return errors.New("depth must be zero (directory) or positive (scan base)")
	}
	meta := ochazuke.Thing{}
	if err := a.db.Get("directories", path, &meta); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	oldArchived, _ := meta["archived"].(bool)
	archived, _ := t["archived"].(bool)
	scanChanged := len(meta) == 0 || ochazuke.IntArg(ochazuke.Arguments(meta), "depth", 0) != depth || oldArchived != archived
	for _, key := range []string{"label", "description", "archived", "created", "updated"} {
		if value, ok := t[key]; ok {
			meta[key] = value
		}
	}
	meta["path"], meta["depth"], meta["source"] = path, depth, "saved"
	if err := a.db.Tx([]string{"directories"}, func(tx *store.Tx) error { return tx.Put("directories", path, meta) }); err != nil {
		return err
	}
	a.directoryRecordChanged(path)
	if scanChanged && !a.cli && a.directoryList != nil {
		return a.loadDirectories()
	}
	return nil
}

func (a *App) retargetDirectory(path, to string, t ochazuke.Thing) error {
	if filepath.Clean(path) == filepath.Clean(to) {
		return nil
	}
	// Refuse only a record collision; overwriting it would lose the other label
	// and metadata. A missing target on disk is deliberately allowed.
	var existing ochazuke.Thing
	if err := a.db.Get("directories", to, &existing); err == nil {
		return fmt.Errorf("directory %s is already recorded", to)
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	xs, err := a.r.RelationsOf(ochazuke.RelationQuery{Kind: work.DirectoryKind, To: work.DirectoryRef(path)})
	if err != nil {
		return err
	}
	// The domain relation operation preserves order, description and default.
	for _, x := range xs {
		_, project, _ := ochazuke.SplitAnyRef(x.From)
		if _, err := a.repointDirectory(ochazuke.Ctx{}, project, path, work.Tree{Kind: "directory", Path: to}); err != nil {
			return err
		}
	}
	t["location"] = to
	if err := a.writeDirectory(to, t); err != nil {
		return err
	}
	err = a.db.Tx([]string{"directories"}, func(tx *store.Tx) error {
		return tx.Put("directories", path, directoryRecord{Path: path, Archived: true})
	})
	if err == nil {
		a.directoryRecordChanged(to)
	}
	return err
}

func (a *App) defineDirectoryNoun() {
	a.r.Define(work.DirectoryNoun).Props(directoryProps()...).Store(a.docStore(work.DirectoryNoun, a.directoryStore())).NoPicker().
		Verb("attach", a.associateDirectory, "Associate this directory with a project; omit project to choose").Positional("path").
		Verb("rescan", a.refreshDirectories, "Rescan the saved directory list off the UI loop").Fetch().
		Verb("scan", a.refreshDirectories, "Scan this directory or base for Git repository candidates").Fetch().Positional("path").
		Verb("make-project", a.makeDirectoryProject, "Make selected or marked Git repository candidates projects; existing projects are reused").Many().Positional("path").
		Verb("default", a.defaultDirectoryRecord, "Make this directory a project's default").
		Mode("normal").Bind("enter", ochazuke.Do("directory.attach")).Bind("i", ochazuke.Do("directory.edit")).
		Bind("m", ochazuke.Do("ochazuke.collection.apply", ochazuke.Arguments{"verb": "make-project"}).Hinted("make-project"))
	a.parameters("directory.attach", map[string]ochazuke.Parameter{"path": {Type: "string", Noun: "directory", Free: true}, "project": {Type: "string", Noun: "project"}})
	a.parameters("directory.default", map[string]ochazuke.Parameter{"path": {Type: "string", Noun: "directory", Free: true}, "project": {Type: "string", Noun: "project", Required: true}})
	a.parameters("directory.scan", map[string]ochazuke.Parameter{"path": {Type: "string", Required: true, From: "row:path"}})
	a.parameters("directory.make-project", map[string]ochazuke.Parameter{"path": {Type: "string", From: "row:path"}})
}

func (a *App) directoryPathAt(c ochazuke.Ctx) string {
	if path := ochazuke.StringArg(c.Args, "path", ""); path != "" {
		return a.expandPath(path)
	}
	if c.Instance != nil {
		if path, ok := c.Instance.Data.(string); ok {
			return a.expandPath(path)
		}
	}
	return ""
}
func (a *App) defaultDirectoryRecord(c ochazuke.Ctx) ochazuke.Result {
	path, project := a.directoryPathAt(c), ochazuke.StringArg(c.Args, "project", "")
	p, err := a.defaultDirectory(c, project, path)
	if err != nil {
		return ochazuke.Failed(err)
	}
	a.reloadDirectories()
	return ochazuke.Done(viewOf(p))
}

func (a *App) makeDirectoryProject(ctx ochazuke.Ctx) ochazuke.Result {
	var paths []string
	for _, subject := range ctx.Subjects {
		if path := a.directoryPathAt(ochazuke.Ctx{Instance: subject}); path != "" {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		paths = append(paths, a.directoryPathAt(ctx))
	}
	return resultOf(a.makeDirectoryProjects(ctx, paths))
}

// Explicit promotion and scan-creates-projects share this data-only path.
// Inspection already ran off-loop; project creation never stats a directory.
func (a *App) makeDirectoryProjects(ctx ochazuke.Ctx, paths []string) ([]projectView, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	projects, err := a.work.Projects()
	if err != nil {
		return nil, err
	}
	existing := map[string]work.Project{}
	for _, p := range projects {
		for _, tree := range p.Trees {
			existing[a.dirKey(tree.Path)] = p
		}
	}
	rows, _ := a.directoryViewRows()
	candidates := map[string]work.Tree{}
	for _, row := range rows {
		if row.Candidate {
			tree := row.Tree
			if tree.Repo == "" {
				tree.Kind, tree.Repo = "repo", tree.Path
			}
			candidates[row.Path] = tree
		}
	}
	for _, path := range paths {
		key := a.dirKey(path)
		if _, ok := existing[key]; !ok {
			if _, candidate := candidates[key]; !candidate {
				return nil, fmt.Errorf("%s is not a scanned Git repository candidate", path)
			}
		}
	}
	out := make([]projectView, 0, len(paths))
	for _, path := range paths {
		key := a.dirKey(path)
		p, ok := existing[key]
		if !ok {
			p, err = a.work.AddProject(filepath.Base(key))
			if err != nil {
				return nil, err
			}
			p, err = a.relateDirectory(ctx, p.ID, candidates[key])
			if err != nil {
				return nil, err
			}
			existing[key] = p
		}
		out = append(out, viewOf(p))
	}
	a.r.Changed("project")
	if err := a.refreshProjects(); err != nil {
		return nil, err
	}
	a.refreshDirectoryRows()
	return out, nil
}
