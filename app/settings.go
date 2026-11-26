package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/handlers"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/core"
	otea "ofam.dev/ochazuke/tea"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/table"
)

// ConfigController owns user settings, theme overlay, file/URL handlers, and
// form draft persistence.
type ConfigController struct {
	Settings     *config.Settings
	userSettings map[string]any
	themeOverlay []byte
	Handlers     *handlers.Registry
	drafts       *form.DraftStore
}

func newConfigController() *ConfigController {
	return &ConfigController{
		userSettings: map[string]any{},
		drafts:       form.NewDraftStore(),
	}
}

// loadConfig lays the user's JSONC files over the defaults.
func (a *App) loadConfig(dir string) {
	read := func(name string) ([]byte, bool) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			return nil, false
		}
		if err != nil {
			a.warn("%s: %v", name, err)
			return nil, false
		}
		return b, true
	}
	if b, ok := read("settings.json"); ok {
		if s, err := config.LoadSettings(b, DefaultSettings()); err != nil {
			a.warn("settings.json: %v", err)
		} else if user, err := readUserSettings(b); err != nil {
			a.warn("settings.json: %v", err)
		} else {
			a.cfg.Settings, a.cfg.userSettings = s, user
		}
	}
	if b, ok := read("themes.json"); ok {
		if _, err := config.LoadTheme(b, ui.DefaultDarkTheme()); err != nil {
			a.warn("themes.json: %v", err)
		} else {
			a.cfg.themeOverlay = b
		}
	}
	if b, ok := read("bindings.json"); ok {
		user, err := config.LoadBindings(b)
		if err != nil {
			a.warn("bindings.json: %v", err)
			return
		}
		// A row that is not a valid binding is left out, so it can never
		// shadow a key that works. One warning says how many were, and why
		// the first one was: a file written for another version of mtm does
		// not fill the status line.
		bad, first := map[int]bool{}, ""
		for _, d := range config.CheckBindings(Modes(), user, a.bind.specs...) {
			if d.Severity != config.SevError {
				continue
			}
			if len(bad) == 0 {
				first = fmt.Sprintf("row %d: %s", d.Index+1, d.Message)
			}
			bad[d.Index] = true
		}
		var good []core.Binding
		for i, row := range user {
			if !bad[i] {
				good = append(good, row)
			}
		}
		if len(bad) > 0 {
			a.warn("bindings.json: %d of %d rows are not v2 bindings and were left out (%s)", len(bad), len(user), first)
		}
		a.bind.userBindingsRaw = slices.Clone(user)
		a.bind.userBindings = good
		a.bind.bindings = config.MergeBindings(a.baseBindings(), good)
	}
}

