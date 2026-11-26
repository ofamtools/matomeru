package data

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke/core"
)

func TestRecordTreeLanesAndTasks(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	actor := core.Actor{Kind: "user", ID: "owner"}

	// Lanes: parentLane hierarchy with no orderField (preserves creation order).
	rootLane, _, err := store.Create(CreateInput{
		Kind:   KindLane,
		ID:     "l-root",
		Fields: map[string]any{"title": "Root Lane", "laneStatus": "running"},
	}, actor)
	if err != nil {
		t.Fatal(err)
	}
	childA, _, err := store.Create(CreateInput{
		Kind:   KindLane,
		ID:     "l-a",
		Fields: map[string]any{"title": "Child A", "parentLane": rootLane.Ref(), "laneStatus": "planned"},
	}, actor)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Create(CreateInput{
		Kind:   KindLane,
		ID:     "l-b",
		Fields: map[string]any{"title": "Child B", "parentLane": rootLane.Ref(), "laneStatus": "done"},
	}, actor)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Create(CreateInput{
		Kind:   KindLane,
		ID:     "l-grand",
		Fields: map[string]any{"title": "Grandchild", "parentLane": childA.Ref(), "laneStatus": "review"},
	}, actor)
	if err != nil {
		t.Fatal(err)
	}

	laneNodes, err := store.RecordTree(KindLane, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(laneNodes) != 1 || laneNodes[0].Record.ID != "l-root" || !laneNodes[0].IsRoot() {
		t.Fatalf("lane roots = %+v, want single l-root", laneNodes)
	}
	if len(laneNodes[0].Children) != 2 || laneNodes[0].Children[0].Record.ID != "l-a" || laneNodes[0].Children[1].Record.ID != "l-b" {
		t.Fatalf("lane children = %+v, want [l-a, l-b] in creation order", laneNodes[0].Children)
	}
	if laneNodes[0].Children[0].IsRoot() || laneNodes[0].Children[0].Depth != 1 {
		t.Fatalf("l-a depth/root = %d/%v", laneNodes[0].Children[0].Depth, laneNodes[0].Children[0].IsRoot())
	}
	if kids := laneNodes[0].Children[0].Children; len(kids) != 1 || kids[0].Record.ID != "l-grand" || kids[0].Depth != 2 {
		t.Fatalf("l-grand = %+v, want depth 2 under l-a", kids)
	}

	// Tasks: parent + numeric order field sorts siblings ascending.
	proj, _, _ := store.Create(CreateInput{
		Kind:   KindTask,
		ID:     "t-proj",
		Fields: map[string]any{"title": "Proj", "order": int64(2)},
	}, actor)
	_, _, _ = store.Create(CreateInput{
		Kind:   KindTask,
		ID:     "t-first",
		Fields: map[string]any{"title": "First Root", "order": int64(1)},
	}, actor)
	_, _, _ = store.Create(CreateInput{
		Kind:   KindTask,
		ID:     "t-sub-late",
		Fields: map[string]any{"title": "Sub Late", "parent": proj.Ref(), "order": int64(10)},
	}, actor)
	_, _, _ = store.Create(CreateInput{
		Kind:   KindTask,
		ID:     "t-sub-early",
		Fields: map[string]any{"title": "Sub Early", "parent": proj.Ref(), "order": int64(3)},
	}, actor)

	taskNodes, err := store.TaskTree(false)
	if err != nil {
		t.Fatal(err)
	}
	if len(taskNodes) != 2 || taskNodes[0].Record.ID != "t-first" || taskNodes[1].Record.ID != "t-proj" || !taskNodes[1].IsProject() {
		t.Fatalf("task roots = %+v, want [t-first, t-proj]", taskNodes)
	}
	if subs := taskNodes[1].Children; len(subs) != 2 || subs[0].Record.ID != "t-sub-early" || subs[1].Record.ID != "t-sub-late" {
		t.Fatalf("task children = %+v, want [t-sub-early, t-sub-late]", subs)
	}
}

func TestBuildTreeCycleSelfLoopOrphanAndArchivedSubtree(t *testing.T) {
	ref := func(id string) core.Ref { return core.Ref{Kind: KindLane, ID: id} }
	mk := func(id string, parent *core.Ref, archived bool) Record {
		fields := map[string]any{"title": id}
		if parent != nil {
			fields["parentLane"] = *parent
		}
		arch := ArchiveActive
		if archived {
			arch = ArchiveArchived
		}
		return Record{Kind: KindLane, ID: id, Fields: fields, ArchiveStatus: arch, StarStatus: StarUnstarred}
	}

	pSelf := ref("self")
	pMissing := ref("does-not-exist")
	pCycA := ref("cycA")
	pCycB := ref("cycB")
	pArch := ref("archParent")
	pArchChild := ref("archChild")

	recs := []Record{
		mk("self", &pSelf, false),
		mk("orphan", &pMissing, false),
		mk("cycA", &pCycB, false),
		mk("cycB", &pCycA, false),
		mk("archParent", nil, true),
		mk("archChild", &pArch, false),
		mk("archGrand", &pArchChild, false),
	}

	activeOnly := BuildTree(recs, "parentLane", "", false)
	seen := map[string]int{}
	var walk func(ns []TreeNode)
	walk = func(ns []TreeNode) {
		for _, n := range ns {
			seen[n.Record.ID] = n.Depth
			walk(n.Children)
		}
	}
	walk(activeOnly)

	if seen["self"] != 0 {
		t.Errorf("self-loop should surface as root, got depth %d (seen=%v)", seen["self"], seen)
	}
	if seen["orphan"] != 0 {
		t.Errorf("orphan should surface as root, got depth %d (seen=%v)", seen["orphan"], seen)
	}
	if _, hasA := seen["cycA"]; !hasA {
		t.Errorf("cycle node cycA lost: seen=%v", seen)
	}
	if _, hasB := seen["cycB"]; !hasB {
		t.Errorf("cycle node cycB lost: seen=%v", seen)
	}
	if _, hasArch := seen["archParent"]; hasArch {
		t.Errorf("archived parent should be omitted when includeArchived=false")
	}
	if _, hasChild := seen["archChild"]; hasChild {
		t.Errorf("active child of archived parent should be suppressed when includeArchived=false")
	}
	if _, hasGrand := seen["archGrand"]; hasGrand {
		t.Errorf("active grandchild of archived parent should be suppressed when includeArchived=false")
	}

	withArchived := BuildTree(recs, "parentLane", "", true)
	seenAll := map[string]int{}
	walk = func(ns []TreeNode) {
		for _, n := range ns {
			seenAll[n.Record.ID] = n.Depth
			walk(n.Children)
		}
	}
	walk(withArchived)
	if seenAll["archParent"] != 0 || seenAll["archChild"] != 1 || seenAll["archGrand"] != 2 {
		t.Fatalf("includeArchived=true expected full archived subtree, got %v", seenAll)
	}
}

func TestRecordTreeCustomSchemaInferredHierarchy(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	actor := core.Actor{Kind: "user", ID: "owner"}

	// Register a custom schema with NO explicit Hierarchy, only a self-referencing ParamRef field.
	store.Registry().Register(Schema{
		Kind:         "custom.node",
		Title:        "Node",
		Plural:       "Nodes",
		SummaryField: "name",
		Fields: []FieldSpec{
			{Name: "name", Type: core.ParamString, Required: true},
			{Name: "up", Type: core.ParamRef, RefKinds: []string{"custom.node"}},
		},
	})
	root, _, err := store.Create(CreateInput{
		Kind:   "custom.node",
		ID:     "n1",
		Fields: map[string]any{"name": "Top"},
	}, actor)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Create(CreateInput{
		Kind:   "custom.node",
		ID:     "n2",
		Fields: map[string]any{"name": "Nested", "up": root.Ref()},
	}, actor)
	if err != nil {
		t.Fatal(err)
	}

	nodes, err := store.RecordTree("custom.node", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Record.ID != "n1" || len(nodes[0].Children) != 1 || nodes[0].Children[0].Record.ID != "n2" {
		t.Fatalf("custom inferred hierarchy tree = %+v", nodes)
	}
}

// A list that hides archived work drops the archived record and everything
// under it; keep leaves one archived record in, but never its subtree.
func TestWithoutArchivedSubtrees(t *testing.T) {
	rec := func(id, parent string, archived bool) Record {
		r := Record{Kind: KindLane, ID: id, Fields: map[string]any{}}
		if parent != "" {
			r.Fields["parentLane"] = core.Ref{Kind: KindLane, ID: parent}
		}
		if archived {
			r.ArchiveStatus = ArchiveArchived
		}
		return r
	}
	recs := []Record{rec("live", "", false), rec("gone", "", true), rec("child", "gone", false), rec("grandchild", "child", false)}
	ids := func(in []Record) string {
		var out []string
		for _, r := range in {
			out = append(out, r.ID)
		}
		return strings.Join(out, ",")
	}
	if got := ids(WithoutArchivedSubtrees(recs, "parentLane", nil)); got != "live" {
		t.Fatalf("active records = %q, want only live", got)
	}
	keepGone := func(r Record) bool { return r.ID == "gone" }
	if got := ids(WithoutArchivedSubtrees(recs, "parentLane", keepGone)); got != "live,gone" {
		t.Fatalf("with a kept archived record = %q, want live,gone", got)
	}
	if got := ids(WithoutArchivedSubtrees(recs, "", nil)); got != "live,child,grandchild" {
		t.Fatalf("flat kind = %q, want live,child,grandchild", got)
	}
}
