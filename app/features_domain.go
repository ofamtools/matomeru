package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/ui"
)

// Domain navigation section and classification keys for the Features page.
const (
	featuresDomainNavGroup = "domain"

	CmdFeaturesViewInCode = "features.viewInCode"

	DomainEverythingKey = "everything"
	DomainIndicatorsKey = "indicators"
	DomainComponentsKey = "components"
	DomainBadgesKey     = "badges"
	DomainSignalsKey    = "signals"
	DomainVerbsKey      = "verbs"
	DomainModesKey      = "modes"
	DomainViewsKey      = "views"
)

type domainGroupMeta struct {
	Key     string
	Title   string
	Icon    string
	Summary string
}

var domainGroupCatalog = []domainGroupMeta{
	{
		Key:     DomainIndicatorsKey,
		Title:   "Indicators",
		Icon:    "●",
		Summary: "First-class visual status indicators: unread dots, hook dots, attention badges, lane progress glyphs, flags, and gutter marks.",
	},
	{
		Key:     DomainComponentsKey,
		Title:   "Components",
		Icon:    "◫",
		Summary: "Reusable UI and domain building blocks: modal frames, forms, pickers, trees, tables, splits, panels, cards, bars, and chips.",
	},
	{
		Key:     DomainBadgesKey,
		Title:   "Badges & Tones",
		Icon:    "◈",
		Summary: "Declarative status badges, recency tones, lock/budget cells, and schema-driven row/cell styling.",
	},
	{
		Key:     DomainSignalsKey,
		Title:   "Signals & Events",
		Icon:    "⚡",
		Summary: "Attention routing, daemon hook telemetry, inbound triage signals, approval alerts, and unread state trackers.",
	},
	{
		Key:     DomainVerbsKey,
		Title:   "Verbs & Bindings",
		Icon:    "⌘",
		Summary: "Atomic registered command specs, declarative keybindings, scope bindings, and catalog verb groups.",
	},
	{
		Key:     DomainModesKey,
		Title:   "Modes & Guards",
		Icon:    "⛨",
		Summary: "Interaction modes, modal escape guards, declarative expr predicates, and focus/filter facts.",
	},
	{
		Key:     DomainViewsKey,
		Title:   "Views & Layouts",
		Icon:    "▤",
		Summary: "Top-level declarative page views, split workspaces, show ledgers, and modal overlay layouts.",
	},
}

func lookupDomainGroupMeta(key string) (domainGroupMeta, bool) {
	k := strings.TrimSpace(key)
	for _, meta := range domainGroupCatalog {
		if meta.Key == k {
			return meta, true
		}
	}
	return domainGroupMeta{}, false
}

func (a *App) featuresDomainNavEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetFeaturesDomainNav, true)
}

func (a *App) snapshotDomainGroups() []views.DomainGroup {
	if !a.featuresDomainNavEnabled() {
		return nil
	}
	_, repoPath := a.detectMatomeruCheckout()
	return a.collectDomainGroups(repoPath)
}

func domOcc(id, class, name, glyph, tone, summary, file string, start, end int, sym string, pages, objects, tags []string) views.DomainOccurrence {
	return views.DomainOccurrence{
		ID:             id,
		Classification: class,
		Name:           name,
		Glyph:          glyph,
		Tone:           tone,
		Summary:        summary,
		Location:       views.CodeLocation{File: file, StartLine: start, EndLine: end, Symbol: sym},
		RelatedPages:   pages,
		RelatedObjects: objects,
		Tags:           tags,
	}
}

