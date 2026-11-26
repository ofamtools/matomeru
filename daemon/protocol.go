// Package daemon implements Matomeru's self-hosted local Unix-socket daemon,
// shared HTTP MCP server, hook telemetry/approval broker, and background
// schedule ticker.
package daemon

import (
	"os"
	"path/filepath"
	"time"

	"ofam.dev/matomeru/ptyhost"
)

// Environment variables injected into every pane opened by the daemon.
const (
	EnvPaneID = "MTM_PANE_ID"
	EnvSocket = "MTM_SOCKET"
	EnvMCPURL = "MTM_MCP_URL"
)

// Wire RPC operations over the Unix domain socket.
const (
	OpStatus          = "status"
	OpOpen            = "open"
	OpWrite           = "write"
	OpResize          = "resize"
	OpSnapshot        = "snapshot"
	OpList            = "list"
	OpClose           = "close"
	OpReset           = "reset"
	OpShutdown        = "shutdown"
	OpHookEvent       = "hook_event"
	OpResolveApproval = "resolve_approval"
	OpTelemetry       = "telemetry"
	OpSubscribe       = "subscribe"
	OpUpgrade         = "upgrade"
	OpWatchEvents     = "watch_events"
	OpHookRegister    = "hook_register"
	OpHookUnregister  = "hook_unregister"
	OpHookList        = "hook_list"
	OpHookLog         = "hook_log"
)

// Hook telemetry event kinds, pane states, subagent actions, and approval decisions.
const (
	HookKindState    = "state"
	HookKindApproval = "approval"
	HookKindActivity = "activity"
	HookKindSubagent = "subagent"

	StateRunning  = "running"
	StateNeedsYou = "needs_you"
	StateIdle     = "idle"
	StateDone     = "done"

	SubagentStart = "start"
	SubagentStop  = "stop"

	DecisionAllow = "allow"
	DecisionDeny  = "deny"
)

// DefaultSocketPath returns ~/.local/share/matomeru/mtm.sock.
func DefaultSocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "mtm.sock")
	}
	return filepath.Join(home, ".local", "share", "matomeru", "mtm.sock")
}

// ResolveSocketPath picks explicit when non-empty, else $MTM_SOCKET, else DefaultSocketPath().
func ResolveSocketPath(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if v := os.Getenv(EnvSocket); v != "" {
		return v
	}
	return DefaultSocketPath()
}

// Request is one newline-delimited JSON RPC request sent over the Unix socket.
type Request struct {
	Op string `json:"op"`

	// PTY operations
	ID   string            `json:"id,omitempty"`
	Cmd  string            `json:"cmd,omitempty"`
	Cwd  string            `json:"cwd,omitempty"`
	Cols int               `json:"cols,omitempty"`
	Rows int               `json:"rows,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
	Data []byte            `json:"data,omitempty"`

	// Hook event & telemetry operations
	PaneID         string         `json:"paneId,omitempty"`
	Harness        string         `json:"harness,omitempty"`
	Kind           string         `json:"kind,omitempty"`
	State          string         `json:"state,omitempty"`
	Tool           string         `json:"tool,omitempty"`
	Summary        string         `json:"summary,omitempty"`
	Files          []string       `json:"files,omitempty"`
	Prompt         string         `json:"prompt,omitempty"`
	Response       string         `json:"response,omitempty"`
	FinalResponse  string         `json:"finalResponse,omitempty"`
	SubagentID     string         `json:"subagentId,omitempty"`
	SubagentAction string         `json:"subagentAction,omitempty"`
	RequestID      string         `json:"requestId,omitempty"`
	Payload        map[string]any `json:"payload,omitempty"`
	TimeoutMs      int64          `json:"timeoutMs,omitempty"`

	// Resolve approval
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`

	// Upgrade
	Binary string `json:"binary,omitempty"`

	// Callback hooks (hook_register, hook_unregister, hook_log)
	Rule  *HookRule `json:"rule,omitempty"`
	Limit int       `json:"limit,omitempty"`
}

// Response is one newline-delimited JSON RPC response returned over the Unix socket.
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`

	// status
	PID       int     `json:"pid,omitempty"`
	Socket    string  `json:"socket,omitempty"`
	MCPURL    string  `json:"mcpURL,omitempty"`
	PaneCount int     `json:"paneCount,omitempty"`
	Uptime    string  `json:"uptime,omitempty"`
	UptimeSec float64 `json:"uptimeSec,omitempty"`
	Version   string  `json:"version,omitempty"`
	Commit    string  `json:"commit,omitempty"`
	Dirty     bool    `json:"dirty,omitempty"`
	BuiltAt   string  `json:"builtAt,omitempty"`
	Binary    string  `json:"binary,omitempty"`

	// snapshot / subscribe
	PaneID              string         `json:"paneId,omitempty"`
	Frame               *ptyhost.Frame `json:"frame,omitempty"`
	Found               bool           `json:"found,omitempty"`
	ScrollbackUnchanged bool           `json:"scrollbackUnchanged,omitempty"`
	TelemetryChanged    bool           `json:"telemetryChanged,omitempty"`

	// list
	Panes []PaneInfo `json:"panes,omitempty"`

	// hook_event (approval)
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`

	// telemetry
	Telemetry        map[string]*PaneTelemetry `json:"telemetry,omitempty"`
	PendingApprovals []*PendingApproval        `json:"pendingApprovals,omitempty"`

	// watch_events / hook_list / hook_log / hook_unregister
	Event      *AgentDoneEvent `json:"event,omitempty"`
	Rules      []HookRule      `json:"rules,omitempty"`
	Deliveries []HookDelivery  `json:"deliveries,omitempty"`
}

