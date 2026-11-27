package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/palette"
)

// forbiddenSurfaceErrors are raw status-line error strings that must never leak
// when executing an active binding or palette/help action in a valid context.
var forbiddenSurfaceErrors = []string{
	"no list is focused",
	"no table is focused",
	"no tree is focused",
	"no terminal is focused",
	"missing required",
	"is required",
	"no handler for",
}

func assertNoForbiddenStatus(t *testing.T, label, status string) {
	t.Helper()
	low := strings.ToLower(status)
	for _, bad := range forbiddenSurfaceErrors {
		if strings.Contains(low, bad) {
			t.Fatalf("%s produced forbidden status-line error %q (matched %q)", label, status, bad)
		}
	}
}

// runViaHelp opens the ? Binding Help modal, selects the binding matching cmdID
// (and optional keys), and confirms it with Enter.
func runViaHelp(t *testing.T, a *App, d *Driver, cmdID, keys string) {
	t.Helper()
	d.Keys("?")
	if a.Modal() != "help" {
		t.Fatalf("? did not open help modal: modal=%q status=%q", a.Modal(), a.Status())
	}
	pm := a.activePicker("help")
	if pm == nil {
		t.Fatal("help picker modal is nil")
	}
	items := pm.Model().Items
	idx := -1
	for i, it := range items {
		b, ok := it.Data.(activeBinding)
		if !ok || b.rawCommand != cmdID {
			continue
		}
		if keys != "" && b.keys != keys {
			continue
		}
		idx = i
		break
	}
	if idx < 0 {
		t.Fatalf("help modal missing binding for command %q (keys=%q) on focus=%q mode=%q", cmdID, keys, a.Focus(), a.Mode())
	}
	pm.Model().SelectedIdx = idx
	d.Keys("enter")
}

// runViaPaletteInteractive opens the : Command Palette, selects entryID, and
// presses Enter without editing arguments.
func runViaPaletteInteractive(t *testing.T, a *App, d *Driver, entryID string) {
	t.Helper()
	d.Keys(":")
	if a.Modal() != "palette" {
		t.Fatalf(": did not open palette modal: modal=%q status=%q", a.Modal(), a.Status())
	}
	pm := a.activePalette()
	if pm == nil {
		t.Fatal("palette modal is nil")
	}
	idx := slices.IndexFunc(pm.Model().Entries, func(e palette.Entry) bool {
		return e.Spec.ID == entryID || e.Item.ID == entryID
	})
	if idx < 0 {
		t.Fatalf("palette missing entry %q", entryID)
	}
	pm.Model().Picker.SetQuery("")
	pm.Model().Picker.Filtering = false
	pm.Model().Picker.SelectedIdx = idx
	pm.Model().Sync()
	d.Keys("enter")
}

