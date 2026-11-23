package app

import (
	"context"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/runs"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/toast"
)

// --- Global KindCommand run & edit-before-run ------------------------------------

func (a *App) execSuggestedCommand(s core.State, rec data.Record, cmdLine, dir, title string) core.State {
	cmdLine = strings.TrimSpace(cmdLine)
	if cmdLine == "" {
		cmdLine = strings.TrimSpace(rec.Text("scriptPath"))
	}
	if cmdLine == "" {
		a.say("command record has no command or scriptPath")
		return s
	}
	if title == "" {
		title = rec.Text("title")
	}
	if title == "" {
		title = cmdLine
	}
	var (
		runRec *data.Record
		err    error
	)
	if a.rt.Runner != nil {
		spec := runs.StartSpec{
			Title:   title,
			Command: cmdLine,
			Dir:     expandHome(dir),
			Actor:   runActor,
		}
		if taskRef, ok := rec.RefField("task"); ok {
			spec.TaskRef = taskRef
		}
		runRec, err = a.rt.Runner.Start(context.Background(), spec)
		s = a.started(s, "ran "+title, runRec, err)
	} else {
		fields := map[string]any{"title": title, "command": cmdLine, "runStatus": "queued"}
		if dir != "" {
			fields["cwd"] = dir
		}
		if taskRef, ok := rec.RefField("task"); ok {
			fields["task"] = taskRef
		}
		var cs *data.ChangeSet
		runRec, cs, err = a.Store.Create(data.CreateInput{Kind: data.KindRun, Fields: fields}, User)
		a.wrote("ran "+title, cs, err)
		if err == nil && runRec != nil {
			a.viewMemo.gen++
			selectRow(a.records(runsTable, data.KindRun), data.RefText(runRec.Ref()))
		}
	}
	if err != nil {
		a.pushToast("Command failed: "+err.Error(), toast.Err)
		return s
	}
	patchFields := map[string]any{"status": "ran"}
	if runRec != nil {
		patchFields["lastRun"] = runRec.Ref()
	}
	if _, _, perr := a.Store.Patch(data.PatchInput{
		Ref:     rec.Ref(),
		Fields:  patchFields,
		Summary: "ran suggested command",
	}, User); perr != nil {
		a.fail("update command status", perr)
	}
	a.pushToast("Running "+title+" in 3 Runs", toast.Ok)
	s = a.openSpace(s, SpaceRuns)
	if runRec != nil {
		a.viewMemo.gen++
		selectRow(a.records(runsTable, data.KindRun), data.RefText(runRec.Ref()))
	}
	return s
}

func (a *App) commandRun(s core.State, c core.Command) core.StepResult {
	// On a Show page ref field, Enter follows the reference link rather than running.
	if raw, isMap := c.Args["ref"].(map[string]any); isMap {
		if r, ok := data.AsRef(raw); ok {
			return core.Next(a.show(s, r))
		}
	}
	ref, ok := a.subject(s, c)
	if !ok || ref.Kind != data.KindCommand {
		a.say("no command here to run")
		return core.Next(s)
	}
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("run command", err)
		return core.Next(s)
	}
	return core.Next(a.execSuggestedCommand(s, *rec, rec.Text("command"), rec.Text("cwd"), rec.Text("title")))
}

func (a *App) commandEditRun(s core.State, c core.Command) core.StepResult {
	ref, ok := a.subject(s, c)
	if !ok || ref.Kind != data.KindCommand {
		a.say("no command here to edit and run")
		return core.Next(s)
	}
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("edit command", err)
		return core.Next(s)
	}
	cmdStr := rec.Text("command")
	if cmdStr == "" {
		cmdStr = rec.Text("scriptPath")
	}
	specs := []core.ParamSpec{
		{Name: "command", Type: core.ParamString, Required: true, Description: "shell command to run"},
		{Name: "args", Type: core.ParamString, Description: "optional extra arguments appended to command"},
		{Name: "cwd", Type: core.ParamPath, Description: "working directory"},
		{Name: "title", Type: core.ParamString, Description: "run label"},
	}
	values := map[string]any{
		"command": cmdStr,
		"cwd":     rec.Text("cwd"),
		"title":   rec.Text("title"),
	}
	recCopy := *rec
	next := a.openLaunch(s, "Edit & Run · "+views.Title(a.schema(data.KindCommand), recCopy), "Run command", specs, values, func(st core.State, v map[string]any) (core.State, bool) {
		full := strings.TrimSpace(data.Text(v["command"]))
		if extra := strings.TrimSpace(data.Text(v["args"])); extra != "" {
			full = full + " " + extra
		}
		if full == "" {
			a.say("command is required")
			return st, false
		}
		dir := strings.TrimSpace(data.Text(v["cwd"]))
		title := strings.TrimSpace(data.Text(v["title"]))
		return a.execSuggestedCommand(st, recCopy, full, dir, title), true
	})
	a.say("edit command/args/cwd · ctrl+s runs in 3 Runs")
	return core.Next(next)
}
