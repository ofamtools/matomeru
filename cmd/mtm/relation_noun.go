package main

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"ofam.dev/matomeru/internal/relations"
	"ofam.dev/matomeru/store"
	"ofam.dev/ochazuke"
)

// The relation noun: one row of the relations table (internal/relations),
// anything pointing to anything. Its key is the short key of the relation's
// identity (kind and ends). The standard verbs are ochazuke's over its
// store (get, list, create, set, delete, archive, watch, with undo); the
// store makes a relation through Relate and removes one through Unrelate, so
// the kind's rules and delete.archive hold for the noun as for the derived
// NOUN.relate and NOUN.unrelate of every other noun. follow, yank and move
// are the relation's own.

const relationNoun = "relation"

// relationProps are the relation's props. kind, from and to are the
// identity: they are written by create and never changed (relate again).
func relationProps() []ochazuke.PropSpec {
	text := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "string", Description: desc} }
	end := func(desc string) ochazuke.Parameter {
		return ochazuke.Parameter{Type: "string", Noun: ochazuke.AnyNoun, Free: true, Description: desc}
	}
	return []ochazuke.PropSpec{
		{Name: "id", Key: true, Parameter: text("The relation's key (the short hash of its kind and ends)")},
		{Name: "summary", Role: ochazuke.RoleLabel, ReadOnly: true, Parameter: text("The relation as a line: kind, from, to")},
		{Name: "kind", CreateOnly: true, Parameter: ochazuke.Parameter{Type: "string", Free: true, Source: "ochazuke.relation-kind.list",
			Description: "The kind of relation, any word (default " + ochazuke.DefaultRelationKind + "); a declared kind adds rules"}},
		{Name: "from", CreateOnly: true, Parameter: end("The thing that holds the relation, noun:key")},
		{Name: "to", CreateOnly: true, Parameter: end("The thing that is held, noun:key (a directory's key is its path)")},
		{Name: "text", Multiline: true, Parameter: text("Free text kept with the relation")},
		{Name: "data", Parameter: ochazuke.Parameter{Type: ochazuke.PropObject, Description: "A JSON object kept with the relation; set replaces it (relate again merges, a key set to null is removed)"}},
		{Name: "position", Parameter: ochazuke.Parameter{Type: "integer", Description: "The order among the relations of one from thing and kind (steps of 1024); move changes it"}},
		{Name: "archived", Role: ochazuke.RoleArchive, Parameter: ochazuke.Parameter{Type: "boolean", Description: "Hidden but kept; unrelating an archived relation removes it"}},
		{Name: "former-from", ReadOnly: true, Parameter: text("The from thing a deleted thing used to be (the relation stays)")},
		{Name: "former-to", ReadOnly: true, Parameter: text("The to thing a deleted thing used to be (the relation stays)")},
		{Name: "created", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "integer", Description: "When the relation was made (Unix ms)"}},
		{Name: "updated", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "integer", Description: "When the relation last changed (Unix ms)"}},
	}
}

func relationThing(r relations.Row) ochazuke.Thing {
	end := r.From
	if end == "" {
		end = "(" + r.FormerFrom + ")"
	}
	to := r.To
	if to == "" {
		to = "(" + r.FormerTo + ")"
	}
	t := ochazuke.Thing{"id": r.Key, "summary": r.Kind + ": " + end + " -> " + to, "kind": r.Kind, "from": r.From, "to": r.To,
		"text": r.Text, "position": r.Position, "archived": r.Archived, "former-from": r.FormerFrom, "former-to": r.FormerTo,
		"created": r.Created, "updated": r.Updated}
	if r.Data != nil {
		t["data"] = r.Data
	}
	return t
}

// relationRow is the stored relation with that key, "no relation" when
// there is none.
func (a *App) relationRow(key string) (relations.Row, error) {
	r, err := a.relStore.ByKey(key)
	if errors.Is(err, store.ErrNotFound) {
		return r, fmt.Errorf("no relation %q", key)
	}
	return r, err
}

