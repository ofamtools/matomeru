# V1 vs v2 parity matrix (groomable checklist)

Snapshot date: 2026-11-23. Audited against local source trees:
- Ochazuke v1 (`/Users/unixsuperhero/proj/ochazuke`, ~172k LOC) vs v2 (`/Users/unixsuperhero/proj/ochazuke-next`, ~14k LOC)
- Matomeru v1 (`/Users/unixsuperhero/proj/matomeru`, ~95.5k LOC) vs v2 (`/Users/unixsuperhero/proj/matomeru-next`, ~15k LOC)
- Extensions: 14 `o-*` v2 migrations (`*-next` worktrees) vs 5 skipped v1 tools (`o-koe`, `o-qbt`, `o-slack`, `o-mcp`, `o-inventory`)

## How to groom this document

- `Parity` and `Improved in v2` rows are pre-marked `[x] Done`.
- `Partial` and `Missing in v2` rows are pre-marked `[ ] Keep  [ ] Drop  [ ] Later`.
- Mark `[x]` on your choice for each Partial or Missing row:
  - **Keep**: Must reach parity before or right after cutover.
  - **Drop**: Intentionally retired in v2 (do not port).
  - **Later**: Nice-to-have; defer to post-cutover backlog.

---

## 1. Ochazuke v2 (core, UI, widgets, CLI)

