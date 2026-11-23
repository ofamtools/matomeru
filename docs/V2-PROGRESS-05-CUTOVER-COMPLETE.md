# V2 Cutover Complete (Final Progress Update #5): All 17 Repos Released as v1.0.0, Pushed, and Developer ID Signed

All 15 waves through Wave G are complete. Every single repository now lives on `main` in its canonical `~/proj/<repo>` directory with zero `-next` worktrees, zero local `replace` directives, official `v1.0.0` (and `v1-archive`) tags pushed to GitHub, canonical Matomeru DB and config migrated with full `.v1-backup-2026-11-23` backups, and all 16 executables built into `~/bin/` and signed with your Apple Developer ID certificate.

## Full wave status (ordered)

| # | Wave | Scope | Status |
| --- | --- | --- | --- |
| 1 | **Ochazuke v2 Core + Readline** | Core kernel, layout, buffer, UI, Tea, widgets, full Readline + cursor placement | Done |
| 2 | **Matomeru v2 Daily-Driver + Shell I/O + Catalogs** | 4 spaces, `;` actions picker, focusable empty Show sections, Readline, live extension catalogs + lazy shadows, `!`/`\|`/`g !` shell I/O | Done |
| 3 | **Data Tools (`o-json`, `o-csv`)** | Readline everywhere; `o-json` Convert Shape (`C`, verified vs `jq`) + Raw JSON closure folding (`za`/`zM`/`zR`) | Done |
| 4 | **Wave A Extensions** | `o-df`, `o-md`, `o-cmds` migrated onto Ochazuke v2 with `catalog` + `query` | Done |
| 5 | **Wave B Extensions** | `o-lane`, `o-questions` migrated onto Ochazuke v2 with `catalog` + `query` + `Store.Redo()` | Done |
| 6 | **Matomeru v2 Wave C** | Full noun CLI parity, managed storage (`data/files.go`), `g r` Recents, Outline (`T`) editing + undo, live extension catalog tests | Done |
| 7 | **Ochazuke v2 Widget Polish + `widgets/palette`** | Multi-key sort, multi-condition filters, visual/cell select, word scopes, ANSI copy-mode, reusable `widgets/palette` live args form | Done |
| 8 | **Matomeru v2 UX Polish + Form Drafts** | Live palette args form (`:`), `1 Panes` `n` & `t` TOC toggle, CLI human `<id>` + TTY picker, explicit Submit (`ctrl+s` + `[ Submit ]`) + auto-saved persistent form drafts (`[`/`]`/`D`) | Done |
| 9 | **Backup Push (`origin/next`)** | Pushed initial `next` branches to GitHub | Done |
| 10 | **Oden v2 + `ochazuke/cli` Bridge** | Pure-stdlib `oden` (`1c7205a`) + Tier 3 `ofam.dev/ochazuke/cli` (`7d29578f`) | Done |
| 11 | **Wave D1 (Named Tools, Batch 1)** | `o-fleet`, `o-gh`, `o-proj` migrated onto Ochazuke + Oden + `ochazuke/cli` | Done |
| 12 | **Wave D2 (Named Tools, Batch 2)** | `o-git`, `o-ai` migrated onto Ochazuke + Oden + `ochazuke/cli` | Done |
| 13 | **Wave E (Cherry-Picked Tools)** | `o-mermaid`, `o-services` migrated onto Ochazuke + Oden + `ochazuke/cli` (skipping `o-koe`, `o-qbt`, `o-slack`, `o-mcp`, `o-inventory`) | Done |
| 14 | **Wave F (Unify Earlier CLIs)** | `matomeru` (`cmd/mtm`) + `o-df`, `o-md`, `o-cmds`, `o-csv`, `o-json`, `o-lane`, `o-questions` unified onto `oden` + `ochazuke/cli` | Done |
| 15 | **Wave G (Full Cutover, `v1.0.0` Release, Push, & Developer ID Signing)** | All 17 repos cut over to canonical `~/proj/<repo>` (`main`, `v1-archive`, `v1.0.0`, 0 local `replace` directives), DB/config migrated, all 16 binaries installed in `~/bin/` & signed | **Done** |

## What was done

### 1. All 17 repositories released as `v1.0.0` on GitHub (`main` + `v1-archive` + `v1.0.0`)

Every consumer module pins published `ofam.dev/ochazuke v1.0.0` and `ofam.dev/oden v1.0.0` with **zero local `replace` directives**, so `go get` and `go install ...@v1.0.0` work cleanly on any machine. All 17 `-next` worktrees have been removed.

