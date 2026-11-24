package views

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func TestTreeFromNodesLanesAllStatusesAndResolveRef(t *testing.T) {
	reg := data.DefaultSchemas()
	laneSchema, ok := reg.Get(data.KindLane)
	if !ok {
		t.Fatal("missing lane schema")
	}
	taskRef := core.Ref{Kind: data.KindTask, ID: "t-main"}

	cases := []struct {
		status   string
		rowTone  string
		cellTone string
		icon     string
		iconTone string
	}{
		{"planned", "row.muted", "state.planned", "○", "state.planned"},
		{"running", "row.active", "state.working", "▶", "state.working"},
		{"review", "row.attention", "state.review", "◉", "state.review"},
		{"merged", "row.dim", "state.done.dim", "✓", "state.done.dim"},
		{"done", "row.dim", "state.done.dim", "✓", "state.done.dim"},
		{"parked", "row.muted", "muted", "⏸", "muted"},
		{"cancelled", "row.cancelled", "state.cancelled", "✕", "state.cancelled"},
	}

	var children []data.TreeNode
	for _, tc := range cases {
		children = append(children, data.TreeNode{
			Depth: 1,
			Record: data.Record{
				Kind: data.KindLane,
				ID:   "l-" + tc.status,
				Fields: map[string]any{
					"title":      "Lane " + tc.status,
					"laneStatus": tc.status,
					"branch":     "feat/" + tc.status,
					"task":       taskRef,
				},
				ArchiveStatus: data.ArchiveActive,
				StarStatus:    data.StarUnstarred,
			},
		})
	}
	root := data.TreeNode{
		Depth: 0,
		Record: data.Record{
			Kind: data.KindLane,
			ID:   "l-root",
			Fields: map[string]any{
				"title":      "Wave A1",
				"laneStatus": "running",
				"branch":     "main",
				"task":       taskRef,
			},
			ArchiveStatus: data.ArchiveActive,
			StarStatus:    data.StarStarred,
		},
		Children: children,
	}

	opts := DefaultTreeOptions()
	opts.ResolveRef = func(ref core.Ref) string {
		if ref == taskRef {
			return "Matomeru v2"
		}
		return ""
	}
	tm := TreeFromNodes(laneSchema, []data.TreeNode{root}, opts)
	if len(tm.Nodes) != 1 || len(tm.Nodes[0].Children) != len(cases) {
		t.Fatalf("tree nodes = %+v", tm.Nodes)
	}
	for i, tc := range cases {
		nd := tm.Nodes[0].Children[i]
		if nd.Tone != tc.rowTone || nd.Icon != tc.icon || nd.IconTone != tc.iconTone {
			t.Errorf("lane %s: got tone=%q icon=%q iconTone=%q, want tone=%q icon=%q iconTone=%q",
				tc.status, nd.Tone, nd.Icon, nd.IconTone, tc.rowTone, tc.icon, tc.iconTone)
		}
		// Meta should contain status (with semantic cell tone), branch, and resolved task title.
		wantMeta := []ui.TreeMeta{
			{Text: tc.status, Tone: tc.cellTone},
			{Text: "feat/" + tc.status, Tone: "muted"},
			{Text: "Matomeru v2", Tone: "muted"},
		}
		if len(nd.Meta) != len(wantMeta) {
			t.Errorf("lane %s meta = %+v, want %+v", tc.status, nd.Meta, wantMeta)
			continue
		}
		for j := range wantMeta {
			if nd.Meta[j] != wantMeta[j] {
				t.Errorf("lane %s meta[%d] = %+v, want %+v", tc.status, j, nd.Meta[j], wantMeta[j])
			}
		}
	}

	// Render full panel and verify visual output includes icons, labels, branch, and resolved task.
	panel := TreeView(laneSchema, tm, "lanes", "lanes.tree", ScopeIndex)
	out := frame(t, panel, "lanes.tree", 110, 16)
	wantText(t, out, "Lanes", "8 lanes", "▶ Wave A1", "main", "Matomeru v2", "★", "Lane running", "Lane cancelled", "feat/review")
}

