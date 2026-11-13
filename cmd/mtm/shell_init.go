package main

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"slices"
	"strings"

	"ofam.dev/ochazuke"
	"ofam.dev/oden"
)

// shell-init prints the snippet a shell's rc file evaluates so the shell
// sends the OSC 133 marks (A prompt start, B prompt end, C command start,
// D;exit command end) that pane.run, the terminal-command inbox items and
// shell turns read. The snippets live in cmd/mtm/shellinit; one mtm version
// always prints the same bytes, so the output can be cached in a file.
//
// It runs before any database opens or the runtime starts (beside agentShell),
// because it is run from every shell's rc file and prints raw text, which the
// JSON-only verb CLI cannot. oden lists it in mtm help (shellInitCommand).
//
// The same snippet is the verb shell.init (defineShell), so the palette,
// MCP and bindings reach it too (the palette shows it in the text view to
// copy). `mtm shell init zsh` runs that verb and answers JSON, as every
// verb does; `mtm shell-init zsh` keeps the raw text an rc file evaluates.

//go:embed shellinit/zsh.sh
var shellInitZsh string

//go:embed shellinit/bash.sh
var shellInitBash string

// shellInitSnippets maps a shell to its snippet: data, not a branch per shell.
var shellInitSnippets = map[string]string{"zsh": shellInitZsh, "bash": shellInitBash}

const shellInitAbout = "Print the shell snippet that makes a pane's shell send OSC 133 marks (zsh, or bash 4.4+); it does nothing unless MTM_ENV=1"

const shellInitHelp = `usage: mtm shell-init zsh|bash

Prints the rc-file snippet that makes a pane's shell send OSC 133 marks (A prompt
start, B prompt end, C command start, D;exit command end), which pane run, the
terminal-command inbox items and shell turns read. It does nothing unless
MTM_ENV=1, keeps your prompt and existing hooks, and sourcing it twice is a no-op.

  eval "$(mtm shell-init zsh)"                  # in ~/.zshrc: runs mtm at every shell start

Starting mtm takes a moment, so cache the output instead; it is the same bytes for
one mtm version (regenerate it after upgrading mtm):

  mtm shell-init zsh > ~/.mtm-init.zsh          # once
  [[ -r ~/.mtm-init.zsh ]] && source ~/.mtm-init.zsh    # in ~/.zshrc

It replaces sourcing another tool's OSC 133 script.
`

// shellInitShell runs shell-init when words begin with it, and returns its
// exit status. Handled is false for any other words: `mtm shell init zsh`
// is the verb shell.init, run by the verb CLI.
func shellInitShell(words []string, stdout, stderr io.Writer) (code int, handled bool) {
	if len(words) == 0 || words[0] != "shell-init" {
		return 0, false
	}
	words = words[1:]
	if len(words) == 1 {
		if s, ok := shellInitSnippets[words[0]]; ok {
			fmt.Fprint(stdout, s)
			return 0, true
		}
	}
	fmt.Fprint(stderr, shellInitHelp)
	return 2, true
}

// shellInitCommand lists shell-init in mtm help; shellInitShell has already
// answered by the time oden would run it.
func shellInitCommand(stdout, stderr io.Writer) oden.Command {
	return oden.Command{Name: "shell-init", Summary: shellInitAbout, Source: "inline", Run: func(_ context.Context, args []string) error {
		if code, _ := shellInitShell(append([]string{"shell-init"}, args...), stdout, stderr); code != 0 {
			return daemonExit(code)
		}
		return nil
	}}
}

// shellInitNames are the shells shell.init answers for, sorted.
func shellInitNames() []string {
	names := make([]string, 0, len(shellInitSnippets))
	for name := range shellInitSnippets {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// defineShellNoun declares the shell noun: the shell a pane runs, and
// shell.init, the rc-file snippet as a verb (the CLI's shell-init).
func (a *App) defineShellNoun() {
	n := a.r.Define("shell").About("The shell a pane runs: the rc-file snippet that makes it send OSC 133 marks")
	a.localVerb(n, "shell", "init", a.shellInit, shellInitAbout).Fetch().TextResult()
	a.parameters("shell.init", map[string]ochazuke.Parameter{
		"name": {Type: "string", Required: true, Enum: shellInitNames(), Description: "The shell: " + strings.Join(shellInitNames(), " or ")},
	})
	op := a.r.Operations.At(ochazuke.Key(a.r.CanonicalKey("shell.init")))
	op.Positional = []string{"name"}
	a.r.Operations.Set(op)
}

// shellInit answers the snippet for the named shell.
func (a *App) shellInit(c ochazuke.Ctx) ochazuke.Result {
	name := ochazuke.StringArg(c.Args, "name", "")
	s, ok := shellInitSnippets[name]
	if !ok {
		return ochazuke.Failed(fmt.Errorf("no snippet for shell %q (%s)", name, strings.Join(shellInitNames(), ", ")))
	}
	return ochazuke.Done(s)
}
