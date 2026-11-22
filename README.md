# Matomeru v2

Matomeru keeps projects, tasks, lanes, notes and runs beside the panes you and your agents work in. It is a terminal program (`mtm`) built on Ochazuke v2: every action is a command, every key is a binding you can change, and every write is a change set you can undo.

This is the `next` branch: version 2, rebuilt from the data up. It keeps its own database and its own configuration, so it runs beside version 1 without touching it.

## Start

```sh
go run ./cmd/mtm --tutorial      # a guided workspace kept in memory
go run ./cmd/mtm --seed-demo     # your own store, with demo data added once
go run ./cmd/mtm                 # your own store
go run ./cmd/mtm --smoke         # a scripted run with no terminal; exits 0 when every check holds
```

`--tutorial` opens a workspace that lives in memory. It reads and writes none of your data or configuration, and it is gone when you quit. Its projects, subtasks, lane, pane pages, notes and runs are the lessons: [docs/TUTORIAL.md](docs/TUTORIAL.md) is the same walk on paper.

`--seed-demo` adds a small, realistic data set to the store you open: two projects with subtasks, two panes with pages and an open question, a lane tree with a timeline, notes, documents, schedules, runs and extensions. Seeding twice changes nothing.

The store is a SQLite file at `$MTM_DB`, or `~/.local/share/matomeru-next/mtm.db`. `--db PATH` names another.

## The four spaces

| Key | Space | What it holds |
| --- | --- | --- |
| `1` | Panes | The pane rail, and each pane's site: its live Screen and the pages its agent sent. |
| `2` | Work | The task index, and a Show page for any record with a back and forward history. |
| `3` | Runs | Commands run as records, with their output. |
| `4` | Extensions | The `o-*` tools found on this machine, their health and their commands. |

`]` and `[` step through them. The footer always shows the mode, the scope chain of the focus, and the keys that work there. `?` lists every binding that applies; `:` runs any command by name.

### Panes

The rail orders panes by your last prompt, never by an agent's input. `enter` goes to a pane's pages, `enter` on a page opens it, and `enter` on Screen types into the pane's terminal.

In the terminal every key goes to the program. `ctrl+a` is the leader:

| Keys | Does |
| --- | --- |
| `ctrl+a d` | detach |
| `ctrl+a z` | zoom the pane: hide the rail and the pages, or bring them back |
| `ctrl+a esc` | send a literal escape to the program |
| `ctrl+a L` | open the pane's active lane |
| `ctrl+a W` | go to the next pane with an open question or a waiting program (`W` outside the terminal) |
| `ctrl+a [` | copy mode over the scrollback |
| `ctrl+a t`, `ctrl+a v` | a shell or an editor in a pane beside this one |
| `ctrl+a` then any normal key | that key's command, such as `ctrl+a 2` for Work |

A live Screen shows the program's process ID in its title. The program is started with `MTM_PANE_ID` and `MTM_TASK_ID` in its environment.

### Lanes

A lane is a record of one piece of work an agent does: its status, summary, task, repository, branch, worktree and commits. Lanes nest by `parentLane`, and any number of panes may claim a lane.

When a pane works in an active lane tree, one line above its pages shows the progress:

```
Lane: Ochazuke v2 Wave 3  [███▓░░] 3/6 done · 1 running (picker-worker) · 2 planned  (ctrl+a L)
```

`█` is done or merged, `▓` is running or in review, `░` is planned or parked. The lane is found from a claim on the pane, walked up to its root lane; with no claim, from the pane's task upward to the nearest task that has an active lane. `(+N)` counts other active trees. The line is hidden when no active lane applies, and the setting `panes.laneProgressBar` turns it off.

A lane's Show page leads with a timeline: the notes logged about it and its change sets, in the order they happened.

### Work

`enter` opens a record's Show page: its props on the left, everything related to it on the right. `backspace` or `H` goes back, `L` goes forward.

| Key | Does |
| --- | --- |
| `e` | edit. The form has every prop: the schema's fields, then star, archive and snooze, then any field the record holds that the schema does not declare. Each change is written as you make it. |
| `n`, `N` | a new record of the index's noun, a new note about the focused record |
| `m` | move the focused record to a task. A task moves under it; `(None / root)` makes a task a project. |
| `s`, `a`, `Z` | star, archive, snooze (the same key undoes each) |
| `u`, `ctrl+r` | undo, redo |
| `g n` | browse any noun: pick one and its index opens here |
| `/`, `M` | filter the list, change how the filter matches |
| `f`, `g s`, `g b` | pick the field to filter on, sort by or group by, from every field of the rows |
| `o`, `O` | cycle the sort (ascending, descending, off), sort by the next column |
| `A` | show or hide archived records |

`g n` lists every registered schema with a live count: tasks, lanes, panes, pages, documents, notes, schedules, occurrences, runs, extensions, and any schema an extension or your own code registered. Every index works the same way.

## Settings and bindings

Configuration lives in `$MTM_CONFIG_DIR`, or `~/.config/matomeru-next`. Version 1's `~/.config/matomeru` is never read.

| File | Holds |
| --- | --- |
| `settings.json` | the settings you changed |
| `bindings.json` | your binding rows, laid over the defaults |
| `themes.json` | colour roles and tones laid over the chosen theme |