func staticDomainIndicators() []views.DomainOccurrence {
	return []views.DomainOccurrence{
		domOcc("ind.status.icon", DomainIndicatorsKey, "Declarative Status Icon", "●", "accent",
			"Schema-driven status glyph and row tone resolved from field values across tables, trees, and pickers.",
			"views/style.go", 193, 219, "StatusIndicator",
			[]string{SpaceWork, "outline", "project_pages", SpaceRuns, SpaceExtensions},
			[]string{data.KindTask, data.KindLane, data.KindRun, data.KindSchedule, "work.tasks"},
			[]string{"indicator", "status", "schema"}),
		domOcc("ind.unread.dot", DomainIndicatorsKey, "Project Nav Unread Dot", "●", "info",
			"Highlights unread records in the Project Portal left nav without washing out the full row.",
			"views/project_workspace.go", 427, 465, "navChildIconTone",
			[]string{"project_pages", SpaceProjects, "questions"},
			[]string{data.KindTask, data.KindQuestion, data.KindContext, data.KindDocument, data.KindPane, "project.home"},
			[]string{"indicator", "unread", "dot"}),
		domOcc("ind.toc.unread", DomainIndicatorsKey, "Pane TOC Unread Dot", "●", "accent",
			"Marks unread agent ui.page cards in the pane Pages table of contents rail.",
			"views/site.go", 116, 173, "compactTOCRow",
			[]string{SpacePanes, "reader"},
			[]string{data.KindPane, data.KindPage, "panes.toc"},
			[]string{"indicator", "unread", "toc"}),
		domOcc("ind.hook.badge", DomainIndicatorsKey, "Hook State Dot & Badge", "◉", "warning",
			"Live daemon hook telemetry badge showing running, waiting, approval, or idle agent state on a pane.",
			"app/runtime_daemon.go", 529, 575, "compactHookBadge",
			[]string{SpacePanes, "project_pages", "sessions"},
			[]string{data.KindPane, "panes.rail"},
			[]string{"indicator", "hook", "daemon", "live"}),
		domOcc("ind.needs.attention", DomainIndicatorsKey, "Needs-You Attention Indicator", "!", "warning",
			"Surfaces pending tool approvals, unanswered questions, or agent waits needing owner attention (W).",
			"app/runtime_daemon.go", 429, 438, "paneHasAttention",
			[]string{SpacePanes, "questions", "queue", "project_pages"},
			[]string{data.KindPane, data.KindQuestion, "panes.rail"},
			[]string{"indicator", "attention", "approval"}),
		domOcc("ind.lane.bar", DomainIndicatorsKey, "Lane Progress Bar & Glyph", "▰", "success",
			"Compact 4-cell colored progress bar and active/done/planned rollup strip for lane trees.",
			"views/lane.go", 56, 118, "CompactLaneBarRuns",
			[]string{SpacePanes, "project_pages", SpaceWork},
			[]string{data.KindLane, data.KindPane, "panes.toc"},
			[]string{"indicator", "lane", "progress"}),
		domOcc("ind.flags.universal", DomainIndicatorsKey, "Universal Flags (Unread/Star/Snooze/Archive)", "★", "accent",
			"Left-edge or trailing record flag column combining unread, starred, snoozed, and archived state marks.",
			"views/style.go", 220, 315, "ResolveRecordStyle",
			[]string{SpaceWork, "outline", "project_pages", SpaceRuns},
			[]string{data.KindTask, data.KindNote, data.KindDocument, data.KindSignal, data.KindContext, "work.tasks"},
			[]string{"indicator", "flags", "star", "snooze", "archive"}),
		domOcc("ind.outline.gutter", DomainIndicatorsKey, "Outline Queue & Agent Gutter", "▸", "info",
			"1-character left gutter column on task outlines marking queued, dispatched, and active-agent items.",
			"app/task_threads.go", 52, 60, "outlineGutterEnabled",
			[]string{"outline", "project_pages", SpaceWork},
			[]string{data.KindTask, "project.tasks", "work.tasks"},
			[]string{"indicator", "gutter", "queue"}),
		domOcc("ind.queue.attention", DomainIndicatorsKey, "Queue Row Attention Marker", "●", "warning",
			"Marks queue entries blocked on approvals, questions, or unread agent output.",
			"views/queue_page.go", 181, 195, "rowNeedsAttention",
			[]string{"queue", "project_pages"},
			[]string{data.KindTask, data.KindLane, data.KindPane, "project.home"},
			[]string{"indicator", "queue", "attention"}),
		domOcc("ind.focus.countdown", DomainIndicatorsKey, "Focus Pomodoro & Lock Indicator", "◷", "accent",
			"Chrome Pomodoro countdown chip and project lock/budget cells on the Focus Center.",
			"views/focus_page.go", 133, 147, "FocusCountdown",
			[]string{"focus", SpaceProjects, "project_pages"},
			[]string{data.KindTask},
			[]string{"indicator", "focus", "timer"}),
		domOcc("ind.threads.unread", DomainIndicatorsKey, "Threads Unread Counter", "●", "info",
			"Header and snippet unread indicators on task and note conversation threads.",
			"views/threads_page.go", 532, 560, "threadsHeader",
			[]string{"threads", "outline", "project_pages"},
			[]string{data.KindNote, data.KindTask},
			[]string{"indicator", "threads", "unread"}),
		domOcc("ind.show.ledger.glyph", DomainIndicatorsKey, "Show Ledger Entry Status Glyph", "✓", "success",
			"Compact semantic status glyph on Show page relation rows contrasting active vs completed records.",
			"views/show_ledger.go", 144, 179, "entryStatusStyle",
			[]string{"show", SpaceWork, "documents"},
			[]string{data.KindTask, data.KindLane, data.KindDocument, data.KindNote, "work.show"},
			[]string{"indicator", "show", "glyph"}),
	}
}

