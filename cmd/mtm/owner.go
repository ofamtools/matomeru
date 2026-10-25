package main

import (
	"errors"
	"fmt"

	"github.com/unixsuperhero/matomeru/internal/store"
	"github.com/unixsuperhero/ochazuke"
)

// Owner is captured at dispatch, never recomputed when an editor or job finishes.
// Notice explains conflicting or unknown explicit associations without losing input.
type Owner struct {
	ProjectID string `json:"project_id"`
	TaskID    string `json:"task_id"`
	Notice    string `json:"notice,omitempty"`
}

func (a *App) resolveOwner(ctx ochazuke.Ctx, args ochazuke.Arguments) (Owner, error) {
	if scratch, _ := args["scratch"].(bool); scratch {
		return Owner{}, nil
	}
	project, hasProject := args["project"]
	task, hasTask := args["task"]
	if hasTask {
		id, _ := task.(string)
		if id == "" {
			return Owner{}, nil
		}
		t, err := a.work.Task(id)
		if errors.Is(err, store.ErrNotFound) {
			return Owner{Notice: fmt.Sprintf("unknown task %s; saved unassociated", id)}, nil
		}
		if err != nil {
			return Owner{}, err
		}
		owner := Owner{ProjectID: t.ProjectID, TaskID: t.ID}
		if hasProject && project != t.ProjectID {
			owner.Notice = "task owner overrides explicit project"
		}
		return owner, nil
	}
	if hasProject {
		id, _ := project.(string)
		if id == "" {
			return Owner{}, nil
		}
		if _, ok := a.project(id); ok {
			return Owner{ProjectID: id}, nil
		}
		return Owner{Notice: fmt.Sprintf("unknown project %s; saved unassociated", id)}, nil
	}
	if !a.cli && ctx.Instance != nil {
		switch ctx.Instance.Noun {
		case "task":
			return a.resolveOwner(ctx, ochazuke.Arguments{"task": ctx.Instance.ID})
		case "project":
			return a.resolveOwner(ctx, ochazuke.Arguments{"project": ctx.Instance.ID})
		case "review":
			review, err := a.work.Review(ctx.Instance.ID)
			if err != nil {
				return Owner{}, err
			}
			return Owner{ProjectID: review.ProjectID, TaskID: review.TaskID}, nil
		case "draft":
			if record, ok := ctx.Instance.Data.(editRecord); ok {
				return Owner{ProjectID: record.ProjectID}, nil
			}
		case "record-editor":
			if a.editor != nil {
				record := a.editor.Record
				owner := Owner{ProjectID: record.ProjectID}
				if record.Noun == "task" && !record.New {
					owner.TaskID = record.ID
				}
				return owner, nil
			}
		case "unassociated":
			return Owner{}, nil
		}
		if ctx.Instance.ID == "tasks" {
			return Owner{ProjectID: a.current}, nil
		}
	}
	// Lane A supplies the persisted pin here. No pin exists in Step 0.
	return Owner{}, nil
}

// inScope is the single list filter. Lane A activates pin/all state here.
func (a *App) inScope(owner string) bool { return true }
