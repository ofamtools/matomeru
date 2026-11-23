package app

import (
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/toast"
)

// --- Schedule pause / resume -----------------------------------------------------

func (a *App) schedulePause(s core.State, c core.Command) core.StepResult {
	ref, ok := a.subject(s, c)
	if !ok || ref.Kind != data.KindSchedule {
		a.say("no schedule here to pause")
		return core.Next(s)
	}
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("pause schedule", err)
		return core.Next(s)
	}
	if rec.Bool("paused") {
		a.say("%s is already paused", a.title(ref))
		return core.Next(s)
	}
	_, cs, err := a.Store.Patch(data.PatchInput{
		Ref:     ref,
		Fields:  map[string]any{"paused": true},
		Summary: "pause schedule",
	}, User)
	a.wrote("paused "+a.title(ref), cs, err)
	if err == nil {
		a.pushToast("Paused "+a.title(ref), toast.Info)
	}
	return core.Next(s)
}

func (a *App) scheduleResume(s core.State, c core.Command) core.StepResult {
	ref, ok := a.subject(s, c)
	if !ok || ref.Kind != data.KindSchedule {
		a.say("no schedule here to resume")
		return core.Next(s)
	}
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("resume schedule", err)
		return core.Next(s)
	}
	fields := map[string]any{"paused": false}
	if _, hasEnabled := rec.Value("enabled"); hasEnabled && !rec.Bool("enabled") {
		fields["enabled"] = true
	}
	switch strings.ToLower(strings.TrimSpace(rec.Text("status"))) {
	case "paused", "disabled", "stopped":
		fields["status"] = "active"
	}
	if !rec.Bool("paused") && len(fields) == 1 {
		a.say("%s is already active", a.title(ref))
		return core.Next(s)
	}
	_, cs, err := a.Store.Patch(data.PatchInput{
		Ref:     ref,
		Fields:  fields,
		Summary: "resume schedule",
	}, User)
	a.wrote("resumed "+a.title(ref), cs, err)
	if err == nil {
		a.pushToast("Resumed "+a.title(ref), toast.Ok)
	}
	return core.Next(s)
}
