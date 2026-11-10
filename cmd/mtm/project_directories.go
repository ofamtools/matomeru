package main

import (
	"path/filepath"

	"ofam.dev/matomeru/internal/relations"
	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// A project's directories are relations (internal/work/directories.go):
// "directory" relations from the project to the directory, ordered, each
// with free text and a data object, the default a flag in the data of at
// most one (the kind is Exclusive on "default"). These are the operations
// the directory verbs share; each goes through ochazuke's Relate and
// Unrelate, so the kind's rules, the archive-first unrelate (delete.archive)
// and undo are the library's, not repeated here.

// relationHub is the relation store the runtime is given: the relations
// table, telling the app when a directory relation changed (a verb, an
// undo, a relation set) so its cached projects and Dirs rows read again.
type relationHub struct {
	*relations.Store
	changed func(ochazuke.Relation)
}

func (h relationHub) Put(x ochazuke.Relation) error {
	if err := h.Store.Put(x); err != nil {
		return err
	}
	h.changed(x)
	return nil
}

func (h relationHub) Remove(x ochazuke.Relation) error {
	if err := h.Store.Remove(x); err != nil {
		return err
	}
	h.changed(x)
	return nil
}

// relationChanged reloads what caches a project's directories.
func (a *App) relationChanged(x ochazuke.Relation) {
	if x.Kind != work.DirectoryKind || a.projectList == nil {
		return
	}
	a.directories.Ready = false
	if err := a.refreshProjects(); err != nil {
		a.r.UI().Notify("could not reload projects: " + err.Error())
		return
	}
	a.refreshDirectoryRows()
}

// declareRelationKinds declares the kinds mtm knows and gives the runtime
// its relation store. It runs once, after every noun is defined.
func (a *App) declareRelationKinds() error {
	if err := a.r.DeclareRelationKind(ochazuke.RelationKind{
		Name: work.DirectoryKind, Description: "A directory a project works in: any number, ordered, each with text and data; data.default marks the default",
		From: "project", To: work.DirectoryNoun, Ordered: true, Exclusive: []string{work.DefaultFlag},
		Prop: "directories", Inverse: "projects",
	}); err != nil {
		return err
	}
	a.r.SetRelationStore(relationHub{Store: a.relStore, changed: a.relationChanged})
	return nil
}

// relationCtx is c, or a context on the runtime when c has none (a chooser
// callback runs outside a request).
func (a *App) relationCtx(c ochazuke.Ctx) ochazuke.Ctx {
	if c.Runtime == nil {
		c.Runtime = a.r
	}
	return c
}

// directoryRelation is the live directory relation from the project to the
// path, false when the project does not have it.
func (a *App) directoryRelation(projectID, path string) (ochazuke.Relation, bool, error) {
	return a.directoryRelationIn(projectID, path, false)
}

// directoryRelationIn is directoryRelation, counting an archived relation
// too when archived is set.
func (a *App) directoryRelationIn(projectID, path string, archived bool) (ochazuke.Relation, bool, error) {
	found, err := a.r.RelationsOf(ochazuke.RelationQuery{Kind: work.DirectoryKind, From: work.ProjectRef(projectID), To: work.DirectoryRef(path), Archived: archived})
	if err != nil || len(found) == 0 {
		return ochazuke.Relation{}, false, err
	}
	return found[0], true, nil
}

// relateDirectory attaches the checkout to the project without claiming its
// path: attaching what the project has does nothing; the project takes the
// checkout's repository when it has none; the first directory becomes the
// default (the kind's rule).
func (a *App) relateDirectory(c ochazuke.Ctx, projectID string, tree work.Tree) (work.Project, error) {
	tree.Path = filepath.Clean(tree.Path)
	p, err := a.work.Project(projectID)
	if err != nil {
		return p, err
	}
	// Attaching what the project has does nothing, unless it brings text or
	// data (then the relation is updated: relating again merges).
	if _, has, err := a.directoryRelation(projectID, tree.Path); err != nil {
		return p, err
	} else if !has || tree.Text != "" || tree.Data != nil {
		x := ochazuke.Relation{Kind: work.DirectoryKind, From: work.ProjectRef(projectID), To: work.DirectoryRef(tree.Path), Text: tree.Text, Data: work.DirectoryData(tree)}
		if _, err := a.r.Relate(a.relationCtx(c), x); err != nil {
			return p, err
		}
	}
	if err := a.work.TouchDir(tree.Path); err != nil {
		return p, err
	}
	if p.Repo == "" && tree.Repo != "" {
		p.Repo = tree.Repo
		if err := a.work.PutProject(p); err != nil {
			return p, err
		}
	}
	return a.work.Project(projectID)
}

// defaultDirectory makes an attached directory the project's default.
func (a *App) defaultDirectory(c ochazuke.Ctx, projectID, path string) (work.Project, error) {
	x, has, err := a.directoryRelation(projectID, path)
	if err != nil {
		return work.Project{}, err
	}
	if !has {
		return work.Project{}, work.ErrNotAttached
	}
	if !x.Flag(work.DefaultFlag) {
		x.Data = map[string]any{work.DefaultFlag: true}
		if _, err := a.r.Relate(a.relationCtx(c), x); err != nil {
			return work.Project{}, err
		}
	}
	return a.work.Project(projectID)
}

// unrelateDirectory detaches the path from the project; the directory on
// disk is never touched. Under delete.archive the relation is archived and
// a second removal deletes it. The library moves the default to the next
// directory and says so; promoted is the new default when it moved.
func (a *App) unrelateDirectory(c ochazuke.Ctx, projectID, path string) (p work.Project, promoted string, err error) {
	path = filepath.Clean(path)
	before, err := a.work.Project(projectID)
	if err != nil {
		return before, "", err
	}
	// An archived relation is unrelated too: that removes it for good.
	if _, has, err := a.directoryRelationIn(projectID, path, true); err != nil {
		return before, "", err
	} else if !has {
		return before, "", work.ErrNotAttached
	}
	if _, err := a.r.Unrelate(a.relationCtx(c), work.DirectoryKind, work.ProjectRef(projectID), work.DirectoryRef(path)); err != nil {
		return before, "", err
	}
	p, err = a.work.Project(projectID)
	if err == nil && filepath.Clean(before.DefaultTree) == path && p.DefaultTree != "" {
		promoted = p.DefaultTree
	}
	return p, promoted, err
}

// repointDirectory points one of the project's directories at another path
// (already inspected), keeping its place, text and default mark. When the
// project already has the new path, the old one is dropped instead and the
// new one takes the mark.
func (a *App) repointDirectory(c ochazuke.Ctx, projectID, path string, tree work.Tree) (work.Project, error) {
	path, tree.Path = filepath.Clean(path), filepath.Clean(tree.Path)
	old, has, err := a.directoryRelation(projectID, path)
	if err != nil {
		return work.Project{}, err
	}
	if !has {
		return work.Project{}, work.ErrNotAttached
	}
	if path != tree.Path {
		c = a.relationCtx(c)
		data := map[string]any{"kind": nil, "repo": nil, "branch": nil}
		for k, v := range work.DirectoryData(tree) {
			data[k] = v
		}
		if old.Flag(work.DefaultFlag) {
			data[work.DefaultFlag] = true
		}
		x := ochazuke.Relation{Kind: work.DirectoryKind, From: old.From, To: work.DirectoryRef(tree.Path), Text: old.Text, Data: data, Position: old.Position}
		if _, err := a.r.Relate(c, x); err != nil {
			return work.Project{}, err
		}
		if _, err := a.r.Unrelate(c, work.DirectoryKind, old.From, old.To); err != nil {
			return work.Project{}, err
		}
	}
	if err := a.work.TouchDir(tree.Path); err != nil {
		return work.Project{}, err
	}
	return a.work.Project(projectID)
}

// projectView is a project as the directory verbs and project new and edit
// answer it: the stored row and its directories, in order, and the default.
type projectView struct {
	work.Project
	Directories      []string `json:"directories"`
	DefaultDirectory string   `json:"default_directory,omitempty"`
}

func viewOf(p work.Project) projectView {
	v := projectView{Project: p, Directories: []string{}, DefaultDirectory: p.DefaultTree}
	for _, t := range p.Trees {
		v.Directories = append(v.Directories, t.Path)
	}
	return v
}
