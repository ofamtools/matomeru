package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

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
//   - a copy row stores only the props its noun's table says it keeps
//     (copiedProp.Copy: paneCopiedProps, sessionCopiedProps), the key, endedProp and
//     endedAtProp; every other prop is read when the copy is read: the
//     source's live facts for a thing the daemon lists, mtm's own records
//     (an owner, a star) from their own stores (copiedNoun.records);
//   - a held prop (copyHeld: the name) is the copy's own once mtm wrote it
//     (copies.hold-names written, the default) or from first sight (all);
//     until then it follows the source and is kept only as last seen;
//   - set writes the copy, and the props the noun declares Through also go
//     to the source (pane_store.go lists which, and why);
//   - refresh {id?} diffs the copies against the source and opens the
//     diff, a pick per prop; differences never block anything;
//   - a thing the source stops listing stays, as ended (endedProp, with
//     endedAtProp), showing what was last seen of it; the Panes page shows
//     ended panes under panes.show-ended. Only a listing this process made
//     of the daemon marks things ended (copyListing.known).

// endedProp is the read-only prop a copy noun's things carry: true when the
// source no longer lists the thing and mtm keeps its copy.
const endedProp = "ended"

// endedAtProp is when mtm saw the thing end (RFC 3339, UTC).
const endedAtProp = "ended-at"

// copyHeldKey is the field of a stored copy that lists the held props mtm
// wrote (copies.hold-names written); no prop can take the name.
const copyHeldKey = "copy.held"

// copyRole is what a copy row keeps of a prop.
type copyRole int

const (
	// copySeen: the source's value as last seen, kept so an ended thing
	// still shows and is found; a listed thing reads the source's.
	copySeen copyRole = iota + 1
	// copyHeld: the copy's own once held (copies.hold-names); until then
	// as copySeen.
	copyHeld
	// copyOwn: local metadata, never replaced by the daemon's facts.
	copyOwn
)

// The copy settings.
const (
	// settingShowEnded shows the panes that ended (kept copies) on the
	// Panes page.
	settingShowEnded = "panes.show-ended"
	// settingHoldNames is which names a copy holds: holdWritten (only a
	// name mtm wrote) or holdAll (every name from first sight).
	settingHoldNames = "copies.hold-names"
	holdWritten      = "written"
	holdAll          = "all"
)

func copySettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingShowEnded, Type: ochazuke.SettingBoolean, Default: false,
			Description: "Show the panes that ended on the Panes page (mtm keeps a copy of every pane it saw; pane list shows them with ended=true); false shows only the daemon's panes"},
		{Name: settingHoldNames, Section: "panes", Type: ochazuke.SettingString, Default: holdWritten, Enum: []string{holdWritten, holdAll},
			Description: "Which pane and session names mtm's copies hold: written (only a name renamed in mtm; the others follow the daemon) or all (every name from first sight, so a rename made outside mtm shows only in refresh)"},
	}
}

// copiedProp is a copy noun's prop with what its copy row keeps of it:
// nothing (0: read from the source or mtm's records each time), the last
// seen value, or a held one.
type copiedProp struct {
	ochazuke.PropSpec
	Copy copyRole
}

// specsOf are the props' specs.
func specsOf(props []copiedProp) []ochazuke.PropSpec {
	out := make([]ochazuke.PropSpec, len(props))
	for i, p := range props {
		out[i] = p.PropSpec
	}
	return out
}

// rolesOf is what a copy row keeps, by prop.
func rolesOf(props []copiedProp) map[string]copyRole {
	out := map[string]copyRole{}
	for _, p := range props {
		if p.Copy != 0 {
			out[p.Name] = p.Copy
		}
	}
	return out
}

// endedProps are endedProp and endedAtProp, which every copy row keeps
// (copiedNoun.keep).
func endedProps(noun string) []copiedProp {
	return []copiedProp{
		{PropSpec: ochazuke.PropSpec{Name: endedProp, ReadOnly: true, Parameter: ochazuke.Parameter{Type: "boolean",
			Description: "The daemon no longer lists the " + noun + ": mtm keeps its copy (what was last seen of it)"}}},
		{PropSpec: ochazuke.PropSpec{Name: endedAtProp, ReadOnly: true, Parameter: ochazuke.Parameter{Type: "string",
			Description: "When mtm saw the " + noun + " end (RFC 3339), empty while the daemon lists it"}}},
	}
}

