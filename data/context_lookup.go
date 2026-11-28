package data

// contextCanonKeyIndex lets FindContextByCanonKey seek the few context records
// sharing one canonKey instead of decoding every context record in the store.
// The partial WHERE must match the lookup's literal kind term.
const contextCanonKeyIndex = `CREATE INDEX IF NOT EXISTS idx_records_context_canonkey ON records (json_extract(fields, '$.canonKey')) WHERE kind = 'context'`

// contextByCanonKeySQL is the indexed lookup behind FindContextByCanonKey.
const contextByCanonKeySQL = "SELECT " + recordCols + " FROM records WHERE kind = 'context' AND json_extract(fields, '$.canonKey') = ? AND archive_status = 'active' ORDER BY created_at, seq"

// FindContextByCanonKey returns the active context records whose canonKey is
// exactly canonKey, oldest first, with local overrides applied. It reads only
// the matching rows through idx_records_context_canonkey.
func (s *Store) FindContextByCanonKey(canonKey string) ([]Record, error) {
	if canonKey == "" {
		return nil, nil
	}
	rows, err := s.db.Query(contextByCanonKeySQL, canonKey)
	if err != nil {
		return nil, err
	}
	var recs []Record
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		recs = append(recs, rec)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range recs {
		local, err := loadLocal(s.db, recs[i].Kind, recs[i].ID)
		if err != nil {
			return nil, err
		}
		recs[i].Local = local[recs[i].Ref()]
		s.coerce(&recs[i])
	}
	return recs, nil
}

// ContextInScope reports whether a context record belongs to the scope a new
// item would be filed under: the task when taskID is set (or, for a record
// with no task, the same pane), else the pane, else neither.
func ContextInScope(rec Record, taskID, paneID string) bool {
	recTask, hasTask := rec.RefField("task")
	recPane, hasPane := rec.RefField("pane")
	switch {
	case taskID != "":
		return (hasTask && recTask.ID == taskID) || (!hasTask && paneID != "" && hasPane && recPane.ID == paneID)
	case paneID != "":
		return !hasTask && hasPane && recPane.ID == paneID
	default:
		return !hasTask && !hasPane
	}
}

// FindContextInScope returns the oldest active context record with canonKey
// in the scope ContextInScope describes, or nil.
func (s *Store) FindContextInScope(canonKey, taskID, paneID string) (*Record, error) {
	recs, err := s.FindContextByCanonKey(canonKey)
	if err != nil {
		return nil, err
	}
	for i := range recs {
		if ContextInScope(recs[i], taskID, paneID) {
			return &recs[i], nil
		}
	}
	return nil, nil
}