// relationStore is the relation noun's store over the relations table.
func (a *App) relationStore() ochazuke.Store {
	return ochazuke.Store{
		Kind: ochazuke.KindCollection,
		Undo: true, AcceptsKey: true,
		ListParams: map[string]ochazuke.Parameter{
			"involving": {Type: "string", Description: "Only relations with this thing at either end, noun:key (filter kind, from and to as props)"},
			"detached":  {Type: "boolean", Description: "Only relations that lost an end (the thing was deleted)"},
		},
		Get: func(_ ochazuke.Ctx, id string) (ochazuke.Thing, error) {
			r, err := a.relationRow(id)
			if err != nil {
				return nil, err
			}
			return relationThing(r), nil
		},
		List: func(c ochazuke.Ctx, q ochazuke.ListQuery) ([]ochazuke.Thing, error) {
			rows, err := a.relStore.Rows(ochazuke.RelationQuery{
				Thing: ochazuke.StringArg(q.Args, "involving", ""), Archived: q.Archived, Detached: boolArg(q.Args, "detached", false),
			})
			if err != nil {
				return nil, err
			}
			out := make([]ochazuke.Thing, len(rows))
			for i, r := range rows {
				out[i] = relationThing(r)
			}
			return out, nil
		},
		Create: a.createRelation,
		Patch:  a.patchRelation,
		Delete: a.deleteRelation,
	}
}

// createRelation is the store's Create: relate from to to in kind, with
// text and data; the kind's rules hold. Relating the same ends again
// updates the relation.
func (a *App) createRelation(c ochazuke.Ctx, values ochazuke.Thing) (ochazuke.Thing, error) {
	x := ochazuke.Relation{}
	x.Kind, _ = thingString(values, "kind")
	x.From, _ = thingString(values, "from")
	x.To, _ = thingString(values, "to")
	x.Text, _ = thingString(values, "text")
	if raw, ok := values["data"]; ok && raw != nil {
		data, ok := raw.(map[string]any)
		if !ok {
			return nil, &ochazuke.ArgumentError{Err: errors.New("data is a JSON object")}
		}
		x.Data = data
	}
	if n, ok := values["position"]; ok {
		x.Position = int(thingInt(n))
	}
	done, err := a.r.Relate(c, x)
	if err != nil {
		return nil, err
	}
	r, err := a.relationRow(relations.KeyOf(done))
	if err != nil {
		return nil, err
	}
	return relationThing(r), nil
}

// patchRelation is the store's Patch: text, data (replaced), position and
// archived. Archiving the holder of an exclusive flag (a default) gives the
// flag to the next live relation of the same from thing and kind, as
// unrelating it does; the kind says which flags (RelationKind.Exclusive).
func (a *App) patchRelation(c ochazuke.Ctx, id string, changes ochazuke.Thing) (ochazuke.Thing, error) {
	r, err := a.relationRow(id)
	if err != nil {
		return nil, err
	}
	x := r.Relation()
	if v, ok := changes["text"]; ok {
		x.Text, _ = v.(string)
	}
	if v, ok := changes["data"]; ok {
		switch d := v.(type) {
		case nil:
			x.Data = nil
		case map[string]any:
			// A kind's exclusive flags (a default) are moved by setting
			// another, never dropped by replacing the data: the old flag is
			// kept unless the new data names the key.
			kind, _ := a.r.RelationKindOf(x.Kind)
			held := x.Data
			x.Data = maps.Clone(d)
			for _, key := range kind.Exclusive {
				if _, named := d[key]; !named && held[key] == true {
					if x.Data == nil {
						x.Data = map[string]any{}
					}
					x.Data[key] = true
				}
			}
		default:
			return nil, &ochazuke.ArgumentError{Err: errors.New("data is a JSON object")}
		}
	}
	if v, ok := changes["position"]; ok {
		x.Position = int(thingInt(v))
	}
	var promote []string
	if v, ok := changes["archived"].(bool); ok && v != x.Archived {
		x.Archived = v
		if v {
			kind, _ := a.r.RelationKindOf(x.Kind)
			for _, key := range kind.Exclusive {
				if x.Flag(key) {
					x.Data = maps.Clone(x.Data)
					x.Data[key] = false
					promote = append(promote, key)
				}
			}
		}
	}
	if err := a.relStoreHub().Put(x); err != nil {
		return nil, err
	}
	for _, key := range promote {
		rest, err := a.r.RelationsOf(ochazuke.RelationQuery{Kind: x.Kind, From: x.From})
		if err != nil {
			return nil, err
		}
		if len(rest) == 0 || slices.ContainsFunc(rest, func(o ochazuke.Relation) bool { return o.Flag(key) }) {
			continue
		}
		next := rest[0]
		next.Data = maps.Clone(next.Data)
		if next.Data == nil {
			next.Data = map[string]any{}
		}
		next.Data[key] = true
		if err := a.relStoreHub().Put(next); err != nil {
			return nil, err
		}
		c.Notify(fmt.Sprintf("%s is now the %s %s of %s", next.To, key, x.Kind, x.From))
	}
	return a.getRelation(c, relations.KeyOf(x))
}

