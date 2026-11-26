package app

import (
	"testing"

	"ofam.dev/matomeru/data"
)

func TestIdleProjectPageZeroTabsRebuildOnSysStatsTicks(t *testing.T) {
	f := setupPerfWorkspaceFixture(t)
	a := f.app
	d := NewDriver(a, 120, 36)

	// Open Project 00 in a dedicated Project Tab and settle initial wake state.
	projID := f.projects[0].ID
	a.ensureProjectTabOpen(projID)
	d.Keys("6")
	a.Shell.Update(wakeMsg())
	_ = d.Frame()

	tabsBefore := a.viewMemo.tabsBuilds
	navBefore := a.projectCtrl().navBuilds

	// Simulate 10 idle sysStats chrome ticks while sitting on the Project Page.
	for range 10 {
		a.rt.live.sysStatsSeq.Add(1)
		a.Shell.Update(wakeMsg())
		_ = d.Frame()
	}

	if got := a.viewMemo.tabsBuilds - tabsBefore; got != 0 {
		t.Fatalf("idle sysStats ticks rebuilt tabs %d times, want 0 (chrome-only updates must reuse cached tabs)", got)
	}
	if got := a.projectCtrl().navBuilds - navBefore; got != 0 {
		t.Fatalf("idle sysStats ticks rebuilt project nav %d times, want 0", got)
	}
}

func TestProjectNavMoveReusesCachedNavTree(t *testing.T) {
	f := setupPerfWorkspaceFixture(t)
	a := f.app
	d := NewDriver(a, 120, 36)

	projID := f.projects[0].ID
	a.ensureProjectTabOpen(projID)
	d.Keys("6")
	_ = d.Frame()

	// Expand the Tasks section in the left nav tree so there are many child items to step across.
	pc := a.projectCtrl()
	pc.setNavSectionExpanded(projID, "tasks", true)
	d.Keys("j") // move from Home to Tasks section header
	_ = d.Frame()

	// Step down into the first child task node (switches activeKey to "tasks" once).
	d.Keys("j")
	_ = d.Frame()
	navAfterSectionEnter := pc.navBuilds

	// Now step up and down across 8 child task nodes inside the same section.
	for range 4 {
		d.Keys("j")
		_ = d.Frame()
	}
	for range 4 {
		d.Keys("k")
		_ = d.Frame()
	}

	if got := pc.navBuilds - navAfterSectionEnter; got != 0 {
		t.Fatalf("moving j/k across expanded nav items rebuilt nav tree %d times, want 0", got)
	}
	_ = data.KindTask
}
