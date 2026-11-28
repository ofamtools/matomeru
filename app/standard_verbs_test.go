package app

import (
	"slices"
	"testing"

	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/widgets/hints"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/tree"
)

func execCmd(d *Driver, id string, args map[string]any) {
	d.Send(core.CommandEvent{Command: core.Command{ID: id, Args: args, Source: "test", Actor: User}})
}

func TestStandardVerbsRegistrationAndLegacyKeepActions(t *testing.T) {
	rep, err := CheckBindings("")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings(\"\") reported %d error(s): %+v", len(rep.Errors), rep.Errors)
	}

	specs := CommandSpecs()
	wantIDs := []string{
		// Canonical standard verbs.
		CmdStandardCycle,
		CmdStandardFold,
		CmdStandardToggle,
		// Keep-actions: every legacy verb ID must stay registered.
		CmdViewCycle,
		cmdQueueTabCycle,
		cmdFilesTabCycle,
		CmdJobsTab,
		CmdHintsActionCycle,
		CmdWorkLayoutToggle,
		tree.CmdFoldToggle,
		tree.CmdFoldOpen,
		tree.CmdFoldClose,
		tree.CmdFoldOpenAll,
		tree.CmdFoldCloseAll,
		CmdShowFoldToggle,
		CmdTOCGroupToggle,
		CmdProjectNavToggle,
		CmdPanesGroupToggle,
		CmdSiteTOCToggle,
		CmdProjectNavNewItemsToggle,
		CmdGroupToggle,
	}
	for _, want := range wantIDs {
		if !slices.ContainsFunc(specs, func(s core.CommandSpec) bool { return s.ID == want }) {
			t.Errorf("CommandSpecs missing %q", want)
		}
	}
}