// copyLocal is ochazuke's LocalStore for one noun over the work store's
// copies table. canon makes a key the one the source lists the thing by
// (a row id or a name to its id), and a thing is kept under its own key
// prop's value when it has one, so every form of a key reaches one copy.
// It stores only what cn keeps (copiedNoun.keep) and reads a copy filled
// in (App.copyView).
type copyLocal struct {
	a       *App
	cn      *copiedNoun
	noun    string
	keyProp string
	canon   func(string) string
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
	c, ok, err := l.a.work.Copy(l.noun, l.canon(key))
	if err != nil || !ok {
		return nil, false, err
	}
	return l.a.copyView(l.cn, l.thingOf(c)), true, nil
}

// Put stores what the noun keeps of t, the held marks and the source the
// copy came from carried over.
func (l copyLocal) Put(key string, t ochazuke.Thing) error {
	k := l.key(key, t)
	props := l.cn.keep(t)
	props[l.keyProp] = k
	source := l.a.server()
	if old, ok, err := l.a.work.Copy(l.noun, k); err != nil {
		return err
	} else if ok {
		source = old.Source
		if held, ok := old.Props[copyHeldKey]; ok {
			props[copyHeldKey] = held
		}
	}
	return l.a.work.PutCopy(l.noun, k, source, props)
}

func (l copyLocal) List() ([]ochazuke.Thing, error) {
	copies, err := l.a.work.Copies(l.noun)
	if err != nil {
		return nil, err
	}
	out := make([]ochazuke.Thing, len(copies))
	for i, c := range copies {
		out[i] = l.a.copyView(l.cn, l.thingOf(c))
	}
	return out, nil
}

func (l copyLocal) Delete(key string) error { return l.a.work.DeleteCopy(l.noun, l.canon(key)) }

// thingOf is a stored copy as a thing, an integer prop's value an int
// again (JSON reads every number as a float).
func (l copyLocal) thingOf(c work.Copy) ochazuke.Thing {
	t := ochazuke.Thing(c.Props)
	if t == nil {
		return ochazuke.Thing{}
	}
	if n := l.a.r.Noun(l.noun); n != nil {
		for _, p := range n.Props {
			if f, ok := t[p.Name].(float64); ok && p.Type == ochazuke.SettingInteger && f == float64(int(f)) {
				t[p.Name] = int(f)
			}
		}
	}
	return t
}

// hold marks names as the copy's own (mtm wrote them).
func (l copyLocal) hold(key string, names []string) error {
	k := l.canon(key)
	c, ok, err := l.a.work.Copy(l.noun, k)
	if err != nil || !ok || len(names) == 0 {
		return err
	}
	held := heldMarks(c.Props)
	before := len(held)
	for _, name := range names {
		held[name] = true
	}
	if len(held) == before {
		return nil
	}
	c.Props[copyHeldKey] = slices.Sorted(maps.Keys(held))
	return l.a.work.PutCopy(l.noun, k, c.Source, c.Props)
}

// heldMarks are the props a stored copy marks as written in mtm.
func heldMarks(props map[string]any) map[string]bool {
	out := map[string]bool{}
	switch v := props[copyHeldKey].(type) {
	case []any:
		for _, name := range v {
			out[fmt.Sprint(name)] = true
		}
	case []string:
		for _, name := range v {
			out[name] = true
		}
	}
	return out
}

// tombstone reports whether a copy is a deleted one's tombstone.
func tombstone(t ochazuke.Thing) bool { b, _ := t[ochazuke.CopyTombstone].(bool); return b }

// copiedNoun is one noun whose store is a copy store: its local store,
// what its copy rows keep of each prop (roles), mtm's own records of a
// thing the source does not list (records), and its source (the noun's
// store before the copy, ochazuke.Store.CopySource).
type copiedNoun struct {
	noun    string
	roles   map[string]copyRole
	records func(t ochazuke.Thing) ochazuke.Thing
	local   copyLocal
	src     ochazuke.Store
	r       *ochazuke.Runtime
	// check, when set, refuses a write no copy can hold (a session with
	// no name): the one refusal a copy keeps, before anything is written.
	check func(changes ochazuke.Thing) error
}

// keep is what a copy row stores of t: the key, the ended props, the
// tombstone and the props the noun's roles name.
func (cn *copiedNoun) keep(t ochazuke.Thing) ochazuke.Thing {
	out := ochazuke.Thing{}
	for name, v := range t {
		switch {
		case name == cn.local.keyProp, name == endedProp, name == endedAtProp, name == ochazuke.CopyTombstone, name == copyHeldKey, cn.roles[name] != 0:
			out[name] = v
		}
	}
	return out
}

// holds reports whether the stored copy t holds prop name as its own:
// a held prop mtm wrote, or (copies.hold-names all) any held prop with a
// value; an empty one is never held under all.
func (cn *copiedNoun) holds(t ochazuke.Thing, name string) bool {
	if cn.roles[name] == copyOwn {
		return true
	}
	if cn.roles[name] != copyHeld {
		return false
	}
	if heldMarks(t)[name] {
		return true
	}
	if cn.r.Settings().String(settingHoldNames) == holdAll {
		v, ok := t[name]
		return ok && v != nil && fmt.Sprint(v) != ""
	}
	return false
}

