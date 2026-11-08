package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/ochazuke"
)

// daemonWord is the hidden host word the daemon runs behind: "mtm __mtmd
// --server NAME daemon". It is handled before any database opens
// (daemon_shell.go) and never shown as a command.
const daemonWord = "__mtmd"

const (
	settingDaemonRunner = "daemon.runner"
	settingDaemonPin    = "daemon.pin-binary"
	envDaemonRunner     = "MTM_DAEMON_RUNNER"

	runnerTuios = "tuios"
	runnerSelf  = "self"
)

// daemonRunner is one way to start the server mtm runs on. The setting
// daemon.runner (or MTM_DAEMON_RUNNER for one run) names a row; nothing
// branches on a name.
type daemonRunner struct {
	Name        string
	Description string
	Built       bool // this build can run it (the self row needs the linked daemon)
	// Hello: Ensure waits for a compatible hello, not a bare connect.
	Hello bool
	Start func(a *App) daemon.StartFunc
}

func daemonRunners() []daemonRunner {
	return []daemonRunner{
		{Name: runnerTuios, Built: true, Description: "tuios start-server from PATH, as before",
			Start: func(*App) daemon.StartFunc {
				return func(ctx context.Context, name string) (<-chan error, error) { return serverStart(ctx, name) }
			}},
		{Name: runnerSelf, Built: daemonBuilt, Hello: true, Description: "the daemon linked into mtm itself: mtm " + daemonWord + " start-server, no tuios needed",
			Start: func(a *App) daemon.StartFunc { return a.selfStart().Start }},
	}
}

func daemonRunnerNames() []string {
	var out []string
	for _, r := range daemonRunners() {
		if r.Built {
			out = append(out, r.Name)
		}
	}
	return out
}

func daemonSettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingDaemonRunner, Type: ochazuke.SettingString, Default: runnerTuios, Enum: daemonRunnerNames(),
			Description: "How mtm starts its TUIOS server: tuios (the tuios on PATH) or self (the daemon linked into mtm, no tuios needed). " + envDaemonRunner + " overrides for one run"},
		{Name: settingDaemonPin, Type: ochazuke.SettingBoolean, Default: true,
			Description: "Run the self daemon from a pinned copy of the mtm binary under the state directory (daemon-bin), so overwriting the installed mtm cannot kill it. Off runs the installed binary"},
	}
}

// runnerName is the runner now chosen: the environment, else the setting.
func (a *App) runnerName() string {
	if v := a.getenv(envDaemonRunner); v != "" {
		return v
	}
	if name := a.r.Settings().String(settingDaemonRunner); name != "" {
		return name
	}
	return runnerTuios
}

// runner is the chosen row, or an error naming what is available.
func (a *App) runner() (daemonRunner, error) {
	name := a.runnerName()
	for _, r := range daemonRunners() {
		if r.Name != name {
			continue
		}
		if !r.Built {
			return r, fmt.Errorf("daemon runner %q is not built into this mtm (-tags nodaemon); use %s", name, strings.Join(daemonRunnerNames(), ", "))
		}
		return r, nil
	}
	return daemonRunner{}, fmt.Errorf("unknown daemon runner %q (%s)", name, strings.Join(daemonRunnerNames(), ", "))
}

// startServer is the daemon client's Start: the chosen runner's.
func (a *App) startServer(ctx context.Context, name string) (<-chan error, error) {
	r, err := a.runner()
	if err != nil {
		return nil, err
	}
	return r.Start(a)(ctx, name)
}

// serverReady is the daemon client's Ready: a hello when the chosen runner
// wants one. A bare connect otherwise, and when the runner is invalid (the
// start reports that).
func (a *App) serverReady(ctx context.Context) error {
	if r, err := a.runner(); err == nil && r.Hello {
		return a.daemon.HelloReady(ctx)
	}
	return nil
}

// selfStart is the self runner's starter: this binary, the hidden word, and
// the pinned copy under the state directory (daemon.pin-binary).
func (a *App) selfStart() daemon.SelfStart {
	s := daemon.SelfStart{Word: daemonWord, Label: daemonVersion(), Environ: os.Environ}
	if a.r.Settings().Bool(settingDaemonPin) {
		s.PinDir = a.daemonBinDir()
	}
	return s
}

