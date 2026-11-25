package app

import (
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/tree"
)

func newClockedDemoApp(t *testing.T, start time.Time) (*App, *Driver, func(time.Duration)) {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := SeedDemo(store); err != nil {
		t.Fatalf("seed demo: %v", err)
	}
	now := start
	a, err := New(store, Options{
		FS:  smokeFS(),
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 36)
	advance := func(dt time.Duration) {
		now = now.Add(dt)
		wake(d)
	}
	return a, d, advance
}

func findTableRow(rows []table.Row, id string) (table.Row, bool) {
	for _, r := range rows {
		if r.ID == id {
			return r, true
		}
	}
	return table.Row{}, false
}

func findTreeNode(nodes []tree.Node, id string) (tree.Node, bool) {
	for _, nd := range nodes {
		if nd.ID == id {
			return nd, true
		}
		if sub, ok := findTreeNode(nd.Children, id); ok {
			return sub, true
		}
	}
	return tree.Node{}, false
}

func TestArchiveAnimPhasesAndCancelOnPanesRail(t *testing.T) {
	a, d, advance := newClockedDemoApp(t, time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC))

	pane, ok := a.SelectedPane()
	if !ok {
		t.Fatal("expected selected pane")
	}
	ref := pane.Ref()
	rowID := data.RefText(ref)
	origRow, ok := findTableRow(a.rail().Rows, rowID)
	if !ok {
		t.Fatalf("pane %s not in initial rail", rowID)
	}
	origTitle := data.Text(origRow.Cells["rail"])

	// Press 'a' on Panes rail -> archived in store, stays visible dimmed while dissolving.
	d.Keys("a")
	rec, _ := a.Store.Get(ref)
	if !rec.IsArchived() {
		t.Fatal("expected pane to be archived in store")
	}
	row0, ok := findTableRow(a.rail().Rows, rowID)
	if !ok {
		t.Fatal("expected dissolving pane row to remain visible in rail at t=0")
	}
	if row0.Tone != "row.dim" || row0.Facts[data.ColArchiveStatus] != data.ArchiveArchived {
		t.Fatalf("t=0 row tone=%q archiveStatus=%v, want row.dim / archived", row0.Tone, row0.Facts[data.ColArchiveStatus])
	}

	// Phase 1 (+500ms, 20% of 2500ms): full title with color-sweep spans.
	advance(500 * time.Millisecond)
	row1, ok := findTableRow(a.rail().Rows, rowID)
	if !ok {
		t.Fatal("expected pane row visible in Phase 1")
	}
	if data.Text(row1.Cells["rail"]) != origTitle {
		t.Fatalf("Phase 1 title = %q, want full %q", row1.Cells["rail"], origTitle)
	}
	if len(row1.CellSpans["rail"]) == 0 {
		t.Fatalf("Phase 1 expected non-empty color-sweep CellSpans on rail column")
	}

	// Phase 2 (+1300ms -> 1800ms total, 72%): text erodes character-by-character.
	advance(1300 * time.Millisecond)
	row2, ok := findTableRow(a.rail().Rows, rowID)
	if !ok {
		t.Fatal("expected pane row visible in Phase 2")
	}
	eroded := data.Text(row2.Cells["rail"])
	if len([]rune(eroded)) == 0 || len([]rune(eroded)) >= len([]rune(origTitle)) {
		t.Fatalf("Phase 2 eroded title = %q (len %d), want shorter than %q (len %d)",
			eroded, len([]rune(eroded)), origTitle, len([]rune(origTitle)))
	}
	if len(row2.CellSpans["rail"]) == 0 {
		t.Fatalf("Phase 2 expected leading-edge CellSpans")
	}

	// Pressing 'a' a second time on the dissolving row restores immediately and cancels animation.
	d.Keys("a")
	rec, _ = a.Store.Get(ref)
	if rec.IsArchived() {
		t.Fatal("second 'a' on dissolving row did not restore record")
	}
	if a.archiveAnimActive(ref) {
		t.Fatal("second 'a' did not cancel archive animation")
	}
	restored, ok := findTableRow(a.rail().Rows, rowID)
	if !ok || data.Text(restored.Cells["rail"]) != origTitle || len(restored.CellSpans["rail"]) != 0 {
		t.Fatalf("restored rail row not snapped back to full title: row=%+v", restored)
	}

	// Archive again and let full duration elapse -> row drops out cleanly.
	d.Keys("a")
	advance(2600 * time.Millisecond)
	if _, still := findTableRow(a.rail().Rows, rowID); still {
		t.Fatal("expected archived pane row to leave rail after duration elapsed")
	}
	if a.archiveAnim.set.Len() != 0 {
		t.Fatalf("expected ExitAnimSet empty after expiry, len=%d", a.archiveAnim.set.Len())
	}
}

