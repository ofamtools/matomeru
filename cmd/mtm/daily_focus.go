package main

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// The day's focus (daily-focus plan, lane focus-2-mtm; owner decisions of
// 2026-11-13: the rail only, no ticker; carry unfinished; its own mark,
// status untouched; = on a task row and Z for the picker; a finished focus
// stays in today's list, flagged). A focus is a focus-item row naming a
// thing (noun:key) and the local day it was picked for (work.FocusItem).
// Today's list is computed on read (work.FocusDay): nothing runs at
// midnight and nothing reads per frame. The things that can be focused
// are a table (focusables), not a branch per noun.

const (
	focusItemNoun     = "focus-item"
	settingFocusCarry = "focus.carry"
	focusPickerName   = "focus"
	focusFilterName   = "focus"
)

// focusSettings are the focus.* settings.
func focusSettings() []ochazuke.Setting {
	return []ochazuke.Setting{{Name: settingFocusCarry, Type: ochazuke.SettingString, Default: work.FocusCarryUnfinished, Enum: work.FocusCarries,
		Description: "What an earlier day's focus does on a later day: unfinished (what you did not finish stays, after the day's own picks) or none (each day starts empty; earlier days stay readable with focus-item list --date)"}}
}

// focusable is a row noun whose things can be focused: the record noun
// the mark names and how a row's id reads as that record's key.
type focusable struct {
	Noun, Record string
	Key          func(row string) string
}

// focusables are the nouns + and task.focus act on. Adding one (an Inbox
// item) is one row here and one in focusRecords.
func focusables() []focusable {
	same := func(id string) string { return id }
	return []focusable{
		{Noun: "task", Record: "task", Key: same},
		{Noun: outlineTaskNoun, Record: "task", Key: taskOf},
	}
}

// focusRecord reads a focused record: its label, whether it is finished
// (and the local day it last changed) and the verb that opens it.
type focusRecord struct {
	Label func(key string) (string, error)
	State func(key string) work.FocusState
	Open  string
}

// focusRecords are the record nouns a focus names, by noun.
func (a *App) focusRecords() map[string]focusRecord {
	return map[string]focusRecord{
		"task": {
			Label: func(key string) (string, error) {
				t, err := a.taskByID(key)
				return t.Title, err
			},
			State: func(key string) work.FocusState {
				t, err := a.work.Task(key)
				if err != nil {
					return work.FocusState{}
				}
				return a.taskFocusState(t)
			},
			Open: "task.open",
		},
	}
}

// taskFocusState is a task as a focus sees it: finished when its status
// closes it (tasks.statuses) or it is archived.
func (a *App) taskFocusState(t work.Task) work.FocusState {
	if !t.Archived && !a.work.StatusClosed(t.Status) {
		return work.FocusState{}
	}
	return work.FocusState{Finished: true, Day: a.localDay(time.UnixMilli(t.Updated))}
}

// localDay is t's local calendar date.
func (a *App) localDay(t time.Time) string { return t.In(a.location()).Format(time.DateOnly) }

// focusToday is today's local date.
func (a *App) focusToday() string { return a.localDay(a.now()) }

// focusDate reads a verb's --date (default today).
func (a *App) focusDate(args ochazuke.Arguments) (string, error) {
	d := strings.TrimSpace(ochazuke.StringArg(args, "date", ""))
	if d == "" {
		return a.focusToday(), nil
	}
	if _, err := time.Parse(time.DateOnly, d); err != nil {
		return "", &ochazuke.ArgumentError{Err: fmt.Errorf("date %q is not YYYY-MM-DD", d)}
	}
	return d, nil
}

// splitThing reads noun:key; a bare key is a task's.
func splitThing(thing string) (noun, key string) {
	if n, k, ok := strings.Cut(thing, ":"); ok {
		return n, k
	}
	return "task", thing
}

// focusState is a thing's state through its record row; a thing of a noun
// with no row, or one that cannot be read, is unfinished.
func (a *App) focusState(records map[string]focusRecord) func(string) work.FocusState {
	return func(thing string) work.FocusState {
		noun, key := splitThing(thing)
		if r, ok := records[noun]; ok {
			return r.State(key)
		}
		return work.FocusState{}
	}
}

