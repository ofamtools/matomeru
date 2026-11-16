package main

import (
	"slices"
	"strings"

	"ofam.dev/ochazuke"
)

// docRead is a prop an application computes on every read of a store's
// things, the way an ochazuke derivation is part of the thing (get's
// record, list's rows, filters, sorts), for a value none of ochazuke's
// derivations computes exactly as the noun showed it before it was a
// declaration: a note's summary is its first line cut to 70 characters,
// "untitled" when blank, where the first-line derivation takes the first
// line that is not blank, whole. Each read is a small named function; the
// ochazuke addition that removes them is a derivation an application
// registers (a kind of its own, or PropSpec.Derive as part of the thing,
// unlike PropSpec.Read, which only get {prop} reads).
type docRead struct {
	prop string
	read func(ochazuke.Thing) any
}

// withDocReads is s with each read's prop computed on every thing it
// answers. A write never stores a read prop: one a create or set is given
// (an undo hands back every prop it recorded) goes to the store as it is,
// so the store's hooks decide what is kept. A list request that filters,
// sorts or groups on a read prop reads the store's matching things
// unpaged (archived ones too) and runs the whole request but its text
// search here (as ochazuke runs a derived prop's);
// any other request is the store's own page. token, when set, is the
// store's change token as the application keys it (its scope).
func withDocReads(r *ochazuke.Runtime, noun string, s ochazuke.Store, reads []docRead, token func(string) string) ochazuke.Store {
	w := docReads{r: r, noun: noun, raw: s, reads: reads}
	s.Get = w.get
	if s.List != nil {
		s.List = w.list
	}
	if s.Query != nil {
		s.Query = w.query
	}
	if s.Create != nil {
		create := s.Create
		s.Create = func(c ochazuke.Ctx, values ochazuke.Thing) (ochazuke.Thing, error) {
			t, err := create(c, values)
			return w.fill(t), err
		}
	}
	if s.Patch != nil {
		patch := s.Patch
		s.Patch = func(c ochazuke.Ctx, key string, changes ochazuke.Thing) (ochazuke.Thing, error) {
			t, err := patch(c, key, changes)
			return w.fill(t), err
		}
	}
	if s.ChangeToken != nil && token != nil {
		own := s.ChangeToken
		s.ChangeToken = func(c ochazuke.Ctx) (string, error) {
			t, err := own(c)
			return token(t), err
		}
	}
	return s
}

// zeroRead reads a prop a document leaves out when it is empty as zero,
// where a store's own decoding gave every field its zero value.
func zeroRead(prop string, zero any) docRead {
	return docRead{prop: prop, read: func(t ochazuke.Thing) any {
		if v, ok := t[prop]; ok && v != nil {
			return v
		}
		return zero
	}}
}

type docReads struct {
	r     *ochazuke.Runtime
	noun  string
	raw   ochazuke.Store
	reads []docRead
}

// fill is t with every read prop computed (t itself, written in place).
func (w docReads) fill(t ochazuke.Thing) ochazuke.Thing {
	if t == nil {
		return nil
	}
	for _, rd := range w.reads {
		t[rd.prop] = rd.read(t)
	}
	return t
}

func (w docReads) fillAll(things []ochazuke.Thing) []ochazuke.Thing {
	for _, t := range things {
		w.fill(t)
	}
	return things
}

func (w docReads) get(c ochazuke.Ctx, key string) (ochazuke.Thing, error) {
	t, err := w.raw.Get(c, key)
	return w.fill(t), err
}

// isRead reports whether a name (a prop's, an alias, or @role) is a read
// prop of the noun.
func (w docReads) isRead(name string) bool {
	n := w.r.Noun(w.noun)
	if n == nil {
		return false
	}
	role, isRole := strings.CutPrefix(name, "@")
	for _, p := range n.Props {
		if isRole && p.Role == role || !isRole && (p.Name == name || slices.Contains(p.Aliases, name)) {
			return slices.ContainsFunc(w.reads, func(rd docRead) bool { return rd.prop == p.Name })
		}
	}
	return false
}

// split separates the conditions on read props from the rest.
func (w docReads) split(filters []ochazuke.PropFilter) (rest, read []ochazuke.PropFilter) {
	for _, f := range filters {
		if w.isRead(f.Prop) {
			read = append(read, f)
		} else {
			rest = append(rest, f)
		}
	}
	return rest, read
}

func (w docReads) list(c ochazuke.Ctx, q ochazuke.ListQuery) ([]ochazuke.Thing, error) {
	rest, read := w.split(q.Filters)
	inner := q
	inner.Filters = rest
	things, err := w.raw.List(c, inner)
	if err != nil {
		return nil, err
	}
	things = w.fillAll(things)
	if len(read) == 0 {
		return things, nil
	}
	return w.r.QueryAll(w.noun, things, ochazuke.ListQuery{Filters: read, Archived: true}).Items, nil
}

func (w docReads) onRead(q ochazuke.ListQuery) bool {
	keys := slices.Concat(q.Sort, q.GroupSort)
	return slices.ContainsFunc(q.Filters, func(f ochazuke.PropFilter) bool { return w.isRead(f.Prop) }) ||
		slices.ContainsFunc(keys, func(k ochazuke.SortKey) bool { return w.isRead(k.Field) }) ||
		slices.ContainsFunc(q.GroupBy, w.isRead)
}

func (w docReads) query(c ochazuke.Ctx, q ochazuke.ListQuery) (ochazuke.ListPage, error) {
	if !w.onRead(q) {
		page, err := w.raw.Query(c, q)
		page.Items = w.fillAll(page.Items)
		return page, err
	}
	// The archive hiding runs here too: a condition on a read prop (the
	// archive prop itself) may be what shows the archived things.
	rest, _ := w.split(q.Filters)
	inner := q
	inner.Filters, inner.Sort, inner.GroupBy, inner.GroupSort, inner.Page, inner.PerPage, inner.Archived = rest, nil, nil, nil, 0, 0, true
	page, err := w.raw.Query(c, inner)
	if err != nil {
		return ochazuke.ListPage{}, err
	}
	all := q
	all.Text = "" // the store ran the text; the other conditions hold again here
	return w.r.QueryAll(w.noun, w.fillAll(page.Items), all), nil
}
