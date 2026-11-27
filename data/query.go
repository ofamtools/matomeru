package data

import (
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"ofam.dev/ochazuke/core"
)

var queryIndexes = []string{
	`CREATE INDEX IF NOT EXISTS idx_records_kind_task_id ON records (kind, json_extract(fields, '$.task.id'))`,
	`CREATE INDEX IF NOT EXISTS idx_records_kind_pane_id ON records (kind, json_extract(fields, '$.pane.id'))`,
	`CREATE INDEX IF NOT EXISTS idx_records_kind_parent_id ON records (kind, json_extract(fields, '$.parent.id'))`,
	`CREATE INDEX IF NOT EXISTS idx_records_kind_parent_lane_id ON records (kind, json_extract(fields, '$.parentLane.id'))`,
	`CREATE INDEX IF NOT EXISTS idx_records_kind_changeset ON records (kind, change_set)`,
	`CREATE INDEX IF NOT EXISTS records_kind_archive_updated ON records (kind, archive_status, updated_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_records_kind_read_at ON records (kind, archive_status) WHERE json_extract(fields, '$.readAt') IS NOT NULL`,
}

var indexedParentFields = map[string]bool{
	"task":       true,
	"pane":       true,
	"parent":     true,
	"parentLane": true,
}

// ensureQueryIndexes creates expression and revision indexes used by indexed
// Query pushdown, ProjectContextAll, MaxChangeSet, QueryChangedSince, and
// newest-first listings of one kind.
func ensureQueryIndexes(db *sql.DB) error {
	for _, stmt := range queryIndexes {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("data: create query index: %w", err)
		}
	}
	return nil
}

// MaxChangeSet returns the highest change_set number among records of kind
// (or across all records when kind is empty), or 0 when none exist.
func (s *Store) MaxChangeSet(kind string) int64 {
	if s == nil || s.db == nil {
		return 0
	}
	var maxCS int64
	var err error
	if kind != "" {
		err = s.db.QueryRow("SELECT COALESCE(MAX(change_set), 0) FROM records WHERE kind = ?", kind).Scan(&maxCS)
	} else {
		err = s.db.QueryRow("SELECT COALESCE(MAX(change_set), 0) FROM records").Scan(&maxCS)
	}
	if err != nil {
		return 0
	}
	return maxCS
}

// QueryChangedSince returns the records of kind last written by a change set
// newer than sinceChangeSet, oldest change first. It reads only those rows
// (through idx_records_kind_changeset), so a caller holding the state as of
// sinceChangeSet can merge the delta instead of reading the whole kind again.
// A deleted record has no row and so is not reported.
func (s *Store) QueryChangedSince(kind string, sinceChangeSet int64, includeArchived bool) ([]Record, error) {
	sqlText, args := "SELECT "+recordCols+" FROM records WHERE kind = ? AND change_set > ?", []any{kind, sinceChangeSet}
	if !includeArchived {
		sqlText, args = sqlText+" AND archive_status = ?", append(args, ArchiveActive)
	}
	rows, err := s.db.Query(sqlText+" ORDER BY change_set, seq", args...)
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
	if err := rows.Close(); err != nil || len(recs) == 0 {
		return nil, err
	}
	local, err := loadLocal(s.db, kind, "")
	if err != nil {
		return nil, err
	}
	for i := range recs {
		recs[i].Local = local[recs[i].Ref()]
		s.coerce(&recs[i])
	}
	return recs, nil
}

// safeJSONField reports whether name is a safe JSON path identifier
// ([A-Za-z_][A-Za-z0-9_]*) that can be interpolated into json_extract.
func safeJSONField(name string) bool {
	if !plainField(name) {
		return false
	}
	return !(name[0] >= '0' && name[0] <= '9')
}

