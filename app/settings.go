package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/core"
	otea "ofam.dev/ochazuke/tea"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/table"
)

// The declared settings: each has a spec, so the Settings overlay (S) is a
// form over them and a change applies at once.
const (
	SetTheme                 = "ui.theme"
	SetDensity               = "ui.density"
	SetBorderStyle           = "ui.borderStyle"
	SetShowMetadata          = "show.metadata"
	SetShowEmpty             = "show.emptyRelations"
	SetFlagsColumn           = "index.flagsColumn"
	SetMatchMode             = "index.defaultMatchMode"
	SetEditUniversal         = "edit.showUniversalFields"
	SetEditExtra             = "edit.showExtraFields"
	SetLaneBar               = "panes.laneProgressBar"
	SetRailAutoWiden         = "panes.railAutoWiden"
	SetShowTOC               = "panes.showTOC"
	SetPanesGroup            = "panes.group"
	SetToast                 = "ui.toast"
	SetTreeGuides            = "ui.treeGuides"
	SetConfirmQuit           = "confirm.quit"
	SetConfirmDaemonReset    = "confirm.daemonReset"
	SetTablePresets          = "table.presets"
	SetWorkTasksLayout       = "work.tasksLayout"
	SetWorkHierarchyLayout   = "work.hierarchyLayout"
	SetRowSemanticStyling    = "ui.rowSemanticStyling"
	SetStatusIcons           = "ui.statusIcons"
	SetTreeTopLevelGap       = "ui.treeTopLevelGap"
	SetAutoArchiveExited     = "terminal.autoArchiveOnExit"
	SetScrollOff             = "ui.scrolloff"
	WorkTasksLayoutTree      = "tree"
	WorkTasksLayoutTable     = "table"
	WorkHierarchyLayoutAuto  = "auto"
	WorkHierarchyLayoutTree  = "tree"
	WorkHierarchyLayoutTable = "table"
	PanesGroupProjectPane    = "project-pane"
	PanesGroupFlat           = "flat"
	defaultThemeName         = "ochazuke-dark"
	settingsFileName         = "settings.json"
	bindingsFileName         = "bindings.json"
	// setTerminalDaemon is read from settings.json only (it has no row in
	// the settings form): panes run in a daemon when one is reachable.
	setTerminalDaemon = "terminal.daemon"
	// factOpenable is set on a row whose record "o" can open.
	factOpenable      = "openable"
	themesFileName    = "themes.json"
	railRatio         = 0.33
	railRatioWidened  = 0.42
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
		flag(SetLaneBar, true, "the lane progress bar above a pane's site"),
		flag(SetRailAutoWiden, false, "the pane rail widens while it has the focus"),
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
	b, err := json.Marshal(a.userSettings)
	if err == nil {
		var s *config.Settings
		if s, err = config.LoadSettings(b, DefaultSettings()); err == nil {
			a.Settings = s
		}
	}
	if err != nil {
		a.warn("settings: %v", err)
	}
	a.syncToastSetting()
}

// theme builds the theme the settings ask for: the named preset, the
// density and border style the user set over it, then the user's themes.json.
func (a *App) theme() ui.Theme {
	theme := ui.DefaultDarkTheme()
	name := a.Settings.String(SetTheme, defaultThemeName)
	for _, t := range ui.ThemePresets() {
		if t.Name == name {
			theme = t
		}
	}
	if v, ok := a.userSettings[SetDensity].(string); ok && v != "" {
		theme.Density = v
	}
	if v, ok := a.userSettings[SetBorderStyle].(string); ok && v != "" {
		theme.BorderStyle = v
	}
	if len(a.themeOverlay) > 0 {
		if t, err := config.LoadTheme(a.themeOverlay, theme); err == nil {
			theme = t
		}
	}
	return theme
}

func (a *App) showOptions() views.ShowOptions {
	return views.ShowOptions{ShowMetadata: a.Settings.Bool(SetShowMetadata, true), ShowEmptyRelations: a.Settings.Bool(SetShowEmpty, true)}
}

func (a *App) editOptions() views.EditOptions {
	return views.EditOptions{ShowUniversalFields: a.Settings.Bool(SetEditUniversal, true), ShowExtraFields: a.Settings.Bool(SetEditExtra, true)}
}

func (a *App) styleOptions() views.StyleOptions {
	if a.Settings == nil {
		return views.DefaultStyleOptions()
	}
	return views.StyleOptions{
		RowSemanticStyling: a.Settings.Bool(SetRowSemanticStyling, true),
		StatusIcons:        a.Settings.Bool(SetStatusIcons, true),
	}
}

