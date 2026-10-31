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
		{Source{Kind: "transcript", EntryKind: "tool_result"}, "transcript"},
		{Source{Kind: "screen"}, "scrollback"},
		{Source{Kind: "history", EntryKind: "command"}, "command"},
		{Source{Kind: "history", EntryKind: "prompt"}, "prompt"},
		{Source{Kind: "transcript", EntryKind: "prompt"}, "prompt"},
		{Source{Kind: "activity", EntryKind: "command"}, "command"},
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
