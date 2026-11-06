package main

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

func TestProjectReferencesResolveNamesAcrossStores(t *testing.T) {
	t.Parallel()
	a, _ := sessionsApp(t)
	if err := a.work.PutProject(work.Project{ID: "project-reference-id", Name: "Readable project"}); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
	if err := a.work.PutVimSession(work.VimSession{ID: "reference-vim", Name: "vim", State: "closed"}); err != nil {
		t.Fatal(err)
	}
	rows := []struct{ noun, id string }{{"pane", "w1"}, {"session", "id-matomeru"}, {"vim", "reference-vim"}}
	for _, seed := range []struct {
		noun   string
		values ochazuke.Arguments
	}{
		{"saved", ochazuke.Arguments{"name": "saved"}},
		{contextNoun, ochazuke.Arguments{"type": "link", "text": "https://example.com"}},
		{actionItemNoun, ochazuke.Arguments{"title": "item"}},
		{"note", ochazuke.Arguments{"text": "note"}},
		{"task", ochazuke.Arguments{"title": "task"}},
	} {
		thing := executeA(t, a, seed.noun, "create", "", seed.values).(map[string]any)
		rows = append(rows, struct{ noun, id string }{seed.noun, thing["id"].(string)})
	}
	for _, row := range rows {
		t.Run(row.noun, func(t *testing.T) {
			for _, ref := range []string{"project-reference-id", "Readable project", "READABLE PROJECT", "missing project", ""} {
				want := "project-reference-id"
				if ref == "missing project" || ref == "" {
					want = ""
				}
				executeA(t, a, row.noun, "set", "", ochazuke.Arguments{"id": row.id, "props": map[string]any{"project": ref}})
				thing := executeA(t, a, row.noun, "get", "", ochazuke.Arguments{"id": row.id}).(map[string]any)
				if thing["project"] != want {
					t.Fatalf("%q saved project %v, want %q", ref, thing["project"], want)
				}
				if ref == "missing project" && !strings.Contains(a.m.Status+frame(a), "unknown project missing project") {
					t.Fatal("missing project saved without a notice")
				}
			}
		})
	}
	task := executeA(t, a, "task", "create", "", ochazuke.Arguments{"title": "owned task", "project": "Readable project"}).(map[string]any)
	taskID := task["id"].(string)
	executeA(t, a, "pane", "set", "", ochazuke.Arguments{"id": "w1", "props": map[string]any{"task": taskID}})
	executeA(t, a, "pane", "set", "", ochazuke.Arguments{"id": "w1", "props": map[string]any{"project": "Readable project"}})
	if owner, err := a.work.PaneOwner("w1", ""); err != nil || owner.TaskID != taskID || owner.ProjectID != "project-reference-id" {
		t.Fatalf("name within task's project lost ownership: %+v %v", owner, err)
	}
	owner, err := a.resolveOwner(ochazuke.Ctx{}, ochazuke.Arguments{"project": "Readable project", "task": taskID})
	if err != nil || owner.Notice != "" || owner.ProjectID != "project-reference-id" || owner.TaskID != taskID {
		t.Fatalf("same project by name conflicts with task: %+v %v", owner, err)
	}
	project, err := a.startProject(ochazuke.Ctx{Args: ochazuke.Arguments{"project": "Readable project"}})
	if err != nil || project.ID != "project-reference-id" {
		t.Fatalf("start project by name: %+v %v", project, err)
	}
	madeSchedule := executeA(t, a, scheduleNoun, "create", "", ochazuke.Arguments{"prompt": "reference check", "when": "every 30m", "project": "Readable project"}).(map[string]any)
	scheduleName := madeSchedule["name"].(string)
	if def, _ := a.scheduleDef(scheduleName); def.Agent.Project != "project-reference-id" {
		t.Fatalf("schedule project by name: %+v", def.Agent)
	}
	executeA(t, a, scheduleNoun, "set", "", ochazuke.Arguments{"name": scheduleName, "props": map[string]any{"project": "missing project"}})
	if def, _ := a.scheduleDef(scheduleName); def.Agent.Project != "" || !strings.Contains(a.m.Status+frame(a), "unknown project missing project") {
		t.Fatalf("unknown schedule project: %+v", def.Agent)
	}
	executeA(t, a, "project", "make-task", "matomeru", ochazuke.Arguments{"parent": "Readable project"})
	if made := taskTitled(tasksOf(t, a, "project-reference-id"), "matomeru"); made.ID == "" || made.ProjectID != "project-reference-id" {
		t.Fatalf("conversion parent by name: %+v", made)
	}
}
