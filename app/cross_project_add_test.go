package app

import (
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

type crossProjectFixture struct {
	a              *App
	d              *Driver
	apollo, hermes data.Record
}

func setupCrossProjectFixture(t *testing.T) *crossProjectFixture {
	t.Helper()
	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	mk := func(fields map[string]any) data.Record {
		t.Helper()
		rec, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: fields}, User)
		if err != nil {
			t.Fatal(err)
		}
		return *rec
	}
	f := &crossProjectFixture{}
	f.apollo = mk(map[string]any{"title": "Apollo", "workStatus": "active"})
	mk(map[string]any{"title": "Build nav shell", "parent": f.apollo.Ref(), "workStatus": "todo"})
	f.hermes = mk(map[string]any{"title": "Hermes", "workStatus": "active"})
	mk(map[string]any{"title": "Wire courier", "parent": f.hermes.Ref(), "workStatus": "todo"})
	a, err := New(store, Options{FS: smokeFS()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	f.a, f.d = a, NewDriver(a, 120, 36)
	return f
}

func (f *crossProjectFixture) children(t *testing.T, proj data.Record) map[string]data.Record {
	t.Helper()
	p := proj.Ref()
	res, err := f.a.Store.Query(data.Query{Kind: data.KindTask, Parent: &p})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]data.Record{}
	for _, r := range res.Items {
		out[r.Text("title")] = r
	}
	return out
}

// openPortalOutline opens the project's tab from 5 Projects and selects its
// Tasks section, so the embedded outline has focus.
func (f *crossProjectFixture) openPortalOutline(t *testing.T, proj data.Record) {
	t.Helper()
	a, d := f.a, f.d
	d.Keys("5")
	for i := 0; i < 5; i++ {
		tbl := a.nav.tables[projectsDirTable]
		if tbl != nil && tbl.SelectedIdx < len(tbl.Rows) && tbl.Rows[tbl.SelectedIdx].Ref.ID == proj.ID {
			break
		}
		d.Keys("j")
	}
	d.Keys("enter")
	if a.Space() != projectTabID(proj.ID) {
		t.Fatalf("enter did not open %s's tab: space=%q", proj.Text("title"), a.Space())
	}
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdProjectSectionSelect, Args: map[string]any{"section": "tasks"}, Actor: User}})
}

// addViaGO presses g o, picks the first row (always a project other than the
// current one), types a title and submits the form.
func (f *crossProjectFixture) addViaGO(t *testing.T, title string) {
	t.Helper()
	a, d := f.a, f.d
	d.Keys("g", "o")
	if a.Modal() != crossProjectPickerID {
		t.Fatalf("g o did not open the project picker: modal=%q mode=%q status=%q", a.Modal(), a.Mode(), a.Status())
	}
	d.Keys("enter")
	if a.Modal() != crossProjectFormID || a.Mode() != ModeForm {
		t.Fatalf("picking a project did not open the task form: modal=%q mode=%q status=%q", a.Modal(), a.Mode(), a.Status())
	}
	d.Keys("i")
	d.Type(title)
	d.Keys("esc", "ctrl+s")
}

func TestCrossProjectAddCatalogOrdersOtherProjectsFirst(t *testing.T) {
	f := setupCrossProjectFixture(t)
	feat := crossProjectAddFactory(f.a)
	got, err := feat.Catalog().ListTargetProjects(f.apollo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != f.hermes.ID || got[0].IsCurrent || !got[1].IsCurrent || got[1].ID != f.apollo.ID {
		t.Fatalf("targets = %+v, want Hermes then current Apollo", got)
	}
	if got[0].TaskCount != 1 || got[1].TaskCount != 1 {
		t.Fatalf("task counts = %d/%d, want 1/1", got[0].TaskCount, got[1].TaskCount)
	}
}

func TestCrossProjectAddFromProjectPortalOutline(t *testing.T) {
	f := setupCrossProjectFixture(t)
	a := f.a
	f.openPortalOutline(t, f.apollo)
	if a.Focus() != projectOutlineTree || a.activePortalProjectID(a.State()) != f.apollo.ID {
		t.Fatalf("expected Apollo portal outline: space=%q focus=%q project=%q", a.Space(), a.Focus(), a.activePortalProjectID(a.State()))
	}
	space, focus := a.Space(), a.Focus()
	f.addViaGO(t, "Ship courier")
	if a.Modal() != "" || a.Space() != space || a.Focus() != focus {
		t.Fatalf("outline not restored: modal=%q space=%q focus=%q status=%q", a.Modal(), a.Space(), a.Focus(), a.Status())
	}
	if _, ok := f.children(t, f.hermes)["Ship courier"]; !ok {
		t.Fatalf("task not created under Hermes: %v (status %q)", f.children(t, f.hermes), a.Status())
	}
	if _, ok := f.children(t, f.apollo)["Ship courier"]; ok {
		t.Fatal("task leaked into the current project Apollo")
	}
}

func TestCrossProjectAddFromIndexTree(t *testing.T) {
	f := setupCrossProjectFixture(t)
	a, d := f.a, f.d
	d.Keys("2")
	if !a.inWorkTree(a.State()) {
		t.Fatalf("2 did not open the Work tree: space=%q focus=%q", a.Space(), a.Focus())
	}
	f.addViaGO(t, "Index added")
	if a.Modal() != "" || a.Space() != SpaceWork {
		t.Fatalf("index tree not restored: modal=%q space=%q", a.Modal(), a.Space())
	}
	total := len(f.children(t, f.hermes)) + len(f.children(t, f.apollo))
	if total != 3 {
		t.Fatalf("expected one new task across projects, got %d children (status %q)", total, a.Status())
	}
}

func TestCrossProjectAddFromOutlineOverlay(t *testing.T) {
	f := setupCrossProjectFixture(t)
	a, d := f.a, f.d
	d.Keys("T")
	if a.Modal() != "outline" || a.Mode() != ModeOutline {
		t.Fatalf("T did not open the outline overlay: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	f.addViaGO(t, "Overlay added")
	if a.Modal() != "outline" || a.Mode() != ModeOutline {
		t.Fatalf("outline overlay not restored: modal=%q mode=%q status=%q", a.Modal(), a.Mode(), a.Status())
	}
	total := len(f.children(t, f.hermes)) + len(f.children(t, f.apollo))
	if total != 3 {
		t.Fatalf("expected one new task across projects, got %d (status %q)", total, a.Status())
	}
}

func TestCrossProjectAddSettingOptOut(t *testing.T) {
	f := setupCrossProjectFixture(t)
	a, d := f.a, f.d
	setSettingForTest(t, a, SetOutlineCrossProjectAdd, false)
	d.Keys("T", "g", "o")
	if a.Modal() == crossProjectPickerID {
		t.Fatal("g o opened the picker with ui.outlineCrossProjectAdd=false")
	}
}

func TestCrossProjectAddUndo(t *testing.T) {
	f := setupCrossProjectFixture(t)
	a, d := f.a, f.d
	f.openPortalOutline(t, f.apollo)
	f.addViaGO(t, "Undo me")
	if _, ok := f.children(t, f.hermes)["Undo me"]; !ok {
		t.Fatalf("task not created (status %q)", a.Status())
	}
	d.Keys("u")
	if _, ok := f.children(t, f.hermes)["Undo me"]; ok {
		t.Fatalf("u did not undo the cross-project add (status %q)", a.Status())
	}
}
