package views

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// Queue page scope, node IDs and grouping modes.
const (
	// ScopeQueue is the page's own scope. It is not "queue": that is the
	// noun's scope, which the Show page gives to sections of queue records.
	ScopeQueue         = "queue.page"
	QueuePageID        = "queue.page"
	QueueListID        = "queue.list"
	QueueStripID       = "queue.progress"
	QueueWatchersID    = "queue.watchers"
	QueueGroupNone     = ""
	QueueGroupStatus   = "status"
	QueueGroupProject  = "project"
	queueBarWidth      = 24
	queueLaneBarWidth  = 10
	queueMaxLaneDetail = 12
)

// QueueGroupModes lists the grouping modes of the items table in cycle order.
var QueueGroupModes = []string{QueueGroupNone, QueueGroupStatus, QueueGroupProject}

// QueueItemRow is one queued task, pre-formatted by the caller.
type QueueItemRow struct {
	ID         string
	Title      string
	Status     string // agent status: queued, working, needs_you, done
	Priority   string
	Project    string
	Lane       string
	Agent      string // pane the item was dispatched to
	Dispatched bool
}

// QueueWatcherRow is one agent pane and how it watches the queues.
type QueueWatcherRow struct {
	PaneID      string
	Title       string
	Harness     string
	State       string
	WatchMode   string
	WatchQueues string
	IntervalSec int
	Current     string // checklist step in progress
	Done, Total int    // checklist steps
}

// QueuePageSpec is the pure data contract of QueuePageView.
type QueuePageSpec struct {
	// Queues are every named queue in tab order; ActiveQueue names the one
	// whose items and lane detail are shown.
	Queues      []data.QueueProgress
	ActiveQueue string
	// Items are the active queue's rows in display order (already filtered
	// and grouped by the caller).
	Items       []QueueItemRow
	Watchers    []QueueWatcherRow
	FilterQuery string
	GroupBy     string
	SelectedIdx int
	// ProjectTitle is set when the page is scoped to one project.
	ProjectTitle string
	// Section, when set, is the Project Portal section the page is embedded
	// in; it is published as the "section" fact.
	Section string
}

// QueuePageView presents the queues, their segmented progress, the active
// queue's items and the watching agents. It reads nothing but its spec.
func QueuePageView(spec QueuePageSpec) ui.ViewNode {
	active := data.NormalizeQueueName(spec.ActiveQueue)
	if !slices.Contains(QueueGroupModes, spec.GroupBy) {
		spec.GroupBy = QueueGroupNone
	}
	filtering := strings.TrimSpace(spec.FilterQuery) != ""

	columns, rows := queueItemRows(spec.Items, spec.GroupBy)
	facts := map[string]any{
		"queueName":      active,
		"queueCount":     len(spec.Queues),
		"count":          len(rows),
		"hasFilter":      filtering,
		"groupBy":        spec.GroupBy,
		"hasSelection":   false,
		"selectedTask":   "",
		"selectedStatus": "",
		"watcherCount":   len(spec.Watchers),
	}
	if spec.Section != "" {
		facts["section"] = spec.Section
	}
	selected := -1
	if i := spec.SelectedIdx; i >= 0 && i < len(spec.Items) {
		selected = i
		facts["hasSelection"] = true
		facts["selectedTask"] = spec.Items[i].ID
		facts["selectedStatus"] = spec.Items[i].Status
	}

	body := ui.ViewNode{
		ID:        QueueListID,
		Kind:      ui.KindTable,
		Focusable: true,
		Filter:    &ui.FilterSpec{ID: QueueListID, Prefix: "queue ", Fields: []string{"title", "status", "project", "lane", "agent"}},
		Box:       layout.Constraints{Flex: 1},
		Props: map[string]any{
			"columns":   columns,
			"rows":      rows,
			"selected":  selected,
			"scrollKey": active + "|" + spec.GroupBy,
		},
	}
	if len(rows) == 0 {
		empty := fmt.Sprintf("Nothing in %q yet · a adds a task · C creates a named queue", active)
		if filtering {
			empty = "No queue items match the filter."
		}
		body.Kind = ui.KindText
		body.Tone = "muted"
		body.Props = map[string]any{"text": " " + empty}
	}

	children := []ui.ViewNode{queueHeader(spec, active)}
	if len(spec.Queues) > 0 {
		children = append(children, queueProgressStrip(QueueStripID, spec.Queues, active))
	}
	for _, q := range spec.Queues {
		if q.Name == active {
			children = append(children, queueLaneDetail(q)...)
		}
	}
	children = append(children, queueWatchersStrip(spec.Watchers))
	if filtering {
		children = append(children, fixed(text("queue.filter", " /"+spec.FilterQuery, "accent"), 1))
	}
	children = append(children, body)
	return ui.ViewNode{
		ID:       QueuePageID,
		Kind:     ui.KindVStack,
		Scope:    ScopeQueue,
		Box:      layout.Constraints{Flex: 1},
		Facts:    facts,
		Children: children,
	}
}

