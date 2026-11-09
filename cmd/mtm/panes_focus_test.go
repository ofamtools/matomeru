package main

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"ofam.dev/matomeru/internal/work"
)

func TestPaneFocusAttachSelectsDisplayedRow(t *testing.T) {
	t.Parallel()
	a, panes := sectionsApp(t)
	if err := a.r.SetFocus("pane:s/wnew"); err != nil {
		t.Fatal(err)
	}
	if err := a.attachLive(panes[4], "pane:s/wold"); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.selectedRow(); got != "pane:s/wold" {
		t.Fatalf("displayed s/oldest but sidebar selects %q", got)
	}
	if a.r.FocusID() != paneTermID || a.r.CurrentMode().Name != "terminal" {
		t.Fatalf("attach focus=%q mode=%q", a.r.FocusID(), a.r.CurrentMode().Name)
	}
	for range 2 {
		a.Update(nil)
		if got, _ := a.selectedRow(); got != "pane:s/wold" || a.r.FocusID() != paneTermID || a.r.CurrentMode().Name != "terminal" {
			t.Fatalf("unchanged update: selected=%q focus=%q mode=%q", got, a.r.FocusID(), a.r.CurrentMode().Name)
		}
	}
	press(t, a, "ctrl+a", "?")
	modalFocus := a.r.FocusID()
	if modalFocus == paneTermID {
		t.Fatal("help did not take focus from the terminal")
	}
	for range 2 {
		a.Update(nil)
		if got, _ := a.selectedRow(); got != "pane:s/wold" || a.r.FocusID() != modalFocus {
			t.Fatalf("modal update: selected=%q focus=%q, want %q", got, a.r.FocusID(), modalFocus)
		}
	}
	press(t, a, "esc", "q")
	if a.r.FocusID() != paneTermID || a.r.CurrentMode().Name != "terminal" {
		t.Fatalf("closing help: focus=%q mode=%q", a.r.FocusID(), a.r.CurrentMode().Name)
	}
}

