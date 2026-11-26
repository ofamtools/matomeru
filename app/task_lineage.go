package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

const (
	CmdTaskRewrite = "task." + data.TaskTypeRewrite
	CmdTaskSplit   = "task." + data.TaskTypeSplit
	CmdTaskJoin    = "task." + data.TaskTypeJoin
)

func taskLineageSpecs() []core.CommandSpec {
	kinds := []string{data.KindTask}
	rewrite := recordSpec(CmdTaskRewrite, "Create a rewrite of a task while preserving its source", kinds, "", "editing")
	split := recordSpec(CmdTaskSplit, "Create smaller tasks linked to their source task", kinds, "", "editing")
	join := recordSpec(CmdTaskJoin, "Combine tasks into a new task linked to all sources", kinds, "", "editing")
	rewrite.Params = []core.ParamSpec{lineageTaskParam("sourceID", false), lineageTitleParam(false), lineageBodyParam()}
	split.Params = []core.ParamSpec{lineageTaskParam("sourceID", false), lineagePartsParam(false)}
	join.Params = []core.ParamSpec{lineageTaskParam("sourceIDs", true), lineageTitleParam(false), lineageBodyParam()}
	return []core.CommandSpec{rewrite, split, join}
}

func lineageTaskParam(name string, repeatable bool) core.ParamSpec {
	return core.ParamSpec{Name: name, Type: core.ParamRef, RefKinds: []string{data.KindTask}, Repeatable: repeatable,
		Description: "source task", AddLabel: "source task"}
}

func lineageTitleParam(required bool) core.ParamSpec {
	return core.ParamSpec{Name: "title", Type: core.ParamString, Required: required, Description: "title of the new task"}
}

func lineageBodyParam() core.ParamSpec {
	return core.ParamSpec{Name: "body", Type: core.ParamString, Description: "Markdown body of the new task"}
}

func lineagePartsParam(required bool) core.ParamSpec {
	return core.ParamSpec{Name: "parts", Type: core.ParamList, Required: required, Repeatable: true, AddLabel: "part",
		Description: "part title, with optional Markdown body pasted on following lines"}
}

func lineageTaskID(value any) (string, error) {
	if ref, ok := data.AsRef(value); ok && ref.Kind == data.KindTask {
		return ref.ID, nil
	}
	if text, ok := value.(string); ok {
		text = strings.TrimSpace(text)
		if kind, id, hasKind := strings.Cut(text, ":"); hasKind {
			if kind == data.KindTask && id != "" {
				return id, nil
			}
		} else if text != "" {
			return text, nil
		}
	}
	return "", fmt.Errorf("choose a source task")
}