func staticDomainComponents() []views.DomainOccurrence {
	return []views.DomainOccurrence{
		domOcc("comp.tree", DomainComponentsKey, "Hierarchical Tree / Outline", "↳", "accent",
			"Box-drawing guide tree with folding, hoisting, sticky breadcrumbs, and inline editing.",
			"views/tree.go", 338, 437, "TreeViewWith",
			[]string{SpaceWork, "outline", "project_pages", SpaceFeatures},
			[]string{data.KindTask, data.KindLane, "work.tasks", "project.tasks"},
			[]string{"component", "tree", "outline"}),
		domOcc("comp.table", DomainComponentsKey, "Record Index Table", "☰", "accent",
			"Schema-projected sortable, filterable, groupable table with declarative cell tones and flags.",
			"views/views.go", 281, 374, "TableFromRecordsWith",
			[]string{SpaceWork, SpaceRuns, SpaceExtensions, "project_pages"},
			[]string{data.KindTask, data.KindRun, data.KindExtension, data.KindSignal, "work.tasks"},
			[]string{"component", "table", "index"}),
		domOcc("comp.record.card", DomainComponentsKey, "Record Card", "▢", "info",
			"Framed summary card with schema badges, status tone, and record reference facts.",
			"views/views.go", 228, 245, "RecordCard",
			[]string{SpaceWork, SpacePanes, SpaceFeatures, "project_pages"},
			[]string{data.KindTask, data.KindNote, data.KindQuestion, data.KindLane},
			[]string{"component", "card"}),
		domOcc("comp.record.form", DomainComponentsKey, "Schema Record Form", "✎", "info",
			"Declarative create/edit form built from Schema fields, universal flags, and relation pickers.",
			"views/views.go", 577, 604, "FormForRecordWith",
			[]string{SpaceWork, "settings", SpaceFeatures, "questions"},
			[]string{data.KindTask, data.KindNote, data.KindSchedule, data.KindConcept},
			[]string{"component", "form", "modal"}),
		domOcc("comp.record.picker", DomainComponentsKey, "Fuzzy Record Picker", "⌕", "info",
			"Reusable filterable picker modal over any record list with schema icons and tones.",
			"views/views.go", 641, 666, "PickerFromRecordsWith",
			[]string{SpaceWork, SpacePanes, SpaceProjects, "project_pages"},
			[]string{data.KindTask, data.KindPane, data.KindContext, data.KindPage},
			[]string{"component", "picker", "modal"}),
		domOcc("comp.lane.bar.stack", DomainComponentsKey, "Lane Bar Stack", "⑂", "success",
			"Focusable stack of active and completed lane progress strips mounted above a pane site.",
			"views/lane_bars.go", 51, 94, "LaneBarStack",
			[]string{SpacePanes, "project_pages"},
			[]string{data.KindLane, data.KindPane, "panes.toc"},
			[]string{"component", "bar", "progress"}),
		domOcc("comp.chip.strip", DomainComponentsKey, "Live Pointer Chip Strip", "◈", "accent",
			"Focusable inline pointer chips for files, worktrees, attachments, and context targets.",
			"views/chips.go", 31, 72, "ChipStripView",
			[]string{"show", "project_pages", SpacePanes},
			[]string{data.KindContext, "directory", data.KindDocument, "work.show"},
			[]string{"component", "chips"}),
		domOcc("comp.zero.field", DomainComponentsKey, "Zero-Field Query Modal", "⚡", "accent",
			"Content-fitted chip overlay for one-key filtering, sorting, and grouping across list views.",
			"views/zero_field.go", 86, 170, "ZeroFieldModalView",
			[]string{SpaceWork, SpaceRuns, "project_pages"},
			[]string{data.KindTask, data.KindSignal, data.KindRun},
			[]string{"component", "modal", "filter"}),
		domOcc("comp.answer.body", DomainComponentsKey, "Question Answer Body", "?", "warning",
			"Structured question prompt card paired with choice and free-text answer form fields.",
			"views/answer.go", 24, 42, "AnswerBody",
			[]string{"questions", SpacePanes, "project_pages"},
			[]string{data.KindQuestion, data.KindPage},
			[]string{"component", "modal", "question"}),
		domOcc("comp.markdown.reader", DomainComponentsKey, "Markdown Reader Modal", "◪", "info",
			"Scrollable Markdown overlay with heading TOC, link navigation, and code-block yank.",
			"app/open.go", 346, 397, "openReader",
			[]string{"reader", "documents", SpaceFeatures},
			[]string{data.KindDocument, data.KindNote, data.KindPage},
			[]string{"component", "markdown", "modal"}),
		domOcc("comp.modal.stack", DomainComponentsKey, "Modal Stack Host", "◫", "muted",
			"Central modal lifecycle stack managing nested overlays, escape guards, and focus restoration.",
			"app/modals.go", 25, 100, "pushModal",
			[]string{"settings", "bindings", "inspector", SpaceFeatures},
			[]string{"features.itemization"},
			[]string{"component", "modal", "stack"}),
	}
}

