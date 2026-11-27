package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ptyhost"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func TestProjectDurableDeltaSkipsContextOnNoteAndLaneWrite(t *testing.T) {
	f := setupPerfWorkspaceFixture(t)
	a := f.app
	projRef := f.projects[0].Ref()
	pc := a.projectCtrl()

	ws1 := a.collectProjectWorkspaceData("Project 00", &projRef)
	if len(ws1.contextItems) == 0 {
		t.Fatal("expected context items in Project 00")
	}
	ctxBuilds1 := pc.contextBuilds
	deltaBefore := pc.durableDeltaRefreshes
	ctxPtr1 := &ws1.contextItems[0]

	// 1. External note write via AppendTaskNote (touches note + task + RelAbout) must NOT rebuild context!
	if _, err := a.Store.AppendTaskNote(data.AppendTaskNoteInput{
		TaskID: projRef.ID, Body: "Delta note test", AuthorKind: "agent",
	}, core.Actor{Kind: "agent", ID: "worker"}); err != nil {
		t.Fatal(err)
	}
	wsAfterNote := a.collectProjectWorkspaceData("Project 00", &projRef)
	if len(wsAfterNote.notes) != len(ws1.notes)+1 {
		t.Fatalf("notes after external note = %d, want %d", len(wsAfterNote.notes), len(ws1.notes)+1)
	}
	if pc.contextBuilds != ctxBuilds1 {
		t.Fatalf("external note write rebuilt context (%d -> %d)", ctxBuilds1, pc.contextBuilds)
	}
	if pc.durableDeltaRefreshes <= deltaBefore {
		t.Fatalf("expected delta refresh on external note write, got %d", pc.durableDeltaRefreshes)
	}
	if &wsAfterNote.contextItems[0] != ctxPtr1 {
		t.Fatal("external note write replaced contextItems slice instead of reusing it")
	}

	// 2. External lane status patch must NOT rebuild context!
	if _, _, err := a.Store.Patch(data.PatchInput{
		Ref:    f.lanes[0].Ref(),
		Fields: map[string]any{"laneStatus": "review"},
	}, core.Actor{Kind: "agent", ID: "worker"}); err != nil {
		t.Fatal(err)
	}
	wsAfterLane := a.collectProjectWorkspaceData("Project 00", &projRef)
	if pc.contextBuilds != ctxBuilds1 {
		t.Fatalf("external lane patch rebuilt context (%d -> %d)", ctxBuilds1, pc.contextBuilds)
	}
	if &wsAfterLane.contextItems[0] != ctxPtr1 {
		t.Fatal("external lane patch replaced contextItems slice instead of reusing it")
	}

	// 3. External context write MUST rebuild context and surface the new item!
	if _, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindContext,
		Fields: map[string]any{
			"title":       "Delta Fresh Context",
			"value":       "https://example.com/delta-fresh",
			"contextKind": "url",
			"task":        projRef,
		},
	}, core.Actor{Kind: "agent", ID: "worker"}); err != nil {
		t.Fatal(err)
	}
	wsAfterCtx := a.collectProjectWorkspaceData("Project 00", &projRef)
	if pc.contextBuilds != ctxBuilds1+1 {
		t.Fatalf("external context write contextBuilds = %d, want %d", pc.contextBuilds, ctxBuilds1+1)
	}
	if len(wsAfterCtx.contextItems) != len(ws1.contextItems)+1 {
		t.Fatalf("contextItems after create = %d, want %d", len(wsAfterCtx.contextItems), len(ws1.contextItems)+1)
	}
}

