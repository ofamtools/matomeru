# mtm tutorials

One tutorial per measured state with distinct actions. Each chapter resets the temporary database and replays its setup, so edits never carry into the next chapter. The Where / What fields describe the reset point; Mode and Keys come from its checked frames and events. Panes, launches, clipboard, editors and daemon calls are faked; live PTY states are outside this survey.

## Agents and panes

- [Choose and manage panes](start.json) — Select a pane, inspect its output, and manage the agent or shell. (42 chapters)
- [Panes: pane list (filter)](state-004.json) — Explore the actions available in pane list. (1 chapters)
- [Panes: task outline · unassociated tasks (normal)](state-013.json) — Explore the actions available in task outline. (5 chapters)
- [Panes: pane list (row selection)](state-014.json) — Explore the actions available in pane list. (1 chapters)
- [Panes: inbox items (normal)](state-016.json) — Explore the actions available in inbox items. (13 chapters)
- [Panes: confirmation · confirmation (normal)](state-020.json) — Confirm or cancel an action on temporary fixture data. (3 chapters)
- [Panes: copy history · copy history (normal)](state-021.json) — Inspect copied text and choose an entry to reuse. (23 chapters)
- [Panes: prompt box (insert)](state-022.json) — Write a prompt and choose how to send it. Delivery is faked in this tutorial. (5 chapters)
- [Panes: rename form · record field (insert)](state-024.json) — Move through record fields and edit their values; changes stay in the temporary database. (2 chapters)
- [Panes: list (filter)](state-033.json) — Explore the actions available in list. (8 chapters)
- [Panes: quick note · note text (normal)](state-041.json) — Explore the actions available in quick note. (1 chapters)
- [Panes: directory picker · choices (insert)](state-042.json) — Explore the actions available in directory picker. (2 chapters)
- [Panes: task outline · outline text (insert)](state-047.json) — Explore the actions available in task outline. (2 chapters)
- [Panes: task outline · list (normal)](state-050.json) — Explore the actions available in task outline. (3 chapters)
- [Panes: task outline · task outline (normal)](state-051.json) — Navigate and change the task outline in the temporary workspace. (20 chapters)
- [Panes: task outline · project outline (normal)](state-052.json) — Choose a project and organize its tasks in the outline. (3 chapters)
- [Panes: inbox items (row selection)](state-061.json) — Explore the actions available in inbox items. (1 chapters)
- [Panes: context picker · choices (insert)](state-084.json) — Explore the actions available in context picker. (2 chapters)
- [Panes: prompt box (normal)](state-085.json) — Write a prompt and choose how to send it. Delivery is faked in this tutorial. (38 chapters)
- [Panes: list (normal)](state-089.json) — Explore the actions available in list. (1 chapters)
- [Panes: prompt box (row selection)](state-148.json) — Write a prompt and choose how to send it. Delivery is faked in this tutorial. (5 chapters)
- [Panes: prompt box (operator g pending)](state-198.json) — Write a prompt and choose how to send it. Delivery is faked in this tutorial. (1 chapters)

## Projects and work

- [Choose a project](state-018.json) — Select a project and work with its tasks. (7 chapters)
- [Work: unassociated tasks (normal)](state-068.json) — Explore the actions available in unassociated tasks. (1 chapters)
- [Manage tasks](state-071.json) — Open, edit, and organize tasks in the selected project. (25 chapters)
- [Work: list (normal)](state-131.json) — Explore the actions available in list. (1 chapters)
- [Work: record field (normal)](state-140.json) — Move through record fields and edit their values; changes stay in the temporary database. (5 chapters)

## Notes and inbox

- [Write a quick note](state-010.json) — Record a note in the temporary workspace. (20 chapters)
- [Notes: notes (normal)](state-040.json) — Explore the actions available in notes. (4 chapters)
- [Notes: note lines (normal)](state-092.json) — Read and edit a note, copy text, and organize its lines. (36 chapters)
- [Notes: markdown edit (insert)](state-161.json) — Explore the actions available in markdown edit. (11 chapters)
- [Notes: list (normal)](state-199.json) — Explore the actions available in list. (1 chapters)
- [Inbox: detail link (normal)](state-221.json) — Explore the actions available in detail link. (1 chapters)
- [Inbox: list (normal)](state-268.json) — Explore the actions available in list. (1 chapters)

## Review results

- [Review: answer (normal)](state-138.json) — Explore the actions available in answer. (3 chapters)
- [Context: list (normal)](state-202.json) — Explore the actions available in list. (2 chapters)
- [Context: context search (insert)](state-228.json) — Explore the actions available in context search. (1 chapters)

## Workspace tools

- [Directories: directory browser (normal)](state-094.json) — Explore the actions available in directory browser. (1 chapters)
- [Schedules: schedule (normal)](state-142.json) — Explore the actions available in schedule. (6 chapters)
- [Directories: dir search (insert)](state-167.json) — Explore the actions available in dir search. (1 chapters)
- [Directories: picker · command input (insert)](state-169.json) — Explore the actions available in picker. (8 chapters)
- [Directories: directory path (normal)](state-204.json) — Explore the actions available in directory path. (3 chapters)
- [Directories: directory path (insert)](state-230.json) — Explore the actions available in directory path. (1 chapters)
- [Schedules: schedule editor · form choice (normal)](state-251.json) — Explore the actions available in schedule editor. (2 chapters)
- [Schedules: schedule editor / field choices · form option (normal)](state-267.json) — Explore the actions available in schedule editor / field choices. (1 chapters)

## Navigation

- [Choose a page](state-002.json) — Find a workspace page and open it. (6 chapters)
- [Browse settings](state-003.json) — Find settings, inspect values, and change temporary preferences. (5 chapters)
- [Run a command](state-006.json) — Find a noun and verb, fill its arguments, and run it. (7 chapters)
- [Find a key](state-007.json) — Inspect the keys available in this context. (5 chapters)
- [Settings: settings section (normal)](state-028.json) — Explore the actions available in settings section. (4 chapters)
- [Settings: setting editor · setting value (insert)](state-029.json) — Explore the actions available in setting editor. (2 chapters)
- [Settings: list (normal)](state-153.json) — Explore the actions available in list. (7 chapters)
- [Vim-Sessions: list (normal)](state-165.json) — Explore the actions available in list. (1 chapters)

Generated from measured bindings with keymap-outline. Stable action ids deduplicate aliases and app-wide bindings, including hidden rows. Existing authored tutorials are preserved unless overwrite is explicitly requested.
