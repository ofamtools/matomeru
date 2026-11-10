package main

import (
	"context"
	"ofam.dev/ochazuke"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldOptionalPropsNewAsksBeforeCreating(t *testing.T) {
	t.Parallel()
	for _, noun := range []string{"action-item", "saved", "note", "schedule"} {
		t.Run(noun, func(t *testing.T) {
			t.Parallel()
			a := open(t)
			args := ochazuke.Arguments{}
			for _, p := range a.r.Noun(noun).Props {
				if p.Role == ochazuke.RoleLabel {
					args[p.Name] = "a stored record"
				}
			}
			if _, err := a.r.Execute(ochazuke.Request{Noun: noun, Verb: "create", Args: args}); err != nil {
				t.Fatal(err)
			}
			if err := a.r.OpenIndex(noun, ""); err != nil {
				t.Fatal(err)
			}
			list := a.r.List(ochazuke.IndexPageID(noun) + "/list")
			if err := list.Focus(); err != nil {
				t.Fatal(err)
			}
			press(t, a, "n")
			if a.r.RecordEditor(ochazuke.ArgsFormID) == nil && a.editor == nil && a.r.RecordEditor("schedule-form") == nil {
				t.Fatalf("new %s did not open a form:\n%s", noun, frame(a))
			}
		})
	}
}

func TestScaffoldSavedVimScheduleArchivesPersist(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	a := openAt(t, path)
	file := filepath.Join(t.TempDir(), "session.vim")
	if err := os.WriteFile(file, []byte("let g:kept = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var saved, vim, schedule map[string]any
	cliJSON(t, a, &saved, "saved", "create", "--name", "kept recipe", "--cwd", "/tmp")
	cliJSON(t, a, &vim, "vim", "create", "--name", "kept session", "--path", file)
	cliJSON(t, a, &schedule, "schedule", "create", "--label", "kept schedule", "--prompt", "Keep this prompt", "--when", "every 30m")
	for _, row := range []struct{ noun, key, value, prop string }{
		{"saved", "id", saved["id"].(string), "name"},
		{"vim", "id", vim["id"].(string), "path"},
		{"schedule", "name", schedule["name"].(string), "prompt"},
	} {
		executeA(t, a, row.noun, "archive", "", ochazuke.Arguments{row.key: row.value})
	}
	b := openAt(t, path)
	for _, row := range []struct{ noun, key, value, prop, want string }{
		{"saved", "id", saved["id"].(string), "name", "kept recipe"},
		{"vim", "id", vim["id"].(string), "path", file},
		{"schedule", "name", schedule["name"].(string), "prompt", "Keep this prompt"},
	} {
		got := executeA(t, b, row.noun, "get", "", ochazuke.Arguments{row.key: row.value}).(map[string]any)
		if got["archived"] != true || got[row.prop] != row.want {
			t.Fatalf("reopened %s: %v", row.noun, got)
		}
		executeA(t, b, row.noun, "restore", "", ochazuke.Arguments{row.key: row.value})
		got = executeA(t, b, row.noun, "get", "", ochazuke.Arguments{row.key: row.value}).(map[string]any)
		if got["archived"] != false || got[row.prop] != row.want {
			t.Fatalf("restored %s: %v", row.noun, got)
		}
	}
	if body, err := os.ReadFile(file); err != nil || string(body) != "let g:kept = 1\n" {
		t.Fatalf("archive changed session file: %q %v", body, err)
	}
}

func TestScaffoldVimDuplicateReferencesButDoesNotOwnSourceFile(t *testing.T) {
	t.Parallel()
	a := open(t)
	source := vimSession(t, a)
	var duplicate map[string]any
	cliJSON(t, a, &duplicate, "vim", "duplicate", source.ID)
	id := duplicate["id"].(string)
	got, err := a.work.VimSession(id)
	if err != nil || id == source.ID || got.Path != source.Path || got.OwnedPath != "" || got.WindowID != "" || got.State != "closed" {
		t.Fatalf("duplicate: %+v %v", got, err)
	}
	mustCLI(t, a, "vim", "delete", id, "--yes")
	if _, err := os.Stat(source.Path); err != nil {
		t.Fatalf("deleting metadata copy touched source file: %v", err)
	}
	if _, err := a.work.VimSession(source.ID); err != nil {
		t.Fatal(err)
	}
}

func TestScaffoldScheduleArchivePausesWithoutChangingTimingOrHistory(t *testing.T) {
	t.Parallel()
	a, _, _ := scheduleApp(t)
	mustCLI(t, a, "schedule", "create", "--label", "Keep runs", "--prompt", "keep this", "--when", "every 30m")
	executeA(t, a, "schedule", "archive", "", ochazuke.Arguments{"name": "keep-this"})
	d, ok := a.scheduleDef("keep-this")
	if !ok || !d.Archived || d.Enabled != "on" || d.When().Text() != "every 30m" {
		t.Fatalf("archived definition: %+v", d)
	}
	if active, why := a.sched.runner.Active(context.Background(), d); active || why != "archived" {
		t.Fatalf("archived schedule active=%v why=%s", active, why)
	}
	executeA(t, a, "schedule", "restore", "", ochazuke.Arguments{"name": "keep-this"})
	executeA(t, a, "schedule", "set", "", ochazuke.Arguments{"name": "keep-this", "props": map[string]any{"@label": "Renamed display"}})
	got := executeA(t, a, "schedule", "get", "", ochazuke.Arguments{"name": "keep-this"}).(map[string]any)
	if got["name"] != "keep-this" || got["label"] != "Renamed display" || got["archived"] != false {
		t.Fatal(got)
	}
	if err := a.r.OpenIndex("schedule", "keep-this"); err != nil {
		t.Fatal(err)
	}
	if fr := frame(a); !strings.Contains(fr, "Renamed display") {
		t.Fatal(fr)
	}
}
