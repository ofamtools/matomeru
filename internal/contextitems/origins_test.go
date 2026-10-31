package contextitems

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestOriginsFromLegacyProvenance(t *testing.T) {
	var legacy Item
	if err := json.Unmarshal([]byte(legacyRow), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Origin() != "scrollback" {
		t.Fatalf("legacy: %v", legacy.Origins())
	}
	for _, tc := range []struct {
		source Source
		want   string
	}{
		{Source{Kind: "turn", EntryKind: "prompt-submitted"}, "prompt"},
		{Source{Kind: "turn", EntryKind: "response-completed"}, "response"},
		{Source{Kind: "transcript", EntryKind: "thinking"}, "thinking"},
		{Source{Kind: "transcript", EntryKind: "reasoning"}, "thinking"},
		{Source{Kind: "transcript", EntryKind: "tool_call"}, "tool"},
		{Source{Kind: "transcript", EntryKind: "tool_result"}, "tool"},
		{Source{Kind: "transcript", EntryKind: "tool"}, "tool"},
		{Source{Kind: "transcript", EntryKind: "text"}, "transcript"},
		{Source{Kind: "transcript"}, "transcript"},
		{Source{Kind: "screen"}, "scrollback"},
		{Source{Kind: "history", EntryKind: "command"}, "command"},
		{Source{Kind: "history", EntryKind: "prompt"}, "prompt"},
		{Source{Kind: "history", EntryKind: "response-completed"}, "command"},
		{Source{Kind: "transcript", EntryKind: "prompt"}, "prompt"},
		{Source{Kind: "activity", EntryKind: "command"}, "command"},
		{Source{Kind: "activity", EntryKind: "response-completed"}, "unknown"},
		{Source{Kind: "", EntryKind: "response-completed"}, "unknown"},
		{Source{Kind: "create"}, "manual"}, {Source{Kind: "mtm"}, "manual"},
		{Source{Kind: "mcp", EntryKind: "prompt"}, "mcp"}, {Source{Kind: "unknown-kind"}, "unknown"},
	} {
		if got := (Item{Sources: []Source{tc.source}}).Origin(); got != tc.want {
			t.Errorf("%+v: %s, want %s", tc.source, got, tc.want)
		}
	}
	if got := (Item{Provenance: Provenance{TurnID: "ambiguous"}}).Origin(); got != "unknown" {
		t.Fatal(got)
	}
}

func TestMergeKeepsEveryOriginAcrossReload(t *testing.T) {
	db := legacyDB(t)
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for i, src := range []Source{
		{Kind: "scrollback", Window: "w"},
		{Kind: "turn", EntryKind: "response-completed", Window: "w"},
		{Kind: "turn", EntryKind: "prompt-submitted", Window: "w"},
		{Kind: "turn", EntryKind: "prompt-submitted", Window: "w"},
	} {
		written, added, err := s.Merge([]Item{{Kind: "link", Text: "https://example.com/both", Sources: []Source{src}}}, Owner{}, int64(i+1))
		if err != nil {
			t.Fatal(err)
		}
		if (added == 1) != (i == 0) {
			t.Fatal("duplicate row", added)
		}
		id = written[0].ID
	}
	s, err = Open(db)
	if err != nil {
		t.Fatal(err)
	}
	it, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(it.Sources) != 3 || it.Origin() != "prompt" || !slices.Equal(it.Origins(), []string{"prompt", "response", "scrollback"}) {
		t.Fatalf("%+v: %v", it, it.Origins())
	}
}
