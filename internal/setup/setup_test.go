package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func opts(t *testing.T) Options {
	return Options{Home: t.TempDir(), Bin: "/opt/mtm/bin/mtm", Data: "/data/mtm.db"}
}

// Installing the Claude Code hooks keeps every other key and hook as it
// was, twice is a no-op, and uninstalling gives back the original content.
func TestClaudeHooksKeepEverythingElse(t *testing.T) {
	o := opts(t)
	path := filepath.Join(o.Home, ".claude", "settings.json")
	original := `{
  "model": "opus",
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "tuios agent-hook claude-code --integration 3",
            "timeout": 5
          }
        ]
      }
    ]
  },
  "theme": "dark"
}
`
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if it, _ := o.Status(Claude); it.Status != "missing" {
		t.Fatalf("before: %+v", it)
	}
	it, err := o.Install(Claude)
	if err != nil || it.Status != "installed" {
		t.Fatalf("install %+v %v", it, err)
	}
	data, _ := os.ReadFile(path)
	var doc struct {
		Model string                                                  `json:"model"`
		Hooks map[string][]struct{ Hooks []struct{ Command string } } `json:"hooks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Model != "opus" || len(doc.Hooks["Stop"]) != 2 || doc.Hooks["Stop"][0].Hooks[0].Command != "tuios agent-hook claude-code --integration 3" || len(doc.Hooks["UserPromptSubmit"]) != 1 {
		t.Fatalf("installed:\n%s", data)
	}
	cmd := doc.Hooks["Stop"][1].Hooks[0].Command
	if cmd != o.HookCommand("claude-code") || !strings.Contains(cmd, "'/opt/mtm/bin/mtm' --data '/data/mtm.db'") || strings.Contains(string(data), "\\u0026") {
		t.Fatalf("command %q\n%s", cmd, data)
	}
	if strings.Index(string(data), `"model"`) > strings.Index(string(data), `"hooks"`) || strings.Index(string(data), `"theme"`) < strings.Index(string(data), `"hooks"`) {
		t.Fatalf("key order changed:\n%s", data)
	}
	if mode, _ := os.Stat(path); mode.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", mode.Mode())
	}
	if bak, _ := os.ReadFile(path + ".mtm.bak"); string(bak) != original {
		t.Fatalf("backup %q", bak)
	}
	if it, _ := o.Install(Claude); it.Status != "installed" {
		t.Fatalf("again %+v", it)
	}
	// A new binary makes the hooks outdated; install replaces them.
	moved := o
	moved.Bin = "/usr/local/bin/mtm"
	if it, _ := moved.Status(Claude); it.Status != "outdated" {
		t.Fatalf("moved %+v", it)
	}
	if _, err := moved.Install(Claude); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); strings.Count(string(data), hookMark) != 2 {
		t.Fatalf("reinstalled:\n%s", data)
	}
	if it, err := moved.Uninstall(Claude); err != nil || it.Status != "missing" {
		t.Fatalf("uninstall %+v %v", it, err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != original {
		t.Fatalf("after uninstall:\n%s\nwant:\n%s", after, original)
	}
}

// Codex's hooks.json, from nothing, and back to no hooks at all.
func TestCodexHooksFromNothing(t *testing.T) {
	o := opts(t)
	path := filepath.Join(o.Home, ".codex", "hooks.json")
	if _, err := o.Install(Codex); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "capture-hook codex") || !strings.Contains(string(data), "UserPromptSubmit") {
		t.Fatalf("hooks.json:\n%s", data)
	}
	if _, err := o.Uninstall(Codex); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); strings.TrimSpace(string(data)) != "{}" {
		t.Fatalf("after uninstall:\n%s", data)
	}
}

// The OMP extension: an owned file with this mtm's binary and database;
// a file mtm does not own is never replaced or removed.
func TestOMPExtension(t *testing.T) {
	o := opts(t)
	p, err := o.Plan(OMP)
	if err != nil || p.Path != filepath.Join(o.Home, ".omp", "agent", "extensions", "matomeru-omp.ts") || p.Status != "missing" {
		t.Fatalf("plan %+v %v", p.Item, err)
	}
	if !strings.HasPrefix(p.Content, ompOwner) || !strings.Contains(p.Content, `const capture = {"bin":"/opt/mtm/bin/mtm","data":"/data/mtm.db"};`) {
		t.Fatalf("content:\n%s", p.Content)
	}
	if _, err := o.Install(OMP); err != nil {
		t.Fatal(err)
	}
	if it, _ := o.Status(OMP); it.Status != "installed" {
		t.Fatalf("status %+v", it)
	}
	if _, err := o.Uninstall(OMP); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Path, []byte("// someone else's\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Install(OMP); err == nil {
		t.Fatal("replaced an unowned file")
	}
	if _, err := o.Uninstall(OMP); err == nil {
		t.Fatal("removed an unowned file")
	}
}
