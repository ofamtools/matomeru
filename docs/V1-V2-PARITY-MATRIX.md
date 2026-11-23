# V1 vs v2 parity matrix (groomable checklist)

Snapshot date: 2026-11-23. Audited against local source trees:

- Ochazuke v1 (`/Users/unixsuperhero/proj/ochazuke`, ~172k LOC) vs v2 (`/Users/unixsuperhero/proj/ochazuke-next`, ~14k LOC)
- Matomeru v1 (`/Users/unixsuperhero/proj/matomeru`, ~95.5k LOC) vs v2 (`/Users/unixsuperhero/proj/matomeru-next`, ~15k LOC)
- Extensions: 14 `o-*` v2 migrations (`*-next` worktrees) vs 5 skipped v1 tools (`o-koe`, `o-qbt`, `o-slack`, `o-mcp`, `o-inventory`)

## How to groom this document

- Features already at `Parity` or `Improved in v2` are summarized in tables at the top of each section.
- Every `Partial` or `Missing in v2` item is listed below as a groomable checklist with four options:
  - **Keep (bring to v2)**: must reach parity before or right after cutover.
  - **Drop (intentionally leave behind)**: intentionally retired in v2 (do not port).
  - **Later (post-cutover backlog)**: nice-to-have; defer to post-cutover backlog.
  - **Discuss (review details before deciding)**: need more context, tradeoffs, or a walkthrough before deciding.

---

## 1. Ochazuke v2 (core, UI, widgets, CLI)

### Already at parity or improved in v2

| Feature | v2 status | Summary |
| --- | --- | --- |
| Modal engine, trie key dispatch, layers, counts | Improved in v2 | Pure stdlib `trie/`, `expr/`, `core/` with declarative `when` guards, `ExcludeTags` leader mirroring, zero Bubble Tea coupling in core |
| Registers and keyboard macros | Parity | `core/registers.go` and `core/macros.go` implement named registers, `q`/`@`/`@@` replay |
| Layout solver and cell buffer renderer | Improved in v2 | Deterministic `layout/` constraint solver, `buffer/` ANSI cell diffing, `ui/` `ViewNode` tree, 0-redraw idle coalescing in `tea/shell.go` |
| Themes, density, and border styles | Improved in v2 | 5 built-in presets, `compact`/`balanced`/`spacious` density, `rounded`/`sharp`/`minimal`/`none` borders, live `themes.json` overlay |
| Config formats (JSONC + JSONL) | Improved in v2 | `config/` supports JSONC comments/trailing commas and JSONL export/import |
| Table widget (`widgets/table`) | Improved in v2 | 100k-row perf verified; multi-key sort, multi-condition filter syntax (`!=`, `a\|b`), `V` visual range, cell select, grouping, marks |
| Form widget (`widgets/form`) | Improved in v2 | `ChoiceResolver` dependent fields, explicit Submit (`ctrl+s` / `[ Submit ]`), persistent auto-saved form drafts (`[`/`]`/`D`) |
| Input widget (`widgets/input`) | Improved in v2 | Full Vim motions/operators/objects/surround, 14 Readline actions + cursor placement, styled ANSI `CopyMode` (`widgets/input/copymode.go`) |
| Picker and path picker (`widgets/picker`, `widgets/pathpicker`) | Parity | Both widgets exist with Readline editing, hidden-file toggle, parent/child navigation |
| Command palette (`widgets/palette`) | Improved in v2 | Always-visible live argument form on the right while browsing commands; `tab`/`right` focuses form |
| Command line (`widgets/commandline`) | Improved in v2 | Supports `:`, `/`, `?`, `!`, `\|`, `r!`, `ctrl+r <reg>` register paste, `$VAR` expansion, history, Tab completion |
| Markdown reader (`widgets/markdown`) | Improved in v2 | Pure v2 `widgets/markdown` with headings TOC (`t`), link navigation (`tab`/`enter`), code-block yank (`y`), link back stack |
| Diff viewer (`widgets/diff`) | Parity | `widgets/diff` provides unified, side-by-side, and JSON diff rendering |
| Binding editor and `?` help (`widgets/bindings`) | Parity | `widgets/bindings` has live rebind/unbind/reset/undo/redo, conflict analysis, `FindFreeBinding`, and `?` help |

