package main

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"
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

// endPane ends w2 (its window gone from the daemon) with panes.show-ended
// on: the page shows it as an ended row.
func endPane(t *testing.T, a *App, f *fakePanes) string {
	t.Helper()
	executeA(t, a, "pane", "list", "", ochazuke.Arguments{"all": true}) // copied in
	if err := a.r.Settings().Set(settingShowEnded, true); err != nil {
		t.Fatal(err)
	}
	f.panes = f.panes[:1]
	reload(t, a)
	row := "pane:ochazuke/w2"
	if a.r.Instances[row] == nil {
		t.Fatalf("no ended row %s", row)
	}
	return row
}

// On an ended row no verb that needs the window is offered: enter says the
// pane ended and attaches nothing (never another pane), d d kills nothing,
// and the hints offer neither.
func TestEndedRowOffersNoLiveVerb(t *testing.T) {
	t.Parallel()
	a, f := panesApp(t)
	row := endPane(t, a, f)
	if err := a.r.SetFocus(row); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"attach", "kill", "prompt", "copy", "hints", "preview"} {
		if a.r.Available(ochazuke.Request{Noun: "pane", Verb: verb}, row, a.r.Mode) {
			t.Errorf("pane.%s is available on an ended row", verb)
		}
	}
	for _, b := range ochazuke.HintBindings(a.r, row) {
		if b.Keys == "enter" || b.Keys == "d d" {
			t.Errorf("the hints offer %s %s on an ended row", b.Keys, b.Request.Key())
		}
	}
	runOnce(a, key(t, a, "enter"))
	if a.attached.ID != "" || len(f.streams) != 0 {
		t.Fatalf("enter on an ended row attached %q", a.attached.ID)
	}
	if got := footerLine(a); !strings.Contains(got, "shell ended") {
		t.Fatalf("footer: %q", got)
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "pane", Verb: "attach", Target: row}); err == nil {
		t.Fatal("pane.attach ran on an ended row")
	}
	// A live row still attaches.
	live := "pane:matomeru/w1"
	if !a.r.Available(ochazuke.Request{Noun: "pane", Verb: "attach"}, live, a.r.Mode) {
		t.Fatal("pane.attach is unavailable on a live row")
	}
}

// Only a listing of the daemon this process made marks a copy ended: a
// failed listing, and a process that never listed (the CLI before its
// first read, a re-scope of the cached listing), change nothing.
func TestOnlyARealListingMarksCopiesEnded(t *testing.T) {
	t.Parallel()
	a, f := panesApp(t)
	executeA(t, a, "pane", "list", "", ochazuke.Arguments{"all": true}) // copied in
	ended := func() []string {
		t.Helper()
		copies, err := a.work.Copies("pane")
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range copies {
			if c.Props[endedProp] == true {
				out = append(out, c.Key)
			}
		}
		return out
	}
	// The daemon is down: the listing fails, the cache is empty.
	f.err = errors.New("daemon down")
	reload(t, a)
	executeA(t, a, "pane", "list", "", ochazuke.Arguments{"all": true})
	if got := ended(); len(got) != 0 {
		t.Fatalf("a failed listing marked %v ended", got)
	}
	// A fresh process that has listed nothing: a re-scope claims no listing.
	f.err = nil
	a.panes, a.pv.listed, a.pv.listErr = nil, false, nil
	if err := a.refreshScope(); err != nil {
		t.Fatal(err)
	}
	if a.pv.listed {
		t.Fatal("re-scoping the cached listing claimed a listing")
	}
	c := ochazuke.Ctx{Runtime: a.r, Args: ochazuke.Arguments{cachedOnlyArg: true}}
	if _, l := a.withListing(c); l.known {
		t.Fatal("a cached read with no listing is known")
	}
	if got := ended(); len(got) != 0 {
		t.Fatalf("no listing marked %v ended", got)
	}
	// Its first read lists the daemon (a CLI's pane list): the panes are there.
	rows, _ := ochazuke.AnswerRows(executeA(t, a, "pane", "list", "", ochazuke.Arguments{"all": true})).([]map[string]any)
	if len(rows) != 2 || len(ended()) != 0 {
		t.Fatalf("list: %v; ended %v", rows, ended())
	}
}

// A copy row stores only what its noun keeps: the name, the last seen
// session, cwd and harness, and when it ended; never the daemon's live
// facts or mtm's own records (the star, the owner). Rows written before
// are narrowed when mtm opens.
func TestCopyRowsKeepOnlyTheirProps(t *testing.T) {
	t.Parallel()
	a, f := panesApp(t)
	f.panes[0].Title, f.panes[0].Cwd = "a title", "/work"
	reload(t, a)
	executeA(t, a, "pane", "set", "w1", ochazuke.Arguments{"props": map[string]any{"starred": true}})
	keys := func(noun, key string) string {
		t.Helper()
		c, ok, err := a.work.Copy(noun, key)
		if err != nil || !ok {
			t.Fatalf("%s %s: %v %v", noun, key, ok, err)
		}
		return strings.Join(slices.Sorted(maps.Keys(c.Props)), ",")
	}
	if got := keys("pane", "w1"); got != "cwd,harness,id,name,session" {
		t.Fatalf("pane copy stores %s", got)
	}
	thing, _ := executeA(t, a, "pane", "get", "", ochazuke.Arguments{"id": "w1"}).(map[string]any)
	if thing["title"] != "a title" || thing["starred"] != true {
		t.Fatalf("get reads the live facts and the star: %v", thing)
	}
	executeA(t, a, "session", "list", "", ochazuke.Arguments{"all": true})
	if got := keys("session", "matomeru"); got != "id,name" {
		t.Fatalf("session copy stores %s", got)
	}
	f.panes = f.panes[1:]
	reload(t, a)
	if got := keys("pane", "w1"); got != "cwd,ended,ended-at,harness,id,name,session" {
		t.Fatalf("ended pane copy stores %s", got)
	}
	thing, _ = executeA(t, a, "pane", "get", "", ochazuke.Arguments{"id": "w1"}).(map[string]any)
	if thing["ended"] != true || thing["cwd"] != "/work" || thing["starred"] != true || thing["title"] != nil {
		t.Fatalf("an ended pane reads %v", thing)
	}
	// A row as wide as the copies were before is narrowed on open.
	wide := map[string]any{"id": "old", "name": "x", "title": "t", "starred": true, "project": "p"}
	if err := a.work.PutCopy("pane", "old", a.server(), wide); err != nil {
		t.Fatal(err)
	}
	if err := a.narrowCopies(a.copies["pane"]); err != nil {
		t.Fatal(err)
	}
	if got := keys("pane", "old"); got != "id,name" {
		t.Fatalf("a wide row narrowed to %s", got)
	}
}

