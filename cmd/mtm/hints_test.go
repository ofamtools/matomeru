package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/unixsuperhero/ochazuke"
)

// hintKeys is the footer's hint line for the focus as "key noun.verb"
// pairs, in the order the footer shows them. The footer carries no
// descriptions: every hint is "key noun.verb", or "key hint" for a row
// with a Hint (the leader's "ctrl+a leader: …", "] next tab").
func hintKeys(a *App) []string {
	var out []string
	for _, h := range strings.Split(ochazuke.BindingHints(a.r, a.r.Focus), " · ") {
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
			t.Errorf("focus %s: %q and %q both read %q", a.r.Focus, other, k, label)
		}
		seen[label] = k
	}
}

func TestTerminalFooterExposesPassthroughLeader(t *testing.T) {
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

// ? opens the framework's help over the focus: on a task row it lists the
// row's own verbs first, each as key, noun.verb and description, and esc
// closes it back onto the row.
func TestHelpOnATaskRowListsItsVerbsFirst(t *testing.T) {
	app := open(t)
	mono(t, app)
	press(t, app, "j", "enter")
	if app.r.Focus != "t1" {
		t.Fatalf("focus %s", app.r.Focus)
	}
	press(t, app, "?")
	if top := app.r.TopModal(); top != "bindings-help" {
		t.Fatalf("top modal %q", top)
	}
	rows := ochazuke.BindingHelp(app.r, "t1")
	if len(rows) == 0 || rows[0].Key != "enter" || rows[0].Operation != "task.edit" || rows[0].Description == "" {
		t.Fatalf("help rows = %+v", rows)
	}
	lines := strings.Split(monoFrame(t, app), "\n")
	var first string
	for _, line := range lines {
		// The help is a table since ochazuke v0.3.0: the selected row is
		// the reverse highlight alone, no "> " marker, under a "task"
		// section label. Since the prefix column (ochazuke's R6.9) a
		// single key sits after a blank prefix cell.
		if _, row, ok := strings.Cut(line, "│"); ok {
			if f := strings.Fields(strings.TrimSuffix(strings.TrimSpace(row), "│")); len(f) > 0 && f[0] == "enter" {
				first = strings.Join(f, " ")
				break
			}
		}
	}
	// The help wraps a long description in its column, so compare the
	// description's first words, not a fixed prefix.
	want := "enter task.edit " + strings.Join(strings.Fields(rows[0].Description)[:2], " ")
	if !strings.HasPrefix(first, want) {
		t.Fatalf("help modal's selected first row = %q\n%s", first, strings.Join(lines, "\n"))
	}
	press(t, app, "esc")
	if app.r.TopModal() != "" || app.r.Focus != "t1" {
		t.Fatalf("esc: modal %q focus %s", app.r.TopModal(), app.r.Focus)
	}
}

// Test pass item 9: a chooser's footer and hint line list no motions
// (chooser.move is a motion, as list.move is on pages).
func TestChooserFooterListsNoMotions(t *testing.T) {
	a := open(t)
	press(t, a, "P")
	if a.r.TopModal() != pinPicker {
		t.Fatalf("modal %q", a.r.TopModal())
	}
	f := frameAt(a, 120)
	if strings.Contains(f, "chooser.move") || !strings.Contains(f, "enter chooser.pick") {
		t.Fatalf("chooser hints:\n%s", f)
	}
}

// UX review 2 F11: hints name what the key does at this focus.
func TestStaleHintsAreGone(t *testing.T) {
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
	app, _ := panesApp(t)
	press(t, app, "enter", "ctrl+a")
	if app.r.Mode.Name != "leader_pending" {
		t.Fatalf("mode %s", app.r.Mode.Name)
	}
	footerLabels(t, app)
}

// The detail's tab line offers "enter live" only on a pane row.
func TestTabLineEnterLiveOnlyOnAPaneRow(t *testing.T) {
	app, _ := panesApp(t)
	settle(app, follow(app))
	if fr := frame(app); !strings.Contains(fr, "enter live") {
		t.Fatalf("pane row:\n%s", fr)
	}
	settle(app, key(t, app, "["))
	press(t, app, "}")
	if fr := frame(app); strings.Contains(fr, "enter live") {
		t.Fatalf("turns tree (focus %s):\n%s", app.r.Focus, fr)
	}
}

// In a live pane the leader never expires by default (sequence-timeout
// 0, ochazuke's default for every sequence): ctrl+a schedules no timer and
// still waits, and ctrl+a d then detaches.
func TestLiveLeaderWaitsPastTheBindingTimeout(t *testing.T) {
	app, _ := panesApp(t)
	app.r.EnsureConfigSettings()
	if v, err := app.r.Settings().Get(ochazuke.SequenceTimeoutSetting); err != nil || v.Value != "0s" {
		t.Fatalf("sequence-timeout %+v %v", v, err)
	}
	press(t, app, "enter")
	live := app.r.Focus
	if app.r.Mode.Name != "terminal" {
		t.Fatalf("enter: focus %s mode %s", live, app.r.Mode.Name)
	}
	cmd := key(t, app, "ctrl+a")
	if app.r.BindingTimeoutCommand() != nil {
		t.Fatal("the leader scheduled a timer")
	}
	time.Sleep(50 * time.Millisecond)
	settle(app, cmd)
	if app.r.Mode.Name != "leader_pending" || app.r.Focus != live {
		t.Fatalf("after the binding timeout: mode %s focus %s", app.r.Mode.Name, app.r.Focus)
	}
	press(t, app, "d")
	if app.r.Mode.Name == "leader_pending" || app.r.Mode.Name == "terminal" || app.r.Focus == live {
		t.Fatalf("ctrl+a d: mode %s focus %s", app.r.Mode.Name, app.r.Focus)
	}
}

// In a live pane an unbound key after the leader is ochazuke's
// sequence-unknown: swallow sends nothing to the pane and stays live;
// pass (the default) sends the leader, then the key.
func TestLiveLeaderUnknownKeyFollowsSequenceUnknown(t *testing.T) {
	app, f := panesApp(t)
	app.r.EnsureConfigSettings()
	press(t, app, "enter")
	live := app.r.Focus
	before := len(f.streams[0].sent)
	if err := app.r.Settings().Set(ochazuke.SequenceUnknownSetting, ochazuke.SequenceSwallow); err != nil {
		t.Fatal(err)
	}
	press(t, app, "ctrl+a", "space")
	if got := f.streams[0].sent[before:]; len(got) != 0 || app.r.Mode.Name != "terminal" || app.r.Focus != live {
		t.Fatalf("swallow: sent %v mode %s focus %s", got, app.r.Mode.Name, app.r.Focus)
	}
	if err := app.r.Settings().Set(ochazuke.SequenceUnknownSetting, ochazuke.SequencePass); err != nil {
		t.Fatal(err)
	}
	press(t, app, "ctrl+a", "space")
	if got := f.streams[0].sent[before:]; len(got) != 2 || got[0][0] != "ctrl+a" || got[1][0] != "space" {
		t.Fatalf("pass: sent %v", got)
	}
}