### Groomable items (Ochazuke v2)

- **Input reference triggers (`@file`, `$skill`, `/command`)** (`Missing in v2`): v1 `internal/compose` provided inline completion popups inside prompt boxes for files, skills, and commands; not yet implemented in `widgets/input` or Matomeru v2 prompt inputs.
  - [x] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [ ] Discuss (review details before deciding)

- **Configurable dot-repeat tag set** (`Partial`): v1 `.` repeated the last editing command with tag filtering; basic edit repeat exists in `widgets/input`, but the global `core` repeatable-tag set is not yet wired.
  - [ ] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [x] Discuss (review details before deciding)

- **CLI bridge (`ochazuke/cli` + `oden` v2)** (`Partial`): `ochazuke-next/cli/` bridges `oden` v2 (`AppInfo`, `binding check`, `capabilities`, TTY `PickerFunc`), but does not include v1's Unix-socket host proxy (`host_serve`/`host_proxy`) or generic `cli/mcp` auto-wrapper.
  - [ ] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [x] Discuss (review details before deciding)

- **Tree guide lines (`├─`, `└─`, `│`)** (`Missing in v2`): indentation works in `ui.KindTree` and Outline, but box-drawing connector lines are not yet rendered.
  - [x] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [ ] Discuss (review details before deciding)

- **Optional widgets (`kanban`, `timeline`, `toast`, `jsontree`, `grid`)** (`Missing in v2`): v1 had `grid/`, `jsontree/`, `sysstat/`, and a toast/notice strip; deferred in v2 (`o-json2` handles JSON tree flattening; `widgets/kanban`, `widgets/timeline`, `widgets/toast` are not built yet).
  - [ ] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [x] Discuss (review details before deciding)

- **Focus jump hints over `FocusOrder`** (`Missing in v2`): `tab`/`shift+tab` cycle focus; visual jump-label hints over `layout.Solved.FocusOrder` are not yet built.
  - [x] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [ ] Discuss (review details before deciding)

- **Static architecture/rule checkers (`rulecheck`, `namecheck`, `lexiconcheck`, `offercheck`, `refusecheck`, `docgen`, `o-story`)** (`Missing in v2`): v1 had 10+ lint/audit packages and `cmd/docgen`, `cmd/o-story`; replaced in v2 by lean `deps_test.go` layer checks, `binding check`, and `--smoke` scripted verification.
  - [ ] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [x] Discuss (review details before deciding)

---

## 2. Matomeru v2: spaces and navigation

### Already at parity or improved in v2

