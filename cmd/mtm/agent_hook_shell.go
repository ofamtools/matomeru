package main

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"ofam.dev/matomeru/internal/agenthook"
	"ofam.dev/matomeru/internal/harness"
	"ofam.dev/ochazuke"
)

// agentShellWords are the agent commands that run before any database opens
// or the runtime starts (beside storyShell and daemonShell): a harness runs
// them on every tool call and status line change, from a hook with a
// 500ms budget, so they open nothing but the daemon's socket. They replace
// tuios agent-hook, agent-statusline and set-agent-state: the daemon's own
// wire verbs, no tuios binary. A hook only connects; with no daemon running
// it exits 0 and says nothing (a hook never starts one).
//
//	mtm agent hook <harness> [event]   a harness's hook event as the pane's agent state
//	mtm agent statusline <harness>     a harness's status line as the pane's agent metadata
//	mtm agent state <state>            a pane's agent state, by hand
var agentShellWords = map[string]func(args []string, env agenthook.Env) int{
	"hook":       agenthook.Hook,
	"statusline": agenthook.StatusLine,
	"state":      agenthook.State,
}

// agentShell runs one of agentShellWords when words are "agent WORD ...", and
// returns its exit status. Handled is false for any other words (agent
// launch, agent start, ... are ordinary verbs).
func agentShell(words []string, opts Options, stdin io.Reader, stdout, stderr io.Writer) (code int, handled bool) {
	if len(words) < 2 || words[0] != "agent" {
		return 0, false
	}
	run, ok := agentShellWords[words[1]]
	if !ok {
		return 0, false
	}
	return run(words[2:], agenthook.Env{
		Stdin: stdin, Stdout: stdout, Stderr: stderr,
		Getenv: opts.getenv, Socket: opts.Socket, Server: opts.Server, DefaultServer: DefaultDaemon,
	}), true
}

// settingAgentHooks turns per-launch hook settings off. A harness that takes
// its hook config on its command line (harness hookArgs; Claude Code's
// --settings) is launched with mtm agent hook as its hooks, so an agent mtm
// starts reports its turn and approval state, session id, transcript, prompt
// and subagents without a global settings file naming a tuios binary.
const settingAgentHooks = "agent.hooks"

func agentHookSettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingAgentHooks, Type: ochazuke.SettingBoolean, Default: true,
			Description: "Launch agents with hook settings on their command line (Claude Code: --settings) so they report their own state through mtm agent hook. Off launches the harness unchanged; hooks in the harness's own config files still run"},
	}
}

// hookFlags are the argv words that carry h's hook settings for a launch: none
// when agent.hooks is off or the harness takes none. They are its hookArgs,
// then its per-event hooks (with their trust words), then its hook files:
// each file is written under the data directory's hooks folder, and skipped
// when a file it would duplicate is installed already.
func (a *App) hookFlags(h harness.Harness) []string {
	if len(h.HookArgs) == 0 && h.Hooks == nil && len(h.HookFiles) == 0 {
		return nil
	}
	if a.executable == nil || !a.r.Settings().Bool(settingAgentHooks) {
		return nil
	}
	exe, err := a.executable()
	if err != nil {
		return nil
	}
	out := append(h.HookFlags(exe), h.EventFlags(exe)...)
	for _, f := range h.HookFiles {
		if slices.ContainsFunc(f.SkipIfExists, func(p string) bool { _, err := os.Stat(a.expandPath(p)); return err == nil }) {
			continue
		}
		body, err := harness.HookFileContent(f.Source, exe)
		if err != nil {
			continue
		}
		path := filepath.Join(a.dataDir(), "hooks", f.Source)
		if old, err := os.ReadFile(path); err != nil || string(old) != body {
			if os.MkdirAll(filepath.Dir(path), 0o755) != nil || os.WriteFile(path, []byte(body), 0o644) != nil {
				continue
			}
		}
		for _, w := range f.Args {
			out = append(out, strings.ReplaceAll(w, "{path}", path))
		}
	}
	return out
}
