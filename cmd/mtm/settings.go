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
	return a.r.DeclareSetting(
		ochazuke.Setting{Name: settingEditDebounce, Type: ochazuke.SettingDuration, Default: editorDebounce,
			Description: "Pause in typing before the record editor and quick note write"},
		ochazuke.Setting{Name: settingRefresh, Type: ochazuke.SettingDuration, Default: refreshEvery,
			Description: "How often the Panes list reloads while the page is open"},
		harnessSetting,
		// The live pane's leader never expires: ctrl+a waits for the next
		// key, as tmux's prefix does (ochazuke's default is the key-sequence
		// timeout). Declared before EnsureTerminalLeader, so this default
		// stands; settings.json may still set a timeout.
		ochazuke.Setting{Name: ochazuke.TerminalLeaderTimeoutSetting, Type: ochazuke.SettingDuration, Default: time.Duration(0),
			Description: "How long the terminal leader waits for the next key before it sends the leader alone; 0 waits until a key comes"},
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
