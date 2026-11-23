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
// status icon and a badge of its status and priority. The tree node is
// id+".tree"; it carries the selected task's Scope and Ref, so the selected
// row is the subject of the context chain. An unknown or empty selectedID
// selects the first row.
func OutlineView(nodes []data.TaskNode, selectedID string, id string) ui.ViewNode {
	kind := "task"
	if len(nodes) > 0 && nodes[0].Record.Kind != "" {
		kind = nodes[0].Record.Kind
	}
	schema, _ := data.DefaultSchemas().Get(kind)

	var flat []data.TaskNode
	selected := 0
	var build func(ns []data.TaskNode) []any
	build = func(ns []data.TaskNode) []any {
		out := make([]any, 0, len(ns))
		for _, n := range ns {
			if n.Record.ID == selectedID {
				selected = len(flat)
			}
			flat = append(flat, n)
			status := n.Record.Text("workStatus")
			icon, _, tone, _ := StatusIndicator(schema, "workStatus", status)
			if icon == "" {
				icon = "○"
			}
			item := map[string]any{"label": icon + " " + Title(schema, n.Record)}
			badge := status
			if tone != "" {
				item["tone"] = tone
			}
			if p := n.Record.Text("priority"); p != "" {
				badge += " " + p
			}
			if n.IsProject() {
				badge = "project · " + badge
			}
			if n.Record.IsStarred() {
				badge += " ★"
			}
			if n.Record.IsArchived() {
				badge += " archived"
				item["tone"] = "dim"
			}
			item["badge"] = badge
			if len(n.Children) > 0 {
				item["children"] = build(n.Children)
			}
			out = append(out, item)
		}
		return out
	}
	items := build(nodes)

	tree := ui.ViewNode{ID: id + ".tree", Kind: ui.KindTree, Scope: kind, Focusable: true,
		Props: map[string]any{"nodes": items, "guides": true, "selected": selected}}
	if len(flat) > 0 {
		cur := flat[selected]
		ref := cur.Record.Ref()
		tree.Ref = &ref
		tree.Facts = statusFacts(schema, cur.Record)
		tree.Facts["depth"] = cur.Depth
		tree.Facts["project"] = cur.IsProject()
		tree.Facts["hasChildren"] = len(cur.Children) > 0
	}
	return ui.ViewNode{ID: id, Kind: ui.KindPanel, Scope: ScopeOutline, Title: "Outline", Border: true,
		Badges:   []ui.Badge{{Text: count(len(nodes), "project")}, {Text: count(len(flat), "task")}},
		Facts:    map[string]any{"rowCount": len(flat)},
		Children: []ui.ViewNode{tree}}
}
