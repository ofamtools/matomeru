package app

import (
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/ptyhost"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func TestPanesUXRailEnterLiveAndOptOut(t *testing.T) {
	if rep, err := CheckBindings(""); err != nil || len(rep.Errors) > 0 {
		t.Fatalf("CheckBindings errors: err=%v errors=%+v", err, rep.Errors)
	}

	a, d := demo(t)
	pane, ok := a.SelectedPane()
	if !ok {
		t.Fatal("expected initial selected pane")
	}

	// Default panes.railEnterMode="live": enter on a pane row enters ModeTerminal directly on siteScreen.
	d.Keys("enter")
	if a.Mode() != ModeTerminal || a.Focus() != siteScreen {
		t.Fatalf("enter on rail pane: mode=%q focus=%q, want mode=%q focus=%q", a.Mode(), a.Focus(), ModeTerminal, siteScreen)
	}
	if a.panes.activePage[pane.ID] != views.ScreenPageID {
		t.Fatalf("activePage = %q, want %q", a.panes.activePage[pane.ID], views.ScreenPageID)
	}

	// Detach and return to rail; l focuses TOC immediately.
	d.Keys("ctrl+a", "d", "h", "h")
	if a.Focus() != railTable || a.Mode() != ModeNormal {
		t.Fatalf("returned to rail: mode=%q focus=%q", a.Mode(), a.Focus())
	}
	d.Keys("l")
	if !strings.HasPrefix(a.Focus(), tocPrefix) {
		t.Fatalf("l from rail did not focus TOC: focus=%q", a.Focus())
	}

	// Moving onto a project group header and pressing enter focuses projectContextTable (NOT terminal).
	d.Keys("h", "g", "g")
	if _, _, onProj := a.selectedRailProject(); !onProj {
		t.Fatal("expected g g on grouped rail to focus a project header")
	}
	d.Keys("enter")
	if a.Mode() != ModeNormal || a.Focus() != projectContextTable {
		t.Fatalf("enter on project header: mode=%q focus=%q, want normal/%q", a.Mode(), a.Focus(), projectContextTable)
	}

	// Opt-out: panes.railEnterMode="toc" restores TOC jump on enter from a pane row.
	d.Keys("h")
	selectRow(a.rail(), data.RefText(pane.Ref()))
	a.cfg.userSettings[SetRailEnterMode] = RailEnterModeTOC
	a.reloadSettings()
	a.applySettings()

	d.Keys("enter")
	if a.Mode() != ModeNormal || !strings.HasPrefix(a.Focus(), tocPrefix) {
		t.Fatalf("railEnterMode=toc: mode=%q focus=%q, want normal/toc", a.Mode(), a.Focus())
	}
}

