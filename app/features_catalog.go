package app

import (
	"slices"
	"strings"

	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
)

type featuresPageCatalogEntry struct {
	Key        string
	Title      string
	Icon       string
	Summary    string
	Highlights []string
	Keywords   []string
}

type featuresPlannedVerb struct {
	ID          string
	Category    string
	Description string
	CLI         string
	MCP         string
}

type featuresNounCatalogEntry struct {
	Kind           string
	Title          string
	Icon           string
	Summary        string
	Planned        bool
	DomainPrefixes []string
	ExcludeIDs     []string
	Highlights     []string
	Keywords       []string
	PlannedVerbs   []featuresPlannedVerb
}

func pageEntry(key, title, icon, summary string, highlights, keywords []string) featuresPageCatalogEntry {
	return featuresPageCatalogEntry{Key: key, Title: title, Icon: icon, Summary: summary, Highlights: highlights, Keywords: keywords}
}

func nounEntry(kind, title, icon, summary string, planned bool, prefixes, highlights, keywords []string, plannedVerbs ...featuresPlannedVerb) featuresNounCatalogEntry {
	return featuresNounCatalogEntry{Kind: kind, Title: title, Icon: icon, Summary: summary, Planned: planned, DomainPrefixes: prefixes, Highlights: highlights, Keywords: keywords, PlannedVerbs: plannedVerbs}
}

func pv(id, cat, desc string) featuresPlannedVerb {
	return featuresPlannedVerb{ID: id, Category: cat, Description: desc}
}

func pvs(id, cat, desc, cli, mcp string) featuresPlannedVerb {
	return featuresPlannedVerb{ID: id, Category: cat, Description: desc, CLI: cli, MCP: mcp}
}

