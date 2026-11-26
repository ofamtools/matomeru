package app

import (
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/tree"
)

func treeIDs(nodes []tree.Node) []string {
	var out []string
	for _, nd := range nodes {
		out = append(out, nd.ID)
		out = append(out, treeIDs(nd.Children)...)
	}
	return out
}

// Archiving a lane in the Project Portal's Lanes section dissolves it in
// place (ui.archiveAnimation) and then takes it, and the sublanes under it,
// out of the section, the left nav and the "/" filter; "A" (showArchived)
// brings them back.
func TestArchiveLaneAnimatesAndLeavesProjectListsAndFilter(t *testing.T) {
	f := setupNavTreeFixture(t)
	a, d := f.app, f.driver
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	a.opts.Now = func() time.Time { return now }
	advance := func(dt time.Duration) { now = now.Add(dt); wake(d) }
	mk := func(title string, parent *data.Record) data.Record {
		fields := map[string]any{"title": title, "laneStatus": "running", "task": f.parentTask.Ref()}
		if parent != nil {
			fields["parentLane"] = parent.Ref()
		}
		return mkRec(t, a, data.KindLane, fields)
	}
	keep := mk("Zebra lane keep", nil)
	gone := mk("Zebra lane gone", nil)
	child := mk("Zebra sublane", &gone)

	d.Keys("P")
	for i := 0; i < 20 && a.Focus() != projectNavPrefix+"lanes"; i++ {
		d.Keys("j")
	}
	d.Keys("l", "enter", "j") // expand the nav section, enter its content, move to "gone"
	lanes := func() []string {
		d.Frame()
		return treeIDs(a.projectCtrl().trees[projectLanesTree].Nodes)
	}
	if got := lanes(); len(got) != 3 {
		t.Fatalf("lanes before archiving = %v, want all three", got)
	}
	if row, ok := a.projectCtrl().trees[projectLanesTree].Focused(); !ok || row.ID != gone.ID {
		t.Fatalf("focused lane = %+v, want %s", row.ID, gone.ID)
	}

	d.Keys("a")
	if !a.archiveAnimActive(gone.Ref()) {
		t.Fatalf("archiving a lane did not start the archive animation (status %q)", a.Status())
	}
	advance(1700 * time.Millisecond)
	if got := lanes(); strings.Join(got, ",") != keep.ID+","+gone.ID {
		t.Fatalf("lanes while dissolving = %v, want keep and the dissolving lane without its sublane", got)
	}
	nd, _ := findTreeNode(a.projectCtrl().trees[projectLanesTree].Nodes, gone.ID)
	if nd.Tone != "row.dim" || len([]rune(nd.Label)) >= len([]rune("Zebra lane gone")) {
		t.Fatalf("dissolving lane node = tone %q label %q, want dimmed and eroded", nd.Tone, nd.Label)
	}
	if nav := findNode(a, projectNavNodePrefix+"lanes."+gone.ID); nav == nil {
		t.Fatal("the left nav dropped the lane before its animation ended")
	}

	advance(2 * time.Second)
	if got := lanes(); strings.Join(got, ",") != keep.ID {
		t.Fatalf("lanes after the animation = %v, want only %s", got, keep.ID)
	}
	for _, id := range []string{gone.ID, child.ID} {
		if findNode(a, projectNavNodePrefix+"lanes."+id) != nil {
			t.Fatalf("archived lane %s is still in the left nav", id)
		}
	}

	// "/" matches all three titles; only the active lane may answer.
	d.Keys("/")
	d.Type("zebra")
	d.Keys("enter")
	frame := d.Frame()
	if !strings.Contains(frame, "Zebra lane keep") || strings.Contains(frame, "Zebra lane gone") || strings.Contains(frame, "Zebra sublane") {
		t.Fatalf("the filter leaks archived lanes while showArchived is off:\n%s", frame)
	}
	// The Work index of lanes (as a table, where a filter lands) agrees.
	rows := a.records(workTable, data.KindLane).Rows
	for _, id := range []string{gone.ID, child.ID} {
		if _, leaked := findTableRow(rows, data.RefText(data.Record{Kind: data.KindLane, ID: id}.Ref())); leaked {
			t.Fatalf("archived lane %s is in the Work lanes table", id)
		}
	}

	d.Send(core.CommandEvent{Command: core.Command{ID: CmdArchivedToggle, Actor: User}})
	if frame := d.Frame(); !a.nav.showArchived || !strings.Contains(frame, "Zebra lane gone") || !strings.Contains(frame, "Zebra sublane") {
		t.Fatalf("showArchived on does not bring the archived lanes back into the filtered list:\n%s", frame)
	}
}
