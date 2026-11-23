# V2 Cutover Progress Update #3: Waves D2 and E Complete, Wave F Active

## Full wave status (ordered)

| # | Wave | Scope | Status |
| --- | --- | --- | --- |
| 1 | **Ochazuke v2 Core + Readline** | Core kernel, layout, buffer, UI, Tea, widgets, full Readline + cursor placement | Done |
| 2 | **Matomeru v2 Daily-Driver + Shell I/O + Catalogs** | 4 spaces, `;` actions picker, focusable empty Show sections, Readline, live extension catalogs + lazy shadows, `!`/`\|`/`g !` shell I/O | Done |
| 3 | **Data Tools (`o-json2`, `o-csv2`)** | Readline everywhere; `o-json2` Convert Shape (`C`, verified vs `jq`) + Raw JSON closure folding (`za`/`zM`/`zR`) | Done |
| 4 | **Wave A Extensions** | `o-df2`, `o-md2`, `o-cmds2` migrated onto Ochazuke v2 with `catalog` + `query` | Done |
| 5 | **Wave B Extensions** | `o-lane2`, `o-questions2` migrated onto Ochazuke v2 with `catalog` + `query` + `Store.Redo()` | Done |
| 6 | **Matomeru v2 Wave C** | Full noun CLI parity, managed storage (`data/files.go`), `g r` Recents, Outline (`T`) editing + undo, `o-*2` live catalog tests | Done |
| 7 | **Ochazuke v2 Widget Polish + `widgets/palette`** | Multi-key sort, multi-condition filters, visual/cell select, word scopes, ANSI copy-mode, reusable `widgets/palette` live args form | Done |
| 8 | **Matomeru v2 UX Polish + Form Drafts** | Live palette args form (`:`), `1 Panes` `n` & `t` TOC toggle, CLI human `<id>` + TTY picker, explicit Submit (`ctrl+s` + `[ Submit ]`) + auto-saved persistent form drafts (`[`/`]`/`D`) | Done |
| 9 | **Backup Push (`origin/next`)** | Pushed all 9 initial `next` branches to GitHub `origin/next` | Done |
| 10 | **Oden v2 + `ochazuke/cli` Bridge** | Pure-stdlib `oden` v2 (`1c7205a`) + Tier 3 `ofam.dev/ochazuke/cli` (`7d29578f`) | Done |
| 11 | **Wave D1 (Named Tools, Batch 1)** | `o-fleet2` (`fbeb9ff`), `o-gh2` (`adbecb2`), `o-proj2` (`5157488`) onto Ochazuke v2 + Oden v2 + `ochazuke/cli` | Done |
| 12 | **Wave D2 (Named Tools, Batch 2)** | `o-git2` (`3a2bce8`), `o-ai2` (`84902ad`) onto Ochazuke v2 + Oden v2 + `ochazuke/cli` | **Done** |
| 13 | **Wave E (Cherry-Picked Tools)** | `o-mermaid2` (`46bfc57`), `o-services2` (`888c1d8`) onto Ochazuke v2 + Oden v2 + `ochazuke/cli` | **Done** |
| 14 | **Wave F (Unify Existing v2 CLIs onto `oden` + `ochazuke/cli`)** | `mtm-oden-worker` (`matomeru`), `wavef-tools-1` (`o-df2`, `o-md2`, `o-cmds2`, `o-csv2`), `wavef-tools-2` (`o-json2`, `o-lane2`, `o-questions2`) | **Active now (3 workers + monitor)** |
| 15 | **Wave G (Cutover, Release Tags, Push, & Developer ID Signing)** | Pre-cutover audit, `v1-archive` tags, move all 17 repos' `main` into `~/proj/<repo>`, rewrite `../ochazuke` + `../oden` replaces, remove `-next` worktrees, migrate Matomeru DB/config (`*.v1-backup` + `mtm migrate --apply`), tag `v1.0.0`, push to GitHub, install into `~/bin/` (with `~/bin/v1-backup/`), Developer ID `codesign` | Queued next |

## What was done

- Completed and verified all remaining tool migrations in Waves D2 and E (all 14 target `o-*` tools are now migrated onto Ochazuke v2!):
  - **`o-git2`** (`~/proj/o-git-next`, `3a2bce8`, `8.7M`): 4 tabs + built-in `widgets/diff`, `196 bindings, 0 errors`, `18/18 --smoke`, `-race` green.
  - **`o-ai2`** (`~/proj/o-ai-next`, `84902ad`, `14M`): 7 inventory/quota views + `harness/` + MCP preserved, `154 bindings, 0 errors`, `--smoke` ok, `-race` green.
  - **`o-mermaid2`** (`~/proj/o-mermaid-next`, `46bfc57`, `11M`): diagram/theme explorer + `pathpicker`, `151 bindings, 0 errors`, `--smoke` ok, `-race` green.
  - **`o-services2`** (`~/proj/o-services-next`, `888c1d8`, `8.6M`): listening endpoints + safe open/kill confirmation, `163 bindings, 0 errors`, `24/24 --smoke`, `-race` green.
- Completed and opened the groomable **V1 vs V2 Parity Matrix** (`docs/V1-V2-PARITY-MATRIX.md`, `http://127.0.0.1:4387/items/1090`).
- Added the read-only **pre-cutover audit script** (`scripts/pre-cutover-audit.sh`).
- Activated `monitor` (`swarm_monitor`) alongside all three Wave F workers (`mtm-oden-worker`, `wavef-tools-1`, `wavef-tools-2`).

## What is left to do

1. Finish & verify Wave F (`matomeru` + the earlier 7 `o-*2` CLIs unified onto `oden` v2 + `ochazuke/cli`).
2. Execute Wave G: run `scripts/pre-cutover-audit.sh`, tag `v1-archive`, cut over all 17 repos into canonical `~/proj/<repo>`, migrate Matomeru's DB/config cleanly (with `.v1-backup`), tag `v1.0.0`, push to GitHub, build & install all binaries into `~/bin/` (backing up v1 to `~/bin/v1-backup/`), and `codesign` every binary with Apple Developer ID.
