# Daemon events and journal reads

Matomeru owns daemon events, pane snapshots and history persistence. These
changes are in the app, including its `store` package; no framework workaround
is needed. Existing bindings/settings cannot retain event payloads or query
only newly inserted journal rows, so configuration alone cannot fix this cost.

`window-retitled` changes cached panes and their existing tree rows without a
listing, database read, lock, exit probe or refresh job. Terminal titles retain
custom window names; explicit rename/clear events update the name. Titles that
arrive during a listing survive its completion. A title-only stream delivers
its first change immediately and at most one batch per throttle interval
thereafter, including the final title after a burst. Repeated identical titles
skip the row push. Subscription reconnects resume by `(seq, boot_id)` and request
a new snapshot; an explicit `gap` also requests one. Filtered event sequences
are sparse, so numeric jumps alone are not gaps.

Structure events collect in a fixed window. A listing in flight allows only one
pending follow-up, including an explicit reconciliation request. Event and timer
listings do not take the cross-process side lock or ask `Exited` per pane.
Startup, explicit `panes.refresh`, and side operations retain reconciliation and
exit cleanup. Exit events mark their pane exited immediately for the next
listing. Lock acquisition for reconciliation observes job cancellation.

History caches immutable decoded rows by SQLite insertion sequence, independent
of a pane's ring sequence. Every projection queries only rows after its cursor;
late ring reads and capture rows are merged into the same historical order.
Separate writers are discovered by the same query. `LastSequence` is a SQL max
with literal boot/window prefix matching. The cache keeps the whole decoded
journal; it grows with the append-only journal. Turn projection still visits
cached rows, but never decodes old rows again.

History notifications collect before refreshing visible views. Review refreshes
on page open; Turns refreshes when Inbox opens or its tab is selected. The
context picker already refreshes only while open and loads turns on opening.
Periodic pane listings remain enabled to observe daemon and persisted domain
metadata changes that have no event in this subscription.

| Setting | Default | Opt out |
| --- | --- | --- |
| `panes.retitle-in-place` | `true` | `false` reloads on retitles |
| `panes.title-throttle` | `200ms` | `0s` delivers title batches immediately |
| `panes.event-coalesce` | `300ms` | `0s` requests a listing immediately |
| `panes.light-listing` | `true` | `false` reconciles on event/timer listings too |
| `history.event-coalesce` | `300ms` | `0s` refreshes immediately |
| `history.visible-only` | `true` | `false` also refreshes hidden history views |

Single-flight protection applies with coalescing disabled as well. No registered
verbs or binding rows are removed. `panes.refresh` defaults to reconciliation;
its optional `reconcile=false` supports lightweight callers.

## Hidden page investigation

Ochazuke v0.72.1 `Model.pageContent` selects `Runtime.PageOf(Runtime.Page)`.
`Model.render` can render that active page twice to keep focus visible, but does
not iterate through other pages. Matomeru's `App.View` delegates to this model;
`renderPanes` is registered only on Inbox. `parentDetailBlock` is reached through
that page's detail composition. A modal still renders its visible underlying
page as backdrop; that is separate from switching pages.

`TestEventLoadHiddenPageDoesNotRenderPanes` wraps the registered Inbox renderer,
switches to a Questions fixture, delivers twenty retitles and renders each
frame: Inbox's render count stays zero. Returning to Inbox renders its current
state. The supplied historical sample has `Model.pageContent` above
`renderPanes`, which is consistent with an active Inbox (possibly a modal backdrop); the
sample alone cannot establish which page the owner saw at the same instant.
No hidden-page rendering bug was reproduced in this checkout.

## Reuse and comparison

The fixed event window and one-running/one-pending pattern adapt TUIOS's
`internal/app/dock_engine.go`. Title delivery adapts the first/immediate,
throttle/trailing design in `internal/app/sidebar_title_debounce.go`. Resuming
and resynchronizing follows `internal/session/daemon_events.go`. Latest payloads
plus a one-slot wake latch preserve the final state while bounding queued work.
These are adapted MIT-licensed techniques (Gaurav Gosain and TUIOS contributors),
not a wholesale port. The old mtm/gmtm/redeck/znbu/now sources had no fitting
history-cursor implementation in the reuse scan.

Compared with Vim: n/a. This changes background daemon synchronization and
journal query cost, with no new motion, key, mode or editing operation.

Benchmarks use 200 synthetic retitles against a temp App and a 27,473-row
synthetic journal. `before-full` implements the previous full-table JSON
decode/sort; `after-idle` and `after-one-new` exercise the shared decoded
snapshot used by turn projections. These measure local processing, excluding
network latency and an interactive terminal renderer. Regression tests cover
coalescing, no-lock/no-probe listings, one running refresh, late titles,
cancelled side locks, external journal writers and visible-only reviews.
