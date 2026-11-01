package main

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// outlineProps opens the all-view outline over the seeded projects plus a
// done task of matomeru.
func outlineProps(t *testing.T) *App {
	t.Helper()
	a := openAt(t, seededWith(t, map[string]string{"shipped": work.StatusDone}))
	press(t, a, "T")
	if a.r.TopModal() != outlineModal || !a.outline.scope.all {
		t.Fatalf("T opened %q, scope %+v", a.r.TopModal(), a.outline.scope)
	}
	return a
}

// outlineRow is the outline's node by id.
func outlineRow(t *testing.T, a *App, id string) ochazuke.TreeNode {
	t.Helper()
	for _, n := range a.r.Tree(outlineTree).Nodes() {
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("no outline row %s", id)
	return ochazuke.TreeNode{}
}

// Outline rows carry their record's fields as props: a task row the
// task's (taskThing), a project row the project's (projectThing), the
// no-project row none.
func TestOutlineRowsCarryRecordProps(t *testing.T) {
	t.Parallel()
	a := outlineProps(t)
	task := outlineRow(t, a, outlineNode("shipped")).Props
	if task["status"] != work.StatusDone || task["project"] != "matomeru" || task["title"] != "shipped task" || task["made-by"] == nil {
		t.Fatalf("task row props %v", task)
	}
	project := outlineRow(t, a, projectNode("ochazuke")).Props
	if project["name"] != "ochazuke" || project["id"] != "ochazuke" || project["glyph"] != "◆" {
		t.Fatalf("project row props %v", project)
	}
	if none := outlineRow(t, a, projectNode("")).Props; len(none) != 0 {
		t.Fatalf("no-project row props %v", none)
	}
}

// A prop filter status=done on the outline keeps the done task (under
// its project) and drops the other tasks; name filters project rows.
func TestOutlinePropFilterStatus(t *testing.T) {
	t.Parallel()
	a := outlineProps(t)
	list := a.r.Tree(outlineTree).List()
	if err := list.SetPropFilters([]ochazuke.PropFilter{{Prop: "status", Op: "=", Value: work.StatusDone}}); err != nil {
		t.Fatal(err)
	}
	f := frame(a)
	if !strings.Contains(f, "shipped task") || strings.Contains(f, "write the brief") || strings.Contains(f, "lift the store") {
		t.Fatalf("status=done outline:\n%s", f)
	}
	if err := list.SetPropFilters([]ochazuke.PropFilter{{Prop: "name", Op: "=", Value: "ochazuke"}}); err != nil {
		t.Fatalf("name is not a filterable prop of project rows: %v", err)
	}
}
