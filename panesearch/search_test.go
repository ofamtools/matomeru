package panesearch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
)

func TestMultiSourceSearchRankingAndSnippets(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	actor := core.Actor{Kind: "user", ID: "test"}

	proj, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Aurora"},
	}, actor)
	if err != nil {
		t.Fatal(err)
	}

	// Five panes each matching the same keyword "zephyr" at a different ranking tier:
	// 1) meta (title), 2) chat/Q&A, 3) page/record, 4) live scrollback, 5) deep transcript.
	pMeta, _, _ := store.Create(data.CreateInput{
		Kind:   data.KindPane,
		Fields: map[string]any{"title": "zephyr control", "harness": "claude", "task": proj.Ref()},
	}, actor)
	pChat, _, _ := store.Create(data.CreateInput{
		Kind:   data.KindPane,
		Fields: map[string]any{"title": "chat worker", "harness": "claude", "task": proj.Ref()},
	}, actor)
	pRec, _, _ := store.Create(data.CreateInput{
		Kind:   data.KindPane,
		Fields: map[string]any{"title": "record worker", "harness": "codex", "task": proj.Ref()},
	}, actor)
	pScroll, _, _ := store.Create(data.CreateInput{
		Kind:   data.KindPane,
		Fields: map[string]any{"title": "scroll worker", "harness": "zsh", "task": proj.Ref()},
	}, actor)

	transcriptFile := filepath.Join(t.TempDir(), "deep.jsonl")
	if err := os.WriteFile(transcriptFile, []byte(
		`{"type":"user","message":{"role":"user","content":"line before"}}`+"\n"+
			`{"type":"assistant","message":{"role":"assistant","content":"deep transcript mentions zephyr protocol"}}`+"\n"+
			`{"type":"assistant","message":{"role":"assistant","content":"line after"}}`+"\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	pTrans, _, _ := store.Create(data.CreateInput{
		Kind:   data.KindPane,
		Fields: map[string]any{"title": "transcript worker", "harness": "claude", "transcriptPath": transcriptFile},
	}, actor)

	// Record-level hits on pRec: ui.page, context, command, note, document.
	_, _, _ = store.Create(data.CreateInput{
		Kind: data.KindPage,
		Fields: map[string]any{
			"title": "Design Plan",
			"pane":  pRec.Ref(),
			"root": map[string]any{
				"kind":  "stack",
				"title": "Architecture",
				"children": []any{
					map[string]any{
						"kind":  "markdown",
						"props": map[string]any{"markdown": "intro\nzephyr rollout checklist\noutro"},
					},
				},
			},
		},
	}, actor)
	_, _, _ = store.Create(data.CreateInput{
		Kind:   data.KindContext,
		Fields: map[string]any{"title": "PR link", "value": "https://github.com/org/repo/pull/77", "pane": pRec.Ref()},
	}, actor)
	_, _, _ = store.Create(data.CreateInput{
		Kind:   data.KindCommand,
		Fields: map[string]any{"title": "Run benchmark", "command": "go test -bench=BenchAlpha", "pane": pRec.Ref()},
	}, actor)
	note, _, _ := store.Create(data.CreateInput{
		Kind:   data.KindNote,
		Fields: map[string]any{"title": "Ops Note", "body": "remember canary flag"},
	}, actor)
	_, _, _ = store.Relate(data.RelateInput{From: note.Ref(), Kind: data.RelAbout, To: pRec.Ref()}, actor)

	// Q&A record on pChat.
	_, _, _ = store.Create(data.CreateInput{
		Kind:   data.KindQuestion,
		Fields: map[string]any{"prompt": "Ship zephyr now?", "answer": "yes, after tests", "pane": pChat.Ref()},
	}, actor)

	cache := NewCache()
	docs, err := BuildDocuments(store, BuildOptions{
		IncludeTranscripts: true,
		Cache:              cache,
		Frames: map[string]LiveFrame{
			pScroll.ID: {
				Revision: 7,
				Lines: []string{
					"$ make build",
					"compiling zephyr daemon",
					"done in 1.2s",
				},
			},
		},
		Turns: map[string][]Turn{
			pChat.ID: {{Prompt: "Check zephyr status", Response: "All healthy", At: time.Now()}},
		},
	})
	if err != nil {
		t.Fatalf("BuildDocuments: %v", err)
	}

	// Verify ranking order across all 5 tiers.
	res := Search(docs, "zephyr", SearchOptions{Source: SourceAll, Mode: traits.MatchSubstring})
	if res.Total != 5 {
		t.Fatalf("total = %d, want 5: %+v", res.Total, res.Results)
	}
	wantOrder := []string{pMeta.ID, pChat.ID, pRec.ID, pScroll.ID, pTrans.ID}
	for i, wantID := range wantOrder {
		if res.Results[i].PaneID != wantID {
			t.Errorf("rank[%d] = %s (%s, score=%d), want %s", i, res.Results[i].PaneID, res.Results[i].Title, res.Results[i].Score, wantID)
		}
	}

	// Verify snippets carry [source] badge and surrounding context lines.
	scrollRes := res.Results[3]
	if len(scrollRes.Snippets) == 0 || scrollRes.Snippets[0].Badge != BadgeScrollback {
		t.Fatalf("scrollback snippets = %+v", scrollRes.Snippets)
	}
	if len(scrollRes.Snippets[0].Before) != 1 || scrollRes.Snippets[0].Before[0] != "$ make build" {
		t.Fatalf("scrollback before context = %+v", scrollRes.Snippets[0].Before)
	}
	if len(scrollRes.Snippets[0].After) != 1 || scrollRes.Snippets[0].After[0] != "done in 1.2s" {
		t.Fatalf("scrollback after context = %+v", scrollRes.Snippets[0].After)
	}

	// Verify source filters isolate each category cleanly.
	onlyScroll := Search(docs, "zephyr", SearchOptions{Source: SourceScrollback})
	if onlyScroll.Total != 1 || onlyScroll.Results[0].PaneID != pScroll.ID {
		t.Fatalf("scrollback filter = %+v", onlyScroll.Results)
	}
	onlyRecords := Search(docs, "zephyr", SearchOptions{Source: SourceRecords})
	if onlyRecords.Total != 2 { // pChat (QA) + pRec (Page)
		t.Fatalf("records filter = %+v, want 2 (qa + page)", onlyRecords.Results)
	}
	onlyChat := Search(docs, "zephyr", SearchOptions{Source: SourceChat})
	if onlyChat.Total != 2 { // pChat + pTrans
		t.Fatalf("chat filter = %+v, want 2 (chat/qa + transcript)", onlyChat.Results)
	}

	// Verify linked note, command, and context records are indexed.
	for _, tc := range []struct {
		query string
		badge string
	}{
		{"canary flag", BadgeNote},
		{"BenchAlpha", BadgeCmd},
		{"pull/77", BadgeContext},
	} {
		r := Search(docs, tc.query, SearchOptions{Source: SourceRecords})
		if r.Total != 1 || r.Results[0].PaneID != pRec.ID || len(r.Results[0].Snippets) == 0 || r.Results[0].Snippets[0].Badge != tc.badge {
			t.Errorf("query %q = %+v, want pane %s badge %s", tc.query, r.Results, pRec.ID, tc.badge)
		}
	}

	// Verify fuzzy & regex match modes.
	fuzzy := Search(docs, "zphr", SearchOptions{Mode: traits.MatchFuzzy})
	if fuzzy.Total != 5 {
		t.Fatalf("fuzzy total = %d, want 5", fuzzy.Total)
	}
	regex := Search(docs, `pull/\d+`, SearchOptions{Mode: traits.MatchRegex})
	if regex.Total != 1 || regex.Results[0].PaneID != pRec.ID {
		t.Fatalf("regex = %+v", regex.Results)
	}
	invalid := Search(docs, `[unclosed`, SearchOptions{Mode: traits.MatchRegex})
	if invalid.FilterErr == "" {
		t.Fatal("expected FilterErr for invalid regex")
	}

	// Verify FrameLines cache hit by (paneID, revision).
	f1 := cache.FrameLines(pScroll.ID, 7, []string{"ignored on cache hit"})
	if len(f1) != 3 || !strings.Contains(f1[1], "zephyr") {
		t.Fatalf("expected FrameLines cache hit for rev 7, got %v", f1)
	}
}
