package mcpui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

type fakePaneRuntime struct {
	opened  []PaneOpenRequest
	written map[string][]byte
	closed  []string
	lines   map[string][]string
}

func (f *fakePaneRuntime) Open(_ context.Context, req PaneOpenRequest) error {
	f.opened = append(f.opened, req)
	return nil
}

func (f *fakePaneRuntime) Write(_ context.Context, paneID string, b []byte) error {
	if f.written == nil {
		f.written = map[string][]byte{}
	}
	f.written[paneID] = append(f.written[paneID], b...)
	return nil
}

func (f *fakePaneRuntime) Snapshot(_ context.Context, paneID string) (PaneSnapshot, bool, error) {
	lines, ok := f.lines[paneID]
	if !ok {
		return PaneSnapshot{}, false, nil
	}
	return PaneSnapshot{Plain: lines, Cols: 120, Rows: 36}, true, nil
}

func (f *fakePaneRuntime) Status(_ context.Context, paneID string) (PaneLiveStatus, error) {
	return PaneLiveStatus{Live: true, HookState: "running", Cols: 120, Rows: 36}, nil
}

func (f *fakePaneRuntime) Close(_ context.Context, paneID string) error {
	f.closed = append(f.closed, paneID)
	return nil
}

func TestPaneSpawnSnapshotSendStatusCloseAndDataRelate(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	actor := core.Actor{Kind: "agent", ID: "coord"}
	proj, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{
		"title":      "Portal",
		"defaultDir": "/tmp/portal-repo",
	}}, actor)
	if err != nil {
		t.Fatal(err)
	}
	lane, _, err := store.Create(data.CreateInput{Kind: data.KindLane, Fields: map[string]any{
		"title": "Build CLI",
	}}, actor)
	if err != nil {
		t.Fatal(err)
	}

	rt := &fakePaneRuntime{lines: map[string][]string{}}
	box := NewToolbox(store, nil)
	box.Panes.Runtime = rt
	box.Panes.ResolveLaunch = func(harnessID, mcpURL, paneID, customCmd string) (string, map[string]string, error) {
		return harnessID + " --mcp " + mcpURL, map[string]string{"HARNESS": harnessID}, nil
	}
	box.Panes.MCPURL = func(paneID string) string { return "http://127.0.0.1:9999/mcp/" + paneID }

	// 1. pane.spawn associates project + lane, inherits defaultDir, injects MTM_PANE_ID & MTM_TASK_ID, and sends initial prompt.
	spawnRaw, _ := json.Marshal(map[string]any{
		"harness": "claude",
		"project": proj.ID,
		"lane":    lane.ID,
		"prompt":  "hello worker",
	})
	out, err := box.Call(context.Background(), actor, ToolPaneSpawn, spawnRaw)
	if err != nil {
		t.Fatalf("pane.spawn: %v", err)
	}
	res := out.(PaneSpawnResult)
	if !res.Spawned || res.Cwd != "/tmp/portal-repo" || res.LaneClaim == nil {
		t.Fatalf("unexpected spawn result: %+v", res)
	}
	if len(rt.opened) != 1 || rt.opened[0].Cwd != "/tmp/portal-repo" || rt.opened[0].Env[EnvPaneID] != res.Pane.ID || rt.opened[0].Env[EnvTaskID] != proj.ID {
		t.Fatalf("unexpected PTY open req: %+v", rt.opened)
	}
	if got := string(rt.written[res.Pane.ID]); got != "hello worker\r" {
		t.Fatalf("initial prompt written = %q, want %q", got, "hello worker\r")
	}
	// Lane inherited project task automatically.
	updatedLane, _ := store.Get(lane.Ref())
	if tRef, ok := updatedLane.RefField("task"); !ok || tRef != proj.Ref() {
		t.Fatalf("lane task = %v (ok=%v), want %v", tRef, ok, proj.Ref())
	}

	// 2. pane.snapshot + pane.send + pane.status + pane.close
	rt.lines[res.Pane.ID] = []string{"line 1", "line 2", "line 3"}
	snapRaw, _ := json.Marshal(map[string]any{"pane": res.Pane.ID, "lines": 2})
	snapOut, err := box.Call(context.Background(), actor, ToolPaneSnapshot, snapRaw)
	if err != nil {
		t.Fatalf("pane.snapshot: %v", err)
	}
	if s := snapOut.(PaneSnapshotResult); !s.Found || len(s.Lines) != 2 || s.Lines[0] != "line 2" {
		t.Fatalf("unexpected snapshot: %+v", s)
	}

	sendRaw, _ := json.Marshal(map[string]any{"pane": res.Pane.ID, "text": "next step"})
	if _, err := box.Call(context.Background(), actor, ToolPaneSend, sendRaw); err != nil {
		t.Fatalf("pane.send: %v", err)
	}
	if !strings.Contains(string(rt.written[res.Pane.ID]), "next step\r") {
		t.Fatalf("pane.send did not write text: %q", string(rt.written[res.Pane.ID]))
	}

	stRaw, _ := json.Marshal(map[string]any{"pane": res.Pane.ID})
	stOut, err := box.Call(context.Background(), actor, ToolPaneStatus, stRaw)
	if err != nil {
		t.Fatalf("pane.status: %v", err)
	}
	if st := stOut.(PaneStatusResult); !st.Runtime.Live || len(st.Lanes) != 1 || st.Task == nil || st.Task.ID != proj.ID {
		t.Fatalf("unexpected pane.status: %+v", st)
	}

	closeRaw, _ := json.Marshal(map[string]any{"pane": res.Pane.ID})
	closeOut, err := box.Call(context.Background(), actor, ToolPaneClose, closeRaw)
	if err != nil {
		t.Fatalf("pane.close: %v", err)
	}
	if cl := closeOut.(PaneCloseResult); !cl.Closed || cl.Pane == nil || cl.Pane.Text("runStatus") != "exited" {
		t.Fatalf("unexpected pane.close: %+v", cl)
	}

	// 3. data.relate & data.unrelate accept both {kind,id} objects and "kind:id" strings.
	relRaw, _ := json.Marshal(map[string]any{
		"from":     "lane:" + lane.ID,
		"relation": data.RelAbout,
		"to":       map[string]string{"kind": data.KindTask, "id": proj.ID},
	})
	if _, err := box.Call(context.Background(), actor, ToolRelate, relRaw); err != nil {
		t.Fatalf("data.relate: %v", err)
	}
	rels, _ := store.Related(lane.Ref(), data.RelAbout, data.Outgoing)
	if len(rels) != 1 || rels[0].To != proj.Ref() {
		t.Fatalf("expected 1 about relation, got %+v", rels)
	}
	if _, err := box.Call(context.Background(), actor, ToolUnrelate, relRaw); err != nil {
		t.Fatalf("data.unrelate: %v", err)
	}
	relsAfter, _ := store.Related(lane.Ref(), data.RelAbout, data.Outgoing)
	if len(relsAfter) != 0 {
		t.Fatalf("expected 0 relations after unrelate, got %+v", relsAfter)
	}
}