func staticDomainBadgesAndSignals() []views.DomainOccurrence {
	return []views.DomainOccurrence{
		domOcc("badge.status.badges", DomainBadgesKey, "Record Status Badges", "◈", "accent",
			"Projects schema enum values and universal flags into tone-coded ui.Badge slices.",
			"views/views.go", 157, 198, "StatusBadges",
			[]string{SpaceWork, "show", "project_pages", SpaceFeatures},
			[]string{data.KindTask, data.KindLane, data.KindRun, data.KindQuestion, "work.show"},
			[]string{"badge", "tone", "schema"}),
		domOcc("badge.recency.tone", DomainBadgesKey, "Recency Bucket Tone", "◷", "info",
			"Maps record age (today, this_week, last_week, older) to semantic theme tones.",
			"views/style.go", 42, 80, "RecencyTone",
			[]string{SpaceWork, SpaceRuns, "show"},
			[]string{data.KindTask, data.KindRun, data.KindNote, "work.tasks"},
			[]string{"badge", "tone", "recency"}),
		domOcc("badge.focus.lock", DomainBadgesKey, "Focus Lock & Budget Cells", "⛨", "warning",
			"Renders Pomodoro lock status and daily budget consumption tone cells.",
			"views/focus_page.go", 234, 260, "FocusLockCell",
			[]string{"focus", SpaceProjects},
			[]string{data.KindTask},
			[]string{"badge", "focus", "lock"}),
		domOcc("badge.test.summary", DomainBadgesKey, "Compact Test Result Summary", "✓", "success",
			"Compacts Go test execution streams into per-result semantic pass/fail/skip symbols.",
			"views/test_summary.go", 1, 37, "CompactTestOutput",
			[]string{SpaceRuns},
			[]string{data.KindRun, data.KindCommand},
			[]string{"badge", "runs", "tests"}),
		domOcc("sig.hook.state", DomainSignalsKey, "Effective Hook Telemetry State", "⚡", "accent",
			"Merges daemon hook telemetry and pending approvals into the live pane status state.",
			"app/runtime_daemon.go", 439, 506, "effectiveHookState",
			[]string{SpacePanes, "sessions", "project_pages"},
			[]string{data.KindPane, "panes.rail"},
			[]string{"signal", "daemon", "hook"}),
		domOcc("sig.next.attention", DomainSignalsKey, "Global Next-Attention Router (W)", "!", "warning",
			"Cycles directly to the next pane requiring owner approval, answer, or review.",
			"app/v2.go", 737, 775, "nextAttention",
			[]string{SpacePanes, "questions", "queue"},
			[]string{data.KindPane, data.KindQuestion},
			[]string{"signal", "attention", "navigation"}),
		domOcc("sig.project.unread", DomainSignalsKey, "Project Nav Unread Resolver", "●", "info",
			"Computes unread state across project tasks, questions, pages, panes, and context items.",
			"app/project_nav_unread.go", 112, 164, "isNavRecordUnread",
			[]string{"project_pages", SpaceProjects, "questions"},
			[]string{data.KindTask, data.KindQuestion, data.KindContext, data.KindPage, "project.home"},
			[]string{"signal", "unread", "project"}),
		domOcc("sig.triage.rule", DomainSignalsKey, "Signal-to-Triage-Rule Builder", "⑃", "warning",
			"Synthesizes a declarative triage.rule from the focused inbound signal record.",
			"app/inspect.go", 476, 515, "ruleFromSignal",
			[]string{SpaceWork, "project_pages"},
			[]string{data.KindSignal, data.KindTriageRule},
			[]string{"signal", "triage", "rule"}),
		domOcc("sig.session.pane", DomainSignalsKey, "Session Desk Pane Signals", "◉", "info",
			"Combines waiting-for-user and unread indicators for the AI Sessions Control Desk.",
			"app/sessions_desk.go", 135, 165, "sessionPaneSignals",
			[]string{"sessions", SpacePanes},
			[]string{data.KindPane, data.KindLane},
			[]string{"signal", "sessions", "attention"}),
	}
}

