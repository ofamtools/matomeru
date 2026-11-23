package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

// wrote reports a change the owner made, with what "u" would now do.
func (a *App) wrote(what string, cs *data.ChangeSet, err error) {
	switch {
	case err != nil:
		a.fail(what, err)
	case cs == nil:
		a.say("%s: nothing changed", what)
	case len(cs.Conflicts) > 0:
		a.say("%s · %s · u undoes", what, cs.Conflicts[0].Message)
	default:
		a.say("%s · u undoes", what)
	}
}

func (a *App) onRecord(what string, do func(core.Ref, core.Actor) (*data.Record, *data.ChangeSet, error)) func(core.State, core.Command) core.StepResult {
	return func(s core.State, c core.Command) core.StepResult {
		ref, ok := a.subject(s, c)
		if !ok {
			a.say("nothing here to %s", what)
			return core.Next(s)
		}
		_ = a.ensureShadow(ref)
		_, cs, err := do(ref, User)
		a.wrote(what+" "+a.title(ref), cs, err)
		return core.Next(s)
	}
}

func (a *App) undo(redo bool) func(core.State, core.Command) core.StepResult {
	return func(s core.State, _ core.Command) core.StepResult {
		do, word := a.Store.UndoLast, "undid"
		if redo {
			do, word = a.Store.RedoLast, "redid"
		}
		cs, err := do(User)
		switch {
		case errors.Is(err, data.ErrNotFound):
			a.say("nothing to %s", map[bool]string{false: "undo", true: "redo"}[redo])
		case err != nil:
			a.fail(word, err)
		case cs == nil:
			a.say("%s: nothing left to change", word)
		default:
			a.say("%s", strings.Replace(cs.Summary, cs.Action, word, 1))
			if n := len(cs.Conflicts); n > 0 {
				a.say("%s · %s", a.status, cs.Conflicts[0].Message)
			}
		}
		a.reloadForm()
		return core.Next(s)
	}
}

func (a *App) rerun(s core.State, c core.Command) core.StepResult {
	ref, ok := a.subject(s, c)
	if !ok || ref.Kind != data.KindRun {
		a.say("no run here to run again")
		return core.Next(s)
	}
	old, err := a.Store.Get(ref)
	if err != nil {
		a.fail("rerun", err)
		return core.Next(s)
	}
	if a.rt.Runner != nil {
		rec, err := a.rt.Runner.Rerun(context.Background(), ref.ID, runActor)
		return core.Next(a.started(s, "ran "+old.Text("title")+" again", rec, err))
	}
	fields := map[string]any{"runStatus": "queued"}
	for _, name := range []string{"title", "command", "cwd", "task"} {
		if v, has := old.Value(name); has {
			fields[name] = v
		}
	}
	rec, cs, err := a.Store.Create(data.CreateInput{Kind: data.KindRun, Fields: fields}, User)
	if err == nil {
		_, _, err = a.Store.Relate(data.RelateInput{From: rec.Ref(), Kind: "rerunOf", To: ref}, User)
	}
	a.wrote("queued "+old.Text("title")+" again", cs, err)
	if err == nil && s.ActivePage == SpaceRuns {
		a.viewMemo.gen++
		selectRow(a.records(runsTable, data.KindRun), data.RefText(rec.Ref()))
	}
	return core.Next(s)
}

func (a *App) openFocused(s core.State, c core.Command) core.StepResult {
	// On a group's header row, "open" unfolds or folds the group.
	if tbl := a.nav.tables[s.ActiveFrame().FocusID]; tbl != nil && a.Modal() == "" {
		if it, ok := tbl.Focused(); ok && it.Header {
			tbl.ToggleGroup()
			return core.Next(s)
		}
	}
	if a.Modal() == "" {
		if chain := ui.ContextChain(a.View(s), s.ActiveFrame().FocusID, nil); len(chain) > 0 && chain[0].Facts["empty"] == true {
			a.say("empty section · press n to add")
			return core.Next(s)
		}
	}
	ref, ok := a.subject(s, c)
	if !ok {
		a.say("nothing here to open")
		return core.Next(s)
	}
	if raw, isMap := c.Args["ref"].(map[string]any); isMap {
		if r, ok := data.AsRef(raw); ok {
			ref = r
		}
	}
	return core.Next(a.show(s, ref))
}