// QueueProgressStripView renders one segmented progress bar per queue:
// Queue -> Lanes -> Child Lanes -> AI Checklist. It is the same component on
// the Queue Page and on the Project Portal Homepage.
func QueueProgressStripView(id string, queues []data.QueueProgress) ui.ViewNode {
	return queueProgressStrip(id, queues, "")
}

func queueProgressStrip(id string, queues []data.QueueProgress, active string) ui.ViewNode {
	if id == "" {
		id = QueueStripID
	}
	nameW := 0
	for _, q := range queues {
		nameW = max(nameW, utf8.RuneCountInString(q.Name))
	}
	nameW = min(nameW, 24)
	lines := make([]ui.ViewNode, 0, len(queues))
	for i, q := range queues {
		var line spanLine
		marker, nameTone := "  ", ""
		if active != "" && q.Name == active {
			marker, nameTone = " ▸", "selected"
		}
		line.write(marker+" ", "")
		line.write(padRunes(q.Name, nameW), nameTone)
		line.write("  ", "")
		queueBar(&line, q, queueBarWidth)
		line.write(fmt.Sprintf("  %d/%d", q.Done, q.Total), "")
		line.write(fmt.Sprintf(" %3d%%", int(math.Round(q.Ratio*100))), queueRatioTone(q.Ratio, q.Total))
		line.write("  ", "")
		queueCounts(&line, q)
		lines = append(lines, fixed(ui.ViewNode{
			ID:    fmt.Sprintf("%s.%d", id, i),
			Kind:  ui.KindText,
			Props: map[string]any{"text": line.String(), "spans": line.spans},
			Facts: map[string]any{"queueName": q.Name, "done": q.Done, "total": q.Total, "segments": len(queueSegments(q))},
		}, 1))
	}
	return ui.ViewNode{
		ID:       id,
		Kind:     ui.KindVStack,
		Box:      layout.Constraints{FixedH: len(lines)},
		Facts:    map[string]any{"queueCount": len(queues)},
		Children: lines,
	}
}

// queueSegment is one cell run of a queue's bar: a root lane, a session
// outside any lane, or the items nobody is working yet.
type queueSegment struct {
	done, total int
	tone        string
}

func queueSegments(q data.QueueProgress) []queueSegment {
	segs := make([]queueSegment, 0, len(q.Lanes)+len(q.Sessions)+1)
	covered, coveredDone := 0, 0
	for _, lane := range q.Lanes {
		segs = append(segs, queueSegment{lane.Done, lane.Total, "state.working"})
		covered, coveredDone = covered+lane.Total, coveredDone+lane.Done
	}
	for _, sess := range q.Sessions {
		segs = append(segs, queueSegment{sess.Done, sess.Total, "accent"})
		covered, coveredDone = covered+sess.Total, coveredDone+sess.Done
	}
	if rest := q.Total - covered; rest > 0 {
		segs = append(segs, queueSegment{q.Done - coveredDone, rest, "state.done"})
	}
	return segs
}