| Feature | v1 behavior | v2 status | Notes / tradeoff | Owner decision |
| --- | --- | --- | --- | --- |
| Modal engine, trie key dispatch, layers, counts | Multi-layer keymap, prefix sequences, leader layers, numeric counts | Improved in v2 | Pure stdlib `trie/`, `expr/`, `core/` with declarative `when` guards, `ExcludeTags` leader mirroring, zero Bubble Tea coupling in core | [x] Done |
| Registers and keyboard macros | Named registers, yank/paste, `q`/`@` macro record and replay | Parity | `core/registers.go` and `core/macros.go` implement named registers, `q`/`@`/`@@` replay | [x] Done |
| Layout solver and cell buffer renderer | Lip Gloss string composition and per-component layout | Improved in v2 | Deterministic `layout/` constraint solver + `buffer/` ANSI cell diffing + `ui/` `ViewNode` tree + 0-redraw idle coalescing in `tea/shell.go` | [x] Done |
| Themes, density, and border styles | Theme presets + `themes.json` overlay | Improved in v2 | 5 built-in presets, `compact`/`balanced`/`spacious` density, `rounded`/`sharp`/`minimal`/`none` borders, live `themes.json` overlay | [x] Done |
| Config formats (JSONC + JSONL) | JSON config loading, export, live reload | Improved in v2 | `config/` supports JSONC comments/trailing commas and JSONL export/import | [x] Done |
| Table widget (`widgets/table`) | Filter (substring/fuzzy/regex), multi-key sort, grouping/fold, marks, visual/cell select | Improved in v2 | 100k-row perf verified; multi-key sort, multi-condition filter syntax (`!=`, `a\|b`), `V` visual range, cell select, grouping, marks | [x] Done |
| Form widget (`widgets/form`) | Field editing, enums, pickers, validation | Improved in v2 | Added `ChoiceResolver` dependent fields, explicit Submit (`ctrl+s` / `[ Submit ]`), and persistent auto-saved form drafts (`[`/`]`/`D`) | [x] Done |
| Input widget (`widgets/input`) | Vim motions, text objects, operators, surround, Readline editing, copy mode | Improved in v2 | Full Vim motions/operators/objects/surround, 14 Readline actions + cursor placement, and styled ANSI `CopyMode` (`widgets/input/copymode.go`) | [x] Done |
| Input reference triggers (`@file`, `$skill`, `/command`) | `internal/compose` inline completion popup inside prompt boxes for files, skills, commands | Missing in v2 | Not yet implemented inside `widgets/input` or Matomeru v2 prompt inputs | [ ] Keep  [ ] Drop  [ ] Later |
| Configurable dot-repeat tag set | `.` repeats last editing command with tag filtering | Partial | Basic edit repeat exists in `widgets/input`; global `core` repeatable-tag set not yet wired | [ ] Keep  [ ] Drop  [ ] Later |
| Picker and path picker (`widgets/picker`, `widgets/pathpicker`) | Fuzzy/substring/regex picker and filesystem path browser | Parity | Both widgets exist with Readline editing, hidden-file toggle, parent/child navigation | [x] Done |
| Command palette (`widgets/palette`) | Palette list with separate arg entry modal | Improved in v2 | Always-visible live argument form on the right while browsing commands; `tab`/`right` focuses form | [x] Done |
| Command line (`widgets/commandline`) | Ex-style bar with history and completion | Improved in v2 | Supports `:`, `/`, `?`, `!`, `\|`, `r!`, `ctrl+r <reg>` register paste, `$VAR` expansion, history, Tab completion | [x] Done |
| Markdown reader (`widgets/markdown`) | Separate `markdown/` package with glamour rendering and TOC | Improved in v2 | Pure v2 `widgets/markdown` with headings TOC (`t`), link navigation (`tab`/`enter`), code-block yank (`y`), link back stack | [x] Done |
| Diff viewer (`widgets/diff`) | `diffview/` package with unified and side-by-side diffs, syntax highlight | Parity | `widgets/diff` provides unified, side-by-side, and JSON diff rendering | [x] Done |
| Binding editor and `?` help (`widgets/bindings`) | Live binding editor, conflict detection, `?` help overlay, `FindFreeBinding` | Parity | `widgets/bindings` has live rebind/unbind/reset/undo/redo, conflict analysis, `FindFreeBinding`, and `?` help | [x] Done |
| CLI bridge (`ochazuke/cli` + `oden` v2) | `cli/` hosted Oden CLI, catalog mount, host proxy, MCP backend, PTY shell | Partial | `ochazuke-next/cli/` bridges `oden` v2 (`AppInfo`, `binding check`, `capabilities`, TTY `PickerFunc`). Does not include v1's Unix-socket host proxy (`host_serve`/`host_proxy`) or generic `cli/mcp` auto-wrapper | [ ] Keep  [ ] Drop  [ ] Later |
| Tree guide lines (`├─`, `└─`, `│`) | Box-drawing guide lines in tree/outline views | Missing in v2 | Indentation works in `ui.KindTree` and Outline; box-drawing connector lines not yet rendered | [ ] Keep  [ ] Drop  [ ] Later |
| Optional widgets: `kanban`, `timeline`, `toast`, `jsontree`, `grid` | v1 had `grid/`, `jsontree/`, `sysstat/`, toast/notice strip | Missing in v2 | Deferred in v2 (`o-json2` handles JSON tree flattening; `widgets/kanban`, `widgets/timeline`, `widgets/toast` not built yet) | [ ] Keep  [ ] Drop  [ ] Later |
| Focus jump hints over `FocusOrder` | Focus-hint overlay / quick jump labels across panels | Missing in v2 | `tab`/`shift+tab` cycle focus; visual jump-label hints over `layout.Solved.FocusOrder` not yet built | [ ] Keep  [ ] Drop  [ ] Later |
| Static architecture/rule checkers (`rulecheck`, `namecheck`, `lexiconcheck`, `offercheck`, `refusecheck`, `docgen`, `o-story`) | 10+ lint/audit packages and `cmd/docgen`, `cmd/o-story` | Missing in v2 | Replaced in v2 by lean `deps_test.go` layer checks, `binding check`, and `--smoke` scripted verification | [ ] Keep  [ ] Drop  [ ] Later |

---

## 2. Matomeru v2: spaces and navigation

