package app

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/tree"
)

func findTreeNodeByID(nodes []tree.Node, id string) (tree.Node, bool) {
	for _, nd := range nodes {
		if nd.ID == id {
			return nd, true
		}
		if found, ok := findTreeNodeByID(nd.Children, id); ok {
			return found, true
		}
	}
	return tree.Node{}, false
}

func TestFinalViewsTasksHierarchyContextAncestorsAndOptOut(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	seed := core.Actor{Kind: "system", ID: "seed"}

	// Done root project with an active child AND a done leaf.
	doneProj, _, _ := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Shipped Release", "workStatus": "done", "order": int64(1)},
	}, seed)
	activeChild, _, _ := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Post-Release Hotfix", "parent": doneProj.Ref(), "workStatus": "active", "order": int64(1)},
	}, seed)
	doneLeaf, _, _ := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Old Milestone", "parent": doneProj.Ref(), "workStatus": "done", "order": int64(2)},
	}, seed)

	// Active root project with a done child AND a todo leaf.
	activeProj, _, _ := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Active Roadmap", "workStatus": "active", "order": int64(2)},
	}, seed)
	doneChild, _, _ := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Completed Spec", "parent": activeProj.Ref(), "workStatus": "done", "order": int64(1)},
	}, seed)
	todoLeaf, _, _ := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Next Backlog Item", "parent": activeProj.Ref(), "workStatus": "todo", "order": int64(2)},
	}, seed)

	a, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	a.cfg.userSettings[SetOutlineStartCollapsed] = false
	a.reloadSettings()
	a.applySettings()

	d := NewDriver(a, 150, 36)
	d.Keys("T")

	// 1. Cycle to hide-final via ).
	d.Keys(")")
	if got := a.outlineViewMode(outlineTree); got != OutlineViewHideFinal {
		t.Fatalf("after ), mode = %q, want %q", got, OutlineViewHideFinal)
	}
	hideFrame := d.Frame()
	if !strings.Contains(hideFrame, "view: hide-final") || !strings.Contains(hideFrame, "3 matched") {
		t.Fatalf("hide-final header missing view/matched badges:\n%s", hideFrame)
	}
	if strings.Contains(hideFrame, "Old Milestone") || strings.Contains(hideFrame, "Completed Spec") {
		t.Fatalf("hide-final leaked final leaves:\n%s", hideFrame)
	}
	if !strings.Contains(hideFrame, "Shipped Release") || !strings.Contains(hideFrame, "Post-Release Hotfix") {
		t.Fatalf("hide-final lost done parent / active child hierarchy:\n%s", hideFrame)
	}

	tm := a.trees.outlineModel()
	ancNode, ok := findTreeNodeByID(tm.Nodes, doneProj.ID)
	if !ok {
		t.Fatalf("missing context ancestor %q in hide-final tree", doneProj.ID)
	}
	if ancNode.Facts[views.FactFilterContextOnly] != true || ancNode.Facts[views.FactMatchesViewFilter] != false {
		t.Fatalf("done parent facts = %+v, want filterContextOnly=true matchesViewFilter=false", ancNode.Facts)
	}
	if ancNode.Tone != "row.dim" || ancNode.Icon != views.ContextAncestorIcon || !views.HasContextAncestorBadge(ancNode.Badges) {
		t.Fatalf("done parent context styling = tone:%q icon:%q badges:%+v", ancNode.Tone, ancNode.Icon, ancNode.Badges)
	}
	hotfixNode, ok := findTreeNodeByID(tm.Nodes, activeChild.ID)
	if !ok {
		t.Fatalf("missing active child %q in hide-final tree", activeChild.ID)
	}
	if hotfixNode.Facts[views.FactFilterContextOnly] != false || hotfixNode.Facts[views.FactMatchesViewFilter] != true {
		t.Fatalf("active child facts = %+v, want filterContextOnly=false matchesViewFilter=true", hotfixNode.Facts)
	}
	if hotfixNode.Tone == "row.dim" || views.HasContextAncestorBadge(hotfixNode.Badges) {
		t.Fatalf("matched active child should stay bright: tone=%q badges=%+v", hotfixNode.Tone, hotfixNode.Badges)
	}

	// 2. Cycle to only-final via ).
	d.Keys(")")
	if got := a.outlineViewMode(outlineTree); got != OutlineViewOnlyFinal {
		t.Fatalf("after second ), mode = %q, want %q", got, OutlineViewOnlyFinal)
	}
	onlyFrame := d.Frame()
	if !strings.Contains(onlyFrame, "view: only-final") || !strings.Contains(onlyFrame, "3 matched") {
		t.Fatalf("only-final header missing view/matched badges:\n%s", onlyFrame)
	}
	if strings.Contains(onlyFrame, "Post-Release Hotfix") || strings.Contains(onlyFrame, "Next Backlog Item") {
		t.Fatalf("only-final leaked open leaves:\n%s", onlyFrame)
	}
	if !strings.Contains(onlyFrame, "Active Roadmap") || !strings.Contains(onlyFrame, "Completed Spec") || !strings.Contains(onlyFrame, "Old Milestone") {
		t.Fatalf("only-final missing active parent / done descendants:\n%s", onlyFrame)
	}

	activeAnc, ok := findTreeNodeByID(tm.Nodes, activeProj.ID)
	if !ok {
		t.Fatalf("missing active ancestor %q in only-final tree", activeProj.ID)
	}
	if activeAnc.Facts[views.FactFilterContextOnly] != true || activeAnc.Facts[views.FactMatchesViewFilter] != false {
		t.Fatalf("active parent facts in only-final = %+v, want contextOnly=true matches=false", activeAnc.Facts)
	}
	if activeAnc.Tone != "row.dim" || activeAnc.Icon != views.ContextAncestorIcon || !views.HasContextAncestorBadge(activeAnc.Badges) {
		t.Fatalf("active parent in only-final not visually dimmed: tone=%q icon=%q badges=%+v", activeAnc.Tone, activeAnc.Icon, activeAnc.Badges)
	}
	specNode, ok := findTreeNodeByID(tm.Nodes, doneChild.ID)
	if !ok {
		t.Fatalf("missing matched done child %q in only-final tree", doneChild.ID)
	}
	if specNode.Facts[views.FactFilterContextOnly] != false || specNode.Facts[views.FactMatchesViewFilter] != true {
		t.Fatalf("done child facts = %+v, want contextOnly=false matches=true", specNode.Facts)
	}
	if specNode.Tone != "text" || specNode.IconTone != "state.done" {
		t.Fatalf("matched done child in only-final should render at full brightness: tone=%q iconTone=%q", specNode.Tone, specNode.IconTone)
	}
	if _, ok := findTreeNodeByID(tm.Nodes, doneLeaf.ID); !ok {
		t.Fatalf("missing doneLeaf %q in only-final tree", doneLeaf.ID)
	}
	if _, ok := findTreeNodeByID(tm.Nodes, todoLeaf.ID); ok {
		t.Fatalf("todoLeaf %q should be hidden in only-final tree", todoLeaf.ID)
	}

	// 3. Opt-out ui.finalViewDimAncestors = false disables ancestor dimming while keeping facts truthful.
	a.cfg.userSettings[SetFinalViewDimAncestors] = false
	a.reloadSettings()
	a.applySettings()
	d.Send(resize(150, 36))
	_ = d.Frame()
	unDimmedAnc, ok := findTreeNodeByID(tm.Nodes, activeProj.ID)
	if !ok {
		t.Fatalf("missing active ancestor after opt-out")
	}
	if unDimmedAnc.Facts[views.FactFilterContextOnly] != true || unDimmedAnc.Facts[views.FactMatchesViewFilter] != false {
		t.Fatalf("opt-out should keep truthful facts: %+v", unDimmedAnc.Facts)
	}
	if unDimmedAnc.Icon == views.ContextAncestorIcon || views.HasContextAncestorBadge(unDimmedAnc.Badges) {
		t.Fatalf("opt-out ui.finalViewDimAncestors=false should skip ancestor scaffolding style: icon=%q badges=%+v", unDimmedAnc.Icon, unDimmedAnc.Badges)
	}
}

