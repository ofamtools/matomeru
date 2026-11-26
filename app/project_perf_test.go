package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ofam.dev/matomeru/contextitems"
	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func TestProjectPerfContextIndexSeenAtNoGenChurn(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	proj, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Atlas", "workStatus": "active"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	pane, _, err := store.Create(data.CreateInput{
		Kind:   data.KindPane,
		Fields: map[string]any{"title": "claude: atlas", "task": proj.Ref(), "cwd": "/tmp/atlas"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	a, err := New(store, Options{FS: smokeFS(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })

	lines := []string{"Working on https://example.com/atlas/pr/1 and /tmp/atlas/main.go"}
	a.harvestScreenLines(*pane, lines, 120, 1)
	gen1 := a.contextIndexGen()
	if gen1 == 0 {
		t.Fatal("expected initial harvestScreenLines to bump contextIndexGen")
	}

	// Re-harvest identical items 5 seconds later (SeenAt changes, visible fields unchanged).
	now = now.Add(5 * time.Second)
	a.harvestScreenLines(*pane, lines, 120, 2)
	if got := a.contextIndexGen(); got != gen1 {
		t.Fatalf("SeenAt-only screen refresh bumped contextIndexGen: got %d, want %d", got, gen1)
	}

	// Unrelated SQLite write (note) must not cause loadPersistedContextIndex to churn gen.
	if _, _, err := store.Create(data.CreateInput{
		Kind:   data.KindNote,
		Fields: map[string]any{"title": "Scratch", "body": "hello", "task": proj.Ref()},
	}, User); err != nil {
		t.Fatal(err)
	}
	a.loadPersistedContextIndex(a.ensureContextIndex())
	if got := a.contextIndexGen(); got != gen1 {
		t.Fatalf("unrelated KindNote write bumped contextIndexGen: got %d, want %d", got, gen1)
	}

	// Adding a visible field (line number on existing file item) or a new item MUST bump gen.
	a.indexContextItems(pane, "", "", []contextitems.Item{{
		Kind:   contextitems.KindFile,
		Value:  "/tmp/atlas/main.go:42",
		Path:   "/tmp/atlas/main.go",
		Line:   42,
		Source: contextitems.SourceScreen,
	}})
	gen2 := a.contextIndexGen()
	if gen2 <= gen1 {
		t.Fatalf("visible field update (line=42) did not bump contextIndexGen: before=%d after=%d", gen1, gen2)
	}

	a.harvestScreenLines(*pane, []string{"Opened https://example.com/atlas/pr/2"}, 120, 3)
	if got := a.contextIndexGen(); got <= gen2 {
		t.Fatalf("new harvested item did not bump contextIndexGen: before=%d after=%d", gen2, got)
	}
}

func TestProjectPerfWorkspaceTelemetrySplitAndContextRecords(t *testing.T) {
	f := setupPerfWorkspaceFixture(t)
	a := f.app
	projRef := f.projects[0].Ref()

	ws1 := a.collectProjectWorkspaceData("Project 00", &projRef)
	if len(ws1.contextRecords) == 0 {
		t.Fatal("expected ws.contextRecords populated from single ProjectContextAll call")
	}
	if len(ws1.tasks) == 0 || len(ws1.panes) == 0 {
		t.Fatalf("expected non-empty tasks (%d) and panes (%d)", len(ws1.tasks), len(ws1.panes))
	}
	tasksPtr1 := &ws1.tasks[0]
	ctxPtr1 := &ws1.contextItems[0]

	// Inject live daemon telemetry change (telemetrySeq bumps, SQLite rev does NOT).
	paneID := ws1.panes[0].ID
	a.rt.live.telemetryMu.Lock()
	a.rt.live.telemetry = &daemon.TelemetryResult{
		Telemetry: map[string]*daemon.PaneTelemetry{
			paneID: {PaneID: paneID, State: daemon.StateNeedsYou},
		},
	}
	a.rt.live.telemetrySeq.Add(1)
	a.rt.live.telemetryMu.Unlock()

	ws2 := a.collectProjectWorkspaceData("Project 00", &projRef)
	if &ws2.tasks[0] != tasksPtr1 || &ws2.contextItems[0] != ctxPtr1 {
		t.Fatal("telemetrySeq tick rebuilt SQLite workspace slices instead of reusing cached workspace")
	}
	if ws2.needsYouPanes != 1 {
		t.Fatalf("needsYouPanes after telemetry update = %d, want 1", ws2.needsYouPanes)
	}
	if ws2.unreadTotal != ws1.unreadTotal+1 {
		t.Fatalf("unreadTotal after pane needs_you = %d, want %d", ws2.unreadTotal, ws1.unreadTotal+1)
	}

	// Clearing telemetry reverts counts in place without rebuilding SQLite slices.
	a.rt.live.telemetryMu.Lock()
	a.rt.live.telemetry = nil
	a.rt.live.telemetrySeq.Add(1)
	a.rt.live.telemetryMu.Unlock()

	ws3 := a.collectProjectWorkspaceData("Project 00", &projRef)
	if &ws3.tasks[0] != tasksPtr1 {
		t.Fatal("clearing telemetry rebuilt SQLite workspace slices")
	}
	if ws3.needsYouPanes != 0 || ws3.unreadTotal != ws1.unreadTotal {
		t.Fatalf("after clearing telemetry: needsYou=%d unreadTotal=%d, want 0 and %d", ws3.needsYouPanes, ws3.unreadTotal, ws1.unreadTotal)
	}
}

func TestProjectPerfSectionTableAndOutlineMemoization(t *testing.T) {
	f := setupProjectWorkspaceFixture(t)
	a, d := f.app, f.driver

	d.Keys("P")

	// 1. Context section table memoization across j/k motions.
	selectProjectSection(t, f, "context")
	_ = d.Frame()
	ctxTbl1 := a.projectCtrl().tables[projectContextTable]
	if ctxTbl1 == nil || len(ctxTbl1.Rows) < 2 {
		t.Fatalf("expected populated context table, got %+v", ctxTbl1)
	}
	rowSlicePtr1 := &ctxTbl1.Rows[0]
	d.Keys("j")
	_ = d.Frame()
	ctxTbl2 := a.projectCtrl().tables[projectContextTable]
	if ctxTbl2 != ctxTbl1 || &ctxTbl2.Rows[0] != rowSlicePtr1 {
		t.Fatal("j motion in project.context rebuilt table rows instead of reusing memoized table")
	}
	if ctxTbl2.SelectedIdx != 1 {
		t.Fatalf("j motion in project.context SelectedIdx = %d, want 1", ctxTbl2.SelectedIdx)
	}

	// 2. Notes section table memoization across j/k motions.
	selectProjectSection(t, f, "notes")
	_ = d.Frame()
	notesTbl1 := a.projectCtrl().tables[projectNotesTable]
	if notesTbl1 == nil || len(notesTbl1.Rows) == 0 {
		t.Fatalf("expected populated notes table, got %+v", notesTbl1)
	}
	notesRowPtr1 := &notesTbl1.Rows[0]
	d.Keys("j")
	_ = d.Frame()
	notesTbl2 := a.projectCtrl().tables[projectNotesTable]
	if notesTbl2 != notesTbl1 || &notesTbl2.Rows[0] != notesRowPtr1 {
		t.Fatal("j motion in project.notes rebuilt table rows instead of reusing memoized table")
	}

	// 3. Panes section tree memoization across j/k motions.
	selectProjectSection(t, f, "panes")
	_ = d.Frame()
	panesTM := a.projectCtrl().trees[projectPanesTree]
	if panesTM == nil || len(panesTM.Nodes) == 0 {
		t.Fatalf("expected populated panes tree, got %+v", panesTM)
	}
	panesNodePtr1 := &panesTM.Nodes[0]
	d.Keys("j")
	_ = d.Frame()
	if &panesTM.Nodes[0] != panesNodePtr1 {
		t.Fatal("j motion in project.panes rebuilt tree nodes instead of reusing memoized tree")
	}

	// 4. Project Tasks outline syncCache hit across j/k + invalidation on create.
	selectProjectSection(t, f, "tasks")
	_ = d.Frame()
	outlineTM := a.trees.projectModel(f.proj.ID)
	if _, cached := a.trees.syncCache[outlineTM]; !cached {
		t.Fatal("expected syncProjectTaskTree to populate a.trees.syncCache")
	}
	outlineNodePtr1 := &outlineTM.Nodes[0]
	a.syncProjectTaskTree(outlineTM, f.proj.ID, projectOutlineTree)
	if &outlineTM.Nodes[0] != outlineNodePtr1 {
		t.Fatal("repeated syncProjectTaskTree rebuilt Nodes instead of hitting syncCache")
	}

	// Creating a new subtask under f.proj must invalidate syncCache and appear in outline.
	if _, _, err := a.Store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Cache Invalidation Task", "parent": f.proj.Ref(), "workStatus": "todo"},
	}, User); err != nil {
		t.Fatal(err)
	}
	a.syncProjectTaskTree(outlineTM, f.proj.ID, projectOutlineTree)
	if len(outlineTM.Nodes) == 0 || len(outlineTM.Nodes[0].Children) < 2 {
		t.Fatalf("expected new subtask after syncCache invalidation, got %+v", outlineTM.Nodes)
	}
}

