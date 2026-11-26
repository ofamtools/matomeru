package views

import (
	"testing"

	"ofam.dev/ochazuke/ui"
)

func TestZeroFieldModalViewPurePresentation(t *testing.T) {
	node := ZeroFieldModalView(ZeroFieldViewSpec{
		Title:         "Zero-Field Query · Tasks",
		Mode:          "filter",
		ModeTone:      "accent",
		SelectedField: "status",
		ActiveSettings: []ZeroFieldSettingPill{
			{ModeLabel: "FILTER", ValueLabel: "status=todo", RemoveHint: "xf", ActiveTone: "badge.accent", InactiveTone: "info"},
			{ModeLabel: "GROUP", ValueLabel: "flat list (implicit)", RemoveHint: "xg", Implicit: true, ActiveTone: "badge.success", InactiveTone: "success"},
		},
		ModeChips: []ZeroFieldChip{
			{Hint: "f", Label: "Filter", Active: true, ActiveTone: "badge.accent"},
			{Hint: "g", Label: "Group", ActiveTone: "badge.success"},
			{Hint: "s", Label: "Sort", ActiveTone: "badge.warning"},
		},
		FieldRows: [][]ZeroFieldChip{{{Hint: "e1", Label: "id"}, {Hint: "e2", Label: "status", Active: true}}},
		ValueChips: []ZeroFieldChip{
			{Hint: "q", Label: "todo", Active: true},
			{Hint: "w", Label: "done"},
		},
		MatchedCount: 3,
		TotalCount:   10,
	})
	if node.ID != ZeroFieldID || node.Scope != ScopeZeroField || node.Kind != ui.KindPanel {
		t.Fatalf("unexpected root node: %+v", node)
	}
	if node.Facts["zeroField"] != true || node.Facts["queryMode"] != "filter" {
		t.Fatalf("unexpected facts: %+v", node.Facts)
	}
	body := node.Find(ZeroFieldID + ".body")
	if body == nil {
		t.Fatal("missing body text node")
	}
	runs, _ := body.Props["runs"].([]ui.InlineRun)
	if len(runs) == 0 {
		t.Fatal("expected inline chip runs in body")
	}
}
