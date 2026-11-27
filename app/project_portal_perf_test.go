package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func setupLongTitlePortalFixture(t *testing.T) (*App, *Driver, *data.Store, data.Record) {
	t.Helper()
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mk := func(kind string, fields map[string]any, actor core.Actor) data.Record {
		t.Helper()
		rec, _, err := store.Create(data.CreateInput{Kind: kind, Fields: fields}, actor)
		if err != nil {
			t.Fatalf("create %s: %v", kind, err)
		}
		return *rec
	}

	// Title intentionally > 24 runes to catch clipped-vs-full title cache thrash.
	proj := mk(data.KindTask, map[string]any{
		"title":      "Matomeru Enterprise Project Portal Workspace",
		"workStatus": "active",
	}, User)
	for i := range 12 {
		mk(data.KindTask, map[string]any{
			"title":      fmt.Sprintf("Subtask %02d", i),
			"parent":     proj.Ref(),
			"workStatus": "todo",
		}, User)
	}
	pane := mk(data.KindPane, map[string]any{
		"title":     "claude: portal",
		"task":      proj.Ref(),
		"cwd":       "/tmp/portal",
		"runStatus": "running",
	}, User)
	mk(data.KindLane, map[string]any{
		"title":      "Portal perf lane",
		"laneStatus": "running",
		"task":       proj.Ref(),
	}, User)
	mk(data.KindDocument, map[string]any{
		"title": "Architecture Spec",
		"body":  "# Architecture\n\nFast portal.",
		"task":  proj.Ref(),
	}, User)
	mk(data.KindNote, map[string]any{
		"title":      "Design Note",
		"body":       "Memoize everything cleanly.",
		"authorKind": "owner",
		"task":       proj.Ref(),
	}, User)
	mk(data.KindPage, map[string]any{
		"title":    "Status Summary",
		"slug":     "status-summary",
		"pageKind": "report",
		"task":     proj.Ref(),
		"pane":     pane.Ref(),
		"viewedAt": data.WriteTime{},
		"root":     ui.ViewNode{Kind: ui.KindMarkdown, ID: "status.md", Props: map[string]any{"markdown": "# Status\n\nAll good."}},
	}, User)
	mk(data.KindQuestion, map[string]any{
		"prompt":       "Ship G2 perf fixes?",
		"questionKind": "decision",
		"status":       "open",
		"task":         proj.Ref(),
	}, core.Actor{Kind: "agent", ID: "worker-g2"})

	now := time.Now().UTC()
	a, err := New(store, Options{FS: smokeFS(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a, NewDriver(a, 140, 40), store, proj
}

func TestOutlineModalOverProjectPortalDoesNotRebuildBackground(t *testing.T) {
	a, d, _, proj := setupLongTitlePortalFixture(t)

	// Open the Project Portal tab and warm all caches.
	a.ensureProjectTabOpen(proj.ID)
	d.Keys("4")
	_ = d.Frame()
	if !isProjectTab(a.Space()) {
		t.Fatalf("expected active Project Portal tab, got space=%q", a.Space())
	}

	// Open Outline modal over the populated Project Portal.
	d.Keys("T")
	_ = d.Frame()
	if a.Modal() != "outline" || a.Mode() != ModeOutline {
		t.Fatalf("T did not open outline modal: modal=%q mode=%q", a.Modal(), a.Mode())
	}

	pc := a.projectCtrl()
	durableBefore := pc.durableBuilds
	tabsBefore := a.viewMemo.tabsBuilds
	navBefore := pc.navBuilds
	homeBefore := pc.homeBuilds
	syncBefore := a.trees.syncBuilds

	// 10 consecutive navigation keystrokes (j/k) inside ModeOutline.
	for range 5 {
		d.Keys("j")
		_ = d.Frame()
	}
	for range 5 {
		d.Keys("k")
		_ = d.Frame()
	}

	// Enter ModeOutlineEdit and type 12 characters into the inline draft input.
	d.Keys("o")
	_ = d.Frame()
	if a.Mode() != ModeOutlineEdit {
		t.Fatalf("o did not enter ModeOutlineEdit: mode=%q", a.Mode())
	}
	for _, ch := range "Fast outline" {
		d.Type(string(ch))
		_ = d.Frame()
	}

	if got := pc.durableBuilds - durableBefore; got != 0 {
		t.Fatalf("outline modal keystrokes triggered %d durableBuilds, want 0", got)
	}
	if got := a.viewMemo.tabsBuilds - tabsBefore; got != 0 {
		t.Fatalf("outline modal keystrokes rebuilt background Project Portal tabs %d times, want 0", got)
	}
	if got := pc.navBuilds - navBefore; got != 0 {
		t.Fatalf("outline modal keystrokes rebuilt background Project nav %d times, want 0", got)
	}
	if got := pc.homeBuilds - homeBefore; got != 0 {
		t.Fatalf("outline modal keystrokes rebuilt background Project home %d times, want 0", got)
	}
	if got := a.trees.syncBuilds - syncBefore; got != 0 {
		t.Fatalf("outline modal navigation/typing rebuilt outline tree %d times, want 0", got)
	}
	if frame := d.Frame(); !strings.Contains(frame, "Fast outline") {
		t.Fatalf("outline modal frame missing typed draft text:\n%s", frame)
	}
}

func TestProjectPortalNavigationZeroDurableRebuilds(t *testing.T) {
	a, d, _, proj := setupLongTitlePortalFixture(t)

	// Open the Project Portal tab (>24-char title) and warm initial caches.
	a.ensureProjectTabOpen(proj.ID)
	d.Keys("4")
	_ = d.Frame()
	if !isProjectTab(a.Space()) {
		t.Fatalf("expected active Project Portal tab, got space=%q", a.Space())
	}

	pc := a.projectCtrl()
	durableBefore := pc.durableBuilds
	navBefore := pc.navBuilds

	// 1. Rapid j/k across left-nav sections (crossing section boundaries!).
	for range 12 {
		d.Keys("j")
		_ = d.Frame()
	}
	for range 12 {
		d.Keys("k")
		_ = d.Frame()
	}

	if got := pc.durableBuilds - durableBefore; got != 0 {
		t.Fatalf("rapid j/k across Project Portal nav triggered %d durableBuilds, want 0", got)
	}
	if got := pc.navBuilds - navBefore; got != 0 {
		t.Fatalf("rapid j/k across Project Portal nav sections triggered %d navBuilds, want 0", got)
	}

	// 2. Step right into Home dashboard and move j/k across dashboard items.
	d.Keys("l")
	_ = d.Frame()
	homeAfterEnter := pc.homeBuilds
	for range 5 {
		d.Keys("j")
		_ = d.Frame()
	}
	for range 5 {
		d.Keys("k")
		_ = d.Frame()
	}

	if got := pc.durableBuilds - durableBefore; got != 0 {
		t.Fatalf("j/k across Project Home dashboard triggered %d durableBuilds, want 0", got)
	}
	if got := pc.homeBuilds - homeAfterEnter; got != 0 {
		t.Fatalf("j/k across Project Home dashboard triggered %d homeBuilds, want 0", got)
	}
}

func TestProjectNavCompactModeAndOptOut(t *testing.T) {
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	proj, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "CompactPortal", "workStatus": "active"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	agentActor := core.Actor{Kind: "agent", ID: "worker-notes"}
	var noteIDs []string
	for i := range 7 {
		rec, _, err := store.Create(data.CreateInput{
			Kind: data.KindNote,
			Fields: map[string]any{
				"title":      fmt.Sprintf("Agent Note %02d", i),
				"body":       "note body",
				"authorKind": "agent",
				"task":       proj.Ref(),
			},
		}, agentActor)
		if err != nil {
			t.Fatal(err)
		}
		noteIDs = append(noteIDs, rec.ID)
	}

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 140, 40)

	a.ensureProjectTabOpen(proj.ID)
	d.Keys("4")

	// Default: ui.projectNavCompact = true.
	if !a.projectNavCompactEnabled() {
		t.Fatal("expected ui.projectNavCompact to default to true")
	}
	root := a.View(a.State())

	// Empty sections (signals, commands, worktrees) omit zero-count badges.
	for _, secKey := range []string{"signals", "commands", "worktrees"} {
		sec := root.Find(projectNavPrefix + secKey)
		if sec == nil {
			t.Fatalf("missing section %q", secKey)
		}
		txt, _ := sec.Props["text"].(string)
		if strings.HasSuffix(strings.TrimSpace(txt), "0") {
			t.Fatalf("compact mode should omit zero-count badge on empty section %q, got %q", secKey, txt)
		}
	}

	// Group spacing spacers appear between groups in compact mode.
	if root.Find("project.nav.gap.context") == nil || root.Find("project.nav.gap.workspace") == nil {
		t.Fatal("compact mode should emit blank spacer rows between nav groups")
	}

	// Collapsed Notes section caps auto-expanded unread children at 4 + muted "+3 more" row,
	// while still showing the full unread count (7) on the section header.
	notesSec := root.Find(projectNavPrefix + "notes")
	if notesSec == nil || notesSec.Facts["unreadCount"] != 7 {
		t.Fatalf("notes section unreadCount = %v, want 7", notesSec)
	}
	shownCount := 0
	for _, id := range noteIDs {
		if root.Find(projectNavNodePrefix+"notes."+id) != nil {
			shownCount++
		}
	}
	if shownCount != projectNavCompactPeekLimit {
		t.Fatalf("compact mode auto-expanded %d unread notes, want %d", shownCount, projectNavCompactPeekLimit)
	}
	moreNode := root.Find(projectNavNodePrefix + "notes." + projectNavOverflowNodeID)
	if moreNode == nil {
		t.Fatal("compact mode missing +N more indicator for capped unread notes")
	}
	if txt, _ := moreNode.Props["text"].(string); !strings.Contains(txt, "+3 more") {
		t.Fatalf("overflow indicator text = %q, want '+3 more'", txt)
	}

	// Explicit user expansion via space shows all 7 notes (not capped to 4).
	a.projectCtrl().setNavSectionExpanded(proj.ID, "notes", true)
	a.viewMemo.gen++
	expandedRoot := a.View(a.State())
	expandedShown := 0
	for _, id := range noteIDs {
		if expandedRoot.Find(projectNavNodePrefix+"notes."+id) != nil {
			expandedShown++
		}
	}
	if expandedShown != len(noteIDs) {
		t.Fatalf("explicitly expanded Notes showed %d items, want %d", expandedShown, len(noteIDs))
	}

	// Collapse back and opt out: ui.projectNavCompact = false restores dense mode.
	a.projectCtrl().setNavSectionExpanded(proj.ID, "notes", false)
	a.cfg.userSettings[SetProjectNavCompact] = false
	a.reloadSettings()
	a.applySettings()

	denseRoot := a.View(a.State())
	for _, secKey := range []string{"signals", "commands", "worktrees"} {
		sec := denseRoot.Find(projectNavPrefix + secKey)
		if sec == nil {
			t.Fatalf("dense mode missing section %q", secKey)
		}
		txt, _ := sec.Props["text"].(string)
		if !strings.HasSuffix(strings.TrimSpace(txt), "0") {
			t.Fatalf("dense mode (ui.projectNavCompact=false) should show 0 badge on empty section %q, got %q", secKey, txt)
		}
	}
	if denseRoot.Find("project.nav.gap.context") != nil {
		t.Fatal("dense mode should not insert group spacer rows")
	}
	denseShown := 0
	for _, id := range noteIDs {
		if denseRoot.Find(projectNavNodePrefix+"notes."+id) != nil {
			denseShown++
		}
	}
	if denseShown != len(noteIDs) {
		t.Fatalf("dense mode showed %d auto-expanded unread notes, want all %d", denseShown, len(noteIDs))
	}
}

func TestProjectNavCompletedLaneDimmedWithOrangeUnreadDot(t *testing.T) {
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	proj, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "LanePolish", "workStatus": "active"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })

	// Create post-startup agent lanes so they start unread.
	time.Sleep(2 * time.Millisecond)
	agentActor := core.Actor{Kind: "agent", ID: "worker-lane"}
	mkLane := func(title, status string) data.Record {
		t.Helper()
		rec, _, err := store.Create(data.CreateInput{
			Kind: data.KindLane,
			Fields: map[string]any{
				"title":      title,
				"laneStatus": status,
				"task":       proj.Ref(),
			},
		}, agentActor)
		if err != nil {
			t.Fatalf("create lane %s: %v", title, err)
		}
		return *rec
	}

	activeLane := mkLane("Active Running Lane", "running")
	doneLane := mkLane("Finished Done Lane", "done")
	mergedLane := mkLane("Shipped Merged Lane", "merged")
	cancelledLane := mkLane("Dropped Cancelled Lane", "cancelled")

	d := NewDriver(a, 140, 40)
	a.ensureProjectTabOpen(proj.ID)
	d.Keys("4")
	a.projectCtrl().setNavSectionExpanded(proj.ID, "lanes", true)
	a.viewMemo.gen++

	root := a.View(a.State())

	assertDimmedWithOrangeDot := func(id, wantIcon string) {
		t.Helper()
		nd := root.Find(projectNavNodePrefix + "lanes." + id)
		if nd == nil {
			t.Fatalf("missing lane nav node %q", id)
		}
		if !nd.Unread || nd.Facts["unread"] != true {
			t.Fatalf("unreviewed completed lane %q should be unread: %+v", id, nd)
		}
		if nd.Tone != "muted" || nd.Facts["dim"] != true {
			t.Fatalf("completed lane %q should carry muted/dim styling: tone=%q facts=%+v", id, nd.Tone, nd.Facts)
		}
		txt, _ := nd.Props["text"].(string)
		if !strings.HasPrefix(txt, "● ") {
			t.Fatalf("unreviewed completed lane %q text = %q, want leading ● unread dot", id, txt)
		}
		if !strings.Contains(txt, wantIcon) {
			t.Fatalf("completed lane %q text = %q, want icon %q", id, txt, wantIcon)
		}
		spans, _ := nd.Props["spans"].([]ui.ToneSpan)
		rs := []rune(txt)
		var hasOrangeDot, hasMutedIcon, hasMutedLabel bool
		for _, sp := range spans {
			seg := string(rs[sp.From:sp.To])
			if sp.From == 0 && seg == "●" && sp.Tone == "warning" {
				hasOrangeDot = true
			}
			if seg == wantIcon && sp.Tone == "muted" {
				hasMutedIcon = true
			}
			if strings.Contains(seg, "Lane") && sp.Tone == "muted" {
				hasMutedLabel = true
			}
		}
		if !hasOrangeDot || !hasMutedIcon || !hasMutedLabel {
			t.Fatalf("lane %q spans = %+v on %q (orangeDot=%v mutedIcon=%v mutedLabel=%v)",
				id, spans, txt, hasOrangeDot, hasMutedIcon, hasMutedLabel)
		}
	}

	assertDimmedWithOrangeDot(doneLane.ID, "✓")
	assertDimmedWithOrangeDot(mergedLane.ID, "✓")
	assertDimmedWithOrangeDot(cancelledLane.ID, "×")

	// Active running lane must NOT be dimmed.
	activeNode := root.Find(projectNavNodePrefix + "lanes." + activeLane.ID)
	if activeNode == nil || activeNode.Facts["dim"] == true || activeNode.Tone == "muted" {
		t.Fatalf("active running lane should not be dimmed: %+v", activeNode)
	}

	// Mark doneLane read -> orange dot disappears while dimmed completed style + ✓ stay!
	a.Shell.State = withFocus(a.State(), projectNavNodePrefix+"lanes."+doneLane.ID)
	a.viewMemo.gen++
	d.Keys("r")

	afterRoot := a.View(a.State())
	readDone := afterRoot.Find(projectNavNodePrefix + "lanes." + doneLane.ID)
	if readDone == nil {
		t.Fatal("read done lane missing from expanded Lanes section")
	}
	if readDone.Unread || readDone.Facts["unread"] == true {
		t.Fatalf("done lane should be read after pressing r: %+v", readDone)
	}
	if readDone.Tone != "muted" || readDone.Facts["dim"] != true {
		t.Fatalf("read done lane should keep dimmed muted styling: tone=%q facts=%+v", readDone.Tone, readDone.Facts)
	}
	readTxt, _ := readDone.Props["text"].(string)
	if strings.HasPrefix(readTxt, "●") {
		t.Fatalf("read done lane should drop orange ● dot, got %q", readTxt)
	}
	if !strings.Contains(readTxt, "✓") {
		t.Fatalf("read done lane should keep ✓ icon, got %q", readTxt)
	}
}
