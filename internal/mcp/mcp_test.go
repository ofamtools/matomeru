package mcp

import (
	"encoding/json"
	"testing"
)

func TestIdentityFromEnv(t *testing.T) {
	t.Parallel()
	env := map[string]string{"TUIOS_SESSION": "work", "TUIOS_PANE_ID": "w1"}
	id := IdentityFromEnv(func(k string) string { return env[k] })
	if id != (Identity{Session: "work", Window: "w1", Bound: true}) {
		t.Fatalf("tuios env: %+v", id)
	}
	env["MTM_SESSION"], env["MTM_WINDOW"] = "s2", "w2"
	if id := IdentityFromEnv(func(k string) string { return env[k] }); id.Session != "s2" || id.Window != "w2" {
		t.Fatalf("MTM_ overrides: %+v", id)
	}
	if id := IdentityFromEnv(func(string) string { return "" }); id.Bound {
		t.Fatalf("no env is unbound: %+v", id)
	}
}

func TestConfigEntries(t *testing.T) {
	t.Parallel()
	cmd := Command{Executable: "/bin/mtm", Args: []string{"--data", "/d/mtm.db", "--server", "mtm-daemon", "mcp", "serve"}}
	env := func(string) string { return "" }
	claude, err := Config(cmd, "claude", "/home/u", env)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(claude)
	want := `{"harness":"claude","file":"/home/u/.claude.json","args":["--mcp-config","{\"mcpServers\":{\"mtm\":{\"args\":[\"--data\",\"/d/mtm.db\",\"--server\",\"mtm-daemon\",\"mcp\",\"serve\"],\"command\":\"/bin/mtm\",\"type\":\"stdio\"}}}"],"entry":{"mcpServers":{"mtm":{"args":["--data","/d/mtm.db","--server","mtm-daemon","mcp","serve"],"command":"/bin/mtm","type":"stdio"}}}}`
	if string(raw) != want {
		t.Fatalf("claude config:\n%s\nwant\n%s", raw, want)
	}
	codex, _ := Config(cmd, "codex", "/home/u", env)
	if codex.File != "/home/u/.codex/config.toml" || codex.Args[1] != `mcp_servers.mtm.command="/bin/mtm"` {
		t.Fatalf("codex config: %+v", codex)
	}
	if omp, _ := Config(cmd, "omp", "/home/u", env); omp.File != "/home/u/.omp/agent/mcp.json" {
		t.Fatalf("omp config: %+v", omp)
	}
	if oc, _ := Config(cmd, "opencode", "/home/u", env); oc.File != "/home/u/.config/opencode/opencode.json" {
		t.Fatalf("opencode config: %+v", oc)
	}
	if _, err := Config(cmd, "vim", "/home/u", env); err == nil {
		t.Fatal("an unknown harness was configured")
	}
}

func TestAliasesKeepNamesAndArgumentMappings(t *testing.T) {
	t.Parallel()
	want := map[string]string{"whoami": "pane.whoami", "task_list": "task.list", "task_done": "task.done", "task_status": "task.status", "task_note_add": "note.new", "context_add": "context-item.add", "document_report": "context-item.report", "report_progress": "pane.progress"}
	aliases := Aliases()
	if len(aliases) != len(want) {
		t.Fatalf("aliases: %v", aliases)
	}
	for _, alias := range aliases {
		if want[alias.Tool.Name] != alias.Verb || alias.Hidden {
			t.Errorf("alias: %+v", alias)
		}
		delete(want, alias.Tool.Name)
	}
	if len(want) > 0 {
		t.Fatal("missing", want)
	}
	for _, alias := range aliases {
		if alias.Tool.Name == "task_status" && (alias.Args["task_id"] != "id" || alias.Args["status"] != "value") {
			t.Fatalf("task_status mapping: %v", alias.Args)
		}
	}
}