| Feature | v1 behavior | v2 status | Notes / tradeoff | Owner decision |
| --- | --- | --- | --- | --- |
| Top-level spaces (`1`-`4`, `[`/`]`) | Multi-page rail (Panes, Projects, Tasks, Notes, Reviews, Schedules, Reminders, Runs, Saved, Vim, Extensions) | Improved in v2 | Consolidated into 4 clean spaces: `1 Panes`, `2 Work`, `3 Runs`, `4 Extensions`, plus `g n` to browse any noun index in Work | [x] Done |
| Panes space (`1 Panes`) rail, site, TOC (`t`), zoom (`ctrl+a z`), attention (`W`) | Pane rail, live terminal, side terminal/vim, zoom, next waiting pane | Improved in v2 | Adds per-pane multi-page site (Screen, Questions, Decisions, Plan, Response, custom), toggleable TOC rail (`t`, `panes.showTOC`), `n` opens New Pane directly, `ctrl+a L` Ambient Lane Progress Bar, `panes.railAutoWiden` | [x] Done |
| Pane rail grouping and sorting (by project, status, `promptedAt`) | Pane rail grouped by project/session with human-only `promptedAt` ordering and custom rail icons | Partial | Human-only `promptedAt` ordering and `W` attention jump are implemented; v1's multi-mode rail grouping (`panes_group.go`, `panes_tree.go`, `rail_triage.go`) is simplified in v2 | [ ] Keep  [ ] Drop  [ ] Later |
| Global scratches (`ctrl+a S` / `ctrl+a V`) and side panes (`ctrl+a t` / `ctrl+a v`) | Global scratch terminal/vim and per-pane side terminal/vim (`side.go`, `side_view.go`) | Parity | Implemented via pane roles (`scratch-term`, `scratch-vim`, `side-term`, `side-vim`) over any space | [x] Done |
| Work space (`2 Work`) dynamic Index / Show / Edit pages | Hand-wired pages per noun (Projects, Tasks, Notes, etc.) | Improved in v2 | Schema-driven Index, Show, and Edit views for all nouns; focusable empty Show-page relation sections (`j` -> `n`); tidy breadcrumbs; `H`/`L`/`backspace` Show-page history | [x] Done |
| Runs space (`3 Runs`) | Saved commands and run history (`runs.go`) | Improved in v2 | Real OS command runner (`runs/`), live output tail, `n` new run, `r` rerun, `x` cancel, extension runs recorded automatically | [x] Done |
| Extensions space (`4 Extensions`) | Mounted v1 catalog extensions via host proxy | Improved in v2 | `R` discovers tools (`capabilities --json` + `catalog` + `query`), async `ReadCache` + lazy shadows, mounted nouns browsable via `g n` | [x] Done |
| Outline overlay (`T`) | Project/task hierarchy tree with reorder and status edits | Improved in v2 | Global `T` overlay with `J`/`K` sibling reorder (with counts like `2J`), `>`/`<` indent/outdent, `space` status cycle, `i` inline rename, `u`/`ctrl+r` undo/redo | [x] Done |
| Recents picker (`g r`) and Pages picker (`'`) | Recency list and page switcher | Improved in v2 | `g r` global newest-first Recents picker across all nouns; `'` picks any page of any pane | [x] Done |
| Command Palette (`:`) with live args form | `:` palette opened a separate arg form after selection | Improved in v2 | Powered by `widgets/palette`; live argument form stays visible on the right as you browse commands | [x] Done |
| Focused-record actions picker (`;`) and yank picker (`y` / `y y`) | Context menus and yank actions | Improved in v2 | `;` shows all applicable actions for the focused record; `y` lists every field value as it will be copied, `y y` yanks primary value immediately | [x] Done |
| Shell I/O redirection (`!`, `\|`, `g !`) | External shell command execution | Improved in v2 | `!` runs shell command, `\|` pipes focused record JSON/text to shell, `g !` (`r!`) captures shell stdout into a new note; supports `ctrl+r <reg>` and `$VAR` expansion | [x] Done |
| Open target (`o`) / Open with (`O`) + `handlers.json` | File/URL open with external apps or inline Markdown | Parity | `handlers/handlers.go` routes files/URLs/dirs via `handlers.json` or opens Markdown inline in `ModeReader` | [x] Done |
| Signals and triage rules (`signal`, `triage.rule`, `R`) | `panesignals` regex/rule matching on pane output + `actionitems` | Improved in v2 | Generalized `signal` + `triage.rule` using `ochazuke/expr` expressions (`mark_noise`, `mark_actionable`, `snooze`, `tag`, `relate_task`), `R` creates rule from focused signal | [x] Done |
| Built-in prompt composer box (`prompt_box.go`, `prompt_attach.go`, `prompt_drafts.go`) | Dedicated Matomeru prompt box with `@file`/`$skill`/`/cmd` autocomplete, attachments, and draft queue before sending to agent pane | Missing in v2 | In v2 you attach directly to the live terminal (`enter`/`i`) and type into the harness; form drafts exist for record forms, not a dedicated terminal prompt composer | [ ] Keep  [ ] Drop  [ ] Later |
| Saved filters / views per noun (`saved.go`, `saved_store.go`) | Persist named filter/sort/group presets per noun and switch between them | Missing in v2 | Multi-condition filter syntax (`!=`, `a\|b`) works in `widgets/table`, but saving/reloading named filter views is not yet wired in Matomeru v2 | [ ] Keep  [ ] Drop  [ ] Later |
| Context items harvesting (`internal/contextitems`, `context_extract.go`, `context_follow.go`) | Auto-extract URLs, file paths, PRs, OSC-8 hyperlinks, and commands from pane scrollback and attach to tasks | Missing in v2 | `RelContext` relation exists in v2 schema, but automatic scrollback harvesting/scraping and `context_picker` are not built yet | [ ] Keep  [ ] Drop  [ ] Later |
| Link/path hints overlay (`internal/hints`) | Visual hint labels over URLs/paths visible in pane terminal to jump/copy/open | Missing in v2 | Not yet implemented in v2 terminal view | [ ] Keep  [ ] Drop  [ ] Later |
| Reviews noun and workflow (`reviews.go`, `review_store.go`) | Dedicated Review records for PR/code review tracking | Missing in v2 | Not a built-in schema in v2 (can be modeled via `signal`, `task`, or `o-gh2` catalog) | [ ] Keep  [ ] Drop  [ ] Later |
| Reminders / schedule firing and desktop notifications (`internal/reminders`, `internal/schedules`, `internal/notify`) | Background ticker evaluates cron schedules/reminders and sends macOS notifications | Partial | `schedule` and `schedule.occurrence` schemas exist in v2 `data/`, and v1 reminders/schedules migrate into them, but no background cron evaluator or desktop notification daemon runs yet | [ ] Keep  [ ] Drop  [ ] Later |
| Privacy mode / redaction (`privacy.go`, `internal/work/privacy.go`) | Toggleable privacy filter to hide private projects/tasks/panes during demos or screen sharing | Missing in v2 | Not implemented in v2 | [ ] Keep  [ ] Drop  [ ] Later |
| Separate `vim` noun (`vim.go`, `vim_store.go`) | Dedicated `vim` record kind for Neovim instances | Missing in v2 | Intentionally retired by locked v2 schema decision #3 (covered by `pane` with `role: scratch-vim` / `side-vim`) | [ ] Keep  [ ] Drop  [ ] Later |

