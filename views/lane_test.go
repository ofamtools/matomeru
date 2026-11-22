package views

import (
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func laneOf(t *testing.T, w *world, id, title, status string, extra map[string]any) data.Record {
	t.Helper()
	fields := map[string]any{"title": title, "laneStatus": status}
	for k, v := range extra {
		fields[k] = v
	}
	rec, _, err := w.store.Create(data.CreateInput{Kind: data.KindLane, ID: id, Fields: fields}, owner)
	if err != nil {
		t.Fatal(err)
	}
	return *rec
}

func nodeText(n *ui.ViewNode) string {
	if n == nil {
		return ""
	}
	s, _ := n.Props["text"].(string)
	return s
}

// The bar follows a claim to its root lane, rolls up the root's children and
// names the running one. It is a line above the site, never a page.
func TestLaneBar(t *testing.T) {
	w := build(t)
	pages := []data.Record{w.page, w.asks}
	bare := PaneSiteViewWith(w.pane, pages, "", "", SiteOptions{Store: w.store, ShowLaneBar: true})
	if bare.Find(LaneBarID) != nil {
		t.Fatal("a bar with no lane")
	}

	root := laneOf(t, w, "wave", "Ochazuke v2 Wave 3", "running", nil)
	child := func(id, title, status string) data.Record {
		return laneOf(t, w, id, title, status, map[string]any{"parentLane": root.Ref()})
	}
	child("l1", "trie", "done")
	child("l2", "expr", "merged")
	child("l3", "layout", "done")
	worker := child("l4", "picker-worker", "running")
	child("l5", "form", "planned")
	child("l6", "docs", "parked")
	child("l7", "dropped idea", "cancelled")
	// The pane claims a child lane, not the root.
	if _, _, err := w.store.Relate(data.RelateInput{From: worker.Ref(), Kind: data.RelClaim, To: w.pane.Ref()}, owner); err != nil {
		t.Fatal(err)
	}

	site := PaneSiteViewWith(w.pane, pages, "", "", SiteOptions{Store: w.store, ShowLaneBar: true, LaneHint: "ctrl+a L"})
	bar := site.Find(LaneBarID)
	want := " Lane: Ochazuke v2 Wave 3  [███▓░░] 3/6 done · 1 running (picker-worker) · 2 planned  (ctrl+a L)"
	if got := nodeText(bar); got != want {
		t.Fatalf("bar = %q\nwant  %q", got, want)
	}
	if site.Children[0].ID != LaneBarID || bar.Ref == nil || *bar.Ref != root.Ref() || site.Facts["lane"] != root.ID {
		t.Fatalf("the bar is not the first line of the site, or does not name the root lane: %+v", bar)
	}
	if toc := site.Find("site.toc"); toc.Find(LaneBarID) != nil || strings.Contains(frame(t, *toc, "", 40, 20), "Lane:") {
		t.Fatal("the bar is in the table of contents")
	}
	wantText(t, frame(t, site, "", 120, 12), "Lane: Ochazuke v2 Wave 3", "3/6 done", "Pages")
	// With little room the hint goes first, then the line is clipped.
	if bar, _ := LaneBar(w.store, w.pane, "ctrl+a L", 90); strings.Contains(nodeText(&bar), "ctrl+a L") || !strings.HasSuffix(nodeText(&bar), "2 planned") {
		t.Fatalf("bar at 90 = %q", nodeText(&bar))
	}
	if bar, _ := LaneBar(w.store, w.pane, "ctrl+a L", 40); len([]rune(nodeText(&bar))) != 40 || !strings.HasSuffix(nodeText(&bar), "…") {
		t.Fatalf("bar at 40 = %q", nodeText(&bar))
	}
	// Off by option, and the plain view never has it.
	for _, v := range []ui.ViewNode{PaneSiteViewWith(w.pane, pages, "", "", SiteOptions{Store: w.store}), PaneSiteView(w.pane, pages, "", "")} {
		if v.Find(LaneBarID) != nil {
			t.Fatal("the bar shows though it was not asked for")
		}
	}

	// A second active tree on the same pane is counted, not shown.
	second := laneOf(t, w, "side", "Side quest", "planned", nil)
	if _, _, err := w.store.Relate(data.RelateInput{From: second.Ref(), Kind: data.RelClaim, To: w.pane.Ref()}, owner); err != nil {
		t.Fatal(err)
	}
	roll, ok, err := ActiveLane(w.store, w.pane)
	if err != nil || !ok || roll.Root.ID != root.ID || roll.OtherTrees != 1 || !strings.Contains(roll.Text(), "Ochazuke v2 Wave 3 (+1)") {
		t.Fatalf("two trees: %v %v %q", ok, err, roll.Text())
	}
}

// With no claim the bar walks up from the pane's task to the nearest task
// that has an active lane; a finished tree shows nothing.
func TestLaneBarFromTask(t *testing.T) {
	w := build(t)
	pane, _, err := w.store.Create(data.CreateInput{Kind: data.KindPane, ID: "w13", Fields: map[string]any{"title": "leaf pane", "task": w.leaf.Ref()}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := ActiveLane(w.store, *pane); ok {
		t.Fatal("a lane with none stored")
	}
	// The lane belongs to the project, two levels above the pane's task.
	lane := laneOf(t, w, "proj-lane", "Ship v2", "running", map[string]any{"task": w.project.Ref()})
	roll, ok, err := ActiveLane(w.store, *pane)
	if err != nil || !ok || roll.Root.ID != lane.ID || roll.Text() != "Lane: Ship v2  [▓] 0/1 done · 1 running (Ship v2)" {
		t.Fatalf("from task: %v %v %q", ok, err, roll.Text())
	}
	// A nearer task's lane wins.
	near := laneOf(t, w, "sub-lane", "Show pages", "planned", map[string]any{"task": w.sub2.Ref()})
	if roll, _, _ := ActiveLane(w.store, *pane); roll.Root.ID != near.ID || roll.OtherTrees != 0 {
		t.Fatalf("nearest lane = %s (+%d)", roll.Root.ID, roll.OtherTrees)
	}
	for _, id := range []string{near.ID, lane.ID} {
		if _, _, err := w.store.Patch(data.PatchInput{Ref: core.Ref{Kind: data.KindLane, ID: id}, Fields: map[string]any{"laneStatus": "done"}}, owner); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, _ := ActiveLane(w.store, *pane); ok {
		t.Fatal("a finished lane still shows")
	}
}

// A lane's Show page leads with a timeline: logged notes and change sets in
// the order they happened. The options drop metadata and empty sections.
func TestLaneShowTimelineAndOptions(t *testing.T) {
	w := build(t)
	lane := laneOf(t, w, "wave", "Wave 3", "planned", map[string]any{"task": w.project.Ref()})
	base := time.Now().UTC()
	log := func(kind, text string, at time.Duration) {
		t.Helper()
		note, _, err := w.store.Create(data.CreateInput{Kind: data.KindNote, Fields: map[string]any{"title": kind + ": " + text, "logKind": kind,
			"loggedAt": base.Add(at).Format(data.TimeFormat)}}, owner)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := w.store.Relate(data.RelateInput{From: note.Ref(), Kind: data.RelAbout, To: lane.Ref()}, owner); err != nil {
			t.Fatal(err)
		}
	}
	// Logged out of order: the timeline sorts by when it happened.
	log("verification", "gate passed", 3*time.Hour)
	log("milestone", "trie merged", time.Hour)
	log("decision", "keep the trie", 2*time.Hour)
	if _, _, err := w.store.Patch(data.PatchInput{Ref: lane.Ref(), Fields: map[string]any{"laneStatus": "running"}, Summary: "status running"}, owner); err != nil {
		t.Fatal(err)
	}

	entries, err := Timeline(w.store, lane.Ref(), 0)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range entries {
		kinds = append(kinds, e.Kind)
	}
	if got := strings.Join(kinds, " "); got != "change change milestone decision verification" {
		t.Fatalf("timeline order = %q", got)
	}
	if entries[2].Text != "trie merged" || entries[2].Ref == nil {
		t.Fatalf("entry = %+v", entries[2])
	}

	root, err := ShowView(w.store, lane.Ref(), "")
	if err != nil {
		t.Fatal(err)
	}
	related := root.Find("show.related")
	if tl := root.Find("show.timeline"); tl == nil || related.Children[0].Children[0].ID != "show.timeline" {
		t.Fatal("the timeline is not the first related panel")
	}
	wantText(t, frame(t, root, "", 140, 50), "Timeline", "When", "◆ milestone", "trie merged", "◇ decision", "keep the trie", "✓ verification", "gate passed",
		"status running", "Claimed by", "Plan", "Created", "Revision")
	if other, _ := ShowView(w.store, w.project.Ref(), ""); other.Find("show.timeline") != nil {
		t.Fatal("a task has a timeline")
	} else if other.Find("show.sec.lane.task") == nil {
		t.Fatal("the task's page has no Lanes section")
	}

	lean, err := ShowViewWith(w.store, lane.Ref(), "", ShowOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if lean.Find("show.sec.rel.claim.out") != nil || lean.Find("show.sec.rel.plan.out") != nil {
		t.Fatal("empty relation sections are still shown")
	}
	if text := frame(t, lean, "", 140, 50); strings.Contains(text, "Revision") || strings.Contains(text, "Created") || !strings.Contains(text, "Timeline") {
		t.Fatalf("metadata is still shown, or the timeline is gone:\n%s", text)
	}
	if lean.Find("show.sec.rel.about.in") == nil {
		t.Fatal("a section with records was dropped")
	}
}

// The Edit form offers every prop: the schema's fields, the universal ones
// and whatever else the record holds.
func TestFormForRecordAllProps(t *testing.T) {
	w := build(t)
	schema := w.schema(data.KindTask)
	until := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	if _, _, err := w.store.Patch(data.PatchInput{Ref: w.sub1.Ref(), Fields: map[string]any{
		"zeta": "last", "estimate": 3, "urgent": true, "owner": core.Ref{Kind: data.KindPane, ID: "w12"}, "labels": []any{"a", "b"}}}, owner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.store.Star(w.sub1.Ref(), owner); err != nil {
		t.Fatal(err)
	}
	rec, _, err := w.store.Snooze(w.sub1.Ref(), until, owner)
	if err != nil {
		t.Fatal(err)
	}

	names := func(opts EditOptions) []string {
		var out []string
		for _, sp := range FormForRecordWith(schema, rec, opts).Specs[len(schema.Fields):] {
			out = append(out, sp.Name+":"+string(sp.Type))
		}
		return out
	}
	if got, want := strings.Join(names(DefaultEditOptions()), " "),
		"starStatus:string archiveStatus:string snoozedUntil:string estimate:int labels:list owner:ref urgent:bool zeta:string"; got != want {
		t.Fatalf("fields after the schema's = %q\nwant %q", got, want)
	}
	if got := strings.Join(names(EditOptions{ShowUniversalFields: true}), " "); got != "starStatus:string archiveStatus:string snoozedUntil:string" {
		t.Fatalf("universal only = %q", got)
	}
	if got := strings.Join(names(EditOptions{ShowExtraFields: true}), " "); got != "estimate:int labels:list owner:ref urgent:bool zeta:string" {
		t.Fatalf("extra only = %q", got)
	}
	fm := FormForRecord(schema, rec)
	if fm.Values[data.ColStarStatus] != data.StarStarred || fm.Values[data.ColArchiveStatus] != data.ArchiveActive || fm.Values[data.ColSnoozedUntil] != until.Format(time.RFC3339) {
		t.Fatalf("universal values = %v %v %v", fm.Values[data.ColStarStatus], fm.Values[data.ColArchiveStatus], fm.Values[data.ColSnoozedUntil])
	}
	// A new record has the universal fields at their defaults and no extras.
	blank := FormForRecord(schema, nil)
	if len(blank.Specs) != len(schema.Fields)+3 || blank.Values[data.ColStarStatus] != data.StarUnstarred {
		t.Fatalf("new form: %d fields, star %v", len(blank.Specs), blank.Values[data.ColStarStatus])
	}
}
