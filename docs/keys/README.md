# mtm states reference

`states.json` is the reviewed state/key reference, outside the embedded story
library. It contains introductions (`where`, `what`), page, focus noun, mode,
complete modal stack, and every measured binding row for each state. It has
no recorded frames, replay steps, tutorial chapters, or captions.

The hub's `tools/keymap-outline/tutorials-build mtm` discovers states with the
same sandboxed fixtures as mtm-story, then refreshes this data and exports it.
The `reference` fixture adds task-associated notes to the seeded projects,
tasks, inbox items and fake panes. Discovery never needs a live daemon or PTY.

`make states-export OUT=/tmp/mtm-tutorials/export` reads only `states.json`;
it writes one JSON per state and `index.json` without building or running mtm.
The index counts distinct key/pattern pairs per state separately from binding
rows: several guarded actions can share a key. Rows retain hidden flags,
conditions, request arguments, patterns and fallback information. Labels come
from verb descriptions; legacy aliases use catalog targets declared in the
hub's rules data. Request arguments distinguish variants sharing a label.

The reference is a finite fixture survey. State identity is page, focus noun,
mode and complete modal stack; content variants with that identity collapse.
Shared global contexts are surveyed on Panes, nested modal depth is bounded,
and terminal passthrough, terminal leader, copy mode and companion-pane paths
behind a live terminal are excluded. A binding row describes an available or
conditional action; it does not demonstrate a real daemon, launcher, editor,
clipboard or PTY effect.

The builder preserves authored `where` and `what` by state identity when it
refreshes measured keys. `--overwrite` replaces introductions with drafts.
Export has stable state/key order and no timestamps or fixture paths. Use a
fresh output folder after removing states; the exporter refuses stale files
instead of silently leaving an older reference or replay alongside it.
