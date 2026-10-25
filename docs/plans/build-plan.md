# Build plan: pin, launch, tasks, notes

Status: approved for build, 2026-10-25. This is the plan to execute. It supersedes `crud-parallel-lanes.md` and, where they conflict, `crud-project-context.md`; `crud-plan-changes.md` records why (the astra and Fable reviews). Owner decisions behind it:
- edit in the right-hand panel, with drafts autosaved;
- saved shells, agents and Vim sessions go to a follow-up plan;
- a nameless project is saved and shown as "untitled";
- old pane assignments are imported.

## Goal

Pin a project. Launch terminals and agents into it through TUIOS. Tasks and notes made while it is pinned attach to it automatically. You can still switch the pin, browse other projects, and act on them directly. Anything mtm can't associate is saved as **unassociated** and can be reassigned later. mtm never refuses for missing context.

**While a project is pinned, everything you see is that project's:** tasks, notes, TUIOS sessions, terminals (shell panes), agents, reviews, and later Vim sessions. Other projects and unassociated items appear only in the explicit `all` view.

## Rules every step follows

- **The pin filters everything.** With a project pinned, every list, count, detail and preview of a project-owned thing shows only that project's items:

  | List | Belongs to the pin when |
  |---|---|
  | Tasks | `project_id` is the pinned project |
  | Notes | its own `project_id`, or its task's, is the pinned project |
  | TUIOS sessions | its `session_owners` row is the pinned project |
  | Terminals (shell panes) | its `assignments` row, or else its session's owner, is the pinned project |
  | Agents (agent panes) | same as terminals |
  | Reviews | its launch-time `project_id` is the pinned project |
  | Project directories | they belong to the pinned project's record |
  | Vim sessions (follow-up plan) | same rule when they are built |

  The projects list itself stays global, so you can always see and pin another project. `all` shows everything with owner labels and doesn't change the pin. With nothing pinned, every list shows everything, unassociated items included and labeled. The command line's `list` verbs apply the same filter, with `--all` to widen it. Every lane filters the lists it builds, through one helper; there is never a second filtering path.
- **Record first.** Missing or unknown association is never a reason to refuse. Refuse only when an operation is physically impossible, and never lose what the user typed.
- **Ownership is a field.** Every stored record carries `project_id`, and `task_id` where it fits; empty means unassociated. Panes and sessions, which TUIOS stores rather than mtm, get their owners in mtm tables keyed by stable ids.
- **One owner resolver.** `resolveOwner(ctx, args)` runs when an action is dispatched. Its order:
  1. explicit arguments (task beats project when they conflict, and the result says so);
  2. the selected record's owner;
  3. the pin;
  4. unassociated.

  An explicit scratch choice is always unassociated. The command line uses arguments or the pin, never another process's selection. The owner is captured once, so a pin change mid-job doesn't move the job.
- **Editing stays in the right-hand panel; drafts autosave.** `new` and `edit` turn the right-hand detail into an editor. Every change is written to a draft file under the data directory (`<data>/drafts/<noun>-<id>.json`):
  - Moving away never prompts and never blocks; the draft stays.
  - Coming back reopens the draft, and the list marks rows that have one.
  - `save` writes the record and removes the draft; `cancel` removes the draft.
  - Drafts survive an mtm restart.

  The record itself changes only on save. Text entry uses the existing insert mode; leaving it returns the keys to the page, so focus is never trapped.
- **A directory can belong to many projects.** A directory, repo, clone or worktree can be attached to any number of projects; each project holds its own entry (`Project.Trees`) and its own default. Nothing enforces one project per path. Anything that maps a path back to projects returns all of them, never "the first wins"; launches take their project from the resolver, never from the directory. Removing a directory from one project leaves it attached to the others. A shared directory record (one place to rename a path for every project) waits for a real need.
- **Deletion orphans, never cascades, never refuses.** Children keep their records, lose only the deleted owner, and record it in `former_project_id` / `former_task_id`. Deletion asks for confirmation and never deletes directories or kills panes.
- **Old tables keep their owners.** The database already declares `notes` (owner `notes`), `assignments` (owner `work`, key `window`), `active` (owner `work`) and `vim_sessions` (owner `vim`). New code declares them exactly so; `store.Declare` refuses a mismatch and mtm would not start.
- **Every commit:** `gofmt -l . && go vet ./... && go test -race ./...` green, `go run ./cmd/mtm --smoke` renders, `ochazuke.ValidateBindings` covered. No amends.
- **Never the owner's data.** Every lane runs with `--data` pointing at a temporary database and checks acceptance on a separate TUIOS server. One acceptance check uses a *copy* of the real database.

