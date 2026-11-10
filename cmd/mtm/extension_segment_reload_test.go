package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"ofam.dev/ochazuke"
)

// stateCatalog is a tool with a link noun beside its rows: a key row on
// an item runs link.toggle (a verb of another noun), and a header
// segment answers link.brief.
const stateCatalog = `{"schema":1,"name":"o-fake","version":"1.0","nouns":[
 {"name":"item","columns":[{"title":"item"},{"title":"state"}],"verbs":[
  {"name":"list","kind":"application","description":"List the items"},
  {"name":"done","kind":"application"}]},
 {"name":"link","verbs":[
  {"name":"toggle","kind":"application"},
  {"name":"brief","kind":"application"}]}],
 "bindings":[{"noun":"item","mode":"normal","keys":"c","request":{"noun":"link","verb":"toggle"}},
  {"noun":"item","mode":"normal","keys":"X","request":{"noun":"item","verb":"done"}}],
 "segments":[{"id":"link","label":"fake","verb":"link.brief","every":"2s"}],
 "pages":[]}`

// stateApp registers the state tool and opens its page; brief answers the
// text in the returned file.
func stateApp(t *testing.T) (a *App, log, brief string) {
	t.Helper()
	brief = filepath.Join(t.TempDir(), "brief")
	if err := os.WriteFile(brief, []byte("off"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin, log := fakeExtension(t, stateCatalog, `"link toggle") echo '{"ok":true}' ;;
"link brief") cat '`+brief+`' ;;`)
	path := seeded(t)
	mustCLI(t, openAt(t, path), "extension", "register", "fake", "--bin", bin)
	a = openAt(t, path)
	openExtPage(t, a, extPageID("fake", ""))
	return a, log, brief
}

// A key row on an item running a verb of another noun (link.toggle) runs
// it with no target: the item is not a thing of the verb's noun. A verb
// of the row's own noun still gets the row's id.
func TestExtensionCrossNounKeyHasNoTarget(t *testing.T) {
	t.Parallel()
	a, log, _ := stateApp(t)
	list := extPageID("fake", "") + "/item"
	if err := a.r.SetFocus(list + "/i1"); err != nil {
		t.Fatal(err)
	}
	pressJob(t, a, "c")
	pressJob(t, a, "X")
	got := calls(t, log)
	toggle := slices.IndexFunc(got, func(c string) bool { return strings.Contains(c, "|link toggle") })
	done := slices.IndexFunc(got, func(c string) bool { return strings.Contains(c, "|item done") })
	if toggle < 0 || strings.Contains(got[toggle], "--target") {
		t.Fatalf("link toggle ran with the item as its target:\n%s", strings.Join(got, "\n"))
	}
	if done < 0 || !strings.Contains(got[done], "item done --target i1 ") {
		t.Fatalf("item done lost its target:\n%s", strings.Join(got, "\n"))
	}
}

// When an extension's header segment changes, its open page reloads
// (once per change, never while the text holds); the setting turns it off.
func TestExtensionSegmentChangeReloadsItsPage(t *testing.T) {
	t.Parallel()
	a, log, brief := stateApp(t)
	now := time.Unix(1_790_000_000, 0)
	a.m.Now = func() time.Time { return now }
	// This test is about the page reload, not the poll rate: with backoff on
	// (ochazuke v0.117.0) an unchanged answer stretches the wait past the
	// test clock's 3s step, so the changed answer would not be asked for.
	if _, err := a.r.Settings().SetSession(ochazuke.SegmentBackoffSetting, false); err != nil {
		t.Fatal(err)
	}
	if _, err := a.r.Settings().SetSession(ochazuke.SegmentSetting("fake-link"), true); err != nil {
		t.Fatal(err)
	}
	lists := func() int {
		n := 0
		for _, c := range calls(t, log) {
			if strings.HasSuffix(c, "|item list --json {}") {
				n++
			}
		}
		return n
	}
	// tick runs the due segments, delivers their answers through
	// App.Update and settles what that started.
	tick := func() {
		t.Helper()
		now = now.Add(3 * time.Second)
		got := make(chan tea.Msg, 8)
		var start func(tea.Cmd)
		start = func(c tea.Cmd) {
			if c == nil {
				return
			}
			go func() {
				switch msg := c().(type) {
				case tea.BatchMsg:
					for _, c := range msg {
						start(c)
					}
				case ochazuke.SegmentMsg:
					got <- msg
				}
			}()
		}
		_, cmd := a.m.Update(ochazuke.SegmentTickMsg{})
		start(cmd)
		var later []tea.Cmd
		deadline := time.After(5 * time.Second)
		for a.m.SegmentsInFlight() > 0 {
			select {
			case msg := <-got:
				_, c := a.Update(msg)
				later = append(later, c)
			case <-deadline:
				t.Fatal("the segment never answered")
			}
		}
		settleJobs(t, a, tea.Batch(later...))
	}
	before := lists()
	tick() // the first answer: "" -> off
	if lists() != before+1 {
		t.Fatalf("the first answer did not reload: %d -> %d", before, lists())
	}
	tick() // unchanged
	if lists() != before+1 {
		t.Fatalf("an unchanged segment reloaded: %d", lists()-before)
	}
	if err := os.WriteFile(brief, []byte("ready"), 0o644); err != nil {
		t.Fatal(err)
	}
	tick()
	if lists() != before+2 {
		t.Fatalf("a changed segment did not reload: %d", lists()-before)
	}
	if !strings.Contains(frame(a), "fake ready") {
		t.Fatalf("header lacks the segment:\n%s", frame(a))
	}
	if _, err := a.r.Settings().SetSession(settingExtSegmentReload, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brief, []byte("off"), 0o644); err != nil {
		t.Fatal(err)
	}
	tick()
	if lists() != before+2 {
		t.Fatalf("extensions.segment-reload off still reloaded")
	}
}