| Feature | v2 status | Summary |
| --- | --- | --- |
| Top-level spaces (`1`-`4`, `[`/`]`) | Improved in v2 | Consolidated into 4 clean spaces (`1 Panes`, `2 Work`, `3 Runs`, `4 Extensions`) plus `g n` to browse any noun index in Work |
| Panes space (`1 Panes`) rail, site, TOC (`t`), zoom (`ctrl+a z`), attention (`W`) | Improved in v2 | Per-pane multi-page site (Screen, Questions, Decisions, Plan, Response, custom), toggleable TOC rail (`t`, `panes.showTOC`), `n` opens New Pane directly, `ctrl+a L` Ambient Lane Progress Bar, `panes.railAutoWiden` |
| Global scratches (`ctrl+a S` / `ctrl+a V`) and side panes (`ctrl+a t` / `ctrl+a v`) | Parity | Implemented via pane roles (`scratch-term`, `scratch-vim`, `side-term`, `side-vim`) over any space |
| Work space (`2 Work`) dynamic Index / Show / Edit pages | Improved in v2 | Schema-driven Index, Show, and Edit views for all nouns; focusable empty Show-page relation sections (`j` -> `n`); tidy breadcrumbs; `H`/`L`/`backspace` Show-page history |
| Runs space (`3 Runs`) | Improved in v2 | Real OS command runner (`runs/`), live output tail, `n` new run, `r` rerun, `x` cancel, extension runs recorded automatically |
| Extensions space (`4 Extensions`) | Improved in v2 | `R` discovers tools (`capabilities --json` + `catalog` + `query`), async `ReadCache` + lazy shadows, mounted nouns browsable via `g n` |
| Outline overlay (`T`) | Improved in v2 | Global `T` overlay with `J`/`K` sibling reorder (with counts like `2J`), `>`/`<` indent/outdent, `space` status cycle, `i` inline rename, `u`/`ctrl+r` undo/redo |
| Recents picker (`g r`) and Pages picker (`'`) | Improved in v2 | `g r` global newest-first Recents picker across all nouns; `'` picks any page of any pane |
| Command Palette (`:`) with live args form | Improved in v2 | Powered by `widgets/palette`; live argument form stays visible on the right as you browse commands |
| Focused-record actions picker (`;`) and yank picker (`y` / `y y`) | Improved in v2 | `;` shows all applicable actions for the focused record; `y` lists every field value as it will be copied, `y y` yanks primary value immediately |
| Shell I/O redirection (`!`, `\|`, `g !`) | Improved in v2 | `!` runs shell command, `\|` pipes focused record JSON/text to shell, `g !` (`r!`) captures shell stdout into a new note; supports `ctrl+r <reg>` and `$VAR` expansion |
| Open target (`o`) / Open with (`O`) + `handlers.json` | Parity | `handlers/handlers.go` routes files/URLs/dirs via `handlers.json` or opens Markdown inline in `ModeReader` |
| Signals and triage rules (`signal`, `triage.rule`, `R`) | Improved in v2 | Generalized `signal` + `triage.rule` using `ochazuke/expr` expressions (`mark_noise`, `mark_actionable`, `snooze`, `tag`, `relate_task`), `R` creates rule from focused signal |

### Groomable items (Matomeru spaces and navigation)

- **Pane rail grouping and sorting (by project, status, `promptedAt`)** (`Partial`): human-only `promptedAt` ordering and `W` attention jump are implemented; v1's multi-mode rail grouping (`panes_group.go`, `panes_tree.go`, `rail_triage.go`) and custom rail icons are simplified in v2.
  - [x] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [x] Discuss (review details before deciding)

- **Built-in prompt composer box (`prompt_box.go`, `prompt_attach.go`, `prompt_drafts.go`)** (`Missing in v2`): v1 had a dedicated Matomeru prompt box with `@file`/`$skill`/`/cmd` autocomplete, attachments, and draft queue before sending to an agent pane; in v2 you attach directly to the live terminal (`enter`/`i`) and type into the harness (form drafts exist for record forms only).
  - [x] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [ ] Discuss (review details before deciding)

- **Saved filters / views per noun (`saved.go`, `saved_store.go`)** (`Missing in v2`): v1 persisted named filter/sort/group presets per noun; multi-condition filter syntax (`!=`, `a|b`) works in `widgets/table`, but saving/reloading named filter views is not yet wired in Matomeru v2.
  - [x] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [ ] Discuss (review details before deciding)

- **Context items harvesting (`internal/contextitems`, `context_extract.go`, `context_follow.go`)** (`Missing in v2`): v1 auto-extracted URLs, file paths, PRs, OSC-8 hyperlinks, and commands from pane scrollback to attach to tasks; `RelContext` relation exists in v2 schema, but automatic scrollback harvesting/scraping and `context_picker` are not built yet.
  - [x] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [ ] Discuss (review details before deciding)

- **Link/path hints overlay (`internal/hints`)** (`Missing in v2`): v1 showed visual hint labels over URLs/paths visible in the pane terminal to jump/copy/open; not yet implemented in v2 terminal view.
  - [x] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [ ] Discuss (review details before deciding)

- **Reviews noun and workflow (`reviews.go`, `review_store.go`)** (`Missing in v2`): v1 had dedicated Review records for PR/code review tracking; not a built-in schema in v2 (can be modeled via `signal`, `task`, or `o-gh2` catalog).
  - [ ] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [ ] Discuss (review details before deciding)

