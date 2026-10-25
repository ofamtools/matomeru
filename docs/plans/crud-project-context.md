# Noun editing, pinned project, and scratch work

Status: proposed implementation plan, 2026-10-25. No application changes in this plan.

## Goal

Most domain noun pages should support creating, viewing, editing, and deleting their records where those operations make sense. Keep the list on the left and the selected record's information or edit draft on the right. CRUD states do not require new first-class keyboard modes.

An explicit app-wide pinned project scopes domain information and lists to that project's associated items and supplies defaults for actions on every page. Projects can gain directories and repositories after creation, with one default directory for terminal and agent launches. Shells, agents, and Vim can also run interactively without project ownership. Creating, saving, and launching never refuse for missing context: what mtm cannot associate is recorded as unassociated and can be associated later.

## Current behavior and gaps

| Concern | Current evidence | Missing behavior |
|---|---|---|
| Project navigation | `cmd/mtm/main.go`: `App.current`, `openProject`, `renderWork` | `current` is the browsed project, not a pin. Work renders projects above tasks rather than list/detail. |
| App-wide pin | `internal/work/work.go`: `LastLaunched`, `SetLastLaunched`; `cmd/mtm/launch.go`: `inferLaunch` | No explicit pinned project. Launch inference uses task row, project row, cwd, then last launch. |
| Project locations | `internal/work/work.go`: `Project.Trees`, `DefaultTree`, `Tree` | Existing data model, but no mtm CRUD for these locations. |
| Location registration | `cmd/o-proj/main.go` appends trees to existing projects and sets an absent default | Reuse shared work-store behavior rather than adding inconsistent mutations in mtm. |
| Task editing | `cmd/mtm/tasks.go`: new, open, edit, done | Open currently edits the title in a prompt. No separate read detail or delete. |
| Live panes | `cmd/mtm/launch.go`, `panes.go`: shell/agent launch, attach, detach, kill | Unassociated launches work only when inference finds nothing. Need an explicit scratch choice even with a pin. |
| Unassociated destinations | Agents use `no-project`; shells use `mtm-only`, both with home as fallback cwd | A running pane is not the same as a saved, reopenable workspace. |
| Saved editor | No Vim noun or editor-session package in this rebuild | Need real Neovim launch, scratch use, and explicit save/resume. |

`matomeru-old/internal/work/scope.go` persists the old pin in `active/singleton`. It is task-based and can create or select a task when pinning a project. Reuse the concept and relevant persistence logic, not that requirement. This plan allows a project-only pin and an empty pin.

The znbu outlines contain implementation claims about the older app. Treat them as reference contracts, not evidence that those packages exist in this rebuild.

## Decisions

### Record first; never refuse for missing context

mtm creates and tracks data whether or not it is complete. Missing or unknown association is never a reason to refuse.

