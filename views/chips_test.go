package views

import (
	"slices"
	"testing"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func TestChipStripViewFocusFactsAndWidth(t *testing.T) {
	ref := core.Ref{Kind: "sample", ID: "sample-1"}
	lineage := []string{"leaf", "root"}
	meta := map[string]string{"hint": "actual value"}
	strip := ChipStripView("strip", []ChipNodeSpec{
		{ID: "compact", Ref: ref, TargetKind: "leaf", TargetID: "value-1", Label: "Compact", Glyph: "*", Tone: "info", Compact: true, Lineage: lineage, VerbCount: 4, Meta: meta},
		{ID: "label", Ref: ref, TargetKind: "leaf", TargetID: "value-2", Label: "Named", Glyph: "+"},
	})
	lineage[0], meta["hint"] = "changed", "changed"
	compact := strip.Children[0]
	if got := compact.Props["text"]; got != "*" || compact.Box.FixedW != 1 || !compact.Focusable || compact.Tone != "info" {
		t.Fatalf("compact chip = %+v", compact)
	}
	if got := strip.Children[1].Props["text"]; got != "+ Named" {
		t.Fatalf("labelled chip = %q", got)
	}
	chain := ui.ContextChain(strip, "strip.compact", nil)
	got := chain[0]
	if got.Scope != ScopePointerChip || got.Subject == nil || *got.Subject != ref {
		t.Fatalf("compact focus frame = %+v", got)
	}
	if facts := got.Facts; facts["chipKind"] != "leaf" || facts["chipTarget"] != "value-1" || facts["chipCompact"] != true || facts["chipVerbCount"] != 4 ||
		!slices.Equal(facts["chipLineage"].([]string), []string{"leaf", "root"}) || facts["chipMeta"].(map[string]string)["hint"] != "actual value" {
		t.Fatalf("compact focus facts = %+v", facts)
	}
	_, solved := ui.NewRenderer(0).Render(strip, ui.DefaultDarkTheme(), "strip.compact", 40, 3)
	if rect := solved.Rects["strip.compact"]; rect.W != 1 || rect.H != 1 || !slices.Contains(solved.FocusOrder, "strip.compact") {
		t.Fatalf("compact chip solved to %+v with focus order %q", rect, solved.FocusOrder)
	}
}