// queueBar writes a segmented bar: one run of cells per segment, sized by its
// share of the total, filled by how much of it is done.
func queueBar(line *spanLine, q data.QueueProgress, width int) {
	segs := queueSegments(q)
	if len(segs) == 0 {
		line.write(strings.Repeat("·", width), "muted")
		return
	}
	totals := make([]int, len(segs))
	for i, seg := range segs {
		totals[i] = seg.total
	}
	// Separators take cells too; drop them when the bar is too narrow.
	seps := len(segs) - 1
	if width-seps < len(segs) {
		seps = 0
	}
	cells := splitCells(totals, width-seps)
	for i, seg := range segs {
		if i > 0 && seps > 0 {
			line.write("│", "muted")
		}
		writeBarCells(line, seg.done, seg.total, cells[i], seg.tone)
	}
}

func writeBarCells(line *spanLine, done, total, cells int, tone string) {
	if cells <= 0 {
		return
	}
	filled := 0
	if total > 0 {
		filled = int(math.Round(float64(done) * float64(cells) / float64(total)))
		if done > 0 && filled == 0 {
			filled = 1
		}
		if done < total && filled == cells {
			filled = cells - 1
		}
	}
	if done >= total && total > 0 {
		tone = "state.done"
	}
	line.write(strings.Repeat("█", filled), tone)
	line.write(strings.Repeat("░", cells-filled), "muted")
}

// splitCells shares width between segments by their totals (largest
// remainder), giving every segment at least one cell when there is room.
func splitCells(totals []int, width int) []int {
	out := make([]int, len(totals))
	sum := 0
	for _, t := range totals {
		sum += max(t, 0)
	}
	if width <= 0 || sum == 0 {
		return out
	}
	type rem struct {
		i    int
		frac float64
	}
	rems := make([]rem, len(totals))
	used := 0
	for i, t := range totals {
		exact := float64(max(t, 0)) * float64(width) / float64(sum)
		out[i] = int(exact)
		used += out[i]
		rems[i] = rem{i, exact - float64(out[i])}
	}
	slices.SortStableFunc(rems, func(a, b rem) int {
		switch {
		case a.frac > b.frac:
			return -1
		case a.frac < b.frac:
			return 1
		}
		return 0
	})
	for k := 0; used < width; k, used = k+1, used+1 {
		out[rems[k%len(rems)].i]++
	}
	if width >= len(totals) {
		for i := range out {
			for out[i] == 0 {
				big := 0
				for j := range out {
					if out[j] > out[big] {
						big = j
					}
				}
				out[big]--
				out[i]++
			}
		}
	}
	return out
}

func queueCounts(line *spanLine, q data.QueueProgress) {
	parts := []struct {
		n          int
		icon, tone string
		label      string
	}{
		{q.Queued, "◇", "muted", "queued"},
		{q.Working, "●", "state.working", "working"},
		{q.NeedsYou, "?", "state.needs-input", "needs you"},
		{q.Finished, "✓", "state.done", "done"},
	}
	wrote := false
	for _, p := range parts {
		if p.n == 0 {
			continue
		}
		if wrote {
			line.write(" · ", "muted")
		}
		line.write(fmt.Sprintf("%s %d %s", p.icon, p.n, p.label), p.tone)
		wrote = true
	}
	if !wrote {
		line.write("empty", "muted")
	}
}

func queueRatioTone(r float64, total int) string {
	switch {
	case total == 0:
		return "muted"
	case r >= 1:
		return "state.done"
	case r > 0:
		return "state.working"
	}
	return "muted"
}

