package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/form"
)

func TestRepoBindingsValid(t *testing.T) {
	rep, err := CheckBindings("")
	if err != nil {
		t.Fatalf("CheckBindings error: %v", err)
	}
	if len(rep.Errors) > 0 {
		t.Fatalf("CheckBindings reported %d error(s): %+v", len(rep.Errors), rep.Errors)
	}
}

func TestRepoMultiSelectRefPickerAndFormSubmit(t *testing.T) {
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	a, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	d := NewDriver(a, 120, 36)

	rAlpha, _, err := store.Create(data.CreateInput{Kind: data.KindRepo, Fields: map[string]any{"name": "alpha", "source": "manual"}}, User)
	if err != nil {
		t.Fatal(err)
	}
	rBeta, _, err := store.Create(data.CreateInput{Kind: data.KindRepo, Fields: map[string]any{"name": "beta", "source": "manual"}}, User)
	if err != nil {
		t.Fatal(err)
	}

	// Open New Task form.
	d.Send(core.CommandEvent{Command: core.Command{ID: views.CmdNew, Args: map[string]any{"kind": data.KindTask}}})
	rf := a.activeRecordForm()
	if rf == nil {
		t.Fatalf("expected active record form, modal=%q", a.Modal())
	}
	fm := rf.Model()
	if slices.ContainsFunc(fm.Specs, func(sp core.ParamSpec) bool { return sp.Name == "repo" }) {
		t.Fatal("legacy free-text repo field should be hidden from task form")
	}
	if !slices.ContainsFunc(fm.Specs, func(sp core.ParamSpec) bool { return sp.Name == "repos" && sp.Repeatable && sp.Type == core.ParamRef }) {
		t.Fatalf("expected repeatable ParamRef 'repos' in task form specs")
	}

	fm.SetValue("title", "Multi-repo task")
	if !fm.FocusField("repos") {
		t.Fatal("failed to focus repos field")
	}
	// Trigger ref picker on repos field.
	d.Send(core.CommandEvent{Command: core.Command{ID: form.CmdPick}})
	pm := a.activePicker("ref")
	if pm == nil {
		t.Fatalf("expected ref picker modal, modal=%q", a.Modal())
	}
	if !pm.Model().MultiSelect {
		t.Fatal("expected MultiSelect=true on repeatable ref picker")
	}
	if a.Mode() != ModePickerFilter || !pm.Model().Filtering {
		t.Fatalf("expected picker to start in filter mode, mode=%q filtering=%v", a.Mode(), pm.Model().Filtering)
	}

	// Exit filter mode -> normal picker mode -> mark both alpha and beta with space -> confirm with enter.
	d.Keys("esc", "space", "j", "space", "enter")
	if a.Modal() != "form" {
		t.Fatalf("expected return to form modal after picker confirm, modal=%q", a.Modal())
	}
	gotRefs := currentPickedRefs(fm.Values["repos"])
	if len(gotRefs) != 2 || !slices.Contains(gotRefs, rAlpha.Ref()) || !slices.Contains(gotRefs, rBeta.Ref()) {
		t.Fatalf("form repos after multi-select = %#v", fm.Values["repos"])
	}

	// Re-open ref picker: verify alpha & beta are pre-marked, then type "gamma" and create inline.
	if !fm.FocusField("repos") {
		t.Fatal("failed to refocus repos field")
	}
	d.Send(core.CommandEvent{Command: core.Command{ID: form.CmdPick}})
	pm = a.activePicker("ref")
	if pm == nil {
		t.Fatal("expected ref picker modal on reopen")
	}
	if !pm.Model().Marked[data.RefText(rAlpha.Ref())] || !pm.Model().Marked[data.RefText(rBeta.Ref())] {
		t.Fatalf("expected pre-marked repos in picker, got Marked=%+v", pm.Model().Marked)
	}
	d.Type("gamma")
	d.Keys("enter")
	if a.Modal() != "form" {
		t.Fatalf("expected return to form modal after inline create, modal=%q", a.Modal())
	}
	rGamma, err := store.GetByField(data.KindRepo, "name", "gamma")
	if err != nil || rGamma == nil {
		t.Fatalf("expected inline create to create KindRepo with SummaryField 'name'=gamma, err=%v", err)
	}
	gotRefs = currentPickedRefs(fm.Values["repos"])
	if len(gotRefs) != 3 || !slices.Contains(gotRefs, rAlpha.Ref()) || !slices.Contains(gotRefs, rBeta.Ref()) || !slices.Contains(gotRefs, rGamma.Ref()) {
		t.Fatalf("form repos after inline create = %#v", fm.Values["repos"])
	}

	// Submit form -> verify task created with RelRepo relations and NO "repos" list stored in Fields.
	d.Keys("ctrl+s")
	if a.Modal() != "" {
		t.Fatalf("expected form to close after submit, modal=%q status=%q", a.Modal(), a.Status())
	}
	taskRec, err := store.GetByField(data.KindTask, "title", "Multi-repo task")
	if err != nil || taskRec == nil {
		t.Fatalf("created task not found: %v", err)
	}
	if _, exists := taskRec.Fields["repos"]; exists {
		t.Fatalf("task.Fields must not contain relation list 'repos': %+v", taskRec.Fields)
	}
	linked, labels := data.RecordRepos(store, *taskRec)
	if len(linked) != 3 || !slices.Contains(labels, "alpha") || !slices.Contains(labels, "beta") || !slices.Contains(labels, "gamma") {
		t.Fatalf("linked repos after create = %v (labels=%v)", linked, labels)
	}

	// Open Edit form on task -> verify repos pre-populated -> remove beta -> save -> verify relation removed.
	d.Send(core.CommandEvent{Command: core.Command{ID: views.CmdEdit, Target: &core.Ref{Kind: data.KindTask, ID: taskRec.ID}}})
	editRF := a.activeRecordForm()
	if editRF == nil {
		t.Fatalf("expected edit form modal, modal=%q", a.Modal())
	}
	editFM := editRF.Model()
	initRefs := currentPickedRefs(editFM.Values["repos"])
	if len(initRefs) != 3 {
		t.Fatalf("expected 3 pre-populated repos in edit form, got %#v", editFM.Values["repos"])
	}
	editFM.SetValue("repos", []string{data.RefText(rAlpha.Ref()), data.RefText(rGamma.Ref())})
	d.Keys("ctrl+s")
	linked, labels = data.RecordRepos(store, *taskRec)
	if len(linked) != 2 || slices.Contains(labels, "beta") {
		t.Fatalf("linked repos after edit = %v (labels=%v)", linked, labels)
	}
}

