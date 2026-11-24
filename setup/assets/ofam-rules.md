<!-- ofam:begin -->
## OFAM Agent Policy

"the goal of these libraries....is to enable and expose possibilities. not impose rules and limtations."

### 1. Orchestrator Delegation
- The main session owns planning, architectural design, owner alignment, diff review, verification, merging, and release.
- Delegate multi-file exploration, substantial design drafts, and code implementation to subagents using `inherit` thinking level and model tier so delegated work keeps full judgment; step down only for purely mechanical, high-volume scans.
- Subagents never merge, push, or tag; the orchestrator reviews every diff against repository rules and confirms checks pass before landing.

### 2. Mandatory `o-lane` & Markdown Dossier Tracking
- Track all agent work in `o-lane` through Matomeru (`mtm lane ...` or the `mtm` MCP gateway), never by creating or editing owner `mtm` tasks unless explicitly asked.
- Every lane has a linked Markdown dossier (`mtm document create` + `mtm lane relate <LANE_ID> plan document:<DOC_ID>`), keeps its `laneStatus` (`planned`, `running`, `review`, `merged`, `done`, `parked`, `cancelled`) and `summary` updated (`mtm lane edit <ID> --field laneStatus=...`), and **claims the active Matomeru pane whenever `$MTM_PANE_ID` is set** (`mtm lane relate <LANE_ID> claim pane:$MTM_PANE_ID` or MCP `lane.start` with `pane: "$MTM_PANE_ID"`) so the pane's lane progress bar and `ctrl+a L` stay live.
- Apply the 3-minute triage rule: work over ~3 minutes or touching multiple files gets its own child lane; group small unrelated fixes under a `Small Fixes` parent lane. Reorganize lanes freely as clearer groupings emerge.

### 3. File-Ownership Matrix & Worker Discipline
- Parallel implementation workers must own strictly disjoint file sets; serialize edits to any shared file.
- Name every Go source and test file after its domain module or responsibility (e.g., `confirm.go`, `presets.go`, `setup.go`). NEVER create `wave*.go`, `sprint*.go`, `phase*.go`, or `milestone*.go` sidecar files.

### 4. Separate Data from Behavior (Value Objects, Facts, Bindings & Atomic Verbs)
- Follow a strict unidirectional pipeline: well-defined atomic mutation API updates domain data -> pure Presenter projects pure value objects (`ViewNode` + truthful read-only `Facts` like `atTop`, `atBottom`, `empty`, `filtering`, `focused`) -> dumb UI renderer draws them with zero store access, zero behavioral policy, and zero per-noun/modal switches.
- Compose interaction behavior in pure data (`bindings.json` `when` guards over `Facts` + generic verb `args`, `themes.json` focus tokens, `Schema` properties, `ViewNode` JSON) rather than Go conditionals or single-caller fake-generic props. Built-in pages and nouns must use the same data vocabulary as dynamic `ui.page` JSON and `o-*` catalogs (`~/hub/docs/2026-11-23-data-from-behavior-architecture.md`).
- Drive visual styling declaratively from theme tokens, schema properties, and view metadata; build rich, scannable TUI layouts (whitespace over nested borders, unmistakable focus contrast, tree/outline defaults for parent-child nouns, semantic color, clear symbols).
- Every UI or layout change must provide an opt-out setting in configuration rather than hardcoded branches.

### 5. Structural Hygiene & Scaled Checks
- Maintain shared repository hygiene invariants: `TestDomainFilenames`, `TestNoGodFiles` (`< 1,000` lines per non-test file), `TestViewLayerPurity`, `TestDependencyBoundaries`, and pre-commit secret/hygiene audits (never bypass `--no-verify`).
- Checks scale with the change: configuration-only edits run only targeted binding/config/verb tests; shared code changes run full checks once per repo.
- Run local Go builds, `vet`, and tests serially through `~/bin/heavy` with `GOWORK=off GOFLAGS=-trimpath`.

### 6. Single MCP Gateway, Startup, Restarts & Releases
- `mtm mcp serve` (and the daemon HTTP endpoint `$MTM_MCP_URL`) is the family's single MCP gateway; prefer it over per-tool MCP servers.
- At session start, inspect `MTM_PANE_ID` / `MTM_SOCKET` / `MTM_MCP_URL`. Whenever `$MTM_PANE_ID` is non-empty, claim it on your root and active child lane immediately (`mtm lane relate <LANE_ID> claim pane:$MTM_PANE_ID`).
- After installing a new `mtm` binary, run `mtm daemon upgrade --json` to re-exec the daemon in place without killing live panes (never quit the user's TUI yourself).
- Follow the `ofam-wave-release` skill runbook when closing waves, tagging releases, Developer ID signing, and atomically installing binaries into `~/bin/`.

### 7. Questions, Q&A Logging & Owner Commands
- **Owner Q&As (`data.log_qa`):** Whenever the owner asks an informational question (wants to know something rather than have work executed), log the Q&A pair through Matomeru via `mtm mcp call data.log_qa '{"prompt":"<owner words>","answer":"<structured Markdown>","pane":"$MTM_PANE_ID","page":"qa"}'` — format `answer` as structured Markdown (headings, bullet lists, bold labels, fenced code blocks; never a flat prose wall). Creates an answered `questionKind="qa"` record + read Q&A card with zero unread nagging; never include secrets.
- **Owner Decisions (`ui.ask`):** When you need a decision from the owner and Matomeru is active, pose it via `mtm mcp call ui.ask '{"pane":"$MTM_PANE_ID","title":"...","details":"...","choices":[...]}'` with 2–4 named options describing what each choice does (or a `questions` batch array; read answers via `ui.list_pages`). Never write options as bare yes/no unless the question is literally yes/no.
- **Owner Commands & Context:** Record reusable commands/recipes for the owner via `mtm mcp call data.suggest_command` (or `mtm cmd create`) and attach key URLs/PR refs/docs via `mtm mcp call data.add_context` (never include secrets).
<!-- ofam:end -->
