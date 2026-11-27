package app

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/ui"
)

func TestInputReadlinePriorityDoesNotOpenModalsAndOptOutRestoresLegacy(t *testing.T) {
	a, d, _, _ := setupInsertPickerFixture(t)

	// 1. ModeFormEdit: ctrl+f moves cursor right (readline) and NEVER opens a modal;
	// ctrl+l does NOT open a modal; alt+i / alt+p still open pickers.
	d.Keys("2", "n", "i")
	if a.Modal() != "form" || a.Mode() != ModeFormEdit {
		t.Fatalf("expected ModeFormEdit, got modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Type("ab")
	d.Keys("ctrl+a", "ctrl+f")
	if a.Modal() != "form" || a.Mode() != ModeFormEdit {
		t.Fatalf("ctrl+f in ModeFormEdit unexpectedly opened modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Type("X")
	gotTitle, _ := a.topFormModal().Model().Values["title"].(string)
	if gotTitle != "aXb" {
		t.Fatalf("ctrl+f did not move cursor right in ModeFormEdit: title=%q want %q", gotTitle, "aXb")
	}
	d.Keys("ctrl+l")
	if a.Modal() != "form" || a.Mode() != ModeFormEdit {
		t.Fatalf("ctrl+l in ModeFormEdit unexpectedly opened modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Keys("esc", "esc", "esc")

	// 2. ModeOutlineEdit: ctrl+f moves cursor right and NEVER opens file picker modal.
	d.Keys("2", "o")
	if a.Mode() != ModeOutlineEdit {
		t.Fatalf("expected ModeOutlineEdit, got %q", a.Mode())
	}
	d.Type("cd")
	d.Keys("ctrl+a", "ctrl+f")
	if a.Modal() != "" || a.Mode() != ModeOutlineEdit {
		t.Fatalf("ctrl+f in ModeOutlineEdit unexpectedly opened modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Type("Y")
	tm, _, ok := a.activeEditTree()
	if !ok || tm.Edit() == nil || tm.Edit().Value() != "cYd" {
		t.Fatalf("ctrl+f did not move cursor right in ModeOutlineEdit: val=%q", tm.Edit().Value())
	}
	d.Keys("ctrl+l")
	if a.Modal() != "" || a.Mode() != ModeOutlineEdit {
		t.Fatalf("ctrl+l in ModeOutlineEdit unexpectedly opened modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Keys("ctrl+c")

	// 3. ModeComposer: ctrl+f moves cursor right, ctrl+l and ctrl+o do NOT open modals.
	d.Keys("1", "c")
	cm := a.activeRichComposer()
	if cm == nil || a.Mode() != ModeComposer {
		t.Fatalf("expected ModeComposer, got modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Type("ef")
	d.Keys("ctrl+a", "ctrl+f")
	if a.Modal() != "composer" || a.Mode() != ModeComposer {
		t.Fatalf("ctrl+f in ModeComposer unexpectedly opened modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Type("Z")
	if cm.in.Value() != "eZf" {
		t.Fatalf("ctrl+f did not move cursor right in ModeComposer: val=%q want %q", cm.in.Value(), "eZf")
	}
	d.Keys("ctrl+l")
	if a.Modal() != "composer" {
		t.Fatalf("ctrl+l in ModeComposer unexpectedly opened modal=%q", a.Modal())
	}
	d.Keys("ctrl+o")
	if a.Modal() != "composer" {
		t.Fatalf("ctrl+o in ModeComposer unexpectedly opened modal=%q", a.Modal())
	}

	// 4. Opt-out input.readlinePriority=false restores legacy ctrl+f / ctrl+l modal triggers.
	a.cfg.userSettings[SetInputReadlinePriority] = false
	a.reloadSettings()
	d.Keys("ctrl+f")
	if a.Modal() != insertPickerModalID {
		t.Fatalf("opt-out input.readlinePriority=false: ctrl+f did not open insert picker, modal=%q", a.Modal())
	}
	d.Keys("esc", "esc", "esc")
}

func TestFilterInsertPageAndHalfPageMotions(t *testing.T) {
	a, d := demo(t)
	a.cfg.userSettings[SetWorkTasksLayout] = WorkTasksLayoutTable
	a.reloadSettings()

	for i := 0; i < 25; i++ {
		mkRec(t, a, data.KindTask, map[string]any{"title": fmt.Sprintf("filter-motion-item-%02d", i)})
	}

	// 1. ModeFilter on 2 Work table: ctrl+n/p, ctrl+d/u, ctrl+f/b move selection while staying in ModeFilter.
	d.Keys("2", "/")
	if a.Mode() != ModeFilter {
		t.Fatalf("expected ModeFilter, got %q", a.Mode())
	}
	d.Type("filter-motion-item")
	tbl := a.nav.tables[workTable]
	if tbl == nil || len(tbl.Filtered()) < 25 {
		t.Fatalf("expected >= 25 filtered rows, got %d", len(tbl.Filtered()))
	}
	if tbl.SelectedIdx != 0 {
		t.Fatalf("initial SelectedIdx = %d, want 0", tbl.SelectedIdx)
	}

	d.Keys("ctrl+n")
	if a.Mode() != ModeFilter || tbl.SelectedIdx != 1 {
		t.Fatalf("ctrl+n in ModeFilter: mode=%q sel=%d want 1", a.Mode(), tbl.SelectedIdx)
	}
	d.Keys("ctrl+p")
	if a.Mode() != ModeFilter || tbl.SelectedIdx != 0 {
		t.Fatalf("ctrl+p in ModeFilter: mode=%q sel=%d want 0", a.Mode(), tbl.SelectedIdx)
	}
	d.Keys("ctrl+d")
	afterHalfDown := tbl.SelectedIdx
	if a.Mode() != ModeFilter || afterHalfDown <= 1 || tbl.Query != "filter-motion-item" {
		t.Fatalf("ctrl+d in ModeFilter: mode=%q sel=%d query=%q", a.Mode(), afterHalfDown, tbl.Query)
	}
	d.Keys("ctrl+f")
	afterPageDown := tbl.SelectedIdx
	if a.Mode() != ModeFilter || afterPageDown <= afterHalfDown || tbl.Query != "filter-motion-item" {
		t.Fatalf("ctrl+f in ModeFilter: mode=%q sel=%d (was %d) query=%q", a.Mode(), afterPageDown, afterHalfDown, tbl.Query)
	}
	d.Keys("ctrl+u")
	afterHalfUp := tbl.SelectedIdx
	if a.Mode() != ModeFilter || afterHalfUp >= afterPageDown || tbl.Query != "filter-motion-item" {
		t.Fatalf("ctrl+u in ModeFilter: mode=%q sel=%d (was %d) query=%q", a.Mode(), afterHalfUp, afterPageDown, tbl.Query)
	}
	d.Keys("ctrl+b")
	afterPageUp := tbl.SelectedIdx
	if a.Mode() != ModeFilter || afterPageUp >= afterHalfUp || tbl.Query != "filter-motion-item" {
		t.Fatalf("ctrl+b in ModeFilter: mode=%q sel=%d (was %d) query=%q", a.Mode(), afterPageUp, afterHalfUp, tbl.Query)
	}
	d.Keys("ctrl+x")
	if tbl.Query != "" {
		t.Fatalf("ctrl+x did not clear filter query: got %q", tbl.Query)
	}
	d.Keys("esc")

	// 2. ModePickerFilter on Command Palette: ctrl+d / ctrl+u / ctrl+f / ctrl+b scroll picker list!
	d.Keys(":")
	pal := a.activePalette()
	if pal == nil || a.Mode() != ModePickerFilter {
		t.Fatalf("expected palette in ModePickerFilter: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Type("a")
	p0 := pal.Model().Picker.SelectedIdx
	d.Keys("ctrl+d")
	p1 := pal.Model().Picker.SelectedIdx
	if a.Mode() != ModePickerFilter || p1 <= p0 || pal.Model().Picker.Query != "a" {
		t.Fatalf("ctrl+d in ModePickerFilter: mode=%q sel=%d->%d query=%q", a.Mode(), p0, p1, pal.Model().Picker.Query)
	}
	d.Keys("ctrl+f")
	p2 := pal.Model().Picker.SelectedIdx
	if a.Mode() != ModePickerFilter || p2 <= p1 || pal.Model().Picker.Query != "a" {
		t.Fatalf("ctrl+f in ModePickerFilter: mode=%q sel=%d->%d query=%q", a.Mode(), p1, p2, pal.Model().Picker.Query)
	}
	d.Keys("ctrl+u")
	p3 := pal.Model().Picker.SelectedIdx
	if a.Mode() != ModePickerFilter || p3 >= p2 || pal.Model().Picker.Query != "a" {
		t.Fatalf("ctrl+u in ModePickerFilter: mode=%q sel=%d->%d query=%q", a.Mode(), p2, p3, pal.Model().Picker.Query)
	}
	d.Keys("ctrl+b")
	p4 := pal.Model().Picker.SelectedIdx
	if a.Mode() != ModePickerFilter || p4 >= p3 || pal.Model().Picker.Query != "a" {
		t.Fatalf("ctrl+b in ModePickerFilter: mode=%q sel=%d->%d query=%q", a.Mode(), p3, p4, pal.Model().Picker.Query)
	}
	d.Keys("esc", "esc", "esc")

	// 3. ModeBindingsFilter on Bindings editor: ctrl+d / ctrl+u / ctrl+f / ctrl+b scroll bindings table!
	d.Keys("B", "/")
	if a.Modal() != "bindings" || a.Mode() != ModeBindingsFilter {
		t.Fatalf("expected ModeBindingsFilter: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Type("a")
	b0, _ := a.bind.bindEd.Focused()
	d.Keys("ctrl+d")
	b1, _ := a.bind.bindEd.Focused()
	if a.Mode() != ModeBindingsFilter || b1 == b0 {
		t.Fatalf("ctrl+d in ModeBindingsFilter did not move selection: mode=%q idx=%d->%d", a.Mode(), b0, b1)
	}
	d.Keys("ctrl+u")
	b2, _ := a.bind.bindEd.Focused()
	if a.Mode() != ModeBindingsFilter || b2 != b0 {
		t.Fatalf("ctrl+u in ModeBindingsFilter did not move selection back: mode=%q idx=%d->%d (want %d)", a.Mode(), b1, b2, b0)
	}
	d.Keys("esc", "esc", "esc")
}

func TestProjectPortalSectionPickSyncsLeftNavPointer(t *testing.T) {
	f := setupNavTreeFixture(t)
	a, d := f.app, f.driver

	d.Keys("P")
	if !isProjectTab(a.Space()) || a.Focus() != projectNavPrefix+"home" {
		t.Fatalf("P did not open project portal at home: space=%q focus=%q", a.Space(), a.Focus())
	}
	projKey := a.activeProjectWorkspaceKey(a.State())
	pc := a.projectCtrl()

	// Jump via g s -> notes while starting on home.
	d.Keys("g", "s")
	if a.Modal() != "project.sections" {
		t.Fatalf("g s did not open section picker: modal=%q", a.Modal())
	}
	d.Keys("/")
	d.Type("notes")
	d.Keys("enter")

	if pc.activeSection(projKey) != "notes" {
		t.Fatalf("activeSection = %q, want notes", pc.activeSection(projKey))
	}
	if got := a.nav.lastFocus["project.nav"]; got != projectNavPrefix+"notes" {
		t.Fatalf("lastFocus[project.nav] = %q, want %q", got, projectNavPrefix+"notes")
	}
	root := a.View(a.State())
	notesNav := root.Find(projectNavPrefix + "notes")
	if notesNav == nil || notesNav.Facts["active"] != true {
		t.Fatalf("project.nav.notes node not marked active after g s: %+v", notesNav)
	}

	// Stepping left back into project.nav MUST land on project.nav.notes (NOT snap back to home!).
	d.Keys("ctrl+w", "h")
	if a.Focus() != projectNavPrefix+"notes" {
		t.Fatalf("ctrl+w h after g s notes landed on %q, want %q", a.Focus(), projectNavPrefix+"notes")
	}
	if pc.activeSection(projKey) != "notes" {
		t.Fatalf("returning to left nav reverted activeSection to %q, want notes", pc.activeSection(projKey))
	}

	// Jump again from inside project.content to documents via g s.
	d.Keys("ctrl+w", "l", "g", "s", "/")
	d.Type("documents")
	d.Keys("enter")
	if pc.activeSection(projKey) != "documents" || a.nav.lastFocus["project.nav"] != projectNavPrefix+"documents" {
		t.Fatalf("second g s jump: sec=%q lastFocus=%q", pc.activeSection(projKey), a.nav.lastFocus["project.nav"])
	}
	d.Keys("ctrl+w", "h")
	if a.Focus() != projectNavPrefix+"documents" {
		t.Fatalf("ctrl+w h after g s documents landed on %q, want %q", a.Focus(), projectNavPrefix+"documents")
	}
}

func TestNoImplicitEdgeFocusSpillAndCtrlONeverOpensModal(t *testing.T) {
	a, d := demo(t)
	if a.edgeFocusSpillEnabled() {
		t.Fatal("SetEdgeFocusSpill must default to false")
	}

	// 1. 1 Panes rail: repeated j and k at edges NEVER spill focus out of railTable.
	d.Keys("1")
	if a.Focus() != railTable {
		t.Fatalf("initial focus = %q, want %q", a.Focus(), railTable)
	}
	for i := 0; i < 30; i++ {
		d.Keys("j")
	}
	if a.Focus() != railTable {
		t.Fatalf("j at bottom of rail spilled focus to %q", a.Focus())
	}
	for i := 0; i < 30; i++ {
		d.Keys("k")
	}
	if a.Focus() != railTable {
		t.Fatalf("k at top of rail spilled focus to %q", a.Focus())
	}

	// 2. Project nav in 1 Panes (selected row 0 is a project header): j/k at edges NEVER spill out of project.nav.
	d.Keys("ctrl+w", "l")
	if !strings.HasPrefix(a.Focus(), projectNavPrefix) {
		t.Fatalf("expected focus in project.nav, got %q", a.Focus())
	}
	for i := 0; i < 30; i++ {
		d.Keys("j")
	}
	if !strings.HasPrefix(a.Focus(), projectNavPrefix) {
		t.Fatalf("j at bottom of project.nav spilled focus to %q", a.Focus())
	}
	for i := 0; i < 30; i++ {
		d.Keys("k")
	}
	if !strings.HasPrefix(a.Focus(), projectNavPrefix) {
		t.Fatalf("k at top of project.nav spilled focus to %q", a.Focus())
	}

	// 3. 1 Panes TOC (step back to rail, select first pane row, step right to TOC): j/k NEVER spill out of site.toc.
	d.Keys("ctrl+w", "h", "j", "ctrl+w", "l")
	if !strings.HasPrefix(a.Focus(), tocPrefix) {
		t.Fatalf("expected focus in TOC, got %q", a.Focus())
	}
	for i := 0; i < 20; i++ {
		d.Keys("j")
	}
	if !strings.HasPrefix(a.Focus(), tocPrefix) {
		t.Fatalf("j at bottom of TOC spilled focus to %q", a.Focus())
	}
	for i := 0; i < 20; i++ {
		d.Keys("k")
	}
	if !strings.HasPrefix(a.Focus(), tocPrefix) {
		t.Fatalf("k at top of TOC spilled focus to %q", a.Focus())
	}

	// 4. ctrl+o navigates jump history back and NEVER opens any modal.
	d.Keys("2", "3", "ctrl+o")
	if a.Modal() != "" || a.Space() != SpaceWork {
		t.Fatalf("ctrl+o after 2->3: modal=%q space=%q, want no modal and SpaceWork", a.Modal(), a.Space())
	}
	d.Keys("ctrl+o")
	if a.Modal() != "" || a.Space() != SpacePanes {
		t.Fatalf("second ctrl+o: modal=%q space=%q, want no modal and SpacePanes", a.Modal(), a.Space())
	}
}

func TestProjectNavSpaceTogglesFoldWithoutLeakingMark(t *testing.T) {
	f := setupNavTreeFixture(t)
	a, d := f.app, f.driver

	d.Keys("P")
	if a.Focus() != projectNavPrefix+"home" {
		t.Fatalf("expected focus on project.nav.home, got %q", a.Focus())
	}

	// 1. Pressing space on non-expandable Home must NOT leak to SelectCmdMarkToggle.
	d.Keys("space")
	for id, tbl := range a.nav.tables {
		if len(tbl.Marked) > 0 {
			t.Fatalf("space on non-expandable project.nav.home leaked mark to table %q (%d marked)", id, len(tbl.Marked))
		}
	}

	// 2. Pressing space on expandable Tasks section expands & collapses it.
	d.Keys("j")
	if a.Focus() != projectNavPrefix+"tasks" {
		t.Fatalf("expected focus on project.nav.tasks, got %q", a.Focus())
	}
	parentFocusID := projectNavNodePrefix + "tasks." + f.parentTask.ID
	subFocusID := projectNavNodePrefix + "tasks." + f.subTask.ID

	d.Keys("space")
	if findNode(a, parentFocusID) == nil {
		t.Fatalf("space did not expand Tasks section in project.nav")
	}

	// 3. Pressing space on expandable parent task node expands & collapses its subtask!
	d.Keys("j")
	if a.Focus() != parentFocusID {
		t.Fatalf("expected focus on %q, got %q", parentFocusID, a.Focus())
	}
	d.Keys("space")
	if findNode(a, subFocusID) == nil {
		t.Fatalf("space did not expand parent task node %q in project.nav", parentFocusID)
	}
	d.Keys("space")
	if findNode(a, subFocusID) != nil {
		t.Fatalf("second space did not collapse parent task node %q in project.nav", parentFocusID)
	}
}

func TestSplitResizeAndResetAcrossViews(t *testing.T) {
	a, d := demo(t)

	// 1. 1 Panes split: ctrl+w > widens panes.split (left rail focused), ctrl+w < narrows, ctrl+w = resets.
	d.Keys("1")
	splitBefore := findNode(a, "panes.split")
	if splitBefore == nil || splitBefore.Kind != ui.KindSplit {
		t.Fatalf("expected panes.split, got %+v", splitBefore)
	}
	baseRatio := splitBefore.Ratio

	d.Keys("ctrl+w", ">")
	widened := findNode(a, "panes.split").Ratio
	if widened <= baseRatio+0.01 {
		t.Fatalf("ctrl+w > did not widen panes.split: before=%.2f after=%.2f", baseRatio, widened)
	}

	d.Keys("ctrl+w", "<")
	narrowed := findNode(a, "panes.split").Ratio
	if math.Abs(narrowed-baseRatio) > 0.01 {
		t.Fatalf("ctrl+w < did not restore panes.split: got %.2f want %.2f", narrowed, baseRatio)
	}

	d.Keys("ctrl+w", "+")
	grown := findNode(a, "panes.split").Ratio
	if grown <= baseRatio+0.01 {
		t.Fatalf("ctrl+w + did not grow panes.split: before=%.2f after=%.2f", baseRatio, grown)
	}

	d.Keys("ctrl+w", "=")
	reset := findNode(a, "panes.split").Ratio
	if math.Abs(reset-baseRatio) > 0.01 {
		t.Fatalf("ctrl+w = did not reset panes.split: got %.2f want %.2f", reset, baseRatio)
	}

	// 2. Focus-aware resize on 3 Runs: focusing runs table and pressing ctrl+w > widens left ratio
	// and ctrl+w = resets back to default.
	d.Keys("3")
	runsSplit := findNode(a, "runs.split")
	if runsSplit == nil {
		t.Fatal("expected runs.split in 3 Runs")
	}
	d.Keys("ctrl+w", ">")
	if got := findNode(a, "runs.split").Ratio; got <= runsSplit.Ratio {
		t.Fatalf("ctrl+w > on runs table did not widen left ratio: %.2f -> %.2f", runsSplit.Ratio, got)
	}
	d.Keys("ctrl+w", "=")
	if got := findNode(a, "runs.split").Ratio; math.Abs(got-runsSplit.Ratio) > 0.01 {
		t.Fatalf("ctrl+w = on runs.split: got %.2f want %.2f", got, runsSplit.Ratio)
	}

	// 3. Opt-out ui.splitResize=false disables split resize cleanly.
	a.cfg.userSettings[SetSplitResize] = false
	a.reloadSettings()
	d.Keys("ctrl+w", ">")
	if got := findNode(a, "runs.split").Ratio; math.Abs(got-runsSplit.Ratio) > 0.01 {
		t.Fatalf("ui.splitResize=false allowed ratio change: got %.2f want %.2f", got, runsSplit.Ratio)
	}
}

func TestFocusHintsSkipsRedundantScrollWrapperInBothModes(t *testing.T) {
	f := setupNavTreeFixture(t)
	a, d := f.app, f.driver

	// Open Helios Questions page in Project Portal so site.content has page.scroll wrapping q-1 card.
	d.Keys("5", "enter", "j", "j", "l")
	qFocusID := projectNavNodePrefix + "pages." + f.qPage.ID
	a.Shell.State = withFocus(a.State(), qFocusID)
	a.viewMemo.gen++
	d.Keys("enter")
	if a.Mode() != ModePage {
		t.Fatalf("expected ModePage, got %q", a.Mode())
	}

	// Default direct mode: q-1 is targeted directly, NOT the layout wrapper page.scroll!
	d.Keys("g", "f")
	targets := a.panes.hints.session.Targets()
	if _, hasQ1 := findFocusTargetByID(targets, "q-1"); !hasQ1 {
		t.Fatalf("direct focus hints missing interactive card q-1: %+v", targets)
	}
	if _, hasScroll := findFocusTargetByID(targets, views.PageScrollID); hasScroll {
		t.Fatalf("direct focus hints should skip redundant layout wrapper %q when focusable descendants exist: %+v", views.PageScrollID, targets)
	}
	d.Keys("esc")

	// Opt-in drilldown mode: picking the Questions panel focuses q-1 directly (not stuck on page.scroll!).
	a.cfg.userSettings[SetFocusHintsDrilldown] = true
	a.reloadSettings()
	d.Keys("g", "f")
	panelTgt, ok := findFocusTargetByID(a.panes.hints.session.Targets(), "site.content")
	if !ok {
		t.Fatalf("drilldown missing site.content panel target: %+v", a.panes.hints.session.Targets())
	}
	d.Type(panelTgt.Label)
	if a.Focus() != "q-1" {
		t.Fatalf("drilldown on site.content focused %q, want q-1 (not layout wrapper page.scroll)", a.Focus())
	}
}
