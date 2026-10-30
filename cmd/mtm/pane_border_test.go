package main

import (
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
)

// A terminal pane's border takes its state tone: every state under the
// default panes.status-border all, only needs input and errored under
// alert, none under off; a shell has none.
func TestPaneBorderToneByState(t *testing.T) {
	app := open(t)
	agent := func(state string) pane { return pane{Harness: "claude", Pane: daemon.Pane{AgentState: state}} }
	cases := []struct {
		name  string
		p     pane
		tone  string
		alert bool
	}{
		{"working", agent("working"), ochazuke.ToneWorking, false},
		{"needs input", agent("needs_input"), ochazuke.ToneNeedsInput, true},
		{"done", agent("done"), ochazuke.ToneDone, false},
		{"errored", agent("errored"), ochazuke.ToneErrored, true},
		{"idle", agent("idle"), ochazuke.ToneIdle, false},
		{"exited", pane{Harness: "claude", Exited: true, Pane: daemon.Pane{AgentState: "idle"}}, ochazuke.ToneExited, false},
		{"shell", pane{}, "", false},
	}
	set := func(v string) {
		t.Helper()
		if err := app.r.Settings().Set(settingPanesStatusBorder, v); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range cases {
		set(statusBorderAll)
		if got := app.paneBorderTone(c.p); got != c.tone {
			t.Errorf("%s, all: tone %q, want %q", c.name, got, c.tone)
		}
		set(statusBorderAlert)
		want := ""
		if c.alert {
			want = c.tone
		}
		if got := app.paneBorderTone(c.p); got != want {
			t.Errorf("%s, alert: tone %q, want %q", c.name, got, want)
		}
		set(statusBorderOff)
		if got := app.paneBorderTone(c.p); got != "" {
			t.Errorf("%s, off: tone %q, want none", c.name, got)
		}
	}
}

// The detail panel draws the selected pane's tone on its border: a
// working agent's frame differs from the same frame under off, and alert
// (working is no alert) draws it as off does.
func TestPaneBorderToneIsDrawn(t *testing.T) {
	app, f := panesApp(t)
	for i := range f.panes {
		f.panes[i].Harness, f.panes[i].AgentState = "claude", "working"
	}
	reload(t, app)
	frame := func(v string) string {
		t.Helper()
		if err := app.r.Settings().Set(settingPanesStatusBorder, v); err != nil {
			t.Fatal(err)
		}
		return rawFrame(app)
	}
	all, alert, off := frame(statusBorderAll), frame(statusBorderAlert), frame(statusBorderOff)
	if all == off {
		t.Fatalf("panes.status-border all draws the working pane's border as off does")
	}
	if alert != off {
		t.Fatalf("panes.status-border alert toned a working pane's border")
	}
}