---

## 3. Matomeru v2: agent integration, hooks, and telemetry

| Feature | v1 behavior | v2 status | Notes / tradeoff | Owner decision |
| --- | --- | --- | --- | --- |
| Open UI MCP tools (`ui.create_page`, `ui.update_page`, `ui.ask`, `ui.list_pages`) | No interactive agent-built UI pages in v1 (only `report_progress` status text and `o-questions` external tool) | Improved in v2 | First-class per-pane ViewNode sites, unread badges, structured `ui.ask` forms/choices, answers stored on cards and returned by `ui.list_pages` | [x] Done |
| Data, Lane, and Triage MCP tools (`data.*`, `lane.*`, `triage.*`) | 8 MCP tools (`whoami`, `task_list`, `task_done`, `task_status`, `task_note_add`, `context_add`, `document_report`, `report_progress`) | Improved in v2 | 18 MCP tools total (`4 ui.*`, `8 data.*`, `4 lane.*`, `2 triage.*`) over stdio JSON-RPC 2.0 (`mato mcp serve`). Note: v1's `whoami`, `task_done`/`task_status`, `context_add`, `document_report`, `report_progress` are replaced by `data.*`/`lane.*`/`ui.*` (no direct `whoami` or `task_status` mutation tool; reads are open, writes are non-destructive) | [x] Done |
| Multi-agent MCP transport (socket / HTTP) | Stdio MCP + Unix socket host proxy | Partial | `mato mcp serve` supports stdio JSON-RPC 2.0 only; shared Unix socket / HTTP transport not yet built | [ ] Keep  [ ] Drop  [ ] Later |
| Grouped multi-question decision forms in pane rail | `o-questions` batched decision cards | Partial | `ui.ask` supports single question cards with multi-field forms; grouped multi-question decision wizard UI in pane rail is still planned | [ ] Keep  [ ] Drop  [ ] Later |
| `mtm agent hook` and `mtm-hook` binary (`internal/agenthook`, `cmd/mtm-hook`) | Dedicated fast `mtm-hook` binary + 7 harness adapters (`claude`, `codex`, `copilot`, `cursor`, `gemini`, `opencode`, `qwen`) capturing turn lifecycle, tool activity, approvals, usage, and statusline | Missing in v2 | No `mtm-hook` binary or `mtm agent hook` CLI in `matomeru-next` yet; pane `runStatus` is currently inferred locally/from daemon only | [ ] Keep  [ ] Drop  [ ] Later |
| Harness registry and launch flags (`internal/harness`, `harnesses.json`) | Built-in harness definitions, `harnesses.json` overrides, automatic hook file installation (`internal/setup/hooks.go`), session resume flags, launch driver | Missing in v2 | v2 `pane` schema has `harness` and `command` string fields, but no harness registry, launch-flag builder, or auto-hook installer | [ ] Keep  [ ] Drop  [ ] Later |
| Structured prompt, activity, and final-response capture (`internal/turns`, `turn_cards.go`, `claude_session.go`) | Parses Claude/OMP/shell JSONL transcripts in real time into Turn cards, subagent trees, tool activity timelines, and final responses | Missing in v2 | In v2, agents can push structured pages via `ui.create_page` / `lane.log`, but passive transcript tailing and automatic Turn/Activity/Response extraction are not implemented | [ ] Keep  [ ] Drop  [ ] Later |
| Live local PTY + VT terminal (`ptyhost/local.go`) | Embedded PTY terminal with VT emulation and key encoding | Parity | `ptyhost/` runs real PTYs with VT snapshots, resize, `ctrl+a` leader passthrough, copy mode (`ctrl+a [`), and `MTM_PANE_ID`/`MTM_TASK_ID` env support | [x] Done |
| Daemon panes (`mtmd` / `tuios`) (`ptyhost/daemon.go`) | Full Unix-socket `internal/daemon` server/client, streaming events, multi-host qualification (`hosts.go`), state import, self-pinning, pane survival across restarts | Partial | `ptyhost/daemon.go` implements CLI-based `DaemonHost` (`mtmd`/`tuios`, opt-in via `"terminal.daemon": true`, unit-tested with fake `ExecFunc`). Not yet live-verified against real `mtmd`/`tuios`, no streaming socket transport, and no multi-host/remote daemon management | [ ] Keep  [ ] Drop  [ ] Later |
| Agent rights checks on destructive actions | `ochazuke/agent_rights.go` allow/refuse checks for agent mutations | Partial | v2 MCP tools follow open-reads / safe-additive-writes design (no destructive MCP tools exposed yet); explicit destructive-only rights policy check is not wired yet | [ ] Keep  [ ] Drop  [ ] Later |

