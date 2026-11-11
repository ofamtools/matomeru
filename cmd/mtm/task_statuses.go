package main

import (
	"slices"
	"sync"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/work"
)

// settingTaskStatuses is the task status table: JSON rows of name (as
// stored), glyph (the one character inside the checkbox brackets), rank
// (table order: lower first) and closed (the status ends the task).
const settingTaskStatuses = "tasks.statuses"

// taskStatusSetting declares the table. The keys that set a status in the
// outline are binding rows (statusKeyRows), not part of it: a status added
// here is bound in bindings.json to outline-task.status with its name.
var taskStatusSetting = ochazuke.Setting{Name: settingTaskStatuses, Type: ochazuke.SettingString, Default: work.DefaultStatusesJSON,
	Validate:    func(v any) error { _, err := work.ParseStatuses(v.(string)); return err },
	Description: "JSON task statuses: name (as stored; todo and done are needed), glyph (one character drawn inside the checkbox brackets), rank (order in the task tables, lower first) and closed (the status ends the task). Existing tasks keep their status name; one the table does not name draws [?]"}

// statusKey is a default key that sets a status from the outline's task
// row, as a binding row to outline-task.status (rebind in bindings.json).
type statusKey struct{ status, key string }

// statusKeyRows are the default status keys; done is x (the toggle).
var statusKeyRows = []statusKey{{work.StatusTodo, "t"}, {work.StatusActive, "a"}, {"waiting", "w"}, {work.StatusBlocked, "b"}, {"cancelled", "c"}}

// statusSource is the store's status table: the setting, parsed again
// only when its text changes.
func (a *App) statusSource() func() []work.StatusDef {
	var mu sync.Mutex
	var text string
	var rows []work.StatusDef
	return func() []work.StatusDef {
		mu.Lock()
		defer mu.Unlock()
		now := a.r.Settings().String(settingTaskStatuses)
		if rows == nil || now != text {
			parsed, err := work.ParseStatuses(now)
			if err != nil {
				parsed = work.DefaultStatuses()
			}
			text, rows = now, parsed
		}
		return rows
	}
}

// statusNames are the status table's names, for parameter enums and pickers.
func (a *App) statusNames() []string { return a.work.StatusNames() }

// defaultStatusNames are the default table's names, for enums fixed when a
// verb is defined (the pickers read the live table).
func defaultStatusNames() []string {
	rows := work.DefaultStatuses()
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out
}

// syncStatusEnums makes the choices of every status parameter and prop the
// live table's names (tasks.statuses), so a status the owner adds is taken
// by outline-task.status, task.edit and task set status=NAME. It runs when
// the setting is declared and each time it changes.
func (a *App) syncStatusEnums() {
	names := a.statusNames()
	for _, key := range []string{outlineTaskNoun + ".status", "task.edit"} {
		op := a.r.Operations.At(ochazuke.Key(a.r.CanonicalKey(key)))
		if p, ok := op.Parameters["status"]; ok {
			p.Enum = slices.Clone(names)
			a.parameters(key, map[string]ochazuke.Parameter{"status": p})
		}
	}
	if n := a.r.Nouns["task"]; n != nil {
		for i := range n.Props {
			if n.Props[i].Name == "status" {
				n.Props[i].Enum = slices.Clone(names)
			}
		}
	}
	a.r.Operations.Invalidate()
}
