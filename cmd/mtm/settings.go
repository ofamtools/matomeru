package main

import (
	"path/filepath"
	"slices"
	"time"

	"github.com/unixsuperhero/ochazuke"
)

// mtm's user configuration rides ochazuke's: bindings.json, settings.json
// and themes.json sit in mtm's config directory beside harnesses.json and
// dirs.json (Options.ConfigDir), and the action history in the state
// directory. A database other than the default keeps both beside it, as
// it already keeps its config, so tests, --smoke and --data copies never
// touch the owner's files.

// The settings mtm declares; settings.json holds the ones the user set.
const (
	settingEditDebounce   = "edit-debounce"
	settingRefresh        = "refresh-interval"
	settingDefaultHarness = "default-harness"
	settingSideLayout     = "side-layout"
)

// configure names mtm's config before the Model starts (Model.Init loads
// the user bindings and settings from it).
func (a *App) configure(opts Options, configDir string) {
	c := a.r.Config()
	c.App, c.Dir = "matomeru", configDir
	if opts.Data != "" && !samePath(opts.Data, DefaultData()) {
		c.StateDir = filepath.Dir(opts.Data)
	}
}

// declareSettings declares mtm's settings: only knobs that were constants.
func (a *App) declareSettings() error {
	harnessSetting := ochazuke.Setting{Name: settingDefaultHarness, Type: ochazuke.SettingString, Default: defaultHarness,
		Description: "Harness a launch starts when the project names none"}
	if names := a.harnesses.Names(); slices.Contains(names, defaultHarness) {
		harnessSetting.Enum = names
	}
	settings := []ochazuke.Setting{
		ochazuke.Setting{Name: settingEditDebounce, Type: ochazuke.SettingDuration, Default: editorDebounce,
			Description: "Pause in typing before the record editor and quick note write"},
		ochazuke.Setting{Name: settingRefresh, Type: ochazuke.SettingDuration, Default: refreshEvery,
			Description: "How often the Panes list reloads while the page is open"},
		harnessSetting,
		ochazuke.Setting{Name: settingNotesTitleField, Type: ochazuke.SettingBoolean, Default: true,
			Description: "Show the title field above the body in the note editor (a note's title is optional; empty, its first line stands in); the quick note is body-only either way"},
		directoryChooserSetting,
		paneGroupSetting,
		afterSubmitSetting,
		afterSubmitModeSetting,
		promptTerminalsSetting,
		promptScreenSetting,
		draftPerTargetSetting,
		extNounNamesSetting,
		ochazuke.Setting{Name: settingSideLayout, Type: ochazuke.SettingString, Default: sideAuto, Enum: []string{sideAuto, sideBeside, sideBelow},
			Description: "Where a live pane's side vim or terminal sits: auto (beside from a 161-column detail, else below), beside or below; leader R flips it"},
		// The live pane's leader never expires: ctrl+a waits for the next
		// key, as tmux's prefix does (ochazuke's default is the key-sequence
		// timeout). Declared before EnsureTerminalLeader, so this default
		// stands; settings.json may still set a timeout.
		ochazuke.Setting{Name: settingTurnsStyle, Type: ochazuke.SettingString, Default: turnsStyleCards, Enum: []string{turnsStyleCards, turnsStyleTree},
			Description: "How the Turns tab draws a pane's turns: cards (a box per turn that follows the newest, like a live terminal) or tree (the accordion)"},
		ochazuke.Setting{Name: settingTurnsThinking, Type: ochazuke.SettingBoolean, Default: true,
			Description: "Show the agent's thinking inside an opened turn of the Turns tab"},
		ochazuke.Setting{Name: settingTurnsThinkingOpen, Type: ochazuke.SettingBoolean, Default: false,
			Description: "Start the thinking open when a turn is opened (else folded; l opens it)"},
		ochazuke.Setting{Name: settingTurnsTools, Type: ochazuke.SettingBoolean, Default: true,
			Description: "Show the agent's tool calls and their results inside an opened turn of the Turns tab"},
		ochazuke.Setting{Name: settingTurnsToolsOpen, Type: ochazuke.SettingBoolean, Default: false,
			Description: "Start each tool call open when a turn is opened (else folded; l opens it)"},
	}
	settings = append(settings, turnStatusSettings()...)
	settings = append(settings, pinColorsSettings()...)
	settings = append(settings, paneBorderSettings()...)
	settings = append(settings, agentViewSettings()...)
	settings = append(settings, paneSectionSettings()...)
	settings = append(settings, panesPanelSettings()...)
	settings = append(settings, a.signalSettings()...)
	settings = append(settings, commandSettings()...)
	settings = append(settings, a.scheduleSettings()...)
	if err := a.declareSettingLayout(); err != nil {
		return err
	}
	return a.r.DeclareSetting(settings...)
}