func (a *App) getRelation(_ ochazuke.Ctx, key string) (ochazuke.Thing, error) {
	r, err := a.relationRow(key)
	if err != nil {
		return nil, err
	}
	return relationThing(r), nil
}

// deleteRelation is the store's Delete (and relation.unrelate under it):
// Unrelate, which archives a live relation first under delete.archive and
// removes an archived one. A relation that lost an end has no ends to name,
// so it is removed.
func (a *App) deleteRelation(c ochazuke.Ctx, id string) error {
	r, err := a.relationRow(id)
	if err != nil {
		return err
	}
	if r.From == "" || r.To == "" {
		return a.relStoreHub().Remove(r.Relation())
	}
	_, err = a.r.Unrelate(c, r.Kind, r.From, r.To)
	return err
}

// movePlace is the 0-based place a move to word takes among n relations,
// the relation now at at: a name from the table, or a 1-based number.
func movePlace(word string, at, n int) (int, error) {
	if place, ok := map[string]int{"first": 0, "last": n - 1, "up": max(0, at-1), "down": min(n-1, at+1)}[word]; ok {
		return place, nil
	}
	var k int
	if _, err := fmt.Sscanf(word, "%d", &k); err != nil || k < 1 {
		return 0, fmt.Errorf("to is first, last, up, down or a place from 1, not %q", word)
	}
	return min(k, n) - 1, nil
}

// relStoreHub is the relation store the runtime uses.
func (a *App) relStoreHub() ochazuke.RelationStore { return a.r.RelationStore() }

// defineRelation defines the relation noun with its store and its own
// verbs. Keys are binding rows; none are bound by default.
func (a *App) defineRelation() {
	a.r.Define(relationNoun).Props(relationProps()...).Store(a.relationStore()).
		Verb("follow", a.followRelation, "Open the other end of the relation (end: from or to; default to, else from)").Positional("id", "end").
		Verb("yank", a.yankRelation, "Copy the relation's key, or an end as noun:key, to the clipboard (what: key, from, to)").Positional("id", "what").
		Verb("move", a.moveRelation, "Move the relation among those of the same from thing and kind: to first, last, up, down or a 1-based place").Positional("id", "to")
	id := relationIDParam()
	a.parameters("relation.follow", map[string]ochazuke.Parameter{"id": id, "end": {Type: "string", Enum: []string{"from", "to"}, Description: "Which end to open (default to, else from)"}})
	a.parameters("relation.yank", map[string]ochazuke.Parameter{"id": id, "what": {Type: "string", Enum: []string{"key", "from", "to"}, Description: "What to copy (default key)"}})
	a.parameters("relation.move", map[string]ochazuke.Parameter{"id": id, "to": {Type: "string", Required: true, Description: "first, last, up, down, or the 1-based place among the live relations of the same from thing and kind"}})
}

// relationIDParam is the relation a verb acts on, when no row is selected.
func relationIDParam() ochazuke.Parameter {
	return ochazuke.Parameter{Type: "string", Noun: relationNoun, Required: true, Description: "The relation's key (relation list answers them)"}
}

