package store

import (
	"encoding/json"
	"fmt"
	"strings"
)

// QueryOps are the operators of a QueryCond. A condition on a field no row
// has treats the row's value as the field's zero (empty text, 0, false), so
// "archived = false" matches a row that never wrote the field.
const (
	OpEq       = "="
	OpNe       = "!="
	OpContains = "contains" // case-insensitive substring; a list value: any of them
	OpIn       = "in"
	OpLt       = "<"
	OpLe       = "<="
	OpGt       = ">"
	OpGe       = ">="
)

// QueryCond is one condition on a row. Fields lists JSON paths; the value
// the row has is the first of them that is not zero ("last_seen" then
// "created" reads the last sighting, else the creation time). Value is a
// string, number or bool, or for in/contains a slice of them.
type QueryCond struct {
	Fields []string
	Op     string
	Value  any
	// Not turns the condition over (not-contains, not-in).
	Not bool
	// Any and All compose conditions; leaf values remain bound SQL arguments.
	Any []QueryCond
	All []QueryCond
}

// QuerySort orders by one field (same Fields rule). Text sorts compare
// without case.
type QuerySort struct {
	Fields []string
	Desc   bool
	Text   bool
}

// QueryProjection adds a caller-owned SQL expression to the JSON row before
// filtering and arranging it. Values remain bound arguments, never SQL text.
type QueryProjection struct {
	Field string
	SQL   string
	Args  []any
}

// Query is one read of a table that SQLite answers: the rows meeting every
// condition (Where) and every word of Search in some field of SearchIn, in
// Sort order (else OrderSQL, a fragment the caller wrote, never user text;
// rows that tie keep insertion order), cut to Limit rows from Offset
// (Limit 0: all of them), and how many rows matched before the cut. It
// reads and decodes only the page's rows, and is not a full read (FullReads
// does not count it). A Query is a generic pushdown: it knows nothing of
// any table's props; the caller maps its props to row fields.
type Query struct {
	Where     []QueryCond
	Search    string
	SearchIn  [][]string
	Sort      []QuerySort
	OrderSQL  string
	OrderArgs []any
	// Projections are derived fields; GroupBy gathers equal values in first-seen
	// order after sorting, unless GroupSort explicitly orders the groups.
	Projections []QueryProjection
	GroupBy     []QuerySort
	GroupSort   []QuerySort
	Limit       int
	Offset      int
}

// QueryResult is a Query's answer: the page's rows and Total, the number of
// rows that matched.
type QueryResult struct {
	Rows  []json.RawMessage
	Total int
}

// valueExpr is the SQL value of the first non-zero field of fields.
func valueExpr(fields []string) (string, error) {
	if len(fields) == 0 {
		return "", fmt.Errorf("store: query needs a field")
	}
	parts := make([]string, len(fields))
	for i, f := range fields {
		e, err := fieldExpr(f)
		if err != nil {
			return "", err
		}
		parts[i] = e
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	for i := range parts[:len(parts)-1] {
		parts[i] = "NULLIF(NULLIF(" + parts[i] + ", 0), '')"
	}
	return "COALESCE(" + strings.Join(parts, ", ") + ")", nil
}

func sqlArg(v any) any {
	if b, ok := v.(bool); ok {
		if b {
			return 1
		}
		return 0
	}
	return v
}

func isZero(v any) bool {
	switch t := sqlArg(v).(type) {
	case string:
		return t == ""
	case int:
		return t == 0
	case int64:
		return t == 0
	case float64:
		return t == 0
	}
	return false
}

func listOf(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case []string:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out
	case []int:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out
	}
	return []any{v}
}

// sql is the SQL of one condition.
func (c QueryCond) sql() (string, []any, error) {
	s, args, err := c.test()
	if err == nil && c.Not {
		s = "NOT (" + s + ")"
	}
	return s, args, err
}

