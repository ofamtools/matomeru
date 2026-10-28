package main

import (
	"regexp"
	"strings"
	"testing"
)

// The framework's header lists every page tab (Header.Tabs from the page
// list) and marks the active one with its ActiveTab style, "› Title".
func TestTheHeaderListsEveryPageTabAndMarksTheActiveOne(t *testing.T) {
	app := openWith(t, Options{Data: seeded(t)}) // mtm opens on Panes, the first tab
	mono(t, app)
	pages := []struct{ id, title string }{{panesPage, panesTitle}, {"work", "Work"}, {reviewsPage, "Review"}}
	for i, want := range pages {
		if i > 0 {
			press(t, app, "ctrl+j")
		}
		if app.r.Page != want.id {
			t.Fatalf("page = %s, want %s", app.r.Page, want.id)
		}
		tabs := strings.Split(monoFrame(t, app), "\n")[1]
		for _, p := range pages {
			marked := strings.Contains(tabs, "› "+p.title)
			if !strings.Contains(tabs, p.title) || marked != (p.id == want.id) {
				t.Fatalf("on %s the tab row is %q: %s listed %v, marked %v", want.id, tabs, p.title, strings.Contains(tabs, p.title), marked)
			}
		}
		// "N›" at the row's end is the overflow mark (tabs past the
		// width), not a marked tab.
		if n := len(regexp.MustCompile(`(^|\s)› `).FindAllString(tabs, -1)); n != 1 {
			t.Fatalf("on %s the tab row marks %d tabs: %q", want.id, n, tabs)
		}
	}
}
