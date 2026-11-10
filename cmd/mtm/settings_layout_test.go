package main

import (
	"strings"
	"testing"
)

// The page draws the three tabs and mtm's sections.
func TestSettingsPageShowsTheTabsAndSections(t *testing.T) {
	t.Parallel()
	a, _, _ := scopeApp(t)
	executeA(t, a, "ochazuke.setting", "open", "", nil)
	a.m.Width, a.m.Height = 120, 40 // every section row on screen: All comes first
	fr := plainView(a)
	for _, want := range []string{"matomeru", "Extensions", "ochazuke", "All", "General", "Panes", "Schedules", "Status bar"} {
		if !strings.Contains(fr, want) {
			t.Fatalf("the Settings page lacks %q:\n%s", want, fr)
		}
	}
}
