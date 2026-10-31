package main

import (
	"strings"
	"testing"
)

// F3: the empty box in normal mode says how to write, in the box and in
// the footer.
func TestBoxInNormalModeSaysHowToWrite(t *testing.T) {
	t.Parallel()
	app, _, _ := claudeTurnsApp(t)
	boxNormal(t, app)
	fr := frame(app)
	if !strings.Contains(fr, "› i to write") {
		t.Fatalf("no cue in the box:\n%s", fr)
	}
	if foot := footerLine(app); !strings.Contains(foot, "i write") {
		t.Fatalf("footer %q", foot)
	}
	// i still positions the cursor as the editor's i does.
	press(t, app, "i")
	if app.r.BaseMode() != "insert" || app.r.Focus != promptID {
		t.Fatalf("i in the box: %s", where(app))
	}
	typeText(t, app, "ab")
	press(t, app, "esc", "i")
	typeText(t, app, "X")
	if got := string(app.pb.input.Text); got != "aXb" {
		t.Fatalf("i inserts before the cursor: %q", got)
	}
}
