// Package agenthook parses agent harness hook payloads and forwards turn state,
// tool activity, subagent lifecycle, and blocking permission approvals to the
// local Matomeru daemon over its Unix domain socket.
//
// agenthook imports only the standard library so both the standalone mtm-hook
// binary and `mtm agent hook` start in milliseconds with zero heavy dependencies.
package agenthook

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Event kinds accepted by the daemon's hook_event RPC.
const (
	KindState    = "state"
	KindApproval = "approval"
	KindActivity = "activity"
	KindSubagent = "subagent"
)

// Turn lifecycle states reported to the daemon.
const (
	StateRunning  = "running"
	StateNeedsYou = "needs_you"
	StateIdle     = "idle"
	StateDone     = "done"
)

// Subagent lifecycle actions.
const (
	SubagentStart = "start"
	SubagentStop  = "stop"
)

// Approval decisions returned by the daemon.
const (
	DecisionAllow = "allow"
	DecisionDeny  = "deny"
)

const (
	// DefaultApprovalTimeout is the default maximum time a blocking
	// PermissionRequest waits for a daemon decision.
	DefaultApprovalTimeout = 300 * time.Second

	// DefaultDenyReason is sent back to harnesses when a user denies a tool
	// approval without specifying a custom reason.
	DefaultDenyReason = "Denied by user in Matomeru."

	nonBlockingIOTimeout = 2 * time.Second
	maxPayloadBytes      = 1 << 20 // 1 MiB safety bound for stdin JSON
)

// HookEvent is the newline-delimited JSON request sent over $MTM_SOCKET.
type HookEvent struct {
	Op             string         `json:"op"`
	PaneID         string         `json:"paneId"`
	Harness        string         `json:"harness,omitempty"`
	Kind           string         `json:"kind"`
	State          string         `json:"state,omitempty"`
	Tool           string         `json:"tool,omitempty"`
	Summary        string         `json:"summary,omitempty"`
	Files          []string       `json:"files,omitempty"`
	Prompt         string         `json:"prompt,omitempty"`
	Response       string         `json:"response,omitempty"`
	SubagentID     string         `json:"subagentId,omitempty"`
	SubagentAction string         `json:"subagentAction,omitempty"`
	RequestID      string         `json:"requestId,omitempty"`
	Payload        map[string]any `json:"payload,omitempty"`
	TimeoutMs      int64          `json:"timeoutMs,omitempty"`
}

// DaemonResponse is the newline-delimited JSON response from the daemon.
type DaemonResponse struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// DefaultSocketPath returns the default local Matomeru daemon socket path:
// ~/.local/share/matomeru/mtm.sock.
func DefaultSocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "matomeru", "mtm.sock")
	}
	return filepath.Join(home, ".local", "share", "matomeru", "mtm.sock")
}

// ResolveSocketPath returns the socket path from an explicit flag or $MTM_SOCKET.
// It returns "" when neither is set so hooks running outside Matomeru exit cleanly.
func ResolveSocketPath(flagSocket string, getenv func(string) string) string {
	if s := strings.TrimSpace(flagSocket); s != "" {
		return s
	}
	if getenv != nil {
		if s := strings.TrimSpace(getenv("MTM_SOCKET")); s != "" {
			return s
		}
	}
	return ""
}

// ValidState reports whether s is one of running, needs_you, idle, done.
func ValidState(s string) bool {
	switch s {
	case StateRunning, StateNeedsYou, StateIdle, StateDone:
		return true
	default:
		return false
	}
}

// Run executes mtm-hook / `mtm agent hook` using os.Getenv and returns a POSIX
// exit code. Outside Matomeru or when the daemon is unreachable, Run exits 0 so
// agents are never disrupted.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return RunWithEnv(args, os.Getenv, stdin, stdout, stderr)
}

