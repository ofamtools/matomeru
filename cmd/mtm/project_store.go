package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/matomeru/store"
	"github.com/unixsuperhero/ochazuke"
)

// The project noun's record side (standard verbs phase 1, lane M3a): its
// props and its store, so project get, list, create, set (alias patch),
// watch, rename (the name) and duplicate are ochazuke's standard verbs
// over the work store. project new and project edit wrap create and set
// (project.go). A project has no owner, so it has no move. delete is the
// standard delete: the store's ConfirmFor counts what it orphans
// (delete.go). pin, unpin and pick are the app's scope, not a
// prop; the directory verbs wait for list props (phase 2); associate is
// the reverse of the relations (relations.go).

// projectEditorProps are the props the project editor shows, in order
// (the editor's schema before props).
var projectEditorProps = []string{"name", "color", "glyph", "harness", "preset"}

// projectProps are the project's props: the key; the name (the label; an
// empty one shows the default directory); the theme's colour and glyph;
// the launch defaults (harness, preset); the default directory, which
// create attaches (the directory verbs change it after); and the
// read-only repo, origin and creation time.
func projectProps() []ochazuke.PropSpec {
	text := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "string", Description: desc} }
	return []ochazuke.PropSpec{
		{Name: "id", Key: true, Parameter: text("The project's key")},
		{Name: "name", Role: ochazuke.RoleLabel, Fallback: "dir", Parameter: text("The project's name (empty: untitled, shown by its directory)")},
		{Name: "color", Parameter: text("The colour the project is drawn with")},
		{Name: "glyph", Parameter: text("The glyph drawn before the project's name (empty: " + defaultProjectGlyph + ")")},
		{Name: "harness", Parameter: ochazuke.Parameter{Type: "string", Source: "harness.names", Free: true,
			Description: "The harness launches into this project use (empty: the default); one not registered is saved with a notice"}},
		{Name: "preset", Parameter: text("The launch preset")},
		{Name: "dir", CreateOnly: true, Parameter: ochazuke.Parameter{Type: "string",
			Description: "The default directory: create attaches it (a missing path is attached and flagged); project default and project directory change it after"}},
		{Name: "repo", ReadOnly: true, Parameter: text("The repository of the project's first checkout")},
		{Name: "origin", ReadOnly: true, Parameter: text("The tool that created or adopted the project (empty: mtm)")},
		{Name: "created", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "integer", Description: "When the project was created (Unix ms)"}},
		// No Default: a create that names none leaves it unset (private, with
		// the Inbox item), not marked private by the owner.
		{Name: "privacy", Parameter: ochazuke.Parameter{Type: "string", Enum: work.Privacies,
			Description: "Whether the project's tasks may leave mtm: family (exported) or private (kept in mtm; a project with none set reads private); make-family and make-private set it; only the owner makes one family"}},
	}
}

// projectStore is the project noun's store over the work store.
// history.undo may revert its writes (Undo), and a create may keep a key
// it is given (AcceptsKey).
func (a *App) projectStore() ochazuke.Store {
	return ochazuke.Store{
		Kind: ochazuke.Collection,
		Undo: true, AcceptsKey: true,
		// The standard delete's prompt names what the delete orphans.
		ConfirmFor: a.deletionConfirm("project"),
		Get: func(_ ochazuke.Ctx, id string) (ochazuke.Thing, error) {
			p, err := a.projectByID(id)
			if err != nil {
				return nil, err
			}
			return projectThing(p), nil
		},
		List: func(ochazuke.Ctx, ochazuke.ListQuery) ([]ochazuke.Thing, error) {
			projects, err := a.work.Projects()
			if err != nil {
				return nil, err
			}
			out := make([]ochazuke.Thing, len(projects))
			for i, p := range projects {
				out[i] = projectThing(p)
			}
			return out, nil
		},
		Create: a.createProject,
		Patch:  a.patchProject,
		Delete: a.removeProject,
	}
}

// projectByID is the stored project with that id, "no project" when there
// is none.
func (a *App) projectByID(id string) (work.Project, error) {
	p, err := a.work.Project(id)
	if errors.Is(err, store.ErrNotFound) {
		return work.Project{}, fmt.Errorf("no project %q", id)
	}
	return p, err
}

// projectThing is a project as its props.
func projectThing(p work.Project) ochazuke.Thing {
	return ochazuke.Thing{
		"id": p.ID, "name": p.Name, "color": p.Theme.Color, "glyph": p.Theme.Glyph,
		"harness": p.Settings.Harness, "preset": p.Settings.Preset, "dir": projectDir(p),
		"repo": p.Repo, "origin": p.Origin, "created": p.Created, "privacy": work.PrivacyOf(p),
	}
}

