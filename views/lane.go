package views

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// LaneBarID is the node ID of the ambient lane progress bar above a pane's site.
const LaneBarID = "site.lanebar"

// Lane status groups of the progress bar.
const (
	laneDone    = "done"    // done, merged
	laneActive  = "active"  // running, review
	lanePlanned = "planned" // planned, parked
	laneDropped = "dropped" // cancelled: not counted
)

func laneGroup(status string) string {
	switch status {
	case "done", "merged":
		return laneDone
	case "running", "review":
		return laneActive
	case "cancelled":
		return laneDropped
	}
	return lanePlanned
}

// LaneRollup is the progress of one lane tree: its root and the state of the
// lanes under it (or of the root itself when it has none).
type LaneRollup struct {
	Root    data.Record
	Done    int
	Active  int
	Planned int
	// Running names the lanes that are running or in review, in order.
	Running []string
	// OtherTrees counts the other active lane trees the same pane or task has.
	OtherTrees int
}

// Total is the number of lanes counted.
func (r LaneRollup) Total() int { return r.Done + r.Active + r.Planned }

// IsActive reports whether any counted lane is still open.
func (r LaneRollup) IsActive() bool { return r.Active+r.Planned > 0 }

// Cells is the bar itself: one cell per lane, done first, then running, then
// planned.
func (r LaneRollup) Cells() string {
	return strings.Repeat("█", r.Done) + strings.Repeat("▓", r.Active) + strings.Repeat("░", r.Planned)
}

// Text is the one-line strip: "Lane: title  [███▓░░] 3/6 done · 1 running
// (name) · 2 planned".
func (r LaneRollup) Text() string {
	title := Title(data.Schema{SummaryField: "title"}, r.Root)
	if r.OtherTrees > 0 {
		title += fmt.Sprintf(" (+%d)", r.OtherTrees)
	}
	parts := []string{fmt.Sprintf("%d/%d done", r.Done, r.Total())}
	if r.Active > 0 {
		part := fmt.Sprintf("%d running", r.Active)
		if len(r.Running) > 0 {
			part += " (" + r.Running[0] + ")"
		}
		parts = append(parts, part)
	}
	if r.Planned > 0 {
		parts = append(parts, fmt.Sprintf("%d planned", r.Planned))
	}
	return fmt.Sprintf("Lane: %s  [%s] %s", title, r.Cells(), strings.Join(parts, " · "))
}

// laneRoot walks parentLane up to the root lane. A parent that is not stored
// (or a loop) ends the walk at the last lane found.
func laneRoot(store *data.Store, lane data.Record) data.Record {
	seen := map[string]bool{lane.ID: true}
	for {
		parent, ok := lane.RefField("parentLane")
		if !ok || seen[parent.ID] {
			return lane
		}
		rec, err := store.Get(parent)
		if err != nil {
			return lane
		}
		seen[parent.ID] = true
		lane = *rec
	}
}

// RollupLane counts the lanes directly under root, or root itself when it
// has none.
func RollupLane(store *data.Store, root data.Record) (LaneRollup, error) {
	out := LaneRollup{Root: root}
	ref := root.Ref()
	res, err := store.Query(data.Query{Kind: data.KindLane, Parent: &ref, ParentField: "parentLane"})
	if err != nil {
		return out, err
	}
	lanes := res.Items
	if len(lanes) == 0 {
		lanes = []data.Record{root}
	}
	for _, lane := range lanes {
		switch laneGroup(lane.Text("laneStatus")) {
		case laneDone:
			out.Done++
		case laneActive:
			out.Active++
			out.Running = append(out.Running, Title(data.Schema{SummaryField: "title"}, lane))
		case lanePlanned:
			out.Planned++
		}
	}
	return out, nil
}

// activeTrees returns the rollups of the active lane trees among lanes, one
// per distinct root, in the order first met.
func activeTrees(store *data.Store, lanes []data.Record) ([]LaneRollup, error) {
	var out []LaneRollup
	seen := map[string]bool{}
	for _, lane := range lanes {
		root := laneRoot(store, lane)
		if seen[root.ID] || root.IsArchived() {
			continue
		}
		seen[root.ID] = true
		roll, err := RollupLane(store, root)
		if err != nil {
			return nil, err
		}
		if roll.IsActive() {
			out = append(out, roll)
		}
	}
	return out, nil
}