func TestInactiveProjectTabUnreadDoesNotBuildDurableSnapshot(t *testing.T) {
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	projA, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "ActiveTab"}}, User)
	if err != nil {
		t.Fatal(err)
	}
	projB, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "InactiveTab"}}, User)
	if err != nil {
		t.Fatal(err)
	}
	paneB, _, err := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{"title": "pane-b", "task": projB.Ref()}}, User)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 200 {
		if _, _, err := store.Create(data.CreateInput{
			Kind: data.KindContext,
			Fields: map[string]any{
				"title":       fmt.Sprintf("B ctx %d", i),
				"value":       fmt.Sprintf("https://example.com/b/%d", i),
				"contextKind": "url",
				"task":        projB.Ref(),
			},
		}, User); err != nil {
			t.Fatal(err)
		}
	}

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 140, 40)

	a.ensureProjectTabOpen(projA.ID)
	a.ensureProjectTabOpen(projB.ID)
	d.Keys("6") // focus ActiveTab (projA)
	pc := a.projectCtrl()
	pc.clearWorkspaceCaches("")
	pc.durableBuilds = 0
	a.viewMemo.tabsKey = ""
	a.viewMemo.gen++
	root := a.View(a.State())

	// Only ActiveTab should have built a full durable snapshot; InactiveTab must NOT be in durableCache!
	if pc.durableBuilds != 1 {
		t.Fatalf("durableBuilds with 1 active + 1 inactive tab = %d, want 1", pc.durableBuilds)
	}
	if _, loadedB := pc.durableCache[projB.ID]; loadedB {
		t.Fatal("inactive background Project Tab loaded full durableCache snapshot")
	}
	tabB := root.Find("space." + projectTabID(projB.ID))
	if tabB == nil || tabB.Unread {
		t.Fatalf("inactive tab B initially Unread = %v, want false", tabB != nil && tabB.Unread)
	}

	// External agent note on InactiveTab flips tab B Unread=true without loading durableCache!
	if _, err := store.AppendTaskNote(data.AppendTaskNoteInput{
		TaskID: projB.ID, Body: "Agent update on B", AuthorKind: "agent",
	}, core.Actor{Kind: "agent", ID: "worker"}); err != nil {
		t.Fatal(err)
	}
	a.viewMemo.tabsKey = ""
	a.viewMemo.gen++
	root = a.View(a.State())
	tabB = root.Find("space." + projectTabID(projB.ID))
	if tabB == nil || !tabB.Unread {
		t.Fatal("inactive tab B did not become Unread after agent note")
	}
	if _, loadedB := pc.durableCache[projB.ID]; loadedB {
		t.Fatal("inactive tab B loaded full durableCache after external note")
	}

	// Live telemetry needs_you on InactiveTab pane also marks tab B unread without loading durableCache.
	if _, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "InactiveTab2"}}, User); err != nil {
		t.Fatal(err)
	}
	a.setTelemetry(&daemon.TelemetryResult{
		Telemetry: map[string]*daemon.PaneTelemetry{
			paneB.ID: {PaneID: paneB.ID, State: daemon.StateNeedsYou},
		},
	}, false)
	if !a.projectTabHasUnread(projB.ID, "InactiveTab") {
		t.Fatal("inactive tab B should report unread on needs_you telemetry")
	}
	if _, loadedB := pc.durableCache[projB.ID]; loadedB {
		t.Fatal("inactive tab B loaded full durableCache on telemetry check")
	}
}

type stubLivePTYHost struct {
	written map[string][]byte
	frames  map[string]ptyhost.Frame
}

func newStubLivePTYHost() *stubLivePTYHost {
	return &stubLivePTYHost{
		written: map[string][]byte{},
		frames:  map[string]ptyhost.Frame{},
	}
}

func (h *stubLivePTYHost) Open(id, _, _ string, cols, rows int) error {
	h.frames[id] = ptyhost.Frame{Lines: []string{"$ "}, Cols: max(cols, 80), Rows: max(rows, 24), Revision: 1}
	return nil
}
func (h *stubLivePTYHost) Write(id string, p []byte) error {
	h.written[id] = append(h.written[id], p...)
	return nil
}
func (h *stubLivePTYHost) Resize(id string, cols, rows int) error {
	f := h.frames[id]
	f.Cols, f.Rows = cols, rows
	h.frames[id] = f
	return nil
}
func (h *stubLivePTYHost) Snapshot(id string) (ptyhost.Frame, bool) {
	f, ok := h.frames[id]
	return f, ok
}
func (h *stubLivePTYHost) Close(id string) error { delete(h.frames, id); return nil }
func (h *stubLivePTYHost) CloseAll()             { clear(h.frames) }

