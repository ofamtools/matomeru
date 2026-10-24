package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/unixsuperhero/oden"
)

// The outer shell is an oden App named mtm (github.com/unixsuperhero/oden):
// the first word resolves, by exact name, alias or unique prefix, to
//
//  1. an inline command: oden's builtins (help, -h/--help, commands, which)
//     and every noun of the runtime, which runs the NOUN VERB shell (cli.go)
//     with the full noun name, so mtm ta launch is task launch;
//  2. an executable mtm-<word> on PATH, run with the remaining words, so a
//     third party adds mtm web by installing mtm-web; a noun wins over a
//     same-named executable, which help lists as shadowed;
//  3. else Default: the TUI with no words, the shell with any (which names
//     an unknown operation).
//
// An ambiguous prefix prints its candidates and exits 1.

// commands is the oden App for one invocation; tui runs the TUI. opts are
// for tests (a fake PATH).
func (a *App) commands(args []string, stdin io.Reader, stdout, stderr io.Writer, tui func() error, opts ...oden.Option) *oden.App {
	cli := oden.New("mtm", args, append([]oden.Option{oden.WithBuiltins(), oden.WithStdio(stdin, stdout, stderr)}, opts...)...)
	shell := func(words []string) error {
		if err := a.ensure(context.Background()); err != nil {
			fmt.Fprintln(stderr, "mtm:", err)
			a.setPanes(nil, err)
		}
		return a.shell(stdin, stdout, stderr).run(words)
	}
	cli.Default = func(_ context.Context, words []string) error {
		if len(words) == 0 {
			return tui()
		}
		return shell(words)
	}
	verbs := map[string][]string{}
	for _, op := range a.r.Operations {
		verbs[op.Noun] = append(verbs[op.Noun], op.Verb)
	}
	nouns := make([]string, 0, len(verbs))
	for noun := range verbs {
		nouns = append(nouns, noun)
	}
	sort.Strings(nouns)
	for _, noun := range nouns {
		sort.Strings(verbs[noun])
		cli.Add(oden.Command{
			Name:    noun,
			Summary: strings.Join(verbs[noun], " "),
			Source:  "noun",
			Run: func(_ context.Context, rest []string) error {
				return shell(append([]string{noun}, rest...))
			},
		})
	}
	return cli
}
