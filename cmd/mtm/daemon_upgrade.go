package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/ochazuke"
)

// The live upgrade of the daemon (daemon.upgrade, and daemon.restart without
// hard): the daemon execs another mtm in its own place and hands it every
// pane's terminal, so the programs in the panes keep running with their pids.
// mtm runs the installed binary's facade word for it, "BIN __mtmd --server
// NAME daemon upgrade --binary BIN --json" (mtmd serve/upgrade.go), which
// checks the binary takes the daemon's state, asks the daemon's
// daemon-upgrade verb and waits for the new image to answer. BIN is the
// installed mtm pinned into daemon-bin (daemon.pin-binary), as the self
// runner starts it, so the next install cannot pull the file from under the
// daemon. A daemon from before live upgrades (mtm v0.53 and earlier) answers
// that it cannot; mtm then changes nothing and says that a hard restart,
// once, is needed.

const (
	// settingRestartUpgradeDaemon is whether & (app.restart) also upgrades
	// the daemon live when it runs another build.
	settingRestartUpgradeDaemon = "restart.upgrade-daemon"
	// envRestarted marks the process app.restart starts, so the new TUI
	// knows it was restarted (restartHook's Env). It is cleared at once.
	envRestarted = "MTM_RESTARTED"
)

func restartSettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingRestartUpgradeDaemon, Type: ochazuke.SettingBoolean, Default: true, Section: "daemon",
			Description: "After & restarts mtm in place, upgrade the daemon live to the new build when it runs another one (daemon.upgrade): every pane keeps its program. A daemon too old to hand its panes over is left running, and a notice says a one-time mtm daemon restart --hard is needed (it ends the programs in the panes). Off leaves the daemon on its build"},
	}
}

