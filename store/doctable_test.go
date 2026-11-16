package store

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
	"ofam.dev/ochazuke/doctest"
)

// docFixture is a store with the table "docs" declared and a DocTable over
// it for noun on r.
func docFixture(t *testing.T, r *ochazuke.Runtime, noun string) (*Store, *DocTable) {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Declare(Table{Name: "docs", Owner: "docs", Key: "id", Version: 1}); err != nil {
		t.Fatal(err)
	}
	return db, db.DocTable("docs", r, noun)
}

// The sqlite table keeps ochazuke's DocTable contract.
func TestDocTableKeepsTheContract(t *testing.T) {
	t.Parallel()
	doctest.Run(t, func(t *testing.T, r *ochazuke.Runtime, noun string) ochazuke.DocTable {
		_, d := docFixture(t, r, noun)
		return d
	})
}

// parityProps are the contract's props and one of every other kind a
// query reads differently: a date text, a free text, a list, a number and
// a boolean.
var parityProps = append(slices.Clone(doctest.Props),
	ochazuke.PropSpec{Name: "due"},
	ochazuke.PropSpec{Name: "kind"},
	ochazuke.PropSpec{Name: "tags", Parameter: ochazuke.Parameter{Type: ochazuke.SettingList}},
	ochazuke.PropSpec{Name: "score", Parameter: ochazuke.Parameter{Type: ochazuke.SettingNumber}},
	ochazuke.PropSpec{Name: "flag", Parameter: ochazuke.Parameter{Type: ochazuke.SettingBoolean}},
)

// parityDocs are n documents with blanks, missing fields, nulls, mixed
// case, dates in two forms and a few values that are not dates.
func parityDocs(n int) []ochazuke.Thing {
	rng := rand.New(rand.NewPCG(7, 11))
	pick := func(vals ...any) any { return vals[rng.IntN(len(vals))] }
	statuses := []string{"todo", "doing", "done"}
	var out []ochazuke.Thing
	for i := 1; i <= n; i++ {
		t := ochazuke.Thing{"id": fmt.Sprintf("d%d", i), "title": fmt.Sprintf("%s %d", pick("doc", "Doc", "note"), i), "size": rng.IntN(9)}
		if i%7 != 0 {
			t["status"] = statuses[rng.IntN(3)]
		}
		t["archived"] = i%5 == 0
		if v := pick("2026-01-02", "2026-01-02T10:00:00+02:00", "2025-12-31T23:59:59Z", "2026-03-04", "", nil, "missing"); v != "missing" {
			t["due"] = v
		}
		if i%13 == 0 {
			t["due"] = "soon"
			t["kind"] = "late"
		} else if v := pick("Alpha", "alpha", "beta", " ", "", "(none)", "Ünïcode", nil, "missing"); v != "missing" {
			t["kind"] = v
		}
		if v := pick([]string{"red"}, []string{"blue", "red"}, []string{}, nil, "missing"); v != "missing" {
			t["tags"] = v
		}
		if v := pick(1.5, 2.0, -3.25, 10.0, nil, "missing"); v != "missing" {
			t["score"] = v
		}
		if v := pick(true, false, "missing"); v != "missing" {
			t["flag"] = v
		}
		if v := pick(3, "three", 1.5, "missing"); v != "missing" {
			t["extra"] = v
		}
		out = append(out, t)
	}
	return out
}

// pageOf is a page as the parity test compares it: the keys, the groups
// and the total.
func pageOf(p ochazuke.ListPage) string {
	ids := make([]string, len(p.Items))
	for i, t := range p.Items {
		ids[i] = fmt.Sprint(t["id"])
	}
	return fmt.Sprintf("total %d ids %v groups %v", p.Total, ids, append([]string{}, p.Groups...))
}