// The declared settings: each has a spec, so the Settings overlay (S) is a
// form over them and a change applies at once.
const (
	SetTheme                           = "ui.theme"
	SetDensity                         = "ui.density"
	SetBorderStyle                     = "ui.borderStyle"
	SetShowMetadata                    = "show.metadata"
	SetShowEmpty                       = "show.emptyRelations"
	SetShowContent                     = "show.content"
	SetFlagsColumn                     = "index.flagsColumn"
	SetMatchMode                       = "index.defaultMatchMode"
	SetEditUniversal                   = "edit.showUniversalFields"
	SetEditExtra                       = "edit.showExtraFields"
	SetLaneBar                         = "panes.laneProgressBar"
	SetRailAutoWiden                   = "panes.railAutoWiden"
	SetRailStateColumn                 = "panes.railStateColumn"
	SetShowTOC                         = "panes.showTOC"
	SetPanesGroup                      = "panes.group"
	SetToast                           = "ui.toast"
	SetTreeGuides                      = "ui.treeGuides"
	SetConfirmQuit                     = "confirm.quit"
	SetConfirmDaemonReset              = "confirm.daemonReset"
	SetTablePresets                    = "table.presets"
	SetWorkTasksLayout                 = "work.tasksLayout"
	SetWorkHierarchyLayout             = "work.hierarchyLayout"
	SetRowSemanticStyling              = "ui.rowSemanticStyling"
	SetStatusIcons                     = "ui.statusIcons"
	SetTreeTopLevelGap                 = "ui.treeTopLevelGap"
	SetAutoArchiveExited               = "terminal.autoArchiveOnExit"
	SetScrollOff                       = "ui.scrolloff"
	SetPickerDefaultMode               = "ui.pickerDefaultMode"
	SetViewCycle                       = "ui.viewCycle"
	SetFormReactiveFields              = "edit.reactiveFields"
	SetSessionRecord                   = "session.record"
	SetSessionPromptSaveOnExit         = "session.promptSaveOnExit"
	SetToolsFDBinary                   = "tools.fdBinary"
	SetSearchFDMaxResults              = "search.fdMaxResults"
	SetSearchPanesIncludeTranscripts   = "search.panesIncludeTranscripts"
	SetSearchPanesIncludeArchived      = "search.panesIncludeArchived"
	SetSearchPanesMaxLogKB             = "search.panesMaxLogKB"
	SetSessionUXNotes                  = "session.uxNotes"
	SetPageLockScroll                  = "panes.pageLockScroll"
	SetInteractiveCanvas               = "panes.interactiveCanvas"
	SetTOCLivePreview                  = "panes.tocLivePreview"
	SetEdgeFocusSpill                  = "nav.edgeFocusSpill"
	SetLongJumpPolicy                  = "nav.longJumpPolicy"
	SetScrollFollowTrait               = "nav.scrollFollowTrait"
	SetVerboseHookBar                  = "panes.verboseHookBar"
	SetLaneInTOC                       = "panes.laneInTOC"
	SetCompactTOC                      = "panes.compactTOC"
	SetTOCGrouped                      = "panes.tocGrouped"
	SetModalEscapeGuard                = "modal.escapeGuard"
	SetWrapInputs                      = "ui.wrapInputs"
	SetWrapOutline                     = "ui.wrapOutline"
	SetOutlineEnterNewline             = "outline.enterNewline"
	SetOutlineStartCollapsed           = "outline.startCollapsed"
	SetCardBorderStyle                 = "ui.cardBorderStyle"
	SetRailEnterMode                   = "panes.railEnterMode"
	SetSideTermSplit                   = "panes.sideTermSplit"
	SetInheritProjectTask              = "panes.inheritProjectTask"
	SetFocusRailOnExit                 = "terminal.focusRailOnExit"
	SetFocusRailOnDetach               = "terminal.focusRailOnDetach"
	SetExtensionsShowCatalog           = "extensions.showCatalog"
	SetExtensionsGoPrivate             = "extensions.goPrivate"
	SetFocusHintsStyle                 = "hints.focusStyle"
	SetFocusHintsDrilldown             = "hints.focusDrilldown"
	SetHintsModifierActions            = "hints.modifierActions"
	SetComposerCompact                 = "panes.composerCompact"
	SetComposerWrap                    = "panes.composerWrap"
	SetArchiveAnimation                = "ui.archiveAnimation"
	SetArchiveAnimationMs              = "ui.archiveAnimationMs"
	SetTasksShowNoteIndicators         = "tasks.showNoteIndicators"
	SetTasksInlineNoteThreads          = "tasks.inlineNoteThreads"
	SetTasksLockThreadEdits            = "tasks.lockThreadEdits"
	SetProjectWorkspaceInPanes         = "project.workspaceInPanes"
	SetProjectOutlineAsPage            = "project.outlineAsPage"
	SetProjectNavSections              = "project.navSections"
	SetContextAutoInject               = "context.autoInjectProjectContext"
	SetEnterOpensShow                  = "ui.enterOpensShow"
	SetTreeHLFolds                     = "tree.hlFolds"
	SetProjectsTopNavTab               = "projects.topNavTab.enabled"
	SetProjectsDynamicTabs             = "projects.dynamicTabs.enabled"
	SetProjectsChildPaneView           = "projects.childPaneView.enabled"
	SetProjectsNavTree                 = "projects.navTree"
	SetProjectsNavSurfaceUnread        = "projects.navSurfaceUnread"
	SetOutlineGutter                   = "outline.gutter"
	SetChromeSystemStats               = "ui.chromeSystemStats"
	SetSelfHostedFeaturesEnabled       = "selfHosted.featuresPage.enabled"
	SetSelfHostedFeaturesRepoPath      = "selfHosted.featuresPage.repoPath"
	SetHintsDefaultAction              = "hints.defaultAction"
	SetProjectHomeDashboard            = "project.homeDashboard"
	SetPanesCompactTopArea             = "panes.compactTopArea"
	SetFilterInPlaceTree               = "filter.inPlaceTree"
	SetAttachmentsEnabled              = "input.clipboardAttachments"
	SetAttachmentsDir                  = "attachments.dir"
	SetProjectsNavSurfaceContextUnread = "projects.navSurfaceContextUnread"
	SetProjectsNavUnreadDotOnly        = "projects.navUnreadDotOnly"
	SetProjectsNavLaneProgressBar      = "projects.navLaneProgressBar"
	SetFilterBarBottomLeft             = "ui.filterBarBottomLeft"
	HintsActionCopy                    = "copy"
	HintsActionInsert                  = "insert"
	HintsActionOpen                    = "open"
	HintsActionReveal                  = "reveal"
	HintsActionPick                    = "pick"
	SelfHostedFeaturesAuto             = "auto"
	SelfHostedFeaturesOn               = "on"
	SelfHostedFeaturesOff              = "off"
	FocusHintsInline                   = "inline"
	FocusHintsOverlay                  = "overlay"
	FocusHintsBoth                     = "both"
	RailEnterModeLive                  = "live"
	RailEnterModeTOC                   = "toc"
	PickerDefaultModeNormal            = "normal"
	PickerDefaultModeAllNormal         = "all-normal"
	PickerDefaultModeFilter            = "filter"
	WorkTasksLayoutTree                = "tree"
	WorkTasksLayoutTable               = "table"
	WorkHierarchyLayoutAuto            = "auto"
	WorkHierarchyLayoutTree            = "tree"
	WorkHierarchyLayoutTable           = "table"
	PanesGroupProjectPane              = "project-pane"
	PanesGroupFlat                     = "flat"
	defaultThemeName                   = "ochazuke-dark"
	settingsFileName                   = "settings.json"
	bindingsFileName                   = "bindings.json"
	// setTerminalDaemon is read from settings.json only (it has no row in
	// the settings form): panes run in a daemon when one is reachable.
	setTerminalDaemon = "terminal.daemon"
	// factOpenable is set on a row whose record "o" can open.
	factOpenable      = "openable"
	themesFileName    = "themes.json"
	railRatio         = 0.23
	railRatioWidened  = 0.39
	laneBarHint       = "ctrl+a L"
	legacyMatchModeID = "matchMode"
)