func TestPanesUXSideSplitVsGlobalScratch(t *testing.T) {
	a, d := demo(t)
	d.Send(resize(160, 36))
	host := newLaunchCaptureHost()
	a.panes.Host = host
	a.panes.Term = ptyhost.NewController(host)

	parent, ok := a.SelectedPane()
	if !ok {
		t.Fatal("expected selected pane")
	}

	// Enter live terminal on parent and populate a distinctive marker on main screen.
	d.Keys("enter")
	if a.Mode() != ModeTerminal || a.Focus() != siteScreen {
		t.Fatalf("enter main pane: mode=%q focus=%q", a.Mode(), a.Focus())
	}
	if f, ok := host.frames[parent.ID]; ok {
		f.Lines = []string{"MAIN_PANE_MARKER"}
		f.Plain = []string{"MAIN_PANE_MARKER"}
		f.Revision++
		host.frames[parent.ID] = f
	}
	d.Send(wakeMsg())

	// ctrl+a t opens a horizontal KindSplit beside parent WITHOUT switching the left rail.
	d.Keys("ctrl+a", "t")
	if a.Mode() != ModeTerminal || a.Focus() != sideScreen {
		t.Fatalf("ctrl+a t: mode=%q focus=%q, want terminal/%q (status=%q)", a.Mode(), a.Focus(), sideScreen, a.Status())
	}
	if sel, _ := a.SelectedPane(); sel == nil || sel.ID != parent.ID {
		t.Fatalf("ctrl+a t switched rail away from parent %q: got %v", parent.ID, sel)
	}
	sideTerm, ok := a.termPane(a.State())
	if !ok || sideTerm.ID == parent.ID || sideTerm.Text("role") != data.RoleSideTerm {
		t.Fatalf("termPane on sideScreen = %+v (ok=%v), want RoleSideTerm", sideTerm, ok)
	}
	if f, ok := host.frames[sideTerm.ID]; ok {
		f.Lines = []string{"SIDE_TERM_MARKER"}
		f.Plain = []string{"SIDE_TERM_MARKER"}
		f.Revision++
		host.frames[sideTerm.ID] = f
	}
	d.Send(wakeMsg())

	view := a.View(a.State())
	splitNode := view.Find("site.screen.split")
	if splitNode == nil || splitNode.Kind != ui.KindSplit || len(splitNode.Children) != 2 {
		t.Fatalf("expected horizontal KindSplit site.screen.split with 2 panels, got %+v", splitNode)
	}
	sidePanel := view.Find("site.side.panel")
	if view.Find(siteScreen) == nil || view.Find(sideScreen) == nil || sidePanel == nil {
		t.Fatalf("expected both site.screen and site.side.screen in view")
	}
	frame := d.Frame()
	if !strings.Contains(frame, "MAIN_PANE_MARKER") || !strings.Contains(frame, "SIDE_TERM_MARKER") || !strings.Contains(frame, "side term") {
		t.Fatalf("split frame missing main marker, side marker, or 'side term' badge:\n%s", frame)
	}

	// Typing on sideScreen sends bytes to sideTerm, not parent.
	host.written[parent.ID] = nil
	host.written[sideTerm.ID] = nil
	d.Type("x")
	if len(host.written[sideTerm.ID]) == 0 || len(host.written[parent.ID]) != 0 {
		t.Fatalf("typing on sideScreen wrote wrong pane: side=%v main=%v", host.written[sideTerm.ID], host.written[parent.ID])
	}

	// ctrl+a o switches focus back to site.screen while staying in ModeTerminal.
	d.Keys("ctrl+a", "o")
	if a.Mode() != ModeTerminal || a.Focus() != siteScreen {
		t.Fatalf("ctrl+a o (to main): mode=%q focus=%q", a.Mode(), a.Focus())
	}
	d.Type("m")
	if len(host.written[parent.ID]) == 0 {
		t.Fatal("typing on siteScreen after ctrl+a o did not write to main pane")
	}

	// ctrl+a o again returns focus to sideScreen.
	d.Keys("ctrl+a", "o")
	if a.Mode() != ModeTerminal || a.Focus() != sideScreen {
		t.Fatalf("ctrl+a o (to side): mode=%q focus=%q", a.Mode(), a.Focus())
	}

	// ctrl+a v swaps side split to RoleSideVim beside the SAME parent pane.
	d.Keys("ctrl+a", "v")
	if a.Mode() != ModeTerminal || a.Focus() != sideScreen {
		t.Fatalf("ctrl+a v: mode=%q focus=%q", a.Mode(), a.Focus())
	}
	sideVim, ok := a.termPane(a.State())
	if !ok || sideVim.Text("role") != data.RoleSideVim {
		t.Fatalf("expected RoleSideVim after ctrl+a v, got %+v", sideVim)
	}
	if pRef, _ := sideVim.RefField("parentPane"); pRef != parent.Ref() {
		t.Fatalf("side editor parentPane = %v, want parent %v", pRef, parent.Ref())
	}
	if !strings.Contains(d.Frame(), "side editor") {
		t.Fatalf("side editor badge missing:\n%s", d.Frame())
	}

	// ctrl+a S opens the floating global scratch overlay, distinct from side split.
	d.Keys("ctrl+a", "S")
	if a.Modal() != "scratch" || !strings.Contains(d.Frame(), "global scratch") {
		t.Fatalf("ctrl+a S did not open floating global scratch overlay: modal=%q\n%s", a.Modal(), d.Frame())
	}
	d.Keys("ctrl+a", "S")
	if a.Modal() != "" {
		t.Fatalf("ctrl+a S did not hide scratch overlay: modal=%q", a.Modal())
	}

	// ctrl+a x closes the side split and focuses site.screen.
	d.Keys("ctrl+a", "x")
	afterClose := a.View(a.State())
	if a.Focus() != siteScreen || afterClose.Find("site.screen.split") != nil {
		t.Fatalf("ctrl+a x did not close side split: focus=%q", a.Focus())
	}

	// Opt-out: panes.sideTermSplit=false falls back to legacy spawn (switches rail, no split).
	a.cfg.userSettings[SetSideTermSplit] = false
	a.reloadSettings()
	a.applySettings()

	d.Keys("ctrl+a", "t")
	if sel, _ := a.SelectedPane(); sel == nil || sel.ID == parent.ID {
		t.Fatalf("sideTermSplit=false did not switch rail to spawned side pane: sel=%v parent=%q", sel, parent.ID)
	}
	legacyView := a.View(a.State())
	if legacyView.Find("site.screen.split") != nil {
		t.Fatal("sideTermSplit=false should not render site.screen.split")
	}
}