| Repo | Canonical path | `main` commit (`v1.0.0`) | Local `replace` count | Binary size (v1 -> `v1.0.0`) | Verification |
| --- | --- | --- | ---: | --- | --- |
| `oden` | `~/proj/oden` | `1c7205a` | 0 | `o`: 4.3M -> 4.4M | `-race` pass, signed |
| `ochazuke` | `~/proj/ochazuke` | `db147238` | 0 | (library) | 19/19 pkgs `-race` pass |
| `matomeru` | `~/proj/matomeru` | `19ad0b3` | 0 | `mtm`: 82M -> 18M | `--smoke`, 412 bindings (0 err), signed |
| `o-csv` | `~/proj/o-csv` | `8b1426b` | 0 | 31M -> 8.5M | `--smoke` 19/19, 0 err, signed |
| `o-json` | `~/proj/o-json` | `cc6dea3` | 0 | 35M -> 8.9M | `--smoke` pass, 0 err, signed |
| `o-df` | `~/proj/o-df` | `c86d3aa` | 0 | 32M -> 8.7M | `--smoke` 21/21, 0 err, signed |
| `o-md` | `~/proj/o-md` | `b025fa4` | 0 | 32M -> 9.6M | `--smoke` 19/19, 0 err, signed |
| `o-cmds` | `~/proj/o-cmds` | `35cb34c` | 0 | 33M -> 8.7M | `--smoke` 19/19, 0 err, signed |
| `o-lane` | `~/proj/o-lane` | `7171a26` | 0 | 34M -> 9.8M | `--smoke` 19/19, 0 err, signed |
| `o-questions` | `~/proj/o-questions` | `26bf6da` | 0 | 34M -> 9.1M | `--smoke` 17/17, 0 err, signed |
| `o-fleet` | `~/proj/o-fleet` | `f50d9dc` | 0 | 32M -> 8.6M | `--smoke` ok, 0 err, signed |
| `o-proj` | `~/proj/o-proj` | `1ca91a4` | 0 | 31M -> 11M | `--smoke` 22/22, 0 err, signed |
| `o-gh` | `~/proj/o-gh` | `e32932e` | 0 | 32M -> 8.6M | `--smoke` ok, 0 err, signed |
| `o-ai` | `~/proj/o-ai` | `b6454a4` | 0 | 37M -> 14M | `--smoke` ok, 0 err, signed |
| `o-git` | `~/proj/o-git` | `794f5c7` | 0 | 33M -> 8.7M | `--smoke` 18/18, 0 err, signed |
| `o-mermaid` | `~/proj/o-mermaid` | `ed85ae8` | 0 | 33M -> 11M | `--smoke` ok, 0 err, signed |
| `o-services` | `~/proj/o-services` | `bc2bda2` | 0 | 32M -> 8.6M | `--smoke` 24/24, 0 err, signed |

Total binary footprint across all 16 executables dropped from **565.3 MB** in v1 to **156.6 MB** in `v1.0.0` (a **3.6x** reduction).

### 2. Canonical Matomeru DB, config, binaries, and backups

- **Canonical DB (`~/.local/share/matomeru/mtm.db`):** Migrated cleanly from v1 in 150ms (39 projects, 233 tasks, 64 notes, 62 note-about relations, 22 directories, 1 schedule, 2 relations).
- **Canonical config (`~/.config/matomeru/`):** Replaced v1 config with v2 config (`mtm binding check`: 412 total, 0 errors).
- **Safe backups preserved:**
  - `~/bin/v1-backup-2026-11-23/` (all 16 v1 executables)
  - `~/.local/share/matomeru.v1-backup-2026-11-23/` and `~/.local/share/matomeru-next.bak-2026-11-23/`
  - `~/.config/matomeru.v1-backup-2026-11-23/` and `~/.config/matomeru-next.bak-2026-11-23/`
- **Clean `~/bin/`:** `m2m` and `mato` symlink to `~/bin/mtm`; all 14 temporary `~/bin/o-*2` binaries removed.
- **Apple Developer ID signing:** Every executable signed with `codesign --force --options runtime --timestamp -s "Developer ID Application: Joshua Toyota (R78J3KN7M4)"` and verified with `codesign --verify --deep --strict`.
- **Portfolio interactive checkboxes:** Updated `~/proj/portfolio` (`3c4d4ed`) + restarted `h-docs` so clicking any checkbox on the groomable Parity Matrix (`http://127.0.0.1:4387/items/1110`, backed by `~/proj/matomeru/docs/V1-V2-PARITY-MATRIX.md`) writes straight back to SQLite and disk.

## What is left to do (post-cutover backlog, ready whenever you want)

1. **Groom the V1 vs V2 Parity Matrix:** Check `[x] Keep / Drop / Later` directly on `http://127.0.0.1:4387/items/1110` (`~/proj/matomeru/docs/V1-V2-PARITY-MATRIX.md`).
2. **Optional post-cutover design waves (discussion-first via `/grill-me` / `/plan`):**
   - **Agent Hook Telemetry:** Port v1's `mtm agent hook` / `mtm-hook` + harness launch flags + structured pane `turn` (prompt / activity / final response) capture.
   - **TUI Storybook / Sandbox:** Interactive widget & page showcase + side-by-side agent UX proposal comparison.
   - **Unified Cross-Surface Actions Catalog + Reference Config Docs:** In-app filter/sort/group table showing Bindings, Context Stack, Palette, CLI, and MCP parity + single-command `config reference` generator.
   - **Hub Gate & Worktree CLI (`oden` v2 in `~/hub`):** Scoped/full gate runner logging `{ts, repo, scope, duration_ms, pass, commit}` to JSONL + open/merged/active/idle worktree overview.
   - **Outline (`T`) authoring polish:** In-outline sibling/child creation (`o`/`O`) and subtree fold/unfold (`z a`).
3. **Optional `/learn` pass:** Run `/learn` to persist tonight's architectural and workflow lessons into durable rules/skills.
