package main

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

// The core verbs' keys on mounted extension rows. A mounted noun with
// catalog props comes with a proxy store (ochazuke's cli.MountNouns), so it
// has the core verbs create, get, set, delete, archive, list and edit,
// each unavailable with a reason where the tool offers no call for it. mtm
// gives them the same keys as its own nouns: n on each list showing the
// noun's rows runs create, d d on a row runs delete after a confirm (the
// tool's own, else "Delete {{label}}?"), and i is the standard edit form
// (ochazuke's i row). A noun mounted without a store keeps the verbs it
// mounted; one of them named create or delete gets the same key. No other
// verb name is guessed at.
//
// A core verb the framework's overlay runs (ochazuke v0.146.4: the tool has
// no call for it, so create makes a thing of your own and delete hides the
// tool's thing) takes mtm's key only where the tool's catalog binds nothing
// on that key for the noun: a tool's own row (gh's d d ignore, git's d d
// remove, csv's n next) keeps its key, and the overlay's verb stays on the
// palette, the CLI and a bindings.json row.

// coreKeyVerbs are the core verbs that take mtm's keys on a mounted noun,
// and which of them the overlay runs (Runtime.OverlayFills).
type coreKeyVerbs struct{ Create, Delete, CreateOverlay, DeleteOverlay bool }

// coreKeysOf reads which of create and delete the mounted noun can run.
func (a *App) coreKeysOf(local string) coreKeyVerbs {
	has := func(verb string) (bool, bool) {
		op, ok := a.r.Operations.Find(ochazuke.Key(a.r.CanonicalKey(local + "." + verb)))
		if !ok || op.Unavailable != "" {
			return false, false
		}
		return true, a.r.OverlayFills(op)
	}
	var v coreKeyVerbs
	v.Create, v.CreateOverlay = has("create")
	v.Delete, v.DeleteOverlay = has("delete")
	return v
}

// catalogBindsKey reports whether the tool's catalog binds a row on the
// catalog noun, in any mode, whose keys clash with keys
// (the same, or one a prefix of the other), other than a prefix's cancel
// row and the noun's plain delete row (mtm's d d takes that one).
func (x *extension) catalogBindsKey(noun, keys string) bool {
	for _, row := range x.doc.Bindings {
		if row.Noun != noun || row.Keys == "" || isPrefixCancel(row) || catalogDeleteRow(row, noun) {
			continue
		}
		if keysClash(strings.Fields(keys), strings.Fields(row.Keys)) {
			return true
		}
	}
	return false
}

// catalogDeleteRow reports whether a catalog row is the noun's plain
// delete: its request is the core delete of that noun, named directly or
// through the noun's alias (Via: o-questions' decision rows run
// question.delete via decision.delete), with no preset arguments
// (row-field sources are kept), on the noun's rows. mtm's d d takes that
// row, and its portable guard; a variant presetting arguments (force)
// stays its own.
func catalogDeleteRow(row ochazuke.Binding, noun string) bool {
	return row.Noun == noun && row.Instance == "" && row.Pattern == "" &&
		(row.Request.Key() == noun+".delete" || row.Request.Via == noun+".delete") &&
		len(row.Request.Args) == 0
}

// mountCRUD binds the core verbs' keys of an extension's mounted nouns: d
// d on the rows' noun, n on each list showing them. It answers the keys it
// bound per mtm noun, which the catalog's own rows may not take.
func (a *App) mountCRUD(x *extension, plan []ocli.CatalogNoun, pages []ocli.CatalogPage) map[string][]string {
	taken := map[string][]string{}
	for _, n := range plan {
		local, ok := x.nouns[n.Name]
		if !ok {
			continue
		}
		if v := a.coreKeysOf(local); !v.Delete || (v.DeleteOverlay && x.catalogBindsKey(n.Name, "d d")) {
			continue
		}
		key := a.r.CanonicalKey(local + ".delete")
		if len(requiredOf(a.r.Operations.At(ochazuke.Key(key)))) > 0 {
			continue
		}
		if op := a.r.Operations.At(ochazuke.Key(key)); op.Confirm == "" {
			op.Confirm = "Delete {{label}}?"
			a.r.Operations.Set(op)
		}
		row := ochazuke.Do(local + ".delete")
		for _, original := range x.doc.Bindings {
			if catalogDeleteRow(original, n.Name) {
				row = original
				row.Request.Noun, row.Request.Via = local, ""
				if !ochazuke.PortableGuard(row.When) {
					row.When = "" // as loadExtBindings: only a portable guard stays
				}
				break
			}
		}
		a.r.Define(local).Mode("normal").Bind("d d", row)
		taken[local] = append(taken[local], "d d")
	}
	for _, p := range pages {
		for _, l := range p.Lists {
			noun := x.newNounOf(l, a)
			if noun == "" {
				continue
			}
			local := x.nouns[noun]
			row := ochazuke.Do(local + ".create").Hinted("new")
			row.Instance, row.Mode, row.Keys = l.ID, "normal", "n"
			if err := a.r.Bind(row); err != nil {
				a.r.UI().Notify(x.name + ": " + err.Error())
				continue
			}
			taken[local] = append(taken[local], "n")
		}
	}
	return taken
}

// newNounOf is the catalog noun whose create n runs on a list: its rows'
// noun, else the list's own noun; "" when neither can create.
func (x *extension) newNounOf(l ocli.CatalogList, a *App) string {
	rows := l.Noun
	if l.Source != "" {
		noun, verb, _ := strings.Cut(l.Source, ".")
		rows = noun
		for _, n := range x.doc.Nouns {
			for _, v := range n.Verbs {
				if n.Name == noun && v.Name == verb && v.Rows != "" {
					rows = v.Rows
				}
			}
		}
	} else if _, r := x.doc.RowSource(l.Noun); r != "" {
		rows = r
	}
	for _, n := range []string{rows, l.Noun} {
		if x.nouns[n] == "" {
			continue
		}
		if v := a.coreKeysOf(x.nouns[n]); v.Create && !(v.CreateOverlay && (x.catalogBindsKey(rows, "n") || x.catalogBindsKey(l.Noun, "n"))) {
			return n
		}
	}
	return ""
}

// requiredOf are an operation's required parameters, sorted.
func requiredOf(op ochazuke.Operation) []string {
	var out []string
	for name, p := range op.Parameters {
		if p.Required {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// fieldText is a JSON value as a field's text.
func fieldText(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	data, _ := json.Marshal(v)
	return string(data)
}