// focusList is day's focus list (work.FocusDay with focus.carry).
func (a *App) focusList(day string, state func(string) work.FocusState) ([]work.FocusEntry, error) {
	rows, err := a.work.FocusItems()
	if err != nil {
		return nil, err
	}
	carry := a.r.Settings().String(settingFocusCarry) != work.FocusCarryNone
	return work.FocusDay(rows, day, carry, state), nil
}

// focusOrders are today's focused things, by thing, with their places;
// the tasks given are read from the list (no read per task), any other
// thing through its record row.
func (a *App) focusOrders(tasks []work.Task) map[string]int {
	known := make(map[string]work.FocusState, len(tasks))
	for _, t := range tasks {
		known["task:"+t.ID] = a.taskFocusState(t)
	}
	base := a.focusState(a.focusRecords())
	list, err := a.focusList(a.focusToday(), func(thing string) work.FocusState {
		if st, ok := known[thing]; ok {
			return st
		}
		return base(thing)
	})
	if err != nil {
		return nil
	}
	out := make(map[string]int, len(list))
	for _, e := range list {
		out[e.Thing] = e.Order
	}
	return out
}

// markFocus sets a task thing's derived focused and focus-order.
func markFocus(thing ochazuke.Thing, orders map[string]int) ochazuke.Thing {
	id, _ := thing["id"].(string)
	order, ok := orders["task:"+id]
	thing["focused"], thing["focus-order"] = ok, order
	return thing
}

// focusTaskProps are the task's derived focus props (read-only, as
// made-by is).
func focusTaskProps() []ochazuke.PropSpec {
	return []ochazuke.PropSpec{
		{Name: "focused", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "boolean", Description: "Whether the task is in today's focus (= or task.focus; focus.carry keeps an unfinished earlier one)"}},
		{Name: "focus-order", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "integer", Description: "The task's place in today's focus, from 1; 0 when it is not focused"}},
	}
}

// focusFilter is the task's saved filter of today's focus, in order.
func focusFilter() ochazuke.SavedFilter {
	return ochazuke.SavedFilter{Name: focusFilterName, Where: map[string]any{"focused": true}, Sort: []ochazuke.SortKey{{Field: "focus-order"}}}
}

func focusProps() []ochazuke.PropSpec {
	text := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "string", Description: desc} }
	integer := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "integer", Description: desc} }
	boolean := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "boolean", Description: desc} }
	return []ochazuke.PropSpec{
		{Name: "id", Key: true, Parameter: text("The focus's key")},
		{Name: "label", Role: ochazuke.RoleLabel, ReadOnly: true, Parameter: text("The focused thing's label (a task's title)")},
		{Name: "thing", Parameter: text("The focused thing as noun:key (task:ID); a bare key is a task")},
		{Name: "day", Parameter: text("The local YYYY-MM-DD it was focused for; default today")},
		{Name: "position", Parameter: integer("Its place among the day's own focus; default last")},
		{Name: "order", ReadOnly: true, Parameter: integer("Its place in the day's list, from 1 (carried ones after the day's own)")},
		{Name: "carried", ReadOnly: true, Parameter: boolean("An earlier day's unfinished focus shown by focus.carry")},
		{Name: "finished", ReadOnly: true, Parameter: boolean("The thing is finished (a closed status or archived); it stays in the list of the day it was finished")},
		{Name: "archived", Role: ochazuke.RoleArchive, Parameter: boolean("Unfocused: kept, and out of the day's list")},
		{Name: "created", ReadOnly: true, Parameter: integer("When it was focused (Unix ms)")},
		{Name: "created-by", ReadOnly: true, Parameter: text("Who focused it: owner or agent")},
		{Name: "changed-by", ReadOnly: true, Parameter: text("Who wrote it last")},
	}
}

var focusColumns = []ochazuke.Column{{Title: "label", Min: 20}, {Title: "order"}, {Title: "day", Priority: 2}, {Title: "finished", Priority: 1}}

// focusThing is an entry as its props.
func (a *App) focusThing(e work.FocusEntry, records map[string]focusRecord) ochazuke.Thing {
	label := e.Thing
	if noun, key := splitThing(e.Thing); records[noun].Label != nil {
		if l, err := records[noun].Label(key); err == nil {
			label = l
		}
	}
	return ochazuke.Thing{"id": e.ID, "label": label, "thing": e.Thing, "day": e.Day, "position": e.Position, "order": e.Order,
		"carried": e.Carried, "finished": e.Finished, "archived": e.Archived, "created": e.Created,
		"created-by": e.CreatedBy.Kind, "changed-by": e.ChangedBy.Kind}
}

