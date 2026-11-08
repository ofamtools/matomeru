// Command mtm-hook is `mtm agent hook` as a small binary of its own.
//
// A harness runs a hook on every event with a 500 ms budget. The first
// exec of a 77 MB mtm after each install takes about two seconds on macOS
// (the system verifies the whole file before it runs), which cuts off the
// first hooks; this binary is about 4 MB and starts in a fraction of a
// second. A launch names it in place of "mtm agent hook" when it sits beside
// mtm (harness.HookCommand). Usage: mtm-hook HARNESS [flags], as mtm agent hook.
package main

import (
	"os"

	"ofam.dev/matomeru/internal/agenthook"
)

func main() {
	server, ok := os.LookupEnv("MTM_SERVER")
	if !ok {
		server = "mtm-daemon" // cmd/mtm MtmServer
	}
	os.Exit(agenthook.Hook(os.Args[1:], agenthook.Env{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv,
		Server: server, DefaultServer: "default", // cmd/mtm DefaultDaemon
	}))
}
