package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

// tableColumn says where a prop lives in a table's JSON row. Fields are
// row paths (the first non-zero one is the value, so "last_seen" then
// "created" reads the last sighting); Kind is text, number, bool, or stamp
// (a number that stands for a flag: the prop is true when it is above 0, an
// archive time is the archived prop). A prop with no column is derived
// (computed from other tables or the clock) and cannot be pushed.
// A columnKind is how a prop reads from its row field.
type columnKind uint8

// The kinds of a tableColumn.
const (
	columnText columnKind = iota
	columnNumber
	columnBool
	columnStamp
)

type tableColumn struct {
	Fields []string
	Kind   columnKind
	// Match, when set, is the condition of an equality filter on a prop
	// whose meaning is not one field (a derived reference: the context
	// items of a pane's project); its other operators are not pushed.
	Match func(value string) (store.QueryCond, bool)
}

// tableQuery is one table-backed noun's ochazuke Store.Query: the
// conditions, sort, text and page of a list request run in SQLite over the
// JSON row (store.Query), so a count or a page of a 20,000 row table reads
// that page only. A request it cannot push (a derived prop in a filter or
// sort, a list argument it has no condition for) is answered the
// way the store's List and the library's queryAll answer it, so every
// request has one right answer and pushing is only ever faster. The
// per-noun part is data: the columns, the searched props, the default
// order and the mapping of the store's ListParams.
type tableQuery struct {
	app   *App
	noun  string
	table string
	// columns maps a prop to its row fields; archive names the archive-role
	// prop, hidden unless the request asks for archived rows.
	columns map[string]tableColumn
	archive string
	// projections supply SQL-derived row fields only when a query uses them.
	projections map[string]func() []store.QueryProjection
	// search are the props a text query matches.
	search []string
	// args turns the request's ListParams into conditions; Every skips
	// defaults. False means the request needs the store's List.
	args func(ochazuke.ListQuery) ([]store.QueryCond, bool)
	// order is the default order (a fragment written here, never user text)
	// and its arguments, used when the request names no sort.
	order     string
	orderArgs func() []any
	// thing starts a page: it reads what every row of the page shares once
	// and answers the builder of the noun's thing from a row. list is the
	// store's List, the answer to a request that is not pushed.
	thing func() func(json.RawMessage) (ochazuke.Thing, error)
	list  func(ochazuke.Ctx, ochazuke.ListQuery) ([]ochazuke.Thing, error)
}

// Query is the ochazuke Store.Query.
func (t *tableQuery) Query(c ochazuke.Ctx, q ochazuke.ListQuery) (ochazuke.ListPage, error) {
	sq, ok := t.translate(q)
	if !ok {
		things, err := t.list(c, q)
		if err != nil {
			return ochazuke.ListPage{}, err
		}
		return t.app.r.QueryAll(t.noun, things, q), nil
	}
	res, err := t.app.db.Query(t.table, sq)
	if err != nil {
		return ochazuke.ListPage{}, err
	}
	page := ochazuke.ListPage{Total: res.Total, Items: make([]ochazuke.Thing, 0, len(res.Rows))}
	build := t.thing()
	for _, raw := range res.Rows {
		th, err := build(raw)
		if err != nil {
			return ochazuke.ListPage{}, fmt.Errorf("%s: %w", t.noun, err)
		}
		page.Items = append(page.Items, th)
	}
	return page, nil
}

// translate is the store query for q, false when q needs more than SQLite
// can see.
func (t *tableQuery) translate(q ochazuke.ListQuery) (store.Query, bool) {
	var sq store.Query
	if t.args == nil {
		return sq, false
	}
	conds, ok := t.args(q)
	if !ok {
		return sq, false
	}
	sq.Where = conds
	// The default view hides archived things unless the query includes them,
	// the setting is off, or a condition names the archive prop
	// (ochazuke's archiveHiding, one rule).
	if t.archive != "" && !q.Archived && t.app.r.Settings().Bool(ochazuke.HideArchivedSetting) &&
		!slices.ContainsFunc(q.Filters, func(f ochazuke.PropFilter) bool { return f.Prop == t.archive }) {
		cs, ok := t.condition(ochazuke.PropFilter{Prop: t.archive, Op: "=", Value: false})
		if !ok {
			return sq, false
		}
		sq.Where = append(sq.Where, cs)
	}
	for _, f := range q.Filters {
		cs, ok := t.condition(f)
		if !ok {
			return sq, false
		}
		sq.Where = append(sq.Where, cs)
	}
	if !t.arrange(q, &sq) {
		return sq, false
	}
	if strings.TrimSpace(q.Text) != "" {
		if len(t.search) == 0 {
			return sq, false
		}
		sq.Search = q.Text
		for _, p := range t.search {
			col, ok := t.columns[p]
			if !ok || len(col.Fields) == 0 {
				return sq, false
			}
			sq.SearchIn = append(sq.SearchIn, col.Fields)
		}
	}
	sq.OrderSQL = t.order
	if t.orderArgs != nil {
		sq.OrderArgs = t.orderArgs()
	}
	if q.PerPage > 0 {
		sq.Limit, sq.Offset = q.PerPage, (max(q.Page, 1)-1)*q.PerPage
	}
	t.project(&sq)
	return sq, true
}