func TestZeroFieldWorksInDefaultWorkTreeAndProjectNavTables(t *testing.T) {
	t.Run("2 Work default tree layout applies and clears Zero-Field filter", func(t *testing.T) {
		a, d := demo(t)
		d.Keys("2")
		_ = d.Frame()
		if !a.inWorkTree(a.State()) {
			t.Fatalf("expected 2 Work to start in tree layout")
		}
		allTasks := a.tableCandidates(data.KindTask)
		if len(allTasks) < 2 {
			t.Fatalf("demo needs multiple tasks, got %d", len(allTasks))
		}

		// Open Zero-Field via f right from default tree view.
		d.Keys("f")
		if a.Modal() != zeroFieldModalID {
			t.Fatalf("f in 2 Work tree did not open Zero-Field: modal=%q status=%q", a.Modal(), a.Status())
		}
		m, ok := a.activeZeroFieldModal()
		if !ok || len(m.records) != len(allTasks) {
			t.Fatalf("Zero-Field records = %d, want %d", len(m.records), len(allTasks))
		}
		// Pick priority = P1 (or first available value chip on priority).
		d.Keys(zfKeys(t, a, zfVerbField, "priority")...)
		var pick zeroFieldAction
		for _, r := range m.actions() {
			if r.Row == zfRowValue {
				pick = r
				break
			}
		}
		if pick.Keys == "" {
			t.Fatal("Zero-Field priority offered no value chips")
		}
		d.Keys(strings.Split(pick.Keys, "")...)
		wantMatched := len(m.matched())
		if wantMatched == 0 || wantMatched == len(allTasks) {
			t.Fatalf("expected partial match on priority, got %d/%d", wantMatched, len(allTasks))
		}
		d.Keys("esc")
		if a.Modal() != "" {
			t.Fatalf("esc did not apply/close Zero-Field: modal=%q", a.Modal())
		}
		if a.inWorkTree(a.State()) {
			t.Fatal("2 Work stayed in unfiltered tree layout after applying Zero-Field filter")
		}
		if got := len(a.records(workTable, data.KindTask).Filtered()); got != wantMatched {
			t.Fatalf("filtered work.table rows = %d, want %d", got, wantMatched)
		}

		// Clearing Zero-Field filter restores default tree layout.
		d.Keys("f", "R", "esc")
		if !a.inWorkTree(a.State()) {
			t.Fatal("clearing Zero-Field filter did not restore 2 Work tree layout")
		}
	})

	t.Run("project.nav beside project table opens populated Zero-Field and filters rows", func(t *testing.T) {
		a, d := openProjectTabOnNav(t, "documents")
		projID := projectIDFromTab(a.Space())
		projRef := core.Ref{Kind: data.KindTask, ID: projID}
		doc1, _, err := a.Store.Create(data.CreateInput{
			Kind:   data.KindDocument,
			Fields: map[string]any{"title": "Architecture Spec", "task": projRef},
		}, User)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := a.Store.Create(data.CreateInput{
			Kind:   data.KindDocument,
			Fields: map[string]any{"title": "Release Runbook", "task": projRef},
		}, User); err != nil {
			t.Fatal(err)
		}
		if _, _, err := a.Store.Star(doc1.Ref(), User); err != nil {
			t.Fatal(err)
		}
		_ = d.Frame()
		if a.viewMemo.PageListID != projectDocumentsTable {
			t.Fatalf("PageListID = %q, want %q", a.viewMemo.PageListID, projectDocumentsTable)
		}

		// Press f while focused on project.nav.documents.
		d.Keys("f")
		assertNoForbiddenStatus(t, "f on project.nav.documents", a.Status())
		if a.Modal() != zeroFieldModalID {
			t.Fatalf("f on project.nav.documents did not open Zero-Field: modal=%q status=%q", a.Modal(), a.Status())
		}
		m, ok := a.activeZeroFieldModal()
		if !ok {
			t.Fatal("expected active Zero-Field modal")
		}
		if m.tableID != projectDocumentsTable || m.kind != data.KindDocument {
			t.Fatalf("Zero-Field target = (%q, %q), want (%q, %q)", m.tableID, m.kind, projectDocumentsTable, data.KindDocument)
		}
		if len(m.records) != 2 {
			t.Fatalf("Zero-Field scoped document records = %d, want 2", len(m.records))
		}
		d.Keys(zfKeys(t, a, zfVerbField, data.ColStarStatus)...)
		d.Keys(zfKeys(t, a, zfVerbValue, "starred")...)
		if len(m.matched()) != 1 {
			t.Fatalf("Zero-Field preview matched = %d, want 1", len(m.matched()))
		}
		d.Keys("esc")
		_ = d.Frame()
		tbl := a.nav.tables[projectDocumentsTable]
		if tbl == nil || len(tbl.Rows) != 1 {
			t.Fatalf("project.documents.table after Zero-Field = %d rows, want 1", len(tbl.Rows))
		}
	})

	t.Run("palette Zero-Field on page without table prompts for table via auto-form", func(t *testing.T) {
		a, d := openProjectTabOnNav(t, "home")
		if a.viewMemo.PageListID != "" {
			t.Fatalf("expected no page list on project home, got %q", a.viewMemo.PageListID)
		}
		d.Send(core.CommandEvent{Command: core.Command{
			ID:     CmdZeroFieldOpen,
			Source: "palette",
			Actor:  User,
		}})
		assertNoForbiddenStatus(t, "palette query.zerofield.open on home", a.Status())
		if a.Modal() != missingArgsModalID {
			t.Fatalf("expected missing.args form modal for table target, got modal=%q status=%q", a.Modal(), a.Status())
		}
		fm := a.activeForm(missingArgsModalID)
		if fm == nil || len(fm.Model().Specs) != 1 || fm.Model().Specs[0].Name != "table" {
			t.Fatalf("expected missing.args form with single 'table' field, got %+v", fm)
		}
		// Submit the pre-populated default table (work.table) -> opens Zero-Field cleanly.
		d.Keys("ctrl+s")
		if a.Modal() != zeroFieldModalID {
			t.Fatalf("submitting table form did not open Zero-Field: modal=%q status=%q", a.Modal(), a.Status())
		}
		d.Keys("esc")
	})
}