// ActiveLane finds the lane tree a pane is working in. Lanes and panes are
// many to many, so nothing here assumes one lane per pane:
//
//  1. the lanes that claim the pane (a "claim" relation from lane to pane),
//     each walked up to its root;
//  2. otherwise the lanes of the pane's task, then of that task's parent, and
//     so on up to the root task: the nearest level with an active tree wins.
//
// A tree is active while any lane counted in it is planned, parked, running
// or in review. It reports false when no active tree applies.
func ActiveLane(store *data.Store, pane data.Record) (LaneRollup, bool, error) {
	pick := func(lanes []data.Record) (LaneRollup, bool, error) {
		trees, err := activeTrees(store, lanes)
		if err != nil || len(trees) == 0 {
			return LaneRollup{}, false, err
		}
		// A tree with a lane running leads; the rest are counted.
		slices.SortStableFunc(trees, func(a, b LaneRollup) int { return min(b.Active, 1) - min(a.Active, 1) })
		trees[0].OtherTrees = len(trees) - 1
		return trees[0], true, nil
	}

	claims, err := store.RelatedRecords(pane.Ref(), data.RelClaim, data.Incoming)
	if err != nil {
		return LaneRollup{}, false, err
	}
	var claimed []data.Record
	for _, rec := range claims {
		if rec.Kind == data.KindLane {
			claimed = append(claimed, rec)
		}
	}
	if roll, ok, err := pick(claimed); ok || err != nil {
		return roll, ok, err
	}

	task, ok := pane.RefField("task")
	for seen := map[string]bool{}; ok && !seen[task.ID]; {
		seen[task.ID] = true
		res, err := store.Query(data.Query{Kind: data.KindLane, Parent: &task, ParentField: "task"})
		if err != nil {
			return LaneRollup{}, false, err
		}
		if roll, found, err := pick(res.Items); found || err != nil {
			return roll, found, err
		}
		rec, err := store.Get(task)
		if err != nil {
			break
		}
		task, ok = rec.RefField("parent")
	}
	return LaneRollup{}, false, nil
}

// LaneBar renders the ambient progress strip of the lane tree a pane works
// in: one line, shown above the pane's site and never in its table of
// contents. hint names the key that opens the lane ("" leaves it out). width,
// when positive, is the room there is: the hint is dropped first, then the
// line is clipped. It reports false when no active lane applies.
func LaneBar(store *data.Store, pane data.Record, hint string, width int) (ui.ViewNode, bool) {
	if store == nil {
		return ui.ViewNode{}, false
	}
	roll, ok, err := ActiveLane(store, pane)
	if err != nil || !ok {
		return ui.ViewNode{}, false
	}
	line := " " + roll.Text()
	if with := line + "  (" + hint + ")"; hint != "" && (width <= 0 || len([]rune(with)) <= width) {
		line = with
	}
	if r := []rune(line); width > 1 && len(r) > width {
		line = string(r[:width-1]) + "…"
	}
	ref := roll.Root.Ref()
	return ui.ViewNode{ID: LaneBarID, Kind: ui.KindText, Tone: "accent", Ref: &ref, Box: layout.Constraints{FixedH: 1},
		Props: map[string]any{"text": line},
		Facts: map[string]any{"lane": ref.ID, "done": roll.Done, "running": roll.Active, "planned": roll.Planned, "trees": roll.OtherTrees + 1}}, true
}

// --- timeline ---------------------------------------------------------------------

// TimelineEntry is one line of a lane's timeline.
type TimelineEntry struct {
	At   string // when, formatted
	Kind string // milestone, decision, blocker, verification, note, or "change"
	Text string
	By   string
	Ref  *core.Ref // the note, for a logged entry

	sort int64
}