func (a *App) focusStore() ochazuke.Store {
	return ochazuke.Store{
		Kind: ochazuke.KindCollection, Undo: true,
		ListParams: map[string]ochazuke.Parameter{
			"date": {Type: "string", Description: "Local YYYY-MM-DD; default today"},
			"all":  {Type: "boolean", Description: "Every focus row of every day, unfocused ones included, not the day's list"},
		},
		Get: func(_ ochazuke.Ctx, id string) (ochazuke.Thing, error) {
			it, err := a.work.FocusItem(id)
			if err != nil {
				return nil, fmt.Errorf("no focus %q", id)
			}
			records := a.focusRecords()
			st := a.focusState(records)(it.Thing)
			return a.focusThing(work.FocusEntry{FocusItem: it, Finished: st.Finished}, records), nil
		},
		List: func(_ ochazuke.Ctx, q ochazuke.ListQuery) ([]ochazuke.Thing, error) {
			records := a.focusRecords()
			if all, _ := q.Args["all"].(bool); all {
				rows, err := a.work.FocusItems()
				if err != nil {
					return nil, err
				}
				slices.SortFunc(rows, func(x, y work.FocusItem) int {
					return strings.Compare(x.Day+fmt.Sprintf("%09d", x.Position), y.Day+fmt.Sprintf("%09d", y.Position))
				})
				out := make([]ochazuke.Thing, 0, len(rows))
				for _, r := range rows {
					out = append(out, a.focusThing(work.FocusEntry{FocusItem: r}, records))
				}
				return out, nil
			}
			day, err := a.focusDate(q.Args)
			if err != nil {
				return nil, err
			}
			list, err := a.focusList(day, a.focusState(records))
			if err != nil {
				return nil, err
			}
			out := make([]ochazuke.Thing, 0, len(list))
			for _, e := range list {
				out = append(out, a.focusThing(e, records))
			}
			return out, nil
		},
		Create: func(_ ochazuke.Ctx, values ochazuke.Thing) (ochazuke.Thing, error) {
			thing, _ := thingString(values, "thing")
			noun, key := splitThing(strings.TrimSpace(thing))
			if key == "" {
				return nil, &ochazuke.ArgumentError{Err: errors.New("a focus needs a thing (noun:key)")}
			}
			day, _ := thingString(values, "day")
			if day, _ = a.focusDate(ochazuke.Arguments{"date": day}); day == "" {
				day = a.focusToday()
			}
			it := work.FocusItem{Thing: noun + ":" + key, Day: day, Position: int(thingInt(values["position"]))}
			if it.Position == 0 {
				rows, err := a.work.FocusItems()
				if err != nil {
					return nil, err
				}
				for _, r := range rows {
					if r.Day == day && !r.Archived {
						it.Position = max(it.Position, r.Position)
					}
				}
				it.Position++
			}
			if v, ok := values["archived"].(bool); ok {
				it.Archived = v
			}
			out, err := a.work.PutFocusItems(it)
			if err != nil {
				return nil, err
			}
			a.focusChanged()
			return a.focusThing(work.FocusEntry{FocusItem: out[0]}, a.focusRecords()), nil
		},
		Patch: func(_ ochazuke.Ctx, id string, changes ochazuke.Thing) (ochazuke.Thing, error) {
			it, err := a.work.FocusItem(id)
			if err != nil {
				return nil, fmt.Errorf("no focus %q", id)
			}
			if v, ok := changes["archived"].(bool); ok {
				it.Archived = v
			}
			if v, ok := changes["position"]; ok {
				it.Position = int(thingInt(v))
			}
			if v, ok := thingString(changes, "day"); ok && v != "" {
				it.Day = v
			}
			out, err := a.work.PutFocusItems(it)
			if err != nil {
				return nil, err
			}
			a.focusChanged()
			return a.focusThing(work.FocusEntry{FocusItem: out[0]}, a.focusRecords()), nil
		},
		Delete: func(_ ochazuke.Ctx, id string) error {
			if err := a.work.DeleteFocusItem(id); err != nil {
				return err
			}
			a.focusChanged()
			return nil
		},
	}
}

// focusChanged redraws what shows the focus: the task rows (focused and
// focus-order are theirs) and every watch of either noun.
func (a *App) focusChanged() {
	if err := a.refreshTasks(); err != nil {
		a.r.UI().Notify("focus: " + err.Error())
	}
	a.r.Changed(focusItemNoun, "task")
}

