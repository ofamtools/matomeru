package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	ocli "github.com/unixsuperhero/ochazuke/cli"
)

// The CLI shell is ochazuke/cli's: mtm NOUN VERB [--target ID] [--json
// '{...}'] [--ARG VALUE] [--yes] runs one registered operation in this
// process through Runtime.HandleCall (history source "cli"), settles its
// jobs, answers a confirm from --yes or stdin and prints the result as
// JSON. mtm passes in only the slice hooks:
//
//   - Prepare starts mtm's server when missing before every command; a
//     failure is printed and kept as the pane list's error, and the
//     command still runs (a store verb needs no daemon).
//   - Reveal loads the rows that hold a --target no list shows yet.
//   - Owners names the instance a noun with none of its own acts for
//     (panes → panes/list, so panes refresh runs).
//   - Settle removes every terminal before each settle round: the CLI has
//     no screen, so a terminal an operation or a completion adds (a launch
//     ends attached to its pane) has its stream released and its first
//     read, cancelled, returns at once instead of waiting for the pane.
//
// The action history is in memory and per process: history list shows the
// requests this invocation ran.

var cliUsage = "usage: mtm [--data PATH] [--server NAME|default] [--socket PATH] NOUN VERB [--target ID] [--json '{...}'] [--ARG VALUE] [--yes]\n" + ocli.UsageHint("mtm")

// shell is the CLI over this app's runtime.
func (a *App) shell(stdin io.Reader, stdout, stderr io.Writer) *ocli.Shell {
	sh := ocli.New(a.r, "mtm", stdin, stdout, stderr)
	a.stdin = stdin
	sh.Usage = cliUsage
	sh.Reveal = func(target string) error {
		if a.revealExtTarget(target) {
			return nil
		}
		return a.reveal(target)
	}
	sh.Owners = a.owners
	sh.Settle = a.closeTerminals
	sh.Prepare = func(ctx context.Context) error {
		a.cli = true
		if err := a.r.OpenPage("work"); err != nil {
			return err
		}
		if err := a.r.SetFocus("work"); err != nil {
			return err
		}
		if err := a.ensure(ctx); err != nil {
			fmt.Fprintln(stderr, "mtm:", err)
			a.setPanes(nil, err)
		}
		return nil
	}
	return sh
}

// closeTerminals removes every terminal instance, in id order; closing one
// cancels its read and releases its stream, a held one included.
func (a *App) closeTerminals() error {
	var ids []string
	for id := range a.r.Instances {
		if a.r.Terminal(id) != nil {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var errs []error
	for _, id := range ids {
		errs = append(errs, a.r.RemoveInstance(id))
	}
	return errors.Join(append(errs, a.dropHeld())...)
}
