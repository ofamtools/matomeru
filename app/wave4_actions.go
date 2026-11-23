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
	"ofam.dev/matomeru/runs"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/toast"
)

// loadUserActions reads ~/.config/matomeru/actions.json and registers every
// ActionDef as a first-class core.CommandSpec so it works in `;` Actions,
// `:` Palette, and bindings.json.
func (a *App) loadUserActions(dir string) {
	if dir == "" {
		return
	}
	defs, err := actions.LoadFile(filepath.Join(dir, actions.ActionsFile))
	if err != nil {
		a.warn("%s: %v", actions.ActionsFile, err)
		return
	}
	if len(defs) == 0 {
		return
	}
	if a.userActions == nil {
		a.userActions = map[string]actions.ActionDef{}
	}
	for _, def := range defs {
		sp := def.ToCommandSpec()
		a.userActions[sp.ID] = def
		if idx := slices.IndexFunc(a.specs, func(s core.CommandSpec) bool { return s.ID == sp.ID }); idx >= 0 {
			a.specs[idx] = sp
		} else {
			a.specs = append(a.specs, sp)
		}
	}
}

func (a *App) recordTemplateVars(ref *core.Ref) map[string]string {
	vars := map[string]string{}
	if ref == nil || ref.ID == "" {
		return vars
	}
	vars["id"] = ref.ID
	vars["target"] = ref.ID
	vars["kind"] = ref.Kind
	vars["ref"] = data.RefText(*ref)

	rec, err := a.Store.Get(*ref)
	if err != nil {
		if extRec, ok := a.cachedExternal(*ref); ok {
			rec = &extRec
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

func (a *App) runUserAction(def actions.ActionDef) func(core.State, core.Command) core.StepResult {
	name := def.CommandName()
	return func(s core.State, c core.Command) core.StepResult {
		var refPtr *core.Ref
		if ref, ok := a.subject(s, c); ok {
			r := ref
			refPtr = &r
		}
		vars := a.recordTemplateVars(refPtr)
		argv := def.ExpandCommand(vars)
		if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
			a.say("%s: empty command", name)
			return core.Next(s)
		}
		dir := expandHome(def.ExpandDir(vars))
		env := def.ExpandEnv(vars)

		if def.Foreground {
			eff := core.ExecEffect{
				Name:    argv[0],
				Args:    argv[1:],
				Dir:     dir,
				Env:     envSlice(env),
				Command: name,
			}
			a.say("running %s: %s", name, shellJoin(argv))
			a.pushToast("Running "+name, toast.Info)
			return core.Next(s, eff)
		}

		cmdLine := shellJoin(argv)
		if a.Runner != nil {
			runArgs := argv
			if len(env) > 0 {
				runArgs = append(append([]string{"env"}, envSlice(env)...), argv...)
			}
			spec := runs.StartSpec{
				Title:   name,
				Command: cmdLine,
				Args:    runArgs,
				Dir:     dir,
				Actor:   runActor,
			}
			if refPtr != nil && refPtr.Kind == data.KindTask {
				spec.TaskRef = *refPtr
			}
			rec, err := a.Runner.Start(context.Background(), spec)
			s = a.started(s, "ran "+name, rec, err)
			if err != nil {
				a.pushToast(name+" failed: "+err.Error(), toast.Err)
			} else {
				a.pushToast("Ran "+name, toast.Ok)
			}
			return core.Next(s)
		}

		// Synthetic app (no live Runner): record a queued run cleanly.
		s = a.startShell(s, name, cmdLine, dir)
		a.pushToast("Ran "+name, toast.Ok)
		return core.Next(s)
	}
}