func TestFinalViewsLanesTreeCyclingAndContextAncestors(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	seed := core.Actor{Kind: "system", ID: "seed"}

	proj, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Matomeru", "workStatus": "active"},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}

	// Lanes under Matomeru:
	//   parent-running (running)
	//     ├── child-merged (merged)
	//     └── child-planned (planned)
	//   parent-done (done)
	//     └── child-review (review)
	//   leaf-cancelled (cancelled)
	parentRunning, _, _ := store.Create(data.CreateInput{
		Kind:   data.KindLane,
		Fields: map[string]any{"title": "Wave Parent", "laneStatus": "running", "task": proj.Ref()},
	}, seed)
	childMerged, _, _ := store.Create(data.CreateInput{
		Kind:   data.KindLane,
		Fields: map[string]any{"title": "Merged Lane", "laneStatus": "merged", "task": proj.Ref(), "parentLane": parentRunning.Ref()},
	}, seed)
	childPlanned, _, _ := store.Create(data.CreateInput{
		Kind:   data.KindLane,
		Fields: map[string]any{"title": "Planned Lane", "laneStatus": "planned", "task": proj.Ref(), "parentLane": parentRunning.Ref()},
	}, seed)
	parentDone, _, _ := store.Create(data.CreateInput{
		Kind:   data.KindLane,
		Fields: map[string]any{"title": "Closed Parent", "laneStatus": "done", "task": proj.Ref()},
	}, seed)
	childReview, _, _ := store.Create(data.CreateInput{
		Kind:   data.KindLane,
		Fields: map[string]any{"title": "Review Followup", "laneStatus": "review", "task": proj.Ref(), "parentLane": parentDone.Ref()},
	}, seed)
	leafCancelled, _, _ := store.Create(data.CreateInput{
		Kind:   data.KindLane,
		Fields: map[string]any{"title": "Dropped Spike", "laneStatus": "cancelled", "task": proj.Ref()},
	}, seed)

	a, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })

	d := NewDriver(a, 150, 36)
	a.ensureProjectTabOpen(proj.ID)
	a.Shell.State = a.openSpace(a.State(), projectTabID(proj.ID))
	a.projectCtrl().setSection(proj.ID, "lanes")
	a.Shell.State = withFocus(a.State(), projectLanesTree)
	d.Send(resize(150, 36))

	// Default "all" shows every lane.
	allFrame := d.Frame()
	for _, want := range []string{"Wave Parent", "Merged Lane", "Planned Lane", "Closed Parent", "Review Followup", "Dropped Spike"} {
		if !strings.Contains(allFrame, want) {
			t.Fatalf("lanes all view missing %q:\n%s", want, allFrame)
		}
	}

	// 1st ) -> hide-final: hides Merged Lane & Dropped Spike; keeps Closed Parent as dimmed context ancestor for Review Followup.
	d.Keys(")")
	if got := a.outlineViewMode(projectLanesTree); got != OutlineViewHideFinal {
		t.Fatalf("lanes ) got %q, want %q", got, OutlineViewHideFinal)
	}
	hideFrame := d.Frame()
	if !strings.Contains(hideFrame, "view: hide-final") || !strings.Contains(hideFrame, "3 matched") {
		t.Fatalf("lanes hide-final missing badges:\n%s", hideFrame)
	}
	for _, hidden := range []string{"Merged Lane", "Dropped Spike"} {
		if strings.Contains(hideFrame, hidden) {
			t.Fatalf("lanes hide-final leaked final lane %q:\n%s", hidden, hideFrame)
		}
	}
	lanesTM := a.projectCtrl().trees[projectLanesTree]
	closedAnc, ok := findTreeNodeByID(lanesTM.Nodes, parentDone.ID)
	if !ok || closedAnc.Facts[views.FactFilterContextOnly] != true || closedAnc.Tone != "row.dim" || !views.HasContextAncestorBadge(closedAnc.Badges) {
		t.Fatalf("lanes hide-final Closed Parent should be dimmed context ancestor: ok=%v node=%+v", ok, closedAnc)
	}
	reviewNd, ok := findTreeNodeByID(lanesTM.Nodes, childReview.ID)
	if !ok || reviewNd.Facts[views.FactMatchesViewFilter] != true {
		t.Fatalf("lanes hide-final Review Followup should be matched: ok=%v node=%+v", ok, reviewNd)
	}

	// 2nd ) -> only-final: hides Planned Lane & Review Followup; keeps Wave Parent as dimmed context ancestor for Merged Lane.
	d.Keys(")")
	if got := a.outlineViewMode(projectLanesTree); got != OutlineViewOnlyFinal {
		t.Fatalf("lanes second ) got %q, want %q", got, OutlineViewOnlyFinal)
	}
	onlyFrame := d.Frame()
	if !strings.Contains(onlyFrame, "view: only-final") || !strings.Contains(onlyFrame, "3 matched") {
		t.Fatalf("lanes only-final missing badges:\n%s", onlyFrame)
	}
	for _, hidden := range []string{"Planned Lane", "Review Followup"} {
		if strings.Contains(onlyFrame, hidden) {
			t.Fatalf("lanes only-final leaked open lane %q:\n%s", hidden, onlyFrame)
		}
	}
	waveAnc, ok := findTreeNodeByID(lanesTM.Nodes, parentRunning.ID)
	if !ok || waveAnc.Facts[views.FactFilterContextOnly] != true || waveAnc.Tone != "row.dim" || !views.HasContextAncestorBadge(waveAnc.Badges) {
		t.Fatalf("lanes only-final Wave Parent should be dimmed context ancestor: ok=%v node=%+v", ok, waveAnc)
	}
	mergedNd, ok := findTreeNodeByID(lanesTM.Nodes, childMerged.ID)
	if !ok || mergedNd.Facts[views.FactMatchesViewFilter] != true || mergedNd.Tone != "text" {
		t.Fatalf("lanes only-final Merged Lane should be matched at full brightness: ok=%v node=%+v", ok, mergedNd)
	}
	if _, ok := findTreeNodeByID(lanesTM.Nodes, leafCancelled.ID); !ok {
		t.Fatalf("lanes only-final missing Dropped Spike (%s)", leafCancelled.ID)
	}
	if _, ok := findTreeNodeByID(lanesTM.Nodes, childPlanned.ID); ok {
		t.Fatalf("lanes only-final leaked Planned Lane (%s)", childPlanned.ID)
	}

	// 3rd ) wraps directly back to "all" (lanes do not include task-only agent-threads), and ( goes backward to "only-final".
	d.Keys(")")
	if got := a.outlineViewMode(projectLanesTree); got != OutlineViewAll {
		t.Fatalf("lanes third ) did not wrap to all: %q", got)
	}
	d.Keys("(")
	if got := a.outlineViewMode(projectLanesTree); got != OutlineViewOnlyFinal {
		t.Fatalf("lanes ( from all got %q, want %q", got, OutlineViewOnlyFinal)
	}
}