func TestTreeFromNodesTasksOptOutsAndGapTopLevel(t *testing.T) {
	reg := data.DefaultSchemas()
	taskSchema, _ := reg.Get(data.KindTask)

	nodes := []data.TreeNode{
		{
			Depth: 0,
			Record: data.Record{
				Kind: data.KindTask,
				ID:   "t1",
				Fields: map[string]any{
					"title":      "Root One",
					"workStatus": "active",
					"priority":   "P0",
				},
				ArchiveStatus: data.ArchiveActive,
				StarStatus:    data.StarStarred,
			},
		},
		{
			Depth: 0,
			Record: data.Record{
				Kind: data.KindTask,
				ID:   "t2",
				Fields: map[string]any{
					"title":      "Root Two",
					"workStatus": "todo",
				},
				ArchiveStatus: data.ArchiveActive,
				StarStatus:    data.StarUnstarred,
			},
		},
	}

	// Default options: project root meta, P0 error tone, facts["project"] == true, GapTopLevel == true.
	tm := TreeFromNodes(taskSchema, nodes, DefaultTreeOptions())
	n0 := tm.Nodes[0]
	if n0.Facts["project"] != true {
		t.Fatalf("root task facts[project] = %v, want true", n0.Facts["project"])
	}
	hasProj, hasP0Err, hasStar := false, false, false
	for _, m := range n0.Meta {
		if m.Text == "project" && m.Tone == "muted" {
			hasProj = true
		}
		if m.Text == "P0" && m.Tone == "error" {
			hasP0Err = true
		}
		if m.Text == "★" && m.Tone == "icon.star" {
			hasStar = true
		}
	}
	if !hasProj || !hasP0Err || !hasStar {
		t.Fatalf("root task meta = %+v, want project/P0(error)/★(icon.star)", n0.Meta)
	}

	// GapTopLevel=true inserts a blank spacer line between Root One and Root Two.
	panelGap := TreeView(taskSchema, tm, "w", "w.tree", ScopeIndex)
	outGap := frame(t, panelGap, "w.tree", 90, 12)
	linesGap := strings.Split(outGap, "\n")
	r1, r2 := -1, -1
	for i, ln := range linesGap {
		if strings.Contains(ln, "Root One") {
			r1 = i
		}
		if strings.Contains(ln, "Root Two") {
			r2 = i
		}
	}
	if r1 < 0 || r2 != r1+2 {
		t.Fatalf("GapTopLevel=true expected blank line between Root One (%d) and Root Two (%d):\n%s", r1, r2, outGap)
	}

	// Opt-outs: RowSemanticStyling=false, StatusIcons=false, GapTopLevel=false.
	offOpts := TreeOptions{
		Style:         StyleOptions{RowSemanticStyling: false, StatusIcons: false},
		GapTopLevel:   false,
		Guides:        true,
		ShowFoldCount: true,
	}
	tmOff := TreeFromNodes(taskSchema, nodes, offOpts)
	if tmOff.Nodes[0].Tone != "" || tmOff.Nodes[0].Icon != "" || tmOff.Nodes[0].IconTone != "" {
		t.Fatalf("StyleOptions opt-out leaked style: %+v", tmOff.Nodes[0])
	}
	for _, m := range tmOff.Nodes[0].Meta {
		if m.Text == "active" && m.Tone != "" {
			t.Fatalf("status meta tone should be empty when RowSemanticStyling=false: %+v", m)
		}
		if m.Text == "★" && m.Tone != "" {
			t.Fatalf("star meta tone should be empty when RowSemanticStyling=false: %+v", m)
		}
	}
	panelNoGap := TreeView(taskSchema, tmOff, "w", "w.tree", ScopeIndex)
	outNoGap := frame(t, panelNoGap, "w.tree", 90, 12)
	linesNoGap := strings.Split(outNoGap, "\n")
	r1, r2 = -1, -1
	for i, ln := range linesNoGap {
		if strings.Contains(ln, "Root One") {
			r1 = i
		}
		if strings.Contains(ln, "Root Two") {
			r2 = i
		}
	}
	if r1 < 0 || r2 != r1+1 {
		t.Fatalf("GapTopLevel=false expected adjacent Root One (%d) and Root Two (%d):\n%s", r1, r2, outNoGap)
	}
}

func TestTreeViewHoistedBreadcrumbAndFacts(t *testing.T) {
	reg := data.DefaultSchemas()
	taskSchema, _ := reg.Get(data.KindTask)

	nodes := []data.TreeNode{
		{
			Depth: 0,
			Record: data.Record{
				Kind:          data.KindTask,
				ID:            "t-root",
				Fields:        map[string]any{"title": "Alpha Project", "workStatus": "active"},
				ArchiveStatus: data.ArchiveActive,
			},
			Children: []data.TreeNode{
				{
					Depth: 1,
					Record: data.Record{
						Kind:          data.KindTask,
						ID:            "t-child",
						Fields:        map[string]any{"title": "Brain Dump", "workStatus": "todo"},
						ArchiveStatus: data.ArchiveActive,
					},
					Children: []data.TreeNode{
						{
							Depth: 2,
							Record: data.Record{
								Kind:          data.KindTask,
								ID:            "t-leaf",
								Fields:        map[string]any{"title": "First Bullet", "workStatus": "todo"},
								ArchiveStatus: data.ArchiveActive,
							},
						},
					},
				},
			},
		},
	}

	tm := TreeFromNodes(taskSchema, nodes, DefaultTreeOptions())
	unhoisted := TreeView(taskSchema, tm, "w", "w.tree", ScopeIndex)
	if unhoisted.Facts["hoisted"] != false || unhoisted.Facts["hoistDepth"] != 0 {
		t.Fatalf("unhoisted panel facts = %+v, want hoisted=false hoistDepth=0", unhoisted.Facts)
	}
	for _, b := range unhoisted.Badges {
		if strings.HasPrefix(b.Text, "hoist:") {
			t.Fatalf("unexpected hoist badge when unhoisted: %+v", unhoisted.Badges)
		}
	}

	if !tm.Hoist("t-child") {
		t.Fatal("Hoist(t-child) returned false")
	}
	hoisted := TreeView(taskSchema, tm, "w", "w.tree", ScopeIndex)
	if hoisted.Facts["hoisted"] != true || hoisted.Facts["hoistDepth"] != 1 {
		t.Fatalf("hoisted panel facts = %+v, want hoisted=true hoistDepth=1", hoisted.Facts)
	}
	wantBadge := ui.Badge{Text: "hoist: Alpha Project › Brain Dump", Tone: "accent"}
	foundBadge := false
	for _, b := range hoisted.Badges {
		if b == wantBadge {
			foundBadge = true
			break
		}
	}
	if !foundBadge {
		t.Fatalf("hoisted panel badges = %+v, want %+v", hoisted.Badges, wantBadge)
	}
	out := frame(t, hoisted, "w.tree", 100, 12)
	wantText(t, out, "hoist: Alpha Project › Brain Dump", "Brain Dump", "First Bullet")
}