// relationKeyOf is the relation a verb acts on: the selected row's, else id.
func relationKeyOf(ctx ochazuke.Ctx) string {
	if id := ochazuke.StringArg(ctx.Args, "id", ""); id != "" || ctx.Instance == nil {
		return id
	}
	return ctx.Instance.ID // a selected relation row
}

// jumps are the pages a noun:key opens (data: noun to the function that
// shows the thing); a noun not listed answers its own get.
func (a *App) jumps() map[string]func(string) error {
	return map[string]func(string) error{
		"project":   a.jumpProject,
		"pane":      a.jumpPane,
		"directory": a.jumpDirectory,
	}
}

// followRelation is relation.follow: it opens the end. Where the app has no
// page for the end's noun it answers the thing (its get).
func (a *App) followRelation(ctx ochazuke.Ctx) ochazuke.Result {
	r, err := a.relationRow(relationKeyOf(ctx))
	if err != nil {
		return ochazuke.Failed(err)
	}
	ends := map[string]string{"from": r.From, "to": r.To}
	ref, named := ends[ochazuke.StringArg(ctx.Args, "end", "")]
	if !named {
		ref = cmp.Or(r.To, r.From) // the other end by default: to, else from
	}
	if ref == "" {
		return ochazuke.Failed(errors.New("that end is gone: the thing was deleted (see former-from, former-to)"))
	}
	noun, key, ok := ochazuke.SplitAnyRef(ref)
	if !ok {
		return ochazuke.Failed(fmt.Errorf("%q is not noun:key", ref))
	}
	if jump, has := a.jumps()[a.r.CanonicalNoun(noun)]; has {
		return resultError(jump(key))
	}
	return ochazuke.Done(ochazuke.Thing{"noun": noun, "key": key, "ref": ref})
}

// yankRelation is relation.yank.
func (a *App) yankRelation(ctx ochazuke.Ctx) ochazuke.Result {
	r, err := a.relationRow(relationKeyOf(ctx))
	if err != nil {
		return ochazuke.Failed(err)
	}
	what := ochazuke.StringArg(ctx.Args, "what", "key")
	text, ok := map[string]string{"key": ochazuke.AnyRef(relationNoun, r.Key), "from": r.From, "to": r.To}[what]
	if !ok {
		return ochazuke.Failed(fmt.Errorf("yank key, from or to, not %q", what))
	}
	if text == "" {
		return ochazuke.Failed(errors.New("that end is gone"))
	}
	if err := a.copyOSC52(text); err != nil {
		return ochazuke.Failed(err)
	}
	ctx.Notify("Yanked " + text)
	return ochazuke.Done(text)
}

// moveRelation is relation.move: the relation takes a new place among the
// live relations of its from thing and kind, in one write per relation that
// moves.
func (a *App) moveRelation(ctx ochazuke.Ctx) ochazuke.Result {
	r, err := a.relationRow(relationKeyOf(ctx))
	if err != nil {
		return ochazuke.Failed(err)
	}
	group, err := a.r.RelationsOf(ochazuke.RelationQuery{Kind: r.Kind, From: r.From})
	if err != nil {
		return ochazuke.Failed(err)
	}
	at := slices.IndexFunc(group, func(o ochazuke.Relation) bool { return o.To == r.To })
	if at < 0 {
		return ochazuke.Failed(errors.New("only a live relation with a from thing is moved"))
	}
	to := strings.TrimSpace(ochazuke.StringArg(ctx.Args, "to", ""))
	place, err := movePlace(to, at, len(group))
	if err != nil {
		return ochazuke.Failed(err)
	}
	moved := group[at]
	rest := slices.Delete(slices.Clone(group), at, at+1)
	order := slices.Insert(rest, place, moved)
	for i, o := range order {
		want := (i + 1) * 1024
		if o.Position == want {
			continue
		}
		key := relations.KeyOf(o)
		if _, err := a.r.Execute(ochazuke.Request{Noun: relationNoun, Verb: "set", Args: ochazuke.Arguments{"id": key, "props": map[string]any{"position": want}}}); err != nil {
			return ochazuke.Failed(err)
		}
	}
	return resultError(nil)
}
