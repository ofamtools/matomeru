# agenthook

What coding-agent hooks and status lines say to the daemon, over its wire
protocol, with no `tuios` binary: `mtm agent hook <harness> [event]`,
`mtm agent statusline <harness>` and `mtm agent state <state>` (cmd/mtm
`agent_hook_shell.go` runs them before any database opens). They replace
`tuios agent-hook`, `tuios agent-statusline` and `tuios set-agent-state`.

A hook only connects. With no daemon running it exits 0 and prints nothing
(`--explain` says "no daemon"); it never starts one.

## Source

Copied and adapted from tuios v0.12.1 (MIT, Copyright (c) 2025 Gaurav Gosain;
`LICENSE-tuios`, and a header comment in each copied file):

| here | tuios v0.12.1 |
|---|---|
| `hook*.go`, `activity.go`, `approval.go`, `summary.go`, `proc*.go` | `internal/integration/` (the hook half; the installers are not copied) |
| `statusline_parse.go` | `internal/integration/statusline.go` (the parser; not the installer) |
| `cmd_agent_hook.go`, `cmd_agent_statusline.go` | `cmd/tuios/agent_hook.go`, `agent_statusline.go` (cobra and the verb client replaced) |
| `invisible/`, `lazyre/`, `procinfo/`, `atomicfile/` | the same `internal/` packages |
| `testdata/hooks/*.json`, `*_test.go` | the recorded payload fixtures and their tests |
| `run.go`, `usage.go` | mtm's own: flags, socket choice, the wire caller, `mtm agent state` |

Differences from tuios, all on purpose:

- **PTY signals first.** The hook does not send `agent_cwd` (the daemon derives a
  pane's folder from the PTY and accepts `agent_cwd` only when it equals that
  folder). `--send-cwd` sends it. Everything else a hook sends is agent-only
  state: turn and approval state, session id, transcript path, prompt,
  subagent events, activity.
- **No mail.** The `agent-mail-notice` line a hook added to the agent's context
  is gone (`mtm mail` is dropped).
- **Session hint.** A Claude Code or Codex SessionStart adds one line to the
  agent's context: the pane, the session, the mtm MCP gateway, the safety rule.
  tuios's version named its own commands. `--no-context` leaves it out.
- **Socket.** `--socket`, else `TUIOS_SERVER` (a non-default server's panes get
  it), else `TUIOS_SOCKET` (the default server's panes), else mtm's `--server`.
- Verb calls use `internal/daemon` (one connection per call) instead of
  tuios's verb client.

## Per-launch hooks

A harness with `hookArgs` (data in `internal/harness/builtins.json`) is launched
with its hook config on its command line, so no global settings file is needed.
Claude Code: `--settings '{"hooks":{...}}'` with every event running
`'<mtm>' agent hook claude-code --integration 3`. The setting `agent.hooks`
(default on) turns this off. Codex takes hooks with `-c hooks.<Event>=[...]`
but holds them untrusted until each is trusted, so it has no `hookArgs`.

## Comparing with tuios

`compare/` runs `tuios agent-hook` v0.12.1 and `mtm agent hook` on the same
fixtures, each against its own daemon in a temp root, and compares the pane's
state and activity afterwards (`compare.py`), plus statusline, state and
absent-daemon checks (`compare_misc.py`):

    sh ../daemon/contract/build-tuios.sh /tmp/x/tb v0.12.1
    go build -o /tmp/x/mtm ../../cmd/mtm
    python3 compare/compare.py /tmp/x/tb/bin/tuios-v0.12.1 /tmp/x/mtm testdata/hooks
    python3 compare/compare_misc.py /tmp/x/tb/bin/tuios-v0.12.1 /tmp/x/mtm
