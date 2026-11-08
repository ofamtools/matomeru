package agenthook

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/term"

	"ofam.dev/matomeru/internal/daemon"
)

// Package agenthook is what coding-agent hooks and status lines say to the
// daemon over its wire protocol: mtm agent hook, mtm agent statusline and mtm
// agent state. They replace tuios agent-hook, agent-statusline and
// set-agent-state, so a machine needs no tuios binary.
//
// The translation of each harness's hook payload (hook*.go, activity.go,
// approval.go, statusline_parse.go) and the commands (cmd_*.go) are copied
// from tuios v0.12.1 (MIT, see LICENSE-tuios); this file is mtm's own glue:
// flags, the socket and the wire caller.
//
// Hooks only connect. They never start a daemon: with none running they exit
// 0 and print nothing a harness could read as an answer.

// Env is everything the commands touch outside themselves. Zero fields mean
// the process's own.
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	// Socket is an explicit daemon socket (mtm --socket); it wins over the
	// environment.
	Socket string
	// Server is the TUIOS server mtm runs on (mtm --server); "" and "default"
	// are the user's own daemon. It is used only when neither Socket nor a
	// pane's TUIOS_SOCKET/TUIOS_SERVER names a daemon.
	Server string
	// DefaultServer is "default", the name that means the user's own daemon.
	DefaultServer string
}

func (e Env) getenv() func(string) string {
	if e.Getenv != nil {
		return e.Getenv
	}
	return os.Getenv
}

// socket is the daemon to reach: an explicit --socket; else the server a pane
// names in TUIOS_SERVER (a non-default server's panes get it; tuios picks its
// daemon by server name and XDG_RUNTIME_DIR the same way); else the socket a
// pane names in TUIOS_SOCKET (the default server's panes have only that); else
// mtm's own --server, else the user's own daemon.
func (e Env) socket() string {
	if e.Socket != "" {
		return e.Socket
	}
	g := e.getenv()
	if s := g("TUIOS_SERVER"); s != "" {
		return daemon.SocketFor(s)
	}
	if s := g("TUIOS_SOCKET"); s != "" {
		return s
	}
	if e.Server != "" && e.Server != e.DefaultServer {
		return daemon.SocketFor(e.Server)
	}
	return daemon.SocketFor("")
}

// errUnknownVerb is the daemon's code for a verb it does not have.
const errUnknownVerb = "unknown_verb"

// wireCaller calls verbs of the daemon on socket, one connection per call. It
// never starts a daemon (daemon.Client without a Server only connects).
type wireCaller struct {
	client *daemon.Client
	ctx    context.Context
}

func (w wireCaller) Call(verb string, params any) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := w.client.Call(w.ctx, verb, params, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// CallWithTimeout is Call with its own limit (an Inbox hold waits minutes).
func (w wireCaller) CallWithTimeout(verb string, params any, timeout time.Duration) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var raw json.RawMessage
	if err := w.client.Call(ctx, verb, params, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// dialer is the dial func of a hook or status line run: it checks that a
// daemon listens on the socket (a quick connect, nothing started) and returns
// the caller.
func dialer(socket string) func() (verbCaller, error) {
	return func() (verbCaller, error) {
		if socket == "" {
			return nil, errors.New("no daemon socket")
		}
		conn, err := net.DialTimeout("unix", socket, 200*time.Millisecond)
		if err != nil {
			return nil, fmt.Errorf("no daemon: %w", err)
		}
		_ = conn.Close()
		return wireCaller{client: &daemon.Client{Socket: socket}, ctx: context.Background()}, nil
	}
}

// parse reads flags that may come before, between or after the positional
// words (the harnesses write "agent hook claude-code --integration 3").
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var words []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return words, nil
		}
		words = append(words, args[0])
		args = args[1:]
	}
}

