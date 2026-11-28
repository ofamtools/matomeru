package app

import (
	"ofam.dev/matomeru/views"
)

// Declared props of pages, components and domain groups. Pure data: settings
// are listed by key and resolved against SettingSpecs; Facts, ViewNode
// surfaces and theme tokens are listed by the name they have in code.

// featuresUniversalRecordProps are the fields every record carries beside
// its schema fields.
var featuresUniversalRecordProps = []views.FeaturesPropRow{
	propRow(featuresPropRoleUniversal, "id", "string", "Record id, unique within its kind"),
	propRow(featuresPropRoleUniversal, "kind", "string", "Noun kind naming the record's schema"),
	propRow(featuresPropRoleUniversal, "archiveStatus", "enum", "Archive flag: active or archived"),
	propRow(featuresPropRoleUniversal, "starStatus", "enum", "Star flag: unstarred or starred"),
	propRow(featuresPropRoleUniversal, "createdAt", "time", "When the record was created"),
	propRow(featuresPropRoleUniversal, "updatedAt", "time", "When the record's fields last changed"),
	propRow(featuresPropRoleUniversal, "snoozedUntil", "time", "Hides the record until this time"),
	propRow(featuresPropRoleUniversal, "relations", "list", "Typed links to other records (about, context, plan, claim, ...)"),
}

// Fact sets the shared traits and widgets expose on every list that uses them.
var (
	scrollFacts = []views.FeaturesPropRow{
		propFact("atTop", "bool", "The list or scroll area is at its first row"),
		propFact("atBottom", "bool", "The list or scroll area is at its last row"),
		propFact("empty", "bool", "There is nothing to show"),
	}
	filterFacts = []views.FeaturesPropRow{
		propFact("filtering", "bool", "The filter input is being typed in"),
		propFact("hasFilter", "bool", "A filter query is kept on the list"),
		propFact("matchMode", "string", "How the filter matches: substring, fuzzy or regex"),
	}
	selectFacts = []views.FeaturesPropRow{
		propFact("hasSelection", "bool", "A visual range or at least one mark is set"),
		propFact("visualActive", "bool", "Visual range selection is on"),
		propFact("markedCount", "int", "How many rows are marked"),
	}
	tableFacts = []views.FeaturesPropRow{
		propFact("rowCount", "int", "How many rows the table shows"),
		propFact("isGroupHeader", "bool", "The focused row is a group header"),
	}
	treeFacts = []views.FeaturesPropRow{
		propFact("isTree", "bool", "The focused list is a tree"),
		propFact("folded", "bool", "The focused node is folded"),
		propFact("hasChildren", "bool", "The focused node has children"),
		propFact("hoisted", "bool", "The tree is hoisted onto one subtree"),
	}
	recordFacts = []views.FeaturesPropRow{
		propFact("unread", "bool", "The focused record is unread"),
		propFact("hasStatus", "bool", "The focused record's schema has a status field"),
	}
)

// featuresUniversalPageProps are the props every page has; a page or
// component with no declared entry shows these.
var featuresUniversalPageProps = featuresPropsEntry{
	Settings: []string{SetTheme, SetDensity, SetBorderStyle, SetScrollOff},
	Facts:    propFacts(scrollFacts, filterFacts),
	Surfaces: []views.FeaturesPropRow{
		propSurface("Scope", "string", "Binding scope of a node; keys resolve from the focused scope outward"),
		propSurface("Facts", "map", "Read-only truths a node exposes to binding when guards"),
		propSurface("Focusable", "bool", "Whether focus can land on the node"),
	},
	Tokens: []views.FeaturesPropRow{
		propToken("focus.border", "Border of the focused panel"),
		propToken("selected", "The focused row"),
	},
}

// featuresDomainOccurrenceProps is the shape of one domain occurrence.
var featuresDomainOccurrenceProps = []views.FeaturesPropRow{
	propRow("occurrence", "Classification", "string", "Domain group the occurrence belongs to"),
	propRow("occurrence", "Name", "string", "Display name of the recurring concept"),
	propRow("occurrence", "Glyph", "string", "Symbol drawn in front of the row"),
	propRow("occurrence", "Tone", "tone", "Semantic tone of the glyph"),
	propRow("occurrence", "Location", "file:start-end", "Source file, line range and symbol opened by View in Code"),
	propRow("occurrence", "RelatedPages", "list", "Pages the concept appears on"),
	propRow("occurrence", "RelatedObjects", "list", "Nouns and components the concept belongs to"),
	propRow("occurrence", "Tags", "list", "Free search tags"),
}

