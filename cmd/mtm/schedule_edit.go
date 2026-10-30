package main

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/drive"
	"github.com/unixsuperhero/matomeru/internal/harness"
	"github.com/unixsuperhero/matomeru/internal/schedules"
)

// Adding and editing schedules: schedule add, edit, duplicate and delete
// write the schedule_defs table (internal/schedules/defs.go), whose rows
// merge over the built-ins and schedules.json by name. Each field is a
// parameter of schedule.edit, and the TUI's form is those parameters
// (scheduleFormFields), written as they change: add records the row first
// (off, so a half-filled row never fires) and opens the form on it.

const scheduleFormID = "schedule-form"

// scheduleFields are the editable fields in the form's order: the
// parameters add and edit share, by name.
var scheduleFields = []string{
	"prompt", "when", "enabled", "harness", "model", "effort", "fast", "permission-mode",
	"project", "task", "dir", "into", "pane", "start", "end", "catch-up", "description",
	"kind", "command", "drive", "timeout",
}

// scheduleParams are add's and edit's parameters (edit's name is the row's).
func (a *App) scheduleParams() map[string]ochazuke.Parameter {
	settingChoices := func(name string) []string {
		var out []string
		for _, h := range a.harnesses.List() {
			for _, v := range h.Choices(name) {
				if !slices.Contains(out, v) {
					out = append(out, v)
				}
			}
		}
		return out
	}
	return map[string]ochazuke.Parameter{
		"prompt":          {Type: "string", Description: "The prompt the agent is given each run (a prompt schedule)"},
		"when":            {Type: "string", Description: "When it runs: every 30m, daily 09:00, mon,wed,fri 09:00,17:30, weekdays 8:30, cron 0 9 * * 1-5, or never"},
		"enabled":         {Type: "string", Enum: schedules.EnabledValues, Description: "on, off, or auto (when its command is installed and its checks pass)"},
		"harness":         {Type: "string", Enum: a.harnesses.Names(), Description: "The harness profile (harnesses.json); empty: the project's, else default-harness"},
		"model":           {Type: "string", Source: "harness.models", Free: true, Description: "The model; empty: the harness's first"},
		"effort":          {Type: "string", Enum: settingChoices(harness.SettingEffort), Free: true, Description: "Thinking effort at launch (the harness's effort setting)"},
		"fast":            {Type: "string", Enum: []string{"on", "off"}, Free: true, Description: "Fast mode at launch: on or off"},
		"permission-mode": {Type: "string", Enum: settingChoices(harness.SettingPermission), Free: true, Description: "Permission mode at launch (the harness's permission-mode setting)"},
		"project":         {Type: "string", Source: "projects.list", Free: true, Description: "The project the agent works in (its session and directory); empty: no project"},
		"task":            {Type: "string", Free: true, Description: "A task id the pane is linked to (its title is the prompt when there is none)"},
		"dir":             dirParam("The working folder; empty: the project's directory, else home", false),
		"into":            {Type: "string", Enum: schedules.TargetValues, Description: "Where each run goes: new, a new pane; pane, send to the pane named by pane when it is ready (a new one when none is open)"},
		"pane":            {Type: "string", Free: true, Description: "The pane's name: the pane a target pane run sends to, and the name of a pane a run opens (default: the schedule's name)"},
		"start":           {Type: "string", Free: true, Description: "First day it may run: 2026-11-01 (or an RFC 3339 time); empty: now"},
		"end":             {Type: "string", Free: true, Description: "Last day it may run: 2026-12-31 (inclusive); empty: no end"},
		"catch-up":        {Type: "string", Enum: schedules.CatchUpValues, Free: true, Description: "Runs missed while mtm was closed: none (skip) or once; empty: the schedules.catch-up setting"},
		"description":     {Type: "string", Free: true, Description: "What it is for, shown on its row"},
		"kind":            {Type: "string", Enum: []string{schedules.KindPrompt, schedules.KindCommand}, Description: "prompt (start an agent) or command (run a command); default: from what is given"},
		"command":         {Type: "string", Free: true, Description: "A command schedule's command line (words split on spaces)"},
		"drive":           {Type: "string", Enum: drive.Values, Description: "How a command row runs: exec (default: a plain process, output kept with the run), terminal (a pane opened running it) or api (reserved: not available yet)"},
		"timeout":         {Type: "string", Free: true, Description: "How long a run may take: 2m (default), 10m, …"},
	}
}