func (a *App) defineFocus() {
	if err := a.r.DeclareSetting(focusSettings()...); err != nil {
		panic(err)
	}
	n := focusItemNoun
	a.r.Define(n).Props(focusProps()...).Store(a.docStore(n, a.focusStore())).Columns(focusColumns...).
		About("The things picked as the day's focus (= on a task row, Z for the picker); today's list is computed on read, with focus.carry").
		Verb("add", a.focusAdd, "Focus a thing for the day (--date, default today), last in its list; with no thing, open the focus picker (Z)").Positional("thing").
		Verb("remove", a.focusRemove, "Unfocus a thing: archive its focus for the day and the earlier days it carried from (undo restores it)").Positional("thing").
		Verb("new", a.focusNew, "Add a task (under the pin, else unassociated) and focus it, as task.new --focus").Positional("title").
		Verb("move", a.focusMove, "Move a thing in the day's list: +N or -N places (+1 down, -1 up), or to place N from 1 (past the end is last)").Positional("thing", "to").
		Verb("clear", a.focusClear, "Unfocus every thing in the day's list (--date, default today); each focus is kept, archived").
		Verb("open", a.focusOpen, "Open a focused thing through its noun's open; with no thing, the current one (next and previous move it)").Positional("thing").
		Verb("next", a.focusStep(1), "Make the next thing in today's list the current one (wrapping) and answer it").Fetch().
		Verb("previous", a.focusStep(-1), "Make the previous thing in today's list the current one (wrapping) and answer it").Fetch()
	thing := ochazuke.Parameter{Type: "string", Description: "The thing as noun:key (task:ID); a bare key is a task's"}
	date := ochazuke.Parameter{Type: "string", Description: "Local YYYY-MM-DD; default today"}
	a.parameters(n+".add", map[string]ochazuke.Parameter{"thing": thing, "date": date})
	a.parameters(n+".remove", map[string]ochazuke.Parameter{"thing": thing, "date": date})
	a.parameters(n+".new", map[string]ochazuke.Parameter{"title": {Type: "string", Required: true, Description: "The new task's title"},
		"project": {Type: "string", Noun: "project", Source: "projects.all", Free: true, Description: "The project; omitted, the pin, else none (unassociated)"}})
	a.parameters(n+".move", map[string]ochazuke.Parameter{"thing": thing, "date": date,
		"to": {Type: "string", Required: true, Description: "+N or -N places, or a place N from 1 (1 is first; past the end is last)"}})
	a.parameters(n+".clear", map[string]ochazuke.Parameter{"date": date})
	a.parameters(n+".open", map[string]ochazuke.Parameter{"thing": thing})
	// The other side of the relation: focus and unfocus on every focusable
	// row noun, one handler with the row as the thing.
	for _, f := range focusables() {
		a.r.Define(f.Noun).Verb("focus", a.focusRow(f), "Focus the "+f.Record+" for the day, or unfocus a focused one (toggles without --value), as focus-item.add and remove").Many().Positional("id").
			Mode("normal").Bind("=", ochazuke.Do(f.Noun+".focus").Hinted("focus"))
		a.parameters(f.Noun+".focus", map[string]ochazuke.Parameter{
			"id":    {Type: "string", Noun: f.Record, Description: "The " + f.Record + "'s key; omitted, the row's (every selected row's)"},
			"value": {Type: "boolean", Description: "Set instead of toggling: true focuses, false unfocuses"}, "date": date})
		if err := a.r.AddActionAlias(ochazuke.ActionAlias{Alias: f.Noun + ".unfocus", Action: f.Noun + ".focus", Args: ochazuke.Arguments{"value": false},
			Note: f.Noun + ".focus with --value false"}); err != nil {
			panic(err)
		}
	}
	a.r.Define("app").Mode("normal").Bind("Z", ochazuke.Do(n+".add").Hinted("focus"))
}

// focusAt is the list entry of thing on day, if listed.
func (a *App) focusAt(day, thing string) (work.FocusEntry, bool, error) {
	list, err := a.focusList(day, a.focusState(a.focusRecords()))
	if err != nil {
		return work.FocusEntry{}, false, err
	}
	for _, e := range list {
		if e.Thing == thing {
			return e, true, nil
		}
	}
	return work.FocusEntry{}, false, nil
}

