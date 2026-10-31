package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/ochazuke"
)

func TestPanePanelCacheMatchesComposition(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	l := ochazuke.Layout{Styles: a.m.Styles()}
	c := &panePanelCache{}
	body := ochazuke.Block{Text: "界 é\nsecond", Cursor: &ochazuke.Pos{X: 1, Y: 0}}
	for _, test := range []struct {
		w, h    int
		focused bool
		tone    string
	}{{60, 12, false, ""}, {60, 12, true, "state.working"}, {35, 8, true, "state.waiting"}, {2, 2, false, ""}} {
		for range 2 {
			got := a.cachedPanePanel(c, l, "title", test.tone, body, test.w, test.h, test.focused)
			want := l.WithPanelTone(test.tone).PanelBlock("title", body, test.w, test.h, test.focused)
			if !reflect.DeepEqual(got, want) {
				t.Fatal("cached panel differs")
			}
			if got.Cursor != nil {
				got.Cursor.X++
			}
		}
	}
	body.Text = "changed body"
	got := a.cachedPanePanel(c, l, "new title", "", body, 60, 12, false)
	if want := l.PanelBlock("new title", body, 60, 12, false); !reflect.DeepEqual(got, want) {
		t.Fatal("content mutation missed")
	}
	l.Styles.Muted = l.Styles.Muted.Bold(true)
	got = a.cachedPanePanel(c, l, "new title", "", body, 60, 12, false)
	if want := l.PanelBlock("new title", body, 60, 12, false); !reflect.DeepEqual(got, want) {
		t.Fatal("style mutation missed")
	}
}

func TestPaneTitleCacheKeepsDuplicateNamesInSync(t *testing.T) {
	t.Parallel()
	for _, working := range []bool{false, true} {
		t.Run(fmt.Sprint("peer-working=", working), func(t *testing.T) {
			a, f := panesApp(t)
			for i := range f.panes {
				f.panes[i].Session = "fixture"
				f.panes[i].Title = f.panes[i].Name
			}
			if working {
				f.panes[1].Harness = "claude-code"
				f.panes[1].AgentState = "working"
			}
			a.setPanes(f.panes, nil)
			for _, test := range []struct{ title, want string }{{"shell", "shell-2"}, {"claude", "shell"}} {
				a.applyPaneEvents([]daemon.Event{retitleEvent("w1", test.title)})
				found := false
				for _, s := range a.pv.sections {
					for _, n := range s.tree.List().Items() {
						if p, ok := n.Data.(pane); ok && p.ID == "w2" {
							found = true
							if len(n.Cells) == 0 || !strings.HasSuffix(n.Cells[0], " "+test.want) {
								t.Fatalf("retitle %q left the other pane's cell %v; want %q", test.title, n.Cells, test.want)
							}
						}
					}
				}
				if !found {
					t.Fatal("other pane vanished")
				}
			}
		})
	}
}

func TestPaneListingRetainsEqualRowsAndUpdatesActions(t *testing.T) {
	t.Parallel()
	a, f := panesApp(t)
	row := f.panes[0].rowID()
	before := a.r.Instances[row]
	a.setPanes(append([]pane(nil), f.panes...), nil)
	if a.r.Instances[row] != before {
		t.Fatal("unchanged listing recreated row instance")
	}
	f.panes[0].Meta = map[string]string{"model": "changed"}
	a.setPanes(append([]pane(nil), f.panes...), nil)
	p, err := a.paneAt(a.r.Instances[row])
	if err != nil || p.Meta["model"] != "changed" {
		t.Fatal("equal labels hid changed action payload")
	}
}

func TestPaneColumnsMemoKeepsCursorAndGeometry(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	for _, size := range [][2]int{{180, 48}, {180, 48}, {120, 30}, {80, 24}, {180, 48}} {
		cached := a.renderPanes(a.r, size[0], size[1])
		if err := a.r.Settings().Set(ochazuke.RenderMaxFPSSetting, 0); err != nil {
			t.Fatal(err)
		}
		uncached := a.renderPanes(a.r, size[0], size[1])
		if !reflect.DeepEqual(cached, uncached) {
			t.Fatal("cached page differs after resize")
		}
		if err := a.r.Settings().Set(ochazuke.RenderMaxFPSSetting, 30); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPaneHistoryVisibilityPolicy(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	a.pv.tab = tabTurns
	if !a.turnsHistoryActive() {
		t.Fatal("shown Turns was paused")
	}
	if err := a.r.OpenPage("work"); err != nil {
		t.Fatal(err)
	}
	if a.turnsHistoryActive() {
		t.Fatal("hidden Turns remained active")
	}
	a.subs.views = nil
	a.wantAgents()
	if a.subs.views != nil {
		t.Fatal("hidden subagents were traversed")
	}
	setting(t, a, settingHistoryVisible, false)
	if !a.turnsHistoryActive() {
		t.Fatal("opt-out did not restore background work")
	}
	setting(t, a, settingHistoryVisible, true)
	if err := a.r.OpenPage(panesPage); err != nil {
		t.Fatal(err)
	}
	a.pv.tab = tabActivity
	if a.turnsHistoryActive() {
		t.Fatal("hidden Turns tab remained active")
	}
}
