package main

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/unixsuperhero/matomeru/internal/mcp"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

func (a *App) currentPaneIdentity() (mcp.Identity, error) {
	id := mcp.IdentityFromEnv(a.getenv)
	if actor := a.work.Actor(); actor.IsAgent() {
		id.Session, id.Window = actor.Session, actor.Pane
		id.Bound = id.Window != ""
	}
	return a.identifyPane(id)
}

func (a *App) paneWhoami(ochazuke.Ctx) ochazuke.Result {
	id, err := a.currentPaneIdentity()
	if err != nil {
		return ochazuke.Failed(err)
	}
	origin := "none"
	if id.Bound {
		origin = "pane"
	}
	return ochazuke.Done(map[string]any{"session": id.Session, "window": id.Window, "task_id": id.TaskID, "project_id": id.ProjectID,
		"cwd": id.Cwd, "harness": id.Harness, "model": id.Model, "bound": id.Bound, "origin": map[string]string{"kind": origin},
		"task_title": id.TaskTitle, "task_status": id.TaskStatus, "task_version": id.TaskVersion, "project_name": id.ProjectName, "project_repo": id.ProjectRepo})
}

func (a *App) paneLane(tasks []work.Task) (func(work.Task) bool, error) {
	id, err := a.currentPaneIdentity()
	if err != nil {
		return nil, err
	}
	if id.TaskID == "" && id.ProjectID == "" {
		return func(work.Task) bool { return true }, nil
	}
	byID := map[string]work.Task{}
	for _, task := range tasks {
		byID[task.ID] = task
	}
	return func(t work.Task) bool {
		seen := map[string]bool{}
		for cur, ok := t, true; ok && !seen[cur.ID]; cur, ok = byID[cur.ParentID] {
			seen[cur.ID] = true
			if id.TaskID != "" {
				if cur.ID == id.TaskID {
					return true
				}
				continue
			}
			if cur.ProjectID != "" {
				return cur.ProjectID == id.ProjectID
			}
		}
		return false
	}, nil
}

// Evidence parameters enrich the standard task set; aliases are data presets.
// The original handler still records changes and owns validation and undo.
func (a *App) defineTaskEvidence() {
	set, _ := a.r.Operation("task.set")
	op := set
	op.Parameters = maps.Clone(set.Parameters)
	for _, name := range []string{"note", "summary", "verification", "commit", "blocker"} {
		op.Parameters[name] = ochazuke.Parameter{Type: "string"}
	}
	op.Parameters["expected_version"] = ochazuke.Parameter{Type: "integer", Description: "Reject a stale task version; 0 skips the check"}
	op.Parameters["require-summary"] = ochazuke.Parameter{Type: "boolean", Description: "Require a summary when setting a task done (task.status preset)"}
	op.App = func(c ochazuke.Ctx) ochazuke.Result {
		args := maps.Clone(c.Args)
		if args == nil {
			args = ochazuke.Arguments{}
		}
		id := ochazuke.StringArg(args, "id", "")
		if id == "" && c.Instance != nil && c.Instance.Noun == "task" {
			id = c.Instance.ID
		}
		if id == "" {
			pane, err := a.currentPaneIdentity()
			if err != nil {
				return ochazuke.Failed(err)
			}
			id = pane.TaskID
		}
		if id == "" {
			return ochazuke.Failed(errors.New("task id is required: the pane has no task"))
		}
		current, err := a.taskByID(id)
		if err != nil {
			return ochazuke.Failed(err)
		}
		if expected := thingInt(args["expected_version"]); expected != 0 && current.Updated != expected {
			return ochazuke.Failed(fmt.Errorf("task %s is at version %d, not %d; re-read it", id, current.Updated, expected))
		}
		value := ochazuke.StringArg(args, "value", "")
		if args["require-summary"] == true && value == work.StatusDone && strings.TrimSpace(ochazuke.StringArg(args, "summary", "")) == "" {
			return ochazuke.Failed(errors.New("summary is required to mark a task done"))
		}
		args["id"] = id
		c.Args = args
		// Evidence is saved by patchTask only after the checked task write succeeds.
		return set.App(c)
	}
	// Replacing just the app handler preserves standard identity and aliases.
	// Re-registering a standard operation would regenerate its checks unnecessarily.
	a.r.Operations[op.Key()] = op
}

// saveTaskEvidence is shared by all task.set spellings, including done/status.
func (a *App) saveTaskEvidence(before, after work.Task, args ochazuke.Arguments) error {
	lines := []string{"status: " + before.Status + " → " + after.Status}
	for _, name := range []string{"summary", "verification", "commit", "blocker"} {
		if text := strings.TrimSpace(ochazuke.StringArg(args, name, "")); text != "" {
			lines = append(lines, name+": "+text)
		}
	}
	note := strings.TrimSpace(ochazuke.StringArg(args, "note", ""))
	if len(lines) > 1 {
		if note != "" {
			note += "\n"
		}
		note += strings.Join(lines, "\n")
	}
	if note == "" {
		return nil
	}
	if _, err := a.work.AddNote(after.ProjectID, after.ID, note); err != nil {
		return err
	}
	return a.refreshNotes()
}

func (a *App) documentReport(c ochazuke.Ctx) ochazuke.Result {
	id, err := a.currentPaneIdentity()
	if err != nil {
		return ochazuke.Failed(err)
	}
	path := strings.TrimSpace(ochazuke.StringArg(c.Args, "path", ""))
	if path == "" {
		return ochazuke.Failed(errors.New("path is required"))
	}
	if !filepath.IsAbs(path) {
		if id.Cwd == "" {
			return ochazuke.Failed(errors.New("path must be absolute: the pane has no cwd"))
		}
		path = filepath.Join(id.Cwd, path)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".html":
	default:
		return ochazuke.Failed(errors.New("path must name a .md or .html file"))
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return ochazuke.Failed(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if !info.Mode().IsRegular() {
		return ochazuke.Failed(errors.New("path must be a regular file"))
	}
	title := strings.TrimSpace(ochazuke.StringArg(c.Args, "title", ""))
	if title == "" {
		title = filepath.Base(path)
	}
	kind := ochazuke.StringArg(c.Args, "kind", "report")
	summary := strings.TrimSpace(ochazuke.StringArg(c.Args, "summary", ""))
	out, err := a.r.Execute(ochazuke.Request{Noun: contextNoun, Verb: "add", Args: ochazuke.Arguments{"text": path, "kind": "file", "cwd": id.Cwd, "session": id.Session, "window": id.Window}})
	if err != nil {
		return ochazuke.Failed(err)
	}
	note, err := a.work.AddNote(id.ProjectID, id.TaskID, strings.TrimSpace(kind+": "+title+"\n"+path+"\n\n"+summary))
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshNotes(); err != nil {
		return ochazuke.Failed(err)
	}
	result := map[string]any{"path": path, "title": title, "kind": kind, "summary": summary, "task_id": id.TaskID, "project_id": id.ProjectID, "size": info.Size(), "note_id": note.ID}
	if added, ok := out.(extracted); ok && len(added.Items) > 0 {
		result["context_item_id"] = added.Items[0].ID
	}
	return ochazuke.Done(result)
}
