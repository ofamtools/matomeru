package agenthook

// HookUsage is the help of mtm agent hook.
const HookUsage = `usage: mtm agent hook <harness> [event] [payload] [--window ID] [--session NAME] [--explain] [--timeout D] [--no-context] [--send-cwd]

Report a coding-agent harness's hook event as the agent state of the pane it
runs in. The harness hands its payload on stdin (Codex notify: as the last
argument). Harnesses: claude-code, codex, copilot, cursor-agent, gemini-cli,
opencode, amp, kilo, kimi, omp, pi, qwen (state) and antigravity, crush, devin,
droid, grok, hermes, qoder (conversation id only).

The pane is found from --window, then MTM_PANE_ID (or TUIOS_PANE_ID), then the process's
terminal and parent processes. It only connects: with no daemon running it
exits 0 and prints nothing. It always exits 0, gives up after 500ms, and
--explain prints on stderr what it decided and why. --no-context leaves out
the line a SessionStart adds to Claude Code's and Codex's context.
--send-cwd also sends the folder the harness reports (agent_cwd); by default
the daemon's own PTY-derived folder is the only one.
`

// StatusLineUsage is the help of mtm agent statusline.
const StatusLineUsage = `usage: mtm agent statusline <harness> [--then COMMAND] [--turn-end] [--window ID] [--session NAME] [--explain]

Read a harness's status line payload on stdin and write the model, context use
and cost it names to the pane's agent metadata. It prints nothing of its own;
--then runs your own status line command with the same stdin and prints its
output unchanged. Reports at most every 15 seconds per pane while the values
change, and exits 0 whatever goes wrong on the daemon side.
`

// StateUsage is the help of mtm agent state.
const StateUsage = `usage: mtm agent state <none|working|needs_input|idle|done|errored|unknown> [-m MESSAGE] [-w ID] [-s SESSION]
       [--source report|osc|screen|stall] [--harness ID] [--kind approval|question|auth]
       [--agent-session-id ID] [--transcript-path FILE] [--if-state STATES]

Report a pane's agent state by hand. Without -w, run in a pane, the report is
about that pane.
`
