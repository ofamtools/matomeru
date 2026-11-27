package data

import "ofam.dev/ochazuke/core"

// ProjectUnreadCandidates returns active records of kind attached to scope that
// could possibly be unread under generic readAt/cutoff rules: records created
// strictly after cutoffNano, records with a non-null readAt field, or records
// carrying local overrides. For large kinds (such as thousands of historical
// context items), this avoids scanning and decoding records that cannot be unread.
func (s *Store) ProjectUnreadCandidates(scope ProjectScope, kind string, cutoffNano int64) ([]Record, error) {
	if s == nil || s.db == nil || (len(scope.TaskIDs) == 0 && len(scope.PaneIDs) == 0) {
		return nil, nil
	}
	local, err := loadLocal(s.db, kind, "")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Record
	if cutoffNano > 0 {
		q := "SELECT " + recordCols + " FROM records WHERE kind = ? AND archive_status = ? AND created_at > ?"
		if err := s.scanScopedCandidates(scope, local, seen, &out, q, kind, ArchiveActive, cutoffNano); err != nil {
			return nil, err
		}
	}
	qRead := "SELECT " + recordCols + " FROM records WHERE kind = ? AND archive_status = ? AND json_extract(fields, '$.readAt') IS NOT NULL"
	if err := s.scanScopedCandidates(scope, local, seen, &out, qRead, kind, ArchiveActive); err != nil {
		return nil, err
	}
	if err := s.scanLocalCandidates(scope, kind, local, seen, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) scanLocalCandidates(scope ProjectScope, kind string, local map[core.Ref]map[string]any, seen map[string]bool, out *[]Record) error {
	if len(local) == 0 {
		return nil
	}
	ids := make(map[string]bool, len(local))
	for ref := range local {
		ids[ref.ID] = true
	}
	ordered := sortedProjectIDs(ids)
	for start := 0; start < len(ordered); start += sqlInBatchSize {
		batch := ordered[start:min(start+sqlInBatchSize, len(ordered))]
		args := []any{kind, ArchiveActive}
		for _, id := range batch {
			args = append(args, id)
		}
		q := "SELECT " + recordCols + " FROM records WHERE kind = ? AND archive_status = ? AND id IN (" + placeholders(len(batch)) + ")"
		if err := s.scanScopedCandidates(scope, local, seen, out, q, args...); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) scanScopedCandidates(scope ProjectScope, local map[core.Ref]map[string]any, seen map[string]bool, out *[]Record, q string, args ...any) error {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return err
		}
		if seen[rec.ID] {
			continue
		}
		seen[rec.ID] = true
		rec.Local = local[rec.Ref()]
		s.coerce(&rec)
		if scope.containsRecord(rec) {
			*out = append(*out, rec)
		}
	}
	return rows.Err()
}
