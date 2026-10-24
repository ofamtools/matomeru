package main

import (
	"context"
	"io"

	ocli "github.com/unixsuperhero/ochazuke/cli"
	"github.com/unixsuperhero/oden"
)

// The outer shell is an oden App named mtm (github.com/unixsuperhero/oden):
// the first word resolves, by exact name, alias or unique prefix, to
//
//  1. an inline command: oden's builtins (help, -h/--help, commands, which),
//     every noun of the runtime and catalog, mounted by ochazuke/cli.Mount, so mtm ta
//     launch is task launch;
//  2. an executable mtm-<word> on PATH, run with the remaining words, so a
//     third party adds mtm web by installing mtm-web; a noun wins over a
//     same-named executable, which help lists as shadowed; then a family
//     tool o-<word> (oden.WithPrefixes("o")), so mtm proj runs o-proj
//     unless an mtm-proj is on PATH;
//  3. else Default: the TUI with no words, the shell with any (which names
//     an unknown operation).
//
// An ambiguous prefix prints its candidates and exits 1.

// commands is the oden App for one invocation; tui runs the TUI. opts are
// for tests (a fake PATH).
func (a *App) commands(args []string, stdin io.Reader, stdout, stderr io.Writer, tui func() error, opts ...oden.Option) *oden.App {
	app := oden.New("mtm", args, append([]oden.Option{oden.WithBuiltins(), oden.WithPrefixes("o"), oden.WithStdio(stdin, stdout, stderr)}, opts...)...)
	sh := a.shell(stdin, stdout, stderr)
	app.Default = func(ctx context.Context, words []string) error {
		if len(words) == 0 {
			return tui()
		}
		return sh.Run(ctx, words)
	}
	ocli.Mount(app, sh, "bindings", "history")
	return app
}