func staticDomainVerbsModesViews() []views.DomainOccurrence {
	return []views.DomainOccurrence{
		domOcc("verb.command.specs", DomainVerbsKey, "Registered Command Specs", "⌘", "accent",
			"Master registry of every atomic Matomeru command spec, params, tags, and metadata.",
			"app/command_specs.go", 39, 120, "CommandSpecs",
			[]string{"inspector", "bindings", SpaceFeatures},
			[]string{data.KindConcept},
			[]string{"verb", "registry", "command"}),
		domOcc("verb.default.bindings", DomainVerbsKey, "Declarative Default Keybindings", "⌨", "accent",
			"Data-driven default keymap binding modes, scopes, keys, and when-guards to atomic verbs.",
			"app/bindings.go", 285, 360, "DefaultBindings",
			[]string{"bindings", "inspector", SpaceFeatures},
			[]string{data.KindConcept},
			[]string{"verb", "binding", "keymap"}),
		domOcc("verb.features.bindings", DomainVerbsKey, "Features Page Bindings", "◇", "info",
			"Scoped Features nav, content, capture, and View-in-Code bindings guarded on Facts.",
			"app/features_capture.go", 181, 225, "featuresPageBindings",
			[]string{SpaceFeatures, "bindings"},
			[]string{data.KindConcept, "features.itemization"},
			[]string{"verb", "binding", "features"}),
		domOcc("mode.registry", DomainModesKey, "Interaction Modes Registry", "⛨", "accent",
			"Declares all Normal, Modal, Terminal, Filter, OutlineEdit, Reader, and CodeViewer modes.",
			"app/bindings.go", 240, 270, "Modes",
			[]string{"inspector", "bindings", SpacePanes},
			[]string{data.KindPane},
			[]string{"mode", "keymap"}),
		domOcc("mode.expr.guard", DomainModesKey, "Declarative Expr Guard Evaluator", "⛨", "info",
			"Cached Ochazuke expr predicate evaluator driving declarative binding guards and styling rules.",
			"views/style.go", 104, 126, "EvalExpr",
			[]string{"bindings", "inspector", SpaceWork},
			[]string{data.KindTriageRule, data.KindTask},
			[]string{"guard", "expr", "facts"}),
		domOcc("mode.record.facts", DomainModesKey, "Record Truthful Facts Builder", "⛨", "info",
			"Projects pure read-only Facts (unread, starred, snoozed, archived, status) onto ViewNodes.",
			"views/style.go", 127, 164, "RecordFacts",
			[]string{SpaceWork, "show", "project_pages"},
			[]string{data.KindTask, data.KindLane, data.KindNote, "work.tasks"},
			[]string{"guard", "facts", "pure"}),
		domOcc("view.pane.site", DomainViewsKey, "Pane Site & TOC View", "▣", "accent",
			"Splits a pane's Pages TOC rail alongside the active Open UI page or terminal surface.",
			"views/site.go", 238, 320, "PaneSiteViewWith",
			[]string{SpacePanes, "reader"},
			[]string{data.KindPane, data.KindPage, "panes.toc"},
			[]string{"view", "layout", "split"}),
		domOcc("view.project.workspace", DomainViewsKey, "Project Portal Workspace View", "◫", "accent",
			"Grouped left navigation rail paired with scoped project content and embedded pane preview.",
			"views/project_workspace.go", 93, 210, "ProjectWorkspaceView",
			[]string{"project_pages", SpaceProjects},
			[]string{data.KindTask, data.KindContext, data.KindPane, data.KindDocument, "project.home"},
			[]string{"view", "layout", "project"}),
		domOcc("view.project.home", DomainViewsKey, "Project Home Dashboard View", "◈", "info",
			"Project homepage surfacing decisions, new items, panes, queue progress, and recent prompts.",
			"views/project_home.go", 77, 180, "ProjectHomeView",
			[]string{"project_pages", "questions", "queue"},
			[]string{data.KindTask, data.KindQuestion, data.KindPane, "project.home"},
			[]string{"view", "dashboard", "project"}),
		domOcc("view.show.ledger", DomainViewsKey, "Show Page Ledger Layout", "▤", "accent",
			"Single-column borderless ledger outline presenting record properties, body, relations, and timeline.",
			"views/show_ledger.go", 793, 865, "showViewLedger",
			[]string{"show", SpaceWork, "documents"},
			[]string{data.KindTask, data.KindDocument, data.KindLane, "work.show"},
			[]string{"view", "layout", "show"}),
		domOcc("view.queue.page", DomainViewsKey, "Queues & Dispatch View", "▶", "info",
			"Live multi-queue board grouping active agent tasks, waiting reviews, and attention jumps.",
			"views/queue_page.go", 86, 175, "QueuePageView",
			[]string{"queue", "project_pages", SpaceWork},
			[]string{data.KindTask, data.KindLane, data.KindPane},
			[]string{"view", "layout", "queue"}),
		domOcc("view.features.page", DomainViewsKey, "Features Catalog Page View", "◇", "accent",
			"20/80 split navigation rail and scrollable detail canvas for Pages, Nouns, Domain, Groups, and Concepts.",
			"views/features_page.go", 124, 171, "FeaturesPageView",
			[]string{SpaceFeatures},
			[]string{data.KindConcept, data.KindTask, "features.itemization"},
			[]string{"view", "layout", "features"}),
		domOcc("view.settings.specs", DomainViewsKey, "Declarative Settings Catalog", "⚡", "info",
			"Ordered ParamSpec list driving the live Settings modal and persistent settings.json defaults.",
			"app/settings.go", 301, 445, "SettingSpecs",
			[]string{"settings", SpaceFeatures},
			[]string{data.KindConcept},
			[]string{"view", "settings", "config"}),
	}
}

