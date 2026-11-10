package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"ofam.dev/ochazuke"
)

// hintKeys is the footer's hint line for the focus as "key noun.verb"
// pairs, in the order the footer shows them. The footer carries no
// descriptions: every hint is "key noun.verb", or "key hint" for a row
// with a Hint (the leader's "ctrl+a leader: …", "] next tab").
func hintKeys(a *App) []string {
	var out []string
	for _, h := range strings.Split(ochazuke.BindingHints(a.r, a.r.FocusID()), " · ") {
		if strings.HasPrefix(h, "ctrl+a leader:") {
			h = "ctrl+a leader"
		}
		if len(strings.Fields(h)) < 2 {
			panic("hint is not key noun.verb or key hint: " + h)
		}
		out = append(out, h)
	}
	return out
}

// footerLabels fails when two footer hints at the focus read the same
// after their key: the same operation under two keys must say what each
// key does (a Hint), not repeat noun.verb.
func footerLabels(t *testing.T, a *App) {
	t.Helper()
	seen := map[string]string{}
	for _, h := range hintKeys(a) {
		k, label, _ := strings.Cut(h, " ")
		if other, ok := seen[label]; ok {
			t.Errorf("focus %s: %q and %q both read %q", a.r.FocusID(), other, k, label)
		}
		seen[label] = k
	}
}

func TestTerminalFooterExposesPassthroughLeader(t *testing.T) {
	t.Parallel()
	app := open(t)
	app.launcher = &fakeDaemon{}

	press(t, app, "j", "enter")
	runOnce(app, launchKey(t, app))
	// An attached terminal passes ctrl+c to the pane, so the footer leads
	// with the leader's hint and does not offer ctrl+c app.quit.
	if hints := hintKeys(app); len(hints) == 0 || hints[0] != "ctrl+a leader" {
		t.Fatalf("terminal leader hint missing: %v", hints)
	}
	for _, h := range hintKeys(app) {
		if h == "ctrl+c app.quit" {
			t.Fatalf("terminal: footer offers ctrl+c app.quit: %v", hintKeys(app))
		}
	}

}

// Test pass item 9: a chooser's footer and hint line list no motions
// (picker.move is a motion, as collection.move is on pages).
func TestChooserFooterListsNoMotions(t *testing.T) {
	t.Parallel()
	a := open(t)
	press(t, a, "P")
	if a.r.TopModal() != pinPicker {
		t.Fatalf("modal %q", a.r.TopModal())
	}
	f := frameAt(a, 120)
	if strings.Contains(f, "picker.move") || !strings.Contains(f, "enter picker.pick") {
		t.Fatalf("chooser hints:\n%s", f)
	}
}

// UX review 2 F11: hints name what the key does at this focus.
func TestStaleHintsAreGone(t *testing.T) {
	t.Parallel()
	app, f := panesApp(t)
	// This test reads the session rows (project → session → pane).
	groupBy(t, app, groupProjectSessionPane)
	has := func(want string) bool {
		for _, h := range hintKeys(app) {
			if h == want {
				return true
			}
		}
		return false
	}
	// The repeated operations read what each key does.
	footerLabels(t, app)
	if !has("] next tab") || !has("[ previous tab") {
		t.Fatalf("tab hints: %v", hintKeys(app))
	}
	// The daemon answers: e daemon.ensure is no key, even with no panes.
	if has("e daemon.ensure") {
		t.Fatalf("daemon up: %v", hintKeys(app))
	}
	// A session with no pane: enter is not offered (it could only fail);
	// one with a pane offers it.
	f.panes = append(f.panes, pane{Session: "empty"})
	reload(t, app)
	if err := app.r.SetFocus("session:empty"); err != nil {
		t.Fatal(err)
	}
	if has("enter session.attach") {
		t.Fatalf("empty session: %v", hintKeys(app))
	}
	if err := app.r.SetFocus("session:matomeru"); err != nil {
		t.Fatal(err)
	}
	if !has("enter session.attach") {
		t.Fatalf("session with a pane: %v", hintKeys(app))
	}
	f.panes = nil
	reload(t, app)
	if has("e daemon.ensure") {
		t.Fatalf("no panes, daemon up: %v", hintKeys(app))
	}
	// Down, e starts it.
	f.err = errors.New("dial unix tuios.sock: connect: no such file or directory")
	reload(t, app)
	if !has("e daemon.ensure") {
		t.Fatalf("daemon down: %v", hintKeys(app))
	}
}

