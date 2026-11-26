package app

import (
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/table"
)

func openProjectTabOnNav(t *testing.T, section string) (*App, *Driver) {
	t.Helper()
	a, d := demo(t)
	proj, _, err := a.Store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "List Target Project"}}, User)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Child", "parent": proj.Ref()}}, User); err != nil {
		t.Fatal(err)
	}
	pc := a.projectCtrl()
	pc.openTabs = append(pc.openTabs, proj.ID)
	pc.setSection(proj.ID, section)
	d.Keys("6")
	a.Shell.State = withFocus(a.State(), projectNavPrefix+section)
	_ = d.Frame()
	return a, d
}

// Focus on the project nav rail beside a table: the list verbs act on the
// page's table.
func TestListVerbsOnNavRailActOnPageTable(t *testing.T) {
	a, d := openProjectTabOnNav(t, "pages")
	if a.viewMemo.PageListID != projectPagesTable {
		t.Fatalf("PageListID = %q, want %q", a.viewMemo.PageListID, projectPagesTable)
	}
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdSortCycle, Actor: User}})
	tbl := a.nav.tables[projectPagesTable]
	if tbl == nil || tbl.SortDir == table.SortNone {
		t.Fatalf("sort.cycle on the nav rail did not sort the pages table (status %q)", a.Status())
	}
}

// Focus on the project nav rail beside the task outline (no table): the list
// keys are hidden and a direct call does nothing, with no status message.
func TestListVerbsWithNoPageTableAreSilent(t *testing.T) {
	a, d := openProjectTabOnNav(t, "tasks")
	if a.viewMemo.PageListID != "" {
		t.Fatalf("PageListID = %q, want none beside the outline", a.viewMemo.PageListID)
	}
	for _, b := range a.activeKeyBindings(a.State()) {
		switch b.command {
		case CmdFilterFieldPick, CmdSortCycle, CmdSortField, CmdSortFieldPick, CmdGroupFieldPick, CmdZeroFieldOpen:
			t.Errorf("%s is active on %q with no list on the page", b.command, b.keys)
		}
	}
	for _, id := range []string{CmdFilterFieldPick, CmdSortCycle, CmdGroupToggle, CmdZeroFieldOpen} {
		before := a.Status()
		d.Send(core.CommandEvent{Command: core.Command{ID: id, Actor: User}})
		if a.Status() != before || a.Modal() != "" {
			t.Errorf("%s with no list: status %q modal %q, want unchanged and none", id, a.Status(), a.Modal())
		}
	}
}