- A record with no resolvable project or task is saved as unassociated. Every list shows it with a visible "unassociated" label (no pin, or the All projects view), and an `associate` action attaches it later.
- Missing optional fields are stored empty, not demanded up front. Drafts save with whatever was entered.
- Where an operation would otherwise refuse, mtm records what it can, picks the documented fallback, and says what it did in the result or notice (for example, "launched in ~ (default directory missing)").
- Deleting a parent never refuses: its children are kept, lose only that association (a deleted task's children keep its project), and record the former owner's id. Deletion still asks for confirmation.
- Refusal is limited to operations that cannot be carried out at all, such as a path that cannot be written or a binary that is not installed. Even then, nothing the user typed is lost.

### Ownership: one owner field, plus links

- Every project-owned record carries its owner in its row: `project_id`, and `task_id` where it fits. Empty means unassociated.
- A generic `links` table (`from_kind`, `from_id`, `to_kind`, `to_id`) holds everything else: many-to-many (a note on two tasks) and ownership of things mtm does not store itself.
- TUIOS sessions and panes are owned by a project or unassociated, recorded in `links` at launch. A pane inherits its session's project unless linked on its own.
- Every unassociated record can be associated or reassigned later with one `associate` verb; reassignment never moves a process or rewrites history snapshots (reviews keep their launch-time owner).
- No SQL foreign keys: the store holds JSON rows, and record-first means a dangling id is kept, not rejected.

### List/detail is the default CRUD presentation

- Selecting a row shows read-only information on the right. Viewing never writes.
- `new` and `edit` open a draft in the right panel. Use a framework modal for short edits or when the terminal is too narrow for both panels.
- Save commits the draft. Cancel discards it. Navigating away from a dirty draft requires save or discard, not an implicit write.
- Keep the left list visible during panel editing. Preserve selection by stable ID after updates. A successful create selects the new record.
- Use existing normal/insert interaction modes for navigation and text entry. Store view/edit/create state on the app-owned draft, not in a new CRUD keyboard-mode system.
- Register every action through `Define`, so keys, palette, CLI, and catalog use the same handlers. CLI arguments execute directly without opening an interactive form.
- Use ochazuke's focus, inputs, modals, confirmation, and layout. Start with concrete project/task editors. Extract shared machinery only when both need the same behavior.

### CRUD applies by noun capability

| Noun | Intended operations |
|---|---|
| Project | New, view, edit, delete, pin/unpin |
| Task | New, view, edit, delete; retain done and launch |
| Project tree | Add an existing directory/repository, view, edit metadata/path, remove association, set default |
| Live pane | New, inspect, attach, detach, kill. Killing a process is not deleting a saved record. |
| Saved Vim session | Save, inspect, rename/update association, reopen, forget |
| Review | Retain open/accept/reject/save-answer semantics; do not force generic CRUD onto review history. |
| Harness | Retain registry/list/models behavior. Editing harness configuration is outside this request. |

The existing Work page must retain access to the selected project's tasks. A project detail can open that project's task list, whose selected task has its own right-hand detail. Do not remove task navigation to make room for project editing.

### Pinned context is separate from navigation and ownership

Proposed app state: `App.context.PinnedProjectID`, persisted as one explicit context row per database. Keep `App.current` as navigation state.

- Pinning is explicit and visible in app chrome on every page. Browsing, opening a project, editing a different project, and launching do not change it.
- An empty pin is valid. Pinning a project never creates a task.
- At action dispatch, resolve ownership once and snapshot it before confirmation or asynchronous work. Pin changes while a job runs cannot change its destination or saved ownership.
- Precedence: explicit scratch choice; explicit target/arguments or source-record ownership; pinned project; no project. A targeted task/project action acts on that record, even if another project is pinned.
- When explicit task and project values conflict, the task's project wins and the result says so. Do not silently substitute the pin.
- Generic creation from a page without an owned source uses the pin. Generic navigation/help/settings actions need no project and must continue to work without one.
- Existing records retain their ownership when opened or acted on. The pin supplies creation defaults; it does not reassign them.
- Do not infer ownership from process cwd or last launch after this cutover. Remove their inference callers and obsolete last-launch API/state after migrating any useful prior default. `projectAt` may remain only if another real caller needs it.
- Import a valid older `active/singleton` task's project on first initialization when present. An existing last-launch project may seed the initial pin only when there is no old pin. Record initialization so an intentional unpin survives restart.
- CLI and TUI use the same persisted context. Reload it at context-consuming action boundaries rather than trusting a startup-only cache. Live push synchronization between separate processes is not required here.

### The pin scopes information and lists app-wide

- When a project is pinned, every project-associated domain list and information panel shows only items associated with that project. Apply this consistently to tasks, trees, panes, agents, saved Vim sessions, reviews, and future project-owned nouns, including counts, search results, previews, and detail panels.
- Association means explicit project ownership or ownership inherited through a task or source record. Cwd and daemon session names alone do not establish association. Historical records use their recorded ownership.
- Lists and their details use the same scope. Changing the pin refreshes lists, clears details and selections that are now out of scope, and prevents late asynchronous results from restoring the previous project's information. Resolve a dirty draft before changing scope.
- With no pin, show items across projects, including unassociated items, with ownership labels. A path-only saved item remains unassociated and is excluded from a pinned project's ordinary lists.
- Keep the project switcher/pin picker global so another project can be selected. Global configuration, harness definitions, help, and other non-project data remain global.
- Provide an explicit, visibly labeled All projects view for finding other projects and unassociated scratch work without changing the pin. Returning to the ordinary view restores pinned scope. Explicit scratch launch can attach its new pane directly; that does not add the pane to the pinned project's list.
- Apply the same default scope to registered list/query verbs and their CLI results. Cross-project listing requires the explicit all-projects option; an explicit action target remains intentional and does not change the pin or silently broaden a list.

### Project trees supply cwd, not ownership

Keep `Project.Trees` and `Project.DefaultTree`; do not introduce a competing directory/repository inventory for this request.

- A tree can be a plain directory, clone, or existing worktree. Adding it attaches an existing path; it does not run `git init`, clone, create worktrees, or delete files.
- Normalize and validate paths at mutation boundaries. Adding a path the project already has is a no-op that selects the existing tree. Git discovery enriches repository metadata but must accept plain directories.
- A project with zero trees is valid. With one or more trees, exactly one is default. The first added tree becomes default.
- Changing a default tree's path updates `DefaultTree` atomically. Removing the default while others remain makes the first remaining tree the default and says so; the user can change it. Removing the last clears it.
- Keep legacy `Project.Repo` compatibility with o-proj, but do not use that scalar to limit a project to one repository. Per-tree metadata describes each checkout.
- Launch cwd precedence: explicit directory, selected source tree when applicable, resolved project's default tree, home for scratch or pathless projects. Show home fallback in the launch confirmation. A configured default path that no longer exists falls back to home with a visible notice, never silently.
- Changing the default affects future launches only. Never change the cwd or ownership of a running process because the pin or default changed.

### Scratch lifetime and persistence are different

- Shell, agent, and Vim launch actions offer an explicit unassociated/scratch choice. This bypasses pin and inference without clearing the pin.
- Scratch work uses the existing unassociated daemon destinations and remains attachable and interactive. Find it through the explicit All projects view in Panes while a project is pinned, or the ordinary unpinned list. Detach must not kill it.
- A scratch process has a runtime cwd, usually home. That fallback is not consent to persist a workspace there.
- Saving a reusable workspace/session always succeeds. With a project it is saved under that project; with an explicit path it is saved there; with neither it is saved unassociated in mtm-owned storage. Never invent a project from a path.
- Project-owned Vim session files live in mtm-owned storage beside the selected database, following the old editor contract. A path-only save uses the explicitly chosen session-file destination.
- Save/associate updates the existing live item. Do not spawn a replacement process or move a running pane merely to match a project-derived session name. Future reopen uses its saved context.
- Persist launch ownership on records that represent it; shell ownership must not exist only in a session name. Agent reviews keep the ownership snapshot from launch, including no project.
- Automatic daemon restoration and existing operational review records are separate from explicitly saving a reusable mtm workspace. Do not promise scratch processes disappear on daemon restart.
- Saving a Vim session does not save modified Vim buffers. Neovim owns those decisions. Forgetting a saved session must not delete edited files or kill its live pane.

## Ordered implementation

### 1. Record ownership and links, including panes and sessions

Likely files: `internal/work/work.go` (a `links` table, owner `work`), `internal/work/links.go`, `cmd/mtm/launch.go`, `cmd/mtm/panes.go`, focused tests.

Add the generic `links` table and an `associate` verb that works on any project-owned noun. Record TUIOS session and pane ownership in mtm at launch, keyed by the daemon's stable session and pane ids. A pane inherits its session's project unless it has its own link. Panes and sessions mtm did not launch, or launched before this step, show as unassociated and can be reassigned. Reassigning moves no process and changes no cwd.

Acceptance:
- Launch a shell and an agent for project A: their session and panes read as A after an mtm restart, without parsing session names.
- A pane started outside mtm lists as unassociated; `associate` moves it to A, then to B, then back to unassociated. Each move persists across restart.
- Reassigning a session moves its panes that have no link of their own; a pane linked separately keeps its link.
- Reassigning a task or review with `associate` updates its owner field; nothing else is touched.
- A link to a pane that has ended is kept (history); live lists show only live panes.

Verification: focused store tests for links and inheritance; real launch, restart, and reassign against an isolated TUIOS server.

Dependencies: none.

### 2. Add the explicit project pin

Likely files: `internal/work/work.go`, `cmd/mtm/main.go`, `cmd/mtm/launch.go`, focused existing tests.

Add persistent context and app state, project pin/unpin verbs, and the global pin indicator. Replace last-launch/cwd ownership fallback with the documented resolver. Implement shared project-scope reads across existing lists and details, using owner fields and links from step 1, plus the explicit All projects view. Preserve source-targeted actions. Keep shell and agent launches working through the cutover.

Acceptance:
- Pin A, browse B, switch to Panes, then launch a generic shell/agent. It uses A. A targeted B launch uses B and leaves A pinned.
- Unpin and restart leaves no pin. A taskless project can be pinned. Existing valid old context imports once without creating tasks.
- Confirm a launch for A, change the pin before its job finishes, and observe A on the resulting records and pane.
- Seed associated items for A and B plus unassociated items. With A pinned, every ordinary domain list, count, search, preview, and detail contains only A's items. The explicit All projects view includes B and unassociated items without changing the pin.
- Switch the pin from A to B during an asynchronous detail load. Only B's information appears after completion. Unpinning restores the cross-project view; CLI list/query results follow the same scope.

Verification: focused context/launch behavior tests, CLI pin/read/unpin against an isolated database, and an interactive launch on an isolated TUIOS server.

Dependencies: step 1 (scoping reads ownership and links).

### 3. Add project list/detail CRUD

Likely files: `cmd/mtm/main.go`, a project editor file, `cmd/mtm/prompt.go`, `internal/work/work.go`, focused tests.

Build project read detail and create/edit drafts using the existing framework components. Keep task navigation reachable. Creation registers a named project without filesystem side effects; filesystem scaffolding remains `o proj new`.

Acceptance:
- View, create, rename, and delete an empty project through the TUI and registered verbs. Cancelled edits leave the store unchanged.
- Dirty draft navigation offers save/discard. Errors retain the draft and keep the list usable.
- Confirm deletion. A deleted project's tasks, notes, reviews, and saved workspace records are kept and become unassociated (former project id recorded); the confirmation states how many. Clear a deleted project's pin atomically. Deletion never deletes directories or kills panes.

Verification: exercise the actual list/detail editor at wide and narrow widths, then reopen the isolated database to inspect persisted values. Cover deletion orphaning and cancel behavior with focused tests.

Dependencies: step 2.

### 4. Add tree CRUD and the default-directory action

Likely files: `internal/work/work.go`, project/tree editor files, `cmd/mtm/launch.go`, `cmd/o-proj/main.go`, focused tests.

Add project-scoped tree verbs and editing controls. Put shared mutations in the work store, and migrate o-proj's relevant registration path to preserve the same invariants. Preserve unrelated tree metadata and project settings on partial edits.

Acceptance:
- Create a pathless project, attach two existing directories from its detail, and mark the second default. Both remain attached after restart.
- Edit/remove/default operations preserve the single-default rule. A duplicate path is a no-op; a path that does not exist is still attached and flagged missing. No partial writes. Removing an association leaves the filesystem untouched.
- Pin the project and launch both a terminal and an agent. Observe the configured default cwd in the actual processes. A missing default path launches in home with a visible notice.

Verification: focused store/default tests; o-proj registration regression checks; real shell `pwd` and agent launch on an isolated daemon.

Dependencies: steps 2 and 3.

### 5. Apply list/detail CRUD to tasks

Likely files: `cmd/mtm/tasks.go`, task detail/editor file, `internal/work/work.go`, focused existing tests.

Separate task viewing from editing. Support title and status edits, explicit create/save/cancel/delete, and retain done/launch. Follow the project editor's concrete pattern without introducing a second form system.

Acceptance:
- Selecting/opening a task shows information without opening an editor or writing. Edit/save/cancel retain list selection.
- Creation uses the explicit project list context, otherwise the pin, otherwise creates an unassociated task. It never silently chooses the first project. A task with an empty title is still saved.
- Confirm deletion. Deleting a task keeps its notes, reviews, and child tasks; they move to the task's project (or stay unassociated) with the former task id recorded. Done remains a status change, not deletion. Pinning a project never pins or creates a task.

Verification: exercise task CRUD from the actual page and CLI; focused tests for cancellation, ownership precedence, unassociated creation, and deletion orphaning.

Dependencies: step 3.

### 6. Add notes tied to the pin and the selected task

Likely files: `internal/work/work.go` (a `notes` table, owner `work`), a `cmd/mtm/notes.go` noun, task and project detail panels, focused tests. Lift the record shape from `~/proj/matomeru-old/internal/notes/notes.go` (id, text, task id, project id, created, updated); do not port its bus, registry, or provenance machinery. Check `~/proj/znbu/docs/outline/knowledge.md` for the contract before building.

A note attaches to the selected task when there is one, otherwise the pinned project, otherwise it is unassociated. The notes page is list/detail like tasks; task and project details show their notes. Notes are scoped by the pin like every other project-owned noun.

Acceptance:
- With A pinned and a task selected, `note new` attaches to that task and A. With no task, it attaches to A. With nothing pinned, it is saved unassociated and listed with that label.
- An empty note saves. Editing or associating a note later never loses its text.
- Deleting a task or project keeps its notes (see "Record first").
- `note new TEXT` from the CLI follows the same rules as the TUI.

Verification: focused store and ownership tests; exercise the notes page, task detail, and CLI against an isolated database.

Dependencies: steps 2 and 5. Independent of steps 7 and 8; build it right after step 5.

### 7. Make scratch launch and explicit persistence work for shells/agents

Likely files: `cmd/mtm/launch.go`, `cmd/mtm/panes.go`, shared work persistence for saved/live associations, focused launch tests.

Expose explicit scratch and directory arguments consistently. Live ownership already exists (step 1). Add a save action that applies to the existing pane and records it under a project, an explicit path, or unassociated. Persist only metadata that can actually be used to reopen supported work. Do not claim to restore arbitrary shell process memory or agent conversations.

Acceptance:
- With A pinned, launch scratch shell and agent, interact, detach, and reattach. Both remain unassociated, and A stays pinned.
- Save without project/path succeeds as an unassociated saved record in mtm-owned storage. Every save survives mtm restart and identifies the same live pane while it exists. Associating it later moves no process.
- Associating scratch work (step 1 `associate`) does not spawn or relocate its process. Existing review history keeps its original launch ownership.

Verification: real daemon launch/input/capture/reattach, inspect persisted associations after reopening mtm, and focused persistence-boundary tests.

Dependencies: steps 2 and 4.

### 8. Add real scratch Vim and saved-session CRUD

Likely files: a small `internal/vim` package lifted selectively from matomeru-old, mtm Vim noun handlers, Panes integration, focused tests.

Start real Neovim in a daemon pane. Reuse terminal attach/detach and scratch launch context. Add explicit session save, inspect, rename/association edit, reopen, and forget. Resume a running session without duplicating it; reopen a closed saved session with its session file and saved cwd.

Acceptance:
- Open Vim with no association despite a pin, type into it, detach, and reattach. No reusable saved session exists until explicit save.
- Save to a project, an explicit session-file path, or neither (unassociated, mtm-owned storage); close the pane, restart mtm, and reopen with the expected cwd and session state. An unwritable explicit path falls back to mtm-owned storage with a notice; the live editor and its buffers are untouched.
- Missing Neovim reports the missing binary. Forget removes mtm's saved registration and only mtm-owned session artifacts, never edited files or the running pane.

Verification: launch the real installed Neovim against an isolated daemon, save/reopen an actual session, inspect the file and cwd, and run focused lifecycle/error tests. If Neovim is unavailable, report that acceptance as unverified rather than substituting a shell.

Dependencies: steps 4 and 7.

## Completion gate

After each implemented step, update the relevant brief/changelog, run `gofmt -l .`, `go vet ./...`, `go test -race ./...`, and `go run ./cmd/mtm --smoke`. Keep `ochazuke.ValidateBindings` coverage. Commit each completed step without amending earlier steps, following the repository's brief.

Use temporary databases/directories and a separate TUIOS server for acceptance. Never run destructive CRUD checks against the user's database, repositories, or running panes.

Final scenario: create a project without a repository; attach two directories; choose a default; pin it; verify every ordinary domain list and detail excludes other projects and unassociated items; browse a different project through the explicit All projects view; open a generic terminal/agent in the pinned default directory; explicitly open scratch Vim; interact; save that editor to a project or path; restart mtm and resume it. Check project/task list-detail CRUD and verify no browse action changed the pin. Switch pins while a preview loads and confirm no stale cross-project information appears.

## Scope limits and proposal defaults

This plan does not port the old bus, registry, UI, forced task pin, repository scanners, remote Place model, or unrelated noun pages. Project-scoped reads and an explicit All projects view are in scope, but do not require porting the old filter implementation. It retains ochazuke as the owner of interaction behavior.

The defaults above are proposals: explicit pin beats cwd/last-launch inference; a pin can be empty; pathless project launches disclose home cwd; deleting referenced records is restricted rather than cascading; an automatically chosen runtime cwd does not qualify as an explicit persistence path. These choices keep ownership and filesystem effects visible.

## Planning verification

Inspected the current app, work-store shapes, launch inference, o-proj registration, old pin implementation, CSV reference consumer, and relevant znbu concepts. Ran `go run ./cmd/mtm --data :memory: --smoke`; it rendered the empty Work page with projects above tasks. No daemon launch, CRUD behavior change, or saved-session implementation was performed during planning.
