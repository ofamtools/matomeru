package app

import (
	"fmt"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// TestRescanGuardPaneWriteSkipsDurableContext is the anti-O(N) guard for the
// hottest write in a live store: a pane's runStatus/promptedAt churn must not
// rebuild a project's whole durable context list. A pane rename still must,
// because context rows show the pane title.
func TestRescanGuardPaneWriteSkipsDurableContext(t *testing.T) {
	f := setupPerfWorkspaceFixture(t)
	a := f.app
	proj := f.projects[0]
	projRef := proj.Ref()
	pane := f.panes[0] // task = Project 00
	const extra = 4000
	for i := range extra {
		if _, _, err := a.Store.Create(data.CreateInput{Kind: data.KindContext, Fields: map[string]any{
			"title": fmt.Sprintf("Bulk %04d", i), "value": fmt.Sprintf("https://example.com/bulk/%d", i),
			"canonKey": fmt.Sprintf("url:https://example.com/bulk/%d", i), "contextKind": "url",
			"source": "screen", "task": projRef, "pane": pane.Ref(),
		}}, User); err != nil {
			t.Fatal(err)
		}
	}
	pc := a.projectCtrl()
	ws := a.collectProjectWorkspaceData("Project 00", &projRef)
	if len(ws.contextItems) < extra {
		t.Fatalf("context items = %d, want >= %d", len(ws.contextItems), extra)
	}
	builds, deltas := pc.contextBuilds, pc.durableDeltaRefreshes
	agent := core.Actor{Kind: "agent", ID: "worker"}

	start := time.Now()
	for i, status := range []string{"running", "idle", "running"} {
		if _, _, err := a.Store.Patch(data.PatchInput{Ref: pane.Ref(), Fields: map[string]any{
			"runStatus": status, "promptedAt": time.Now().Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano),
		}}, agent); err != nil {
			t.Fatal(err)
		}
		a.collectProjectWorkspaceData("Project 00", &projRef)
	}
	t.Logf("3 pane status writes over %d context rows: %v", len(ws.contextItems), time.Since(start))
	if pc.contextBuilds != builds {
		t.Fatalf("pane status writes rebuilt durable context %d times over %d rows", pc.contextBuilds-builds, len(ws.contextItems))
	}
	if pc.durableDeltaRefreshes <= deltas {
		t.Fatal("pane status writes did not take the delta refresh path")
	}

	if _, _, err := a.Store.Patch(data.PatchInput{Ref: pane.Ref(), Fields: map[string]any{"title": "renamed-pane"}}, agent); err != nil {
		t.Fatal(err)
	}
	renamed := a.collectProjectWorkspaceData("Project 00", &projRef)
	if pc.contextBuilds != builds+1 {
		t.Fatalf("pane rename contextBuilds = %d, want %d", pc.contextBuilds, builds+1)
	}
	for _, it := range renamed.contextItems {
		if it.PaneID == pane.ID && it.PaneTitle != "renamed-pane" {
			t.Fatalf("context item %s pane title = %q after rename", it.Key, it.PaneTitle)
		}
	}
}
