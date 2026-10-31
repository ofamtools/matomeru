//go:build ochazuke_render_trace

package main

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/ochazuke"
)

// This fixture uses the real App and framework with fake panes. Its clock
// advances deterministically; no timer, stream read or daemon is executed.
func idleRenderFixture(t *testing.T) (*App, *fakePanes, *int, *time.Time) {
	t.Helper()
	a, f := panesApp(t)
	f.panes = nil
	for i := range 40 {
		f.panes = append(f.panes, pane{Session: "fixture", Pane: daemon.Pane{ID: fmt.Sprint(i), Name: fmt.Sprintf("pane %02d 界 é", i), Title: "initial", Cwd: "/tmp/fixture"}})
	}
	f.panes[0].Title = f.panes[0].Name
	a.setPanes(f.panes, nil)
	if err := a.selectPaneRow(f.panes[0].rowID()); err != nil {
		t.Fatal(err)
	}
	a.m.Width, a.m.Height = 180, 48
	now := time.Unix(1800000000, 0)
	a.m.Now = func() time.Time { return now }
	calls := new(int)
	p := a.r.PageOf(panesPage)
	render := p.Render
	p.Render = func(r *ochazuke.Runtime, w, h int) ochazuke.Block { *calls++; return render(r, w, h) }
	a.View()
	return a, f, calls, &now
}

func TestIdleRenderMeasurements(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"idle", "spinner", "stream", "hidden-stream", "listing", "mixed"} {
		t.Run(kind, func(t *testing.T) {
			a, f, calls, now := idleRenderFixture(t)
			trace := a.m.TraceRender()
			var term *ochazuke.Terminal
			if kind == "stream" || kind == "hidden-stream" || kind == "mixed" {
				p := f.panes[0]
				if kind == "mixed" {
					p = f.panes[1]
				}
				if err := a.attachLive(p, p.rowID()); err != nil {
					t.Fatal(err)
				}
				term = a.r.Terminal(paneTermID)
				if kind == "hidden-stream" {
					a.pv.tab = tabActivity
					if err := a.r.SetFocus(p.rowID()); err != nil {
						t.Fatal(err)
					}
				}
			}
			previous := a.View()
			before := *calls
			composed := trace().Compositions
			changed := 0
			views := 0
			types := map[string]int{}
			deliver := func(msg tea.Msg) {
				types[fmt.Sprintf("%T", msg)]++
				a.Update(msg)
				v := a.View()
				views++
				if v.Content != previous.Content || !reflect.DeepEqual(v.Cursor, previous.Cursor) {
					changed++
				}
				previous = v
			}
			step := func(i int) {
				*now = now.Add(100 * time.Millisecond)
				if kind == "mixed" {
					*now = now.Add(-50 * time.Millisecond)
					deliver(daemonChanged{events: []daemon.Event{retitleEvent("0", fmt.Sprintf("spinner ⠋ %d", i%10))}})
					*now = now.Add(50 * time.Millisecond)
				}
				var msg tea.Msg = ochazuke.SettingsPollMsg{}
				switch kind {
				case "idle":
					msg = inboxDue{}
				case "spinner":
					msg = daemonChanged{events: []daemon.Event{retitleEvent("0", fmt.Sprintf("spinner ⠋ %d", i%10))}}
				case "stream", "hidden-stream", "mixed":
					for _, j := range a.r.Jobs() {
						if j.Owner == term.ID() && j.State == ochazuke.JobRunning {
							msg = ochazuke.Completion{Ticket: ochazuke.Ticket{Owner: j.Owner, Job: j.Job, Slot: j.Slot}, Value: ochazuke.TerminalFrame{Lines: []string{fmt.Sprintf("output 界 %d", i)}}}
							break
						}
					}
				case "listing":
					ticket, _, err := a.r.Issue(paneListID, "", "", ochazuke.Ctx{})
					if err != nil {
						t.Fatal(err)
					}
					a.setPanes(append([]pane(nil), f.panes...), nil)
					msg = ochazuke.Completion{Ticket: ticket}
				}
				deliver(msg)
			}
			for i := range 100 {
				step(i)
			}
			t.Logf("%s %s: messages=%v Views=%d page-renders=%d compositions=%d visible-changes=%d framework=%v", kind, now.Sub(time.Unix(1800000000, 0)), types, views, *calls-before, trace().Compositions-composed, changed, trace().Messages)
			result := testing.Benchmark(func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; b.Loop(); i++ {
					step(i)
				}
			})
			t.Logf("%s: %s %s", kind, result.String(), result.MemString())
		})
	}
}

func TestIdleRenderTriggerTrace(t *testing.T) {
	t.Parallel()
	for _, msg := range []tea.Msg{ochazuke.SettingsPollMsg{}, refreshDue{}, followDue{seq: 9999}, ochazuke.SegmentTickMsg{}, ochazuke.JobLingerMsg{}, scheduleDue{}, runsTick{}} {
		t.Run(fmt.Sprintf("%T", msg), func(t *testing.T) {
			a, _, _, now := idleRenderFixture(t)
			trace := a.m.TraceRender()
			before := a.View()
			composed := trace().Compositions
			*now = now.Add(time.Second)
			a.Update(msg)
			after := a.View()
			t.Logf("messages=%v Views=%d compositions=%d visible-change=%v", trace().Messages, trace().Views-1, trace().Compositions-composed, after.Content != before.Content)
			if after.Content == before.Content && trace().Compositions != composed {
				t.Fatal("unchanged timer composed the shell")
			}
		})
	}
}
