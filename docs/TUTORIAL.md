# Matomeru v2 tutorial

Run `mtm --tutorial`. It opens a workspace kept in memory: nothing you do there touches your own data or configuration, and it is gone when you quit (`q`).

The workspace is the lesson. The project **Learn Matomeru** in Work has one subtask per step below; mark each done as you go (`e`, move to the status, `l`).

## 1. Move between the four spaces

Keys: `1 2 3 4 · ] [`

Panes, Work, Runs and Extensions. The footer always shows the mode, where the focus is, and the keys that work there.

## 2. Move the focus

Keys: `j k h l · g g · G · tab`

j and k walk everything in drawing order and spill from one list into the next. h and l change column.

## 3. Open a pane's pages

Keys: `1 · enter · j · enter`

A pane has a site: the live Screen and the pages its agent sent. Enter on the rail goes to the pages, enter on a page opens it.

## 4. Answer the agent's question

Keys: `W · a · space · enter`

W jumps to the next pane that wants you. The unread dot stays until the question is answered. u takes an answer back.

## 5. Read the lane progress bar

Keys: `1 · ctrl+a L`

The line above the pane's pages is its lane: done, running and planned parts. ctrl+a L opens the lane with its timeline.

## 6. Type in the terminal

Keys: `enter on Screen · ctrl+a d · ctrl+a z · ctrl+a esc`

Everything you type goes to the pane. ctrl+a is the leader: d detaches, z zooms, esc sends a real escape, and any normal key works after it.

## 7. Open records and go back

Keys: `2 · enter · j · enter · backspace · L`

A Show page lists a record's props and everything related to it. Enter drills in, backspace goes back, L goes forward.

## 8. Edit every prop, and undo

Keys: `e · i · enter · l · esc · u · ctrl+r`

The form has every field: the schema's, star, archive and snooze, and any extra field the record holds. Each change is written as you make it; u undoes.

## 9. Move a record to another task

Keys: `m`

m picks a task for the focused record: a task moves under it, a note or a run is assigned to it. (None / root) makes a task a project.

## 10. Browse any noun

Keys: `g n`

Every noun has an index: lanes, notes, documents, schedules, runs. Pick one; enter, e, n and A work the same on all of them.

## 11. Filter, sort and group on any field

Keys: `/ · f · g s · g b · o · M`

f, g s and g b offer every field of the rows, shown or not. / types the filter, M changes how it matches.

## 12. Change the settings

Keys: `S`

Theme, density, borders and what the pages show. A change applies at once.

## 13. Rebind a key

Keys: `B · / · r · x · R · u`

Filter with key: verb: mode: scope: conflict:. r reads new keys, x unbinds, R resets, u undoes. The keymap changes as you go.

## 14. Find everything else

Keys: `? · : · T · ' · ctrl+o`

? lists the keys that work here, : runs any command, T is the outline, ' picks a page of any pane, ctrl+o picks a path.

## What is in the workspace

- **Panes**: `guide: start here` (a Start here page, an unread question, a lane progress bar) and `shell: try me` (enter on Screen starts a real shell).
- **Work**: the projects `Learn Matomeru` and `Sandbox project`, with subtasks, notes and a document.
- **Lanes** (`g n`, Lanes): `Tutorial lane` with six child lanes and a timeline of milestones, decisions and a blocker.
- **Runs**: three finished runs and a queued one; `n` in Runs starts a real command.
