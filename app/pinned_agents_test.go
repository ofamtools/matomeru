package app

import (
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func TestPinnedAgentsPinUnpinJumpCycleSendAndChrome(t *testing.T) {
	f := setupCaptureFixture(t)
	f.now = time.Date(2026, 11, 27, 16, 0, 0, 0, time.UTC)

	// Initially no pinned agent -> chrome chip empty.
	if chip := f.app.pinnedAgentChromeChip(); chip != "" {
		t.Fatalf("expected empty pinned chrome chip initially, got %q", chip)
	}

	// Pin paneA then paneB (paneB becomes most recently pinned & active).
	f.driver.Send(core.CommandEvent{Command: core.Command{ID: CmdPanePin, Args: map[string]any{"pane": f.paneA.ID}, Actor: User}})
	f.now = f.now.Add(time.Minute)
	f.driver.Send(core.CommandEvent{Command: core.Command{ID: CmdPanePin, Args: map[string]any{"pane": f.paneB.ID}, Actor: User}})

	active, idx, total := f.app.activePinnedPane()
	if active == nil || active.ID != f.paneB.ID || idx != 0 || total != 2 {
		t.Fatalf("activePinnedPane = (%v, %d, %d), want (%s, 0, 2)", active, idx, total, f.paneB.ID)
	}
	if chip := f.app.pinnedAgentChromeChip(); !strings.Contains(chip, "★ codex-beta (1/2)") {
		t.Fatalf("chrome chip = %q, want ★ codex-beta (1/2)", chip)
	}

	// Opt-out setting hides the chrome chip cleanly.
	f.setting(SetPinnedAgentChrome, false)
	if chip := f.app.pinnedAgentChromeChip(); chip != "" {
		t.Fatalf("expected empty chrome chip when ui.pinnedAgentChrome=false, got %q", chip)
	}
	f.setting(SetPinnedAgentChrome, true)

	// From 2 Work, g k jumps straight into active pinned agent's live view (ModeTerminal).
	f.driver.Keys("2")
	f.driver.Keys("g", "k")
	if f.app.Mode() != ModeTerminal {
		t.Fatalf("g k mode = %q, want %q", f.app.Mode(), ModeTerminal)
	}
	if activePane, ok := f.app.activePane(f.app.State()); !ok || activePane.ID != f.paneB.ID {
		t.Fatalf("g k activePane = %v (ok=%v), want %s", activePane, ok, f.paneB.ID)
	}

	// Inside live ModeTerminal, leader-mirrored ctrl+a g > cycles to next pinned agent (paneA)!
	f.driver.Keys("ctrl+a", "g", ">")
	if f.app.Mode() != ModeTerminal {
		t.Fatalf("ctrl+a g > mode = %q, want %q", f.app.Mode(), ModeTerminal)
	}
	if active, _, _ := f.app.activePinnedPane(); active == nil || active.ID != f.paneA.ID {
		t.Fatalf("after cycle next active pinned = %v, want %s", active, f.paneA.ID)
	}

	// ctrl+a g < cycles backward back to paneB!
	f.driver.Keys("ctrl+a", "g", "<")
	if active, _, _ := f.app.activePinnedPane(); active == nil || active.ID != f.paneB.ID {
		t.Fatalf("after cycle prev active pinned = %v, want %s", active, f.paneB.ID)
	}

	// ctrl+a g K opens Capture Compose modal pre-targeted at active pinned agent (paneB).
	f.driver.Keys("ctrl+a", "g", "K")
	if f.app.Modal() != captureComposeModalID {
		t.Fatalf("ctrl+a g K modal = %q, want %q", f.app.Modal(), captureComposeModalID)
	}
	f.driver.Type("Direct to pinned codex")
	f.driver.Keys("ctrl+s")
	if got := strings.Join(f.host.writes[f.paneB.ID], ""); !strings.Contains(got, "Direct to pinned codex") {
		t.Fatalf("expected pinned paneB to receive prompt via g K, got %q", got)
	}

	// Detach & verify rail shows ★ decoration on pinned panes.
	f.driver.Keys("ctrl+a", "d")
	f.driver.Keys("1")
	rail := f.app.rail()
	foundStar := false
	for _, r := range rail.Rows {
		if r.Ref != nil && r.Ref.ID == f.paneB.ID && r.Facts["pinned"] == true && strings.Contains(data.Text(r.Cells["rail"]), "★") {
			foundStar = true
		}
	}
	if !foundStar {
		t.Fatalf("expected ★ marker and pinned fact on paneB rail row")
	}

	// Toggle pin via p on the rail unpins the focused pane, shifting active pinned to the remaining one.
	selectRow(rail, data.RefText(f.paneB.Ref()))
	f.driver.Keys("p")
	recB, _ := f.app.Store.Get(f.paneB.Ref())
	if recB.Bool("pinned") {
		t.Fatal("expected p on rail to unpin paneB")
	}
	if active, _, total := f.app.activePinnedPane(); active == nil || active.ID != f.paneA.ID || total != 1 {
		t.Fatalf("after unpinning paneB, activePinnedPane = (%v, total=%d), want (%s, 1)", active, total, f.paneA.ID)
	}
}

func TestPinnedAgentJumpFallbackToPanesSpaceWhenUnassigned(t *testing.T) {
	f := setupCaptureFixture(t)
	unassigned := f.mk(data.KindPane, map[string]any{"title": "solo-agent", "harness": "claude", "runStatus": "idle"})
	f.host.sessions[unassigned.ID] = true

	f.driver.Send(core.CommandEvent{Command: core.Command{ID: CmdPanePin, Args: map[string]any{"pane": unassigned.ID}, Actor: User}})
	f.driver.Keys("2")
	f.driver.Keys("g", "k")

	if f.app.Space() != SpacePanes || f.app.Mode() != ModeTerminal {
		t.Fatalf("unassigned jump landed on space=%q mode=%q, want panes/terminal", f.app.Space(), f.app.Mode())
	}
	if p, ok := f.app.SelectedPane(); !ok || p.ID != unassigned.ID {
		t.Fatalf("selected pane = %v (ok=%v), want %s", p, ok, unassigned.ID)
	}
}
