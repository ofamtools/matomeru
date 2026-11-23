# Matomeru v2 Day-1 and Post-Cutover Build Checklist

Created: 2026-11-23. Built from the groomed parity discussions across Tiers 1 through 4.

## Time estimates summary

| Wave | Scope | Estimated wall time | Workday milestone |
| --- | --- | --- | --- |
| Wave 1 (In progress now) | Shared Ochazuke primitives + Matomeru daemon, HTTP MCP, hooks, harness config, context extractor | 15 to 25 minutes | Core packages tested in parallel |
| Wave 2 | Wire Topic 1 Day-1 runtime into `cmd/mtm` and `app/`, build and sign `~/bin/mtm` and `~/bin/mtm-hook` | 15 to 20 minutes | **Day-1 usable build ready for workday** |
| Wave 3 | Tier 2 UX: stable `project-pane` rail + project context overview, live hints, prompt composer with async `o-ai`, batched `ui.ask` | 20 to 30 minutes | Full pane workflow + context harvesting |
| Wave 4 | Tier 3 UX + Ochazuke wiring: table presets per table ID, `actions.json`, namespace-grouped `;` actions, toasts, tree guides, focus jump hints | 15 to 25 minutes | Daily workspace polish complete |
| **Total** | **Day-1 ready after Wave 2 (30 to 45 min); full Tiers 1 to 4 complete after Wave 4 (65 to 100 min)** | | |

---

## Wave 1: Foundation packages (DONE)

### 1A. Ochazuke shared primitives (`~/proj/ochazuke`, commit `79ad3cae`)
- [x] `widgets/toast`: non-blocking transient notification strip with levels, TTL, dismiss, and opt-out support
- [x] Tree guide lines (`├─`, `└─`, `│`) in `ui/draw_content.go` for `ui.KindTree` and Outline
- [x] `widgets/hints`: shared letter-label overlay engine for both `layout.Solved.FocusOrder` jump hints and terminal link hints
- [x] `widgets/table` saved presets keyed by table ID (named presets, default preset per table, JSON serialization for `settings.json`)
- [x] `deps_test.go` updated and full `ochazuke` `-race` test suite passing (21/21 packages)

### 1B. Matomeru local daemon and shared HTTP MCP (`~/proj/matomeru`, commit `90aa08b`)
- [x] `daemon/` package: self-hosted local Unix-socket daemon over `ptyhost.LocalHost`
- [x] Pane survival across `mtm` restarts + `reset` op that drops all panes cleanly
- [x] Automatic injection of `MTM_PANE_ID`, `MTM_SOCKET`, and `MTM_MCP_URL` into every launched pane
- [x] Daemon hook telemetry store (`state`, `activity`, `subagent`) and blocking `PermissionRequest` approval queue + `resolve_approval`
- [x] `daemon/scheduler.go`: background cron ticker for `remind` (macOS desktop notification), `run`, and `check` writing `schedule.occurrence`
- [x] `mcpui/http.go`: shared local HTTP JSON-RPC MCP server (`/mcp` and `/mcp/{pane_id}`) with auto-bound pane ID and open agent rights (descriptions prefer archive over delete)
- [x] Batched `ui.ask` accepting a list of questions in one call

### 1C. Matomeru hooks, harness config, and context extractor (`~/proj/matomeru`, commit `90aa08b`)
- [x] `agenthook/` + standalone fast `cmd/mtm-hook` binary (also callable via `mtm agent hook`)
- [x] Adapters for `claude`, `codex`, `omp`, `gemini`/`antigravity` sending state, blocking approvals, tool activity, and subagent events
- [x] `harness/` package: `~/.config/matomeru/harnesses.json` (configurable launch commands and hook config) merged with installed `o-ai` `ai.harness` rows asynchronously
- [x] `contextitems/` package: high-signal extractor for URLs, OSC-8 links, file/document paths, and PR refs (no noisy SHAs or uncontextualized branches) + screen coordinate matches for live hints

