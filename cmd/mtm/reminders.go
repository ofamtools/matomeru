package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/reminders"
	"ofam.dev/matomeru/internal/work"
)

const (
	reminderNoun         = "reminder"
	reminderListID       = "schedules/reminders"
	reminderFormID       = "reminder-form"
	settingRemindersShow = "reminders.show"
)

type reminderState struct {
	store  *reminders.Store
	list   *ochazuke.Collection
	date   string
	notify *reminderNotification
}

var reminderColumns = []ochazuke.Column{{Title: "reminder", Min: 14}, {Title: "time"}, {Title: "days", Priority: 2}, {Title: "today"}, {Title: "last done", Priority: 3}}

func reminderProps() []ochazuke.PropSpec {
	text := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "string", Description: desc} }
	return []ochazuke.PropSpec{
		{Name: "id", Key: true, Parameter: text("The reminder's stable key")},
		{Name: "title", Role: ochazuke.RoleLabel, Multiline: true, Parameter: text("Any reminder text")},
		{Name: "time", Default: "10:30", Parameter: text("Local HH:MM, 00:00 through 23:59")},
		{Name: "days", Default: "sun,mon,tue,wed,thu,fri,sat", Parameter: text("Comma-separated sun,mon,tue,wed,thu,fri,sat; daily for all, none for no days")},
		{Name: "enabled", Default: true, Parameter: ochazuke.Parameter{Type: "boolean", Description: "Whether notifications and scheduled days are enabled"}},
		{Name: "archived", Role: ochazuke.RoleArchive, Parameter: ochazuke.Parameter{Type: "boolean", Description: "Keep the reminder and its history, but remove it from the active list"}},
		{Name: "due-today", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "boolean", Description: "Scheduled on today's local date"}},
		{Name: "done-today", ReadOnly: true, Parameter: ochazuke.Parameter{Type: "boolean", Description: "Checked on today's local date"}},
		{Name: "last-done", ReadOnly: true, Parameter: text("The latest checked local date, empty when none")},
	}
}

func (a *App) openReminders() error {
	s, err := reminders.Open(a.db)
	if err != nil {
		return err
	}
	a.reminders = &reminderState{store: s, notify: newReminderNotifications()}
	return nil
}

func (a *App) reminderNow() time.Time { return a.now().In(a.location()) }

func (a *App) defineReminders() {
	n := reminderNoun
	a.r.Define(n).Props(reminderProps()...).Store(a.reminderStore()).Columns(reminderColumns...).
		Verb("add", a.addReminder, "Record a reminder and open its form; CLI uses reminder create").
		Verb("edit", a.editReminder, "Edit the reminder's title, local time, days and enabled state").Positional("name").ShadowsStandard().
		Verb("check", a.checkReminder("check"), "Check the reminder for today, or --date YYYY-MM-DD").Positional("name").
		Verb("uncheck", a.checkReminder("uncheck"), "Uncheck the reminder for today, or --date YYYY-MM-DD").Positional("name").
		Verb("toggle", a.checkReminder("toggle"), "Toggle the reminder for today, or --date YYYY-MM-DD").Positional("name").
		Verb("history", a.reminderHistory, "Daily history, newest first: done, missed or not scheduled").Positional("name").Fetch().
		Mode("normal").Bind("x", ochazuke.Do(n+".toggle").Hinted("check/uncheck")).
		Bind("enter", ochazuke.Do(n+".history").Hinted("history")).Bind("E", ochazuke.Do(n+".edit").Hinted("edit")).
		Bind("n", ochazuke.Do(n+".add").Hinted("add reminder")).Bind("d d", ochazuke.Do(n+".archive").Hinted("archive"))
	name := ochazuke.Parameter{Type: "string", Noun: n, Description: "Exact reminder title or id; omitted, the selected reminder"}
	for _, v := range []string{"edit", "check", "uncheck", "toggle", "history"} {
		p := map[string]ochazuke.Parameter{"name": name}
		if v == "check" || v == "uncheck" || v == "toggle" {
			p["date"] = ochazuke.Parameter{Type: "string", Description: "Local YYYY-MM-DD; default today; a past date corrects history"}
		}
		if v == "history" {
			p["limit"] = ochazuke.Parameter{Type: "integer", Description: "Maximum days; default all days since creation"}
		}
		a.parameters(n+"."+v, p)
	}
	a.ownedBy(n, reminderListID)
}