func defaultFeaturesPageCatalog() []featuresPageCatalogEntry {
	return []featuresPageCatalogEntry{
		pageEntry("panes", "Panes", "▣", "Live multi-pane terminal and AI agent workspace with paired Open UI sites and prompt composer.",
			[]string{"Project-grouped or flat pane rail with attention badges", "Zoomed terminal, paired side split, and link/path/PR screen hints", "Multi-line prompt composer with drafts, completions, and attachments"},
			[]string{"pane", "terminal", "rail", "composer", "hints", "side split", "zoom"}),
		pageEntry("projects", "Projects", "◈", "Directory of all root projects in a 20/80 split with live preview and dynamic Project Tabs.",
			[]string{"Fast project switcher with preview pane", "Open projects as top-level tabs beside Panes and Work", "Scoped project status, active lanes, and pane counts"},
			[]string{"project", "projects", "directory", "tab", "workspace"}),
		pageEntry("project_pages", "Project Pages", "◫", "Scoped project workspace with grouped left navigation across tasks, pages, context, panes, and documents.",
			[]string{"Grouped left nav rail (Core, Context, Workspace, Knowledge)", "Embedded child pane view without leaving project context", "Pinned/official project context items and quick note capture"},
			[]string{"project page", "project workspace", "nav", "official context", "pinned"}),
		pageEntry("work", "Work", "☰", "Unified noun browser supporting tree and table layouts, filtering, sorting, grouping, and saved presets.",
			[]string{"Switch between any registered noun via noun picker", "Tree and table layout toggle for hierarchical nouns", "Saved view presets, multi-mark actions, and inline status cycling"},
			[]string{"work", "table", "tree", "presets", "filter", "sort", "group"}),
		pageEntry("outline", "Outline", "↳", "Keyboard-first hierarchical task outline with inline editing, hoisting, clean-slate canvas, and agent triage.",
			[]string{"Inline add above/below, indent/outdent, reorder, and hoist", "Clean-slate focus canvas for distraction-free brainstorming", "One-key agent triage sending subtrees into read-only proposals"},
			[]string{"outline", "task tree", "clean slate", "hoist", "triage", "brainstorm"}),
		pageEntry("runs", "Runs", "▶", "Execution log for shell commands and extension invocations with status, duration, exit code, and output.",
			[]string{"Live status tracking for active and finished runs", "One-key rerun and cancel of tracked executions", "Output inspection and linkage back to tasks and lanes"},
			[]string{"run", "runs", "command output", "rerun", "cancel", "execution"}),
		pageEntry("extensions", "Extensions", "⚙", "Discovery, health verification, installation, setup, and invocation hub for OFAM o-* tools.",
			[]string{"Auto-discovery of installed and installable OFAM extensions", "One-key go install / install-all and guided extension setup", "Direct command execution routed through tracked Runs"},
			[]string{"extension", "extensions", "ofam", "install", "setup", "plugin"}),
		pageEntry("questions", "Questions", "?", "Structured inbox for agent questions, owner decisions, batch answer staging, and logged Q&A cards.",
			[]string{"Multi-choice decision cards with batch staging and submit-all", "Unread attention signaling across panes and project nav", "Zero-nag answered Q&A records for informational queries"},
			[]string{"question", "questions", "decision", "qa", "batch", "answer"}),
		pageEntry("documents", "Documents", "▤", "Managed and external Markdown dossiers linked to tasks, lanes, and projects.",
			[]string{"Integrated Markdown reader with heading TOC and code yank", "Backed by files on disk or inline store bodies", "Interactive HTML serving through mdweb"},
			[]string{"document", "documents", "dossier", "markdown", "mdweb"}),
		pageEntry("settings", "Settings", "⚡", "Declarative configuration editor applying theme, density, layout, and opt-out toggles immediately.",
			[]string{"Live apply-on-change with persistent config saving", "Declarative opt-outs for every UI and layout behavior", "Theme preset switching and display customization"},
			[]string{"settings", "config", "theme", "opt-out", "preferences"}),
		pageEntry("bindings", "Bindings", "⌨", "Interactive keybinding editor to browse, filter, rebind, unbind, reset, and undo bindings.",
			[]string{"Filterable view of all bindings across scopes and modes", "Live key-sequence capture for rebinding without editing JSON", "Full reset and undo/redo history for binding changes"},
			[]string{"bindings", "keymap", "shortcuts", "rebind", "keys"}),
		pageEntry("reader", "Reader", "◪", "Rich Markdown and Open UI canvas reader with link navigation, checkboxes, tabs, and code copy.",
			[]string{"Heading outline jumps and back-navigation history", "Interactive canvas checkboxes, tab switching, and link opening", "Direct code-block yanking without mouse selection"},
			[]string{"reader", "markdown", "canvas", "toc", "links", "checkbox"}),
		pageEntry("inspector", "Inspector", "◎", "Live runtime inspector exposing registered actions, modes, bindings, and schemas.",
			[]string{"Inspect every registered command spec and active mode", "Filter actions live and bind unbound actions to free keys", "Ground-truth view of Matomeru's declarative registries"},
			[]string{"inspector", "actions", "modes", "schemas", "registry", "debug"}),
	}
}

