package main

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

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
//     tool o-<word> (oden.WithPrefixes("o")), so mtm ai runs o-ai and mtm
//     inv runs o-inventory. o-proj is hidden from this discovery
//     (familyFS): it keeps repositories, a different thing from mtm's
//     project noun, and mtm proj is the inline short spelling of project;
//  3. else Default: the TUI with no words; any other word names nothing,
//     so Default returns oden.ErrNotFound and oden reports it: "mtm:
//     command "tasks" not found", a "did you mean: task" line, then the
//     command list, exit 1 (an unknown verb, mtm task frob, is reported the
//     same way by the noun's group).
//
// An ambiguous prefix prints its candidates and exits 1.

// commands is the oden App for one invocation; tui runs the TUI. opts are
// for tests (a fake PATH).
func (a *App) commands(args []string, stdin io.Reader, stdout, stderr io.Writer, tui func() error, opts ...oden.Option) *oden.App {
	app := oden.New("mtm", args, append([]oden.Option{oden.WithBuiltins(), oden.WithPrefixes("o"), oden.WithFS(familyFS{hidden: hiddenTools}), oden.WithStdio(stdin, stdout, stderr)}, opts...)...)
	sh := a.shell(stdin, stdout, stderr)
	app.Default = func(_ context.Context, words []string) error {
		if len(words) == 0 {
			return tui()
		}
		return oden.ErrNotFound
	}
	ocli.Mount(app, sh, "bindings", "history")
	a.defineMCPCommand(app, stdin, stdout, stderr) // mtm mcp (mcp.go)
	// Mount also mounts the framework's binding noun, which made "bind" an
	// ambiguous prefix; bind stays the short spelling of bindings.
	app.Add(oden.Command{Name: "bind", Summary: "bindings (short spelling)", Source: "alias", Run: func(ctx context.Context, args []string) error {
		return sh.Run(ctx, append([]string{"bindings"}, args...))
	}})
	// proj is the short spelling of project, mtm's own noun (a work
	// context). An inline name outranks any external, and o-proj is hidden
	// besides, so no oden rule can route mtm proj to o proj.
	app.Add(oden.Command{Name: "proj", Summary: "project (short spelling)", Source: "alias", Run: func(ctx context.Context, args []string) error {
		return a.commands(append([]string{"project"}, args...), stdin, stdout, stderr, tui, opts...).Run(ctx)
	}})
	return app
}

// hiddenTools are executables mtm's discovery never sees: o-proj is the
// family's repository tool (o proj), not mtm's project noun.
var hiddenTools = map[string]bool{"o-proj": true}

// familyFS is the file system oden discovers externals through, with the
// hidden tools left out.
type familyFS struct{ hidden map[string]bool }

func (f familyFS) ReadDir(dir string) ([]fs.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	return slices.DeleteFunc(entries, func(e fs.DirEntry) bool { return f.hidden[e.Name()] }), err
}

func (f familyFS) Stat(path string) (fs.FileInfo, error) {
	if f.hidden[filepath.Base(path)] {
		return nil, &fs.PathError{Op: "stat", Path: path, Err: fs.ErrNotExist}
	}
	return os.Stat(path)
}
