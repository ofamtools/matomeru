package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
			if a.r.FocusID() != editFormID+"/name" || !strings.Contains(frame(a), "Edit pane name: matomeru/claude") {
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

// The golden is the reviewable key migration record, not a second resolver.
func TestLeaderInventory(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	outside := a.r.ListBindings(a.r.FocusID())
	press(t, a, "enter", "ctrl+a")
	leader := a.r.ListBindings(a.r.FocusID())
	explicit := map[string]ochazuke.Binding{}
	for _, b := range leader {
		if b.Mode == "leader_pending" && b.Keys != "" {
			explicit[b.Keys] = b
		}
	}
	rows := map[string]ochazuke.Binding{}
	for _, b := range outside {
		if (b.Mode == "normal" || b.Mode == "") && b.Keys != "" && !strings.Contains(b.Keys, " ") {
			if _, ok := rows[b.Keys]; !ok {
				rows[b.Keys] = b
			}
		}
	}
	for k := range explicit {
		if _, ok := rows[k]; !ok {
			rows[k] = ochazuke.Binding{}
		}
	}
	keys := make([]string, 0, len(rows))
	for k := range rows {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var out strings.Builder
	fmt.Fprintln(&out, "key\toutside\tleader (no-motions)")
	for _, k := range keys {
		b := rows[k]
		action := "—"
		if b.Request.Verb != "" {
			action = a.r.DisplayKey(a.r.CanonicalKey(b.Request.Key()))
			if b.When != "" {
				action += " [" + b.When + "]"
			}
		}
		live := "send to program"
		if e, ok := explicit[k]; ok {
			live = a.r.DisplayKey(a.r.CanonicalKey(e.Request.Key())) + " (explicit)"
		} else if b.Request.Verb != "" {
			op, _ := a.r.Operation(b.Request.Key())
			if op.Motion {
				live += " (motion)"
			} else if op.FocusBound {
				live += " (focus-bound)"
			} else {
				live = action + " (outside)"
			}
		}
		fmt.Fprintf(&out, "%s\t%s\t%s\n", k, action, live)
	}
	path := filepath.Join("testdata", "leader-inventory.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != out.String() {
		t.Fatalf("leader inventory changed:\n%s", out.String())
	}
}

func TestLiveFocusBoundOutsideKeysStayLive(t *testing.T) {
	t.Parallel()
	for _, k := range []string{"enter", "]", "i"} {
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
