package main

import (
	"encoding/json"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

// forgetCopies drops every pane and session copy, for a test whose fixture
// recasts the daemon's windows under the same ids after the first
// listing: mtm sees them for the first time at the next one.
func forgetCopies(t *testing.T, a *App) {
	t.Helper()
	for _, noun := range []string{"pane", "session"} {
		copies, err := a.work.Copies(noun)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range copies {
			if err := a.work.DeleteCopy(noun, c.Key); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// A pane renamed in mtm keeps the name when the daemon restarts and lists
// the old one: the row, pane get and pane list read mtm's copy; refresh
// shows the difference (the copy's name, the daemon's) as a pick, and
// picking the source's takes it.
func TestPaneRenameSurvivesADaemonRestart(t *testing.T) {
	t.Parallel()
	a, f := panesApp(t)
	a.renamer = &fakeRenamer{panes: f}
	row := "pane:matomeru/w1"
	executeA(t, a, "pane", "set", row, ochazuke.Arguments{"props": map[string]any{"@label": "fixer"}})
	settle(a, a.r.PendingCommands())
	if f.panes[0].Name != "fixer" {
		t.Fatalf("the daemon was not renamed: %q", f.panes[0].Name)
	}
	f.panes[0].Name = "claude" // restarted: the daemon lists the name it had
	reload(t, a)
	if got := cells(a, row); !strings.Contains(got, "fixer") {
		t.Fatalf("after a restart the row reads %q", got)
	}
	thing, _ := executeA(t, a, "pane", "get", "", ochazuke.Arguments{"id": "w1"}).(map[string]any)
	if thing["name"] != "fixer" || thing["session"] != "matomeru" {
		t.Fatalf("get: %v", thing)
	}
	out := executeA(t, a, "pane", "refresh", "", ochazuke.Arguments{"id": "w1"})
	settle(a, a.r.PendingCommands())
	diffs, _ := out.(map[string]any)["differences"]
	if got := strings.Join(diffText(diffs), "; "); got != "name: fixer → claude" {
		t.Fatalf("refresh found %q (%v)", got, out)
	}
	e := a.r.RecordEditor("copies-refresh")
	if e == nil {
		t.Fatalf("refresh opened %q", a.r.TopModal())
	}
	// Nothing else differs: the daemon's state, title and the rest are
	// the copy's as mtm last read them.
	rows, _ := ochazuke.AnswerRows(executeA(t, a, "pane", "list", "", ochazuke.Arguments{"all": true})).([]map[string]any)
	if len(rows) != 2 || rows[0]["name"] != "fixer" {
		t.Fatalf("list: %v", rows)
	}
}

// diffText is a refresh's differences as "prop: local → source", read
// through their JSON form.
func diffText(v any) []string {
	data, _ := json.Marshal(v)
	var ds []struct{ Prop, Local, Source string }
	_ = json.Unmarshal(data, &ds)
	var out []string
	for _, d := range ds {
		out = append(out, d.Prop+": "+d.Local+" → "+d.Source)
	}
	return out
}

// Panes and sessions the daemon stops listing stay as ended copies:
// pane list and session list keep them (ended), the Panes page shows the
// pane only under panes.show-ended, as an ended row.
func TestEndedPanesAndSessionsStay(t *testing.T) {
	t.Parallel()
	a, f := sessionsApp(t)
	executeA(t, a, "session", "list", "", ochazuke.Arguments{"all": true}) // copied in
	f.panes = f.panes[:1]                                                  // w2 and its session ochazuke ended
	reload(t, a)
	panes, _ := ochazuke.AnswerRows(executeA(t, a, "pane", "list", "", ochazuke.Arguments{"all": true})).([]map[string]any)
	if len(panes) != 2 || panes[1]["id"] != "w2" || panes[1]["ended"] != true || panes[0]["ended"] == true {
		t.Fatalf("panes: %v", panes)
	}
	sessions, _ := ochazuke.AnswerRows(executeA(t, a, "session", "list", "", ochazuke.Arguments{"all": true})).([]map[string]any)
	if len(sessions) != 2 || sessions[1]["name"] != "ochazuke" || sessions[1]["ended"] != true {
		t.Fatalf("sessions: %v", sessions)
	}
	ended := "pane:ochazuke/w2"
	if a.r.Instances[ended] != nil {
		t.Fatal("an ended pane is on the page with panes.show-ended off")
	}
	if err := a.r.Settings().Set(settingShowEnded, true); err != nil {
		t.Fatal(err)
	}
	if err := a.rebuildPanes(); err != nil {
		t.Fatal(err)
	}
	if got := cells(a, ended); !strings.Contains(got, "ended") {
		t.Fatalf("ended row: %q", got)
	}
	// The pane comes back: it is listed, not ended.
	f.panes = newFakePanes().panes
	reload(t, a)
	if got := cells(a, ended); strings.Contains(got, "ended") {
		t.Fatalf("a listed pane reads ended: %q", got)
	}
}

// One database serves every mtm, on any TUIOS server: a copy another
// server's mtm made is neither listed here nor marked ended by this
// listing.
func TestCopiesStayWithTheirSource(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	if err := a.work.PutCopy("pane", "elsewhere", "/other/server.sock", map[string]any{"id": "elsewhere", "name": "theirs"}); err != nil {
		t.Fatal(err)
	}
	reload(t, a)
	rows, _ := ochazuke.AnswerRows(executeA(t, a, "pane", "list", "", ochazuke.Arguments{"all": true})).([]map[string]any)
	if len(rows) != 2 {
		t.Fatalf("list: %v", rows)
	}
	c, ok, err := a.work.Copy("pane", "elsewhere")
	if err != nil || !ok || c.Props["ended"] != nil {
		t.Fatalf("the other server's copy: %+v %v %v", c, ok, err)
	}
}
