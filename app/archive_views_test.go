package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/table"
)

func tableTitles(rows []table.Row) string {
	var out []string
	for _, r := range rows {
		out = append(out, data.Text(r.Cells["title"]))
	}
	return strings.Join(out, ",")
}

// Archived lanes and records stay out of the Work lists and trees and of every
// Project Portal section and its left nav until "A" (showArchived) asks for
// them; then they are all back, to inspect or restore.
func TestArchiveHiddenByDefaultInWorkAndProjectPortal(t *testing.T) {
	f := setupNavTreeFixture(t)
	a, d := f.app, f.driver
	sections := map[string]string{
		data.KindTask: "tasks", data.KindLane: "lanes", data.KindNote: "notes", data.KindDocument: "documents",
		data.KindQuestion: "questions", data.KindCommand: "commands", data.KindPage: "pages", data.KindPane: "panes",
	}
	const ghost = "ZZGHOST"
	for kind := range sections {
		fields := map[string]any{"title": ghost + kind, "task": f.parentTask.Ref(), "question": ghost + kind, "name": ghost + kind, "slug": "ghost"}
		if kind == data.KindTask {
			fields = map[string]any{"title": ghost + kind, "parent": f.parentTask.Ref()}
		}
		rec := mkRec(t, a, kind, fields)
		if _, _, err := a.Store.Archive(rec.Ref(), User); err != nil {
			t.Fatalf("archive %s: %v", kind, err)
		}
	}
	// ghosts counts the archived records each default view shows, by view.
	ghosts := func() map[string]int {
		out := map[string]int{}
		for kind, sec := range sections {
			for _, row := range a.records(workTable, kind).Rows {
				if strings.Contains(fmt.Sprint(row.Cells), ghost) {
					out["work table "+kind]++
				}
			}
			if a.schema(kind).EffectiveHierarchy() != nil {
				nodes, err := a.recordTreeNodes(kind)
				if err != nil {
					t.Fatalf("tree %s: %v", kind, err)
				}
				for _, nd := range flattenTreeDescendants(nodes) {
					if strings.Contains(views.Title(a.schema(kind), nd), ghost) {
						out["work tree "+kind]++
					}
				}
			}
			ws, ok := a.resolveProjectNavWorkspace(a.State())
			if !ok {
				t.Fatal("the Project Portal has no workspace")
			}
			nav, _ := a.buildProjectNavChildren(ws, sec, "", true, true)
			var walk func(ns []views.ProjectNavNode)
			walk = func(ns []views.ProjectNavNode) {
				for _, nd := range ns {
					if strings.Contains(nd.Label, ghost) {
						out["portal nav "+sec]++
					}
					walk(nd.Children)
				}
			}
			walk(nav)
			// A section draws from its body, its table or its tree.
			shown := fmt.Sprintf("%+v", a.buildProjectSectionBody(a.State(), ws, sec))
			for id, tbl := range a.nav.tables {
				if strings.HasPrefix(id, "project.") {
					shown += fmt.Sprint(tbl.Rows)
				}
			}
			for _, tm := range a.projectCtrl().trees {
				shown += fmt.Sprint(tm.Nodes)
			}
			if strings.Contains(shown, ghost+kind) {
				out["portal section "+sec]++
			}
		}
		return out
	}

	d.Keys("P")
	if got := ghosts(); len(got) != 0 {
		t.Fatalf("archived records leak into default views: %v", got)
	}
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdArchivedToggle, Actor: User}})
	got := ghosts()
	for kind, sec := range sections {
		for _, view := range []string{"work table " + kind, "portal section " + sec} {
			if got[view] == 0 {
				t.Errorf("showArchived on does not bring the archived %s back into %s (got %v)", kind, view, got)
			}
		}
	}
	for _, view := range []string{"work tree " + data.KindLane, "portal nav lanes", "portal nav documents", "portal nav notes"} {
		if got[view] == 0 {
			t.Errorf("showArchived on does not bring archived records back into %s (got %v)", view, got)
		}
	}
}

