package main

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

// settingPickModal is the in-context settings modal's id (ochazuke).
const settingPickModal = "ochazuke.setting-pick"

// pickedGroups are the in-context settings modal's groups for the focus it
// opened over.
func pickedGroups(t *testing.T, a *App) []ochazuke.SettingPickGroup {
	t.Helper()
	if a.r.TopModal() != settingPickModal {
		t.Fatalf("modal %q, focus %s", a.r.TopModal(), a.r.FocusID())
	}
	origin, _ := a.r.ModalReturn(settingPickModal)
	return a.r.SettingPickGroups(origin)
}

// On a pane row, , opens the in-context settings with the pane's Turns
// settings first (thinking, tool calls).
func TestSettingPickOnAPaneShowsTurnsFirst(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	settle(a, follow(a))
	if n := a.r.Instances[a.r.FocusID()]; n == nil || n.Noun != "pane" {
		t.Fatalf("focus %s is not a pane row", a.r.FocusID())
	}
	press(t, a, ",")
	g := pickedGroups(t, a)
	if g[0].Heading != "pane: Turns" || !slices.Contains(g[0].Settings, "turns.thinking") || !slices.Contains(g[0].Settings, "turns.tools") {
		t.Fatalf("first group %+v", g[0])
	}
	t.Logf("first groups: %+v", g[:4])
	if a.r.FocusID() != "__setting-pick/row/"+g[0].Settings[0] {
		t.Fatalf("focus %s", a.r.FocusID())
	}
}

// On Inbox, the Inbox settings come before the app's.
func TestSettingPickOnInboxShowsInboxBeforeTheApp(t *testing.T) {
	t.Parallel()
	a, _, clock := inboxApp(t)
	seedInbox(t, a, clock)
	toPage(t, a, inboxPage)
	press(t, a, ",")
	var headings []string
	for _, g := range pickedGroups(t, a) {
		headings = append(headings, g.Heading)
	}
	inbox := slices.IndexFunc(headings, func(h string) bool { return strings.HasSuffix(h, ": Inbox") })
	app := slices.Index(headings, "matomeru: General")
	if inbox < 0 || app < 0 || inbox > app {
		t.Fatalf("headings %q", headings)
	}
}