func defaultFeaturesNounCatalog() []featuresNounCatalogEntry {
	return []featuresNounCatalogEntry{
		nounEntry("task", "Task / Project", "☐", "Hierarchical work unit spanning root projects, feature groups, tasks, and subtasks.", false,
			[]string{"task.", "outline.", "project.page.", "project.note.", "project.section.", "project.content.", "project.child_pane."},
			[]string{"Root tasks act as projects with defaultDir, repo, and defaultHarness", "Supports outline hierarchy, agentStatus workflow, and inline note threads"},
			[]string{"task", "project", "subtask", "outline", "agentStatus", "priority"},
			pvs("task.status.set", "lifecycle", "Set task workStatus directly from table, show, or project home", "mtm task edit --field workStatus=...", "data.edit"),
			pv("task.status.cycle", "lifecycle", "Cycle task workStatus outside outline views"),
			pv("task.priority.cycle", "triage", "Cycle task priority across P0..P5"),
			pvs("task.priority.set", "triage", "Set task priority directly (P0..P5)", "mtm task edit --field priority=...", "data.edit"),
			pv("task.promote", "hierarchy", "Promote a subtask to a root project"),
			pv("task.reparent", "hierarchy", "Move a task under another project or parent task"),
			pv("task.spawn_lane", "handoff", "Spin up an agent work lane linked to this task with an auto-dossier"),
			pv("task.spawn_pane", "execution", "Launch an agent or terminal pane in defaultDir using defaultHarness"),
			pv("task.flag_agent", "handoff", "Flag task for autonomous agent pickup"),
			pv("task.note.add", "notes", "Attach a note directly onto a task via the about relation"),
		),
		nounEntry("pane", "Pane", "▣", "Live daemon-hosted PTY terminal or AI coding agent session with paired Open UI pages.", false,
			[]string{"pane.", "panes.", "terminal."},
			[]string{"Persistent Unix-socket daemon PTY surviving TUI reloads", "Approval prompts, attention signaling, side splits, and prompt composer"},
			[]string{"pane", "pty", "terminal", "agent", "harness", "approval"},
			pv("pane.side.spawn", "execution", "Spawn a paired side terminal inheriting cwd, env, and task with parentPane"),
			pv("pane.restart", "lifecycle", "Restart a stopped or stuck pane PTY cleanly"),
			pv("pane.kill", "lifecycle", "Terminate a running pane PTY process"),
			pv("pane.respawn", "lifecycle", "Respawn a pane preserving its task and harness metadata"),
			pv("pane.rename", "metadata", "Quick inline rename of the focused pane title"),
			pv("pane.transcript.open", "inspection", "Open the pane's linked transcriptPath or AI session log"),
			pv("pane.transcript.export", "export", "Export the pane's session transcript to a document"),
			pv("pane.send_selection", "handoff", "Send selected text or record into the pane prompt/stdin"),
			pv("pane.send_note", "handoff", "Send an attached note into the pane prompt/stdin"),
			pv("pane.mark_read", "attention", "Clear unread attention badge on the pane after review"),
		),
		nounEntry("note", "Note", "✎", "Lightweight top-level or item-attached note linked via outgoing about relations.", false,
			[]string{"note."},
			[]string{"Attachable to any record via about relation", "Participates in inline task note threads and quick capture"},
			[]string{"note", "jot", "about", "reply", "thread"},
			pv("note.add_on_focused", "creation", "Create a note attached via about to whichever record has focus"),
			pv("note.edit_inline", "editing", "Fast inline quick-edit of note title/body"),
			pv("note.open_editor", "editing", "Open note body in $EDITOR"),
			pv("note.promote_to_task", "conversion", "Convert a note into a task or subtask under the current project"),
			pv("note.promote_to_document", "conversion", "Graduate a long note into a managed Markdown document"),
			pv("note.send_to_pane", "handoff", "Dispatch note content as context or prompt to an agent pane"),
			pv("note.jump_about", "navigation", "Jump directly to the target record this note is about"),
		),
		nounEntry("document", "Document", "▤", "Managed or external Markdown dossier storing plans, specs, research, and reports.", false,
			[]string{"document.", "markdown.", "reader."},
			[]string{"Supports filePath-backed files and inline Markdown bodies", "Links directly to lanes as plan dossiers and projects as knowledge docs"},
			[]string{"document", "doc", "dossier", "markdown", "plan", "mdweb"},
			pvs("document.open_web", "viewing", "Open document in mtm web / mdweb interactive HTML server", "mtm web", ""),
			pv("document.edit_external", "editing", "Open document filePath in $EDITOR or configured handler"),
			pv("document.sync_disk", "storage", "Synchronize inline body and managed Markdown file on disk"),
			pv("document.materialize", "storage", "Write an inline document body out to a managed file on disk"),
			pv("document.attach_to_lane", "relations", "Relate document as plan dossier on an active or planned lane"),
			pv("document.send_to_pane", "handoff", "Share document path and summary with an agent pane"),
			pv("document.extract_tasks", "conversion", "Extract checkboxes or headings into subtasks or triage proposals"),
			pv("document.pin_to_project", "organization", "Pin or star document onto the Project left navigation rail"),
		),
		nounEntry("lane", "Lane", "⑂", "Agent work lane tracking ownership, status, dossier plan, git branch/worktree, and artifacts.", false,
			[]string{"lane."},
			[]string{"Ambient progress bar rollup across parent/child lane trees", "Claims live Matomeru panes and links plan documents and created artifacts"},
			[]string{"lane", "agent work", "dossier", "claim", "branch", "worktree"},
			pvs("lane.status.set", "lifecycle", "Set laneStatus (planned, running, review, merged, done, parked, cancelled)", "mtm lane edit --field laneStatus=...", "lane.finish"),
			pv("lane.status.cycle", "lifecycle", "Advance laneStatus along the primary workflow progression"),
			pvs("lane.claim", "coordination", "Claim the current or selected pane on this lane", "mtm lane relate <id> claim pane:<pane>", "lane.start"),
			pv("lane.unclaim", "coordination", "Release a claimed pane from this lane"),
			pv("lane.plan.open", "navigation", "Jump directly to the lane's linked Markdown plan dossier"),
			pv("lane.plan.create", "creation", "Create a Markdown dossier and relate it as plan on this lane"),
			pv("lane.spawn_child", "hierarchy", "Create a child lane inheriting parentLane and repo metadata"),
			pv("lane.worktree.open", "navigation", "Open the lane's git worktree directory or pane"),
			pv("lane.diff", "inspection", "View git diff for the lane's baseCommit..headCommit range"),
			pv("lane.handoff", "handoff", "Hand off lane dossier and ownerWords to an agent pane"),
			pv("lane.launch_agent", "execution", "Launch a new agent pane seeded with the lane dossier"),
			pv("lane.artifacts", "inspection", "Browse created docs, pages, questions, commands, and runs"),
		),
		nounEntry("schedule", "Schedule", "◷", "Recurring cron or one-shot schedule ticked by the Matomeru daemon.", false,
			[]string{"schedule."},
			[]string{"Daemon-backed execution with pause/resume control", "Records schedule.occurrence entries for every scheduled fire"},
			[]string{"schedule", "cron", "timer", "recurring", "daemon"},
			pv("schedule.run_now", "execution", "Trigger an immediate ad-hoc execution and record an occurrence/run"),
			pv("schedule.toggle_pause", "lifecycle", "Single-key toggle between paused and active states"),
			pv("schedule.occurrences", "navigation", "Jump to filtered schedule.occurrence history for this schedule"),
			pv("schedule.edit_cron", "editing", "Guided cron expression editor with next-3-runs preview"),
			pv("schedule.last_output", "inspection", "Inspect output of the most recent scheduled execution"),
		),
		nounEntry("schedule.occurrence", "Schedule Occurrence", "◔", "Individual scheduled fire recording due time, check time, and outcome status.", false,
			[]string{"schedule.occurrence.", "occurrence."},
			[]string{"Audit trail of due, missed, completed, and skipped schedule firings"},
			[]string{"occurrence", "schedule occurrence", "due", "outcome"},
			pv("occurrence.ack", "lifecycle", "Acknowledge a due or failed occurrence"),
			pv("occurrence.skip", "lifecycle", "Mark occurrence outcome as skipped"),
			pv("occurrence.complete", "lifecycle", "Mark occurrence outcome as completed"),
			pv("occurrence.rerun", "execution", "Re-trigger a failed or missed occurrence"),
			pv("occurrence.jump_schedule", "navigation", "Jump to the parent schedule record"),
			pv("occurrence.open_run", "inspection", "Inspect the associated run output for this occurrence"),
		),
		nounEntry("run", "Run", "▶", "Tracked command or extension execution capturing args, cwd, timing, exitCode, and output.", false,
			[]string{"run."},
			[]string{"Single execution audit log for commands and extensions", "Supports live cancellation and one-key rerun"},
			[]string{"run", "stdout", "stderr", "exitCode", "execution"},
			pv("run.output.open", "inspection", "Open full-screen scrollable stdout/stderr output pager"),
			pv("run.output.yank", "clipboard", "Copy run stdout/stderr output to clipboard"),
			pv("run.output.save_doc", "conversion", "Save run output as a managed document or note"),
			pv("run.promote_to_command", "conversion", "Save an ad-hoc run as a reusable command recipe"),
			pv("run.open_in_pane", "execution", "Re-run interactively inside a live terminal pane in cwd"),
			pv("run.send_to_pane", "handoff", "Send run output or error trace to an agent pane for debugging"),
		),
		nounEntry("signal", "Signal", "!", "Inbound event or notification routed through automated triage rules.", false,
			[]string{"signal."},
			[]string{"Captures source, eventKind, author, container, URL, and triageState", "Supports one-key triage rule creation directly from a signal"},
			[]string{"signal", "notification", "inbox", "triage", "event"},
			pv("signal.triage.ack", "triage", "Acknowledge the focused signal"),
			pv("signal.triage.dismiss", "triage", "Dismiss the focused signal for inbox-zero triage"),
			pv("signal.triage.cycle", "triage", "Cycle the signal's triageState"),
			pv("signal.open_url", "navigation", "Open the signal's source URL in the browser"),
			pv("signal.promote_to_task", "conversion", "Turn signal into a linked task under a chosen project"),
			pv("signal.send_to_pane", "handoff", "Hand off signal to an agent pane for investigation"),
			pv("signal.mute_source", "triage", "Quick-create a suppress triage.rule for this signal's source/container"),
		),
		nounEntry("triage.rule", "Triage Rule", "⑃", "Declarative expression rule that automatically classifies, assigns, or suppresses signals.", false,
			[]string{"triage.rule.", "rule."},
			[]string{"Evaluates Ochazuke expr predicates in priority order"},
			[]string{"triage", "rule", "expression", "automation", "filter"},
			pv("rule.toggle_enabled", "lifecycle", "Quickly enable or disable the focused triage rule"),
			pv("rule.test", "inspection", "Test the rule expression against stored signals"),
			pv("rule.dry_run", "inspection", "Preview which existing signals match when without mutating them"),
			pv("rule.apply_backlog", "execution", "Run rule retroactively against untriaged signals"),
			pv("rule.priority.up", "ordering", "Raise rule evaluation priority"),
			pv("rule.priority.down", "ordering", "Lower rule evaluation priority"),
			pv("rule.duplicate", "creation", "Clone rule as a starting point for a new rule"),
		),
		nounEntry("directory", "Directory", "⌂", "Filesystem directory linked to projects and tasks via CLI and directory relations.", false,
			[]string{"directory.", "path."},
			[]string{"Attaches working directories to projects and records", "Integrates with path picker and file search"},
			[]string{"directory", "path", "folder", "cwd", "repo"},
			pv("directory.open_terminal", "execution", "Spawn a terminal pane at this directory path"),
			pv("directory.open_agent", "execution", "Spawn an agent pane at this directory path"),
			pv("directory.search_files", "navigation", "Launch search.files scoped to this directory"),
			pv("directory.git_status", "git", "Inspect git repository/worktree status via o-git"),
			pv("directory.git_diff", "git", "Inspect git diff for this directory via o-git"),
			pv("directory.open_finder", "external", "Reveal directory externally in Finder / file manager"),
			pv("directory.yank_path", "clipboard", "Copy the directory path to clipboard"),
		),
		nounEntry("extension", "Extension", "⚙", "OFAM o-* companion CLI extension exposing capabilities, setup, and runnable commands.", false,
			[]string{"extension."},
			[]string{"Discover, install, refresh, setup, and execute OFAM tools in-app"},
			[]string{"extension", "ofam", "tool", "binary", "install"},
			pv("extension.upgrade", "lifecycle", "Upgrade an installed extension binary"),
			pv("extension.check_updates", "inspection", "Check whether newer versions are available"),
			pv("extension.commands.browse", "execution", "Inspect subcommands and invoke with a guided args form"),
			pv("extension.docs", "viewing", "Render extension --skill or docs inside the reader page"),
			pv("extension.help", "viewing", "Render extension --help output inside the reader page"),
			pv("extension.health", "inspection", "Verify binary path, signing, version, and MCP exposure"),
		),
		nounEntry("question", "Question", "?", "Agent decision prompt, batch question, or answered Q&A record.", false,
			[]string{"question."},
			[]string{"Supports single-click choices, batch staging, and Q&A logging"},
			[]string{"question", "answer", "decision", "qa", "batch"},
			pv("question.answer_custom", "answering", "Submit a free-text custom reply beyond fixed choices"),
			pv("question.defer", "lifecycle", "Snooze or defer a question for later review"),
			pv("question.dismiss", "lifecycle", "Dismiss a stale question without answering"),
			pv("question.jump_pane", "navigation", "Jump to the pane that asked this question"),
			pv("question.jump_page", "navigation", "Jump to the ui.page card presenting this question"),
			pv("question.promote_to_decision", "organization", "Pin answered question into the project Decisions log"),
			pv("question.reopen", "lifecycle", "Reopen an answered question to revise the decision"),
			pv("question.convert_to_task", "conversion", "Turn a follow-up question into an actionable task"),
		),
		nounEntry("command", "Command", "$", "Saved or agent-suggested runnable shell command recipe with cwd and lastRun link.", false,
			[]string{"command."},
			[]string{"Run directly or edit arguments/cwd before executing in Runs"},
			[]string{"command", "recipe", "shell", "script", "lastRun"},
			pv("command.run_in_pane", "execution", "Execute interactively in the focused or paired side terminal"),
			pv("command.last_run.open", "navigation", "Jump straight to lastRun status and output"),
			pv("command.copy", "clipboard", "Yank raw shell command string to clipboard"),
			pv("command.promote_to_schedule", "conversion", "Turn a saved command into a recurring schedule"),
			pv("command.verify", "inspection", "Verify resolved cwd and command binary availability"),
			pv("command.dry_run", "inspection", "Preview resolved cwd and interpolated arguments without running"),
		),
		nounEntry("context", "Context", "◈", "High-signal URL, PR, file, or doc context item extracted from panes or officially pinned.", false,
			[]string{"context.", "project.context."},
			[]string{"Collects URLs, PRs, files, and docs with official/starred pinning"},
			[]string{"context", "url", "file", "doc", "link", "official"},
			pv("context.pin", "organization", "Promote context item to Pinned links or Pinned files in Project nav"),
			pv("context.unpin", "organization", "Remove context item from pinned project navigation"),
			pv("context.open_at_line", "navigation", "Open file at line:col, URL/PR in browser, or doc in reader/mdweb"),
			pv("context.promote_to_document", "conversion", "Register a file context item as a tracked document"),
			pv("context.dedupe", "hygiene", "Deduplicate matching canonical context entries"),
			pv("context.prune_noise", "hygiene", "Clean auto-scraped low-signal noise from screen/tool sources"),
			pv("context.filter_kind", "filtering", "Quick-cycle context kind filter (all | url | file | pr | doc)"),
			pv("context.send_to_pane", "handoff", "Inject @path or URL directly into the focused pane composer"),
		),
		nounEntry("ui.page", "UI Page", "◫", "Interactive Open UI ViewNode page or canvas published by agents, tools, or self-hosted views.", false,
			[]string{"ui.page.", "page.", "pages.", "site.", "canvas."},
			[]string{"Declarative ViewNode JSON rendered identically to native views", "Supports TOC rails, interactive canvas items, checkboxes, and tabs"},
			[]string{"ui.page", "page", "canvas", "site", "toc", "open ui"},
			pv("page.pin", "organization", "Pin page in Project Pages Index and pane TOC"),
			pv("page.unpin", "organization", "Unpin page from Project Pages Index and pane TOC"),
			pv("page.reply", "collaboration", "Send a direct reply tagged with pageId and focused card ref"),
			pv("page.annotate", "collaboration", "Attach inline markup or commentary to a page node"),
			pv("page.mark_read", "attention", "Mark page viewedAt to clear unread indicator"),
			pv("page.mark_unread", "attention", "Mark page unread to flag it for follow-up"),
			pv("page.detach_from_pane", "organization", "Promote a pane-bound page to a standalone project page"),
			pv("page.assign_project", "organization", "Assign page to a target project task"),
			pv("page.export_markdown", "export", "Export a ViewNode page to a Markdown document"),
			pv("page.open_web", "viewing", "Open page in browser via mdweb / HTML preview"),
			pv("page.clone", "creation", "Duplicate a template or reference page"),
			pv("page.archive", "lifecycle", "Archive a stale interactive canvas page"),
		),
		nounEntry("pr", "Pull Request", "⎇", "Planned noun tracking GitHub pull requests, CI checks, diffs, and review comments per project.", true,
			[]string{"pr."},
			[]string{"First-class PR navigation in Project Left Nav", "Links PRs directly to active agent work lanes"},
			[]string{"pr", "pull request", "github", "review", "checks", "diff"},
			pv("pr.open_browser", "navigation", "Open pull request in the default web browser"),
			pv("pr.checkout", "git", "Check out the PR branch locally or into a worktree"),
			pv("pr.diff", "inspection", "Inspect the pull request diff in-app"),
			pv("pr.review_comments", "review", "Browse and respond to PR review threads and comments"),
			pv("pr.checks", "ci", "Inspect CI check runs and failing job logs"),
			pv("pr.refresh", "sync", "Refresh PR metadata, review state, and CI status"),
			pv("pr.assign_lane", "coordination", "Link the pull request to an active or planned lane"),
		),
		nounEntry("worktree", "Worktree", "⌥", "Planned noun managing isolated Git worktrees per project and agent lane.", true,
			[]string{"worktree."},
			[]string{"First-class git worktrees section in Project Left Nav", "Clean isolation for parallel agent panes and lanes"},
			[]string{"worktree", "git", "branch", "isolation"},
			pv("worktree.open_pane", "execution", "Open a terminal or agent pane rooted in this worktree"),
			pv("worktree.diff_base", "git", "Inspect diff against the worktree's base commit/branch"),
			pv("worktree.diff_head", "git", "Inspect uncommitted working tree changes against HEAD"),
			pv("worktree.status", "git", "Show branch, ahead/behind, and dirty file status"),
			pv("worktree.create", "lifecycle", "Create a new git worktree on a branch"),
			pv("worktree.remove", "lifecycle", "Safely remove a clean or merged git worktree"),
			pv("worktree.attach_lane", "coordination", "Attach this worktree to an agent work lane"),
		),
		nounEntry("tag", "Tag", "#", "Planned first-class noun over Matomeru's universal tag relation across all schemas.", true,
			[]string{"tag."},
			[]string{"Unified cross-noun tagging, filtering, and renaming"},
			[]string{"tag", "label", "taxonomy", "filter"},
			pv("tag.add", "organization", "Attach a tag to the focused record"),
			pv("tag.remove", "organization", "Remove a tag from the focused record"),
			pv("tag.filter", "filtering", "Filter the current list or workspace by tag"),
			pv("tag.rename", "lifecycle", "Rename a tag across all tagged records"),
		),
	}
}

