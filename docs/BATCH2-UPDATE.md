# Matomeru v2 daily-driver update (`64e6964`)

Everything below is committed on `matomeru-next` (`next` at `64e6964`) and installed in `~/bin/m2m` (symlinked as `~/bin/mato`).

## Focused-record actions picker (`;`)

- Command: `record.actions`
- Default key: `;` (unshifted twin of `:` for the global palette; free across all modes and tools).
- Opens `Actions · <record title>` containing only commands that apply to the focused record, without spaces, motions, list controls, settings, or other global commands.
- Driven by `CommandSpec.Meta` (`record`, optional `kinds`, optional `when`) evaluated against the focused record's live facts:
  - Offers `record.unstar` instead of `record.star` when starred, `record.restore` when archived, `record.unsnooze` when snoozed, and `open.target` / `open.with` only when the record is openable.
  - Lists noun-specific actions first (`run.rerun` and `run.cancel` on runs, `triage.rule.from-signal` on signals, `extension.run` on extensions, `lane.show` on panes and lanes), followed by universal record actions plus `record.new note` and `record.new subtask` (on tasks).
  - Surfaces unbound record commands as well as bound ones (showing key badges when bound).
  - When focused on an empty Show-page section, puts `record.new.here` at the top.

Files:
- `app/record_actions.go`
- `app/bindings.go`
- `app/app.go`

## Focusable empty Show-page sections

- Empty related sections (`Schedules [0]`, `Lanes [0]`, `Panes [0]`, `Documents [0]`, `Notes [0]`, `Runs [0]`, `Directories [0]`, `Signals [0]`) are focusable with `j`/`k` and display `none · n adds` when creatable.
- Pressing `n` on an empty section opens the create form for that section's noun pre-filled with the Show page's record:
  - `Schedules [0]` opens New Schedule with `task` pre-filled.
  - `Notes [0]` opens New Note linked via `about`.
  - `Directories [0]` opens the path picker attached to the record.
- Pressing `enter` on an empty section reports `empty section · press n to add`.

Files:
- `views/show.go`
- `data/schema.go`
- `app/app.go`

## Tidy Show-page breadcrumbs

- When Show-page history exceeds 4 entries, the breadcrumb bar collapses the middle to `Tasks › … › prev › current` while keeping full `H`/`L` history intact.

Files:
- `app/view.go`

## Batch 2 daily-driver pack

1. **Yank picker and macros (`app/registers.go`)**
   - `y` opens a picker listing every value of the focused record with a live preview; `y y` copies the primary value immediately into Vim registers and the system clipboard.
   - `q <letter>` records keys into a macro register; `@ <letter>` (and `@ @`) replays it.
   - Quit moved from `q` to `Q` (`ctrl+c` still quits) so `q` can start macro recording.
2. **Global scratches (`app/scratch.go`)**
   - `ctrl+a S` toggles the global scratch terminal and `ctrl+a V` toggles the global scratch notes editor over the current space.
   - Stored as regular `pane` records with `role` (`scratch-term`, `scratch-vim`), alongside per-pane side terminals and editors (`side-term`, `side-vim`).
3. **Data-driven file handlers and built-in Markdown reader (`handlers/handlers.go`, `app/open.go`)**
   - `o` opens what the focused record points at (file, URL, directory, note, or document) using the first matching rule in `handlers.json`.
   - Markdown opens inline in the built-in reader with a toggleable heading TOC (`t`, `]`/`[`), link navigation (`tab`/`enter`), and code-block yank (`y`).
   - `O` opens the handler picker to choose any matching handler or copy the path.
4. **Actions and modes inspector (`app/inspect.go`, `cmd/mtm/main.go`)**
   - CLI: `mato inspect actions|bindings|modes|schemas [--json]`.
   - In-app: `: inspect actions`, `: inspect modes`, or via `g n`; press `a` on an action to bind it to a free key using `FindFreeBinding`.
5. **Signals and triage rules (`triage/triage.go`, `mcpui/triagetools.go`)**
   - Adds `signal` and `triage.rule` schemas with an `expr`-based rules engine.
   - `R` on a focused signal drafts a rule from it and applies it to unreviewed signals.
   - Exposes `triage.ingest` and `triage.evaluate` MCP tools.
6. **Extension catalog and read cache (`ext/catalog.go`, `data/cache.go`)**
   - Formal extension manifest protocol and shadow records for local overrides on external items, backed by an async `data.ReadCache`.
7. **Opt-in `mtmd` `DaemonHost` (`ptyhost/daemon.go`)**
   - Persistent remote PTY host behind `"terminal.daemon": true` with injectable exec support and `ListRemotePanes`.

## Verification

- `gofmt`, `go vet ./...`, and `go test -p 1 -race -count=1 ./...` pass across all 12 packages (including `TestShowEmptySectionNewAndBreadcrumbs` and `TestRecordActionsPicker` in `app/daily_test.go`).
- Rebuilt `~/bin/m2m` (`~/bin/mato`) and verified `mato --smoke` passes (all checks green, 267us render).
