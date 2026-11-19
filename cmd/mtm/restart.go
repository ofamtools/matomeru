package main

import (
	"os/exec"
	"path/filepath"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/store"
)

// Restarting in place (ochazuke's app.restart, key &). mtm gives the
// runtime its name and its --version line everywhere, so the quit question
// reads "Quit mtm?" from the CLI too; only the TUI gives it the binary to
// start and the cleanup to run first (wireRestart), so a command or the MCP
// gateway never replaces itself.
//
// What a restart keeps: the TUIOS panes (they live in the daemon, which is
// another process: only this client's connections close), the database
// (the pinned project, tasks, notes, the inbox, history) and the config.
// What it loses: what this process held in memory alone. mtm starts on the
// Panes page as on any start, so the current page and focus are not kept;
// nor are open modals and forms, the palette's line, jobs that were
// running, or an attached pane's live view.
//
// The restart request (ochazuke.restart-request) is stored in mtm.db
// (restartTable), so an agent's post from its own mtm process, a command
// or the MCP gateway, reaches every TUI open on the database: each reads
// it through the table's change token (ochazuke's badge segment). The
// restarted TUI started after the request, so it shows none. The old
// relay's tables (ui_hosts, ui_calls, ui_answers) are no longer declared;
// their rows stay in the file, unread.

// restartTable holds the one restart request.
var restartTable = store.Table{Name: "restart_requests", Owner: "restart", Key: "id", Version: 1}

// restartHook is mtm's hook: name and running build always; the installed
// binary's path and the cleanup only in the TUI (tui true).
func (a *App) restartHook(tui bool) ochazuke.RestartHook {
	h := ochazuke.RestartHook{
		Name:    "mtm",
		Running: func() string { return versionLine(buildStamp()) },
		// Installed nil: the runtime runs PATH --version off the loop,
		// which prints the same line and starts neither TUI nor daemon.
	}
	if tui {
		h.Path = a.installedPath
		// The new TUI knows it was restarted, and upgrades the daemon
		// (restart.upgrade-daemon, daemon_upgrade.go).
		h.Env = restartEnv
	}
	return h
}

// defineRestart gives the runtime the hook every process shares and the
// table the request is stored in; its verbs never start a daemon. Where
// the table cannot be declared the request stays in this process's
// memory and the failure is said.
func (a *App) defineRestart() {
	a.r.SetRestart(a.restartHook(false))
	// Who asked (the restart dialog, an agent's confirm, history rows)
	// names an agent's pane as the pane list does, not by its window id.
	a.r.SetActorLabels(ochazuke.ActorLabels{Pane: a.paneLabelOf})
	a.localNoun(ochazuke.RestartRequestNoun)
	if err := a.db.Declare(restartTable); err != nil {
		a.r.UI().Error("restart request: " + err.Error())
		return
	}
	a.r.SetRestartTable(a.db.DocTable(restartTable.Name, a.r, ochazuke.RestartRequestNoun))
}

// wireRestart is the TUI's hook: app.restart can start the installed
// build, after mtm's cleanup (a quit hook's Close). A TUI that app.restart
// started also upgrades the daemon (upgradeAfterRestart).
func (a *App) wireRestart() {
	a.r.SetRestart(a.restartHook(true))
	a.r.AddQuitHook(ochazuke.QuitHook{Name: "close", Close: a.beforeRestart})
	a.upgradeAfterRestart()
}

// installedPath is the binary a restart starts: this process's executable
// by its path (an install replaces ~/bin/mtm by rename, so the path names
// the new build; symlinks are left for the exec to follow, so a link the
// install moved names the new target), else the program's name on PATH;
// "" when neither is known (app.restart then asks to quit and quits).
func (a *App) installedPath() string {
	exe := a.executable
	if exe == nil {
		return ""
	}
	path, err := exe()
	if err != nil || path == "" {
		if path, err = exec.LookPath("mtm"); err != nil {
			return ""
		}
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return path
}

// beforeRestart is what main does after the program returns, run once the
// terminal is released and before the exec: Close (the editor's and quick
// note's last change, the attached terminal's stream, the schedules, the
// runtime's jobs, extension processes and pools, the database), then the
// daemon presence this client holds. The
// daemon itself and its panes are untouched. Close is idempotent, so
// main's deferred Close after a failed exec does nothing more.
func (a *App) beforeRestart() error {
	err := a.Close()
	if a.daemon != nil {
		a.daemon.DropPresence()
	}
	return err
}