func (a *App) edit(s core.State, c core.Command) core.StepResult {
	ref, ok := a.subject(s, c)
	if !ok {
		a.say("nothing here to edit")
		return core.Next(s)
	}
	_ = a.ensureShadow(ref)
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("edit", err)
		return core.Next(s)
	}
	rf := a.newRecordFormModal(ref.Kind, &ref, nil, views.FormForRecordWith(a.schema(ref.Kind), rec, a.editOptions()))
	a.say("ctrl+s saves · esc closes (saves draft)")
	return core.Next(a.pushModal(s, rf))
}

func (a *App) create(s core.State, c core.Command) core.StepResult {
	kind := c.Arg("kind")
	if kind == "" {
		kind = data.KindTask
	}
	schema := a.schema(kind)
	fm := views.FormForRecordWith(schema, nil, a.editOptions())
	var about *core.Ref
	if ownerRef, ok := c.Args["ownerRef"].(core.Ref); ok && ownerRef.ID != "" {
		if field, _ := c.Args["ownerField"].(string); field != "" {
			if _, hasField := schema.Field(field); hasField {
				fm.SetValue(field, ownerRef)
			}
		}
		if rel, _ := c.Args["ownerRel"].(string); rel == data.RelAbout {
			r := ownerRef
			about = &r
		}
		if _, hasTask := schema.Field("task"); hasTask && fm.Values["task"] == nil {
			task := ownerRef
			if ownerRef.Kind != data.KindTask {
				if rec, err := a.Store.Get(ownerRef); err == nil {
					task, _ = rec.RefField("task")
				}
			}
			if task.Kind == data.KindTask && task.ID != "" {
				fm.SetValue("task", task)
			}
		}
		fm.Dirty = false
		rf := a.newRecordFormModal(kind, nil, about, fm)
		a.say("new %s · ctrl+s creates · esc closes", strings.ToLower(schema.Title))
		return core.Next(a.pushModal(s, rf))
	}
	subject, has := a.subject(s, c)
	switch {
	case kind == data.KindTask:
		// On a task's Show page (or when targeting a task directly) a new task is its subtask.
		if cur, ok := a.Showing(); ok && cur.Kind == data.KindTask && s.ActivePage == SpaceWork {
			fm.SetValue("parent", cur)
		} else if c.Target != nil && c.Target.Kind == data.KindTask && c.Target.ID != "" {
			fm.SetValue("parent", *c.Target)
		}
	case has:
		// A note (or anything else with a task) belongs to the focused
		// record's task, and a note is about the focused record.
		if kind == data.KindNote {
			about = &subject
		}
		task := subject
		if subject.Kind != data.KindTask {
			if rec, err := a.Store.Get(subject); err == nil {
				task, _ = rec.RefField("task")
			}
		}
		if _, ok := schema.Field("task"); ok && task.Kind == data.KindTask && task.ID != "" {
			fm.SetValue("task", task)
		}
	}
	fm.Dirty = false
	rf := a.newRecordFormModal(kind, nil, about, fm)
	a.say("new %s · ctrl+s creates · esc closes", strings.ToLower(schema.Title))
	return core.Next(a.pushModal(s, rf))
}

