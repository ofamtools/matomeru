package main

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
	"ofam.dev/oden"
)

// How mtm names an extension's nouns (setting extensions.noun-names). The
// naming is data: a row of extNounNamings per value, read where a noun is
// mounted, never a branch on one extension or noun.
const (
	settingExtNounNames = "extensions.noun-names"
	// extNounNamesNamespaced mounts each noun as <ext>.<noun> (ochazuke's
	// cli.Namespace, as every family host does), with the bare <noun> as
	// an alias while no other mounted extension has a noun of that name,
	// and the old <ext>-<noun> kept as a deprecated alias.
	extNounNamesNamespaced = "namespaced"
	// extNounNamesLegacy is mtm before namespaces: <ext>-<noun> only.
	extNounNamesLegacy = "legacy"
)

// extNounNamesSince and extNounNamesRemoveAfter date the deprecated
// <ext>-<noun> spelling: the release that renamed the nouns and the one
// after which the old spelling goes.
const (
	extNounNamesSince       = "v0.2.0"
	extNounNamesRemoveAfter = "v0.3.0"
)

// extNounNaming is one way of naming an extension's nouns.
type extNounNaming struct {
	// local is mtm's noun of the extension's noun.
	local func(ext, noun string) string
	// old, when set, is a former spelling kept as a deprecated alias.
	old func(ext, noun string) string
	// bare gives a noun no other mounted extension has its own name too.
	bare bool
}

func dashedExtNoun(ext, noun string) string { return ext + "-" + noun }

var extNounNamings = map[string]extNounNaming{
	extNounNamesNamespaced: {local: func(ext, noun string) string { return ocli.Namespace(ext)(noun) }, old: dashedExtNoun, bare: true},
	extNounNamesLegacy:     {local: dashedExtNoun},
}

var extNounNamesSetting = ochazuke.Setting{Name: settingExtNounNames, Owner: extensionsOwner, Section: ochazuke.SettingsGeneralSection,
	Type: ochazuke.SettingString, Default: extNounNamesNamespaced, Enum: []string{extNounNamesNamespaced, extNounNamesLegacy},
	Description: "How an extension's nouns are named: namespaced (<ext>.<noun>, the bare <noun> too while no other extension has one of that name, and the old <ext>-<noun> accepted with a notice until " + extNounNamesRemoveAfter + ") or legacy (<ext>-<noun> only, as before " + extNounNamesSince + "); a change remounts the extensions"}

// extNamingFor is the naming an extension of this name mounts with: the
// setting's, except that a name ochazuke reserves as a namespace (the
// framework's short names, app, ochazuke: cli.ExtensionNamespace) keeps
// the dashed spelling, with why.
func (a *App) extNamingFor(ext string) (extNounNaming, error) {
	naming, ok := extNounNamings[a.r.Settings().String(settingExtNounNames)]
	if !ok {
		naming = extNounNamings[extNounNamesNamespaced]
	}
	if naming.old == nil {
		return naming, nil
	}
	if ns, err := ocli.ExtensionNamespace(ext); err != nil || ns != ext {
		if err == nil {
			err = fmt.Errorf("namespace of %q is %q", ext, ns)
		}
		return extNounNamings[extNounNamesLegacy], fmt.Errorf("nouns keep the %s spelling: %v", dashedExtNoun(ext, "NOUN"), err)
	}
	return naming, nil
}

// extNoun is mtm's noun of an extension's noun under the naming it would
// mount with now.
func (a *App) extNoun(ext, noun string) string {
	naming, _ := a.extNamingFor(ext)
	return naming.local(ext, noun)
}

// catalogNounOf is the extension's noun of a host noun under its naming,
// "" when local is not one.
func (x *extension) catalogNounOf(local string) string {
	for _, n := range x.doc.Nouns {
		if x.naming.local != nil && x.naming.local(x.name, n.Name) == local {
			return n.Name
		}
	}
	return ""
}