func TestProjectPerfContextNavUnreadFilterAndOverflowCap(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	proj, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Beacon", "workStatus": "active"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	pane, _, err := store.Create(data.CreateInput{
		Kind:   data.KindPane,
		Fields: map[string]any{"title": "claude: beacon", "task": proj.Ref()},
	}, User)
	if err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })

	// Create post-startup auto-harvested context items (screen/prompt/response) + 1 curated/tool agent item.
	time.Sleep(2 * time.Millisecond)
	agentActor := core.Actor{Kind: "agent", ID: "worker-ctx"}
	for i := range 60 {
		src := "screen"
		if i%3 == 1 {
			src = "prompt"
		} else if i%3 == 2 {
			src = "response"
		}
		if _, _, err := store.Create(data.CreateInput{
			Kind: data.KindContext,
			Fields: map[string]any{
				"title":       fmt.Sprintf("Scraped %02d", i),
				"value":       fmt.Sprintf("https://example.com/beacon/scraped/%d", i),
				"contextKind": "url",
				"source":      src,
				"pane":        pane.Ref(),
				"task":        proj.Ref(),
			},
		}, contextHarvestActor); err != nil {
			t.Fatal(err)
		}
	}
	toolCtx, _, err := store.Create(data.CreateInput{
		Kind: data.KindContext,
		Fields: map[string]any{
			"title":       "Agent Added Link",
			"value":       "https://example.com/beacon/agent-link",
			"contextKind": "url",
			"source":      "tool",
			"pane":        pane.Ref(),
			"task":        proj.Ref(),
		},
	}, agentActor)
	if err != nil {
		t.Fatal(err)
	}

	d := NewDriver(a, 165, 42)
	d.Keys("P")
	root := a.View(a.State())

	// Collapsed Context must ONLY count/surface the explicit agent "tool" item (not the 60 auto-harvested ones!).
	ctxSec := root.Find(projectNavPrefix + "context")
	if ctxSec == nil || ctxSec.Facts["unreadCount"] != 1 {
		t.Fatalf("collapsed Context unreadCount = %v, want 1 (auto-harvested items must not flood unread)", ctxSec)
	}
	if root.Find(projectNavNodePrefix+"context."+toolCtx.ID) == nil {
		t.Fatal("explicit agent tool context item should still surface when collapsed")
	}

	// Expanding Context caps rendered children at projectNavContextMaxItems (40) + 1 muted overflow row.
	a.Shell.State = withFocus(a.State(), projectNavPrefix+"context")
	a.viewMemo.gen++
	d.Keys("l")
	root = a.View(a.State())

	moreNodeID := projectNavNodePrefix + "context." + projectNavOverflowNodeID
	moreNode := root.Find(moreNodeID)
	if moreNode == nil {
		t.Fatalf("expected overflow node %q when 61 context items expanded", moreNodeID)
	}
	if txt, _ := moreNode.Props["text"].(string); !strings.Contains(txt, "+21 more — open Context section") {
		t.Fatalf("overflow node text = %q, want '+21 more — open Context section'", txt)
	}
	if moreNode.Tone != "muted" {
		t.Fatalf("overflow node tone = %q, want muted", moreNode.Tone)
	}

	// Pressing enter on the overflow node opens the full Context section table.
	a.Shell.State = withFocus(a.State(), moreNodeID)
	a.viewMemo.gen++
	d.Keys("enter")
	if got := a.projectCtrl().activeSection(proj.ID); got != "context" {
		t.Fatalf("enter on overflow node activeSection = %q, want context", got)
	}
	if got := a.Focus(); got != projectContextTable {
		t.Fatalf("enter on overflow node focus = %q, want %q", got, projectContextTable)
	}
}

