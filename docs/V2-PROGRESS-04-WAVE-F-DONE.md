# V2 Cutover Progress Update #4: Wave F Complete, Launching Wave G (Cutover, Release, Signing)

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
| 11 | **Wave D1 (Named Tools, Batch 1)** | `o-fleet2` (`fbeb9ff`), `o-gh2` (`adbecb2`), `o-proj2` (`5157488`) | Done |
| 12 | **Wave D2 (Named Tools, Batch 2)** | `o-git2` (`3a2bce8`), `o-ai2` (`84902ad`) | Done |
| 13 | **Wave E (Cherry-Picked Tools)** | `o-mermaid2` (`46bfc57`), `o-services2` (`888c1d8`) | Done |
| 14 | **Wave F (Unify Existing v2 CLIs onto `oden` + `ochazuke/cli`)** | `matomeru` (`cmd/mtm`) + `o-df2`, `o-md2`, `o-cmds2`, `o-csv2`, `o-json2`, `o-lane2`, `o-questions2` all unified onto `oden` v2 + `ochazuke/cli` | **Done** |
| 15 | **Wave G (Cutover, Release Tags, Push, & Developer ID Signing)** | Pre-cutover audit, `v1-archive` tags, move all 17 repos' `main` into `~/proj/<repo>`, rewrite `../ochazuke` + `../oden` replaces, remove `-next` worktrees, migrate Matomeru DB/config (`*.v1-backup` + `mtm migrate --apply`), tag `v1.0.0`, push to GitHub, install all binaries into `~/bin/` (backing up v1 to `~/bin/v1-backup/`), Developer ID `codesign` | **Starting now** |

## What was done

- Completed **Wave F** across ALL 8 earlier v2 executables so every single one of the 15 executables (`mtm` + 14 `o-*` tools) now runs on `oden` v2 + `ofam.dev/ochazuke/cli` with uniform unique-prefix dispatch (`mtm bi ch`), rich `help` / `help --all` / `which`, `binding check` (0 errors across all 15 tools!), `inspect actions|bindings|modes`, `capabilities --json`, `catalog`, and `query`:
  - `matomeru-next` (`cmd/mtm` + expanded `ext.DefaultTools` for all 14 tools)
  - `o-df-next` (`483dda0`, `8.7M`), `o-md-next` (`6e54de0`, `9.7M`), `o-cmds-next` (`b2e272c`, `8.8M`), `o-csv-next` (`39deead`, `8.5M`)
  - `o-json-next` (`3c0c51c`, `8.9M`), `o-lane-next` (`400a1ea`, `9.9M`), `o-questions-next` (`180eacc`, `9.1M`)

## What is left to do

1. **Wave G (starting now):**
   - Wait for `portfolio-worker` to finish interactive checkboxes + `V1-V2-PARITY-MATRIX.md` update (takes ~2 min) and commit `V1-V2-PARITY-MATRIX.md`.
   - Run `scripts/pre-cutover-audit.sh` across all 17 repos.
   - Tag `v1-archive` in all 17 repos so v1 history is preserved forever.
   - Cut over `main` to `next` in all 17 canonical `~/proj/<repo>` directories, update `replace` lines (`../ochazuke-next` -> `../ochazuke`, `../oden-next` -> `../oden`), point `matomeru` canonical DB/config paths to `~/.local/share/matomeru/` and `~/.config/matomeru/` (backing up v1 to `*.v1-backup-2026-11-23` first, then running `mtm migrate --apply`).
   - Verify builds + `--smoke` + `binding check` across all 17 canonical repos and remove the 17 `-next` worktrees cleanly.
   - Tag `v1.0.0` (and push `main`, `v1-archive`, `v1.0.0` to GitHub across all 17 repos).
   - Back up v1 binaries to `~/bin/v1-backup/`, install all 16 freshly built binaries (`o`, `mtm`, plus all 14 `o-*` tools) into `~/bin/`, sign every binary with `"Developer ID Application: Joshua Toyota (R78J3KN7M4)"` + verify `codesign --verify --deep --strict`, and publish the final cutover report in Markdown + `mdoc`.
