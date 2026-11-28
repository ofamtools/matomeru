package data

import (
	"errors"
	"fmt"

	"ofam.dev/ochazuke/core"
)

// ErrUndoConflict reports that an out-of-order undo or redo was refused
// because a newer active change set modified the same record or relation.
var ErrUndoConflict = errors.New("data: undo conflict")

// UndoState is a newest-first snapshot of one actor's undo/redo history along
// with the derived undone/redone state across the store's recent history.
type UndoState struct {
	History    []ChangeSet     // actor's change sets, newest first
	Undone     map[int64]int   // change set -> un-redone undo count
	Redone     map[int64]bool  // undo change set -> already redone
	UndoTarget map[int64]int64 // undo change set -> original change set it undid
}

// ActorUndoState returns the actor's recent change sets (newest first) together
// with the current undone/redone state.
func (s *Store) ActorUndoState(actor core.Actor) (UndoState, error) {
	history, undone, redone, err := s.undoState()
	if err != nil {
		return UndoState{}, err
	}
	undoTarget := make(map[int64]int64)
	actorHistory := make([]ChangeSet, 0, len(history))
	for _, cs := range history {
		if cs.Action == "undo" {
			undoTarget[cs.Number] = cs.UndoOf
		}
		if sameActor(cs.Actor, actor) {
			actorHistory = append(actorHistory, cs)
		}
	}
	return UndoState{
		History:    actorHistory,
		Undone:     undone,
		Redone:     redone,
		UndoTarget: undoTarget,
	}, nil
}

// ChangeSetsSince returns change sets with number > afterNum in ascending
// order (oldest first).
func (s *Store) ChangeSetsSince(afterNum int64) ([]ChangeSet, error) {
	rows, err := s.db.Query("SELECT "+changeSetCols+" FROM change_sets c WHERE c.number > ? ORDER BY c.number ASC", afterNum)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChangeSet
	for rows.Next() {
		cs, err := scanChangeSet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cs)
	}
	return out, rows.Err()
}

// UndoChangeSet safely undoes a specific non-undo/non-redo change set when no
// newer active change set modified the same record or relation. If a newer
// active edit touched an overlapping record, it refuses with ErrUndoConflict.
func (s *Store) UndoChangeSet(changeSetNum int64, actor core.Actor) (*ChangeSet, error) {
	target, err := s.ChangeSet(changeSetNum)
	if err != nil {
		return nil, err
	}
	if target.Action == "undo" || target.Action == "redo" {
		return nil, fmt.Errorf("%w: change set #%d is not undoable", ErrNotFound, changeSetNum)
	}
	history, undone, _, err := s.undoState()
	if err != nil {
		return nil, err
	}
	if undone[target.Number] > 0 {
		return nil, fmt.Errorf("%w: change set #%d is already undone", ErrNotFound, changeSetNum)
	}
	if blocker, ref := findLaterConflict(history, undone, target, target.Number); blocker != nil {
		return nil, fmt.Errorf("%w: %s was modified later by #%d (%s)", ErrUndoConflict, RefText(ref), blocker.Number, blocker.Summary)
	}
	return s.Undo(target.Number, actor)
}

// RedoChangeSet safely re-applies a specific undo change set when it has not
// already been redone and no newer active edit modified the same record or
// relation since the undo.
func (s *Store) RedoChangeSet(undoChangeSetNum int64, actor core.Actor) (*ChangeSet, error) {
	target, err := s.ChangeSet(undoChangeSetNum)
	if err != nil {
		return nil, err
	}
	if target.Action != "undo" {
		return nil, fmt.Errorf("%w: change set #%d is not an undo", ErrNotFound, undoChangeSetNum)
	}
	history, undone, redone, err := s.undoState()
	if err != nil {
		return nil, err
	}
	if redone[target.Number] {
		return nil, fmt.Errorf("%w: undo #%d was already redone", ErrNotFound, undoChangeSetNum)
	}
	if blocker, ref := findLaterConflict(history, undone, target, target.Number); blocker != nil {
		return nil, fmt.Errorf("%w: %s was modified later by #%d (%s)", ErrUndoConflict, RefText(ref), blocker.Number, blocker.Summary)
	}
	return s.Redo(target.Number, actor)
}

// findLaterConflict inspects active non-bookkeeping writes strictly newer than
// afterNum and returns the newest one that overlaps target's records/relations.
func findLaterConflict(history []ChangeSet, undone map[int64]int, target *ChangeSet, afterNum int64) (*ChangeSet, core.Ref) {
	records, rels := changeSetFootprint(target.Changes)
	for i := range history {
		cs := &history[i]
		if cs.Number <= afterNum {
			break // history is newest-first
		}
		if cs.Action == "undo" || cs.Action == "redo" || undone[cs.Number] > 0 {
			continue
		}
		if ref, hit := overlapsFootprint(cs.Changes, records, rels); hit {
			return cs, ref
		}
	}
	return nil, core.Ref{}
}

type relationFootprint struct {
	from core.Ref
	kind string
	to   core.Ref
}

func isUndoBookkeepingChange(ch FieldChange) bool {
	if ch.Op != OpField {
		return false
	}
	switch ch.Field {
	case fieldReadAt, "viewedAt", "promptedAt":
		return true
	}
	return false
}

func changeRelationFootprint(ch FieldChange) (relationFootprint, bool) {
	if ch.Op != OpRelate && ch.Op != OpUnrelate {
		return relationFootprint{}, false
	}
	raw := ch.New
	if raw == nil {
		raw = ch.Old
	}
	rel, err := decodeAs[Relation](raw)
	if err != nil || rel.Kind == "" {
		return relationFootprint{}, false
	}
	return relationFootprint{from: rel.From, kind: rel.Kind, to: rel.To}, true
}

func changeSetFootprint(changes []FieldChange) (map[core.Ref]bool, map[relationFootprint]bool) {
	records := map[core.Ref]bool{}
	rels := map[relationFootprint]bool{}
	for _, ch := range changes {
		if isUndoBookkeepingChange(ch) {
			continue
		}
		if rf, ok := changeRelationFootprint(ch); ok {
			rels[rf] = true
			continue
		}
		if ch.Ref.Kind != "" && ch.Ref.ID != "" {
			records[ch.Ref] = true
		}
	}
	return records, rels
}

func overlapsFootprint(changes []FieldChange, records map[core.Ref]bool, rels map[relationFootprint]bool) (core.Ref, bool) {
	for _, ch := range changes {
		if isUndoBookkeepingChange(ch) {
			continue
		}
		if rf, ok := changeRelationFootprint(ch); ok {
			if rels[rf] {
				return rf.from, true
			}
			continue
		}
		if records[ch.Ref] {
			return ch.Ref, true
		}
		if ch.Op == OpDelete && relsTouchRef(rels, ch.Ref) {
			return ch.Ref, true
		}
	}
	return core.Ref{}, false
}

func relsTouchRef(rels map[relationFootprint]bool, ref core.Ref) bool {
	for rf := range rels {
		if rf.from == ref || rf.to == ref {
			return true
		}
	}
	return false
}