---

## Wave 2: Day-1 Matomeru runtime integration (DONE — signed & installed!)
- [x] Register new packages in `matomeru/deps_test.go`
- [x] Wire `mtm daemon serve|status|reset|stop` and `mtm agent hook` subcommands onto `oden` in `cmd/mtm/`
- [x] Auto-connect `app/` to local daemon so panes survive closing `mtm`, plus in-app `daemon.reset` verb
- [x] Populate New Pane (`n`) harness picker asynchronously from `o-ai` + `harnesses.json`
- [x] Surface live hook state (`running`/`needs_you`/`idle`/`done`), active tool/subagent status, and interactive `PermissionRequest` approval modal in `1 Panes`
- [x] Wire `mtm schedule` pause/stop controls backed by the daemon ticker
- [x] Build, Apple Developer ID sign, and install `~/bin/mtm` and `~/bin/mtm-hook`
- [x] Verify live: daemon survive, `daemon reset`, HTTP MCP call, `mtm-hook` state/approval round-trip, `--smoke`, `binding check`

---

## Wave 3: Tier 2 pane workflow and talking to panes (DONE)
- [x] Stable `project-pane` rail grouping with `flat` setting (no `session` layer, panes never jump or reorder; color + indicator symbols for status)
- [x] Project header selection in `1 Panes` rail opens project overview on the right with notes, links, and filterable/sortable/groupable context items table
- [x] High-signal context harvesting from prompts, agent official responses, created files, and visible screen + searchable context picker (`g c`)
- [x] Live terminal hints (`ctrl+a f` / `f`) using `ochazuke/widgets/hints` (lowercase copy, `ctrl+label` open, uppercase insert into pane)
- [x] Prompt composer box (`send`, `queue`, `interrupt`, drafts) with async `@file`, `$skill`, `/command` completions fed from `o-ai` cache (`c` / `ctrl+a c`)
- [x] Multi-question `Questions` TOC page UI for batched `ui.ask` calls with Submit All

---

## Wave 4: Tier 3 workspace polish, confirmation dialogs, and Ochazuke wiring (DONE — signed & installed!)
- [x] Shared confirmation dialog (`confirm.quit` on `Q`/`ctrl+c`, `confirm.daemonReset` on `g R` showing live pane count, both default on with opt-out settings)
- [x] Table-level saved presets wired across Matomeru tables + `settings.json` (switch preset, save preset, set default per table ID via `g v`)
- [x] `~/.config/matomeru/actions.json` loader registering custom shell verbs into `core.CommandSpec`
- [x] Namespace-grouped `;` focused-record Actions picker (exact focused noun actions at top, generic record actions grouped underneath)
- [x] Wire `ochazuke/widgets/toast` for non-blocking notices (with `ui.toast` opt-out setting)
- [x] Wire `ochazuke` tree guide lines (`ui.treeGuides`) on Outline and tree views + focus jump hints (`g f`) over `layout.Solved.FocusOrder`
- [x] Final build, Developer ID sign & install `~/bin/mtm` and `~/bin/mtm-hook`, full `--smoke` + `binding check` verification

---

## Explicitly dropped or deferred to backlog
- **Dropped:** Privacy mode, separate `vim` noun, separate `saved` noun, `reviews` and action items, statusline parsing, passive JSONL transcript tailing, screen-scraping permission modes, destructive MCP permission walls, CLI MCP auto-wrapper, `grid`/`jsontree`/`kanban`/`timeline`, `shell-init`, dot-repeat, `o-koe`/`o-qbt`/`o-inventory`.
- **Backlog:** Multi-host remote daemons, `o-slack` stateless signal connector, static checkers / `o-story` (revisit with UI Sandbox prototype), `o-mcp` (leave v1 standalone for now; revisit folding live MCP calling into `o-ai` later).