// RunWithEnv is the testable core of Run with an injected environment lookup.
func RunWithEnv(args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	cli, showHelp, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "mtm-hook: %v\n", err)
		return 2
	}
	if showHelp {
		printUsage(stdout)
		return 0
	}

	explain := func(format string, a ...any) {
		if cli.explain {
			fmt.Fprintf(stderr, "mtm-hook: "+format+"\n", a...)
		}
	}

	paneID := strings.TrimSpace(cli.paneID)
	if paneID == "" {
		paneID = strings.TrimSpace(getenv("MTM_PANE_ID"))
	}

	// Manual/scripted state update: `mtm-hook state <running|needs_you|idle|done>`
	if cli.subcommand == "state" {
		if !ValidState(cli.stateArg) {
			fmt.Fprintf(stderr, "mtm-hook: invalid state %q (want running, needs_you, idle, or done)\n", cli.stateArg)
			return 2
		}
		sock := ResolveSocketPath(cli.socket, getenv)
		if sock == "" && cli.paneID != "" {
			sock = DefaultSocketPath()
		}
		if sock == "" {
			explain("skipped: MTM_SOCKET is unset")
			return 0
		}
		harnessName := CanonicalHarness(cli.harness)
		if harnessName == "" {
			harnessName = "manual"
		}
		ev := HookEvent{
			Op:      "hook_event",
			PaneID:  paneID,
			Harness: harnessName,
			Kind:    KindState,
			State:   cli.stateArg,
			Summary: strings.TrimSpace(cli.summary),
		}
		if _, err := Send(sock, ev, nonBlockingIOTimeout); err != nil {
			explain("skipped: daemon unreachable at %s: %v", sock, err)
			return 0
		}
		explain("sent state=%s pane=%s via %s", ev.State, ev.PaneID, sock)
		return 0
	}

	isAntigravity := CanonicalHarness(cli.harness) == "antigravity"
	emittedStdout := false
	defer func() {
		if isAntigravity && !emittedStdout {
			_, _ = io.WriteString(stdout, "{}\n")
		}
	}()

	sock := ResolveSocketPath(cli.socket, getenv)
	if sock == "" {
		explain("skipped: MTM_SOCKET is unset")
		return 0
	}

	raw, err := readStdinBounded(stdin, maxPayloadBytes)
	if err != nil {
		explain("skipped: read stdin: %v", err)
		return 0
	}

	res := Translate(cli.harness, cli.event, raw)
	if CanonicalHarness(res.Event.Harness) == "antigravity" || (!isAntigravity && isAntigravityRaw(raw)) {
		isAntigravity = true
	}
	if res.SkipReason != "" {
		explain("skipped: %s", res.SkipReason)
		return 0
	}

	ev := res.Event
	ev.Op = "hook_event"
	ev.PaneID = paneID
	if ev.Kind == KindApproval && ev.RequestID == "" {
		ev.RequestID = newRequestID()
	}

	timeout := nonBlockingIOTimeout
	if ev.Kind == KindApproval {
		timeout = cli.timeout
		if timeout <= 0 {
			timeout = DefaultApprovalTimeout
		}
		ev.TimeoutMs = timeout.Milliseconds()
	}

	resp, err := Send(sock, ev, timeout)
	if err != nil {
		explain("skipped: daemon unreachable at %s: %v", sock, err)
		return 0
	}

	if ev.Kind == KindApproval {
		out, ok := FormatApprovalResponse(ev.Harness, resp.Decision, resp.Reason, ev.Payload)
		if ok && resp.OK {
			_, _ = io.WriteString(stdout, out)
			emittedStdout = true
			explain("approval %s -> %s", ev.RequestID, resp.Decision)
		} else {
			explain("approval %s produced no decision (ok=%v decision=%q)", ev.RequestID, resp.OK, resp.Decision)
		}
		return 0
	}

	explain("sent kind=%s state=%s harness=%s pane=%s", ev.Kind, ev.State, ev.Harness, ev.PaneID)
	return 0
}

