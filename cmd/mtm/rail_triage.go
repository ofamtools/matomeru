package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/actionitems"
	"ofam.dev/matomeru/internal/contextitems"
	"ofam.dev/matomeru/internal/panesignals"
	"ofam.dev/matomeru/store"
)

// Rail triage (lane rail-triage; owner, 2026-11-13): what a pane's rail
// lists is what still wants a look. Context items carry a dismissed
// state beside the star: New lists the pane's items that are neither
// (flat, newest first), Starred the starred ones, the kind sections the
// starred and new ones, and the dismissed ones leave the rail
// (rail.dismissed hide) or fold under one Dismissed heading (collapsed).
// Needs you lists the pane's open items oldest first, near-duplicates
// folded to one row with a count, each row labelled by what it asks, and
// the read and archived ones under Resolved below. Every rail row starts
// with a one-cell glyph of its kind (rail.icons).
//
// Each section is a filter-only prop naming the pane (as project-pane
// is), so the rail's stack lists it as a referrer section and the groups
// setting gives it its heading: one mechanism, the rows' own nouns and
// verbs, no per-section code in the rail.

// The filter-only props of the rail's sections.
const (
	contextKeptPane    = "kept-pane"
	contextNewPane     = "new-pane"
	contextStarredPane = "starred-pane"
	contextDismissPane = "dismissed-pane"
	actionNeedsPane    = "needs-pane"
	actionResolvedPane = "resolved-pane"
)

// The rail triage settings and their values.
const (
	settingRailDismissed = "rail.dismissed"
	railDismissedHide    = "hide"
	railDismissedFold    = "collapsed"
	settingRailArchived  = "rail.archived"
	settingRailIcons     = "rail.icons"
	railIconsSymbols     = "symbols"
	railIconsNerd        = "nerd"
	railIconsLetters     = "letters"
	railIconsOff         = "off"
)

// railTriageSettings are rail.dismissed and rail.icons.
func railTriageSettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingRailDismissed, Section: "rail", Type: ochazuke.SettingString, Default: railDismissedHide, Enum: []string{railDismissedHide, railDismissedFold},
			Description: "Dismissed context items on a pane's rail: hide (left out; context-item list --where dismissed=true still lists them) or collapsed (one folded Dismissed (N) heading at the end)"},
		{Name: settingRailArchived, Section: "rail", Type: ochazuke.SettingList, Default: []string{"action-item." + actionResolvedPane},
			Description: "Rail section ids (NOUN.PROP) that list archived things too, whatever collection.hide-archived says for other lists. Default: Resolved, so old items stay visible below Needs you"},
		{Name: settingRailIcons, Section: "rail", Type: ochazuke.SettingString, Default: railIconsSymbols, Enum: []string{railIconsSymbols, railIconsNerd, railIconsLetters, railIconsOff},
			Description: "The one-cell glyph each rail row starts with, by its kind: symbols (plain Unicode), nerd (Nerd Font glyphs), letters (f d l ...) or off"},
	}
}

// contextTriage is one context section's rule: which items of the pane
// it holds, as store conditions and as the same test over an item.
type contextTriage struct {
	prop, desc string
	// shown, when set, is the rail.dismissed value the section needs:
	// under another value it holds nothing.
	shown string
	conds []store.QueryCond
	keeps func(contextitems.Item) bool
}

func boolCond(field string, v bool) store.QueryCond {
	return store.QueryCond{Fields: []string{field}, Op: store.OpEq, Value: v}
}

// railKept is the condition of an item the rail keeps: not dismissed, or
// starred (a star outranks a dismissal).
var railKept = store.QueryCond{Any: []store.QueryCond{boolCond("dismissed", false), boolCond("starred", true)}}

func keptItem(it contextitems.Item) bool { return !it.Dismissed || it.Starred }

