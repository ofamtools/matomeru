package app

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/palette"
	"ofam.dev/ochazuke/widgets/picker"
)

func TestPaletteLiveArgsForm(t *testing.T) {
	a, d := demo(t)

	// Open palette and filter to record.new.
	d.Keys(":")
	if a.Modal() != "palette" || a.Mode() != ModePickerFilter {
		t.Fatalf(": did not open palette: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Type("record.new")
	frame := d.Frame()
	if !strings.Contains(frame, "Arguments · record.new") || !strings.Contains(frame, "kind") {
		t.Fatalf("highlighting record.new did not show live args form:\n%s", frame)
	}
	palModal := a.activePalette()
	if a.Mode() != ModePickerFilter || palModal == nil || palModal.Model().FocusForm {
		t.Fatalf("args form should start unfocused while browsing: mode=%q pal=%v", a.Mode(), palModal)
	}
	pal := palModal.Model()

	// Verify record.new's kind param includes all registered noun kinds.
	if pal.ActiveForm == nil || len(pal.ActiveForm.Specs) == 0 {
		t.Fatal("record.new has no ActiveForm specs")
	}
	kindEnum := pal.ActiveForm.Specs[0].Enum
	for _, want := range []string{data.KindTask, data.KindPane, data.KindNote, data.KindDocument, data.KindLane, data.KindSchedule, data.KindRun, data.KindSignal} {
		if !slices.Contains(kindEnum, want) {
			t.Fatalf("record.new kind enum %v missing %q", kindEnum, want)
		}
	}

	// Tab into the args form, cycle kind to pane, and press enter to open "New pane".
	d.Keys("tab")
	if a.Mode() != ModeForm || !pal.FocusForm || a.Focus() != pal.FocusID("palette") {
		t.Fatalf("tab did not focus args form: mode=%q focus=%q", a.Mode(), a.Focus())
	}
	// Cycle with l until kind == "pane".
	for range len(kindEnum) {
		if v, _ := pal.ActiveForm.TypedValues()["kind"].(string); v == data.KindPane {
			break
		}
		d.Keys("l")
	}
	if v, _ := pal.ActiveForm.TypedValues()["kind"].(string); v != data.KindPane {
		t.Fatalf("failed to cycle kind to pane, got %q", v)
	}
	d.Keys("enter")
	if rf := a.activeRecordForm(); a.Modal() != "form" || rf == nil || rf.kind != data.KindPane || !strings.Contains(d.Frame(), "New pane") {
		t.Fatalf("enter in palette args form did not open New pane: modal=%q kind=%v\n%s", a.Modal(), rf, d.Frame())
	}
	d.Keys("esc", "esc")

	// !cmd shell shortcut still works in the palette.
	d.Keys(":")
	d.Type("!echo hi")
	d.Keys("enter")
	if a.Modal() != "" {
		t.Fatalf("!echo hi did not close palette: modal=%q", a.Modal())
	}
	runsRes, err := a.Store.Query(data.Query{Kind: data.KindRun})
	if err != nil {
		t.Fatal(err)
	}
	foundShell := false
	for _, r := range runsRes.Items {
		if r.Text("command") == "echo hi" {
			foundShell = true
			break
		}
	}
	if !foundShell {
		t.Fatalf("!echo hi did not create a run record (status=%q)", a.Status())
	}
}

func TestPaletteSmartEnterAndPickers(t *testing.T) {
	a, d := demo(t)

	// Register a synthetic test command with a required string, a path param, and a ref param.
	const testCmd = "test.palette.pick"
	var gotArgs map[string]any
	spec := core.CommandSpec{
		ID:          testCmd,
		Description: "Test command with required, path, and ref params",
		Params: []core.ParamSpec{
			{Name: "label", Type: core.ParamString, Required: true},
			{Name: "dir", Type: core.ParamPath},
			{Name: "task", Type: core.ParamRef, RefKinds: []string{data.KindTask}},
		},
	}
	a.bind.specs = append(a.bind.specs, spec)
	a.handle(testCmd, func(s core.State, c core.Command) core.StepResult {
		gotArgs = c.Args
		return core.Next(s)
	})

	// Open palette, filter to test command, press enter -> missing required param shifts focus into args form.
	d.Keys(":")
	d.Type(testCmd)
	d.Keys("enter")
	palModal := a.activePalette()
	if a.Modal() != "palette" || a.Mode() != ModeForm || palModal == nil || !palModal.Model().FocusForm {
		t.Fatalf("smart enter on missing required param did not shift focus to form: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	pal := palModal.Model()
	if gotArgs != nil {
		t.Fatalf("command executed prematurely with missing required param: %v", gotArgs)
	}

	// Fill required label field via i -> type -> enter.
	d.Keys("i")
	if a.Mode() != ModeFormEdit {
		t.Fatalf("i in palette form did not enter ModeFormEdit: mode=%q", a.Mode())
	}
	d.Type("deploy")
	d.Keys("enter")
	if a.Mode() != ModeForm {
		t.Fatalf("enter in ModeFormEdit did not return to ModeForm: mode=%q", a.Mode())
	}

	// Move to path param and press p -> opens path picker; tab selects a path and fills the palette arg.
	d.Keys("j", "p")
	if a.Modal() != "path" {
		t.Fatalf("p on path param did not open modalPath: modal=%q", a.Modal())
	}
	d.Keys("tab")
	if a.Modal() != "palette" || a.Mode() != ModeForm {
		t.Fatalf("selecting path did not return to palette form: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	pickedDir, _ := pal.ActiveForm.Values["dir"].(string)
	if pickedDir == "" {
		t.Fatal("path picker did not populate dir arg in palette form")
	}

	// Move to ref param and press p -> opens ref picker; enter selects a task and fills the palette arg.
	d.Keys("j", "p")
	if a.Modal() != "ref" {
		t.Fatalf("p on ref param did not open modalRef: modal=%q", a.Modal())
	}
	d.Keys("enter")
	if a.Modal() != "palette" || a.Mode() != ModeForm {
		t.Fatalf("selecting ref did not return to palette form: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	if ref, ok := pal.ActiveForm.TypedValues()["task"].(core.Ref); !ok || ref.Kind != data.KindTask || ref.ID == "" {
		t.Fatalf("ref picker did not populate task arg: raw=%#v typed=%#v", pal.ActiveForm.Values["task"], pal.ActiveForm.TypedValues()["task"])
	}

	// Esc in form returns focus to command list (ModePickerFilter), tab goes back to form.
	d.Keys("esc")
	if a.Modal() != "palette" || a.Mode() != ModePickerFilter || pal.FocusForm {
		t.Fatalf("esc in palette form should return to command list: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Keys("tab", "enter")
	if a.Modal() != "" || gotArgs == nil {
		t.Fatalf("enter in palette form did not submit command: modal=%q args=%v", a.Modal(), gotArgs)
	}
	if gotArgs["label"] != "deploy" || gotArgs["dir"] != pickedDir {
		t.Fatalf("submitted args = %#v", gotArgs)
	}
	if ref, ok := gotArgs["task"].(core.Ref); !ok || ref.Kind != data.KindTask || ref.ID == "" {
		t.Fatalf("submitted task ref = %#v", gotArgs["task"])
	}

	// Unused import guard for palette/picker types in case of future tweaks.
	_ = palette.Entry{Item: picker.Item{}}
}

func TestNewHereOnPanesOpensNewPaneForm(t *testing.T) {
	a, d := demoLegacy(t)
	d.Keys("1", "n")
	if rf := a.activeRecordForm(); a.Modal() != "form" || rf == nil || rf.kind != data.KindPane {
		t.Fatalf("n on 1 Panes opened modal=%q form=%+v, want kind=pane", a.Modal(), rf)
	}
	frame := d.Frame()
	if !strings.Contains(frame, "New Agent") || strings.Contains(frame, "New task") {
		t.Fatalf("n on 1 Panes did not render New Agent form:\n%s", frame)
	}
	d.Keys("esc")

	// CmdNewHere remains registered and still opens the unfiltered New pane form on SpacePanes.
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdNewHere, Source: "test", Actor: User}})
	if rf := a.activeRecordForm(); a.Modal() != "form" || rf == nil || rf.kind != data.KindPane {
		t.Fatalf("record.new.here on 1 Panes opened modal=%q form=%+v, want kind=pane", a.Modal(), rf)
	}
	if !strings.Contains(d.Frame(), "New pane") {
		t.Fatalf("record.new.here on 1 Panes did not render New pane form:\n%s", d.Frame())
	}
}

func TestPanesTOCToggleAndReaderTOC(t *testing.T) {
	a, d := demoLegacy(t)
	d.Keys("1")
	if !strings.Contains(d.Frame(), "Pages") {
		t.Fatalf("Pages TOC rail not visible initially:\n%s", d.Frame())
	}

	// Focus into site.toc.* then toggle TOC off with t.
	d.Keys("l")
	if !strings.HasPrefix(a.Focus(), tocPrefix) {
		t.Fatalf("l on rail did not focus TOC: focus=%q", a.Focus())
	}
	d.Keys("t")
	if a.cfg.Settings.Bool(SetShowTOC, true) {
		t.Fatal("t did not turn panes.showTOC off")
	}
	if strings.Contains(d.Frame(), "Pages") {
		t.Fatalf("Pages TOC rail still visible after t:\n%s", d.Frame())
	}
	if a.Focus() != siteScreen {
		t.Fatalf("hiding TOC while focused in TOC did not move focus to site.screen: focus=%q", a.Focus())
	}

	// Press t again to restore the Pages TOC rail.
	d.Keys("t")
	if !a.cfg.Settings.Bool(SetShowTOC, true) || !strings.Contains(d.Frame(), "Pages") {
		t.Fatalf("second t did not restore Pages TOC rail:\n%s", d.Frame())
	}

	// Verify t in ModeReader still toggles the Markdown headings TOC.
	guide, _, err := a.Store.Create(data.CreateInput{Kind: data.KindNote, Fields: map[string]any{
		"title": "TOC Test Doc",
		"body":  "# Intro\n\nHello.\n\n## Details\n\nWorld.\n",
	}}, User)
	if err != nil {
		t.Fatal(err)
	}
	d.Keys("g", "n", "/")
	d.Type("notes")
	d.Keys("enter")
	selectRow(a.nav.tables[workTable], data.RefText(guide.Ref()))
	a.viewMemo.gen++
	d.Keys("o")
	if a.Modal() != "reader" || a.Mode() != ModeReader {
		t.Fatalf("o did not open Markdown reader: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	reader := a.activeMarkdown("reader")
	if reader == nil || !reader.Model().ShowTOC || !strings.Contains(d.Frame(), "Contents") {
		t.Fatalf("reader TOC should be visible initially:\n%s", d.Frame())
	}
	d.Keys("t")
	if reader.Model().ShowTOC || strings.Contains(d.Frame(), "Contents") {
		t.Fatalf("t in ModeReader did not hide Markdown headings TOC:\n%s", d.Frame())
	}
	d.Keys("t")
	if !reader.Model().ShowTOC || !strings.Contains(d.Frame(), "Contents") {
		t.Fatalf("second t in ModeReader did not restore Markdown headings TOC:\n%s", d.Frame())
	}
}