func TestBindingHelpExecutesSelectedBinding(t *testing.T) {
	a, d := demo(t)
	d.Keys("2", "g", "t")
	_ = d.Frame()

	ref, ok := a.subject(a.State(), core.Command{})
	if !ok {
		t.Fatal("expected focused task in 2 Work table")
	}

	// Execute record.star directly from the ? Binding Help modal.
	runViaHelp(t, a, d, views.CmdStar, "m")
	assertNoForbiddenStatus(t, "help -> record.star", a.Status())
	rec, err := a.Store.Get(ref)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.IsStarred() {
		t.Fatalf("executing record.star via ? help modal did not star %s (status=%q)", ref.ID, a.Status())
	}

	// Execute table.filter.field.pick via ? Binding Help modal -> opens Zero-Field.
	runViaHelp(t, a, d, CmdFilterFieldPick, "f")
	assertNoForbiddenStatus(t, "help -> table.filter.field.pick", a.Status())
	if a.Modal() != zeroFieldModalID {
		t.Fatalf("executing table.filter.field.pick via ? help modal did not open Zero-Field: modal=%q status=%q", a.Modal(), a.Status())
	}
	d.Keys("esc")
}

func TestMissingRequiredArgsOpensAutoFormAcrossSurfaces(t *testing.T) {
	t.Run("custom multi-param command prompts ONLY for missing required fields and preserves provided args", func(t *testing.T) {
		a, d := demo(t)
		const testCmd = "test.missing.form"
		var captured map[string]any
		spec := core.CommandSpec{
			ID:          testCmd,
			Description: "Test command with two required params and one optional param",
			Params: []core.ParamSpec{
				{Name: "alpha", Type: core.ParamString, Required: true, Description: "First required arg"},
				{Name: "beta", Type: core.ParamString, Required: true, Description: "Second required arg"},
				{Name: "note", Type: core.ParamString, Description: "Optional note"},
			},
		}
		a.bind.specs = append(a.bind.specs, spec)
		if err := a.rebuildKeymap(); err != nil {
			t.Fatal(err)
		}
		a.handle(testCmd, func(s core.State, c core.Command) core.StepResult {
			captured = c.Args
			a.say("ran %s", c.ID)
			return core.Next(s)
		})

		// Invoke with alpha + optional note provided, beta missing -> form MUST prompt ONLY for beta!
		d.Send(core.CommandEvent{Command: core.Command{
			ID:     testCmd,
			Args:   map[string]any{"alpha": "already-set", "note": "keep-me"},
			Source: "palette",
			Actor:  User,
		}})
		if a.Modal() != missingArgsModalID {
			t.Fatalf("expected missing.args modal, got %q (status=%q)", a.Modal(), a.Status())
		}
		assertNoForbiddenStatus(t, "missing.args open", a.Status())
		fm := a.activeForm(missingArgsModalID)
		if fm == nil {
			t.Fatal("missing.args FormModal is nil")
		}
		if len(fm.Model().Specs) != 1 || fm.Model().Specs[0].Name != "beta" {
			t.Fatalf("form specs = %+v, want ONLY missing required field 'beta'", fm.Model().Specs)
		}
		d.Type("filled-beta")
		d.Keys("ctrl+s")
		if a.Modal() != "" {
			t.Fatalf("submitting missing.args form did not close modal: %q", a.Modal())
		}
		if captured["alpha"] != "already-set" || captured["beta"] != "filled-beta" || captured["note"] != "keep-me" {
			t.Fatalf("captured args = %+v, want alpha=already-set beta=filled-beta note=keep-me", captured)
		}
	})

	t.Run("built-in required-arg commands open forms on missing args across palette, help, and binding", func(t *testing.T) {
		a, d := demo(t)
		d.Keys("2", "g", "t")
		_ = d.Frame()

		// 1. Interactive palette ':' with missing required arg focuses inline args form (no raw error).
		runViaPaletteInteractive(t, a, d, CmdTablePresetDelete)
		assertNoForbiddenStatus(t, "interactive palette table.preset.delete", a.Status())
		if a.Modal() != "palette" {
			t.Fatalf("interactive palette on missing required arg should keep palette open on args form, got modal=%q", a.Modal())
		}
		if pm := a.activePalette(); pm == nil || !pm.Model().FocusForm {
			t.Fatalf("interactive palette did not focus argument form for missing required 'name'")
		}
		d.Keys("esc", "esc", "esc")

		// 2. Direct palette/binding dispatch of table.preset.delete with empty args opens missing.args form.
		d.Send(core.CommandEvent{Command: core.Command{ID: CmdTablePresetSave, Args: map[string]any{"name": "eval-preset"}, Actor: User}})
		for _, source := range []string{"palette", "help", "binding"} {
			d.Send(core.CommandEvent{Command: core.Command{
				ID:     CmdTablePresetDelete,
				Args:   map[string]any{},
				Source: source,
				Actor:  User,
			}})
			assertNoForbiddenStatus(t, source+" table.preset.delete empty args", a.Status())
			if a.Modal() != missingArgsModalID {
				t.Fatalf("[%s] table.preset.delete with empty args did not open missing.args form: modal=%q status=%q", source, a.Modal(), a.Status())
			}
			fm := a.activeForm(missingArgsModalID)
			if fm == nil || len(fm.Model().Specs) != 1 || fm.Model().Specs[0].Name != "name" {
				t.Fatalf("[%s] expected single 'name' missing field, got %+v", source, fm)
			}
			if !slices.Contains(fm.Model().Specs[0].Enum, "eval-preset") {
				t.Fatalf("[%s] expected dynamic preset choices containing 'eval-preset', got %v", source, fm.Model().Specs[0].Enum)
			}
			d.Keys("esc", "esc")
		}

		// 3. Submitting the missing.args form for preset.save and preset.apply succeeds end-to-end.
		d.Send(core.CommandEvent{Command: core.Command{ID: CmdPresetSave, Source: "palette", Actor: User}})
		if a.Modal() != missingArgsModalID {
			t.Fatalf("preset.save with missing name did not open missing.args form: modal=%q", a.Modal())
		}
		d.Type("saved-via-autoform")
		d.Keys("ctrl+s")
		if a.Modal() != "" {
			t.Fatalf("preset.save auto-form did not close on submit: modal=%q", a.Modal())
		}
		if list := a.genericPresets.List(data.PresetKindQuery, ""); len(list) == 0 || list[0].Name != "saved-via-autoform" {
			t.Fatalf("preset.save auto-form did not persist preset: %+v", list)
		}

		d.Send(core.CommandEvent{Command: core.Command{ID: CmdPresetApply, Source: "palette", Actor: User}})
		assertNoForbiddenStatus(t, "preset.apply missing name", a.Status())
		if a.Modal() != missingArgsModalID {
			t.Fatalf("preset.apply with missing name did not open missing.args form: modal=%q status=%q", a.Modal(), a.Status())
		}
		d.Keys("ctrl+s")
		assertNoForbiddenStatus(t, "preset.apply auto-form submit", a.Status())
		if a.Modal() != "" {
			t.Fatalf("preset.apply auto-form did not close on submit: modal=%q", a.Modal())
		}
	})
}

