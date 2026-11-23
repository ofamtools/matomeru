// Command mtm-hook is a fast, minimal standalone hook binary for Matomeru.
// It delegates directly to agenthook.Run so `mtm agent hook` shares the exact
// same implementation.
package main

import (
	"os"

	"ofam.dev/matomeru/agenthook"
)

func main() {
	os.Exit(agenthook.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