// daemonHandoff is what daemon.upgrade and daemon.restart answer.
type daemonHandoff struct {
	// Action is upgraded (live, panes kept), fallback (the new binary could
	// not take the panes and the old one kept them), restarted (stopped and
	// started: pane programs ended), started (none ran), unchanged (it runs
	// this build already).
	Action string `json:"action"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	PID    int    `json:"pid,omitempty"`
	Handed int    `json:"panes"`      // how many panes were handed over
	Kept   bool   `json:"panes_kept"` // every pane's program still runs
	Binary string `json:"binary,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// facadeOutcome is the JSON of mtmd's daemon upgrade (serve.UpgradeOutcome);
// mtm reads it without importing the module (TestDaemonImportBoundary).
type facadeOutcome struct {
	Action  string `json:"action"`
	PID     int    `json:"pid"`
	From    string `json:"from"`
	To      string `json:"to"`
	Binary  string `json:"binary"`
	Handed  int    `json:"panes"`
	Kept    bool   `json:"panes_kept"`
	NotLive bool   `json:"not_live"`
	Reason  string `json:"reason"`
	Error   string `json:"error"`
}

// handoffRefused is a live upgrade that never started: nothing was changed and
// every pane's program still runs.
type handoffRefused struct{ reason string }

func (e *handoffRefused) Error() string {
	return "the daemon was not upgraded live: " + strings.TrimSuffix(e.reason, ".") +
		". Nothing was changed and its panes keep running. A one-time `mtm daemon restart --hard` moves it to this build; it ends the programs in every pane"
}

// execUpgrade runs the facade's command line; tests replace it (App.runUpgrade).
func execUpgrade(ctx context.Context, argv, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = env
	return cmd.Output()
}

// handoffBinary is the binary the daemon hands off to: the installed mtm
// (this process's executable), pinned into daemon-bin when daemon.pin-binary
// is on, else that path; the daemon's own binary when neither is known.
func (a *App) handoffBinary(daemonPID int) (string, error) {
	exe := ""
	if a.executable != nil {
		exe, _ = a.executable()
	}
	if exe == "" {
		exe = processBinary(daemonPID)
	}
	if exe == "" {
		exe = "mtm" // the PATH's; the facade says when it does not run
	}
	if abs, err := filepath.Abs(exe); err == nil {
		exe = abs
	}
	if a.r.Settings().Bool(settingDaemonPin) {
		if pinned, err := daemon.Pin(exe, a.daemonBinDir(), daemonVersion(), nil); err == nil {
			return pinned, nil
		}
	}
	return exe, nil
}

// handOff upgrades the running daemon live to the installed mtm. whenDifferent
// leaves a daemon that runs this build alone. A daemon that is not running is
// started (nothing ends). A *handoffRefused means nothing was changed.
func (a *App) handOff(ctx context.Context, whenDifferent bool) (daemonHandoff, error) {
	info := a.daemonStatus(ctx)
	if !info.Running {
		if err := a.ensure(ctx); err != nil {
			return daemonHandoff{Action: "failed"}, err
		}
		now := a.daemonStatus(ctx)
		return daemonHandoff{Action: "started", To: now.Version, PID: now.PID, Kept: true}, nil
	}
	res := daemonHandoff{Action: "unchanged", From: info.Version, To: info.Version, PID: info.PID, Kept: true}
	if whenDifferent && !info.Skew {
		return res, nil
	}
	if r, err := a.runner(); err != nil || r.Name != runnerSelf {
		return res, &handoffRefused{reason: "the daemon runs under the " + a.runnerName() + " runner, and only mtm's own daemon (daemon.runner self) hands its panes to a new mtm"}
	}
	bin, err := a.handoffBinary(info.PID)
	if err != nil {
		return res, &handoffRefused{reason: err.Error()}
	}
	argv := []string{bin, daemonWord}
	env := a.selfStart().Env()
	if a.daemon.Server != "" {
		argv = append(argv, "--server", a.daemon.Server)
	} else {
		env = append(env, "MTM_SOCKET="+a.daemon.Socket)
	}
	argv = append(argv, "daemon", "upgrade", "--binary", bin, "--json")
	run := a.runUpgrade
	if run == nil {
		run = execUpgrade
	}
	out, runErr := run(ctx, argv, env)
	var o facadeOutcome
	if err := json.Unmarshal(out, &o); err != nil {
		if runErr == nil {
			runErr = err
		}
		return res, &handoffRefused{reason: fmt.Sprintf("%s did not run the upgrade: %v", bin, runErr)}
	}
	res = daemonHandoff{Action: o.Action, From: o.From, To: o.To, PID: o.PID, Handed: o.Handed, Kept: o.Kept, Binary: o.Binary, Reason: o.Reason}
	switch {
	case o.Action == "upgraded":
		return res, nil
	case o.NotLive:
		if res.PID == 0 {
			res.PID = info.PID
		}
		res.Kept = true
		return res, &handoffRefused{reason: o.Reason}
	}
	return res, fmt.Errorf("daemon upgrade %s: %s", o.Action, o.Error)
}

// hardRestart is the restart that ends pane programs: stop the daemon, start
// one (it restores its saved sessions with new shells).
func (a *App) hardRestart(ctx context.Context) (daemonHandoff, error) {
	before, err := a.stopDaemon(ctx)
	if err != nil {
		return daemonHandoff{Action: "failed"}, err
	}
	if err := a.ensure(ctx); err != nil {
		return daemonHandoff{Action: "failed", From: before.Version}, err
	}
	now := a.daemonStatus(ctx)
	return daemonHandoff{Action: "restarted", From: before.Version, To: now.Version, PID: now.PID}, nil
}

// handoffJob is the job of daemon.upgrade and daemon.restart: live, else with
// orRestart the hard restart. A refusal is a notice as well as the job's error.
func (a *App) handoffJob(whenDifferent, orRestart bool) ochazuke.Result {
	return ochazuke.Pending(func(c context.Context) (any, error) {
		res, err := a.handOff(c, whenDifferent)
		var refused *handoffRefused
		if errors.As(err, &refused) && orRestart {
			hard, herr := a.hardRestart(c)
			hard.Reason = refused.reason
			return hard, herr
		}
		return res, err
	}).Then(func(_ ochazuke.Ctx, _ any, err error) {
		var refused *handoffRefused
		if errors.As(err, &refused) {
			a.r.UI().Notify(refused.Error())
			return
		}
		if err == nil {
			_ = a.reloadPanes()
		}
	})
}

func (a *App) daemonUpgradeVerb(ctx ochazuke.Ctx) ochazuke.Result {
	return a.handoffJob(boolArg(ctx.Args, "when-different", false), boolArg(ctx.Args, "or-restart", false))
}

// daemonRestartVerb hands the daemon off to the installed mtm live, keeping
// every pane's program; hard stops it and starts it again (it restores its
// saved state with new shells), as restart did before.
func (a *App) daemonRestartVerb(ctx ochazuke.Ctx) ochazuke.Result {
	if !boolArg(ctx.Args, "hard", false) {
		return a.handoffJob(false, false)
	}
	return ochazuke.Pending(func(c context.Context) (any, error) {
		return a.hardRestart(c)
	}).Then(func(_ ochazuke.Ctx, _ any, err error) {
		if err == nil {
			_ = a.reloadPanes()
		}
	})
}

// upgradeAfterRestart runs in the TUI app.restart started (envRestarted):
// daemon.upgrade with when-different, unless restart.upgrade-daemon is off.
func (a *App) upgradeAfterRestart() {
	if a.getenv(envRestarted) == "" {
		return
	}
	_ = os.Unsetenv(envRestarted)
	if !a.r.Settings().Bool(settingRestartUpgradeDaemon) {
		return
	}
	// The person confirmed the restart; the upgrade keeps every pane.
	if _, err := a.r.Execute(ochazuke.Request{Noun: "daemon", Verb: "upgrade", Confirmed: true,
		Args: ochazuke.Arguments{"when-different": true}}); err != nil {
		a.r.UI().Error("daemon upgrade: " + err.Error())
	}
}

// restartEnv is the environment app.restart starts the new TUI with: this
// one, marked envRestarted.
func restartEnv() []string {
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, envRestarted+"=") {
			env = append(env, kv)
		}
	}
	return append(env, envRestarted+"=1")
}

func (a *App) defineDaemonUpgrade(verb func(name string, local bool, h any, about string) *ochazuke.NounBuilder) {
	verb("upgrade", true, a.daemonUpgradeVerb, "Upgrade the daemon live to the installed mtm (pinned into daemon-bin): every pane keeps its program, and the daemon its pid. Answers the old and new versions and whether the panes were kept. A daemon too old to hand over is left as it is unless or-restart").
		Confirm("Upgrade the daemon to the installed mtm? Panes keep running.")
	a.parameters("daemon.upgrade", map[string]ochazuke.Parameter{
		"or-restart":     {Type: "boolean", Description: "When the daemon cannot be upgraded live, restart it (stop, then start), which ends the programs in its panes"},
		"when-different": {Type: "boolean", Description: "Do nothing when the daemon already runs this build"},
	})
	a.parameters("daemon.restart", map[string]ochazuke.Parameter{
		"hard": {Type: "boolean", Description: "Stop the daemon and start it again, which ends the programs in its panes (they are restored from saved state with new shells). Without it the restart is daemon.upgrade's live handoff"},
	})
}