// contextTriages are the rail's context sections, by prop.
var contextTriages = []contextTriage{
	{prop: contextKeptPane, desc: "the items it printed first that are not dismissed (or are starred): the rail's kind sections",
		conds: []store.QueryCond{railKept}, keeps: keptItem},
	{prop: contextNewPane, desc: "the items it printed first that are neither starred nor dismissed: the rail's New section",
		conds: []store.QueryCond{boolCond("dismissed", false), boolCond("starred", false)},
		keeps: func(it contextitems.Item) bool { return !it.Dismissed && !it.Starred }},
	{prop: contextStarredPane, desc: "the starred items it printed first: the rail's Starred section",
		conds: []store.QueryCond{boolCond("starred", true)}, keeps: func(it contextitems.Item) bool { return it.Starred }},
	{prop: contextDismissPane, desc: "the dismissed, unstarred items it printed first: the rail's Dismissed section (rail.dismissed collapsed; hide: none)",
		shown: railDismissedFold, conds: []store.QueryCond{boolCond("dismissed", true), boolCond("starred", false)},
		keeps: func(it contextitems.Item) bool { return it.Dismissed && !it.Starred }},
}

// contextTriageProps are the filter-only props of contextTriages and the
// dismissal stamp.
func contextTriageProps() []ochazuke.PropSpec {
	out := []ochazuke.PropSpec{{Name: "dismissed-at", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "integer",
		Description: "When the item was dismissed (Unix ms; 0 never): one bulk dismissal stamps every item alike, so --filter dismissed-at=STAMP names that batch"}}}
	for _, t := range contextTriages {
		out = append(out, ochazuke.PropSpec{Name: t.prop, ReadOnly: true, Parameter: ochazuke.Parameter{Type: "string", Noun: "pane",
			Description: "Filter only (--where " + t.prop + "=PANE): " + t.desc}})
	}
	return out
}

func contextTriageOf(prop string) (contextTriage, bool) {
	i := slices.IndexFunc(contextTriages, func(t contextTriage) bool { return t.prop == prop })
	if i < 0 {
		return contextTriage{}, false
	}
	return contextTriages[i], true
}

// triageShown reports whether the section's setting lets it hold things.
func (a *App) triageShown(t contextTriage) bool {
	return t.shown == "" || a.r.Settings().String(settingRailDismissed) == t.shown
}

// triageCond is the store condition of prop = window.
func (a *App) triageCond(prop string) func(string) (store.QueryCond, bool) {
	return func(window string) (store.QueryCond, bool) {
		t, _ := contextTriageOf(prop)
		if !a.triageShown(t) {
			return store.QueryCond{Fields: []string{"id"}, Op: store.OpEq, Value: ""}, true
		}
		return store.QueryCond{All: append([]store.QueryCond{{Fields: []string{"window"}, Op: store.OpEq, Value: window}}, t.conds...)}, true
	}
}

// markTriage gives the listed things the value of each triage filter they
// meet, so a request that is not pushed answers what the pushed one does.
func (a *App) markTriage(things []ochazuke.Thing, filters []ochazuke.PropFilter) {
	for _, f := range filters {
		t, ok := contextTriageOf(f.Prop)
		if !ok || f.Op != "" && f.Op != "=" || !a.triageShown(t) {
			continue
		}
		window := fmt.Sprint(f.Value)
		for _, th := range things {
			it := contextitems.Item{Starred: th["starred"] == true, Dismissed: th["dismissed"] == true}
			if th["window"] == window && t.keeps(it) {
				th[f.Prop] = window
			}
		}
	}
}

// Dismissal ----------------------------------------------------------------

// contextTriageParams are dismiss's and undismiss's parameters.
func contextTriageParams() map[string]ochazuke.Parameter {
	return map[string]ochazuke.Parameter{
		"id":     {Type: "string", Source: contextNoun + ".list", Description: "The item id; omitted, the selected rows"},
		"before": {Type: "string", Description: "Every item first kept before this time (2026-11-14, 2026-11-14T09:00, RFC 3339 or Unix ms), in one write"},
		"filter": {Type: "string", Free: true, Description: "Every item meeting this list filter (prop=value words, as context-item list --filter), in one write"},
		"all":    {Type: "boolean", Description: "Every item, in one write"},
	}
}

