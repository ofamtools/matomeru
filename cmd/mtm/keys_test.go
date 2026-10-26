package main

import (
	"testing"
)

// mtm's key map: q never starts a macro; Q records and stops one; q closes
// the outline and the help modal as esc does.
func TestQClosesAndCapitalQRecordsAMacro(t *testing.T) {
	app := open(t)
	press(t, app, "q")
	if app.r.Mode.Name != "normal" {
		t.Fatalf("q on a page: mode %s", app.r.Mode.Name)
	}
	press(t, app, "Q")
	if app.r.Mode.Name != "macro_pending" {
		t.Fatalf("Q: mode %s", app.r.Mode.Name)
	}
	press(t, app, "a")
	if app.r.Registers().Macro.Recording != "a" {
		t.Fatalf("Qa: recording %q", app.r.Registers().Macro.Recording)
	}
	press(t, app, "Q")
	if app.r.Registers().Macro.Recording != "" {
		t.Fatalf("Q again: still recording %q", app.r.Registers().Macro.Recording)
	}
	press(t, app, "T")
	if app.r.TopModal() != outlineModal {
		t.Fatalf("T: modal %q", app.r.TopModal())
	}
	press(t, app, "q")
	if app.r.TopModal() != "" || app.r.Mode.Name != "normal" {
		t.Fatalf("q in the outline: modal %q mode %s", app.r.TopModal(), app.r.Mode.Name)
	}
	press(t, app, "?")
	if app.r.TopModal() != "bindings-help" {
		t.Fatalf("?: modal %q", app.r.TopModal())
	}
	press(t, app, "q")
	if app.r.TopModal() != "" {
		t.Fatalf("q in the help: modal %q", app.r.TopModal())
	}
}

// L on a project or task row opens the harness chooser with the project's
// harness first, so L enter launches it; l opens the row (UX key map).
func TestLLaunchesThroughTheChooserOnTheProjectsHarness(t *testing.T) {
	app := open(t)
	f := &fakeDaemon{panes: app.panesd.(*fakePanes)}
	app.launcher = f
	setHarness(t, app, "codex")
	if err := app.r.OpenPage("work"); err != nil {
		t.Fatal(err)
	}
	if err := app.r.SetFocus("matomeru"); err != nil {
		t.Fatal(err)
	}
	press(t, app, "L")
	c := app.r.Chooser(agentChooser)
	if c == nil || len(c.Matches()) == 0 || c.Matches()[0].ID != "codex" {
		t.Fatalf("L on the project: chooser %+v", c)
	}
	press(t, app, "esc")
	press(t, app, "l")
	if app.r.Focus == "matomeru" {
		t.Fatalf("l did not open the project (focus %s)", app.r.Focus)
	}
	settle(app, launchKey(t, app))
	if len(f.launches) != 1 || f.launches[0].Session != "matomeru" {
		t.Fatalf("L enter on a task: launches %+v", f.launches)
	}
}

// m moves a task, like a pane: the project chooser, then the pick.
func TestMMovesATaskThroughTheProjectChooser(t *testing.T) {
	app := open(t)
	press(t, app, "j", "enter")
	if app.r.Focus != "t1" {
		t.Fatalf("focus %s", app.r.Focus)
	}
	press(t, app, "m")
	c := app.r.Chooser(moveChooser)
	if c == nil {
		t.Fatalf("m opened %q", app.r.TopModal())
	}
	for _, row := range c.Matches() {
		if row.ID == "matomeru" {
			t.Fatalf("the chooser offers the task's own project: %+v", c.Matches())
		}
	}
	press(t, app, "o", "c", "h", "a", "enter")
	if got := stored(t, app, "t1").ProjectID; got != "ochazuke" {
		t.Fatalf("project after m: %q", got)
	}
}

// keyRow is one normal-mode row: where it is bound, its keys and operation.
type keyRow struct{ at, keys, op string }

// normalRows lists every normal-mode row of the runtime: noun tables and
// instance rows.
func normalRows(a *App) []keyRow {
	var out []keyRow
	for name, noun := range a.r.Nouns {
		for _, b := range noun.Bindings["normal"] {
			if !b.Unbind {
				out = append(out, keyRow{"noun " + name, b.Keys, b.Request.Key()})
			}
		}
	}
	for id, inst := range a.r.Instances {
		for _, b := range inst.Bindings {
			if b.Mode == "normal" && !b.Unbind {
				out = append(out, keyRow{"instance " + id, b.Keys, b.Request.Key()})
			}
		}
	}
	return out
}