---

## 4. Matomeru v2: data, storage, CLI, and config

| Feature | v1 behavior | v2 status | Notes / tradeoff | Owner decision |
| --- | --- | --- | --- | --- |
| SQLite store and unified schema (`data/`) | `store/` JSON-blob `DocTable` per table (`projects`, `tasks`, `notes`, `relations`, `schedule_defs`, `reminder_defs`, etc.) | Improved in v2 | Pure SQLite `data/` with immutable change sets, global undo/redo, opaque IDs + unique fields, universal `archiveStatus`/`starStatus`/`snoozedUntil`, unified `task` hierarchy (roots = projects), distinct `note` vs `document`, `lane`, `ui.page`, `signal`, `triage.rule` | [x] Done |
| Automatic store backups, compaction, and expiration (`store/backup.go`, `compact.go`, `expire.go`) | Scheduled/automatic DB backups, WAL/row compaction, TTL expiration | Missing in v2 | v2 SQLite store has change-set history and undo/redo, but no built-in auto-backup rotation, history compaction, or TTL pruning | [ ] Keep  [ ] Drop  [ ] Later |
| Managed files storage (`data/files.go`) | Notes/docs scattered across external paths or `o-md` directories | Improved in v2 | Canonical `$MTM_FILES_DIR` (or `<dbDir>/files/{notes,documents,artifacts}`) with atomic write + slugged filenames, shared across TUI, CLI (`--managed`), and MCP (`data.create_note`) | [x] Done |
| CLI nouns and CRUD subcommands (`cmd/mtm/nouns.go`) | `mtm <noun> <verb>` via Oden + Ochazuke CLI | Parity | Covers `task`, `pane`, `note`, `document`, `lane`, `schedule`, `run`, `signal`, `directory`, `extension`, `rule` with `list`, `get`, `create`/`add`, `edit`/`set`/`patch`, `star`/`unstar`, `archive`/`restore`, `snooze`/`unsnooze`, `delete`, `relate`/`unrelate`, `undo`/`redo`, `binding list/check`, `inspect`, `config export`, `mcp`, `migrate` | [x] Done |
| Human-friendly `<id>` resolution + interactive TTY picker (`cmd/mtm/resolve.go`) | Exact ID lookup or manual search | Improved in v2 | Resolves exact ID, unique ID prefix, case-insensitive title, or unique title substring; omits/ambiguous IDs in a TTY open an Ochazuke `widgets/picker` | [x] Done |
| Unify `cmd/mtm` CLI onto `oden` v2 + `ochazuke/cli` | v1 used Oden dispatch with shell completions (`shellinit/`) | Partial | `matomeru-next/cmd/mtm` currently uses a hand-rolled flag parser; Wave F will unify it onto `oden` v2 + `ochazuke/cli` (adding consistent help, flags, and shell completions) | [ ] Keep  [ ] Drop  [ ] Later |
| Shell integration (`cmd/mtm/shellinit/`, `shell_init.go`, `internal/setup/`) | `mtm shell-init` emits zsh/bash functions, completions, and `mtm setup` installs hooks/config | Missing in v2 | Not yet ported to `matomeru-next` | [ ] Keep  [ ] Drop  [ ] Later |
| Settings, bindings, themes, and handlers config | `~/.config/matomeru/{settings,bindings,themes,actions,harnesses}.json` + live reload | Improved in v2 | Reads `~/.config/matomeru-next/{settings,bindings,themes,handlers}.json` (JSONC), live-applying `S` Settings form and `B` Bindings editor, `config export [--jsonl]`. Note: `actions.json` user-defined shell verbs and `harnesses.json` are not yet loaded in v2 | [ ] Keep  [ ] Drop  [ ] Later |
| Config file hot-reload on external edits (`config_reload.go`) | Watches config files on disk and reloads without restarting TUI | Missing in v2 | In-app `S`/`B` edits apply live and save; external file edits require restart in v2 | [ ] Keep  [ ] Drop  [ ] Later |
| Read-only v1 DB migration (`migrate/`) | N/A (new for v2 cutover) | Improved in v2 | `mtm migrate --from V1_DB [--dry-run \| --apply]` opens v1 strictly read-only, maps projects -> root tasks, tasks, notes, directories, reminders + schedules, relations; idempotent via `legacyId`/`legacyHash` | [x] Done |
| Interactive tutorial & `--smoke` harness | `mtm-story` scenario runner | Improved in v2 | Built-in `mtm --tutorial` (in-memory workspace) and `mtm --smoke [--dump]` headless verification | [x] Done |

