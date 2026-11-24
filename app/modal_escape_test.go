package app

import (
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
)

func newClockedApp(t *testing.T, clock *time.Time, settings map[string]any) (*App, *Driver) {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := SeedDemo(store); err != nil {
		t.Fatal(err)
	}
	a, err := New(store, Options{
		FS:  smokeFS(),
		Now: func() time.Time { return *clock },
	})
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
	return a, NewDriver(a, 100, 32)
}

func TestModalEscapeGuardPolicies(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	t.Run("double policy default arms warning border disarms on key and closes on 2nd esc", func(t *testing.T) {
		a, d := newClockedApp(t, &now, nil)
		if got := a.cfg.Settings.String(SetModalEscapeGuard, ""); got != traits.EscapeGuardDouble {
			t.Fatalf("default %s = %q, want %q", SetModalEscapeGuard, got, traits.EscapeGuardDouble)
		}

		// Open nouns picker (ModePicker).
		d.Keys("g", "n")
		if a.Modal() != "nouns" {
			t.Fatalf("expected nouns modal, got %q", a.Modal())
		}
		// 1st esc arms guard with warning border & badge.
		d.Keys("esc")
		if a.Modal() != "nouns" {
			t.Fatalf("1st esc closed modal under double policy")
		}
		if !strings.Contains(a.Status(), "press esc again to close") {
			t.Fatalf("status = %q, want press esc again to close", a.Status())
		}
		if frame := d.Frame(); !strings.Contains(frame, "esc again to close") {
			t.Fatalf("warning badge missing after 1st esc:\n%s", frame)
		}
		root := a.View(a.State())
		ov := findNodeByKind(root, ui.KindOverlay)
		if ov == nil || ov.Tone != "warning" {
			t.Fatalf("expected warning tone on armed overlay, got %+v", ov)
		}

		// Any other key (j) disarms the guard and clears warning badge.
		d.Keys("j")
		if a.escGuard.Armed {
			t.Fatal("j did not disarm escape guard")
		}
		if frame := d.Frame(); strings.Contains(frame, "esc again to close") {
			t.Fatalf("warning badge still visible after disarming key:\n%s", frame)
		}

		// Next esc arms again; 2nd consecutive esc closes the modal.
		d.Keys("esc")
		if a.Modal() != "nouns" || !a.escGuard.Armed {
			t.Fatalf("expected re-armed modal after esc, got modal=%q armed=%v", a.Modal(), a.escGuard.Armed)
		}
		d.Keys("esc")
		if a.Modal() != "" {
			t.Fatalf("2nd esc did not close modal: %q", a.Modal())
		}

		// Explicit close keys (q in picker, T in outline) still close on 1st press.
		d.Keys("g", "n", "q")
		if a.Modal() != "" {
			t.Fatalf("q did not close picker immediately: %q", a.Modal())
		}
		d.Keys("T", "T")
		if a.Modal() != "" {
			t.Fatalf("T did not close outline immediately: %q", a.Modal())
		}
	})

	t.Run("cooloff policy swallows esc for 1s after exiting insert mode then closes on 1st esc", func(t *testing.T) {
		a, d := newClockedApp(t, &now, map[string]any{SetModalEscapeGuard: traits.EscapeGuardCooloff})

		// Open new task form, enter ModeFormEdit, type, exit with esc -> starts 1s cooloff.
		d.Keys("2", "n", "i")
		d.Type("Guard test")
		d.Keys("esc")
		if a.Modal() != "form" || a.Mode() != ModeForm {
			t.Fatalf("expected form normal mode after edit exit, got modal=%q mode=%q", a.Modal(), a.Mode())
		}
		if frame := d.Frame(); !strings.Contains(frame, "esc locked") {
			t.Fatalf("expected esc locked badge during cooloff:\n%s", frame)
		}

		// Within 1s cooloff, esc is swallowed.
		now = now.Add(400 * time.Millisecond)
		d.Keys("esc")
		if a.Modal() != "form" {
			t.Fatalf("esc during cooloff closed modal")
		}
		if !strings.Contains(a.Status(), "esc locked") {
			t.Fatalf("status = %q, want esc locked · cool-off", a.Status())
		}

		// After 1s elapses, wake clears warning border and 1st esc closes directly.
		now = now.Add(700 * time.Millisecond)
		a.Shell.Update(wakeMsg())
		if frame := d.Frame(); strings.Contains(frame, "esc locked") {
			t.Fatalf("esc locked badge did not clear after cooloff expired:\n%s", frame)
		}
		d.Keys("esc")
		if a.Modal() != "" {
			t.Fatalf("esc after cooloff expired did not close modal: %q", a.Modal())
		}
	})

	t.Run("both policy enforces cooloff then double esc", func(t *testing.T) {
		a, d := newClockedApp(t, &now, map[string]any{SetModalEscapeGuard: traits.EscapeGuardBoth})

		// Palette starts in ModePickerFilter; esc exits filter into ModePicker and starts cooloff.
		d.Keys(":")
		d.Type("help")
		d.Keys("esc")
		if a.Modal() != "palette" || a.Mode() != ModePicker {
			t.Fatalf("expected palette ModePicker, got modal=%q mode=%q", a.Modal(), a.Mode())
		}

		// At +300ms: cooloff swallows esc.
		now = now.Add(300 * time.Millisecond)
		d.Keys("esc")
		if a.Modal() != "palette" || !strings.Contains(a.Status(), "esc locked") {
			t.Fatalf("expected cooloff swallow under both: modal=%q status=%q", a.Modal(), a.Status())
		}

		// At +1100ms: cooloff expired, 1st esc arms double-esc warning.
		now = now.Add(800 * time.Millisecond)
		d.Keys("esc")
		if a.Modal() != "palette" || !a.escGuard.Armed || !strings.Contains(d.Frame(), "esc again to close") {
			t.Fatalf("expected armed warning after cooloff under both: modal=%q armed=%v status=%q", a.Modal(), a.escGuard.Armed, a.Status())
		}

		// 2nd esc closes.
		d.Keys("esc")
		if a.Modal() != "" {
			t.Fatalf("2nd esc under both did not close palette: %q", a.Modal())
		}
	})

	t.Run("off policy closes immediately on 1st esc", func(t *testing.T) {
		a, d := newClockedApp(t, &now, map[string]any{SetModalEscapeGuard: traits.EscapeGuardOff})

		d.Keys("g", "n", "esc")
		if a.Modal() != "" {
			t.Fatalf("1st esc under off policy did not close picker: %q", a.Modal())
		}
		d.Keys("2", "n", "i")
		d.Type("Quick")
		d.Keys("esc", "esc")
		if a.Modal() != "" {
			t.Fatalf("edit exit + esc under off policy did not close form: %q", a.Modal())
		}
	})
}

