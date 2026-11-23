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
	case ColSnoozedUntil:
		if rec.SnoozedUntil == nil {
			return ""
		}
		return rec.SnoozedUntil.Format(TimeFormat)
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

// sortTreeSiblings orders sibling records by their numeric orderField ascending
// when orderField is non-empty and set, falling back to creation order
// (preserved by stable sort). When orderField is empty it is a no-op.
func sortTreeSiblings(recs []Record, orderField string) {
	if orderField == "" {
		return
	}
	slices.SortStableFunc(recs, func(a, b Record) int {
		av, aok := a.Value(orderField)
		bv, bok := b.Value(orderField)
		an, aNum := number(av)
		bn, bNum := number(bv)
		aHas := aok && aNum
		bHas := bok && bNum
		switch {
		case aHas && bHas:
			if an < bn {
				return -1
			}
			if an > bn {
				return 1
			}
			return 0
		case aHas:
			return -1
		case bHas:
			return 1
		default:
			return 0
		}
	})
}

// BuildTree arranges recs into a recursive tree using parentField (a
// single-valued Ref field) and optional numeric orderField for sibling
// ordering. Records are keyed by core.Ref; self-loops, missing parents, and
// cycles are surfaced as roots so no record is lost. When includeArchived is
// false, an archived record is omitted together with its entire subtree.
func BuildTree(recs []Record, parentField, orderField string, includeArchived bool) []TreeNode {
	known := make(map[core.Ref]bool, len(recs))
	for _, rec := range recs {
		known[rec.Ref()] = true
	}
	children := map[core.Ref][]Record{}
	var roots []Record
	for _, rec := range recs {
		if parentField != "" {
			if parent, ok := rec.RefField(parentField); ok && known[parent] && parent != rec.Ref() {
				children[parent] = append(children[parent], rec)
				continue
			}
		}
		roots = append(roots, rec)
	}
	sortTreeSiblings(roots, orderField)
	for ref := range children {
		sortTreeSiblings(children[ref], orderField)
	}
	placed := map[core.Ref]bool{}
	var build func(list []Record, depth int) []TreeNode
	build = func(list []Record, depth int) []TreeNode {
		var out []TreeNode
		for _, rec := range list {
			ref := rec.Ref()
			if placed[ref] {
				continue
			}
			placed[ref] = true
			if rec.IsArchived() && !includeArchived {
				markTreeSubtree(ref, children, placed)
				continue
			}
			out = append(out, TreeNode{Record: rec, Depth: depth, Children: build(children[ref], depth+1)})
		}
		return out
	}
	tree := build(roots, 0)
	// Records whose parents form a loop are reachable from no root: surface them as roots.
	for _, rec := range recs {
		if !placed[rec.Ref()] {
			tree = append(tree, build([]Record{rec}, 0)...)
		}
	}
	return tree
}

func markTreeSubtree(ref core.Ref, children map[core.Ref][]Record, placed map[core.Ref]bool) {
	for _, child := range children[ref] {
		cref := child.Ref()
		if !placed[cref] {
			placed[cref] = true
			markTreeSubtree(cref, children, placed)
		}
	}
}

// RecordTree returns the recursive hierarchy tree for any kind using its
// schema's EffectiveHierarchy (parentField and optional orderField).
func (s *Store) RecordTree(kind string, includeArchived bool) ([]TreeNode, error) {
	res, err := s.Query(Query{Kind: kind, IncludeArchived: true})
	if err != nil {
		return nil, err
	}
	parentField, orderField := "", ""
	if schema, ok := s.reg.Get(kind); ok {
		if h := schema.EffectiveHierarchy(); h != nil {
			parentField, orderField = h.ParentField, h.OrderField
		}
	}
	return BuildTree(res.Items, parentField, orderField, includeArchived), nil
}

// TaskTree returns the recursive task tree for the outline: root tasks
// (projects) at depth 0, each with its subtasks, ordered by sibling "order"
// ascending when set and falling back to creation order.
func (s *Store) TaskTree(includeArchived bool) ([]TaskNode, error) {
	return s.RecordTree(KindTask, includeArchived)
}