var featuresPagePropsCatalog = map[string]featuresPropsEntry{
	SpacePanes: {
		Settings: []string{SetPanesGroup, SetShowTOC, SetTOCStyle, SetTOCGrouped, SetLaneBar, SetRailStateColumn, SetRailEnterMode, SetSideTermSplit, SetComposerCompact, SetInteractiveCanvas, SetPanesCompactTopArea},
		Facts: propFacts(scrollFacts, filterFacts, []views.FeaturesPropRow{
			propFact("hasPendingApproval", "bool", "The focused pane waits on a tool approval"),
			propFact("statusTone", "tone", "Tone of the focused pane's hook state"),
			propFact("unlinked", "bool", "The focused pane has no project or task"),
			propFact("tocGroup", "string", "Group of the focused row in the pane's page list"),
			propFact("canvasMarkdown", "bool", "Focus is on a Markdown block of a page"),
			propFact("hasLinks", "bool", "The focused page block has links"),
		}),
		Surfaces: []views.FeaturesPropRow{
			propSurface(railTable, "table", "Pane rail, scope rail"),
			propSurface(views.LaneBarsID, "stack", "Lane progress bars above the pane's site, scope lanebar"),
			propSurface(views.ScopeTOC, "scope", "Page list of the active pane"),
			propSurface(views.ScopeSite, "scope", "The pane's terminal or its open page"),
		},
		Tokens: []views.FeaturesPropRow{
			propToken("state.working", "Pane whose agent is running"),
			propToken("row.attention", "Pane that needs the owner"),
			propToken("progress.fill", "Filled part of a lane progress bar"),
		},
	},
	SpaceProjects: {
		Settings: []string{SetProjectsTopNavTab, SetProjectsDynamicTabs, SetProjectsChildPaneView, SetProjectPortalOnP, SetFocusHideNonPriority},
		Facts: propFacts(scrollFacts, filterFacts, []views.FeaturesPropRow{
			propFact("projectsDir", "bool", "Focus is on the project directory list"),
			propFact("projectUnread", "bool", "The focused project has unread items"),
		}),
		Surfaces: []views.FeaturesPropRow{
			propSurface(views.ScopeProjectsDir, "scope", "Project list on the left"),
			propSurface(views.ScopeProjectsPreview, "scope", "Preview of the focused project on the right"),
			propSurface(views.ScopeProjectTab, "scope", "A project opened as a top tab"),
		},
		Tokens: []views.FeaturesPropRow{propToken("icon.unread", "Unread dot on a project row")},
	},
	"project_pages": {
		Settings: []string{SetProjectWorkspaceInPanes, SetProjectHomeDashboard, SetProjectOutlineAsPage, SetProjectNavSections, SetProjectNavMode, SetProjectsNavTree, SetProjectsNavSurfaceUnread, SetProjectsNavUnreadDotOnly, SetProjectsNavLaneProgressBar, SetProjectNavCompact},
		Facts: propFacts(scrollFacts, filterFacts, []views.FeaturesPropRow{
			propFact("section", "string", "Project nav section the focus is in"),
			propFact("navMode", "string", "Left nav mode: sections, tree or by pane"),
			propFact("navExpandable", "bool", "The focused nav row can be opened"),
			propFact("unreadCount", "int", "Unread items under the focused nav row"),
			propFact("inChildPaneView", "bool", "A project pane is shown inside the project page"),
			propFact("projectOutline", "bool", "Focus is on the project's task outline"),
		}),
		Surfaces: []views.FeaturesPropRow{
			propSurface(views.ProjectNavID, "panel", "Grouped left nav, scope project.nav"),
			propSurface(views.ProjectHomePanelID, "panel", "Project home dashboard"),
			propSurface(projectOutlineTree, "tree", "Project task outline"),
			propSurface(views.ScopeProjectContent, "scope", "Content of the chosen nav row"),
		},
		Tokens: []views.FeaturesPropRow{
			propToken("icon.unread", "Unread dot on a nav row"),
			propToken("progress.active", "Running part of a lane progress bar"),
		},
	},
	SpaceWork: {
		Settings: []string{SetWorkTasksLayout, SetWorkHierarchyLayout, SetZeroFieldQueryDefault, SetFlagsColumn, SetMatchMode, SetViewCycle, SetEnterOpensShow, SetRowSemanticStyling, SetStatusIcons, SetColorByRecency},
		Facts:    propFacts(scrollFacts, filterFacts, selectFacts, tableFacts, treeFacts, recordFacts),
		Surfaces: []views.FeaturesPropRow{
			propSurface(workTable, "table", "Records of the chosen noun, scope index"),
			propSurface(views.ScopeShow, "scope", "Detail of one record"),
			propSurface(views.ZeroFieldModalID, "modal", "One-key filter, sort and group overlay"),
		},
		Tokens: []views.FeaturesPropRow{
			propToken("table.header", "Column titles"),
			propToken("table.group", "Group header rows"),
			propToken("row.muted", "Done or archived rows"),
		},
	},
	"outline": {
		Settings: []string{SetOutlineEnterNewline, SetOutlineStartCollapsed, SetOutlineGutter, SetOutlineAutoSuggest, SetWrapOutline, SetTreeGuides, SetTreeTopLevelGap, SetTreeHLFolds, SetStickyTreeBreadcrumbs, SetTasksInlineNoteThreads},
		Facts: propFacts(scrollFacts, filterFacts, selectFacts, treeFacts, []views.FeaturesPropRow{
			propFact("outlineView", "string", "Which outline view is on"),
			propFact("cleanSlate", "bool", "The clean-slate canvas is on"),
			propFact("hasNotes", "bool", "The focused task has notes"),
		}),
		Surfaces: []views.FeaturesPropRow{
			propSurface(outlineTree, "tree", "Task outline, scope outline"),
			propSurface("gutter", "prop", "One-character left gutter for queue and agent marks"),
			propSurface("guides", "prop", "Box-drawing guide lines between parents and children"),
		},
		Tokens: []views.FeaturesPropRow{
			propToken("state.done.dim", "Finished tasks"),
			propToken("input.edit", "Row being edited in place"),
		},
	},
	SpaceRuns: {
		Settings: []string{SetCompactTestOutput, SetFlagsColumn, SetMatchMode, SetRowSemanticStyling},
		Facts:    propFacts(scrollFacts, filterFacts, selectFacts, tableFacts, recordFacts),
		Surfaces: []views.FeaturesPropRow{
			propSurface(runsTable, "table", "Runs with state, exit code and flags"),
			propSurface(views.JobsListID, "list", "Background jobs with their output"),
		},
		Tokens: []views.FeaturesPropRow{
			propToken("state.errored", "Failed run"),
			propToken("state.done", "Finished run"),
		},
	},
	SpaceExtensions: {
		Settings: []string{SetExtensionsShowCatalog, SetExtensionsGoPrivate},
		Facts:    propFacts(scrollFacts, filterFacts, tableFacts),
		Surfaces: []views.FeaturesPropRow{propSurface(extTable, "table", "Extensions with health and binary")},
		Tokens:   []views.FeaturesPropRow{propToken("badge.success", "Installed and healthy extension")},
	},
	"questions": {
		Settings: []string{SetAnswerShowQuestion},
		Facts: propFacts(scrollFacts, filterFacts, []views.FeaturesPropRow{
			propFact("answered", "bool", "The focused question card is answered"),
			propFact("batchId", "string", "Batch the focused question belongs to"),
			propFact("batchSubmit", "bool", "Focus is on the submit-all card of a batch"),
		}),
		Surfaces: []views.FeaturesPropRow{propSurface(views.ScopeIndex, "scope", "Question list")},
		Tokens:   []views.FeaturesPropRow{propToken("badge.warning", "Question waiting for an answer")},
	},
	"documents": {
		Settings: []string{SetShowLayout, SetShowContent, SetShowMetadata, SetShowEmpty},
		Facts: propFacts(scrollFacts, filterFacts, []views.FeaturesPropRow{
			propFact("showLayout", "string", "Layout of the record detail"),
			propFact("relationKind", "string", "Relation section the focus is in"),
		}),
		Surfaces: []views.FeaturesPropRow{propSurface(views.ScopeShow, "scope", "Document detail with body and relations")},
		Tokens:   []views.FeaturesPropRow{propToken("md.code", "Code in a Markdown body")},
	},
	"settings": {
		Settings: []string{SetTheme, SetDensity, SetBorderStyle, SetCardBorderStyle, SetToast, SetModalEscapeGuard},
		Facts:    propFacts(scrollFacts, filterFacts, []views.FeaturesPropRow{propFact("editing", "bool", "A setting's value is being edited")}),
		Surfaces: []views.FeaturesPropRow{propSurface(views.ScopeEdit, "scope", "Setting form; a change applies at once")},
		Tokens:   []views.FeaturesPropRow{propToken("input.focus", "The focused setting field")},
	},
	"bindings": {
		Settings: []string{SetHintsNounFirst, SetInputReadlinePriority},
		Facts:    propFacts(scrollFacts, filterFacts, tableFacts),
		Surfaces: []views.FeaturesPropRow{propSurface(bindingsTable, "table", "Every binding with its mode, scope, key and verb")},
		Tokens:   []views.FeaturesPropRow{propToken("key", "Key names in hints and tables")},
	},
	"reader": {
		Settings: []string{SetPageLockScroll, SetInteractiveCanvas, SetSearchHighlight, SetScrollOff},
		Facts: propFacts(scrollFacts, []views.FeaturesPropRow{
			propFact("onLink", "bool", "Focus is on a link"),
			propFact("linkCount", "int", "Links in the focused block"),
			propFact("canvasTree", "bool", "Focus is on a tree block"),
		}),
		Surfaces: []views.FeaturesPropRow{
			propSurface(ScopeCanvasMarkdown, "scope", "Markdown block of a page"),
			propSurface("scrollKey", "prop", "Key the scroll position is remembered under"),
		},
		Tokens: []views.FeaturesPropRow{
			propToken("link.focused", "The focused link"),
			propToken("md.quote", "Quoted text"),
		},
	},
	"inspector": {
		Settings: []string{SetActionLogEnabled, SetDeclarativePresentations},
		Facts:    propFacts(scrollFacts, filterFacts, []views.FeaturesPropRow{propFact("inspecting", "string", "What the inspector is showing")}),
		Surfaces: []views.FeaturesPropRow{propSurface(views.ScopeIndex, "scope", "Lists of commands, modes, bindings and schemas")},
		Tokens:   []views.FeaturesPropRow{propToken("match", "Matched text in a filtered list")},
	},
	SpaceFeatures: {
		Settings: []string{SetSelfHostedFeaturesEnabled, SetSelfHostedFeaturesRepoPath, SetFeaturesItemization, SetFeaturesOutline, SetFeaturesDomainNav, SetFeaturesCaptureDocked},
		Facts: propFacts(scrollFacts, filterFacts, treeFacts, []views.FeaturesPropRow{
			propFact("featuresPageActive", "bool", "The Features page is the active page"),
			propFact("featuresActiveKey", "string", "Key of the item chosen in the left nav"),
			propFact("featuresOutline", "bool", "The detail is shown as one outline"),
			propFact("featuresSection", "string", "Outline section the focus is in"),
			propFact("captureExpanded", "bool", "The docked capture panel is expanded"),
		}),
		Surfaces: []views.FeaturesPropRow{
			propSurface(views.ScopeFeaturesNav, "scope", "Left nav: pages, nouns, domain, groups, concepts"),
			propSurface(views.ScopeFeaturesContent, "scope", "Detail of the chosen item"),
			propSurface(featuresOutlineTree, "tree", "Detail outline: features, concepts, views, ideas, props"),
			propSurface(views.FeaturesThoughtInputID, "input", "Inline raw-thought note box"),
		},
		Tokens: []views.FeaturesPropRow{
			propToken("badge.warning", "Planned nouns and verbs"),
			propToken("badge.success", "Live and existing nouns"),
		},
	},
}

