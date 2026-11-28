package views

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func TestRecordRefCardExactThreeLinesUnderSpaciousTheme(t *testing.T) {
	laneRef := core.Ref{Kind: "lane", ID: "4f8e01412677"}
	taskRef := core.Ref{Kind: "task", ID: "99aabbcc1122"}
	missingRef := core.Ref{Kind: "lane", ID: "deadbeef0000"}

	laneCard := RecordRefCardView(RecordRefCardSpec{
		ID:          "q.lane.1",
		Ref:         laneRef,
		Kind:        "lane",
		KindLabel:   "Lane",
		Title:       "App-Level Ref Card",
		StatusLabel: "Running",
		StatusIcon:  "▶",
		StatusTone:  "state.working",
		Summary:     "Hydrate live lane cards on ui.page",
		ProgressBar: CompactLaneBarRuns(2, 1, 1, 4),
		Progress:    "2/4 ▶1",
		Meta:        []string{"feat/c1-noun-ref-card", "now"},
	})
	taskCard := RecordRefCardView(RecordRefCardSpec{
		ID:          "q.task.1",
		Ref:         taskRef,
		Kind:        "task",
		KindLabel:   "Task",
		Title:       "Ship live deep links",
		StatusLabel: "Active",
		StatusIcon:  "▶",
		StatusTone:  "state.working",
		Summary:     "Make noun ref cards jump straight to records",
		ProgressBar: CompactLaneBarRuns(1, 1, 0, 4),
		Progress:    "1/2 ▶1",
		Meta:        []string{"P1"},
	})
	missingCard := RecordRefCardView(RecordRefCardSpec{
		ID:        "q.missing.1",
		Ref:       missingRef,
		Kind:      "lane",
		KindLabel: "Lane",
		Title:     "(missing lane)",
		Missing:   true,
	})

	stack := ui.ViewNode{
		ID:       "page.root",
		Kind:     ui.KindVStack,
		Gap:      1,
		Children: []ui.ViewNode{laneCard, taskCard, missingCard},
	}

	themes := []ui.Theme{
		ui.SpaciousDarkTheme(),
		ui.DefaultDarkTheme(),
		ui.CompactDarkTheme(),
	}
	for _, theme := range themes {
		fr, solved := ui.Render(stack, theme, "q.lane.1", 100, 30)
		for _, id := range []string{"q.lane.1", "q.task.1", "q.missing.1"} {
			gotH := solved.Rects[id].H
			if gotH != RecordRefCardLines {
				t.Errorf("theme=%s node=%s height=%d, want %d", theme.Name, id, gotH, RecordRefCardLines)
			}
		}
		plain := stripANSI(fr.ANSI)
		for _, want := range []string{
			"▶ App-Level Ref Card", "Running", "██▓░", "2/4 ▶1", "Hydrate live lane cards on ui.page",
			"▶ Ship live deep links", "Active", "1/2 ▶1",
			"✕ (missing lane)", "missing", "record unavailable or deleted",
		} {
			if !strings.Contains(plain, want) {
				t.Errorf("theme=%s rendered frame missing %q:\n%s", theme.Name, want, plain)
			}
		}
	}

	// Verify truthful Facts, Scope, Ref, Focusable, and Padding.
	if !laneCard.Focusable || laneCard.Scope != "lane" || laneCard.Ref == nil || *laneCard.Ref != laneRef {
		t.Fatalf("laneCard metadata = %+v", laneCard)
	}
	if laneCard.Padding != ([4]int{0, 1, 0, 1}) || laneCard.Box.FixedH != 3 {
		t.Fatalf("laneCard padding=%v fixedH=%d, want [0 1 0 1] and 3", laneCard.Padding, laneCard.Box.FixedH)
	}
	wantFacts := map[string]any{
		"recordCard": true,
		"recordKind": "lane",
		"recordId":   "4f8e01412677",
		"status":     "Running",
		"missing":    false,
		"deepLink":   "mtm://lane/4f8e01412677",
	}
	for k, want := range wantFacts {
		if laneCard.Facts[k] != want {
			t.Errorf("laneCard.Facts[%q] = %#v, want %#v", k, laneCard.Facts[k], want)
		}
	}
	if missingCard.Facts["missing"] != true || missingCard.Facts["deepLink"] != "mtm://lane/deadbeef0000" {
		t.Errorf("missingCard.Facts = %#v", missingCard.Facts)
	}
	noRawRefs(t, "laneCard", laneCard)
	noRawRefs(t, "missingCard", missingCard)
}
