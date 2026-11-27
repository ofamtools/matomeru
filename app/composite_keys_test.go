package app

import (
	"testing"

	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/table"
)

func TestFeatureCompositeKeysRoundTrip(t *testing.T) {
	cases := []struct {
		key  featureKey
		want string
	}{
		{featurePageKey("panes"), "page:panes"},
		{featureComponentKey("panes.rail"), "component:panes.rail"},
		{featureNounKey("schedule"), "noun:schedule"},
		{featureGroupKey("grp-1"), "group:grp-1"},
		{featureConceptKey("prop"), "concept:prop"},
	}
	for _, tc := range cases {
		if tc.key.IsZero() {
			t.Fatalf("%+v unexpectedly zero", tc.key)
		}
		got := tc.key.String()
		if got != tc.want {
			t.Fatalf("String() = %q, want %q", got, tc.want)
		}
		parsed, ok := parseFeatureKey(" " + got + " ")
		if !ok || parsed != tc.key {
			t.Fatalf("parseFeatureKey(%q) = (%+v, %v), want (%+v, true)", got, parsed, ok, tc.key)
		}
	}

	for _, bad := range []string{"", "page:", ":panes", "unknown:foo", "bare"} {
		if k, ok := parseFeatureKey(bad); ok || !k.IsZero() {
			t.Fatalf("parseFeatureKey(%q) = (%+v, %v), want zero/false", bad, k, ok)
		}
	}

	if got := threadReplySubtitle("t-42"); got != "Thread task:t-42 · append-only" {
		t.Fatalf("threadReplySubtitle = %q", got)
	}
}

func TestTableSelectionSingleOwner(t *testing.T) {
	mkRows := func(ids ...string) []table.Row {
		out := make([]table.Row, 0, len(ids))
		for _, id := range ids {
			out = append(out, table.Row{ID: id, Cells: map[string]any{"name": id}})
		}
		return out
	}

	tbl := table.New(nil, nil)
	tbl.FilterField = "name"

	// Initial load + explicit preferredID.
	shown := syncTableSelection(tbl, mkRows("alpha", "beta", "gamma"), "beta")
	if len(shown) != 3 || tbl.SelectedIdx != 1 {
		t.Fatalf("initial sync: len=%d idx=%d, want 3, 1", len(shown), tbl.SelectedIdx)
	}

	// Reordering preserves selection by stable row ID (beta moves from 1 -> 2).
	shown = syncTableSelection(tbl, mkRows("gamma", "alpha", "beta"), "")
	if len(shown) != 3 || tbl.SelectedIdx != 2 {
		t.Fatalf("reordered sync: len=%d idx=%d, want 3, 2", len(shown), tbl.SelectedIdx)
	}
	if row, ok := tbl.FocusedRow(); !ok || row.ID != "beta" {
		t.Fatalf("focused row after reorder = (%+v, %v), want beta", row, ok)
	}

	// Deleting selected row clamps cleanly to the end of the remaining list.
	shown = syncTableSelection(tbl, mkRows("gamma", "alpha"), "")
	if len(shown) != 2 || tbl.SelectedIdx != 1 {
		t.Fatalf("after deletion: len=%d idx=%d, want 2, 1", len(shown), tbl.SelectedIdx)
	}

	// Active filter narrows visible rows while keeping selection valid.
	tbl.Query = "gam"
	shown = syncTableSelection(tbl, mkRows("alpha", "gamma"), "")
	if len(shown) != 1 || shown[0].ID != "gamma" || tbl.SelectedIdx != 0 {
		t.Fatalf("filtered sync: shown=%+v idx=%d, want [gamma], 0", shown, tbl.SelectedIdx)
	}

	// Empty list clamps safely to 0 without panicking.
	tbl.Query = ""
	shown = syncTableSelection(tbl, nil, "")
	if len(shown) != 0 || tbl.SelectedIdx != 0 {
		t.Fatalf("empty sync: len=%d idx=%d, want 0, 0", len(shown), tbl.SelectedIdx)
	}

	// selectTableRowID & resetTableSelection.
	syncTableSelection(tbl, mkRows("one", "two", "three"), "")
	if idx, ok := selectTableRowID(tbl, "three"); !ok || idx != 2 || tbl.SelectedIdx != 2 {
		t.Fatalf("selectTableRowID(three) = (%d, %v) idx=%d", idx, ok, tbl.SelectedIdx)
	}
	tbl.Offset = 5
	resetTableSelection(tbl)
	if tbl.SelectedIdx != 0 || tbl.Offset != 0 {
		t.Fatalf("resetTableSelection left idx=%d offset=%d", tbl.SelectedIdx, tbl.Offset)
	}

	// Grouped header selection via selectTableGroupHeader.
	grouped := table.New([]table.Column{{Key: "name", Title: "Name"}, {Key: "group", Title: "Group"}}, []table.Row{
		{ID: "r1", Cells: map[string]any{"name": "r1", "group": "ProjA"}},
		{ID: "r2", Cells: map[string]any{"name": "r2", "group": "ProjB"}},
	})
	grouped.GroupField = "group"
	if !selectTableGroupHeader(grouped, "ProjB") {
		t.Fatal("selectTableGroupHeader(ProjB) = false, want true")
	}
	if it, ok := grouped.Focused(); !ok || !it.Header || it.Group != "ProjB" {
		t.Fatalf("focused group item = (%+v, %v), want header ProjB", it, ok)
	}

	// Picker ID selection via selectPickerItemID.
	pm := picker.New("Pick", []picker.Item{{ID: "a"}, {ID: "b"}, {ID: "c"}})
	if !selectPickerItemID(pm, "c") || pm.SelectedIdx != 2 {
		t.Fatalf("selectPickerItemID(c): idx=%d, want 2", pm.SelectedIdx)
	}
	if selectPickerItemID(pm, "missing") {
		t.Fatal("selectPickerItemID(missing) = true, want false")
	}
}