func TestPanesUXProjectInheritanceOverviewAndOpen(t *testing.T) {
	a, d := demo(t)

	// Set a distinct defaultDir on the top-level matomeru-v2 project.
	projRec, err := a.Store.GetByField(data.KindTask, "title", "matomeru-v2")
	if err != nil {
		t.Fatal(err)
	}
	projDir := filepath.Join(t.TempDir(), "matomeru-root")
	if _, _, err := a.Store.Patch(data.PatchInput{
		Ref:    projRec.Ref(),
		Fields: map[string]any{"defaultDir": projDir},
	}, User); err != nil {
		t.Fatal(err)
	}

	// 1. Focused on child pane assigned to nested subtask ("Wave 5B: the four spaces"):
	//    creating a pane or task inherits TOP-LEVEL project task (matomeru-v2) + defaultDir.
	pane, ok := a.SelectedPane()
	if !ok {
		t.Fatal("expected selected pane")
	}
	if subRef, _ := pane.RefField("task"); subRef == projRec.Ref() {
		t.Fatal("expected demo pane to be assigned to a nested subtask, not root")
	}
	d.Keys("N") // new note
	rf := a.activeRecordForm()
	if rf == nil {
		t.Fatal("expected note form")
	}
	if gotTask, ok := asRefValue(rf.Model().Values["task"]); !ok || gotTask != projRec.Ref() {
		t.Fatalf("note created from subtask pane inherited task=%v, want top-level project %v", gotTask, projRec.Ref())
	}
	d.Keys("esc", "esc")

	// Creating a KindTask from SpacePanes inherits top-level project as parent.
	d.Send(core.CommandEvent{Command: core.Command{ID: views.CmdNew, Args: map[string]any{"kind": data.KindTask}, Source: "test", Actor: User}})
	rf = a.activeRecordForm()
	if rf == nil {
		t.Fatal("expected task form")
	}
	if gotParent, ok := asRefValue(rf.Model().Values["parent"]); !ok || gotParent != projRec.Ref() {
		t.Fatalf("task created from subtask pane inherited parent=%v, want top-level project %v", gotParent, projRec.Ref())
	}
	d.Keys("esc", "esc")

	// Creating a pane from a child pane with no explicit cwd inherits project's defaultDir and top-level project task.
	if _, _, err := a.Store.Patch(data.PatchInput{Ref: pane.Ref(), Fields: map[string]any{"cwd": ""}}, User); err != nil {
		t.Fatal(err)
	}
	d.Keys("n") // New Agent form
	rf = a.activeRecordForm()
	if rf == nil {
		t.Fatal("expected New Agent form")
	}
	if gotTask, ok := asRefValue(rf.Model().Values["task"]); !ok || gotTask != projRec.Ref() {
		t.Fatalf("pane created from subtask pane inherited task=%v, want %v", gotTask, projRec.Ref())
	}
	if gotCwd := data.Text(rf.Model().Values["cwd"]); gotCwd != projDir {
		t.Fatalf("pane created from subtask pane inherited cwd=%q, want %q", gotCwd, projDir)
	}
	d.Keys("esc", "esc")

	// 2. Focused on Project group header: projectOverviewView displays Documents + badge,
	//    and record.new inherits project task + defaultDir.
	d.Keys("g", "g")
	if groupName, _, onProj := a.selectedRailProject(); !onProj || groupName != "matomeru-v2" {
		t.Fatalf("expected g g on rail to select matomeru-v2 header, got onProj=%v group=%q", onProj, groupName)
	}
	ovFrame := d.Frame()
	if !strings.Contains(ovFrame, "Documents (1):") || !strings.Contains(ovFrame, "Matomeru v2 design") || !strings.Contains(ovFrame, "1 docs") {
		t.Fatalf("projectOverviewView missing Documents row or badge:\n%s", ovFrame)
	}

	d.Keys("n")
	rf = a.activeRecordForm()
	if rf == nil {
		t.Fatal("expected New Agent form from project header")
	}
	if gotTask, ok := asRefValue(rf.Model().Values["task"]); !ok || gotTask != projRec.Ref() {
		t.Fatalf("pane created from project header inherited task=%v, want %v", gotTask, projRec.Ref())
	}
	if gotCwd := data.Text(rf.Model().Values["cwd"]); gotCwd != projDir {
		t.Fatalf("pane created from project header inherited cwd=%q, want %q", gotCwd, projDir)
	}
	d.Keys("esc", "esc")

	// 3. P opens the top-level Project Show page from both ScopeRail and ScopeSite.
	d.Keys("P")
	if shown, ok := a.Showing(); !ok || shown != projRec.Ref() || a.Space() != SpaceWork {
		t.Fatalf("P from project header: space=%q showing=%v (ok=%v), want %v", a.Space(), shown, ok, projRec.Ref())
	}
	d.Keys("1")
	selectRow(a.rail(), data.RefText(pane.Ref()))
	a.viewMemo.gen++
	d.Keys("l", "P") // from ScopeSite (TOC)
	if shown, ok := a.Showing(); !ok || shown != projRec.Ref() || a.Space() != SpaceWork {
		t.Fatalf("P from ScopeSite: space=%q showing=%v (ok=%v), want %v", a.Space(), shown, ok, projRec.Ref())
	}

	// Unassigned pane reports "no project task assigned" on P.
	unassigned, _, err := a.Store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{"title": "loose-shell", "harness": "zsh"}}, User)
	if err != nil {
		t.Fatal(err)
	}
	d.Keys("1")
	a.rail()
	selectRow(a.rail(), data.RefText(unassigned.Ref()))
	a.viewMemo.gen++
	d.Keys("P")
	if a.Space() != SpacePanes || !strings.Contains(a.Status(), "no project task assigned") {
		t.Fatalf("P on unassigned pane: space=%q status=%q", a.Space(), a.Status())
	}

	// 4. Opt-out: panes.inheritProjectTask=false disables auto-inheritance from project header & subtask pane.
	a.cfg.userSettings[SetInheritProjectTask] = false
	a.reloadSettings()
	a.applySettings()

	if !selectGroupHeader(a.rail(), "matomeru-v2") {
		t.Fatal("could not select matomeru-v2 header")
	}
	a.viewMemo.gen++
	d.Send(core.CommandEvent{Command: core.Command{ID: views.CmdNew, Args: map[string]any{"kind": data.KindTask}, Source: "test", Actor: User}})
	rf = a.activeRecordForm()
	if rf == nil {
		t.Fatal("expected task form")
	}
	if _, hasParent := asRefValue(rf.Model().Values["parent"]); hasParent {
		t.Fatalf("inheritProjectTask=false should not inherit parent on project header, got %v", rf.Model().Values["parent"])
	}
	d.Keys("esc", "esc")
}

