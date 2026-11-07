package main

import (
	"encoding/json"
	"fmt"
	"maps"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/matomeru/internal/work"
)

// Copies: mtm's own copies of the things the daemon holds (DESIGN-001
// "Copy stores: malleable things"; owner decisions Q1 to Q3). The pane and
// session nouns' stores are ochazuke copy stores (CopyStore) over their
// daemon stores (pane_store.go, session_store.go), kept in the work
// store's copies table (copyLocal):
//
//   - the first read of a thing copies it in once (the backfill); after
//     that mtm's copy is the thing: a pane renamed in mtm keeps its name
//     when the daemon restarts and lists the old one;
//   - set writes the copy, and the props the noun declares Through also go
//     to the source (pane_store.go lists which, and why);
//   - refresh {id?} diffs the copies against the source and opens the
//     diff, a pick per prop; differences never block anything;
//   - a thing the source stops listing stays, as ended (endedProp); the
//     Panes page shows ended panes under panes.show-ended.
//
// A copy holds every prop, but only the ones the noun names as held
// (copiedNoun.held: the name) are the copy's own. The others are the
// source's facts (the daemon's state, title, cwd…) or mtm's own records
// (a star, an owner), and mtm writes them into the copy whenever it reads
// the source (syncCopies), so a read and a refresh see them as they are
// now and an ended thing keeps what was last seen of it.

// endedProp is the read-only prop a copy noun's things carry: true when the
// source no longer lists the thing and mtm keeps its copy.
const endedProp = "ended"

// settingShowEnded shows the panes that ended (kept copies) on the Panes
// page.
const settingShowEnded = "panes.show-ended"

func copySettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingShowEnded, Type: ochazuke.SettingBoolean, Default: false,
			Description: "Show the panes that ended on the Panes page (mtm keeps a copy of every pane it saw; pane list shows them with ended=true); false shows only the daemon's panes"},
	}
}

// endedPropSpec is endedProp's spec.
func endedPropSpec(noun string) ochazuke.PropSpec {
	return ochazuke.PropSpec{Name: endedProp, ReadOnly: true, Parameter: ochazuke.Parameter{Type: "boolean",
		Description: "The daemon no longer lists the " + noun + ": mtm keeps its copy (what was last seen of it)"}}
}

// copyLocal is ochazuke's LocalStore for one noun over the work store's
// copies table. canon makes a key the one the source lists the thing by
// (a row id or a name to its id), and a thing is kept under its own key
// prop's value when it has one, so every form of a key reaches one copy.
type copyLocal struct {
	w       *work.Store
	noun    string
	keyProp string
	canon   func(string) string
	source  func() string // where a new copy comes from (App.server)
	r       *ochazuke.Runtime
}

func (l copyLocal) key(key string, t ochazuke.Thing) string {
	if t != nil && !tombstone(t) {
		if v, ok := t[l.keyProp]; ok && v != nil && fmt.Sprint(v) != "" {
			key = fmt.Sprint(v)
		}
	}
	return l.canon(key)
}

func (l copyLocal) Get(key string) (ochazuke.Thing, bool, error) {
	c, ok, err := l.w.Copy(l.noun, l.canon(key))
	if err != nil || !ok {
		return nil, false, err
	}
	return l.thingOf(c), true, nil
}

func (l copyLocal) Put(key string, t ochazuke.Thing) error {
	k := l.key(key, t)
	props := maps.Clone(t)
	if props == nil {
		props = ochazuke.Thing{}
	}
	props[l.keyProp] = k
	return l.w.PutCopy(l.noun, k, l.source(), props)
}

func (l copyLocal) List() ([]ochazuke.Thing, error) {
	copies, err := l.w.Copies(l.noun)
	if err != nil {
		return nil, err
	}
	out := make([]ochazuke.Thing, len(copies))
	for i, c := range copies {
		out[i] = l.thingOf(c)
	}
	return out, nil
}

