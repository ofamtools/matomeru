# mtm as an MCP server

`mtm mcp serve` lets an agent running in a TUIOS pane drive mtm over the
Model Context Protocol (JSON-RPC 2.0 on stdio). It is ported from
matomeru-old's `internal/mcp`; the protocol and the tool names and argument
schemas are unchanged where the new mtm has the feature.

```sh
mtm mcp serve            # stdio; respects --data, --server, --socket, --config
mtm mcp tools            # the tools, with the operations each runs
mtm mcp whoami           # what the whoami tool answers for this pane
mtm mcp config [HARNESS] # the entry a harness needs; writes nothing
```

## How a tool runs

Each tool call runs one or more mtm operations as `mtm NOUN VERB --json
'{...}'` would: a fresh app opened from the serve process's command line,
its CLI shell in process, the shell's JSON output read back. No state
carries between calls, as between two CLI commands.

| Tool              | Operations                                     |
| ----------------- | ---------------------------------------------- |
| `whoami`          | none: the store's pane assignment and recipe   |
| `task_list`       | `task list --all`, filtered to the pane's task subtree or project (`scope: pin`, the default) |
| `task_done`       | `task done`, then `note new --task` for `note` |
| `task_status`     | `task list --all` (the `expected_version` check), `task status`, `note new --task` with the evidence |
| `task_note_add`   | `note new --task` or `--project`               |
| `context_add`     | `context-item add`                             |
| `document_report` | `context-item add --kind file`, `note new` naming the document |
| `report_progress` | `pane progress` (the daemon's `set-agent-state`, state working) |

`context-item add` and `pane progress` were added for these tools and are
ordinary CLI verbs too.

`document_report` keeps the document as a file context item of the pane's
task, with a note giving its kind, title, path and summary. The Review page
reviews the screens of launched agents, not documents, so nothing lands
there.

`task_status` takes the new statuses only: todo, active, blocked, done.

## Who the writes are

Every tool call writes as the agent of the server's pane: its harness,
pane, session, the pane's task (its lane) and model. A task it creates is
stamped `created-by` that agent (`made-by` agent), and every write it makes
is `changed-by` it. What it may change is data, the `agents.*` settings:
by default the tasks its pane or lane made, plus the agent-made tasks of its
lane task's subtree (`agents.write-scope` subtree, `agents.write-makers`
agent), and its own pane and the panes it launched or opened beside it
(`agents.associate-scope` own-pane). A tool on a task the owner made is
refused with a message naming who to ask (the owner or the coordinator)
and the setting that refused it. The CLI follows the same rule inside an
agent's pane (`MTM_ACTOR=owner` overrides it); the TUI is always the owner,
and the owner is never refused.

## Which pane am I

The pane comes from the server's environment, never from tool arguments:
`TUIOS_SESSION` and `TUIOS_PANE_ID`, which TUIOS sets in every pane and the
harness passes to the MCP servers it starts (`MTM_SESSION` and `MTM_WINDOW`
override them). Each call resolves the pane again:

- task and project: the pane's assignment, else the owner of its session
  (looked up by name), the rule the history journal uses;
- cwd, harness and model: the recipe mtm launched the pane from; a pane mtm
  did not launch has no recipe, and its cwd is the server's.

Tools that act for the pane (`task_*` but `task_list`, `context_add`,
`document_report`, `report_progress`) refuse with `no_pane` outside one, and
default their task to the pane's.

## Errors

An unknown tool, or arguments that do not match the tool's schema (a
missing required argument, an unknown argument, a wrong type, a value
outside an enum), answers JSON-RPC error -32602. A failing operation is a
tool result with `isError: true` and the text `CODE: message`.

## Harness config

`mtm mcp config claude` prints the entry for this binary (`os.Executable`)
with this invocation's `--data` and `--server` (or `--socket`), the file it
belongs in (`~/.claude.json`, user scope) and the `--mcp-config` argument
that passes it for one run instead. codex, omp and opencode are known too.
mtm writes no harness config; run `config` from the installed binary, not
from `go run`.

## Two processes on one database

The store opens SQLite in WAL mode with a 5 s busy timeout and takes the
write lock at the start of every write transaction, so `mtm mcp serve`,
the TUI and CLI commands can write the same database at once, as two CLI
commands can today. A running TUI does not watch the database: it shows a
tool's writes after its next reload of that list, as with a CLI write.

## Not ported

These old tools need features the new mtm does not have:

| Old tool                                   | Needs |
| ------------------------------------------ | ----- |
| `project_home_add`, `project_home_search`  | a project home page: answers and artifacts per project, with primary, support and byproduct levels, and a search over them, context and tasks |
| `action_item_create`                       | action items (B-15): a table, a page and verbs |
| `queue_add`, `queue_list`, `queue_claim`, `queue_finish`, `queue_release` | a work queue (B-39): items, batches by repository, claim, finish and release by a pane, the `queue.batch_size` setting |
| `todo_list`, `todo_note_add`, `todo_claim` | todo items (B-13) as rows with notes and claims; the `todos` noun only opens the outline |

matomeru-old's `mtm mcp install` (writing harness configs) and its automatic
wiring of launched agents are not ported either: adding the entry is the
owner's decision.
