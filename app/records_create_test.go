package app

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/harness"
	"ofam.dev/matomeru/ptyhost"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
)

type launchCaptureHost struct {
	opens   []launchOpenCall
	frames  map[string]ptyhost.Frame
	written map[string][][]byte
}

type launchOpenCall struct {
	id, cmd, cwd string
	cols, rows   int
	env          map[string]string
}

func newLaunchCaptureHost() *launchCaptureHost {
	return &launchCaptureHost{
		frames:  map[string]ptyhost.Frame{},
		written: map[string][][]byte{},
	}
}

func (h *launchCaptureHost) Open(id, cmd, cwd string, cols, rows int) error {
	return h.OpenWithEnv(id, cmd, cwd, cols, rows, nil)
}

func (h *launchCaptureHost) OpenWithEnv(id, cmd, cwd string, cols, rows int, env map[string]string) error {
	cp := map[string]string{}
	for k, v := range env {
		cp[k] = v
	}
	h.opens = append(h.opens, launchOpenCall{id: id, cmd: cmd, cwd: cwd, cols: cols, rows: rows, env: cp})
	h.frames[id] = ptyhost.Frame{
		ID:       id,
		Cols:     cols,
		Rows:     rows,
		Lines:    []string{"% "},
		Plain:    []string{"% "},
		Revision: 1,
	}
	return nil
}

func (h *launchCaptureHost) Resize(id string, cols, rows int) error {
	if f, ok := h.frames[id]; ok {
		f.Cols, f.Rows = cols, rows
		h.frames[id] = f
	}
	return nil
}

func (h *launchCaptureHost) Close(id string) error {
	delete(h.frames, id)
	return nil
}

func (h *launchCaptureHost) CloseAll() {
	h.frames = map[string]ptyhost.Frame{}
}

func (h *launchCaptureHost) Snapshot(id string) (ptyhost.Frame, bool) {
	f, ok := h.frames[id]
	return f, ok
}

func (h *launchCaptureHost) Write(id string, p []byte) error {
	h.written[id] = append(h.written[id], append([]byte(nil), p...))
	return nil
}