func TestStandardCycleDirectionWrappingAndTargeting(t *testing.T) {
	t.Run("work index and show focused + targeted", func(t *testing.T) {
		a, d := demoLegacy(t)
		d.Keys("2")
		_ = a.nav.presets.Save(workTable, table.Preset{Name: "p-one", Filter: "tier"})
		_ = a.nav.presets.Save(workTable, table.Preset{Name: "p-two", Group: "workStatus"})

		// Focused forward wrap: tree -> table -> p-one -> p-two -> tree.
		steps := []struct {
			dir        any
			wantTree   bool
			wantPreset string
		}{
			{"next", false, ""},
			{"next", false, "p-one"},
			{"next", false, "p-two"},
			{"next", true, ""},
			// Backward wrap: tree -> p-two -> p-one -> table -> tree.
			{"prev", false, "p-two"},
			{"prev", false, "p-one"},
			{-1, false, ""},
			{"prev", true, ""},
		}
		for i, tc := range steps {
			execCmd(d, CmdStandardCycle, map[string]any{"dir": tc.dir})
			if a.inWorkTree(a.State()) != tc.wantTree || a.nav.activePreset[workTable] != tc.wantPreset {
				t.Fatalf("step %d (dir=%v): inTree=%v preset=%q, want inTree=%v preset=%q",
					i, tc.dir, a.inWorkTree(a.State()), a.nav.activePreset[workTable], tc.wantTree, tc.wantPreset)
			}
		}

		// Show page: cycle toggles compact <-> full even when targeting "show" explicitly.
		d.Keys("E")
		if _, ok := a.Showing(); !ok {
			t.Fatal("expected Show page to be open")
		}
		execCmd(d, CmdStandardCycle, map[string]any{"target": "show"})
		if !a.nav.showCompact {
			t.Fatal("cycle target=show did not enable compact view")
		}
		execCmd(d, CmdStandardCycle, map[string]any{"target": "show"})
		if a.nav.showCompact {
			t.Fatal("second cycle target=show did not restore full view")
		}
	})

	t.Run("explicit ViewNode.ByID targeting while another node is focused", func(t *testing.T) {
		a, d := demoLegacy(t)
		d.Keys("1")
		// Focus is on panes.rail.table; explicitly target site.toc via ViewNode.ByID!
		if a.Focus() != railTable {
			t.Fatalf("initial focus = %q, want %q", a.Focus(), railTable)
		}
		if root := a.View(a.State()); root.ByID(views.SiteTOCID) == nil {
			t.Fatal("expected site.toc node in ViewNode tree")
		}
		if !a.cfg.Settings.Bool(SetShowTOC, true) {
			t.Fatal("expected TOC initially shown")
		}
		if got := a.effectiveTOCStyle(); got != views.TOCStyleCompact {
			t.Fatalf("initial effectiveTOCStyle = %q, want %q", got, views.TOCStyleCompact)
		}
		execCmd(d, CmdStandardCycle, map[string]any{"target": views.SiteTOCID})
		if got := a.tocStyleSetting(); got != views.TOCStyleTight {
			t.Fatalf("cycle target=site.toc while focused on rail: tocStyle=%q, want %q", got, views.TOCStyleTight)
		}
		if a.Focus() != railTable {
			t.Fatalf("focus drifted to %q, want %q", a.Focus(), railTable)
		}
		execCmd(d, CmdStandardCycle, map[string]any{"target": views.SiteTOCID, "dir": "prev"})
		if got := a.tocStyleSetting(); got != views.TOCStyleCompact {
			t.Fatalf("cycle prev target=site.toc: tocStyle=%q, want %q", got, views.TOCStyleCompact)
		}
		execCmd(d, CmdStandardCycle, map[string]any{"target": "site"})
		if a.cfg.Settings.Bool(SetShowTOC, true) {
			t.Fatal("cycle target=site did not hide TOC")
		}
		execCmd(d, CmdStandardCycle, map[string]any{"target": "site"})
		if !a.cfg.Settings.Bool(SetShowTOC, true) {
			t.Fatal("second cycle target=site did not restore TOC")
		}

		// Now focus site.toc and explicitly target panes.rail.table to cycle rail grouping!
		d.Keys("l")
		if a.Focus() == railTable {
			t.Fatalf("expected focus to leave rail, still on %q", a.Focus())
		}
		beforeGroup := a.panesGroupMode()
		execCmd(d, CmdStandardCycle, map[string]any{"target": railTable})
		if a.panesGroupMode() == beforeGroup {
			t.Fatalf("cycle target=panes.rail.table did not cycle rail group from %q", beforeGroup)
		}
	})

	t.Run("queue, files, jobs, and hints cyclers wrap forward and backward", func(t *testing.T) {
		a, d := demoLegacy(t)

		// Files tabs: focused + explicit target + prev/next wrap.
		d.Keys("g", "d")
		for _, tc := range []struct {
			args map[string]any
			want string
		}{
			{map[string]any{"dir": "next"}, views.FilesTabBaseDirs},
			{map[string]any{"dir": "next"}, views.FilesTabRepos},
			{map[string]any{"dir": "next"}, views.FilesTabPaths},
			{map[string]any{"dir": "prev"}, views.FilesTabRepos},
			{map[string]any{"target": views.FilesListID, "dir": "next"}, views.FilesTabPaths},
		} {
			execCmd(d, CmdStandardCycle, tc.args)
			if a.files.tab != tc.want {
				t.Fatalf("files cycle %v: tab=%q, want %q", tc.args, a.files.tab, tc.want)
			}
		}

		// Jobs tabs: focused + explicit target + prev/next wrap.
		d.Keys("g", "j")
		for _, tc := range []struct {
			args map[string]any
			want string
		}{
			{map[string]any{"dir": "next"}, views.JobsTabActive},
			{map[string]any{"dir": "next"}, views.JobsTabHistory},
			{map[string]any{"dir": "next"}, views.JobsTabActions},
			{map[string]any{"dir": "next"}, views.JobsTabAll},
			{map[string]any{"dir": "prev"}, views.JobsTabActions},
			{map[string]any{"target": views.JobsListID, "dir": "next"}, views.JobsTabAll},
		} {
			execCmd(d, CmdStandardCycle, tc.args)
			if got := jobsTabs[a.jobs.tab]; got != tc.want {
				t.Fatalf("jobs cycle %v: tab=%q, want %q", tc.args, got, tc.want)
			}
		}

		// Queue tabs: create two named queues and cycle forward/backward.
		if _, err := a.Store.EnsureNamedQueue("alpha", "", nil, User); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Store.EnsureNamedQueue("beta", "", nil, User); err != nil {
			t.Fatal(err)
		}
		d.Keys("g", "q")
		first := a.queue.active
		execCmd(d, CmdStandardCycle, map[string]any{"dir": "next"})
		second := a.queue.active
		if second == first {
			t.Fatalf("queue cycle next did not advance from %q", first)
		}
		execCmd(d, CmdStandardCycle, map[string]any{"target": views.QueueListID, "dir": "prev"})
		if a.queue.active != first {
			t.Fatalf("queue cycle prev got %q, want %q", a.queue.active, first)
		}

		// Hints action cycling via traits.Cycle forward & backward.
		sess := hints.NewSession([]hints.Target{
			{ID: "h-0", Rect: layout.Rect{X: 0, Y: 0, W: 10, H: 1}, Kind: "url", Value: "https://example.com"},
		})
		a.panes.hints = &hintsState{session: sess}
		execCmd(d, CmdStandardCycle, map[string]any{"target": "hints", "dir": "next"})
		if got := sess.ActiveAction(); got != hints.ActionSecondary {
			t.Fatalf("hints cycle next = %q, want %q", got, hints.ActionSecondary)
		}
		execCmd(d, CmdStandardCycle, map[string]any{"target": "hints", "dir": "prev"})
		if got := sess.ActiveAction(); got != hints.ActionDefault {
			t.Fatalf("hints cycle prev = %q, want %q", got, hints.ActionDefault)
		}
		execCmd(d, CmdStandardCycle, map[string]any{"target": "hints", "dir": "prev"})
		if got := sess.ActiveAction(); got != hints.ActionMeta {
			t.Fatalf("hints cycle prev wrap = %q, want %q", got, hints.ActionMeta)
		}
		a.panes.hints = nil
	})
}

