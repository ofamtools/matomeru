package data

import (
	"testing"

	"ofam.dev/ochazuke/core"
)

func TestIsFinalRecordAcrossSchemasAndArchived(t *testing.T) {
	reg := DefaultSchemas()

	mustSchema := func(kind string) Schema {
		t.Helper()
		s, ok := reg.Get(kind)
		if !ok {
			t.Fatalf("missing schema %q", kind)
		}
		return s
	}

	taskSchema := mustSchema(KindTask)
	laneSchema := mustSchema(KindLane)
	paneSchema := mustSchema(KindPane)
	runSchema := mustSchema(KindRun)
	questionSchema := mustSchema(KindQuestion)

	cases := []struct {
		name   string
		schema Schema
		rec    Record
		want   bool
	}{
		{"task todo", taskSchema, Record{Kind: KindTask, Fields: map[string]any{"workStatus": "todo"}, ArchiveStatus: ArchiveActive}, false},
		{"task active", taskSchema, Record{Kind: KindTask, Fields: map[string]any{"workStatus": "active"}, ArchiveStatus: ArchiveActive}, false},
		{"task blocked", taskSchema, Record{Kind: KindTask, Fields: map[string]any{"workStatus": "blocked"}, ArchiveStatus: ArchiveActive}, false},
		{"task active with agentStatus done stays open", taskSchema, Record{Kind: KindTask, Fields: map[string]any{"workStatus": "active", "agentStatus": AgentStatusDone}, ArchiveStatus: ArchiveActive}, false},
		{"task done", taskSchema, Record{Kind: KindTask, Fields: map[string]any{"workStatus": "done"}, ArchiveStatus: ArchiveActive}, true},
		{"task cancelled fallback", taskSchema, Record{Kind: KindTask, Fields: map[string]any{"workStatus": "cancelled"}, ArchiveStatus: ArchiveActive}, true},
		{"task archived even if todo", taskSchema, Record{Kind: KindTask, Fields: map[string]any{"workStatus": "todo"}, ArchiveStatus: ArchiveArchived}, true},

		{"lane planned", laneSchema, Record{Kind: KindLane, Fields: map[string]any{"laneStatus": "planned"}, ArchiveStatus: ArchiveActive}, false},
		{"lane running", laneSchema, Record{Kind: KindLane, Fields: map[string]any{"laneStatus": "running"}, ArchiveStatus: ArchiveActive}, false},
		{"lane review", laneSchema, Record{Kind: KindLane, Fields: map[string]any{"laneStatus": "review"}, ArchiveStatus: ArchiveActive}, false},
		{"lane parked", laneSchema, Record{Kind: KindLane, Fields: map[string]any{"laneStatus": "parked"}, ArchiveStatus: ArchiveActive}, false},
		{"lane merged", laneSchema, Record{Kind: KindLane, Fields: map[string]any{"laneStatus": "merged"}, ArchiveStatus: ArchiveActive}, true},
		{"lane done", laneSchema, Record{Kind: KindLane, Fields: map[string]any{"laneStatus": "done"}, ArchiveStatus: ArchiveActive}, true},
		{"lane cancelled", laneSchema, Record{Kind: KindLane, Fields: map[string]any{"laneStatus": "cancelled"}, ArchiveStatus: ArchiveActive}, true},
		{"lane archived", laneSchema, Record{Kind: KindLane, Fields: map[string]any{"laneStatus": "running"}, ArchiveStatus: ArchiveArchived}, true},

		{"pane running", paneSchema, Record{Kind: KindPane, Fields: map[string]any{"runStatus": "running"}, ArchiveStatus: ArchiveActive}, false},
		{"pane idle", paneSchema, Record{Kind: KindPane, Fields: map[string]any{"runStatus": "idle"}, ArchiveStatus: ArchiveActive}, false},
		{"pane done", paneSchema, Record{Kind: KindPane, Fields: map[string]any{"runStatus": "done"}, ArchiveStatus: ArchiveActive}, true},
		{"pane exited", paneSchema, Record{Kind: KindPane, Fields: map[string]any{"runStatus": "exited"}, ArchiveStatus: ArchiveActive}, true},
		{"pane dead", paneSchema, Record{Kind: KindPane, Fields: map[string]any{"runStatus": "dead"}, ArchiveStatus: ArchiveActive}, true},

		{"run running", runSchema, Record{Kind: KindRun, Fields: map[string]any{"runStatus": "running"}, ArchiveStatus: ArchiveActive}, false},
		{"run failed", runSchema, Record{Kind: KindRun, Fields: map[string]any{"runStatus": "failed"}, ArchiveStatus: ArchiveActive}, true},
		{"run cancelled", runSchema, Record{Kind: KindRun, Fields: map[string]any{"runStatus": "cancelled"}, ArchiveStatus: ArchiveActive}, true},

		{"question open", questionSchema, Record{Kind: KindQuestion, Fields: map[string]any{"status": "open"}, ArchiveStatus: ArchiveActive}, false},
		{"question answered", questionSchema, Record{Kind: KindQuestion, Fields: map[string]any{"status": "answered"}, ArchiveStatus: ArchiveActive}, true},
		{"question dismissed", questionSchema, Record{Kind: KindQuestion, Fields: map[string]any{"status": "dismissed"}, ArchiveStatus: ArchiveActive}, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsFinalRecord(tc.schema, tc.rec); got != tc.want {
				t.Fatalf("IsFinalRecord(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestFilterTreeByFinalStatePreservesHierarchyAndContextOnly(t *testing.T) {
	reg := DefaultSchemas()
	laneSchema, _ := reg.Get(KindLane)

	lane := func(id, status string, parentID string) Record {
		fields := map[string]any{"title": id, "laneStatus": status}
		if parentID != "" {
			fields["parentLane"] = core.Ref{Kind: KindLane, ID: parentID}
		}
		return Record{Kind: KindLane, ID: id, Fields: fields, ArchiveStatus: ArchiveActive}
	}

	// Hierarchy:
	//   done-root (done)
	//     ├── active-child (running)
	//     └── merged-leaf (merged)
	//   active-root (running)
	//     ├── cancelled-child (cancelled)
	//     └── open-parent (planned)
	//           └── done-grandchild (done)
	//   closed-only (cancelled)
	recs := []Record{
		lane("done-root", "done", ""),
		lane("active-child", "running", "done-root"),
		lane("merged-leaf", "merged", "done-root"),
		lane("active-root", "running", ""),
		lane("cancelled-child", "cancelled", "active-root"),
		lane("open-parent", "planned", "active-root"),
		lane("done-grandchild", "done", "open-parent"),
		lane("closed-only", "cancelled", ""),
	}
	tree := BuildTree(recs, "parentLane", "", false)

	// 1. hide-final:
	//   - done-root kept as ContextOnly=true because active-child is running
	//   - merged-leaf, cancelled-child, done-grandchild, closed-only are hidden
	//   - active-root and open-parent are matched (ContextOnly=false)
	hidden := FilterTreeByFinalState(tree, laneSchema, FinalViewHideFinal)
	if len(hidden) != 2 {
		t.Fatalf("hide-final roots = %d, want 2 (done-root context + active-root)", len(hidden))
	}
	if hidden[0].Record.ID != "done-root" || !hidden[0].ContextOnly {
		t.Fatalf("hide-final root[0] = %q contextOnly=%v, want done-root contextOnly=true", hidden[0].Record.ID, hidden[0].ContextOnly)
	}
	if len(hidden[0].Children) != 1 || hidden[0].Children[0].Record.ID != "active-child" || hidden[0].Children[0].ContextOnly {
		t.Fatalf("hide-final done-root children = %+v, want [active-child (matched)]", hidden[0].Children)
	}
	if hidden[1].Record.ID != "active-root" || hidden[1].ContextOnly {
		t.Fatalf("hide-final root[1] = %q contextOnly=%v, want active-root contextOnly=false", hidden[1].Record.ID, hidden[1].ContextOnly)
	}
	if len(hidden[1].Children) != 1 || hidden[1].Children[0].Record.ID != "open-parent" || len(hidden[1].Children[0].Children) != 0 {
		t.Fatalf("hide-final active-root children = %+v, want [open-parent (no children)]", hidden[1].Children)
	}
	if matched, total := CountFilteredMatches(hidden); matched != 3 || total != 4 {
		t.Fatalf("hide-final counts = (%d, %d), want (3, 4)", matched, total)
	}

	// 2. only-final:
	//   - done-root matched (ContextOnly=false), active-child hidden, merged-leaf matched
	//   - active-root kept as ContextOnly=true -> cancelled-child (matched) & open-parent (ContextOnly=true) -> done-grandchild (matched)
	//   - closed-only matched (ContextOnly=false)
	only := FilterTreeByFinalState(tree, laneSchema, FinalViewOnlyFinal)
	if len(only) != 3 {
		t.Fatalf("only-final roots = %d, want 3", len(only))
	}
	if only[0].Record.ID != "done-root" || only[0].ContextOnly || len(only[0].Children) != 1 || only[0].Children[0].Record.ID != "merged-leaf" {
		t.Fatalf("only-final root[0] = %+v, want done-root(matched) -> [merged-leaf]", only[0])
	}
	if only[1].Record.ID != "active-root" || !only[1].ContextOnly {
		t.Fatalf("only-final root[1] = %q contextOnly=%v, want active-root contextOnly=true", only[1].Record.ID, only[1].ContextOnly)
	}
	if len(only[1].Children) != 2 {
		t.Fatalf("only-final active-root children = %d, want 2", len(only[1].Children))
	}
	if only[1].Children[0].Record.ID != "cancelled-child" || only[1].Children[0].ContextOnly {
		t.Fatalf("only-final cancelled-child = %+v, want matched", only[1].Children[0])
	}
	openAnc := only[1].Children[1]
	if openAnc.Record.ID != "open-parent" || !openAnc.ContextOnly || len(openAnc.Children) != 1 || openAnc.Children[0].Record.ID != "done-grandchild" || openAnc.Children[0].ContextOnly {
		t.Fatalf("only-final open-parent chain = %+v, want open-parent(context) -> done-grandchild(matched)", openAnc)
	}
	if only[2].Record.ID != "closed-only" || only[2].ContextOnly {
		t.Fatalf("only-final root[2] = %+v, want closed-only(matched)", only[2])
	}
	if matched, total := CountFilteredMatches(only); matched != 5 || total != 7 {
		t.Fatalf("only-final counts = (%d, %d), want (5, 7)", matched, total)
	}
	ctxIDs := CollectContextOnlyIDs(only)
	if !ctxIDs["active-root"] || !ctxIDs["open-parent"] || len(ctxIDs) != 2 {
		t.Fatalf("only-final contextIDs = %v, want {active-root, open-parent}", ctxIDs)
	}

	// 3. Alias normalization: "active" maps to FinalViewHideFinal.
	if got := NormalizeFinalViewMode("active"); got != FinalViewHideFinal {
		t.Fatalf("NormalizeFinalViewMode(active) = %q, want %q", got, FinalViewHideFinal)
	}
}
