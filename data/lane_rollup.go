package data

import (
	"math"
	"slices"

	"ofam.dev/ochazuke/core"
)

const (
	laneBarPhaseActive    = "active"
	laneBarPhaseCompleted = "completed"
)

// LaneRollup is the progress of one lane tree and the state of its direct work.
type LaneRollup struct {
	Root    Record
	Done    int
	Active  int
	Planned int
	// Running holds the lanes that are running or in review, in order.
	Running []Record
	// OtherTrees counts the other active lane trees the same pane or task has.
	OtherTrees int
}

// Total is the number of lanes counted.
func (r LaneRollup) Total() int { return r.Done + r.Active + r.Planned }

// Percent is the rounded completion percentage (0-100).
func (r LaneRollup) Percent() int {
	total := r.Total()
	if total <= 0 {
		return 0
	}
	return int(math.Round(float64(r.Done) * 100 / float64(total)))
}

// IsActive reports whether any counted lane is still open.
func (r LaneRollup) IsActive() bool { return r.Active+r.Planned > 0 }

// LaneBarEntry is one bar of a pane's stack: a root lane tree and whether all
// of its work is finished.
type LaneBarEntry struct {
	Roll      LaneRollup
	Completed bool
}

// Key identifies the bar in its present phase. A dismissal is recorded under
// it, so a finished tree that is reopened shows again by itself.
func (e LaneBarEntry) Key() string { return LaneBarKey(e.Roll.Root.ID, e.Completed) }

// LaneBarKey is LaneBarEntry.Key for a lane ID and phase.
func LaneBarKey(laneID string, completed bool) string {
	if completed {
		return laneID + "|" + laneBarPhaseCompleted
	}
	return laneID + "|" + laneBarPhaseActive
}

// RollupLane reports the current progress of the stored lane tree.
func (s *Store) RollupLane(root Record) (LaneRollup, error) {
	progress, err := s.LaneProgress(root)
	if err != nil {
		return LaneRollup{Root: root}, err
	}
	return LaneRollup{Root: root, Done: progress.Done, Active: len(progress.Active), Planned: progress.Planned,
		Running: progress.Active}, nil
}

// laneRoot walks parentLane up to the root lane. A parent that is not stored
// (or a loop) ends the walk at the last lane found.
func (s *Store) laneRoot(lane Record) Record {
	seen := map[string]bool{lane.ID: true}
	for {
		parent, ok := lane.RefField("parentLane")
		if !ok || seen[parent.ID] {
			return lane
		}
		rec, err := s.Get(parent)
		if err != nil {
			return lane
		}
		seen[parent.ID] = true
		lane = *rec
	}
}

// laneTrees returns the rollups of the unarchived lane trees among lanes, one
// per distinct root, in the order first met.
func (s *Store) laneTrees(lanes []Record) ([]LaneRollup, error) {
	var out []LaneRollup
	seen := map[string]bool{}
	for _, lane := range lanes {
		root := s.laneRoot(lane)
		if seen[root.ID] || root.IsArchived() {
			continue
		}
		seen[root.ID] = true
		roll, err := s.RollupLane(root)
		if err != nil {
			return nil, err
		}
		out = append(out, roll)
	}
	return out, nil
}

// leadingActiveTree picks the active tree among lanes: one with a lane running
// leads, and the rest are counted in OtherTrees.
func (s *Store) leadingActiveTree(lanes []Record) (LaneRollup, bool, error) {
	trees, err := s.laneTrees(lanes)
	if err != nil {
		return LaneRollup{}, false, err
	}
	trees = slices.DeleteFunc(trees, func(r LaneRollup) bool { return !r.IsActive() })
	if len(trees) == 0 {
		return LaneRollup{}, false, nil
	}
	slices.SortStableFunc(trees, func(a, b LaneRollup) int { return min(b.Active, 1) - min(a.Active, 1) })
	trees[0].OtherTrees = len(trees) - 1
	return trees[0], true, nil
}

// claimedLanes are the lanes that claim a pane (a "claim" relation from lane
// to pane).
func (s *Store) claimedLanes(pane Record) ([]Record, error) {
	claims, err := s.RelatedRecords(pane.Ref(), RelClaim, Incoming)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(claims, func(rec Record) bool { return rec.Kind != KindLane }), nil
}

// lanesOfTask are the lanes filed directly under a task.
func (s *Store) lanesOfTask(task core.Ref) ([]Record, error) {
	res, err := s.Query(Query{Kind: KindLane, Parent: &task, ParentField: "task"})
	return res.Items, err
}

// lanesOfPane are the lanes tied to a pane: those that claim it, then those
// of its own task.
func (s *Store) lanesOfPane(pane Record) ([]Record, error) {
	out, err := s.claimedLanes(pane)
	if err != nil {
		return nil, err
	}
	if task, ok := pane.RefField("task"); ok {
		lanes, err := s.lanesOfTask(task)
		if err != nil {
			return nil, err
		}
		out = append(out, lanes...)
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
func (s *Store) ActiveLane(pane Record) (LaneRollup, bool, error) {
	claimed, err := s.claimedLanes(pane)
	if err != nil {
		return LaneRollup{}, false, err
	}
	if roll, ok, err := s.leadingActiveTree(claimed); ok || err != nil {
		return roll, ok, err
	}

	task, ok := pane.RefField("task")
	for seen := map[string]bool{}; ok && !seen[task.ID]; {
		seen[task.ID] = true
		lanes, err := s.lanesOfTask(task)
		if err != nil {
			return LaneRollup{}, false, err
		}
		if roll, found, err := s.leadingActiveTree(lanes); found || err != nil {
			return roll, found, err
		}
		rec, err := s.Get(task)
		if err != nil {
			break
		}
		task, ok = rec.RefField("parent")
	}
	return LaneRollup{}, false, nil
}

// CompletedLanes are the finished root lane trees of a pane (claimed by it or
// of its task): every counted lane done or merged, none open. Archived roots
// are left out. Oldest first, so the newest sits next to the active bar.
func (s *Store) CompletedLanes(pane Record) ([]LaneBarEntry, error) {
	lanes, err := s.lanesOfPane(pane)
	if err != nil {
		return nil, err
	}
	trees, err := s.laneTrees(lanes)
	if err != nil {
		return nil, err
	}
	var out []LaneBarEntry
	for _, roll := range trees {
		if !roll.IsActive() && roll.Done > 0 {
			out = append(out, LaneBarEntry{Roll: roll, Completed: true})
		}
	}
	slices.SortStableFunc(out, func(a, b LaneBarEntry) int {
		return a.Roll.Root.UpdatedAt.Compare(b.Roll.Root.UpdatedAt)
	})
	return out, nil
}

// PaneLaneBars are the bars a pane's stack can show, before dismissals: its
// finished trees (when keepCompleted), then the active tree, if any. A nil
// store has none.
func (s *Store) PaneLaneBars(pane Record, keepCompleted bool) ([]LaneBarEntry, error) {
	if s == nil {
		return nil, nil
	}
	var out []LaneBarEntry
	if keepCompleted {
		done, err := s.CompletedLanes(pane)
		if err != nil {
			return nil, err
		}
		out = done
	}
	roll, ok, err := s.ActiveLane(pane)
	if err != nil {
		return nil, err
	}
	if ok {
		out = append(out, LaneBarEntry{Roll: roll})
	}
	return out, nil
}