func (t *tableQuery) arrange(q ochazuke.ListQuery, sq *store.Query) bool {
	// Enum ordering belongs to the declared prop, not lexical SQL ordering.
	for _, key := range q.GroupSort {
		for _, p := range t.app.r.Noun(t.noun).Props {
			if p.Name == key.Field && len(p.Enum) > 0 {
				return false
			}
		}
	}
	for _, pair := range []struct {
		from []ochazuke.SortKey
		to   *[]store.QuerySort
	}{{q.Sort, &sq.Sort}, {q.GroupSort, &sq.GroupSort}} {
		for _, k := range pair.from {
			col, ok := t.columns[k.Field]
			if !ok || len(col.Fields) == 0 {
				return false
			}
			*pair.to = append(*pair.to, store.QuerySort{Fields: col.Fields, Desc: k.Desc, Text: col.Kind == columnText})
		}
	}
	for _, field := range q.GroupBy {
		col, ok := t.columns[field]
		if !ok || col.Kind == columnStamp || len(col.Fields) == 0 {
			return false
		}
		sq.GroupBy = append(sq.GroupBy, store.QuerySort{Fields: col.Fields, Text: col.Kind == columnText})
	}
	return true
}

func (t *tableQuery) project(sq *store.Query) {
	seen := map[string]bool{}
	add := func(fields []string) {
		for _, field := range fields {
			root, _, _ := strings.Cut(field, ".")
			if project := t.projections[root]; project != nil && !seen[root] {
				sq.Projections = append(sq.Projections, project()...)
				seen[root] = true
			}
		}
	}
	for _, c := range sq.Where {
		add(c.Fields)
	}
	for _, sorts := range [][]store.QuerySort{sq.Sort, sq.GroupSort} {
		for _, k := range sorts {
			add(k.Fields)
		}
	}
	for _, group := range sq.GroupBy {
		add(group.Fields)
	}
	for _, fields := range sq.SearchIn {
		add(fields)
	}
}

// condition is the store condition for one prop filter.
func (t *tableQuery) condition(f ochazuke.PropFilter) (store.QueryCond, bool) {
	col, ok := t.columns[f.Prop]
	if !ok {
		return store.QueryCond{}, false
	}
	if col.Match != nil {
		if f.Op != "" && f.Op != "=" {
			return store.QueryCond{}, false
		}
		return col.Match(fmt.Sprint(f.Value))
	}
	op, not := f.Op, false
	switch op {
	case "", "=":
		op = store.OpEq
	case "!=":
		op = store.OpEq
		not = true
	case "contains":
		op = store.OpContains
	case "not-contains":
		op, not = store.OpContains, true
	case "in":
		op = store.OpIn
	case "not-in":
		op, not = store.OpIn, true
	default:
		return store.QueryCond{}, false
	}
	value, ok := col.value(f.Value, op == store.OpIn)
	if !ok {
		return store.QueryCond{}, false
	}
	if col.Kind == columnStamp && op == store.OpEq {
		// true: stamped (above 0); false: not.
		want, _ := value.(bool)
		if want {
			return store.QueryCond{Fields: col.Fields, Op: store.OpGt, Value: 0, Not: not}, true
		}
		return store.QueryCond{Fields: col.Fields, Op: store.OpLe, Value: 0, Not: not}, true
	}
	if col.Kind == columnStamp {
		return store.QueryCond{}, false
	}
	return store.QueryCond{Fields: col.Fields, Op: op, Value: value, Not: not}, true
}

// value reads a filter value as the column's kind (a filter chip's value is
// text); false when it does not read as one.
func (c tableColumn) value(v any, list bool) (any, bool) {
	if list {
		var out []any
		switch t := v.(type) {
		case []any:
			for _, e := range t {
				x, ok := c.value(e, false)
				if !ok {
					return nil, false
				}
				out = append(out, x)
			}
		case []string:
			for _, e := range t {
				x, ok := c.value(e, false)
				if !ok {
					return nil, false
				}
				out = append(out, x)
			}
		default:
			x, ok := c.value(v, false)
			if !ok {
				return nil, false
			}
			out = append(out, x)
		}
		return out, true
	}
	switch c.Kind {
	case columnBool, columnStamp:
		switch t := v.(type) {
		case bool:
			return t, true
		case string:
			b, err := strconv.ParseBool(t)
			return b, err == nil
		}
		return nil, false
	case columnNumber:
		switch t := v.(type) {
		case int:
			return t, true
		case int64:
			return t, true
		case float64:
			return t, true
		case string:
			n, err := strconv.ParseFloat(t, 64)
			return n, err == nil
		}
		return nil, false
	}
	if s, ok := v.(string); ok {
		return s, true
	}
	if v == nil {
		return "", true
	}
	return fmt.Sprint(v), true
}
