package app

import (
	"context"
	"encoding/json"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"ofam.dev/matomeru/actions"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ext"
	"ofam.dev/matomeru/runs"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/toast"
)

// loadUserActions reads ~/.config/matomeru/actions.json and registers every
// ActionDef as a first-class RecordActionSpec / core.CommandSpec (for `;`
// Actions, `:` Palette, and bindings.json) and stores declarative parameterized
// CustomActions for the `g a` Custom Actions picker.
func (a *App) loadUserActions(dir string) {
	if dir == "" {
		return
	}
	defs, customs, err := actions.LoadAllFile(filepath.Join(dir, actions.ActionsFile))
	if err != nil {
		a.warn("%s: %v", actions.ActionsFile, err)
		return
	}
	for _, def := range defs {
		a.registerRecordAction(def.ToRecordActionSpec())
	}
	a.ext.customActions = customs
}

// registerRecordAction registers a declarative record action's CommandSpec and
// handler (when the engine is already constructed).
func (a *App) registerRecordAction(spec actions.RecordActionSpec) {
	if strings.TrimSpace(spec.ID) == "" {
		return
	}
	if a.ext.recordActions == nil {
		a.ext.recordActions = map[string]actions.RecordActionSpec{}
	}
	sp := spec.ToCommandSpec()
	a.ext.recordActions[sp.ID] = spec
	if idx := slices.IndexFunc(a.bind.specs, func(s core.CommandSpec) bool { return s.ID == sp.ID }); idx >= 0 {
		a.bind.specs[idx] = sp
	} else {
		a.bind.specs = append(a.bind.specs, sp)
	}
	if a.Engine != nil {
		a.handle(sp.ID, a.runRecordAction(spec))
	}
}

func (a *App) resolveRecord(ref core.Ref) (*data.Record, bool) {
	if r, err := a.Store.Get(ref); err == nil && r != nil {
		return r, true
	}
	if cached, ok := a.cachedExternal(ref); ok {
		return &cached, true
	}
	return nil, false
}

func (a *App) recordTemplateVars(ref *core.Ref, rec *data.Record, spec actions.RecordActionSpec) map[string]string {
	vars := map[string]string{}
	if spec.Binary != "" {
		vars["binary"] = spec.Binary
	}
	if spec.Noun != "" {
		vars["noun"] = spec.Noun
	}
	if spec.Verb != "" {
		vars["verb"] = spec.Verb
	}
	if ref == nil || ref.ID == "" {
		return vars
	}
	vars["id"] = ref.ID
	vars["target"] = ref.ID
	vars["kind"] = ref.Kind
	vars["ref"] = data.RefText(*ref)

	if rec == nil {
		if resolved, ok := a.resolveRecord(*ref); ok {
			rec = resolved
		}
	}
	if rec == nil {
		vars["title"] = ref.ID
		vars["label"] = ref.ID
		return vars
	}
	schema := a.schema(rec.Kind)
	title := views.Title(schema, *rec)
	if title == "" {
		title = rec.ID
	}
	merged := rec.MergedFields()
	for k, v := range merged {
		if v == nil {
			continue
		}
		vars[k] = data.Text(v)
	}
	vars["id"] = rec.ID
	vars["target"] = rec.ID
	vars["kind"] = rec.Kind
	vars["ref"] = data.RefText(rec.Ref())
	vars["title"] = title
	if vars["label"] == "" {
		vars["label"] = title
	}
	vars[data.ColStarStatus] = rec.StarStatus
	vars[data.ColArchiveStatus] = rec.ArchiveStatus
	if spec.Binary != "" {
		vars["binary"] = spec.Binary
	}
	if b, err := json.Marshal(merged); err == nil {
		vars["data"] = string(b)
	}
	return vars
}

func envSlice(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := slices.Sorted(maps.Keys(m))
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k + "=" + m[k]
	}
	return out
}

// runRecordAction executes the unified declarative record-action pipeline:
// resolve record -> evaluate Guard.When -> optional Confirm modal -> execute
// argv (foreground ExecEffect, live Runner with RefreshKinds + feedback toasts,
// or synthetic shell run).
func (a *App) runRecordAction(spec actions.RecordActionSpec) func(core.State, core.Command) core.StepResult {
	id := spec.ID
	return func(s core.State, c core.Command) core.StepResult {
		var refPtr *core.Ref
		var rec *data.Record
		if ref, ok := a.subject(s, c); ok {
			if len(spec.Kinds) > 0 && !kindMatchesAny(ref.Kind, spec.Kinds) {
				a.say("no %s focused", strings.Join(spec.Kinds, "/"))
				return core.Next(s)
			}
			r := ref
			refPtr = &r
			if resolved, found := a.resolveRecord(ref); found {
				rec = resolved
			} else if len(spec.Kinds) > 0 {
				a.fail(id, data.ErrNotFound)
				return core.Next(s)
			}
		} else if len(spec.Kinds) > 0 {
			a.say("no %s focused", strings.Join(spec.Kinds, "/"))
			return core.Next(s)
		}

		vars := a.recordTemplateVars(refPtr, rec, spec)

		if spec.Guard != nil && strings.TrimSpace(spec.Guard.When) != "" {
			var env map[string]any
			if rec != nil {
				env = views.RecordExprEnv(a.schema(rec.Kind), *rec, a.now())
			}
			ok, err := views.EvalExpr(spec.Guard.When, env)
			if err != nil || !ok {
				msg := strings.TrimSpace(actions.ExpandTemplate(spec.Guard.RefuseMessage, vars))
				if msg == "" {
					msg = id + ": guard condition not met"
				}
				a.say("%s", msg)
				a.pushToast(msg, toast.Warn)
				return core.Next(s)
			}
		}

		execute := func(st core.State) core.State {
			return a.execRecordAction(st, spec, refPtr, vars)
		}

		if spec.Confirm != nil {
			title := strings.TrimSpace(actions.ExpandTemplate(spec.Confirm.Title, vars))
			msg := strings.TrimSpace(actions.ExpandTemplate(spec.Confirm.Message, vars))
			label := strings.TrimSpace(actions.ExpandTemplate(spec.Confirm.Label, vars))
			return core.Next(a.openConfirm(s, modal.ConfirmOptions{
				Title:        title,
				Message:      msg,
				ConfirmLabel: label,
				Tone:         strings.TrimSpace(spec.Confirm.Tone),
				OnConfirm: func() modal.Result {
					a.afterModal(execute)
					return modal.Result{}
				},
			}))
		}

		return core.Next(execute(s))
	}
}

