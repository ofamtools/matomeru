package main

import (
	"fmt"
	"slices"
	"testing"

	"ofam.dev/ochazuke"
)

func TestLiveRenameAssociation(t *testing.T) {
	t.Parallel()
	for _, side := range []bool{false, true} {
		t.Run(fmt.Sprint(side), func(t *testing.T) {
			t.Parallel()
			var a *App
			var f *fakePanes
			if side {
				a, f = sideViewApp(t)
				if err := a.showSide(a.attached, "term"); err != nil {
					t.Fatal(err)
				}
			} else {
				a, f = panesApp(t)
				press(t, a, "enter")
			}
			live := a.r.FocusID()
			fr := &fakeRenamer{panes: f}
			a.renamer = fr
			// A different selected row must never steal the live subject.
			if err := a.paneList.Select("pane:ochazuke/w2"); err != nil {
				t.Fatal(err)
			}
			press(t, a, "ctrl+a", "r")
			if editor := editing(a); a.r.FocusID() != editFormID+"/name" || editor == nil || editor.Values()["name"] != "claude" {
				t.Fatalf("rename did not name live parent: %s", frame(a))
			}
			typeText(t, a, "reviewer")
			pressJob(t, a, "enter")
			if !slices.Equal(fr.calls, []string{"window matomeru/w1 reviewer"}) {
				t.Fatalf("renamed %v", fr.calls)
			}
			if a.r.FocusID() != live || a.r.CurrentMode().Name != "terminal" || a.attached.ID != "w1" {
				t.Fatalf("lost live: %s %s %+v", a.r.FocusID(), a.r.CurrentMode().Name, a.attached)
			}
		})
	}
}

func TestLiveRenameOptOutAndNestedLeader(t *testing.T) {
	t.Parallel()
	for _, setting := range []string{ochazuke.SubjectFromAssociationSetting, ochazuke.LeaderFallThroughSetting, "nested"} {
		t.Run(setting, func(t *testing.T) {
			t.Parallel()
			a, f := panesApp(t)
			press(t, a, "enter")
			if setting == ochazuke.SubjectFromAssociationSetting {
				if err := a.r.Settings().Set(setting, false); err != nil {
					t.Fatal(err)
				}
			}
			if setting == ochazuke.LeaderFallThroughSetting {
				if err := a.r.Settings().Set(setting, "off"); err != nil {
					t.Fatal(err)
				}
			}
			if setting == "nested" {
				press(t, a, "ctrl+a", "ctrl+a", "r")
			} else {
				press(t, a, "ctrl+a", "r")
			}
			if a.r.FocusID() != paneTermID || a.r.CurrentMode().Name != "terminal" {
				t.Fatalf("lost live: %s %s", a.r.FocusID(), a.r.CurrentMode().Name)
			}
			if !slices.Equal(slices.Concat(f.streams[0].sent...), []string{"ctrl+a", "r"}) {
				t.Fatalf("forwarded %v", f.streams[0].sent)
			}
		})
	}
}

func TestLiveFocusBoundOutsideKeysStayLive(t *testing.T) {
	t.Parallel()
	for _, k := range []string{"enter", "]"} {
		t.Run(k, func(t *testing.T) {
			t.Parallel()
			a, f := panesApp(t)
			press(t, a, "enter")
			stream := a.r.Terminal(paneTermID)
			press(t, a, "ctrl+a", k)
			if a.r.Terminal(paneTermID) != stream || a.r.FocusID() != paneTermID || a.r.CurrentMode().Name != "terminal" {
				t.Fatalf("%s changed the live terminal", k)
			}
			if !slices.Equal(slices.Concat(f.streams[0].sent...), []string{"ctrl+a", k}) {
				t.Fatalf("%s sent %v", k, f.streams[0].sent)
			}
		})
	}
}