func (l copyLocal) Delete(key string) error { return l.w.DeleteCopy(l.noun, l.canon(key)) }

// thingOf is a stored copy as a thing, an integer prop's value an int
// again (JSON reads every number as a float).
func (l copyLocal) thingOf(c work.Copy) ochazuke.Thing {
	t := ochazuke.Thing(c.Props)
	if t == nil {
		return ochazuke.Thing{}
	}
	if n := l.r.Noun(l.noun); n != nil {
		for _, p := range n.Props {
			if f, ok := t[p.Name].(float64); ok && p.Type == ochazuke.SettingInteger && f == float64(int(f)) {
				t[p.Name] = int(f)
			}
		}
	}
	return t
}

// tombstone reports whether a copy is a deleted one's tombstone.
func tombstone(t ochazuke.Thing) bool { b, _ := t[ochazuke.CopyTombstone].(bool); return b }

// copiedNoun is one noun whose store is a copy store: its local store, the
// props its copies hold as their own, and its source (the noun's store
// before the copy, ochazuke.Store.CopySource).
type copiedNoun struct {
	noun  string
	held  map[string]bool
	local copyLocal
	src   ochazuke.Store
	// check, when set, refuses a write no copy can hold (a session with
	// no name): the one refusal a copy keeps, before anything is written.
	check func(changes ochazuke.Thing) error
}

// copyListing is the daemon's listing a read of a copy noun made, in the
// read's args (copyListingArg), so the source's reads inside it use it and
// never list again: known is false for a cached read before any listing,
// which says nothing of what the source has.
type copyListing struct {
	panes []pane
	known bool
	err   error
}

// copyListingArg carries a copyListing in a store read's args.
const copyListingArg = "copy-listing"

// withListing is c with the daemon's listing in its args (listing once).
func (a *App) withListing(c ochazuke.Ctx) (ochazuke.Ctx, copyListing) {
	if l, ok := c.Args[copyListingArg].(copyListing); ok {
		return c, l
	}
	cached, _ := c.Args[cachedOnlyArg].(bool)
	panes, err := a.livePanes(c)
	l := copyListing{panes: panes, err: err, known: err == nil && (!cached || a.pv != nil && a.pv.listed)}
	args := maps.Clone(c.Args)
	if args == nil {
		args = ochazuke.Arguments{}
	}
	args[copyListingArg] = l
	c.Args = args
	return c, l
}

// copyStore is the store of noun's copies of src's things, held its own
// props (the rest are synced from the source on every read).
func (a *App) copyStore(noun string, src ochazuke.Store, canon func(string) string, held ...string) ochazuke.Store {
	local := copyLocal{w: a.work, noun: noun, keyProp: "id", canon: canon, source: a.server, r: a.r}
	if a.copies == nil {
		a.copies = map[string]*copiedNoun{}
	}
	cn := &copiedNoun{noun: noun, held: map[string]bool{}, local: local, src: src}
	for _, name := range held {
		cn.held[name] = true
	}
	a.copies[noun] = cn
	s := ochazuke.CopyStore(src, local)
	s.ListParams = src.ListParams
	return s
}

