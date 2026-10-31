# mtm as an MCP server

`mtm mcp serve` is the family's MCP gateway. It serves ochazuke's catalog
protocol (JSON-RPC 2.0 on stdio) over mtm's headless runtime. Its four
primary tools are `catalog`, `describe`, `run` and `state`: discover a verb,
read its parameters and right, then run it through the same checks as the CLI.

The existing harness registration still starts `mtm mcp serve` with the
selected database, daemon and config paths. `mtm mcp tools` lists its tools,
`mtm mcp instructions` prints the startup guidance, `mtm mcp whoami` resolves
the startup pane, and `mtm mcp config [HARNESS]` prints configuration without
writing a harness file.

The word `mcp` is always this gateway, even with an extension registered as
`mcp` (the family's `o-mcp`): an extension's namespace yields its word to
mtm's own command (ochazuke v0.75.0's `cli.Mount`), so `mtm mcp serve` keeps
working and help lists `shadows alias mcp of mcp. namespace` under it. The
extension's nouns stay reachable by their full names (`mtm mcp.mcp-call list`,
read as `mtm mcp. mcp-call list`) and by their bare names when unique.

## How a tool runs

One headless runtime serves the connection, so history and exact undo entry
ids survive calls. Each run reads the pane's current assignment and stamps
writes with the bound agent actor. It does not attach to the owner's TUI.
`state` describes this headless runtime; it has no owner's marks or live
confirmation dialogs. Successful changes return their history entry and an
undo call when their store supports undo. Undo refuses another actor's entry
and refuses to overwrite changes made since that entry.

Mounted extensions are included by default (`mcp.include-extensions: true`).
Their nouns, verbs and declared rights come from their catalogs. Their own
words are quoted as data in the generated guidance, subject to its budget.
Setting this option off hides extension verbs from MCP discovery and their
text from instructions; the `agents.rights` setting controls permission.

The eight legacy tool names remain listed for one release and answer as
alias rows over `run`, preserving their argument schemas:

| Tool | Verb | Argument mapping |
| --- | --- | --- |
| `whoami` | `pane.whoami` | unchanged |
| `task_list` | `task.list` | unchanged (`scope` and `status`) |
| `task_done` | `task.done` | `task_id` → `id` |
| `task_status` | `task.status` | `task_id` → `id`, `status` → `value` |
| `task_note_add` | `note.new` | unchanged (`owner` object and `text`) |
| `context_add` | `context-item.add` | unchanged; `task_id` remains ignored |
| `document_report` | `context-item.report` | unchanged |
| `report_progress` | `pane.progress` | unchanged |

Alias calls return the catalog `run` envelope (`status`, `result`, messages,
history entry and optional undo), so clients reading structured results
should read `result` for the domain value. The stored effects are the same.

Lane filtering belongs to `task.list` for agent callers (`scope: all` widens
it); closing notes, evidence and optimistic version checking belong to
`task.set`, so `task.done` and `task.status` keep their standard-set aliases.
`task.status` requires a summary to set done. Evidence notes are append-only
records and remain when the task status is undone. `context-item.report`
keeps an existing markdown or HTML file as context and appends its report
note. Unrecognised context text is kept as a prompt rather than lost, following
mtm's record-first rule. None of these behaviors depends on which MCP tool
name was used.

## What agents are told

`mtm mcp instructions` prints ochazuke's generated guidance followed by mtm's
own About and agent rules (`mtmAbout` and `mtmAgentRules` in
`cmd/mtm/agent_declarations.go`, pinned by
`cmd/mtm/testdata/mcp-instructions.golden`; ochazuke caps each at 60 tokens).
They say:

- mtm is the only MCP server an agent needs. The mounted family tools are
  reached through this gateway's `run`, not their own servers: o-lane
  (`lane.lane.define`, `lane.lane.state`, `lane.lane.associate`), o-questions
  (`questions.question.ask`, `answer`, `follow-up`), o-cmds (`cmds.cmd.add`),
  o-md (`md.doc.new`) and o-git.
- Track work as lanes. Every lane belongs to an mtm project; when none fits,
  the agent creates one with `project.new` (declared `allow`). Set the lane's
  state as the work moves and associate the lane with the agent's pane.
- mtm tasks are the owner's. An agent changes a task's status only when
  asked, and never its title, project or parent.
- Findings, reviews and reports go in a Markdown document: `md.doc.new` under
  the project's `docs/` or a docs root, the body written, then
  `document_report` (`context-item.report`) to link it. Notes are for short
  jots and closing lines.

The text is data quoted into the guidance; it grants no rights. The record
policy below still decides every write.

## Who the writes are

Every tool call writes as the agent of the server's pane: its harness,
pane, session, the pane's task (its lane) and model. A task it creates is
stamped `created-by` that agent (`made-by` agent), and every write it makes
is `changed-by` it. What it may change is data, the `agents.*` settings: by default the tasks
its pane or lane made, the agent-made tasks of its lane task's subtree
(`agents.write-scope` subtree, `agents.write-makers` agent) wholly, and the
status only of the owner's (or unknown) tasks in that subtree
(`agents.owner-task-props` status), so `task_done` and `task_status` work
on the lane task the owner made; and its own pane and the panes it launched
or opened beside it (`agents.associate-scope` own-pane). Any other write
(the owner's wording, a move, a delete, a task outside the lane) is
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

The actor is bound from the startup pane and harness, even when
`MTM_ACTOR=owner` is present. A run cannot replace it with tool arguments.
An agent cannot give another session or window to pane progress or context
capture. A missing task must be named for a task update; findings with no
known owner are saved unassociated.

## Errors

Unknown tool names and schema errors answer JSON-RPC error -32602. A failed
or refused operation returns `isError: true` with its reason and structured
`status`. Destructive verbs require the owner's confirmation; headless calls
refuse and include the owner's CLI command. Owner-only verbs are declared
`never`; record policy failures name who to ask. No refusal silently succeeds.

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
