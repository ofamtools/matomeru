package main

import (
	"fmt"
	"maps"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/actionitems"
)

// A pane's action items as one thing: the action-item verbs that have a
// meaning for every item of a pane at once, run on each of the pane's open
// items. They answer for a pane row what the item keys answer on an item
// row now that the Inbox lists hold one row per pane (panes.section-rows
// panes): x archives them. read-items and snooze-items have no key on a
// pane row (u is the row's undo; leader s is live's next starred), so
// they are reached from the palette and the CLI; enter, which attaches the
// pane, marks its items read by the one rule.

// paneItemsVerb is one of the pane verbs over its items: the action-item
// verb it runs on each, which open items it takes, the parameters it
// passes on, and the one a chooser asks for when none of them is given.
type paneItemsVerb struct {
	verb, run string
	takes     func(it actionitems.Item) bool
	params    map[string]ochazuke.Parameter
	chooser   pickSpec
	asks      string
}

var paneItemsVerbs = []paneItemsVerb{
	{verb: "archive-items", run: "archive",
		takes: func(actionitems.Item) bool { return true }},
	{verb: "read-items", run: "read",
		takes: func(it actionitems.Item) bool { return !it.Read() }},
	{verb: "snooze-items", run: "snooze",
		takes: func(actionitems.Item) bool { return true },
		params: map[string]ochazuke.Parameter{
			"until": {Type: "string", Description: "When they come back: RFC 3339, 2006-01-02 15:04, 2006-01-02 or tomorrow"},
			"for":   {Type: "string", Description: "How long: 90m, 4h, 2d, 1w"}},
		chooser: pickSpec{Kind: "snooze-chooser", Title: "Snooze %d items until"}, asks: "until"},
}

var panePaneParam = ochazuke.Parameter{Type: "string", Noun: "pane", Source: "pane.list", Free: true, Description: "The pane (its row id or window id); omitted, the selected pane"}

// parameters are the verb's: the pane and those it passes on. The verbs'
// rows and x (on a pane row it archives the pane's items; off the footer,
// which is full, as t) are in nouns/pane.json; the handlers take these
// (nouns.go).
func (v paneItemsVerb) parameters() map[string]ochazuke.Parameter {
	params := map[string]ochazuke.Parameter{"pane": panePaneParam}
	maps.Copy(params, v.params)
	return params
}

// paneItemsWindow is the window a pane-items verb acts on: --pane, else
// the pane the row holds (a closed pane's row too), else the pane at the
// focus; "" for none.
func (a *App) paneItemsWindow(ctx ochazuke.Ctx) string {
	if ref, _ := ctx.Args["pane"].(string); ref != "" {
		return a.paneRef(ref).ID
	}
	if ctx.Instance != nil {
		if p, ok := ctx.Instance.Data.(pane); ok && p.ID != "" {
			return p.ID
		}
	}
	p, _ := a.paneAt(ctx.Instance)
	return p.ID
}

// paneOpenItems are the ids of the window's open items v takes.
func (a *App) paneOpenItems(v paneItemsVerb, window string) ([]string, error) {
	if a.inbox == nil || a.inbox.items == nil || window == "" {
		return nil, nil
	}
	all, err := a.inbox.items.All()
	if err != nil {
		return nil, err
	}
	now := a.inbox.items.Now()
	var ids []string
	for _, it := range all {
		if it.Window == window && it.Open(now) && v.takes(it) {
			ids = append(ids, it.ID)
		}
	}
	return ids, nil
}

// runPaneItems is the handler of one pane-items verb. A pane with no open
// items (or none at the focus) is nothing to do, said and done.
func (a *App) runPaneItems(v paneItemsVerb) func(ochazuke.Ctx) ochazuke.Result {
	return func(ctx ochazuke.Ctx) ochazuke.Result {
		window := a.paneItemsWindow(ctx)
		ids, err := a.paneOpenItems(v, window)
		if err != nil {
			return ochazuke.Failed(err)
		}
		if len(ids) == 0 {
			a.r.UI().Info("no open items")
			return ochazuke.Done(map[string]any{"pane": window, "items": 0})
		}
		args := ochazuke.Arguments{}
		for k := range v.params {
			if x, ok := ctx.Args[k]; ok {
				args[k] = x
			}
		}
		if v.asks == "" || len(args) > 0 || a.cli {
			return a.applyPaneItems(v, window, ids, args)
		}
		// One chooser, its answer for every item.
		spec := v.chooser
		spec.Title = fmt.Sprintf(spec.Title, len(ids))
		spec.Pick = func(answer string) ochazuke.Result {
			return a.applyPaneItems(v, window, ids, ochazuke.Arguments{v.asks: answer})
		}
		_, err = a.askParameter(actionItemNoun+"."+v.run, v.asks, ochazuke.Arguments{"id": ids[0]}, spec)
		return resultError(err)
	}
}

// applyPaneItems runs the verb's action-item verb on each id.
func (a *App) applyPaneItems(v paneItemsVerb, window string, ids []string, extra ochazuke.Arguments) ochazuke.Result {
	for _, id := range ids {
		args := ochazuke.Arguments{"id": id}
		for k, x := range extra {
			args[k] = x
		}
		if _, err := a.r.Execute(ochazuke.Request{Noun: actionItemNoun, Verb: v.run, Args: args}); err != nil {
			return ochazuke.Failed(err)
		}
	}
	return ochazuke.Done(map[string]any{"pane": window, "items": len(ids)})
}
