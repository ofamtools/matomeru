# Matomeru on ochazuke: first slice brief

Spec: `~/proj/znbu/docs/outline.md` and its concept outlines (architecture, forms, panes, agents). Reference consumer: `examples/csv` in this repo. The app is a new Go module importing `github.com/unixsuperhero/ochazuke`; the old app is at `~/proj/matomeru-old`; it and gmtm stay untouched as sources to lift from.

## The slice

One complete workflow, end to end, before any second feature: select a project and task, launch an agent into a TUIOS pane, see the launch delivered, preview the pane, attach live and detach, review the result, save the answer. This is the acceptance check the direction documents agreed on. Nothing else (Slack, GitHub, reminders, MCP) until it works.

## Nouns and verbs (data; handlers are the app's)

```js
define("project",  { verbs: { open, launch, star },                   bindings: { normal: { enter: "open", l: "launch" } } })
define("task",     { verbs: { open, launch, done, edit },             bindings: { normal: { enter: "open", l: "launch", x: "done", i: "edit" } } })
define("pane",     { verbs: { attach, preview, kill: { confirm: "Kill pane {{label}}?" } }, bindings: { normal: { enter: "attach", p: "preview", K: "kill" } } })
define("agent",    { verbs: { launch: { params: { model: { enum: [...] }, project: { source: "projects.list" } } } } })
define("review",   { verbs: { open, accept, reject, saveAnswer },      bindings: { normal: { enter: "open", a: "accept", r: "reject", s: "saveAnswer" } } })
define("prompt",   framework noun: Readline, insert pinned)
define("terminal", framework noun: passthrough, ctrl+a leader, d detaches)
```

Pages (instances of kind page under app): `work` (projects list → tasks list, two stops), `panes` (pane list → terminal detail, two stops), `review` (reviews list → answer prompt). `}`/`{` move between stops; `ctrl+j`/`ctrl+k` between pages.

## State the app owns

```js
App = { store: sqlite, daemon: tuiosClient, projects, tasks, panes, reviews, ui: ochazuke.Runtime }
```

Store and daemon client are lifted from `matomeru-old/internal/store` and `internal/daemon` (971 lines) or `gmtm/internal/tuios` (1,551 lines, includes the protocol notes). Pick one, do not write a third.

Decision (step 1): matomeru-old. Its store is the one the existing `~/.local/share/matomeru/mtm.db` was written with, so real projects and tasks open unchanged; its daemon client already has `start-agent`, `capture-pane`, `LaunchWorkspace` and the stream-pane lease frames that steps 3 and 4 need; and it is the smaller of the two. gmtm's client is Bubble Tea v1 era, has no store, and its pooled-connection interface would have been a third design to adapt. The lift is verbatim except for inlining the two things it took from the old `core` package (`Event`, `newID`) into `internal/daemon/event.go`. The row shapes live in `internal/work`.

## Off-loop work (the delivery contract)

- `agent.launch` → `Pending(work)`, accumulate; its completion refreshes the pane list and shows up in `jobs.list` (the status rail renders that list).
- `pane.preview` → `Pending(work).Replacing("preview")`; a newer preview cancels the older.
- Attach: `AddTerminal(id, Parent(panesPage), stream)` with the daemon's `stream-pane`; the terminal noun's leader contract handles detach. Size lease released on `Close`.
- Everything else runs on the loop and returns `Done`.

## Session inference (znbu requirement)

Never show a session picker. `project → session name` is a pure function in the app; the destination is shown in the launch confirm prompt (`Launch into {{session}}?` via `{{<arg>}}`). Launch never asks for a project either: it infers one (the task under the cursor, the project row under the cursor, the project whose tree holds the cwd, the last launched) and opens the palette on `agent into` only when nothing is inferred.

## Order of work

use 1 subagent per step, do sequentially (in serial one after another)
use opus 5.5 unless u need fable then u can use fable 5.1

1. Module skeleton, store and daemon client lifted, `--smoke` renders the work page with real projects. Commit.
2. Projects and tasks lists, open and edit through prompts. Commit.
3. Launch with the confirm prompt and the job in the status rail. Commit.
4. Panes page: list, preview (replacing slot), attach and detach. Commit.
5. Review page and save answer. Commit.
6. `bindings list`, `history.list`, CLI `mtm task launch --target t12` all work, which proves the shell is generic.

Each step: `go test -race ./...` green, ValidateBindings in a test, no key-name switches, no verb-specific branches in the shell (the matomeru post-mortem's failure mode).

COMMIT and merge AFTER EVERY STEP
no need to use separate worktrees, use main

## Status

All six steps are done on main:

| Step | Commits |
|---|---|
| 1. Skeleton, store and daemon lifted, `--smoke` | 33f7547, 82f10da |
| 2. Projects and tasks, open/edit through prompts | 33c854d |
| 3. Launch with confirm, job in the status rail | f31d289, 3e15cb1 |
| 4. Panes: list, preview, attach/detach | bf0155b |
| 5. Review page, save answer | d0af710 |
| 6. CLI shell: `bindings list`, `history list`, `task launch --target` | the commit that adds this section |

CLI (`cmd/mtm/cli.go`): `mtm [--data P] [--socket P] NOUN VERB [--target ID] [--json '{...}'] [--ARG VALUE] [--yes]` runs the operation in-process through `Runtime.HandleCall` (history source `cli`), settles its jobs, prints JSON. A `--target` no list shows is revealed by the app (`App.reveal`: a task id opens its project; anything else reloads panes). A confirm takes `--yes`, else reads y/n from stdin (EOF declines, non-zero exit). Any other modal is an error. History is in memory, per invocation.

Known gaps:

- Framework: Pending slots are per owner (ReplacingAt only scopes them); `terminal.detach` only moves focus; `Terminal.Close` does not notify the stream; `{{label}}` ignores arguments other than `label`; `execute` (runtime.go) rejects a target that is not an instance and has no resolver hook (the app's `reveal` covers it); there is no importable CLI entry (only `cmd/ochazuke`, a socket client), so the in-process shell lives in the app; `bindings.list` is focus-scoped, so an app-wide listing is one `--target` per stop; history is not persisted.
- App: the harness is always claude; the fallback launch (`agent.into`) creates the project before the confirm; there is no "review this pane" verb.