`S` opens the settings as a form. A change applies at once and is saved.

| Setting | Values | Default |
| --- | --- | --- |
| `ui.theme` | the built-in themes | `ochazuke-dark` |
| `ui.density` | `compact`, `balanced`, `spacious` | `balanced` |
| `ui.borderStyle` | `rounded`, `sharp`, `minimal`, `none` | `rounded` |
| `show.metadata` | on, off | on |
| `show.emptyRelations` | on, off | on |
| `index.flagsColumn` | on, off | on |
| `index.defaultMatchMode` | `substring`, `fuzzy`, `regex` | `substring` |
| `edit.showUniversalFields` | on, off | on |
| `edit.showExtraFields` | on, off | on |
| `panes.laneProgressBar` | on, off | on |
| `panes.railAutoWiden` | on, off | off |

`B` opens the binding editor over the whole keymap.

| Key | Does |
| --- | --- |
| `/` | filter. `key:`, `verb:`, `mode:`, `scope:`, `when:` and `conflict:` narrow one column, such as `mode:normal verb:record`. |
| `r` | rebind: press the new keys, then `enter` (`esc` cancels) |
| `x` | unbind |
| `R` | reset to what it was |
| `u`, `ctrl+r` | undo, redo |

The Status column names conflicts (duplicate, shadowed, prefix collision, same key under different conditions) and how a binding reaches the leader. A change is the live keymap at once and is saved as an override row. There is no save key.

`mtm config export` prints the settings and bindings in effect as JSON. With `--jsonl` it prints one setting per line, then one binding per line.

## Agents: `mtm mcp serve`

`mtm mcp serve` is an MCP server on stdin and stdout (JSON-RPC 2.0, one message per line) over the same store the TUI shows.

| Tools | For |
| --- | --- |
| `ui.create_page`, `ui.update_page`, `ui.ask`, `ui.list_pages` | pages and questions in a pane's site |
| `lane.start`, `lane.update`, `lane.log`, `lane.attach` | the lane an agent works in |
| `data.schemas`, `data.query`, `data.get`, `data.related`, `data.task_tree`, `data.history` | reads |
| `data.create_note`, `data.record_agent_input` | two writes that add and never overwrite |

An agent started inside a pane does not have to say where it is. `lane.start` takes its task from `MTM_TASK_ID` and is claimed by the pane in `MTM_PANE_ID`; `ui.create_page`, `ui.update_page` and `ui.ask` use the same pane when none is given. `lane.log` adds a timestamped note (`milestone`, `decision`, `blocker`, `verification` or any word) to the lane's timeline. `lane.attach` relates a document, page, run or plan to the lane.

`mtm mcp tools` lists the tools and `mtm mcp call TOOL 'JSON'` runs one.

## Moving from version 1: `mtm migrate`

```sh
mtm migrate --from ~/.local/share/matomeru/mtm.db --dry-run
mtm migrate --from ~/.local/share/matomeru/mtm.db --apply
```

The version 1 file is only read. A dry run writes nothing and prints a JSON report of what would be imported; `--apply` writes it to the version 2 store.

## Examples

Each example is one file and checks itself with `--smoke`.

| Example | Shows |
| --- | --- |
| [examples/mcp-agent](examples/mcp-agent/main.go) | An MCP client doing what an agent does: `initialize`, `tools/list`, `lane.start`, `lane.log`, `ui.create_page` with a comparison grid, `ui.ask`, `ui.list_pages`, `data.query`. It serves `mcpui.ServeStdio` in process over `io.Pipe`, or talks to a real server with `--cmd "mtm mcp serve"`. |
| [examples/extension-api](examples/extension-api/main.go) | A whole third-party extension: a small bookmark tool. It answers the probe the Extensions space reads (`--version`, `capabilities --json`) and a JSON protocol of its own (`catalog`, `query`, `open`) whose documents are Matomeru schemas, records and `ui.ViewNode` cards. |
| [examples/custom-schema](examples/custom-schema/main.go) | Registering a `link` schema in a `data.Store` and getting its Index, Show and Edit views with no view code. |

```sh
go run ./examples/mcp-agent --smoke
go run ./examples/extension-api --smoke
go run ./examples/custom-schema --smoke
```

The Extensions space reads the probe today and runs an extension's commands as runs. It does not yet call `catalog`, `query` or `open` itself: the example shows the protocol and proves that what it prints is accepted by the store and by `ui.create_page`.

## Packages

| Package | Is |
| --- | --- |
| `data` | schemas, records, relations, change sets and the SQLite store. No UI. |
| `mcpui` | the MCP tools and the stdio server |
| `views` | generators of `ui.ViewNode` trees: Index, Show, Edit, the pane site, the lane bar |
| `runs`, `ext`, `ptyhost` | commands as run records, extension discovery, PTY sessions |
| `migrate` | the version 1 importer |
| `app` | the four spaces, the overlays and the keymap, on the Ochazuke engine |
| `cmd/mtm` | the binary |

`data`, `mcpui` and `views` never import Bubble Tea; `deps_test.go` holds every package to its allowed imports.

## Checks

```sh
gofmt -l .
go vet ./...
go test -p 1 -race -count=1 ./...
go run ./cmd/mtm --smoke
```
