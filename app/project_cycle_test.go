package app

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func TestProjectPortalCyclePanesAndSectionsAnywhereIncludingLiveLeader(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	proj, _, err := store.Create(data.CreateInput{
		Kind: data.KindTask,
		Fields: map[string]any{
			"title":      "Portal Power",
			"defaultDir": "/tmp/portal-power",
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	p1, _, err := store.Create(data.CreateInput{
		Kind: data.KindPane,
		Fields: map[string]any{
			"title":     "pane-alpha",
			"harness":   "claude",
			"runStatus": "running",
			"task":      proj.Ref(),
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	p2, _, err := store.Create(data.CreateInput{
		Kind: data.KindPane,
		Fields: map[string]any{
			"title":     "pane-beta",
			"harness":   "codex",
			"runStatus": "running",
			"task":      proj.Ref(),
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	p3, _, err := store.Create(data.CreateInput{
		Kind: data.KindPane,
		Fields: map[string]any{
			"title":     "pane-gamma",
			"harness":   "zsh",
			"runStatus": "running",
			"task":      proj.Ref(),
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 120, 36)

	// Open the Project Portal tab.
	d.Send(core.CommandEvent{Command: core.Command{
		ID:    CmdProjectTabOpen,
		Args:  map[string]any{"project": proj.ID},
		Actor: User,
	}})
	tabID := projectTabID(proj.ID)
	if a.Space() != tabID {
		t.Fatalf("space = %q, want %q", a.Space(), tabID)
	}

	// Switch to Tasks section (NOT Panes!) and verify ` cycles panes from anywhere in the portal.
	d.Send(core.CommandEvent{Command: core.Command{
		ID:    CmdProjectSectionSelect,
		Args:  map[string]any{"section": "tasks"},
		Actor: User,
	}})
	d.Keys("`")
	firstPane := a.activeChildPaneID(a.State())
	if firstPane == "" || a.Mode() != ModeNormal {
		t.Fatalf("` from Tasks did not open project pane in ModeNormal: pane=%q mode=%q status=%q", firstPane, a.Mode(), a.Status())
	}
	if !strings.Contains(a.Status(), "pane 1/3:") {
		t.Fatalf("unexpected status after first `: %q", a.Status())
	}

	// Repeated ` cycles to the second pane, and ~ cycles back to the first.
	d.Keys("`")
	secondPane := a.activeChildPaneID(a.State())
	if secondPane == "" || secondPane == firstPane {
		t.Fatalf("second ` did not advance pane: first=%q second=%q", firstPane, secondPane)
	}
	if !strings.Contains(a.Status(), "pane 2/3:") {
		t.Fatalf("unexpected status after second `: %q", a.Status())
	}
	d.Keys("~")
	if got := a.activeChildPaneID(a.State()); got != firstPane {
		t.Fatalf("~ did not cycle back to %q, got %q", firstPane, got)
	}

	// Also works when focused in site.toc inside the embedded pane site!
	d.Keys("h")
	if !strings.HasPrefix(a.Focus(), tocPrefix) {
		t.Fatalf("expected focus in site.toc, got %q", a.Focus())
	}
	d.Keys("`")
	if got := a.activeChildPaneID(a.State()); got != secondPane {
		t.Fatalf("` from site.toc did not cycle to %q, got %q", secondPane, got)
	}

	// Attach live terminal (ModeTerminal) and use ctrl+a ` / ctrl+a ~ via leader fallthrough!
	d.Keys("enter")
	if a.Mode() != ModeTerminal {
		t.Fatalf("expected ModeTerminal after enter on site.screen, got %q", a.Mode())
	}
	d.Keys("ctrl+a", "`")
	thirdPane := a.activeChildPaneID(a.State())
	if a.Mode() != ModeTerminal {
		t.Fatalf("ctrl+a ` must stay in ModeTerminal, got %q", a.Mode())
	}
	if thirdPane == "" || thirdPane == secondPane || thirdPane == firstPane {
		t.Fatalf("ctrl+a ` did not advance to third pane: got %q (first=%q second=%q)", thirdPane, firstPane, secondPane)
	}
	if !strings.Contains(a.Status(), "pane 3/3:") {
		t.Fatalf("unexpected status after ctrl+a `: %q", a.Status())
	}

	// Wrap-around via ctrl+a ` goes back to firstPane, and ctrl+a ~ goes back to thirdPane.
	d.Keys("ctrl+a", "`")
	if got := a.activeChildPaneID(a.State()); got != firstPane || a.Mode() != ModeTerminal {
		t.Fatalf("wrap ctrl+a ` expected pane=%q ModeTerminal, got pane=%q mode=%q", firstPane, got, a.Mode())
	}
	d.Keys("ctrl+a", "~")
	if got := a.activeChildPaneID(a.State()); got != thirdPane || a.Mode() != ModeTerminal {
		t.Fatalf("ctrl+a ~ expected pane=%q ModeTerminal, got pane=%q mode=%q", thirdPane, got, a.Mode())
	}

	// Section cycling via ctrl+a g ] from inside live terminal detaches cleanly and advances section!
	d.Keys("ctrl+a", "g", "]")
	if a.Mode() != ModeNormal {
		t.Fatalf("ctrl+a g ] from live terminal should return to ModeNormal, got %q", a.Mode())
	}
	afterSec := a.projectCtrl().activeSection(proj.ID)
	if afterSec == "panes" {
		t.Fatalf("ctrl+a g ] did not advance away from panes section")
	}
	d.Keys("g", "[")
	if got := a.projectCtrl().activeSection(proj.ID); got != "panes" {
		t.Fatalf("g [ did not cycle back to panes section, got %q", got)
	}

	_, _, _ = p1, p2, p3
}
