package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
)

// Pick lists for a mounted tool's sourced parameters. A catalog parameter
// whose source names one of the tool's verbs (NOUN.VERB) gets that verb's
// rows as its choices, the same rule nabe adopted: the choices are
// suggestions only, every such parameter is Free, so mtm never refuses a
// value outside them (a stale or partial list must not refuse a value the
// tool would take); the tool checks its own arguments. mtm never execs on
// the loop, so the choices are a cache written into the parameter's Enum
// (ids, after its static enum): one quiet load per source, off the loop,
// whenever the extension's rows reload (its page opened, a verb run, a
// refresh). Until the first load, and from the CLI, the parameter takes
// any value with no list. A source whose noun mtm did not mount gives no
// list and a notice.

// extSource is one source of an extension and the parameters it fills.
type extSource struct {
	key    string // the tool's NOUN.VERB
	params []extSourceParam
	ids    []string
	err    string // why its last load failed
}

// extSourceParam is one parameter a source fills: the operation key, the
// parameter's name and its catalog description (Enum the static choices).
type extSourceParam struct {
	op, name string
	base     ochazuke.Parameter
}

// mountSources collects the sources of an extension's mounted verbs; only
// the TUI loads them (the CLI never reloads a page).
func (a *App) mountSources(x *extension, plan []ocli.CatalogNoun) {
	x.sources = map[string]*extSource{}
	for _, n := range plan {
		local, ok := x.nouns[n.Name]
		if !ok {
			continue
		}
		for _, v := range n.Verbs {
			names := make([]string, 0, len(v.Params))
			for name := range v.Params {
				names = append(names, name)
			}
			slices.Sort(names)
			for _, name := range names {
				p := v.Params[name]
				if p.Source == "" {
					continue
				}
				srcNoun, srcVerb, _ := strings.Cut(p.Source, ".")
				if _, mounted := a.r.Operations[x.nouns[srcNoun]+"."+srcVerb]; !mounted || x.nouns[srcNoun] == "" {
					a.r.UI().Notify(fmt.Sprintf("%s: %s %s --%s: choices source %s is not mounted; no pick list", x.name, n.Name, v.Name, name, p.Source))
					continue
				}
				src := x.sources[p.Source]
				if src == nil {
					src = &extSource{key: p.Source}
					x.sources[p.Source] = src
				}
				base := p
				base.Source = ""
				src.params = append(src.params, extSourceParam{op: local + "." + v.Name, name: name, base: base})
			}
		}
	}
}

// applyChoices writes a source's cache into each parameter it fills: the
// static Enum, then the loaded ids; always Free.
func (a *App) applyChoices(src *extSource) {
	for _, sp := range src.params {
		p := sp.base
		p.Enum = slices.Clone(p.Enum)
		for _, id := range src.ids {
			if !slices.Contains(p.Enum, id) {
				p.Enum = append(p.Enum, id)
			}
		}
		p.Free = true
		op, ok := a.r.Operations[a.r.CanonicalKey(sp.op)]
		if !ok {
			continue
		}
		params := make(map[string]ochazuke.Parameter, len(op.Parameters))
		for k, v := range op.Parameters {
			params[k] = v
		}
		params[sp.name] = p
		op.Parameters = params
		a.r.Operations[a.r.CanonicalKey(sp.op)] = op
	}
}

// loadSources loads every source of an extension, each a quiet job in its
// own slot on the extension's first page; a failed load keeps the cache.
func (a *App) loadSources(x *extension) error {
	if len(x.sources) == 0 || len(x.views) == 0 {
		return nil
	}
	keys := make([]string, 0, len(x.sources))
	for key := range x.sources {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	owner := x.views[0].Page()
	bin, env := x.bin, a.extEnv(x.name)
	var errs []error
	for _, key := range keys {
		src := x.sources[key]
		noun, verb, _ := strings.Cut(key, ".")
		res := ochazuke.Pending(func(ctx context.Context) (any, error) {
			out, err := execExt(ctx, bin, env, noun, verb, "--json", "{}")
			if err != nil {
				return nil, err
			}
			return extDecoded(out), nil
		}).Replacing("choices " + key).Label(key).Then(func(_ ochazuke.Ctx, v any, err error) {
			var choices []ochazuke.Choice
			if err == nil {
				choices, err = extChoices(v)
			}
			if err != nil {
				src.err = fmt.Sprintf("choices %s: %v", key, err)
				for _, view := range x.views {
					view.SetStatus(src.err)
				}
				return
			}
			src.err = ""
			src.ids = src.ids[:0]
			for _, c := range choices {
				src.ids = append(src.ids, c.ID)
			}
			a.applyChoices(src)
		})
		if _, err := a.r.Launch(owner, ocli.LoadVerb, res, ochazuke.Ctx{Runtime: a.r}); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// extChoices reads a source's answer: rows as a list verb answers them
// (ids; any record with an id, label or name), else ochazuke's choices.
func extChoices(v any) ([]ochazuke.Choice, error) {
	if list, ok := v.([]any); ok {
		out := make([]ochazuke.Choice, 0, len(list))
		for _, item := range list {
			switch item := item.(type) {
			case map[string]any:
				id := ""
				for _, k := range []string{"id", "ID", "name", "Name", "label", "Label"} {
					if s, ok := item[k]; ok && s != nil {
						id = fieldText(s)
						break
					}
				}
				if id != "" {
					out = append(out, ochazuke.Choice{ID: id, Label: id})
				}
			default:
				if s := fieldText(item); s != "" {
					out = append(out, ochazuke.Choice{ID: s, Label: s})
				}
			}
		}
		return out, nil
	}
	return ochazuke.ParseChoices(v)
}
