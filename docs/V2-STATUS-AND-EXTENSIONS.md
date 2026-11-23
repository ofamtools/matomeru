# Ochazuke v2, Matomeru v2, and extensions status

Snapshot taken 2026-11-23 against the local `next` branches and `~/proj/o-*` repos. All four v2 repos are clean on local branch `next` (unpushed) and all four v1 `main` repos remain untouched.

## Size and health snapshot

| Repo / Binary | v1 | v2 (`next`) | Binary | Status |
|---|---|---|---|---|
| `ochazuke` -> `ochazuke-next` | ~172k LOC | ~13.8k LOC (-92%) | library | `71a1836b`, 18/18 packages `-race` |
| `matomeru` -> `matomeru-next` | ~95.5k LOC, 86.2 MB | ~14.5k LOC, 17.9 MB (4.8x smaller) | `~/bin/m2m` (`mato`) | `9529727`, 12/12 packages `-race`, `--smoke` green |
| `o-json` -> `o-json-next` | 81.3k LOC, 36.6 MB | 8.0k LOC, 9.0 MB (4.1x smaller) | `~/bin/o-json2` | `d893f56`, `-race` + `--smoke` green |
| `o-csv` -> `o-csv-next` | 6.4k LOC, 32.6 MB | 3.4k LOC, 8.4 MB (3.9x smaller) | `~/bin/o-csv2` | `9c728da`, `-race` + 19/19 `--smoke` green |

---

## 1. What is left in Ochazuke v2 (`ochazuke-next`)

### Done
- Tier 0 (`trie`, `expr`, `layout`, `buffer`, `deps_test.go`).
- Tier 1 (`core` modal kernel + `Registers` + `MacroRecorder`, `ui` renderer + 5 theme presets + density/borders, `tea` Bubble Tea adapter with 0-redraw idle coalescing).
- Tier 2 widgets: `table` (100k-row perf verified), `picker`, `pathpicker`, `form` (with `ChoiceResolver` dependent fields), `input` (Vim motions/operators/objects/surround + 14 Readline actions/defaults/cursor), `bindings` (editor + `?` help + `FindFreeBinding`), `diff` (unified + side-by-side + JSON diff), `markdown` (reader + TOC + links + code yank), `commandline` (`:`/`/`/`?`/`!`/`|`/`r!` + `ctrl+r <reg>` + `$VAR` expansion), and `config` (JSONC + JSONL).

### Remaining in Ochazuke v2
1. **Table power features (`widgets/table`):**
   - Multi-key sort (secondary sort keys preserved when direction flips) and reserved sort-arrow width.
   - Cell-mode selection (`CellSelect`), `V` visual row range marks, and condition filter syntax (`!=`, `a|b`, saved filter presets).
2. **Input and copy-mode polish (`widgets/input`, `core`):**
   - Configurable dot-repeat tag set (`repeatable`).
   - Styled copy-mode buffer adapter so live terminal copy mode preserves ANSI colors while reusing the shared motion table.
   - Scope-aware word boundary tables (`markdown`, `csv`, `json`, `code`, `path`).
   - Reference lookup triggers inside text inputs (`@file`, `$skill`, `/command`).
3. **Optional Tier 2 widgets (deferred until needed by a consumer):**
   - `widgets/kanban` and `widgets/timeline` alternate collection presentations.
   - `widgets/toast` non-blocking notification strip (opt-outable via setting).
   - Focus hints / focus jump picker over `layout.Solved.FocusOrder`.
   - Tree guide lines (`├─ └─ │`) in tree/outline rendering.
4. **Release / cutover:**
   - Merge `next` to `main` (or tag v2) once you are ready to retire v1.

---

## 2. What is left in Matomeru v2 (`matomeru-next` / `mato`)

### Done
- Pure SQLite `data/` store with all 7 locked schema decisions (`note` vs `document`, `schedule` + `schedule.occurrence`, no `vim` noun, opaque IDs + unique fields, human-only `promptedAt`, JSONC + JSONL, plain `snoozedUntil` + contextual `Z`).
- All 4 spaces (`Panes`, `Work`, `Runs`, `Extensions`) + dynamic Index/Show/Edit views + focusable empty Show-page sections (`j` -> `n`) + tidy breadcrumbs.
- Rich `lane` schema, timeline, `lane.*` MCP tools, and opt-outable Ambient Lane Progress Bar (`ctrl+a L`).
- Live PTY + VT `ptyhost/` (`ctrl+a d`/`z`/`esc`/`[`), real OS command `runs/`, live `mcpui` stdio JSON-RPC 2.0 server (`mato mcp serve`), and read-only v1 `migrate/`.
- Daily-driver pack: focused-record actions picker (`;`), yank picker (`y` / `y y`), macros (`q` / `@`), global scratches (`ctrl+a S` / `ctrl+a V`), `handlers.json` + inline Markdown reader (`o` / `O`), actions/modes/bindings/schemas inspector (`mato inspect`), signals + `expr` triage rules (`R`, `triage.*` MCP tools), Readline across all 5 input modes, live extension catalog mounting + async `ReadCache` + lazy shadows, and `!` / `|` / `r!` shell I/O redirection.

### Remaining in Matomeru v2
1. **Data-flow encapsulation (managed storage):**
   - Route notes, documents, artifacts, and exports through one canonical managed directory under Matomeru's data dir (TUI, CLI, and MCP all hitting the same boundary) plus one global newest-first recents/history picker across all managed records.
   - Encode the rule in `~/hub/AGENTS.md` once confirmed.