var featuresComponentPropsCatalog = map[string]featuresPropsEntry{
	"panes.rail": {
		Settings: []string{SetPanesGroup, SetRailAutoWiden, SetRailStateColumn, SetRailEnterMode},
		Facts:    propFacts(scrollFacts, filterFacts, []views.FeaturesPropRow{propFact("hasPendingApproval", "bool", "The focused pane waits on a tool approval"), propFact("unlinked", "bool", "The focused pane has no project or task")}),
		Surfaces: []views.FeaturesPropRow{propSurface(railTable, "table", "Pane rail, scope rail")},
		Tokens:   []views.FeaturesPropRow{propToken("row.attention", "Pane that needs the owner")},
	},
	"panes.toc": {
		Settings: []string{SetShowTOC, SetTOCStyle, SetTOCGrouped, SetCompactTOC, SetTOCLivePreview, SetLaneInTOC},
		Facts:    propFacts(scrollFacts, []views.FeaturesPropRow{propFact("tocGroup", "string", "Group of the focused row"), propFact("unread", "bool", "The focused page is unread")}),
		Surfaces: []views.FeaturesPropRow{propSurface(views.ScopeTOC, "scope", "Page list of the active pane")},
		Tokens:   []views.FeaturesPropRow{propToken("icon.unread", "Unread dot on a page row")},
	},
	"panes.composer": {
		Settings: []string{SetComposerCompact, SetComposerWrap, SetAttachmentsEnabled},
		Facts:    []views.FeaturesPropRow{propFact("editing", "bool", "Text is being typed")},
		Surfaces: []views.FeaturesPropRow{propSurface("multiline", "prop", "The input takes several lines"), propSurface("placeholder", "prop", "Text shown while the input is empty")},
		Tokens:   []views.FeaturesPropRow{propToken("input.edit", "The input while typing")},
	},
	"project.home": {
		Settings: []string{SetProjectHomeDashboard, SetProjectHomeQueueProgress, SetProjectNavShowNewItems},
		Facts:    propFacts(scrollFacts, []views.FeaturesPropRow{propFact("openQuestion", "bool", "The focused card is an unanswered question"), propFact("projectUnread", "bool", "The project has unread items")}),
		Surfaces: []views.FeaturesPropRow{propSurface(views.ProjectHomePanelID, "panel", "Project home dashboard"), propSurface(views.ProjectHomeQueuesID, "panel", "Queue progress strip")},
		Tokens:   []views.FeaturesPropRow{propToken("badge.warning", "Decision waiting for the owner")},
	},
	"project.tasks": {
		Settings: []string{SetProjectOutlineAsPage, SetOutlineGutter, SetTreeGuides},
		Facts:    propFacts(scrollFacts, filterFacts, treeFacts, []views.FeaturesPropRow{propFact("projectOutline", "bool", "Focus is on the project's task outline")}),
		Surfaces: []views.FeaturesPropRow{propSurface(projectOutlineTree, "tree", "Project task outline")},
		Tokens:   []views.FeaturesPropRow{propToken("state.done.dim", "Finished tasks")},
	},
	"work.tasks": {
		Settings: []string{SetWorkTasksLayout, SetZeroFieldQueryDefault, SetFlagsColumn, SetStatusIcons},
		Facts:    propFacts(scrollFacts, filterFacts, selectFacts, tableFacts, treeFacts),
		Surfaces: []views.FeaturesPropRow{propSurface(workTable, "table", "Tasks as a table or a tree")},
		Tokens:   []views.FeaturesPropRow{propToken("table.header", "Column titles")},
	},
	"work.show": {
		Settings: []string{SetShowLayout, SetShowMetadata, SetShowEmpty, SetShowHideEmptyRelations, SetShowTitleAttachmentChips},
		Facts:    propFacts(scrollFacts, []views.FeaturesPropRow{propFact("showLayout", "string", "Layout of the record detail"), propFact("relationKind", "string", "Relation section the focus is in"), propFact("sectionKind", "string", "Kind of the focused section")}),
		Surfaces: []views.FeaturesPropRow{propSurface(views.ScopeShow, "scope", "Fields, body and relations of one record")},
		Tokens:   []views.FeaturesPropRow{propToken("title", "Record title")},
	},
	"extensions.catalog": {
		Settings: []string{SetExtensionsShowCatalog, SetExtensionsGoPrivate},
		Facts:    propFacts(scrollFacts, filterFacts, tableFacts),
		Surfaces: []views.FeaturesPropRow{propSurface(extTable, "table", "Installed and installable extensions")},
		Tokens:   []views.FeaturesPropRow{propToken("badge.success", "Installed extension")},
	},
	"features.itemization": {
		Settings: []string{SetFeaturesItemization, SetFeaturesOutline},
		Facts:    propFacts(treeFacts, []views.FeaturesPropRow{propFact("featuresSection", "string", "Outline section the focus is in"), propFact("isSection", "bool", "The focused row is a section heading")}),
		Surfaces: []views.FeaturesPropRow{propSurface(featuresOutlineTree, "tree", "Props and views listed in the detail outline")},
		Tokens:   []views.FeaturesPropRow{propToken("badge", "The suggested mark on a proposed prop or view")},
	},
}