// writeProjectProps writes the changed writable props onto p: the name
// trimmed; a harness the registry does not know is written as given with
// a notice (record first: the launch falls back to the default).
func (a *App) writeProjectProps(c ochazuke.Ctx, p *work.Project, values ochazuke.Thing) {
	if v, ok := thingString(values, "name"); ok {
		p.Name = strings.TrimSpace(v)
	}
	if v, ok := thingString(values, "color"); ok {
		p.Theme.Color = v
	}
	if v, ok := thingString(values, "glyph"); ok {
		p.Theme.Glyph = v
	}
	if v, ok := thingString(values, "harness"); ok {
		if !a.knownHarness(v) {
			c.Notify(fmt.Sprintf("unknown harness %s; saved, launches use the default until it is registered", v))
		}
		p.Settings.Harness = v
	}
	if v, ok := thingString(values, "preset"); ok {
		p.Settings.Preset = v
	}
	if v, ok := thingString(values, "privacy"); ok && v != "" {
		p.Settings.Privacy = v
	}
}

// createProject is the store's Create: a new open project from the props
// given, under the key given when it is free; dir is attached and made
// the default.
func (a *App) createProject(c ochazuke.Ctx, values ochazuke.Thing) (ochazuke.Thing, error) {
	id, _ := thingString(values, "id")
	if id != "" {
		if _, err := a.work.Project(id); err == nil {
			id = ""
		}
	}
	if id == "" {
		id = work.NewID()
	}
	p := work.Project{ID: id, Status: "open", Created: work.Now()}
	a.writeProjectProps(c, &p, values)
	if err := a.work.PutProject(p); err != nil {
		return nil, err
	}
	if dir, _ := thingString(values, "dir"); strings.TrimSpace(dir) != "" {
		tree := inspectDirectory(expandPath(dir))
		if _, err := a.work.AddTree(p.ID, tree); err != nil {
			return nil, err
		}
		var err error
		if p, err = a.work.SetDefaultTree(p.ID, tree.Path); err != nil {
			return nil, err
		}
		a.directories.Ready = false
	}
	if err := a.refreshProjects(); err != nil {
		return nil, err
	}
	a.notePrivacy(func(m string) { c.Notify(m) }, p)
	return projectThing(p), nil
}

// patchProject is the store's Patch: it writes the changed props. An
// editor open on the project writes its last change and closes first, so
// it never writes its old values over the patch.
func (a *App) patchProject(c ochazuke.Ctx, id string, changes ochazuke.Thing) (ochazuke.Thing, error) {
	p, err := a.projectByID(id)
	if err != nil {
		return nil, err
	}
	if a.editor != nil && a.editor.Record.Noun == "project" && a.editor.Record.ID == id {
		if err := a.leaveEditor(); err != nil {
			return nil, err
		}
		if p, err = a.projectByID(id); err != nil {
			return nil, err
		}
	}
	a.writeProjectProps(c, &p, changes)
	if err := a.work.PutProject(p); err != nil {
		return nil, err
	}
	if err := a.refreshProjects(); err != nil {
		return nil, err
	}
	if _, ok := changes["privacy"]; ok {
		a.notePrivacy(func(m string) { c.Notify(m) }, p)
	}
	return projectThing(p), nil
}

// removeProject is the store's Delete (project.delete, and history.undo
// of a create): the project goes and what it held is orphaned.
func (a *App) removeProject(c ochazuke.Ctx, id string) error {
	if _, err := a.projectByID(id); err != nil {
		return err
	}
	_, err := a.deleteThing(c, "project", id)
	return err
}

// projectFields are the project editor's fields, read from the project's
// props (projectEditorProps); the harness is chosen from the registry's
// names, never typed free.
func (a *App) projectFields() []ochazuke.RecordField {
	var out []ochazuke.RecordField
	for _, name := range projectEditorProps {
		p, ok := a.r.PropOf("project", name)
		if !ok {
			continue
		}
		f := ochazuke.PropField(p)
		f.Parameter.Description, f.Parameter.Free = "", false
		out = append(out, f)
	}
	return out
}

// saveProjectRecord is the editor's write of a project: a new project is
// created by its first write; a harness the registry does not know is
// never written from the editor (it keeps the stored one).
func (a *App) saveProjectRecord(record editRecord, values ochazuke.Record) (any, error) {
	p := work.Project{ID: record.ID, Status: "open", Created: work.Now()}
	if !record.New {
		var err error
		if p, err = a.work.Project(record.ID); err != nil {
			return nil, err
		}
	}
	p.Name = strings.TrimSpace(values["name"])
	p.Theme = work.Theme{Color: values["color"], Glyph: values["glyph"]}
	harness := values["harness"]
	if !a.knownHarness(harness) {
		harness = p.Settings.Harness
	}
	p.Settings.Harness, p.Settings.Preset = harness, values["preset"] // the privacy stays
	return p, a.work.PutProject(p)
}
