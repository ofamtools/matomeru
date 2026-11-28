package app

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
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

func batchPastTense(what string) string {
	switch what {
	case "star":
		return "starred"
	case "unstar":
		return "unstarred"
	case "snooze":
		return "snoozed"
	case "unsnooze":
		return "unsnoozed"
	case "archive":
		return "archived"
	case "restore":
		return "restored"
	default:
		return what
	}
}

func (a *App) onRecord(what string, do func(core.Ref, core.Actor) (*data.Record, *data.ChangeSet, error)) func(core.State, core.Command) core.StepResult {
	return func(s core.State, c core.Command) core.StepResult {
		refs, fromSel := a.targetRefs(s, c)
		if len(refs) == 0 {
			a.say("nothing here to %s", what)
			return core.Next(s)
		}
		if fromSel {
			a.clearActiveVisual(s)
		}
		if len(refs) == 1 {
			ref := refs[0]
			_ = a.ensureShadow(ref)
			_, cs, err := do(ref, User)
			a.wrote(what+" "+a.title(ref), cs, err)
			return core.Next(s)
		}
		changed := 0
		var firstErr error
		for _, ref := range refs {
			_ = a.ensureShadow(ref)
			_, cs, err := do(ref, User)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if cs != nil {
				changed++
			}
		}
		if changed == 0 && firstErr != nil {
			a.fail(what, firstErr)
			return core.Next(s)
		}
		if changed == 0 {
			a.say("%s: nothing changed", what)
			return core.Next(s)
		}
		a.say("%s %d items · u undoes", batchPastTense(what), changed)
		return core.Next(s)
	}
}

func (a *App) archiveRecord(s core.State, c core.Command) core.StepResult {
	refs, fromSel := a.targetRefs(s, c)
	if len(refs) == 0 {
		a.say("nothing here to archive")
		return core.Next(s)
	}
	if fromSel {
		a.clearActiveVisual(s)
	}
	if len(refs) == 1 {
		ref := refs[0]
		_ = a.ensureShadow(ref)
		_, cs, err := a.Store.Archive(ref, User)
		a.wroteRecord("archive", ref, cs, err)
		if err == nil && cs != nil && a.archiveAnimEnabled() && !a.nav.showArchived {
			if eff := a.startArchiveAnim(ref); eff != nil {
				return core.StepResult{State: s, Effects: []core.Effect{eff}}
			}
		}
		return core.Next(s)
	}
	changed := 0
	var firstErr error
	var effects []core.Effect
	for _, ref := range refs {
		_ = a.ensureShadow(ref)
		_, cs, err := a.Store.Archive(ref, User)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if cs != nil {
			changed++
			if a.archiveAnimEnabled() && !a.nav.showArchived {
				if eff := a.startArchiveAnim(ref); eff != nil {
					effects = append(effects, eff)
				}
			}
		}
	}
	if changed == 0 && firstErr != nil {
		a.fail("archive", firstErr)
		return core.Next(s)
	}
	if changed == 0 {
		a.say("archive: nothing changed")
		return core.Next(s)
	}
	a.say("archived %d %s · u undoes", changed, a.batchNoun(refs))
	return core.StepResult{State: s, Effects: effects}
}

func (a *App) restoreRecord(s core.State, c core.Command) core.StepResult {
	refs, fromSel := a.targetRefs(s, c)
	if len(refs) == 0 {
		a.say("nothing here to restore")
		return core.Next(s)
	}
	if fromSel {
		a.clearActiveVisual(s)
	}
	if len(refs) == 1 {
		ref := refs[0]
		_ = a.ensureShadow(ref)
		_, cs, err := a.Store.Restore(ref, User)
		if err == nil {
			a.cancelArchiveAnim(ref)
		}
		a.wroteRecord("restore", ref, cs, err)
		return core.Next(s)
	}
	changed := 0
	var firstErr error
	for _, ref := range refs {
		_ = a.ensureShadow(ref)
		_, cs, err := a.Store.Restore(ref, User)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		a.cancelArchiveAnim(ref)
		if cs != nil {
			changed++
		}
	}
	if changed == 0 && firstErr != nil {
		a.fail("restore", firstErr)
		return core.Next(s)
	}
	if changed == 0 {
		a.say("restore: nothing changed")
		return core.Next(s)
	}
	a.say("restored %d %s · u undoes", changed, a.batchNoun(refs))
	return core.Next(s)
}

