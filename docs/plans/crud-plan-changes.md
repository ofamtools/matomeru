# What changes in the pin, tasks and notes plan

2026-10-25. Two independent reviews, by astra (`gpt-6-astra` through omp) and Fable, read `crud-project-context.md` and `crud-parallel-lanes.md` against the code. Nothing is built yet. This note lists what the revision will change and the few choices that are yours.

The goal is unchanged: pin a project, launch terminals and agents into it through TUIOS, and have tasks and notes attach to it automatically. Anything mtm can't associate is saved as unassociated and can be reassigned later.

## The two reviews

### Astra (15 findings)

Astra read the plan mostly as a spec to test against, and was strongest on edge cases and failure paths.
- **Blockers:**
  - the notes table owner would stop mtm opening your database;
  - a no-argument `defaultOwner()` can't express the rules;
  - reviews are both fixed and reassignable;
  - pane and session ownership had no rules for clashing names or for "explicitly unassociated".
- **Failure paths:** ownership lost when listing panes fails after a successful launch. Late launches and launches into another project while one is pinned need defined behavior. Deleting a project or task was specified before the notes and saved records it would affect exist.
- **Record first:** found the code that still refuses (empty project name, unknown ids at launch, blank titles passed on the command line, a test that requires the refusal). It also found that unassociated tasks can't be reached once saved.
- **Schedule:** lanes overlap far more than "two small conflicts"; an integration owner is needed. Lane A can't finish the pin with scoping deferred. The per-commit race-test rule was loosened by mistake.
- **Scope:** drop the generic `links` table; saved shells and agents have no way to reopen.

### Fable (13 findings)

Fable checked the plan against your actual database and the ochazuke rules, and was strongest on concrete shortcuts and cuts.
- **Blockers:** your database already declares `notes` (owner `notes`) and `vim_sessions` (owner `vim`), so the plan as written would stop mtm from starting.
- **Framework conflict:** a right-panel draft that asks "save or discard?" traps the cursor, which ochazuke forbids, and contradicts "drafts save with whatever was entered". Edit in a modal instead.
- **Record first:** listed each refusing function with its line number and asked for one step to remove them all before the lanes start. It also asked for `former_project_id` / `former_task_id` fields so deleted owners leave a trace.
- **Reuse:** the old `assignments` table and existing reviews already record pane owners; import them. Key panes by TUIOS window id. Show the pin in the app title.
- **Lanes:** notes in the detail panels collide with lane B; lanes A and B both edit the `project` verbs, so split them into `pin.go` and `project.go`. Step 2 is quietly split across waves.
- **Cuts:** steps 7 and 8 (saved shells, agents and Vim sessions) and editing or removing directories are over-built for the goal. "All projects" can be a toggle instead of a separate page.
- **Tests:** several acceptance lines can't be tested as written ("search" doesn't exist; "without parsing session names" isn't observable).

### Where they agree and differ

Both found:
- the notes table blocker;
- the review contradiction;
- the `defaultOwner()` problem;
- the code that still refuses;
- the lane overlap;
- that the generic `links` table is too much.

Astra alone covered launch failure paths and pinned-scope landing. Fable alone covered the cursor trap, importing old pane owners, and most of the scope cuts. They don't contradict each other anywhere.

## Would have broken things

**Notes and Vim would stop mtm from starting.** Your database already has `notes` (owner `notes`) and `vim_sessions` (owner `vim`) from the old app. The plan declared notes with owner `work`, and the store refuses a mismatched owner, so mtm would fail to open your database. *Change:* declare both tables exactly as the old app did, and keep the old notes readable. New acceptance check: mtm opens a copy of your real database and lists your existing notes. Both reviewers found this.

**Ownership could be lost right after a launch.** Today mtm records an agent's details only after it lists panes. If listing fails, the pane runs with nothing recorded. *Change:* record the owner as soon as TUIOS confirms the pane was created. Found by astra.

**Reviews contradicted themselves.** The plan said reviews keep their launch-time owner, but also that `associate` and deletion change it. *Change:* a review is history and keeps its owner. Reassigning a pane shows on the review's detail page by looking up the pane's current owner; the review row is never rewritten. Both reviewers found this.

## "Record first" becomes true in the code first

The code still refuses things the rule says it shouldn't:
- creating a project with no name;
- creating a task with no project or no title, or renaming a task to an empty title;
- `task.new` with no project open;
- launching an unassociated task;
- jumping to an unassociated task (`--target`).

No step owned fixing these. *Change:* a new **step 0** removes those refusals first, so every lane builds on a store that already records first. Step 0 also:
- adds a list of unassociated tasks;
- sends an unassociated task's launch to the `no-project` session;
- gives a nameless project a display name such as "untitled".