// copyView is a stored copy as the thing reads: a thing the last known
// listing has is the source's, its held props the copy's; any other (an
// ended thing, or none known yet) is what was stored with mtm's own
// records. The held marks never show.
func (a *App) copyView(cn *copiedNoun, t ochazuke.Thing) ochazuke.Thing {
	if tombstone(t) {
		return t
	}
	kp := cn.local.keyProp
	ended, _ := t[endedProp].(bool)
	if src, ok := a.copyFacts[cn.noun][fmt.Sprint(t[kp])]; ok && !ended {
		out := maps.Clone(src)
		for name := range cn.roles {
			if cn.holds(t, name) {
				out[name] = t[name]
			}
		}
		out[kp] = t[kp]
		return out
	}
	out := maps.Clone(t)
	delete(out, copyHeldKey)
	if cn.records != nil {
		maps.Copy(out, cn.records(t))
	}
	return out
}

// copyListing is the daemon's listing a read of a copy noun made, in the
// read's args (copyListingArg), so the source's reads inside it use it and
// never list again: known is true only for a listing of the daemon this
// process made and that succeeded (now, or the page's last listing when it
// did not fail), never for a cached read before any listing or after a
// failed one, which says nothing of what the source has.
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
	fromCache := cached || a.pv != nil && a.pv.listed
	panes, err := a.livePanes(c)
	l := copyListing{panes: panes, err: err, known: err == nil && (!fromCache || a.listedNow())}
	args := maps.Clone(c.Args)
	if args == nil {
		args = ochazuke.Arguments{}
	}
	args[copyListingArg] = l
	c.Args = args
	return c, l
}

// listedNow reports whether the cached listing is a listing of the daemon
// this process made and that succeeded: the last one did not fail.
func (a *App) listedNow() bool { return a.pv != nil && a.pv.listed && a.pv.listErr == nil }

// copyStore is the store of noun's copies of src's things, what a copy row
// keeps of each prop given by roles, and records the props of mtm's own
// a thing the source does not list reads (its owner, its star).
func (a *App) copyStore(noun string, src ochazuke.Store, canon func(string) string, roles map[string]copyRole, records func(ochazuke.Thing) ochazuke.Thing) ochazuke.Store {
	if a.copies == nil {
		a.copies = map[string]*copiedNoun{}
	}
	cn := &copiedNoun{noun: noun, roles: roles, records: records, src: src, r: a.r}
	cn.local = copyLocal{a: a, cn: cn, noun: noun, keyProp: "id", canon: canon}
	a.copies[noun] = cn
	s := ochazuke.CopyStore(src, cn.local)
	s.ListParams = src.ListParams
	return s
}

// narrowCopies rewrites noun's stored copies to what the noun keeps
// (copiedNoun.keep), each keeping its source: copies written before the
// roles were declared stored every prop. Run once the noun is declared.
func (a *App) narrowCopies(cn *copiedNoun) error {
	copies, err := a.work.Copies(cn.noun)
	if err != nil {
		return err
	}
	for _, c := range copies {
		kept := cn.keep(c.Props)
		if sameJSON(c.Props, kept) {
			continue
		}
		if err := a.work.PutCopy(cn.noun, c.Key, c.Source, kept); err != nil {
			return err
		}
	}
	return nil
}