func (a *App) execRecordAction(s core.State, spec actions.RecordActionSpec, refPtr *core.Ref, vars map[string]string) core.State {
	name := spec.ID
	argv := actions.ExpandArgv(spec.Exec.Argv, vars)
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		a.say("%s: empty command", name)
		return s
	}
	dir := expandHome(actions.ExpandTemplate(spec.Exec.Dir, vars))
	env := actions.ExpandEnvMap(spec.Exec.Env, vars)

	runTitle := strings.TrimSpace(actions.ExpandTemplate(spec.Exec.Title, vars))
	if runTitle == "" {
		runTitle = name
	}

	var runningMsg, successMsg, failMsg, queuedMsg string
	if spec.Feedback != nil {
		runningMsg = strings.TrimSpace(actions.ExpandTemplate(spec.Feedback.Running, vars))
		successMsg = strings.TrimSpace(actions.ExpandTemplate(spec.Feedback.Success, vars))
		failMsg = strings.TrimSpace(actions.ExpandTemplate(spec.Feedback.Failure, vars))
		queuedMsg = strings.TrimSpace(actions.ExpandTemplate(spec.Feedback.Queued, vars))
	}

	if spec.Exec.Foreground {
		eff := core.ExecEffect{
			Name:    argv[0],
			Args:    argv[1:],
			Dir:     dir,
			Env:     envSlice(env),
			Command: name,
		}
		a.say("running %s: %s", name, shellJoin(argv))
		msg := runningMsg
		if msg == "" {
			msg = "Running " + name
		}
		a.pushToast(msg, toast.Info)
		a.effect(eff)
		return s
	}

	cmdLine := shellJoin(argv)
	refreshKinds := slices.Clone(spec.Exec.RefreshKinds)

	if a.rt.Runner != nil {
		runArgs := argv
		if len(env) > 0 {
			runArgs = append(append([]string{"env"}, envSlice(env)...), argv...)
		}
		startSpec := runs.StartSpec{
			Title:   runTitle,
			Command: cmdLine,
			Args:    runArgs,
			Dir:     dir,
			Actor:   runActor,
		}
		if refPtr != nil && refPtr.Kind == data.KindTask {
			startSpec.TaskRef = *refPtr
		}
		rec, err := a.rt.Runner.Start(context.Background(), startSpec)
		s = a.started(s, "ran "+runTitle, rec, err)
		if err != nil {
			msg := failMsg
			if msg == "" {
				msg = name + " failed: " + err.Error()
			}
			a.pushToast(msg, toast.Err)
			return s
		}
		needWait := len(refreshKinds) > 0 || runningMsg != "" || successMsg != "" || failMsg != ""
		if !needWait {
			a.pushToast("Ran "+name, toast.Ok)
			return s
		}
		if runningMsg != "" {
			a.pushToast(runningMsg, toast.Info)
		} else {
			a.pushToast("Running "+runTitle+"…", toast.Info)
		}
		doneCh := a.rt.Runner.Done(rec.ID)
		runID := rec.ID
		a.background(func(ctx context.Context) func(core.State) core.State {
			if doneCh != nil {
				select {
				case <-doneCh:
				case <-ctx.Done():
					return nil
				}
			}
			refreshed := map[string]*data.QueryResult{}
			for _, rk := range refreshKinds {
				if bin := a.ext.extCatalogs[rk]; bin != "" {
					if res, qerr := ext.QueryExternal(ctx, bin, rk, a.Store); qerr == nil {
						cp := res
						refreshed[rk] = &cp
					}
				}
			}
			finalRec, _ := a.Store.Get(core.Ref{Kind: data.KindRun, ID: runID})
			return func(s2 core.State) core.State {
				if a.ext.readCache != nil {
					for _, rk := range refreshKinds {
						a.ext.readCache.MarkStale(rk)
						if res := refreshed[rk]; res != nil {
							a.ext.readCache.Put(rk, res.Items, res.Total)
						}
					}
				}
				if finalRec != nil && finalRec.Text("runStatus") == "failed" {
					msg := failMsg
					if msg == "" {
						msg = runTitle + " failed"
					}
					a.pushToast(msg, toast.Err)
				} else {
					msg := successMsg
					if msg == "" {
						msg = "Ran " + runTitle
					}
					a.pushToast(msg, toast.Ok)
				}
				a.viewMemo.gen++
				return s2
			}
		})
		return s
	}

	// Synthetic app (no live Runner): record a queued run cleanly.
	s = a.startShell(s, runTitle, cmdLine, dir)
	if a.ext.readCache != nil {
		for _, rk := range refreshKinds {
			a.ext.readCache.MarkStale(rk)
		}
	}
	msg := queuedMsg
	if msg == "" {
		msg = successMsg
	}
	if msg == "" {
		msg = "Ran " + name
	}
	a.pushToast(msg, toast.Ok)
	return s
}