// triageContext is context-item.dismiss (value true) and undismiss
// (false). On rows (the selected ones, or --id) it is the standard set of
// dismissed: one history entry, undone by history-entry.undo. With
// --before, --filter or --all it writes every matching item in one store
// write, stamped alike, and answers the command that takes that batch
// back.
func (a *App) triageContext(value bool) ochazuke.AppHandler {
	return func(ctx ochazuke.Ctx) ochazuke.Result {
		if bulk, ok := triageBulk(ctx.Args); ok {
			return a.triageWhere(ctx, value, bulk)
		}
		props := map[string]any{"dismissed": value}
		if id := ochazuke.StringArg(ctx.Args, "id", ""); id != "" {
			return resultOf(a.r.Execute(ochazuke.Request{Noun: contextNoun, Verb: "set", Args: ochazuke.Arguments{"id": id, "props": props}}))
		}
		if ctx.Instance == nil {
			return ochazuke.Failed(&ochazuke.ArgumentError{Err: fmt.Errorf("name an item (--id), or every item with --before, --filter or --all")})
		}
		return resultOf(a.r.Execute(ochazuke.Request{Noun: contextNoun, Verb: "set", Target: ctx.Instance.ID, Subjects: ctx.SubjectIDs(),
			Args: ochazuke.Arguments{"props": props}}))
	}
}

// triageQuery is a bulk dismissal's selection.
type triageQuery struct {
	before int64 // Unix ms; 0 none
	filter string
}

// triageBulk is the bulk form's selection, false when the request names
// none (it acts on rows).
func triageBulk(args ochazuke.Arguments) (triageQuery, bool) {
	var q triageQuery
	all, _ := args["all"].(bool)
	before := strings.TrimSpace(ochazuke.StringArg(args, "before", ""))
	q.filter = strings.TrimSpace(ochazuke.StringArg(args, "filter", ""))
	if before != "" {
		q.before = parseTriageTime(before)
		if q.before == 0 {
			q.before = -1 // unreadable: refused by triageWhere
		}
	}
	return q, all || before != "" || q.filter != ""
}

// parseTriageTime reads a time as Unix ms: Unix ms, RFC 3339, or a local
// date or date and time. 0 when it reads none.
func parseTriageTime(s string) int64 {
	if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
		return ms
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UnixMilli()
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t.UnixMilli()
		}
	}
	return 0
}

// triageWhere writes dismissed = value on every item the query selects, in
// one transaction; a dismissal stamps them all with one dismissed-at.
func (a *App) triageWhere(ctx ochazuke.Ctx, value bool, q triageQuery) ochazuke.Result {
	if q.before < 0 {
		return ochazuke.Failed(&ochazuke.ArgumentError{Err: fmt.Errorf("--before: not a time (2026-11-14, 2026-11-14T09:00, RFC 3339 or Unix ms)")})
	}
	ids, err := a.triageIDs(q)
	if err != nil {
		return ochazuke.Failed(err)
	}
	stamp := a.now().UnixMilli()
	n, err := a.contexts.UpdateAll(ids, func(it *contextitems.Item) {
		if it.Dismissed == value {
			return
		}
		it.Dismissed = value
		it.DismissedAt = 0
		if value {
			it.DismissedAt = stamp
		}
	})
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.reloadContext(); err != nil {
		return ochazuke.Failed(err)
	}
	verb, back := "Dismissed", fmt.Sprintf("mtm context-item undismiss --filter dismissed-at=%d", stamp)
	if !value {
		verb, back = "Undismissed", ""
	}
	ctx.Notify(fmt.Sprintf("%s %d context items", verb, n))
	out := map[string]any{"count": n, "dismissed": value}
	if value {
		out["dismissed-at"], out["undo"] = stamp, back
	}
	return ochazuke.Done(out)
}

