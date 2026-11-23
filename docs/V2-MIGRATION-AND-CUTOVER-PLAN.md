# v2 migration plan (priority 5 extensions, Matomeru, Ochazuke) and primary cutover

## Exact scope of the 5 priority extensions (excluding `.worktrees`)

| Tool | Real v1 Go LOC | Data / config kept 100% compatible | Key Ochazuke v2 reuse |
|---|---:|---|---|
| `o-df` -> `o-df-next` (`o-df2`) | 3,141 | `~/.config/o-df/culprits.json` (plus built-in defaults) | `widgets/table`, `widgets/picker`, `widgets/form`, `widgets/input`, `tea` + `catalog`/`query` |
| `o-md` -> `o-md-next` (`o-md2`) | 3,070 | Existing Markdown notes/docs/trash directories | `widgets/markdown`, `widgets/table`, `widgets/picker`, `widgets/input` + `catalog`/`query` |
| `o-cmds` -> `o-cmds-next` (`o-cmds2`) | 4,783 | Existing commands & run history store | `widgets/table`, `widgets/form`, `widgets/markdown`, `widgets/input`, `tea` + `catalog`/`query` |
| `o-lane` -> `o-lane-next` (`o-lane2`) | 7,640 | Existing `o-lane` JSON store & backups | `widgets/table`, `widgets/form`, `widgets/picker`, `widgets/input` + `catalog`/`query` |
| `o-questions` -> `o-questions-next` (`o-questions2`) | 8,500 | Existing question journal, decision docs, web UI, MCP | `widgets/table`, `widgets/form`, `widgets/markdown`, `widgets/input` + `catalog`/`query` |

Every v2 port follows the same rules proven by `o-csv-next` and `o-json-next`:
- Built in a clean sibling worktree (`/Users/unixsuperhero/proj/<tool>-next` on branch `next`), leaving v1 `main` untouched.
- Keeps the tool's existing pure domain logic and on-disk storage/config format intact so zero user data is lost or migrated destructively.
- Replaces only the Ochazuke v1 TUI/CLI shell with Ochazuke v2 (`replace ofam.dev/ochazuke => ../ochazuke-next`), adding Readline input bindings and native `<tool> catalog` + `<tool> query` so `mato` mounts its nouns live into `g n` + async `ReadCache` + lazy shadows.
- Installed side-by-side as `~/bin/<tool>2` (`o-df2`, `o-md2`, `o-cmds2`, `o-lane2`, `o-questions2`) until cutover.

---

## Execution waves

### Wave A: Smallest & fastest 3 extensions (starting now)
1. **`o-df-next` (`o-df2`)** — Keep `culprits.go`, `culprits.json`, `resolve.go`, `walk.go`, `scan.go`, `probe.go`, `trim.go`, `actions.go`, `stat_*.go`; rebuild TUI/CLI cleanly on Ochazuke v2 + `o-df catalog` + `o-df query` + `--smoke`.
2. **`o-md-next` (`o-md2`)** — Keep `doc_store.go`, `file_ops.go`, `namespace.go`, `notes.go`, `scratch.go`, `trash.go`, `open.go`, `relations.go`; rebuild TUI/CLI on `ochazuke/widgets/markdown` + `widgets/table` + `o-md catalog` + `o-md query` + `--smoke`.
3. **`o-cmds-next` (`o-cmds2`)** — Keep `store.go`, `run_store.go`, `run_capture.go`, `run_state.go`, `process.go`, `job_io.go`, `mcp.go`; rebuild TUI/CLI on Ochazuke v2 + `o-cmds catalog` + `o-cmds query` + `--smoke`.

### Wave B: `o-lane-next` and `o-questions-next`
4. **`o-lane-next` (`o-lane2`)** — Keep domain/store/rollup/stale/backup/MCP intact; port TUI/CLI to Ochazuke v2 + `catalog`/`query` (ensuring unknown lane statuses are never refused).
5. **`o-questions-next` (`o-questions2`)** — Keep journal/store/decision/web/blocker/backup/MCP intact; port TUI/CLI to Ochazuke v2 + `catalog`/`query` (fixing v1 decision-answer loss cleanly).

### Wave C: Remaining Matomeru v2 & Ochazuke v2 cutover gates
6. **Matomeru v2 CLI parity (`cmd/mtm`)** — Full CLI subcommands (`task`, `pane`, `note`, `document`, `lane`, `schedule`, `run`, `signal`, `extension`, `binding`) so scripts and AI agents calling `mtm ...` work identically on v2.
7. **Matomeru v2 Data-Flow Encapsulation + Outline (`T`) + Filtering UX** — Canonical managed storage directory for notes/docs/artifacts + global newest-first recents picker; Outline reorder/rename/undo; multi-condition (`!=`, `a|b`) & saved filters; expand `ext.DefaultTools` to discover all v2 tools cleanly.
8. **Matomeru v2 live `mtmd` `DaemonHost` verification** — Live integration verification against a real `mtmd` daemon (`"terminal.daemon": true`, `MTM_PANE_ID` auto-injection).
9. **Ochazuke v2 final polish** — Multi-key sort + cell/visual selection in `widgets/table`, styled ANSI copy-mode adapter, scope-aware word boundaries, and tree guide lines.

---

## When can we cut over to make v2 the primary?

### Cutover readiness checklist
We can cut over (`~/bin/mtm` -> v2, `~/bin/o-{df,md,cmds,lane,questions,csv,json}` -> v2, and merge `next` -> `main`) as soon as these 4 gates pass:
1. **All 5 priority extensions** (`o-df2`, `o-md2`, `o-cmds2`, `o-lane2`, `o-questions2`) pass `-race` + `--smoke` and mount live in `mato` via `R` (`extension.refresh`) + `g n`.
2. **Matomeru v2 CLI parity** covers the `mtm` subcommands that your shell workflows and agents (`AGENTS.md`, `o-lane`, hooks) invoke daily.
3. **Real `mtmd` daemon integration** is verified live on your Mac (attach/detach, pane survival across `mato` restart, `MTM_PANE_ID` env).
4. **1 to 2 days of real daily-driving** with `mato` + `*2` binaries on a fresh migration of `~/.local/share/matomeru/mtm.db`, keeping `~/bin/mtm-v1` (and v1 `o-*-v1` backups) in place for instant 1-command rollback if anything surprises you.

### Realistic timing
- **Waves A + B (the 5 priority extensions):** ~1 focused session (running Wave A and Wave B in parallel workers, reusing each tool's pure Go domain/store code).
- **Wave C (Matomeru CLI parity + managed storage + `mtmd` live check + Ochazuke table/copy-mode polish):** ~1 focused session right after.
- **Primary cutover:** Ready to switch `mato` + the 7 v2 tools (`o-csv`, `o-json`, `o-df`, `o-md`, `o-cmds`, `o-lane`, `o-questions`) to primary immediately after Waves A–C plus your 1–2 day burn-in check. Other v1 tools (`o-git`, `o-ai`, `o-gh`, `o-slack`, `o-inventory`, etc.) can keep running unchanged alongside v2 and migrate cleanly afterward without blocking cutover!