func lookupFeaturesPageCatalog(key string) (featuresPageCatalogEntry, bool) {
	k := strings.TrimSpace(key)
	for _, entry := range defaultFeaturesPageCatalog() {
		if entry.Key == k {
			return entry, true
		}
	}
	return featuresPageCatalogEntry{}, false
}

func lookupFeaturesNounCatalog(kind string) (featuresNounCatalogEntry, bool) {
	k := strings.TrimSpace(kind)
	for _, entry := range defaultFeaturesNounCatalog() {
		if entry.Kind == k {
			return entry, true
		}
	}
	return featuresNounCatalogEntry{}, false
}

var globalChromeCommandPrefixes = []string{
	"motion.", "focus.", "layer.", "app.", "count.", "hints.", "modal.", "input.",
	"table.", "tree.", "search.", "yank.", "filter.", "macro.", "bindings.", "settings.",
	"help.", "palette.", "inspect.", "scratch.", "shell.", "history.", "view.", "session.",
	"open.", "store.", "record.", "archived.", "recents.", "confirm.", "form.", "picker.",
	"commandline.", "daemon.", "composer.", "ux.", "features.", "space.", "tab.",
	"project.tab.", "insert.picker.",
}

func isGlobalChromeCommand(spec core.CommandSpec) bool {
	if isMetaBool(spec.Meta, "internal") || isMetaBool(spec.Meta, "selfHosted") {
		return true
	}
	for _, prefix := range globalChromeCommandPrefixes {
		if strings.HasPrefix(spec.ID, prefix) {
			return true
		}
	}
	return false
}