func TestStandardFoldTargetsFocusedAndExplicit(t *testing.T) {
	t.Run("work tree fold actions", func(t *testing.T) {
		a, d := demoLegacy(t)
		d.Keys("2")
		if !a.inWorkTree(a.State()) {
			t.Fatal("expected work tree active")
		}
		tm := a.trees.workModel(a.workKind())
		execCmd(d, CmdStandardFold, map[string]any{"target": workTable, "action": "open_all"})
		if len(tm.Folded) != 0 {
			t.Fatalf("fold open_all left folded nodes: %+v", tm.Folded)
		}
		execCmd(d, CmdStandardFold, map[string]any{"target": workTable, "action": "close_all"})
		if len(tm.Folded) == 0 {
			t.Fatal("fold close_all did not fold any parent nodes")
		}
		// Focused toggle unfolds the focused parent node.
		row, ok := tm.Focused()
		if !ok {
			t.Fatal("no focused tree row")
		}
		wasFolded := tm.Folded[row.ID]
		execCmd(d, CmdStandardFold, map[string]any{"action": "toggle"})
		if tm.Folded[row.ID] == wasFolded {
			t.Fatalf("fold toggle did not flip folded state for %q", row.ID)
		}
	})

	t.Run("show detail fold targeted while focused on header", func(t *testing.T) {
		a, d := demoLegacy(t)
		d.Send(resize(120, 40))
		d.Keys("2", "E")
		if a.Focus() != showHeader {
			t.Fatalf("Show focus = %q, want %q", a.Focus(), showHeader)
		}
		if a.nav.showEmptyFoldExpanded {
			t.Fatal("expected empty relations initially folded")
		}
		execCmd(d, CmdStandardFold, map[string]any{"target": "show.fold.empty", "action": "open"})
		if !a.nav.showEmptyFoldExpanded {
			t.Fatal("fold target=show.fold.empty action=open did not expand")
		}
		execCmd(d, CmdStandardFold, map[string]any{"target": "show.fold.empty", "action": "close"})
		if a.nav.showEmptyFoldExpanded {
			t.Fatal("fold target=show.fold.empty action=close did not collapse")
		}
		execCmd(d, CmdStandardFold, map[string]any{"target": "show.fold.empty", "action": "toggle"})
		if !a.nav.showEmptyFoldExpanded {
			t.Fatal("fold target=show.fold.empty action=toggle did not expand")
		}
	})

	t.Run("TOC group fold targeted while focused on rail", func(t *testing.T) {
		a, d := demoLegacy(t)
		d.Keys("1")
		if a.Focus() != railTable {
			t.Fatalf("focus = %q, want %q", a.Focus(), railTable)
		}
		target := views.TOCGroupPrefix + "pages"
		execCmd(d, CmdStandardFold, map[string]any{"target": target, "action": "close"})
		if !a.panes.tocCollapsed["pages"] {
			t.Fatal("fold target=site.toc.group.pages action=close did not collapse TOC group")
		}
		execCmd(d, CmdStandardFold, map[string]any{"target": target, "action": "open"})
		if a.panes.tocCollapsed["pages"] {
			t.Fatal("fold target=site.toc.group.pages action=open did not expand TOC group")
		}
	})

	t.Run("project nav section fold targeted while focused elsewhere", func(t *testing.T) {
		a, d := demoLegacy(t)
		d.Keys("1", "g", "g")
		if _, _, onProj := a.selectedRailProject(); !onProj {
			t.Fatal("expected rail selection on a project header")
		}
		projKey := a.activeProjectWorkspaceKey(a.State())
		if projKey == "" {
			t.Fatal("expected active project workspace key")
		}
		// Focus is still on panes.rail.table; explicitly target project.nav.tasks!
		navTarget := views.ProjectNavPrefix + "tasks"
		if root := a.View(a.State()); root.ByID(navTarget) == nil {
			t.Fatalf("expected %q node in ViewNode tree", navTarget)
		}
		pc := a.projectCtrl()
		wasExpanded := pc.isNavSectionExpanded(projKey, "tasks")
		execCmd(d, CmdStandardFold, map[string]any{"target": navTarget, "action": "toggle"})
		if pc.isNavSectionExpanded(projKey, "tasks") == wasExpanded {
			t.Fatalf("fold target=%s did not toggle tasks section (still %v)", navTarget, wasExpanded)
		}
	})

	t.Run("table group fold on grouped panes rail", func(t *testing.T) {
		a, d := demoLegacy(t)
		d.Keys("1", "g", "g")
		tbl := a.rail()
		if tbl.GroupField == "" {
			t.Fatal("expected rail to be grouped")
		}
		it, ok := tbl.Focused()
		if !ok || !it.Header {
			t.Fatalf("expected focused group header, got %+v ok=%v", it, ok)
		}
		execCmd(d, CmdStandardFold, map[string]any{"target": railTable, "action": "close"})
		if !tbl.Collapsed[it.Group] {
			t.Fatalf("fold target=panes.rail.table action=close did not collapse group %q", it.Group)
		}
		execCmd(d, CmdStandardFold, map[string]any{"target": railTable, "action": "open"})
		if tbl.Collapsed[it.Group] {
			t.Fatalf("fold target=panes.rail.table action=open did not expand group %q", it.Group)
		}
	})
}

