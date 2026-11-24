package mcpui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/panesearch"
	"ofam.dev/ochazuke/core"
)

func TestPaneSearchMCPTool(t *testing.T) {
	f := setup(t)
	transcriptPath := filepath.Join(t.TempDir(), "mcp-pane.jsonl")
	if err := os.WriteFile(transcriptPath, []byte(
		`{"type":"assistant","message":{"role":"assistant","content":"mcp transcript needle ORION_99"}}`+"\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.store.Patch(data.PatchInput{
		Ref:    f.pane.Ref(),
		Fields: map[string]any{"transcriptPath": transcriptPath},
	}, owner); err != nil {
		t.Fatal(err)
	}
	_, _, _ = f.store.Create(data.CreateInput{
		Kind:   data.KindQuestion,
		Fields: map[string]any{"prompt": "Check ORION_QA?", "answer": "confirmed", "pane": f.pane.Ref()},
	}, owner)

	box := NewToolbox(f.store, nil)
	box.PaneSearch.LiveData = func() PaneSearchLiveSnapshot {
		return PaneSearchLiveSnapshot{
			Frames: map[string]panesearch.LiveFrame{
				f.pane.ID: {Revision: 1, Lines: []string{"live scrollback ORION_SCROLL"}},
			},
		}
	}

	if !box.HasTool(ToolPaneSearch) {
		t.Fatalf("Toolbox missing %s", ToolPaneSearch)
	}

	call := func(args map[string]any) *panesearch.SearchResult {
		t.Helper()
		raw, _ := json.Marshal(args)
		out, err := box.Call(context.Background(), core.Actor{Kind: "agent", ID: "mcp"}, ToolPaneSearch, raw)
		if err != nil {
			t.Fatalf("pane.search(%v): %v", args, err)
		}
		res, ok := out.(*panesearch.SearchResult)
		if !ok {
			t.Fatalf("unexpected result type %T", out)
		}
		return res
	}

	if res := call(map[string]any{"query": "ORION_99", "source": "chat"}); res.Total != 1 || res.Results[0].PaneID != f.pane.ID {
		t.Fatalf("chat transcript search = %+v", res)
	}
	if res := call(map[string]any{"query": "ORION_SCROLL", "source": "scrollback"}); res.Total != 1 || res.Results[0].Snippets[0].Badge != panesearch.BadgeScrollback {
		t.Fatalf("scrollback search = %+v", res)
	}
	if res := call(map[string]any{"query": "ORION_QA", "source": "records"}); res.Total != 1 || res.Results[0].Snippets[0].Badge != panesearch.BadgeQA {
		t.Fatalf("records search = %+v", res)
	}
	if res := call(map[string]any{"query": "ORION_99", "includeTranscripts": false}); res.Total != 0 {
		t.Fatalf("expected 0 when includeTranscripts=false, got %+v", res)
	}
}