func TestOutlineInsertFlowAtomicRowsAndMultiline(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	seed := core.Actor{Kind: "system", ID: "seed"}
	rootTask, _, err := store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: map[string]any{"title": "Anchor Task", "workStatus": "todo", "order": int64(1)},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	d := NewDriver(a, 100, 28)

	// Open Outline modal.
	d.Keys("T")
	if a.Modal() != "outline" || a.Mode() != ModeOutline {
		t.Fatalf("T did not open outline: modal=%q mode=%q", a.Modal(), a.Mode())
	}

	// 1. `esc` in ModeOutlineEdit saves & returns to ModeOutline (does NOT discard).
	d.Keys("a")
	d.Type(" Edited")
	d.Keys("esc")
	if a.Mode() != ModeOutline || a.Modal() != "outline" {
		t.Fatalf("esc in ModeOutlineEdit did not return to ModeOutline: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	if rec, _ := store.Get(rootTask.Ref()); rec.Text("title") != "Anchor Task Edited" {
		t.Fatalf("esc discarded edit instead of saving: title=%q", rec.Text("title"))
	}

	// 2. `enter` chains multiple tasks as independent atomic units; pressing `esc` on
	// an empty newly created row drops ONLY that empty draft (never earlier tasks).
	d.Keys("o")
	d.Type("Chain One")
	d.Keys("enter")
	if a.Mode() != ModeOutlineEdit {
		t.Fatalf("enter in ModeOutlineEdit did not stay in edit mode for next sibling: mode=%q", a.Mode())
	}
	d.Type("Chain Two")
	d.Keys("enter")
	if a.Mode() != ModeOutlineEdit {
		t.Fatalf("2nd enter did not stay in edit mode: mode=%q", a.Mode())
	}
	// Leave 3rd row empty and press esc.
	d.Keys("esc")
	if a.Mode() != ModeOutline {
		t.Fatalf("esc on empty draft did not exit to ModeOutline: mode=%q", a.Mode())
	}
	one, err := store.GetByField(data.KindTask, "title", "Chain One")
	if err != nil {
		t.Fatalf("Chain One missing after chaining + empty esc: %v", err)
	}
	two, err := store.GetByField(data.KindTask, "title", "Chain Two")
	if err != nil {
		t.Fatalf("Chain Two missing after chaining + empty esc: %v", err)
	}

	// 3. Chaining under an indented child preserves the parent/indent level across `enter`.
	d.Keys("o", "tab")
	d.Type("Child A")
	d.Keys("enter")
	d.Type("Child B")
	d.Keys("esc")
	childA, err := store.GetByField(data.KindTask, "title", "Child A")
	if err != nil {
		t.Fatalf("Child A missing: %v", err)
	}
	childB, err := store.GetByField(data.KindTask, "title", "Child B")
	if err != nil {
		t.Fatalf("Child B missing: %v", err)
	}
	pA, okA := childA.RefField("parent")
	pB, okB := childB.RefField("parent")
	if !okA || pA != two.Ref() || !okB || pB != two.Ref() {
		t.Fatalf("chained children parents = (%v,%v) and (%v,%v), want %v", pA, okA, pB, okB, two.Ref())
	}

	// 4. Clearing an existing task's title and pressing `esc` archives ONLY that task,
	// leaving every other task untouched, and `u` restores it.
	om := a.trees.outlineModel()
	om.Reveal(one.ID)
	a.viewMemo.gen++
	d.Keys("a", "ctrl+u", "esc")
	gotOne, err := store.Get(one.Ref())
	if err != nil || !gotOne.IsArchived() {
		t.Fatalf("emptied existing task should be archived: rec=%+v err=%v", gotOne, err)
	}
	if gotTwo, _ := store.Get(two.Ref()); gotTwo == nil || gotTwo.IsArchived() {
		t.Fatalf("emptying Chain One unexpectedly affected Chain Two: %+v", gotTwo)
	}
	// Undo restores the emptied task.
	d.Keys("u")
	if gotOne, _ := store.Get(one.Ref()); gotOne == nil || gotOne.IsArchived() {
		t.Fatalf("u did not restore archived empty task: %+v", gotOne)
	}

	// 5. `shift+enter` inserts a literal line break creating a multiline task that wraps cleanly.
	d.Keys("o")
	d.Type("Multi First Line")
	d.Keys("shift+enter")
	if a.Mode() != ModeOutlineEdit {
		t.Fatalf("shift+enter exited ModeOutlineEdit: mode=%q", a.Mode())
	}
	d.Type("Multi Second Line")
	d.Keys("esc")
	multi, err := store.GetByField(data.KindTask, "title", "Multi First Line\nMulti Second Line")
	if err != nil {
		t.Fatalf("multiline task not created with literal newline: %v", err)
	}
	if !strings.Contains(multi.Text("title"), "\n") {
		t.Fatalf("expected newline in task title, got %q", multi.Text("title"))
	}
	frame := d.Frame()
	if !strings.Contains(frame, "Multi First Line") || !strings.Contains(frame, "Multi Second Line") {
		t.Fatalf("multiline task did not render both lines cleanly:\n%s", frame)
	}

	// 6. `ctrl+c` still discards an edit via kept CmdOutlineEditCancel.
	d.Keys("o")
	d.Type("Discard Me")
	d.Keys("ctrl+c")
	if a.Mode() != ModeOutline {
		t.Fatalf("ctrl+c did not exit ModeOutlineEdit: mode=%q", a.Mode())
	}
	if _, err := store.GetByField(data.KindTask, "title", "Discard Me"); err == nil {
		t.Fatal("ctrl+c unexpectedly saved discarded draft")
	}
}

func findNodeByKind(n ui.ViewNode, kind string) *ui.ViewNode {
	if n.Kind == kind {
		return &n
	}
	for _, c := range n.Children {
		if got := findNodeByKind(c, kind); got != nil {
			return got
		}
	}
	return nil
}

func TestWrapInputsAndWrapOutlineSettings(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	// Default: ui.wrapInputs=true and ui.wrapOutline=true.
	a, d := newClockedApp(t, &now, nil)
	if !a.cfg.Settings.Bool(SetWrapInputs, false) || !a.cfg.Settings.Bool(SetWrapOutline, false) {
		t.Fatalf("expected %s and %s default true", SetWrapInputs, SetWrapOutline)
	}

	// Record form wraps by default and propagates wrap=true into its ViewNode.
	d.Keys("2", "n")
	rf := a.activeRecordForm()
	if rf == nil || !rf.Model().Wrap {
		t.Fatalf("expected record form Wrap=true by default")
	}
	if formNode := findNodeByKind(a.View(a.State()), ui.KindForm); formNode == nil || formNode.Props["wrap"] != true {
		t.Fatalf("expected wrap=true prop on form ViewNode, got %+v", formNode)
	}
	// Type a long title and ensure overflow remains visible across wrapped lines.
	d.Keys("i")
	d.Type("SuperLongWrappedInputTokenAlpha SuperLongWrappedInputTokenOmega")
	if frame := d.Frame(); !strings.Contains(frame, "SuperLongWrappedInputTokenAlpha") || !strings.Contains(frame, "SuperLongWrappedInputTokenOmega") {
		t.Fatalf("long form input overflow was clipped instead of wrapped:\n%s", frame)
	}
	d.Keys("esc", "esc", "esc")

	// Settings form honors ui.wrapInputs.
	d.Keys("S")
	if sf := a.activeSettingsForm(); sf == nil || !sf.Model().Wrap {
		t.Fatal("expected settings form Wrap=true")
	}
	d.Keys("esc", "esc")

	// Outline & Work tree honor ui.wrapOutline=true by default.
	d.Keys("T")
	if !a.trees.outlineModel().Wrap {
		t.Fatal("expected outline tree Wrap=true by default")
	}
	vOn := a.View(a.State())
	if treeNode := vOn.Find(outlineTree); treeNode == nil || treeNode.Props["wrap"] != true {
		t.Fatalf("expected wrap=true prop on outline tree ViewNode, got %+v", treeNode)
	}
	d.Keys("esc", "esc")

	// Opt-out: ui.wrapInputs=false and ui.wrapOutline=false.
	aOff, dOff := newClockedApp(t, &now, map[string]any{
		SetWrapInputs:  false,
		SetWrapOutline: false,
	})
	dOff.Keys("2", "n")
	if rf := aOff.activeRecordForm(); rf == nil || rf.Model().Wrap {
		t.Fatal("expected record form Wrap=false when ui.wrapInputs=false")
	}
	dOff.Keys("esc", "esc", "T")
	if aOff.trees.outlineModel().Wrap {
		t.Fatal("expected outline tree Wrap=false when ui.wrapOutline=false")
	}
	vOff := aOff.View(aOff.State())
	if treeNode := vOff.Find(outlineTree); treeNode == nil || treeNode.Props["wrap"] != false {
		t.Fatalf("expected wrap=false prop on outline tree ViewNode, got %+v", treeNode)
	}
	dOff.Keys("esc", "esc")

	// Verify Work tree also picks up ui.wrapOutline=false.
	if wt := aOff.trees.workModel(data.KindTask); wt.Wrap {
		t.Fatal("expected work task tree Wrap=false when ui.wrapOutline=false")
	}

	// Verify all 4 new specs are appended at the very end of SettingSpecs().
	specs := SettingSpecs()
	if len(specs) < 4 {
		t.Fatalf("unexpected SettingSpecs length %d", len(specs))
	}
	tail := []string{
		specs[len(specs)-4].Name,
		specs[len(specs)-3].Name,
		specs[len(specs)-2].Name,
		specs[len(specs)-1].Name,
	}
	wantTail := []string{SetModalEscapeGuard, SetWrapInputs, SetWrapOutline, SetOutlineEnterNewline}
	if !slices.Equal(tail, wantTail) {
		t.Fatalf("last 4 SettingSpecs = %v, want %v", tail, wantTail)
	}
}

func TestOutlineEnterNewlineOptOut(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	a, d := newClockedApp(t, &now, map[string]any{SetOutlineEnterNewline: false})

	d.Keys("T", "o")
	if a.Mode() != ModeOutlineEdit {
		t.Fatalf("o did not enter ModeOutlineEdit: mode=%q", a.Mode())
	}
	d.Type("Single Commit Row")
	d.Keys("enter")
	if a.Mode() != ModeOutline || a.trees.editing() {
		t.Fatalf("with outline.enterNewline=false, enter should commit and exit to ModeOutline: mode=%q editing=%v", a.Mode(), a.trees.editing())
	}
	if _, err := a.Store.GetByField(data.KindTask, "title", "Single Commit Row"); err != nil {
		t.Fatalf("Single Commit Row not created: %v", err)
	}
}