func staticDomainOccurrences() []views.DomainOccurrence {
	out := make([]views.DomainOccurrence, 0, 48)
	out = append(out, staticDomainIndicators()...)
	out = append(out, staticDomainComponents()...)
	out = append(out, staticDomainBadgesAndSignals()...)
	out = append(out, staticDomainVerbsModesViews()...)
	return out
}

func (a *App) collectDomainGroups(repoPath string) []views.DomainGroup {
	occurrences := a.mergedDomainOccurrences(repoPath)
	byClass := make(map[string][]views.DomainOccurrence, len(domainGroupCatalog))
	for _, occ := range occurrences {
		byClass[occ.Classification] = append(byClass[occ.Classification], occ)
	}
	groups := make([]views.DomainGroup, 0, len(domainGroupCatalog))
	for _, meta := range domainGroupCatalog {
		items := byClass[meta.Key]
		groups = append(groups, views.DomainGroup{
			Key:     meta.Key,
			Title:   meta.Title,
			Icon:    meta.Icon,
			Summary: meta.Summary,
			Items:   items,
		})
	}
	return groups
}

func totalDomainOccurrences(groups []views.DomainGroup) int {
	total := 0
	for _, g := range groups {
		total += len(g.Items)
	}
	return total
}

func (a *App) buildFeaturesDomainNavItems(snap featuresSnapshot, activeKey string, validKeys map[string]bool) []views.FeaturesNavItem {
	items := make([]views.FeaturesNavItem, 0, len(snap.domainGroups)+1)
	allKey := featureDomainKey(DomainEverythingKey).String()
	validKeys[allKey] = true
	items = append(items, views.FeaturesNavItem{
		Key:     allKey,
		Section: featuresDomainNavGroup,
		Label:   "Everything",
		Icon:    "❖",
		Count:   totalDomainOccurrences(snap.domainGroups),
		Active:  activeKey == allKey,
	})
	for _, g := range snap.domainGroups {
		itemKey := featureDomainKey(g.Key).String()
		validKeys[itemKey] = true
		items = append(items, views.FeaturesNavItem{
			Key:     itemKey,
			Section: featuresDomainNavGroup,
			Label:   g.Title,
			Icon:    firstNonEmpty(g.Icon, "◆"),
			Count:   len(g.Items),
			Active:  activeKey == itemKey,
		})
	}
	return items
}