func isTTY(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func (e Env) fill() Env {
	if e.Stdin == nil {
		e.Stdin = os.Stdin
	}
	if e.Stdout == nil {
		e.Stdout = os.Stdout
	}
	if e.Stderr == nil {
		e.Stderr = os.Stderr
	}
	return e
}

// Hook is mtm agent hook <harness> [event] [payload]: it reports one harness
// hook event as the pane's agent state. It always returns 0.
func Hook(args []string, env Env) int {
	env = env.fill()
	var o agentHookOptions
	fs := flag.NewFlagSet("mtm agent hook", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.session, "session", "", "")
	fs.StringVar(&o.session, "s", "", "")
	fs.StringVar(&o.window, "window", "", "")
	fs.StringVar(&o.window, "w", "", "")
	fs.BoolVar(&o.explain, "explain", false, "")
	fs.DurationVar(&o.timeout, "timeout", agentHookDeadline, "")
	fs.IntVar(&o.integration, "integration", 0, "")
	noContext := fs.Bool("no-context", false, "")
	sendCwd := fs.Bool("send-cwd", false, "")
	words, err := parse(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(env.Stdout, HookUsage)
		return 0
	}
	if err != nil || len(words) < 1 || len(words) > 3 {
		fmt.Fprint(env.Stderr, HookUsage)
		return 2
	}
	runAgentHook(o, words, agentHookIO{
		stdin:      env.Stdin,
		stdinTTY:   isTTY(env.Stdin),
		stdout:     env.Stdout,
		stderr:     env.Stderr,
		getenv:     env.getenv(),
		dial:       dialer(env.socket()),
		self:       SelfProcess,
		harnessPID: HarnessPID,
		noContext:  *noContext,
		sendCwd:    *sendCwd,
		stampDir:   func() (string, error) { return filepath.Dir(env.socket()), nil },
		now:        time.Now,
	})
	return 0
}

// StatusLine is mtm agent statusline <harness>: it feeds a harness's status
// line to the pane's agent metadata. It returns the chained --then command's
// status, else 0.
func StatusLine(args []string, env Env) int {
	env = env.fill()
	var o agentStatusLineOptions
	fs := flag.NewFlagSet("mtm agent statusline", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&o.session, "session", "", "")
	fs.StringVar(&o.session, "s", "", "")
	fs.StringVar(&o.window, "window", "", "")
	fs.StringVar(&o.window, "w", "", "")
	fs.StringVar(&o.then, "then", "", "")
	fs.BoolVar(&o.explain, "explain", false, "")
	fs.BoolVar(&o.turnEnd, "turn-end", false, "")
	fs.DurationVar(&o.timeout, "timeout", agentStatusLineDeadline, "")
	fs.IntVar(&o.integration, "integration", 0, "")
	words, err := parse(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(env.Stdout, StatusLineUsage)
		return 0
	}
	if err != nil || len(words) != 1 {
		fmt.Fprint(env.Stderr, StatusLineUsage)
		return 2
	}
	return runAgentStatusLine(o, words[0], agentStatusLineIO{
		stdin:    env.Stdin,
		stdinTTY: isTTY(env.Stdin),
		stdout:   env.Stdout,
		stderr:   env.Stderr,
		getenv:   env.getenv(),
		dial:     dialer(env.socket()),
		self:     SelfProcess,
		stampDir: func() (string, error) { return filepath.Dir(env.socket()), nil },
		now:      time.Now,
		runThen:  runStatusLineThen,
	})
}

// State is mtm agent state <state>: it reports a pane's agent state by hand
// (tuios set-agent-state). Unlike a hook it says why it failed and exits 1.
func State(args []string, env Env) int {
	env = env.fill()
	fs := flag.NewFlagSet("mtm agent state", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var sess, window, message, source, harness, kind, agentSession, transcript, ifState string
	fs.StringVar(&sess, "session", "", "")
	fs.StringVar(&sess, "s", "", "")
	fs.StringVar(&window, "window", "", "")
	fs.StringVar(&window, "w", "", "")
	fs.StringVar(&message, "message", "", "")
	fs.StringVar(&message, "m", "", "")
	fs.StringVar(&source, "source", "", "")
	fs.StringVar(&harness, "harness", "", "")
	fs.StringVar(&kind, "kind", "", "")
	fs.StringVar(&agentSession, "agent-session-id", "", "")
	fs.StringVar(&transcript, "transcript-path", "", "")
	fs.StringVar(&ifState, "if-state", "", "")
	words, err := parse(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprint(env.Stdout, StateUsage)
		return 0
	}
	if err != nil || len(words) != 1 {
		fmt.Fprint(env.Stderr, StateUsage)
		return 2
	}
	client, err := dialer(env.socket())()
	if err != nil {
		fmt.Fprintf(env.Stderr, "mtm agent state: %v\n", err)
		return 1
	}
	res, err := setState(client, env.getenv(), sess, window, words[0], message, source, harness, stateExtras{kind, agentSession, transcript, ifState})
	if err != nil {
		fmt.Fprintf(env.Stderr, "mtm agent state: %v\n", err)
		return 1
	}
	if !res.Applied {
		fmt.Fprintf(env.Stderr, "Not applied: %s It still reports %s.\n", agentRefusalText(res.Reason), res.State)
	}
	return 0
}

type stateExtras struct{ kind, sessionID, transcriptPath, ifState string }

type stateResult struct {
	Applied bool   `json:"applied"`
	State   string `json:"state"`
	Source  string `json:"source"`
	Reason  string `json:"reason"`
}

// setState is runSetAgentState of tuios: the report is about the pane the
// command runs in unless a window is named.
func setState(client verbCaller, getenv func(string) string, sess, window, state, message, source, harness string, extra stateExtras) (stateResult, error) {
	if window == "" && (sess == "" || sess == getenv("TUIOS_SESSION")) {
		if pane := getenv("TUIOS_PANE_ID"); pane != "" && getenv("TUIOS_SESSION") != "" {
			window, sess = pane, getenv("TUIOS_SESSION")
		}
	}
	params := map[string]any{"session": sess, "window": window, "state": state, "message": message, "source": source, "harness": harness}
	if extra.ifState != "" {
		if err := requireIfState(client); err != nil {
			return stateResult{}, err
		}
	}
	for k, v := range map[string]string{"kind": extra.kind, "agent_session_id": extra.sessionID, "transcript_path": extra.transcriptPath, "if_state": extra.ifState} {
		if v != "" {
			params[k] = v
		}
	}
	raw, err := client.Call("set-agent-state", params)
	if err != nil {
		return stateResult{}, err
	}
	var res stateResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return stateResult{}, fmt.Errorf("failed to parse response: %w", err)
	}
	return res, nil
}

// agentRefusalText says in words why a report was not applied.
func agentRefusalText(reason string) string {
	switch reason {
	case "if_state":
		return "the pane was not in any of the --if-state states."
	case "foreign_session":
		return "the pane's agent is mid-turn in another conversation, so this looks like a nested run."
	case "foreign_harness":
		return "another harness reported this pane mid-turn, so this looks like a nested run."
	case "foreign_pane":
		return "the harness that ran the hook is a background session outside this pane. It inherited the pane's TUIOS_PANE_ID."
	default:
		return "a higher-ranked source owns this pane."
	}
}

// shortWindowID trims a window uuid to the prefix that addresses it.
func shortWindowID(id string) string {
	const prefix = 8
	if len(id) <= prefix {
		return id
	}
	return id[:prefix]
}
