package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func setupMultiProjectFixture(t *testing.T) (*App, *Driver, data.Record, data.Record, data.Record, data.Record) {
	t.Helper()
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	mk := func(kind string, fields map[string]any) data.Record {
		t.Helper()
		rec, _, err := store.Create(data.CreateInput{Kind: kind, Fields: fields}, User)
		if err != nil {
			t.Fatalf("create %s: %v", kind, err)
		}
		return *rec
	}

	now := time.Now().UTC()
	alpha := mk(data.KindTask, map[string]any{
		"title":       "Alpha",
		"description": "Oldest quiet project",
		"workStatus":  "active",
	})
	beta := mk(data.KindTask, map[string]any{
		"title":       "Beta",
		"description": "Active pane project",
		"repo":        "ofam/beta",
		"defaultDir":  "/tmp/beta",
		"workStatus":  "active",
	})
	gamma := mk(data.KindTask, map[string]any{
		"title":       "Gamma",
		"description": "Recently noted project",
		"workStatus":  "active",
	})
	betaSub := mk(data.KindTask, map[string]any{
		"title":      "Beta child task",
		"parent":     beta.Ref(),
		"workStatus": "todo",
	})
	betaPane := mk(data.KindPane, map[string]any{
		"title":      "claude: beta",
		"task":       betaSub.Ref(),
		"cwd":        "/tmp/beta",
		"runStatus":  "running",
		"promptedAt": now.Add(2 * time.Hour).Format(data.TimeFormat),
	})
	_ = mk(data.KindPage, map[string]any{
		"title":    "Beta Plan",
		"slug":     "beta-plan",
		"pageKind": "plan",
		"pane":     betaPane.Ref(),
		"task":     beta.Ref(),
		"root": ui.ViewNode{
			Kind:  ui.KindMarkdown,
			ID:    "beta.plan.md",
			Props: map[string]any{"markdown": "# Beta Plan\n\nShip dynamic tabs cleanly."},
		},
	})
	if _, _, err := store.AddOfficialProjectContext(data.OfficialContextInput{
		ProjectID: beta.ID,
		Title:     "Beta Spec",
		Value:     "https://docs.google.com/document/d/beta-spec/edit",
		Pinned:    true,
	}, User); err != nil {
		t.Fatal(err)
	}
	gammaNote := mk(data.KindNote, map[string]any{
		"title": "Gamma update",
		"body":  "Fresh note on Gamma",
		"task":  gamma.Ref(),
	})
	_ = gammaNote

	baseNow := now.Add(3 * time.Hour)
	a, err := New(store, Options{
		FS: smokeFS(),
		Now: func() time.Time {
			baseNow = baseNow.Add(time.Second)
			return baseNow
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a, NewDriver(a, 150, 40), alpha, beta, gamma, betaPane
}

func TestProjectsSpaceDirectorySortingAndLivePreview(t *testing.T) {
	a, d, alpha, beta, gamma, _ := setupMultiProjectFixture(t)

	// Press 5 to open permanent 5 Projects space.
	d.Keys("5")
	if a.Space() != SpaceProjects || a.Focus() != projectsDirTable {
		t.Fatalf("5 did not open Projects space: space=%q focus=%q", a.Space(), a.Focus())
	}

	root := a.View(a.State())
	split := root.Find("projects.split")
	if split == nil || split.Kind != ui.KindSplit || split.Ratio != 0.20 {
		t.Fatalf("expected 20/80 projects.split, got %+v", split)
	}
	if root.Find("projects.dir") == nil || root.Find("projects.preview") == nil {
		t.Fatal("expected projects.dir and projects.preview panels in 5 Projects")
	}

	tbl := a.nav.tables[projectsDirTable]
	if tbl == nil || len(tbl.Rows) != 3 {
		t.Fatalf("expected 3 top-level projects in projects.dir.table, got %+v", tbl)
	}
	// Beta has promptedAt +2h (most recent), then Gamma (has newest note), then Alpha.
	if tbl.Rows[0].Ref.ID != beta.ID {
		t.Fatalf("expected Beta first (most recent promptedAt), got %q", tbl.Rows[0].Ref.ID)
	}
	if tbl.Rows[1].Ref.ID != gamma.ID || tbl.Rows[2].Ref.ID != alpha.ID {
		t.Fatalf("expected order [Beta, Gamma, Alpha], got [%s, %s, %s]",
			tbl.Rows[0].Ref.ID, tbl.Rows[1].Ref.ID, tbl.Rows[2].Ref.ID)
	}

	// Right 80% live preview shows Beta's Home overview.
	frame := d.Frame()
	for _, want := range []string{"Projects", "Beta", "Active pane project", "Beta Spec", "WORKSPACE COUNTS"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("Projects preview for Beta missing %q:\n%s", want, frame)
		}
	}

	// Moving down highlights Gamma and immediately updates the live Home preview.
	d.Keys("j")
	gammaFrame := d.Frame()
	if !strings.Contains(gammaFrame, "Recently noted project") {
		t.Fatalf("moving cursor to Gamma did not update live Home preview:\n%s", gammaFrame)
	}

	// Opening Alpha bumps its lastUsedAt so returning to 5 Projects sorts Alpha first.
	d.Keys("j", "enter")
	if a.Space() != projectTabID(alpha.ID) {
		t.Fatalf("enter on Alpha did not open project tab: space=%q", a.Space())
	}
	d.Keys("5")
	tbl = a.nav.tables[projectsDirTable]
	if tbl == nil || len(tbl.Rows) == 0 || tbl.Rows[0].Ref.ID != alpha.ID {
		t.Fatalf("expected recently used Alpha to sort first, got %+v", tbl.Rows[0].Ref)
	}
}

func TestProjectTabWorkspaceSectionsOutlineAndQuickNote(t *testing.T) {
	f := setupProjectWorkspaceFixture(t)
	a, d := f.app, f.driver

	// Open 5 Projects and press enter on Apollo to open 6 Apollo dynamic tab.
	d.Keys("5", "enter")
	wantSpace := projectTabID(f.proj.ID)
	if a.Space() != wantSpace {
		t.Fatalf("enter on Apollo did not open dynamic Project Tab: space=%q want=%q", a.Space(), wantSpace)
	}

	root := a.View(a.State())
	tabsNode := root.Find("spaces")
	if tabsNode == nil || len(tabsNode.Children) != 6 {
		t.Fatalf("expected 6 top-bar tabs (1..5 + 6 Apollo), got %d", len(tabsNode.Children))
	}
	tab6 := tabsNode.Children[5]
	if tab6.Scope != wantSpace || !strings.Contains(tab6.Title, "Apollo") {
		t.Fatalf("unexpected 6th tab metadata: id=%q scope=%q title=%q", tab6.ID, tab6.Scope, tab6.Title)
	}
	tabInner := tab6.Find("project.tab." + f.proj.ID)
	if tabInner == nil || tabInner.Scope != views.ScopeProjectTab {
		t.Fatalf("expected inner ProjectTab scope wrapper, got %+v", tab6.Children)
	}
	if tabInner.Facts["isProjectTab"] != true || tabInner.Facts["canCloseTab"] != true {
		t.Fatalf("missing ProjectTab facts: %+v", tabInner.Facts)
	}

	// Verify 20/80 split, project name as nav title, and hidden redundant header.
	split := root.Find("project.split")
	if split == nil || split.Ratio != 0.20 {
		t.Fatalf("expected 0.20 project.split in Project Tab, got %+v", split)
	}
	if root.Find("project.header") != nil {
		t.Fatal("expected project.header to be hidden inside Project Tab")
	}
	nav := root.Find("project.nav")
	if nav == nil || nav.Title != "Apollo" {
		t.Fatalf("expected project.nav title %q, got %+v", "Apollo", nav)
	}

	// Verify all 14 sections render cleanly inside 6 Apollo.
	for _, sec := range defaultProjectNavSections {
		d.Send(core.CommandEvent{Command: core.Command{
			ID:    CmdProjectSectionSelect,
			Args:  map[string]any{"section": sec.Key},
			Actor: User,
		}})
		if got := a.projectCtrl().activeSection(f.proj.ID); got != sec.Key {
			t.Fatalf("section %q not activated: got %q", sec.Key, got)
		}
		secRoot := a.View(a.State())
		if secRoot.Find("project.content") == nil {
			t.Fatalf("section %q missing project.content", sec.Key)
		}
	}

	// Verify embedded project.tasks.tree outline works inside 6 Apollo.
	d.Send(core.CommandEvent{Command: core.Command{
		ID:    CmdProjectSectionSelect,
		Args:  map[string]any{"section": "tasks"},
		Actor: User,
	}})
	if a.Focus() != projectOutlineTree {
		t.Fatalf("tasks section in Project Tab focus = %q, want %q", a.Focus(), projectOutlineTree)
	}
	if frame := d.Frame(); !strings.Contains(frame, "Build nav shell") {
		t.Fatalf("tasks outline in Project Tab missing child task:\n%s", frame)
	}

	// Verify n quick add-note on Home opens Universal Rich Composer and attaches note to project.
	d.Send(core.CommandEvent{Command: core.Command{
		ID:    CmdProjectSectionSelect,
		Args:  map[string]any{"section": "home"},
		Actor: User,
	}})
	d.Keys("n")
	if a.Modal() != taskNoteComposerModalID || a.Mode() != ModeComposer {
		t.Fatalf("n on Home did not open note composer: modal=%q mode=%q status=%q", a.Modal(), a.Mode(), a.Status())
	}
	d.Type("Quick note attached from Project Tab")
	d.Keys("ctrl+s")
	if a.Modal() != "" {
		t.Fatalf("ctrl+s did not submit/close project note composer: modal=%q status=%q", a.Modal(), a.Status())
	}

	// Switch to Notes section and confirm the new note is attached to Apollo.
	d.Send(core.CommandEvent{Command: core.Command{
		ID:    CmdProjectSectionSelect,
		Args:  map[string]any{"section": "notes"},
		Actor: User,
	}})
	if notesFrame := d.Frame(); !strings.Contains(notesFrame, "Quick note attached from Project Tab") {
		t.Fatalf("attached project note not shown in Notes section:\n%s", notesFrame)
	}
}

func TestProjectTabInlineChildPaneView(t *testing.T) {
	a, d, _, beta, _, betaPane := setupMultiProjectFixture(t)

	// Open 5 Projects -> enter on Beta (first row) -> opens 6 Beta.
	d.Keys("5", "enter")
	tabSpace := projectTabID(beta.ID)
	if a.Space() != tabSpace {
		t.Fatalf("expected %q, got %q", tabSpace, a.Space())
	}

	// Navigate to Panes section and open betaPane inline.
	d.Send(core.CommandEvent{Command: core.Command{
		ID:    CmdProjectSectionSelect,
		Args:  map[string]any{"section": "panes"},
		Actor: User,
	}})
	if a.Focus() != projectPanesTree {
		t.Fatalf("panes section focus = %q, want %q", a.Focus(), projectPanesTree)
	}
	d.Keys("enter")

	// Must stay inside 6 Beta with child pane rendered inline in project.content
	// and attached directly in ModeTerminal when panes.railEnterMode == "live" (default).
	if a.Space() != tabSpace {
		t.Fatalf("enter on pane left Project Tab: space=%q want=%q", a.Space(), tabSpace)
	}
	if got := a.activeChildPaneID(a.State()); got != betaPane.ID {
		t.Fatalf("activeChildPaneID = %q, want %q", got, betaPane.ID)
	}
	if a.Mode() != ModeTerminal {
		t.Fatalf("enter on pane in Project Tab with railEnterMode=live: mode=%q, want %q", a.Mode(), ModeTerminal)
	}
	frame := d.Frame()
	for _, want := range []string{"Beta", "Screen", "Beta Plan"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("inline child pane frame missing %q:\n%s", want, frame)
		}
	}

	// Type in the attached live terminal screen and detach via ctrl+a d: stays in Project Tab child pane!
	d.Type("echo inline")
	d.Keys("enter", "ctrl+a", "d")
	if a.Mode() != ModeNormal || a.Space() != tabSpace || a.activeChildPaneID(a.State()) != betaPane.ID {
		t.Fatalf("ctrl+a d did not stay in Project Tab child pane: mode=%q space=%q child=%q",
			a.Mode(), a.Space(), a.activeChildPaneID(a.State()))
	}

	// Press esc in ModeNormal -> returns cleanly to project.panes.tree without leaving 6 Beta.
	d.Keys("esc")
	if a.Space() != tabSpace || a.activeChildPaneID(a.State()) != "" || a.Focus() != projectPanesTree {
		t.Fatalf("esc did not close inline child pane cleanly: space=%q child=%q focus=%q",
			a.Space(), a.activeChildPaneID(a.State()), a.Focus())
	}

	// With panes.railEnterMode == "toc", enter opens inline child pane in ModeNormal (TOC/screen focus).
	a.cfg.userSettings[SetRailEnterMode] = RailEnterModeTOC
	a.reloadSettings()
	a.applySettings()
	d.Keys("enter")
	if a.activeChildPaneID(a.State()) != betaPane.ID || a.Mode() != ModeNormal {
		t.Fatalf("railEnterMode=toc enter on pane: child=%q mode=%q, want ModeNormal", a.activeChildPaneID(a.State()), a.Mode())
	}
	if a.Focus() == siteScreen {
		d.Keys("h")
		if !strings.HasPrefix(a.Focus(), tocPrefix) {
			t.Fatalf("h from inline site.screen did not focus inline site.toc: focus=%q", a.Focus())
		}
	}
	d.Keys("h")
	if a.Space() != tabSpace || a.activeChildPaneID(a.State()) != "" || a.Focus() != projectPanesTree {
		t.Fatalf("h from inline TOC did not close child pane cleanly: space=%q child=%q focus=%q",
			a.Space(), a.activeChildPaneID(a.State()), a.Focus())
	}
}

func TestMultipleProjectTabsNavigationClosingAndOptOuts(t *testing.T) {
	a, d, _, beta, gamma, betaPane := setupMultiProjectFixture(t)

	// Open 6 Beta, then return to 5 Projects and open 7 Gamma.
	d.Keys("5", "enter")
	betaTab := projectTabID(beta.ID)
	if a.Space() != betaTab {
		t.Fatalf("expected %q, got %q", betaTab, a.Space())
	}
	d.Keys("5", "j", "enter")
	gammaTab := projectTabID(gamma.ID)
	if a.Space() != gammaTab {
		t.Fatalf("expected %q, got %q", gammaTab, a.Space())
	}
	if frame := d.Frame(); !strings.Contains(frame, "Beta") || !strings.Contains(frame, "Gamma") {
		t.Fatalf("top bar missing Beta / Gamma:\n%s", frame)
	}

	// Switch with digits 5, 6, 7 and [ / ] cycling.
	d.Keys("5")
	if a.Space() != SpaceProjects {
		t.Fatalf("5 -> %q, want %q", a.Space(), SpaceProjects)
	}
	d.Keys("6")
	if a.Space() != betaTab {
		t.Fatalf("6 -> %q, want %q", a.Space(), betaTab)
	}
	d.Keys("7")
	if a.Space() != gammaTab {
		t.Fatalf("7 -> %q, want %q", a.Space(), gammaTab)
	}
	d.Keys("]")
	if a.Space() != SpacePanes {
		t.Fatalf("] from last dynamic tab -> %q, want %q", a.Space(), SpacePanes)
	}
	d.Keys("[")
	if a.Space() != gammaTab {
		t.Fatalf("[ from Panes -> %q, want %q", a.Space(), gammaTab)
	}

	// Close 7 Gamma with x -> returns cleanly to 5 Projects while 6 Beta stays open.
	d.Keys("x")
	if a.Space() != SpaceProjects {
		t.Fatalf("x on 7 Gamma -> %q, want %q", a.Space(), SpaceProjects)
	}
	if len(a.projectCtrl().openTabs) != 1 || a.projectCtrl().openTabs[0] != beta.ID {
		t.Fatalf("openTabs after closing Gamma = %v, want [%s]", a.projectCtrl().openTabs, beta.ID)
	}

	// Opt-out 1: projects.childPaneView.enabled = false -> enter on pane jumps to 1 Panes.
	a.cfg.userSettings[SetProjectsChildPaneView] = false
	a.reloadSettings()
	a.applySettings()
	d.Keys("6")
	d.Send(core.CommandEvent{Command: core.Command{
		ID:    CmdProjectSectionSelect,
		Args:  map[string]any{"section": "panes"},
		Actor: User,
	}})
	d.Keys("enter")
	if a.Space() != SpacePanes {
		t.Fatalf("childPaneView=false should jump to 1 Panes, got space=%q", a.Space())
	}
	if sel, ok := a.SelectedPane(); !ok || sel == nil || sel.ID != betaPane.ID {
		t.Fatalf("expected betaPane selected in 1 Panes, got %v", sel)
	}
	// Exit terminal mode after jumpToPane(..., true).
	d.Keys("ctrl+a", "d")

	// Opt-out 2: projects.dynamicTabs.enabled = false -> 6+ tabs hidden, enter in 5 Projects falls back to projectHomeOpen.
	a.cfg.userSettings[SetProjectsDynamicTabs] = false
	a.reloadSettings()
	a.applySettings()
	d.Keys("5", "g", "g")
	if strings.Contains(d.Frame(), "6 Beta") {
		t.Fatalf("dynamicTabs=false should hide 6 Beta tab:\n%s", d.Frame())
	}
	d.Keys("enter")
	if a.Space() != SpacePanes {
		t.Fatalf("dynamicTabs=false enter in 5 Projects should fall back to 1 Panes project overview, got %q", a.Space())
	}

	// Opt-out 3: projects.topNavTab.enabled = false -> 5 Projects hidden, ] from 4 Extensions wraps to 1 Panes.
	a.cfg.userSettings[SetProjectsTopNavTab] = false
	a.reloadSettings()
	a.applySettings()
	d.Keys("4")
	if strings.Contains(d.Frame(), "5 Projects") {
		t.Fatalf("topNavTab=false should hide 5 Projects tab:\n%s", d.Frame())
	}
	d.Keys("]")
	if a.Space() != SpacePanes {
		t.Fatalf("topNavTab=false ] from Extensions -> %q, want %q", a.Space(), SpacePanes)
	}
}
