# Matomeru on ochazuke: first slice brief

Spec: `~/proj/znbu/docs/outline.md` and its concept outlines (architecture, forms, panes, agents). Reference consumer: `examples/csv` in this repo. The app is a new Go module importing `github.com/unixsuperhero/ochazuke`; the old app is at `~/proj/matomeru-old`; it and gmtm stay untouched as sources to lift from.

## The slice

One complete workflow, end to end, before any second feature: select a project and task, launch an agent into a TUIOS pane, see the launch delivered, preview the pane, attach live and detach, review the result, save the answer. This is the acceptance check the direction documents agreed on. Nothing else (Slack, GitHub, reminders, MCP) until it works.

## Nouns and verbs (data; handlers are the app's)

```js
define("project",  { verbs: { new, open, launch },                  bindings: { normal: { enter: "open", l: "launch" } } })
define("task",     { verbs: { list, new, open, launch, done, edit }, bindings: { normal: { enter: "open", l: "launch", x: "done", i: "edit" } } })
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

`agent.launch` and `pane.new` normally finish on the panes page with the new pane attached. An explicit launch outside the pin attaches with `outside pin: PROJECT`, without widening the list or changing the pin. If the pin changes while a launch is running, completion records its review and only notifies; it does not navigate. Ownership is captured before the job and saved immediately after creation, before readiness waits or listing. A failed listing retains the created pane; a failed ownership write reports the failure and leaves the pane available for reassignment.

## Project pin and launch ownership

`project pin ID` persists a project-only pin; `project unpin` persists an empty pin. `P` opens the pin picker from every page in normal mode. The title shows `Matomeru · PROJECT`. `S` (`scope all`) widens the current process's lists without changing the pin. `task list`, `pane list`, `session list`, and `review list` accept `--all`.

Generic `agent launch` and `pane new` use explicit task/project arguments, then the pin, never a merely browsed project, cwd, or last launch. `project launch`, `task launch`, and explicit CLI `--target` actions use the addressed record. Task ownership wins conflicting project arguments. Other record creation continues through `resolveOwner`: explicit arguments, selected record, pin, unassociated. Unknown associations remain unassociated with a notice.

`project → session name` remains a pure slug function; there is no session picker. `--scratch` overrides all ownership and confirms `no-project` for agents or `mtm-only` for shells. Unassociated launches start in home. A project's missing default directory also falls back to home with a notice. Every launch and attach records directory recency.

Pane ownership lives in the existing `assignments` table keyed by window id. Session ownership lives in `session_owners` keyed by stable session id, not its display name. A pane without an assignment inherits its session; an empty assignment explicitly remains unassociated. `pane associate --target pane:SESSION/WINDOW --project ID`, `session associate --target session:ID --project ID`, and `task associate --target ID --project ID` reassign records; `--scratch` removes ownership. Reviews keep launch-time ownership and show the pane's current owner in their detail.

On first open, existing assignments are preserved and missing pane assignments are imported from reviews once. The old `active/singleton` task pin becomes its project; otherwise legacy `launch_state/last` seeds the pin once. Subsequent explicit unpinning never reseeds it. The old last-launch API and cwd ownership inference are removed.

## CLI extensions

`mtm` is an oden app (`github.com/unixsuperhero/oden`, at ~/proj/oden; a local `replace` in go.mod until it is tagged). Any executable `mtm-<sub>` on PATH is the command `mtm <sub>`, run with the remaining words, mtm's stdio and `ODEN_PARENT`/`ODEN_ROOT` set, so a third party adds `mtm web` by installing `mtm-web` without touching this repo. After the global flags the first word resolves by exact name, alias or unique prefix to: oden's builtins (`mtm help`/`-h`/`--help` lists every command with kind, source and shadowed claims; `mtm commands` prints the catalog as JSON; `mtm which WORD` prints what a word resolves to), then the runtime's nouns (`mtm ta launch` is `task launch`; the shell also takes a unique verb prefix, `mtm bind li`), then `mtm-<sub>` bins; no word runs the TUI, an unknown word reaches the NOUN VERB shell, an ambiguous prefix lists its candidates and exits 1. A noun always wins over a same-named bin, which help shows as `shadows external PATH`. After its own `mtm-<sub>` bins mtm also discovers the family's `o-<sub>` tools (`oden.WithPrefixes("o")`): `mtm proj` runs `o-proj` unless an `mtm-proj` is on PATH, which then wins and lists `o-proj` as shadowed. mtm has no bare-name lookup. Nesting follows the name: `mtm-ai` built on oden resolves `mtm-ai-skills`, so `mtm ai skills` runs `mtm-ai skills` runs `mtm-ai-skills`; a descendant never makes a prefix ambiguous (`mtm a` is `ai`, not `ai` or `ai-skills`). The adapter is `cmd/mtm/oden.go`.

`o-proj` (`cmd/o-proj`) is a bare data tool of the `o` family (the root bin is `o`, `~/proj/oden/cmd/o`): `o-proj ...`, `o proj ...` and `mtm proj ...` are the same command, found by the normal `<bin>-<sub>` rule (mtm through its `o` prefix), and its messages name it as invoked (`o-proj`, `o proj`, `mtm proj`, from `ODEN_PARENT`). Install: `go build -o ~/bin/o-proj ./cmd/o-proj` and `go build -o ~/bin/o ../oden/cmd/o`. Making a project always goes through `o proj new NAME [--root DIR] [--template T] [--existing] [--no-register] [--github] [--private|--public] [--license mit|none] [--hub DIR|none]`: it creates `~/proj/NAME`, runs `git init` there and verifies `git rev-parse --show-toplevel` is the directory itself before anything is committed (the home directory is a git repository, so a directory without its own `git init` commits into it silently), copies a template if one is named, writes `AGENTS.md`, `.gitignore` and an MIT `LICENSE` (the current year, `git config user.name`, else `$USER`; `--license none` skips it) where absent, makes the commit `chore: new project NAME`, optionally creates and pushes the GitHub repository, registers the project with its tree in mtm's store (`-data`, mtm's default), links it from the hub (`link: ~/hub/o/NAME -> DIR`; `--hub DIR`, else `$O_PROJ_HUB`, else `~/hub/o`, created if missing; `none` skips) and prints `cd DIR` last. Hub links are absolute, so `readlink` prints the tree the store holds and a link works wherever the hub is reached from; a path already there that is not a link to DIR is never replaced (`keep: ~/hub/o/NAME exists -> OTHER`). `o proj link NAME` or `o proj link --all` (same `--hub`) backfills the links from the store, one line per project, then `linked N, kept M, missing K, skipped S (not made by o-proj)`, where missing is a registered tree no longer on disk (reported, not linked). Provenance: `new` (and `--existing`) records `origin: o-proj` on the project row (`o proj adopt NAME` records it for a project o-proj registered earlier, touching only the row), `link --all` links only those while `link NAME` links any registered project, and `o proj unlink NAME | --all` removes the hub link of NAME or of every project not made by o-proj, only when it is a symlink to that project's own tree (anything else prints `keep:`; hub entries naming no registered project are never touched). Templates are directories or `.tar.gz` files under `$XDG_CONFIG_HOME/matomeru/templates/` (else `~/.config/matomeru/templates/`), or a path; paths may use `{{.Name}}`, `*.tmpl` files render with Go's text/template (data `Name Dir Root Date User Module`) and lose the suffix, and an entry escaping the directory is refused; `o proj templates` lists the catalog. The guard is `o proj check [DIR]`: it prints the git toplevel and fails when that is `$HOME` or a repository at or above the project root; agents run it before their first commit in any new directory. `o proj list` prints the registered projects and trees as JSON. The same work is the ochazuke noun `project` (`cmd/o-proj/shell.go`, mounted with `ochazuke/cli.Mount`): `o-proj project list|new|check|templates|open|link|unlink` print JSON (`project new NAME` takes NAME positionally and still prints the step lines first), `project list` rows are `{id, label: name, data: {trees}}` (declared `Rows("project")`), `project open --target ID` prints that project's trees, `project link --name NAME|--all [--hub DIR]` prints the link lines then `{linked, kept, missing, skipped}` (`project unlink` likewise, `{unlinked, kept}`) (NAME is a flag there: ochazuke fills positional words in required-then-alphabetical order, so a bare word would land in `all`), and `o-proj catalog` answers schema 1, so nabe can register o-proj as an extension. The seven inline spellings stay inline commands with their own output. No daemon, no TUI.

`o proj new NAME --family` (short form `-o`) explicitly requests the o/oden family symlink, `~/hub/o/NAME -> ~/proj/NAME`. Family linking remains enabled by default; `--family=false` (or `-o=false`) skips it. `--hub DIR` still selects another hub and `--hub none` still skips linking, even with `--family`. The noun spelling `o proj project new NAME --family` exposes the same boolean in its catalog; JSON callers can pass `"family": false` to skip.

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

CLI (`ochazuke/cli`, configured in `cmd/mtm/cli.go`): `mtm [--data P] [--server NAME|default] [--socket P] NOUN VERB [--target ID] [--json '{...}'] [--ARG VALUE] [--yes]` runs the operation in-process through `Runtime.HandleCall` (history source `cli`), settles its jobs, prints JSON. The shell, the oden mount of the nouns (`cli.Mount`, `cmd/mtm/oden.go`) and `mtm catalog` (the runtime as catalog JSON) are the framework's `cli` package; mtm passes its bin name, usage line and the hooks below. A `--target` no list shows is revealed by the app (`App.reveal`: a task id opens its project; anything else reloads panes). Without `--target` the shell picks the owner: the instance the app declares for a noun with none of its own (`App.ownedBy`: `panes` and `daemon` → `panes/list`, so `mtm panes refresh` runs), else the noun's only instance; with several (`mtm pane preview`) a failing command lists them as `--target` candidates. A confirm takes `--yes`, else reads y/n from stdin (EOF declines, non-zero exit). Any other modal is an error. The CLI has no screen: a terminal a command adds (a launch lands attached) is removed before the shell runs more work, which releases its stream and lets the process exit. History is in memory, per invocation.

The [build plan's Step 0 handoff](plans/build-plan.md#step-0-implementation-and-handoff) supersedes the original task-prompt behavior. `project new ""`, `task new ""`, and `task edit --target ID --title ""` save empty values. Omitting the name/title opens a right-panel draft editor instead. `a` on the projects stop creates a project; `a` on the tasks stop creates a task. The `no project (unassociated)` project-list row opens unassociated tasks, and `--target` can reveal those tasks. `task list` includes unassociated records. Empty project names and task titles display as `untitled`.

Project creation and project/task editing autosave drafts under the database's sibling `drafts/` directory. Project fields are name, theme color, glyph, harness, and preset. Task fields are title and status. Enter saves; Escape leaves insert mode, where normal-mode `s` saves and `q` cancels. Normal-mode focus navigation moves between fields. Moving between stops or pages does not prompt. Reopening the editor after an mtm restart restores every field. Saving an existing record preserves its directories, ownership, and other metadata. Selecting a task shows its detail; `i` opens its editor.

`project directory PATH --target ID` attaches a directory without creating it. Omitting `PATH` opens the cached known-directory chooser. The chooser merges all project trees, TUIOS directories, and repositories under the configured project base directories. Its search field applies fuzzy filtering without rescanning. `browse…` opens ochazuke's path picker; `ctrl+y` selects the current directory, including an empty directory. After the picker returns, Enter attaches the path. A typed missing path also attaches and appears as missing in project detail.

`project default PATH --target ID` sets an attached directory as the default. The first attached directory is the initial default. Paths can belong to several projects; project detail names the other projects. `internal/work.Store.AddTree` and `SetDefaultTree` own these changes, and `o-proj` registration uses `AddTree`.

Project bases are read from `$XDG_CONFIG_HOME/matomeru/dirs.json`, or `~/.config/matomeru/dirs.json`. `MTM_PROJECT_BASE_DIRS` overrides the file. `dirs bases` reports the effective sources; `dirs base.add PATH --depth N` and `dirs base.remove PATH` update the file. The default depth is one. Repository discovery follows symlinks and deduplicates real paths. Missing bases appear in the chooser footer. Recency comes from `dir_recent`, with TUIOS activity imported only for paths without recorded usage.

Notes have their own Notes page, reached with the framework page navigation. `a` creates a draft, Enter views the saved body, and `i` edits. The editor is lane B's shared right-panel editor on Work: note text is multiline, Enter inserts a newline, Escape leaves insert mode, and normal-mode `s` saves or `q` cancels. Whitespace and empty bodies are retained. Moving away keeps the draft; reopening with `i` or opening a new draft row restores it after restart.

CLI: `note new TEXT [--task ID] [--project ID]`, `note list [--all]`, `note view --target ID`, `note edit TEXT --target ID`, and `note associate --target ID [--task ID|--project ID|--scratch]`. Omitted text opens the shared editor; an explicit `""` saves empty. New notes capture `resolveOwner` once; task ownership wins over a conflicting project, and CLI creation never reads TUI selection. The list and saved preview use `inScope`, including task-only legacy notes whose project is inherited from their task. Association changes ownership without changing the saved body or losing an unfinished draft.

`internal/work/notes.go` declares no migration: the existing notes table keeps owner `notes`, key `id`, version 1. Reads ignore unknown legacy fields; saving preserves them, including provenance. Project and task details now list their scoped notes.

Integration is complete. Pin changes clear out-of-scope editors, details, directory paths, and pane previews. Unfinished edits remain on disk. Preview and review completions check the pin version before publishing output. A review cannot capture a pane whose current owner is outside the pin. The project picker and known-directory chooser remain global by design; project details and task lists do not expose another project's contents while scoped.

`project delete --target ID` and `task delete --target ID`, or normal-mode `D` on their rows, show a confirmation with separate counts for affected tasks, notes, pane owners, session owners, and reviews. `--yes` accepts through the framework CLI. Deletion preserves child records and unknown JSON fields, clears the deleted owner, and records `former_project_id` or `former_task_id`. Deleting a task preserves its children's effective project. Deleting the pinned project clears the pin. Related drafts lose the deleted owner without losing their text; the deleted record's own draft is removed. Neither directories nor live panes are deleted.

CLI preparation opens neutral Work focus before dispatch. Generic launches therefore use the pin even when the first project row belongs to another project. Explicit `--target` actions still address their record. The [integration evidence](plans/build-plan.md#integration-verification) records the isolated ten-step run, merged editor acceptance, compatibility-copy changes, and final checks.


Known gaps:

- Framework: `execute` (runtime.go) rejects a target that is not an instance and has no resolver hook (the app's `reveal` covers it); `bindings.list` is focus-scoped, so an app-wide listing is one `--target` per stop; history is not persisted.
- App: there is no "review this pane" verb; a job's rail label is its verb (the framework's `JobInfo` has no label), so the harness shows in the launch error and the review row, not in the rail.
