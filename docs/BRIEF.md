# Matomeru on ochazuke: first slice brief

Spec: `~/proj/znbu/docs/outline.md` and its concept outlines (architecture, forms, panes, agents). Reference consumer: `examples/csv` in this repo. The app is a new Go module importing `github.com/unixsuperhero/ochazuke`; the old app is at `~/proj/matomeru-old`; it and gmtm stay untouched as sources to lift from.

## The slice

One complete workflow, end to end, before any second feature: select a project and task, launch an agent into a TUIOS pane, see the launch delivered, preview the pane, attach live and detach, review the result, save the answer. This is the acceptance check the direction documents agreed on. Nothing else (Slack, GitHub, reminders, MCP) until it works.

## Nouns and verbs (data; handlers are the app's)

```js
define("project",  { verbs: { open, launch, star },                   bindings: { normal: { enter: "open", l: "launch" } } })
define("task",     { verbs: { open, launch, done, edit },             bindings: { normal: { enter: "open", l: "launch", x: "done", i: "edit" } } })
define("pane",     { verbs: { attach, preview, kill: { confirm: "Kill pane {{label}}?" } }, bindings: { normal: { enter: "attach", p: "preview", K: "kill" } } })
define("agent",    { verbs: { launch: { params: { harness: { enum: registry.names }, model: { source: "harness.models", free: true }, project: { source: "projects.list" } } } } })
define("harness",  { verbs: { list, models } })                       // data only: the registry and the model pick list
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

## Daemon

mtm talks to its own TUIOS server, `mtm-daemon` (`tuios --server mtm-daemon`, socket `<runtime dir>/servers/mtm-daemon/tuios.sock`), and starts it with `tuios --server mtm-daemon start-server` when nothing listens there (`daemon.Client.Ensure`: probe, start once, poll up to 5 s). The TUI ensures off the loop (`daemon.ensure`, a quiet job, then `panes.refresh`), so it opens at once; the CLI ensures before dispatch; `--smoke` never does. A server that cannot start is the panes page hint and the footer error (`daemon mtm-daemon not running: ...`) and the rest of the app keeps working. `-server NAME` (or `MTM_SERVER`; the flag beats the env, the env beats `mtm-daemon`) picks another server; `-server default` is the user's own daemon (`TUIOS_SOCKET` or the runtime dir), which mtm only probes and never starts. An explicit `-socket PATH` wins over both; `TUIOS_SOCKET` alone does not, so mtm run inside the user's tuios still uses `mtm-daemon`. Panes mtm launches live on that daemon and are attached through mtm, not through the user's main tuios.

## Harnesses

A harness is data, not code: `internal/harness` holds `Harness{name, command, modelFlag, models, prompt: "send-text" | "arg", ready: {state | pattern, timeoutMs}, env}`, the built-ins `claude` (`claude --model opus|sonnet|haiku`), `omp` (`omp [--model provider/id]`, any model) and `codex` (`codex [--model m]`, any model), all typed into once tuios reports them `idle,done`. `$XDG_CONFIG_HOME/matomeru/harnesses.json` (else `~/.config/matomeru/harnesses.json`, or `-harnesses PATH`) is a JSON array merged by name over the built-ins: an entry naming a built-in changes only the fields it gives (`{"name": "omp", "command": ["omp-dev"]}`), any other adds a harness, and a bad entry (no name, empty command, unknown prompt mode, unknown field) stops startup with the file and the reason. Every launch is one flow (`harness.Launcher.Launch` over a three-call `Driver`): `LaunchWorkspace`, `new-window` running the harness's argv (`env K=V` first when it sets env, the model flag when a model is given, the prompt last for `prompt: "arg"`) in the project's tree, named from the task; then, when there is a prompt, `wait-for` the readiness probe (or a fixed delay when it has none) and `send-text` with submit. A pane that is not ready in time keeps its prompt unsent and says so in the panes header. `pane.new` is the same flow with no harness and no prompt. `start-agent` is not used: it would be a second path, for the harnesses tuios knows, beside one that runs any argv. The harness of a launch is its `harness` argument, else the project's `settings.harness`, else `claude`; the model is checked against that harness's models (free text when it lists none). The review row and the launch error name the harness; `mtm harness list` prints the registry. This is the last time harness code is written in the app: a new harness is a JSON entry, and the package imports nothing from the app, so it can move to its own repository as is.

## Off-loop work (the delivery contract)

- `agent.launch` → `Pending(work)`, accumulate; its completion refreshes the pane list and shows up in `jobs.list` (the status rail renders that list).
- `pane.preview` → `Pending(work).Replacing("preview")`; a newer preview cancels the older.
- Attach: `AddTerminal(id, Parent(panesPage), stream)` with the daemon's `stream-pane`; the terminal noun's leader contract handles detach. Size lease released on `Close`.
- Everything else runs on the loop and returns `Done`.

## A launch ends with the pane on screen (owner rule)

`agent.launch` and `pane.new` are not done when the job completes. The completion handler opens the panes page, refreshes the list, focuses the new pane's row, and attaches (`pane.attach`); preview only if attach is undesirable. Test: after launch, `Runtime.Page == "panes"`, focus is the new row, a terminal instance exists. The header lists every page tab with the active one marked, so page switching is discoverable.

## Session inference (znbu requirement)

Never show a session picker. `project → session name` is a pure function in the app; the destination is shown in the launch confirm prompt (`Launch into {{session}}?` via `{{<arg>}}`). Launch never asks for a project either: it infers one (the task under the cursor, the project row under the cursor, the project whose tree holds the cwd, the last launched) and, when nothing is inferred, goes to a fixed session with no project: an agent launch into `no-project` (`Launch into no-project?`), a plain shell (`pane.new`) into `mtm-only` (`New pane in mtm-only?`), both started in the home directory. The names are fixed so the owner can type them by hand. A launch with no project does not change the last launched project, and its review row shows `(no project)` and the pane `no-project/<pane>`.

## CLI extensions

`mtm` is an oden app (`github.com/unixsuperhero/oden`, at ~/proj/oden; a local `replace` in go.mod until it is tagged). Any executable `mtm-<sub>` on PATH is the command `mtm <sub>`, run with the remaining words, mtm's stdio and `ODEN_PARENT`/`ODEN_ROOT` set, so a third party adds `mtm web` by installing `mtm-web` without touching this repo. After the global flags the first word resolves by exact name, alias or unique prefix to: oden's builtins (`mtm help`/`-h`/`--help` lists every command with kind, source and shadowed claims; `mtm commands` prints the catalog as JSON; `mtm which WORD` prints what a word resolves to), then the runtime's nouns (`mtm ta launch` is `task launch`; the shell also takes a unique verb prefix, `mtm bind li`), then `mtm-<sub>` bins; no word runs the TUI, an unknown word reaches the NOUN VERB shell, an ambiguous prefix lists its candidates and exits 1. A noun always wins over a same-named bin, which help shows as `shadows external PATH`. Nesting follows the name: `mtm-ai` built on oden resolves `mtm-ai-skills`, so `mtm ai skills` runs `mtm-ai skills` runs `mtm-ai-skills`; a descendant never makes a prefix ambiguous (`mtm a` is `ai`, not `ai` or `ai-skills`). The adapter is `cmd/mtm/oden.go`.

`mtm-proj` (`cmd/mtm-proj`) is the first external command, so `mtm proj ...` and `mtm-proj ...` are the same. Making a project always goes through `mtm proj new NAME [--root DIR] [--template T] [--existing] [--no-register] [--github] [--private|--public]`: it creates `~/proj/NAME`, runs `git init` there and verifies `git rev-parse --show-toplevel` is the directory itself before anything is committed (the home directory is a git repository, so a directory without its own `git init` commits into it silently), copies a template if one is named, writes `AGENTS.md` and `.gitignore` where absent, makes the commit `chore: new project NAME`, optionally creates and pushes the GitHub repository, registers the project with its tree in mtm's store (`-data`, mtm's default) and prints `cd DIR` last. Templates are directories or `.tar.gz` files under `$XDG_CONFIG_HOME/matomeru/templates/` (else `~/.config/matomeru/templates/`), or a path; paths may use `{{.Name}}`, `*.tmpl` files render with Go's text/template (data `Name Dir Root Date User Module`) and lose the suffix, and an entry escaping the directory is refused; `mtm proj templates` lists the catalog. The guard is `mtm proj check [DIR]`: it prints the git toplevel and fails when that is `$HOME` or a repository at or above the project root; agents run it before their first commit in any new directory. `mtm proj list` prints the registered projects and trees as JSON. No daemon, no TUI.

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
| 6. CLI shell: `bindings list`, `history list`, `task launch --target` | c88549e |
| Follow-up pass | item 5 (panes use ReplacingAt and CloseOnDetach) 3f4b260; item 4 (`pane.new`) e2af2fe; item 1 (a launch ends with the pane on screen) c5b5a58; item 2 (terminal and preview borders) 588b1c8; item 3 (page tabs: the framework's header already lists them and marks the active one, now tested) this commit |

CLI (`cmd/mtm/cli.go`): `mtm [--data P] [--server NAME|default] [--socket P] NOUN VERB [--target ID] [--json '{...}'] [--ARG VALUE] [--yes]` runs the operation in-process through `Runtime.HandleCall` (history source `cli`), settles its jobs, prints JSON. A `--target` no list shows is revealed by the app (`App.reveal`: a task id opens its project; anything else reloads panes). Without `--target` the shell picks the owner: the instance the app declares for a noun with none of its own (`App.ownedBy`: `panes` and `daemon` → `panes/list`, so `mtm panes refresh` runs), else the noun's only instance; with several (`mtm pane preview`) a failing command lists them as `--target` candidates. A confirm takes `--yes`, else reads y/n from stdin (EOF declines, non-zero exit). Any other modal is an error. The CLI has no screen: a terminal a command adds (a launch lands attached) is removed before the shell runs more work, which releases its stream and lets the process exit. History is in memory, per invocation.

Known gaps:

- Framework: `execute` (runtime.go) rejects a target that is not an instance and has no resolver hook (the app's `reveal` covers it); there is no importable CLI entry (only `cmd/ochazuke`, a socket client), so the in-process shell lives in the app; `bindings.list` is focus-scoped, so an app-wide listing is one `--target` per stop; history is not persisted.
- App: there is no "review this pane" verb; a job's rail label is its verb (the framework's `JobInfo` has no label), so the harness shows in the launch error and the review row, not in the rail.
