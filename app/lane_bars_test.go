package app

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/ui"
)

// laneBarIDs are the IDs of the lane bars above the active pane's site, top down.
func laneBarIDs(a *App) []string {
	stack := findNode(a, views.LaneBarsID)
	if stack == nil {
		return nil
	}
	ids := make([]string, len(stack.Children))
	for i, c := range stack.Children {
		ids[i] = c.ID
	}
	return ids
}

// With panes.laneProgressBar on, a pane's finished lane trees stay stacked
// above its active lane bar (panes.keepCompletedLaneBars). Each bar is a
// focusable lane-bar node: d dismisses the focused one, D opens a picker that
// can dismiss all completed bars, and the opt-out shows the active bar alone.
func TestLaneBarsKeepCompletedFocusDismissAndPicker(t *testing.T) {
	a, d := demoLegacy(t)
	setSettingForTest(t, a, SetLaneBar, true)
	pane, ok := a.SelectedPane()
	if !ok || findNode(a, views.LaneBarID) == nil {
		t.Fatalf("the demo's selected pane shows no active lane bar: %v", laneBarIDs(a))
	}
	finished := func(title, status string) data.Record {
		root := mkRec(t, a, data.KindLane, map[string]any{"title": title, "laneStatus": status})
		mkRec(t, a, data.KindLane, map[string]any{"title": title + " step", "laneStatus": "done", "parentLane": root.Ref()})
		if _, _, err := a.Store.Relate(data.RelateInput{From: root.Ref(), Kind: data.RelClaim, To: pane.Ref()}, User); err != nil {
			t.Fatal(err)
		}
		return root
	}
	first := finished("Finished wave one", "done")
	second := finished("Finished wave two", "merged")
	firstBar, secondBar := views.LaneBarPrefix+first.ID, views.LaneBarPrefix+second.ID

	// Stacked: the finished trees, oldest first, then the active bar.
	d.Keys("1")
	if got := laneBarIDs(a); !slices.Equal(got, []string{firstBar, secondBar, views.LaneBarID}) {
		t.Fatalf("lane bars = %v, want the two finished bars above the active one", got)
	}
	bar := findNode(a, secondBar)
	if !bar.Focusable || bar.Scope != views.ScopeLaneBar || bar.Ref == nil || *bar.Ref != second.Ref() ||
		bar.Facts["laneId"] != second.ID || bar.Facts["laneStatus"] != "merged" || bar.Facts["dismissable"] != true {
		t.Fatalf("finished lane bar node = %+v", bar)
	}
	if active := findNode(a, views.LaneBarID); !active.Focusable || active.Scope != views.ScopeLaneBar || active.Facts["laneStatus"] != "running" || active.Kind != ui.KindProgress {
		t.Fatalf("active lane bar node = %+v", active)
	}
	if frame := d.Frame(); !strings.Contains(frame, "Finished wave one") || !strings.Contains(frame, "Finished wave two") || !strings.Contains(frame, "Matomeru v2 Step 2") {
		t.Fatalf("the stacked bars are not all drawn:\n%s", frame)
	}

	// Focus the bars, move onto a finished one and dismiss it on demand.
	d.Keys("g", "l")
	if a.Focus() != views.LaneBarID {
		t.Fatalf("g l focus = %q, want the active lane bar", a.Focus())
	}
	d.Keys("k")
	if a.Focus() != secondBar {
		t.Fatalf("k from the active bar focus = %q, want %q", a.Focus(), secondBar)
	}
	d.Keys("d")
	if got := laneBarIDs(a); !slices.Equal(got, []string{firstBar, views.LaneBarID}) {
		t.Fatalf("after d, lane bars = %v (status %q)", got, a.Status())
	}
	if !slices.Contains(laneBarIDs(a), a.Focus()) {
		t.Fatalf("after dismissing, the focus left the lane bars: %q", a.Focus())
	}

	// D opens the picker; its first entry dismisses every completed bar.
	d.Keys("D")
	if a.Modal() != laneBarsPickerID {
		t.Fatalf("D did not open the dismiss picker: modal=%q status=%q", a.Modal(), a.Status())
	}
	if ids := pickerIDs(a, laneBarsPickerID); len(ids) != 3 || ids[0] != laneBarsPickAllID || !slices.Contains(ids, data.LaneBarKey(first.ID, true)) {
		t.Fatalf("dismiss picker items = %v", ids)
	}
	if !strings.Contains(d.Frame(), laneBarsPickAllTitle) {
		t.Fatalf("the picker does not offer %q:\n%s", laneBarsPickAllTitle, d.Frame())
	}
	d.Keys("enter")
	if got := laneBarIDs(a); a.Modal() != "" || !slices.Equal(got, []string{views.LaneBarID}) {
		t.Fatalf("after Dismiss all completed: modal=%q bars=%v", a.Modal(), got)
	}

	// A new session state shows them again; the opt-out never stacks them.
	a.laneBars = nil
	a.viewMemo.gen++
	if got := laneBarIDs(a); len(got) != 3 {
		t.Fatalf("without dismissals, lane bars = %v, want all three", got)
	}
	setSettingForTest(t, a, SetPanesKeepCompletedLaneBars, false)
	if got := laneBarIDs(a); !slices.Equal(got, []string{views.LaneBarID}) {
		t.Fatalf("panes.keepCompletedLaneBars off: lane bars = %v, want only the active bar", got)
	}
}