## Order

```
step 0 (serial, merges first)
   ├── lane A: ownership of panes/sessions, pin
   ├── lane B: project editing, directories, task editing
   └── lane C: notes
integration (after A, B, C merge): scoping, notes in details, deletion, final scenario
follow-up plan (later): saved shells/agents, Vim sessions, directory edit/remove, many-to-many links
```

## Step 0: the base everyone builds on

One agent, on main, before any lane starts.

- **`resolveOwner(ctx, args)`** in `cmd/mtm/owner.go`, as specified above. Until lane A adds the pin, the pin step is empty. Record creation uses the resolver; `inferLaunch` keeps its current guesses until lane A replaces it.
- **Remove the refusals:**
  - `AddProject` with an empty name saves, displayed as "untitled" (`internal/work/work.go:157`);
  - `AddTask` without a project or title, and `Retitle` with an empty title, save (`work.go:188-211`);
  - `task.new` with no project open creates an unassociated task (`cmd/mtm/tasks.go:153`);
  - `reveal` of an unassociated task lands on the unassociated list (`cmd/mtm/main.go:138`);
  - launching an unassociated task goes to the `no-project` session (`cmd/mtm/launch.go:351`).
- **Omitted vs empty:** an argument that was omitted opens the editor; one passed as an empty string saves empty.
- **The unassociated task list:** a "no project" row in the projects list that opens the unassociated tasks.
- **`former_project_id` / `former_task_id`** fields on `Task`, unused until integration.
- **`internal/draft`:** save, load, list and clear a draft file per noun and id. Lanes B and C both use it.
- **Directory recency:** a `dir_recent` table (owner `work`, key = cleaned path; `{path, last_used, uses}`) and `work.TouchDir(path)`. Lane A calls it on every launch and attach; lane B calls it on every directory assignment.
- **`inScope(owner)`**, the one filter helper: true when nothing is pinned, when the `all` view is on, or when the owner is the pinned project. Until lane A adds the pin it always returns true. The task list goes through it now; every later list must too.
- **Tests:** replace the tests that assert refusals (`internal/work/work_test.go:68`) with tests that assert the record is saved.

Acceptance: from the CLI and the TUI, create a nameless project, a titleless task with no project, and a task with an empty title. All save and are listed. The unassociated task opens through `--target` and launches into `no-project`. A draft written, then mtm restarted, then the draft reopened shows the typed text.

### Step 0 implementation and handoff

Completed on main, 2026-10-25. No lane A, B, C, or integration work is included.

- `cmd/mtm/owner.go`: `(*App).resolveOwner(ctx ochazuke.Ctx, args ochazuke.Arguments) (Owner, error)` returns `Owner{ProjectID, TaskID, Notice}`. Explicit `task` wins over `project`; `scratch: true` and explicit empty ownership stay unassociated. Unknown explicit ids return an unassociated owner and a notice. TUI creation can use the selected project, task, review, or draft. CLI execution ignores selection. The pin fallback remains empty for lane A. Capture this value before an editor or job starts.
- `(*App).inScope(owner string) bool` is the shared filter in `owner.go`. It returns true until lane A supplies pin/all state. Both `task.list` and the task page call it.
- `internal/draft.New(dataDir) *draft.Store` stores files under `dataDir/drafts`. `Save(noun, id, value)`, `Load(noun, id, dst)`, `List(noun) ([]draft.Entry, error)`, and `Clear(noun, id)` use one JSON file per noun/id. `Entry` has `ID` and `Data json.RawMessage`. Save uses a synced temporary file and atomic rename. Missing loads return `os.ErrNotExist`; clearing a missing draft succeeds. The app passes the directory containing `--data`.
- `cmd/mtm/editor.go` supplies the minimum right-panel editor needed for Step 0 acceptance: project creation and task title creation/editing. `editRecord` captures `Noun`, `ID`, `Text`, `ProjectID`, and `New`. The `App` Bubble Tea wrapper autosaves after input updates, including paste and undo. New rows are not stored until save. Draft rows reopen after restart. Enter saves; Escape returns to normal mode, where `s` saves and `q` cancels. Framework focus/page navigation remains available. Lane B can extend this editor for its additional fields; lane C uses the same draft store.
- `internal/work`: `Task.FormerProjectID` and `Task.FormerTaskID` serialize as `former_project_id` and `former_task_id`. `(*Store).TouchDir(path string) error` updates the cleaned-path key in `dir_recent` with owner `work`, key `path`, version 1. Empty paths do nothing. `RecentDirs() ([]DirRecent, error)` returns `{Path, LastUsed, Uses}` with millisecond timestamps. It does not sort or infer ownership. Launch/attach and directory-assignment callers remain for lanes A and B.
- The old `notes`, `assignments`, `active`, and `vim_sessions` declarations and rows are unchanged. Step 0 does not redeclare or migrate them.