// readCopiesNow makes noun's reads sync its copies from the source first
// (syncCopies): get, list and set answer the thing as it is now, a list
// keeps only this source's copies and, for an ended thing, the scope's
// (list --all every project's), and set on a thing with no copy that the
// source does not list writes the source alone (a pane mtm has not listed
// yet still takes a star or an owner: record first, never refuse). Called
// once the noun's store is declared (it wraps the bound store).
func (a *App) readCopiesNow(noun string, check func(ochazuke.Thing) error) {
	cn, n := a.copies[noun], a.r.Noun(noun)
	if cn == nil || n == nil || n.Store == nil {
		panic("copies: " + noun + " has no copy store")
	}
	cn.check = check
	s := n.Store
	get, list, patch := s.Get, s.List, s.Patch
	s.Get = func(c ochazuke.Ctx, key string) (ochazuke.Thing, error) {
		c = a.syncCopies(c, cn, key)
		return get(c, key)
	}
	s.List = func(c ochazuke.Ctx, q ochazuke.ListQuery) ([]ochazuke.Thing, error) {
		c = a.syncCopies(c, cn, "")
		things, err := list(c, q)
		if err != nil {
			return nil, err
		}
		mine, err := a.copiesOf(cn)
		if err != nil {
			return nil, err
		}
		all, _ := q.Args["all"].(bool)
		out := things[:0]
		for _, t := range things {
			if !mine[fmt.Sprint(t[cn.local.keyProp])] {
				continue
			}
			if ended, _ := t[endedProp].(bool); ended && !all {
				if project, _ := t["project"].(string); !a.inScope(project) {
					continue
				}
			}
			out = append(out, t)
		}
		return out, nil
	}
	s.Patch = func(c ochazuke.Ctx, key string, changes ochazuke.Thing) (ochazuke.Thing, error) {
		if cn.check != nil {
			if err := cn.check(changes); err != nil {
				return nil, err
			}
		}
		c = a.syncCopies(c, cn, key)
		if _, had, err := cn.local.Get(key); err != nil {
			return nil, err
		} else if !had {
			if _, err := cn.src.Get(c, key); err != nil && cn.src.Patch != nil {
				return cn.src.Patch(c, key, changes)
			}
		}
		return patch(c, key, changes)
	}
	if op, ok := a.r.Operations[a.r.CanonicalKey(noun+".refresh")]; ok && op.App != nil {
		refresh := op.App
		op.App = func(c ochazuke.Ctx) ochazuke.Result {
			a.syncCopies(c, cn, "")
			return refresh(c)
		}
		a.r.Operations[a.r.CanonicalKey(noun+".refresh")] = op
	}
}

// copiesOf are the keys of cn's copies that came from this mtm's source.
func (a *App) copiesOf(cn *copiedNoun) (map[string]bool, error) {
	copies, err := a.work.Copies(cn.noun)
	if err != nil {
		return nil, err
	}
	src := a.server()
	out := make(map[string]bool, len(copies))
	for _, c := range copies {
		if c.Source == src {
			out[c.Key] = true
		}
	}
	return out, nil
}

// syncCopies writes the source's values into cn's copies (key's alone,
// or every one this source copied when key is ""): every prop but the
// key, the base times, endedProp and the held props takes the source's
// value (dropped when the source has none), and endedProp says whether
// the source still lists the thing. Nothing is written unless something
// changed, nothing is copied in (the copy store's first read does that),
// and nothing changes when the read knows nothing of the source (a cached
// read before any listing, a listing that failed). It answers c with the
// listing in its args.
func (a *App) syncCopies(c ochazuke.Ctx, cn *copiedNoun, key string) ochazuke.Ctx {
	c, l := a.withListing(c)
	if !l.known {
		return c
	}
	n := a.r.Noun(cn.noun)
	kp := cn.local.keyProp
	var copies []ochazuke.Thing
	sources := map[string]ochazuke.Thing{}
	if key != "" {
		t, had, err := cn.local.Get(key)
		if err != nil || !had {
			return c
		}
		copies = []ochazuke.Thing{t}
		if src, err := cn.src.Get(c, key); err == nil {
			sources[fmt.Sprint(t[kp])] = src
		}
	} else {
		mine, err := a.copiesOf(cn)
		if err != nil {
			return c
		}
		all, err := cn.local.List()
		if err != nil {
			return c
		}
		for _, t := range all {
			if mine[fmt.Sprint(t[kp])] {
				copies = append(copies, t)
			}
		}
		srcs, err := cn.src.List(c, ochazuke.ListQuery{Args: ochazuke.Arguments{"all": true}})
		if err != nil {
			return c
		}
		for _, s := range srcs {
			sources[fmt.Sprint(s[kp])] = s
		}
	}
	for _, t := range copies {
		if tombstone(t) {
			continue
		}
		k := fmt.Sprint(t[kp])
		src, listed := sources[k]
		next := maps.Clone(t)
		if listed {
			for _, p := range n.Props {
				if p.Key || cn.held[p.Name] || p.Name == endedProp || p.Role == ochazuke.RoleCreated || p.Role == ochazuke.RoleUpdated {
					continue
				}
				if v, ok := src[p.Name]; ok {
					next[p.Name] = v
				} else {
					delete(next, p.Name)
				}
			}
			delete(next, endedProp)
		} else {
			next[endedProp] = true
		}
		if sameJSON(t, next) {
			continue
		}
		if err := cn.local.Put(k, next); err != nil {
			c.Error(fmt.Sprintf("%s %s: the copy was not updated: %v", cn.noun, k, err))
		}
	}
	return c
}

