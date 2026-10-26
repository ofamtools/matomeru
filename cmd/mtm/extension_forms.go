package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
)

// Per-tool CRUD on mounted extension rows. A catalog noun with a new (or
// create), edit (or update) or delete (or remove) application verb gets
// mtm's key map on its rows: n new, i edit, d d delete. New and edit open
// ochazuke's record editor as a modal, its fields the verb's parameters
// (RecordFields), edit's prefilled from the row's data; every change is
// written as it is made by running the tool's verb off the loop. A new
// record is created by its first write once every required field is
// filled (the tool's answer names its id), and later changes run the
// edit verb on it. d d runs the delete verb after its confirm: the tool's
// own, else "Delete {{label}}?" (mtm cannot undo it).

// extCRUD names a catalog noun's verbs for the key map ("" when it has
// none).
type extCRUD struct{ New, Edit, Delete string }

// crudVerbs are the verb names each action answers to, in preference
// order.
var crudVerbs = map[string][]string{
	"new":    {"new", "create"},
	"edit":   {"edit", "update"},
	"delete": {"delete", "remove"},
}

// extFormID is the form's modal instance.
const extFormID = "ext/form"

// extFormNoun is mtm's noun whose verbs open the form; its rows sit on the
// extension's nouns and lists.
const extFormNoun = "ext-form"

// crudOf finds a catalog noun's new, edit and delete verbs.
func crudOf(n ocli.CatalogNoun) extCRUD {
	find := func(action string) string {
		for _, name := range crudVerbs[action] {
			if slices.ContainsFunc(n.Verbs, func(v ocli.CatalogVerb) bool { return v.Name == name }) {
				return name
			}
		}
		return ""
	}
	return extCRUD{New: find("new"), Edit: find("edit"), Delete: find("delete")}
}

// defineExtForms adds the form noun: new on a list, edit on a row.
func (a *App) defineExtForms() {
	a.r.Define(extFormNoun).
		Verb("new", a.extFormNew, "Create a row of a mounted extension through a form of its new verb's parameters; written as you type").
		Verb("edit", a.extFormEdit, "Edit the focused row of a mounted extension through a form of its edit verb's parameters, prefilled from the row; written as you type")
	a.parameters(extFormNoun+".new", map[string]ochazuke.Parameter{
		"noun": {Type: "string", Required: true, Description: "mtm's noun of the extension's rows (EXT-NOUN)"},
	})
}

