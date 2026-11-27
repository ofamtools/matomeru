package mcpui

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func TestCreatePageAcceptsRecordRefCardForms(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	router := New(store)
	rawArgs := json.RawMessage(`{
		"title": "Deep Link Board",
		"slug": "deep-link-board",
		"root": {
			"id": "board.root",
			"kind": "vstack",
			"children": [
				{
					"id": "q.lane.1",
					"kind": "card",
					"ref": {"kind": "lane", "id": "4f8e01412677"},
					"props": {"recordCard": true, "lines": 3}
				},
				{
					"id": "q.lane.2",
					"kind": "card",
					"ref": {"kind": "lane", "id": "5f9e01412678"},
					"props": {"hydrateRef": true}
				},
				{
					"id": "q.task.1",
					"kind": "record.card",
					"ref": {"kind": "task", "id": "task-123"}
				},
				{
					"id": "q.doc.1",
					"kind": "ref.card",
					"ref": {"kind": "document", "id": "doc-456"}
				}
			]
		}
	}`)

	resAny, err := router.Call(context.Background(), core.Actor{Kind: "agent", ID: "tester"}, ToolCreatePage, rawArgs)
	if err != nil {
		t.Fatalf("ui.create_page rejected ref-card JSON: %v", err)
	}
	res := resAny.(*PageResult)
	page, err := store.Get(res.Ref)
	if err != nil {
		t.Fatalf("get created page: %v", err)
	}
	root, err := PageRoot(*page)
	if err != nil {
		t.Fatalf("decode page root: %v", err)
	}
	for _, id := range []string{"q.lane.1", "q.lane.2", "q.task.1", "q.doc.1"} {
		n := root.Find(id)
		if n == nil {
			t.Fatalf("missing node %q in stored page root", id)
		}
		if !IsRecordCardNode(n) {
			t.Errorf("IsRecordCardNode(%q) = false, want true (node=%+v)", id, n)
		}
	}

	// Verify ToolSpecs exposes record.card, ref.card, and ref property.
	var foundCreate bool
	for _, sp := range router.ToolSpecs() {
		if sp.Name != ToolCreatePage {
			continue
		}
		foundCreate = true
		propsMap := sp.InputSchema["properties"].(map[string]any)
		rootSchema := propsMap["root"].(map[string]any)
		nodeProps := rootSchema["properties"].(map[string]any)
		kindEnum := nodeProps["kind"].(map[string]any)["enum"].([]string)
		if !slices.Contains(kindEnum, KindRecordCard) || !slices.Contains(kindEnum, KindRefCard) {
			t.Fatalf("ui.create_page node kind enum missing record.card/ref.card: %v", kindEnum)
		}
		if _, ok := nodeProps["ref"]; !ok {
			t.Fatalf("ui.create_page node schema missing ref property")
		}
	}
	if !foundCreate {
		t.Fatal("ToolCreatePage not found in ToolSpecs")
	}
}
