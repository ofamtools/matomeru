package main

import (
	"strings"
	"testing"
)

// F7: the detail panel is drawn focused while the box, inside it, has
// the focus.
func TestDetailPanelFocusedWithTheBox(t *testing.T) {
	app, _ := promptApp(t)
	fr := frame(app)
	for _, l := range strings.Split(fr, "\n") {
		if strings.Contains(l, "─ matomeru/claude ") || strings.Contains(l, "━ matomeru/claude ") {
			if !strings.Contains(l, "╭━ matomeru/claude") {
				t.Fatalf("the detail panel looks unfocused:\n%s", fr)
			}
			return
		}
	}
	t.Fatalf("no detail panel title:\n%s", fr)
}
