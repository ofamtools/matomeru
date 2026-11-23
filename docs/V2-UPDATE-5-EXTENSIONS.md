# V2 extension wave complete: all five priority tools migrated

All five requested extensions (`o-df`, `o-md`, `o-cmds`, `o-lane`, and `o-questions`) are migrated onto Ochazuke v2 in sibling `-next` worktrees on local branch `next`. Every v1 `main` repository and every v1 binary under `~/bin/` remains untouched.

## Summary of migrated extensions

| Tool | v2 binary | Worktree (`next` commit) | v1 size | v2 size | Exported catalog nouns | Verification |
| --- | --- | --- | ---: | ---: | --- | --- |
| `o-df` | `~/bin/o-df2` | `~/proj/o-df-next` (`9d15403`) | 32M | 8.3M | `df.culprit` | `go vet`, `-race` (2.56s), 21/21 `--smoke`, `catalog` JSON |
| `o-md` | `~/bin/o-md2` | `~/proj/o-md-next` (`1d92764`) | 32M | 9.2M | `md.document` | `go vet`, `-race` (1.86s), 19/19 `--smoke`, `catalog` JSON |
| `o-cmds` | `~/bin/o-cmds2` | `~/proj/o-cmds-next` (`3fb32d3`) | 33M | 8.4M | `cmds.command`, `cmds.job` | `go vet`, `-race` (3.80s), 19/19 `--smoke`, `catalog` JSON |
| `o-lane` | `~/bin/o-lane2` | `~/proj/o-lane-next` (`04eafe4`) | 34M | 9.4M | `lane.lane`, `lane.plan` | `go vet`, `-race` (7.72s), 19/19 `--smoke`, `catalog` JSON |
| `o-questions` | `~/bin/o-questions2` | `~/proj/o-questions-next` (`4bc21f5`) | 34M | 8.7M | `questions.question` | `go vet`, `-race` (7.38s), 17/17 `--smoke`, `catalog` JSON |

Alongside `m2m` (`mato`, 17M vs 82M v1), `o-csv2` (8.0M vs 31M v1), and `o-json2` (8.6M vs 35M v1), the full v2 suite now covers eight standalone binaries totaling 77.6M versus 313M in v1 (a 4.0x reduction).

## What each migration delivered

- **On-disk compatibility:** Each tool reads and writes its existing v1 JSON, Markdown, and history stores without breaking v1 tools.
- **Ochazuke v2 architecture:** Built on `ofam.dev/ochazuke` (`core`, `ui`, `tea`, `widgets/table`, `widgets/form`, `widgets/picker`, `widgets/markdown`, `widgets/input`, and `config`) with zero v1 `ochazuke` or `oden` dependencies.
- **Full Readline editing:** Every filter bar, prompt, form field, and picker wires `input.ReadlineSpecs()` and `input.ReadlineBindings(...)`.
- **Live Matomeru mounting:** Every binary exposes `catalog` (`ext.Manifest` JSON) and `query` (`{"items": [...], "total": n}`), so `mato` can mount and browse all seven external nouns (`df.culprit`, `md.document`, `cmds.command`, `cmds.job`, `lane.lane`, `lane.plan`, `questions.question`) through its async `ReadCache` and lazy local shadows.
- **MCP and CLI preserved:** `o-cmds2` keeps `shell-init`, job capture, `OCHAZUKE_ASSUME_YES=1` (`g Y`), and MCP; `o-lane2` keeps `lane`, `plan`, `rollup`, and adds `Store.Redo()`; `o-questions2` keeps all 12 `q_*` MCP tools, append-only threads, decision docs, and adds `Store.Redo()`.

## Next active wave (in flight)

1. **Matomeru v2 Wave C (`mtm-wavec-worker` running now in `~/proj/matomeru-next`):**
   - Full noun CLI parity (`task`, `pane`, `note`, `document`, `lane`, `schedule`, `run`, `signal`, `extension`, `binding`, stdin `-`, `binding check`).
   - Data-Flow Encapsulation (`data/files.go` canonical managed storage root) plus global newest-first Recents picker (`g r`).
   - Outline (`T`) sibling reorder (`J`/`K` with counts), indent/outdent (`>`/`<`), status cycle (`space`), inline rename (`i`), and full undo.
   - Side-by-side v2 extension discovery (`o-*2`) and live catalog integration test against the real installed v2 binaries.
2. **Ochazuke v2 polish (queued right after Matomeru Wave C finishes):**
   - Multi-key sort, visual row marking (`V`), cell-select mode, and multi-condition filter expressions (`!term`, `a|b`) in `widgets/table`.
   - Scope-aware word boundaries (`code`, `path`, `json`, `csv`, `markdown`) and styled ANSI copy-mode adapter in `widgets/input`.
   - Rebuild `m2m` (`mato`) and all `o-*2` binaries onto the polished Ochazuke v2 commit.
