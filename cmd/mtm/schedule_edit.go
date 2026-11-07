package main

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/drive"
	"ofam.dev/matomeru/internal/harness"
	"ofam.dev/matomeru/internal/schedules"
)

// Adding and editing schedules: schedule add, edit, duplicate and delete
// write the schedule_defs table (internal/schedules/defs.go), whose rows
// merge over the built-ins and schedules.json by name. Each field is a
// parameter of schedule.edit, and the TUI's form is those parameters
// (scheduleFormFields), written as they change: add records the row first
// (off, so a half-filled row never fires) and opens the form on it.

const scheduleFormID = "schedule-form"

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
		"enabled":         {Type: "string", Enum: scheduleChoices("enabled"), Description: "on, off, or auto (when its command is installed and its checks pass)"},
		"harness":         {Type: "string", Enum: a.harnesses.Names(), Description: "The harness profile (harnesses.json); empty: the project's, else default-harness"},
		"model":           {Type: "string", Source: "harness.models", SourceArgs: map[string]string{"harness": "harness"}, Free: true, Description: "The model; empty: the harness's first"},
		"effort":          {Type: "string", Enum: settingChoices(harness.SettingEffort), Free: true, Description: "Thinking effort at launch (the harness's effort setting)"},
		"fast":            {Type: "string", Enum: scheduleChoices("fast"), Free: true, Description: "Fast mode at launch: on or off"},
		"permission-mode": {Type: "string", Enum: settingChoices(harness.SettingPermission), Free: true, Description: "Permission mode at launch (the harness's permission-mode setting)"},
		"project":         {Type: "string", Noun: "project", Source: "projects.all", Free: true, Description: "The project the agent works in (its session and directory); empty: no project"},
		"task":            {Type: "string", Free: true, Description: "A task id the pane is linked to (its title is the prompt when there is none)"},
		"dir":             dirParam("The working folder; empty: the project's directory, else home", false),
		"into":            {Type: "string", Enum: scheduleChoices("into"), Description: "Where each run goes: new, a new pane; pane, send to the pane named by pane when it is ready (a new one when none is open)"},
		"pane":            {Type: "string", Free: true, Description: "The pane's name: the pane a target pane run sends to, and the name of a pane a run opens (default: the schedule's name)"},
		"start":           {Type: "string", Free: true, Description: "First day it may run: 2026-11-01 (or an RFC 3339 time); empty: now"},
		"end":             {Type: "string", Free: true, Description: "Last day it may run: 2026-12-31 (inclusive); empty: no end"},
		"catch-up":        {Type: "string", Enum: scheduleChoices("catch-up"), Free: true, Description: "Runs missed while mtm was closed: none (skip) or once; empty: the schedules.catch-up setting"},
		"interval-from":   {Type: "string", Enum: scheduleChoices("interval-from"), Free: true, Description: "Where an interval counts from: start (the last run's start, the default) or finish (its finish, so a slow run does not shorten the gap); empty: start"},
		"description":     {Type: "string", Free: true, Description: "What it is for, shown on its row"},
		"kind":            {Type: "string", Enum: []string{schedules.KindPrompt, schedules.KindCommand}, Description: "prompt (start an agent) or command (run a command); default: from what is given"},
		"command":         {Type: "string", Free: true, Description: "A command schedule's command line (words split on spaces)"},
		"drive":           {Type: "string", Enum: drive.Values, Description: "How a command row runs: exec (default: a plain process, output kept with the run), terminal (a pane opened running it) or api (reserved: not available yet)"},
		"timeout":         {Type: "string", Free: true, Description: "How long a run may take: 2m (default), 10m, …"},
	}
}

// scheduleFieldSpec is one editable field as data: where it lives in the
// Def (str, a plain string), the choices its text must be one of (empty
// text passes unless strict), a check run first (a lookup or a parse), and
// set/get for the few fields that are more than a string.
type scheduleFieldSpec struct {
	name    string
	str     func(*schedules.Def) *string
	trim    string // a prefix stripped from the text (a typed reference)
	choices []string
	label   string // the name in the choices error (default: the field's)
	msg     string // the whole choices error, when it is not "X is one of …"
	strict  bool   // empty text is not a choice either
	check   func(a *App, v string) error
	set     func(a *App, d *schedules.Def, v string) error
	get     func(d schedules.Def) string
}

func checkHarness(a *App, v string) error {
	if v == "" {
		return nil
	}
	_, err := a.harnesses.Lookup(v)
	return err
}

func checkBound(end bool) func(a *App, v string) error {
	return func(a *App, v string) error {
		_, err := schedules.ParseBound(v, end, a.location())
		return err
	}
}

func checkDrive(_ *App, v string) error { return drive.Check(v) }

func setWhen(_ *App, d *schedules.Def, v string) error {
	w, err := schedules.ParseWhen(v)
	if err != nil {
		return err
	}
	d.SetWhen(w)
	return nil
}

func getWhen(d schedules.Def) string {
	if d.When().Zero() {
		return ""
	}
	return d.When().Text()
}

func setProject(a *App, d *schedules.Def, v string) error {
	d.Agent.Project, _ = a.noteOwner(ochazuke.Ctx{Runtime: a.r}, strings.TrimPrefix(v, "project:"), true, "", false)
	return nil
}

