package app

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/pathpicker"
)

// TestModalStackNestedFlows verifies nested modal stacks preserve parent modal
// state and apply picked values on return:
//   - Form -> Path/Ref -> Form
//   - Palette -> Path/Ref -> Palette
//   - Presets -> SaveForm -> Presets
func TestModalStackNestedFlows(t *testing.T) {
	a, d := demo(t)

	// 1. Form -> Path -> Form and Form -> Ref -> Form
	d.Keys("2", "n")
	if a.Modal() != "form" || a.Mode() != ModeForm {
		t.Fatalf("n did not open form: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	// Type title on the first field so we verify form edits survive nested pickers.
	d.Keys("i")
	d.Type("Nested stack task")
	d.Keys("enter")

	// Move to defaultDir (path field) and press p to push "path" over "form".
	fm := a.activeRecordForm().Model()
	dirIdx := slices.IndexFunc(fm.Specs, func(p core.ParamSpec) bool { return p.Name == "defaultDir" })
	if dirIdx < 0 {
		t.Fatal("task form missing defaultDir field")
	}
	for fm.ActiveIdx < dirIdx {
		d.Keys("j")
	}
	d.Keys("p")
	if a.Modal() != "path" || !slices.Equal(a.modals.IDs(), []string{"form", "path"}) {
		t.Fatalf("expected [form path] stack, got modal=%q ids=%v", a.Modal(), a.modals.IDs())
	}
	// Tab chooses current directory and pops back to form.
	d.Keys("tab")
	if a.Modal() != "form" || !slices.Equal(a.modals.IDs(), []string{"form"}) {
		t.Fatalf("expected return to [form], got modal=%q ids=%v", a.Modal(), a.modals.IDs())
	}
	if got := fm.Values["title"]; got != "Nested stack task" {
		t.Fatalf("form title lost across path picker: got %v", got)
	}
	if got, _ := fm.Values["defaultDir"].(string); got == "" {
		t.Fatalf("defaultDir not populated from path picker: %+v", fm.Values)
	}

	// Move to parent (ref field) and press p to push "ref" over "form".
	parentIdx := slices.IndexFunc(fm.Specs, func(p core.ParamSpec) bool { return p.Name == "parent" })
	if parentIdx < 0 {
		t.Fatal("task form missing parent field")
	}
	for fm.ActiveIdx > parentIdx {
		d.Keys("k")
	}
	for fm.ActiveIdx < parentIdx {
		d.Keys("j")
	}
	d.Keys("p")
	if a.Modal() != "ref" || !slices.Equal(a.modals.IDs(), []string{"form", "ref"}) {
		t.Fatalf("expected [form ref] stack, got modal=%q ids=%v", a.Modal(), a.modals.IDs())
	}
	d.Keys("enter")
	if a.Modal() != "form" || a.Mode() != ModeForm {
		t.Fatalf("expected return to form after ref pick: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	if _, ok := data.AsRef(fm.TypedValues()["parent"]); !ok {
		t.Fatalf("parent ref not populated from ref picker: %#v", fm.TypedValues()["parent"])
	}
	if got := fm.Values["title"]; got != "Nested stack task" {
		t.Fatalf("form title lost across ref picker: got %v", got)
	}
	d.Keys("esc")

	// 2. Palette -> Path/Ref -> Palette
	a.specs = append(a.specs, core.CommandSpec{
		ID:          "modalstack.test.cmd",
		Description: "Command with path and ref params",
		Params: []core.ParamSpec{
			{Name: "dir", Type: core.ParamPath},
			{Name: "target", Type: core.ParamRef, RefKinds: []string{data.KindTask}},
		},
	})
	d.Keys(":")
	d.Type("modalstack.test.cmd")
	d.Keys("tab") // focus args form
	if a.Modal() != "palette" || a.Mode() != ModeForm {
		t.Fatalf("palette args form not focused: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Keys("p")
	if !slices.Equal(a.modals.IDs(), []string{"palette", "path"}) {
		t.Fatalf("expected [palette path], got %v", a.modals.IDs())
	}
	d.Keys("tab")
	if !slices.Equal(a.modals.IDs(), []string{"palette"}) || a.Mode() != ModeForm {
		t.Fatalf("expected return to [palette] ModeForm, got ids=%v mode=%q", a.modals.IDs(), a.Mode())
	}
	pal := a.activePalette().Model()
	if got, _ := pal.ActiveForm.Values["dir"].(string); got == "" {
		t.Fatalf("palette dir param not set: %+v", pal.ActiveForm.Values)
	}
	d.Keys("j", "p")
	if !slices.Equal(a.modals.IDs(), []string{"palette", "ref"}) {
		t.Fatalf("expected [palette ref], got %v", a.modals.IDs())
	}
	d.Keys("enter")
	if !slices.Equal(a.modals.IDs(), []string{"palette"}) || a.Mode() != ModeForm {
		t.Fatalf("expected return to [palette] ModeForm after ref pick, got ids=%v mode=%q", a.modals.IDs(), a.Mode())
	}
	if _, ok := data.AsRef(pal.ActiveForm.TypedValues()["target"]); !ok {
		t.Fatalf("palette target ref not set: %#v", pal.ActiveForm.TypedValues()["target"])
	}
	d.Keys("esc", "esc")

	// 3. Presets -> SaveForm -> Presets
	d.Keys("2", "g", "v")
	if a.Modal() != "table.presets" || a.Mode() != ModePicker {
		t.Fatalf("g v did not open table.presets: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Keys("s")
	if !slices.Equal(a.modals.IDs(), []string{"table.presets", "launch"}) || a.Mode() != ModeForm {
		t.Fatalf("s in presets did not stack launch form: ids=%v mode=%q", a.modals.IDs(), a.Mode())
	}
	d.Keys("i")
	d.Type("stacked-preset")
	d.Keys("ctrl+s")
	if !slices.Equal(a.modals.IDs(), []string{"table.presets"}) || a.Mode() != ModePicker {
		t.Fatalf("submitting preset save form did not return to table.presets: ids=%v mode=%q", a.modals.IDs(), a.Mode())
	}
	if !slices.Contains(pickerIDs(a, "table.presets"), "stacked-preset") {
		t.Fatalf("saved preset missing from refreshed table.presets picker: %v", pickerIDs(a, "table.presets"))
	}
	d.Keys("esc")
}

// TestModalStackDropAllLIFOCleanup verifies DropAll runs OnClose in LIFO order,
// persists unsaved composer/form drafts, clears outline inline edit state, and
// empties core.State.Modals.
func TestModalStackDropAllLIFOCleanup(t *testing.T) {
	a, d := demo(t)

	// 1. Composer open with unsaved text -> dropModals saves draft and clears stack.
	d.Keys("1", "c")
	if a.Modal() != "composer" {
		t.Fatalf("expected composer open, got %q", a.Modal())
	}
	cm := a.activeComposer()
	paneID := cm.paneID
	d.Type("unsaved composer prompt before drop")
	st := a.dropModals(a.State())
	if len(st.Modals) != 0 || !a.modals.Empty() || a.Modal() != "" {
		t.Fatalf("dropModals left modals: state=%v stack=%v", st.Modals, a.modals.IDs())
	}
	if drafts := a.drafts.List(composerDraftKey(paneID)); len(drafts) == 0 {
		t.Fatal("dropModals did not save composer draft via OnClose(CloseDropAll)")
	}
	a.Shell.State = st

	// 2. Outline open in inline edit mode -> dropModals clears edit state and stack.
	d.Keys("2", "T", "i")
	if a.Modal() != "outline" || a.Mode() != ModeOutlineEdit || !a.trees.editingOutline() {
		t.Fatalf("expected outline in edit mode: modal=%q mode=%q editing=%v", a.Modal(), a.Mode(), a.trees.editingOutline())
	}
	d.Type("wip ")
	st = a.dropModals(a.State())
	if len(st.Modals) != 0 || !a.modals.Empty() || a.trees.editing() {
		t.Fatalf("dropModals did not clean outline edit: state=%v stack=%v editing=%v", st.Modals, a.modals.IDs(), a.trees.editing())
	}
	a.Shell.State = st

	// 3. Nested stack LIFO OnClose order + form draft persistence on DropAll.
	var closeOrder []string
	var closeReasons []modal.CloseReason
	d.Keys("2", "n", "i")
	d.Type("Draft saved on drop")
	d.Keys("enter")
	probe := &lifoProbeModal{
		id: "probe",
		onClose: func(r modal.CloseReason) {
			closeOrder = append(closeOrder, "probe")
			closeReasons = append(closeReasons, r)
		},
	}
	st = a.pushModal(a.State(), probe)
	if !slices.Equal(a.modals.IDs(), []string{"form", "probe"}) {
		t.Fatalf("expected [form probe], got %v", a.modals.IDs())
	}
	st = a.dropModals(st)
	if len(st.Modals) != 0 || !a.modals.Empty() {
		t.Fatalf("dropModals left frames: state=%v stack=%v", st.Modals, a.modals.IDs())
	}
	if !slices.Equal(closeOrder, []string{"probe"}) || !slices.Equal(closeReasons, []modal.CloseReason{modal.CloseDropAll}) {
		t.Fatalf("unexpected probe close: order=%v reasons=%v", closeOrder, closeReasons)
	}
	if drafts := a.drafts.List("new:task"); len(drafts) == 0 || drafts[0].Values["title"] != "Draft saved on drop" {
		t.Fatalf("form draft not saved during DropAll: %+v", drafts)
	}
}

type lifoProbeModal struct {
	id      string
	onClose func(modal.CloseReason)
}

func (m *lifoProbeModal) ID() string      { return m.id }
func (m *lifoProbeModal) Mode() string    { return ModePicker }
func (m *lifoProbeModal) Scope() string   { return m.id }
func (m *lifoProbeModal) FocusID() string { return m.id + ".focus" }
func (m *lifoProbeModal) View(_, _ int) ui.ViewNode {
	return modal.Overlay(modal.OverlaySpec{ID: m.id, Scope: m.id})
}
func (m *lifoProbeModal) Handle(core.Command) modal.Result { return modal.Result{} }
func (m *lifoProbeModal) OnClose(r modal.CloseReason) {
	if m.onClose != nil {
		m.onClose(r)
	}
}

// TestModalStackScopeBindings verifies all 17 migrated Scope-targeted bindings
// fire cleanly against Modal.Scope() without any modal.id expression guards.
func TestModalStackScopeBindings(t *testing.T) {
	a, d := demo(t)

	// 1. "yank" scope: y with empty query copies primary value; typing y after query filters.
	d.Keys("2", "y")
	if a.Modal() != "yank" {
		t.Fatalf("y did not open yank picker: %q", a.Modal())
	}
	d.Type("t")
	d.Type("y") // query is non-empty, so y inserts into query rather than triggering CmdYankPrimary
	if a.Modal() != "yank" || a.activePicker("yank").Model().Query != "ty" {
		t.Fatalf("y with non-empty query should filter: modal=%q query=%q", a.Modal(), a.activePicker("yank").Model().Query)
	}
	d.Keys("esc", "y", "y") // second y with empty query yanks primary and closes
	if a.Modal() != "" || !strings.Contains(a.Status(), "yanked") {
		t.Fatalf("y y in yank scope failed: modal=%q status=%q", a.Modal(), a.Status())
	}

	// 2. "context" scope: y yanks and a attaches when query is empty.
	pane, ok := a.SelectedPane()
	if !ok {
		t.Fatal("demo has no selected pane")
	}
	a.harvestPrompt(*pane, "Check https://example.com/spec")
	d.Keys("1", "g", "c")
	if a.Modal() != "context" {
		t.Fatalf("g c did not open context picker: %q", a.Modal())
	}
	topCtx, ok := a.activePicker("context").Model().Focused()
	if !ok {
		t.Fatal("context picker has no focused item")
	}
	d.Keys("y")
	if a.Modal() != "" || a.Register('"') != topCtx.Title {
		t.Fatalf("y in context scope failed: modal=%q reg=%q want=%q", a.Modal(), a.Register('"'), topCtx.Title)
	}
	d.Keys("g", "c", "a")
	if a.Modal() != "" || !strings.Contains(a.Status(), "attached") {
		t.Fatalf("a in context scope failed: modal=%q status=%q", a.Modal(), a.Status())
	}

	// 3. "table.presets" scope: s saves, d sets default, x deletes.
	d.Keys("2", "g", "v", "s")
	if a.Modal() != "launch" {
		t.Fatalf("s in table.presets scope did not open save form: %q", a.Modal())
	}
	d.Keys("i")
	d.Type("scope-preset")
	d.Keys("ctrl+s")
	if a.Modal() != "table.presets" {
		t.Fatalf("expected table.presets after save: %q", a.Modal())
	}
	d.Keys("d")
	if !strings.Contains(a.Status(), "default") {
		t.Fatalf("d in table.presets scope failed: status=%q", a.Status())
	}
	d.Keys("x")
	if slices.Contains(pickerIDs(a, "table.presets"), "scope-preset") {
		t.Fatalf("x in table.presets scope did not delete preset: %v", pickerIDs(a, "table.presets"))
	}
	d.Keys("esc")

	// 4. "path" scope: ctrl+t toggles hidden, left goes to parent, right enters dir, tab selects.
	d.Keys("2", "ctrl+o")
	if a.Modal() != "path" {
		t.Fatalf("ctrl+o did not open path picker: %q", a.Modal())
	}
	pp := a.activePathPicker().Model()
	wasHidden := pp.ShowHidden
	d.Keys("ctrl+t")
	if pp.ShowHidden == wasHidden {
		t.Fatal("ctrl+t in path scope did not toggle hidden files")
	}
	beforeDir := pp.Dir
	d.Keys("left")
	if pp.Dir == beforeDir {
		t.Fatalf("left in path scope did not move to parent from %q", beforeDir)
	}
	// Highlight a directory child and press right to enter it.
	for i, it := range pp.List().Filtered() {
		if e, ok := it.Data.(pathpicker.DirEntry); ok && e.IsDir {
			pp.List().SelectedIdx = i
			break
		}
	}
	parentDir := pp.Dir
	d.Keys("right")
	if pp.Dir == parentDir {
		t.Fatalf("right in path scope did not open directory from %q", parentDir)
	}
	d.Keys("tab")
	if a.Modal() != "" {
		t.Fatalf("tab in path scope did not select and close: modal=%q", a.Modal())
	}

	// 5. "palette" scope: tab/right focuses args form, shift+tab/esc returns to list, enter runs.
	d.Keys(":")
	d.Type("record.new")
	d.Keys("right")
	if a.Modal() != "palette" || a.Mode() != ModeForm {
		t.Fatalf("right in palette scope did not focus args form: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Keys("shift+tab")
	if a.Modal() != "palette" || a.Mode() != ModePicker {
		t.Fatalf("shift+tab in palette scope did not return to picker: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Keys("tab")
	if a.Modal() != "palette" || a.Mode() != ModeForm {
		t.Fatalf("tab in palette scope did not focus args form: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Keys("esc")
	if a.Modal() != "palette" || a.Mode() != ModePicker {
		t.Fatalf("esc in palette form scope did not return to picker: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Keys("tab", "enter")
	if a.Modal() != "form" {
		t.Fatalf("enter in palette form scope did not execute record.new: modal=%q status=%q", a.Modal(), a.Status())
	}
	d.Keys("esc")

	// 6. "answer" scope: ctrl+n / ctrl+p cycle batched questions.
	res, err := a.Router.Ask(User, mcpui.AskArgs{
		Pane:  pane.ID,
		ID:    "batch-check",
		Title: "Batch check",
		Questions: []mcpui.AskQuestion{
			{ID: "q1", Title: "First?", Form: &mcpui.AskForm{Fields: []core.ParamSpec{{Name: "a", Type: core.ParamString}}}},
			{ID: "q2", Title: "Second?", Form: &mcpui.AskForm{Fields: []core.ParamSpec{{Name: "b", Type: core.ParamString}}}},
		},
	})
	if err != nil {
		t.Fatalf("Router.Ask: %v", err)
	}
	a.Shell.State = a.jumpToPage(a.State(), res.ID)
	d.Keys("l") // focus first question card
	for i := 0; i < 10 && a.Modal() != "answer"; i++ {
		d.Keys("a")
		if a.Modal() != "answer" {
			d.Keys("j")
		}
	}
	if a.Modal() != "answer" {
		t.Fatalf("could not open batch answer modal: modal=%q focus=%q", a.Modal(), a.Focus())
	}
	firstCard := a.activeAnswerForm().cardID
	d.Keys("ctrl+n")
	if secondCard := a.activeAnswerForm().cardID; secondCard == firstCard {
		t.Fatalf("ctrl+n in answer scope did not advance batch question: card=%q", secondCard)
	}
	d.Keys("ctrl+p")
	if backCard := a.activeAnswerForm().cardID; backCard != firstCard {
		t.Fatalf("ctrl+p in answer scope did not return to first batch question: got %q want %q", backCard, firstCard)
	}
	d.Keys("esc")
}

// TestModalStackPickerCreateHook verifies the "+ Create new" PickerCreateHook
// end-to-end inside ref, assign, and pane.associate pickers.
func TestModalStackPickerCreateHook(t *testing.T) {
	a, d := demo(t)

	// 1. Inside "ref" picker (from record form parent field).
	d.Keys("2", "n")
	fm := a.activeRecordForm().Model()
	parentIdx := slices.IndexFunc(fm.Specs, func(p core.ParamSpec) bool { return p.Name == "parent" })
	for fm.ActiveIdx < parentIdx {
		d.Keys("j")
	}
	d.Keys("p")
	if a.Modal() != "ref" {
		t.Fatalf("expected ref picker, got %q", a.Modal())
	}
	d.Type("Created Via Ref Hook")
	d.Keys("enter")
	if a.Modal() != "form" {
		t.Fatalf("expected return to form after ref create hook, got %q", a.Modal())
	}
	createdRefTask, err := a.Store.GetByField(data.KindTask, "title", "Created Via Ref Hook")
	if err != nil {
		t.Fatalf("ref PickerCreateHook did not create task: %v", err)
	}
	if got, ok := data.AsRef(fm.TypedValues()["parent"]); !ok || got != createdRefTask.Ref() {
		t.Fatalf("form parent = %#v, want %v", fm.TypedValues()["parent"], createdRefTask.Ref())
	}
	d.Keys("esc")

	// 2. Inside "assign" picker (m on a task).
	targetTask := mkRec(t, a, data.KindTask, map[string]any{"title": "Move Me Task"})
	browseTo(t, a, d, targetTask)
	d.Keys("m")
	if a.Modal() != "assign" {
		t.Fatalf("expected assign picker, got %q", a.Modal())
	}
	d.Type("Created Via Assign Hook")
	d.Keys("enter")
	if a.Modal() != "" {
		t.Fatalf("assign picker did not close: %q", a.Modal())
	}
	createdAssignParent, err := a.Store.GetByField(data.KindTask, "title", "Created Via Assign Hook")
	if err != nil {
		t.Fatalf("assign PickerCreateHook did not create task: %v", err)
	}
	moved, _ := a.Store.Get(targetTask.Ref())
	if p, ok := moved.RefField("parent"); !ok || p != createdAssignParent.Ref() {
		t.Fatalf("moved task parent = %v (%v), want %v", p, ok, createdAssignParent.Ref())
	}

	// 3. Inside "pane.associate" picker (L on 1 Panes).
	d.Keys("1")
	for i := 0; i < 10; i++ {
		if _, ok := a.SelectedPane(); ok {
			break
		}
		d.Keys("j")
	}
	pane, ok := a.SelectedPane()
	if !ok {
		t.Fatal("could not select a pane on 1 Panes")
	}
	d.Keys("L")
	if a.Modal() != "pane.associate" {
		t.Fatalf("expected pane.associate picker, got %q (status=%q)", a.Modal(), a.Status())
	}
	d.Type("Created Via Pane Hook")
	d.Keys("enter")
	if a.Modal() != "" {
		t.Fatalf("pane.associate picker did not close: %q", a.Modal())
	}
	createdPaneTask, err := a.Store.GetByField(data.KindTask, "title", "Created Via Pane Hook")
	if err != nil {
		t.Fatalf("pane.associate PickerCreateHook did not create task: %v", err)
	}
	updatedPane, _ := a.Store.Get(pane.Ref())
	if p, ok := updatedPane.RefField("task"); !ok || p != createdPaneTask.Ref() {
		t.Fatalf("associated pane task = %v (%v), want %v", p, ok, createdPaneTask.Ref())
	}
}

// TestModalStackCheckBindingsAndNoModalIDGuards verifies CheckBindings("")
// reports zero errors and no binding uses a legacy modal.id expression guard.
func TestModalStackCheckBindingsAndNoModalIDGuards(t *testing.T) {
	rep, err := CheckBindings("")
	if err != nil {
		t.Fatalf("CheckBindings error: %v", err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings reported %d error(s): %+v", len(rep.Errors), rep.Errors)
	}
	for i, b := range DefaultBindings() {
		if strings.Contains(b.When, "modal.id") {
			t.Errorf("DefaultBindings()[%d] (%s %v) still uses legacy modal.id guard: %q", i, b.Mode, b.Keys, b.When)
		}
	}
}