// applyScheduleField sets one field of d from its parameter's text.
func (a *App) applyScheduleField(d *schedules.Def, name, v string) error {
	v = strings.TrimSpace(v)
	switch name {
	case "prompt":
		d.Prompt = v
	case "when":
		w, err := schedules.ParseWhen(v)
		if err != nil {
			return err
		}
		d.SetWhen(w)
	case "enabled":
		if !slices.Contains(schedules.EnabledValues, v) {
			return fmt.Errorf("enabled is one of %s", strings.Join(schedules.EnabledValues, ", "))
		}
		d.Enabled = v
	case "harness":
		if v != "" {
			if _, err := a.harnesses.Lookup(v); err != nil {
				return err
			}
		}
		d.Agent.Harness = v
	case "model":
		d.Agent.Model = v
	case "effort":
		d.Agent.Effort = v
	case "fast":
		if v != "" && v != "on" && v != "off" {
			return errors.New("fast is on or off")
		}
		d.Agent.Fast = v
	case "permission-mode":
		d.Agent.Permission = v
	case "project":
		d.Agent.Project = strings.TrimPrefix(v, "project:")
	case "task":
		d.Agent.Task = strings.TrimPrefix(v, "task:")
	case "dir":
		d.Agent.Dir = v
	case "into":
		if v != "" && !slices.Contains(schedules.TargetValues, v) {
			return fmt.Errorf("target is one of %s", strings.Join(schedules.TargetValues, ", "))
		}
		d.Agent.Target = v
	case "pane":
		d.Agent.Pane = v
	case "start", "end":
		if _, err := schedules.ParseBound(v, name == "end", a.location()); err != nil {
			return err
		}
		if name == "start" {
			d.Start = v
		} else {
			d.End = v
		}
	case "catch-up":
		if v != "" && !slices.Contains(schedules.CatchUpValues, v) {
			return fmt.Errorf("catch-up is one of %s", strings.Join(schedules.CatchUpValues, ", "))
		}
		d.CatchUp = v
	case "description":
		d.Description = v
	case "kind":
		d.Kind = v
	case "command":
		d.Command = strings.Fields(v)
	case "drive":
		if err := drive.Check(v); err != nil {
			return err
		}
		d.Drive = v
	case "timeout":
		if v == "" {
			d.Timeout = 0
			break
		}
		t, err := time.ParseDuration(v)
		if err != nil || t < time.Second {
			return fmt.Errorf("timeout %q: a duration such as 2m", v)
		}
		d.Timeout = schedules.Duration(t)
	default:
		return fmt.Errorf("schedules have no field %q", name)
	}
	return nil
}

// scheduleField is one field of d as its parameter's text (what the form
// loads and edit compares).
func scheduleField(d schedules.Def, name string) string {
	switch name {
	case "prompt":
		return d.Prompt
	case "when":
		if d.When().Zero() {
			return ""
		}
		return d.When().Text()
	case "enabled":
		return d.Enabled
	case "harness":
		return d.Agent.Harness
	case "model":
		return d.Agent.Model
	case "effort":
		return d.Agent.Effort
	case "fast":
		return d.Agent.Fast
	case "permission-mode":
		return d.Agent.Permission
	case "project":
		return d.Agent.Project
	case "task":
		return d.Agent.Task
	case "dir":
		return d.Agent.Dir
	case "into":
		return d.Agent.Target
	case "pane":
		return d.Agent.Pane
	case "start":
		return d.Start
	case "end":
		return d.End
	case "catch-up":
		return d.CatchUp
	case "description":
		return d.Description
	case "kind":
		return d.Kind
	case "command":
		return strings.Join(d.Command, " ")
	case "drive":
		return d.Drive
	case "timeout":
		if d.Timeout == 0 {
			return ""
		}
		return scheduleSpan(time.Duration(d.Timeout))
	}
	return ""
}