Both reviewers found this.

## How ownership works now

- **A smaller ownership table.** Ownership stays a field on each record (`project_id`, plus `task_id` where it fits). The new table holds only **pane and session ownership**, the one thing mtm doesn't store itself. Many-to-many (one note on two tasks) waits for a real need. Both reviewers recommended this.
- **Keyed by stable ids.** A pane is keyed by its TUIOS window id and a session by its session id, with the name kept for display. "Explicitly unassociated" is stored differently from "follows its session", so unlinking a pane can't make it quietly inherit an owner again.
- **Your existing panes keep their owners.** The old database already records which pane belongs to which task and project, and so do agent reviews. Step 1 imports both once, so existing panes don't all show up as unassociated. Found by Fable.
- **A record of what something used to belong to.** When a project or task is deleted, its children keep `former_project_id` / `former_task_id` fields. Found by Fable.
- **One rule decides who owns something new.** The `defaultOwner()` sketch in the parallel plan let the project you're browsing beat the pin, which breaks the pin rule. *Change:* one owner resolver, called at the moment an action runs:
  - explicit arguments or the selected record first;
  - then the pin;
  - otherwise unassociated.

  An explicit scratch launch always comes out unassociated. The command line uses arguments or the pin, never what's selected in a running TUI. Both reviewers found this.

## Pin and views

- **The pin shows in the app title** ("Matomeru · A"). The framework has no other place for an app to draw it.
- **"All projects" becomes a toggle, not a separate page.** With nothing pinned, the lists already show everything, labeled by owner.
- **Reaching things outside the pin.** You can target an unassociated task, or a project B task while A is pinned. Doing so doesn't move the pin or widen the normal lists.
- **Launching into B while A is pinned.** The new pane opens and is clearly labeled as outside the pin. A launch that finishes after you've switched pins only notifies you; it doesn't yank you to another project.

## Editing (your call, see below)

The plan put draft editing in the right-hand panel and asked "save or discard?" when you moved away from an unsaved edit. Fable pointed out that this traps the cursor, which ochazuke forbids (only a modal may hold keys). It also contradicts "drafts save with whatever was entered".

*Proposed:* create and edit open in a modal. Enter saves, Esc cancels, and the right panel only shows details. There's then no dirty-draft state and no narrow-screen switch to maintain.

## Trimmed scope

- **Steps 7 and 8** (saved shells, agents and Vim sessions) move to a follow-up plan. Step 7 keeps only the explicit "scratch" choice on launch. Neither is on the pin → launch → tasks → notes path. Fable called them over-built; astra noted step 7 had no way to reopen anything.
- **Directories:** keep "add a directory" and "set default"; editing and removing directories come later.
- **Deletion** is built noun by noun as each noun arrives, so step 3 doesn't promise to orphan notes before notes exist.

## The parallel schedule, corrected

- **Step 0 merges before anything starts**: the owner resolver plus the refusal removals.
- **Lane A:** pane and session ownership, the pin and its verbs (in `pin.go`), the title indicator, the scoping helper.
- **Lane B:** project and task editing, with the project verbs in `project.go` so lanes A and B don't both edit the same `Define` block.
- **Lane C:** the notes table, noun, page and command line only, without the task and project detail panels.
- **Integration lane:** waits for A, B and C. It owns the app wiring, the scoped lists, notes inside task and project details, deletion, and the final end-to-end scenario.
- **Step 2 is split honestly:** 2a, the pin itself, lands in lane A; 2b, filtering lists by the pin, lands in integration.
- **The full `go test -race ./...` runs on every commit**, as AGENTS.md requires. The parallel plan had loosened that by mistake.
- **No lane can touch your real database.** Every lane's run script passes `--data` pointing at a temporary database.

## Acceptance checks that couldn't be tested as written

These get rewritten to something a test can see:
- "without parsing session names" becomes: launch into a session whose name is unrelated to the project, and the owner still reads A.
- The "search" checks are dropped; mtm has no search.
- "asynchronous detail load" names the two real cases: pane preview and review capture.
- "wide and narrow widths" goes away along with the panel editor.
- Note ownership is spelled out case by case: nothing selected, an A task, a B task, an unassociated task, scratch.

## Choices that are yours

1. **Editing in a modal instead of the right panel.** Recommended: modal.
2. **Moving saved shells, agents and Vim sessions (steps 7 and 8) to a follow-up plan.** Recommended: yes.
3. **Allowing nameless projects**, saved and shown as "untitled". Recommended: yes, under "record first".
4. **Importing the old database's pane assignments** so existing panes keep their owners. Recommended: yes.

Once you've picked, I revise both plan files, get one quick confirming pass from a reviewer, and start step 0.