// Send writes a single HookEvent line to the Unix domain socket at socketPath
// and waits up to timeout for a single DaemonResponse line.
func Send(socketPath string, ev HookEvent, timeout time.Duration) (DaemonResponse, error) {
	if socketPath == "" {
		return DaemonResponse{}, errors.New("empty socket path")
	}
	if ev.Op == "" {
		ev.Op = "hook_event"
	}
	if timeout <= 0 {
		timeout = nonBlockingIOTimeout
	}

	dialTimeout := min(timeout, 2*time.Second)
	conn, err := net.DialTimeout("unix", socketPath, dialTimeout)
	if err != nil {
		return DaemonResponse{}, err
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return DaemonResponse{}, err
	}

	line, err := json.Marshal(ev)
	if err != nil {
		return DaemonResponse{}, err
	}
	line = append(line, '\n')
	if _, err := conn.Write(line); err != nil {
		return DaemonResponse{}, err
	}

	br := bufio.NewReader(conn)
	respLine, err := br.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return DaemonResponse{}, err
	}
	respLine = []byte(strings.TrimSpace(string(respLine)))
	if len(respLine) == 0 {
		return DaemonResponse{}, io.EOF
	}
	var resp DaemonResponse
	if err := json.Unmarshal(respLine, &resp); err != nil {
		return DaemonResponse{}, err
	}
	return resp, nil
}

// FormatApprovalResponse formats a daemon allow/deny decision for the given
// harness's stdout hook contract. Returns ("", false) when the decision is not
// recognized so the harness falls back to prompting the user interactively.
func FormatApprovalResponse(harness, decision, reason string, payload map[string]any) (string, bool) {
	decision = strings.ToLower(strings.TrimSpace(decision))
	if decision != DecisionAllow && decision != DecisionDeny {
		return "", false
	}
	reason = strings.TrimSpace(reason)
	if decision == DecisionDeny && reason == "" {
		reason = DefaultDenyReason
	}

	switch CanonicalHarness(harness) {
	case "claude", "codex":
		inner := map[string]any{
			"behavior": decision,
		}
		if decision == DecisionDeny {
			inner["message"] = reason
		}
		// Claude Code's ExitPlanMode requires updatedInput on allow.
		if decision == DecisionAllow && payload != nil {
			if toolName, _ := payload["tool_name"].(string); toolName == "ExitPlanMode" {
				if input, ok := payload["tool_input"]; ok && input != nil {
					inner["updatedInput"] = input
				}
			}
		}
		b, err := json.Marshal(map[string]any{
			"hookSpecificOutput": map[string]any{
				"hookEventName": "PermissionRequest",
				"decision":      inner,
			},
		})
		if err != nil {
			return "", false
		}
		return string(b) + "\n", true

	case "omp":
		obj := map[string]any{
			"approved": decision == DecisionAllow,
			"decision": decision,
		}
		if reason != "" {
			obj["reason"] = reason
		}
		b, err := json.Marshal(obj)
		if err != nil {
			return "", false
		}
		return string(b) + "\n", true

	default:
		obj := map[string]any{
			"decision": decision,
		}
		if reason != "" {
			obj["reason"] = reason
		}
		b, err := json.Marshal(obj)
		if err != nil {
			return "", false
		}
		return string(b) + "\n", true
	}
}

type cliArgs struct {
	subcommand string
	stateArg   string
	harness    string
	event      string
	socket     string
	paneID     string
	summary    string
	timeout    time.Duration
	explain    bool
}

