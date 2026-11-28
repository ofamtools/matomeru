package app

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func TestPageRefCardsHydrateLiveUpdateAndDeepLink(t *testing.T) {
	a, d := demoLegacy(t)
	pane, ok := a.SelectedPane()
	if !ok {
		t.Fatal("expected selected pane in demo")
	}

	// Create a second pane to test pane ref-card deep-linking.
	otherPane, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindPane,
		ID:   "pane-target-1",
		Fields: map[string]any{
			"title":     "Worker Pane",
			"runStatus": "running",
			"cwd":       "/tmp/worker",
		},
	}, User)
	if err != nil {
		t.Fatalf("create target pane: %v", err)
	}

	// Create a root lane + 2 children so live rollup progress renders.
	rootLane, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindLane,
		ID:   "4f8e01412677",
		Fields: map[string]any{
			"title":      "Ref Card Hydrator",
			"laneStatus": "running",
			"summary":    "Initial live summary",
			"branch":     "feat/c1-noun-ref-card",
		},
	}, User)
	if err != nil {
		t.Fatalf("create root lane: %v", err)
	}
	for _, child := range []struct {
		id, title, status string
	}{
		{"lane-child-done", "Pure presenter", "done"},
		{"lane-child-run", "App hydrator", "running"},
		{"lane-child-plan", "Verify tests", "planned"},
	} {
		if _, _, err := a.Store.Create(data.CreateInput{
			Kind: data.KindLane,
			ID:   child.id,
			Fields: map[string]any{
				"title":      child.title,
				"laneStatus": child.status,
				"parentLane": rootLane.Ref(),
			},
		}, User); err != nil {
			t.Fatalf("create child lane %s: %v", child.id, err)
		}
	}

	// Create a task + subtasks to verify task ref card hydration.
	rootTask, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindTask,
		ID:   "task-ref-1",
		Fields: map[string]any{
			"title":       "Design Live Cards",
			"workStatus":  "active",
			"description": "Render noun refs cleanly on pages",
			"priority":    "P1",
		},
	}, User)
	if err != nil {
		t.Fatalf("create root task: %v", err)
	}
	for _, sub := range []struct {
		id, title, status string
	}{
		{"sub-1", "Define schema", "done"},
		{"sub-2", "Wire enter deep link", "active"},
	} {
		if _, _, err := a.Store.Create(data.CreateInput{
			Kind: data.KindTask,
			ID:   sub.id,
			Fields: map[string]any{
				"title":      sub.title,
				"workStatus": sub.status,
				"parent":     rootTask.Ref(),
			},
		}, User); err != nil {
			t.Fatalf("create subtask %s: %v", sub.id, err)
		}
	}

	laneRef := rootLane.Ref()
	taskRef := rootTask.Ref()
	paneRef := otherPane.Ref()
	missingRef := core.Ref{Kind: data.KindLane, ID: "missing-lane-999"}

	pageRes, err := a.Router.CreatePage(User, mcpui.CreatePageArgs{
		Pane:  pane.ID,
		Title: "Live Ref Cards",
		Slug:  "live-ref-cards",
		Root: ui.ViewNode{
			ID:   "cards.stack",
			Kind: ui.KindVStack,
			Gap:  1,
			Children: []ui.ViewNode{
				{
					ID:    "q.lane.1",
					Kind:  ui.KindCard,
					Ref:   &laneRef,
					Props: map[string]any{"recordCard": true, "lines": 3},
				},
				{
					ID:   "q.task.1",
					Kind: mcpui.KindRecordCard,
					Ref:  &taskRef,
				},
				{
					ID:    "q.pane.1",
					Kind:  ui.KindCard,
					Ref:   &paneRef,
					Props: map[string]any{"hydrateRef": true},
				},
				{
					ID:   "q.missing.1",
					Kind: mcpui.KindRefCard,
					Ref:  &missingRef,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreatePage with ref cards: %v", err)
	}

	// Open the page in pane site.
	a.panes.activePage[pane.ID] = pageRes.ID
	a.viewMemo.gen++

	view := a.View(a.State())
	spacious := ui.SpaciousDarkTheme()
	fr, solved := ui.Render(view, spacious, "q.lane.1", 140, 45)
	plain := ansiRe.ReplaceAllString(fr.ANSI, "")

	// 1. Assert exact 3-line height under SpaciousDarkTheme for every ref card.
	for _, id := range []string{"q.lane.1", "q.task.1", "q.pane.1", "q.missing.1"} {
		node := view.Find(id)
		if node == nil {
			t.Fatalf("expected hydrated node %q in view", id)
		}
		if node.Facts["recordCard"] != true {
			t.Errorf("node %q Facts[recordCard] = %#v, want true", id, node.Facts["recordCard"])
		}
		if gotH := solved.Rects[id].H; gotH != views.RecordRefCardLines {
			t.Errorf("node %q rendered height under SpaciousDarkTheme = %d, want %d", id, gotH, views.RecordRefCardLines)
		}
	}
	for _, want := range []string{
		"Ref Card Hydrator", "Running", "1/3 ▶1", "Initial live summary", "feat/c1-noun-ref-card",
		"Design Live Cards", "Active", "1/2 ▶1", "Render noun refs cleanly on pages",
		"Worker Pane",
		"(missing lane)", "missing", "record unavailable or deleted",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("rendered page missing %q:\n%s", want, plain)
		}
	}

	// 2. Live updates: edit laneStatus & summary in Store WITHOUT touching page JSON.
	if _, _, err := a.Store.Patch(data.PatchInput{
		Ref: rootLane.Ref(),
		Fields: map[string]any{
			"laneStatus": "review",
			"summary":    "Ready for final review",
		},
	}, User); err != nil {
		t.Fatalf("update root lane: %v", err)
	}
	updatedView := a.View(a.State())
	updatedFrame, _ := ui.Render(updatedView, spacious, "q.lane.1", 140, 45)
	updatedPlain := ansiRe.ReplaceAllString(updatedFrame.ANSI, "")
	if !strings.Contains(updatedPlain, "Review") || !strings.Contains(updatedPlain, "Ready for final review") {
		t.Fatalf("expected live store edit to reflect immediately on ui.page without rewriting page JSON:\n%s", updatedPlain)
	}
	if strings.Contains(updatedPlain, "Initial live summary") {
		t.Fatalf("stale summary still rendered after live store edit:\n%s", updatedPlain)
	}

	// 3. Opt-out setting: pages.hydrateRefCards = false renders raw fallback without hitting live summary.
	a.cfg.userSettings[SetPageHydrateRefCards] = false
	a.reloadSettings()
	rawView := a.View(a.State())
	rawCard := rawView.Find("q.lane.1")
	if rawCard == nil || rawCard.Facts["hydrated"] != false {
		t.Fatalf("with pages.hydrateRefCards=false, q.lane.1 = %+v, want hydrated=false", rawCard)
	}
	rawFrame, rawSolved := ui.Render(rawView, spacious, "q.lane.1", 140, 45)
	if rawSolved.Rects["q.lane.1"].H != views.RecordRefCardLines {
		t.Errorf("raw fallback height = %d, want 3", rawSolved.Rects["q.lane.1"].H)
	}
	if strings.Contains(ansiRe.ReplaceAllString(rawFrame.ANSI, ""), "Ready for final review") {
		t.Errorf("raw fallback should skip live store hydration:\n%s", ansiRe.ReplaceAllString(rawFrame.ANSI, ""))
	}
	// Re-enable hydration for deep-link tests.
	a.cfg.userSettings[SetPageHydrateRefCards] = true
	a.reloadSettings()

	// 4. ModePage navigation (j steps between ref cards) and enter deep-link: lane ref card -> Lane Show page.
	a.Shell.State = withMode(withFocus(a.State(), "q.lane.1"), ModePage)
	d.Keys("j")
	if a.Focus() != "q.task.1" {
		t.Fatalf("j from q.lane.1 in ModePage focused %q, want q.task.1", a.Focus())
	}
	d.Keys("k")
	if a.Focus() != "q.lane.1" {
		t.Fatalf("k from q.task.1 in ModePage focused %q, want q.lane.1", a.Focus())
	}
	d.Keys("enter")
	showing, hasShow := a.Showing()
	if !hasShow || showing != rootLane.Ref() {
		t.Fatalf("enter on hydrated lane ref card in ModePage: showing=(%v, %v), want %v (page=%s focus=%s mode=%s)",
			showing, hasShow, rootLane.Ref(), a.Space(), a.Focus(), a.Mode())
	}
	if !isViewTabID(a.Space()) || a.Mode() != ModeNormal {
		t.Fatalf("after deep link to lane: space=%q mode=%q, want view tab/normal", a.Space(), a.Mode())
	}

	// 5. Deep-link navigation on enter in ModeNormal (with pageLockScroll off): task ref card -> Task Show page.
	a.panes.activePage[pane.ID] = pageRes.ID
	a.viewMemo.gen++
	st := withPage(a.State(), SpacePanes, "q.task.1", ModeNormal)
	st.ActivePage = SpacePanes
	a.Shell.State = st
	d.Keys("enter")
	showing, hasShow = a.Showing()
	if !hasShow || showing != rootTask.Ref() {
		t.Fatalf("enter on hydrated task ref card in ModeNormal: showing=(%v, %v), want %v", showing, hasShow, rootTask.Ref())
	}

	// 6. Deep-link navigation on enter for a pane ref card switches to that pane in SpacePanes.
	a.panes.activePage[pane.ID] = pageRes.ID
	a.viewMemo.gen++
	st = withPage(a.State(), SpacePanes, "q.pane.1", ModePage)
	st.ActivePage = SpacePanes
	a.Shell.State = st
	d.Keys("enter")
	if a.Space() != SpacePanes {
		t.Fatalf("enter on pane ref card: space=%q, want %q", a.Space(), SpacePanes)
	}
	selPane, ok := a.SelectedPane()
	if !ok || selPane.ID != otherPane.ID {
		t.Fatalf("enter on pane ref card: selected pane=%+v, want %s", selPane, otherPane.ID)
	}
}
