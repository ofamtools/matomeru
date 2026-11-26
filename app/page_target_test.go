package app

import (
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/widgets/tree"
)

// Focus on the project nav rail beside a table: the preset verbs act on the
// page's table (they used to say "no table is focused").
func TestPresetVerbOnNavRailActsOnPageTable(t *testing.T) {
	a, d := openProjectTabOnNav(t, "pages")
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdTablePresetPick, Actor: User}})
	if a.Modal() != "table.presets" {
		t.Fatalf("g v on the nav rail: modal %q (status %q), want the presets picker", a.Modal(), a.Status())
	}
	if pm := a.activePicker("table.presets"); pm == nil || pm.View(0, 0).Facts["table"] != projectPagesTable {
		t.Fatalf("presets picker is not for %s", projectPagesTable)
	}
}

// Focus on the project nav rail beside a table: visual select starts in the
// page's table and focus moves to it, so j/k extend the selection.
func TestSelectVerbOnNavRailFocusesPageTable(t *testing.T) {
	a, d := openProjectTabOnNav(t, "pages")
	d.Send(core.CommandEvent{Command: core.Command{ID: traits.SelectCmdVisualToggle, Actor: User}})
	tbl := a.nav.tables[projectPagesTable]
	if a.Focus() != projectPagesTable || tbl == nil || !tbl.VisualActive {
		t.Fatalf("v on the nav rail: focus %q, want %q with visual active", a.Focus(), projectPagesTable)
	}
}

// Focus on the project nav rail beside the task outline: the tree verbs act
// on the page's tree (they used to say "no tree is focused"), and inline
// edit moves focus into it.
func TestTreeVerbOnNavRailActsOnPageTree(t *testing.T) {
	a, d := openProjectTabOnNav(t, "tasks")
	child := titled(t, a, data.KindTask, "Child")
	if _, _, err := a.Store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Grandchild", "parent": child.Ref()}}, User); err != nil {
		t.Fatal(err)
	}
	_ = d.Frame()
	if a.viewMemo.PageTreeID != projectOutlineTree {
		t.Fatalf("PageTreeID = %q, want %q", a.viewMemo.PageTreeID, projectOutlineTree)
	}
	d.Send(core.CommandEvent{Command: core.Command{ID: tree.CmdFoldCloseAll, Actor: User}})
	tm := a.trees.projectModel(a.activeProjectOutlineID(a.State()))
	if !tm.Folded[child.ID] {
		t.Fatalf("fold all on the nav rail did not fold the outline (status %q)", a.Status())
	}
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdOutlineAddBelow, Actor: User}})
	if a.Focus() != projectOutlineTree || a.Engine.Mode(a.State()) != ModeOutlineEdit {
		t.Fatalf("add below on the nav rail: focus %q mode %q, want the outline in edit mode", a.Focus(), a.Engine.Mode(a.State()))
	}
}

// Focus on the panes rail beside the pane site: terminal.attach types in the
// page's terminal (it used to say "no terminal is focused").
func TestTerminalAttachFromRailUsesPageTerminal(t *testing.T) {
	a, d := demo(t)
	d.Keys("1")
	_ = d.Frame()
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdTermAttach, Actor: User}})
	if a.Engine.Mode(a.State()) != ModeTerminal {
		t.Fatalf("terminal.attach on the rail: mode %q (status %q), want terminal", a.Engine.Mode(a.State()), a.Status())
	}
}

// A page with no list, tree or terminal (the project Home section): the
// verbs do nothing, say nothing, and their default keys are hidden.
func TestPageVerbsWithNoTargetAreSilent(t *testing.T) {
	a, d := openProjectTabOnNav(t, "home")
	if a.viewMemo.PageListID != "" || a.viewMemo.PageTreeID != "" {
		t.Fatalf("home draws list %q tree %q, want none", a.viewMemo.PageListID, a.viewMemo.PageTreeID)
	}
	for _, b := range a.activeKeyBindings(a.State()) {
		switch b.command {
		case CmdTablePresetPick, traits.SelectCmdMarkToggle, traits.SelectCmdVisualToggle, traits.SelectCmdVisualLine:
			t.Errorf("%s is active on %q with nothing on the page", b.command, b.keys)
		}
	}
	for _, id := range []string{CmdTablePresetPick, CmdTablePresetSave, traits.SelectCmdVisualToggle, CmdMarkToggle,
		CmdTaskFoldToggle, tree.CmdHoistIn, CmdOutlineEditStart, CmdOutlineAddBelow, CmdOutlineCleanSlateExit, CmdTermAttach} {
		before, mode, focus := a.Status(), a.Engine.Mode(a.State()), a.Focus()
		d.Send(core.CommandEvent{Command: core.Command{ID: id, Actor: User}})
		if a.Status() != before || a.Modal() != "" || a.Engine.Mode(a.State()) != mode || a.Focus() != focus {
			t.Errorf("%s with nothing on the page: status %q modal %q mode %q focus %q, want unchanged", id, a.Status(), a.Modal(), a.Engine.Mode(a.State()), a.Focus())
		}
	}
}
