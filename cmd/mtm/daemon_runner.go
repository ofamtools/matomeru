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
	"slices"
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
	settingDaemonPaths  = "daemon.paths"
	settingDaemonLegacy = "daemon.legacy-env"
	envDaemonRunner     = "MTM_DAEMON_RUNNER"

	runnerTuios = "tuios"
	runnerSelf  = "self"
)

// defaultDaemonRunner is the runner when neither the environment nor the
// setting names one: the linked daemon, or tuios in a -tags nodaemon build
// that has no daemon to run.
func defaultDaemonRunner() string {
	if daemonBuilt {
		return runnerSelf
	}
	return runnerTuios
}

// daemonRunner is one way to start the server mtm runs on. The setting
// daemon.runner (or MTM_DAEMON_RUNNER for one run) names a row; nothing
// branches on a name.
type daemonRunner struct {
	Name        string
	Description string
	Built       bool // this build can run it (the self row needs the linked daemon)
	// Hello: Ensure waits for a compatible hello, not a bare connect.
	Hello bool
	// Paths is the layout (a daemon.Layouts row) this runner's daemon always
	// uses; "" is the daemon.paths setting's.
	Paths string
	Start func(a *App) daemon.StartFunc
}

func daemonRunners() []daemonRunner {
	return []daemonRunner{
		{Name: runnerTuios, Built: true, Paths: "tuios", Description: "tuios start-server from PATH, as before",
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
		{Name: settingDaemonRunner, Type: ochazuke.SettingString, Default: defaultDaemonRunner(), Enum: daemonRunnerNames(),
			Description: "How mtm starts its pane server: self (the daemon linked into mtm; the default, and tuios is not needed) or tuios (the tuios on PATH; the default in a -tags nodaemon build). " + envDaemonRunner + " overrides for one run"},
		{Name: settingDaemonPaths, Type: ochazuke.SettingString, Default: daemon.Layouts[0].Name, Enum: daemon.LayoutNames(),
			Description: "The names the self daemon gives its socket, state and config folders: mtm (socket <runtime>/mtm/servers/NAME/mtm.sock, state <state home>/mtm/servers/NAME; a server's tuios-named state moves there once, on start) or tuios (the folders from before the rename). " + daemon.EnvPaths + " overrides for one run. The tuios runner always uses tuios's"},
		{Name: settingDaemonLegacy, Type: ochazuke.SettingBoolean, Default: false,
			Description: "Also give panes, hooks and plugins the TUIOS_ variable names (TUIOS_PANE_ID, ...) beside the MTM_ ones, for a machine with an old tool that reads them. Off (the default) exports only the MTM_ names. Takes effect when the daemon is restarted. " + daemon.EnvLegacy + " overrides for one run"},
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
	return defaultDaemonRunner()
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

// daemonLayout is the layout the daemon mtm starts uses: the runner's own
// when it has one, else MTM_DAEMON_PATHS, else the daemon.paths setting.
func (a *App) daemonLayout() daemon.Layout {
	name := ""
	if r, err := a.runner(); err == nil {
		name = r.Paths
	}
	if name == "" {
		name = a.getenv(daemon.EnvPaths)
	}
	if name == "" {
		name = a.r.Settings().String(settingDaemonPaths)
	}
	if l, err := daemon.LayoutNamed(name); err == nil {
		return l
	}
	return daemon.Layouts[0]
}

// daemonLegacyEnv is whether the daemon mtm starts also exports the TUIOS_
// names: MTM_DAEMON_LEGACY_ENV, else the daemon.legacy-env setting.
func (a *App) daemonLegacyEnv() bool {
	if v := a.getenv(daemon.EnvLegacy); v != "" {
		return daemon.LegacyEnvOn(v)
	}
	return a.r.Settings().Bool(settingDaemonLegacy)
}

// resolveDaemonSocket aims the client of a named server at its socket under
// the daemon's layout, once the settings are read (the client was made
// before them, under PreferredLayout). An explicit -socket and the default
// daemon are left alone.
func (a *App) resolveDaemonSocket() {
	if a.daemon == nil || a.daemon.Server == "" || (a.opts.Socket != "") {
		return
	}
	a.daemon.Socket = daemon.ResolveSocket(a.daemonLayout(), a.daemon.Server)
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
	s := daemon.SelfStart{Word: daemonWord, Label: daemonVersion(), Environ: os.Environ, Paths: a.daemonLayout().Name, LegacyEnv: a.daemonLegacyEnv()}
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
	st := buildStamp()
	v := st.Version
	if v == "" {
		v = "devel"
	}
	return daemonLabelOf(v, st.Commit)
}

// daemonLabelOf is the label a self daemon announces: the version, then "+"
// and the first 12 characters of the VCS revision, so status can compare
// builds by what they are made from.
func daemonLabelOf(version, commit string) string {
	if len(commit) > 12 {
		commit = commit[:12]
	}
	if commit == "" {
		return version
	}
	return version + "+" + commit
}

// labelRevision is the revision a daemon label names: the part after "+",
// else the "-g<hex>" suffix of a git-describe label. Empty when it has none.
func labelRevision(label string) string {
	if _, rev, ok := strings.Cut(label, "+"); ok {
		return rev
	}
	if i := strings.LastIndex(label, "-g"); i >= 0 {
		rev := label[i+2:]
		if rev != "" && strings.Trim(rev, "0123456789abcdef") == "" {
			return rev
		}
	}
	return ""
}

// daemonSkew says whether the daemon and the client are different builds,
// and the daemon's revision when its label names one. The self runner
// compares revisions (prefix match, so a short describe hash equals the full
// one) and falls back to the labels when the daemon names none; the tuios
// runner keeps comparing labels.
func daemonSkew(runner, daemonLabel, clientLabel, clientCommit string) (string, bool) {
	if runner != runnerSelf {
		return "", daemonLabel != clientLabel
	}
	rev := labelRevision(daemonLabel)
	if rev == "" {
		return "", daemonLabel != clientLabel
	}
	if clientCommit == "" {
		return rev, daemonLabel != clientLabel
	}
	return rev, !strings.HasPrefix(clientCommit, rev) && !strings.HasPrefix(rev, clientCommit)
}

// daemonInfo is what daemon.status reports.
type daemonInfo struct {
	Running bool   `json:"running"`
	Server  string `json:"server"`
	Socket  string `json:"socket"`
	Runner  string `json:"runner"`
	// Paths is the layout mtm starts the daemon under; StateDir the
	// server's state folder in it.
	Paths    string `json:"paths"`
	StateDir string `json:"state_dir,omitempty"`
	PID      int    `json:"pid,omitempty"`
	Version  string `json:"daemon_version,omitempty"`
	Client   string `json:"client_version"`
	// Commit is the daemon's VCS revision when its label names one;
	// ClientCommit is this client's. Skew compares them, not the labels.
	Commit       string `json:"daemon_commit,omitempty"`
	ClientCommit string `json:"client_commit,omitempty"`
	Binary       string `json:"binary,omitempty"`
	Skew         bool   `json:"skew"`
	Protocol     int    `json:"protocol,omitempty"`
	Sessions     int    `json:"sessions,omitempty"`
	Error        string `json:"error,omitempty"`
}

// daemonStatus asks the daemon's hello. It never starts one: a daemon that
// is not there is a row with running false.
func (a *App) daemonStatus(ctx context.Context) daemonInfo {
	info := daemonInfo{Server: a.daemon.Server, Socket: a.daemon.Socket, Runner: a.runnerName(), Paths: a.daemonLayout().Name, Client: daemonVersion(), ClientCommit: buildStamp().Commit}
	if a.daemon.Server != "" {
		if bases := a.stateBases(); len(bases) > 0 {
			info.StateDir = a.daemonLayout().StateDir(bases[0], a.daemon.Server)
		}
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	h, err := a.daemon.Hello(cctx)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	info.Running, info.PID, info.Version, info.Protocol, info.Sessions = true, h.PID, h.DaemonVersion, h.Protocol, h.Sessions
	info.Binary = processBinary(h.PID)
	info.Commit, info.Skew = daemonSkew(info.Runner, h.DaemonVersion, info.Client, info.ClientCommit)
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

// stateBases are the state homes a daemon may keep its files under, the one
// a daemon started from here uses first (daemon.StateHome).
func (a *App) stateBases() []string {
	var bases []string
	if v := a.getenv("XDG_STATE_HOME"); v != "" {
		bases = append(bases, v)
	}
	if home, err := a.home(); err == nil {
		bases = append(bases, daemon.StateHome(a.getenv, home))
		for _, b := range []string{filepath.Join(home, ".local", "state"), filepath.Join(home, "Library", "Application Support")} {
			if !slices.Contains(bases, b) {
				bases = append(bases, b)
			}
		}
	}
	return bases
}

// daemonLogPath is the daemon's log file for the server, where the daemon
// writes it: the layout's state folder under the state home, then
// daemon.log. Of the candidate state homes and layouts (the daemon's first),
// the first whose log exists wins, else the daemon's layout under the first
// state home.
func (a *App) daemonLogPath() string {
	bases := a.stateBases()
	first := a.daemonLayout()
	layouts := []daemon.Layout{first}
	for _, l := range daemon.Layouts {
		if l.Name != first.Name {
			layouts = append(layouts, l)
		}
	}
	var fallback string
	for _, l := range layouts {
		for _, base := range bases {
			path := l.LogPath(base, a.daemon.Server)
			if fallback == "" {
				fallback = path
			}
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
	}
	return fallback
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
	if code := daemonForeground(a.daemon.Server, a.daemonLayout().Name, a.daemonLegacyEnv()); code != 0 {
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

// defineDaemon adds the daemon noun's verbs beside ensure. A verb declared
// local (see localVerb) never starts a daemon itself: it asks about the
// daemon, stops it, or is the daemon.
func (a *App) defineDaemon(r *ochazuke.Runtime) {
	n := r.Define("daemon").About("The TUIOS server mtm runs on: its state, its start and stop, its log")
	// verb declares one verb of the noun; local says it needs no daemon
	// running first (see localVerb).
	verb := func(name string, local bool, h any, about string) *ochazuke.NounBuilder {
		if local {
			return a.localVerb(n, "daemon", name, h, about)
		}
		return n.Verb(name, h, about)
	}
	verb("start", false, a.ensureVerb, "Start mtm's TUIOS server when it is not running (same as ensure), then reload the pane list").Quiet()
	verb("status", true, a.daemonStatusVerb, "Whether the daemon runs: pid, its version beside this client's, runner, socket, binary and version skew. Never starts one").Fetch().Quiet()
	verb("show-log", true, a.daemonLogsVerb, "Tail the daemon log (lines, default 50). Never starts a daemon").Fetch().Quiet()
	verb("stop", true, a.daemonStopVerb, "Stop the daemon; its panes die. A daemon that is not running is left alone").Confirm("Stop the daemon? Its panes end.")
	verb("restart", true, a.daemonRestartVerb, "Stop the daemon, then start it again (it restores its saved state)").Confirm("Restart the daemon? Panes end and are restored from saved state.")
	verb("import-state", true, a.daemonImportStateVerb, daemonImportAbout)
	a.defineDaemonImportParameters()
	verb("serve", true, a.daemonServeVerb, "Run the daemon in this process until it is stopped (CLI)")
	a.parameters("daemon.show-log", map[string]ochazuke.Parameter{"lines": {Type: "integer", Description: "How many trailing lines (default 50)"}})
}

// localVerb declares a verb of any noun that never starts a daemon: the CLI
// skips its start-when-missing step before it. It is the one way a noun says
// so, at the verb's declaration; no table of names exists.
func (a *App) localVerb(n *ochazuke.NounBuilder, noun, name string, h any, about string) *ochazuke.NounBuilder {
	if a.localVerbs == nil {
		a.localVerbs = map[string]bool{}
	}
	a.localVerbs[noun+"."+name] = true
	return n.Verb(name, h, about)
}

// startsNoDaemon is true when this run's command resolves to a verb declared
// local. It resolves the typed words the way the shell does (an abbreviated
// noun or verb included), so the answer is about the verb, not the spelling.
func (a *App) startsNoDaemon() bool {
	noun, rest, ok := a.r.NounWords(a.cliWords)
	if !ok || len(rest) == 0 {
		return false
	}
	if _, found := a.r.Operation(noun + "." + rest[0]); found {
		return a.localVerbs[noun+"."+rest[0]]
	}
	var verbs []string
	for _, op := range a.r.Operations.Entries() {
		if op.Noun() == noun {
			verbs = append(verbs, op.Name())
		}
	}
	read := a.r.ReadNounWord(noun, rest[0], verbs)
	return read.Kind == ochazuke.NounWordVerb && a.localVerbs[noun+"."+read.Value]
}

// wireDaemonStart gives the client the runner-based start and the readiness
// probe. The runner (daemon.runner, MTM_DAEMON_RUNNER) is read when a start
// is needed, so settings.json and the environment apply to this run.
func (a *App) wireDaemonStart(client *daemon.Client) {
	if a.localVerbs == nil {
		a.localVerbs = map[string]bool{}
	}
	if client.Server != "" {
		client.Start, client.Ready = a.startServer, a.serverReady
	}
}