func TestStandardToggleTargetsFocusedAndExplicit(t *testing.T) {
	a, d := demoLegacy(t)

	// 1. Work layout toggle (focused + targeted).
	d.Keys("2")
	if !a.inWorkTree(a.State()) {
		t.Fatal("expected Tasks to start in tree layout")
	}
	execCmd(d, CmdStandardToggle, nil)
	if a.inWorkTree(a.State()) {
		t.Fatal("focused toggle on work.table did not switch to table layout")
	}
	execCmd(d, CmdStandardToggle, map[string]any{"target": workTable})
	if !a.inWorkTree(a.State()) {
		t.Fatal("toggle target=work.table did not switch back to tree layout")
	}

	// 2. Site TOC toggle targeted while focused on panes.rail.table!
	d.Keys("1")
	if a.Focus() != railTable || !a.cfg.Settings.Bool(SetShowTOC, true) {
		t.Fatalf("initial Panes state: focus=%q showTOC=%v", a.Focus(), a.cfg.Settings.Bool(SetShowTOC, true))
	}
	execCmd(d, CmdStandardToggle, map[string]any{"target": views.SiteTOCID})
	if a.cfg.Settings.Bool(SetShowTOC, true) {
		t.Fatal("toggle target=site.toc did not hide TOC")
	}
	execCmd(d, CmdStandardToggle, map[string]any{"target": views.SiteTOCID})
	if !a.cfg.Settings.Bool(SetShowTOC, true) {
		t.Fatal("second toggle target=site.toc did not restore TOC")
	}

	// 3. Panes group toggle (focused on rail).
	beforeGroup := a.panesGroupMode()
	execCmd(d, CmdStandardToggle, nil)
	if a.panesGroupMode() == beforeGroup {
		t.Fatalf("focused toggle on rail did not flip panes.group from %q", beforeGroup)
	}
	execCmd(d, CmdStandardToggle, map[string]any{"target": railTable})
	if a.panesGroupMode() != beforeGroup {
		t.Fatalf("second toggle target=panes.rail.table did not restore %q", beforeGroup)
	}

	// 4. Project nav new items toggle (explicit target).
	beforeNewItems := a.projectNavShowNewItemsEnabled()
	execCmd(d, CmdStandardToggle, map[string]any{"target": standardTargetProjectNavNewItem})
	if a.projectNavShowNewItemsEnabled() == beforeNewItems {
		t.Fatalf("toggle target=%s did not flip projects.navShowNewItems", standardTargetProjectNavNewItem)
	}
	execCmd(d, CmdStandardToggle, map[string]any{"target": standardTargetProjectNavNewItem})
	if a.projectNavShowNewItemsEnabled() != beforeNewItems {
		t.Fatalf("second toggle target=%s did not restore projects.navShowNewItems", standardTargetProjectNavNewItem)
	}
}