func parseArgs(args []string) (cliArgs, bool, error) {
	out := cliArgs{timeout: DefaultApprovalTimeout}
	var pos []string

	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		switch {
		case a == "-h" || a == "--help" || a == "help":
			return out, true, nil
		case a == "--explain":
			out.explain = true
		case a == "--harness" || strings.HasPrefix(a, "--harness="):
			v, next, err := takeFlagValue(args, i, "--harness")
			if err != nil {
				return out, false, err
			}
			out.harness = v
			i = next
		case a == "--event" || strings.HasPrefix(a, "--event="):
			v, next, err := takeFlagValue(args, i, "--event")
			if err != nil {
				return out, false, err
			}
			out.event = v
			i = next
		case a == "--socket" || strings.HasPrefix(a, "--socket="):
			v, next, err := takeFlagValue(args, i, "--socket")
			if err != nil {
				return out, false, err
			}
			out.socket = v
			i = next
		case a == "--pane" || strings.HasPrefix(a, "--pane="):
			v, next, err := takeFlagValue(args, i, "--pane")
			if err != nil {
				return out, false, err
			}
			out.paneID = v
			i = next
		case a == "--summary" || strings.HasPrefix(a, "--summary="):
			v, next, err := takeFlagValue(args, i, "--summary")
			if err != nil {
				return out, false, err
			}
			out.summary = v
			i = next
		case a == "--timeout" || strings.HasPrefix(a, "--timeout="):
			v, next, err := takeFlagValue(args, i, "--timeout")
			if err != nil {
				return out, false, err
			}
			d, err := parseDuration(v)
			if err != nil {
				return out, false, fmt.Errorf("invalid --timeout %q: %w", v, err)
			}
			out.timeout = d
			i = next
		case a == "--integration" || strings.HasPrefix(a, "--integration="):
			// Legacy v1 compatibility flag (e.g. `mtm-hook claude-code --integration 3`).
			_, next, err := takeFlagValue(args, i, "--integration")
			if err != nil {
				return out, false, err
			}
			i = next
		case a == "--server" || strings.HasPrefix(a, "--server="):
			_, next, err := takeFlagValue(args, i, "--server")
			if err != nil {
				return out, false, err
			}
			i = next
		case a == "--no-context" || a == "--send-cwd":
			// Legacy v1 boolean flags accepted for compatibility.
		case strings.HasPrefix(a, "-"):
			return out, false, fmt.Errorf("unknown flag %q", a)
		default:
			pos = append(pos, a)
		}
	}

	if len(pos) > 0 && pos[0] == "state" {
		out.subcommand = "state"
		if len(pos) < 2 {
			return out, false, errors.New("state subcommand requires <running|needs_you|idle|done>")
		}
		out.stateArg = strings.TrimSpace(pos[1])
		if len(pos) > 2 && out.summary == "" {
			out.summary = strings.Join(pos[2:], " ")
		}
		return out, false, nil
	}

	if len(pos) > 0 && out.harness == "" {
		out.harness = pos[0]
		pos = pos[1:]
	}
	if len(pos) > 0 && out.event == "" {
		out.event = pos[0]
	}
	return out, false, nil
}

func takeFlagValue(args []string, i int, name string) (string, int, error) {
	if v, ok := strings.CutPrefix(args[i], name+"="); ok {
		return v, i, nil
	}
	if i+1 >= len(args) {
		return "", i, fmt.Errorf("%s requires a value", name)
	}
	return args[i+1], i + 1, nil
}

func parseDuration(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if secs, err := strconv.Atoi(raw); err == nil {
		return time.Duration(secs) * time.Second, nil
	}
	return time.ParseDuration(raw)
}

func readStdinBounded(r io.Reader, limit int64) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(io.LimitReader(r, limit))
}

func newRequestID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return "req-" + hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("req-%d", time.Now().UnixNano())
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `mtm-hook — Matomeru agent hook forwarder

Usage:
  mtm-hook [harness] [flags]        Read hook JSON from stdin and forward to Matomeru daemon
  mtm-hook state <state> [flags]    Send manual state update (running|needs_you|idle|done)

Harnesses:
  claude (claude-code), codex, omp, gemini, antigravity, generic

Flags:
  --harness <name>      Override harness adapter
  --event <name>        Override hook event name
  --socket <path>       Daemon Unix socket (default: $MTM_SOCKET)
  --pane <id>           Pane ID (default: $MTM_PANE_ID)
  --timeout <dur>       Blocking approval timeout (default: 300s)
  --summary <text>      Optional summary for state updates
  --explain             Explain skips and actions on stderr
`)
}
