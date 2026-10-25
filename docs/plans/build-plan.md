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

## Lane B: project editing, directories, task editing

Files: `cmd/mtm/project.go` (project verbs, never in `main.go`'s block), `cmd/mtm/editor.go` (the right-hand panel editor), `cmd/mtm/tasks.go`, tree functions in `internal/work/work.go`.

- **B1, projects:** view, new, edit (name, theme, settings) and delete in the right-hand panel through `internal/draft`. Creating a project registers its name only; making directories on disk stays with `o proj new`. Deletion is built in integration.
- **Known directories**, the source the directory chooser lists from: `knownDirs()` merges
  - every project's directories (from all projects, ignoring the pin), with their repo and branch;
  - the directories of TUIOS sessions and the current directories of panes;
  - `~/hub/o` links;
  - git repos one level under `~/proj` (only folders with a `.git`; no deep scan).

  Duplicates collapse by cleaned path. The order is most recently used first, by `dir_recent.last_used`, seeded on first run from TUIOS session `last_active`; never-used directories follow alphabetically. Each row shows the path, repo and branch, when it was last used, and which projects already have it. Fuzzy search filters the list; a `browse…` row opens ochazuke's directory picker for anything not listed. The list is cached and refreshed in the background, never on a keypress.
- **B2, directories:** in the project detail, add a directory chosen from known directories (no-op if this project already has it; attaching a path another project has is allowed, and the detail lists the other projects that share it; a missing path is attached and flagged) and set the default. The first one added becomes the default. These store functions are shared, and o-proj's registration moves onto them. Editing and removing directories wait for the follow-up plan.
- Project directories and tasks are listed through `inScope`.
- **B3, tasks:** selecting a task shows it and never opens the editor; `edit` does. Title and status edits, `new` through the resolver, `done` and `launch` kept. Deletion is built in integration.

Acceptance:
- Create, view and rename a project and a task. A half-typed edit survives moving away, an mtm restart, and coming back. Cancel removes the draft and leaves the record unchanged.
- A new project with two directories, with the second marked default, keeps both after a restart.
- Known directories list a repo launched yesterday above one launched last week, and above repos never used; a repo already attached to B shows "B" beside it; a path typed in `browse…` is attached and appears at the top next time.
- The same repo attached to A (as default) and B (as an extra directory): both details list it and name the other project; pinning B shows it in B's directories; A's launches use it and B's use B's default.
- A shell and an agent launched for that project open in the default directory (check `pwd` in the real pane). A missing default directory launches in home with a notice.

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

- Step 0 and integration run on `~/proj/matomeru` main.
- Lanes run in plain clones beside it, `~/proj/matomeru-lane-a`, `-b` and `-c`, so `../ochazuke` still resolves. Branches are `lane/a`, `lane/b`, `lane/c`. No git worktrees.
- Each lane rebases onto main and merges in the order A, B, C. After each merge, the merged result reruns its acceptance checks, not just a clean text merge.

## Follow-up plan, not in this build

- Saving a shell or agent pane as a reopenable record, with reopen and forget.
- Neovim panes and saved Vim sessions (`vim_sessions`, owner `vim`).
- Editing and removing directories.
- Many-to-many links (one note on several tasks), when a real case appears.
