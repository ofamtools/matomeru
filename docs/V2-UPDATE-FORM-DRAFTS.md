# Explicit form submit and auto-saved persistent form drafts

Landed across `ochazuke-next` (`5629b1b1`) and `matomeru-next` (`858b8cc`) and installed into `~/bin/m2m` (`mato`). All swarm workers are now retired and every `-next` repo is clean on `next`.

## What shipped

### 1. Ochazuke v2 (`~/proj/ochazuke-next`, `5629b1b1`)
- **Focusable `[ Submit ]` row:** Added `form.Model.SubmitRow` + `OnSubmitRow()` and highlighted `▸ [ ⏎ <SubmitLabel> ]` rendering in `ui/draw_content.go`. `CmdSubmit` flushes in-progress text edits from `input.Line` before validating.
- **Pure form drafts ring (`widgets/form/drafts.go`):** Zero-I/O, JSON-serializable `DraftStore` (`Save` with deduplication and `MaxPerKey`, `List`, `Remove`) and `form.Model` cycling (`DraftPrev`, `DraftNext`, `DraftDiscard`, `form.draft.prev`, `form.draft.next`, `form.draft.discard`) preserving live working edits when stepping back to `-1`.

### 2. Matomeru v2 (`~/proj/matomeru-next`, `858b8cc`, `~/bin/m2m`)
- **Explicit submit everywhere (`modalForm`, `modalAnswer`, `modalLaunch`):**
  - Editing fields no longer writes prematurely to SQLite or fires actions. (`Settings` `S` stays live-apply.)
  - Submit from any field (normal or edit mode) with `ctrl+s` (or `ctrl+enter`), or navigate down (`j`/`G`) to the highlighted `[ Create ... ]` / `[ Save ... ]` / `[ Send answer ]` / `[ Run ]` row and press `enter` or `space`.
  - Submission writes one atomic undoable `ChangeSet` (`u` undoes the whole form), deletes the restored draft if one was loaded, confirms in the status bar, and closes the modal.
- **Auto-saved persistent drafts (`app/drafts.go`):**
  - Closing a dirty form with `esc`/`q` never submits; it snapshots the form values into `~/.config/matomeru-next/drafts.json` (in-memory only for tutorial/tests) keyed by form type (`new:pane`, `new:task`, `edit:task:<id>`, `answer:...`, `launch:...`).
  - Reopening starts fresh with header badges (`draft 1/2 · [ / ] · D discard`, `2 draft(s) · [ restores`, `unsaved · ctrl+s submits`).
  - Inside `ModeForm`: `[` restores/steps older drafts, `]` steps forward back to live edits, `D` discards a draft. Zero conflict with Normal-mode space switching (`binding check`: 412 total, 0 errors).

## Verification

- `ochazuke-next`: `gofmt`, `go vet`, `go test -p 1 -race -count=1 ./...` (19/19 packages pass)
- `matomeru-next`: `gofmt`, `go vet`, `go test -p 1 -race -count=1 ./...` (all packages pass)
- `~/bin/m2m --smoke`: `smoke: all checks passed` (297us render)
- `~/bin/m2m binding check`: `bindings: 412 total, 0 errors, 164 warnings`
