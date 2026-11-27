package app

import (
	"encoding/json"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/table"
)

func (a *App) showOptions() views.ShowOptions {
	showContent := a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetShowContent, true)
	layout := views.ShowLayoutLedger
	hideEmpty := true
	if a.cfg != nil && a.cfg.Settings != nil {
		layout = a.cfg.Settings.String(SetShowLayout, views.ShowLayoutLedger)
		hideEmpty = a.cfg.Settings.Bool(SetShowHideEmptyRelations, true)
	}
	chips := a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetShowTitleAttachmentChips, true)
	if a.nav != nil && a.nav.showCompact {
		return views.ShowOptions{
			TitleAttachmentChips:     chips,
			DeclarativePresentations: a.declarativePresentations(),

			Layout:             layout,
			HideEmptyRelations: hideEmpty,
			GroupLabelsOnce:    true,
			SectionTallies:     true,
			ShowMetadata:       false,
			ShowEmptyRelations: false,
			ShowContent:        showContent,
		}
	}
	return views.ShowOptions{
		TitleAttachmentChips:     chips,
		DeclarativePresentations: a.declarativePresentations(),

		Layout:             layout,
		HideEmptyRelations: hideEmpty,
		GroupLabelsOnce:    true,
		SectionTallies:     true,
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
		FlagsOnLeft:        a.cfg.Settings.Bool(SetFlagsOnLeft, true),
		ColorByRecency:     a.cfg.Settings.String(SetColorByRecency, views.RecencyCell),
	}
}

func (a *App) topChromeRedesignEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetTopChromeRedesign, true)
}

func (a *App) hintsNounFirstEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetHintsNounFirst, true)
}

func (a *App) stickyTreeBreadcrumbsEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetStickyTreeBreadcrumbs, true)
}

func (a *App) outlineRescopingEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetOutlineRescoping, true)
}

func (a *App) extractCodeBlocksEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetContextExtractCodeBlocks, true)
}

func (a *App) inputReadlinePriorityEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetInputReadlinePriority, true)
}

func (a *App) inputFilterScrollMotionsEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetInputFilterScrollMotions, true)
}

func (a *App) splitResizeEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetSplitResize, true)
}

func (a *App) projectNavCompactEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetProjectNavCompact, true)
}

func (a *App) projectNavPriorityOrderEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetProjectNavPriorityOrder, true)
}

func (a *App) repoAutoHarvestPanesEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetRepoAutoHarvestPanes, true)
}

func (a *App) richAddToQueueEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetRichAddToQueue, true)
}

func (a *App) detachFocusTarget() string {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return DetachFocusLeft
	}
	// Honor legacy terminal.focusRailOnDetach=false if user explicitly turned it
	// off and did not override terminal.detachFocusTarget.
	if raw, set := a.cfg.userSettings[SetDetachFocusTarget]; set {
		if s, ok := raw.(string); ok && s != "" {
			return s
		}
	}
	if raw, set := a.cfg.userSettings[SetFocusRailOnDetach]; set {
		if b, ok := raw.(bool); ok && !b {
			return DetachFocusStay
		}
	}
	return a.cfg.Settings.String(SetDetachFocusTarget, DetachFocusLeft)
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

func (a *App) projectHomeQueueProgressEnabled() bool {
	return a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetProjectHomeQueueProgress, true)
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

func (a *App) featuresCaptureDockedEnabled() bool {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return false
	}
	return a.cfg.Settings.Bool(SetFeaturesCaptureDocked, false)
}

func (a *App) threadsIndexShowEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetThreadsIndexShow, true)
}

func (a *App) treeHLFolds() bool {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return true
	}
	return a.cfg.Settings.Bool(SetTreeHLFolds, true)
}

func (a *App) yankDefaultFormat() string {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return YankFormatRef
	}
	if a.cfg.Settings.String(SetYankDefaultFormat, YankFormatRef) == YankFormatTitle {
		return YankFormatTitle
	}
	return YankFormatRef
}

func (a *App) pasteContextualEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetPasteContextual, true)
}

func (a *App) remindersTopNavTabEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetRemindersTopNavTab, true)
}

func (a *App) reminderToastsEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetReminderToasts, true)
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

// projectNavSectionMaxItems is the number of rows a Project left-nav section
// shows before it scrolls inside itself; 0 means unlimited.
func (a *App) projectNavSectionMaxItems() int {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return 7
	}
	return max(0, a.cfg.Settings.Int(SetProjectsNavSectionMaxItems, 7))
}

func (a *App) showLegacyPanesWorkPages() bool {
	return a != nil && a.cfg != nil && a.cfg.Settings != nil && a.cfg.Settings.Bool(SetShowLegacyPanesWorkPages, false)
}