func TestCmdTermInputLivePassthroughSkipsPortalRebuild(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	proj, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "TermProj"}}, User)
	if err != nil {
		t.Fatal(err)
	}
	pane, _, err := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{"title": "live-pane", "task": proj.Ref(), "cwd": "/tmp"}}, User)
	if err != nil {
		t.Fatal(err)
	}

	host := newStubLivePTYHost()
	a, err := New(store, Options{FS: smokeFS(), Host: host})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 140, 40)

	// Open the project tab and attach live into its child pane terminal.
	a.ensureProjectTabOpen(proj.ID)
	d.Keys("6")
	a.Shell.State = a.openProjectChildPane(a.State(), proj.ID, pane.ID, true)
	a.viewMemo.gen++
	_ = a.View(a.State())
	_ = d.Frame()
	if a.Mode() != ModeTerminal || !a.hasSession(pane.ID) {
		t.Fatalf("expected ModeTerminal with live session, got mode=%q live=%v", a.Mode(), a.hasSession(pane.ID))
	}

	genBefore := a.viewMemo.gen
	buildsBefore := a.viewMemo.builds
	tabsBefore := a.viewMemo.tabsBuilds

	for _, ch := range "hello" {
		d.Send(core.CommandEvent{Command: core.Command{
			ID:     CmdTermInput,
			Args:   map[string]any{"key": string(ch), "text": string(ch)},
			Source: "terminal",
			Actor:  User,
		}})
	}

	if got := string(host.written[pane.ID]); got != "hello" {
		t.Fatalf("PTY bytes = %q, want %q", got, "hello")
	}
	if a.viewMemo.gen != genBefore {
		t.Fatalf("live CmdTermInput bumped viewMemo.gen (%d -> %d)", genBefore, a.viewMemo.gen)
	}
	if a.viewMemo.builds != buildsBefore {
		t.Fatalf("live CmdTermInput triggered a.build (%d -> %d)", buildsBefore, a.viewMemo.builds)
	}
	if a.viewMemo.tabsBuilds != tabsBefore {
		t.Fatalf("live CmdTermInput rebuilt tabs (%d -> %d)", tabsBefore, a.viewMemo.tabsBuilds)
	}
}

