package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/ochazuke/core"
)

func TestSchemaCommandsAndConsumers(t *testing.T) {
	t.Setenv("MTM_CONFIG_DIR", t.TempDir())
	db := filepath.Join(t.TempDir(), "mtm.db")
	c := cli{t: t, db: db}
	g := globalOpts{db: db}
	mcpStore, source, err := openConfiguredStore(g)
	if err != nil {
		t.Fatal(err)
	}
	defer mcpStore.Close()
	box := newCLIToolbox(g, mcpStore)
	box.RefreshSchemas = source.Refresh
	if _, err := box.Call(t.Context(), core.Actor{}, "data.schemas", json.RawMessage(`{"kind":"task"}`)); err != nil {
		t.Fatal(err)
	}
	if got := c.ok("schema.noun.add", "--kind", "reading", "--summaryField", "label"); !strings.Contains(got, "Added noun") {
		t.Fatal(got)
	}
	if got := c.ok("schema.prop.add", "--noun", "task", "--name", "estimate", "--type", "int", "--default", "3"); !strings.Contains(got, "Added property") {
		t.Fatal(got)
	}
	reading := decode[record](t, c.ok("reading", "create", "--title", "A book"))
	if reading.Kind != "reading" || reading.Fields["label"] != "A book" {
		t.Fatalf("reading = %+v", reading)
	}
	task := decode[record](t, c.ok("task", "create", "--title", "An estimate"))
	if task.Fields["estimate"] != float64(3) {
		t.Fatalf("task = %+v", task)
	}
	task = decode[record](t, c.ok("task", "edit", task.ID, "--field", "estimate=5"))
	if task.Fields["estimate"] != float64(5) {
		t.Fatalf("edited task = %+v", task)
	}
	if got := c.ok("mcp", "call", "data.schemas", `{"kind":"task"}`); !strings.Contains(got, `"name": "estimate"`) {
		t.Fatalf("MCP schemas = %s", got)
	}
	if _, err := box.Call(t.Context(), core.Actor{}, "data.schemas", json.RawMessage(`{"kind":"task"}`)); err != nil {
		t.Fatal(err)
	}
	taskSchema, _ := mcpStore.Registry().Get("task")
	if _, ok := taskSchema.Field("estimate"); !ok {
		t.Fatal("the existing MCP server did not see the new field")
	}
}