// StatusResult describes a running daemon's identity, build version, and health.
type StatusResult struct {
	PID       int     `json:"pid"`
	Socket    string  `json:"socket"`
	MCPURL    string  `json:"mcpURL,omitempty"`
	PaneCount int     `json:"paneCount"`
	Uptime    string  `json:"uptime"`
	UptimeSec float64 `json:"uptimeSec"`
	Version   string  `json:"version,omitempty"`
	Commit    string  `json:"commit,omitempty"`
	Dirty     bool    `json:"dirty,omitempty"`
	BuiltAt   string  `json:"builtAt,omitempty"`
	Binary    string  `json:"binary,omitempty"`
}

// OpenRequest describes a PTY pane to launch inside the daemon.
type OpenRequest struct {
	ID   string            `json:"id"`
	Cmd  string            `json:"cmd,omitempty"`
	Cwd  string            `json:"cwd,omitempty"`
	Cols int               `json:"cols,omitempty"`
	Rows int               `json:"rows,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
}

// PaneInfo describes one live or exited pane tracked by the daemon.
type PaneInfo struct {
	ID        string    `json:"id"`
	Cmd       string    `json:"cmd,omitempty"`
	Cwd       string    `json:"cwd,omitempty"`
	Title     string    `json:"title,omitempty"`
	Cols      int       `json:"cols"`
	Rows      int       `json:"rows"`
	PID       int       `json:"pid,omitempty"`
	Exited    bool      `json:"exited,omitempty"`
	ExitCode  int       `json:"exitCode,omitempty"`
	StartedAt time.Time `json:"startedAt,omitempty"`
}

// HookEvent is a telemetry or blocking approval event delivered by mtm-hook.
type HookEvent struct {
	PaneID         string         `json:"paneId"`
	Harness        string         `json:"harness,omitempty"`
	Kind           string         `json:"kind"`
	State          string         `json:"state,omitempty"`
	Tool           string         `json:"tool,omitempty"`
	Summary        string         `json:"summary,omitempty"`
	Files          []string       `json:"files,omitempty"`
	Prompt         string         `json:"prompt,omitempty"`
	Response       string         `json:"response,omitempty"`
	FinalResponse  string         `json:"finalResponse,omitempty"`
	SubagentID     string         `json:"subagentId,omitempty"`
	SubagentAction string         `json:"subagentAction,omitempty"`
	RequestID      string         `json:"requestId,omitempty"`
	Payload        map[string]any `json:"payload,omitempty"`
	TimeoutMs      int64          `json:"timeoutMs,omitempty"`
}

// HookEventResult is the daemon's response to a hook_event call.
type HookEventResult struct {
	OK       bool   `json:"ok"`
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// SubagentInfo tracks an active subagent reported by hook events.
type SubagentInfo struct {
	ID        string    `json:"id"`
	Summary   string    `json:"summary,omitempty"`
	StartedAt time.Time `json:"startedAt"`
}

// HookTurn is one bounded prompt/response/summary turn captured from hook events.
type HookTurn struct {
	Prompt   string    `json:"prompt,omitempty"`
	Response string    `json:"response,omitempty"`
	Summary  string    `json:"summary,omitempty"`
	At       time.Time `json:"at"`
}

// PaneTelemetry holds the latest hook-reported state and activity for one pane.
type PaneTelemetry struct {
	PaneID          string                  `json:"paneId"`
	Harness         string                  `json:"harness,omitempty"`
	State           string                  `json:"state,omitempty"`
	Tool            string                  `json:"tool,omitempty"`
	Summary         string                  `json:"summary,omitempty"`
	TranscriptPath  string                  `json:"transcriptPath,omitempty"`
	SessionID       string                  `json:"sessionId,omitempty"`
	RecentTurns     []HookTurn              `json:"recentTurns,omitempty"`
	ActiveSubagents map[string]SubagentInfo `json:"activeSubagents,omitempty"`
	SubagentCount   int                     `json:"subagentCount"`
	LastKind        string                  `json:"lastKind,omitempty"`
	LastEventAt     time.Time               `json:"lastEventAt"`
	Payload         map[string]any          `json:"payload,omitempty"`
}

// PendingApproval is an active PermissionRequest awaiting allow/deny resolution.
type PendingApproval struct {
	RequestID string         `json:"requestId"`
	PaneID    string         `json:"paneId"`
	Harness   string         `json:"harness,omitempty"`
	Tool      string         `json:"tool,omitempty"`
	Summary   string         `json:"summary,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
	CreatedAt time.Time      `json:"createdAt"`
}

// TelemetryResult is returned by the "telemetry" RPC op.
type TelemetryResult struct {
	Telemetry        map[string]*PaneTelemetry `json:"telemetry"`
	PendingApprovals []*PendingApproval        `json:"pendingApprovals"`
}
