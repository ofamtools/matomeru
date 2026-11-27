package views

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/ui"
)

// toneAt returns the tone of the span covering rune offset i.
func toneAt(n *ui.ViewNode, i int) string {
	spans, _ := n.Props["spans"].([]ui.ToneSpan)
	for _, sp := range spans {
		if i >= sp.From && (sp.To == 0 || i < sp.To) {
			return sp.Tone
		}
	}
	return ""
}

func queueFixture() []data.QueueProgress {
	return []data.QueueProgress{
		{Name: "default", Total: 0},
		{
			Name: "review", QueueID: "q1", Queued: 2, Working: 1, NeedsYou: 1, Finished: 1, Done: 6, Total: 12, Ratio: 0.5,
			Lanes: []data.LaneProgressSegment{{
				LaneID: "root", Title: "ship lane", Status: "running", Done: 4, Total: 6,
				Children: []data.LaneProgressSegment{{LaneID: "kid", Title: "child lane", Status: "review", Done: 2, Total: 4,
					Sessions: []data.SessionProgressSegment{{PaneID: "p1", Title: "claude", Harness: "claude", Done: 2, Total: 4, Current: "Patch"}}}},
			}},
			Sessions: []data.SessionProgressSegment{{PaneID: "p3", Title: "loose", Harness: "codex", State: "running", Done: 1, Total: 2}},
		},
	}
}

func TestQueueProgressStripSegmentsTheBar(t *testing.T) {
	strip := QueueProgressStripView("home.queues", queueFixture())
	if strip.ID != "home.queues" || len(strip.Children) != 2 || strip.Box.FixedH != 2 {
		t.Fatalf("strip = %+v", strip)
	}
	empty, review := &strip.Children[0], &strip.Children[1]
	if !strings.Contains(nodeText(empty), strings.Repeat("·", queueBarWidth)) || !strings.Contains(nodeText(empty), "empty") {
		t.Errorf("an empty queue must draw an empty bar: %q", nodeText(empty))
	}

	line := nodeText(review)
	for _, want := range []string{"review", "6/12", "50%", "◇ 2 queued", "● 1 working", "? 1 needs you", "✓ 1 done"} {
		if !strings.Contains(line, want) {
			t.Errorf("review line is missing %q: %q", want, line)
		}
	}
	// Three segments (root lane 6, loose session 2, uncovered items 4) and two
	// separators share the bar's width exactly.
	if review.Facts["segments"] != 3 {
		t.Fatalf("segments fact = %v, want 3", review.Facts["segments"])
	}
	runes := []rune(line)
	start := slices.IndexFunc(runes, func(r rune) bool { return r == '█' || r == '░' })
	bar := runes[start : start+queueBarWidth]
	if n := strings.Count(string(bar), "│"); n != 2 {
		t.Fatalf("bar %q has %d separators, want 2", string(bar), n)
	}
	parts := strings.Split(string(bar), "│")
	widths := []int{utf8.RuneCountInString(parts[0]), utf8.RuneCountInString(parts[1]), utf8.RuneCountInString(parts[2])}
	if !slices.Equal(widths, []int{11, 4, 7}) {
		t.Errorf("segment widths = %v, want [11 4 7] for totals 6/2/4 over 22 cells", widths)
	}
	// Root lane is 4/6 done: 7 of its 11 cells are filled, in the lane tone.
	if got := strings.Count(parts[0], "█"); got != 7 {
		t.Errorf("root lane filled %d of 11 cells, want 7", got)
	}
	if tone := toneAt(review, start); tone != "state.working" {
		t.Errorf("lane segment tone = %q", tone)
	}
	if tone := toneAt(review, start+12); tone != "accent" {
		t.Errorf("session segment tone = %q", tone)
	}

	if def := QueueProgressStripView("", nil); def.ID != QueueStripID || len(def.Children) != 0 {
		t.Errorf("default strip = %+v", def)
	}
}