func TestRepoAutoHarvestPanesSetting(t *testing.T) {
	makeRepoDir := func(name string) string {
		dir := filepath.Join(t.TempDir(), name)
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	// 1. Default (repo.autoHarvestPanes = true): harvesting registers repo with source="pane".
	storeOn, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { storeOn.Close() })
	aOn, err := New(storeOn, Options{})
	if err != nil {
		t.Fatal(err)
	}
	repoDirOn := makeRepoDir("pane-harvest-on")
	aOn.maybeHarvestPaneRepo(repoDirOn)
	rec, err := storeOn.GetByField(data.KindRepo, "name", "pane-harvest-on")
	if err != nil || rec == nil || rec.Text("source") != "pane" {
		t.Fatalf("expected auto-harvested repo when setting is true, rec=%+v err=%v", rec, err)
	}

	// 2. Opt-out (repo.autoHarvestPanes = false): harvesting does nothing.
	cfgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfgDir, "settings.json"), []byte(`{"repo.autoHarvestPanes": false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	storeOff, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { storeOff.Close() })
	aOff, err := New(storeOff, Options{ConfigDir: cfgDir})
	if err != nil {
		t.Fatal(err)
	}
	repoDirOff := makeRepoDir("pane-harvest-off")
	aOff.maybeHarvestPaneRepo(repoDirOff)
	if res, err := storeOff.Query(data.Query{Kind: data.KindRepo}); err != nil || len(res.Items) != 0 {
		t.Fatalf("expected 0 repos when repo.autoHarvestPanes=false, got %d (err=%v)", len(res.Items), err)
	}
}