Verification used temporary databases and the separate TUIOS server `mtm0-BfVsjW`. CLI and actual TUI runs saved and listed a nameless project, unassociated titleless tasks, and a project-owned empty-title task. CLI retitle to `""` persisted. An unassociated `--target` opened successfully. CLI and TUI launches both created real `/bin/sh` panes through the configurable `shell-proof` harness in `no-project`; the TUI attached to its new pane. No AI worker was started.

A TUI draft containing `restart-proof typed text` survived process exit and a new mtm process, reopened with the same text, and disappeared on cancel without creating a task. The full race suite covers owner precedence, CLI selection isolation, empty-record persistence, draft navigation/restart/cancel, directory recency, and `ValidateBindings`. `gofmt -l .`, `go vet ./...`, `go test -race ./...`, and isolated `go run ./cmd/mtm ... --smoke` passed. A read-only SQLite backup of the real database also rendered; all original metadata and every row byte in the four legacy owner tables matched before and after opening the copy.


## Lane A: pane and session ownership, then the pin

Files: `internal/work/owners.go`, `cmd/mtm/pin.go`, `cmd/mtm/launch.go`, `cmd/mtm/panes.go`. The pin's verbs live in `pin.go` (`Define("project")` merges across files), never in `main.go`'s block.

**A1, ownership of panes and sessions**
- Pane owners go in the old `assignments` table (key = TUIOS window id; `{window, session, task_id, project_id}`). Session owners go in a new `session_owners` table, keyed by session id with the name kept for display.
- A pane follows its session's owner unless it has its own row. A row can say "explicitly unassociated", which is different from having no row.
- The owner is written as soon as TUIOS confirms the pane or session was created, before listing panes or any UI update. If that write fails, the result says so, and the pane is still listed and can be reassigned.
- One-time import: existing `assignments` rows keep their owners, and agent panes recorded in `reviews` get their review's owner.
- An `associate` verb on tasks, panes and sessions. Reviews are history and are never reassigned; a review's detail shows its pane's current owner.
- Every launch and attach calls `work.TouchDir` with the pane's directory, so known directories put it first.
- Explicit scratch launch: a `scratch` flag on `agent.launch` and `pane.new` makes the owner unassociated whatever the pin. Shells go to `mtm-only`, agents to `no-project`, and the confirmation names which.

**A2, the pin**
- Stored in the old `active` table, row `singleton`. On first run, an old pinned task's project is imported (only the project, never the task); otherwise the last-launch project seeds it once. Unpinning survives a restart.
- `project.pin`, `project.unpin` and a pin picker reachable from every page. The pin shows in the app title ("Matomeru · A").
- The resolver's pin step goes live. `inferLaunch`'s current-directory and last-launch guesses, and the last-launch API, are removed. `projectAt` (first project wins a tie) goes with them.
- An `all` toggle verb shows every project's items with owner labels, without changing the pin.
- `inScope` goes live. The panes page (sessions, terminals, agents) and the reviews page filter through it.
- Launching into B while A is pinned opens the pane labeled "outside pin: B". A launch that finishes after the pin changed only notifies; it doesn't switch views.