// normalThing is a thing argument as noun:key.
func normalThing(thing string) string {
	noun, key := splitThing(strings.TrimSpace(thing))
	if key == "" {
		return ""
	}
	return noun + ":" + key
}

// focusAdd is focus-item.add: a thing already in the day's list stays
// where it is; with no thing, the focus picker opens (never "no target").
func (a *App) focusAdd(c ochazuke.Ctx) ochazuke.Result {
	thing := normalThing(ochazuke.StringArg(c.Args, "thing", ""))
	if thing == "" {
		if a.cli {
			return ochazuke.Failed(&ochazuke.ArgumentError{Err: errors.New("name a thing (task:ID); Z opens the focus picker")})
		}
		return a.openNamedPicker(focusPickerName)
	}
	day, err := a.focusDate(c.Args)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return resultOf(a.setFocus(thing, day, true))
}

// setFocus focuses (on) or unfocuses a thing for day and answers its
// entry (nil once unfocused). Focusing creates a focus row (an undoable
// create); unfocusing archives every live row of the thing up to day, so
// no earlier day carries it back.
func (a *App) setFocus(thing, day string, on bool) (any, error) {
	e, listed, err := a.focusAt(day, thing)
	if err != nil {
		return nil, err
	}
	if on {
		if listed && !e.Carried {
			return a.focusThing(e, a.focusRecords()), nil
		}
		return a.r.Execute(ochazuke.Request{Noun: focusItemNoun, Verb: "create", Args: ochazuke.Arguments{"thing": thing, "day": day}})
	}
	rows, err := a.work.FocusItems()
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.Thing == thing && !r.Archived && r.Day <= day {
			if _, err := a.r.Execute(ochazuke.Request{Noun: focusItemNoun, Verb: "set", Args: ochazuke.Arguments{"id": r.ID, "props": map[string]any{"archived": true}}}); err != nil {
				return nil, err
			}
		}
	}
	return nil, nil
}

// focusRemove is focus-item.remove.
func (a *App) focusRemove(c ochazuke.Ctx) ochazuke.Result {
	thing := normalThing(ochazuke.StringArg(c.Args, "thing", ""))
	if thing == "" {
		return ochazuke.Failed(&ochazuke.ArgumentError{Err: errors.New("name a thing (task:ID); on a focus row, archive unfocuses it")})
	}
	day, err := a.focusDate(c.Args)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return resultOf(a.setFocus(thing, day, false))
}

// focusRow is NOUN.focus on a focusable row: toggles each subject's focus,
// or sets it with --value.
func (a *App) focusRow(f focusable) ochazuke.AppHandler {
	return func(c ochazuke.Ctx) ochazuke.Result {
		day, err := a.focusDate(c.Args)
		if err != nil {
			return ochazuke.Failed(err)
		}
		return c.Each(func(one ochazuke.Ctx) ochazuke.Result {
			id := ochazuke.StringArg(one.Args, "id", "")
			if id == "" && one.Instance != nil {
				id = one.Instance.ID
			}
			if id == "" {
				return ochazuke.Failed(&ochazuke.ArgumentError{Err: fmt.Errorf("no %s is selected", f.Noun)})
			}
			thing := f.Record + ":" + f.Key(id)
			on, set := one.Args["value"].(bool)
			if !set {
				e, listed, err := a.focusAt(day, thing)
				if err != nil {
					return ochazuke.Failed(err)
				}
				on = !listed || e.Archived
			}
			v, err := a.setFocus(thing, day, on)
			if err == nil && v == nil {
				v = map[string]any{"thing": thing, "focused": false}
			}
			return resultOf(v, err)
		})
	}
}

// focusNew is focus-item.new: task.new --focus.
func (a *App) focusNew(c ochazuke.Ctx) ochazuke.Result {
	args := ochazuke.Arguments{"title": ochazuke.StringArg(c.Args, "title", ""), "focus": true}
	if p := ochazuke.StringArg(c.Args, "project", ""); p != "" {
		args["project"] = p
	}
	return resultOf(a.r.Execute(ochazuke.Request{Noun: "task", Verb: "new", Args: args}))
}

// focusNewTask focuses a task task.new just made (--focus).
func (a *App) focusNewTask(c ochazuke.Ctx, id string) error {
	if on, _ := c.Args["focus"].(bool); !on {
		return nil
	}
	_, err := a.setFocus("task:"+id, a.focusToday(), true)
	return err
}

