package main

import (
	"context"
	"errors"
	"path/filepath"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/ochazuke"
)

// importOptions are daemon.import-state's options for this run: the target is
// the server mtm runs on, the state homes are where a daemon started from
// this environment keeps its files (and, for the source, the places a tuios
// started from another shell may have used).
func (a *App) importOptions(from string, dry, overwrite bool) (daemon.ImportOptions, error) {
	if a.daemon.Server == "" || a.daemon.Server == daemon.DefaultServerName {
		return daemon.ImportOptions{}, errors.New("daemon.import-state needs a named mtm server (-server NAME), not -socket or -server default: it imports into that server's state")
	}
	home, _ := a.home()
	base := daemon.StateHome(a.getenv, home)
	o := daemon.ImportOptions{From: from, To: a.daemon.Server, Base: base, DryRun: dry, Overwrite: overwrite}
	if home != "" {
		o.Bases = []string{filepath.Join(home, ".local", "state"), filepath.Join(home, "Library", "Application Support")}
	}
	return o, nil
}

// daemonImportStateVerb is daemon.import-state: copy a tuios server's saved
// layout and history into the server mtm runs on, so its daemon restores the
// sessions and panes with their IDs. Copies, never moves.
func (a *App) daemonImportStateVerb(ctx ochazuke.Ctx) ochazuke.Result {
	from := ochazuke.StringArg(ctx.Args, "from", daemon.DefaultServerName)
	o, err := a.importOptions(from, boolArg(ctx.Args, "dry-run", false), boolArg(ctx.Args, "overwrite", false))
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Pending(func(context.Context) (any, error) {
		return daemon.ImportState(o)
	})
}

const daemonImportAbout = "Copy another TUIOS server's saved sessions and history (--from default or a server name) into mtm's server, so its daemon restores them with their session and pane IDs. Copies, never moves; refuses while either daemon runs or when the saved state is of a version it cannot read. --dry-run lists what would be copied. Processes do not survive: panes restart as new shells"

// defineDaemonImportParameters declares import-state's parameters.
func (a *App) defineDaemonImportParameters() {
	a.parameters("daemon.import-state", map[string]ochazuke.Parameter{
		"from":      {Type: "string", Description: "The server to copy from: default (your own tuios) or a server name"},
		"dry-run":   {Type: "boolean", Description: "List what would be copied and change nothing"},
		"overwrite": {Type: "boolean", Description: "Move target files of the same names into the target's archive/import-TIME folder first, instead of refusing"},
	})
}