// The leader's footer says what each side key opens.
func TestLeaderHintsNameEachKey(t *testing.T) {
	t.Parallel()
	app, _ := panesApp(t)
	press(t, app, "enter", "ctrl+a")
	if app.r.CurrentMode().Name != "leader_pending" {
		t.Fatalf("mode %s", app.r.CurrentMode().Name)
	}
	footerLabels(t, app)
}

// The detail's tab line offers "enter live" only on a pane row.
func TestTabLineEnterLiveOnlyOnAPaneRow(t *testing.T) {
	t.Parallel()
	app, _ := panesApp(t)
	settle(app, follow(app))
	if fr := frame(app); !strings.Contains(fr, "enter live") {
		t.Fatalf("pane row:\n%s", fr)
	}
	settle(app, key(t, app, "["))
	press(t, app, "}")
	if fr := frame(app); strings.Contains(fr, "enter live") {
		t.Fatalf("turns tree (focus %s):\n%s", app.r.FocusID(), fr)
	}
}

// In a live pane the leader never expires by default (sequence-timeout
// 0, ochazuke's default for every sequence): ctrl+a schedules no timer and
// still waits, and ctrl+a d then detaches.
func TestLiveLeaderWaitsPastTheBindingTimeout(t *testing.T) {
	t.Parallel()
	app, _ := panesApp(t)
	app.r.EnsureConfigSettings()
	if v, err := app.r.Settings().Get(ochazuke.SequenceTimeoutSetting); err != nil || v.Value != "0s" {
		t.Fatalf("sequence-timeout %+v %v", v, err)
	}
	press(t, app, "enter")
	live := app.r.FocusID()
	if app.r.CurrentMode().Name != "terminal" {
		t.Fatalf("enter: focus %s mode %s", live, app.r.CurrentMode().Name)
	}
	cmd := key(t, app, "ctrl+a")
	if app.r.BindingTimeoutCommand() != nil {
		t.Fatal("the leader scheduled a timer")
	}
	time.Sleep(50 * time.Millisecond)
	settle(app, cmd)
	if app.r.CurrentMode().Name != "leader_pending" || app.r.FocusID() != live {
		t.Fatalf("after the binding timeout: mode %s focus %s", app.r.CurrentMode().Name, app.r.FocusID())
	}
	press(t, app, "d")
	if app.r.CurrentMode().Name == "leader_pending" || app.r.CurrentMode().Name == "terminal" || app.r.FocusID() == live {
		t.Fatalf("ctrl+a d: mode %s focus %s", app.r.CurrentMode().Name, app.r.FocusID())
	}
}

// In a live pane an unbound key after the leader is ochazuke's
// sequence-unknown: swallow sends nothing to the pane and stays live;
// pass (the default) sends the leader, then the key.
func TestLiveLeaderUnknownKeyFollowsSequenceUnknown(t *testing.T) {
	t.Parallel()
	app, f := panesApp(t)
	app.r.EnsureConfigSettings()
	press(t, app, "enter")
	live := app.r.FocusID()
	before := len(f.streams[0].sent)
	if err := app.r.Settings().Set(ochazuke.SequenceUnknownSetting, ochazuke.SequenceSwallow); err != nil {
		t.Fatal(err)
	}
	press(t, app, "ctrl+a", "ctrl+o")
	if got := f.streams[0].sent[before:]; len(got) != 0 || app.r.CurrentMode().Name != "terminal" || app.r.FocusID() != live {
		t.Fatalf("swallow: sent %v mode %s focus %s", got, app.r.CurrentMode().Name, app.r.FocusID())
	}
	if err := app.r.Settings().Set(ochazuke.SequenceUnknownSetting, ochazuke.SequencePass); err != nil {
		t.Fatal(err)
	}
	press(t, app, "ctrl+a", "ctrl+o")
	if got := f.streams[0].sent[before:]; len(got) != 2 || got[0][0] != "ctrl+a" || got[1][0] != "ctrl+o" {
		t.Fatalf("pass: sent %v", got)
	}
}