func (s *Store) resolveParentField(q Query) string {
	if q.ParentField != "" {
		return q.ParentField
	}
	if q.Kind != "" && s.reg != nil {
		if schema, ok := s.reg.Get(q.Kind); ok {
			if h := schema.EffectiveHierarchy(); h != nil && h.ParentField != "" {
				return h.ParentField
			}
		}
	}
	return "parent"
}

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

func buildQuerySQL(q Query, parentField string, pushParent bool) (string, []any) {
	var conds []string
	var args []any
	preferParentIdx := pushParent && q.Parent != nil && q.Kind != "" && indexedParentFields[parentField] && safeJSONField(parentField)
	if q.Kind != "" {
		conds, args = append(conds, "kind = ?"), append(args, q.Kind)
	}
	if !q.IncludeArchived {
		if preferParentIdx {
			conds, args = append(conds, "+archive_status = ?"), append(args, ArchiveActive)
		} else {
			conds, args = append(conds, "archive_status = ?"), append(args, ArchiveActive)
		}
	}
	if q.StarredOnly {
		if preferParentIdx {
			conds, args = append(conds, "+star_status = ?"), append(args, StarStarred)
		} else {
			conds, args = append(conds, "star_status = ?"), append(args, StarStarred)
		}
	}
	if pushParent && safeJSONField(parentField) {
		if q.Parent != nil {
			conds = append(conds,
				"json_extract(fields, '$."+parentField+".id') = ?",
				"json_extract(fields, '$."+parentField+".kind') = ?",
			)
			args = append(args, q.Parent.ID, q.Parent.Kind)
		}
		if q.Roots {
			conds = append(conds,
				"(json_extract(fields, '$."+parentField+".id') IS NULL OR json_extract(fields, '$."+parentField+".id') = '' OR json_extract(fields, '$."+parentField+".kind') IS NULL OR json_extract(fields, '$."+parentField+".kind') = '')",
			)
		}
	}
	sqlText := "SELECT " + recordCols + " FROM records"
	if len(conds) > 0 {
		sqlText += " WHERE " + strings.Join(conds, " AND ")
	}
	return sqlText + " ORDER BY created_at, seq", args
}

// Query returns the records matching q.
func (s *Store) Query(q Query) (QueryResult, error) {
	var res QueryResult
	local, err := loadLocal(s.db, q.Kind, "")
	if err != nil {
		return res, err
	}
	parentField := s.resolveParentField(q)
	pushParent := true
	if (q.Parent != nil || q.Roots) && len(local) > 0 {
		for _, fields := range local {
			if _, ok := fields[parentField]; ok {
				pushParent = false
				break
			}
		}
	}
	sqlText, args := buildQuerySQL(q, parentField, pushParent)
	rows, err := s.db.Query(sqlText, args...)
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

	keep := []func(Record) bool{}
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

// WithoutArchivedSubtrees returns recs, in order, without the archived
// records and without every record under one (through parentField; "" for a
// flat kind): what a list that hides archived work shows, whether it draws a
// tree or a table. keep names the archived records to leave in (a row still
// animating out); nothing under an archived record survives either way.
func WithoutArchivedSubtrees(recs []Record, parentField string, keep func(Record) bool) []Record {
	byRef := make(map[core.Ref]Record, len(recs))
	for _, rec := range recs {
		byRef[rec.Ref()] = rec
	}
	underArchived := func(rec Record) bool {
		if parentField == "" {
			return false
		}
		cur := rec
		for range len(recs) { // bounded: parents may form a loop
			parentRef, ok := cur.RefField(parentField)
			if !ok || parentRef == cur.Ref() {
				return false
			}
			parent, known := byRef[parentRef]
			if !known {
				return false
			}
			if parent.IsArchived() {
				return true
			}
			cur = parent
		}
		return false
	}
	out := make([]Record, 0, len(recs))
	for _, rec := range recs {
		if rec.IsArchived() && (keep == nil || !keep(rec)) {
			continue
		}
		if underArchived(rec) {
			continue
		}
		out = append(out, rec)
	}
	return out
}
