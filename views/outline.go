package views

import (
	"strconv"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/ui"
)

func itoa(i int) string { return strconv.Itoa(i) }

// count is "1 task" or "3 tasks".
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return itoa(n) + " " + noun + "s"
}

// OutlineView renders the recursive task outline as one tree with guide
// lines: root tasks (projects) at the top level, each task with its schema
// status icon and right-aligned metadata columns. The tree node is id+".tree";
// it carries the selected task's Scope and Ref, so the selected row is the
// subject of the context chain. An unknown or empty selectedID selects the
// first row.
func OutlineView(nodes []data.TaskNode, selectedID string, id string) ui.ViewNode {
	kind := "task"
	if len(nodes) > 0 && nodes[0].Record.Kind != "" {
		kind = nodes[0].Record.Kind
	}
	schema, _ := data.DefaultSchemas().Get(kind)
	tm := TreeFromNodes(schema, nodes, DefaultTreeOptions())
	tm.Select(selectedID)
	panel := TreeView(schema, tm, id, id+".tree", ScopeOutline)
	panel.Title = "Outline"
	return panel
}