func TestProjectNavTelemetryTickAndHomeMinuteCache(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	proj, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "TelProj"}}, User)
	if err != nil {
		t.Fatal(err)
	}
	pane, _, err := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{"title": "claude: tel", "task": proj.Ref(), "runStatus": "idle"}}, User)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 27, 15, 0, 5, 0, time.UTC)
	a, err := New(store, Options{FS: smokeFS(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 140, 40)

	a.ensureProjectTabOpen(proj.ID)
	d.Keys("6")
	_ = d.Frame()

	pc := a.projectCtrl()
	navBefore := pc.navBuilds
	homeBefore := pc.homeBuilds

	// 1. Advancing clock within the same minute must NOT invalidate homeCache!
	now = now.Add(20 * time.Second)
	a.viewMemo.gen++
	_ = a.View(a.State())
	if pc.homeBuilds != homeBefore {
		t.Fatalf("20s clock tick inside same minute rebuilt homeCache (%d -> %d)", homeBefore, pc.homeBuilds)
	}

	// Advancing to next minute DOES refresh homeCache.
	now = now.Add(60 * time.Second)
	a.viewMemo.gen++
	_ = a.View(a.State())
	if pc.homeBuilds != homeBefore+1 {
		t.Fatalf("next-minute tick homeBuilds = %d, want %d", pc.homeBuilds, homeBefore+1)
	}

	// 2. Telemetry state change (idle -> needs_you -> running) must NOT rebuild full navCache!
	a.setTelemetry(&daemon.TelemetryResult{
		Telemetry: map[string]*daemon.PaneTelemetry{
			pane.ID: {PaneID: pane.ID, State: daemon.StateNeedsYou},
		},
	}, false)
	a.viewMemo.gen++
	root := a.View(a.State())
	if pc.navBuilds != navBefore {
		t.Fatalf("telemetry needs_you tick rebuilt full navCache (%d -> %d)", navBefore, pc.navBuilds)
	}
	panesSec := root.Find(projectNavPrefix + "panes")
	if panesSec == nil || panesSec.Facts["unreadCount"] != 1 {
		t.Fatalf("panes nav section did not reflect needs_you unread after cached patch: %+v", panesSec)
	}

	a.setTelemetry(&daemon.TelemetryResult{
		Telemetry: map[string]*daemon.PaneTelemetry{
			pane.ID: {PaneID: pane.ID, State: daemon.StateRunning},
		},
	}, false)
	a.viewMemo.gen++
	root = a.View(a.State())
	if pc.navBuilds != navBefore {
		t.Fatalf("telemetry running tick rebuilt full navCache (%d -> %d)", navBefore, pc.navBuilds)
	}
	panesSec = root.Find(projectNavPrefix + "panes")
	if panesSec == nil || panesSec.Facts["unreadCount"] != 0 {
		t.Fatalf("panes nav section did not clear unread after running transition: %+v", panesSec)
	}
}

func TestProjectSectionSelectFilterAndSearchSingleBuild(t *testing.T) {
	f := setupPerfWorkspaceFixture(t)
	a := f.app
	d := NewDriver(a, 140, 40)

	projID := f.projects[0].ID
	a.ensureProjectTabOpen(projID)
	d.Keys("6")
	_ = d.Frame()

	// Hover the "context" section header in project.nav first (just like moving via j/k).
	a.Shell.State = withFocus(a.State(), projectNavPrefix+"context")
	a.viewMemo.gen++
	_ = a.View(a.State())
	_ = d.Frame()

	// 1. Pressing enter on already-hovered section header must do <= 1 build!
	beforeEnter := a.viewMemo.builds
	d.Keys("enter")
	_ = d.Frame()
	if got := a.viewMemo.builds - beforeEnter; got != 1 {
		t.Fatalf("enter on hovered project.nav section did %d builds, want 1", got)
	}
	if a.Focus() != projectContextTable {
		t.Fatalf("enter on context section focused %q, want %q", a.Focus(), projectContextTable)
	}

	// 2. Typing in Filter (/) on project.context.table must do 1 build per keystroke (not 2)!
	d.Keys("/")
	_ = d.Frame()
	for _, ch := range "proj" {
		before := a.viewMemo.builds
		d.Type(string(ch))
		_ = d.Frame()
		if got := a.viewMemo.builds - before; got != 1 {
			t.Fatalf("filter key %q did %d builds, want 1", string(ch), got)
		}
	}
	d.Keys("esc")
	_ = d.Frame()

	// 3. Typing in Page Search (\) must do 1 build per keystroke (not 2)!
	d.Keys(`\`)
	_ = d.Frame()
	for _, ch := range "ctx" {
		before := a.viewMemo.builds
		d.Type(string(ch))
		_ = d.Frame()
		if got := a.viewMemo.builds - before; got != 1 {
			t.Fatalf("page search key %q did %d builds, want 1", string(ch), got)
		}
	}
	d.Keys("esc")
}

func TestProjectNotesAndDocsResolveWorkspaceRefTitlesAndSyncPath(t *testing.T) {
	f := setupPerfWorkspaceFixture(t)
	a := f.app
	projRef := f.projects[0].Ref()
	ws := a.collectProjectWorkspaceData("Project 00", &projRef)

	// Verify notes section resolves target labels for all notes (including RelAbout-only notes).
	relOnlyNote, _, err := a.Store.Create(data.CreateInput{
		Kind:   data.KindNote,
		Fields: map[string]any{"title": "RelOnly Note", "body": "via rel", "authorKind": "owner"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Store.Relate(data.RelateInput{From: relOnlyNote.Ref(), Kind: data.RelAbout, To: f.subtasks[0].Ref()}, User); err != nil {
		t.Fatal(err)
	}
	ws = a.collectProjectWorkspaceData("Project 00", &projRef)
	_ = a.projectSectionNotes(ws)
	notesTbl := a.projectCtrl().tables[projectNotesTable]
	if notesTbl == nil || len(notesTbl.Rows) == 0 {
		t.Fatal("expected populated project notes table")
	}
	foundRelOnly := false
	for _, r := range notesTbl.Rows {
		if r.Ref != nil && r.Ref.ID == relOnlyNote.ID {
			if got := data.Text(r.Cells["target"]); got != f.subtasks[0].Text("title") {
				t.Fatalf("RelAbout note target = %q, want %q", got, f.subtasks[0].Text("title"))
			}
			foundRelOnly = true
		}
	}
	if !foundRelOnly {
		t.Fatal("RelAbout-only note missing from project notes table")
	}

	// Verify syncPath skips Store.Get for tasks and resolves filePath for documents.
	d := NewDriver(a, 140, 40)
	a.ensureProjectTabOpen(projRef.ID)
	d.Keys("6")
	_ = d.Frame()
	if reg := a.Register(core.RegPath); !strings.HasPrefix(reg, "task:") {
		t.Fatalf("syncPath on project task = %q, want task:...", reg)
	}
	a.Shell.State = withFocus(a.State(), projectNavPrefix+"documents")
	d.Keys("enter")
	_ = d.Frame()
	if reg := a.Register(core.RegPath); !strings.HasSuffix(reg, ".md") {
		t.Fatalf("syncPath on document row = %q, want .md filePath", reg)
	}
	_ = ui.KindPanel
}

func setupPortalBenchFixture(b *testing.B) (*App, *Driver, []data.Record, []data.Record) {
	b.Helper()
	store, err := data.Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })

	mk := func(kind string, fields map[string]any) data.Record {
		rec, _, err := store.Create(data.CreateInput{Kind: kind, Fields: fields}, User)
		if err != nil {
			b.Fatalf("create %s: %v", kind, err)
		}
		return *rec
	}

	var projects, panes []data.Record
	for p := range 3 {
		proj := mk(data.KindTask, map[string]any{
			"title":      fmt.Sprintf("BenchProject %02d", p),
			"workStatus": "active",
		})
		projects = append(projects, proj)
		for i := range 30 {
			mk(data.KindTask, map[string]any{
				"title":      fmt.Sprintf("Subtask %d-%02d", p, i),
				"parent":     proj.Ref(),
				"workStatus": "todo",
			})
		}
		pane := mk(data.KindPane, map[string]any{
			"title": fmt.Sprintf("claude: bench-%d", p),
			"task":  proj.Ref(),
			"cwd":   fmt.Sprintf("/tmp/bench-%d", p),
		})
		panes = append(panes, pane)
		for i := range 5 {
			mk(data.KindLane, map[string]any{
				"title":      fmt.Sprintf("Lane %d-%d", p, i),
				"task":       proj.Ref(),
				"laneStatus": "running",
			})
			mk(data.KindNote, map[string]any{
				"title":      fmt.Sprintf("Note %d-%d", p, i),
				"body":       "Bench note body",
				"task":       proj.Ref(),
				"authorKind": "owner",
			})
		}
		for i := range 500 {
			mk(data.KindContext, map[string]any{
				"title":       fmt.Sprintf("Ctx %d-%04d", p, i),
				"value":       fmt.Sprintf("https://example.com/p/%d/item/%d", p, i),
				"contextKind": "url",
				"source":      "tool",
				"pane":        pane.Ref(),
				"task":        proj.Ref(),
				"official":    i%100 == 0,
			})
		}
	}

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { a.Close() })
	d := NewDriver(a, 180, 48)
	for _, p := range projects {
		a.ensureProjectTabOpen(p.ID)
	}
	d.Keys("6")
	_ = d.Frame()
	return a, d, projects, panes
}

func BenchmarkPortalNavMotion(b *testing.B) {
	a, d, _, _ := setupPortalBenchFixture(b)
	pc := a.projectCtrl()
	dur0, bld0, nav0 := pc.durableBuilds, a.viewMemo.builds, pc.navBuilds
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		if i%2 == 0 {
			d.Keys("j")
		} else {
			d.Keys("k")
		}
		_ = d.Frame()
	}
	b.StopTimer()
	b.ReportMetric(float64(pc.durableBuilds-dur0)/float64(b.N), "dur/op")
	b.ReportMetric(float64(a.viewMemo.builds-bld0)/float64(b.N), "bld/op")
	b.ReportMetric(float64(pc.navBuilds-nav0)/float64(b.N), "nav/op")
}

func BenchmarkPortalExternalNoteWriteWake(b *testing.B) {
	a, d, projects, _ := setupPortalBenchFixture(b)
	pc := a.projectCtrl()
	dur0, ctx0, bld0, nav0 := pc.durableBuilds, pc.contextBuilds, a.viewMemo.builds, pc.navBuilds
	agent := core.Actor{Kind: "agent", ID: "bench-worker"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		_, _ = a.Store.AppendTaskNote(data.AppendTaskNoteInput{
			TaskID: projects[0].ID, Body: fmt.Sprintf("bench note %d", i), AuthorKind: "agent",
		}, agent)
		a.Shell.Update(wakeMsg())
		_ = d.Frame()
	}
	b.StopTimer()
	b.ReportMetric(float64(pc.durableBuilds-dur0)/float64(b.N), "dur/op")
	b.ReportMetric(float64(pc.contextBuilds-ctx0)/float64(b.N), "ctx/op")
	b.ReportMetric(float64(a.viewMemo.builds-bld0)/float64(b.N), "bld/op")
	b.ReportMetric(float64(pc.navBuilds-nav0)/float64(b.N), "nav/op")
}

func BenchmarkPortalTelemetryStateTick(b *testing.B) {
	a, d, _, panes := setupPortalBenchFixture(b)
	pc := a.projectCtrl()
	dur0, bld0, nav0 := pc.durableBuilds, a.viewMemo.builds, pc.navBuilds
	paneID := panes[0].ID
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		state := daemon.StateRunning
		if i%2 == 0 {
			state = daemon.StateNeedsYou
		}
		a.setTelemetry(&daemon.TelemetryResult{
			Telemetry: map[string]*daemon.PaneTelemetry{
				paneID: {PaneID: paneID, State: state},
			},
		}, false)
		a.Shell.Update(wakeMsg())
		_ = d.Frame()
	}
	b.StopTimer()
	b.ReportMetric(float64(pc.durableBuilds-dur0)/float64(b.N), "dur/op")
	b.ReportMetric(float64(a.viewMemo.builds-bld0)/float64(b.N), "bld/op")
	b.ReportMetric(float64(pc.navBuilds-nav0)/float64(b.N), "nav/op")
}

func BenchmarkPortalContextFilterKey(b *testing.B) {
	a, d, _, _ := setupPortalBenchFixture(b)
	a.Shell.State = withFocus(a.State(), projectNavPrefix+"context")
	a.viewMemo.gen++
	_ = a.View(a.State())
	d.Keys("enter", "/")
	_ = d.Frame()
	bld0 := a.viewMemo.builds
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		if i%2 == 0 {
			d.Type("9")
		} else {
			d.Keys("backspace")
		}
		_ = d.Frame()
	}
	b.StopTimer()
	b.ReportMetric(float64(a.viewMemo.builds-bld0)/float64(b.N), "bld/op")
}
