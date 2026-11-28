package setup

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

const (
	// MCPTransportStdio configures harnesses to spawn `mtm mcp serve` per agent.
	MCPTransportStdio = "stdio"
	// MCPTransportHTTP configures harnesses to reach the daemon's shared
	// Streamable HTTP MCP endpoint.
	MCPTransportHTTP = "http"
	// DefaultMCPURL is the daemon's well-known HTTP MCP endpoint.
	DefaultMCPURL = "http://127.0.0.1:4388/mcp"
)

// mcpSpec is the desired mtm MCP server entry, rendered per harness.
type mcpSpec struct {
	transport string
	cmd       string
	url       string
}

func newMCPSpec(opts Options) (mcpSpec, error) {
	transport := strings.ToLower(strings.TrimSpace(opts.MCPTransport))
	if transport == "" {
		transport = MCPTransportStdio
	}
	spec := mcpSpec{transport: transport}
	switch transport {
	case MCPTransportStdio:
		spec.cmd = strings.TrimSpace(opts.MCPCmd)
		if spec.cmd == "" {
			spec.cmd = DefaultMCPCmd
		}
	case MCPTransportHTTP:
		spec.url = strings.TrimSpace(opts.MCPURL)
		if spec.url == "" {
			spec.url = DefaultMCPURL
		}
	default:
		return spec, fmt.Errorf("unknown MCP transport %q (want %s or %s)", opts.MCPTransport, MCPTransportStdio, MCPTransportHTTP)
	}
	return spec, nil
}

// httpURLKey names the URL field each JSON harness reads for HTTP servers.
var httpURLKey = map[string]string{
	"claude":      "url",
	"antigravity": "serverUrl",
	"gemini":      "httpUrl",
}

// jsonEntry renders the mcpServers.mtm object for a JSON-configured harness.
func (s mcpSpec) jsonEntry(target string) map[string]any {
	if s.transport == MCPTransportHTTP {
		entry := map[string]any{httpURLKey[target]: s.url}
		if target == "claude" {
			entry["type"] = "http"
		}
		return entry
	}
	args := make([]any, len(mtmMCPArgs))
	for i, a := range mtmMCPArgs {
		args[i] = a
	}
	entry := map[string]any{"command": s.cmd, "args": args}
	if target == "claude" {
		entry["type"] = "stdio"
	}
	return entry
}

// codexBlock renders the [mcp_servers.mtm] table for ~/.codex/config.toml.
func (s mcpSpec) codexBlock() string {
	if s.transport == MCPTransportHTTP {
		return fmt.Sprintf("[mcp_servers.mtm]\nurl = %s\n", strconv.Quote(s.url))
	}
	return formatCodexMTMMCPBlock(s.cmd)
}

func (s mcpSpec) detail() string {
	if s.transport == MCPTransportHTTP {
		return "http " + s.url
	}
	return s.cmd + " mcp serve"
}

// sameMCPEntry reports whether existing equals want after JSON normalization.
func sameMCPEntry(existing any, want map[string]any) bool {
	a, errA := json.Marshal(existing)
	b, errB := json.Marshal(want)
	if errA != nil || errB != nil {
		return false
	}
	var na, nb any
	if json.Unmarshal(a, &na) != nil || json.Unmarshal(b, &nb) != nil {
		return false
	}
	return reflect.DeepEqual(na, nb)
}
