<!-- ofam:begin -->
## OFAM Agent Policy

"the goal of these libraries....is to enable and expose possibilities. not impose rules and limtations."

### 1. Session-Start Matomeru Checklist & Task-Anchored Tracking
- **Inspect environment first:** Check `$MTM_PANE_ID`, `$MTM_TASK_ID`, `$MTM_SOCKET`, and `$MTM_MCP_URL` at session start.
- **Tasks are the anchor:** Tasks lead lanes, panes, context, docs, notes, and decisions — everything must be connected to the right task. Never leave orphan panes, lanes, docs, or context disconnected during task-driven work.
- **Track agent work in `o-lane`:** Manage all agent work through Matomeru (`mtm lane ...` or the `mtm` MCP gateway), never by creating or editing owner `mtm` tasks unless explicitly asked.
- **Wire every link up front:**
  - Connect every lane to its owner task (`mtm lane relate <LANE_ID> track task:<TASK_ID>` or MCP `data.relate`) and project (`--project <PROJ_ID>` or `mtm lane set <LANE_ID> project <PROJ_ID>`).
  - Attach a linked Markdown dossier (`mtm document create` + `mtm lane relate <LANE_ID> plan document:<DOC_ID>`) and connect panes (`project`/`task` or `relate`), context, and docs to the active task and project.
  - **Claim `$MTM_PANE_ID`** on both root and active child lanes immediately (`mtm lane relate <LANE_ID> claim pane:$MTM_PANE_ID` or MCP `lane.start` with `pane: "$MTM_PANE_ID"`) so the pane progress bar and `ctrl+a L` stay live.
- **Keep lanes live & triaged:** Continuously update `laneStatus` (`planned`, `running`, `review`, `merged`, `done`, `parked`, `cancelled`) and `summary` (`mtm lane edit <ID> --field laneStatus=...`). Apply the 3-minute triage rule: work over ~3 minutes or touching multiple files gets its own child lane; group small unrelated fixes under a `Small Fixes` parent lane.

### 2. Orchestrator Delegation & File-Ownership Discipline
- The main session owns planning, architectural design, owner alignment, diff review, verification, merging, and release.
- Delegate multi-file exploration, substantial design drafts, and code implementation to subagents using `inherit` thinking level and model tier so delegated work keeps full judgment; step down only for purely mechanical, high-volume scans.
- Subagents never merge, push, or tag; the orchestrator reviews every diff against repository rules and confirms checks pass before landing.
- Parallel implementation workers must own strictly disjoint file sets; serialize edits to any shared file.
- Name every Go source and test file after its domain module or responsibility (e.g., `confirm.go`, `presets.go`, `setup.go`). NEVER create `wave*.go`, `sprint*.go`, `phase*.go`, or `milestone*.go` sidecar files.

### 3. Full CLI & MCP Orchestration Surface
- `mtm mcp serve` (and daemon HTTP `$MTM_MCP_URL`) is the family's single MCP gateway; prefer it over per-tool MCP servers.
- Full CLI parity covers every noun and verb: `mtm <noun> list|show|create|edit|set|relate|unrelate|related` plus `mtm mcp call <tool> '<json>'`.
- Daemon-backed pane orchestration works on both MCP and CLI: `pane.spawn`, `pane.snapshot`, `pane.send`, `pane.status`, and `pane.close` — use them to launch agents in any project/lane and connect them to tasks/lanes via `data.relate` / `data.unrelate`.

### 4. Separate Data from Behavior (Value Objects, Facts, Bindings & Atomic Verbs)
- Follow a strict unidirectional pipeline: well-defined atomic mutation API updates domain data -> pure Presenter projects pure value objects (`ViewNode` + truthful read-only `Facts` like `atTop`, `atBottom`, `empty`, `filtering`, `focused`) -> dumb UI renderer draws them with zero store access, zero behavioral policy, and zero per-noun/modal switches.
- Compose interaction behavior in pure data (`bindings.json` `when` guards over `Facts` + generic verb `args`, `themes.json` focus tokens, `Schema` properties, `ViewNode` JSON) rather than Go conditionals or single-caller fake-generic props. Built-in pages and nouns must share the same data vocabulary as dynamic `ui.page` JSON and `o-*` catalogs (`~/hub/docs/2026-11-23-data-from-behavior-architecture.md`).
- Drive visual styling declaratively from theme tokens, schema properties, and view metadata; build rich, scannable TUI layouts (whitespace over nested borders, unmistakable focus contrast, tree/outline defaults for parent-child nouns, semantic color, clear symbols).
- Every UI or layout change must provide an opt-out setting in configuration rather than hardcoded branches.