func domainVerbsForNoun(kind string, specs []core.CommandSpec, keysByID map[string]string) (implemented []views.FeaturesVerbRow, planned []views.FeaturesVerbRow) {
	entry, _ := lookupFeaturesNounCatalog(kind)
	excluded := make(map[string]bool, len(entry.ExcludeIDs))
	for _, id := range entry.ExcludeIDs {
		excluded[id] = true
	}

	seen := make(map[string]bool)
	for _, sp := range specs {
		if sp.ID == "" || seen[sp.ID] || excluded[sp.ID] || isGlobalChromeCommand(sp) {
			continue
		}
		if !specMatchesNoun(sp, kind, entry.DomainPrefixes) {
			continue
		}
		seen[sp.ID] = true
		keys := ""
		if keysByID != nil {
			keys = strings.TrimSpace(keysByID[sp.ID])
		}
		if keys == "" {
			keys = "(unbound)"
		}
		implemented = append(implemented, views.FeaturesVerbRow{
			ID: sp.ID, Description: sp.Description, Keys: keys,
			Category: metaString(sp.Meta, "category"), CLI: metaString(sp.Meta, "cli"), MCP: metaString(sp.Meta, "mcp"),
			Implemented: true,
		})
	}

	slices.SortFunc(implemented, func(a, b views.FeaturesVerbRow) int {
		return strings.Compare(a.ID, b.ID)
	})

	for _, p := range entry.PlannedVerbs {
		if seen[p.ID] {
			continue
		}
		planned = append(planned, views.FeaturesVerbRow{
			ID: p.ID, Description: p.Description, Category: p.Category, CLI: p.CLI, MCP: p.MCP, Implemented: false,
		})
	}
	return implemented, planned
}

