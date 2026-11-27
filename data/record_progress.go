package data

import "ofam.dev/ochazuke/core"

// RecordProgressSummary holds live completion counts for a record with
// hierarchical children (such as a lane tree or a task with subtasks).
type RecordProgressSummary struct {
	Done        int
	Active      int
	Planned     int
	ActiveLabel string
}

// Total returns the total counted items.
func (p RecordProgressSummary) Total() int {
	return p.Done + p.Active + p.Planned
}

// RecordProgress computes live completion counts for rec when its noun tracks
// child work (lane trees and tasks with direct subtasks).
func (s *Store) RecordProgress(rec Record) (RecordProgressSummary, bool) {
	if s == nil || rec.ID == "" {
		return RecordProgressSummary{}, false
	}
	switch rec.Kind {
	case KindLane:
		return s.laneRecordProgress(rec)
	case KindTask:
		return s.taskRecordProgress(rec)
	default:
		return RecordProgressSummary{}, false
	}
}

func (s *Store) laneRecordProgress(rec Record) (RecordProgressSummary, bool) {
	roll, err := s.RollupLane(rec)
	if err != nil || roll.Total() <= 0 {
		return RecordProgressSummary{}, false
	}
	activeLabel := ""
	if len(roll.Running) > 0 && roll.Running[0].ID != rec.ID {
		activeLabel = roll.Running[0].Text("title")
	}
	return RecordProgressSummary{
		Done:        roll.Done,
		Active:      roll.Active,
		Planned:     roll.Planned,
		ActiveLabel: activeLabel,
	}, true
}

func (s *Store) taskRecordProgress(rec Record) (RecordProgressSummary, bool) {
	ref := rec.Ref()
	res, err := s.Query(Query{Kind: KindTask, Parent: &ref, ParentField: "parent"})
	if err != nil || len(res.Items) == 0 {
		return RecordProgressSummary{}, false
	}
	var schema Schema
	if s.reg != nil {
		schema, _ = s.reg.Get(KindTask)
	}
	col, hasCol := schema.primaryStatus()
	var summary RecordProgressSummary
	for _, child := range res.Items {
		classifyChildProgress(&summary, child, col, hasCol)
	}
	return summary, summary.Total() > 0
}

func classifyChildProgress(summary *RecordProgressSummary, child Record, col StatusColumn, hasCol bool) {
	if !hasCol {
		summary.Planned++
		return
	}
	sv, ok := col.Value(child.Text(col.Name))
	switch {
	case ok && sv.Ends:
		summary.Done++
	case ok && sv.Starts:
		summary.Active++
		if summary.ActiveLabel == "" {
			summary.ActiveLabel = child.Text("title")
		}
	default:
		summary.Planned++
	}
}

// RecordPrimaryStatus extracts the primary status value and schema metadata for rec.
func RecordPrimaryStatus(schema Schema, rec Record) (label, icon, tone string, ok bool) {
	col, hasCol := schema.primaryStatus()
	if !hasCol {
		return "", "", "", false
	}
	raw := rec.Text(col.Name)
	if raw == "" {
		raw = col.Default
	}
	if raw == "" {
		return "", "", "", false
	}
	sv, found := col.Value(raw)
	if !found {
		return raw, "", "", true
	}
	label = sv.Label
	if label == "" {
		label = sv.Name
	}
	icon = sv.Icon
	tone = sv.Tone
	if tone == "" {
		tone = sv.IconTone
	}
	return label, icon, tone, true
}

// RecordParentRef returns the first populated parent/owner reference on rec
// from common hierarchy fields, without formatting raw kind:id text.
func RecordParentRef(rec Record) (core.Ref, bool) {
	for _, field := range []string{"parentLane", "task", "parent", "pane"} {
		if ref, ok := rec.RefField(field); ok && ref.Kind != "" && ref.ID != "" {
			return ref, true
		}
	}
	return core.Ref{}, false
}
