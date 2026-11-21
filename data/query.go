package data

import (
	"regexp"
	"slices"
	"strings"

	"ofam.dev/ochazuke/core"
)

// MatchMode selects how Query.Query matches text.
type MatchMode string

// Match modes.
const (
	MatchSubstring MatchMode = "substring"
	MatchFuzzy     MatchMode = "fuzzy"
	MatchRegex     MatchMode = "regex"
)

// Query selects records. The zero value selects every unarchived record.
type Query struct {
	Kind string // "" = every kind

	// Parent keeps records whose ParentField (default "parent") refers to
	// it: a task's subtasks, or with ParentField "task" a task's panes.
	// Roots keeps records whose ParentField is empty (root tasks).
	Parent      *core.Ref
	ParentField string
	Roots       bool

	// Query is matched against FilterField, or against every field when
	// FilterField is empty. Matching ignores case.
	Query       string
	FilterField string
	MatchMode   MatchMode

	// Common features, filterable on every noun.
	IncludeArchived bool
	StarredOnly     bool
	PinnedOnly      bool
	MarkedOnly      bool
	Tag             string

	// SortField is a field name or one of id, kind, createdAt, updatedAt,
	// archiveStatus, starStatus. A status column sorts by its values'
	// positions. Empty sorts by creation order.
	SortField string
	SortDesc  bool

	Limit  int // 0 = no limit
	Offset int
}

// QueryResult is one page of a query. Total counts every match, before
// Limit and Offset. FilterErr holds the error of an invalid regex; the query
// then matched as a substring instead.
type QueryResult struct {
	Items     []Record `json:"items"`
	Total     int      `json:"total"`
	FilterErr string   `json:"filterErr,omitempty"`
}

func fuzzyMatch(hay, needle string) bool {
	h := []rune(hay)
	i := 0
	for _, n := range needle {
		for i < len(h) && h[i] != n {
			i++
		}
		if i == len(h) {
			return false
		}
		i++
	}
	return true
}

// matcher builds the text predicate for a query and reports a regex error.
func matcher(query string, mode MatchMode) (func(string) bool, string) {
	low := strings.ToLower(query)
	substring := func(s string) bool { return strings.Contains(strings.ToLower(s), low) }
	switch mode {
	case MatchFuzzy:
		return func(s string) bool { return fuzzyMatch(strings.ToLower(s), low) }, ""
	case MatchRegex:
		re, err := regexp.Compile("(?i)" + query)
		if err != nil {
			return substring, err.Error()
		}
		return re.MatchString, ""
	}
	return substring, ""
}

// fieldValue reads a field or one of the record's own columns by name.
func fieldValue(rec Record, field string) any {
	switch field {
	case "id":
		return rec.ID
	case "kind":
		return rec.Kind
	case ColArchiveStatus:
		return rec.ArchiveStatus
	case ColStarStatus:
		return rec.StarStatus
	case "createdAt":
		return rec.CreatedAt.UnixNano()
	case "updatedAt":
		return rec.UpdatedAt.UnixNano()
	}
	v, _ := rec.Value(field)
	return v
}