func TestActionSurfaceConsistencyEvals(t *testing.T) {
	// Verify representative core verbs work consistently across all 5 surfaces:
	// 1. Key binding, 2. Binding Help (?), 3. Palette (:), 4. CLI catalog, 5. MCP tool dispatch.
	t.Run("record.star and record.unstar across all 5 surfaces", func(t *testing.T) {
		a, d := demo(t)
		d.Keys("2", "g", "t")
		_ = d.Frame()
		ref, ok := a.subject(a.State(), core.Command{})
		if !ok {
			t.Fatal("expected focused task")
		}

		// 1. Key binding: m stars
		d.Keys("m")
		assertNoForbiddenStatus(t, "binding m", a.Status())
		rec, _ := a.Store.Get(ref)
		if !rec.IsStarred() {
			t.Fatal("binding m did not star task")
		}

		// 2. Binding Help (?): m unstars
		runViaHelp(t, a, d, views.CmdUnstar, "m")
		assertNoForbiddenStatus(t, "help record.unstar", a.Status())
		rec, _ = a.Store.Get(ref)
		if rec.IsStarred() {
			t.Fatal("help record.unstar did not unstar task")
		}

		// 3. Command Palette (:): record.star stars
		runViaPaletteInteractive(t, a, d, views.CmdStar)
		assertNoForbiddenStatus(t, "palette record.star", a.Status())
		rec, _ = a.Store.Get(ref)
		if !rec.IsStarred() {
			t.Fatal("palette record.star did not star task")
		}

		// 4. CLI catalog & binding audit parity
		rows := ActionRows(a.bind.bindings, a.bind.specs)
		starIdx := slices.IndexFunc(rows, func(r ActionInfo) bool { return r.ID == views.CmdStar })
		if starIdx < 0 || rows[starIdx].CLI == "" || rows[starIdx].MCP == "" {
			t.Fatalf("ActionRows parity missing for record.star: %+v", rows[starIdx])
		}

		// 5. MCP tool dispatch: data.update unstars the same record & data.query sees it
		box := mcpui.NewToolbox(a.Store, a.Router)
		rawArgs, _ := json.Marshal(map[string]any{
			"kind":   ref.Kind,
			"id":     ref.ID,
			"fields": map[string]any{data.ColStarStatus: "none"},
		})
		if _, err := box.Call(context.Background(), User, mcpui.ToolUpdate, rawArgs); err != nil {
			t.Fatalf("MCP %s failed: %v", mcpui.ToolUpdate, err)
		}
		rec, _ = a.Store.Get(ref)
		if rec.IsStarred() {
			t.Fatal("MCP data.update did not unstar task")
		}
	})

	t.Run("record.archive, record.new, record.edit, and task.agent.toggle across UI + MCP + CLI catalog", func(t *testing.T) {
		a, d := demo(t)
		d.Keys("2", "g", "t")
		_ = d.Frame()
		ref, ok := a.subject(a.State(), core.Command{})
		if !ok {
			t.Fatal("expected focused task")
		}

		// record.edit via binding, help, palette all open the edit form modal cleanly
		d.Keys("e")
		if a.Modal() != "form" {
			t.Fatalf("binding e did not open edit form: modal=%q", a.Modal())
		}
		d.Keys("esc", "esc")

		runViaHelp(t, a, d, views.CmdEdit, "e")
		if a.Modal() != "form" {
			t.Fatalf("help record.edit did not open edit form: modal=%q", a.Modal())
		}
		d.Keys("esc", "esc")

		runViaPaletteInteractive(t, a, d, views.CmdEdit)
		if a.Modal() != "form" {
			t.Fatalf("palette record.edit did not open edit form: modal=%q", a.Modal())
		}
		d.Keys("esc", "esc")

		// task.agent.toggle via binding (w), help (?), palette (:), and MCP task.flag_agent
		d.Keys("w")
		assertNoForbiddenStatus(t, "binding w", a.Status())
		rec, _ := a.Store.Get(ref)
		queued1 := rec.Bool("forAgent")
		runViaHelp(t, a, d, CmdTaskAgentToggle, "w")
		assertNoForbiddenStatus(t, "help task.agent.toggle", a.Status())
		rec, _ = a.Store.Get(ref)
		if rec.Bool("forAgent") == queued1 {
			t.Fatal("help task.agent.toggle did not toggle forAgent flag")
		}
		runViaPaletteInteractive(t, a, d, CmdTaskAgentToggle)
		assertNoForbiddenStatus(t, "palette task.agent.toggle", a.Status())
		rec, _ = a.Store.Get(ref)
		if rec.Bool("forAgent") != queued1 {
			t.Fatal("palette task.agent.toggle did not toggle forAgent flag back")
		}
		box := mcpui.NewToolbox(a.Store, a.Router)
		flagArgs, _ := json.Marshal(map[string]any{"task": ref.ID, "forAgent": !queued1})
		if _, err := box.Call(context.Background(), User, mcpui.ToolTaskFlagAgent, flagArgs); err != nil {
			t.Fatalf("MCP %s failed: %v", mcpui.ToolTaskFlagAgent, err)
		}
		rec, _ = a.Store.Get(ref)
		if rec.Bool("forAgent") == queued1 {
			t.Fatal("MCP task.flag_agent did not update forAgent flag")
		}

		// record.archive via palette and restore via MCP data.update
		runViaPaletteInteractive(t, a, d, views.CmdArchive)
		assertNoForbiddenStatus(t, "palette record.archive", a.Status())
		rec, _ = a.Store.Get(ref)
		if !rec.IsArchived() {
			t.Fatal("palette record.archive did not archive task")
		}
		restoreArgs, _ := json.Marshal(map[string]any{
			"kind":   ref.Kind,
			"id":     ref.ID,
			"fields": map[string]any{data.ColArchiveStatus: "active"},
		})
		if _, err := box.Call(context.Background(), User, mcpui.ToolUpdate, restoreArgs); err != nil {
			t.Fatalf("MCP %s restore failed: %v", mcpui.ToolUpdate, err)
		}
		rec, _ = a.Store.Get(ref)
		if rec.IsArchived() {
			t.Fatal("MCP data.update did not restore task")
		}
	})
}