func TestArchiveAnimWorkTreeAndUndoCancel(t *testing.T) {
	a, d, advance := newClockedDemoApp(t, time.Date(2026, 11, 25, 11, 0, 0, 0, time.UTC))

	// Switch to 2 Work (tree layout by default).
	d.Keys("2")
	if !a.inWorkTree(a.State()) {
		t.Fatal("expected 2 Work in tree layout")
	}
	tm := a.trees.workModel(data.KindTask)
	focused, ok := tm.Focused()
	if !ok {
		t.Fatal("expected focused task in Work tree")
	}
	taskID := focused.ID
	origLabel := focused.Node.Label

	// Press 'd' in Work tree to archive.
	d.Keys("d")
	advance(1700 * time.Millisecond)
	_ = d.Frame() // rebuilds Work tree view
	nd, ok := findTreeNode(a.trees.workModel(data.KindTask).Nodes, taskID)
	if !ok {
		t.Fatalf("expected dissolving task %s to remain visible in Work tree during Phase 2", taskID)
	}
	if nd.Tone != "row.dim" || len(nd.LabelSpans) == 0 || len([]rune(nd.Label)) >= len([]rune(origLabel)) {
		t.Fatalf("expected dimmed eroded tree node, got tone=%q label=%q spans=%+v (orig=%q)", nd.Tone, nd.Label, nd.LabelSpans, origLabel)
	}

	// Press 'u' (undo) -> cancels dissolve and restores full label immediately!
	d.Keys("u")
	_ = d.Frame()
	ndRestored, ok := findTreeNode(a.trees.workModel(data.KindTask).Nodes, taskID)
	if !ok {
		t.Fatalf("expected undone task %s in Work tree", taskID)
	}
	if ndRestored.Label != origLabel || len(ndRestored.LabelSpans) != 0 {
		t.Fatalf("undo did not snap tree node back: label=%q spans=%+v want %q", ndRestored.Label, ndRestored.LabelSpans, origLabel)
	}
}

func TestArchiveAnimRecordTablesAndOptOut(t *testing.T) {
	a, d, advance := newClockedDemoApp(t, time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC))

	// Switch to 3 Runs (standard record table).
	d.Keys("3")
	tbl := a.records(runsTable, data.KindRun)
	row, ok := tbl.FocusedRow()
	if !ok || row.Ref == nil {
		t.Fatal("expected focused run row")
	}
	rowID := row.ID
	origTitle := data.Text(row.Cells["title"])

	// Archive in Runs table -> stays visible with dissolve wave in Phase 1, drops out after 2500ms.
	d.Keys("a")
	advance(600 * time.Millisecond)
	midRow, ok := findTableRow(a.records(runsTable, data.KindRun).Rows, rowID)
	if !ok {
		t.Fatal("expected run row visible during dissolve")
	}
	if midRow.Tone != "row.dim" || len(midRow.CellSpans["title"]) == 0 || data.Text(midRow.Cells["title"]) != origTitle {
		t.Fatalf("Phase 1 run row = %+v, want dim + spans + full title %q", midRow, origTitle)
	}
	advance(2100 * time.Millisecond)
	if _, still := findTableRow(a.records(runsTable, data.KindRun).Rows, rowID); still {
		t.Fatal("expected run row to drop out after animation expires")
	}

	// Restore via undo, then opt out with ui.archiveAnimation = false -> immediate removal!
	d.Keys("u")
	if _, back := findTableRow(a.records(runsTable, data.KindRun).Rows, rowID); !back {
		t.Fatal("undo did not restore run row")
	}
	a.cfg.userSettings[SetArchiveAnimation] = false
	a.reloadSettings()
	a.applySettings()

	optOutRow, ok := a.records(runsTable, data.KindRun).FocusedRow()
	if !ok || optOutRow.Ref == nil {
		t.Fatal("expected focused run row before opt-out archive")
	}
	optOutRef := *optOutRow.Ref
	optOutID := optOutRow.ID

	d.Keys("a")
	if a.archiveAnimActive(optOutRef) {
		t.Fatal("with ui.archiveAnimation=false, archive animation should not start")
	}
	if slices.ContainsFunc(a.records(runsTable, data.KindRun).Rows, func(r table.Row) bool { return r.ID == optOutID }) {
		t.Fatalf("with ui.archiveAnimation=false, archived row %s should leave list immediately (status=%q)", optOutID, a.Status())
	}
	if !strings.Contains(a.Status(), "archive") {
		t.Fatalf("unexpected status=%q", a.Status())
	}
}
