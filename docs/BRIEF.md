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

## Off-loop work (the delivery contract)

- `agent.launch` → `Pending(work)`, accumulate; its completion refreshes the pane list and shows up in `jobs.list` (the status rail renders that list).
- `pane.preview` → `Pending(work).Replacing("preview")`; a newer preview cancels the older.
- Attach: `AddTerminal(id, Parent(panesPage), stream)` with the daemon's `stream-pane`; the terminal noun's leader contract handles detach. Size lease released on `Close`.
- Everything else runs on the loop and returns `Done`.

## Session inference (znbu requirement)

Never show a session picker. `project → session name` is a pure function in the app; the destination is shown in the launch confirm prompt (`Launch into session {{session}}?` via `{{<arg>}}`).

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
