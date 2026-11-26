package data

import (
	"errors"

	"ofam.dev/ochazuke/core"
)

// taskOwnerRefFields are single-valued reference fields on non-task records
// that attach the record to a task (and therefore to its ancestor task chain).
var taskOwnerRefFields = []string{"task", "project", "parent"}

// hierarchyParentField returns the schema-declared (or inferred) self-parent
// field for kind, or "" when the schema has no hierarchy.
func (t *txn) hierarchyParentField(kind string) string {
	if t.s == nil || t.s.reg == nil {
		return ""
	}
	schema, ok := t.s.reg.Get(kind)
	if !ok {
		return ""
	}
	if h := schema.EffectiveHierarchy(); h != nil {
		return h.ParentField
	}
	return ""
}

// isBubbleRefField reports whether changing field on a record of kind can move
// the record out of a previous parent or task-owner chain.
func (t *txn) isBubbleRefField(kind, field string) bool {
	if field == "" {
		return false
	}
	if field == t.hierarchyParentField(kind) {
		return true
	}
	switch field {
	case "parent", "parentLane", "task", "project":
		return true
	}
	return false
}

// bubbleAncestorUpdatedAt propagates t.now monotonically up schema hierarchies
// (such as task.parent and lane.parentLane) and from task-owned records or
// task relations to the referenced task and its ancestor tasks.
//
// It runs inside the write transaction from flushTouched so every write path
// participates atomically, without bumping change_set on bubbled ancestors or
// appending synthetic FieldChanges. Bookkeeping-only writes (order, readAt),
// local overrides, star/snooze statuses, and workspace pin/mark relations do
// not trigger bubbling.
func (t *txn) bubbleAncestorUpdatedAt(_ core.Actor) error {
	deleted := map[core.Ref]bool{}
	for _, ch := range t.changes {
		if ch.Op == OpDelete {
			deleted[ch.Ref] = true
		}
	}

	var order []core.Ref
	targets := map[core.Ref]bool{}
	walked := map[core.Ref]bool{}

	addTarget := func(ref core.Ref) {
		if ref.Kind == "" || ref.ID == "" || deleted[ref] || t.touched[ref] || targets[ref] {
			return
		}
		targets[ref] = true
		order = append(order, ref)
	}

	var walkAncestors func(rec Record) error
	var visitRef func(ref core.Ref, includeSelf bool) error

	visitRef = func(ref core.Ref, includeSelf bool) error {
		if ref.Kind == "" || ref.ID == "" {
			return nil
		}
		if includeSelf {
			addTarget(ref)
		}
		if walked[ref] || deleted[ref] {
			return nil
		}
		walked[ref] = true
		rec, err := t.s.loadRecord(t.tx, ref)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return walkAncestors(*rec)
	}

	walkAncestors = func(rec Record) error {
		if parentField := t.hierarchyParentField(rec.Kind); parentField != "" {
			if parentRef, ok := rec.RefField(parentField); ok {
				if err := visitRef(parentRef, true); err != nil {
					return err
				}
			}
		}
		if rec.Kind != KindTask {
			for _, field := range taskOwnerRefFields {
				if taskRef, ok := rec.RefField(field); ok && taskRef.Kind == KindTask {
					if err := visitRef(taskRef, true); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}

	// 1. Every record whose content changed (OpCreate or non-bookkeeping OpField).
	for ref, content := range t.touched {
		if content {
			if err := visitRef(ref, false); err != nil {
				return err
			}
		}
	}

	// 2-5. Deletions, reparenting/re-attaching previous refs, task archive/restore,
	// and non-workspace task relations.
	for _, ch := range t.changes {
		switch ch.Op {
		case OpDelete:
			if ch.HadOld {
				if oldRec, err := decodeAs[Record](ch.Old); err == nil {
					walked[oldRec.Ref()] = true
					if err := walkAncestors(oldRec); err != nil {
						return err
					}
				}
			}
		case OpField:
			if ch.HadOld && t.isBubbleRefField(ch.Ref.Kind, ch.Field) {
				if oldRef, ok := AsRef(ch.Old); ok {
					if ch.Field == t.hierarchyParentField(ch.Ref.Kind) || oldRef.Kind == KindTask {
						if err := visitRef(oldRef, true); err != nil {
							return err
						}
					}
				}
			}
		case OpStatus:
			if ch.Field == ColArchiveStatus && ch.Ref.Kind == KindTask {
				if err := visitRef(ch.Ref, false); err != nil {
					return err
				}
			}
		case OpRelate, OpUnrelate:
			raw := ch.New
			if ch.Op == OpUnrelate {
				raw = ch.Old
			}
			if raw != nil {
				if rel, err := decodeAs[Relation](raw); err == nil && rel.Kind != RelPinned && rel.Kind != RelMarked {
					if rel.From.Kind == KindTask {
						if err := visitRef(rel.From, true); err != nil {
							return err
						}
					}
					if rel.To.Kind == KindTask {
						if err := visitRef(rel.To, true); err != nil {
							return err
						}
					}
				}
			}
		}
	}

	for _, ref := range order {
		anc, err := t.s.loadRecord(t.tx, ref)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if !t.now.After(anc.UpdatedAt) {
			continue
		}
		// Preserve read state for already-read ancestor tasks: only a descendant
		// or attached item changed (and carries its own unread state), not the
		// ancestor's own fields. Already-unread ancestors remain unread, and an
		// explicit readAt write in the same transaction is always respected.
		prevReadAt := anc.Time(fieldReadAt)
		wasRead := !prevReadAt.IsZero() && !anc.UpdatedAt.After(prevReadAt)
		if _, err := t.tx.Exec("UPDATE records SET updated_at = ? WHERE kind = ? AND id = ?", t.now.UnixNano(), ref.Kind, ref.ID); err != nil {
			return err
		}
		if anc.Kind == KindTask && wasRead && !t.explicitReadAt[ref] {
			if err := t.stampTaskReadAt(ref); err != nil {
				return err
			}
		}
	}
	return nil
}