func TestRecordNewGenericBindingArgs(t *testing.T) {
	a, d := demo(t)

	// 1. Test shapeCreateSchema directly on Task schema (100% noun-agnostic).
	taskSchema := a.schema(data.KindTask)
	shaped := shapeCreateSchema(taskSchema, map[string]any{
		"fields":         []string{"title", "workStatus", "priority", "repo"},
		"hideFields":     []string{"repo"},
		"choices":        map[string]any{"priority": []string{"P0", "P1"}},
		"excludeChoices": map[string]any{"workStatus": []string{"todo", "done"}},
	})
	var names []string
	for _, f := range shaped.Fields {
		names = append(names, f.Name)
	}
	wantFields := []string{"title", "workStatus", "priority"}
	if !slices.Equal(names, wantFields) {
		t.Fatalf("shaped fields = %v, want %v", names, wantFields)
	}
	prioField, ok := shaped.Field("priority")
	if !ok || !slices.Equal(prioField.Enum, []string{"P0", "P1"}) {
		t.Fatalf("shaped priority enum = %v, want [P0 P1]", prioField.Enum)
	}
	statusField, ok := shaped.Field("workStatus")
	if !ok {
		t.Fatal("shaped schema missing workStatus")
	}
	if slices.Contains(statusField.Enum, "todo") || slices.Contains(statusField.Enum, "done") {
		t.Fatalf("excludeChoices did not remove todo/done from workStatus: %v", statusField.Enum)
	}
	if gotDef := data.Text(statusField.Default); gotDef != statusField.Enum[0] {
		t.Fatalf("expected workStatus default to advance to %q after excluding old default, got %q", statusField.Enum[0], gotDef)
	}

	// 2. Execute record.new on KindTask with title, defaults (visible + hidden),
	//    hideFields, choices, and excludeChoices; verify hidden defaults persist on submit.
	d.Send(core.CommandEvent{Command: core.Command{
		ID: views.CmdNew,
		Args: map[string]any{
			"kind":           data.KindTask,
			"title":          "Quick Bug",
			"fields":         []string{"title", "priority", "workStatus"},
			"hideFields":     []string{"repo", "defaultHarness"},
			"defaults":       map[string]any{"priority": "P1", "repo": "ofam/matomeru", "defaultHarness": "codex"},
			"choices":        map[string]any{"priority": []string{"P0", "P1"}},
			"excludeChoices": map[string]any{"workStatus": []string{"todo"}},
		},
		Source: "test",
		Actor:  User,
	}})

	if a.Modal() != "form" || a.Mode() != ModeForm {
		t.Fatalf("record.new did not open form modal: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	rf := a.activeRecordForm()
	if rf == nil || rf.kind != data.KindTask {
		t.Fatalf("expected task form, got %+v", rf)
	}
	fm := rf.Model()
	if fm.Title != "Quick Bug" {
		t.Fatalf("form title = %q, want %q", fm.Title, "Quick Bug")
	}
	var specNames []string
	var prioEnum, statusEnum []string
	for _, sp := range fm.Specs {
		specNames = append(specNames, sp.Name)
		switch sp.Name {
		case "priority":
			prioEnum = sp.Enum
		case "workStatus":
			statusEnum = sp.Enum
		}
	}
	if !slices.Equal(specNames, []string{"title", "workStatus", "priority"}) {
		t.Fatalf("form specs = %v, want [title workStatus priority]", specNames)
	}
	if !slices.Equal(prioEnum, []string{"P0", "P1"}) {
		t.Fatalf("form priority enum = %v, want [P0 P1]", prioEnum)
	}
	if slices.Contains(statusEnum, "todo") {
		t.Fatalf("form workStatus enum still contains excluded value todo: %v", statusEnum)
	}
	if got := data.Text(fm.Values["priority"]); got != "P1" {
		t.Fatalf("default priority = %q, want P1", got)
	}

	// Fill title and submit via ctrl+s.
	d.Keys("i")
	d.Type("Fix login redirect")
	d.Keys("enter", "ctrl+s")
	if a.Modal() != "" {
		t.Fatalf("form did not close on submit: modal=%q status=%q", a.Modal(), a.Status())
	}

	res, err := a.Store.Query(data.Query{Kind: data.KindTask})
	if err != nil {
		t.Fatal(err)
	}
	var created *data.Record
	for i := range res.Items {
		if res.Items[i].Text("title") == "Fix login redirect" {
			created = &res.Items[i]
			break
		}
	}
	if created == nil {
		t.Fatal("submitted task was not created")
	}
	if created.Text("priority") != "P1" {
		t.Fatalf("created task priority = %q, want P1", created.Text("priority"))
	}
	if created.Text("repo") != "ofam/matomeru" {
		t.Fatalf("hidden default repo not persisted: got %q, want %q", created.Text("repo"), "ofam/matomeru")
	}
	if created.Text("defaultHarness") != "codex" {
		t.Fatalf("hidden default defaultHarness not persisted: got %q, want %q", created.Text("defaultHarness"), "codex")
	}
	if created.Text("workStatus") == "todo" {
		t.Fatalf("created task kept excluded workStatus %q", created.Text("workStatus"))
	}

	// 3. Instant submit on a non-pane noun (KindTask) synthesizes summary fallback without opening modal.
	d.Send(core.CommandEvent{Command: core.Command{
		ID: views.CmdNew,
		Args: map[string]any{
			"kind":     data.KindTask,
			"defaults": map[string]any{"priority": "P0", "repo": "instant/repo"},
			"submit":   true,
		},
		Source: "test",
		Actor:  User,
	}})
	if a.Modal() != "" {
		t.Fatalf("submit:true should not open modal, got %q", a.Modal())
	}
	res, err = a.Store.Query(data.Query{Kind: data.KindTask})
	if err != nil {
		t.Fatal(err)
	}
	foundInstant := false
	for _, rec := range res.Items {
		if rec.Text("repo") == "instant/repo" && rec.Text("priority") == "P0" && strings.TrimSpace(rec.Text("title")) != "" {
			foundInstant = true
			break
		}
	}
	if !foundInstant {
		t.Fatal("submit:true did not create task with defaults and fallback title")
	}
}

func TestSpacePanesInstantTerminalAndAgentForm(t *testing.T) {
	// 0. CheckBindings("") must report 0 errors with the new SpacePanes C and n bindings.
	rep, err := CheckBindings("")
	if err != nil {
		t.Fatalf("CheckBindings error: %v", err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings reported %d error(s): %+v", len(rep.Errors), rep.Errors)
	}

	a, d := demo(t)
	host := newLaunchCaptureHost()
	a.panes.Host = host
	a.panes.Term = ptyhost.NewController(host)

	// Set a concrete defaultDir on the selected pane's project/task to verify cwd inheritance.
	projDir := filepath.Join(t.TempDir(), "alpha-workspace")
	selPane, ok := a.SelectedPane()
	if !ok || selPane == nil {
		t.Fatal("expected an initial selected pane")
	}
	taskRef, hasTask := selPane.RefField("task")
	if !hasTask {
		t.Fatal("expected initial selected pane to belong to a task")
	}
	if _, _, err := a.Store.Patch(data.PatchInput{
		Ref:    taskRef,
		Fields: map[string]any{"defaultDir": projDir},
	}, User); err != nil {
		t.Fatalf("patch task defaultDir: %v", err)
	}

	beforePanes, err := a.Store.Query(data.Query{Kind: data.KindPane})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Press C in SpacePanes: creates zsh pane immediately (no modal!),
	//    selects it in rail, opens live session with inherited cwd, and lands
	//    directly in ModeTerminal focused on siteScreen.
	d.Keys("1", "C")
	if a.Modal() != "" {
		t.Fatalf("C in SpacePanes should not open a modal, got modal=%q", a.Modal())
	}
	if a.Space() != SpacePanes {
		t.Fatalf("expected SpacePanes, got %q", a.Space())
	}
	if a.Mode() != ModeTerminal {
		t.Fatalf("expected ModeTerminal after C, got %q (status=%q)", a.Mode(), a.Status())
	}
	if a.Focus() != siteScreen {
		t.Fatalf("expected focus on %q after C, got %q", siteScreen, a.Focus())
	}

	afterPanes, err := a.Store.Query(data.Query{Kind: data.KindPane})
	if err != nil {
		t.Fatal(err)
	}
	if len(afterPanes.Items) != len(beforePanes.Items)+1 {
		t.Fatalf("pane count = %d, want %d", len(afterPanes.Items), len(beforePanes.Items)+1)
	}

	created, ok := a.SelectedPane()
	if !ok || created == nil {
		t.Fatal("expected newly created pane to be selected in rail")
	}
	if created.ID == selPane.ID {
		t.Fatalf("rail did not move selection to newly created pane (still %q)", selPane.ID)
	}
	if created.Text("harness") != "zsh" {
		t.Fatalf("created instant terminal harness = %q, want zsh", created.Text("harness"))
	}
	if created.Text("cwd") != projDir {
		t.Fatalf("created instant terminal cwd = %q, want inherited %q", created.Text("cwd"), projDir)
	}
	if gotTask, ok := created.RefField("task"); !ok || gotTask != taskRef {
		t.Fatalf("created instant terminal task = %v (ok=%v), want %v", gotTask, ok, taskRef)
	}
	if a.panes.activePage[created.ID] != views.ScreenPageID {
		t.Fatalf("created pane activePage = %q, want %q", a.panes.activePage[created.ID], views.ScreenPageID)
	}
	if len(host.opens) != 1 {
		t.Fatalf("expected 1 live PTY session opened, got %d", len(host.opens))
	}
	if host.opens[0].id != created.ID || host.opens[0].cwd != projDir || host.opens[0].cmd != "zsh" {
		t.Fatalf("unexpected PTY open call: %+v", host.opens[0])
	}

	// Detach from terminal back to Normal mode.
	d.Keys("ctrl+a", "d")
	if a.Mode() != ModeNormal {
		t.Fatalf("expected ModeNormal after ctrl+a d, got %q", a.Mode())
	}

	// 2. Press n in SpacePanes: opens "New Agent" form (ModeForm) where harness
	//    choices exclude "zsh" and only contain agent harnesses.
	d.Keys("n")
	if a.Modal() != "form" || a.Mode() != ModeForm {
		t.Fatalf("n in SpacePanes did not open form: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	rf := a.activeRecordForm()
	if rf == nil || rf.kind != data.KindPane {
		t.Fatalf("expected pane form, got %+v", rf)
	}
	fm := rf.Model()
	if fm.Title != "New Agent" {
		t.Fatalf("form title = %q, want %q", fm.Title, "New Agent")
	}
	checkNoZsh := func(stage string) {
		t.Helper()
		var enum []string
		for _, sp := range fm.Specs {
			if sp.Name == "harness" {
				enum = sp.Enum
				break
			}
		}
		if len(enum) == 0 {
			t.Fatalf("%s: expected non-empty agent harness choices", stage)
		}
		if slices.Contains(enum, "zsh") {
			t.Fatalf("%s: New Agent harness choices must exclude zsh, got %v", stage, enum)
		}
		if got := data.Text(fm.Values["harness"]); got == "" || got == "zsh" || !slices.Contains(enum, got) {
			t.Fatalf("%s: New Agent default harness = %q, want one of %v", stage, got, enum)
		}
	}
	checkNoZsh("initial")

	// Async harness inventory updates while New Agent form is open must keep zsh excluded.
	a.SetInstalledHarnesses([]harness.InstalledHarness{
		{ID: "claude", Installed: true},
		{ID: "codex", Installed: true},
	})
	checkNoZsh("after SetInstalledHarnesses")
}
