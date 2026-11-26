package app

import "testing"

// setSettingForTest sets one setting the way settings.json would and reloads.
func setSettingForTest(t *testing.T, a *App, name string, value any) {
	t.Helper()
	a.cfg.userSettings[name] = value
	a.reloadSettings()
}
