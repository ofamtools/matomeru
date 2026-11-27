package app

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
)

func TestW2ChromeRedesignNounFirstHintsAndOptOut(t *testing.T) {
	a, d := demo(t)
	a.rt.live.sysStats = &sysStatsSampler{}
	snap := SysStats{
		Ready:          true,
		ProcCPUPct:     3,
		HasProcCPU:     true,
		SysCPUPct:      14,
		HasSysCPU:      true,
		RAMUsedPct:     55,
		RAMTotalBytes:  64 * 1024 * 1024 * 1024,
		HasRAM:         true,
		DiskFreeBytes:  140 * 1024 * 1024 * 1024,
		DiskTotalBytes: 1024 * 1024 * 1024 * 1024,
		HasDisk:        true,
	}
	a.rt.live.sysStats.snap.Store(&snap)

	frame := d.Frame()
	lines := strings.Split(frame, "\n")
	if len(lines) < 4 {
		t.Fatalf("short frame:\n%s", frame)
	}
	// 1. Top tab bar omits leading numbers by default (ui.topChromeRedesign=true)
	// and hides legacy Panes/Work by default (ui.showLegacyPanesWorkPages=false).
	if strings.Contains(lines[0], "1 Projects") || strings.Contains(lines[0], "2 Runs") {
		t.Fatalf("redesigned top tab bar should omit leading numbers: %q", lines[0])
	}
	if !strings.Contains(lines[0], "Projects") || !strings.Contains(lines[0], "Runs") || !strings.Contains(lines[0], "Extensions") {
		t.Fatalf("top tab bar missing expected space names: %q", lines[0])
	}
	if strings.Contains(lines[0], "Panes") || strings.Contains(lines[0], "Work") {
		t.Fatalf("default top tab bar should hide legacy Panes/Work: %q", lines[0])
	}
	// 2. Chrome separator bar carries CPU/RAM/disk + context chain + MODE badge.
	root := a.View(a.State())
	sepNode := root.Find("chrome.sep")
	if sepNode == nil {
		t.Fatal("expected chrome.sep node when ui.topChromeRedesign=true")
	}
	sepText, _ := sepNode.Props["text"].(string)
	if !strings.Contains(sepText, "cpu 3%/14%") || !strings.Contains(sepText, "[NORMAL]") {
		t.Fatalf("chrome.sep missing stats or mode badge: %q", sepText)
	}

	// 3. Noun-first hint ordering puts noun-scoped bindings before global bindings.
	chain := []core.ContextFrame{
		{Scope: data.KindRun},
		{Scope: SpaceRuns},
		{Scope: "app"},
	}
	if hintScopeRank(data.KindRun, chain) >= hintScopeRank(SpaceRuns, chain) {
		t.Fatal("noun scope should outrank space scope")
	}
	if hintScopeRank(SpaceRuns, chain) >= hintScopeRank("", chain) {
		t.Fatal("space scope should outrank global scope")
	}

	// 4. Opt-out ui.topChromeRedesign=false restores numbered tabs and legacy bottom status.
	a.cfg.userSettings[SetTopChromeRedesign] = false
	a.reloadSettings()
	a.applySettings()
	d.Send(resize(120, 36))
	legacyRoot := a.View(a.State())
	if legacyRoot.Find("chrome.sep") != nil {
		t.Fatalf("opt-out should omit chrome.sep (enabled=%v)", a.topChromeRedesignEnabled())
	}
	legacyFrame := d.Frame()
	legacyLines := strings.Split(legacyFrame, "\n")
	if !strings.Contains(legacyLines[0], "1 Projects") {
		t.Fatalf("opt-out should restore numbered tabs (enabled=%v, tab0=%q): %q", a.topChromeRedesignEnabled(), legacyRoot.Find("space.projects").Title, legacyLines[0])
	}
}

func TestW2DetachLeftToTOCAndRightEntersLive(t *testing.T) {
	a, d := demoLegacy(t)
	// Start on Panes rail -> l moves to TOC first (NOT directly into live terminal).
	if a.Focus() != railTable || a.Mode() != ModeNormal {
		t.Fatalf("initial state focus=%q mode=%q", a.Focus(), a.Mode())
	}
	d.Keys("l")
	if !strings.HasPrefix(a.Focus(), tocPrefix) || a.Mode() != ModeNormal {
		t.Fatalf("l from rail should land on TOC in ModeNormal, got focus=%q mode=%q", a.Focus(), a.Mode())
	}
	// Second l from Screen in TOC enters live ModeTerminal.
	a.Shell.State = withFocus(a.State(), tocPrefix+views.ScreenPageID)
	d.Keys("l")
	if a.Mode() != ModeTerminal || a.Focus() != siteScreen {
		t.Fatalf("l from TOC Screen should enter live ModeTerminal, got focus=%q mode=%q", a.Focus(), a.Mode())
	}
	// Detach (ctrl+a d) with default detachFocusTarget="left" moves 1 component left to TOC.
	d.Keys("ctrl+a", "d")
	if a.Mode() != ModeNormal || a.Focus() != tocPrefix+views.ScreenPageID {
		t.Fatalf("detach (left) expected focus=%q mode=normal, got focus=%q mode=%q", tocPrefix+views.ScreenPageID, a.Focus(), a.Mode())
	}
	// Opt-out detachFocusTarget="rail" jumps directly to panes.rail.table.
	a.cfg.userSettings[SetDetachFocusTarget] = DetachFocusRail
	a.reloadSettings()
	a.applySettings()
	d.Keys("l") // back into live terminal
	d.Keys("ctrl+a", "d")
	if a.Mode() != ModeNormal || a.Focus() != railTable {
		t.Fatalf("detach (rail) expected focus=%q, got %q", railTable, a.Focus())
	}
}

func TestW2ProjectSectionPickAndCtrlWRegionJump(t *testing.T) {
	a, d := demo(t)
	proj, _, err := a.Store.Create(data.CreateInput{
		Kind:   data.KindTask,
		ID:     "w2-proj",
		Fields: map[string]any{"title": "W2 Portal Project"},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	pc := a.projectCtrl()
	pc.openTabs = append(pc.openTabs, proj.ID)
	pc.setSection(proj.ID, "home")
	d.Keys("4")
	a.Shell.State = withFocus(a.State(), projectNavPrefix+"home")

	// g s opens the Project Section quick-jump picker.
	projKey := a.activeProjectWorkspaceKey(a.State())
	d.Keys("g", "s")
	if a.Modal() != "project.sections" {
		t.Fatalf("g s did not open project.sections picker, modal=%q", a.Modal())
	}
	d.Keys("/")
	d.Type("lanes")
	d.Keys("enter")
	if pc.activeSection(projKey) != "lanes" {
		t.Fatalf("project.sections picker did not switch section to lanes, got %q (modal=%q status=%q focus=%q)", pc.activeSection(projKey), a.Modal(), a.Status(), a.Focus())
	}

	// ctrl+w l jumps focus right to project.content without toggling tree folds.
	a.Shell.State = withFocus(a.State(), projectNavPrefix+"lanes")
	d.Keys("ctrl+w", "l")
	if strings.HasPrefix(a.Focus(), projectNavPrefix) {
		t.Fatalf("ctrl+w l did not leave project.nav, focus=%q", a.Focus())
	}
}