// givenFields are the schedule fields a request names, in form order.
func givenFields(args ochazuke.Arguments) []string {
	var out []string
	for _, name := range scheduleFields {
		if _, ok := args[name]; ok {
			out = append(out, name)
		}
	}
	return out
}

// applyScheduleArgs sets every field the request names.
func (a *App) applyScheduleArgs(d *schedules.Def, args ochazuke.Arguments) error {
	for _, name := range givenFields(args) {
		if err := a.applyScheduleField(d, name, argText(args[name])); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

func argText(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case nil:
		return ""
	case bool:
		if v {
			return "on"
		}
		return "off"
	}
	return fmt.Sprint(v)
}

// freeScheduleName is base, else base-2, base-3, …: a name no row has.
func (a *App) freeScheduleName(base string) string {
	taken := map[string]bool{}
	for _, d := range a.scheduleDefs() {
		taken[d.Name] = true
	}
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		if n := fmt.Sprintf("%s-%d", base, i); !taken[n] {
			return n
		}
	}
}

// scheduleSlug is a name from words: the first few, lowercased, dashed.
func scheduleSlug(text string) string {
	var words []string
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		words = append(words, w)
		if len(words) == 4 {
			break
		}
	}
	if len(words) == 0 {
		return "prompt"
	}
	return strings.Join(words, "-")
}

// saveSchedule stores d and rebuilds the rows.
func (a *App) saveSchedule(d schedules.Def) error {
	if err := a.sched.runner.Save(d); err != nil {
		return err
	}
	return a.reloadStoredSchedules()
}

// reloadStoredSchedules rereads the stored rows (another mtm, or the CLI,
// may have written them) and rebuilds the page.
func (a *App) reloadStoredSchedules() error {
	stored, err := a.sched.runner.Stored()
	if err != nil {
		return err
	}
	a.sched.stored = stored
	return a.refreshSchedules()
}

// addSchedule is schedule.add: a stored row from the fields given (on when
// it has a time, else off), recorded at once. In the TUI with no fields
// it records an empty prompt row, off, and opens the form on it.
func (a *App) addSchedule(ctx ochazuke.Ctx) ochazuke.Result {
	given := givenFields(ctx.Args)
	d := schedules.Def{Kind: schedules.KindPrompt, Enabled: schedules.Off}
	if err := a.applyScheduleArgs(&d, ctx.Args); err != nil {
		return ochazuke.Failed(err)
	}
	if !slices.Contains(given, "kind") && len(d.Command) > 0 && d.Prompt == "" {
		d.Kind = schedules.KindCommand
	}
	if !slices.Contains(given, "enabled") && !d.When().Zero() {
		d.Enabled = schedules.On
	}
	name := strings.TrimPrefix(ochazuke.StringArg(ctx.Args, "name", ""), "schedule:")
	if name == "" {
		name = a.freeScheduleName(scheduleSlug(cmp.Or(d.Prompt, strings.Join(d.Command, " "))))
	} else if _, taken := a.scheduleDef(name); taken {
		return ochazuke.Failed(fmt.Errorf("a schedule named %s exists: schedule edit %s changes it, schedule duplicate %s copies it", name, name, name))
	}
	d.Name = name
	if err := a.saveSchedule(d); err != nil {
		return ochazuke.Failed(err)
	}
	if !a.cli && len(given) == 0 {
		if err := a.openScheduleForm(name, "New schedule "+name); err != nil {
			return ochazuke.Failed(err)
		}
		ctx.Info("recorded " + name + " (off); fill it in, then turn it on")
	}
	return a.scheduleAnswer(d)
}

