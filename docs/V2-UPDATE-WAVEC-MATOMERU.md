# Matomeru v2 Wave C complete: CLI parity, managed storage, outline editing, and live v2 catalogs

Matomeru v2 Wave C landed on local branch `next` in `~/proj/matomeru-next` (`91165a9`) and is now live in `~/bin/m2m` (`~/bin/mato`, 17M).

## What shipped

### 1. Full noun CLI parity (`cmd/mtm/nouns.go`, `cmd/mtm/main.go`)

Every built-in noun (`task`, `pane`, `note`, `document`, `lane`, `schedule`, `run`, `signal`, `directory`, `extension`, `rule`, plus plural/short aliases like `tasks`, `docs`, `notes`) now supports ergonomic subcommands over the exact same SQLite store:

```sh
mato task list --format table
mato task create --title "Cutover check" --field priority=P1
mato note create --title "Findings" --body - < notes.md
mato task edit <id> --field workStatus=active
mato task star <id>
mato task snooze <id> "2026-11-24 09:00"
mato task relate <id> depends_on task:<other-id>
mato binding list --all
mato binding check
mato extension discover
mato extension run o-df2 --version
mato undo
mato redo
```

All existing subcommands (`--tutorial`, `--smoke`, `config export`, `inspect`, `record`, `mcp`, `migrate`) remain intact.

### 2. Data-Flow Encapsulation and global Recents (`data/files.go`, `app/outline.go`)

- **Canonical managed storage:** `data/files.go` establishes one managed storage boundary (`$MTM_FILES_DIR` or `<dirname(dbPath)>/files/{notes,documents,artifacts}/`) using atomic temp+rename writes via `WriteManagedFile`. TUI, CLI (`--managed` or default when a document has body content and no path), and MCP (`data.create_note`) all write through this boundary while keeping external user paths untouched.
- **Global newest-first Recents (`g r`):** Press `g r` anywhere in Normal mode to open the cross-noun Recents picker ordered newest `UpdatedAt` first, with noun badges and relative timestamps. Press `enter` to open any record's Show page.

### 3. Outline (`T`) editing with single-step undo (`app/outline.go`, `data/query.go`, `data/store.go`)

- Tasks now carry an `order` field honored by `Store.TaskTree` (siblings sort by `order` ascending, falling back to creation order).
- Inside Outline (`T`):
  - `J` / `K` (including Vim counts like `2J`, `3K`) reorder the focused task among its siblings.
  - `>` indents the focused task under its preceding sibling.
  - `<` outdents the focused task to become a sibling of its parent.
  - `space` cycles `workStatus` (`todo -> active -> blocked -> done -> todo`).
  - `i` opens quick inline rename on the task title.
  - `u` / `ctrl+r` undo and redo each outline edit in one atomic `ChangeSet` via `Store.PatchMany`.

### 4. Side-by-side v2 extension discovery and live catalog mounting (`ext/ext.go`, `ext/catalog.go`, `app/ext_catalog_test.go`)

- `ext.DefaultTools` now discovers `o-csv2`, `o-json2`, `o-df2`, `o-md2`, `o-cmds2`, `o-lane2`, and `o-questions2` alongside canonical `o-*` names.
- Raised catalog/query stdout limit in `ext/catalog.go` from 64 KB to 32 MB so large live extension queries never truncate.
- `TestLiveV2ExtensionCatalogs` verified real `LoadCatalog`, `MountCatalog`, and `QueryExternal` execution against all five installed `~/bin/o-{df,md,cmds,lane,questions}2` binaries.

## Verification

- `gofmt -l .`: clean
- `heavy nice -n 10 go vet ./...`: passed
- `heavy nice -n 10 go test -p 1 -race -count=1 ./...`: all 12 packages passed
- `~/bin/m2m --smoke`: `smoke: all checks passed` (266us render, 384 renders)
- `~/bin/m2m binding check`: `bindings: 400 total, 0 errors, 157 warnings`

## Active next step

`ochazuke-polish-worker` is running now in `~/proj/ochazuke-next` to deliver:
- Multi-key sort, visual row marking (`V`), cell-select mode, and multi-condition filters (`!term`, `a|b`) in `widgets/table` and `ui/draw_content.go`.
- Scope-aware word boundaries (`code`, `path`, `json`, `csv`, `markdown`) and the styled ANSI copy-mode adapter in `widgets/input`.

Once that lands, we will rebuild `m2m` and all seven `o-*2` binaries onto the polished Ochazuke v2 commit and publish the final cutover readiness report.
