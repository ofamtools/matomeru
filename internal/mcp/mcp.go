// Package mcp holds mtm MCP identity, compatibility aliases and harness config.
// The protocol and catalog server belong to ochazuke/cli/mcp.
package mcp

import (
	"os"

	"ofam.dev/matomeru/internal/daemon"
)

// Identity is who is talking: the pane and what it is assigned to.
type Identity struct {
	Session   string `json:"session"`
	Window    string `json:"window"`
	TaskID    string `json:"task_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	Harness   string `json:"harness,omitempty"`
	Model     string `json:"model,omitempty"`
	Bound     bool   `json:"bound"` // a pane is known; tools that need one refuse otherwise

	TaskTitle   string `json:"task_title,omitempty"`
	TaskStatus  string `json:"task_status,omitempty"`
	TaskVersion int64  `json:"task_version,omitempty"` // the task's updated stamp, for task_status's expected_version
	ProjectName string `json:"project_name,omitempty"`
	ProjectRepo string `json:"project_repo,omitempty"`
}

// IdentityFromEnv reads the pane the server was started in from getenv
// (os.Getenv when nil). The daemon sets MTM_SESSION and MTM_PANE_ID (the
// TUIOS_ names only with daemon.legacy-env, or from an older daemon) on every pane, and a
// harness passes them on to the MCP servers it starts; MTM_WINDOW overrides
// the pane for tests and explicit bridges, as in matomeru-old.
func IdentityFromEnv(getenv func(string) string) Identity {
	if getenv == nil {
		getenv = os.Getenv
	}
	session := daemon.EnvSession.Get(getenv)
	window := getenv("MTM_WINDOW")
	if window == "" {
		window = daemon.EnvPaneID.Get(getenv)
	}
	return Identity{Session: session, Window: window, Bound: window != ""}
}