func (a *App) buildFeaturesDomainDetail(snap featuresSnapshot, fk featureKey) views.FeaturesDetailSpec {
	key, activeKey := fk.Name, fk.String()
	thoughts := a.domainRawThoughts(snap, key)
	if key == DomainEverythingKey {
		total := totalDomainOccurrences(snap.domainGroups)
		return views.FeaturesDetailSpec{
			Kind:         string(featureKeyDomain),
			Key:          activeKey,
			Title:        "Everything",
			Subtitle:     "Domain · Complete non-segmented codebase index",
			Summary:      "Every recurring domain classification across Matomeru (Indicators, Components, Badges, Signals, Verbs, Modes, Views) with verified file:start-end locations. Press enter or C on any row to View in Code.",
			Badges:       []ui.Badge{{Text: "everything", Tone: "accent"}, {Text: fmt.Sprintf("%d classes", len(snap.domainGroups)), Tone: "info"}, {Text: fmt.Sprintf("%d occurrences", total), Tone: "muted"}},
			DomainGroups: snap.domainGroups,
			RawThoughts:  thoughts,
			Concepts: a.featuresConceptItems(snap.concepts, func(rec data.Record) bool {
				return featuresConceptTargets(rec, key, activeKey)
			}),
		}
	}
	for _, g := range snap.domainGroups {
		if g.Key != key {
			continue
		}
		return views.FeaturesDetailSpec{
			Kind:         string(featureKeyDomain),
			Key:          activeKey,
			Title:        g.Title,
			Subtitle:     "Domain · " + g.Key + " (complete, non-segmented)",
			Summary:      g.Summary + " Press enter or C on any row to View in Code.",
			Badges:       []ui.Badge{{Text: "domain", Tone: "accent"}, {Text: fmt.Sprintf("%d occurrences", len(g.Items)), Tone: "info"}},
			DomainGroups: []views.DomainGroup{g},
			RawThoughts:  thoughts,
			Concepts: a.featuresConceptItems(snap.concepts, func(rec data.Record) bool {
				return featuresConceptTargets(rec, key, activeKey)
			}),
		}
	}
	return views.FeaturesDetailSpec{
		Kind:        string(featureKeyDomain),
		Key:         activeKey,
		Title:       views.Label(key),
		Subtitle:    "Domain · " + key,
		Summary:     "Domain classification catalog.",
		RawThoughts: thoughts,
	}
}