// Archiving a row says what was archived, by kind and title; the row dissolves
// where it stands (or leaves at once with ui.archiveAnimation off) and the
// cursor ends on its neighbour. Restoring it under showArchived says so too.
func TestArchiveFeedbackNamesRecordAndCursorLandsOnNeighbour(t *testing.T) {
	for _, anim := range []bool{true, false} {
		t.Run(fmt.Sprintf("animation=%t", anim), func(t *testing.T) {
			f := setupNavTreeFixture(t)
			a, d := f.app, f.driver
			now := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
			a.opts.Now = func() time.Time { return now }
			setSettingForTest(t, a, SetShowLegacyPanesWorkPages, true)
			setSettingForTest(t, a, SetArchiveAnimation, anim)
			if _, _, err := a.Store.Archive(f.note.Ref(), User); err != nil {
				t.Fatal(err)
			}
			for _, title := range []string{"ZZone", "ZZtwo", "ZZthree", "ZZfour", "ZZfive"} {
				mkRec(t, a, data.KindNote, map[string]any{"title": title})
			}
			a.nav.browseKind = data.KindNote
			d.Keys("2", "j")
			rows := func() string { d.Frame(); return tableTitles(a.nav.tables[workTable].Filtered()) }
			focused := func() string {
				row, _ := a.nav.tables[workTable].FocusedRow()
				return data.Text(row.Cells["title"])
			}
			if rows() != "ZZone,ZZtwo,ZZthree,ZZfour,ZZfive" || focused() != "ZZtwo" {
				t.Fatalf("before archiving: rows %q, focused %q", rows(), focused())
			}

			d.Keys("a")
			if got, want := a.Status(), `archived note "ZZtwo" · u undoes`; got != want {
				t.Fatalf("archive feedback = %q, want %q", got, want)
			}
			if anim {
				if got := rows(); got != "ZZone,ZZtwo,ZZthree,ZZfour,ZZfive" || focused() != "ZZtwo" {
					t.Fatalf("the dissolving row moved: rows %q, focused %q", got, focused())
				}
				now = now.Add(3 * time.Second)
				wake(d)
			}
			if got := rows(); got != "ZZone,ZZthree,ZZfour,ZZfive" || focused() != "ZZthree" {
				t.Fatalf("after archiving: rows %q, focused %q; want the row gone and the cursor on ZZthree", got, focused())
			}

			d.Send(core.CommandEvent{Command: core.Command{ID: CmdArchivedToggle, Actor: User}})
			if got := rows(); !strings.Contains(got, "ZZtwo") {
				t.Fatalf("showArchived on: rows %q, want the archived note back", got)
			}
			for i := 0; i < 8 && focused() != "ZZtwo"; i++ {
				d.Keys("k")
			}
			d.Keys("a")
			if got, want := a.Status(), `restored note "ZZtwo" · u undoes`; got != want {
				t.Fatalf("restore feedback = %q, want %q", got, want)
			}
			d.Send(core.CommandEvent{Command: core.Command{ID: CmdArchivedToggle, Actor: User}})
			if got := rows(); got != "ZZone,ZZtwo,ZZthree,ZZfour,ZZfive" {
				t.Fatalf("after restoring: rows %q, want all five", got)
			}
		})
	}
}

// "d" on a lane in the Work tree names the lane it archived and leaves the
// cursor on the lane after it; a batch names the kind it archived.
func TestArchiveLaneInWorkTreeFeedbackAndBatchNoun(t *testing.T) {
	f := setupNavTreeFixture(t)
	a, d := f.app, f.driver
	setSettingForTest(t, a, SetShowLegacyPanesWorkPages, true)
	setSettingForTest(t, a, SetArchiveAnimation, false)
	var lanes []data.Record
	for _, title := range []string{"Lane one", "Lane two", "Lane three", "Lane four"} {
		lanes = append(lanes, mkRec(t, a, data.KindLane, map[string]any{"title": title, "laneStatus": "running", "task": f.parentTask.Ref()}))
	}
	a.nav.browseKind = data.KindLane
	d.Keys("2", "j", "d")
	if got, want := a.Status(), `archived lane "Lane two" · u undoes`; got != want {
		t.Fatalf("archive feedback = %q, want %q", got, want)
	}
	d.Frame()
	tm := a.trees.workModel(data.KindLane)
	if _, still := findTreeNode(tm.Nodes, lanes[1].ID); still {
		t.Fatal("the archived lane is still in the Work tree")
	}
	if row, ok := tm.Focused(); !ok || row.ID != lanes[2].ID {
		t.Fatalf("focused lane = %q, want the next lane %q", row.ID, lanes[2].ID)
	}

	if got := a.batchNoun([]core.Ref{lanes[0].Ref(), lanes[3].Ref()}); got != "lanes" {
		t.Fatalf("batch noun for two lanes = %q, want lanes", got)
	}
	if got := a.batchNoun([]core.Ref{lanes[0].Ref(), f.note.Ref()}); got != "items" {
		t.Fatalf("batch noun for a lane and a note = %q, want items", got)
	}
}