// editSchedule is schedule.edit: the fields given, the standard set (a
// field is written to the row's stored copy, a built-in or file row
// getting one; enabled where the row keeps it). With none, the TUI opens
// the form.
func (a *App) editSchedule(ctx ochazuke.Ctx) ochazuke.Result {
	d, err := a.scheduleAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if len(givenFields(ctx.Args)) == 0 {
		if a.cli {
			return ochazuke.Failed(fmt.Errorf("give the fields to change: --%s …", strings.Join(scheduleFields[:4], ", --")))
		}
		return resultError(a.openScheduleForm(d.Name, "Edit schedule "+d.Name))
	}
	changes := map[string]any{}
	for _, name := range givenFields(ctx.Args) {
		changes[name] = argText(ctx.Args[name])
	}
	return a.scheduleSet(d.Name, changes)
}

// duplicateSchedule is schedule.duplicate: a stored copy named --as (else
// NAME-copy), off, its history empty.
func (a *App) duplicateSchedule(ctx ochazuke.Ctx) ochazuke.Result {
	d, err := a.scheduleAt(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	name := ochazuke.StringArg(ctx.Args, "as", "")
	if name == "" {
		name = a.freeScheduleName(d.Name + "-copy")
	} else if _, taken := a.scheduleDef(name); taken {
		return ochazuke.Failed(fmt.Errorf("a schedule named %s exists", name))
	}
	d.Name, d.Enabled, d.Created, d.Updated = name, schedules.Off, 0, 0
	if err := a.saveSchedule(d); err != nil {
		return ochazuke.Failed(err)
	}
	return a.scheduleAnswer(d)
}

func sourceWhere(source string) string {
	switch source {
	case schedules.SourceFile:
		return "in " + scheduleFile
	case schedules.SourceBuiltin:
		return "shipped with mtm"
	}
	return "stored by mtm"
}

// openScheduleForm opens the record editor on the stored row name, its
// fields schedule.edit's parameters; every change runs schedule.edit.
func (a *App) openScheduleForm(name, title string) error {
	if a.r.Instances[scheduleFormID] != nil {
		return errors.New("a schedule form is already open")
	}
	op := a.r.Operations[a.r.CanonicalKey(scheduleNoun+".edit")]
	var fields []ochazuke.RecordField
	for _, f := range scheduleFields {
		p, ok := op.Parameters[f]
		if !ok {
			continue
		}
		field := ochazuke.RecordField{Name: f, Parameter: p, Multiline: f == "prompt"}
		field.Validate = func(v string) error {
			var probe schedules.Def
			return a.applyScheduleField(&probe, f, v)
		}
		fields = append(fields, field)
	}
	_, err := a.r.OpenRecordEditor(scheduleFormID, ochazuke.RecordSpec{
		Title:    title + " · written as you type",
		Fields:   fields,
		Debounce: a.editDebounce(),
		Insert:   true,
		Load: func() (ochazuke.Record, error) {
			d, ok := a.scheduleDef(name)
			if !ok {
				return nil, fmt.Errorf("schedule %s is gone", name)
			}
			rec := ochazuke.Record{}
			for _, f := range scheduleFields {
				rec[f] = scheduleField(d, f)
			}
			return rec, nil
		},
		Save: func(w ochazuke.RecordWrite) ochazuke.Result {
			args := ochazuke.Arguments{"name": name}
			for _, f := range w.Changed {
				args[f] = w.Values[f]
			}
			out, err := a.r.Execute(ochazuke.Request{Noun: scheduleNoun, Verb: "edit", Args: args})
			if err != nil {
				return ochazuke.Failed(err)
			}
			return ochazuke.Done(out)
		},
	})
	return err
}
