package main

import (
	"cmp"

	"ofam.dev/ochazuke"
)

// How the task outline opens (tasks.open --mode --title): the focus
// selector (# with focus.selector outline) opens the same outline in the
// focus-set mode under its own title. The mode is data: the modal declares
// it (Instance.Pin), so every row beneath lands in it and its keys are
// that mode's rows (bindings/rows.json); nothing here reads its name.

// outlineOpen is how an outline opens: the base mode its modal declares
// ("" is normal) and the word its title starts with ("" is Tasks).
type outlineOpen struct{ mode, label string }

// title is the word the outline's title starts with.
func (o outlineOpen) title() string { return cmp.Or(o.label, "Tasks") }

// outlineOpenOf reads tasks.open's --mode and --title.
func outlineOpenOf(args ochazuke.Arguments) outlineOpen {
	return outlineOpen{mode: ochazuke.StringArg(args, "mode", ""), label: ochazuke.StringArg(args, "title", "")}
}

// outlineOpenParams are tasks.open's parameters (outlineOpenOf reads them).
func outlineOpenParams() map[string]ochazuke.Parameter {
	return map[string]ochazuke.Parameter{
		"mode":  {Type: "string", Description: "The base mode the outline declares while open: its rows answer that mode's binding rows (NOUN/MODE), and the normal rows where a noun has none; default normal"},
		"title": {Type: "string", Description: "The word the outline's title starts with; default Tasks"}}
}

// openOutlineModal opens the outline's modal declaring mode ("" drops a
// pin): the focus beneath it lands in that mode. A pin it cannot take
// closes the modal again.
func (a *App) openOutlineModal(mode string) error {
	if _, err := a.r.OpenModal(outlineModal, ochazuke.Payload{}, ochazuke.CloseWith(ochazuke.Request{Noun: outlineModal, Verb: "close"})); err != nil {
		return err
	}
	if err := a.r.PinInstance(outlineModal, mode); err != nil {
		_ = a.r.Pop()
		return err
	}
	return nil
}

// outlineFocusMark is a row's focus mark: focusGlyph when the thing keyed
// id is in today's focus (tasks.focus-glyph on), else nothing. The focus
// is read once per outline (o.focused) and again after a focus change
// (refreshTaskRows). Keys are a record's own ids, so a task's id names
// only that task.
func (a *App) outlineFocusMark(o *outlineState, id string) string {
	if o == nil || id == "" || !a.r.Settings().Bool(settingTasksFocusGlyph) {
		return ""
	}
	if o.focused == nil {
		o.focused = map[string]bool{}
		for thing := range a.focusOrders(nil) {
			_, key := splitThing(thing)
			o.focused[key] = true
		}
	}
	if o.focused[id] {
		return focusGlyph + " "
	}
	return ""
}

// refreshTaskRows redraws every row that shows a task, after the focus or
// a row's look (tasks.* glyph settings) changed: the open outline's rows,
// their focus read again (outlineChanged, which reloads the task rows
// too), else the task rows.
func (a *App) refreshTaskRows() error {
	if o := a.outline; o != nil && a.r.Tree(outlineTree) == o.tree {
		o.focused = nil
		return a.outlineChanged(o)
	}
	return a.refreshTasks()
}