// queueLaneDetail lists the active queue's lanes, child lanes and sessions
// under its bar, each with its own bar, capped so the items table keeps room.
func queueLaneDetail(q data.QueueProgress) []ui.ViewNode {
	var out []ui.ViewNode
	more := 0
	add := func(id string, line spanLine) {
		if len(out) >= queueMaxLaneDetail {
			more++
			return
		}
		out = append(out, fixed(ui.ViewNode{
			ID: id, Kind: ui.KindText,
			Props: map[string]any{"text": line.String(), "spans": line.spans},
		}, 1))
	}
	session := func(depth int, s data.SessionProgressSegment) {
		var line spanLine
		line.write(strings.Repeat("  ", depth+2)+"▸ ", "muted")
		name := s.Title
		if name == "" {
			name = s.PaneID
		}
		line.write(name, "")
		if s.Harness != "" && !containsFold(name, s.Harness) {
			line.write(" ("+s.Harness+")", "muted")
		}
		line.write("  ", "")
		writeBarCells(&line, s.Done, s.Total, queueLaneBarWidth, "accent")
		line.write(fmt.Sprintf("  %d/%d", s.Done, s.Total), "")
		if s.Current != "" {
			line.write(" · "+s.Current, "muted")
		} else if s.State != "" {
			line.write(" · "+s.State, "muted")
		}
		add("queue.session."+s.PaneID, line)
	}
	var lane func(depth int, l data.LaneProgressSegment)
	lane = func(depth int, l data.LaneProgressSegment) {
		var line spanLine
		line.write(strings.Repeat("  ", depth+2)+"⎇ ", "muted")
		line.write(l.Title, "")
		line.write("  ", "")
		writeBarCells(&line, l.Done, l.Total, queueLaneBarWidth, "state.working")
		line.write(fmt.Sprintf("  %d/%d", l.Done, l.Total), "")
		if l.Status != "" {
			line.write(" · "+l.Status, "muted")
		}
		add("queue.lane."+l.LaneID, line)
		for _, child := range l.Children {
			lane(depth+1, child)
		}
		for _, s := range l.Sessions {
			session(depth+1, s)
		}
	}
	for _, l := range q.Lanes {
		lane(0, l)
	}
	for _, s := range q.Sessions {
		session(0, s)
	}
	if more > 0 {
		out = append(out, fixed(text("queue.lanes.more", fmt.Sprintf("      +%d more lanes and sessions", more), "muted"), 1))
	}
	return out
}

func queueHeader(spec QueuePageSpec, active string) ui.ViewNode {
	var line spanLine
	title := " Queues"
	if p := strings.TrimSpace(spec.ProjectTitle); p != "" {
		title += " · " + p
	}
	line.write(title+" ", "accent")
	for _, q := range spec.Queues {
		tone := "muted"
		if q.Name == active {
			tone = "selected"
		}
		line.write(" ", "")
		line.write(fmt.Sprintf("[%s (%d)]", q.Name, q.Queued+q.Working+q.NeedsYou), tone)
	}
	if spec.GroupBy != QueueGroupNone {
		line.write("  ", "")
		line.write(" Group: "+spec.GroupBy+" ", "badge.accent")
	}
	return fixed(ui.ViewNode{
		ID:    "queue.header",
		Kind:  ui.KindText,
		Props: map[string]any{"text": line.String(), "spans": line.spans},
	}, 1)
}