func getProject(d schedules.Def) string { return d.Agent.Project }

func setCommand(_ *App, d *schedules.Def, v string) error { d.Command = strings.Fields(v); return nil }

func getCommand(d schedules.Def) string { return strings.Join(d.Command, " ") }

func setTimeout(_ *App, d *schedules.Def, v string) error {
	if v == "" {
		d.Timeout = 0
		return nil
	}
	t, err := time.ParseDuration(v)
	if err != nil || t < time.Second {
		return fmt.Errorf("timeout %q: a duration such as 2m", v)
	}
	d.Timeout = schedules.Duration(t)
	return nil
}

func getTimeout(d schedules.Def) string {
	if d.Timeout == 0 {
		return ""
	}
	return scheduleSpan(time.Duration(d.Timeout))
}

// scheduleSpecs are the editable fields in the form's order. The plain
// string fields are one row each; the custom parsers are when, project,
// command and timeout.
var scheduleSpecs = []scheduleFieldSpec{
	{name: "prompt", str: func(d *schedules.Def) *string { return &d.Prompt }},
	{name: "when", set: setWhen, get: getWhen},
	{name: "enabled", str: func(d *schedules.Def) *string { return &d.Enabled }, choices: schedules.EnabledValues, strict: true},
	{name: "harness", str: func(d *schedules.Def) *string { return &d.Agent.Harness }, check: checkHarness},
	{name: "model", str: func(d *schedules.Def) *string { return &d.Agent.Model }},
	{name: "effort", str: func(d *schedules.Def) *string { return &d.Agent.Effort }},
	{name: "fast", str: func(d *schedules.Def) *string { return &d.Agent.Fast }, choices: []string{"on", "off"}, msg: "fast is on or off"},
	{name: "permission-mode", str: func(d *schedules.Def) *string { return &d.Agent.Permission }},
	{name: "project", set: setProject, get: getProject},
	{name: "task", str: func(d *schedules.Def) *string { return &d.Agent.Task }, trim: "task:"},
	{name: "dir", str: func(d *schedules.Def) *string { return &d.Agent.Dir }},
	{name: "into", str: func(d *schedules.Def) *string { return &d.Agent.Target }, choices: schedules.TargetValues, label: "target"},
	{name: "pane", str: func(d *schedules.Def) *string { return &d.Agent.Pane }},
	{name: "start", str: func(d *schedules.Def) *string { return &d.Start }, check: checkBound(false)},
	{name: "end", str: func(d *schedules.Def) *string { return &d.End }, check: checkBound(true)},
	{name: "catch-up", str: func(d *schedules.Def) *string { return &d.CatchUp }, choices: schedules.CatchUpValues},
	{name: "interval-from", str: func(d *schedules.Def) *string { return &d.IntervalFrom }, choices: schedules.IntervalFromValues},
	{name: "description", str: func(d *schedules.Def) *string { return &d.Description }},
	{name: "kind", str: func(d *schedules.Def) *string { return &d.Kind }},
	{name: "command", set: setCommand, get: getCommand},
	{name: "drive", str: func(d *schedules.Def) *string { return &d.Drive }, check: checkDrive},
	{name: "timeout", set: setTimeout, get: getTimeout},
}

// scheduleFields are the editable fields in the form's order: the
// parameters add and edit share, by name.
var scheduleFields = func() []string {
	out := make([]string, len(scheduleSpecs))
	for i, s := range scheduleSpecs {
		out[i] = s.name
	}
	return out
}()

// scheduleSpec is the field's row, nil for a name no field has.
func scheduleSpec(name string) *scheduleFieldSpec {
	if i := slices.IndexFunc(scheduleSpecs, func(s scheduleFieldSpec) bool { return s.name == name }); i >= 0 {
		return &scheduleSpecs[i]
	}
	return nil
}

// scheduleChoices are the field's choices (its prop's enum).
func scheduleChoices(name string) []string { return scheduleSpec(name).choices }

// applyScheduleField sets one field of d from its parameter's text.
func (a *App) applyScheduleField(d *schedules.Def, name, v string) error {
	s := scheduleSpec(name)
	if s == nil {
		return fmt.Errorf("schedules have no field %q", name)
	}
	v = strings.TrimSpace(v)
	if len(s.choices) > 0 && !slices.Contains(s.choices, v) && (v != "" || s.strict) {
		if s.msg != "" {
			return errors.New(s.msg)
		}
		return fmt.Errorf("%s is one of %s", cmp.Or(s.label, s.name), strings.Join(s.choices, ", "))
	}
	if s.check != nil {
		if err := s.check(a, v); err != nil {
			return err
		}
	}
	if s.set != nil {
		return s.set(a, d, v)
	}
	*s.str(d) = strings.TrimPrefix(v, s.trim)
	return nil
}

// scheduleField is one field of d as its parameter's text (what the form
// loads and edit compares).
func scheduleField(d schedules.Def, name string) string {
	s := scheduleSpec(name)
	switch {
	case s == nil:
		return ""
	case s.get != nil:
		return s.get(d)
	}
	return *s.str(&d)
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