// extensionsOwner is the Settings page's Extensions tab (settings-layout
// decision 9): one section per mounted extension (its scope setting) and
// the status bar segments the extensions add.
const extensionsOwner = "extensions"

// declareSettingLayout declares the Settings page's tabs and sections
// beside ochazuke's: the Extensions owner (after the app's tab, before
// ochazuke's) and mtm's own sections, titled and in page order. A
// setting's section is its row's Section, else the name before its first
// dot (panes.layout is in panes); the schedule rows name theirs.
func (a *App) declareSettingLayout() error {
	if err := a.r.DeclareSettingOwner(ochazuke.SettingOwner{ID: extensionsOwner, Title: "Extensions", Order: 50,
		Description: "The mounted extensions: a section each, and the status segments they add"}); err != nil {
		return err
	}
	app := func(id, title, desc string) ochazuke.SettingSection {
		return ochazuke.SettingSection{Owner: ochazuke.OwnerApp, ID: id, Title: title, Description: desc}
	}
	return a.r.DeclareSettingSection(
		app(ochazuke.SettingsGeneralSection, "General", "Editing, refresh, the default harness, the directory chooser and the side pane"),
		app("panes", "Panes", "The Panes page: grouping, its sections and panels, which states count as working, waiting or errored, and which pane borders take their state colour"),
		app("prompt", "Prompt", "The prompt box: where it shows and what follows a submit"),
		app("turns", "Turns", "A pane's Turns tab: cards or tree, thinking and tool calls"),
		app("agent", "Agent", "The agent view of a pane"),
		app("notes", "Notes", "The note editor"),
		app("inbox", "Inbox", "Which pane signals and terminal commands make inbox items"),
		app("schedules", "Schedules", "Missed runs, and each configured schedule's switch and interval"),
		app("status", "Status bar", "mtm's status segments in the header rule: cpu, ram and disk"),
		app("pin", "Pin", "How the pin state (pinned, show all, no pin) colours the theme"),
		ochazuke.SettingSection{Owner: extensionsOwner, ID: ochazuke.SettingsGeneralSection, Title: "General", Description: "How every extension mounts: the names of its nouns"},
		ochazuke.SettingSection{Owner: extensionsOwner, ID: "status", Title: "Status bar", Description: "The status segments the extensions add"},
	)
}

// editDebounce is the edit-debounce setting, editorDebounce when unset
// or not positive.
func (a *App) editDebounce() time.Duration {
	return positive(a.r.Settings().Duration(settingEditDebounce), editorDebounce)
}

// refreshInterval is the refresh-interval setting, refreshEvery when
// unset or not positive.
func (a *App) refreshInterval() time.Duration {
	return positive(a.r.Settings().Duration(settingRefresh), refreshEvery)
}

// launchHarness is the default-harness setting, defaultHarness when unset.
func (a *App) launchHarness() string {
	if name := a.r.Settings().String(settingDefaultHarness); name != "" {
		return name
	}
	return defaultHarness
}

func positive(d, fallback time.Duration) time.Duration {
	if d <= 0 {
		return fallback
	}
	return d
}