2. **Real `mtmd` `DaemonHost` integration and testing:**
   - `ptyhost/daemon.go` exists (opt-in via `"terminal.daemon": true`) with unit tests over injectable `ExecFunc`, but needs live verification against a real `mtmd` daemon, `MTM_PANE_ID` auto-injection checks, and remote daemon management.
3. **Outline overlay polish (`T`):**
   - Reorder with counts and across parents, quick inline rename (`i`), status cycling in fewer steps, and undo for outline moves.
4. **Filtering UX and saved views:**
   - Multi-condition filter syntax (`!=`, `a|b`), saved filters per noun, and newest-first global search/recents picker.
5. **Panes and Open UI polish:**
   - Context harvesting from pane scrollback (with setting to skip tool/thinking output) and scraping any pane/file/page on demand.
   - Grouped multi-question decision forms in the pane right rail and multi-agent MCP transport (socket/HTTP alongside stdio) plus destructive-only rights checks.
6. **CLI parity and v1 cutover:**
   - Expose full CRUD/query CLI subcommands alongside `mato inspect` and `mato mcp serve` so every verb is reachable from CLI as well as TUI and MCP.
   - Final v1 `mtm.db` cutover and swapping `mtm` to v2 when ready.

---

## 3. Status of every `o-*` extension

Out of 25 `o-*` repos in `~/proj/`: **2 are ported to Ochazuke v2**, **3 are superseded by Matomeru v2 built-ins**, **2 are standalone non-Ochazuke CLIs**, and **16 remain on Ochazuke v1** (`v0.24` to `v0.144.1`).

### Ported to Ochazuke v2 (done and installed)
| Tool | v1 LOC | v2 LOC | Installed binary | Notes |
|---|---:|---:|---|---|
| `o-csv` -> `o-csv-next` | 6,445 | 3,328 | `~/bin/o-csv2` | Full table/edit/Readline parity; still needs `catalog`/`query` if mounted as a noun |
| `o-json` -> `o-json-next` | 81,329 | 7,562 | `~/bin/o-json2` | Relational flatten, real `jq` bar, Convert Shape (`C`), closure folding (`za`/`zR`), surround + Readline |

### Superseded by Matomeru v2 built-ins (no port needed)
| Tool | v1 LOC | Replaced by in `mato` |
|---|---:|---|
| `o-lane` | 24,445 | Built-in `lane` schema, timeline, Ambient Lane Progress Bar, `lane.*` MCP tools |
| `o-questions` | 28,749 | Built-in MCP Open UI (`ui.ask`, `Questions` and `Decisions` pane pages) |
| `o-md` | 7,965 | Built-in `ochazuke/widgets/markdown` reader + `handlers.json` (`o`) |

### Standalone CLIs (do not use Ochazuke, no port needed)
| Tool | LOC | Notes |
|---|---:|---|
| `o-dotfiles` | 4,591 | Dotfiles manager and pre-commit audit hook |
| `o-modes` | 2,753 | Standalone CLI |

### High-priority extensions to bridge or port next
| Tool | v1 LOC | v1 Ochazuke | Has `catalog`? | Recommended v2 plan |
|---|---:|---|---|---|
| `o-git` | 22,483 | `v0.144.1` | Yes | Port to Ochazuke v2 reusing `widgets/diff`, or bridge `catalog`/`query` into `mato` first for branches/PRs/status on Project Show pages |
| `o-ai` | 12,135 | `v0.144.1` | Yes | Port or bridge to manage MCPs, skills, agents, hooks, and harness configs as tracked Matomeru records |
| `o-gh` | 6,846 | `v0.144.1` | Yes | Wire as a stateless connector feeding `mato`'s `signal` + `triage.rule` engine (and mount PR/issue catalog) |
| `o-slack` | 5,787 | `v0.133.0` | Yes | Wire as a stateless connector feeding `mato`'s `signal` + `triage.rule` engine |
| `o-cmds` | 13,302 | `v0.144.1` | Yes | Decide whether saved commands fold into `mato` Runs or mount as a lightweight v2 catalog |
| `o-links` | (new) | none yet | Planned | Build small v2 replacement for `h-link` (URL templates + placeholder forms + `o-links catalog`/`query`) |

### Other v1 extensions (keep running on v1 Ochazuke until needed)
| Tool | v1 LOC | v1 Ochazuke | Has `catalog`? |
|---|---:|---|---|
| `o-inventory` | 6,933 | `v0.144.1` | Yes |
| `o-df` | 5,526 | `v0.144.1` | Yes |
| `o-fleet` | 5,040 | `v0.144.1` | Yes |
| `o-qbt` | 4,991 | `v0.144.1` | Yes |
| `o-koe` | 4,520 | `v0.134.0` | Yes |
| `o-services` | 3,645 | `v0.144.1` | No |
| `o-proj` | 2,861 | `v0.144.1` | Yes |
| `o-mcp` | 8,660 | `v0.105.0` | Yes |
| `o-mermaid` | 1,707 | `v0.134.0` | No |
| `o-animation` / `o-animation-editor` | 1,652 | `v0.24.0` | Yes |

### Quick win available right now for v1 extensions
`matomeru-next/ext/ext.go` currently lists only 9 names in `DefaultTools` (`o-csv`, `o-git`, `o-lane`, `o-json`, `o-links`, `o-md`, `o-cmds`, `o-questions`, `o-df`) and expects `TOOL catalog` + `TOOL query`. Because 18 existing v1 tools already implement a `catalog` command, adding a thin v1-catalog compatibility reader (plus expanding `DefaultTools` to include `o-ai`, `o-gh`, `o-slack`, `o-fleet`, `o-inventory`, `o-proj`, `o-qbt`, `o-services`) would let `mato` discover and mount existing v1 extensions read-only right away while we port only the ones that need a v2 TUI rewrite.
