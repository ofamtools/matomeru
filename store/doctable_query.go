package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"ofam.dev/ochazuke"
)

// Query answers one page of the documents matching q, and how many match
// in all, as ochazuke's MemDocTable does, with SQLite doing the work:
//
//   - the archive hiding (Hide), the text search (each word in one of the
//     TextFields, case-insensitive) and every condition on a string, path,
//     url, integer, number or boolean prop (=, !=, in, not-in, contains,
//     not-contains; contains on a list prop too) are the statement's WHERE.
//     An equality repeats an Index's expression (json_extract(row,
//     '$.field') = ?), so a table indexed on the field reads the index;
//   - the sort keys, the groups (labels, first-seen order, GroupSort over
//     each group's first document) and the page (LIMIT and OFFSET, a page
//     past the end answering the last one) are its ORDER BY and window
//     functions, ordered by the ofam_doc_* functions (doctable_values.go),
//     which read values by ochazuke's rules;
//   - the count is a count(*) over the same WHERE.
//
// Only the page's documents are decoded. A condition it cannot put into
// SQL exactly (an equality or in on a list, time, object or duration prop,
// or a value that does not read as the prop's type) runs in Go: the
// documents that meet the rest of the request are read and the whole
// request is answered by Runtime.QueryAll (Fallbacks counts these).
//
// Differences from MemDocTable: a stored value whose JSON type is not its
// prop's (the text "2" in an integer prop) is not equal to the typed value
// in SQL, where ochazuke's comparison would convert it; numbers sort as
// float64 (equal past 2^53); a field name that is not letters, digits, _
// and - is a FieldNameError before anything runs, where MemDocTable would
// answer no documents for a condition on it.
func (d *DocTable) Query(q ochazuke.DocQuery) (ochazuke.ListPage, error) {
	d.queries.Add(1)
	if err := d.s.declared(d.table); err != nil {
		return ochazuke.ListPage{}, err
	}
	n := d.nounOf()
	w, err := d.whereOf(n, q)
	if err != nil {
		return ochazuke.ListPage{}, err
	}
	sorts, err := d.keysOf(n, q.Sort)
	if err != nil {
		return ochazuke.ListPage{}, err
	}
	groups, err := d.groupsOf(n, q.GroupBy)
	if err != nil {
		return ochazuke.ListPage{}, err
	}
	gsorts, err := d.keysOf(n, q.GroupSort)
	if err != nil {
		return ochazuke.ListPage{}, err
	}
	if !w.all {
		return d.fallback(q, w)
	}
	var total int
	if err := d.row(`SELECT count(*) FROM `+quote(d.table)+w.sql, w.args).Scan(&total); err != nil {
		return ochazuke.ListPage{}, err
	}
	page := ochazuke.ListPage{Items: []ochazuke.Thing{}, Total: total}
	if len(groups) > 0 {
		page.Groups = []string{}
	}
	if total == 0 {
		return page, nil
	}
	if err := d.dateKeys(w, sorts); err != nil {
		return ochazuke.ListPage{}, err
	}
	if len(groups) > 0 {
		if err := d.groupDateKeys(w, sorts, groups, gsorts); err != nil {
			return ochazuke.ListPage{}, err
		}
	}
	stmt, args := d.pageSQL(w, sorts, groups, gsorts)
	if pp := max(0, q.PerPage); pp > 0 {
		pages := max(1, (total+pp-1)/pp)
		at := min(max(q.Page, 1), pages)
		stmt += ` LIMIT ? OFFSET ?`
		args = append(args, pp, (at-1)*pp)
	}
	return d.readPage(page, stmt, args, len(groups) > 0)
}

// docWhere is a request's WHERE clause; all is false when a condition
// was left for Go.
type docWhere struct {
	sql  string
	args []any
	all  bool
}

// docKey is one sort key as SQL reads it.
type docKey struct {
	path string
	kind int
	enum string
	desc bool
	date bool
}

func (k docKey) expr() (string, []any) {
	return fmt.Sprintf("ofam_doc_sort(row -> %s, %d, ?, %d, %d)", k.path, k.kind, sqlBool(k.date), sqlBool(k.desc)), []any{k.enum}
}

// propOf is the noun's prop a name names (its name, an alias, or @role),
// as ochazuke's lookup reads it; nil when it names none.
func propOf(n *ochazuke.Noun, name string) *ochazuke.PropSpec {
	if n == nil {
		return nil
	}
	role, isRole := strings.CutPrefix(name, "@")
	if !isRole {
		// DocStore names a renamed prop by its stored field.
		for i := range n.Props {
			if p := &n.Props[i]; p.Field != "" && p.Field == name && p.Derivation == nil {
				return p
			}
		}
	}
	for i := range n.Props {
		p := &n.Props[i]
		if isRole && p.Role == role || !isRole && (p.Name == name || slices.Contains(p.Aliases, name)) {
			return p
		}
	}
	return nil
}

