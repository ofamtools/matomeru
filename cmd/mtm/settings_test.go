package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/unixsuperhero/ochazuke"
)

// mtm's ochazuke config lives in mtm's config directory (here beside the
// test database), never the owner's, and its history beside the database.
func TestTheConfigIsMatomerusBesideTheData(t *testing.T) {
	a := open(t)
	c := a.r.Config()
	dir := filepath.Dir(a.dataPath)
	if c.App != "matomeru" || c.Directory() != dir || c.StateDirectory() != dir {
		t.Fatalf("app %q dir %q state %q, want matomeru %q", c.App, c.Directory(), c.StateDirectory(), dir)
	}
	if got := c.Path(ochazuke.SettingsFile); got != filepath.Join(dir, ochazuke.SettingsFile) {
		t.Fatalf("settings file %q", got)
	}
}

// The declared settings default to the constants they replace and read
// settings.json; a value that is not positive falls back.
func TestSettingsDefaultToTheOldConstantsAndReadTheFile(t *testing.T) {
	a := open(t)
	if a.editDebounce() != editorDebounce || a.refreshInterval() != refreshEvery || a.launchHarness() != defaultHarness {
		t.Fatalf("defaults %v %v %q", a.editDebounce(), a.refreshInterval(), a.launchHarness())
	}
	mustCLI(t, a, "settings", "set", settingEditDebounce, "1s")
	mustCLI(t, a, "settings", "set", settingRefresh, "10s")
	mustCLI(t, a, "settings", "set", settingDefaultHarness, "codex")
	if a.editDebounce() != time.Second || a.refreshInterval() != 10*time.Second || a.launchHarness() != "codex" {
		t.Fatalf("set %v %v %q", a.editDebounce(), a.refreshInterval(), a.launchHarness())
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(a.dataPath), ochazuke.SettingsFile)); err != nil {
		t.Fatalf("settings.json not written: %v", err)
	}
	if _, _, err := cli(a, "", "settings", "set", settingDefaultHarness, "nope"); err == nil {
		t.Fatal("an unknown harness was accepted")
	}
	mustCLI(t, a, "settings", "set", settingRefresh, "0s")
	if a.refreshInterval() != refreshEvery {
		t.Fatalf("0s refresh %v", a.refreshInterval())
	}
}

// A launch on a project with no harness of its own starts the
// default-harness setting's.
func TestALaunchStartsTheDefaultHarnessSetting(t *testing.T) {
	app := open(t)
	if err := app.r.Settings().Set(settingDefaultHarness, "omp"); err != nil {
		t.Fatal(err)
	}
	f := &fakeDaemon{panes: app.panesd.(*fakePanes)}
	app.launcher = f
	if err := app.r.OpenPage("work"); err != nil {
		t.Fatal(err)
	}
	if err := app.r.SetFocus("matomeru"); err != nil {
		t.Fatal(err)
	}
	settle(app, launchKey(t, app))
	if len(f.launches) != 1 || f.launches[0].Name != "omp" {
		t.Fatalf("launches %+v", f.launches)
	}
}

// , opens the framework's Settings page from a page, esc goes back to the
// page it was opened from, and , does nothing inside a modal.
func TestCommaOpensTheSettingsEditor(t *testing.T) {
	app := open(t)
	from := app.r.Page
	press(t, app, ",")
	if app.r.Page != ochazuke.SettingsPageID || app.r.TopModal() != "" {
		t.Fatalf("page %q, top modal %q; want the Settings page", app.r.Page, app.r.TopModal())
	}
	press(t, app, "esc")
	if app.r.Page != from {
		t.Fatalf("esc left page %q, want %q", app.r.Page, from)
	}
	press(t, app, "?")
	top := app.r.TopModal()
	press(t, app, ",")
	if app.r.TopModal() != top {
		t.Fatalf(", over %s opened %q", top, app.r.TopModal())
	}
}