func TestPaneFocusLiveAcrossCollapsedSections(t *testing.T) {
	t.Parallel()
	a, panes := sectionsApp(t)
	middle := a.pv.sections[1].tree
	for _, node := range middle.Nodes() {
		if node.Parent == "" {
			if err := middle.SetCollapsed(node.ID, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := a.r.SetFocus("pane:s/wold"); err != nil {
		t.Fatal(err)
	}
	press(t, a, "enter")
	for _, state := range []string{"idle", "working"} {
		panes[4].AgentState = state
		a.setPanes(panes, nil)
		if a.paneErr != "" {
			t.Fatal(a.paneErr)
		}
		if got, _ := a.selectedRow(); got != "pane:s/wold" {
			t.Fatalf("%s: displayed s/oldest but sidebar selects %q", state, got)
		}
		if a.r.FocusID() != paneTermID || a.r.CurrentMode().Name != "terminal" {
			t.Fatalf("%s: focus=%q mode=%q", state, a.r.FocusID(), a.r.CurrentMode().Name)
		}
		if fr := frameWH(a, 160, 40); !strings.Contains(fr, "s/oldest") {
			t.Fatalf("%s: displayed pane missing:\n%s", state, fr)
		}
	}
	press(t, a, "x")
	f := a.panesd.(*fakePanes)
	if len(f.streams) != 1 || len(f.streams[0].sent) != 1 || !slices.Equal(f.streams[0].sent[0], []string{"x"}) {
		t.Fatalf("terminal input lost: %+v", f.streams)
	}
	press(t, a, "ctrl+a", "d")
	if a.r.FocusID() != "pane:s/wold" || a.r.CurrentMode().Name != "normal" {
		t.Fatalf("detach focus=%q mode=%q", a.r.FocusID(), a.r.CurrentMode().Name)
	}
}

func TestPaneFocusOutsidePinSurvivesIdle(t *testing.T) {
	t.Parallel()
	a, panes := sectionsApp(t)
	if err := a.work.SetPaneOwner(work.Assignment{Window: "wold", Session: "s", ProjectID: "ochazuke"}); err != nil {
		t.Fatal(err)
	}
	a.pin = "matomeru"
	a.setPanes(panes, nil)
	if err := a.r.SetFocus("pane:s/wold"); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"idle", "working"} {
		panes[4].AgentState = state
		a.setPanes(panes, nil)
		if got, _ := a.selectedRow(); got != "pane:s/wold" || a.r.FocusID() != "pane:s/wold" {
			t.Fatalf("%s outside pin: selected=%q focus=%q", state, got, a.r.FocusID())
		}
		if state == "idle" && !strings.Contains(frameWH(a, 160, 40), "outside pin") {
			t.Fatal("retained pane is not labelled outside pin")
		}
	}
}

func TestPaneFocusBrowsingLeavesAttachedScreen(t *testing.T) {
	t.Parallel()
	a, panes := sectionsApp(t)
	if err := a.r.SetFocus("pane:s/wold"); err != nil {
		t.Fatal(err)
	}
	if err := a.attachLive(panes[4], "pane:s/wold"); err != nil {
		t.Fatal(err)
	}
	if err := a.r.SetFocus("pane:s/wnew"); err != nil {
		t.Fatal(err)
	}
	a.Update(nil)
	fr := frameWH(a, 160, 40)
	if !strings.Contains(fr, "s/newest") || strings.Contains(fr, "s/oldest") {
		t.Fatalf("browsing newest still displays the old attachment:\n%s", fr)
	}
	if a.r.FocusID() != "pane:s/wnew" || a.attached.ID != "wold" {
		t.Fatalf("browsing changed input ownership: focus=%q attached=%q", a.r.FocusID(), a.attached.ID)
	}
	if err := a.r.SetFocus(paneTermID); err != nil {
		t.Fatal(err)
	}
	a.Update(nil)
	if got, _ := a.selectedRow(); got != "pane:s/wold" {
		t.Fatalf("returning to the live terminal leaves sidebar on %q", got)
	}
	if fr := frameWH(a, 160, 40); !strings.Contains(fr, "s/oldest") || strings.Contains(fr, "s/newest") {
		t.Fatalf("returning to the live terminal displays another pane:\n%s", fr)
	}
	press(t, a, "ctrl+a", "d")
	if a.r.FocusID() != "pane:s/wold" || a.r.CurrentMode().Name != "normal" {
		t.Fatalf("detach after browsing: focus=%q mode=%q", a.r.FocusID(), a.r.CurrentMode().Name)
	}
}

func TestPaneFocusOnlyDisplayedBar(t *testing.T) {
	t.Parallel()
	a, panes := sectionsApp(t)
	a.m.Appearance.Theme = "mono"
	if err := a.r.SetFocus("pane:s/wnew"); err != nil {
		t.Fatal(err)
	}
	if err := a.attachLive(panes[3], "pane:s/wnew"); err != nil {
		t.Fatal(err)
	}
	reverse := regexp.MustCompile(`\x1b\[(?:[0-9]+;)*7(?:;[0-9]+)*m`)
	highlighted := func() []string {
		t.Helper()
		frameWH(a, 160, 40)
		var rows []string
		for _, line := range strings.Split(a.sectionsBlock(50, 35).Text, "\n") {
			if reverse.MatchString(line) {
				rows = append(rows, strings.TrimSpace(ansi.Strip(line)))
			}
		}
		return rows
	}
	rows := highlighted()
	if len(rows) != 1 || !strings.Contains(rows[0], "newest") {
		t.Fatalf("displayed newest must own the only sidebar bar, got %q", rows)
	}
	setting(t, a, "panes.remembered-highlights", true)
	if rows := highlighted(); len(rows) != 3 {
		t.Fatalf("remembered-highlight optout must restore each section's bar, got %q", rows)
	}
	setting(t, a, "panes.remembered-highlights", false)
	panes[3].AgentState = "idle"
	a.setPanes(panes, nil)
	if rows := highlighted(); len(rows) != 1 || !strings.Contains(rows[0], "newest") {
		t.Fatalf("moved displayed pane lost its bar, got %q", rows)
	}
}