- **Reminders / schedule firing and desktop notifications (`internal/reminders`, `internal/schedules`, `internal/notify`)** (`Partial`): `schedule` and `schedule.occurrence` schemas exist in v2 `data/` and v1 reminders/schedules migrate into them, but no background cron evaluator or macOS desktop notification daemon runs yet.
  - [x] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [ ] Discuss (review details before deciding)

- **Privacy mode / redaction (`privacy.go`, `internal/work/privacy.go`)** (`Missing in v2`): v1 had a toggleable privacy filter to hide private projects/tasks/panes during demos or screen sharing; not implemented in v2.
  - [ ] Keep (bring to v2)
  - [x] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [ ] Discuss (review details before deciding)

- **Separate `vim` noun (`vim.go`, `vim_store.go`)** (`Missing in v2`): v1 had a dedicated `vim` record kind for Neovim instances; intentionally retired by locked v2 schema decision #3 (covered by `pane` with `role: scratch-vim` / `side-vim`).
  - [ ] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [x] Discuss (review details before deciding)

---

## 3. Matomeru v2: agent integration, hooks, and telemetry

### Already at parity or improved in v2

| Feature | v2 status | Summary |
| --- | --- | --- |
| Open UI MCP tools (`ui.create_page`, `ui.update_page`, `ui.ask`, `ui.list_pages`) | Improved in v2 | First-class per-pane ViewNode sites, unread badges, structured `ui.ask` forms/choices, answers stored on cards and returned by `ui.list_pages` |
| Data, Lane, and Triage MCP tools (`data.*`, `lane.*`, `triage.*`) | Improved in v2 | 18 MCP tools total (`4 ui.*`, `8 data.*`, `4 lane.*`, `2 triage.*`) over stdio JSON-RPC 2.0 (`mato mcp serve`). Replaces v1's 8 MCP tools; reads are open, writes are non-destructive |
| Live local PTY + VT terminal (`ptyhost/local.go`) | Parity | `ptyhost/` runs real PTYs with VT snapshots, resize, `ctrl+a` leader passthrough, copy mode (`ctrl+a [`), and `MTM_PANE_ID`/`MTM_TASK_ID` env support |

### Groomable items (agent integration, hooks, and telemetry)

- **Multi-agent MCP transport (socket / HTTP)** (`Partial`): v1 had stdio MCP plus a Unix-socket host proxy; `mato mcp serve` currently supports stdio JSON-RPC 2.0 only (shared Unix socket / HTTP transport not yet built).
  - [x] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [ ] Discuss (review details before deciding)

- **Grouped multi-question decision forms in pane rail** (`Partial`): v1 used `o-questions` batched decision cards; `ui.ask` supports single question cards with multi-field forms, and a grouped multi-question decision wizard UI in the pane rail is still planned.
  - [ ] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [x] Discuss (review details before deciding)

- **`mtm agent hook` and `mtm-hook` binary (`internal/agenthook`, `cmd/mtm-hook`)** (`Missing in v2`): v1 had a dedicated fast `mtm-hook` binary and 7 harness adapters (`claude`, `codex`, `copilot`, `cursor`, `gemini`, `opencode`, `qwen`) capturing turn lifecycle, tool activity, approvals, usage, and statusline; not in `matomeru-next` yet (`runStatus` is currently inferred locally/from daemon only).
  - [x] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [ ] Discuss (review details before deciding)

- **Harness registry and launch flags (`internal/harness`, `harnesses.json`)** (`Missing in v2`): v1 had built-in harness definitions, `harnesses.json` overrides, automatic hook file installation (`internal/setup/hooks.go`), session resume flags, and a launch driver; v2 `pane` schema has `harness` and `command` string fields, but no harness registry, launch-flag builder, or auto-hook installer.
  - [x] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [x] Discuss (review details before deciding)

- **Structured prompt, activity, and final-response capture (`internal/turns`, `turn_cards.go`, `claude_session.go`)** (`Missing in v2`): v1 parsed Claude/OMP/shell JSONL transcripts in real time into Turn cards, subagent trees, tool activity timelines, and final responses; in v2 agents can push structured pages via `ui.create_page` / `lane.log`, but passive transcript tailing and automatic Turn/Activity/Response extraction are not implemented.
  - [ ] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [x] Discuss (review details before deciding)