// TestActionSurfaceConsistencyMatrix is a structural registry eval scanning all
// active bindings across default views AND every registered command spec with
// required parameters.
func TestActionSurfaceConsistencyMatrix(t *testing.T) {
	// Skip process-level / external-binary commands in automated matrix execution.
	skipMatrixCmd := map[string]bool{
		core.CmdAppQuit:       true,
		CmdDaemonReset:        true,
		CmdDaemonUpgrade:      true,
		CmdAppReload:          true,
		CmdExtInstall:         true,
		CmdExtInstallAll:      true,
		CmdExtSetup:           true,
		CmdExtRefresh:         true,
		CmdExtRun:             true,
		CmdFDSearch:           true,
		CmdFDSearchAgain:      true,
		CmdSessionSandboxOpen: true,
		CmdOpenTarget:         true,
		CmdOpenWith:           true,
	}

	type surfaceCtx struct {
		name  string
		setup func(t *testing.T) (*App, *Driver)
	}
	contexts := []surfaceCtx{
		{
			name: "1 Panes",
			setup: func(t *testing.T) (*App, *Driver) {
				a, d := demo(t)
				d.Keys("1")
				_ = d.Frame()
				return a, d
			},
		},
		{
			name: "2 Work (tree)",
			setup: func(t *testing.T) (*App, *Driver) {
				a, d := demo(t)
				d.Keys("2")
				_ = d.Frame()
				return a, d
			},
		},
		{
			name: "2 Work (table)",
			setup: func(t *testing.T) (*App, *Driver) {
				a, d := demo(t)
				d.Keys("2", "g", "t")
				_ = d.Frame()
				return a, d
			},
		},
		{
			name: "5 Projects",
			setup: func(t *testing.T) (*App, *Driver) {
				a, d := demo(t)
				d.Keys("5")
				_ = d.Frame()
				return a, d
			},
		},
		{
			name: "Project Portal (home)",
			setup: func(t *testing.T) (*App, *Driver) {
				return openProjectTabOnNav(t, "home")
			},
		},
		{
			name: "Project Portal (tasks)",
			setup: func(t *testing.T) (*App, *Driver) {
				return openProjectTabOnNav(t, "tasks")
			},
		},
		{
			name: "Project Portal (pages)",
			setup: func(t *testing.T) (*App, *Driver) {
				return openProjectTabOnNav(t, "pages")
			},
		},
	}

	// 1. Every active normal-mode binding in every default context must execute
	// cleanly without raw status-line errors.
	for _, sc := range contexts {
		t.Run("active_bindings/"+sc.name, func(t *testing.T) {
			a, d := sc.setup(t)
			active := a.activeKeyBindings(a.State())
			if len(active) == 0 {
				t.Fatalf("%s reported 0 active bindings", sc.name)
			}
			baseState := a.State()
			baseRev, _ := a.Store.LatestChangeSetNumber()
			for _, b := range active {
				if b.rawCommand == "" || skipMatrixCmd[b.rawCommand] {
					continue
				}
				a.status = ""
				d.Send(core.CommandEvent{Command: core.Command{
					ID:     b.rawCommand,
					Args:   b.args,
					Source: "binding",
					Actor:  User,
				}})
				assertNoForbiddenStatus(t, sc.name+" binding ["+b.keys+" -> "+b.rawCommand+"]", a.Status())
				curRev, _ := a.Store.LatestChangeSetNumber()
				if curRev != baseRev || a.Space() != baseState.ActivePage || a.Mode() != ModeNormal {
					a, d = sc.setup(t)
					baseState = a.State()
					baseRev, _ = a.Store.LatestChangeSetNumber()
				} else if a.Modal() != "" {
					a.Shell.State = a.dropModals(a.State())
					a.Shell.State = withFocus(a.State(), baseState.ActiveFrame().FocusID)
				}
			}
		})
	}

	// 2. Every non-internal registered CommandSpec with Required: true params
	// MUST open an auto-form (or inline palette form) when invoked with empty args
	// from palette and help — never emitting a raw status-line error.
	t.Run("required_param_specs_open_autoform", func(t *testing.T) {
		probe, _ := demo(t)
		reg := core.DefaultRegistry()
		reg.Register(probe.bind.specs...)
		checked := 0
		for _, sp := range reg.List() {
			if internal(sp) || len(missingRequiredParams(sp.Params, nil)) == 0 {
				continue
			}
			checked++
			for _, source := range []string{"palette", "help"} {
				a, d := demo(t)
				d.Keys("2", "g", "t")
				_ = d.Frame()
				a.status = ""
				d.Send(core.CommandEvent{Command: core.Command{
					ID:     sp.ID,
					Args:   map[string]any{},
					Source: source,
					Actor:  User,
				}})
				assertNoForbiddenStatus(t, source+" "+sp.ID+" empty args", a.Status())
				if a.Modal() == "" {
					t.Errorf("[%s] command %q declares required params %+v but opened no form/picker modal (status=%q)", source, sp.ID, sp.Params, a.Status())
				}
				if a.Modal() == missingArgsModalID {
					fm := a.activeForm(missingArgsModalID)
					if fm == nil || len(fm.Model().Specs) == 0 {
						t.Errorf("[%s] command %q opened empty missing.args form", source, sp.ID)
					}
					_ = modal.CloseCancel
				}
			}
		}
		if checked < 4 {
			t.Fatalf("expected at least 4 required-param command specs to be evaluated, got %d", checked)
		}
	})
}