func TestPanesUXCardBorderStyleSubtleAndOptOut(t *testing.T) {
	a, d := demo(t)

	// Default ui.cardBorderStyle is "subtle" applied directly to Shell.Theme.CardBorderStyle.
	if a.Shell.Theme.CardBorderStyle != ui.CardBorderSubtle {
		t.Fatalf("default CardBorderStyle = %q, want %q", a.Shell.Theme.CardBorderStyle, ui.CardBorderSubtle)
	}

	// Open the Architecture page (which contains two KindCard option nodes) and focus a card.
	arch, err := a.Store.GetByField(data.KindPage, "slug", "architecture")
	if err != nil {
		t.Fatal(err)
	}
	a.Shell.State = a.jumpToPage(a.State(), arch.ID)
	d.Keys("enter", "tab") // enter reading mode, tab to first KindCard ("arch.opt.thin")
	if a.Focus() != "arch.opt.thin" {
		t.Fatalf("expected focus on arch.opt.thin card, got %q", a.Focus())
	}
	subtleFrame := d.Frame()
	// Subtle card border draws a quiet left rail (┃ on focus / │ unfocused) instead of 4-sided card boxes.
	if !strings.Contains(subtleFrame, "┃") {
		t.Fatalf("subtle card border style missing ┃ focus accent:\n%s", subtleFrame)
	}
	subtleCorners := strings.Count(subtleFrame, "╭")

	// Opt-out via live Settings form: switching ui.cardBorderStyle to "rounded" updates Theme immediately and restores card boxes.
	prevVer := a.Shell.Theme.Version
	d.Keys("esc", "S") // leave ModePage, open Settings
	if a.Modal() != "settings" {
		t.Fatalf("S did not open Settings: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	specs := SettingSpecs()
	for i := range specs {
		if specs[i].Name == SetCardBorderStyle {
			break
		}
		d.Keys("j")
	}
	d.Keys("l", "esc", "esc") // subtle -> rounded, close settings
	d.Keys("enter", "tab")    // re-enter Architecture page and focus card

	if a.Shell.Theme.CardBorderStyle != ui.CardBorderRounded {
		t.Fatalf("CardBorderStyle after setting = %q, want %q", a.Shell.Theme.CardBorderStyle, ui.CardBorderRounded)
	}
	if a.Shell.Theme.Version <= prevVer {
		t.Fatalf("expected Theme.Version to bump: before=%d after=%d", prevVer, a.Shell.Theme.Version)
	}
	roundedFrame := d.Frame()
	roundedCorners := strings.Count(roundedFrame, "╭")
	if roundedCorners <= subtleCorners {
		t.Fatalf("rounded card border style did not restore 4-sided card boxes (subtle ╭=%d, rounded ╭=%d):\n%s", subtleCorners, roundedCorners, roundedFrame)
	}
}

func TestDataLogQAMarkdownHint(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	dt := mcpui.NewDataTools(store)
	const wantHint = "Format answer as structured Markdown (headings, bullet lists, bold labels, fenced code blocks; never a flat prose wall)."
	for _, spec := range dt.ToolSpecs() {
		if spec.Name != mcpui.ToolLogQA {
			continue
		}
		if !strings.Contains(spec.Description, wantHint) {
			t.Fatalf("data.log_qa description missing Markdown hint: %q", spec.Description)
		}
		props, _ := spec.InputSchema["properties"].(map[string]any)
		ans, _ := props["answer"].(map[string]any)
		ansDesc, _ := ans["description"].(string)
		if !strings.Contains(ansDesc, wantHint) {
			t.Fatalf("data.log_qa answer field description missing Markdown hint: %q", ansDesc)
		}
		return
	}
	t.Fatal("data.log_qa spec not found")
}

func TestPanesUXDeadPaneExitClosesSideSplitDetachesFocusesRailAndSelectsNext(t *testing.T) {
	a, d := demo(t)
	first, ok := a.SelectedPane()
	if !ok {
		t.Fatal("expected initial selected pane")
	}
	rows := a.rail().Filtered()
	if len(rows) < 2 || rows[1].Ref == nil {
		t.Fatalf("expected at least 2 demo panes in rail, got %d", len(rows))
	}
	secondID := rows[1].Ref.ID

	// 1. Side term dies: closes side split, detaches from ModeTerminal, moves focus to left rail.
	d.Keys("enter")
	if a.Mode() != ModeTerminal || a.Focus() != siteScreen {
		t.Fatalf("expected live terminal before side split: mode=%q focus=%q", a.Mode(), a.Focus())
	}
	d.Keys("ctrl+a", "t")
	sideID := a.panes.sideSplit[first.ID]
	if sideID == "" || a.Focus() != sideScreen || a.Mode() != ModeTerminal {
		t.Fatalf("expected open side split: sideID=%q focus=%q mode=%q", sideID, a.Focus(), a.Mode())
	}
	a.Shell.State = a.handlePaneExited(a.State(), sideID, 0)
	if a.panes.sideSplit[first.ID] != "" {
		t.Fatalf("dead side term split was not closed: %v", a.panes.sideSplit)
	}
	if a.Mode() != ModeNormal || a.Focus() != railTable {
		t.Fatalf("dead side term exit: mode=%q focus=%q, want normal / %s", a.Mode(), a.Focus(), railTable)
	}
	if sel, ok := a.SelectedPane(); !ok || sel.ID != first.ID {
		t.Fatalf("after dead side term exit expected parent pane %q selected, got %v", first.ID, sel)
	}

	// 2. Main pane dies while attached: detaches, archives dead pane, focuses left rail, selects NEXT pane.
	d.Keys("enter")
	if a.Mode() != ModeTerminal || a.Focus() != siteScreen {
		t.Fatalf("expected live terminal on first pane: mode=%q focus=%q", a.Mode(), a.Focus())
	}
	a.Shell.State = a.handlePaneExited(a.State(), first.ID, 0)
	if a.Mode() != ModeNormal || a.Focus() != railTable {
		t.Fatalf("dead main pane exit: mode=%q focus=%q, want normal / %s", a.Mode(), a.Focus(), railTable)
	}
	if sel, ok := a.SelectedPane(); !ok || sel.ID != secondID {
		t.Fatalf("dead main pane did not advance rail to next pane %q, got %v", secondID, sel)
	}

	// 3. Opt-out terminal.focusRailOnExit=false keeps focus off railTable while still detaching.
	a.cfg.userSettings[SetFocusRailOnExit] = false
	a.reloadSettings()
	d.Keys("enter")
	if a.Mode() != ModeTerminal || a.Focus() != siteScreen {
		t.Fatalf("expected live terminal on second pane: mode=%q focus=%q", a.Mode(), a.Focus())
	}
	a.Shell.State = a.handlePaneExited(a.State(), secondID, 0)
	if a.Mode() != ModeNormal {
		t.Fatalf("opt-out should still detach to ModeNormal, got %q", a.Mode())
	}
	if a.Focus() != siteScreen {
		t.Fatalf("opt-out terminal.focusRailOnExit=false moved focus to %q, want %q", a.Focus(), siteScreen)
	}
}