// focusMove is focus-item.move: the day's list is written in its new
// order as the day's own focus (a carried one becomes the day's).
func (a *App) focusMove(c ochazuke.Ctx) ochazuke.Result {
	day, err := a.focusDate(c.Args)
	if err != nil {
		return ochazuke.Failed(err)
	}
	thing := normalThing(ochazuke.StringArg(c.Args, "thing", ""))
	list, err := a.focusList(day, a.focusState(a.focusRecords()))
	if err != nil {
		return ochazuke.Failed(err)
	}
	from := slices.IndexFunc(list, func(e work.FocusEntry) bool { return e.Thing == thing })
	if from < 0 {
		return ochazuke.Failed(&ochazuke.ArgumentError{Err: fmt.Errorf("%s is not in the focus of %s", thing, day)})
	}
	to, err := focusPlace(ochazuke.StringArg(c.Args, "to", ""), from, len(list))
	if err != nil {
		return ochazuke.Failed(err)
	}
	moved := list[from]
	list = slices.Insert(slices.Delete(list, from, from+1), to, moved)
	var writes []work.FocusItem
	for i, e := range list {
		it := e.FocusItem
		if e.Carried {
			// The day's own row; the earlier one stays as it was.
			it = work.FocusItem{Thing: e.Thing, Day: day}
		}
		it.Position = i + 1
		writes = append(writes, it)
	}
	if _, err := a.work.PutFocusItems(writes...); err != nil {
		return ochazuke.Failed(err)
	}
	a.focusChanged()
	return ochazuke.Done(map[string]any{"thing": thing, "order": to + 1})
}

// focusPlace reads a move's to as an index into a list of n, the moved
// entry at from: +N or -N moves it relative (+1 down, -1 up), N puts it at
// place N from 1 (past the end is last). segment.move's words (first,
// last, up, down) are a private switch in ochazuke; the general form is an
// exported move vocabulary both verbs share (lane report).
func focusPlace(to string, from, n int) (int, error) {
	to = strings.TrimSpace(to)
	v, err := strconv.Atoi(to)
	if err != nil {
		return 0, &ochazuke.ArgumentError{Err: fmt.Errorf("to %q is not +N, -N or a place N", to)}
	}
	i := v - 1
	if strings.HasPrefix(to, "+") || strings.HasPrefix(to, "-") {
		i = from + v
	}
	return min(max(i, 0), n-1), nil
}

// focusClear is focus-item.clear: every row the day's list shows is
// archived, in one write.
func (a *App) focusClear(c ochazuke.Ctx) ochazuke.Result {
	day, err := a.focusDate(c.Args)
	if err != nil {
		return ochazuke.Failed(err)
	}
	list, err := a.focusList(day, a.focusState(a.focusRecords()))
	if err != nil {
		return ochazuke.Failed(err)
	}
	listed := map[string]bool{}
	for _, e := range list {
		listed[e.Thing] = true
	}
	rows, err := a.work.FocusItems()
	if err != nil {
		return ochazuke.Failed(err)
	}
	var writes []work.FocusItem
	for _, r := range rows {
		if listed[r.Thing] && !r.Archived && r.Day <= day {
			r.Archived = true
			writes = append(writes, r)
		}
	}
	if _, err := a.work.PutFocusItems(writes...); err != nil {
		return ochazuke.Failed(err)
	}
	a.focusChanged()
	return ochazuke.Done(map[string]any{"day": day, "cleared": len(list)})
}

// focusCurrent is today's current entry (next and previous move it).
func (a *App) focusCurrent(step int) (work.FocusEntry, bool, error) {
	list, err := a.focusList(a.focusToday(), a.focusState(a.focusRecords()))
	if err != nil || len(list) == 0 {
		return work.FocusEntry{}, false, err
	}
	a.focusCursor = ((a.focusCursor+step)%len(list) + len(list)) % len(list)
	return list[a.focusCursor], true, nil
}

// focusStep is focus-item.next (1) and previous (-1).
func (a *App) focusStep(step int) ochazuke.AppHandler {
	return func(ochazuke.Ctx) ochazuke.Result {
		e, ok, err := a.focusCurrent(step)
		if err != nil || !ok {
			return resultOf(nil, err)
		}
		return ochazuke.Done(a.focusThing(e, a.focusRecords()))
	}
}