// mountCRUD finds the CRUD verbs of an extension's mounted nouns and binds
// the key map: d d and i on the rows' noun, n on each list showing them.
// It answers the keys it bound per mtm noun, which the catalog's own rows
// may not take.
func (a *App) mountCRUD(x *extension, plan []ocli.CatalogNoun, pages []ocli.CatalogPage) map[string][]string {
	taken := map[string][]string{}
	x.crud = map[string]extCRUD{}
	for _, n := range plan {
		local, ok := x.nouns[n.Name]
		if !ok {
			continue
		}
		c := crudOf(n)
		if c == (extCRUD{}) {
			continue
		}
		x.crud[n.Name] = c
		b := a.r.Define(local).Mode("normal")
		if c.Delete != "" && len(requiredOf(a.r.Operations[local+"."+c.Delete])) == 0 {
			key := local + "." + c.Delete
			if op := a.r.Operations[key]; op.Confirm == "" {
				op.Confirm = "Delete {{label}}?"
				a.r.Operations[key] = op
			}
			b.Bind("d d", ochazuke.Do(key))
			taken[local] = append(taken[local], "d")
		}
		if c.Edit != "" && len(a.r.Operations[local+"."+c.Edit].Parameters) > 0 {
			b.Bind("i", ochazuke.Do(extFormNoun+".edit").Hinted("edit"))
			taken[local] = append(taken[local], "i")
		}
	}
	for _, p := range pages {
		for _, l := range p.Lists {
			noun := x.newNounOf(l)
			if noun == "" {
				continue
			}
			local := x.nouns[noun]
			row := ochazuke.Do(extFormNoun+".new", ochazuke.Arguments{"noun": local}).Hinted("new")
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

// newNounOf is the catalog noun whose new verb n runs on a list: its rows'
// noun, else the list's own noun; "" when neither has one.
func (x *extension) newNounOf(l ocli.CatalogList) string {
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
		if x.crud[n].New != "" && x.nouns[n] != "" {
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

// extOfLocal is the mounted extension and catalog noun of an mtm noun.
func (a *App) extOfLocal(local string) (*extension, string) {
	for _, x := range a.exts {
		if n := x.nounOfLocal(local); n != "" {
			return x, n
		}
	}
	return nil, ""
}

// extFormNew is ext-form.new: a form of the noun's new verb; a verb with
// no parameters runs at once.
func (a *App) extFormNew(c ochazuke.Ctx) ochazuke.Result {
	local := ochazuke.StringArg(c.Args, "noun", "")
	x, noun := a.extOfLocal(local)
	if x == nil || x.crud[noun].New == "" {
		return ochazuke.Failed(fmt.Errorf("%s has no new verb", local))
	}
	verb := x.crud[noun].New
	op := a.r.Operations[local+"."+verb]
	if len(op.Parameters) == 0 {
		return a.extRun(x, noun, verb, "", nil, nil)
	}
	if a.cli {
		return ochazuke.Failed(fmt.Errorf("the form is the TUI's; run %s %s with its arguments", local, verb))
	}
	f := &extForm{a: a, x: x, noun: noun, local: local}
	return resultError(a.openExtForm(f, "New "+noun, ochazuke.RecordFields(op), ochazuke.Record{}))
}

// extFormEdit is ext-form.edit on a row: a form of its noun's edit verb,
// prefilled from the row's data.
func (a *App) extFormEdit(c ochazuke.Ctx) ochazuke.Result {
	inst := c.Instance
	if inst == nil {
		return ochazuke.Failed(errors.New("no row selected"))
	}
	x, noun := a.extOfLocal(inst.Noun)
	id, _ := inst.Data.(string)
	if x == nil || x.crud[noun].Edit == "" || id == "" {
		return ochazuke.Failed(fmt.Errorf("%s has no edit verb", inst.Noun))
	}
	if a.cli {
		return ochazuke.Failed(fmt.Errorf("the form is the TUI's; run %s %s --target ID with its arguments", inst.Noun, x.crud[noun].Edit))
	}
	row := ocli.Row{ID: id, Label: inst.Label}
	for _, v := range x.views {
		if r, ok := v.Row(inst.ID); ok {
			row = r
		}
	}
	fields := ochazuke.RecordFields(a.r.Operations[inst.Noun+"."+x.crud[noun].Edit])
	f := &extForm{a: a, x: x, noun: noun, local: inst.Noun, target: id}
	return resultError(a.openExtForm(f, "Edit "+noun+" "+row.Label, fields, prefill(fields, row)))
}

// extForm is one open form: the row it edits (target), "" until a new
// one is created.
type extForm struct {
	a           *App
	x           *extension
	noun, local string
	target      string
	created     bool // the new verb ran (target is "" when its answer named no id)
}

// openExtForm opens the record editor modal over the focus.
func (a *App) openExtForm(f *extForm, title string, fields []ochazuke.RecordField, values ochazuke.Record) error {
	if a.extForm != nil && a.r.Instances[extFormID] != nil {
		return errors.New("a form is already open")
	}
	form, err := a.r.OpenRecordEditor(extFormID, ochazuke.RecordSpec{
		Title:    title + " · written as you type",
		Fields:   fields,
		Debounce: a.editDebounce(),
		Insert:   true,
		Load:     func() (ochazuke.Record, error) { return values, nil },
		Save:     f.save,
		OnLeave:  func() { a.extForm = nil },
	})
	if err != nil {
		return err
	}
	a.extForm = form
	return nil
}

// save writes one change: the new verb until the record exists (once every
// required field is filled), then the edit verb on it.
func (f *extForm) save(w ochazuke.RecordWrite) ochazuke.Result {
	c := f.x.crud[f.noun]
	if f.created && f.target == "" {
		return ochazuke.Failed(fmt.Errorf("created; %s new answered no id, so later changes are not written", f.noun))
	}
	if f.target == "" {
		op := f.a.r.Operations[f.local+"."+c.New]
		var missing []string
		for _, name := range requiredOf(op) {
			if strings.TrimSpace(w.Values[name]) == "" {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			return ochazuke.Failed(fmt.Errorf("created once %s is filled", strings.Join(missing, ", ")))
		}
		args := extArgs(op, w.Values, nil)
		return f.a.extRun(f.x, f.noun, c.New, "", args, func(v any, err error) {
			if err == nil {
				f.created, f.target = true, createdID(v)
			}
		})
	}
	if c.Edit == "" {
		return ochazuke.Failed(fmt.Errorf("created; %s has no edit verb, so later changes are not written", f.noun))
	}
	op := f.a.r.Operations[f.local+"."+c.Edit]
	changed := slices.Clone(w.Changed)
	for _, name := range requiredOf(op) {
		if !slices.Contains(changed, name) {
			changed = append(changed, name)
		}
	}
	return f.a.extRun(f.x, f.noun, c.Edit, f.target, extArgs(op, w.Values, changed), nil)
}

// createdID is the id a new verb answered: its id field, the id of the one
// row it answered, or its text.
func createdID(v any) string {
	switch v := v.(type) {
	case map[string]any:
		for _, k := range []string{"id", "ID"} {
			if id, ok := v[k]; ok && id != nil {
				return fieldText(id)
			}
		}
	case []any:
		if len(v) == 1 {
			return createdID(v[0])
		}
	case string:
		if !strings.ContainsAny(v, " \n") {
			return v
		}
	case float64:
		return fieldText(v)
	}
	return ""
}

// extArgs are a write's values as the verb's typed arguments: the names
// given (nil: every non-empty field). An empty value of a type other than
// string, or one that does not parse, is left out (the tool checks).
func extArgs(op ochazuke.Operation, values ochazuke.Record, names []string) map[string]any {
	if names == nil {
		for name, v := range values {
			if v != "" {
				names = append(names, name)
			}
		}
	}
	out := map[string]any{}
	for _, name := range names {
		p, ok := op.Parameters[name]
		if !ok {
			continue
		}
		v := values[name]
		s := strings.TrimSpace(v)
		switch p.Type {
		case "integer":
			if n, err := strconv.Atoi(s); err == nil {
				out[name] = n
			}
		case "number":
			if n, err := strconv.ParseFloat(s, 64); err == nil {
				out[name] = n
			}
		case "boolean":
			if b, err := strconv.ParseBool(s); err == nil {
				out[name] = b
			}
		default:
			out[name] = v
		}
	}
	return out
}

// prefill is a row's values for a form's fields: its data by field name,
// else its label for a label, name or title field.
func prefill(fields []ochazuke.RecordField, row ocli.Row) ochazuke.Record {
	data, _ := row.Data.(map[string]any)
	out := ochazuke.Record{}
	for _, f := range fields {
		if v, ok := data[f.Name]; ok && v != nil {
			out[f.Name] = fieldText(v)
			continue
		}
		switch strings.ToLower(f.Name) {
		case "label", "name", "title":
			out[f.Name] = row.Label
		}
	}
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

// extFormFailure is a write the form made between messages that failed
// (its label also says "not saved").
func (a *App) extFormFailure() error {
	if a.extForm == nil {
		return nil
	}
	return a.extForm.Failure()
}

// flushExtForm writes the open form's pending change (on quit).
func (a *App) flushExtForm() error {
	if a.extForm == nil {
		return nil
	}
	return a.extForm.Flush()
}