---

## 5. Extensions (`o-*` suite)

### Migrated v2 tools (14 tools)

All 14 tools below are being migrated in sibling `*-next` worktrees onto Ochazuke v2 (preserving each tool's on-disk data/config format). Note: `matomeru-next/ext/ext.go` `DefaultTools` currently lists 9 tool families (`o-csv`, `o-git`, `o-lane`, `o-json`, `o-links`, `o-md`, `o-cmds`, `o-questions`, `o-df`) and should be expanded to include the Wave D/E tools (`o-fleet`, `o-gh`, `o-proj`, `o-ai`, `o-mermaid`, `o-services`).

| Feature | v1 behavior | v2 status | Notes / tradeoff | Owner decision |
| --- | --- | --- | --- | --- |
| `o-csv` -> `o-csv2` (`~/proj/o-csv-next`) | Ochazuke v1 CSV table viewer/editor (6.4k LOC, 32.6 MB) | Improved in v2 | 3.4k LOC, 8.1 MB; Readline across filter/pickers/form, `--smoke` 19/19. Queued for Wave F (`oden` v2 + `ochazuke/cli`) | [x] Done |
| `o-json` -> `o-json2` (`~/proj/o-json-next`) | Ochazuke v1 JSON explorer (81.3k LOC, 36.6 MB) | Improved in v2 | 8.0k LOC, 8.7 MB; relational flatten, real `jq` bar, Convert Shape (`C`), raw JSON closure folding (`za`/`zM`/`zR`), Readline. Queued for Wave F | [x] Done |
| `o-df` -> `o-df2` (`~/proj/o-df-next`) | Disk usage & culprit trimmer (5.5k LOC) | Parity | Wave A done; keeps `culprits.json` & trim logic; adds `catalog`/`query`; 8.3 MB, `--smoke` 21/21. Queued for Wave F | [x] Done |
| `o-md` -> `o-md2` (`~/proj/o-md-next`) | Markdown notes/docs browser (8.0k LOC) | Parity | Wave A done; reuses `widgets/markdown`, keeps existing dirs/trash; adds `catalog`/`query`; 9.2 MB, `--smoke` 19/19. Queued for Wave F | [x] Done |
| `o-cmds` -> `o-cmds2` (`~/proj/o-cmds-next`) | Saved commands & run history (13.3k LOC) | Parity | Wave A done; keeps store/process/MCP; adds `catalog`/`query`; 8.4 MB, `--smoke` 19/19. Queued for Wave F | [x] Done |
| `o-lane` -> `o-lane2` (`~/proj/o-lane-next`) | Agent lane tracker (24.4k LOC) | Parity | Wave B done; keeps JSON store/rollup/MCP + `Store.Redo()`, unknown statuses never refused; adds `catalog`/`query`; 9.5 MB. Queued for Wave F | [x] Done |
| `o-questions` -> `o-questions2` (`~/proj/o-questions-next`) | Question journal, decision docs, web UI, MCP (28.7k LOC) | Improved in v2 | Wave B done; fixes v1 decision-answer loss, keeps journal/web/MCP; adds `catalog`/`query`; 8.7 MB. Queued for Wave F | [x] Done |
| `o-fleet` -> `o-fleet2` (`~/proj/o-fleet-next`) | Machine fleet & health checks (5.0k LOC) | Improved in v2 | Wave D1 done on Ochazuke v2 + `oden` v2 + `ochazuke/cli`; 8.6 MB, 201 bindings (0 errors), `catalog` (`fleet.machine`, `fleet.check`) | [x] Done |
| `o-gh` -> `o-gh2` (`~/proj/o-gh-next`) | GitHub PR/collection browser (6.8k LOC) | Improved in v2 | Wave D1 done on Ochazuke v2 + `oden` v2 + `ochazuke/cli`; 8.6 MB, 130 bindings (0 errors), `catalog` (`gh.pr`, `gh.collection`) | [x] Done |
| `o-proj` -> `o-proj2` (`~/proj/o-proj-next`) | Project/template scaffolder (2.9k LOC) | Improved in v2 | Wave D1 done on Ochazuke v2 + `oden` v2 + `ochazuke/cli`; 11 MB, 170 bindings (0 errors), `catalog` (`proj.project`, `proj.template`) | [x] Done |
| `o-git` -> `o-git2` (`~/proj/o-git-next`) | Git status, branches, diffs, PRs (22.5k LOC) | Partial | Wave D2 active now (in progress onto Ochazuke v2 + `oden` v2 + `ochazuke/cli` + `widgets/diff`) | [ ] Keep  [ ] Drop  [ ] Later |
| `o-ai` -> `o-ai2` (`~/proj/o-ai-next`) | AI MCPs, skills, agents, hooks, harnesses manager (12.1k LOC) | Partial | Wave D2 active now (in progress onto Ochazuke v2 + `oden` v2 + `ochazuke/cli`) | [ ] Keep  [ ] Drop  [ ] Later |
| `o-mermaid` -> `o-mermaid2` (`~/proj/o-mermaid-next`) | Mermaid diagram & theme previewer (1.7k LOC) | Partial | Wave E active now (in progress onto Ochazuke v2 + `oden` v2 + `ochazuke/cli`) | [ ] Keep  [ ] Drop  [ ] Later |
| `o-services` -> `o-services2` (`~/proj/o-services-next`) | Local/remote service monitor (3.6k LOC) | Partial | Wave E active now (in progress onto Ochazuke v2 + `oden` v2 + `ochazuke/cli`) | [ ] Keep  [ ] Drop  [ ] Later |

### Skipped v1 tools (5 tools, kept on v1 unless promoted)

| Feature | v1 behavior | v2 status | Notes / tradeoff | Owner decision |
| --- | --- | --- | --- | --- |
| `o-koe` (`~/proj/o-koe`, 4.5k LOC) | Cloned-voice TTS CLI/TUI calling remote VoxCPM2 over SSH | Missing in v2 | Skipped for v2 migration; v1 binary continues working standalone | [ ] Keep  [ ] Drop  [ ] Later |
| `o-qbt` (`~/proj/o-qbt`, 5.0k LOC) | qBittorrent Web API v2 TUI/CLI with transfer limits and free-space guard | Missing in v2 | Skipped for v2 migration; v1 binary continues working standalone | [ ] Keep  [ ] Drop  [ ] Later |
| `o-slack` (`~/proj/o-slack`, 5.8k LOC) | Slack browser/client on Ochazuke v1 (`v0.133.0`) | Missing in v2 | Skipped for full TUI port; candidate to wire later as a stateless connector feeding Matomeru v2 `signal` + `triage.rule` | [ ] Keep  [ ] Drop  [ ] Later |
| `o-mcp` (`~/proj/o-mcp`, 8.7k LOC) | MCP server registry/inspector on older Ochazuke v1 (`v0.105.0`) | Missing in v2 | Skipped for v2 migration; overlaps partially with `o-ai2` | [ ] Keep  [ ] Drop  [ ] Later |
| `o-inventory` (`~/proj/o-inventory`, 6.9k LOC) | Home/hardware inventory manager on Ochazuke v1 (`v0.144.1`) | Missing in v2 | Skipped for v2 migration; v1 binary continues working standalone | [ ] Keep  [ ] Drop  [ ] Later |
