package app

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func TestProjectPanesBindingsCheckClean(t *testing.T) {
	rep, err := CheckBindings("")
	if err != nil {
		t.Fatalf("CheckBindings error: %v", err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings reported %d error(s): %+v", len(rep.Errors), rep.Errors)
	}
	a, _ := demo(t)
	if len(a.Warnings) != 0 {
		t.Fatalf("unexpected keymap warnings: %v", a.Warnings)
	}
}

func TestProjectPanesBindingsNewTerminalAndAgent(t *testing.T) {
	a, d, _, beta, _, betaPane := setupMultiProjectFixture(t)

	// Open 5 Projects -> enter on Beta (most recently active) -> opens 6 Beta.
	d.Keys("5", "enter")
	betaTab := projectTabID(beta.ID)
	if a.Space() != betaTab {
		t.Fatalf("expected %q, got %q", betaTab, a.Space())
	}

	assertNewAgentModal := func(ctx string) {
		t.Helper()
		d.Keys("n")
		if a.Modal() != "form" || a.Mode() != ModeForm {
			t.Fatalf("%s: n did not open New Agent form: modal=%q mode=%q status=%q", ctx, a.Modal(), a.Mode(), a.Status())
		}
		rf := a.activeRecordForm()
		if rf == nil || rf.kind != data.KindPane {
			t.Fatalf("%s: expected pane form, got %+v", ctx, rf)
		}
		fm := rf.Model()
		if fm.Title != "New Agent" {
			t.Fatalf("%s: form title = %q, want %q", ctx, fm.Title, "New Agent")
		}
		if gotTask, ok := asRefValue(fm.Values["task"]); !ok || gotTask != beta.Ref() {
			t.Fatalf("%s: New Agent task = %v (ok=%v), want %v", ctx, gotTask, ok, beta.Ref())
		}
		if gotCwd := data.Text(fm.Values["cwd"]); gotCwd != "/tmp/beta" {
			t.Fatalf("%s: New Agent cwd = %q, want %q", ctx, gotCwd, "/tmp/beta")
		}
		var enum []string
		for _, sp := range fm.Specs {
			if sp.Name == "harness" {
				enum = sp.Enum
				break
			}
		}
		if len(enum) == 0 || slices.Contains(enum, "zsh") {
			t.Fatalf("%s: New Agent harness choices must be non-empty and exclude zsh, got %v", ctx, enum)
		}
		d.Keys("esc", "esc")
		if a.Modal() != "" {
			t.Fatalf("%s: esc did not close New Agent form: modal=%q", ctx, a.Modal())
		}
	}

	assertNewTerminal := func(ctx string) {
		t.Helper()
		before, err := a.Store.Query(data.Query{Kind: data.KindPane})
		if err != nil {
			t.Fatal(err)
		}
		beforeIDs := map[string]bool{}
		for _, p := range before.Items {
			beforeIDs[p.ID] = true
		}

		d.Keys("C")
		if a.Modal() != "" {
			t.Fatalf("%s: C should create terminal without modal, got modal=%q", ctx, a.Modal())
		}
		after, err := a.Store.Query(data.Query{Kind: data.KindPane})
		if err != nil {
			t.Fatal(err)
		}
		if len(after.Items) != len(before.Items)+1 {
			t.Fatalf("%s: C pane count = %d, want %d (status=%q)", ctx, len(after.Items), len(before.Items)+1, a.Status())
		}
		var created *data.Record
		for i := range after.Items {
			if !beforeIDs[after.Items[i].ID] {
				created = &after.Items[i]
				break
			}
		}
		if created == nil {
			t.Fatalf("%s: could not locate newly created terminal pane", ctx)
		}
		if created.Text("harness") != "zsh" {
			t.Fatalf("%s: created terminal harness = %q, want zsh", ctx, created.Text("harness"))
		}
		if gotTask, ok := created.RefField("task"); !ok || gotTask != beta.Ref() {
			t.Fatalf("%s: created terminal task = %v (ok=%v), want %v", ctx, gotTask, ok, beta.Ref())
		}
		if gotCwd := created.Text("cwd"); gotCwd != "/tmp/beta" {
			t.Fatalf("%s: created terminal cwd = %q, want %q", ctx, gotCwd, "/tmp/beta")
		}
		if a.Space() != betaTab || a.activeChildPaneID(a.State()) != created.ID {
			t.Fatalf("%s: C should open new terminal inline in %q, got space=%q child=%q", ctx, betaTab, a.Space(), a.activeChildPaneID(a.State()))
		}
		// Exit live terminal + inline child pane view back to project.panes.tree.
		if a.Mode() == ModeTerminal {
			d.Keys("ctrl+a", "d")
		}
		d.Keys("esc")
		if a.activeChildPaneID(a.State()) != "" {
			t.Fatalf("%s: esc did not exit inline child pane view: child=%q", ctx, a.activeChildPaneID(a.State()))
		}
	}

	// 1. In project.panes.tree.
	d.Send(core.CommandEvent{Command: core.Command{
		ID:    CmdProjectSectionSelect,
		Args:  map[string]any{"section": "panes"},
		Actor: User,
	}})
	if a.Focus() != projectPanesTree {
		t.Fatalf("expected focus %q, got %q", projectPanesTree, a.Focus())
	}
	assertNewAgentModal("project.panes.tree")
	assertNewTerminal("project.panes.tree")

	// 2. In project.nav.panes (left nav Panes section header).
	d.Keys("esc")
	if a.Focus() != projectNavPrefix+"panes" {
		t.Fatalf("expected focus %q, got %q", projectNavPrefix+"panes", a.Focus())
	}
	assertNewAgentModal("project.nav.panes")
	assertNewTerminal("project.nav.panes")

	// 3. In project.nav.node.panes.<id> (left nav child pane row).
	d.Keys("esc")
	if a.Focus() != projectNavPrefix+"panes" {
		t.Fatalf("expected focus %q before expanding nav, got %q", projectNavPrefix+"panes", a.Focus())
	}
	d.Keys("space") // expand panes section in left nav
	nodeFocus := projectNavNodePrefix + "panes." + betaPane.ID
	root := a.View(a.State())
	if root.Find(nodeFocus) == nil {
		t.Fatalf("expected %q in view after expanding panes nav section", nodeFocus)
	}
	d.Send(core.CommandEvent{Command: core.Command{ID: core.CmdFocusNext, Actor: User}})
	// Step directly onto betaPane's nav node.
	for i := 0; i < 6 && a.Focus() != nodeFocus; i++ {
		d.Keys("j")
	}
	if a.Focus() != nodeFocus {
		t.Fatalf("could not focus %q, stuck on %q", nodeFocus, a.Focus())
	}
	assertNewAgentModal("project.nav.node.panes")
	assertNewTerminal("project.nav.node.panes")

	// 4. Inside inChildPaneView (inline child pane in 6 Beta).
	if a.Focus() != projectPanesTree {
		d.Send(core.CommandEvent{Command: core.Command{
			ID:    CmdProjectSectionSelect,
			Args:  map[string]any{"section": "panes"},
			Actor: User,
		}})
	}
	d.Keys("enter")
	if a.activeChildPaneID(a.State()) == "" {
		t.Fatalf("enter on project.panes.tree did not open inline child pane: focus=%q", a.Focus())
	}
	if a.Mode() == ModeTerminal {
		d.Keys("ctrl+a", "d")
	}
	if a.Mode() != ModeNormal || a.activeChildPaneID(a.State()) == "" {
		t.Fatalf("expected ModeNormal in inline child pane, got mode=%q child=%q", a.Mode(), a.activeChildPaneID(a.State()))
	}
	assertNewAgentModal("inChildPaneView")
	assertNewTerminal("inChildPaneView")
}

func TestProjectPanesBindingsComposeAndAssociate(t *testing.T) {
	a, d, alpha, beta, _, betaPane := setupMultiProjectFixture(t)

	// Create an unrelated pane on Alpha and select it in 1 Panes so SelectedPane() != betaPane!
	alphaPane, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindPane,
		Fields: map[string]any{
			"title":     "alpha-other",
			"task":      alpha.Ref(),
			"cwd":       "/tmp/alpha",
			"runStatus": "idle",
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	a.viewMemo.gen++
	_ = a.View(a.State())
	if !selectRow(a.rail(), data.RefText(alphaPane.Ref())) {
		t.Fatalf("failed to select alphaPane in rail")
	}
	if sel, ok := a.SelectedPane(); !ok || sel == nil || sel.ID != alphaPane.ID {
		t.Fatalf("expected SelectedPane() = %q, got %+v", alphaPane.ID, sel)
	}

	// Open 6 Beta and focus project.panes.tree on betaPane.
	d.Send(core.CommandEvent{Command: core.Command{
		ID:    CmdProjectTabOpen,
		Args:  map[string]any{"project": beta.ID},
		Actor: User,
	}})
	d.Send(core.CommandEvent{Command: core.Command{
		ID:    CmdProjectSectionSelect,
		Args:  map[string]any{"section": "panes"},
		Actor: User,
	}})
	if a.Focus() != projectPanesTree {
		t.Fatalf("expected %q, got %q", projectPanesTree, a.Focus())
	}

	assertComposeTargetsBeta := func(ctx string) {
		t.Helper()
		d.Keys("c")
		if a.Mode() != ModeComposer {
			t.Fatalf("%s: c did not open composer: mode=%q modal=%q status=%q", ctx, a.Mode(), a.Modal(), a.Status())
		}
		cm := a.activeRichComposer()
		if cm == nil {
			t.Fatalf("%s: activeRichComposer is nil", ctx)
		}
		if cm.paneID != betaPane.ID {
			t.Fatalf("%s: composer paneID = %q, want %q (not rail SelectedPane %q)", ctx, cm.paneID, betaPane.ID, alphaPane.ID)
		}
		d.Keys("esc")
		if a.Modal() != "" {
			t.Fatalf("%s: esc did not close composer: modal=%q", ctx, a.Modal())
		}
	}

	assertAssociateTargetsBeta := func(ctx string) {
		t.Helper()
		d.Keys("L")
		if a.Modal() != "pane.associate" {
			t.Fatalf("%s: L did not open pane.associate picker: modal=%q status=%q", ctx, a.Modal(), a.Status())
		}
		frame := d.Frame()
		wantTitle := "Associate " + a.title(betaPane.Ref()) + " with task"
		if !strings.Contains(frame, wantTitle) {
			t.Fatalf("%s: associate picker did not target betaPane (%q):\n%s", ctx, wantTitle, frame)
		}
		d.Keys("esc", "esc")
		if a.Modal() != "" {
			t.Fatalf("%s: esc did not close pane.associate picker: modal=%q", ctx, a.Modal())
		}
	}

	// 1. project.panes.tree: c and L target betaPane (not alphaPane).
	assertComposeTargetsBeta("project.panes.tree")
	assertAssociateTargetsBeta("project.panes.tree")

	// 2. project.nav.node.panes.<betaPane.ID>: c and L target betaPane (not alphaPane).
	d.Keys("esc")
	if a.Focus() != projectNavPrefix+"panes" {
		t.Fatalf("expected %q, got %q", projectNavPrefix+"panes", a.Focus())
	}
	d.Keys("space")
	nodeFocus := projectNavNodePrefix + "panes." + betaPane.ID
	for i := 0; i < 6 && a.Focus() != nodeFocus; i++ {
		d.Keys("j")
	}
	if a.Focus() != nodeFocus {
		t.Fatalf("expected %q, got %q", nodeFocus, a.Focus())
	}
	assertComposeTargetsBeta("project.nav.node.panes")
	assertAssociateTargetsBeta("project.nav.node.panes")

	// 3. inChildPaneView: focus site.toc (so subject is NOT KindPane) and press L ->
	//    must fall back to activePane(s) == betaPane, NOT SelectedPane() == alphaPane!
	d.Send(core.CommandEvent{Command: core.Command{
		ID:    CmdProjectSectionSelect,
		Args:  map[string]any{"section": "panes"},
		Actor: User,
	}})
	d.Keys("enter")
	if a.Mode() == ModeTerminal {
		d.Keys("ctrl+a", "d")
	}
	if a.activeChildPaneID(a.State()) != betaPane.ID {
		t.Fatalf("expected active child pane %q, got %q", betaPane.ID, a.activeChildPaneID(a.State()))
	}
	// Move focus to TOC so subject is a page/toc node rather than a KindPane ref.
	if a.Focus() == siteScreen {
		d.Keys("h")
	}
	if !strings.HasPrefix(a.Focus(), tocPrefix) {
		t.Fatalf("expected focus in inline site.toc, got %q", a.Focus())
	}
	assertAssociateTargetsBeta("inChildPaneView.toc")
}

func TestProjectPanesBindingsNavPendingApprovalFacts(t *testing.T) {
	a, d, _, beta, _, betaPane := setupMultiProjectFixture(t)

	// Attach a pending approval to betaPane in live telemetry.
	a.setTelemetry(&daemon.TelemetryResult{
		Telemetry: map[string]*daemon.PaneTelemetry{},
		PendingApprovals: []*daemon.PendingApproval{
			{RequestID: "req-1", PaneID: betaPane.ID, Tool: "Bash", Summary: "run tests"},
		},
	}, true)

	d.Send(core.CommandEvent{Command: core.Command{
		ID:    CmdProjectTabOpen,
		Args:  map[string]any{"project": beta.ID},
		Actor: User,
	}})
	d.Send(core.CommandEvent{Command: core.Command{
		ID:    CmdProjectSectionSelect,
		Args:  map[string]any{"section": "panes"},
		Actor: User,
	}})
	d.Keys("esc", "space") // expand panes section in left nav

	nodeFocus := projectNavNodePrefix + "panes." + betaPane.ID
	root := a.View(a.State())
	node := root.Find(nodeFocus)
	if node == nil {
		t.Fatalf("expected nav node %q in view", nodeFocus)
	}
	if got, _ := node.Facts["hasPendingApproval"].(bool); !got {
		t.Fatalf("expected hasPendingApproval=true on %q, got facts=%+v", nodeFocus, node.Facts)
	}

	// Focus the nav node and verify Y and D trigger pane.approve / pane.deny.
	for i := 0; i < 6 && a.Focus() != nodeFocus; i++ {
		d.Keys("j")
	}
	if a.Focus() != nodeFocus {
		t.Fatalf("expected focus %q, got %q", nodeFocus, a.Focus())
	}
	d.Keys("Y")
	if !strings.Contains(a.Status(), "no local daemon is connected") {
		t.Fatalf("Y on nav pane with pending approval did not invoke pane.approve: status=%q", a.Status())
	}
	a.say("")
	d.Keys("D")
	if !strings.Contains(a.Status(), "no local daemon is connected") {
		t.Fatalf("D on nav pane with pending approval did not invoke pane.deny: status=%q", a.Status())
	}
}