// ThemeNames lists the built-in themes a user can pick.
func ThemeNames() []string {
	var out []string
	for _, t := range ui.ThemePresets() {
		out = append(out, t.Name)
	}
	return out
}

// SettingSpecs returns the declared settings in display order. Their
// defaults are part of DefaultSettings.
func SettingSpecs() []core.ParamSpec {
	enum := func(name, def, desc string, values ...string) core.ParamSpec {
		return core.ParamSpec{Name: name, Type: core.ParamString, Enum: values, Default: def, Description: desc}
	}
	flag := func(name string, def bool, desc string) core.ParamSpec {
		return core.ParamSpec{Name: name, Type: core.ParamBool, Default: def, Description: desc}
	}
	return []core.ParamSpec{
		enum(SetTheme, defaultThemeName, "colour theme", ThemeNames()...),
		enum(SetDensity, ui.DensityBalanced, "rows per form field and card spacing", ui.DensityCompact, ui.DensityBalanced, ui.DensitySpacious),
		enum(SetBorderStyle, ui.BorderRounded, "how panels and cards are framed", ui.BorderRounded, ui.BorderSharp, ui.BorderMinimal, ui.BorderNone),
		flag(SetShowMetadata, true, "Show pages list created, updated and revision"),
		flag(SetShowEmpty, true, "Show pages keep relation sections that hold nothing"),
		flag(SetFlagsColumn, true, "Index tables end with the flags column (unread, star, snooze, archive)"),
		enum(SetMatchMode, string(table.MatchSubstring), "how a new filter matches", string(table.MatchSubstring), string(table.MatchFuzzy), string(table.MatchRegex)),
		flag(SetEditUniversal, true, "Edit forms offer star, archive and snooze"),
		flag(SetEditExtra, true, "Edit forms offer fields the schema does not declare"),
		flag(SetLaneBar, false, "the lane progress bar above a pane's site (opt-in; lane appears in TOC by default)"),
		flag(SetRailAutoWiden, false, "the pane rail widens while it has the focus"),
		flag(SetRailStateColumn, false, "show a separate spelled-out State column in the left pane rail instead of an inline status icon"),
		flag(SetShowTOC, true, "the pane's Pages table of contents rail"),
		enum(SetPanesGroup, PanesGroupProjectPane, "how panes are grouped in the rail", PanesGroupProjectPane, PanesGroupFlat),
		flag(SetToast, true, "show transient toast notifications"),
		flag(SetTreeGuides, true, "draw box-drawing guide lines in tree views"),
		flag(SetRowSemanticStyling, true, "apply semantic row and cell tones from schema rules"),
		flag(SetStatusIcons, true, "show declarative status icons in tables, trees, and pickers"),
		flag(SetTreeTopLevelGap, true, "draw a blank line between top-level items in tree views"),
		flag(SetConfirmQuit, true, "prompt before quitting Matomeru"),
		flag(SetConfirmDaemonReset, true, "prompt before resetting the daemon and dropping all live panes"),
		flag(SetAutoArchiveExited, true, "auto-detach and archive panes when their process exits"),
		{Name: SetScrollOff, Type: core.ParamInt, Default: 5, Description: "context lines kept above and below the cursor when scrolling"},
		enum(SetWorkTasksLayout, WorkTasksLayoutTree, "how tasks are displayed in 2 Work (tree or table)", WorkTasksLayoutTree, WorkTasksLayoutTable),
		enum(SetWorkHierarchyLayout, WorkHierarchyLayoutAuto, "how hierarchical nouns are displayed in 2 Work (auto, tree, or table)", WorkHierarchyLayoutAuto, WorkHierarchyLayoutTree, WorkHierarchyLayoutTable),
		enum(SetPickerDefaultMode, PickerDefaultModeNormal, "initial mode when opening pickers (normal, all-normal, or filter)", PickerDefaultModeNormal, PickerDefaultModeAllNormal, PickerDefaultModeFilter),
		flag(SetViewCycle, true, "cycle focused component views, layouts, density, and saved presets with ( / ) (normal) / alt+v (insert)"),
		flag(SetFormReactiveFields, true, "evaluate reactive VisibleWhen predicates in forms (false shows all fields)"),
		flag(SetSessionRecord, true, "record session events into a bounded in-memory ring buffer"),
		flag(SetSessionPromptSaveOnExit, false, "prompt to save the session recording before quitting"),
		{Name: SetToolsFDBinary, Type: core.ParamString, Default: "", Description: "path or command name for fd/fdfind file search binary (empty auto-detects fd then fdfind)"},
		{Name: SetSearchFDMaxResults, Type: core.ParamInt, Default: defaultFDMaxResults, Description: "default maximum results returned by fd file search (0 = unlimited)"},
		flag(SetSearchPanesIncludeTranscripts, true, "include external agent session transcripts (Claude/Codex/Gemini) in global pane search (F)"),
		flag(SetSearchPanesIncludeArchived, false, "include archived panes by default in global pane search (F)"),
		{Name: SetSearchPanesMaxLogKB, Type: core.ParamInt, Default: defaultPaneSearchMaxLogKB, Description: "maximum tail size in KiB read per session transcript during global pane search"},
		flag(SetSessionUXNotes, true, "enable zero-interruption in-context UX note capture (alt+n) and session note replay"),
		flag(SetPageLockScroll, true, "lock reading and scrolling inside a ui.page until explicit detach (esc/q/ctrl+a d)"),
		flag(SetInteractiveCanvas, true, "make canvas tables, Markdown links, and checkboxes interactive in reading mode"),
		flag(SetTOCLivePreview, true, "live-preview the focused canvas in the right panel while moving through the middle rail"),
		flag(SetEdgeFocusSpill, false, "allow j/k at list or pane edges to silently spill focus to adjacent panes"),
		enum(SetLongJumpPolicy, traits.LongJumpHalfPage, "moving focus to an off-screen item first scrolls half a page toward it (half-page) or reveals it at once (jump)", traits.LongJumpHalfPage, traits.LongJumpDirect),
		flag(SetScrollFollowTrait, true, "scroll areas keep their offset and follow focus with ui.scrolloff rows of padding (off: the focused item is pinned flush to the bottom edge from the top)"),
		flag(SetVerboseHookBar, false, "show verbose tool, subagent, and summary text in the pane hook indicator"),
		flag(SetLaneInTOC, true, "show the pane's active lane and progress counter inside the pane TOC"),
		flag(SetCompactTOC, true, "render pane TOC entries as compact 1-line scannable rows"),
		flag(SetTOCGrouped, true, "group pane TOC entries into collapsible Pinned / Active / Pages sections"),
		enum(SetModalEscapeGuard, traits.EscapeGuardDouble, "guard against accidental esc modal close (double, cooloff, both, off)", traits.EscapeGuardDouble, traits.EscapeGuardCooloff, traits.EscapeGuardBoth, traits.EscapeGuardOff),
		flag(SetWrapInputs, true, "wrap long text in form inputs so overflow stays visible"),
		flag(SetWrapOutline, true, "wrap long titles and multiline items in trees and outline"),
		flag(SetOutlineEnterNewline, true, "pressing enter in inline outline edit commits the row and starts a new sibling below"),
		flag(SetOutlineStartCollapsed, true, "Start outlines and hierarchical trees collapsed to reduce visual noise"),
		enum(SetCardBorderStyle, ui.CardBorderSubtle, "how cards are framed (subtle, rounded, minimal, none)", ui.CardBorderSubtle, ui.CardBorderRounded, ui.CardBorderMinimal, ui.CardBorderNone),
		enum(SetRailEnterMode, RailEnterModeLive, "what enter on a pane in the left rail does (live enters terminal, toc focuses pages)", RailEnterModeLive, RailEnterModeTOC),
		flag(SetSideTermSplit, true, "open ctrl+a t / ctrl+a v as a side-by-side split beside the active pane"),
		flag(SetInheritProjectTask, true, "auto-inherit the active top-level project task when creating records in 1 Panes"),
		flag(SetFocusRailOnExit, true, "when an active terminal or side split exits, detach, move focus to the left rail, and select the next pane"),
		flag(SetFocusRailOnDetach, true, "when detaching from a live terminal (ctrl+a d), move focus back to the left pane rail"),
		flag(SetExtensionsShowCatalog, true, "show uninstalled OFAM catalog tools as available in 4 Extensions"),
		{Name: SetExtensionsGoPrivate, Type: core.ParamString, Default: "ofam.dev", Description: "GOPRIVATE value used when installing OFAM extensions via go install"},
		enum(SetFocusHintsStyle, FocusHintsInline, "how focus jump hints (g f) display labels: inline on screen, overlay picker, or both", FocusHintsInline, FocusHintsOverlay, FocusHintsBoth),
		flag(SetFocusHintsDrilldown, true, "drill into focus hints from outside in (tabs & rails first, then items inside a chosen rail)"),
		flag(SetComposerCompact, true, "use a compact, focused modal size for the prompt composer (false uses the large modal)"),
		flag(SetComposerWrap, true, "soft-wrap long lines inside the prompt composer input"),
		flag(SetArchiveAnimation, true, "animate archived rows with a color-fade dissolve before removing them from lists"),
		{Name: SetArchiveAnimationMs, Type: core.ParamInt, Default: 2500, Description: "duration in milliseconds for the archive dissolve animation"},
		flag(SetTasksShowNoteIndicators, true, "show note-count, unread-agent-note, and attachment indicators on task tree rows"),
		flag(SetTasksInlineNoteThreads, true, "allow expanding note threads inline beneath task rows"),
		flag(SetTasksLockThreadEdits, false, "enforce append-only conversation lock on thread notes and tasks with active note threads"),
		flag(SetProjectWorkspaceInPanes, true, "show the full Project Page workspace with left nav on project headers and P"),
		flag(SetProjectOutlineAsPage, true, "render the Project Page Tasks section as an embedded outline todo page"),
		{Name: SetProjectNavSections, Type: core.ParamString, Default: "", Description: "comma-separated override list of Project Page left-nav sections (blank shows all 14)"},
		flag(SetContextAutoInject, true, "automatically include curated official project context in prompts and agent task dispatch"),
		flag(SetEnterOpensShow, false, "pressing enter on plain lists and tables opens the record's Show page (E always opens Show)"),
		flag(SetTreeHLFolds, true, "use h/l to smart-fold/unfold trees, outlines, and grouped headers (spilling to regions at the edge)"),
		flag(SetHintsModifierActions, true, "allow ctrl (open), Shift (insert into active/main input), and alt (reveal in Finder) modifiers on hint labels"),
		flag(SetProjectsTopNavTab, true, "show permanent 5 Projects tab in the top navigation bar"),
		flag(SetProjectsDynamicTabs, true, "open selected projects as dedicated 6+ tabs in the top navigation bar"),
		flag(SetProjectsChildPaneView, true, "open project panes inline inside a Project Tab's content area"),
		enum(SetSelfHostedFeaturesEnabled, SelfHostedFeaturesAuto, "enable the self-hosted Features page (auto detects local matomeru checkout, on forces visible, off hides)", SelfHostedFeaturesAuto, SelfHostedFeaturesOn, SelfHostedFeaturesOff),
		{Name: SetSelfHostedFeaturesRepoPath, Type: core.ParamString, Default: "", Description: "override path to local matomeru checkout for Features page auto-detection"},
		flag(SetProjectsNavTree, true, "allow expanding Project left-nav sections into interactive inline trees with live right-pane preview"),
		flag(SetOutlineGutter, true, "show a 1-character left gutter column on task outlines for queue, dispatch, and agent status indicators"),
		flag(SetChromeSystemStats, true, "show non-blocking CPU, RAM, and disk stats in the bottom chrome bar"),
		flag(SetShowContent, true, "Show pages render full Markdown body and long-text fields in readable panels"),
		flag(SetProjectsNavSurfaceUnread, true, "surface unread/new items visibly underneath collapsed Project left-nav sections and parents"),
		enum(SetHintsDefaultAction, HintsActionCopy, "default action when activating a lowercase hint label (copy, insert, open, reveal, or pick)", HintsActionCopy, HintsActionInsert, HintsActionOpen, HintsActionReveal, HintsActionPick),
		flag(SetProjectHomeDashboard, true, "show interactive Project Page Home dashboard with decisions, new items, panes, recent prompts, and recent questions"),
		flag(SetPanesCompactTopArea, true, "render a concise, prominent lane progress bar and trim verbose top-area chrome above live panes"),
		flag(SetFilterInPlaceTree, true, "filter task/pane/lane/outline trees in place as a tree instead of swapping 2 Work to a flat table"),
		flag(SetAttachmentsEnabled, true, "paste clipboard images/files as @path attachments on ctrl+v in any text input"),
		{Name: SetAttachmentsDir, Type: core.ParamString, Default: "", Description: "directory for pasted attachments (empty = XDG data dir matomeru/attachments)"},
		flag(SetProjectsNavSurfaceContextUnread, false, "surface unread context items underneath the collapsed Context section in the Project left nav"),
		flag(SetProjectsNavUnreadDotOnly, true, "color only the unread dot (●) on unread Project left-nav rows instead of tinting the whole row"),
		flag(SetProjectsNavLaneProgressBar, true, "show a compact 4-character colored progress bar on lane rows in the Project left nav"),
		flag(SetFilterBarBottomLeft, true, "anchor the active filter input in a fixed bottom-left bar instead of inline in the status breadcrumb"),
	}
}