func lineageTaskIDs(value any) ([]string, error) {
	var values []any
	switch refs := value.(type) {
	case nil:
		return nil, nil
	case []any:
		values = refs
	case []string:
		for _, ref := range refs {
			values = append(values, ref)
		}
	case []core.Ref:
		for _, ref := range refs {
			values = append(values, ref)
		}
	default:
		return nil, fmt.Errorf("choose a list of source tasks")
	}
	ids := make([]string, 0, len(values))
	for _, value := range values {
		id, err := lineageTaskID(value)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func lineageSplitParts(value any) ([]data.TaskSplitPart, error) {
	if titlesAndBodies, ok := value.([]string); ok {
		parts := make([]data.TaskSplitPart, 0, len(titlesAndBodies))
		for _, text := range titlesAndBodies {
			title, body, _ := strings.Cut(text, "\n")
			parts = append(parts, data.TaskSplitPart{Title: title, Body: body})
		}
		return parts, nil
	}
	var parts []data.TaskSplitPart
	raw, err := json.Marshal(value)
	if err == nil {
		err = json.Unmarshal(raw, &parts)
	}
	if err != nil {
		return nil, fmt.Errorf("parts must be a list of tasks with a title and optional body: %w", err)
	}
	return parts, nil
}

func lineageActor(c core.Command) core.Actor {
	if c.Actor.Kind != "" {
		return c.Actor
	}
	return User
}

func (a *App) lineageSource(s core.State, c core.Command) any {
	if value := c.Args["sourceID"]; value != nil {
		return value
	}
	if ref, ok := a.subject(s, c); ok && ref.Kind == data.KindTask {
		return ref
	}
	return nil
}

func (a *App) taskRewrite(s core.State, c core.Command) core.StepResult {
	source := a.lineageSource(s, c)
	submit := func(st core.State, values map[string]any) (core.State, bool) {
		id, err := lineageTaskID(values["sourceID"])
		if err != nil {
			a.fail("rewrite task", err)
			return st, false
		}
		record, err := a.Store.RewriteTask(id, data.Text(values["title"]), data.Text(values["body"]), lineageActor(c))
		return st, a.finishTaskLineage("rewrote task", record, err)
	}
	values := map[string]any{"sourceID": source, "title": c.Args["title"], "body": c.Args["body"]}
	if source != nil && c.Arg("title") != "" {
		next, _ := submit(s, values)
		return core.Next(next)
	}
	if id, err := lineageTaskID(source); err == nil {
		if record, err := a.Store.Get(core.Ref{Kind: data.KindTask, ID: id}); err == nil {
			if values["title"] == nil {
				values["title"] = record.Text("title")
			}
			if values["body"] == nil {
				values["body"] = record.Text("description")
			}
		}
	}
	sourceParam := lineageTaskParam("sourceID", false)
	sourceParam.Required = true
	m := a.newLaunchModal("Rewrite Task", "Create Rewrite", []core.ParamSpec{sourceParam, lineageTitleParam(true), lineageBodyParam()}, values, "", submit)
	return core.Next(a.pushModal(s, m))
}

func (a *App) taskSplit(s core.State, c core.Command) core.StepResult {
	source := a.lineageSource(s, c)
	submit := func(st core.State, values map[string]any) (core.State, bool) {
		id, err := lineageTaskID(values["sourceID"])
		if err != nil {
			a.fail("split task", err)
			return st, false
		}
		parts, err := lineageSplitParts(values["parts"])
		if err != nil {
			a.fail("split task", err)
			return st, false
		}
		records, err := a.Store.SplitTask(id, parts, lineageActor(c))
		if err != nil {
			a.fail("split task", err)
			return st, false
		}
		for i := range records {
			a.markOwnerCreated(records[i].Ref())
		}
		a.say("created %d split tasks · u undoes", len(records))
		return st, true
	}
	if source != nil && c.Args["parts"] != nil {
		next, _ := submit(s, map[string]any{"sourceID": source, "parts": c.Args["parts"]})
		return core.Next(next)
	}
	values := map[string]any{"sourceID": source}
	if parts, err := lineageSplitParts(c.Args["parts"]); err == nil && len(parts) != 0 {
		var initial []string
		for _, part := range parts {
			text := part.Title
			if part.Body != "" {
				text += "\n" + part.Body
			}
			initial = append(initial, text)
		}
		values["parts"] = initial
	}
	sourceParam := lineageTaskParam("sourceID", false)
	sourceParam.Required = true
	m := a.newLaunchModal("Split Task", "Create Split Tasks", []core.ParamSpec{sourceParam, lineagePartsParam(true)}, values, "", submit)
	return core.Next(a.pushModal(s, m))
}

func (a *App) taskJoin(s core.State, c core.Command) core.StepResult {
	ids, err := lineageTaskIDs(c.Args["sourceIDs"])
	if err != nil {
		a.fail("join tasks", err)
		return core.Next(s)
	}
	if c.Args["sourceIDs"] == nil {
		refs := c.Targets
		if len(refs) == 0 {
			refs, _ = a.targetRefs(s, c)
		}
		for _, ref := range refs {
			if ref.Kind == data.KindTask {
				ids = append(ids, ref.ID)
			}
		}
	}
	submit := func(st core.State, values map[string]any) (core.State, bool) {
		ids, err := lineageTaskIDs(values["sourceIDs"])
		if err != nil {
			a.fail("join tasks", err)
			return st, false
		}
		record, err := a.Store.JoinTasks(ids, data.Text(values["title"]), data.Text(values["body"]), lineageActor(c))
		return st, a.finishTaskLineage("joined tasks", record, err)
	}
	values := map[string]any{"sourceIDs": ids, "title": c.Args["title"], "body": c.Args["body"]}
	if len(ids) > 1 && c.Arg("title") != "" {
		next, _ := submit(s, values)
		return core.Next(next)
	}
	for i, id := range ids {
		ids[i] = data.RefText(core.Ref{Kind: data.KindTask, ID: id})
	}
	sourcesParam := lineageTaskParam("sourceIDs", true)
	sourcesParam.Required = true
	m := a.newLaunchModal("Join Tasks", "Create Joined Task", []core.ParamSpec{sourcesParam, lineageTitleParam(true), lineageBodyParam()}, values, "", submit)
	return core.Next(a.pushModal(s, m))
}

func (a *App) finishTaskLineage(label string, record *data.Record, err error) bool {
	if err != nil {
		a.fail(label, err)
		return false
	}
	a.markOwnerCreated(record.Ref())
	a.say("%s · %s · u undoes", label, record.Text("title"))
	return true
}