func (c QueryCond) test() (string, []any, error) {
	if c.Any != nil || c.All != nil {
		children, join := c.Any, " OR "
		if c.All != nil {
			children, join = c.All, " AND "
		}
		parts := make([]string, 0, len(children))
		var args []any
		for _, child := range children {
			part, values, err := child.sql()
			if err != nil {
				return "", nil, err
			}
			parts, args = append(parts, part), append(args, values...)
		}
		if len(parts) == 0 {
			if c.All != nil {
				return "1", nil, nil
			}
			return "0", nil, nil
		}
		return "(" + strings.Join(parts, join) + ")", args, nil
	}
	expr, err := valueExpr(c.Fields)
	if err != nil {
		return "", nil, err
	}
	// A missing field is its zero value: the row's value is read as
	// COALESCE(value, zero) where the compared value says which zero.
	switch c.Op {
	case OpEq, OpNe:
		v := sqlArg(c.Value)
		not := ""
		if c.Op == OpNe {
			not = "NOT "
		}
		if isZero(c.Value) {
			zero := any(0)
			if _, ok := v.(string); ok {
				zero = ""
			}
			return not + "(COALESCE(" + expr + ", ?) = ?)", []any{zero, v}, nil
		}
		return not + "(COALESCE(" + expr + " = ?, 0))", []any{v}, nil
	case OpIn:
		vals := listOf(c.Value)
		if len(vals) == 0 {
			return "0", nil, nil
		}
		marks := strings.TrimSuffix(strings.Repeat("?,", len(vals)), ",")
		args := make([]any, len(vals))
		for i, v := range vals {
			args[i] = sqlArg(v)
		}
		return "COALESCE(" + expr + " IN (" + marks + "), 0)", args, nil
	case OpContains:
		vals := listOf(c.Value)
		if len(vals) == 0 {
			return "1", nil, nil
		}
		var parts []string
		var args []any
		for _, v := range vals {
			parts = append(parts, "instr(lower(COALESCE(CAST("+expr+" AS TEXT), '')), lower(?)) > 0")
			args = append(args, fmt.Sprint(v))
		}
		return "(" + strings.Join(parts, " OR ") + ")", args, nil
	case OpLt, OpLe, OpGt, OpGe:
		return "(COALESCE(" + expr + ", 0) " + c.Op + " ?)", []any{sqlArg(c.Value)}, nil
	}
	return "", nil, fmt.Errorf("store: unknown query operator %q", c.Op)
}