// DefaultSettings are the settings and their defaults: the declared ones of
// SettingSpecs and the few read only from settings.json.
func DefaultSettings() map[string]any {
	out := map[string]any{
		"startSpace":   SpacePanes,
		"snoozeHours":  24,
		"showArchived": false,
		"mouse":        true,
		"maxFPS":       otea.DefaultMaxFPS,
		// Directories searched for extensions before PATH.
		"extensionDirs": []any{},
	}
	for _, sp := range SettingSpecs() {
		out[sp.Name] = sp.Default
	}
	return out
}

// readUserSettings parses the user's settings file into the keys it sets.
func readUserSettings(b []byte) (map[string]any, error) {
	out := map[string]any{}
	clean := config.StripJSONC(b)
	if len(slices.DeleteFunc(slices.Clone(clean), func(c byte) bool { return c == ' ' || c == '\n' || c == '\t' || c == '\r' })) == 0 {
		return out, nil
	}
	err := json.Unmarshal(clean, &out)
	return out, err
}

// reloadSettings rebuilds Settings from the keys the user set.
func (a *App) reloadSettings() {
	b, err := json.Marshal(a.cfg.userSettings)
	if err == nil {
		var s *config.Settings
		if s, err = config.LoadSettings(b, DefaultSettings()); err == nil {
			a.cfg.Settings = s
		}
	}
	if err != nil {
		a.warn("settings: %v", err)
	}
	a.syncToastSetting()
	a.syncSessionRecorder()
	a.applyRendererSettings()
}

