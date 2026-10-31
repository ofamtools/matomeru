package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/schedules"
)

// The schedule noun's record side (standard verbs phase 1, lane M3c): its
// props and its store, so schedule get, list, create, set (alias patch),
// delete and watch are ochazuke's standard verbs over the rows as they are
// now (the built-ins and schedules.json, the stored rows over them by
// name, a row's settings applied).
//
// The key is name, so the subject is --name (schedule set --name gh
// enabled=off). Where a write lands is the row's: a field (prompt, when,
// the agent's, the command's) is written to the row's stored copy (a
// built-in or file row gets one, as edit always did); enabled and interval
// are the row's settings (schedule.NAME.enabled, .interval) for a
// configured row and its own fields for a stored one, as enable, disable
// and set-interval always did.
//
//   - show is a lasting alias of get (its recent runs are the read-only
//     runs prop); enable and disable are aliases of set of enabled;
//   - edit with fields is set (without, the form); toggle and set-interval
//     run set (set-interval's chooser too);
//   - add stays named (its --name, the form it opens with no fields),
//     beside the standard create; duplicate stays named (a copy is off and
//     takes --as), as do history, run-now and reset.
//
// A schedule has no label prop (its name is the key), so no rename: a
// stored row's settings and runs are kept by name.

// scheduleIntervalProp is the interval's prop: a duration, not one of
// edit's fields (set-interval writes it).
const scheduleIntervalProp = "interval"

// scheduleProps are a schedule's props: the key; the fields edit writes
// (strings, as its parameters are; enabled one of on, off, auto); the
// interval; and the read-only source and state.
func (a *App) scheduleProps() []ochazuke.PropSpec {
	params := a.scheduleParams()
	props := []ochazuke.PropSpec{{Name: "name", Key: true, Parameter: ochazuke.Parameter{Type: "string",
		Description: "The schedule's name (letters, digits, dashes): its key"}}}
	for _, f := range scheduleFields {
		p := params[f]
		param := ochazuke.Parameter{Type: "string", Source: p.Source, Description: p.Description}
		if f == "enabled" {
			param.Enum = schedules.EnabledValues
		}
		props = append(props, ochazuke.PropSpec{Name: f, Multiline: f == "prompt", Parameter: param})
	}
	ro := func(name, typ, desc string) ochazuke.PropSpec {
		return ochazuke.PropSpec{Name: name, ReadOnly: true, Parameter: ochazuke.Parameter{Type: typ, Description: desc}}
	}
	return append(props,
		ochazuke.PropSpec{Name: scheduleIntervalProp, Parameter: ochazuke.Parameter{Type: ochazuke.SettingDuration,
			Description: "How often it runs (at least 1m; 0 for a row timed by the clock); a stored or clock-timed row's interval replaces its timing"}},
		ro("source", "string", "Where the row comes from: builtin, file (schedules.json) or stored (by mtm)"),
		ro("active", "boolean", "Whether it runs: on, or auto with its checks passing"),
		ro("why", "string", "Why an auto row does not run"),
		ro("running", "boolean", "Whether a run is under way"),
		ro("last-run", "integer", "When it last ran (Unix ms)"),
		ro("last-outcome", "string", "How the last run ended"),
		ro("last-error", "string", "The last run's error"),
		ro("failures", "integer", "Failures in a row (they back off the interval)"),
		ro("next-run", "integer", "When it is due next (Unix ms; 0 when it does not run)"),
		ro("missed", "integer", "Runs missed while mtm was closed"),
		ro("runs", "list", "Its recent runs, newest first (get only)"),
	)
}

// scheduleStore is the schedule noun's store over the rows. history.undo
// may revert its writes (Undo); an undone delete puts the stored row back
// as it was (AcceptsKey, scheduleForgotten).
func (a *App) scheduleStore() ochazuke.Store {
	return ochazuke.Store{
		Kind: ochazuke.Collection,
		Undo: true, AcceptsKey: true,
		Get: func(_ ochazuke.Ctx, name string) (ochazuke.Thing, error) {
			d, err := a.scheduleNamed(name)
			if err != nil {
				return nil, err
			}
			t, err := a.scheduleThing(d)
			if err != nil {
				return nil, err
			}
			runs, err := a.sched.runner.History(d.Name, historyShown)
			if err != nil {
				return nil, err
			}
			t["runs"] = recordsAsList(runs)
			return t, nil
		},
		List: func(ochazuke.Ctx, ochazuke.ListQuery) ([]ochazuke.Thing, error) {
			out := []ochazuke.Thing{}
			for _, d := range a.scheduleDefs() {
				t, err := a.scheduleThing(d)
				if err != nil {
					return nil, err
				}
				out = append(out, t)
			}
			return out, nil
		},
		Create: a.createSchedule,
		Patch:  a.patchSchedule,
		Delete: a.removeSchedule,
	}
}