// triageIDs are the keys of the items the query selects, every project's:
// first kept before q.before, and meeting q.filter (the list verb's own
// filter, so the words are the list's).
func (a *App) triageIDs(q triageQuery) ([]string, error) {
	args := ochazuke.Arguments{"all": true, "archived": true, "limit": 0}
	if q.filter != "" {
		args["filter"] = q.filter
	}
	res, err := a.r.Execute(ochazuke.Request{Noun: contextNoun, Verb: "list", Args: args})
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		var page struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("context-item list: %w", err)
		}
		rows = page.Items
	}
	var ids []string
	for _, row := range rows {
		id, _ := row["id"].(string)
		created, _ := row["created"].(float64)
		if id == "" || q.before > 0 && int64(created) >= q.before {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// Needs you -----------------------------------------------------------------

// An item is in Needs you by the panel's one membership rule
// (needsYouItem: needsYouCategory, an open item in a queue category);
// every other item is resolved. Both the rail and the Items page draw the
// two as two tables: Needs you oldest first (panes.sort-clock), resolved
// newest first, near-duplicates folded to one row with a count.

// needsYouItem reports whether the item is a Needs-you row: open and in a
// category panes.queue-order lists, as the panel counts it.
func (a *App) needsYouItem(it actionitems.Item, now int64, ranks map[string]int) bool {
	if !it.Open(now) {
		return false
	}
	c := a.needsYouCategory(it, a.oneRule())
	_, ok := ranks[c]
	return c != "" && ok
}

// actionRail is one action-item section of the rail: the pane's Needs-you
// items (needs) or the rest (resolved).
type actionRail struct {
	prop, desc string
	needs      bool
}

// actionRails are the rail's action-item sections.
var actionRails = []actionRail{
	{prop: actionNeedsPane, desc: "its Needs-you items (the panel's rule), oldest first, near-duplicates folded: the rail's Needs you", needs: true},
	{prop: actionResolvedPane, desc: "its other items (read, archived, snoozed), newest first, near-duplicates folded: the rail's Resolved"},
}

func actionRailOf(prop string) (actionRail, bool) {
	i := slices.IndexFunc(actionRails, func(r actionRail) bool { return r.prop == prop })
	if i < 0 {
		return actionRail{}, false
	}
	return actionRails[i], true
}

// actionRailProps are the filter-only props of actionRails and the fold
// count.
func actionRailProps() []ochazuke.PropSpec {
	var out []ochazuke.PropSpec
	for _, r := range actionRails {
		out = append(out, ochazuke.PropSpec{Name: r.prop, ReadOnly: true, Parameter: ochazuke.Parameter{Type: "string", Noun: "pane",
			Description: "Filter only (--where " + r.prop + "=PANE): " + r.desc}})
	}
	out = append(out,
		ochazuke.PropSpec{Name: "folded", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "integer",
			Description: "On a folded row (the rail, the Items page grouped by needs-you): how many near-duplicate items (same pane, source, kind and label) it stands for"}},
		ochazuke.PropSpec{Name: inboxGroupOrderProp, ReadOnly: true, Parameter: ochazuke.Parameter{Type: "integer",
			Description: "The row's place in its Items group when the grouping orders its rows (needs-you: oldest first, then newest first)"}})
	return out
}

// actionRailColumns are the action-item table's columns of the rail props:
// never pushed (Match answers false), so the list folds them.
func actionRailColumns() map[string]tableColumn {
	out := map[string]tableColumn{}
	for _, r := range actionRails {
		out[r.prop] = tableColumn{Match: func(string) (store.QueryCond, bool) { return store.QueryCond{}, false }}
	}
	return out
}

// needsYouTables splits items into the Needs-you rows and the resolved
// rows, each folded and ordered.
func (a *App) needsYouTables(items []actionitems.Item) (needs, resolved []actionFold) {
	now, ranks := a.inbox.items.Now(), a.queueRanks()
	var in, out []actionitems.Item
	for _, it := range items {
		if a.needsYouItem(it, now, ranks) {
			in = append(in, it)
		} else {
			out = append(out, it)
		}
	}
	clock := func(it actionitems.Item) int64 { return a.itemClock(it, pane{}, false, now) }
	return foldActionItems(in, a.actionLabel, clock, false), foldActionItems(out, a.actionLabel, func(it actionitems.Item) int64 { return it.Created }, true)
}

// foldThing is a fold as its row: the newest item's thing with the count
// and the folded label.
func (a *App) foldThing(f actionFold, now int64) ochazuke.Thing {
	t := a.actionItemThing(f.item)
	t["folded"] = f.count
	t["title"] = a.railActionTitle(f, now)
	return t
}

// actionRailThings answers a list request that names a rail prop: the
// pane's items of that table, folded and ordered; false when the request
// names none.
func (a *App) actionRailThings(q ochazuke.ListQuery) ([]ochazuke.Thing, bool, error) {
	var rail actionRail
	window := ""
	for _, f := range q.Filters {
		if r, ok := actionRailOf(f.Prop); ok && (f.Op == "" || f.Op == "=") {
			rail, window = r, fmt.Sprint(f.Value)
			break
		}
	}
	if rail.prop == "" {
		return nil, false, nil
	}
	// The store, not the cache: the rail reads on a change, which may come
	// before the cache reloads.
	items, err := a.inboxItems(false)
	if err != nil {
		return nil, true, err
	}
	items = slices.DeleteFunc(slices.Clone(items), func(it actionitems.Item) bool { return it.Window != window })
	needs, resolved := a.needsYouTables(items)
	folds := resolved
	if rail.needs {
		folds = needs
	}
	now := a.inbox.items.Now()
	var out []ochazuke.Thing
	for _, f := range folds {
		t := a.foldThing(f, now)
		t[rail.prop] = window
		out = append(out, t)
	}
	return out, true, nil
}

// actionFold is a row of near-duplicate items: the newest stands for them
// (its verbs act on it); at is the time the row sorts by (its first
// item's when oldest first, its newest's when newest first).
type actionFold struct {
	item   actionitems.Item
	oldest int64
	at     int64
	count  int
}

// foldActionItems folds items of the same pane, source, kind and label
// into one row each, ordered by clock: oldest first by each row's first
// item, or newest first by its newest.
func foldActionItems(items []actionitems.Item, label func(actionitems.Item) string, clock func(actionitems.Item) int64, newest bool) []actionFold {
	var folds []actionFold
	at := map[string]int{}
	for _, it := range items {
		key := it.Window + "\x00" + it.Source + "\x00" + it.Kind + "\x00" + label(it)
		c := clock(it)
		i, ok := at[key]
		if !ok {
			at[key] = len(folds)
			folds = append(folds, actionFold{item: it, oldest: it.Created, at: c, count: 1})
			continue
		}
		f := &folds[i]
		f.count++
		f.oldest = min(f.oldest, it.Created)
		if newest {
			f.at = max(f.at, c)
		} else {
			f.at = min(f.at, c)
		}
		if it.Created > f.item.Created || it.Created == f.item.Created && it.ID > f.item.ID {
			f.item = it
		}
	}
	slices.SortStableFunc(folds, func(x, y actionFold) int {
		if newest {
			return cmp.Compare(y.at, x.at)
		}
		return cmp.Compare(x.at, y.at)
	})
	return folds
}

// railActionTitle is a folded row's label: what the item asks, the count
// of the items it folds, and its age (the first one's, when folded).
func (a *App) railActionTitle(f actionFold, now int64) string {
	label := a.actionLabel(f.item)
	if f.count > 1 {
		label += fmt.Sprintf(" ×%d", f.count)
	}
	return label + " · " + ago(time.Duration(now-f.oldest)*time.Millisecond)
}

// paneTitledKinds are the kinds (source/kind) whose items are titled with
// their pane's name (the pane signals and command items): the title says
// which pane, never what the item asks, so the label reads past it.
var paneTitledKinds = func() []string {
	var out []string
	for _, kind := range append(slices.Clone(panesignals.Kinds), panesignals.Command) {
		out = append(out, panesignals.Source+"/"+kind)
	}
	return out
}()

// actionLabel is an item's descriptive label: what it is and what it asks
// (its title, else why it is here, else its URL), never just the name of
// its pane, which the for column and the rail's pane already say.
func (a *App) actionLabel(it actionitems.Item) string {
	title := it.Title
	if title != "" && (title == it.PaneLabel || slices.Contains(paneTitledKinds, it.Source+"/"+it.Kind)) {
		title = ""
	}
	return cmp.Or(title, it.Why, it.URL, it.Label())
}

// withColumns is cols with more's columns added.
func withColumns(cols, more map[string]tableColumn) map[string]tableColumn {
	for k, v := range more {
		cols[k] = v
	}
	return cols
}

// inboxFolds reports whether a list request is grouped by an Items
// grouping that folds (inbox.group needs-you): the store's List answers
// it, folded and ordered.
func (a *App) inboxFolds(q ochazuke.ListQuery) bool {
	return a.inboxGrouping().fold && slices.Contains(q.GroupBy, inboxGroupProp)
}

// inboxItems are every action item: with cached, the inbox's domain cache
// (read once per write, reloadInbox) when it is loaded, so a folded list
// reads no table; else the store.
func (a *App) inboxItems(cached bool) ([]actionitems.Item, error) {
	if cached && a.inbox.all != nil {
		return a.inbox.all, nil
	}
	return a.inbox.items.All()
}