// The sqlite table answers what MemDocTable answers, for every kind of
// condition, sort, group, text search and page, alone and combined.
func TestDocTableMatchesMemDocTable(t *testing.T) {
	t.Parallel()
	r := ochazuke.NewRuntime()
	r.Config().Disable()
	mem := ochazuke.NewMemDocTable(r, "doc")
	r.Define("doc").Props(parityProps...).Store(ochazuke.DocStore(mem, "doc"))
	db, d := docFixture(t, r, "doc")
	// The rows are written as JSON, the way other code writes a table's
	// rows, as MemDocTable holds them: since ochazuke v0.146.4 its Put
	// removes a field given as nil, so neither table holds a null.
	if err := db.Tx([]string{"docs"}, func(tx *Tx) error {
		for _, doc := range parityDocs(80) {
			id := fmt.Sprint(doc["id"])
			if err := mem.Put(id, doc); err != nil {
				return err
			}
			held, _, err := mem.Get(id)
			if err != nil {
				return err
			}
			if err := tx.Put("docs", id, held); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f := func(prop, op string, v any) []ochazuke.PropFilter {
		return []ochazuke.PropFilter{{Prop: prop, Op: op, Value: v}}
	}
	filters := [][]ochazuke.PropFilter{nil,
		f("status", "=", "done"), f("status", "!=", "done"), f("status", "in", []any{"todo", "doing"}), f("status", "not-in", []any{"done"}),
		f("size", "=", 3), f("size", "!=", "3"), f("size", "in", []any{1, 2, "3"}), f("score", "=", 1.5), f("score", "=", "2"),
		f("flag", "=", true), f("flag", "!=", true), f("flag", "=", "false"), f("title", "contains", "Doc"), f("title", "not-contains", "doc"),
		f("tags", "contains", "red"), f("tags", "not-contains", "red"), f("kind", "=", nil), f("kind", "!=", nil), f("nosuch", "=", "x"),
		f("due", "=", "2026-01-02"), f("@label", "contains", "1"), f("tags", "=", []any{"red"}), f("size", "contains", "3"), f("status", "like", "x"),
		append(f("status", "=", "done"), f("flag", "=", true)...),
	}
	k := func(field string, desc bool) ochazuke.SortKey { return ochazuke.SortKey{Field: field, Desc: desc} }
	sorts := [][]ochazuke.SortKey{nil, {k("size", false)}, {k("size", true)}, {k("title", false)}, {k("title", true)}, {k("due", false)}, {k("due", true)},
		{k("kind", false)}, {k("kind", true)}, {k("status", false)}, {k("status", true)}, {k("flag", false)}, {k("score", true)}, {k("tags", false)},
		{k("status", false), k("size", true)}, {k("extra", false)}, {k("extra", true)}}
	groups := []struct {
		by   []string
		sort []ochazuke.SortKey
	}{{}, {by: []string{"status"}}, {by: []string{"kind"}}, {by: []string{"status", "flag"}}, {by: []string{"status"}, sort: []ochazuke.SortKey{k("status", true)}},
		{by: []string{"kind"}, sort: []ochazuke.SortKey{k("kind", false)}}, {by: []string{"due"}, sort: []ochazuke.SortKey{k("due", true)}}, {by: []string{"tags"}}}
	texts := []string{"", "doc 1", "RED", "zz", "ünï"}
	pages := [][2]int{{0, 0}, {2, 7}, {99, 7}, {1, 0}}
	hides := []string{"", "archived"}
	fields := []string{"id", "title", "kind", "tags", "due"}
	query := func(fi, si, gi, ti, pi, hi int) ochazuke.DocQuery {
		g := groups[gi]
		return ochazuke.DocQuery{Filters: filters[fi], Sort: sorts[si], GroupBy: g.by, GroupSort: g.sort, Text: texts[ti], TextFields: fields,
			Page: pages[pi][0], PerPage: pages[pi][1], Hide: hides[hi]}
	}
	check := func(q ochazuke.DocQuery) {
		t.Helper()
		want, err := mem.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		got, err := d.Query(q)
		if err != nil {
			t.Fatalf("%+v: %v", q, err)
		}
		if pageOf(got) != pageOf(want) {
			t.Errorf("%+v\n got %s\nwant %s", q, pageOf(got), pageOf(want))
		}
	}
	for i := range filters {
		check(query(i, 0, 0, 0, 0, 0))
	}
	for i := range sorts {
		check(query(0, i, 0, 0, 0, 0))
	}
	for i := range groups {
		check(query(0, 1, i, 0, 0, 0))
	}
	for i := range texts {
		check(query(0, 0, 0, i, 0, 0))
	}
	for i := range pages {
		check(query(0, 1, 1, 0, i, 1))
	}
	rng := rand.New(rand.NewPCG(3, 5))
	for range 400 {
		check(query(rng.IntN(len(filters)), rng.IntN(len(sorts)), rng.IntN(len(groups)), rng.IntN(len(texts)), rng.IntN(len(pages)), rng.IntN(len(hides))))
	}
	if d.Fallbacks() == 0 {
		t.Error("the list equality must have run in Go")
	}
}

// Put merges: a field no prop declares survives a write of the thing, a
// declared prop the thing leaves out is cleared, and Merge writes only
// what it is given.
func TestDocTablePutKeepsUndeclaredFields(t *testing.T) {
	t.Parallel()
	r := ochazuke.NewRuntime()
	r.Config().Disable()
	db, d := docFixture(t, r, doctest.Noun)
	// owner is a renamed prop (PropSpec.Field): stored as owner_id, which
	// Put clears by its field; a stored field named owner is no prop's.
	props := append(slices.Clone(doctest.Props), ochazuke.PropSpec{Name: "owner", Field: "owner_id"})
	r.Define(doctest.Noun).Props(props...).Store(ochazuke.DocStore(d, doctest.Noun))
	if err := db.Tx([]string{"docs"}, func(tx *Tx) error {
		return tx.Put("docs", "a", map[string]any{"id": "a", "title": "first", "status": "todo", "position": 4, "created_by": "me", "owner_id": "ann", "owner": "kept"})
	}); err != nil {
		t.Fatal(err)
	}
	stored := func() map[string]any {
		t.Helper()
		var m map[string]any
		if err := db.Get("docs", "a", &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if _, err := r.Execute(ochazuke.Request{Noun: doctest.Noun, Verb: "set", Args: ochazuke.Arguments{"id": "a", "props": map[string]any{"title": "renamed"}}}); err != nil {
		t.Fatal(err)
	}
	if m := stored(); m["title"] != "renamed" || m["status"] != "todo" || m["position"] != 4.0 || m["created_by"] != "me" || m["owner_id"] != "ann" {
		t.Fatalf("after set: %v", m)
	}
	if err := d.Put("a", ochazuke.Thing{"id": "a", "title": "again"}); err != nil {
		t.Fatal(err)
	}
	if m := stored(); m["title"] != "again" || m["status"] != nil || m["owner_id"] != nil || m["owner"] != "kept" || m["position"] != 4.0 || m["created_by"] != "me" {
		t.Fatalf("a put clears the props it leaves out (by stored field) and keeps the rest: %v", m)
	}
	if err := d.Merge("a", ochazuke.Thing{"created_by": nil, "size": 2}); err != nil {
		t.Fatal(err)
	}
	if m := stored(); m["title"] != "again" || m["size"] != 2.0 || m["position"] != 4.0 || m["created_by"] != nil {
		t.Fatalf("merge: %v", m)
	}
	if err := d.Merge("new", ochazuke.Thing{"id": "new", "title": "made", "gone": nil}); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := d.Get("new"); err != nil || !ok || len(got) != 2 || got["title"] != "made" {
		t.Fatalf("merge on a new key creates it with the given values: %v %v %v", got, ok, err)
	}
}

// Version moves on Put and Delete and on nothing else; a delete of a key
// the table lacks is a DocNotFoundError.
func TestDocTableVersion(t *testing.T) {
	t.Parallel()
	_, d := docFixture(t, nil, doctest.Noun)
	v0, _ := d.Version()
	if err := d.Put("a", ochazuke.Thing{"id": "a"}); err != nil {
		t.Fatal(err)
	}
	v1, _ := d.Version()
	if _, _, err := d.Get("a"); err != nil {
		t.Fatal(err)
	}
	if v, _ := d.Version(); v == v0 || v != v1 {
		t.Fatalf("put %s -> %s, read -> %s", v0, v1, v)
	}
	if err := d.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if v2, _ := d.Version(); v2 == v1 {
		t.Fatal("delete must move the version")
	}
	var nf *DocNotFoundError
	if err := d.Delete("a"); !errors.As(err, &nf) || !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete of a missing key: %v", err)
	}
}

// A query over 20,000 documents decodes one page, counts every match in
// SQL, reads no whole table and answers an indexed condition from the
// index.
func TestDocTableQueryReadsOnePage(t *testing.T) {
	t.Parallel()
	r := ochazuke.NewRuntime()
	r.Config().Disable()
	db, d := docFixture(t, r, doctest.Noun)
	r.Define(doctest.Noun).Props(doctest.Props...).Store(ochazuke.DocStore(d, doctest.Noun))
	if err := db.Index("docs", "status", "status"); err != nil {
		t.Fatal(err)
	}
	statuses := []string{"todo", "doing", "done"}
	var want []string
	if err := db.Tx([]string{"docs"}, func(tx *Tx) error {
		for i := range 20000 {
			doc := map[string]any{"id": fmt.Sprintf("d%d", i), "title": fmt.Sprintf("doc %d", i), "status": statuses[i%3], "size": i, "archived": i%10 == 0}
			if i%3 == 2 && i%10 != 0 {
				want = append(want, fmt.Sprint(doc["id"]))
			}
			if err := tx.Put("docs", fmt.Sprint(doc["id"]), doc); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	slices.Reverse(want)
	var stmts []string
	var countArgs []any
	d.trace = func(s string, args []any) {
		if strings.HasPrefix(s, "SELECT count(*)") {
			countArgs = args
		}
		stmts = append(stmts, s)
	}
	page, err := d.Query(ochazuke.DocQuery{Filters: []ochazuke.PropFilter{{Prop: "status", Value: "done"}}, Hide: "archived",
		Sort: []ochazuke.SortKey{{Field: "size", Desc: true}}, Page: 3, PerPage: 25})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != len(want) || len(page.Items) != 25 || fmt.Sprint(page.Items[0]["id"]) != want[50] || fmt.Sprint(page.Items[24]["id"]) != want[74] {
		t.Fatalf("page 3: total %d (want %d), %d items, first %v", page.Total, len(want), len(page.Items), page.Items[0]["id"])
	}
	if d.Reads() != 25 || d.Fallbacks() != 0 || db.FullReads("docs") != 0 {
		t.Fatalf("reads %d fallbacks %d full reads %d, want 25 0 0", d.Reads(), d.Fallbacks(), db.FullReads("docs"))
	}
	if !strings.Contains(stmts[len(stmts)-1], "LIMIT ? OFFSET ?") {
		t.Fatalf("the page is cut in SQL: %s", stmts[len(stmts)-1])
	}
	rows, err := db.db.Query("EXPLAIN QUERY PLAN "+stmts[0], countArgs...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if !strings.Contains(strings.Join(plan, "\n"), "idx_docs_status") {
		t.Fatalf("the status condition does not read its index: %v", plan)
	}
}

// A field name that could reach SQL as anything but a name is refused,
// typed, before any statement runs.
func TestDocTableRefusesUnsafeFieldNames(t *testing.T) {
	t.Parallel()
	r := ochazuke.NewRuntime()
	r.Config().Disable()
	_, d := docFixture(t, r, doctest.Noun)
	r.Define(doctest.Noun).Props(doctest.Props...).Store(ochazuke.DocStore(d, doctest.Noun))
	ran := 0
	d.trace = func(string, []any) { ran++ }
	bad := "x') OR 1=1 --"
	for _, q := range []ochazuke.DocQuery{
		{Sort: []ochazuke.SortKey{{Field: bad}}},
		{GroupBy: []string{bad}},
		{GroupBy: []string{"status"}, GroupSort: []ochazuke.SortKey{{Field: bad}}},
		{Filters: []ochazuke.PropFilter{{Prop: bad, Value: "x"}}},
		{Text: "a", TextFields: []string{bad}},
		{Hide: bad},
	} {
		var fe *FieldNameError
		if _, err := d.Query(q); !errors.As(err, &fe) || fe.Field != bad {
			t.Errorf("%+v: %v", q, err)
		}
	}
	if ran != 0 {
		t.Fatalf("%d statements ran", ran)
	}
	// A dashed name is a name: it is read quoted.
	if err := d.Put("a", ochazuke.Thing{"id": "a", "archived-by-you": true}); err != nil {
		t.Fatal(err)
	}
	page, err := d.Query(ochazuke.DocQuery{Hide: "archived-by-you"})
	if err != nil || page.Total != 0 {
		t.Fatalf("dashed hide: %v %v", page, err)
	}
}