// mountOldNounNames registers each mounted noun's former spelling as a
// deprecated alias (Keep actions: a bindings.json row or a script naming
// md-doc still runs md.doc, with one notice). A spelling mtm or another
// noun already owns is left to it.
func (a *App) mountOldNounNames(x *extension, notify func(string, ...any)) {
	if x.naming.old == nil {
		return
	}
	for _, n := range slices.Sorted(maps.Keys(x.nouns)) {
		local, old := x.nouns[n], x.naming.old(x.name, n)
		if old == local || a.r.Noun(old) != nil {
			continue
		}
		err := a.r.AddNounAlias(ochazuke.NounAlias{Alias: old, Noun: local, Since: extNounNamesSince, RemoveAfter: extNounNamesRemoveAfter,
			Note: "set extensions.noun-names to legacy to keep the old names"})
		if err != nil {
			notify("old name %s: %v", old, err)
			continue
		}
		x.aliases = append(x.aliases, old)
	}
}

// unmountNounNames removes the aliases mtm gave an extension's nouns: its
// old spellings and the bare names that reach it.
func (a *App) unmountNounNames(x *extension) {
	for _, alias := range x.aliases {
		_ = a.r.Unalias(alias)
	}
	x.aliases = nil
	locals := map[string]bool{}
	for _, l := range x.nouns {
		locals[l] = true
	}
	for alias, noun := range a.extBare {
		if locals[noun] {
			_ = a.r.Unalias(alias)
			delete(a.extBare, alias)
		}
	}
}

// syncBareNounNames gives each mounted extension noun its bare name when
// its naming asks for one and the name is free: no other mounted
// extension has a noun of that name (ochazuke's palette offers the full
// names of an ambiguous tail, palette.noun-tails), it is no extension's
// namespace, and mtm or the framework does not own it. A bare name that
// stopped being free is removed. Shared names are kept for the CLI's
// exact ambiguity commands. Call it after every mount and unmount.
func (a *App) syncBareNounNames() {
	owners := map[string][]string{} // a catalog noun -> the mtm nouns of that name
	namespaces := map[string]bool{}
	for _, x := range a.exts {
		namespaces[x.name] = true
		if !x.naming.bare {
			continue
		}
		for n, local := range x.nouns {
			owners[n] = append(owners[n], local)
		}
	}
	want := map[string]string{}
	for n, locals := range owners {
		if len(locals) == 1 && !namespaces[n] {
			want[n] = locals[0]
		}
	}
	for alias, noun := range a.extBare {
		if want[alias] != noun {
			_ = a.r.Unalias(alias)
			delete(a.extBare, alias)
		}
	}
	if a.extBare == nil {
		a.extBare = map[string]string{}
	}
	for _, alias := range slices.Sorted(maps.Keys(want)) {
		noun := want[alias]
		if a.extBare[alias] == noun || a.r.Noun(alias) != nil || a.r.CanonicalNoun(alias) != alias {
			continue // ours already, mtm's own noun, or another noun's alias
		}
		if err := a.r.Alias(alias, noun); err == nil {
			a.extBare[alias] = noun
		}
	}
	a.extAmbiguous = map[string][]string{}
	for n, locals := range owners {
		if len(locals) > 1 && !namespaces[n] && a.r.Noun(n) == nil && a.r.CanonicalNoun(n) == n {
			slices.Sort(locals)
			a.extAmbiguous[n] = locals
		}
	}
}

// mountAmbiguousNounNames reserves shared bare words in the outer shell:
// an exact command beats a prefix of an unrelated noun or alias (doc
// would otherwise resolve to docs). Owners are sorted by canonical name.
// Real host commands are registered first and keep their exact names.
func (a *App) mountAmbiguousNounNames(app *oden.App) {
	for _, name := range slices.Sorted(maps.Keys(a.extAmbiguous)) {
		owners := a.extAmbiguous[name]
		uses := make([]string, len(owners))
		for i, noun := range owners {
			uses[i] = "mtm " + strings.ReplaceAll(noun, ".", " ") + " ..."
		}
		err := fmt.Errorf("%s is ambiguous: %s (use %s)", name, strings.Join(owners, " or "), strings.Join(uses, " or "))
		app.Add(oden.Command{Name: name, Summary: err.Error(), Source: "extensions",
			Run: func(context.Context, []string) error { return err },
		})
	}
}

// observeExtNounNames remounts every extension when the naming changes
// from the one they mounted with (a reload that reads the same value
// remounts nothing).
func (a *App) observeExtNounNames() {
	mounted := a.r.Settings().String(settingExtNounNames)
	a.r.Settings().Observe(settingExtNounNames, func(_ string, v any) {
		if now, _ := v.(string); now != mounted {
			mounted = now
			a.remountExtensions()
		}
	})
}