### 5. Structural Hygiene, Scaled Checks, CI & Releases
- Maintain shared repository hygiene invariants: `TestDomainFilenames`, `TestNoGodFiles` (`< 1,000` lines per non-test file), `TestViewLayerPurity`, `TestDependencyBoundaries`, and pre-commit secret/hygiene audits (never bypass `--no-verify`).
- Checks scale with the change: configuration-only edits run only targeted binding/config/verb tests; shared code changes run full checks once per repo. Run local Go builds, `vet`, and tests serially through `~/bin/heavy` with `GOWORK=off GOFLAGS=-trimpath`.
- **Releases & Upgrades:** Use `o-release` (`ofam-wave-release` runbook; pass `--skip-tests` when free public GitHub Actions CI is green on the commit) to tag, Developer ID sign, and atomically install into `~/bin/`. After installing a new `mtm` binary, run `mtm daemon upgrade --json` to re-exec the daemon in place without killing live panes (never quit the user's TUI yourself).
- **Auto-Synced Setup:** `mtm` hash-gates and auto-syncs embedded rules, skills, hooks, and MCP configs on TUI startup, `g U` self-reload, and `mtm daemon upgrade` (manual controls: `mtm setup sync`, `mtm setup purge`).

### 6. Questions, Q&A Logging & Owner Commands
- **Owner Q&As (`data.log_qa`):** Whenever the owner asks an informational question (wants to know something rather than have work executed), log the Q&A pair through Matomeru via `mtm mcp call data.log_qa '{"prompt":"<owner words>","answer":"<structured Markdown>","pane":"$MTM_PANE_ID","page":"qa"}'` — format `answer` as structured Markdown (headings, bullet lists, bold labels, fenced code blocks; never a flat prose wall). Creates an answered `questionKind="qa"` record + read Q&A card with zero unread nagging; never include secrets.
- **Dedicated Pages for >10-Line Output (`ui.create_page`):** When building pages with `ui.create_page`, asking questions with `ui.ask`, or logging Q&As with `data.log_qa`, if an output, answer, or supporting context block exceeds **10 lines of text**, publish it as its **own dedicated page** via `ui.create_page` (using `kind: "markdown"` for rich text) rather than stuffing long prose into a shared card or Questions page.
- **Owner Decisions (`ui.ask`):** When you need a decision from the owner and Matomeru is active, pose it via `mtm mcp call ui.ask '{"pane":"$MTM_PANE_ID","title":"...","details":"...","choices":[...]}'` with 2–4 named options describing what each choice does (or a `questions` batch array; read answers via `ui.list_pages`). Never write options as bare yes/no unless the question is literally yes/no.
- **Owner Commands & Context:** Record reusable commands/recipes for the owner via `mtm mcp call data.suggest_command` (or `mtm cmd create`) and attach key URLs/PR refs/docs via `mtm mcp call data.add_context` (never include secrets).
### 7. Multi-Provider Delegation, Pre-PR Neon Review Gate & Inter-Agent Comms
- **Conserve Neon (Antigravity):** reserve it for architecture and interface/API design, pre-PR review, pushing back on weak code, and synthesizing reports. Route implementation, research, triage and refactors to Fable 5.2 / Opus 5.5 (`--harness claude-fable|claude-opus`), then Sol / Astra (`--harness codex-sol|codex-astra`), spawned with `mtm pane spawn` wired to `project`, `task`, `lane`, `cwd`, `prompt` and `--on-done-notify $MTM_PANE_ID`.
- **Pre-PR Neon gate (hard rule):** workers commit on their own branch/worktree and may push it, but **no agent runs `gh pr create` until a Neon reviewer approves the diff**. On finishing: `gofmt -l .` + targeted and hygiene tests serially via `~/bin/heavy`, push the branch, set the lane to `laneStatus=review` with SHA, diff stat and test summary, then `mtm msg send <orchestrator-pane> --kind review_request --lane <LANE_ID> "..."` and address any pushback.
- **Mailbox, not keystrokes:** talk to other agents with `mtm msg send|inbox|ack|reply` (MCP `agent.send`, `agent.inbox`, `agent.ack`, `agent.reply`) using `msgKind` `request | report | blocked | review_request | approved | rejected | note`. Messages persist in the store; the daemon emits `mail.received` and pastes a one-line wake notice only once the recipient is idle, so it never collides with an active turn. Send `--kind blocked` when stuck (emits `agent.blocked`). Read your inbox when woken and `ack` what you handled.
- **Callbacks:** `pane.spawn` `onDone.notifyPane` (CLI `--on-done-notify PANE`) mails the orchestrator on `agent.done`, `agent.needs_input` and `agent.blocked`; `mtm hook add --on <event[,event]> --notify-pane PANE` does the same for any rule; `mtm agent wait --pane ID` blocks on `agent.done`. Put findings, specs and reviews in linked documents, never only in scrollback.
<!-- ofam:end -->
