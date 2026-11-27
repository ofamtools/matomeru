package app

import (
	"testing"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
)

func TestProjectSnapshotDurableCachedAcrossLiveOverlayTicks(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	proj, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Apollo", "workStatus": "active"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Durable child", "parent": proj.Ref(), "workStatus": "active"},
	}, User); err != nil {
		t.Fatal(err)
	}
	pane, _, err := store.Create(data.CreateInput{
		Kind:   data.KindPane,
		Fields: map[string]any{"title": "claude: apollo", "task": proj.Ref(), "runStatus": "idle"},
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

	projRef := proj.Ref()
	pc := a.projectCtrl()

	// 1. Cold load builds both durable snapshot and live overlay once.
	ws1 := a.collectProjectWorkspaceData("Apollo", &projRef)
	if pc.durableBuilds != 1 || pc.liveBuilds != 1 {
		t.Fatalf("cold load builds = (%d, %d), want (1, 1)", pc.durableBuilds, pc.liveBuilds)
	}
	if len(ws1.tasks) != 1 || len(ws1.panes) != 1 {
		t.Fatalf("cold load counts tasks=%d panes=%d, want 1, 1", len(ws1.tasks), len(ws1.panes))
	}
	tasksPtr1 := &ws1.tasks[0]
	panesPtr1 := &ws1.panes[0]

	// 2. Repeated read with identical store revision & telemetrySeq hits both caches.
	wsSame := a.collectProjectWorkspaceData("Apollo", &projRef)
	if pc.durableBuilds != 1 || pc.liveBuilds != 1 {
		t.Fatalf("steady read rebuilt caches: durable=%d live=%d, want 1, 1", pc.durableBuilds, pc.liveBuilds)
	}
	if &wsSame.tasks[0] != tasksPtr1 || &wsSame.panes[0] != panesPtr1 {
		t.Fatal("steady read did not reuse durable snapshot slices")
	}

	// 3. Live daemon telemetry ticks refresh ONLY the volatile overlay, reusing durable snapshot.
	setTelemetry := func(state string) {
		a.rt.live.telemetryMu.Lock()
		if state == "" {
			a.rt.live.telemetry = nil
		} else {
			a.rt.live.telemetry = &daemon.TelemetryResult{
				Telemetry: map[string]*daemon.PaneTelemetry{
					pane.ID: {PaneID: pane.ID, State: state},
				},
			}
		}
		a.rt.live.telemetrySeq.Add(1)
		a.rt.live.telemetryMu.Unlock()
	}

	setTelemetry(daemon.StateRunning)
	wsRun := a.collectProjectWorkspaceData("Apollo", &projRef)
	if pc.durableBuilds != 1 || pc.liveBuilds != 2 {
		t.Fatalf("running tick builds = (%d, %d), want (1, 2)", pc.durableBuilds, pc.liveBuilds)
	}
	if &wsRun.tasks[0] != tasksPtr1 || &wsRun.panes[0] != panesPtr1 {
		t.Fatal("running telemetry tick rebuilt durable snapshot")
	}
	if wsRun.runningPanes != 1 || wsRun.needsYouPanes != 0 || wsRun.live.paneStatus[pane.ID] != daemon.StateRunning {
		t.Fatalf("running overlay = (running=%d needsYou=%d status=%q), want (1, 0, %q)",
			wsRun.runningPanes, wsRun.needsYouPanes, wsRun.live.paneStatus[pane.ID], daemon.StateRunning)
	}

	setTelemetry(daemon.StateNeedsYou)
	wsNeeds := a.collectProjectWorkspaceData("Apollo", &projRef)
	if pc.durableBuilds != 1 || pc.liveBuilds != 3 {
		t.Fatalf("needs_you tick builds = (%d, %d), want (1, 3)", pc.durableBuilds, pc.liveBuilds)
	}
	if &wsNeeds.tasks[0] != tasksPtr1 {
		t.Fatal("needs_you telemetry tick rebuilt durable snapshot")
	}
	if wsNeeds.needsYouPanes != 1 || wsNeeds.runningPanes != 0 || wsNeeds.unreadTotal != ws1.unreadTotal+1 || !wsNeeds.live.unreadPanes[pane.ID] {
		t.Fatalf("needs_you overlay = (running=%d needsYou=%d unread=%d paneUnread=%v), want (0, 1, %d, true)",
			wsNeeds.runningPanes, wsNeeds.needsYouPanes, wsNeeds.unreadTotal, wsNeeds.live.unreadPanes[pane.ID], ws1.unreadTotal+1)
	}

	// 4. Store mutation bumps storeRevision and immediately invalidates the durable snapshot.
	if _, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Second durable child", "parent": proj.Ref(), "workStatus": "active"},
	}, User); err != nil {
		t.Fatal(err)
	}

	wsAfterMut := a.collectProjectWorkspaceData("Apollo", &projRef)
	if pc.durableBuilds != 2 {
		t.Fatalf("store mutation durableBuilds = %d, want 2", pc.durableBuilds)
	}
	if len(wsAfterMut.tasks) != 2 {
		t.Fatalf("store mutation tasks = %d, want 2", len(wsAfterMut.tasks))
	}
	if wsAfterMut.needsYouPanes != 1 {
		t.Fatalf("live overlay lost needs_you state after durable rebuild: needsYou=%d", wsAfterMut.needsYouPanes)
	}
}