// The key map (owner, 2026-10-26) as a check: every verb of a family is
// bound to the family's key wherever it is bound at all, and to no other
// key, across nouns; the retired keys answer nowhere in mtm.
func TestBindingsFollowTheKeyMap(t *testing.T) {
	app := open(t)
	families := map[string][]string{
		"d d": {"pane.kill", "session.kill", "saved.forget", "vim.forget", "project-dir.remove", "project-base.remove", "project.delete", "task.delete", "note.delete", "outline.delete", contextNoun + ".forget"},
		"n":   {"pane.new", "project.new", "task.new", "note.new", "vim.open", "dirs.base.add"},
		"L":   {"agent.choose", "project.launch", "task.launch"},
		"i":   {"project.edit", "task.edit", "note.edit", "project-dir.edit", "review.saveAnswer", "outline.edit"},
		"r":   {"pane.rename", "session.rename", "saved.rename"},
		"x":   {"task.toggle", outlineModal + ".toggle", "review.accept"},
		"s":   {"task.status", outlineModal + ".status", "review.status"},
		"w":   {"saved.save", "vim.save"},
		"m":   {"pane.associate", "session.associate", "task.associate", "note.associate", "saved.associate"},
	}
	want := map[string]string{}
	for k, ops := range families {
		for _, op := range ops {
			want[op] = k
		}
	}
	rows := normalRows(app)
	bound := map[string]bool{}
	for _, row := range rows {
		k, ok := want[row.op]
		if !ok {
			continue
		}
		// pane.killBlocked shares d d; any other key for a family verb is a
		// second meaning.
		if row.keys != k {
			t.Errorf("%s binds %q to %s; the key map says %q", row.at, row.keys, row.op, k)
		}
		bound[row.op] = true
	}
	for op, k := range want {
		if !bound[op] {
			t.Errorf("%s has no %q row", op, k)
		}
	}
	// Retired keys: a and D answer nowhere (the editor noun's Vim rows,
	// inside an input, keep a as append); q is a close key (the record
	// editor, ochazuke's form, the outline, modals) and never a macro;
	// refresh has no key.
	closers := map[string]bool{copyNoun + ".leave": true, "record-editor.leave": true, "form.leave": true, outlineModal + ".close": true, quickNoteModal + ".close": true, "modal.pop": true}
	for _, row := range rows {
		switch {
		case row.at == "noun editor":
		case row.keys == "a" || row.keys == "D":
			t.Errorf("%s still binds %q (%s)", row.at, row.keys, row.op)
		case row.keys == "q" && !closers[row.op]:
			t.Errorf("%s binds q to %s, not a close", row.at, row.op)
		case row.op == "saved.list" || row.op == "vim.list":
			t.Errorf("%s binds %q to %s; refresh is automatic", row.at, row.keys, row.op)
		}
	}
}

// UX review 2 F22: P inside a modal (the help) does not stack the pin
// picker over it; on a page it opens it.
func TestPDoesNotStackThePickerOverAModal(t *testing.T) {
	app := open(t)
	press(t, app, "?", "P")
	if app.r.TopModal() != "bindings-help" {
		t.Fatalf("P in the help: top modal %q", app.r.TopModal())
	}
	press(t, app, "esc", "P")
	if app.r.TopModal() != "pin-picker" {
		t.Fatalf("P on the page: top modal %q", app.r.TopModal())
	}
}

// UX review 2 F22: an agent launched on a project is named after its
// harness; the session already names the project.
func TestAgentPanesAreNamedAfterTheHarness(t *testing.T) {
	app := open(t)
	f := &fakeDaemon{panes: app.panesd.(*fakePanes)}
	app.launcher = f
	if err := app.r.OpenPage("work"); err != nil {
		t.Fatal(err)
	}
	if err := app.r.SetFocus("matomeru"); err != nil {
		t.Fatal(err)
	}
	settle(app, launchKey(t, app))
	if len(f.launches) != 1 || f.launches[0].Name != defaultHarness || f.launches[0].Session != "matomeru" {
		t.Fatalf("launches %+v", f.launches)
	}
}