func cardBorderStyleValue(v string) string {
	switch v {
	case ui.CardBorderSubtle:
		return ui.CardBorderSubtle
	case ui.CardBorderRounded:
		return ui.CardBorderRounded
	case ui.CardBorderMinimal:
		return ui.CardBorderMinimal
	case ui.CardBorderNone:
		return ui.CardBorderNone
	default:
		return ui.CardBorderSubtle
	}
}

// theme builds the theme the settings ask for: the named preset, the
// density, border style and card border style the user set over it, then the
// user's themes.json.
func (a *App) theme() ui.Theme {
	theme := ui.DefaultDarkTheme()
	name := a.cfg.Settings.String(SetTheme, defaultThemeName)
	for _, t := range ui.ThemePresets() {
		if t.Name == name {
			theme = t
		}
	}
	if v, ok := a.cfg.userSettings[SetDensity].(string); ok && v != "" {
		theme.Density = v
	}
	if v, ok := a.cfg.userSettings[SetBorderStyle].(string); ok && v != "" {
		theme.BorderStyle = v
	}
	theme.CardBorderStyle = cardBorderStyleValue(a.cfg.Settings.String(SetCardBorderStyle, ui.CardBorderSubtle))
	if v, ok := a.cfg.userSettings[SetCardBorderStyle].(string); ok && v != "" {
		theme.CardBorderStyle = cardBorderStyleValue(v)
	}
	if len(a.cfg.themeOverlay) > 0 {
		if t, err := config.LoadTheme(a.cfg.themeOverlay, theme); err == nil {
			theme = t
		}
	}
	return theme
}