// fieldOf is the stored field a name reads: its prop's stored field
// (storedField), else the name.
func fieldOf(n *ochazuke.Noun, name string) (string, *ochazuke.PropSpec) {
	if p := propOf(n, name); p != nil {
		return storedField(p), p
	}
	return name, nil
}

// storedField is the document field that holds p: PropSpec.Field, else
// the prop's name.
func storedField(p *ochazuke.PropSpec) string {
	if p.Field != "" {
		return p.Field
	}
	return p.Name
}

func (d *DocTable) keysOf(n *ochazuke.Noun, keys []ochazuke.SortKey) ([]docKey, error) {
	out := make([]docKey, 0, len(keys))
	for _, k := range keys {
		field, p := fieldOf(n, k.Field)
		path, err := d.path(field)
		if err != nil {
			return nil, err
		}
		key := docKey{path: path, kind: sortDateText, desc: k.Desc}
		if p != nil {
			key.kind = kindsOf(p.Type).sort
			if len(p.Enum) > 0 {
				raw, _ := json.Marshal(p.Enum)
				key.kind, key.enum = sortEnum, string(raw)
			}
		}
		out = append(out, key)
	}
	return out, nil
}

func (d *DocTable) groupsOf(n *ochazuke.Noun, by []string) ([]string, error) {
	out := make([]string, 0, len(by))
	for _, name := range by {
		field, _ := fieldOf(n, name)
		path, err := d.path(field)
		if err != nil {
			return nil, err
		}
		out = append(out, path)
	}
	return out, nil
}

func (d *DocTable) whereOf(n *ochazuke.Noun, q ochazuke.DocQuery) (docWhere, error) {
	w := docWhere{all: true}
	var conds []string
	if q.Hide != "" {
		path, err := d.path(q.Hide)
		if err != nil {
			return w, err
		}
		conds = append(conds, "json_type(row, "+path+") IS NOT 'true'")
	}
	cells := make([]string, 0, len(q.TextFields))
	for _, f := range q.TextFields {
		path, err := d.path(f)
		if err != nil {
			return w, err
		}
		cells = append(cells, "COALESCE(instr(ofam_doc_cell(row -> "+path+"), ?), 0) > 0")
	}
	for _, word := range strings.Fields(strings.ToLower(q.Text)) {
		if len(cells) == 0 {
			conds = append(conds, "0")
			continue
		}
		conds = append(conds, "("+strings.Join(cells, " OR ")+")")
		for range cells {
			w.args = append(w.args, word)
		}
	}
	for _, f := range q.Filters {
		cond, args, ok, err := d.filterSQL(n, f)
		if err != nil {
			return w, err
		}
		if !ok {
			w.all = false
			continue
		}
		conds = append(conds, cond)
		w.args = append(w.args, args...)
	}
	if len(conds) > 0 {
		w.sql = " WHERE " + strings.Join(conds, " AND ")
	}
	return w, nil
}

// filterSQL is one condition as SQL; ok is false when it must run in Go.
// A document without the prop matches no condition, a negated one
// included (ochazuke's propsMatch).
func (d *DocTable) filterSQL(n *ochazuke.Noun, f ochazuke.PropFilter) (string, []any, bool, error) {
	field, p := fieldOf(n, f.Prop)
	path, err := d.path(field)
	if err != nil || p == nil {
		return "0", nil, err == nil, err
	}
	op := f.Op
	if op == "" {
		op = "="
	}
	tests := map[string]struct {
		test func(*ochazuke.PropSpec, string, any) (string, []any, bool)
		not  bool
	}{
		"=":            {eqSQL, false},
		"!=":           {eqSQL, true},
		"contains":     {containsSQL, false},
		"not-contains": {containsSQL, true},
		"in":           {inSQL, false},
		"not-in":       {inSQL, true},
	}
	t, known := tests[op]
	if !known {
		return "0", nil, true, nil
	}
	cond, args, ok := t.test(p, path, f.Value)
	if ok && t.not {
		cond = "(json_type(row, " + path + ") IS NOT NULL AND NOT COALESCE(" + cond + ", 0))"
	}
	return cond, args, ok, nil
}

// eqSQL is an equality as SQL, the value typed by the prop as ochazuke
// types it; nil equals a stored null.
func eqSQL(p *ochazuke.PropSpec, path string, v any) (string, []any, bool) {
	if v == nil {
		return "(json_type(row, " + path + ") = 'null')", nil, true
	}
	kind := kindsOf(p.Type).filter
	typed, ok := typedValue(kind, v)
	if !ok {
		return "", nil, false
	}
	return "(json_extract(row, " + path + ") = ? AND json_type(row, " + path + ") IN (" + jsonTypes(kind) + "))", []any{typed}, true
}