var featuresDomainPropsCatalog = map[string]featuresPropsEntry{
	DomainIndicatorsKey: {
		Settings: []string{SetStatusIcons, SetFlagsOnLeft, SetOutlineGutter, SetProjectsNavUnreadDotOnly, SetTasksShowNoteIndicators},
		Facts:    propFacts(recordFacts, []views.FeaturesPropRow{propFact("hasPendingApproval", "bool", "A pane waits on a tool approval"), propFact("unreadCount", "int", "Unread items under a row")}),
		Tokens:   []views.FeaturesPropRow{propToken("icon.unread", "Unread dot"), propToken("icon.star", "Star mark"), propToken("row.attention", "Row that needs the owner")},
	},
	DomainComponentsKey: {
		Settings: []string{SetDensity, SetBorderStyle, SetCardBorderStyle, SetPickerDefaultMode, SetLivePointerChips},
		Facts:    propFacts(scrollFacts, filterFacts),
		Surfaces: []views.FeaturesPropRow{propSurface("Kind", "string", "Which component a node is: panel, card, table, tree, form, picker"), propSurface("Border", "bool", "Whether the node is framed"), propSurface("Badges", "list", "Tone-coded marks beside the title")},
		Tokens:   []views.FeaturesPropRow{propToken("border", "Frame of an unfocused component"), propToken("surface", "Background of a component")},
	},
	DomainBadgesKey: {
		Settings: []string{SetRowSemanticStyling, SetColorByRecency, SetCompactTestOutput},
		Facts:    propFacts(recordFacts, []views.FeaturesPropRow{propFact("statusTone", "tone", "Tone of the focused row's status")}),
		Tokens:   []views.FeaturesPropRow{propToken("badge", "Plain badge"), propToken("badge.accent", "Accent badge"), propToken("badge.success", "Success badge"), propToken("badge.warning", "Warning badge"), propToken("badge.error", "Error badge")},
	},
	DomainSignalsKey: {
		Settings: []string{SetVerboseHookBar, SetProjectsNavSurfaceUnread, SetProjectsNavSurfaceContextUnread, SetToast},
		Facts:    []views.FeaturesPropRow{propFact("hasPendingApproval", "bool", "A pane waits on a tool approval"), propFact("unread", "bool", "The focused record is unread"), propFact("projectUnread", "bool", "A project has unread items")},
		Tokens:   []views.FeaturesPropRow{propToken("state.working", "Agent is running"), propToken("unread", "Unread text")},
	},
	DomainVerbsKey: {
		Settings: []string{SetHintsNounFirst, SetHintsDefaultAction, SetHintsModifierActions, SetViewCycle},
		Facts:    []views.FeaturesPropRow{propFact("hasSelection", "bool", "A batch verb has rows to act on"), propFact("hasStatus", "bool", "Status verbs apply to the focused record")},
		Surfaces: []views.FeaturesPropRow{propSurface("bindings.json", "file", "Rows of mode, scope, key, when guard, verb and args"), propSurface("CommandSpec", "struct", "A verb's id, description, params and meta")},
		Tokens:   []views.FeaturesPropRow{propToken("key", "Key names in hints")},
	},
	DomainModesKey: {
		Settings: []string{SetModalEscapeGuard, SetInputReadlinePriority, SetInputFilterScrollMotions, SetPickerDefaultMode},
		Facts:    propFacts(filterFacts, []views.FeaturesPropRow{propFact("editing", "bool", "Text is being typed"), propFact("visualActive", "bool", "Visual range selection is on")}),
		Surfaces: []views.FeaturesPropRow{propSurface("when", "expr", "Guard over Facts that decides whether a binding applies")},
		Tokens:   []views.FeaturesPropRow{propToken("input.edit", "An input in insert mode")},
	},
	DomainViewsKey: {
		Settings: []string{SetShowLayout, SetWorkTasksLayout, SetWorkHierarchyLayout, SetSplitResize, SetTopChromeRedesign, SetDeclarativePresentations},
		Facts:    propFacts(scrollFacts, []views.FeaturesPropRow{propFact("showLayout", "string", "Layout of the record detail"), propFact("isTree", "bool", "The focused list is a tree")}),
		Surfaces: []views.FeaturesPropRow{propSurface("ViewNode", "struct", "ID, Kind, Scope, Title, Props, Facts and Children of every drawn node"), propSurface("Box", "constraints", "Fixed or flexible size of a node in its split")},
		Tokens:   []views.FeaturesPropRow{propToken("border.focus", "Frame of the focused panel")},
	},
	DomainTraitsKey: {
		Settings: []string{SetMatchMode, SetScrollOff, SetLongJumpPolicy, SetEdgeFocusSpill, SetScrollFollowTrait, SetFilterInPlaceTree, SetModalEscapeGuard, SetViewCycle},
		Facts:    propFacts(scrollFacts, filterFacts, selectFacts, []views.FeaturesPropRow{propFact("folded", "bool", "The focused node is folded")}),
		Tokens:   []views.FeaturesPropRow{propToken("match", "Text the filter matched"), propToken("selected", "The focused row"), propToken("selected.inactive", "The focused row of an unfocused list")},
	},
	DomainChromeKey: {
		Settings: []string{SetTopChromeRedesign, SetChromeSystemStats, SetPinnedAgentChrome, SetFocusChromeCountdown, SetFilterBarBottomLeft, SetHintsNounFirst, SetFocusHintsStyle, SetProjectsDynamicTabs},
		Facts:    []views.FeaturesPropRow{propFact("hasFilter", "bool", "A kept filter is shown in the status bar"), propFact("filtering", "bool", "The filter bar is being typed in")},
		Surfaces: []views.FeaturesPropRow{propSurface("Title", "string", "Panel title, where a focus hint badge is stamped"), propSurface("Badges", "list", "Marks beside a title, such as the [key] hint")},
		Tokens:   []views.FeaturesPropRow{propToken("key", "Key names in hints"), propToken("title", "Tab and panel titles"), propToken("muted", "System stats and quiet status text")},
	},
	DomainPickersKey: {
		Settings: []string{SetPickerDefaultMode, SetModalEscapeGuard, SetConfirmQuit, SetConfirmDaemonReset, SetAnswerShowQuestion, SetProjectSectionPickerNested},
		Facts:    propFacts(scrollFacts, filterFacts, []views.FeaturesPropRow{propFact("markedCount", "int", "How many picker rows are marked")}),
		Surfaces: []views.FeaturesPropRow{propSurface("items", "prop", "Rows a picker offers"), propSurface("selected", "prop", "The focused picker row"), propSurface("placeholder", "prop", "Text shown while the picker input is empty")},
		Tokens:   []views.FeaturesPropRow{propToken("input.focus", "The picker's input"), propToken("match", "Text the query matched")},
	},
	DomainGuttersKey: {
		Settings: []string{SetFlagsOnLeft, SetFlagsColumn, SetOutlineGutter, SetTreeGuides, SetTreeHLFolds, SetStatusIcons, SetColorByRecency, SetTasksShowNoteIndicators},
		Facts:    propFacts(treeFacts, recordFacts, []views.FeaturesPropRow{propFact("hasNotes", "bool", "The focused row has notes")}),
		Surfaces: []views.FeaturesPropRow{propSurface("gutter", "prop", "Turns the left gutter column of a tree on"), propSurface("guides", "prop", "Box-drawing guide lines of a tree")},
		Tokens:   []views.FeaturesPropRow{propToken("icon.unread", "Unread dot"), propToken("icon.star", "Star mark"), propToken("dim", "Guide lines and quiet marks")},
	},
	DomainThemesKey: {
		Settings: []string{SetTheme, SetDensity, SetBorderStyle, SetCardBorderStyle, SetRowSemanticStyling, SetColorByRecency, SetSearchHighlight},
		Surfaces: []views.FeaturesPropRow{propSurface("themes.json", "file", "User overrides of roles and tones over the chosen preset"), propSurface("Tone", "string", "Semantic tone a node asks for by name")},
		Tokens: []views.FeaturesPropRow{
			propToken("accent", "Semantic role for the main highlight"), propToken("success", "Semantic role for done and healthy"),
			propToken("warning", "Semantic role for waiting and planned"), propToken("error", "Semantic role for failed"), propToken("info", "Semantic role for neutral notes"),
			propToken("focus.border", "Focus contrast: border of the focused panel"), propToken("focus.bg", "Focus contrast: background of the focused row"),
			propToken("unfocused.fg", "Focus contrast: text of unfocused panels"), propToken("badge.accent", "Accent badge"),
		},
	},
}
