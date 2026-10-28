<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
    <img alt="matomeru" src="assets/logo-light.svg" width="360">
  </picture>
  <br>
  <a href="go.mod"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/unixsuperhero/matomeru"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/unixsuperhero/matomeru"></a>
</p>

<p align="center">A terminal workspace for coding agents: projects, tasks, notes, live agent panes, their turns and your reviews, gathered in one TUI.</p>

<p align="center"><img alt="mtm over invented data: moving through projects and tasks, filtering a list, pinning a project from the palette, ticking a checklist in a note, the key help and the Settings page" src="assets/demo.gif" width="720"></p>

Matomeru (`mtm`) is built on [ochazuke](https://github.com/unixsuperhero/ochazuke) and runs its agents in panes of a [TUIOS](https://github.com/Gaurav-Gosain/tuios) daemon it starts for itself. Every action in the TUI is also a command: `mtm task new --title ...`, `mtm project pin`, `mtm catalog`.

The Inbox page (`I` from anywhere) is what needs you: action items from any source, unread first, with the count of unread and open items in the title row (`inbox 2/5`). `enter` opens an item's URL, `u` marks it read or unread, `s` snoozes it, `x` archives and `X` restores it, `t` and `c` promote it to a todo or a task, `m` moves it to a project and `d d` deletes it. Tools push items in through one command, idempotent on the source and `--key` (else the source and `--url`), so a poll that runs again refreshes its row:

```sh
mtm action-item add --source gh --url https://github.com/o/r/pull/7 --why "review requested" --title "Fix the store" --dir ~/proj/r
```

It prints the item as JSON (`id`, `new`, `state`, …). An item needs nothing: one with no project, task or pane is unassociated. `mtm action-item list`, `count`, `show`, `read`, `snooze --for 4h`, `archive`, `restore`, `promote-task`, `associate --task ID` and `delete` are the rest.

mtm's own panes feed the inbox too, while the TUI is open: an agent that waits for your input, a finished turn and an errored agent each become an item (source `mtm`, kind `needs-input`, `done` or `errored`), owned by the pane's task or project. A needs-input or errored item is archived once the pane moves on; a done item is marked read once you attach the pane. The settings `inbox.signal.needs-input`, `inbox.signal.done` and `inbox.signal.errored` turn each kind off.

Schedules run the outside signal producers on an interval, while the TUI is open, so their items arrive without a key press. The built-ins are `gh-signals` (`o-gh signals`, every 5m, `auto`: on where `o-gh` is installed and `gh auth status --active` passes) and `slack-signals` (`o-slack signals`, every 5m, off; it belongs to the work machine, and elsewhere turning it on only records "missing" on its row). One setting flips each: `mtm settings set schedule.gh-signals.enabled on|off|auto`, and `schedule.NAME.interval` sets how often. `<config>/schedules.json`, a JSON array like `harnesses.json`, adds schedules or changes a built-in's command (`[{"name": "todo-sync", "command": ["o-todo", "sync"], "interval": "1h"}]`). `S` on the Inbox opens the Schedules page: `r` runs the selected schedule now, `e` turns it on or off, `i` sets its interval, and the detail shows its recent runs and output. Runs go under `nice`, never overlap (two open TUIs run each occurrence once), time out after 2m (`"timeout"` in the file), back off after failures (the interval doubled per failure, at most an hour) and say a first failure as a notice. `mtm schedule list`, `show`, `history`, `run-now`, `enable`, `disable`, `toggle`, `set-interval --interval 10m` and `reset` are the CLI.

Schedules can also run prompts. `mtm schedule add --prompt "summarise overnight PRs" --when "weekdays 8:30" --harness claude --model sonnet --effort high --permission-mode plan --project matomeru` stores a row (on when it has a time, else off) that starts an agent at each time: an ordinary agent pane in the project's session, named after the schedule, linked to `--task` when given. `--into pane --pane reviewer` queues the prompt in the open pane named `reviewer` instead (tuios types it when the agent rests, never over a question; with no such pane a run starts one). `--when` takes `every 30m`, `daily 09:00`, `mon,wed,fri 09:00,17:30`, `weekdays 8:30`, `cron 0 9 * * 1-5` or `never`; `--start 2026-11-01` and `--end 2026-12-31` bound it. Runs missed while mtm was closed are skipped (`schedules.catch-up none`) or run once on start (`once`; a row's `--catch-up` wins). `schedule edit NAME --field value`, `duplicate NAME [--as NEW]` and `delete NAME` change the stored rows; editing a built-in stores a copy. On the Schedules page `n` adds a row and opens its form, `E` edits one, `d d` deletes a stored one; the form writes each change as you type. Each run's history says what it launched and links its pane.

An agent in a TUIOS pane can drive mtm over MCP with `mtm mcp serve`; `mtm mcp config claude` prints the entry to add. See [docs/MCP.md](docs/MCP.md).

Module `github.com/unixsuperhero/matomeru`; requires Go 1.26 or newer.

```sh
go install github.com/unixsuperhero/matomeru/cmd/mtm@latest
```

## Live shell keys

In a live pane every key goes to the program in it, except the `terminal-leader` setting (default `ctrl+a`). After the leader, the app's own normal-mode keys do what they do outside live: the same verb, and the same guard. The palette, settings and pickers open over the pane; closing them (`esc`) gives the pane its keys again.

| Key after the leader | Does | Verb |
|---|---|---|
| `:`, `ctrl+space` | command palette | `ochazuke.palette.open` |
| `,` | Settings (guard `no-modal`) | `ochazuke.settings.edit` |
| `'` | pick a page (guard `no-modal`) | `ochazuke.page.pick` |
| `ctrl+j` / `ctrl+k` | next / previous page | `ochazuke.page.next` / `ochazuke.page.previous` |
| `I` | the Inbox page | `ochazuke.page.open {id: inbox}` |
| `?` | keyboard help: the help tree, with the leader as one row and its keys under it | `ochazuke.bindings.help` |
| `` ` `` | next starred pane, attached live (`s` does the same) | `terminal.starred` |
| `W` | next pane that needs input, attached live | `terminal.waiting` |
| `*` | star or unstar the live pane | `terminal.star` (shares `pane.star`'s handler) |
| `\`, `p` | pick a pane | `pane.pick` |
| `P` | pick a project to pin (guard `no-modal`) | `project.pick` |
| `L` | launch an agent | `agent.choose` |

The live-only leader keys stay as they were: `v`/`t` side vim and terminal, `d` close a side or detach, `z` zoom, `o` the other pane, `R` side layout, `w` sides, `j`/`k` next and previous pane, `s` starred, `[` copy mode, `c` context, `f` hints, `N` quick note, `n` new task, `T` todos and `S` all projects. The leader twice sends one leader to the pane. The leader's `?` used to open the leader-only keys modal; that is `terminal.keys` now, from the palette (`terminal keys`). Changing `terminal-leader` moves every row.

Not after the leader: `[`/`]` tabs (`[` is copy mode, and live is the Screen tab), `(`/`)` focus regions, `"` and `@` registers and macros, `/` filter, `<` `>` `=` resize and `.` dot-repeat (they do nothing on the Panes page, which draws its own split and records no repeatable change), and in-component motions (`$ % ^ 0 | ; { }`).
