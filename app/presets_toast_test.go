package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/actions"
	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/toast"
)

func hasQuitEffect(a *App) bool {
	return slices.ContainsFunc(a.Shell.LastEffects, func(e core.Effect) bool {
		_, ok := e.(core.QuitEffect)
		return ok
	})
}

func TestWave4ConfirmationModalQuitAndDaemonReset(t *testing.T) {
	// 1. confirm.quit = true (default) vs false.
	a, d := demo(t)
	d.Keys("Q")
	if a.Modal() != "confirm" || a.Mode() != ModeConfirm {
		t.Fatalf("Q did not open confirm modal: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	frame := d.Frame()
	if !strings.Contains(frame, "Quit Matomeru? Your daemon panes will keep running in the background.") ||
		!strings.Contains(frame, "[y/enter]") || !strings.Contains(frame, "[n/esc] Cancel") {
		t.Fatalf("confirm modal missing expected text:\n%s", frame)
	}
	if hasQuitEffect(a) {
		t.Fatal("Q emitted QuitEffect before confirmation")
	}

	// Cancel via n, esc, q, ctrl+c.
	for _, cancelKey := range []string{"n", "esc", "q", "ctrl+c"} {
		if a.Modal() != "confirm" {
			d.Keys("Q")
		}
		d.Keys(cancelKey)
		if a.Modal() != "" || hasQuitEffect(a) {
			t.Fatalf("%s did not cancel quit confirm: modal=%q quit=%v", cancelKey, a.Modal(), hasQuitEffect(a))
		}
	}

	// Confirm via y and enter.
	d.Keys("Q", "y")
	if a.Modal() != "" || !hasQuitEffect(a) {
		t.Fatalf("Q y did not emit QuitEffect: modal=%q quit=%v", a.Modal(), hasQuitEffect(a))
	}
	d.Keys("Q", "enter")
	if a.Modal() != "" || !hasQuitEffect(a) {
		t.Fatalf("Q enter did not emit QuitEffect: modal=%q quit=%v", a.Modal(), hasQuitEffect(a))
	}

	// Opt-out: confirm.quit = false quits immediately.
	a.cfg.userSettings[SetConfirmQuit] = false
	a.reloadSettings()
	d.Keys("Q")
	if a.Modal() != "" || !hasQuitEffect(a) {
		t.Fatalf("Q with confirm.quit=false failed: modal=%q quit=%v", a.Modal(), hasQuitEffect(a))
	}

	// 2. confirm.daemonReset = true (default) vs false, showing live daemon pane count.
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("SHELL", "/bin/sh")

	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	_, client := startTestDaemon(t, store)
	da, err := New(store, Options{Live: true, Daemon: client, Watch: time.Hour, FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { da.Close() })
	dd := NewDriver(da, 120, 36)

	for _, title := range []string{"live-1", "live-2"} {
		p, _, err := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{
			"title":   title,
			"harness": "sh",
			"command": "exec cat",
			"cwd":     t.TempDir(),
		}}, User)
		if err != nil {
			t.Fatal(err)
		}
		if err := da.attach(*p); err != nil {
			t.Fatal(err)
		}
	}
	wake(dd)

	dd.Keys("1", "g", "R")
	if da.Modal() != "confirm" || da.Mode() != ModeConfirm {
		t.Fatalf("g R did not open confirm modal: modal=%q mode=%q status=%q", da.Modal(), da.Mode(), da.Status())
	}
	resetFrame := dd.Frame()
	wantMsg := "Drop all 2 live daemon panes and reset the daemon? This cannot be undone."
	if !strings.Contains(resetFrame, wantMsg) {
		t.Fatalf("daemon reset confirm missing pane count message %q:\n%s", wantMsg, resetFrame)
	}
	// Cancel leaves panes running.
	dd.Keys("n")
	if da.Modal() != "" {
		t.Fatalf("n did not close daemon reset confirm modal: %q", da.Modal())
	}
	if live, err := client.List(context.Background()); err != nil || len(live) != 2 {
		t.Fatalf("daemon panes after cancel = %d (err=%v), want 2", len(live), err)
	}

	// Confirm via y drops both panes.
	dd.Keys("g", "R", "y")
	if da.Modal() != "" || !strings.Contains(da.Status(), "daemon reset") {
		t.Fatalf("g R y did not reset daemon: modal=%q status=%q", da.Modal(), da.Status())
	}
	if live, err := client.List(context.Background()); err != nil || len(live) != 0 {
		t.Fatalf("daemon panes after confirm = %d (err=%v), want 0", len(live), err)
	}

	// Opt-out: confirm.daemonReset = false resets immediately without prompting.
	p3, _, err := store.Create(data.CreateInput{Kind: data.KindPane, Fields: map[string]any{
		"title":   "live-3",
		"harness": "sh",
		"command": "exec cat",
		"cwd":     t.TempDir(),
	}}, User)
	if err != nil {
		t.Fatal(err)
	}
	if err := da.attach(*p3); err != nil {
		t.Fatal(err)
	}
	da.cfg.userSettings[SetConfirmDaemonReset] = false
	da.reloadSettings()
	dd.Keys("g", "R")
	if da.Modal() != "" || !strings.Contains(da.Status(), "daemon reset") {
		t.Fatalf("g R with confirm.daemonReset=false failed: modal=%q status=%q", da.Modal(), da.Status())
	}
	if live, err := client.List(context.Background()); err != nil || len(live) != 0 {
		t.Fatalf("daemon panes after immediate reset = %d (err=%v), want 0", len(live), err)
	}
}

func TestWave4TablePresetsKeyedByTableIDAndPersist(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mtm-cfg")
	a, d := configured(t, dir)

	// Configure work.table with filter + sort + group and save via g v -> s.
	d.Keys("2", "/")
	d.Type("tier")
	d.Keys("enter", "o") // sort title asc
	d.Keys("g", "b", "/")
	d.Type("workstatus")
	d.Keys("enter")

	if wt := a.nav.tables[workTable]; wt.Query != "tier" || wt.SortField != "title" || wt.GroupField != "workStatus" {
		t.Fatalf("work.table before preset save: q=%q sort=%q group=%q", wt.Query, wt.SortField, wt.GroupField)
	}

	// Open presets picker (g v), press s to save current view as "tier-grouped".
	d.Keys("g", "v")
	if a.Modal() != "table.presets" || a.Mode() != ModePicker {
		t.Fatalf("g v did not open presets picker: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Keys("s")
	if a.Modal() != "launch" || a.Mode() != ModeForm {
		t.Fatalf("s in presets picker did not open save form: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Keys("i")
	d.Type("tier-grouped")
	d.Keys("ctrl+s")
	if a.Modal() != "table.presets" {
		t.Fatalf("saving preset should return to presets picker: modal=%q status=%q", a.Modal(), a.Status())
	}

	// Set "tier-grouped" as default via d in picker.
	d.Keys("d")
	if def, ok := a.nav.presets.Default(workTable); !ok || def.Name != "tier-grouped" {
		t.Fatalf("d did not set default preset: ok=%v def=%+v", ok, def)
	}
	if !strings.Contains(d.Frame(), "default") {
		t.Fatalf("default badge not shown in presets picker:\n%s", d.Frame())
	}
	d.Keys("esc", "esc")

	// Clear work.table and save a second preset "clean" via direct command.
	d.Keys("/", "esc", "g", "b", "enter", "o", "o") // clear filter, group, sort
	a.Engine.Step(a.State(), nil, core.CommandEvent{Command: core.Command{
		ID:   CmdTablePresetSave,
		Args: map[string]any{"name": "clean"},
	}})
	if len(a.nav.presets.List(workTable)) != 2 {
		t.Fatalf("expected 2 presets on work.table, got %+v", a.nav.presets.List(workTable))
	}

	// Switch back to "tier-grouped" via g v -> enter.
	d.Keys("g", "v", "enter")
	if wt := a.nav.tables[workTable]; wt.Query != "tier" || wt.SortField != "title" || wt.GroupField != "workStatus" {
		t.Fatalf("applying preset did not restore work.table state: q=%q sort=%q group=%q", wt.Query, wt.SortField, wt.GroupField)
	}

	// Configure a completely separate preset on runs.table (proving tableID keying).
	d.Keys("3", "/")
	d.Type("gate")
	d.Keys("enter", "g", "s", "/")
	d.Type("title")
	d.Keys("enter", "g", "v", "s", "i")
	d.Type("gate-runs")
	d.Keys("ctrl+s", "d", "esc", "esc")

	// Verify distinct presets per table ID.
	if len(a.nav.presets.List(workTable)) != 2 || len(a.nav.presets.List(runsTable)) != 1 {
		t.Fatalf("presets not isolated by tableID: work=%+v runs=%+v", a.nav.presets.List(workTable), a.nav.presets.List(runsTable))
	}
	if _, existsOnRuns := a.nav.presets.Get(runsTable, "tier-grouped"); existsOnRuns {
		t.Fatal("work.table preset leaked into runs.table")
	}

	// Delete "clean" via g v -> down -> x.
	d.Keys("2", "g", "v", "down", "x", "esc", "esc")
	if got := a.nav.presets.List(workTable); len(got) != 1 || got[0].Name != "tier-grouped" {
		t.Fatalf("x did not delete second preset: %+v", got)
	}

	// Verify settings.json & ExportConfig contain table.presets.
	exported, err := ExportConfig(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	var cfg ConfigExport
	if err := json.Unmarshal(exported, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Settings[SetTablePresets] == nil || cfg.UserSettings[SetTablePresets] == nil {
		t.Fatalf("ExportConfig missing %q: settings=%v", SetTablePresets, cfg.UserSettings)
	}

	// Reload a brand-new App over the same ConfigDir and verify ApplyDefault runs automatically.
	b, bd := configured(t, dir)
	bd.Keys("2")
	if wt := b.nav.tables[workTable]; wt == nil || wt.Query != "tier" || wt.SortField != "title" || wt.SortDir != table.SortAsc || wt.GroupField != "workStatus" {
		t.Fatalf("reloaded work.table did not auto-apply default preset: %+v", wt)
	}
	bd.Keys("3")
	if rt := b.nav.tables[runsTable]; rt == nil || rt.Query != "gate" || rt.SortField != "title" {
		t.Fatalf("reloaded runs.table did not auto-apply default preset: %+v", rt)
	}
}

func TestWave4ActionsJSONAndNamespaceGroupedRecordActions(t *testing.T) {
	dir := t.TempDir()
	actionsJSON := `[
		{
			"noun": "pane",
			"verb": "inspect-custom",
			"description": "Inspect custom pane details",
			"command": ["echo", "pane={{id}}", "{{title}}", "{{harness}}"]
		},
		{
			"noun": "task",
			"verb": "ship",
			"description": "Ship this task",
			"command": ["echo", "ship={{label}}", "status={{workStatus}}", "data={{data}}"]
		},
		{
			"noun": "task",
			"verb": "fg-open",
			"description": "Open task in foreground tool",
			"command": ["mytool", "{{id}}", "{{title}}"],
			"foreground": true,
			"dir": "/tmp",
			"env": {"MTM_TASK_ID": "{{id}}"}
		}
	]`
	if err := os.WriteFile(filepath.Join(dir, actions.ActionsFile), []byte(actionsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	// Also bind a key in bindings.json to a user action to verify CheckBindings accepts it.
	if err := os.WriteFile(filepath.Join(dir, bindingsFileName), []byte(`[{"mode":"normal","scope":"task","keys":["g","S"],"command":"task.ship"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if rep, err := CheckBindings(dir); err != nil || len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings with actions.json + bindings.json failed: err=%v errors=%+v", err, rep.Errors)
	}

	a, d := configured(t, dir)

	// Verify user actions appear in Palette (:).
	d.Keys(":")
	d.Type("task.ship")
	if !strings.Contains(d.Frame(), "task.ship") {
		t.Fatalf("palette missing task.ship from actions.json:\n%s", d.Frame())
	}
	d.Keys("esc", "esc", "esc")

	// 1. Focus a PANE and open `;`: pane.* MUST sit strictly above record.* with visible headings.
	d.Keys("1")
	if _, ok := a.SelectedPane(); !ok {
		t.Fatal("expected a selected pane on 1 Panes")
	}
	d.Keys(";")
	if a.Modal() != "record.actions" {
		t.Fatalf("; on pane did not open record actions: modal=%q", a.Modal())
	}
	paneIDs := pickerIDs(a, "record.actions")
	paneIdx := slices.Index(paneIDs, "pane.inspect-custom")
	recIdx := slices.IndexFunc(paneIDs, func(id string) bool { return strings.HasPrefix(id, "record.") })
	if paneIdx < 0 || recIdx < 0 || paneIdx >= recIdx {
		t.Fatalf("expected pane.* above record.* on pane, got paneIdx=%d recIdx=%d ids=%v", paneIdx, recIdx, paneIDs)
	}
	paneFrame := d.Frame()
	if !strings.Contains(paneFrame, "[pane]") || !strings.Contains(paneFrame, "[record]") {
		t.Fatalf("pane actions picker missing namespace headings:\n%s", paneFrame)
	}
	d.Keys("esc", "esc")

	// 2. Focus a TASK and open `;`: task.* MUST sit strictly above record.* with visible headings.
	task := mkRec(t, a, data.KindTask, map[string]any{"title": "Ship Wave 4", "workStatus": "active"})
	browseTo(t, a, d, task)
	d.Keys(";")
	taskIDs := pickerIDs(a, "record.actions")
	taskShipIdx := slices.Index(taskIDs, "task.ship")
	taskFgIdx := slices.Index(taskIDs, "task.fg-open")
	taskRecIdx := slices.IndexFunc(taskIDs, func(id string) bool { return strings.HasPrefix(id, "record.") })
	if taskShipIdx < 0 || taskFgIdx < 0 || taskRecIdx < 0 || taskShipIdx >= taskRecIdx || taskFgIdx >= taskRecIdx {
		t.Fatalf("expected task.* above record.* on task, got ship=%d fg=%d rec=%d ids=%v", taskShipIdx, taskFgIdx, taskRecIdx, taskIDs)
	}
	taskFrame := d.Frame()
	if !strings.Contains(taskFrame, "[task]") || !strings.Contains(taskFrame, "[record]") {
		t.Fatalf("task actions picker missing namespace headings:\n%s", taskFrame)
	}

	// Execute background action task.ship from `;` and verify template expansion in created run.
	d.Keys("/")
	d.Type("task.ship")
	d.Keys("enter")
	runsRes, err := a.Store.Query(data.Query{Kind: data.KindRun})
	if err != nil {
		t.Fatal(err)
	}
	foundShip := false
	for _, r := range runsRes.Items {
		if r.Text("title") == "task.ship" {
			cmd := r.Text("command")
			if strings.Contains(cmd, "ship=Ship Wave 4") && strings.Contains(cmd, "status=active") && strings.Contains(cmd, "data={") {
				foundShip = true
				break
			}
		}
	}
	if !foundShip {
		t.Fatalf("task.ship did not create run with expanded vars (status=%q)", a.Status())
	}

	// Execute foreground action task.fg-open from `;` and verify core.ExecEffect.
	d.Keys(";", "/")
	d.Type("task.fg-open")
	d.Keys("enter")
	var execEff *core.ExecEffect
	for _, eff := range a.Shell.LastEffects {
		if ee, ok := eff.(core.ExecEffect); ok {
			cp := ee
			execEff = &cp
			break
		}
	}
	if execEff == nil {
		t.Fatalf("foreground action did not emit ExecEffect: effects=%+v", a.Shell.LastEffects)
	}
	if execEff.Name != "mytool" || !slices.Equal(execEff.Args, []string{task.ID, "Ship Wave 4"}) || execEff.Dir != "/tmp" || !slices.Contains(execEff.Env, "MTM_TASK_ID="+task.ID) {
		t.Fatalf("unexpected ExecEffect: %+v", execEff)
	}
}

func TestWave4ToastsRenderExpireAndOptOut(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := SeedDemo(store); err != nil {
		t.Fatal(err)
	}
	cur := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	a, err := New(store, Options{FS: smokeFS(), Now: func() time.Time { return cur }})
	if err != nil {
		t.Fatal(err)
	}
	d := NewDriver(a, 120, 36)

	// Push toast -> rendered in frame.
	a.pushToast("Preset saved!", toast.Ok, 2*time.Second)
	d.Send(resize(120, 36))
	if frame := d.Frame(); !strings.Contains(frame, "Preset saved!") {
		t.Fatalf("active toast not rendered in frame:\n%s", frame)
	}

	// Advance clock past TTL -> expires cleanly on wakeUp/render.
	cur = cur.Add(3 * time.Second)
	wake(d)
	if a.rt.toasts.Len() != 0 {
		t.Fatalf("toast did not expire: len=%d", a.rt.toasts.Len())
	}
	if frame := d.Frame(); strings.Contains(frame, "Preset saved!") {
		t.Fatalf("expired toast still rendered in frame:\n%s", frame)
	}

	// Opt-out: ui.toast = false suppresses pushing & rendering.
	a.cfg.userSettings[SetToast] = false
	a.reloadSettings()
	a.applySettings()
	a.pushToast("Should not show", toast.Info, 5*time.Second)
	d.Send(resize(120, 36))
	if a.rt.toasts.Len() != 0 || strings.Contains(d.Frame(), "Should not show") {
		t.Fatalf("ui.toast=false did not suppress toast: len=%d", a.rt.toasts.Len())
	}
}

func TestWave4TreeGuidesAndFocusJumpHints(t *testing.T) {
	a, d := demo(t)

	// 1. Tree guide lines: ui.treeGuides = true (default) vs false on Outline (T).
	d.Keys("T")
	if a.Modal() != "outline" {
		t.Fatalf("T did not open outline: modal=%q", a.Modal())
	}
	withGuides := d.Frame()
	if !strings.Contains(withGuides, "└─") && !strings.Contains(withGuides, "├─") {
		t.Fatalf("outline with ui.treeGuides=true missing box-drawing guides:\n%s", withGuides)
	}
	d.Keys("esc")

	a.cfg.userSettings[SetTreeGuides] = false
	a.reloadSettings()
	a.applySettings()
	d.Keys("T")
	withoutGuides := d.Frame()
	if strings.Contains(withoutGuides, "└─") || strings.Contains(withoutGuides, "├─") {
		t.Fatalf("outline with ui.treeGuides=false still rendered box-drawing guides:\n%s", withoutGuides)
	}
	d.Keys("esc")

	// 2. Focus jump hints (g f -> focus.hints over layout.Solved.FocusOrder).
	d.Keys("1")
	initialFocus := a.Focus()
	d.Keys("g", "f")
	if a.Mode() != ModeHints || a.panes.hints == nil || !a.panes.hints.focusJump || !a.panes.hints.session.Active() {
		t.Fatalf("g f did not activate focus jump hints: mode=%q hints=%+v", a.Mode(), a.panes.hints)
	}
	targets := a.panes.hints.session.Targets()
	if len(targets) < 2 {
		t.Fatalf("expected at least 2 focus hint targets on 1 Panes, got %+v", targets)
	}
	hintsFrame := d.Frame()
	if !strings.Contains(hintsFrame, "Hints") || !strings.Contains(hintsFrame, "["+targets[0].Label+"]") {
		t.Fatalf("focus hints overlay not rendered:\n%s", hintsFrame)
	}

	// Cancel with esc restores mode and focus.
	d.Keys("esc")
	if a.Mode() != ModeNormal || a.panes.hints != nil || a.Focus() != initialFocus {
		t.Fatalf("esc did not cancel focus hints cleanly: mode=%q focus=%q", a.Mode(), a.Focus())
	}

	// Re-open with g f, pick a target whose ID differs from initialFocus, and type its label.
	d.Keys("g", "f")
	var pickTargetID, pickLabel string
	for _, tgt := range a.panes.hints.session.Targets() {
		if tgt.ID != initialFocus {
			pickTargetID = tgt.ID
			pickLabel = tgt.Label
			break
		}
	}
	if pickTargetID == "" || pickLabel == "" {
		t.Fatalf("no alternate focus target found among %+v", a.panes.hints.session.Targets())
	}
	d.Type(pickLabel)
	if a.Mode() != ModeNormal || a.panes.hints != nil {
		t.Fatalf("typing focus hint label %q did not exit ModeHints: mode=%q", pickLabel, a.Mode())
	}
	if a.Focus() != pickTargetID {
		t.Fatalf("focus after hint %q = %q, want %q", pickLabel, a.Focus(), pickTargetID)
	}
}

func TestWave4BindingsCheckZeroErrors(t *testing.T) {
	rep, err := CheckBindings("")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings reported %d error(s): %+v", len(rep.Errors), rep.Errors)
	}
	specs := CommandSpecs()
	for _, want := range []string{
		CmdTablePresetPick, CmdTablePresetSave, CmdTablePresetApply, CmdTablePresetDefault, CmdTablePresetDelete,
		CmdFocusHints, CmdConfirmAccept, CmdConfirmCancel,
	} {
		if !slices.ContainsFunc(specs, func(s core.CommandSpec) bool { return s.ID == want }) {
			t.Errorf("CommandSpecs missing %q", want)
		}
	}
}