// scheduleNamed is the row of that name as it is now.
func (a *App) scheduleNamed(name string) (schedules.Def, error) {
	return a.scheduleAt(ochazuke.Ctx{Args: ochazuke.Arguments{"name": name}})
}

// recordsAsList is runs as a list prop's value: each record its JSON
// object.
func recordsAsList(runs []schedules.Record) []any {
	out := make([]any, 0, len(runs))
	for _, rec := range runs {
		raw, err := json.Marshal(rec)
		if err != nil {
			continue
		}
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

// scheduleThing is a row as its props, its state now.
func (a *App) scheduleThing(d schedules.Def) (ochazuke.Thing, error) {
	states, err := a.sched.runner.States()
	if err != nil {
		return nil, err
	}
	v := a.viewSchedule(d, states[d.Name], a.scheduleNow())
	t := ochazuke.Thing{"name": d.Name}
	for _, f := range scheduleFields {
		t[f] = scheduleField(d, f)
	}
	t["when"] = d.When().Text() // "never" for none, which edit reads back
	interval := time.Duration(0)
	if !d.When().Calendar() {
		interval = d.Every()
	}
	t[scheduleIntervalProp] = interval.String()
	for name, value := range map[string]any{"source": d.Source, "active": v.Active, "why": v.Why, "running": v.Running,
		"last-run": v.LastRun, "last-outcome": v.LastOutcome, "last-error": v.LastError, "failures": v.Failures,
		"next-run": v.NextRun, "missed": v.Missed} {
		t[name] = value
	}
	return t, nil
}

// scheduleFieldsOf are the schedule fields values gives, in form order,
// each as its parameter's text (enabled and the interval apart).
func scheduleFieldsOf(values ochazuke.Thing) []string {
	var out []string
	for _, name := range scheduleFields {
		if _, ok := values[name]; ok && name != "enabled" {
			out = append(out, name)
		}
	}
	return out
}

// applyScheduleValues sets d's fields from values (enabled and the
// interval are not fields: scheduleEnable and scheduleEvery write them).
func (a *App) applyScheduleValues(d *schedules.Def, values ochazuke.Thing) error {
	for _, name := range scheduleFieldsOf(values) {
		if err := a.applyScheduleField(d, name, argText(values[name])); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// newSchedule is a new stored row from values: a prompt row unless it has
// a command and no prompt; on when it has a time, else off (so a
// half-filled row never fires), unless enabled is given; named name, else
// after its prompt or command.
func (a *App) newSchedule(values ochazuke.Thing, name string) (schedules.Def, error) {
	d := schedules.Def{Kind: schedules.KindPrompt, Enabled: schedules.Off}
	if err := a.applyScheduleValues(&d, values); err != nil {
		return d, err
	}
	if _, given := values["kind"]; !given && len(d.Command) > 0 && d.Prompt == "" {
		d.Kind = schedules.KindCommand
	}
	if v, ok := values[scheduleIntervalProp]; ok {
		every, err := scheduleInterval(v)
		if err != nil {
			return d, err
		}
		if every > 0 {
			d.SetWhen(schedules.When{Interval: schedules.Duration(every)})
		}
	}
	if v, ok := values["enabled"]; ok {
		d.Enabled = argText(v)
		if !slices.Contains(schedules.EnabledValues, d.Enabled) {
			return d, fmt.Errorf("enabled is one of %s", strings.Join(schedules.EnabledValues, ", "))
		}
	} else if !d.When().Zero() {
		d.Enabled = schedules.On
	}
	if name == "" {
		name = a.freeScheduleName(scheduleSlug(cmp.Or(d.Prompt, strings.Join(d.Command, " "))))
	}
	d.Name = name
	return d, nil
}

// scheduleInterval reads an interval value: a duration or its text.
func scheduleInterval(v any) (time.Duration, error) {
	switch v := v.(type) {
	case time.Duration:
		return v, nil
	case nil:
		return 0, nil
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	if s == "" || s == "0" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("interval %q: %w", s, err)
	}
	return d, nil
}

// createSchedule is the store's Create: a stored row from the props given,
// named after its prompt or command. The name of a row this session
// deleted puts it back as it was (history.undo of a delete).
func (a *App) createSchedule(_ ochazuke.Ctx, values ochazuke.Thing) (ochazuke.Thing, error) {
	name, _ := thingString(values, "name")
	if _, taken := a.scheduleDef(name); taken {
		name = ""
	}
	d, restored := a.scheduleForgotten[name]
	if !restored {
		var err error
		if d, err = a.newSchedule(values, name); err != nil {
			return nil, err
		}
	}
	delete(a.scheduleForgotten, name)
	if err := a.saveSchedule(d); err != nil {
		return nil, err
	}
	d, err := a.scheduleNamed(d.Name)
	if err != nil {
		return nil, err
	}
	return a.scheduleThing(d)
}

// patchSchedule is the store's Patch: the fields to the row's stored copy
// (edit's), then enabled and the interval where the row keeps them
// (enable's and set-interval's).
func (a *App) patchSchedule(_ ochazuke.Ctx, name string, changes ochazuke.Thing) (ochazuke.Thing, error) {
	d, err := a.scheduleNamed(name)
	if err != nil {
		return nil, err
	}
	changes = sameTiming(changes)
	if len(scheduleFieldsOf(changes)) > 0 {
		if err := a.applyScheduleValues(&d, changes); err != nil {
			return nil, err
		}
		if err := a.saveSchedule(d); err != nil {
			return nil, err
		}
		if d, err = a.scheduleNamed(name); err != nil {
			return nil, err
		}
	}
	if v, ok := changes["enabled"]; ok {
		if err := a.scheduleEnable(d, argText(v)); err != nil {
			return nil, err
		}
		if d, err = a.scheduleNamed(name); err != nil {
			return nil, err
		}
	}
	if v, ok := changes[scheduleIntervalProp]; ok {
		every, err := scheduleInterval(v)
		if err != nil {
			return nil, err
		}
		if err := a.scheduleEvery(d, every); err != nil {
			return nil, err
		}
	}
	if err := a.refreshSchedules(); err != nil {
		return nil, err
	}
	if d, err = a.scheduleNamed(name); err != nil {
		return nil, err
	}
	return a.scheduleThing(d)
}

// sameTiming drops when from changes that also give the interval when
// both say the same timing: they are two views of one value, and the
// interval writes it where the row keeps it (a configured row's setting),
// where when would make a stored copy. Undo of a set sends both, since it
// restores every prop the write changed.
func sameTiming(changes ochazuke.Thing) ochazuke.Thing {
	text, hasWhen := changes["when"]
	every, hasInterval := changes[scheduleIntervalProp]
	if !hasWhen || !hasInterval {
		return changes
	}
	w, err := schedules.ParseWhen(argText(text))
	d, err2 := scheduleInterval(every)
	if err != nil || err2 != nil || w.Calendar() || time.Duration(w.Interval) != d {
		return changes
	}
	out := maps.Clone(changes)
	delete(out, "when")
	return out
}

// scheduleEnable turns a row on, off or auto where its value lives: a
// stored row's own field (a setting set for it is reset), a configured
// row's setting.
func (a *App) scheduleEnable(d schedules.Def, value string) error {
	if !slices.Contains(schedules.EnabledValues, value) {
		return fmt.Errorf("enabled is one of %s", strings.Join(schedules.EnabledValues, ", "))
	}
	if d.Source == schedules.SourceStored {
		d.Enabled = value
		if _, declared := a.r.Settings().Get(scheduleEnabledSetting(d.Name)); declared == nil {
			_ = a.r.Settings().Reset(scheduleEnabledSetting(d.Name))
		}
		return a.saveSchedule(d)
	}
	return a.r.Settings().Set(scheduleEnabledSetting(d.Name), value)
}

// scheduleEvery sets how often a row runs: a stored row, or one timed by
// the clock, holds its timing itself (the interval replaces it); a
// configured interval row's is its interval setting.
func (a *App) scheduleEvery(d schedules.Def, every time.Duration) error {
	if every < time.Minute {
		return errors.New("an interval is at least 1m")
	}
	if d.Source == schedules.SourceStored || d.When().Calendar() {
		d.SetWhen(schedules.When{Interval: schedules.Duration(every)})
		return a.saveSchedule(d)
	}
	return a.r.Settings().Set(scheduleIntervalSetting(d.Name), every.String())
}

// removeSchedule is the store's Delete (schedule.delete, d d; the standard
// delete asks first): the stored row goes (a built-in or file row it
// overrode comes back). Built-in and file rows are refused: their source
// is configuration (schedule disable turns one off). The stored row is
// kept for this session, so history.undo puts it back as it was.
func (a *App) removeSchedule(c ochazuke.Ctx, name string) error {
	d, err := a.scheduleNamed(name)
	if err != nil {
		return err
	}
	if d.Source != schedules.SourceStored {
		return fmt.Errorf("%s is %s (%s): schedule disable %s turns it off", d.Name, d.Source, sourceWhere(d.Source), d.Name)
	}
	for _, s := range a.sched.stored {
		if s.Name == d.Name {
			d = s
		}
	}
	if err := a.sched.runner.Delete(d.Name); err != nil {
		return err
	}
	if a.scheduleForgotten == nil {
		a.scheduleForgotten = map[string]schedules.Def{}
	}
	a.scheduleForgotten[d.Name] = d
	if err := a.reloadStoredSchedules(); err != nil {
		return err
	}
	c.Info("deleted schedule " + d.Name)
	return nil
}

// scheduleSet runs the standard set of changes on the row name.
func (a *App) scheduleSet(name string, changes map[string]any) ochazuke.Result {
	return resultOf(a.r.Execute(ochazuke.Request{Noun: scheduleNoun, Verb: "set", Args: ochazuke.Arguments{"name": name, "changes": changes}}))
}