// daemonBinDir holds the pinned binaries: <state>/daemon-bin/mtm-<build>.
// Copies of other builds that no process runs are removed whenever a new one
// is made.
func (a *App) daemonBinDir() string {
	return filepath.Join(a.r.Config().StateDirectory(), "daemon-bin")
}

// daemonVersion labels the daemon's hello: this build, else the daemon's own.
func daemonVersion() string {
	if v := buildStamp().Version; v != "" {
		return v
	}
	return "devel"
}

// daemonInfo is what daemon.status reports.
type daemonInfo struct {
	Running  bool   `json:"running"`
	Server   string `json:"server"`
	Socket   string `json:"socket"`
	Runner   string `json:"runner"`
	PID      int    `json:"pid,omitempty"`
	Version  string `json:"daemon_version,omitempty"`
	Client   string `json:"client_version"`
	Binary   string `json:"binary,omitempty"`
	Skew     bool   `json:"skew"`
	Protocol int    `json:"protocol,omitempty"`
	Sessions int    `json:"sessions,omitempty"`
	Error    string `json:"error,omitempty"`
}

// daemonStatus asks the daemon's hello. It never starts one: a daemon that
// is not there is a row with running false.
func (a *App) daemonStatus(ctx context.Context) daemonInfo {
	info := daemonInfo{Server: a.daemon.Server, Socket: a.daemon.Socket, Runner: a.runnerName(), Client: daemonVersion()}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	h, err := a.daemon.Hello(cctx)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	info.Running, info.PID, info.Version, info.Protocol, info.Sessions = true, h.PID, h.DaemonVersion, h.Protocol, h.Sessions
	info.Binary = processBinary(h.PID)
	info.Skew = h.DaemonVersion != info.Client
	return info
}

func (a *App) daemonStatusVerb(ochazuke.Ctx) ochazuke.Result {
	return ochazuke.Pending(func(c context.Context) (any, error) { return a.daemonStatus(c), nil })
}

