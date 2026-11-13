//go:build !nodaemon

package main

import (
	"os"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/mtmd/serve"
)

// This file and daemon_off.go are the only ones that import ofam.dev/mtmd
// (TestDaemonImportBoundary). mtm links the TUIOS daemon (the mtmd module)
// and runs it behind the hidden word daemonWord, so a machine needs no tuios:
// the daemon is "mtm __mtmd ... daemon" and re-runs itself the same way.
// -tags nodaemon leaves it out (daemon_off.go) and keeps the tuios runner.

// daemonBuilt says this binary carries the daemon.
const daemonBuilt = true

// daemonShell runs the daemon's own command line when words begin with
// daemonWord, before any database opens or the runtime starts (beside
// storyShell), and returns the exit status. Handled is false for any other
// words.
func daemonShell(words []string) (code int, handled bool) {
	if len(words) == 0 || words[0] != daemonWord {
		return 0, false
	}
	return serve.Main(words[1:], daemonOptions(daemon.PreferredLayout(), daemon.LegacyEnvOn(os.Getenv(daemon.EnvLegacy)))), true
}

// daemonForeground runs the daemon of server in this process until it is
// stopped (mtm daemon serve), under the named layout. An empty server is the
// default name.
func daemonForeground(server, paths string, legacyEnv bool) int {
	args := []string{"daemon"}
	if server != "" {
		args = append([]string{"--server", server}, args...)
	}
	l, err := daemon.LayoutNamed(paths)
	if err != nil {
		l = daemon.PreferredLayout()
	}
	return serve.Main(args, daemonOptions(l, legacyEnv))
}

// daemonOptions are the daemon's options under layout l: its folder and
// socket names, and the library's table of MTM_ pane variables; with legacy
// (setting daemon.legacy-env) the TUIOS_ names are exported beside them, else
// children carry only the MTM_ ones.
func daemonOptions(l daemon.Layout, legacy bool) serve.Options {
	names := serve.Names{
		SocketApp: l.SocketApp, SocketTmp: l.SocketTmp, SocketFile: l.SocketFile,
		StateApp: l.StateApp, StateServers: l.StateServers, ServerApp: l.ServerApp, ConfigApp: l.ConfigApp,
	}
	names.Env = serve.MTMEnv
	names.DropUpstream = !legacy
	// tuios first until the owner's per-host cutover (tuios purge plan O2):
	// a remote host whose row names no command keeps answering as tuios.
	return serve.Options{SelfCommand: []string{"", daemonWord}, Version: daemonVersion(), Names: &names, RemotePrograms: []string{"tuios", "mtm"}}
}
