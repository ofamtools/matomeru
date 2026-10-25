# Parallel lanes for the CRUD, pin, and notes plan

Status: proposed schedule, 2026-10-25. No code yet. Executes `docs/plans/crud-project-context.md` (steps 1 to 8); this note only decides what runs at the same time and how it merges.

Goal of the slice: pin a project; launch terminals and agents through TUIOS into it; tasks and notes attach to the pin automatically; anything mtm cannot associate is saved unassociated and can be reassigned later ("Record first" in the main plan).

## What depends on what

| Step | Needs | Why |
|---|---|---|
| 1 Ownership and links (panes, sessions, `associate`) | nothing | the base every list scopes on |
| 2 Pin | 1 | scoping reads owner fields and links |
| 3 Project CRUD | nothing hard; 2 for pin-clear on delete | screens and store only |
| 4 Trees (directories, default) | store side: nothing; screens: 3 | the editor lives in the project detail |
| 5 Task CRUD | 3 (follows the project editor's pattern) | default owner comes from `defaultOwner()` |
| 6 Notes | 1 (owner fields), 5 (task detail shows notes) | default owner comes from `defaultOwner()` |
| 7 Saved shells and agents | 2, 4 | needs pin and default directory |
| 8 Vim sessions | 4, 7 | wiring only; the package lift is independent |

The only coupling between lanes is the default-owner choice for new records. All lanes call one function, `defaultOwner()` in `cmd/mtm`, which returns `{ProjectID, TaskID}` (both may be empty: unassociated). Lane A lands it first with today's behavior (selected row, then the browsed project, then unassociated; no cwd or last-launch guess), and later points it at the pin. Lanes B and C never decide ownership themselves.

## Wave 1: three lanes at once

| Lane | Steps | Main files | Merge order |
|---|---|---|---|
| A, the core | 1, then 2 | `internal/work/work.go`, `internal/work/links.go`, `cmd/mtm/launch.go`, `cmd/mtm/panes.go` | first |
| B, editing | 3, then 5 | `cmd/mtm/main.go`, new project and task editor files, `cmd/mtm/tasks.go` | second |
| C, new data | 6 (table, noun, page), and the store side of 4 (tree mutations in the work store, o-proj moved onto them) | `cmd/mtm/notes.go`, tree functions in `internal/work/work.go`, `cmd/o-proj` | third |

Lane A's first commit is `defaultOwner()` with its current behavior, merged to main immediately, so B and C start from it.

## Wave 2: one lane after A merges

- Pin scoping in the project, task, and notes lists and details; deleting a pinned project clears the pin.
- Tree screens (step 4 UI) in the project detail from lane B.
- The main plan's final scenario on an isolated TUIOS server, then one full suite.

## Background, any time

- Step 8's Neovim package lifted from `~/proj/matomeru-old` into `internal/vim`, no wiring. Wiring waits for step 7.
- Step 7 itself follows wave 2.

## Checkouts and merging

- Each lane works in its own plain clone of `~/proj/matomeru` (`~/proj/matomeru-lane-a`, `-b`, `-c`), on branch `lane/a`, `lane/b`, `lane/c`. No new git worktrees (standing rule). The local `replace` of `../ochazuke` must resolve from each clone; the clones sit beside `~/proj/ochazuke` so the relative path holds.
- Commit after each step; no amends. Rebase onto main before merging; fast-forward merge in the order A, B, C. Expected conflicts: the `Tables` list in `internal/work/work.go`, page registration in `cmd/mtm/main.go`. The planner resolves them.
- Each lane runs targeted tests for what it touched plus `gofmt -l .`, `go vet ./...`, `go run ./cmd/mtm --smoke`. The full `go test -race ./...` runs at each merge and once at the end of wave 2.
- Acceptance uses temporary databases and a separate TUIOS server; never the owner's database, repositories, or panes.

## Risks

- Lane B and C assume `defaultOwner()`'s signature does not change when the pin lands. If step 2 needs more (for example, an explicit scratch flag), the change goes through lane A and B/C rebase.
- Lane C's tree mutations and lane B's project editor both touch `Project`; B must not write `Trees` or `DefaultTree` directly, only through C's store functions once merged (until then, B leaves trees read-only).
- Pane ownership (step 1) is new behavior on live daemon data; panes started before it ship as unassociated, which is correct under "Record first" but will look empty until reassigned.
