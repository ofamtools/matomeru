# V2 Cutover Progress Update #2: Wave D1 Complete, Wave D2 + E Active

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
| 11 | **Wave D1 (Named Tools, Batch 1)** | `o-fleet2` (`fbeb9ff`), `o-gh2` (`adbecb2`), `o-proj2` (`5157488`) onto Ochazuke v2 + Oden v2 + `ochazuke/cli` | **Done** |
| 12 | **Wave D2 (Named Tools, Batch 2)** | `o-git2`, `o-ai2` onto Ochazuke v2 + Oden v2 + `ochazuke/cli` | **Active now** |
| 13 | **Wave E (Cherry-Picked Tools)** | `o-mermaid2`, `o-services2` onto Ochazuke v2 + Oden v2 + `ochazuke/cli` | **Active now** |
| 14 | **Wave F (Unify Existing v2 CLIs onto `oden` + `ochazuke/cli`)** | Wire `matomeru` + the earlier 7 `o-*2` CLIs onto `oden` v2 + `ochazuke/cli` | Queued next |
| 15 | **Wave G (Cutover, Release Tags, Push, & Developer ID Signing)** | Archive v1 tags, move all 17 repos' `main` into `~/proj/<repo>`, rewrite `../ochazuke` + `../oden` replaces, remove `-next` worktrees, migrate Matomeru DB/config (`*.v1-backup` + `mtm migrate --apply`), tag `v1.0.0`, push to GitHub, install into `~/bin/` (backing up v1 to `~/bin/v1-backup/`), Developer ID `codesign` | Queued |

## What was done

- Completed and verified all three Wave D1 migrations on `ochazuke-next` + `oden-next` + `ofam.dev/ochazuke/cli`:
  - **`o-fleet2`** (`~/proj/o-fleet-next`, `fbeb9ff`, `8.6M`): `go vet`, `-race` (11/11), `binding check` (201 bindings, 0 errors), `--smoke` ok, `catalog` (`fleet.machine`, `fleet.check`) + `capabilities --json` verified.
  - **`o-gh2`** (`~/proj/o-gh-next`, `adbecb2`, `8.6M`): `go vet`, `-race`, `binding check` (130 bindings, 0 errors), `--smoke` ok, `catalog` (`gh.pr`, `gh.collection`) + `capabilities --json` verified.
  - **`o-proj2`** (`~/proj/o-proj-next`, `5157488`, `11M`): `go vet`, `-race`, `binding check` (170 bindings, 0 errors), `--smoke` (22/22 checks), `catalog` (`proj.project`, `proj.template`) + `capabilities --json` verified.
- Retired Wave D1 workers and immediately launched all four Wave D2 + E workers (`ogit-worker`, `oai-worker`, `omermaid-worker`, `oservices-worker`) in parallel.

## What is left to do

1. Finish & verify Wave D2 (`o-git2`, `o-ai2`) and Wave E (`o-mermaid2`, `o-services2`).
2. Run Wave F: unify `matomeru` (`cmd/mtm`) and the earlier 7 `o-*2` binaries (`o-csv2`, `o-json2`, `o-df2`, `o-md2`, `o-cmds2`, `o-lane2`, `o-questions2`) onto `oden` v2 + `ochazuke/cli`.
3. Execute Wave G: full cutover of all 17 repos into canonical `~/proj/<repo>`, canonical Matomeru DB & config migration (with `.v1-backup`), `v1.0.0` release tags, GitHub push, `~/bin/` installation (with `~/bin/v1-backup/`), and Apple Developer ID `codesign` + verification.
