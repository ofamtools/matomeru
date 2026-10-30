# README demo

`demo.tape` records `assets/demo.gif`, the animation under the README header. It drives `mtm` over invented projects, tasks and notes that `seed.sh` writes, with three **scripted demo agents** (`fake-agent.sh`) on the Inbox page. They are not real agents: the script calls no model and changes no file. It plays a fixed scene so the gif shows the Inbox the same way on every run, offline.

Regenerate from the repository root (needs [VHS](https://github.com/charmbracelet/vhs), Go and [TUIOS](https://github.com/Gaurav-Gosain/tuios) on `PATH`; `brew install vhs`):

```sh
vhs docs/vhs/demo.tape
```

It takes about a minute and a half and writes a GIF of about 27 seconds and about 2 MB.

## Your data stays out of it

Off camera, the tape makes a fresh `/tmp/mtm.XXXX` and runs `seed.sh` on it. The script builds `mtm` into that directory, links `tuios` beside it, and writes an `env` file that points `HOME`, every `XDG_*` directory (`XDG_RUNTIME_DIR` too) and `PATH` inside it, unsets `TUIOS_*`, `MTM_CONFIG_DIR`, `MTM_SERVER` and `CLAUDE_CONFIG_DIR`, and defines an `mtm` function that always passes `--data`, `--config` and `--socket` there, and `demo_stop`. The seed goes through mtm's own CLI (`project new`, `project edit`, `task new`, `task status`, `note new`, `agent launch`).

The agents run on a throwaway TUIOS server: `tuios --server demo start-server`, whose socket is `$D/run/tuios/servers/demo/tuios.sock` because `XDG_RUNTIME_DIR` is `$D/run`. It never reaches your own TUIOS daemon or sessions. The tape kills it (`demo_stop`) and deletes the directory when it ends.

## The scripted agents

`seed.sh` registers `fake-agent.sh` as the harness `demo-agent` in the temp `harnesses.json` and launches it three times in acme-web, each on a task whose title is its first prompt. The prompt picks the scene (the table at the end of the script):

| task | scene | Inbox |
| --- | --- | --- |
| Upgrade the image CDN | reads, then asks to approve `npm install`; an answer sends it back to work | Needs you |
| Flaky test: login redirect on Safari | keeps working, a step every few seconds | Working |
| Checkout page: show the shipping estimate | two turns, the second with two edits, then done | the list |

Each scene prints a plausible transcript in its pane, reports its state with `tuios set-agent-state` (working, needs_input, done; with a session id and the harness id `claude-code`, because the log it writes is in Claude Code's format), and writes that log under the temp `$HOME/.claude/projects`, which mtm reads as the pane's turns. The banner in the pane says it is a scripted demo agent.

The recording's `settings.json` (in `seed.sh`) also puts finished agents in the middle list (`panes.sections`, `panes.queue-order` without `unread`), opens tool calls in a turn (`turns.tools-open`), and skips the task picker after a send (`prompt.after-submit`).

To look at the demo by hand:

```sh
D=$(mktemp -d /tmp/mtm.XXXX) && docs/vhs/seed.sh "$D" && (. "$D/env" && mtm; demo_stop); rm -rf "$D"
```

Keep the directory path short: unix socket paths are limited to about 104 bytes.

## Changing it

Edit the key steps between `Show` and `Hide`. On the Inbox, a Needs you row is the agent's inbox item: `enter` opens the agent live (answer there, `ctrl+a d` back); `i` and `l` are pane keys and do not answer on that row. Inside a chooser, move with `Down`, not `j`: typed letters go into the chooser's filter, and a typed name that matches nothing is taken as a new entry. Never send function keys. To stay under 2 MB, keep the window small (`Width`, `Height`), the run short and the pauses few. Check a few frames before committing, for example:

```sh
ffmpeg -i assets/demo.gif -vf fps=1 /tmp/frame%02d.png
```