func TestSplitCells(t *testing.T) {
	for _, tc := range []struct {
		totals []int
		width  int
		want   []int
	}{
		{[]int{6, 2, 4}, 22, []int{11, 4, 7}},
		{[]int{1, 1, 100}, 10, []int{1, 1, 8}}, // every segment stays visible
		{[]int{5, 5}, 1, []int{1, 0}},          // narrower than the segments
		{[]int{0, 0}, 8, []int{0, 0}},
		{[]int{3}, 0, []int{0}},
	} {
		got := splitCells(tc.totals, tc.width)
		if !slices.Equal(got, tc.want) {
			t.Errorf("splitCells(%v, %d) = %v, want %v", tc.totals, tc.width, got, tc.want)
		}
	}
}

func TestBarCellsNeverLieAboutCompletion(t *testing.T) {
	bar := func(done, total, cells int) string {
		var l spanLine
		writeBarCells(&l, done, total, cells, "accent")
		return l.String()
	}
	if got := bar(1, 100, 4); got != "█░░░" {
		t.Errorf("started work must show at least one cell: %q", got)
	}
	if got := bar(99, 100, 4); got != "███░" {
		t.Errorf("unfinished work must never look complete: %q", got)
	}
	if got := bar(3, 3, 4); got != "████" {
		t.Errorf("finished = %q", got)
	}
	if got := bar(0, 0, 3); got != "░░░" {
		t.Errorf("no work = %q", got)
	}
}

func TestQueuePageViewLayoutFactsAndFilter(t *testing.T) {
	items := []QueueItemRow{
		{ID: "t1", Title: "Ship it", Status: data.AgentStatusQueued, Priority: "P1", Project: "Alpha", Lane: "ship lane", Agent: "claude", Dispatched: true},
		{ID: "t2", Title: "Review it", Status: data.AgentStatusWorking, Project: "Alpha"},
	}
	page := QueuePageView(QueuePageSpec{
		Queues: queueFixture(), ActiveQueue: "review", Items: items, SelectedIdx: 1, ProjectTitle: "Alpha", Section: "queues",
		Watchers: []QueueWatcherRow{
			{PaneID: "p1", Title: "claude", State: "running", WatchMode: data.WatchModeOnDone, WatchQueues: "review, bugs", Done: 2, Total: 4, Current: "Patch"},
			{PaneID: "p2", Title: "codex", State: "idle", WatchMode: data.WatchModeSchedule, IntervalSec: 60},
		},
	})
	if page.ID != QueuePageID || page.Scope != ScopeQueue || ScopeQueuePage != ScopeQueue {
		t.Fatalf("page = %s scope %s", page.ID, page.Scope)
	}
	wantFacts := map[string]any{"queueName": "review", "queueCount": 2, "count": 2, "hasFilter": false, "groupBy": "",
		"hasSelection": true, "selectedTask": "t2", "selectedStatus": data.AgentStatusWorking,
		"selectedNeedsYou": false, "needsYouCount": 0, "hasNeedsYou": true, "watcherCount": 2, "section": "queues"}
	for k, want := range wantFacts {
		if page.Facts[k] != want {
			t.Errorf("fact %s = %v, want %v", k, page.Facts[k], want)
		}
	}

	list := page.Find(QueueListID)
	if list == nil || list.Kind != ui.KindTable || !list.Focusable {
		t.Fatalf("items table = %+v", list)
	}
	if list.Filter == nil || list.Filter.ID != QueueListID {
		t.Fatalf("the items table must declare ViewNode.Filter so / works through trait.filter: %+v", list.Filter)
	}
	rows := list.Props["rows"].([][]any)
	if len(rows) != 2 || rows[0][1] != "dispatched" || rows[1][1] != data.AgentStatusWorking || list.Props["selected"] != 1 {
		t.Errorf("rows = %v selected %v", rows, list.Props["selected"])
	}

	header := nodeText(page.Find("queue.header"))
	if !strings.Contains(header, "Queues · Alpha") || !strings.Contains(header, "[review (4)]") || !strings.Contains(header, "[default (0)]") {
		t.Errorf("header = %q", header)
	}
	if strip := page.Find(QueueStripID); strip == nil || !strings.Contains(nodeText(&strip.Children[1]), "▸ review") {
		t.Errorf("the active queue is not marked in the strip")
	}
	// Queue -> Lanes -> Child Lanes -> AI Checklist, indented by depth.
	for id, want := range map[string]string{
		"queue.lane.root":    "    ⎇ ship lane",
		"queue.lane.kid":     "      ⎇ child lane",
		"queue.session.p1":   "        ▸ claude",
		"queue.session.p3":   "    ▸ loose (codex)",
		QueueWatchersID:      "● claude (on_done · review,bugs) 2/4 Patch",
		"queue.session.none": "",
	} {
		got := nodeText(page.Find(id))
		if want == "" {
			continue
		}
		if !strings.Contains(got, want) {
			t.Errorf("%s = %q, want it to contain %q", id, got, want)
		}
	}
	if w := nodeText(page.Find(QueueWatchersID)); !strings.Contains(w, "✓ codex (schedule 60s · any)") {
		t.Errorf("watchers = %q", w)
	}
	if !strings.Contains(nodeText(page.Find("queue.session.p1")), "Patch") {
		t.Error("a session line must show its checklist step in progress")
	}

	filtered := QueuePageView(QueuePageSpec{Queues: queueFixture(), ActiveQueue: "review", FilterQuery: "zzz", SelectedIdx: -1})
	if filtered.Facts["hasFilter"] != true || filtered.Facts["hasSelection"] != false {
		t.Errorf("filtered facts = %v", filtered.Facts)
	}
	if body := filtered.Find(QueueListID); body.Kind != ui.KindText || !strings.Contains(nodeText(body), "No queue items match") {
		t.Errorf("empty filtered body = %+v", body)
	}
	if nodeText(filtered.Find("queue.filter")) != " /zzz" {
		t.Error("the active filter is not shown")
	}

	bare := QueuePageView(QueuePageSpec{})
	if bare.Facts["queueName"] != data.DefaultQueueName || !strings.Contains(nodeText(bare.Find(QueueWatchersID)), "no agents watching") {
		t.Errorf("empty page = %v / %q", bare.Facts, nodeText(bare.Find(QueueWatchersID)))
	}
	if _, section := bare.Facts["section"]; section {
		t.Error("a standalone page must not publish a section fact")
	}
}