- **Daemon panes (`mtmd` / `tuios`) (`ptyhost/daemon.go`)** (`Partial`): v1 had a full Unix-socket `internal/daemon` server/client, streaming events, multi-host qualification (`hosts.go`), state import, self-pinning, and pane survival across restarts; `ptyhost/daemon.go` implements CLI-based `DaemonHost` (opt-in via `"terminal.daemon": true`, unit-tested with fake `ExecFunc`), not yet live-verified against real `mtmd`/`tuios`, with no streaming socket transport or multi-host/remote daemon management.
  - [ ] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [ ] Discuss (review details before deciding)

- **Agent rights checks on destructive actions** (`Partial`): v1 had `ochazuke/agent_rights.go` allow/refuse checks for agent mutations; v2 MCP tools follow open-reads / safe-additive-writes (no destructive MCP tools exposed yet), and an explicit destructive-only rights policy check is not wired yet.
  - [ ] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [x] Discuss (review details before deciding)

---

## 4. Matomeru v2: data, storage, CLI, and config

### Already at parity or improved in v2

| Feature | v2 status | Summary |
| --- | --- | --- |
| SQLite store and unified schema (`data/`) | Improved in v2 | Pure SQLite `data/` with immutable change sets, global undo/redo, opaque IDs + unique fields, universal `archiveStatus`/`starStatus`/`snoozedUntil`, unified `task` hierarchy (roots = projects), distinct `note` vs `document`, `lane`, `ui.page`, `signal`, `triage.rule` |
| Managed files storage (`data/files.go`) | Improved in v2 | Canonical `$MTM_FILES_DIR` (or `<dbDir>/files/{notes,documents,artifacts}`) with atomic write + slugged filenames, shared across TUI, CLI (`--managed`), and MCP (`data.create_note`) |
| CLI nouns and CRUD subcommands (`cmd/mtm/nouns.go`) | Parity | Covers `task`, `pane`, `note`, `document`, `lane`, `schedule`, `run`, `signal`, `directory`, `extension`, `rule` with `list`, `get`, `create`/`add`, `edit`/`set`/`patch`, `star`/`unstar`, `archive`/`restore`, `snooze`/`unsnooze`, `delete`, `relate`/`unrelate`, `undo`/`redo`, `binding list/check`, `inspect`, `config export`, `mcp`, `migrate` |
| Human-friendly `<id>` resolution + interactive TTY picker (`cmd/mtm/resolve.go`) | Improved in v2 | Resolves exact ID, unique ID prefix, case-insensitive title, or unique title substring; omitted/ambiguous IDs in a TTY open an Ochazuke `widgets/picker` |
| Read-only v1 DB migration (`migrate/`) | Improved in v2 | `mtm migrate --from V1_DB [--dry-run \| --apply]` opens v1 strictly read-only, maps projects -> root tasks, tasks, notes, directories, reminders + schedules, relations; idempotent via `legacyId`/`legacyHash` |
| Interactive tutorial & `--smoke` harness | Improved in v2 | Built-in `mtm --tutorial` (in-memory workspace) and `mtm --smoke [--dump]` headless verification |

### Groomable items (data, storage, CLI, and config)

- **Automatic store backups, compaction, and expiration (`store/backup.go`, `compact.go`, `expire.go`)** (`Missing in v2`): v1 had scheduled/automatic DB backups, WAL/row compaction, and TTL expiration; v2 SQLite store has change-set history and undo/redo, but no built-in auto-backup rotation, history compaction, or TTL pruning.
  - [ ] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [x] Discuss (review details before deciding)

- **Unify `cmd/mtm` CLI onto `oden` v2 + `ochazuke/cli`** (`Partial`): v1 used Oden dispatch with shell completions; `matomeru-next/cmd/mtm` currently uses a hand-rolled flag parser (Wave F will unify it onto `oden` v2 + `ochazuke/cli` for consistent help, flags, and shell completions).
  - [ ] Keep (bring to v2)
  - [x] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [ ] Discuss (review details before deciding)