// aboutOnly reports whether a change set only attached notes: the notes
// themselves are in the timeline already.
func aboutOnly(cs data.ChangeSet) bool {
	for _, ch := range cs.Changes {
		if ch.Op != data.OpRelate && ch.Op != data.OpUnrelate {
			return false
		}
		var rel data.Relation
		raw := ch.New
		if ch.Op == data.OpUnrelate {
			raw = ch.Old
		}
		if b, err := json.Marshal(raw); err != nil || json.Unmarshal(b, &rel) != nil || rel.Kind != data.RelAbout {
			return false
		}
	}
	return len(cs.Changes) > 0
}

// Timeline merges the notes about a record with its recent change sets,
// oldest first: what was logged and what was changed, in the order it
// happened. limit bounds the change sets read (0 reads 50).
func Timeline(store *data.Store, ref core.Ref, limit int) ([]TimelineEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	notes, err := store.RelatedRecords(ref, data.RelAbout, data.Incoming)
	if err != nil {
		return nil, err
	}
	var out []TimelineEntry
	for _, note := range notes {
		if note.Kind != data.KindNote || note.IsArchived() {
			continue
		}
		at := note.CreatedAt
		if logged := note.Time("loggedAt"); !logged.IsZero() {
			at = logged
		}
		kind, text := note.Text("logKind"), note.Text("title")
		if k, rest, ok := strings.Cut(text, ": "); ok && (kind == "" || k == kind) && !strings.Contains(k, " ") {
			kind, text = k, rest
		}
		if kind == "" {
			kind = "note"
		}
		if text == "" {
			text = firstLine(note.Text("body"), 100)
		}
		noteRef := note.Ref()
		out = append(out, TimelineEntry{At: at.Format(timeLayout), Kind: kind, Text: text, Ref: &noteRef, sort: at.UnixNano()})
	}
	sets, err := store.History(&ref, limit)
	if err != nil {
		return nil, err
	}
	for _, cs := range sets {
		if aboutOnly(cs) {
			continue
		}
		text := cs.Summary
		if text == "" {
			text = cs.Action
		}
		by := cs.Actor.Label
		if by == "" {
			by = cs.Actor.ID
		}
		out = append(out, TimelineEntry{At: cs.CreatedAt.Format(timeLayout), Kind: "change", Text: firstLine(text, 100), By: by,
			sort: cs.CreatedAt.UnixNano()})
	}
	slices.SortStableFunc(out, func(a, b TimelineEntry) int {
		switch {
		case a.sort < b.sort:
			return -1
		case a.sort > b.sort:
			return 1
		}
		return 0
	})
	return out, nil
}

var timelineGlyph = map[string]string{"milestone": "◆", "decision": "◇", "blocker": "■", "verification": "✓", "change": "·"}

// timelinePanel is the Timeline section of a Show page.
func timelinePanel(store *data.Store, ref core.Ref) (ui.ViewNode, error) {
	entries, err := Timeline(store, ref, 0)
	if err != nil {
		return ui.ViewNode{}, err
	}
	// A table: one row per entry, clipped to the panel rather than wrapped.
	rows := make([]any, 0, len(entries))
	facts := make([]any, 0, len(entries))
	for _, e := range entries {
		glyph := timelineGlyph[e.Kind]
		if glyph == "" {
			glyph = "•"
		}
		what := e.Text
		if e.By != "" {
			what += "  (" + e.By + ")"
		}
		rows = append(rows, []any{e.At, glyph + " " + e.Kind, what})
		facts = append(facts, map[string]any{"kind": e.Kind, "at": e.At, "text": e.Text})
	}
	body := text("show.timeline.rows", "nothing logged yet", "muted")
	if len(rows) > 0 {
		body = fixed(ui.ViewNode{ID: "show.timeline.rows", Kind: ui.KindTable, Props: map[string]any{
			"columns": []any{map[string]any{"key": "when", "title": "When", "width": 16}, map[string]any{"key": "kind", "title": "Kind", "width": 14},
				map[string]any{"key": "what", "title": "What"}},
			"rows": rows,
		}}, 2+len(rows))
	}
	return ui.ViewNode{ID: "show.timeline", Kind: ui.KindPanel, Title: "Timeline", Border: true,
		Badges: []ui.Badge{{Text: fmt.Sprintf("%d", len(entries))}}, Facts: map[string]any{"entries": facts},
		Children: []ui.ViewNode{body}}, nil
}