func specMatchesNoun(sp core.CommandSpec, kind string, domainPrefixes []string) bool {
	if metaString(sp.Meta, "domainNoun") == kind || metaString(sp.Meta, "noun") == kind {
		return true
	}
	if metaIncludesKind(sp.Meta, "kinds", kind) {
		return true
	}
	if kind != "" && strings.HasPrefix(sp.ID, kind+".") {
		return true
	}
	for _, pref := range domainPrefixes {
		if pref != "" && strings.HasPrefix(sp.ID, pref) {
			return true
		}
	}
	return false
}

func isMetaBool(meta map[string]any, key string) bool {
	b, _ := meta[key].(bool)
	return b
}

func metaString(meta map[string]any, key string) string {
	s, _ := meta[key].(string)
	return strings.TrimSpace(s)
}

func metaIncludesKind(meta map[string]any, key, kind string) bool {
	if meta == nil || kind == "" {
		return false
	}
	switch x := meta[key].(type) {
	case []string:
		return slices.Contains(x, kind)
	case []any:
		for _, item := range x {
			if s, ok := item.(string); ok && strings.TrimSpace(s) == kind {
				return true
			}
		}
	case string:
		for _, part := range strings.Split(x, ",") {
			if strings.TrimSpace(part) == kind {
				return true
			}
		}
	}
	return false
}