// processBinary is the executable path of pid, "" when unknown.
func processBinary(pid int) string {
	if pid <= 0 {
		return ""
	}
	if runtime.GOOS == "linux" {
		p, _ := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
		return p
	}
	out, err := execOutput("ps", "-o", "comm=", "-p", fmt.Sprint(pid))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// daemonLogPath is the daemon's log file for the server, where the daemon
// writes it (state home, tuios-servers/NAME for a named server, then
// tuios/daemon.log); of the candidate state homes the first that exists wins.
func (a *App) daemonLogPath() string {
	var bases []string
	if v := a.getenv("XDG_STATE_HOME"); v != "" {
		bases = append(bases, v)
	}
	if home, err := a.home(); err == nil {
		bases = append(bases, filepath.Join(home, ".local", "state"), filepath.Join(home, "Library", "Application Support"))
	}
	var first string
	for _, base := range bases {
		if name := a.daemon.Server; name != "" {
			base = filepath.Join(base, "tuios-servers", name)
		}
		path := filepath.Join(base, "tuios", "daemon.log")
		if first == "" {
			first = path
		}
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return first
}

func (a *App) daemonLogsVerb(ctx ochazuke.Ctx) ochazuke.Result {
	n := intArg(ctx.Args, "lines", 50)
	path := a.daemonLogPath()
	return ochazuke.Pending(func(context.Context) (any, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		var tail []string
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 8<<20)
		for sc.Scan() {
			tail = append(tail, sc.Text())
			if len(tail) > n {
				tail = tail[1:]
			}
		}
		return strings.Join(tail, "\n"), sc.Err()
	})
}

// stopDaemon asks the daemon for its pid and sends it SIGTERM (which stops
// it: panes die), then waits for the socket to go. A daemon that is not
// running is not an error; stopping never starts one.
func (a *App) stopDaemon(ctx context.Context) (daemonInfo, error) {
	info := a.daemonStatus(ctx)
	if !info.Running {
		return info, nil
	}
	if info.PID <= 0 {
		return info, errors.New("the daemon reported no pid")
	}
	if err := killPID(info.PID, syscall.SIGTERM); err != nil {
		return info, err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(a.daemon.Socket); err != nil {
			break
		}
		if err := probeDial(a.daemon.Socket); err != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return info, nil
}

// killPID signals a process; tests replace it.
var killPID = func(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) }

func (a *App) daemonStopVerb(ochazuke.Ctx) ochazuke.Result {
	return ochazuke.Pending(func(c context.Context) (any, error) {
		info, err := a.stopDaemon(c)
		return info, err
	})
}

// daemonRestartVerb stops the daemon, then ensures one (restoring saved
// state, as the daemon does on its own start).
func (a *App) daemonRestartVerb(ochazuke.Ctx) ochazuke.Result {
	ensure := a.ensure
	return ochazuke.Pending(func(c context.Context) (any, error) {
		if _, err := a.stopDaemon(c); err != nil {
			return nil, err
		}
		if err := ensure(c); err != nil {
			return nil, err
		}
		return a.daemonStatus(c), nil
	}).Then(func(_ ochazuke.Ctx, _ any, err error) {
		if err == nil {
			_ = a.reloadPanes()
		}
	})
}

// daemonServeVerb runs the daemon in the foreground of this process (CLI
// only; it returns when the daemon stops).
func (a *App) daemonServeVerb(ochazuke.Ctx) ochazuke.Result {
	if !daemonBuilt {
		return ochazuke.Failed(errors.New("this mtm is built with -tags nodaemon: no daemon to serve"))
	}
	if a.daemon.Server == "" {
		return ochazuke.Failed(errors.New("daemon.serve needs a named server (-server NAME), not -socket or -server default"))
	}
	if code := daemonForeground(a.daemon.Server); code != 0 {
		return ochazuke.Failed(fmt.Errorf("daemon exited with status %d", code))
	}
	return ochazuke.Done(nil)
}

// intArg reads a number argument that may arrive as int, float or string.
func intArg(args ochazuke.Arguments, name string, fallback int) int {
	switch v := args[name].(type) {
	case int:
		return v
	case float64:
		return int(v)
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func execOutput(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

// probeDial connects to a unix socket and hangs up.
func probeDial(socket string) error {
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return err
	}
	return conn.Close()
}

// daemonExit is the exit status of the hidden daemon route, carried out of
// run to main.
type daemonExit int

func (e daemonExit) Error() string { return fmt.Sprintf("daemon exited with status %d", int(e)) }

// defineDaemon adds the daemon noun's verbs beside ensure. status, show-log,
// stop and restart never start one themselves.
func (a *App) defineDaemon(r *ochazuke.Runtime) {
	n := r.Define("daemon").About("The TUIOS server mtm runs on: its state, its start and stop, its log")
	n.Verb("start", a.ensureVerb, "Start mtm's TUIOS server when it is not running (same as ensure), then reload the pane list").Quiet()
	n.Verb("status", a.daemonStatusVerb, "Whether the daemon runs: pid, its version beside this client's, runner, socket, binary and version skew. Never starts one").Fetch().Quiet()
	n.Verb("show-log", a.daemonLogsVerb, "Tail the daemon log (lines, default 50). Never starts a daemon").Fetch().Quiet()
	n.Verb("stop", a.daemonStopVerb, "Stop the daemon; its panes die. A daemon that is not running is left alone").Confirm("Stop the daemon? Its panes end.")
	n.Verb("restart", a.daemonRestartVerb, "Stop the daemon, then start it again (it restores its saved state)").Confirm("Restart the daemon? Panes end and are restored from saved state.")
	n.Verb("serve", a.daemonServeVerb, "Run the daemon in this process until it is stopped (CLI)")
	a.parameters("daemon.show-log", map[string]ochazuke.Parameter{"lines": {Type: "integer", Description: "How many trailing lines (default 50)"}})
}

// noDaemonStart lists the verbs (noun.verb) that never start a daemon: they
// ask about it, stop it, or are the daemon. The CLI skips its start-when-
// missing step before them. Data, not a branch: add a row for a new one.
var noDaemonStart = map[string]bool{
	"daemon.status": true, "daemon.show-log": true,
	"daemon.stop": true, "daemon.restart": true, "daemon.serve": true,
}

// startsNoDaemon is true when this run's command is one of noDaemonStart.
func (a *App) startsNoDaemon() bool {
	return len(a.cliWords) >= 2 && noDaemonStart[a.cliWords[0]+"."+a.cliWords[1]]
}