func (a *App) showOptions() views.ShowOptions {
	showContent := a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetShowContent, true)
	if a.nav != nil && a.nav.showCompact {
		return views.ShowOptions{ShowMetadata: false, ShowEmptyRelations: false, ShowContent: showContent}
	}
	return views.ShowOptions{
		ShowMetadata:       a.cfg.Settings.Bool(SetShowMetadata, true),
		ShowEmptyRelations: a.cfg.Settings.Bool(SetShowEmpty, true),
		ShowContent:        showContent,
	}
}

func (a *App) editOptions() views.EditOptions {
	return views.EditOptions{ShowUniversalFields: a.cfg.Settings.Bool(SetEditUniversal, true), ShowExtraFields: a.cfg.Settings.Bool(SetEditExtra, true)}
}

func (a *App) styleOptions() views.StyleOptions {
	if a.cfg.Settings == nil {
		return views.DefaultStyleOptions()
	}
	return views.StyleOptions{
		RowSemanticStyling: a.cfg.Settings.Bool(SetRowSemanticStyling, true),
		StatusIcons:        a.cfg.Settings.Bool(SetStatusIcons, true),
	}
}

func (a *App) hintsDefaultAction() string {
	if a.cfg == nil || a.cfg.Settings == nil {
		return HintsActionCopy
	}
	switch a.cfg.Settings.String(SetHintsDefaultAction, HintsActionCopy) {
	case HintsActionInsert, HintsActionOpen, HintsActionReveal, HintsActionPick:
		return a.cfg.Settings.String(SetHintsDefaultAction, HintsActionCopy)
	default:
		return HintsActionCopy
	}
}

