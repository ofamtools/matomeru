package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

func makerTasks(t *testing.T, a *App) {
	t.Helper()
	for i, row := range []struct{ id, maker string }{{"owner", work.ActorOwner}, {"agent", work.ActorAgent}, {"unknown", work.ActorUnknown}} {
		if err := a.work.PutTask(work.Task{ID: row.id, ProjectID: "matomeru", Title: row.id + " example", Status: work.StatusTodo,
			Position: int64(i + 10), CreatedBy: work.Actor{Kind: row.maker}}); err != nil {
			t.Fatal(err)
		}
	}
	executeA(t, a, "project", "pin", "", ochazuke.Arguments{"project": "matomeru"})
}

func assertMakerFrame(t *testing.T, a *App, name string) {
	t.Helper()
	f := frameAt(a, 140)
	for _, maker := range []string{"owner", "agent", "unknown"} {
		want := name == "all" || name == "mine" && maker == "owner" || name == "agents" && maker == "agent"
		if strings.Contains(f, maker+" example") != want {
			t.Fatalf("filter %s, maker %s, want visible %v:\n%s", name, maker, want, f)
		}
	}
	if !strings.Contains(f, "["+name+"]") {
		t.Fatalf("filter strip lacks saved name %s:\n%s", name, f)
	}
}

func TestSavedTaskFiltersCycleWorkAndModal(t *testing.T) {
	t.Parallel()
	a := open(t)
	makerTasks(t, a)
	if err := a.taskList.Focus(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mine", "agents", "all"} {
		press(t, a, "z", "]")
		assertMakerFrame(t, a, name)
	}
	press(t, a, "z", "[")
	assertMakerFrame(t, a, "agents")
	// A data refresh keeps the session's filter, rather than reapplying
	// the Work startup setting (all).
	if err := a.refreshTasks(); err != nil {
		t.Fatal(err)
	}
	assertMakerFrame(t, a, "agents")
	executeA(t, a, "project", "outline", "matomeru", nil)
	for _, name := range []string{"mine", "agents", "all"} {
		press(t, a, "z", "]")
		assertMakerFrame(t, a, name)
	}
	press(t, a, "z", "[")
	assertMakerFrame(t, a, "agents")
}

func TestSavedTaskFiltersCLI(t *testing.T) {
	t.Parallel()
	a := open(t)
	makerTasks(t, a)
	for _, name := range []string{"mine", "agents", "all"} {
		out := mustCLI(t, a, "task", "list", "--all", "--filter", name)
		for _, maker := range []string{"owner", "agent", "unknown"} {
			want := name == "all" || name == "mine" && maker == "owner" || name == "agents" && maker == "agent"
			if strings.Contains(out, maker+" example") != want {
				t.Fatalf("CLI filter %s, maker %s, want visible %v: %s", name, maker, want, out)
			}
		}
	}
}

func TestSavedTaskFiltersMigration(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"mine", "agents", "all"} {
		t.Run(name, func(t *testing.T) {
			path := seeded(t)
			settings := filepath.Join(filepath.Dir(path), ochazuke.SettingsFile)
			if err := os.WriteFile(settings, []byte(`{"tasks.made-by":"`+name+`","tasks.agent-glyph":false}`), 0600); err != nil {
				t.Fatal(err)
			}
			a := openAt(t, path)
			makerTasks(t, a)
			assertMakerFrame(t, a, name)
			if got := a.r.Settings().String(settingTasksMadeBy); got != name {
				t.Fatalf("old alias = %q", got)
			}
			raw, err := os.ReadFile(settings)
			if err != nil {
				t.Fatal(err)
			}
			var values map[string]any
			if err := json.Unmarshal(raw, &values); err != nil {
				t.Fatal(err)
			}
			if _, old := values[settingTasksMadeBy]; old || values[settingWorkTasksFilter] != name || values[settingTasksAgentGlyph] != false {
				t.Fatalf("migration lost values: %s", raw)
			}
			// A fresh app neither rewrites the migration nor reapplies it
			// over a new default chosen through the old alias.
			if err := a.r.Settings().Set(settingTasksMadeBy, "agents"); err != nil {
				t.Fatal(err)
			}
			fresh := openAt(t, path)
			assertMakerFrame(t, fresh, "agents")
		})
	}
}

func TestSavedTaskFiltersKeepAncestorContext(t *testing.T) {
	t.Parallel()
	a := open(t)
	makerTasks(t, a)
	child, err := a.work.Task("owner")
	if err != nil {
		t.Fatal(err)
	}
	child.ParentID = "agent"
	if err := a.work.PutTask(child); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshTasks(); err != nil {
		t.Fatal(err)
	}
	if err := a.taskList.ApplyFilter("mine"); err != nil {
		t.Fatal(err)
	}
	f := frameAt(a, 140)
	if !strings.Contains(f, "owner example") || !strings.Contains(f, "agent example") || strings.Contains(f, "unknown example") {
		t.Fatalf("matching descendant needs its ancestor:\n%s", f)
	}
	if err := a.r.Settings().Set(settingWorkTasksLayout, tasksLayoutFlat); err != nil {
		t.Fatal(err)
	}
	// Flattening removes the ancestor's matching context. It lingers
	// under the active filter until its deadline or an explicit flush.
	if f := frameAt(a, 140); !a.taskList.Lingering("agent") || !strings.Contains(f, "agent example") || !strings.Contains(f, "~") {
		t.Fatalf("former ancestor must linger after flattening:\n%s", f)
	}
	executeA(t, a, "ochazuke.list", "flush-lingering", "", ochazuke.Arguments{"id": "agent"})
	if a.taskList.Lingering("agent") {
		t.Fatal("flushed ancestor still lingering")
	}
	assertMakerFrame(t, a, "mine")
}

func TestSavedTaskFiltersOutlineEdits(t *testing.T) {
	t.Parallel()
	a := open(t)
	makerTasks(t, a)
	executeA(t, a, "project", "outline", "matomeru", nil)
	if err := a.outline.tree.List().ApplyFilter("mine"); err != nil {
		t.Fatal(err)
	}
	focusRow(t, a, outlineNode("owner"))
	press(t, a, "o")
	typeText(t, a, "new owner task")
	press(t, a, "esc")
	if f := frameAt(a, 140); !strings.Contains(f, "new owner task") || !strings.Contains(f, "[mine]") {
		t.Fatalf("newly written task lost its maker props:\n%s", f)
	}
}

func TestSavedTaskFiltersCanonicalMigrationWins(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	settings := filepath.Join(filepath.Dir(path), ochazuke.SettingsFile)
	if err := os.WriteFile(settings, []byte(`{"tasks.made-by":"agents","work.tasks-filter":"mine"}`), 0600); err != nil {
		t.Fatal(err)
	}
	a := openAt(t, path)
	makerTasks(t, a)
	assertMakerFrame(t, a, "mine")
	before, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(before), "tasks.made-by") {
		t.Fatalf("old alias not removed: %s", before)
	}
	fresh := openAt(t, path)
	assertMakerFrame(t, fresh, "mine")
	after, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("reopening rewrote a completed migration: %s -> %s", before, after)
	}
}