func queueWatchersStrip(watchers []QueueWatcherRow) ui.ViewNode {
	var line spanLine
	line.write(" WATCHING ", "muted")
	if len(watchers) == 0 {
		line.write("no agents watching · enter dispatches to a new agent · W sets a pane's watch cadence", "muted")
	}
	for i, w := range watchers {
		if i > 0 {
			line.write("  ·  ", "muted")
		}
		icon, tone := "○", "muted"
		switch strings.ToLower(w.State) {
		case "running":
			icon, tone = "●", "state.working"
		case "needs_you":
			icon, tone = "?", "state.needs-input"
		case "done", "idle":
			icon, tone = "✓", "state.done"
		}
		line.write(icon+" ", tone)
		name := w.Title
		if name == "" {
			name = w.PaneID
		}
		line.write(name, "")
		mode := data.NormalizeWatchMode(w.WatchMode)
		if mode == data.WatchModeSchedule && w.IntervalSec > 0 {
			mode = fmt.Sprintf("%s %ds", mode, w.IntervalSec)
		}
		line.write(" ("+mode+" · "+strings.Join(data.ParseWatchQueues(w.WatchQueues), ",")+")", "muted")
		if w.Total > 0 {
			line.write(fmt.Sprintf(" %d/%d", w.Done, w.Total), "accent")
		}
		if w.Current != "" {
			line.write(" "+w.Current, "muted")
		}
	}
	return fixed(ui.ViewNode{
		ID:    QueueWatchersID,
		Kind:  ui.KindText,
		Props: map[string]any{"text": line.String(), "spans": line.spans},
		Facts: map[string]any{"watcherCount": len(watchers)},
	}, 1)
}

func queueItemRows(items []QueueItemRow, groupBy string) (columns []any, rows [][]any) {
	columns = []any{"Task", "Status", "Pri", "Project", "Lane", "Agent"}
	if groupBy != QueueGroupNone {
		columns = append([]any{Label(groupBy)}, columns...)
	}
	prev, first := "", true
	for _, item := range items {
		status := item.Status
		if item.Dispatched && status == data.AgentStatusQueued {
			status = "dispatched"
		}
		cells := []any{item.Title, status, item.Priority, item.Project, item.Lane, item.Agent}
		if groupBy != QueueGroupNone {
			group := QueueGroupKey(item, groupBy)
			shown := group
			if !first && group == prev {
				shown = "" // name each group once, on its first row
			}
			prev, first = group, false
			cells = append([]any{shown}, cells...)
		}
		rows = append(rows, cells)
	}
	return columns, rows
}

// QueueGroupKey is the group an item falls in under groupBy.
func QueueGroupKey(item QueueItemRow, groupBy string) string {
	switch groupBy {
	case QueueGroupStatus:
		return item.Status
	case QueueGroupProject:
		if item.Project == "" {
			return "(no project)"
		}
		return item.Project
	}
	return ""
}

// GroupQueueItems orders items so each group is contiguous, keeping queue
// order inside a group and groups in order of first appearance. Status
// groups follow the agent workflow instead.
func GroupQueueItems(items []QueueItemRow, groupBy string) []QueueItemRow {
	if groupBy == QueueGroupNone || !slices.Contains(QueueGroupModes, groupBy) {
		return items
	}
	rank := map[string]int{}
	if groupBy == QueueGroupStatus {
		rank = map[string]int{data.AgentStatusNeedsYou: -4, data.AgentStatusWorking: -3, data.AgentStatusQueued: -2, data.AgentStatusDone: -1}
	}
	for _, item := range items {
		key := QueueGroupKey(item, groupBy)
		if _, ok := rank[key]; !ok {
			rank[key] = len(rank)
		}
	}
	out := slices.Clone(items)
	slices.SortStableFunc(out, func(a, b QueueItemRow) int {
		return rank[QueueGroupKey(a, groupBy)] - rank[QueueGroupKey(b, groupBy)]
	})
	return out
}

// spanLine builds one line of text with tone spans at rune offsets.
type spanLine struct {
	b      strings.Builder
	spans  []ui.ToneSpan
	cursor int
}

func (l *spanLine) write(value, tone string) {
	if value == "" {
		return
	}
	l.b.WriteString(value)
	end := l.cursor + utf8.RuneCountInString(value)
	if tone != "" {
		l.spans = append(l.spans, ui.ToneSpan{From: l.cursor, To: end, Tone: tone})
	}
	l.cursor = end
}

func (l *spanLine) String() string { return l.b.String() }

func padRunes(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n > width && width > 1 {
		return string([]rune(s)[:width-1]) + "…"
	}
	if n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}