func (a *App) projectHomeDashboardEnabled() bool {
	return a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetProjectHomeDashboard, true)
}

func (a *App) panesCompactTopAreaEnabled() bool {
	return a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetPanesCompactTopArea, true)
}

func (a *App) hierarchyLayout() string {
	if a.cfg.Settings == nil {
		return WorkHierarchyLayoutAuto
	}
	if v, ok := a.cfg.userSettings[SetWorkHierarchyLayout].(string); ok && v != "" {
		switch v {
		case WorkHierarchyLayoutAuto, WorkHierarchyLayoutTree, WorkHierarchyLayoutTable:
			return v
		}
	}
	if v, ok := a.cfg.userSettings[SetWorkTasksLayout].(string); ok && v != "" {
		switch v {
		case WorkTasksLayoutTree:
			return WorkHierarchyLayoutTree
		case WorkTasksLayoutTable:
			return WorkHierarchyLayoutTable
		}
	}
	if v := a.cfg.Settings.String(SetWorkHierarchyLayout, WorkHierarchyLayoutAuto); v != "" {
		return v
	}
	return WorkHierarchyLayoutAuto
}

// matchMode is how a table's filter matches until the owner changes it.
func (a *App) matchMode() table.MatchMode {
	if v, ok := a.cfg.userSettings[legacyMatchModeID].(string); ok && v != "" {
		if _, set := a.cfg.userSettings[SetMatchMode]; !set {
			return table.MatchMode(v)
		}
	}
	return table.MatchMode(a.cfg.Settings.String(SetMatchMode, string(table.MatchSubstring)))
}

// applySettings makes the settings take effect on what is already open: the
// theme of the shell and the view built from them.
func (a *App) applySettings() {
	if a.Shell != nil {
		theme := a.theme()
		theme.Version = a.Shell.Theme.Version + 1 // drops the renderer's caches
		a.Shell.Theme = theme
	}
	a.syncSessionRecorder()
	a.nav.recCacheKeys = map[string]string{}
	a.panes.railCacheKey = ""
	a.viewMemo.tabsKey = ""
	if a.features != nil {
		a.features.invalidateDetection()
	}
	a.viewMemo.gen++
}

// writeConfig writes one of the user's configuration files. Without a
// configuration directory (the smoke run, the tutorial) nothing is written
// and it reports false.
func (a *App) writeConfig(name string, v any) (bool, error) {
	dir := a.opts.ConfigDir
	if dir == "" {
		return false, nil
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(filepath.Join(dir, name), append(b, '\n'), 0o600)
}

// settingValues are the current values of the declared settings, with the
// density and border style the theme really has.
func (a *App) settingValues() map[string]any {
	out := map[string]any{}
	for _, sp := range SettingSpecs() {
		if v, ok := a.cfg.Settings.Raw(sp.Name); ok {
			out[sp.Name] = v
		}
	}
	theme := a.theme()
	out[SetDensity], out[SetBorderStyle] = theme.EffectiveDensity(), theme.EffectiveBorderStyle()
	out[SetCardBorderStyle] = cardBorderStyleValue(theme.CardBorderStyle)
	out[SetMatchMode] = string(a.matchMode())
	return out
}

func (a *App) wrapInputs() bool {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return true
	}
	return a.cfg.Settings.Bool(SetWrapInputs, true)
}

func (a *App) enterOpensShow() bool {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return false
	}
	return a.cfg.Settings.Bool(SetEnterOpensShow, false)
}

func (a *App) treeHLFolds() bool {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return true
	}
	return a.cfg.Settings.Bool(SetTreeHLFolds, true)
}

func (a *App) settingsOpen(s core.State, _ core.Command) core.StepResult {
	fm := form.New("Settings", SettingSpecs(), a.settingValues())
	fm.Wrap = a.wrapInputs()
	fm.SubmitLabel = "change · applied and saved as you go"
	m := modal.NewFormModal(fm, modal.FormOptions{
		ID:            "settings",
		Scope:         "settings",
		DisableDrafts: true,
		DisableSubmit: true,
		OnChange: func(fm *form.Model, field string) modal.Result {
			a.commitSetting(fm, field)
			return modal.Result{Handled: true}
		},
	})
	where := "not saved: this session has no configuration directory"
	if dir := a.opts.ConfigDir; dir != "" {
		where = "saved to " + filepath.Join(dir, settingsFileName)
	}
	a.say("space and h/l change · applied at once · %s", where)
	return core.Next(a.pushModal(s, m))
}

