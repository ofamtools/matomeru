package daemon

import (
	"context"
	"encoding/json"
	"errors"
)

type Session struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Attached    bool   `json:"attached"`
	Dir         string `json:"dir"`
	Branch      string `json:"branch"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	WindowCount int    `json:"window_count"`
	Created     int64  `json:"created"`
	LastActive  int64  `json:"last_active"`
}
type Pane struct {
	ID         string `json:"window_id"`
	PTYID      string `json:"pty_id"`
	Name       string `json:"display_name"`
	Title      string `json:"title"`
	AgentState string `json:"agent_state"`
	Cwd        string `json:"cwd"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Workspace  int    `json:"workspace"`
	PID        int    `json:"pid"`
	Focused    bool   `json:"focused"`
	Minimized  bool   `json:"minimized"`
	Revision   uint64 `json:"revision"`
}

func (c *Client) Sessions(ctx context.Context) ([]Session, error) {
	var out struct {
		Sessions []Session `json:"sessions"`
	}
	err := c.Call(ctx, "list-sessions", nil, &out)
	return out.Sessions, err
}
func (c *Client) Panes(ctx context.Context, session string) ([]Pane, error) {
	var out struct {
		Windows []Pane `json:"windows"`
	}
	err := c.Call(ctx, "list-windows", map[string]any{"session": session}, &out)
	return out.Windows, err
}

type StartAgentParams struct {
	Session      string            `json:"session,omitempty"`
	Agent        string            `json:"agent"`
	Args         []string          `json:"args,omitempty"`
	Name         string            `json:"name,omitempty"`
	Cwd          string            `json:"cwd,omitempty"`
	Prompt       string            `json:"prompt,omitempty"`
	ReadyTimeout int               `json:"ready_timeout,omitempty"`
	Workspace    int               `json:"workspace,omitempty"`
	Focus        bool              `json:"focus"`
	Env          map[string]string `json:"env,omitempty"`
	Protocol     string            `json:"protocol,omitempty"`
	Grants       []string          `json:"grants,omitempty"`
	Repo         string            `json:"repo,omitempty"`
	RepoURL      string            `json:"repo_url,omitempty"`
	ReposRoot    string            `json:"repos_root,omitempty"`
	Clone        bool              `json:"clone,omitempty"`
}
type StartAgentResult struct {
	Session        string `json:"session"`
	SessionID      string `json:"session_id"`
	CreatedSession bool   `json:"created_session"`
	WindowID       string `json:"window_id"`
	PTYID          string `json:"pty_id"`
	Name           string `json:"name"`
	Agent          string `json:"agent"`
	Command        string `json:"command"`
	Cwd            string `json:"cwd"`
	Workspace      int    `json:"workspace"`
	Protocol       string `json:"protocol"`
	Ready          bool   `json:"ready"`
	ReadyBy        string `json:"ready_by"`
	State          string `json:"state"`
	Outcome        string `json:"outcome"`
	BlockedBy      string `json:"blocked_by"`
	Reason         string `json:"reason"`
	PromptStatus   string `json:"prompt_status"`
	PromptNote     string `json:"prompt_note"`
}

// Resize sets a window's PTY to cols×rows through the resize verb so the
// program inside lays out for the area deck will show it in. Zero or negative
// sizes are a no-op. The caller is any unit's daemon caller.
func Resize(ctx context.Context, c interface {
	Call(context.Context, string, any, any) error
}, session, window string, cols, rows int) error {
	if cols <= 0 || rows <= 0 || window == "" {
		return nil
	}
	return c.Call(ctx, "resize", map[string]any{"session": session, "window": window, "width": cols, "height": rows}, nil)
}

func (c *Client) StartAgent(ctx context.Context, p StartAgentParams) (StartAgentResult, error) {
	var out StartAgentResult
	err := c.Call(ctx, "start-agent", p, &out)
	return out, err
}