func TestQueueLaneDetailIsCapped(t *testing.T) {
	q := data.QueueProgress{Name: "big"}
	for i := range queueMaxLaneDetail + 5 {
		q.Lanes = append(q.Lanes, data.LaneProgressSegment{LaneID: string(rune('a' + i)), Title: "lane", Total: 1})
	}
	lines := queueLaneDetail(q)
	if len(lines) != queueMaxLaneDetail+1 || !strings.Contains(nodeText(&lines[len(lines)-1]), "+5 more") {
		t.Errorf("%d detail lines, last %q", len(lines), nodeText(&lines[len(lines)-1]))
	}
}

func TestGroupQueueItems(t *testing.T) {
	items := []QueueItemRow{
		{ID: "a", Status: data.AgentStatusQueued, Project: "Beta"},
		{ID: "b", Status: data.AgentStatusDone, Project: "Alpha"},
		{ID: "c", Status: data.AgentStatusNeedsYou, Project: "Beta"},
		{ID: "d", Status: data.AgentStatusQueued},
	}
	ids := func(rows []QueueItemRow) string {
		var b strings.Builder
		for _, r := range rows {
			b.WriteString(r.ID)
		}
		return b.String()
	}
	if got := ids(GroupQueueItems(items, QueueGroupStatus)); got != "cadb" {
		t.Errorf("by status = %s, want needs-you, queued (in queue order), done", got)
	}
	if got := ids(GroupQueueItems(items, QueueGroupProject)); got != "acbd" {
		t.Errorf("by project = %s, want groups in order of first appearance", got)
	}
	if got := ids(GroupQueueItems(items, QueueGroupNone)); got != "abcd" {
		t.Errorf("ungrouped = %s", got)
	}
	if ids(items) != "abcd" {
		t.Error("GroupQueueItems reordered its input")
	}

	cols, rows := queueItemRows(GroupQueueItems(items, QueueGroupProject), QueueGroupProject)
	if cols[0] != "Project" || len(cols) != 7 {
		t.Fatalf("grouped columns = %v", cols)
	}
	var groups []any
	for _, r := range rows {
		groups = append(groups, r[0])
	}
	if !slices.Equal(groups, []any{"Beta", "", "Alpha", "(no project)"}) {
		t.Errorf("group column = %v, want each group named once", groups)
	}
}