Acceptance:
- Launch into a session whose name has nothing to do with the project; after a restart its panes still read as A.
- With A pinned, the panes page shows only A's sessions, terminals and agents, and reviews shows only A's. `all` shows B's and unassociated ones, labeled, with A still pinned.
- A pane started outside mtm lists as unassociated; associate it to A, then B, then unassociated, and each survives a restart.
- Reassigning a session moves its panes that have no row of their own and leaves a separately linked pane alone.
- Pin A, browse B, and launch a shell and an agent with no target: both go to A. A launch aimed at B goes to B, and A stays pinned.
- Unpin and restart: nothing is pinned.
- Change the pin while a launch is running: the pane still belongs to the original project.
- A pane start succeeds but listing panes fails: the owner is still recorded.

### Lane A implementation and handoff

A1/A2 implemented in `lane/a`, 2026-10-25; no merge or rebase. Project/task editors, directories, notes, and deletion remain with their assigned lanes/integration.

- `internal/work/owners.go`: `Assignment`, `SessionOwner`; `SetPaneOwner`, `PaneOwner(window, sessionID)`, `SetSessionOwner`, `SessionOwner`, `AssociateTask`, `Pin`, and `SetPin`. The existing assignments/active metadata is unchanged. `session_owners` is owner `work`, key `id`, version 1. Empty assignment rows override inheritance. Legacy task assignments resolve their parent chain without rewriting original bytes. Import markers live in `active/singleton`; unknown active fields are preserved, the old task pin is cleared, and review import never overwrites an assignment.
- `App.pin`, `App.all`, and `App.pinVersion` activate `inScope` and the resolver's pin fallback. `pin.go` owns project pin/unpin/pick, the `P` picker, and `S`/`scope.all`. `project.launch` and `task.launch` now use `launchRecord`; generic launch handlers ignore browsing selection. The small `tasks.go` changes also add CLI `--all` filtering and avoid selecting a saved out-of-scope task row.
- `launch_owner.go` records newly created session ownership before opening its window and pane ownership before harness readiness. It retains created panes and warnings on ownership/listing failure. Both launch types capture the pin generation; late completion does not navigate. Explicit outside-pin launches attach without widening the list. `pane` carries `SessionID` from the daemon, including empty sessions.
- Pane/session rows and review rows filter through `inScope`; pane and session labels name their owner or `unassociated`. `pane list`, `session list`, `review list`, and `task list` support `--all`. Association verbs are registered in `cmd/mtm/owners.go`, including `task.associate`, so lane B need not duplicate it. CLI target reveal permits an explicitly addressed out-of-scope record. `pane.attach` opens its page before focusing the terminal.
- Integration should preserve these minimal shared changes in `main.go`, `tasks.go`, and `owner.go`. Lane B's extended editor and lane C's notes still own their refresh hooks; integration must add those to scope refresh and the final detail/deletion pass.

Acceptance used only temporary databases and the named server `mtma-1792958`. Actual CLI invocations (each a new process) verified arbitrary session names, A-only pane/session/review lists, labeled `--all`, external-pane reassignment A → B → unassociated, session inheritance with explicit-row exceptions, generic and explicit shell/agent destinations, scratch sessions, persisted unpin, and attach directory recency. Real TUI captures verified browsing B while generic launches use A, all-view labels, both explicit outside-pin launch types, and changing A → B in the pin picker while an agent launch awaited readiness: its owner stayed A and completion stayed on Work.

A temporary SQLite trigger forced an ownership write failure after a real pane opened: the result named the failure, retained the pane, and reassignment succeeded after removing the trigger. A temporary socket proxy forwarded real daemon creation but rejected the following list; it observed the session owner before `new-window` and the pane owner before the rejected list, and the result retained the created pane. A read-only backup of the real database rendered with its imported project pin; all existing table metadata and original assignment/note/Vim row bytes were unchanged (5 assignments, 0 notes, 2 Vim rows). Regression coverage includes pin/import persistence, precedence, scoping, session inheritance, a blocked-readiness stale completion, listing failure, outside-pin task creation, and `ValidateBindings`.

Final checks passed: `gofmt -l .`, `go vet ./...`, `go test -race ./...`, and `go run ./cmd/mtm --data TEMP_DB --server mtma-1792958 --smoke`. A final real CLI pass verified explicit out-of-pin task creation, task reassignment A → B → unassociated, scoped/all task lists, and labeled session lists. The isolated server was stopped and temporary binaries/databases (including the real-data backup) were removed.


## Lane B: project editing, directories, task editing