func matches(rec Record, field string, match func(string) bool) bool {
	if field != "" {
		return match(Text(fieldValue(rec, field)))
	}
	if match(rec.ID) {
		return true
	}
	for _, v := range rec.MergedFields() {
		if match(Text(v)) {
			return true
		}
	}
	return false
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

func compareValues(a, b any) int {
	if x, ok := number(a); ok {
		if y, ok := number(b); ok {
			switch {
			case x < y:
				return -1
			case x > y:
				return 1
			}
			return 0
		}
	}
	return strings.Compare(strings.ToLower(Text(a)), strings.ToLower(Text(b)))
}

// relatedSet returns the refs at the far end of every relation of one kind
// that has fixed at its near end.
func (s *Store) relatedSet(fixed core.Ref, relKind string, dir RelationDir) (map[core.Ref]bool, error) {
	rels, err := s.Related(fixed, relKind, dir)
	if err != nil {
		return nil, err
	}
	out := make(map[core.Ref]bool, len(rels))
	for _, rel := range rels {
		out[rel.Other(fixed)] = true
	}
	return out, nil
}

// Query returns the records matching q.
func (s *Store) Query(q Query) (QueryResult, error) {
	var res QueryResult
	var conds []string
	var args []any
	if q.Kind != "" {
		conds, args = append(conds, "kind = ?"), append(args, q.Kind)
	}
	if !q.IncludeArchived {
		conds, args = append(conds, "archive_status = ?"), append(args, ArchiveActive)
	}
	if q.StarredOnly {
		conds, args = append(conds, "star_status = ?"), append(args, StarStarred)
	}
	sqlText := "SELECT " + recordCols + " FROM records"
	if len(conds) > 0 {
		sqlText += " WHERE " + strings.Join(conds, " AND ")
	}
	rows, err := s.db.Query(sqlText+" ORDER BY created_at, seq", args...)
	if err != nil {
		return res, err
	}
	var recs []Record
	for rows.Next() {
		rec, err := scanRecord(rows)
		if err != nil {
			rows.Close()
			return res, err
		}
		recs = append(recs, rec)
	}
	if err := rows.Close(); err != nil {
		return res, err
	}
	local, err := loadLocal(s.db, q.Kind, "")
	if err != nil {
		return res, err
	}

	keep := []func(Record) bool{}
	parentField := q.ParentField
	if parentField == "" {
		parentField = "parent"
	}
	if q.Parent != nil {
		keep = append(keep, func(r Record) bool { ref, ok := r.RefField(parentField); return ok && ref == *q.Parent })
	}
	if q.Roots {
		keep = append(keep, func(r Record) bool { _, ok := r.RefField(parentField); return !ok })
	}
	for _, f := range []struct {
		on    bool
		fixed core.Ref
		kind  string
		dir   RelationDir
	}{
		{q.PinnedOnly, WorkspaceRef, RelPinned, Outgoing},
		{q.MarkedOnly, WorkspaceRef, RelMarked, Outgoing},
		{q.Tag != "", core.Ref{Kind: KindTag, ID: q.Tag}, RelTag, Incoming},
	} {
		if !f.on {
			continue
		}
		set, err := s.relatedSet(f.fixed, f.kind, f.dir)
		if err != nil {
			return res, err
		}
		keep = append(keep, func(r Record) bool { return set[r.Ref()] })
	}
	if q.Query != "" {
		match, filterErr := matcher(q.Query, q.MatchMode)
		res.FilterErr = filterErr
		keep = append(keep, func(r Record) bool { return matches(r, q.FilterField, match) })
	}

	items := recs[:0]
	for _, rec := range recs {
		rec.Local = local[rec.Ref()]
		s.coerce(&rec)
		if !slices.ContainsFunc(keep, func(ok func(Record) bool) bool { return !ok(rec) }) {
			items = append(items, rec)
		}
	}

	switch {
	case q.SortField != "":
		positions := map[string]map[string]int{} // kind -> status value -> position
		key := func(r Record) any {
			v := fieldValue(r, q.SortField)
			pos, ok := positions[r.Kind]
			if !ok {
				pos = map[string]int{}
				if schema, found := s.reg.Get(r.Kind); found {
					if col, isStatus := schema.StatusColumn(q.SortField); isStatus {
						for _, sv := range col.Values {
							pos[sv.Name] = sv.Position
						}
					}
				}
				positions[r.Kind] = pos
			}
			if p, isStatus := pos[Text(v)]; isStatus {
				return p
			}
			return v
		}
		slices.SortStableFunc(items, func(a, b Record) int {
			c := compareValues(key(a), key(b))
			if q.SortDesc {
				return -c
			}
			return c
		})
	case q.SortDesc:
		slices.Reverse(items)
	}

	res.Total = len(items)
	if q.Offset > 0 {
		items = items[min(q.Offset, len(items)):]
	}
	if q.Limit > 0 && len(items) > q.Limit {
		items = items[:q.Limit]
	}
	res.Items = slices.Clone(items)
	return res, nil
}

// TaskTree returns the recursive task tree for the outline: root tasks
// (projects) at depth 0, each with its subtasks, in creation order. Without
// includeArchived an archived task is left out together with its subtree;
// its subtasks are still found by Get. A task whose parent does not exist is
// shown as a root, so nothing is lost.
func (s *Store) TaskTree(includeArchived bool) ([]TaskNode, error) {
	res, err := s.Query(Query{Kind: KindTask, IncludeArchived: true})
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(res.Items))
	for _, rec := range res.Items {
		known[rec.ID] = true
	}
	children := map[string][]Record{}
	var roots []Record
	for _, rec := range res.Items {
		if parent, ok := rec.RefField("parent"); ok && parent.Kind == KindTask && known[parent.ID] && parent.ID != rec.ID {
			children[parent.ID] = append(children[parent.ID], rec)
		} else {
			roots = append(roots, rec)
		}
	}
	placed := map[string]bool{}
	var build func(recs []Record, depth int) []TaskNode
	build = func(recs []Record, depth int) []TaskNode {
		var out []TaskNode
		for _, rec := range recs {
			if placed[rec.ID] {
				continue
			}
			placed[rec.ID] = true
			if rec.IsArchived() && !includeArchived {
				markSubtree(rec.ID, children, placed)
				continue
			}
			out = append(out, TaskNode{Record: rec, Depth: depth, Children: build(children[rec.ID], depth+1)})
		}
		return out
	}
	tree := build(roots, 0)
	// Tasks whose parents form a loop are reachable from no root: show them as roots.
	for _, rec := range res.Items {
		if !placed[rec.ID] {
			tree = append(tree, build([]Record{rec}, 0)...)
		}
	}
	return tree, nil
}

func markSubtree(id string, children map[string][]Record, placed map[string]bool) {
	for _, child := range children[id] {
		if !placed[child.ID] {
			placed[child.ID] = true
			markSubtree(child.ID, children, placed)
		}
	}
}