// focusOpen is focus-item.open: the thing's noun's open verb.
func (a *App) focusOpen(c ochazuke.Ctx) ochazuke.Result {
	thing := normalThing(ochazuke.StringArg(c.Args, "thing", ""))
	if thing == "" {
		e, ok, err := a.focusCurrent(0)
		if err != nil {
			return ochazuke.Failed(err)
		}
		if !ok {
			a.r.UI().Info("nothing is focused today: = on a task row or Z focuses one")
			return ochazuke.Done(nil)
		}
		thing = e.Thing
	}
	noun, key := splitThing(thing)
	rec, ok := a.focusRecords()[noun]
	if !ok || rec.Open == "" {
		return ochazuke.Failed(fmt.Errorf("%s has no open verb", noun))
	}
	verb, _ := strings.CutPrefix(rec.Open, noun+".")
	return resultOf(a.r.Execute(ochazuke.Request{Noun: noun, Verb: verb, Target: key}))
}

// openFocusPicker is the focus picker (Z): today's focus first (marked),
// then the open tasks (the pinned project's first); enter toggles the
// highlighted task's focus and stays open; a typed title that matches
// nothing offers "+ focus new task".
func (a *App) openFocusPicker(*ochazuke.Runtime, string) (*ochazuke.Chooser, error) {
	var ch *ochazuke.Chooser
	refresh := func() {
		if ch != nil {
			ch.SetRows(ochazuke.ChoiceRows(a.focusChoices()))
		}
	}
	ch, err := a.choose(pickSpec{
		Kind: focusPickerName, Title: "Focus today", Noun: "task", NoSort: true, Stay: true,
		Choices: a.focusChoices(),
		Notice:  "no open tasks: type a title to focus a new one",
		Pick: func(id string) ochazuke.Result {
			_, key := splitThing(id)
			_, listed, err := a.focusAt(a.focusToday(), "task:"+key)
			if err == nil {
				_, err = a.setFocus("task:"+key, a.focusToday(), !listed)
			}
			refresh()
			return resultError(err)
		},
		Create: func(query string) ochazuke.Result {
			_, err := a.r.Execute(ochazuke.Request{Noun: focusItemNoun, Verb: "new", Args: ochazuke.Arguments{"title": strings.TrimSpace(query)}})
			refresh()
			return resultError(err)
		},
		CreateLabel: func(query string) string { return "+ focus new task: " + strings.TrimSpace(query) },
	})
	return ch, err
}

// focusGlyph marks a focused row in the picker.
const focusGlyph = "◎"

// focusChoices are the picker's rows: today's focus in order, then every
// open task, the pinned project's first, then by most recent change.
func (a *App) focusChoices() []ochazuke.Choice {
	tasks, err := a.work.Tasks("")
	if err != nil {
		a.r.UI().Error("tasks: " + err.Error())
	}
	tasks = a.liveTasks(tasks)
	byID := make(map[string]work.Task, len(tasks))
	for _, t := range tasks {
		byID[t.ID] = t
	}
	label := func(t work.Task) string {
		l := a.taskLabel(t)
		if t.ProjectID != "" {
			l += " · " + a.ownerLabel(t.ProjectID)
		}
		return l
	}
	var out []ochazuke.Choice
	orders := a.focusOrders(tasks)
	focused := make([]work.Task, 0, len(orders))
	for _, t := range tasks {
		if orders["task:"+t.ID] > 0 {
			focused = append(focused, t)
		}
	}
	slices.SortFunc(focused, func(x, y work.Task) int { return orders["task:"+x.ID] - orders["task:"+y.ID] })
	for _, t := range focused {
		out = append(out, ochazuke.Choice{ID: "task:" + t.ID, Label: focusGlyph + " " + label(t), RowFlags: ochazuke.RowFlags{Bare: true}})
	}
	rest := slices.DeleteFunc(slices.Clone(tasks), func(t work.Task) bool { return orders["task:"+t.ID] > 0 || a.work.StatusClosed(t.Status) })
	pinned := func(t work.Task) bool { return a.pin != "" && t.ProjectID == a.pin }
	slices.SortStableFunc(rest, func(x, y work.Task) int {
		return cmp.Or(cmpBool(pinned(y), pinned(x)), cmp.Compare(y.Updated, x.Updated))
	})
	for _, t := range rest {
		out = append(out, ochazuke.Choice{ID: "task:" + t.ID, Label: label(t), RowFlags: ochazuke.RowFlags{Bare: true}})
	}
	return out
}
