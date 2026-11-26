package app

import (
	"errors"
	"path/filepath"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/input"
)

// actionLogResult observes the current synchronous handler. Nested dispatches
// own their own results.
type actionLogResult struct {
	failure string
}

func (a *App) newActionLog() *data.ActionLogStore {
	dbPath := a.Store.DBPath()
	if dbPath == "" || dbPath == ":memory:" || strings.Contains(dbPath, "mode=memory") {
		return data.NewActionLogStore("")
	}
	if path, ok := strings.CutPrefix(dbPath, "file:"); ok {
		dbPath, _, _ = strings.Cut(path, "?")
		if dbPath == "" || dbPath == ":memory:" {
			return data.NewActionLogStore("")
		}
	}
	dir := a.opts.DataDir
	if dir == "" {
		dir = filepath.Dir(dbPath)
	}
	return data.NewActionLogStore(filepath.Join(dir, "action-log.jsonl"))
}

func (a *App) executeAction(s core.State, c core.Command, fn core.Handler) core.StepResult {
	previous := a.actionResult
	a.actionResult = nil
	defer func() { a.actionResult = previous }()

	if !a.actionLogEnabled() || noisyAction(c.ID) {
		return fn(s, c)
	}
	spec, _ := a.bind.Keymap.Commands.Lookup(c.ID)
	if enabled, ok := spec.Meta["actionLog"].(bool); ok && !enabled {
		return fn(s, c)
	}

	entry := data.ActionLogEntry{
		At: a.now(), Command: c.ID, Target: s.ActiveFrame().FocusID,
		Space: s.ActivePage, Source: c.Source, Outcome: "ok", Detail: spec.Description,
	}
	if ref, ok := a.subject(s, c); ok {
		entry.Target = data.RefText(ref)
	}
	if entry.Source == "" {
		entry.Source = "app"
	}

	result := &actionLogResult{}
	a.actionResult = result
	out := fn(s, c)
	if result.failure != "" {
		entry.Outcome = "error"
		if entry.Detail == "" {
			entry.Detail = result.failure
		} else {
			entry.Detail += ": " + result.failure
		}
	}
	a.actionLog.Record(entry)
	return out
}

func (a *App) actionLogEnabled() bool {
	return a.actionLog != nil && a.cfg.Settings.Bool(SetActionLogEnabled, true)
}

func noisyAction(id string) bool {
	return id == "" || id == CmdTermInput || input.IsReadlineCommand(id) ||
		strings.HasPrefix(id, "motion.") || strings.HasPrefix(id, "focus.") ||
		strings.HasSuffix(id, ".insert") || strings.HasSuffix(id, ".backspace")
}

// The status line can interpolate input and details returned by subprocesses.
// Persist the operation and known cause instead of copying those values.
func (a *App) observeActionFailure(err error) {
	if a.actionResult == nil {
		return
	}
	switch {
	case errors.Is(err, data.ErrNotFound):
		a.actionResult.failure = "record not found"
	default:
		a.actionResult.failure = "handler failed"
	}
}
