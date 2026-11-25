package app

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ofam.dev/matomeru/contextitems"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/handlers"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/commandline"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/hints"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
)

func setupHintsApp(t *testing.T, lines []string, settings map[string]any) (*App, *Driver, *wave3FakeHost, *data.Record, string) {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	cwd := t.TempDir()
	host := newWave3FakeHost()
	pane, _, err := store.Create(data.CreateInput{
		Kind: data.KindPane,
		Fields: map[string]any{
			"title":     "hints-mod-pane",
			"cwd":       cwd,
			"runStatus": "running",
		},
	}, User)
	if err != nil {
		t.Fatal(err)
	}
	host.setLines(pane.ID, lines, 1)

	a, err := New(store, Options{Host: host, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	for k, v := range settings {
		a.cfg.userSettings[k] = v
	}
	if len(settings) > 0 {
		a.reloadSettings()
		a.applySettings()
	}
	d := NewDriver(a, 120, 36)
	return a, d, host, pane, cwd
}

func triggerHintsFromCurrentState(t *testing.T, a *App) []hints.Target {
	t.Helper()
	step := a.paneHints(a.Shell.State, core.Command{ID: CmdPaneHints})
	a.Shell.State = step.State
	if a.Mode() != ModeHints || a.panes.hints == nil || !a.panes.hints.session.Active() {
		t.Fatalf("paneHints did not activate ModeHints: mode=%q status=%q", a.Mode(), a.Status())
	}
	return a.panes.hints.session.Targets()
}

func latestRunCommands(t *testing.T, store *data.Store) []string {
	t.Helper()
	res, err := store.Query(data.Query{Kind: data.KindRun})
	if err != nil {
		t.Fatal(err)
	}
	cmds := make([]string, 0, len(res.Items))
	for _, r := range res.Items {
		cmds = append(cmds, r.Text("command"))
	}
	return cmds
}

func hasCommand(cmds []string, want string) bool {
	for _, c := range cmds {
		if c == want {
			return true
		}
	}
	return false
}

func findTargetByLabel(targets []hints.Target, label string) (hints.Target, bool) {
	for _, tgt := range targets {
		if tgt.Label == label {
			return tgt, true
		}
	}
	return hints.Target{}, false
}

func findTargetByValueContains(targets []hints.Target, substr string) (hints.Target, bool) {
	for _, tgt := range targets {
		if strings.Contains(tgt.Value, substr) {
			return tgt, true
		}
	}
	return hints.Target{}, false
}

func TestPaneHintsModifierCopyOpenAndCtrlH(t *testing.T) {
	lines := []string{
		"1 https://ofam.dev/one",
		"2 app/view.go:10",
		"3 assets/diagram.png",
		"4 https://ofam.dev/four",
		"5 docs/readme.md",
		"6 assets/photo.png",
	}
	a, d, _, pane, cwd := setupHintsApp(t, lines, nil)

	// Activate hints via `f` and verify modifier legend.
	d.Keys("f")
	if a.Mode() != ModeHints || a.panes.hints == nil {
		t.Fatalf("f did not activate ModeHints: mode=%q status=%q", a.Mode(), a.Status())
	}
	if !strings.Contains(a.Status(), "ctrl opens · Shift inserts · alt reveals") {
		t.Fatalf("unexpected hints status line: %q", a.Status())
	}
	targets := a.panes.hints.session.Targets()
	if len(targets) != 6 {
		t.Fatalf("expected 6 targets, got %d: %+v", len(targets), targets)
	}

	// 1. Plain lowercase copies target to unnamed register + clipboard.
	d.Keys(targets[0].Label)
	if a.Mode() != ModeNormal || a.Register(core.RegUnnamed) != targets[0].Value || a.rt.live.clipboard != targets[0].Value {
		t.Fatalf("lowercase hint failed: mode=%q reg=%q clip=%q want=%q", a.Mode(), a.Register(core.RegUnnamed), a.rt.live.clipboard, targets[0].Value)
	}

	// 2. ctrl+<label> opens URL directly via OS opener.
	d.Keys("f")
	urlTgt, ok := findTargetByValueContains(a.panes.hints.session.Targets(), "https://ofam.dev/one")
	if !ok {
		t.Fatal("missing URL target")
	}
	d.Keys("ctrl+" + urlTgt.Label)
	wantURLCmd := systemOpener() + " " + handlers.Quote("https://ofam.dev/one")
	if cmds := latestRunCommands(t, a.Store); !hasCommand(cmds, wantURLCmd) {
		t.Fatalf("ctrl+label on URL did not run %q; got %v", wantURLCmd, cmds)
	}

	// 3. ctrl+<label> opens arbitrary local files (.go and .png) via OS opener.
	d.Keys("f")
	goTgt, ok := findTargetByValueContains(a.panes.hints.session.Targets(), "app/view.go:10")
	if !ok {
		t.Fatal("missing .go target")
	}
	d.Keys("ctrl+" + goTgt.Label)
	wantGoCmd := systemOpener() + " " + handlers.Quote(filepath.Join(cwd, "app/view.go"))
	if cmds := latestRunCommands(t, a.Store); !hasCommand(cmds, wantGoCmd) {
		t.Fatalf("ctrl+label on .go did not run %q; got %v", wantGoCmd, cmds)
	}

	d.Keys("f")
	pngTgt, ok := findTargetByValueContains(a.panes.hints.session.Targets(), "assets/diagram.png")
	if !ok {
		t.Fatal("missing .png target")
	}
	d.Keys("ctrl+" + pngTgt.Label)
	wantPngCmd := systemOpener() + " " + handlers.Quote(filepath.Join(cwd, "assets/diagram.png"))
	if cmds := latestRunCommands(t, a.Store); !hasCommand(cmds, wantPngCmd) {
		t.Fatalf("ctrl+label on .png did not run %q; got %v", wantPngCmd, cmds)
	}

	// 4. openContextValue also falls back cleanly to systemOpenTarget when no handler rule matches.
	a.Shell.State = a.openContextValue(a.Shell.State, contextitems.KindFile, "assets/fallback.bin", pane).State
	wantBinCmd := systemOpener() + " " + handlers.Quote(filepath.Join(cwd, "assets/fallback.bin"))
	if cmds := latestRunCommands(t, a.Store); !hasCommand(cmds, wantBinCmd) {
		t.Fatalf("openContextValue fallback did not run %q; got %v", wantBinCmd, cmds)
	}

	// 5. Home-row ctrl+h matches label 'h' (6th target in DefaultAlphabet) instead of backspacing.
	d.Keys("f")
	hTgt, ok := findTargetByLabel(a.panes.hints.session.Targets(), "h")
	if !ok {
		t.Fatalf("expected 6th target to have label 'h', got %+v", a.panes.hints.session.Targets())
	}
	d.Keys("ctrl+h")
	if a.Mode() != ModeNormal || a.panes.hints != nil {
		t.Fatalf("ctrl+h did not match label 'h' and exit hints: mode=%q status=%q", a.Mode(), a.Status())
	}
	wantHCmd := systemOpener() + " " + handlers.Quote(filepath.Join(cwd, "assets/photo.png"))
	if cmds := latestRunCommands(t, a.Store); !hasCommand(cmds, wantHCmd) {
		t.Fatalf("ctrl+h did not open %q (%q); got %v", hTgt.Value, wantHCmd, cmds)
	}
}

func TestPaneHintsShiftInsertTargets(t *testing.T) {
	// Generate 28 targets so multi-char labels exist alongside single-char labels.
	lines := make([]string, 0, 28)
	for i := 1; i <= 28; i++ {
		lines = append(lines, fmt.Sprintf("https://ofam.dev/item/%02d", i))
	}
	a, d, host, pane, _ := setupHintsApp(t, lines, nil)

	// 1. Main terminal pane (triggered via `f` from 1 Panes).
	d.Keys("f")
	targets := a.panes.hints.session.Targets()
	var single hints.Target
	var multi hints.Target
	for _, tgt := range targets {
		if len(tgt.Label) == 1 && single.Label == "" {
			single = tgt
		}
		if len(tgt.Label) == 2 && multi.Label == "" {
			multi = tgt
		}
	}
	if single.Label == "" || multi.Label == "" {
		t.Fatalf("expected both 1-char and 2-char hint labels, got single=%+v multi=%+v", single, multi)
	}
	d.Keys(strings.ToUpper(single.Label))
	if a.Mode() != ModeNormal || a.panes.hints != nil {
		t.Fatalf("Shift single-char did not exit hints: mode=%q", a.Mode())
	}
	if got := host.joinedWrites(pane.ID); !strings.Contains(got, single.Value) {
		t.Fatalf("Shift hint did not insert %q into main terminal pane; writes=%q", single.Value, got)
	}

	// 2. Multi-char label with Shift ONLY on the final character -> inserts into main terminal pane.
	d.Keys("f")
	d.Keys(multi.Label[:1])
	if a.Mode() != ModeHints || a.panes.hints == nil || a.panes.hints.session.Prefix() != multi.Label[:1] {
		t.Fatalf("first char of multi-char label did not narrow prefix: mode=%q status=%q", a.Mode(), a.Status())
	}
	d.Keys(strings.ToUpper(multi.Label[1:]))
	if a.Mode() != ModeNormal || a.panes.hints != nil {
		t.Fatalf("final Shift char did not complete match: mode=%q", a.Mode())
	}
	if got := host.joinedWrites(pane.ID); !strings.Contains(got, multi.Value) {
		t.Fatalf("multi-char final-Shift hint did not insert %q into terminal pane; writes=%q", multi.Value, got)
	}

	// 3. Active *richComposerModal input.
	d.Keys("c")
	cm := a.activeRichComposer()
	if cm == nil || a.Mode() != ModeComposer {
		t.Fatalf("c did not open richComposerModal: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Type("See ")
	targets = triggerHintsFromCurrentState(t, a)
	// Use a 2-char label with Shift ONLY on the final char inside composer too.
	d.Keys(multi.Label[:1], strings.ToUpper(multi.Label[1:]))
	if a.Mode() != ModeComposer || a.activeRichComposer() == nil {
		t.Fatalf("composer modal/mode not restored after Shift hint: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	if got := cm.in.Value(); got != "See "+multi.Value {
		t.Fatalf("composer value = %q, want %q", got, "See "+multi.Value)
	}
	d.Keys("esc")

	// 4. Active *modal.FormModal field (editing a text field).
	fm := form.New("Test Form", []core.ParamSpec{
		{Name: "url", Type: core.ParamString},
	}, map[string]any{"url": "ref: "})
	fm.Editing = true
	formModal := modal.NewFormModal(fm, modal.FormOptions{ID: "hint-form"})
	a.Shell.State = a.pushModal(a.Shell.State, formModal)
	if a.Modal() != "hint-form" || a.Mode() != ModeFormEdit {
		t.Fatalf("expected hint-form in ModeFormEdit, got modal=%q mode=%q", a.Modal(), a.Mode())
	}
	targets = triggerHintsFromCurrentState(t, a)
	d.Keys(strings.ToUpper(single.Label))
	if a.Modal() != "hint-form" || a.Mode() != ModeFormEdit {
		t.Fatalf("form modal/mode not restored after Shift hint: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	if got := fm.Values["url"]; got != "ref: "+single.Value {
		t.Fatalf("form field value = %v, want %q", got, "ref: "+single.Value)
	}
	a.Shell.State = a.popModal(a.Shell.State, modal.CloseCancel)

	// 5. Active *modal.CommandLineModal.
	cl := commandline.New(commandline.PrefixShell)
	cl.Insert("echo ")
	clModal := modal.NewCommandLineModal(cl, modal.CommandLineOptions{ID: "commandline"})
	a.Shell.State = a.pushModal(a.Shell.State, clModal)
	triggerHintsFromCurrentState(t, a)
	d.Keys(strings.ToUpper(single.Label))
	if a.Modal() != "commandline" || a.Mode() != ModeCommandLine {
		t.Fatalf("commandline modal not restored: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	if got := cl.Value(); got != "echo "+single.Value {
		t.Fatalf("commandline value = %q, want %q", got, "echo "+single.Value)
	}
	a.Shell.State = a.popModal(a.Shell.State, modal.CloseCancel)

	// 6. Active *modal.PickerModal in filter mode.
	pm := picker.New("Items", []picker.Item{{ID: "1", Title: single.Value}})
	pickModal := modal.NewPickerModal(pm, modal.PickerOptions{ID: "hint-picker", StartInFilter: true})
	a.Shell.State = a.pushModal(a.Shell.State, pickModal)
	triggerHintsFromCurrentState(t, a)
	d.Keys(strings.ToUpper(single.Label))
	if a.Modal() != "hint-picker" || a.Mode() != ModePickerFilter {
		t.Fatalf("picker modal not restored: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	if pm.Query != single.Value {
		t.Fatalf("picker filter query = %q, want %q", pm.Query, single.Value)
	}
	a.Shell.State = a.popModal(a.Shell.State, modal.CloseCancel)

	// 7. Active inline ModeFilter query (on 3 Runs table while pane remains selected in 1 Panes).
	d.Keys("3", "/")
	if a.Mode() != ModeFilter {
		t.Fatalf("/ on 3 Runs did not enter ModeFilter: mode=%q", a.Mode())
	}
	d.Type("q:")
	triggerHintsFromCurrentState(t, a)
	d.Keys(strings.ToUpper(single.Label))
	if a.Mode() != ModeFilter || a.Space() != SpaceRuns {
		t.Fatalf("ModeFilter/SpaceRuns not restored after Shift hint: space=%q mode=%q", a.Space(), a.Mode())
	}
	if got := a.nav.tables[runsTable].Query; got != "q:"+single.Value {
		t.Fatalf("inline filter query = %q, want %q", got, "q:"+single.Value)
	}
	d.Keys("esc")

	// 8. Active inline ModeOutlineEdit (on 2 Work task tree).
	d.Keys("2", "o")
	if a.Mode() != ModeOutlineEdit || a.trees.activeEdit() == nil {
		t.Fatalf("o on 2 Work did not enter ModeOutlineEdit: mode=%q", a.Mode())
	}
	d.Type("Fix ")
	triggerHintsFromCurrentState(t, a)
	d.Keys(strings.ToUpper(single.Label))
	if a.Mode() != ModeOutlineEdit || a.trees.activeEdit() == nil {
		t.Fatalf("ModeOutlineEdit not restored after Shift hint: mode=%q", a.Mode())
	}
	if got := a.trees.activeEdit().Value(); got != "Fix "+single.Value {
		t.Fatalf("inline outline edit value = %q, want %q", got, "Fix "+single.Value)
	}
	d.Keys("ctrl+c")

	// 9. Graceful fallback when no active/main input exists.
	emptyStore, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { emptyStore.Close() })
	emptyApp, err := New(emptyStore, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { emptyApp.Close() })
	emptyApp.insertHintTarget(emptyApp.State(), &hintsState{returnMode: ModeNormal}, nil, contextitems.KindURL, "https://ofam.dev/fallback")
	if emptyApp.Register(core.RegUnnamed) != "https://ofam.dev/fallback" || emptyApp.rt.live.clipboard != "https://ofam.dev/fallback" {
		t.Fatalf("fallback did not copy value: reg=%q clip=%q", emptyApp.Register(core.RegUnnamed), emptyApp.rt.live.clipboard)
	}
}

func TestPaneHintsAltReveal(t *testing.T) {
	lines := []string{
		"1 https://ofam.dev/reveal-url",
		"2 app/view.go:42",
	}
	for i := 3; i <= 28; i++ {
		lines = append(lines, fmt.Sprintf("%d assets/file_%02d.png", i, i))
	}
	a, d, _, _, cwd := setupHintsApp(t, lines, nil)

	wantRevealCmd := func(fullPath string) string {
		if runtime.GOOS == "darwin" {
			return "open -R " + handlers.Quote(fullPath)
		}
		return systemOpener() + " " + handlers.Quote(filepath.Dir(fullPath))
	}

	// 1. alt+<single-char> on local file runs open -R '<path>' (darwin) or parent dir.
	d.Keys("f")
	targets := a.panes.hints.session.Targets()
	fileTgt, ok := findTargetByValueContains(targets, "app/view.go:42")
	if !ok {
		t.Fatal("missing local file target")
	}
	d.Keys("alt+" + fileTgt.Label)
	wantFileCmd := wantRevealCmd(filepath.Join(cwd, "app/view.go"))
	if cmds := latestRunCommands(t, a.Store); !hasCommand(cmds, wantFileCmd) {
		t.Fatalf("alt+label on local file did not run %q; got %v", wantFileCmd, cmds)
	}

	// 2. alt+<single-char> on URL falls back to open '<url>'.
	d.Keys("f")
	urlTgt, ok := findTargetByValueContains(a.panes.hints.session.Targets(), "https://ofam.dev/reveal-url")
	if !ok {
		t.Fatal("missing URL target")
	}
	d.Keys("alt+" + urlTgt.Label)
	wantURLCmd := systemOpener() + " " + handlers.Quote("https://ofam.dev/reveal-url")
	if cmds := latestRunCommands(t, a.Store); !hasCommand(cmds, wantURLCmd) {
		t.Fatalf("alt+label on URL did not fall back to %q; got %v", wantURLCmd, cmds)
	}

	// 3. Multi-char label with alt+ ONLY on the final character -> reveals local file.
	d.Keys("f")
	var multiA, multiB hints.Target
	for _, tgt := range a.panes.hints.session.Targets() {
		if len(tgt.Label) == 2 && strings.Contains(tgt.Value, "assets/file_") {
			if multiA.Label == "" {
				multiA = tgt
			} else if multiB.Label == "" {
				multiB = tgt
				break
			}
		}
	}
	if multiA.Label == "" || multiB.Label == "" {
		t.Fatalf("expected two 2-char file targets, got %+v %+v", multiA, multiB)
	}
	d.Keys(multiA.Label[:1], "alt+"+multiA.Label[1:])
	wantMultiACmd := wantRevealCmd(filepath.Join(cwd, multiA.Value))
	if cmds := latestRunCommands(t, a.Store); !hasCommand(cmds, wantMultiACmd) {
		t.Fatalf("multi-char final-alt did not run %q; got %v", wantMultiACmd, cmds)
	}

	// 4. Multi-char label with alt+ on the FIRST character followed by lowercase -> reveals local file.
	d.Keys("f")
	d.Keys("alt+"+multiB.Label[:1], multiB.Label[1:])
	wantMultiBCmd := wantRevealCmd(filepath.Join(cwd, multiB.Value))
	if cmds := latestRunCommands(t, a.Store); !hasCommand(cmds, wantMultiBCmd) {
		t.Fatalf("multi-char leading-alt did not run %q; got %v", wantMultiBCmd, cmds)
	}

	// 5. Backspace back to empty prefix clears pendingReveal so subsequent lowercase copies instead.
	d.Keys("f")
	d.Keys("alt+"+multiA.Label[:1], "backspace")
	if a.panes.hints == nil || a.panes.hints.pendingReveal || a.panes.hints.session.Prefix() != "" {
		t.Fatalf("backspace did not clear pendingReveal/prefix: hints=%+v", a.panes.hints)
	}
	beforeRuns := len(latestRunCommands(t, a.Store))
	d.Keys(multiA.Label[:1], multiA.Label[1:])
	if len(latestRunCommands(t, a.Store)) != beforeRuns {
		t.Fatalf("expected copy (no new run) after clearing pendingReveal with backspace")
	}
	if a.Register(core.RegUnnamed) != multiA.Value {
		t.Fatalf("expected %q copied after clearing pendingReveal, got %q", multiA.Value, a.Register(core.RegUnnamed))
	}
}

func TestPaneHintsModifierActionsOptOut(t *testing.T) {
	lines := []string{
		"1 https://ofam.dev/optout",
		"2 assets/optout.png",
	}
	a, d, host, pane, _ := setupHintsApp(t, lines, map[string]any{
		SetHintsModifierActions: false,
	})

	// Status line hides modifier hints when disabled.
	d.Keys("f")
	if got := a.Status(); got != "hints (2) · lowercase copies · esc/q cancels" {
		t.Fatalf("disabled hints status = %q, want %q", got, "hints (2) · lowercase copies · esc/q cancels")
	}
	targets := a.panes.hints.session.Targets()

	// 1. ctrl+<label> copies instead of opening.
	d.Keys("ctrl+" + targets[0].Label)
	if len(latestRunCommands(t, a.Store)) != 0 {
		t.Fatalf("ctrl+label opened target when hints.modifierActions=false")
	}
	if a.Register(core.RegUnnamed) != targets[0].Value {
		t.Fatalf("ctrl+label with modifierActions=false did not copy %q; reg=%q", targets[0].Value, a.Register(core.RegUnnamed))
	}

	// 2. Shift/UPPERCASE copies instead of inserting into pane.
	d.Keys("f")
	d.Keys(strings.ToUpper(targets[1].Label))
	if got := host.joinedWrites(pane.ID); got != "" {
		t.Fatalf("UPPERCASE hint wrote to pane when hints.modifierActions=false: %q", got)
	}
	if a.Register(core.RegUnnamed) != targets[1].Value {
		t.Fatalf("UPPERCASE hint with modifierActions=false did not copy %q; reg=%q", targets[1].Value, a.Register(core.RegUnnamed))
	}

	// 3. alt+<label> copies instead of revealing.
	d.Keys("f")
	d.Keys("alt+" + targets[1].Label)
	if len(latestRunCommands(t, a.Store)) != 0 {
		t.Fatalf("alt+label revealed target when hints.modifierActions=false")
	}
	if a.Register(core.RegUnnamed) != targets[1].Value {
		t.Fatalf("alt+label with modifierActions=false did not copy %q; reg=%q", targets[1].Value, a.Register(core.RegUnnamed))
	}
}
