package main

import (
	"encoding/json"
	"slices"
	"testing"
)

// An application's MCP command keeps its tools when the framework also
// supplies MCP. The framework server remains under its full namespace.
func TestMCPServeRoutesToAppAndFrameworkStaysNamespaced(t *testing.T) {
	t.Parallel()
	app, _, _ := mcpApp(t, nil)
	responses := rpc(t, app,
		map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-06-18"}},
		map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"},
		map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"},
	)
	var names []string
	for _, response := range responses {
		result, _ := response["result"].(map[string]any)
		tools, _ := result["tools"].([]any)
		for _, tool := range tools {
			names = append(names, tool.(map[string]any)["name"].(string))
		}
	}
	slices.Sort(names)
	want := []string{"context_add", "document_report", "report_progress", "task_done", "task_list", "task_note_add", "task_status", "whoami"}
	if !slices.Equal(names, want) {
		t.Fatalf("mtm mcp serve reached tools %v, want %v", names, want)
	}
	out, stderr, err := mtm(app, t.TempDir(), "ochazuke", "mcp", "tools")
	if err != nil {
		t.Fatalf("framework tools: %v\n%s", err, stderr)
	}
	var framework []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &framework); err != nil {
		t.Fatal(err)
	}
	names = nil
	for _, tool := range framework {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"catalog", "describe", "run", "state"}) {
		t.Fatalf("framework MCP tools: %v", names)
	}
}