// jsonTypes are the stored JSON types an equality of the kind compares.
func jsonTypes(kind int) string {
	switch kind {
	case filterInt, filterFloat:
		return "'integer', 'real'"
	case filterBool:
		return "'true', 'false'"
	}
	return "'text'"
}

// typedValue is a wanted value as the prop's type reads it (ochazuke's
// coercion), false when it does not read as one.
func typedValue(kind int, v any) (any, bool) {
	text, isText := v.(string)
	if isText {
		text = strings.TrimSpace(text)
	}
	switch kind {
	case filterText:
		return v, isText
	case filterInt:
		switch x := v.(type) {
		case int:
			return x, true
		case int64:
			return x, true
		case float64:
			return int(x), x == float64(int(x))
		}
		i, err := strconv.Atoi(text)
		return i, isText && err == nil
	case filterFloat:
		switch x := v.(type) {
		case int:
			return float64(x), true
		case float64:
			return x, true
		}
		f, err := strconv.ParseFloat(text, 64)
		return f, isText && err == nil
	case filterBool:
		if b, ok := v.(bool); ok {
			return sqlBool(b), true
		}
		b, err := strconv.ParseBool(text)
		return sqlBool(b), isText && err == nil
	}
	return nil, false
}

// containsSQL is a substring of a text value (case-sensitive, as
// ochazuke's), or an item of a list prop's strings.
func containsSQL(p *ochazuke.PropSpec, path string, v any) (string, []any, bool) {
	sub, ok := v.(string)
	if !ok {
		return "0", nil, true
	}
	if kindsOf(p.Type).list {
		return "(json_type(row, " + path + ") = 'array' AND EXISTS (SELECT 1 FROM json_each(row, " + path + ") WHERE type = 'text' AND value = ?) AND NOT EXISTS (SELECT 1 FROM json_each(row, " + path + ") WHERE type <> 'text'))", []any{sub}, true
	}
	return "(json_type(row, " + path + ") = 'text' AND instr(json_extract(row, " + path + "), ?) > 0)", []any{sub}, true
}

// inSQL is any of the values (ochazuke's filterValues reads them).
func inSQL(p *ochazuke.PropSpec, path string, v any) (string, []any, bool) {
	values, ok := filterValues(v)
	if !ok || len(values) == 0 {
		return "0", nil, true
	}
	parts := make([]string, 0, len(values))
	var args []any
	for _, one := range values {
		cond, a, ok := eqSQL(p, path, one)
		if !ok {
			return "", nil, false
		}
		parts = append(parts, cond)
		args = append(args, a...)
	}
	return "(" + strings.Join(parts, " OR ") + ")", args, true
}

// filterValues is an in condition's alternatives: an array, a JSON array
// written as text, or anything that is an array as JSON.
func filterValues(v any) ([]any, bool) {
	if values, ok := v.([]any); ok && values != nil {
		return values, true
	}
	var raw []byte
	if word, ok := v.(string); ok {
		raw = []byte(word)
	} else {
		var err error
		if raw, err = json.Marshal(v); err != nil {
			return nil, false
		}
	}
	var values []any
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return nil, false
	}
	return values, true
}

// dateKeys marks the text sort keys whose every non-blank matching value
// is a date or time: those order by time (ochazuke's dateTyped).
func (d *DocTable) dateKeys(w docWhere, keys []docKey) error {
	for i := range keys {
		if keys[i].kind != sortDateText {
			continue
		}
		var all sql.NullInt64
		stmt := `SELECT MIN(ofam_doc_isdate(row -> ` + keys[i].path + `)) FROM ` + quote(d.table) + w.sql + and(w.sql) + `NOT ofam_doc_blank(row -> ` + keys[i].path + `)`
		if err := d.row(stmt, w.args).Scan(&all); err != nil {
			return err
		}
		keys[i].date = all.Valid && all.Int64 == 1
	}
	return nil
}

// groupDateKeys marks the text group-sort keys whose every non-blank value
// on each group's first document is a date or time.
func (d *DocTable) groupDateKeys(w docWhere, sorts []docKey, groups []string, gsorts []docKey) error {
	ordered, args := d.orderedSQL(w, sorts, groups)
	for i := range gsorts {
		if gsorts[i].kind != sortDateText {
			continue
		}
		var all sql.NullInt64
		stmt := `WITH o AS (` + ordered + `), f AS (SELECT row, pos, MIN(pos) OVER (PARTITION BY g) AS gfirst FROM o) ` +
			`SELECT MIN(ofam_doc_isdate(row -> ` + gsorts[i].path + `)) FROM f WHERE pos = gfirst AND NOT ofam_doc_blank(row -> ` + gsorts[i].path + `)`
		if err := d.row(stmt, args).Scan(&all); err != nil {
			return err
		}
		gsorts[i].date = all.Valid && all.Int64 == 1
	}
	return nil
}

