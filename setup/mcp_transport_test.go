package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readMTMEntry(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return root.MCPServers["mtm"]
}

func TestConfigureMCP_HTTPTransportMigration(t *testing.T) {
	home := t.TempDir()
	const url = "http://127.0.0.1:5000/mcp"
	stdio := Options{Home: home, Now: fixedClock()}
	http := Options{Home: home, MCPTransport: MCPTransportHTTP, MCPURL: url, Now: fixedClock()}

	if _, err := ConfigureMCP(stdio); err != nil {
		t.Fatal(err)
	}
	// Existing stdio entries are left alone without --force.
	acts, err := ConfigureMCP(http)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range acts {
		if a.Status != StatusUnchanged {
			t.Fatalf("%s without force: status %s, want unchanged", a.Target, a.Status)
		}
	}

	// --check --force reports drift without writing.
	check := http
	check.Check, check.Force = true, true
	acts, err = ConfigureMCP(check)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range acts {
		if a.Status != StatusOutdated {
			t.Fatalf("%s check: status %s, want outdated", a.Target, a.Status)
		}
	}

	force := http
	force.Force = true
	acts, err = ConfigureMCP(force)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range acts {
		if a.Status != StatusUpdated || a.Backup == "" || a.Detail != "http "+url {
			t.Fatalf("%s force: %+v, want updated with backup", a.Target, a)
		}
	}

	wantJSON := map[string]map[string]any{
		filepath.Join(home, ".claude.json"):                         {"type": "http", "url": url},
		filepath.Join(home, ".gemini", "config", "mcp_config.json"): {"serverUrl": url},
		filepath.Join(home, ".gemini", "settings.json"):             {"httpUrl": url},
	}
	for path, want := range wantJSON {
		if got := readMTMEntry(t, path); !sameMCPEntry(got, want) {
			t.Fatalf("%s mtm entry = %v, want %v", path, got, want)
		}
	}
	codex, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(codex); got != "[mcp_servers.mtm]\nurl = \""+url+"\"\n" || strings.Contains(got, "command") {
		t.Fatalf("codex config = %q", got)
	}

	// Converged: --check --force is clean and a rerun is unchanged.
	acts, err = ConfigureMCP(check)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range acts {
		if a.Status != StatusUnchanged {
			t.Fatalf("%s converged check: status %s", a.Target, a.Status)
		}
	}
}

func TestConfigureMCP_HTTPDefaultsAndUnknownTransport(t *testing.T) {
	home := t.TempDir()
	if _, err := ConfigureMCP(Options{Home: home, MCPTransport: "http", Now: fixedClock()}); err != nil {
		t.Fatal(err)
	}
	if got := readMTMEntry(t, filepath.Join(home, ".claude.json")); got["url"] != DefaultMCPURL {
		t.Fatalf("claude url = %v, want %s", got["url"], DefaultMCPURL)
	}
	if _, err := ConfigureMCP(Options{Home: home, MCPTransport: "sse"}); err == nil {
		t.Fatal("unknown transport: want error")
	}
}