func (a *App) newHere(s core.State, c core.Command) core.StepResult {
	if s.ActivePage == SpacePanes {
		c.Args = map[string]any{"kind": data.KindPane}
		return a.create(s, c)
	}
	// On a Show page, focus on or inside a related section creates (or
	// attaches) one of that section's kind linked to the shown record.
	if cur, showing := a.Showing(); showing && s.ActivePage == SpaceWork {
		for _, f := range ui.ContextChain(a.View(s), s.ActiveFrame().FocusID, nil) {
			if rel, _ := f.Facts["relationKind"].(string); rel == data.RelDirectory {
				target := cur
				var start any
				if rec, err := a.Store.Get(cur); err == nil {
					start, _ = rec.Value("defaultDir")
				}
				return core.Next(a.openPath(s, "", &target, start))
			}
			if kind, _ := f.Facts["sectionKind"].(string); kind != "" {
				if _, known := a.Store.Registry().Get(kind); known {
					args := map[string]any{"kind": kind, "ownerRef": cur}
					if field, _ := f.Facts["sectionField"].(string); field != "" {
						args["ownerField"] = field
					}
					if rel, _ := f.Facts["relationKind"].(string); rel != "" {
						args["ownerRel"] = rel
					}
					c.Args = args
					return a.create(s, c)
				}
			}
		}
	}
	// In Work, "new" makes a record of the noun the index lists.
	c.Args = map[string]any{"kind": a.workKind()}
	return a.create(s, c)
}

// reloadForm refreshes the open edit form from the store (after undo, redo).
func (a *App) reloadForm() {
	rf := a.activeRecordForm()
	if rf == nil || rf.ref == nil {
		return
	}
	rec, err := a.Store.Get(*rf.ref)
	if err != nil {
		return
	}
	fm := rf.Model()
	idx := fm.ActiveIdx
	schema := a.schema(rf.kind)
	fresh := views.FormForRecordWith(schema, rec, a.editOptions())
	fresh.SubmitRow = true
	fresh.SubmitLabel = "Save " + strings.ToLower(schema.Title)
	a.applyHarnessChoicesToForm(rf.kind, rf.ref, fresh)
	a.attachFormDrafts(fresh, rf.draftKey)

	fm.Title = fresh.Title
	fm.Specs = fresh.Specs
	fm.Values = fresh.Values
	fm.Errors = fresh.Errors
	fm.Dirty = fresh.Dirty
	fm.Editing = false
	fm.SubmitRow = fresh.SubmitRow
	fm.SubmitLabel = fresh.SubmitLabel
	fm.SetDrafts(fresh.Drafts)
	fm.ActiveIdx = min(idx, max(len(fm.Specs), 0))
}

// syncManagedFile writes a managed file for a document or note when it has a
// body and no external user filePath, keeping filePath and path in sync.
func (a *App) syncManagedFile(rec *data.Record) {
	if rec == nil || (rec.Kind != data.KindDocument && rec.Kind != data.KindNote) {
		return
	}
	root := a.Store.FilesRoot()
	if root == "" {
		return
	}
	body := rec.Text("body")
	if body == "" {
		return
	}
	existing := rec.Text("filePath")
	if existing == "" {
		existing = rec.Text("path")
	}
	if existing != "" && !data.IsManagedPath(root, existing) {
		return // preserve external user path
	}
	cat := data.CategoryNotes
	if rec.Kind == data.KindDocument {
		cat = data.CategoryDocuments
	}
	p, err := data.WriteManagedFile(root, cat, rec.ID, rec.Text("title"), ".md", body)
	if err != nil {
		return
	}
	if rec.Text("filePath") != p || rec.Text("path") != p {
		_, _, _ = a.Store.Patch(data.PatchInput{
			Ref:     rec.Ref(),
			Fields:  map[string]any{"filePath": p, "path": p},
			Summary: "record managed file path",
		}, core.Actor{Kind: "system", ID: "mtm.files", Label: "managed files"})
	}
}

func recordIntField(rec data.Record, name string) int {
	v, ok := rec.Value(name)
	if !ok || v == nil {
		return 0
	}
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		var n int
		_, _ = fmt.Sscan(strings.TrimSpace(x), &n)
		return n
	}
	return 0
}
