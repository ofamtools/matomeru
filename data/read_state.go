package data

import (
	"encoding/json"
	"fmt"

	"ofam.dev/ochazuke/core"
)

const (
	fieldOrder  = "order"
	fieldReadAt = "readAt"
)

// isBookkeepingField reports whether a record field holds pure layout or
// read-state bookkeeping rather than content. Writing only bookkeeping fields
// moves the record's revision without bumping UpdatedAt.
func isBookkeepingField(field string) bool {
	return field == fieldOrder || field == fieldReadAt
}

// markExplicitReadAt notes that the current transaction explicitly set or
// cleared readAt on ref, so automatic owner read-state stamping leaves it alone.
func (t *txn) markExplicitReadAt(ref core.Ref) {
	if t.explicitReadAt == nil {
		t.explicitReadAt = map[core.Ref]bool{}
	}
	t.explicitReadAt[ref] = true
}

// noteFieldWrite records whether ch explicitly writes readAt before any no-op
// short-circuit in applyValue.
func (t *txn) noteFieldWrite(ch FieldChange) {
	if ch.Op == OpField && ch.Field == fieldReadAt {
		t.markExplicitReadAt(ch.Ref)
	}
}

// prepareCreateReadState stamps readAt on owner-created tasks so they start
// read at creation time. Agent- and system-created tasks stay unread, and
// callers that explicitly pass readAt (including nil) keep their value.
func (t *txn) prepareCreateReadState(ref core.Ref, actor core.Actor, explicit, fields map[string]any) {
	if _, set := explicit[fieldReadAt]; set {
		t.markExplicitReadAt(ref)
		return
	}
	if actor.Kind != "user" || ref.Kind != KindTask {
		return
	}
	if _, set := fields[fieldReadAt]; !set {
		fields[fieldReadAt] = t.now.Format(TimeFormat)
	}
}

// flushTouched updates revision and UpdatedAt for every record touched in t,
// and keeps owner-edited tasks marked read when readAt was not explicitly
// written in the same transaction.
func (t *txn) flushTouched(actor core.Actor) error {
	for ref, content := range t.touched {
		query, args := "UPDATE records SET change_set = ? WHERE kind = ? AND id = ?", []any{t.num, ref.Kind, ref.ID}
		if content {
			query, args = "UPDATE records SET change_set = ?, updated_at = ? WHERE kind = ? AND id = ?", []any{t.num, t.now.UnixNano(), ref.Kind, ref.ID}
		}
		if _, err := t.tx.Exec(query, args...); err != nil {
			return err
		}
		if content && actor.Kind == "user" && ref.Kind == KindTask && !t.explicitReadAt[ref] {
			if err := t.stampTaskReadAt(ref); err != nil {
				return err
			}
		}
	}
	if err := t.bubbleAncestorUpdatedAt(actor); err != nil {
		return err
	}
	return nil
}

// stampTaskReadAt advances readAt to t.now on ref's stored fields JSON without
// appending a synthetic FieldChange to the user's change set.
func (t *txn) stampTaskReadAt(ref core.Ref) error {
	var raw string
	if err := t.tx.QueryRow("SELECT fields FROM records WHERE kind = ? AND id = ?", ref.Kind, ref.ID).Scan(&raw); err != nil {
		return err
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return fmt.Errorf("data: fields of %s: %w", RefText(ref), err)
	}
	if fields == nil {
		fields = map[string]any{}
	}
	stamp := t.now.Format(TimeFormat)
	if fields[fieldReadAt] == stamp {
		return nil
	}
	fields[fieldReadAt] = stamp
	_, err := t.tx.Exec("UPDATE records SET fields = ? WHERE kind = ? AND id = ?", toJSON(fields), ref.Kind, ref.ID)
	return err
}