func (a *App) reminderStore() ochazuke.Store {
	return ochazuke.Store{
		Kind: ochazuke.KindCollection,
		Get: func(_ ochazuke.Ctx, id string) (ochazuke.Thing, error) {
			r, err := a.reminderNamed(id)
			if err != nil {
				return nil, err
			}
			return a.reminderThing(r)
		},
		List: func(_ ochazuke.Ctx, _ ochazuke.ListQuery) ([]ochazuke.Thing, error) {
			rs, err := a.reminders.store.All()
			if err != nil {
				return nil, err
			}
			out := []ochazuke.Thing{}
			for _, r := range rs {
				v, err := a.reminderThing(r)
				if err != nil {
					return nil, err
				}
				out = append(out, v)
			}
			return out, nil
		},
		Create: func(_ ochazuke.Ctx, values ochazuke.Thing) (ochazuke.Thing, error) {
			r, err := a.reminders.store.Write(work.NewID(), a.reminderNow(), func(r *reminders.Reminder, rule *reminders.Rule) error { return applyReminder(r, rule, values) })
			if err != nil {
				return nil, err
			}
			if err := a.refreshReminders(); err != nil {
				return nil, err
			}
			return a.reminderThing(r)
		},
		Patch: func(_ ochazuke.Ctx, id string, values ochazuke.Thing) (ochazuke.Thing, error) {
			r, err := a.reminderNamed(id)
			if err != nil {
				return nil, err
			}
			r, err = a.reminders.store.Write(r.ID, a.reminderNow(), func(r *reminders.Reminder, rule *reminders.Rule) error { return applyReminder(r, rule, values) })
			if err != nil {
				return nil, err
			}
			if err := a.refreshReminders(); err != nil {
				return nil, err
			}
			return a.reminderThing(r)
		},
		Delete: func(_ ochazuke.Ctx, id string) error {
			r, err := a.reminderNamed(id)
			if err != nil {
				return err
			}
			if err := a.reminders.store.Delete(r.ID); err != nil {
				return err
			}
			return a.refreshReminders()
		},
	}
}

func applyReminder(r *reminders.Reminder, rule *reminders.Rule, values ochazuke.Thing) error {
	if v, ok := values["title"].(string); ok {
		r.Title = v
	}
	if v, ok := values["time"].(string); ok {
		if err := reminders.CheckTime(v); err != nil {
			return err
		}
		rule.Time = v
	}
	if v, ok := values["days"].(string); ok {
		days, err := reminders.ParseDays(v)
		if err != nil {
			return err
		}
		rule.Days = days
	}
	if v, ok := values["enabled"].(bool); ok {
		rule.Enabled = v
	}
	if v, ok := values["archived"].(bool); ok {
		rule.Archived = v
	}
	return nil
}

func (a *App) reminderThing(r reminders.Reminder) (ochazuke.Thing, error) {
	rule, now := r.Current(), a.reminderNow()
	d, last, err := a.reminders.store.Summary(r.ID, now.Format(time.DateOnly))
	if err != nil {
		return nil, err
	}
	return ochazuke.Thing{"id": r.ID, "title": r.Title, "time": rule.Time, "days": strings.Join(rule.Days, ","), "enabled": rule.Enabled, "archived": rule.Archived,
		"due-today": r.On(now.Format(time.DateOnly)).Due(now), "done-today": d.Done, "last-done": last}, nil
}