// commitSetting applies and saves the changed field of the Settings form.
func (a *App) commitSetting(fm *form.Model, name string) {
	if fm == nil {
		return
	}
	if name == "" {
		name = fm.ActiveFieldName()
		if name == "" {
			return
		}
	}
	value, has := fm.TypedValues()[name]
	if !has {
		return
	}
	if old, set := a.cfg.Settings.Raw(name); set && old == value && name != SetDensity && name != SetBorderStyle && name != SetCardBorderStyle {
		return
	}
	a.cfg.userSettings[name] = value
	if name == SetTheme {
		// A preset brings its own density and border style.
		delete(a.cfg.userSettings, SetDensity)
		delete(a.cfg.userSettings, SetBorderStyle)
	}
	a.reloadSettings()
	a.applySettings()
	if name == SetTheme {
		theme := a.theme()
		fm.SetValue(SetDensity, theme.EffectiveDensity())
		fm.SetValue(SetBorderStyle, theme.EffectiveBorderStyle())
	}
	if name == SetMatchMode {
		for _, tbl := range a.nav.tables {
			if tbl.Query == "" {
				tbl.MatchMode = a.matchMode()
			}
		}
	}
	if name == SetPanesGroup {
		a.panes.railGroupSet = false
		a.panes.railCustomGroup = ""
		a.rail()
	}
	if (name == SetWorkHierarchyLayout || name == SetWorkTasksLayout) && a.trees != nil {
		clear(a.trees.layoutOverride)
	}
	if name == SetOutlineStartCollapsed && a.trees != nil {
		a.applyStartCollapsedSetting()
	}
	if name == SetFormReactiveFields {
		showAll := !a.cfg.Settings.Bool(SetFormReactiveFields, true)
		fm.ShowAllFields = showAll
		if rf := a.activeRecordForm(); rf != nil {
			rf.Model().ShowAllFields = showAll
		}
		if lf := a.activeLaunchForm(); lf != nil {
			lf.Model().ShowAllFields = showAll
		}
		if ff := a.activeForm(fdSearchModalID); ff != nil {
			ff.Model().ShowAllFields = showAll
		}
	}
	if name == SetWrapInputs {
		wrap := a.wrapInputs()
		fm.Wrap = wrap
		if rf := a.activeRecordForm(); rf != nil {
			rf.Model().Wrap = wrap
		}
		if lf := a.activeLaunchForm(); lf != nil {
			lf.Model().Wrap = wrap
		}
		if af := a.activeAnswerForm(); af != nil {
			af.Model().Wrap = wrap
		}
		if ff := a.activeForm(fdSearchModalID); ff != nil {
			ff.Model().Wrap = wrap
		}
		if uf := a.activeForm(uxNoteModalID); uf != nil {
			uf.Model().Wrap = wrap
		}
	}
	if name == SetModalEscapeGuard {
		a.resetEscapeGuard()
	}
	if name == SetSelfHostedFeaturesEnabled || name == SetSelfHostedFeaturesRepoPath {
		a.featuresCtrl().invalidateDetection()
		a.viewMemo.gen++
	}
	saved, err := a.writeConfig(settingsFileName, a.cfg.userSettings)
	switch {
	case err != nil:
		a.fail("save settings", err)
	case saved:
		a.say("%s = %s · saved", name, data.Text(value))
	default:
		a.say("%s = %s · for this session", name, data.Text(value))
	}
}

// ConfigExport is the configuration in effect: the defaults with the user's
// files laid over them.
type ConfigExport struct {
	ConfigDir    string         `json:"configDir"`
	Settings     map[string]any `json:"settings"`     // every setting and its value
	UserSettings map[string]any `json:"userSettings"` // only what settings.json sets
	Bindings     []core.Binding `json:"bindings"`     // the whole keymap, the user's rows first
	UserBindings []core.Binding `json:"userBindings"` // only the rows of bindings.json
	Warnings     []string       `json:"warnings,omitempty"`
}

// ExportConfig reads the configuration directory and returns what is in
// effect, as indented JSON or, with jsonl, as JSON lines: one {"key","value"}
// object per setting, then one binding per line in keymap order.
func ExportConfig(configDir string, jsonl bool) ([]byte, error) {
	store, err := data.Open(":memory:")
	if err != nil {
		return nil, err
	}
	defer store.Close()
	a, err := New(store, Options{ConfigDir: configDir})
	if err != nil {
		return nil, err
	}
	if jsonl {
		settings, err := config.ExportSettingsJSONL(a.cfg.Settings.Merged())
		if err != nil {
			return nil, err
		}
		rows, err := config.ExportBindingsJSONL(a.bind.bindings)
		if err != nil {
			return nil, err
		}
		return append(settings, rows...), nil
	}
	out := ConfigExport{ConfigDir: configDir, Settings: a.cfg.Settings.Merged(), UserSettings: a.cfg.userSettings, Bindings: a.bind.bindings,
		UserBindings: a.bind.userBindings, Warnings: a.Warnings}
	if out.UserBindings == nil {
		out.UserBindings = []core.Binding{}
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
