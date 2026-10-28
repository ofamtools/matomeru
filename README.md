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

An agent in a TUIOS pane can drive mtm over MCP with `mtm mcp serve`; `mtm mcp config claude` prints the entry to add. See [docs/MCP.md](docs/MCP.md).

Module `github.com/unixsuperhero/matomeru`; requires Go 1.26 or newer.

```sh
go install github.com/unixsuperhero/matomeru/cmd/mtm@latest
```