func (q Query) where() (string, []any, error) {
	var conds []string
	var args []any
	for _, c := range q.Where {
		s, a, err := c.sql()
		if err != nil {
			return "", nil, err
		}
		conds = append(conds, s)
		args = append(args, a...)
	}
	for _, word := range strings.Fields(q.Search) {
		var any []string
		for _, f := range q.SearchIn {
			s, a, err := QueryCond{Fields: f, Op: OpContains, Value: word}.sql()
			if err != nil {
				return "", nil, err
			}
			any = append(any, s)
			args = append(args, a...)
		}
		if len(any) == 0 {
			return "", nil, fmt.Errorf("store: search needs fields to search in")
		}
		conds = append(conds, "("+strings.Join(any, " OR ")+")")
	}
	if len(conds) == 0 {
		return "", nil, nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args, nil
}

func (q Query) order() (string, []any, error) {
	var parts []string
	for _, k := range q.Sort {
		e, err := valueExpr(k.Fields)
		if err != nil {
			return "", nil, err
		}
		if k.Text {
			e = "lower(COALESCE(CAST(" + e + " AS TEXT), ''))"
		} else {
			e = "COALESCE(" + e + ", 0)"
		}
		if k.Desc {
			e += " DESC"
		}
		parts = append(parts, e)
	}
	var args []any
	if q.OrderSQL != "" {
		parts = append(parts, q.OrderSQL)
		args = q.OrderArgs
	}
	parts = append(parts, "rowid")
	return " ORDER BY " + strings.Join(parts, ", "), args, nil
}

// source projects derived fields once, before conditions and window functions.
func (q Query) source(table string) (string, string, []any, error) {
	if len(q.Projections) == 0 {
		return "", quote(table), nil, nil
	}
	parts := []string{"source.row"}
	var args []any
	for _, p := range q.Projections {
		if _, err := fieldExpr(p.Field); err != nil {
			return "", "", nil, err
		}
		parts = append(parts, "?, "+p.SQL)
		args = append(args, "$."+p.Field)
		args = append(args, p.Args...)
	}
	prefix := "WITH projected AS (SELECT source.rowid, json_set(" + strings.Join(parts, ", ") + ") AS row FROM " + quote(table) + " AS source) "
	return prefix, "projected", args, nil
}

// grouped gathers rows by the first sorted occurrence of each group, retaining
// their order within it. The final LIMIT/OFFSET is applied only to this result.
func (q Query) grouped(source, where, order string) (string, error) {
	var by []string
	for _, k := range q.GroupBy {
		e, err := valueExpr(k.Fields)
		if err != nil {
			return "", err
		}
		if k.Text {
			e = "CASE WHEN trim(COALESCE(CAST(" + e + " AS TEXT), '')) = '' THEN '' ELSE CAST(" + e + " AS TEXT) END"
		} else {
			e = "COALESCE(" + e + ", 0)"
		}
		by = append(by, e)
	}
	partition := strings.Join(by, ", ")
	parts := make([]string, 0, len(q.GroupSort)+2)
	for _, k := range q.GroupSort {
		// FIRST_VALUE reads the group's first row, as the document adapter does.
		value, err := valueExpr(k.Fields)
		if err != nil {
			return "", err
		}
		if k.Text {
			value = "lower(COALESCE(CAST(" + value + " AS TEXT), ''))"
		} else {
			value = "COALESCE(" + value + ", 0)"
		}
		value = "FIRST_VALUE(" + value + ") OVER (PARTITION BY " + partition + " ORDER BY position)"
		if k.Desc {
			value += " DESC"
		}
		parts = append(parts, value)
	}
	parts = append(parts, "MIN(position) OVER (PARTITION BY "+partition+")", "position")
	return "SELECT row FROM (WITH matched AS (SELECT row, rowid FROM " + source + where + ") SELECT row, ROW_NUMBER() OVER (" + order + ") AS position FROM matched) ORDER BY " + strings.Join(parts, ", "), nil
}

// Query runs q on a declared table (see Query). The count and the page are
// two statements over the same conditions; a table with an Index over the
// fields a condition names reads the index.
func (s *Store) Query(table string, q Query) (QueryResult, error) {
	if err := s.declared(table); err != nil {
		return QueryResult{}, err
	}
	where, wargs, err := q.where()
	if err != nil {
		return QueryResult{}, err
	}
	var res QueryResult
	prefix, source, pargs, err := q.source(table)
	if err != nil {
		return QueryResult{}, err
	}
	countArgs := append(append([]any{}, pargs...), wargs...)
	if err := s.db.QueryRow(prefix+`SELECT count(*) FROM `+source+where, countArgs...).Scan(&res.Total); err != nil {
		return QueryResult{}, err
	}
	if q.Limit < 0 || q.Offset < 0 || q.Limit == 0 && q.Offset > 0 {
		return QueryResult{}, fmt.Errorf("store: query limit %d offset %d", q.Limit, q.Offset)
	}
	order, oargs, err := q.order()
	if err != nil {
		return QueryResult{}, err
	}
	sql := `SELECT row FROM ` + source + where + order
	if len(q.GroupBy) > 0 {
		sql, err = q.grouped(source, where, order)
		if err != nil {
			return QueryResult{}, err
		}
	}
	sql = prefix + sql
	args := append(countArgs, oargs...)
	if q.Limit > 0 {
		sql += ` LIMIT ? OFFSET ?`
		args = append(args, q.Limit, q.Offset)
	}
	res.Rows, err = readRows(s.db.Query(sql, args...))
	return res, err
}
