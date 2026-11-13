//go:build nodaemon

package main

// daemonBuilt is false in a -tags nodaemon build: no mtmd, so no hidden route
// and only the tuios runner (daemon_shell.go is the normal build).
const daemonBuilt = false

func daemonShell([]string) (int, bool) { return 0, false }

// daemonForeground runs nothing without the daemon; mtm daemon serve checks
// daemonBuilt first.
func daemonForeground(string, string, bool) int { return 2 }