func (a *App) reminderNamed(name string) (reminders.Reminder, error) {
	name = strings.TrimPrefix(name, "reminder:")
	if r, err := a.reminders.store.Get(name); err == nil {
		return r, nil
	}
	rs, err := a.reminders.store.All()
	if err != nil {
		return reminders.Reminder{}, err
	}
	var matches []reminders.Reminder
	for _, r := range rs {
		if r.Title == name {
			matches = append(matches, r)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return reminders.Reminder{}, fmt.Errorf("several reminders named %q; use the id from reminder list", name)
	}
	return reminders.Reminder{}, fmt.Errorf("no reminder %q; reminder list shows ids", name)
}
func (a *App) reminderAt(c ochazuke.Ctx) (reminders.Reminder, error) {
	name := ochazuke.StringArg(c.Args, "name", "")
	if name == "" && c.Instance != nil && c.Instance.Noun == reminderNoun {
		name = c.Instance.ID
	}
	if name == "" {
		return reminders.Reminder{}, errors.New("select a reminder or give its title/id")
	}
	return a.reminderNamed(name)
}

func (a *App) checkReminder(verb string) ochazuke.AppHandler {
	return func(c ochazuke.Ctx) ochazuke.Result {
		r, err := a.reminderAt(c)
		if err != nil {
			return ochazuke.Failed(err)
		}
		now := a.reminderNow()
		date := ochazuke.StringArg(c.Args, "date", now.Format(time.DateOnly))
		d, err := a.reminders.store.Check(r.ID, date, verb, now, c.Actor())
		if err != nil {
			return ochazuke.Failed(err)
		}
		if err := a.refreshReminders(); err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(d)
	}
}

func (a *App) addReminder(c ochazuke.Ctx) ochazuke.Result {
	if a.cli {
		return ochazuke.Failed(errors.New("use reminder create TITLE --time HH:MM --days mon,wed,fri"))
	}
	out, err := a.r.Execute(ochazuke.Request{Noun: reminderNoun, Verb: "create", Args: ochazuke.Arguments{"title": ""}})
	if err != nil {
		return ochazuke.Failed(err)
	}
	thing, err := ochazuke.ThingOf(out)
	if err != nil {
		return ochazuke.Failed(err)
	}
	r, err := a.reminderNamed(fmt.Sprint(thing["id"]))
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.r.SetFocus("reminder:" + r.ID); err != nil {
		return ochazuke.Failed(err)
	}
	return resultError(a.openReminderForm(r, "New reminder"))
}
func (a *App) editReminder(c ochazuke.Ctx) ochazuke.Result {
	if a.cli {
		return ochazuke.Failed(errors.New("use reminder set --id ID time=10:30 days=mon,wed,fri"))
	}
	r, err := a.reminderAt(c)
	if err != nil {
		return ochazuke.Failed(err)
	}
	return resultError(a.openReminderForm(r, "Edit reminder"))
}
func (a *App) openReminderForm(r reminders.Reminder, title string) error {
	var fields []ochazuke.RecordField
	for _, p := range reminderProps() {
		if !slices.Contains([]string{"title", "time", "days", "enabled"}, p.Name) {
			continue
		}
		f := ochazuke.PropField(p)
		f.Validate = func(text string) error {
			if f.Name == "time" {
				return reminders.CheckTime(text)
			}
			if f.Name == "days" {
				_, err := reminders.ParseDays(text)
				return err
			}
			return nil
		}
		fields = append(fields, f)
	}
	_, err := a.r.OpenRecordEditor(reminderFormID, ochazuke.RecordSpec{
		Title: title + " · written as you type", Fields: fields, Debounce: a.editDebounce(), Insert: true,
		Load: func() (ochazuke.Record, error) {
			r, err := a.reminders.store.Get(r.ID)
			if err != nil {
				return nil, err
			}
			rule := r.Current()
			on := "false"
			if rule.Enabled {
				on = "true"
			}
			return ochazuke.Record{"title": r.Title, "time": rule.Time, "days": strings.Join(rule.Days, ","), "enabled": on}, nil
		},
		Save: func(w ochazuke.RecordWrite) ochazuke.Result {
			changes := map[string]any{}
			for _, f := range w.Changed {
				changes[f] = w.Values[f]
			}
			return resultOf(a.r.Execute(ochazuke.Request{Noun: reminderNoun, Verb: "set", Args: ochazuke.Arguments{"id": r.ID, "props": changes}}))
		},
	})
	return err
}

func (a *App) reminderHistory(c ochazuke.Ctx) ochazuke.Result {
	r, err := a.reminderAt(c)
	if err != nil {
		return ochazuke.Failed(err)
	}
	limit := ochazuke.IntArg(c.Args, "limit", 0)
	if limit < 0 {
		return ochazuke.Failed(errors.New("limit must not be negative"))
	}
	hs, err := a.reminders.store.History(r.ID, a.reminderNow(), limit)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if !a.cli {
		rows := make([]ochazuke.PickerRow, 0, len(hs))
		for _, h := range hs {
			rows = append(rows, ochazuke.PickerRow{ID: h.Date, Label: h.Date + " · " + h.State + " · " + h.ScheduledTime, Data: h})
		}
		_, err = a.r.OpenPicker(ochazuke.PickerSpec{Kind: "reminder-history", Title: "Reminder history · " + r.Title, Rows: rows, NoSort: true,
			Preview: func(row ochazuke.PickerRow) string {
				d := row.Data.(reminders.HistoryDay)
				return fmt.Sprintf("%s\n%s\nDone at: %s\nActor: %+v\nNotified: %s", d.Date, d.State, d.DoneAt, d.Actor, d.NotifiedAt)
			},
			Choose: func(row ochazuke.PickerRow) ochazuke.Result { return ochazuke.Done(row.Data) },
		})
		if err != nil {
			return ochazuke.Failed(err)
		}
	}
	return ochazuke.Done(hs)
}

func (a *App) addRemindersList() error {
	var err error
	a.reminders.list, err = a.r.AddList(reminderListID, nil, ochazuke.Parent(schedulesPage), ochazuke.RowNoun(reminderNoun), ochazuke.Columns(reminderColumns...))
	if err != nil {
		return err
	}
	row := ochazuke.Do(reminderNoun + ".add").Hinted("add reminder")
	row.Instance, row.Mode, row.Keys = reminderListID, "normal", "n"
	if err := a.r.Bind(row); err != nil {
		return err
	}
	a.r.Settings().Observe(settingRemindersShow, func(string, any) {
		if err := a.refreshReminders(); err != nil {
			a.r.UI().Error(err.Error())
		}
	})
	a.r.Settings().Observe(settingReminderInApp, func(string, any) { a.syncReminderNotices() })
	return a.refreshReminders()
}

func (a *App) refreshReminders() error {
	st := a.reminders
	if st == nil || st.list == nil {
		return nil
	}
	rows := []ochazuke.ListItem{}
	if a.r.Settings().Bool(settingRemindersShow) {
		rs, err := st.store.All()
		if err != nil {
			return err
		}
		for _, r := range rs {
			if r.Current().Archived {
				continue
			}
			v, err := a.reminderThing(r)
			if err != nil {
				return err
			}
			mark, state := "☐ ", "not scheduled"
			if v["due-today"].(bool) {
				state = "due"
			}
			if v["done-today"].(bool) {
				mark, state = "☑ ", "done"
			}
			if !r.Current().Enabled {
				state = "disabled"
			}
			label := mark + strings.Join(strings.Fields(r.Title), " ")
			if r.Title == "" {
				label = mark + "untitled"
			}
			item := ochazuke.ListItem{ID: "reminder:" + r.ID, Noun: reminderNoun, Label: label, Data: v, Props: v, Cells: []string{label, r.Current().Time, v["days"].(string), state, v["last-done"].(string)}}
			if state == "due" {
				item.Cells[3] = "! due"
				item.Tones = []ochazuke.ToneSpan{{Cell: 0, Tone: ochazuke.ToneNeedsInput}, {Cell: 3, Tone: ochazuke.ToneNeedsInput}}
			}
			rows = append(rows, item)
		}
	}
	st.date = a.reminderNow().Format(time.DateOnly)
	if err := st.list.SetItems(rows); err != nil {
		return err
	}
	a.syncReminderNotices()
	return nil
}

func (a *App) renderReminders(w, h int) string {
	lines := []string{a.pageStyles().Muted.Render("Reminders · daily checkoffs (local dates)")}
	lines = append(lines, a.view(a.reminders.list, w, max(1, h-len(lines))))
	return strings.Join(lines, "\n")
}