func and(where string) string {
	if where == "" {
		return " WHERE "
	}
	return " AND "
}

// orderBy is the ORDER BY terms of the sort keys, insertion order last.
func orderBy(keys []docKey) (string, []any) {
	terms := make([]string, 0, len(keys)+1)
	var args []any
	for _, k := range keys {
		e, a := k.expr()
		terms = append(terms, e)
		args = append(args, a...)
	}
	return strings.Join(append(terms, "rowid"), ", "), args
}

// orderedSQL numbers the matching documents in sort order (pos) and
// labels each with its group (g).
func (d *DocTable) orderedSQL(w docWhere, sorts []docKey, groups []string) (string, []any) {
	order, oargs := orderBy(sorts)
	labels := make([]string, len(groups))
	for i, path := range groups {
		labels[i] = "ofam_doc_label(row -> " + path + ")"
	}
	stmt := `SELECT row, ROW_NUMBER() OVER (ORDER BY ` + order + `) AS pos, ` + strings.Join(labels, " || ' / ' || ") + ` AS g FROM ` + quote(d.table) + w.sql
	return stmt, append(oargs, w.args...)
}

// pageSQL is the statement answering the page's documents, before LIMIT.
func (d *DocTable) pageSQL(w docWhere, sorts []docKey, groups []string, gsorts []docKey) (string, []any) {
	if len(groups) == 0 {
		order, oargs := orderBy(sorts)
		return `SELECT row FROM ` + quote(d.table) + w.sql + ` ORDER BY ` + order, append(append([]any{}, w.args...), oargs...)
	}
	ordered, args := d.orderedSQL(w, sorts, groups)
	cols := []string{"row", "pos", "g", "MIN(pos) OVER (PARTITION BY g) AS gfirst"}
	var terms []string
	var gargs []any
	for i, k := range gsorts {
		e, a := k.expr()
		cols = append(cols, fmt.Sprintf("FIRST_VALUE(%s) OVER (PARTITION BY g ORDER BY pos) AS k%d", e, i))
		terms = append(terms, fmt.Sprintf("k%d", i))
		gargs = append(gargs, a...)
	}
	terms = append(terms, "gfirst", "pos")
	stmt := `WITH o AS (` + ordered + `), f AS (SELECT ` + strings.Join(cols, ", ") + ` FROM o) SELECT row, g FROM f ORDER BY ` + strings.Join(terms, ", ")
	return stmt, append(args, gargs...)
}

func (d *DocTable) row(stmt string, args []any) *sql.Row {
	if d.trace != nil {
		d.trace(stmt, args)
	}
	return d.s.db.QueryRow(stmt, args...)
}

// readPage decodes the page's documents (and group labels).
func (d *DocTable) readPage(page ochazuke.ListPage, stmt string, args []any, grouped bool) (ochazuke.ListPage, error) {
	if d.trace != nil {
		d.trace(stmt, args)
	}
	rows, err := d.s.db.Query(stmt, args...)
	if err != nil {
		return ochazuke.ListPage{}, err
	}
	defer rows.Close()
	n := d.nounOf()
	for rows.Next() {
		var raw []byte
		var label string
		dest := []any{&raw}
		if grouped {
			dest = append(dest, &label)
		}
		if err := rows.Scan(dest...); err != nil {
			return ochazuke.ListPage{}, err
		}
		t, err := decodeDoc(raw, n)
		if err != nil {
			return ochazuke.ListPage{}, err
		}
		d.reads.Add(1)
		page.Items = append(page.Items, t)
		if grouped {
			page.Groups = append(page.Groups, label)
		}
	}
	return page, rows.Err()
}

// fallback answers a request with a condition SQL cannot run exactly: the
// documents meeting the rest are read, in insertion order, and the whole
// request runs over them in Go.
func (d *DocTable) fallback(q ochazuke.DocQuery, w docWhere) (ochazuke.ListPage, error) {
	d.fallbacks.Add(1)
	page, err := d.readPage(ochazuke.ListPage{Items: []ochazuke.Thing{}}, `SELECT row FROM `+quote(d.table)+w.sql+` ORDER BY rowid`, w.args, false)
	if err != nil {
		return ochazuke.ListPage{}, err
	}
	return d.r.QueryAll(d.noun, page.Items, ochazuke.ListQuery{Filters: q.Filters, Sort: q.Sort, GroupBy: q.GroupBy, GroupSort: q.GroupSort, Page: q.Page, PerPage: q.PerPage, Archived: true}), nil
}
