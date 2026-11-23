# V2 Cutover Progress Update #1: Wave D1 + o-git launched

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
| 11 | **Wave D1 (Named Tools, Batch 1)** | `o-fleet2`, `o-proj2`, `o-gh2` onto Ochazuke v2 + Oden v2 + `ochazuke/cli` | **Active now** |
| 12 | **Wave D2 (Named Tools, Batch 2)** | `o-git2` (active now) + `o-ai2` (queued next) | **Active (`o-git2`) / Queued (`o-ai2`)** |
| 13 | **Wave E (Cherry-Picked Tools)** | `o-mermaid2`, `o-services2` (skipping `o-koe`, `o-qbt`, `o-slack`, `o-mcp`, `o-inventory`) | Queued |
| 14 | **Wave F (Unify Existing v2 CLIs onto `oden` + `ochazuke/cli`)** | Wire `matomeru` + the earlier 7 `o-*2` CLIs onto `oden` v2 + `ochazuke/cli` | Queued |
| 15 | **Wave G (Cutover, Release Tags, Push, & Developer ID Signing)** | Tag `v1-archive`, move all 17 repos' `main` into `~/proj/<repo>`, rewrite `../ochazuke` + `../oden` replaces, remove `-next` worktrees, migrate Matomeru DB/config (`*.v1-backup` + `mtm migrate --apply`), tag `v1.0.0`, push to GitHub, install into `~/bin/` (backing up v1 to `~/bin/v1-backup/`), Developer ID `codesign` | Queued |

## What was done

- Completed and verified `oden` v2 (`~/proj/oden-next`, `1c7205a`, pure stdlib, zero deps) and Tier 3 `ofam.dev/ochazuke/cli` (`~/proj/ochazuke-next`, `7d29578f`, all 19 packages `-race` green).
- Created clean `next` worktrees for all 7 remaining migration targets: `o-fleet-next`, `o-proj-next`, `o-gh-next`, `o-ai-next`, `o-git-next`, `o-mermaid-next`, and `o-services-next`.
- Launched 4 parallel workers on `gemini-4-neon-medium` (`ofleet-worker`, `oproj-worker`, `ogh-worker`, `ogit-worker`) targeting Ochazuke v2 + Oden v2 + `ochazuke/cli`.

## What is left to do

1. Finish & verify Wave D1 (`o-fleet2`, `o-proj2`, `o-gh2`) and `o-git2`.
2. Run `o-ai2`, `o-mermaid2`, and `o-services2` (finishing Wave D2 + Wave E).
3. Run Wave F: unify `matomeru` (`cmd/mtm`) and the earlier 7 `o-*2` binaries onto `oden` v2 + `ochazuke/cli` so all 15 executables share one consistent CLI/help/flag/picker contract.
4. Execute Wave G: full cutover of all 17 repos into canonical `~/proj/<repo>`, canonical Matomeru DB & config migration (with full `.v1-backup`), `v1.0.0` release tags, push to GitHub, build into `~/bin/`, and Developer ID `codesign` + verification.
