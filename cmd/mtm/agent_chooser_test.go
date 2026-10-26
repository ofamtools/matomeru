package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// a on the panes page opens a harness chooser, the default first; Enter
// launches that agent into the pin's project and lands it live here.
func TestPanesAOpensHarnessChooserAndLaunches(t *testing.T) {
	app, f := shellApp(t)
	key(t, app, "a")
	if app.r.TopModal() != agentChooser {
		t.Fatalf("a opened %q", app.r.TopModal())
	}
	if fr := frame(app); !strings.Contains(fr, "Launch an agent in matomeru") || !strings.Contains(fr, defaultHarness) {
		t.Fatalf("chooser frame:\n%s", fr)
	}
	settle(app, key(t, app, "enter"))
	if len(f.launches) != 1 || f.launches[0].Session != "matomeru" || !strings.Contains(strings.Join(f.launches[0].Command, " "), defaultHarness) {
		t.Fatalf("launches %+v", f.launches)
	}
	if app.r.TopModal() != "" || app.attached.ID != f.launches[0].ID {
		t.Fatalf("not landed live: modal %q attached %+v", app.r.TopModal(), app.attached)
	}
}

// Notices are transient: a launch's notice goes with the next key or
// after noticeLinger, so it never piles up over the footer's hints.
func TestNoticesClearOnNextKeyOrAfterLinger(t *testing.T) {
	app, _ := shellApp(t)
	var scheduled []tea.Msg
	app.pv.after = func(_ time.Duration, msg tea.Msg) tea.Cmd {
		scheduled = append(scheduled, msg)
		return nil
	}
	app.m.Notify("Launching claude into matomeru")
	app.Update(nil) // a message after which the notice is shown
	if len(app.m.Notices) != 1 {
		t.Fatalf("notice not shown: %q", app.m.Notices)
	}
	app.Update(keyMsg("j"))
	if len(app.m.Notices) != 0 {
		t.Fatalf("the next key kept the notice: %q", app.m.Notices)
	}
	app.m.Notify("first")
	app.Update(nil)
	app.m.Notify("second")
	app.Update(nil)
	var expiries []noticeExpired
	for _, msg := range scheduled {
		if e, ok := msg.(noticeExpired); ok {
			expiries = append(expiries, e)
		}
	}
	if len(expiries) < 2 {
		t.Fatalf("no linger timers: %+v", scheduled)
	}
	app.Update(expiries[len(expiries)-2]) // a stale timer keeps the newer notice
	if len(app.m.Notices) != 2 {
		t.Fatalf("stale timer cleared: %q", app.m.Notices)
	}
	app.Update(expiries[len(expiries)-1])
	if len(app.m.Notices) != 0 {
		t.Fatalf("linger kept: %q", app.m.Notices)
	}
}