func TestProjectPerfPreviewFileCache(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	dir := t.TempDir()
	mdPath := filepath.Join(dir, "spec.md")
	if err := os.WriteFile(mdPath, []byte("# Version 1\n\nInitial body."), 0o644); err != nil {
		t.Fatal(err)
	}

	var reads atomic.Int32
	a, err := New(store, Options{
		FS: smokeFS(),
		ReadFile: func(path string) ([]byte, error) {
			reads.Add(1)
			return os.ReadFile(path)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })

	doc := data.Record{
		ID:     "doc-1",
		Kind:   data.KindDocument,
		Fields: map[string]any{"title": "Spec", "filePath": mdPath},
	}

	for range 5 {
		body, ok := a.recordMarkdownBody(doc)
		if !ok || !strings.Contains(body, "Version 1") {
			t.Fatalf("recordMarkdownBody = (%q, %v), want Version 1", body, ok)
		}
	}
	if got := reads.Load(); got != 1 {
		t.Fatalf("ReadFile calls for unchanged file = %d, want 1", got)
	}

	// Modify file on disk (different size & mtime) -> cache invalidates immediately.
	if err := os.WriteFile(mdPath, []byte("# Version 2\n\nUpdated body with more text."), 0o644); err != nil {
		t.Fatal(err)
	}
	body, ok := a.recordMarkdownBody(doc)
	if !ok || !strings.Contains(body, "Version 2") {
		t.Fatalf("recordMarkdownBody after edit = (%q, %v), want Version 2", body, ok)
	}
	if got := reads.Load(); got != 2 {
		t.Fatalf("ReadFile calls after file edit = %d, want 2", got)
	}
}

func TestProjectContextStarAppearsInPinnedAndPersists(t *testing.T) {
	f := setupProjectWorkspaceFixture(t)
	a, d := f.app, f.driver

	// Create a persisted context record with colon-delimited canonKey (as written by tool hooks)
	// and linked via RelContext (reproducing the viewnode.go colon-canonKey bug).
	ctxRec, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindContext,
		Fields: map[string]any{
			"title":       "viewnode.go",
			"contextKind": "file",
			"canonKey":    "file:/Users/test/proj/ochazuke/ui/viewnode.go",
			"value":       "/Users/test/proj/ochazuke/ui/viewnode.go",
			"source":      "tool",
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Store.Relate(data.RelateInput{
		From: f.proj.Ref(),
		Kind: data.RelContext,
		To:   ctxRec.Ref(),
	}, User); err != nil {
		t.Fatal(err)
	}

	// Open the Project Page, go to the Context section table, and focus our viewnode.go row.
	d.Keys("P")
	selectProjectSection(t, f, "context")
	_ = d.Frame()
	tbl := a.projectCtrl().tables[projectContextTable]
	if tbl == nil {
		t.Fatalf("expected %s table", projectContextTable)
	}
	found := false
	for i, r := range tbl.Rows {
		if r.Ref != nil && r.Ref.ID == ctxRec.ID {
			tbl.SelectedIdx = i
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("context record %s not found in %s", ctxRec.ID, projectContextTable)
	}
	d.Keys("m")

	// 1. Star must persist in SQLite.
	afterStar, err := a.Store.Get(ctxRec.Ref())
	if err != nil || !afterStar.IsStarred() {
		t.Fatalf("context record not persisted as starred in SQLite: err=%v star=%v", err, afterStar != nil && afterStar.IsStarred())
	}
	// 2. Parent project task must NOT have been starred accidentally.
	projAfter, _ := a.Store.Get(f.proj.Ref())
	if projAfter.IsStarred() {
		t.Fatalf("starring context item accidentally starred parent project task")
	}
	// 3. Item must immediately appear in ws.pinnedItems and Pinned Links & Files table.
	projRef := f.proj.Ref()
	ws := a.collectProjectWorkspaceData(f.proj.Text("title"), &projRef)
	inPinned := false
	for _, it := range ws.pinnedItems {
		if it.RecordID == ctxRec.ID && it.Starred {
			inPinned = true
			break
		}
	}
	if !inPinned {
		t.Fatalf("starred context item %s missing from Pinned Links & Files (pinnedItems=%d)", ctxRec.ID, len(ws.pinnedItems))
	}
	selectProjectSection(t, f, "pinned")
	if frame := d.Frame(); !strings.Contains(frame, "viewnode.go") {
		t.Fatalf("Pinned Links & Files frame missing starred item:\n%s", frame)
	}

	// 4. Pressing `m` on the starred row in Pinned Links & Files unstars it in SQLite and removes it.
	pinTbl := a.projectCtrl().tables[projectPinnedTable]
	for i, r := range pinTbl.Rows {
		if r.Ref != nil && r.Ref.ID == ctxRec.ID {
			pinTbl.SelectedIdx = i
			break
		}
	}
	d.Keys("m")
	afterUnstar, _ := a.Store.Get(ctxRec.Ref())
	if afterUnstar.IsStarred() {
		t.Fatalf("second m did not unstar context record in SQLite")
	}
	wsAfter := a.collectProjectWorkspaceData(f.proj.Text("title"), &projRef)
	for _, it := range wsAfter.pinnedItems {
		if it.RecordID == ctxRec.ID {
			t.Fatalf("unstarred context item %s still present in pinnedItems", ctxRec.ID)
		}
	}

	// 5. Starring from project.nav context node stars the context item (not the project task).
	d.Keys("esc")
	a.Shell.State = withFocus(a.State(), projectNavPrefix+"context")
	a.viewMemo.gen++
	_ = d.Frame()
	d.Keys("space")
	navFocus := projectNavNodePrefix + "context." + ctxRec.ID
	a.Shell.State = withFocus(a.State(), navFocus)
	a.viewMemo.gen++
	_ = d.Frame()
	d.Keys("m")
	navStarred, _ := a.Store.Get(ctxRec.Ref())
	if !navStarred.IsStarred() {
		t.Fatalf("m on project.nav context node did not star context record")
	}
	projAfterNav, _ := a.Store.Get(f.proj.Ref())
	if projAfterNav.IsStarred() {
		t.Fatalf("m on project.nav context node accidentally starred parent project task")
	}
}
