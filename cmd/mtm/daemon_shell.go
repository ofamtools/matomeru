//go:build !nodaemon

package main

import "ofam.dev/mtmd/serve"

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
	return serve.Main(words[1:], serve.Options{SelfCommand: []string{"", daemonWord}, Version: daemonVersion()}), true
}

// daemonForeground runs the daemon of server in this process until it is
// stopped (mtm daemon serve). An empty server is the default name.
func daemonForeground(server string) int {
	args := []string{"daemon"}
	if server != "" {
		args = append([]string{"--server", server}, args...)
	}
	return serve.Main(args, serve.Options{SelfCommand: []string{"", daemonWord}, Version: daemonVersion()})
}