// A pane named at creation keeps the daemon's name when its program
// retitles it: the copy holds no name mtm did not write
// (copies.hold-names written), so a rename outside mtm shows too; one
// renamed in mtm is held, and an outside rename shows only in refresh.
// Under copies.hold-names all every name seen is held (an empty one never).
func TestCopiesHoldOnlyNamesWrittenInMtm(t *testing.T) {
	t.Parallel()
	a, f := panesApp(t)
	a.renamer = &fakeRenamer{panes: f}
	row := "pane:matomeru/w1"
	f.panes[0].Name, f.panes[0].Title = "fixer", "fixer" // named at creation
	forgetCopies(t, a)
	reload(t, a)
	f.panes[0].Title = "claude: thinking" // the program retitles it
	reload(t, a)
	if got := cells(a, row); !strings.Contains(got, "fixer") {
		t.Fatalf("after a retitle the row reads %q", got)
	}
	f.panes[0].Name = "outside" // renamed outside mtm
	reload(t, a)
	if got := cells(a, row); !strings.Contains(got, "outside") {
		t.Fatalf("an outside rename does not show: %q", got)
	}
	executeA(t, a, "pane", "set", row, ochazuke.Arguments{"props": map[string]any{"name": "mine"}})
	settle(a, a.r.PendingCommands())
	f.panes[0].Name = "theirs"
	reload(t, a)
	if got := cells(a, row); !strings.Contains(got, "mine") {
		t.Fatalf("a held name was not kept: %q", got)
	}
	out := executeA(t, a, "pane", "refresh", "", ochazuke.Arguments{"id": "w1"})
	settle(a, a.r.PendingCommands())
	if got := strings.Join(diffText(out.(map[string]any)["differences"]), "; "); got != "name: mine → theirs" {
		t.Fatalf("refresh found %q", got)
	}

	// copies.hold-names all: w2's name is held from first sight.
	if err := a.r.Settings().Set(settingHoldNames, holdAll); err != nil {
		t.Fatal(err)
	}
	f.panes[1].Name = "renamed-outside"
	reload(t, a)
	if got := cells(a, "pane:ochazuke/w2"); !strings.Contains(got, "shell") || strings.Contains(got, "renamed-outside") {
		t.Fatalf("under all an outside rename shows: %q", got)
	}
}

func TestUniqueID(t *testing.T) {
	t.Parallel()
	ids := []string{"b34d4d11-dc2b", "b3ff0000-0000", "621ac6ca-2e45"}
	for in, want := range map[string]string{"b34d4d11-dc2b": "b34d4d11-dc2b", "b34d": "b34d4d11-dc2b", "b3": "b3", "62": "621ac6ca-2e45", "zz": "zz", "": ""} {
		if got := uniqueID(in, ids); got != want {
			t.Errorf("uniqueID(%q) = %q, want %q", in, got, want)
		}
	}
}

// pane set names a pane by its window id, or a unique prefix of it, from a
// process that lists the daemon itself (the CLI): the daemon renames it.
// A pane mtm keeps a copy of that the daemon no longer lists keeps the
// name in its copy, with a notice that names it once.
func TestPaneSetByWindowIDFromTheCLI(t *testing.T) {
	t.Parallel()
	a, f := panesApp(t)
	r := &fakeRenamer{panes: f}
	a.renamer = r
	f.panes[1].ID = "b34d4d11-dc2b-4066"
	a.panes, a.pv.listed, a.pv.listErr = nil, false, nil // as the CLI starts
	forgetCopies(t, a)
	executeA(t, a, "pane", "set", "w1", ochazuke.Arguments{"props": map[string]any{"name": "byid"}})
	executeA(t, a, "pane", "set", "b34d", ochazuke.Arguments{"props": map[string]any{"name": "byprefix"}})
	settle(a, a.r.PendingCommands())
	if got := strings.Join(slices.Sorted(slices.Values(r.calls)), "; "); got != "window matomeru/w1 byid; window ochazuke/b34d4d11-dc2b-4066 byprefix" {
		t.Fatalf("renames: %s", got)
	}
	f.panes = f.panes[:1]
	executeA(t, a, "pane", "set", "b34d", ochazuke.Arguments{"props": map[string]any{"name": "gone"}})
	c, ok, err := a.work.Copy("pane", "b34d4d11-dc2b-4066")
	if err != nil || !ok || c.Props["name"] != "gone" {
		t.Fatalf("the copy: %+v %v %v", c.Props, ok, err)
	}
}