type CaptureParams struct {
	Session    string   `json:"session,omitempty"`
	Window     string   `json:"window"`
	Source     string   `json:"source,omitempty"`
	Scrollback bool     `json:"scrollback,omitempty"`
	ANSI       bool     `json:"ansi,omitempty"`
	Styled     bool     `json:"styled,omitempty"`
	Resolved   bool     `json:"resolved,omitempty"`
	Palette    []string `json:"palette,omitempty"`
	Lines      int      `json:"lines,omitempty"`
	Start      *int     `json:"start,omitempty"`
	End        *int     `json:"end,omitempty"`
}
type CaptureResult struct {
	Content     string `json:"content"`
	Source      string `json:"source"`
	Styled      bool   `json:"styled"`
	Resolved    bool   `json:"resolved"`
	HistoryRows int    `json:"history_rows"`
	Revision    uint64 `json:"revision"`
	BootID      string `json:"boot_id"`
	Cmdline     string `json:"cmdline,omitempty"`
	ExitCode    *int   `json:"exit_code,omitempty"`
	CommandSeq  uint64 `json:"command_seq,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
}

func (c *Client) Capture(ctx context.Context, p CaptureParams) (CaptureResult, error) {
	var out CaptureResult
	err := c.Call(ctx, "capture-pane", p, &out)
	return out, err
}

type SendTextParams struct {
	Session string `json:"session,omitempty"`
	Window  string `json:"window"`
	Text    string `json:"text"`
	Paste   bool   `json:"paste,omitempty"`
	Submit  bool   `json:"submit,omitempty"`
}

func (c *Client) SendText(ctx context.Context, p SendTextParams) error {
	if p.Window == "" {
		return errors.New("send-text requires an explicit window")
	}
	return c.Call(ctx, "send-text", p, nil)
}

type ActivityParams struct {
	Session  string `json:"session,omitempty"`
	Window   string `json:"window"`
	Since    int64  `json:"since,omitempty"`
	SinceSeq uint64 `json:"since_seq,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Recap    bool   `json:"recap,omitempty"`
}
type ActivityEntry struct {
	Seq    uint64   `json:"seq"`
	At     int64    `json:"at"`
	Kind   string   `json:"kind"`
	Tool   string   `json:"tool,omitempty"`
	Target string   `json:"target,omitempty"`
	Files  []string `json:"files,omitempty"`
	OK     *bool    `json:"ok,omitempty"`
	Exit   *int     `json:"exit,omitempty"`
	Text   string   `json:"text,omitempty"`
}
type ActivityResult struct {
	Window    string          `json:"window"`
	LastSeq   uint64          `json:"last_seq"`
	Entries   []ActivityEntry `json:"entries"`
	Recap     map[string]any  `json:"recap,omitempty"`
	Untrusted bool            `json:"untrusted"`
}

func (c *Client) Activity(ctx context.Context, p ActivityParams) (ActivityResult, error) {
	var out ActivityResult
	err := c.Call(ctx, "agent-activity", p, &out)
	return out, err
}

type TranscriptParams struct {
	Session    string `json:"session,omitempty"`
	Window     string `json:"window"`
	HumanNonce string `json:"human_nonce"`
	After      string `json:"after,omitempty"`
	Before     string `json:"before,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}
type TranscriptEntry struct {
	ID        string          `json:"id"`
	At        int64           `json:"at"`
	Role      string          `json:"role"`
	Kind      string          `json:"kind"`
	Text      string          `json:"text"`
	Truncated bool            `json:"truncated"`
	Tool      string          `json:"tool,omitempty"`
	Target    string          `json:"target,omitempty"`
	Status    string          `json:"status,omitempty"`
	ToolID    string          `json:"tool_id,omitempty"`
	Diff      json.RawMessage `json:"diff,omitempty"`
	Plan      json.RawMessage `json:"plan,omitempty"`
	Todos     json.RawMessage `json:"todos,omitempty"`
}
type TranscriptResult struct {
	Session   string            `json:"session"`
	Window    string            `json:"window"`
	Harness   string            `json:"harness"`
	Cursor    string            `json:"cursor"`
	Reset     bool              `json:"reset"`
	More      bool              `json:"more"`
	Older     string            `json:"older"`
	Entries   []TranscriptEntry `json:"entries"`
	Untrusted bool              `json:"untrusted"`
}

func (c *Client) Transcript(ctx context.Context, p TranscriptParams) (TranscriptResult, error) {
	var out TranscriptResult
	err := c.Call(ctx, "agent-transcript", p, &out)
	return out, err
}

type HistoryParams struct {
	Session string `json:"session,omitempty"`
	Window  string `json:"window,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	Query   string `json:"query,omitempty"`
}
type CommandEntry struct {
	Session    string `json:"session"`
	Window     string `json:"window"`
	Name       string `json:"name"`
	Seq        uint64 `json:"seq"`
	Cmdline    string `json:"cmdline"`
	Exit       *int   `json:"exit"`
	At         int64  `json:"at"`
	DurationMS int64  `json:"duration_ms"`
	Cwd        string `json:"cwd"`
	Row        int    `json:"row"`
}

func (c *Client) History(ctx context.Context, p HistoryParams) ([]CommandEntry, error) {
	var out struct {
		Commands []CommandEntry `json:"commands"`
	}
	err := c.Call(ctx, "command-history", p, &out)
	return out.Commands, err
}
