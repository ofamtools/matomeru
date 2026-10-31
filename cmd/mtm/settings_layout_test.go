package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// The Settings page's tabs and sections (settings-layout lane D): mtm's
// tab lists its sections titled and in page order; each configured
// schedule's two settings sit under a heading of its name in Schedules;
// a mounted extension's scope setting is its own section of the
// Extensions tab, which draws between mtm's tab and ochazuke's.
func TestSettingsSectionsAndOwners(t *testing.T) {
	t.Parallel()
	a, _, _ := scopeApp(t)
	s := a.r.Settings()
	sections, err := s.Sections(ochazuke.OwnerApp)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, sec := range sections {
		titles = append(titles, sec.Title)
	}
	want := []string{"General", "Work", "Tasks", "Projects", "Agents", "Panes", "Prompt", "Turns", "Agent", "Notes", "Inbox", "Schedules", "Status bar", "Pin"}
	if !slices.Equal(titles, want) {
		t.Fatalf("app sections %q, want %q", titles, want)
	}
	for _, name := range []string{"schedule.gh-signals.enabled", "schedule.gh-signals.interval"} {
		one, err := s.Get(name)
		if err != nil {
			t.Fatal(err)
		}
		if one.Section != scheduleSection || one.Group != "gh-signals" || s.OwnerOf(name) != ochazuke.OwnerApp {
			t.Errorf("%s: section %q group %q owner %q", name, one.Section, one.Group, s.OwnerOf(name))
		}
	}
	if got := s.OwnerOf(extScopeSetting("md")); got != extensionsOwner {
		t.Fatalf("ext.md.scope owner %q, want %s", got, extensionsOwner)
	}
	ext, err := s.Sections(extensionsOwner)
	if err != nil {
		t.Fatal(err)
	}
	// General (extensions.noun-names), then a section per extension.
	if len(ext) != 2 || ext[0].ID != ochazuke.SettingsGeneralSection || !slices.Contains(ext[0].Settings, settingExtNounNames) ||
		ext[1].ID != "md" || !slices.Contains(ext[1].Settings, extScopeSetting("md")) {
		t.Fatalf("extensions sections %+v", ext)
	}
	var owners []string
	for _, o := range s.Owners() {
		owners = append(owners, o.ID)
	}
	if !slices.Equal(owners, []string{ochazuke.OwnerApp, extensionsOwner, ochazuke.OwnerFramework}) {
		t.Fatalf("owners %q", owners)
	}
}

// The page draws the three tabs and mtm's sections.
func TestSettingsPageShowsTheTabsAndSections(t *testing.T) {
	t.Parallel()
	a, _, _ := scopeApp(t)
	executeA(t, a, "ochazuke.settings", "edit", "", nil)
	fr := frame(a)
	for _, want := range []string{"matomeru", "Extensions", "ochazuke", "General", "Panes", "Schedules", "Status bar"} {
		if !strings.Contains(fr, want) {
			t.Fatalf("the Settings page lacks %q:\n%s", want, fr)
		}
	}
}
