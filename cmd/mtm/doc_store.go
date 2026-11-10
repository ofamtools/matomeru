package main

import (
	"errors"
	"maps"
	"reflect"

	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

// domainDocs adapts domain records to DocTable without flattening their storage:
// reminder rules keep their dates, relations keep their identity and exclusive
// flags, and daemon copies keep their Through writes and tombstones. CRUD and
// query semantics come from DocStore and the catalog, not noun-name branches.
// The runtime is single-owner; with restores the request across nested calls.
type domainDocs struct {
	a      *App
	noun   string
	source ochazuke.Store
	ctx    ochazuke.Ctx
}

func (d *domainDocs) Get(key string) (ochazuke.Thing, bool, error) {
	t, err := d.source.Get(d.ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return nil, false, nil
	}
	return t, t != nil && err == nil, err
}

func (d *domainDocs) Put(key string, t ochazuke.Thing) error {
	old, ok, err := d.Get(key)
	if err != nil {
		return err
	}
	var out ochazuke.Thing
	if !ok {
		out, err = d.source.Create(d.ctx, t)
	} else {
		changes := ochazuke.Thing{}
		for _, p := range d.a.r.Noun(d.noun).Props {
			if !p.Key && !p.ReadOnly && !p.CreateOnly && !reflect.DeepEqual(old[p.Name], t[p.Name]) {
				changes[p.Name] = t[p.Name]
			}
		}
		out, err = d.source.Patch(d.ctx, key, changes)
	}
	if err == nil {
		clear(t)
		maps.Copy(t, out)
	}
	return err
}

func (d *domainDocs) Delete(key string) error { return d.source.Delete(d.ctx, key) }

func (d *domainDocs) Version() (string, error) {
	if d.source.ChangeToken == nil {
		return "", nil
	}
	return d.source.ChangeToken(d.ctx)
}

func (d *domainDocs) Query(q ochazuke.DocQuery) (ochazuke.ListPage, error) {
	query := ochazuke.ListQuery{Args: d.ctx.Args, Archived: true}
	things, err := d.source.List(d.ctx, query)
	if err != nil {
		return ochazuke.ListPage{}, err
	}
	out := things[:0]
	for _, t := range things {
		if archived, _ := t[q.Hide].(bool); q.Hide != "" && archived {
			continue
		}
		if ochazuke.TextMatches(t, q.TextFields, q.Text) {
			out = append(out, t)
		}
	}
	return d.a.r.QueryAll(d.noun, out, ochazuke.ListQuery{Filters: q.Filters, Sort: q.Sort, Archived: true, Page: q.Page, PerPage: q.PerPage}), nil
}

func (d *domainDocs) with(c ochazuke.Ctx) func() {
	previous := d.ctx
	d.ctx = c
	return func() { d.ctx = previous }
}

// docStore preserves domain identity on create (relations are keyed by their
// ends, reminders use stable IDs). All other CRUD runs through DocStore. Keep
// the source's metadata, including its copy contract and list arguments.
func (a *App) docStore(noun string, source ochazuke.Store) ochazuke.Store {
	d := &domainDocs{a: a, noun: noun, source: source}
	doc := ochazuke.DocStore(d, noun)
	out := source
	out.Get = func(c ochazuke.Ctx, key string) (ochazuke.Thing, error) { defer d.with(c)(); return doc.Get(c, key) }
	out.List = func(c ochazuke.Ctx, q ochazuke.ListQuery) ([]ochazuke.Thing, error) {
		c.Args = q.Args
		defer d.with(c)()
		return doc.List(c, q)
	}
	out.Query = func(c ochazuke.Ctx, q ochazuke.ListQuery) (ochazuke.ListPage, error) {
		c.Args = q.Args
		defer d.with(c)()
		return doc.Query(c, q)
	}
	out.Patch = func(c ochazuke.Ctx, key string, t ochazuke.Thing) (ochazuke.Thing, error) {
		defer d.with(c)()
		return doc.Patch(c, key, t)
	}
	out.Delete = func(c ochazuke.Ctx, key string) error { defer d.with(c)(); return doc.Delete(c, key) }
	return out
}
