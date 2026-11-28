package data

import (
	"strings"

	"ofam.dev/ochazuke/core"
)

const queryOrderAsc = " ORDER BY created_at, seq"

// canPushLimit reports whether q's Limit and Offset can run in SQL: nothing
// that filters or sorts in Go (parent, roots, pins, marks, tags, search,
// field sort) is set, so the SQL row order and count are the final ones.
func canPushLimit(q Query) bool {
	return q.Limit > 0 && q.Parent == nil && !q.Roots && !q.PinnedOnly && !q.MarkedOnly &&
		q.Tag == "" && q.Query == "" && q.SortField == ""
}

// queryPushedLimit runs a canPushLimit query with LIMIT/OFFSET in SQL and a
// COUNT(*) for Total, decoding only the rows it returns.
func (s *Store) queryPushedLimit(q Query, local map[core.Ref]map[string]any) (QueryResult, error) {
	var res QueryResult
	sqlText, args := buildQuerySQL(q, "", false)
	where := strings.TrimSuffix(strings.TrimPrefix(sqlText, "SELECT "+recordCols+" FROM records"), queryOrderAsc)
	if err := s.db.QueryRow("SELECT COUNT(*) FROM records"+where, args...).Scan(&res.Total); err != nil {
		return res, err
	}
	order := queryOrderAsc
	if q.SortDesc {
		order = " ORDER BY created_at DESC, seq DESC"
	}
	rows, err := s.db.Query("SELECT "+recordCols+" FROM records"+where+order+" LIMIT ? OFFSET ?", append(args, q.Limit, max(q.Offset, 0))...)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			return res, err
		}
		rec.Local = local[rec.Ref()]
		s.coerce(&rec)
		res.Items = append(res.Items, rec)
	}
	return res, rows.Err()
}