Files: `cmd/mtm/project.go` (project verbs, never in `main.go`'s block), `cmd/mtm/editor.go` (the right-hand panel editor), `cmd/mtm/tasks.go`, tree functions in `internal/work/work.go`.

- **B1, projects:** view, new, edit (name, theme, settings) and delete in the right-hand panel through `internal/draft`. Creating a project registers its name only; making directories on disk stays with `o proj new`. Deletion is built in integration.
- **Known directories**, the source the directory chooser lists from: `knownDirs()` merges
  - every project's directories (from all projects, ignoring the pin), with their repo and branch;
  - the directories of TUIOS sessions and the current directories of panes;
  - git repos found under the configured **project base dirs** (below).

  **Project base dirs** are per machine, never hard-coded. They're read from `$XDG_CONFIG_HOME/matomeru/dirs.json` (else `~/.config/matomeru/dirs.json`, the same place as `harnesses.json`):

  ```json
  { "project_base_dirs": [ { "path": "~/proj" }, { "path": "~/hub/o" }, { "path": "~/work", "depth": 2 } ] }
  ```

  - `depth` is how many levels below the base to look for folders with a `.git` (default 1; no deeper scan). Symlinks are followed and deduplicated by their real path, so a folder of links like `~/hub/o` works as a base.
  - `MTM_PROJECT_BASE_DIRS` (colon-separated paths, depth 1) overrides the file, for hosts reached over ssh or one-off runs.
  - With no file and no variable, mtm uses whichever of `~/proj`, `~/projects`, `~/src`, `~/code`, `~/dev` exist; on a machine with none of them, known directories come only from projects and TUIOS.
  - A configured base that doesn't exist is skipped and noted once in the list's footer, never an error.
  - `dirs.bases` lists the effective bases and where each came from (file, variable or default); `dirs.base.add PATH` and `dirs.base.remove PATH` edit the file, so nobody has to edit JSON by hand.

  Duplicates collapse by cleaned path. The order is most recently used first, by `dir_recent.last_used`, seeded on first run from TUIOS session `last_active`; never-used directories follow alphabetically. Each row shows the path, repo and branch, when it was last used, and which projects already have it. Fuzzy search filters the list; a `browse…` row opens ochazuke's directory picker for anything not listed. The list is cached and refreshed in the background, never on a keypress.
- **B2, directories:** in the project detail, add a directory chosen from known directories (no-op if this project already has it; attaching a path another project has is allowed, and the detail lists the other projects that share it; a missing path is attached and flagged) and set the default. The first one added becomes the default. These store functions are shared, and o-proj's registration moves onto them. Editing and removing directories wait for the follow-up plan.
- Project directories and tasks are listed through `inScope`.
- **B3, tasks:** selecting a task shows it and never opens the editor; `edit` does. Title and status edits, `new` through the resolver, `done` and `launch` kept. Deletion is built in integration.

Acceptance:
- Create, view and rename a project and a task. A half-typed edit survives moving away, an mtm restart, and coming back. Cancel removes the draft and leaves the record unchanged.
- A new project with two directories, with the second marked default, keeps both after a restart.
- With `MTM_PROJECT_BASE_DIRS` pointing at a temporary folder holding two repos and no `~/proj` or `~/hub`, known directories list exactly those two plus project and TUIOS directories; a missing base shows in the footer and nothing fails.
- Known directories list a repo launched yesterday above one launched last week, and above repos never used; a repo already attached to B shows "B" beside it; a path typed in `browse…` is attached and appears at the top next time.
- The same repo attached to A (as default) and B (as an extra directory): both details list it and name the other project; pinning B shows it in B's directories; A's launches use it and B's use B's default.
- A shell and an agent launched for that project open in the default directory (check `pwd` in the real pane). A missing default directory launches in home with a notice.

### Lane B implementation and handoff

Lane B adds project view/edit and launch settings, task view versus explicit edit, and shared multi-field draft editing in `cmd/mtm/editor.go`. Saving patches the latest stored project or task instead of replacing its other fields. Drafts include every field and captured ownership. New task drafts start with status `todo`.

The shared note editor extension consists of `App.editorSave`, `editorRefresh`, and `editorFinish` callbacks keyed by noun. `editRecord.TaskID` carries note ownership. A note input is multiline and preserves its text; insert-mode Enter inserts a newline, `ctrl+s` saves, and normal-mode `s` saves. Note handlers and persistence remain lane C's files.

`internal/work/trees.go` exports `Project(id)`, `AddTree(id, tree)`, and `SetDefaultTree(id, path)`. Directory attachment is idempotent per project, permits shared paths and missing paths, and touches recency. `o-proj` registration uses the same attachment function. `SeedDirs` imports TUIOS activity without replacing recorded usage.

`cmd/mtm/directories.go` owns base configuration, bounded symlink-aware repository discovery, the background directory cache, fuzzy filtering, and the framework picker. `dirs bases`, `dirs base.add PATH --depth N`, and `dirs base.remove PATH` expose base configuration. `project directory [PATH]` attaches or chooses; `project default PATH` selects the default.

Lane B smoke uses temporary databases under `/tmp/mtm-lane-b-proof.QnOeHp` and the separate TUIOS server `mtmb-QnOeHp`. Actual TUI checks covered project draft navigation, process restart, cancel without changing the record, the known-directory chooser, shared-project labels, and attachment of a typed missing path. The attached path became the first known-directory row. CLI checks covered project/task creation and rename, task status, two retained directories with the second default, and explicit base overrides with a missing-base footer.

Real shell and shell-backed agent panes printed the selected project's default directory with `pwd`. The missing-default home fallback and its TUI notice were exercised using a read-only build of lane A's launch implementation against the same B-created temporary records. `launch.go` remains unchanged in B; integration must retain A's `startCwd` implementation. Pin state and the live `inScope` implementation also remain lane A dependencies.

Actual TUI checks also created and renamed a project, created a task with status `todo`, and edited a task's title and status through keyboard focus navigation. The smoke run found that input descendants were unreachable as separate focus stops. Additional fields now sit beside the main input under `work`; `TestTaskFieldKeyboardFocusAndSave` failed before the fix and passed afterward.

`gofmt -l .`, `go vet ./...`, `go test -race ./...`, and isolated `go run ./cmd/mtm --smoke` passed. The race suite includes `ValidateBindings` coverage, draft restart/cancel, shared directory defaults, recency ordering, base configuration, discovery depth/symlink deduplication, and chooser delivery from a project row on another page. A read-only SQLite backup of the real database rendered with every pre-existing row and table declaration unchanged. The copy gained only the Step 0 `dir_recent` table declaration.


## Lane C: notes

Files: `internal/work/notes.go` (or `internal/notes`), `cmd/mtm/notes.go`.

- The table is declared `{Name: "notes", Owner: "notes", Key: "id", Version: 1}`. Old rows load as they are, ignoring fields like `provenance` and keeping them when saved.
- A `note` noun with new, view, edit and associate, plus a notes page. Editing uses the right-hand panel and `internal/draft`. New notes go through the resolver: the selected task, then the pin, then unassociated. An empty note saves.
- `note new TEXT [--task ID] [--project ID]` on the command line follows the same rules, without reading any TUI's selection.
- The notes page lists through `inScope`.
- Notes inside task and project details are left to integration.

Acceptance:
- A copy of the real database opens and lists its existing notes.
- Note creation, case by case:
  - nothing selected, nothing pinned: unassociated;
  - A pinned, nothing selected: belongs to A;
  - A pinned, an A task selected: that task and A;
  - A pinned, a B task selected (all-projects view): that task and B;
  - an unassociated task selected: that task, no project;
  - `--task`/`--project` given on the command line: those win.


### Lane C implementation and handoff

Lane C adds `internal/work/notes.go` and `cmd/mtm/notes.go`; no editor implementation is forked. `work.Tables` adds the exact legacy declaration `{Name: "notes", Owner: "notes", Key: "id", Version: 1}`. `Note` retains unknown JSON fields through read/edit/write, including provenance, and carries current and former project/task ids. `Notes`, `Note`, `PutNote`, `AddNote`, and `NoteProject` are the storage interface; `NoteProject` resolves task-only legacy rows without rewriting them.

The note noun exposes `new`, `list`, `view`, `edit`, and `associate`. New records capture the shared resolver's owner once; omitted text opens a draft and explicit empty text saves immediately. All note lists and saved previews use `inScope`; `note list --all` temporarily widens the shared `all` flag and restores it. Notes page rows always identify their owner. Association preserves the body, legacy metadata, and an existing draft.

Dependency on lane B: `editRecord.TaskID` and the shared `App.editorSave`, `editorRefresh`, and `editorFinish` maps. `defineNotes` registers the note writer, row refresh, and return-to-notes callback. Initialize the maps before calling it; create the Notes page after project rows load. Lane C's main wiring includes those maps and `all`; keep one declaration of each when merging B/A. The shared Work editor handles multiline notes, verbatim whitespace, autosave, restart, save, and cancel. Lane C does not change `editor.go`. Integration adds `refreshNotes()` to pin/all refresh and clears out-of-scope drafts/details alongside other lists; this is the plan's integration scoping work, not a second filter.

Verification ran in a throwaway sibling `matomeru-lane-c-proof`, with B-authored editor/dependency snapshots and A's owner resolver/storage snapshot, without merging or rebasing either lane. The race suite passed, including the existing `ValidateBindings` coverage and permanent note tests for empty/explicit ownership, unknown-field preservation, draft navigation/restart/cancel/save, first-visit page creation, and association of unfinished drafts. A throwaway smoke exercised all six creation acceptance cases against A's resolver, CLI selection isolation, inherited project filtering, `--all` restoration, and a pin change while a draft was open. `go vet ./...` passed; `gofmt -l` was empty after formatting the throwaway B snapshot. An isolated `go run ./cmd/mtm ... --smoke` rendered.

Actual CLI and TUI checks used only `/tmp/mtm-c-notes-1025-cc61/` and the separate TUIOS server `mtmc-cc61`. CLI checks covered empty notes, explicit conflicting task/project ownership (including an unassociated task), whitespace-preserving multiline edit/view, and scratch reassociation. The actual Notes page created and displayed a multiline note; its unfinished edit survived navigation and process restart, reopened intact, and cancelled without changing the saved body. From a selected A task, the palette's `note new` captured that task and A and showed both in the saved note's detail.

A read-only SQLite backup of the real database opened and listed notes through the CLI and rendered through `--smoke`. That real database currently contains **zero** notes: the copied list was `[]`, and the original/copy note row digest and notes metadata matched. Legacy nonempty task-owned rows and provenance preservation were exercised with an explicit old-schema fixture, not falsely attributed to the owner's data.

## Integration: after A, B and C merge

One agent, on main.

- **Check the pin filter end to end:** with A pinned, tasks, notes, sessions, terminals, agents, reviews and directories, plus their counts, details and previews, contain nothing of B's and nothing unassociated. `all` and the command line's `--all` show them. Changing the pin clears selections and details that fall outside it. A late pane preview or review capture can't bring back the previous project's information.
- **Notes in details:** task and project details list their notes.
- **Deletion,** noun by noun: deleting a project or task orphans its tasks, notes, pane and session owners, with `former_*` set, and clears the pin if needed. The confirmation states how many records are affected.
- **The final scenario** on a separate TUIOS server and a temporary database:
  1. Create a nameless project and name it later.
  2. Add two directories and set the default.
  3. Pin the project. Every list (tasks, notes, sessions, terminals, agents, reviews) now shows only its items.
  4. Launch a shell and an agent; both land in the default directory.
  5. Add a task and a note; both attach to the pin.
  6. Launch a scratch agent; it's unassociated.
  7. Browse another project with `all`, then launch into it; the pin stays.
  8. Associate the scratch pane with the pinned project.
  9. Delete a task and see its notes orphaned.
  10. Restart mtm; everything above holds.

  Then one full `go test -race ./...`.

## Checkouts and merging

- **Who builds what:** step 0, lane A and integration run on astra (`command omp --allow-home -p --no-session --model openai-codex/gpt-6-astra --cwd <checkout>`, as a background process, always the `openai-codex` provider); lanes B and C run on Opus subagents.
- Step 0 and integration run on `~/proj/matomeru` main.
- Lanes run in plain clones beside it, `~/proj/matomeru-lane-a`, `-b` and `-c`, so `../ochazuke` still resolves. Branches are `lane/a`, `lane/b`, `lane/c`. No git worktrees.
- Each lane rebases onto main and merges in the order A, B, C. After each merge, the merged result reruns its acceptance checks, not just a clean text merge.

## Follow-up plan, not in this build

- Saving a shell or agent pane as a reopenable record, with reopen and forget.
- Neovim panes and saved Vim sessions (`vim_sessions`, owner `vim`).
- Editing and removing directories.
- Many-to-many links (one note on several tasks), when a real case appears.
