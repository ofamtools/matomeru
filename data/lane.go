package data

import "strings"

const laneStatusField = "laneStatus"

// LaneProgress groups lane statuses for completion counts.
type LaneProgress uint8

const (
	LaneProgressPlanned LaneProgress = iota
	LaneProgressActive
	LaneProgressDone
	LaneProgressExcluded
)

// IsOpen reports whether the lane still has work awaiting completion.
func (p LaneProgress) IsOpen() bool {
	return p == LaneProgressPlanned || p == LaneProgressActive
}

var laneStates = []struct {
	status   StatusValue
	progress LaneProgress
}{
	{StatusValue{Name: "planned", Label: "Planned", Tone: "state.planned", RowTone: "row.muted", Icon: "○", IconTone: "state.planned"}, LaneProgressPlanned},
	{StatusValue{Name: "running", Label: "Running", Tone: "state.working", RowTone: "row.active", Icon: "▶", IconTone: "state.working", Starts: true}, LaneProgressActive},
	{StatusValue{Name: "review", Label: "Review", Tone: "state.review", RowTone: "row.attention", Icon: "◉", IconTone: "state.review"}, LaneProgressActive},
	{StatusValue{Name: "merged", Label: "Merged", Tone: "state.done.dim", RowTone: "row.dim", Icon: "✓", IconTone: "state.done.dim"}, LaneProgressDone},
	{StatusValue{Name: "done", Label: "Done", Tone: "state.done.dim", RowTone: "row.dim", Icon: "✓", IconTone: "state.done.dim"}, LaneProgressDone},
	{StatusValue{Name: "parked", Label: "Parked", Tone: "muted", RowTone: "row.muted", Icon: "⏸", IconTone: "muted"}, LaneProgressPlanned},
	{StatusValue{Name: "cancelled", Label: "Cancelled", Tone: "state.cancelled", RowTone: "row.cancelled", Icon: "✕", IconTone: "state.cancelled"}, LaneProgressExcluded},
}

// LaneProgressOf returns the lane's completion group. Unknown and missing
// statuses remain open so that legacy records do not imply completion.
func LaneProgressOf(lane Record) LaneProgress {
	name := strings.ToLower(strings.TrimSpace(lane.Text(laneStatusField)))
	for _, state := range laneStates {
		if state.status.Name == name {
			return state.progress
		}
	}
	return LaneProgressPlanned
}

// LaneProgressSummary describes the work counted in a lane tree.
type LaneProgressSummary struct {
	Done    int
	Active  []Record
	Planned int
}

func (p *LaneProgressSummary) add(lane Record, progress LaneProgress) {
	switch progress {
	case LaneProgressDone:
		p.Done++
	case LaneProgressActive:
		p.Active = append(p.Active, lane)
	case LaneProgressPlanned:
		p.Planned++
	}
}

// LaneTreeProgress counts a lane's direct children. The root contributes
// active work when no child is active, or planned work when no child is open.
func LaneTreeProgress(root Record, children []Record) LaneProgressSummary {
	var summary LaneProgressSummary
	for _, child := range children {
		summary.add(child, LaneProgressOf(child))
	}
	rootProgress := LaneProgressOf(root)
	countActiveRoot := rootProgress == LaneProgressActive && len(summary.Active) == 0
	countPlannedRoot := rootProgress == LaneProgressPlanned && len(summary.Active)+summary.Planned == 0
	if len(children) == 0 || countActiveRoot || countPlannedRoot {
		summary.add(root, rootProgress)
	}
	return summary
}

// LaneProgress reports the progress of a stored lane and its direct children.
func (s *Store) LaneProgress(root Record) (LaneProgressSummary, error) {
	ref := root.Ref()
	res, err := s.Query(Query{Kind: KindLane, Parent: &ref, ParentField: "parentLane"})
	if err != nil {
		return LaneProgressSummary{}, err
	}
	return LaneTreeProgress(root, res.Items), nil
}

func laneStatusColumn() StatusColumn {
	column := StatusColumn{
		Name: laneStatusField, Title: "Status", Default: "planned", PrimaryRowStyle: true,
		Values: make([]StatusValue, 0, len(laneStates)),
	}
	for position, state := range laneStates {
		value := state.status
		value.Position = position
		value.Open = state.progress.IsOpen()
		value.Ends = !value.Open
		column.Values = append(column.Values, value)
	}
	return column
}