func (a *App) undo(redo bool) func(core.State, core.Command) core.StepResult {
	return func(s core.State, _ core.Command) core.StepResult {
		return a.contextualUndo(s, redo)
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
	return core.Next(a.openRecordTarget(s, ref))
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
	schema := a.schema(ref.Kind)
	rf := a.newRecordFormModal(ref.Kind, &ref, nil, views.FormForRecordWith(schema, rec, a.editOptionsFor(schema, rec)))
	a.say("ctrl+s saves · esc closes (saves draft)")
	return core.Next(a.pushModal(s, rf))
}

func boolArg(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "1", "yes":
			return true
		}
	}
	return false
}

func objectArg(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func stringListArg(v any) []string {
	switch x := v.(type) {
	case []string:
		var out []string
		for _, s := range x {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		var out []string
		for _, item := range x {
			if s := strings.TrimSpace(data.Text(item)); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		var out []string
		for _, part := range strings.Split(x, ",") {
			if s := strings.TrimSpace(part); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func stringListMapArg(v any) map[string][]string {
	if v == nil {
		return nil
	}
	out := map[string][]string{}
	switch m := v.(type) {
	case map[string][]string:
		for k, vals := range m {
			if list := stringListArg(vals); len(list) > 0 {
				out[k] = list
			}
		}
	case map[string]any:
		for k, raw := range m {
			if list := stringListArg(raw); len(list) > 0 {
				out[k] = list
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func filterChoiceValues(enum, allow, exclude []string) []string {
	var out []string
	switch {
	case len(allow) > 0 && len(enum) > 0:
		for _, v := range enum {
			if slices.Contains(allow, v) {
				out = append(out, v)
			}
		}
	case len(allow) > 0:
		out = slices.Clone(allow)
	default:
		out = slices.Clone(enum)
	}
	if len(exclude) > 0 && len(out) > 0 {
		filtered := make([]string, 0, len(out))
		for _, v := range out {
			if !slices.Contains(exclude, v) {
				filtered = append(filtered, v)
			}
		}
		out = filtered
	}
	return out
}

// shapeCreateSchema returns a copy of schema shaped by generic record.new
// binding args: fields (allowlist), hideFields (denylist), choices (allowed
// enum values), and excludeChoices (excluded enum values).
func shapeCreateSchema(schema data.Schema, args map[string]any) data.Schema {
	if len(args) == 0 {
		return schema
	}
	allow := stringListArg(args["fields"])
	hide := stringListArg(args["hideFields"])
	choices := stringListMapArg(args["choices"])
	exclude := stringListMapArg(args["excludeChoices"])
	if len(allow) == 0 && len(hide) == 0 && len(choices) == 0 && len(exclude) == 0 {
		return schema
	}
	shaped := schema
	fields := make([]data.FieldSpec, 0, len(schema.Fields))
	for _, f := range schema.Fields {
		if len(allow) > 0 && !slices.Contains(allow, f.Name) {
			continue
		}
		if len(hide) > 0 && slices.Contains(hide, f.Name) {
			continue
		}
		if len(f.Enum) > 0 {
			f.Enum = slices.Clone(f.Enum)
		}
		fieldAllow := choices[f.Name]
		fieldExclude := exclude[f.Name]
		if len(fieldAllow) > 0 || len(fieldExclude) > 0 {
			f.Enum = filterChoiceValues(f.Enum, fieldAllow, fieldExclude)
			if len(f.Enum) > 0 {
				if oldDef := strings.TrimSpace(data.Text(f.Default)); oldDef != "" && !slices.Contains(f.Enum, oldDef) {
					f.Default = f.Enum[0]
				}
			}
		}
		fields = append(fields, f)
	}
	shaped.Fields = fields
	return shaped
}

func filterFormFields(fm *form.Model, allow, hide []string) {
	if fm == nil || (len(allow) == 0 && len(hide) == 0) {
		return
	}
	kept := make([]core.ParamSpec, 0, len(fm.Specs))
	for _, sp := range fm.Specs {
		if len(allow) > 0 && !slices.Contains(allow, sp.Name) {
			continue
		}
		if len(hide) > 0 && slices.Contains(hide, sp.Name) {
			continue
		}
		kept = append(kept, sp)
	}
	fm.Specs = kept
}

// applyFormChoiceFilters applies generic choices and excludeChoices narrowing
// to a form's field specs and updates any excluded default/current value.
func applyFormChoiceFilters(fm *form.Model, choices, exclude map[string][]string) {
	if fm == nil || (len(choices) == 0 && len(exclude) == 0) {
		return
	}
	wasDirty := fm.Dirty
	for i := range fm.Specs {
		name := fm.Specs[i].Name
		allow := choices[name]
		excl := exclude[name]
		if len(allow) == 0 && len(excl) == 0 {
			continue
		}
		nextEnum := filterChoiceValues(fm.Specs[i].Enum, allow, excl)
		fm.Specs[i].Enum = nextEnum
		if len(nextEnum) > 0 {
			if oldDef := strings.TrimSpace(data.Text(fm.Specs[i].Default)); oldDef != "" && !slices.Contains(nextEnum, oldDef) {
				fm.Specs[i].Default = nextEnum[0]
			}
			if cur := strings.TrimSpace(data.Text(fm.Values[name])); cur != "" && !slices.Contains(nextEnum, cur) {
				fm.SetValue(name, nextEnum[0])
			}
		}
	}
	fm.Dirty = wasDirty
}

func synthesizeSummaryFallback(schema data.Schema, values, defaults map[string]any) string {
	label := strings.TrimSpace(data.Text(defaults["harness"]))
	if label == "" {
		label = strings.TrimSpace(data.Text(values["harness"]))
	}
	base := ""
	if cwd := strings.TrimSpace(data.Text(values["cwd"])); cwd != "" {
		b := filepath.Base(filepath.Clean(cwd))
		if b != "" && b != "." && b != string(filepath.Separator) {
			base = b
		}
	}
	switch {
	case label != "" && base != "":
		return label + " · " + base
	case label != "":
		return label
	case base != "":
		return base
	case strings.TrimSpace(schema.Title) != "":
		return schema.Title
	default:
		return schema.Kind
	}
}

func asRefValue(v any) (core.Ref, bool) {
	if ref, ok := data.AsRef(v); ok {
		return ref, true
	}
	if s, ok := v.(string); ok {
		if kind, id, has := strings.Cut(strings.TrimSpace(s), ":"); has && kind != "" && id != "" {
			return core.Ref{Kind: kind, ID: id}, true
		}
	}
	return core.Ref{}, false
}

func (a *App) inferCreateCwd(s core.State, subject core.Ref, hasSubject bool, taskVal any) string {
	if taskRef, ok := asRefValue(taskVal); ok {
		if dir := a.defaultDirForTask(taskRef); dir != "" {
			return dir
		}
	}
	if hasSubject {
		if subject.Kind == data.KindTask {
			if dir := a.defaultDirForTask(subject); dir != "" {
				return dir
			}
		} else if rec, err := a.Store.Get(subject); err == nil && rec != nil {
			if dir := strings.TrimSpace(rec.Text("defaultDir")); dir != "" {
				return dir
			}
			if dir := strings.TrimSpace(rec.Text("cwd")); dir != "" {
				return dir
			}
		}
	}
	if isProjectTab(s.ActivePage) {
		if pid := projectIDFromTab(s.ActivePage); pid != "" {
			if dir := a.defaultDirForTask(core.Ref{Kind: data.KindTask, ID: pid}); dir != "" {
				return dir
			}
		}
	}
	if s.ActivePage == SpacePanes {
		inheritProj := a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetInheritProjectTask, true)
		if inheritProj {
			if projTask, ok := a.railActiveProjectTask(s); ok {
				if dir := a.defaultDirForTask(projTask); dir != "" {
					return dir
				}
			}
		}
		if pane, ok := a.SelectedPane(); ok && pane != nil {
			if t, ok := pane.RefField("task"); ok {
				if dir := a.defaultDirForTask(t); dir != "" {
					return dir
				}
			}
			if dir := strings.TrimSpace(pane.Text("cwd")); dir != "" {
				return dir
			}
		}
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return ""
}

// createRecordFromValues persists a new record of kind from submitted form or
// instant-create values, runs post-create hooks (managed file sync, about
// relation, universal star/archive/snooze), and reports status.
func (a *App) createRecordFromValues(kind string, about *core.Ref, values map[string]any, snoozeUntil *time.Time) (*data.Record, bool) {
	schema := a.schema(kind)
	fields := maps.Clone(values)
	if fields == nil {
		fields = map[string]any{}
	}
	maps.DeleteFunc(fields, func(k string, _ any) bool {
		return views.IsUniversalField(k) || data.IsRelationFormField(schema, k)
	})

	rec, cs, err := a.Store.Create(data.CreateInput{Kind: kind, Fields: fields}, User)
	if err != nil {
		a.fail("create "+kind, err)
		return nil, false
	}
	ref := rec.Ref()
	a.markOwnerCreated(ref)
	a.syncManagedFile(rec)
	sub := data.FormSubmission{Schema: schema, Submitted: values, ResolveRef: a.resolveFormRef}
	_, _ = a.applyRelationDiff(ref, sub.RelationDiff())
	if about != nil {
		if _, _, err := a.Store.Relate(data.RelateInput{From: ref, Kind: data.RelAbout, To: *about}, User); err != nil {
			a.fail("attach note", err)
		}
	}
	if data.Text(values[data.ColStarStatus]) == data.StarStarred {
		_, _, _ = a.Store.Star(ref, User)
	}
	if data.Text(values[data.ColArchiveStatus]) == data.ArchiveArchived {
		_, _, _ = a.Store.Archive(ref, User)
	}
	if snoozeUntil != nil {
		_, _, _ = a.Store.Snooze(ref, *snoozeUntil, User)
	} else if snoozeText := strings.TrimSpace(data.Text(values[data.ColSnoozedUntil])); snoozeText != "" {
		if t, ok := parseSnoozeTime(snoozeText); ok {
			_, _, _ = a.Store.Snooze(ref, t, User)
		}
	}
	a.wrote(fmt.Sprintf("created %s %q", strings.ToLower(schema.Title), views.Title(schema, *rec)), cs, nil)
	return rec, true
}

func (a *App) create(s core.State, c core.Command) core.StepResult {
	kind := c.Arg("kind")
	if kind == "" {
		kind = data.KindTask
	}
	schema := a.schema(kind)
	shaped := shapeCreateSchema(schema, c.Args)
	fm := views.FormForRecordWith(shaped, nil, a.editOptions())
	allowFields := stringListArg(c.Args["fields"])
	hideFields := stringListArg(c.Args["hideFields"])
	filterFormFields(fm, allowFields, hideFields)

	hiddenDefaults := map[string]any{}
	hasSpec := func(name string) bool {
		return slices.ContainsFunc(fm.Specs, func(sp core.ParamSpec) bool { return sp.Name == name })
	}
	setField := func(name string, val any) {
		if hasSpec(name) {
			fm.SetValue(name, val)
		} else {
			hiddenDefaults[name] = val
		}
	}
	getField := func(name string) any {
		if v, ok := fm.Values[name]; ok && v != nil && strings.TrimSpace(data.Text(v)) != "" {
			return v
		}
		if v, ok := hiddenDefaults[name]; ok && v != nil && strings.TrimSpace(data.Text(v)) != "" {
			return v
		}
		return nil
	}

	var about *core.Ref
	var subject core.Ref
	var hasSubject bool
	if ownerRef, ok := c.Args["ownerRef"].(core.Ref); ok && ownerRef.ID != "" {
		subject, hasSubject = ownerRef, true
		if field, _ := c.Args["ownerField"].(string); field != "" {
			if _, hasField := schema.Field(field); hasField {
				setField(field, ownerRef)
			}
		}
		if rel, _ := c.Args["ownerRel"].(string); rel == data.RelAbout {
			r := ownerRef
			about = &r
		}
		if _, hasTask := schema.Field("task"); hasTask && getField("task") == nil {
			task := ownerRef
			if ownerRef.Kind != data.KindTask {
				if rec, err := a.Store.Get(ownerRef); err == nil {
					task, _ = rec.RefField("task")
				}
			}
			if task.Kind == data.KindTask && task.ID != "" {
				setField("task", task)
			}
		}
	} else {
		subject, hasSubject = a.subject(s, c)
		inheritProj := (s.ActivePage == SpacePanes || isProjectTab(s.ActivePage)) && (a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetInheritProjectTask, true))
		var projTask core.Ref
		var hasProjTask bool
		if inheritProj {
			if isProjectTab(s.ActivePage) {
				if pid := projectIDFromTab(s.ActivePage); pid != "" {
					projTask, hasProjTask = core.Ref{Kind: data.KindTask, ID: pid}, true
				}
			} else {
				projTask, hasProjTask = a.railActiveProjectTask(s)
			}
		}
		switch {
		case kind == data.KindTask:
			// On a task's Show page (or when targeting a task directly) a new task is its subtask.
			if cur, ok := a.showingOnPage(s); ok && cur.Kind == data.KindTask {
				setField("parent", cur)
			} else if c.Target != nil && c.Target.Kind == data.KindTask && c.Target.ID != "" {
				setField("parent", *c.Target)
			} else if hasProjTask && getField("parent") == nil {
				setField("parent", projTask)
			}
		case hasSubject:
			// A note (or anything else with a task) belongs to the focused
			// record's task, and a note is about the focused record.
			if kind == data.KindNote {
				r := subject
				about = &r
			}
			task := subject
			if subject.Kind != data.KindTask {
				if rec, err := a.Store.Get(subject); err == nil {
					task, _ = rec.RefField("task")
				}
			}
			if hasProjTask {
				task = projTask
			} else if (task.Kind != data.KindTask || task.ID == "") && s.ActivePage == SpacePanes && inheritProj {
				if pane, ok := a.SelectedPane(); ok && pane != nil {
					task, _ = pane.RefField("task")
				}
			}
			if _, ok := schema.Field("task"); ok && task.Kind == data.KindTask && task.ID != "" {
				setField("task", task)
			}
		case s.ActivePage == SpacePanes || isProjectTab(s.ActivePage):
			if _, ok := schema.Field("task"); ok && hasProjTask {
				setField("task", projTask)
			}
		}
	}

	defaults := objectArg(c.Args["defaults"])
	for k, v := range defaults {
		setField(k, v)
	}

	if _, hasCwd := schema.Field("cwd"); hasCwd && getField("cwd") == nil {
		if dir := a.inferCreateCwd(s, subject, hasSubject, getField("task")); dir != "" {
			setField("cwd", dir)
		}
	}

	if title := strings.TrimSpace(c.Arg("title")); title != "" {
		fm.Title = title
	}

	choices := stringListMapArg(c.Args["choices"])
	exclude := stringListMapArg(c.Args["excludeChoices"])

	if boolArg(c.Args["submit"]) {
		finalValues := fm.TypedValues()
		if finalValues == nil {
			finalValues = map[string]any{}
		}
		for k, v := range fm.Values {
			if _, exists := finalValues[k]; !exists && v != nil {
				finalValues[k] = v
			}
		}
		for k, v := range hiddenDefaults {
			if _, exists := finalValues[k]; !exists {
				finalValues[k] = v
			}
		}
		summaryField := schema.SummaryField
		if summaryField == "" {
			summaryField = "title"
		}
		if _, hasSummary := schema.Field(summaryField); hasSummary && strings.TrimSpace(data.Text(finalValues[summaryField])) == "" {
			finalValues[summaryField] = synthesizeSummaryFallback(schema, finalValues, defaults)
		}
		rec, ok := a.createRecordFromValues(kind, about, finalValues, nil)
		if !ok {
			return core.Next(s)
		}
		if rec.Kind == data.KindPane {
			a.maybeHarvestPaneRepo(rec.Text("cwd"))
			a.viewMemo.gen++
			if isProjectTab(s.ActivePage) && a.projectChildPaneViewEnabled() {
				s = a.openProjectChildPane(s, projectIDFromTab(s.ActivePage), rec.ID, true)
				return core.Next(s)
			}
			rail := a.rail()
			if !selectRow(rail, data.RefText(rec.Ref())) {
				rail.Query = ""
				selectRow(rail, data.RefText(rec.Ref()))
			}
			a.panes.activePage[rec.ID] = views.ScreenPageID
			s.ActivePage = SpacePanes
			s = a.enterScreen(withFocus(withPage(s, SpacePanes, siteScreen, ModeNormal), siteScreen), *rec)
		}
		return core.Next(s)
	}

	fm.Dirty = false
	rf := a.newRecordFormModalWith(kind, nil, about, fm, recordFormOptions{
		hiddenDefaults: hiddenDefaults,
		choices:        choices,
		excludeChoices: exclude,
	})
	a.say("new %s · ctrl+s creates · esc closes", strings.ToLower(schema.Title))
	return core.Next(a.pushModal(s, rf))
}

func (a *App) showingOnPage(s core.State) (core.Ref, bool) {
	if vt := a.viewTabByID(s.ActivePage); vt != nil {
		return vt.currentRef(), true
	}
	if s.ActivePage == SpaceWork {
		return a.workShowing()
	}
	return core.Ref{}, false
}

func (a *App) newHere(s core.State, c core.Command) core.StepResult {
	if s.ActivePage == SpacePanes {
		c.Args = map[string]any{"kind": data.KindPane}
		return a.create(s, c)
	}
	// On a Show page, focus on or inside a related section creates (or
	// attaches) one of that section's kind linked to the shown record.
	if cur, showing := a.showingOnPage(s); showing {
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
	activeField := fm.ActiveFieldName()
	onSubmit := fm.OnSubmitRow()
	schema := a.schema(rf.kind)
	fresh := views.FormForRecordWith(schema, rec, a.editOptionsFor(schema, rec))
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
	fm.Wrap = a.wrapInputs()
	if a.cfg != nil && a.cfg.Settings != nil {
		fm.ShowAllFields = !a.cfg.Settings.Bool(SetFormReactiveFields, true)
	}
	fm.SetDrafts(fresh.Drafts)
	if activeField != "" && fm.FocusField(activeField) {
		return
	}
	if onSubmit {
		fm.Bottom()
	} else {
		fm.Top()
	}
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
