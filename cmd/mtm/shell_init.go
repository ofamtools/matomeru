package main

import (
	"context"
	_ "embed"
	"fmt"
	"io"

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

// shellInitShell runs shell-init (or "shell init") when words begin with it,
// and returns its exit status. Handled is false for any other words.
func shellInitShell(words []string, stdout, stderr io.Writer) (code int, handled bool) {
	switch {
	case len(words) > 0 && words[0] == "shell-init":
		words = words[1:]
	case len(words) > 1 && words[0] == "shell" && words[1] == "init":
		words = words[2:]
	default:
		return 0, false
	}
	if len(words) == 1 {
		if s, ok := shellInitSnippets[words[0]]; ok {
			fmt.Fprint(stdout, s)
			return 0, true
		}
	}
	if len(words) == 1 && (words[0] == "-h" || words[0] == "--help" || words[0] == "help") {
		fmt.Fprint(stdout, shellInitHelp)
		return 0, true
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