- **Shell integration (`cmd/mtm/shellinit/`, `shell_init.go`, `internal/setup/`)** (`Missing in v2`): v1 `mtm shell-init` emitted zsh/bash functions and completions, and `mtm setup` installed hooks/config; not yet ported to `matomeru-next`.
  - [ ] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [x] Discuss (review details before deciding)

- **User-defined `actions.json` and `harnesses.json` config loading** (`Partial`): v2 reads `~/.config/matomeru-next/{settings,bindings,themes,handlers}.json` (JSONC) with live-applying `S` Settings and `B` Bindings editors and `config export [--jsonl]`, but `actions.json` user-defined shell verbs and `harnesses.json` are not yet loaded in v2.
  - [x] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [x] Discuss (review details before deciding)

- **Config file hot-reload on external edits (`config_reload.go`)** (`Missing in v2`): v1 watched config files on disk and reloaded without restarting the TUI; in v2 in-app `S`/`B` edits apply live and save, while external file edits require a restart.
  - [ ] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [x] Later (post-cutover backlog)
  - [ ] Discuss (review details before deciding)

---

## 5. Extensions (`o-*` suite)

All 14 migrated tools below live in sibling `*-next` worktrees on Ochazuke v2 (preserving each tool's on-disk data/config format). Note: `matomeru-next/ext/ext.go` `DefaultTools` currently lists 9 tool families (`o-csv`, `o-git`, `o-lane`, `o-json`, `o-links`, `o-md`, `o-cmds`, `o-questions`, `o-df`) and should be expanded to include the Wave D/E tools (`o-fleet`, `o-gh`, `o-proj`, `o-ai`, `o-mermaid`, `o-services`).

### Migrated v2 tools at parity or improved (10 tools)

| Tool | v2 status | Summary |
| --- | --- | --- |
| `o-csv` -> `o-csv2` (`~/proj/o-csv-next`) | Improved in v2 | 3.4k LOC, 8.1 MB (was 6.4k LOC, 32.6 MB); Readline across filter/pickers/form, `--smoke` 19/19. Queued for Wave F (`oden` v2 + `ochazuke/cli`) |
| `o-json` -> `o-json2` (`~/proj/o-json-next`) | Improved in v2 | 8.0k LOC, 8.7 MB (was 81.3k LOC, 36.6 MB); relational flatten, real `jq` bar, Convert Shape (`C`), raw JSON closure folding (`za`/`zM`/`zR`), Readline. Queued for Wave F |
| `o-df` -> `o-df2` (`~/proj/o-df-next`) | Parity | Wave A done; keeps `culprits.json` & trim logic; adds `catalog`/`query`; 8.3 MB, `--smoke` 21/21. Queued for Wave F |
| `o-md` -> `o-md2` (`~/proj/o-md-next`) | Parity | Wave A done; reuses `widgets/markdown`, keeps existing dirs/trash; adds `catalog`/`query`; 9.2 MB, `--smoke` 19/19. Queued for Wave F |
| `o-cmds` -> `o-cmds2` (`~/proj/o-cmds-next`) | Parity | Wave A done; keeps store/process/MCP; adds `catalog`/`query`; 8.4 MB, `--smoke` 19/19. Queued for Wave F |
| `o-lane` -> `o-lane2` (`~/proj/o-lane-next`) | Parity | Wave B done; keeps JSON store/rollup/MCP + `Store.Redo()`, unknown statuses never refused; adds `catalog`/`query`; 9.5 MB. Queued for Wave F |
| `o-questions` -> `o-questions2` (`~/proj/o-questions-next`) | Improved in v2 | Wave B done; fixes v1 decision-answer loss, keeps journal/web/MCP; adds `catalog`/`query`; 8.7 MB. Queued for Wave F |
| `o-fleet` -> `o-fleet2` (`~/proj/o-fleet-next`) | Improved in v2 | Wave D1 done on Ochazuke v2 + `oden` v2 + `ochazuke/cli`; 8.6 MB, 201 bindings (0 errors), `catalog` (`fleet.machine`, `fleet.check`) |
| `o-gh` -> `o-gh2` (`~/proj/o-gh-next`) | Improved in v2 | Wave D1 done on Ochazuke v2 + `oden` v2 + `ochazuke/cli`; 8.6 MB, 130 bindings (0 errors), `catalog` (`gh.pr`, `gh.collection`) |
| `o-proj` -> `o-proj2` (`~/proj/o-proj-next`) | Improved in v2 | Wave D1 done on Ochazuke v2 + `oden` v2 + `ochazuke/cli`; 11 MB, 170 bindings (0 errors), `catalog` (`proj.project`, `proj.template`) |

### In-progress v2 tool migrations (4 tools)

- **`o-git` -> `o-git2` (`~/proj/o-git-next`)** (`Partial`): git status, branches, diffs, and PRs (v1 22.5k LOC); Wave D2 active now onto Ochazuke v2 + `oden` v2 + `ochazuke/cli` + `widgets/diff`.
  - [ ] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [x] Discuss (review details before deciding)

- **`o-ai` -> `o-ai2` (`~/proj/o-ai-next`)** (`Partial`): AI MCPs, skills, agents, hooks, and harnesses manager (v1 12.1k LOC); Wave D2 active now onto Ochazuke v2 + `oden` v2 + `ochazuke/cli`.
  - [ ] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [x] Discuss (review details before deciding)

- **`o-mermaid` -> `o-mermaid2` (`~/proj/o-mermaid-next`)** (`Partial`): Mermaid diagram & theme previewer (v1 1.7k LOC); Wave E active now onto Ochazuke v2 + `oden` v2 + `ochazuke/cli`.
  - [ ] Keep (bring to v2)
  - [x] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [ ] Discuss (review details before deciding)

- **`o-services` -> `o-services2` (`~/proj/o-services-next`)** (`Partial`): local/remote service monitor (v1 3.6k LOC); Wave E active now onto Ochazuke v2 + `oden` v2 + `ochazuke/cli`.
  - [ ] Keep (bring to v2)
  - [x] Drop (intentionally leave behind)
  - [x] Later (post-cutover backlog)
  - [ ] Discuss (review details before deciding)

### Skipped v1 tools (5 tools, kept on v1 unless promoted)

- **`o-koe` (`~/proj/o-koe`, 4.5k LOC)** (`Missing in v2`): cloned-voice TTS CLI/TUI calling remote VoxCPM2 over SSH; skipped for v2 migration (v1 binary continues working standalone).
  - [ ] Keep (bring to v2)
  - [x] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [ ] Discuss (review details before deciding)

- **`o-qbt` (`~/proj/o-qbt`, 5.0k LOC)** (`Missing in v2`): qBittorrent Web API v2 TUI/CLI with transfer limits and free-space guard; skipped for v2 migration (v1 binary continues working standalone).
  - [ ] Keep (bring to v2)
  - [x] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [ ] Discuss (review details before deciding)

- **`o-slack` (`~/proj/o-slack`, 5.8k LOC)** (`Missing in v2`): Slack browser/client on Ochazuke v1 (`v0.133.0`); skipped for full TUI port (candidate to wire later as a stateless connector feeding Matomeru v2 `signal` + `triage.rule`).
  - [ ] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [x] Later (post-cutover backlog)
  - [ ] Discuss (review details before deciding)

- **`o-mcp` (`~/proj/o-mcp`, 8.7k LOC)** (`Missing in v2`): MCP server registry/inspector on older Ochazuke v1 (`v0.105.0`); skipped for v2 migration (overlaps partially with `o-ai2`).
  - [ ] Keep (bring to v2)
  - [ ] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [x] Discuss (review details before deciding)

- **`o-inventory` (`~/proj/o-inventory`, 6.9k LOC)** (`Missing in v2`): home/hardware inventory manager on Ochazuke v1 (`v0.144.1`); skipped for v2 migration (v1 binary continues working standalone).
  - [ ] Keep (bring to v2)
  - [x] Drop (intentionally leave behind)
  - [ ] Later (post-cutover backlog)
  - [ ] Discuss (review details before deciding)
