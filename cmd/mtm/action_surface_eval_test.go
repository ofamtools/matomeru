package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/app"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/ochazuke/core"
)

// TestCrossSurfaceActionConsistencyFiveWays verifies that core record actions
// produce consistent store state across ALL FIVE surfaces sharing one SQLite DB:
//  1. Key binding (TUI)
//  2. Binding Help (?) execution (TUI)
//  3. Command Palette (:) execution (TUI)
//  4. CLI dispatch (mtm)
//  5. MCP tool dispatch (mcpui)
func TestCrossSurfaceActionConsistencyFiveWays(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mtm-eval.db")
	c := cli{t: t, db: dbPath}

	// 4. CLI: create a task.
	created := decode[record](t, c.ok("task", "create", "--title", "Five Surface Eval Task", "--field", "priority=P1"))
	if created.ID == "" {
		t.Fatal("CLI task create returned empty ID")
	}
	ref := core.Ref{Kind: data.KindTask, ID: created.ID}

	// Open the same store in TUI + MCP.
	store, err := data.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	a, err := app.New(store, app.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := app.NewDriver(a, 120, 40)
	d.Send(core.CommandEvent{Command: core.Command{
		ID:   core.CmdPageOpen,
		Args: map[string]any{"id": app.SpaceWork},
	}})
	d.Keys("g", "t")
	_ = d.Frame()

	// 1. Key binding: m stars the task.
	d.Keys("m")
	gotCLI := decode[record](t, c.ok("task", "get", created.ID))
	if gotCLI.StarStatus != "starred" {
		t.Fatalf("after TUI binding m, CLI task get starStatus = %q, want starred", gotCLI.StarStatus)
	}

	// 2. Binding Help (?): select record.unstar and press Enter.
	d.Keys("?")
	if a.Modal() != "help" {
		t.Fatalf("? did not open help modal: %q", a.Modal())
	}
	d.Keys("/")
	d.Type("record.unstar")
	d.Keys("enter", "enter")
	gotCLI = decode[record](t, c.ok("task", "get", created.ID))
	if gotCLI.StarStatus == "starred" {
		t.Fatalf("after Help (?) record.unstar, CLI task get starStatus = %q, want unstarred", gotCLI.StarStatus)
	}

	// 3. Command Palette (:): execute record.star.
	d.Keys(":")
	if a.Modal() != "palette" {
		t.Fatalf(": did not open palette modal: %q", a.Modal())
	}
	d.Type("record.star")
	d.Keys("enter")
	gotCLI = decode[record](t, c.ok("task", "get", created.ID))
	if gotCLI.StarStatus != "starred" {
		t.Fatalf("after Palette record.star, CLI task get starStatus = %q, want starred", gotCLI.StarStatus)
	}

	// 4. CLI: unstar via mtm task unstar.
	c.ok("task", "unstar", created.ID)
	rec, err := store.Get(ref)
	if err != nil {
		t.Fatal(err)
	}
	if rec.IsStarred() {
		t.Fatal("CLI task unstar did not unstar record in shared store")
	}

	// 5. MCP: star via data.update and verify via CLI list --starred and MCP data.query.
	box := mcpui.NewToolbox(store, a.Router)
	rawArgs, _ := json.Marshal(map[string]any{
		"kind":   data.KindTask,
		"id":     created.ID,
		"fields": map[string]any{data.ColStarStatus: "starred"},
	})
	if _, err := box.Call(context.Background(), app.User, mcpui.ToolUpdate, rawArgs); err != nil {
		t.Fatalf("MCP %s failed: %v", mcpui.ToolUpdate, err)
	}
	starred := decode[[]record](t, c.ok("task", "list", "--starred"))
	if len(starred) != 1 || starred[0].ID != created.ID {
		t.Fatalf("after MCP data.update star, CLI task list --starred = %+v, want [%s]", starred, created.ID)
	}
	queryArgs, _ := json.Marshal(map[string]any{"kind": data.KindTask, "query": "Five Surface Eval"})
	if _, err := box.Call(context.Background(), app.User, mcpui.ToolQuery, queryArgs); err != nil {
		t.Fatalf("MCP %s failed: %v", mcpui.ToolQuery, err)
	}

	// Verify CLI binding check reports zero errors on the updated keymap/specs.
	rep := decode[app.BindingCheckReport](t, c.ok("binding", "check", "--json"))
	if len(rep.Errors) != 0 {
		t.Fatalf("mtm binding check reported errors: %+v", rep.Errors)
	}
}