func (a *App) domainRawThoughts(snap featuresSnapshot, key string) []views.FeaturesLinkedItem {
	var thoughts []views.FeaturesLinkedItem
	for _, rt := range snap.rawThoughts {
		if a.recordMatchesTarget(rt, "targetDomains", key, []string{key}, false) {
			thoughts = append(thoughts, a.linkedItemFromRecord(rt))
		}
	}
	return thoughts
}

func relatedDomainGroupsForTarget(allGroups []views.DomainGroup, pageKey string, objectKeys ...string) []views.DomainGroup {
	if len(allGroups) == 0 {
		return nil
	}
	var out []views.DomainGroup
	for _, g := range allGroups {
		var matched []views.DomainOccurrence
		for _, item := range g.Items {
			if occurrenceMatchesTarget(item, pageKey, objectKeys) {
				matched = append(matched, item)
			}
		}
		if len(matched) == 0 {
			continue
		}
		out = append(out, views.DomainGroup{
			Key:     "related." + g.Key,
			Title:   "Related " + g.Title,
			Icon:    g.Icon,
			Summary: fmt.Sprintf("%s tied to this view/object · press enter or C to View in Code", g.Title),
			Items:   matched,
		})
	}
	return out
}

func occurrenceMatchesTarget(item views.DomainOccurrence, pageKey string, objectKeys []string) bool {
	if pageKey != "" && containsFold(item.RelatedPages, pageKey) {
		return true
	}
	for _, obj := range objectKeys {
		if obj != "" && containsFold(item.RelatedObjects, obj) {
			return true
		}
	}
	return false
}

func domainTargetLabel(key string) string {
	if key == DomainEverythingKey {
		return "Everything"
	}
	if meta, ok := lookupDomainGroupMeta(key); ok {
		return meta.Title
	}
	return views.Label(key)
}

func sortDomainOccurrences(items []views.DomainOccurrence) {
	slices.SortStableFunc(items, func(a, b views.DomainOccurrence) int {
		return strings.Compare(a.ID, b.ID)
	})
}