func (a *App) hierarchyLayout() string {
	if a.Settings == nil {
		return WorkHierarchyLayoutAuto
	}
	if v, ok := a.userSettings[SetWorkHierarchyLayout].(string); ok && v != "" {
		switch v {
		case WorkHierarchyLayoutAuto, WorkHierarchyLayoutTree, WorkHierarchyLayoutTable:
			return v
		}
	}
	if v, ok := a.userSettings[SetWorkTasksLayout].(string); ok && v != "" {
		switch v {
		case WorkTasksLayoutTree:
			return WorkHierarchyLayoutTree
		case WorkTasksLayoutTable:
			return WorkHierarchyLayoutTable
		}
	}
	if v := a.Settings.String(SetWorkHierarchyLayout, WorkHierarchyLayoutAuto); v != "" {
		return v
	}
	return WorkHierarchyLayoutAuto
}

// matchMode is how a table's filter matches until the owner changes it.
func (a *App) matchMode() table.MatchMode {
	if v, ok := a.userSettings[legacyMatchModeID].(string); ok && v != "" {
		if _, set := a.userSettings[SetMatchMode]; !set {
			return table.MatchMode(v)
		}
	}
	return table.MatchMode(a.Settings.String(SetMatchMode, string(table.MatchSubstring)))
}

// applySettings makes the settings take effect on what is already open: the
// theme of the shell and the view built from them.
func (a *App) applySettings() {
	if a.Shell != nil {
		theme := a.theme()
		theme.Version = a.Shell.Theme.Version + 1 // drops the renderer's caches
		a.Shell.Theme = theme
	}
	a.recCacheKeys = map[string]string{}
	a.railCacheKey = ""
	a.gen++
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
		if v, ok := a.Settings.Raw(sp.Name); ok {
			out[sp.Name] = v
		}
	}
	theme := a.theme()
	out[SetDensity], out[SetBorderStyle] = theme.EffectiveDensity(), theme.EffectiveBorderStyle()
	out[SetMatchMode] = string(a.matchMode())
	return out
}

func (a *App) settingsOpen(s core.State, _ core.Command) core.StepResult {
	fm := form.New("Settings", SettingSpecs(), a.settingValues())
	fm.SubmitLabel = "change · applied and saved as you go"
	m := modal.NewFormModal(fm, modal.FormOptions{
		ID:            modalSettings,
		Scope:         modalSettings,
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
		if fm.ActiveIdx >= len(fm.Specs) {
			return
		}
		name = fm.Specs[fm.ActiveIdx].Name
	}
	value, has := fm.TypedValues()[name]
	if !has {
		return
	}
	if old, set := a.Settings.Raw(name); set && old == value && name != SetDensity && name != SetBorderStyle {
		return
	}
	a.userSettings[name] = value
	if name == SetTheme {
		// A preset brings its own density and border style.
		delete(a.userSettings, SetDensity)
		delete(a.userSettings, SetBorderStyle)
	}
	a.reloadSettings()
	a.applySettings()
	if name == SetTheme {
		theme := a.theme()
		fm.SetValue(SetDensity, theme.EffectiveDensity())
		fm.SetValue(SetBorderStyle, theme.EffectiveBorderStyle())
	}
	if name == SetMatchMode {
		for _, tbl := range a.tables {
			if tbl.Query == "" {
				tbl.MatchMode = a.matchMode()
			}
		}
	}
	if name == SetPanesGroup {
		a.railGroupSet = false
		a.railCustomGroup = ""
		a.rail()
	}
	if (name == SetWorkHierarchyLayout || name == SetWorkTasksLayout) && a.trees != nil {
		clear(a.trees.layoutOverride)
	}
	saved, err := a.writeConfig(settingsFileName, a.userSettings)
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
		settings, err := config.ExportSettingsJSONL(a.Settings.Merged())
		if err != nil {
			return nil, err
		}
		rows, err := config.ExportBindingsJSONL(a.bindings)
		if err != nil {
			return nil, err
		}
		return append(settings, rows...), nil
	}
	out := ConfigExport{ConfigDir: configDir, Settings: a.Settings.Merged(), UserSettings: a.userSettings, Bindings: a.bindings,
		UserBindings: a.userBindings, Warnings: a.Warnings}
	if out.UserBindings == nil {
		out.UserBindings = []core.Binding{}
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
