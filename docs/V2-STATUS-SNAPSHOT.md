# V2 migration status snapshot (01:42)

## Completed

All five priority extensions are migrated onto Ochazuke v2, verified with `go vet`, `-race` tests, and `--smoke`, committed cleanly on local branch `next`, and installed side-by-side in `~/bin/`. All v1 `main` repos and v1 binaries remain untouched.

| Binary | Worktree (`next` commit) | v1 size | v2 size | Exported `mato` catalog nouns |
| --- | --- | ---: | ---: | --- |
| `~/bin/o-df2` | `~/proj/o-df-next` (`9d15403`) | 32M | 8.3M | `df.culprit` |
| `~/bin/o-md2` | `~/proj/o-md-next` (`1d92764`) | 32M | 9.2M | `md.document` |
| `~/bin/o-cmds2` | `~/proj/o-cmds-next` (`3fb32d3`) | 33M | 8.4M | `cmds.command`, `cmds.job` |
| `~/bin/o-lane2` | `~/proj/o-lane-next` (`04eafe4`) | 34M | 9.4M | `lane.lane`, `lane.plan` |
| `~/bin/o-questions2` | `~/proj/o-questions-next` (`4bc21f5`) | 34M | 8.7M | `questions.question` |

Combined with `m2m` (`mato`, 17M vs 82M v1), `o-csv2` (8.0M vs 31M v1), and `o-json2` (8.6M vs 35M v1), all eight v2 binaries total 77.6M versus 313M in v1 (a 4.0x reduction).

Full extension wave report: `http://127.0.0.1:4387/items/1080` (`http://studio/items/1080`).

## Currently working (1 active agent)

`mtm-wavec-worker` (`gemini-4-neon-medium`) is finishing Matomeru v2 Wave C in `~/proj/matomeru-next`. All four pillars are already written in the working tree and are now moving through test and verification:

1. **Full CLI parity (`cmd/mtm/nouns.go`, `cmd/mtm/nouns_test.go`, `cmd/mtm/main.go`):** Noun CRUD/query subcommands (`task`, `pane`, `note`, `document`, `lane`, `schedule`, `run`, `signal`, `extension`, `binding`), stdin `-`, `mtm binding list/check`, `mtm extension discover/run`, and `mtm undo`/`redo`.
2. **Data-Flow Encapsulation and global Recents (`data/files.go`, `data/files_test.go`, `mcpui/datatools.go`, `app/open.go`, `app/app.go`):** Canonical managed storage root (`notes/`, `documents/`, `artifacts/`) shared across TUI, CLI, and MCP, plus the global newest-first Recents picker (`g r`).
3. **Outline (`T`) editing (`app/outline.go`, `data/query.go`, `data/schema.go`):** Sibling reorder (`J`/`K` with Vim counts), indent/outdent (`>`/`<`), status cycle (`space`), inline rename (`i`), and single-step undo.
4. **Live v2 extension mounting (`ext/ext.go`, `ext/catalog.go`, `app/ext_catalog_test.go`):** Side-by-side discovery of `o-*2` binaries and live catalog integration tests against the real installed v2 tools.

## Queued next (as soon as `mtm-wavec-worker` lands)

1. Verify and commit `matomeru-next` Wave C + rebuild `~/bin/m2m` (`mato`).
2. Run Ochazuke v2 polish in `~/proj/ochazuke-next` (kept serialized so `matomeru-next` never compiles against a mid-edit local `replace ../ochazuke-next`):
   - Multi-key sort, visual row marking (`V`), cell-select mode, and multi-condition filters (`!term`, `a|b`) in `widgets/table`.
   - Scope-aware word boundaries (`code`, `path`, `json`, `csv`, `markdown`) and styled ANSI copy-mode adapter in `widgets/input`.
   - (Tree guide lines `├─ └─ │` are already implemented in `ui/draw_content.go`.)
3. Rebuild `m2m` and the `o-*2` binaries onto the polished Ochazuke v2 commit and publish the final cutover readiness report.