// sameJSON reports whether two things store the same.
func sameJSON(a, b ochazuke.Thing) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ja) == string(jb)
}

// applyPaneCopies brings the copies to a listing the page shows (none
// when the listing failed: listErr) and keeps a failure for the hint. Each
// listed pane's held props from its copy (the name: what the daemon calls
// it is kept as the source's, paneThing), and the panes that ended, which
// the page shows under panes.show-ended. The read is the pane store's own
// (cached: the listing is a.panes), so a pane seen for the first time is
// copied in here.
func (a *App) applyPaneCopies(listErr error) {
	if listErr != nil {
		return
	}
	if err := a.copyPanes(); err != nil {
		a.paneErr = err.Error()
	}
}

// copyPanes is applyPaneCopies after a listing.
func (a *App) copyPanes() error {
	n := a.r.Noun("pane")
	if n == nil || n.Store == nil || a.copies["pane"] == nil {
		return nil
	}
	c := ochazuke.Ctx{Runtime: a.r, Args: ochazuke.Arguments{cachedOnlyArg: true}}
	things, err := n.Store.List(c, ochazuke.ListQuery{Args: ochazuke.Arguments{"all": true}})
	if err != nil {
		return err
	}
	byID := map[string]ochazuke.Thing{}
	a.pv.ended = nil
	for _, t := range things {
		id, _ := t["id"].(string)
		if ended, _ := t[endedProp].(bool); ended {
			a.pv.ended = append(a.pv.ended, endedPane(t))
			continue
		}
		byID[id] = t
	}
	for i := range a.panes {
		t, ok := byID[a.panes[i].ID]
		if !ok {
			continue
		}
		if !a.panes[i].copied {
			a.panes[i].copied, a.panes[i].sourceName = true, a.panes[i].Name
		}
		switch name, _ := t["name"].(string); {
		case name != a.panes[i].givenName():
			a.panes[i].Name, a.panes[i].named = name, true
		case a.panes[i].named:
			a.panes[i].Name, a.panes[i].named = a.panes[i].sourceName, false
		}
	}
	return nil
}

// endedPane is an ended pane's copy as a row: exited, with what was last
// seen of it.
func endedPane(t ochazuke.Thing) pane {
	text := func(name string) string { s, _ := t[name].(string); return s }
	starred, _ := t["starred"].(bool)
	p := pane{Session: text("session"), Harness: text("harness"), Exited: true, ended: true, Starred: starred,
		Pane: daemon.Pane{ID: text("id"), Name: text("name"), Title: text("title"), Cwd: text("cwd")}}
	return p
}

// shownPanes are the panes the page lists: the daemon's, then the ones
// that ended when panes.show-ended is on.
func (a *App) shownPanes() []pane {
	if len(a.pv.ended) == 0 || !a.r.Settings().Bool(settingShowEnded) {
		return a.panes
	}
	listed := make(map[string]bool, len(a.panes))
	for _, p := range a.panes {
		listed[p.ID] = true
	}
	out := append([]pane(nil), a.panes...)
	for _, p := range a.pv.ended {
		if !listed[p.ID] {
			out = append(out, p)
		}
	}
	return out
}
