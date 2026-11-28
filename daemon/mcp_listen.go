package daemon

import "net"

// DefaultMCPAddr is the well-known loopback address the daemon's shared HTTP
// MCP server tries first when Config.MCPAddr is empty, so harness configs can
// point at one stable URL instead of spawning `mtm mcp serve` per agent.
const DefaultMCPAddr = "127.0.0.1:4388"

// ephemeralMCPAddr is the fallback when DefaultMCPAddr is already taken.
const ephemeralMCPAddr = "127.0.0.1:0"

// listenMCP binds the HTTP MCP listener. An explicit addr binds strictly and
// fails when busy; an empty addr tries DefaultMCPAddr and falls back to an
// ephemeral loopback port so parallel daemons and tests never collide.
func listenMCP(addr string) (net.Listener, error) {
	if addr != "" {
		return net.Listen("tcp", addr)
	}
	if ln, err := net.Listen("tcp", DefaultMCPAddr); err == nil {
		return ln, nil
	}
	return net.Listen("tcp", ephemeralMCPAddr)
}
