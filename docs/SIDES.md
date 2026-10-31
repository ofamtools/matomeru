# Side panes

Side panes are mtm domain state: daemon windows linked to a parent pane,
launched in its current folder with its recorded launch environment. The
`side-layout` setting controls where the visible side sits. The program's
exit closes it; detaching or quitting mtm keeps it running.

Definitions load in this order, later rows winning by name:

1. Built-in `vim` (owned vim session, key `v`) and `term` (the configured shell, key `t`).
2. Registered extension catalogs' `sides` arrays, in extension registration order. A built-in name is reserved here; mtm reports and skips the clash.
3. `<config>/sides.json`, a JSON array merged by name. Omitted fields retain the previous definition; explicit fields replace them. `{"name":"term","disabled":true}` hides a definition. Every definition is enabled by default.

For example:

```json
[
  {"name":"scratch","title":"Side scratch","key":"g",
   "command":["scratch-tool","--directory","{dir}"]},
  {"name":"term","key":"u"}
]
```

The Settings page's side-layout description names the file. `config.reload`
reads it again; invalid configuration keeps the last working definitions.
Catalog refresh and extension removal update the extension definitions.
A companion whose definition has gone remains reachable from the side
chooser under its name, so removing configuration never loses a running
program.

`launch` defaults to `command`. Its argv is expanded element by element,
without shell parsing or splitting. Placeholders: `{dir}` (the parent's
current folder), `{pane}` (window ID), `{session}` (session name), `{project}`
(project ID, possibly empty), `{shell}` (`SHELL`, falling back to `/bin/sh`),
and `{bin}` (the registered extension binary). User overrides of an extension
row retain its binary. `vim-session` selects the existing owned vim-session
launcher. An unknown launcher produces a notice and launches nothing.

`side.list` shows definitions, sources, keys and commands. `side.show NAME`
and `pane.side --kind NAME` open or focus a side; `--pane WINDOW_ID` supplies
the parent to CLI callers. `side.restart NAME` closes that side and opens it
again in the parent's current folder; in live use the omitted name means
the front side. A later `cd` does not restart a running side automatically.
`pane.side --kind list` lists that parent's companions; `panes.sides` opens
the chooser of every running side.

Each definition's `key` generates one live-only leader row. Pressing it
again returns focus to the parent. An empty key leaves the side available
through verbs. Existing leader-key clashes are reported and the side key
is skipped; `bindings.json` can replace or unbind the default row. Leader
`w` chooses any open side, `o` moves between parent and front side, `R`
changes layout, and `d` hides the front side, falls back to the previously
shown side, then detaches. Hiding a side does not kill its program.

## Compared with Vim

Checked against Vim's local `terminal.txt` and `windows.txt` help.

| Vim form | Help tag | mtm |
|---|---|---|
| `:terminal` | `:terminal` | Similar: runs a program in a split; mtm chooses argv from the side definition and uses a daemon window. |
| Terminal window commands | `terminal-typing` | Similar: leader then an application key; mtm's configurable leader defaults to `ctrl+a`. |
| `CTRL-W w` | `CTRL-W_w` | Similar: leader `o` cycles parent and front side, rather than all windows. |
| Terminal job exit | `terminal-close` | Similar to `:terminal ++close`: an exited side's daemon window is closed during reconciliation. |