// readCopiesNow makes noun's reads sync its copies from the source first
// (syncCopies): get, list and set answer the thing as it is now, a list
// keeps only this source's copies and, for an ended thing, the scope's
// (list --all every project's), and set on a thing with no copy that the
// source does not list writes the source alone (a pane mtm has not listed
// yet still takes a star or an owner: record first, never refuse). A set
// that writes a held prop marks it the copy's own (copyLocal.hold). Called
// once the noun's store is declared (it wraps the bound store); it narrows
// the copies stored before (narrowCopies).
func (a *App) readCopiesNow(noun string, check func(ochazuke.Thing) error) {
	cn, n := a.copies[noun], a.r.Noun(noun)
	if cn == nil || n == nil || n.Store == nil {
		panic("copies: " + noun + " has no copy store")
	}
	if err := a.narrowCopies(cn); err != nil {
		a.r.UI().Notify(fmt.Sprintf("%s copies: %v", noun, err))
	}
	cn.check = check
	s := n.Store
	get, list, patch := s.Get, s.List, s.Patch
	s.Get = func(c ochazuke.Ctx, key string) (ochazuke.Thing, error) {
		c = a.syncCopies(c, cn)
		return get(c, key)
	}
	s.List = func(c ochazuke.Ctx, q ochazuke.ListQuery) ([]ochazuke.Thing, error) {
		c = a.syncCopies(c, cn)
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
		c = a.syncCopies(c, cn)
		if _, had, err := cn.local.Get(key); err != nil {
			return nil, err
		} else if !had {
			if _, err := cn.src.Get(c, key); err != nil && cn.src.Patch != nil {
				return cn.src.Patch(c, key, changes)
			}
		}
		t, err := patch(c, key, changes)
		if err != nil {
			return nil, err
		}
		var held []string
		for name := range changes {
			if cn.roles[name] == copyHeld {
				held = append(held, name)
			}
		}
		if err := cn.local.hold(key, held); err != nil {
			return nil, err
		}
		if err := a.refreshArchivedPanes(); err != nil {
			return nil, err
		}
		return t, nil
	}
	if op, ok := a.r.Operations.Find(ochazuke.Key(a.r.CanonicalKey(noun + ".refresh"))); ok && op.AppHandler != nil {
		refresh := op.AppHandler
		op.AppHandler = func(c ochazuke.Ctx) ochazuke.Result {
			a.syncCopies(c, cn)
			return refresh(c)
		}
		a.r.Operations.Set(op)
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

// syncCopies reads the source's things (the listing the read carries)
// into the copies' view (App.copyFacts) and brings this source's stored
// copies to it: a listed thing's kept props take the source's values (a
// held one stays), and it is not ended; a thing the listing lacks is
// ended, with when mtm saw it end. Nothing is written unless something
// changed, nothing is copied in (the copy store's first read does that),
// and nothing changes when the read knows nothing of the source (a cached
// read before any listing, a listing that failed, a process that never
// listed the daemon: copyListing.known). It answers c with the listing in
// its args.
func (a *App) syncCopies(c ochazuke.Ctx, cn *copiedNoun) ochazuke.Ctx {
	c, l := a.withListing(c)
	if !l.known {
		return c
	}
	kp := cn.local.keyProp
	srcs, err := cn.src.List(c, ochazuke.ListQuery{Args: ochazuke.Arguments{"all": true}})
	if err != nil {
		return c
	}
	sources := make(map[string]ochazuke.Thing, len(srcs))
	for _, s := range srcs {
		sources[cn.local.canon(fmt.Sprint(s[kp]))] = s
	}
	if a.copyFacts == nil {
		a.copyFacts = map[string]map[string]ochazuke.Thing{}
	}
	a.copyFacts[cn.noun] = sources
	copies, err := a.work.Copies(cn.noun)
	if err != nil {
		return c
	}
	here := a.server()
	for _, cp := range copies {
		if cp.Source != here {
			continue
		}
		t := cn.local.thingOf(cp)
		if tombstone(t) {
			continue
		}
		next := maps.Clone(t)
		if src, listed := sources[cp.Key]; listed {
			for name := range cn.roles {
				if cn.holds(t, name) {
					continue
				}
				if v, ok := src[name]; ok {
					next[name] = v
				} else {
					delete(next, name)
				}
			}
			delete(next, endedProp)
			delete(next, endedAtProp)
		} else if ended, _ := t[endedProp].(bool); !ended {
			next[endedProp] = true
			next[endedAtProp] = time.Now().UTC().Format(time.RFC3339)
		}
		if sameJSON(t, next) {
			continue
		}
		if err := a.work.PutCopy(cn.noun, cp.Key, cp.Source, next); err != nil {
			c.Error(fmt.Sprintf("%s %s: the copy was not updated: %v", cn.noun, cp.Key, err))
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
		a.panes[i].Archived, _ = t["archived"].(bool)
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
	archived, _ := t["archived"].(bool)
	p := pane{Session: text("session"), Harness: text("harness"), Exited: true, ended: true, Starred: starred, Archived: archived,
		Pane: daemon.Pane{ID: text("id"), Name: text("name"), Title: text("title"), Cwd: text("cwd")}}
	return p
}

// shownPanes are the panes the page lists: the daemon's, then the ones
// that ended when panes.show-ended is on.
func (a *App) shownPanes() []pane {
	out := make([]pane, 0, len(a.panes)+len(a.pv.ended))
	for _, p := range a.panes {
		if a.archiveVisible(p.Archived, a.pv.tree.List()) {
			out = append(out, p)
		}
	}
	if len(a.pv.ended) == 0 || !a.r.Settings().Bool(settingShowEnded) {
		return out
	}
	listed := make(map[string]bool, len(a.panes))
	for _, p := range a.panes {
		listed[p.ID] = true
	}
	for _, p := range a.pv.ended {
		if !listed[p.ID] && a.archiveVisible(p.Archived, a.pv.tree.List()) {
			out = append(out, p)
		}
	}
	return out
}
