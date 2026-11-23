# V2 stopping point: all 9 repos clean, all 8 binaries rebuilt and ready to test

Every active worker is finished and retired, all nine `-next` worktrees are clean on local branch `next` (unpushed, all v1 `main` repos and v1 binaries untouched), and all eight v2 binaries under `~/bin/` are freshly built onto the latest `ochazuke-next` (`ad605cfc`) and verified with `--smoke`.

## Just landed in `mato` (`~/bin/m2m`, `62f6514`)

1. **Command Palette (`:`) with always-visible live argument form:** Powered by Ochazuke's new `widgets/palette`. As soon as a command with `Params` is highlighted (such as `record.new`), its argument form appears on the right immediately (unfocused while you browse). Press `tab` or `right` to focus the form (`h`/`l` cycles enums including all registered nouns on `record.new`, `i` edits with Readline, `space` toggles booleans, `p` opens record/path pickers, `shift+tab`/`esc` returns to the command list, `enter` runs). Pressing `enter` on a command with a missing required param shifts focus into the form automatically.
2. **New pane on `1 Panes` (`n`):** `record.new.here` (`n`) on `1 Panes` now opens **New pane** directly instead of falling through to **New task**.
3. **Toggleable Pages TOC rail on `1 Panes` (`t`):** Added `site.toc.toggle` bound to `t` on `1 Panes` plus opt-out setting `panes.showTOC` (default `true`). Hiding the TOC moves focus cleanly to the screen/content; `ctrl+a t` (side terminal) and `ModeReader` `t` (Markdown headings TOC) remain unaffected.
4. **CLI human-friendly `<id>` resolution + interactive TTY picker:** Every single-record noun command (`mato task star`, `mato pane edit`, `mato note get`, etc.) now resolves exact ID, unique ID prefix, exact title (case-insensitive), or unique title substring. Omitting `<id>` or hitting an ambiguous match in an interactive terminal launches an Ochazuke `widgets/picker` over matching records; non-TTY invocations fail cleanly with candidate lists or usage errors.

## Current state of all 8 v2 binaries

| Binary | Worktree (`next`) | Size | Smoke verification |
| --- | --- | ---: | --- |
| `~/bin/m2m` (`mato`) | `~/proj/matomeru-next` (`62f6514`) | 17M | `smoke: all checks passed` (`binding check`: 407 total, 0 errors) |
| `~/bin/o-csv2` | `~/proj/o-csv-next` (`9c728da`) | 8.1M | `19/19 checks passed` |
| `~/bin/o-json2` | `~/proj/o-json-next` (`d893f56`) | 8.7M | `smoke passed` |
| `~/bin/o-df2` | `~/proj/o-df-next` (`9d15403`) | 8.3M | `21/21 checks passed` |
| `~/bin/o-md2` | `~/proj/o-md-next` (`1d92764`) | 9.2M | `19/19 checks passed` |
| `~/bin/o-cmds2` | `~/proj/o-cmds-next` (`3fb32d3`) | 8.4M | `19/19 checks passed` |
| `~/bin/o-lane2` | `~/proj/o-lane-next` (`04eafe4`) | 9.5M | `19/19 checks passed` |
| `~/bin/o-questions2` | `~/proj/o-questions-next` (`4bc21f5`) | 8.7M | `17/17 checks passed` |

Foundation library: `~/proj/ochazuke-next` (`ad605cfc`, 19/19 packages `-race` pass).

## Quick things to test right now

```sh
# 1. Open Matomeru v2
mato

# Try inside mato:
#   - On 1 Panes: press n (opens "New pane"), press t (toggles the Pages TOC rail)
#   - Press : and highlight record.new (args form appears on the right; press tab, l to pick pane/note/etc., enter)
#   - Press g r (global newest-first Recents picker across all nouns)
#   - Press T (Outline: J/K reorder with counts, >/< indent/outdent, space status cycle, i rename, u undo)
#   - Go to 4 Extensions, press R to discover o-*2 tools, then g n to browse mounted external nouns

# 2. Try human CLI IDs & interactive TTY picker fallback:
mato task star                  # interactive picker opens over your tasks
mato task get "matomeru"        # resolves by unique title substring
mato binding check              # verify keymap health
```
