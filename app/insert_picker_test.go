package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/modal"
)

func setupInsertPickerFixture(t *testing.T) (*App, *Driver, *data.Store, string) {
	t.Helper()
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "alpha.go"), []byte("package alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Demo\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := data.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	seed := core.Actor{Kind: "system", ID: "seed"}

	proj, _, err := store.Create(data.CreateInput{
		Kind: data.KindTask,
		ID:   "proj-insert",
		Fields: map[string]any{
			"title":      "Insert Project",
			"defaultDir": workDir,
		},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Create(data.CreateInput{
		Kind: data.KindContext,
		ID:   "ctx-auth-spec",
		Fields: map[string]any{
			"title":       "Auth Spec",
			"value":       "https://docs.example.com/auth",
			"contextKind": "url",
			"official":    true,
			"task":        proj.Ref(),
		},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Create(data.CreateInput{
		Kind: data.KindDocument,
		ID:   "doc-plan",
		Fields: map[string]any{
			"title":    "Architecture Plan",
			"filePath": filepath.Join(workDir, "README.md"),
			"task":     proj.Ref(),
		},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Create(data.CreateInput{
		Kind: data.KindCommand,
		ID:   "cmd-test",
		Fields: map[string]any{
			"title":   "Run unit tests",
			"command": "go test ./app",
			"task":    proj.Ref(),
		},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.Create(data.CreateInput{
		Kind: data.KindPane,
		ID:   "pane-insert",
		Fields: map[string]any{
			"title":     "claude: insert",
			"harness":   "claude",
			"cwd":       workDir,
			"runStatus": "running",
			"task":      proj.Ref(),
		},
	}, seed)
	if err != nil {
		t.Fatal(err)
	}

	a, err := New(store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	a.SetComposerSkills([]composerCompletion{
		{Trigger: "$", Label: "$tdd-loop", Insert: "tdd-loop", Detail: "Test-driven loop", Kind: "skill"},
	})
	d := NewDriver(a, 120, 36)
	return a, d, store, workDir
}

func selectInsertSourceInPicker(t *testing.T, a *App, d *Driver, sourceID string) {
	t.Helper()
	pm := a.topPicker()
	if pm == nil {
		t.Fatalf("expected top modal to be a PickerModal, got modal=%q", a.Modal())
	}
	items := pm.Model().Filtered()
	idx := -1
	for i, it := range items {
		if it.ID == sourceID {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("source %q not found in picker items %+v", sourceID, items)
	}
	pm.Model().SelectedIdx = idx
	d.Keys("enter")
}

func selectPathEntryInInsertPicker(t *testing.T, a *App, d *Driver, name string) {
	t.Helper()
	pp, ok := a.modals.Top().(*modal.PathPickerModal)
	if !ok || pp == nil {
		t.Fatalf("expected top modal to be PathPickerModal, got modal=%q %T", a.Modal(), a.modals.Top())
	}
	d.Keys("/")
	d.Type(name)
	d.Keys("esc", "enter")
}

func TestInsertPickerBindingsValid(t *testing.T) {
	rep, err := CheckBindings("")
	if err != nil {
		t.Fatalf("CheckBindings error: %v", err)
	}
	if len(rep.Errors) > 0 {
		t.Fatalf("CheckBindings(\"\") reported %d error(s): %+v", len(rep.Errors), rep.Errors)
	}
}

func TestInsertPickerOutlineModalAndWorkTreeLiveSwitch(t *testing.T) {
	a, d, store, _ := setupInsertPickerFixture(t)

	// --- Part A: T Outline modal inline edit (ModeOutlineEdit) ---
	d.Keys("T")
	if a.Modal() != "outline" {
		t.Fatalf("T did not open outline modal: modal=%q", a.Modal())
	}
	d.Keys("a")
	if a.Mode() != ModeOutlineEdit {
		t.Fatalf("a in outline did not enter ModeOutlineEdit: mode=%q", a.Mode())
	}
	d.Type(" see ")

	// Open File Picker via ctrl+f.
	d.Keys("ctrl+f")
	if a.Modal() != insertPickerModalID || a.Mode() != ModePicker {
		t.Fatalf("ctrl+f in ModeOutlineEdit: modal=%q mode=%q status=%q", a.Modal(), a.Mode(), a.Status())
	}
	if _, ok := a.modals.Top().(*modal.PathPickerModal); !ok {
		t.Fatalf("expected PathPickerModal for ctrl+f, got %T", a.modals.Top())
	}
	ov := a.modals.Top().View(120, 36)
	var badgeTexts []string
	for _, b := range ov.Badges {
		badgeTexts = append(badgeTexts, b.Text)
	}
	if !strings.Contains(strings.Join(badgeTexts, "|"), "Files") || !strings.Contains(strings.Join(badgeTexts, "|"), "ctrl+l / alt+p switch") {
		t.Fatalf("file insert picker badges = %v, want Files and ctrl+l / alt+p switch", badgeTexts)
	}
	if ok, _ := ov.Facts["insertPicker"].(bool); !ok || ov.Facts["insertSource"] != "files" {
		t.Fatalf("file insert picker facts = %+v, want insertPicker=true insertSource=files", ov.Facts)
	}

	// Open Switch Picker via ctrl+l, verify (current) marker, then Esc back to File Picker!
	d.Keys("ctrl+l")
	if a.Modal() != insertPickerSwitchModalID {
		t.Fatalf("ctrl+l inside file picker did not open Switch Picker: modal=%q status=%q", a.Modal(), a.Status())
	}
	if frame := d.Frame(); !strings.Contains(frame, "Switch Picker") || !strings.Contains(frame, "Files (current)") {
		t.Fatalf("Switch Picker frame missing expected content:\n%s", frame)
	}
	// Default double-esc guard arms on first esc and closes ONLY Switch Picker on second esc.
	d.Keys("esc", "esc")
	if a.Modal() != insertPickerModalID {
		t.Fatalf("Esc in Switch Picker should return to File Picker: modal=%q", a.Modal())
	}
	if _, ok := a.modals.Top().(*modal.PathPickerModal); !ok {
		t.Fatalf("after Esc from Switch Picker, expected PathPickerModal, got %T", a.modals.Top())
	}

	// Switch from File Picker -> Context Picker via alt+p (also verifying filter-mode switch via ctrl+l!).
	d.Keys("/")
	if a.Mode() != ModePickerFilter {
		t.Fatalf("/ did not enter ModePickerFilter: mode=%q", a.Mode())
	}
	d.Keys("alt+p")
	if a.Modal() != insertPickerSwitchModalID {
		t.Fatalf("alt+p in ModePickerFilter did not open Switch Picker: modal=%q", a.Modal())
	}
	selectInsertSourceInPicker(t, a, d, "context")
	if a.Modal() != insertPickerModalID {
		t.Fatalf("switching to context did not replace insert picker: modal=%q status=%q", a.Modal(), a.Status())
	}
	if frame := d.Frame(); !strings.Contains(frame, "Auth Spec") {
		t.Fatalf("Context insert picker missing Auth Spec:\n%s", frame)
	}

	// Pick Auth Spec -> inserts URL straight into outline edit buffer while staying in ModeOutlineEdit!
	d.Keys("enter")
	if a.Modal() != "outline" || a.Mode() != ModeOutlineEdit {
		t.Fatalf("after picking context item: modal=%q mode=%q status=%q", a.Modal(), a.Mode(), a.Status())
	}
	tm, _, ok := a.activeEditTree()
	if !ok || tm.Edit() == nil {
		t.Fatalf("expected active outline edit tree after context insertion")
	}
	if got := tm.Edit().Value(); !strings.Contains(got, "see https://docs.example.com/auth") {
		t.Fatalf("outline edit buffer = %q, want it to contain context URL", got)
	}

	// Also insert a relative file via ctrl+f -> alpha.go while staying in ModeOutlineEdit.
	d.Type(" ")
	d.Keys("ctrl+f")
	selectPathEntryInInsertPicker(t, a, d, "alpha.go")
	if a.Modal() != "outline" || a.Mode() != ModeOutlineEdit {
		t.Fatalf("after picking file in outline edit: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	if got := tm.Edit().Value(); !strings.Contains(got, "@alpha.go") {
		t.Fatalf("outline edit buffer = %q, want @alpha.go", got)
	}
	// Commit outline edit and close outline modal.
	d.Keys("esc")
	if a.Mode() != ModeOutline {
		t.Fatalf("esc did not commit outline edit: mode=%q", a.Mode())
	}
	rec, err := store.Get(core.Ref{Kind: data.KindTask, ID: "proj-insert"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Text("title"), "https://docs.example.com/auth") || !strings.Contains(rec.Text("title"), "@alpha.go") {
		t.Fatalf("persisted outline title = %q, missing inserted values", rec.Text("title"))
	}
	d.Keys("q")
	if a.Modal() != "" {
		t.Fatalf("q did not close outline modal: modal=%q", a.Modal())
	}

	// --- Part B: 2 Work Tree inline edit (ModeOutlineEdit on page frame) ---
	d.Keys("2", "o")
	if a.Modal() != "" || a.Mode() != ModeOutlineEdit {
		t.Fatalf("o in 2 Work tree did not enter ModeOutlineEdit: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Type("Review ")
	d.Keys("ctrl+f")
	if a.Modal() != insertPickerModalID {
		t.Fatalf("ctrl+f in 2 Work ModeOutlineEdit did not open file picker: modal=%q", a.Modal())
	}
	d.Keys("ctrl+l")
	selectInsertSourceInPicker(t, a, d, "context")
	d.Keys("enter")
	if a.Modal() != "" || a.Mode() != ModeOutlineEdit {
		t.Fatalf("after picking context in 2 Work tree: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	workTM, _, ok := a.activeEditTree()
	if !ok || workTM.Edit() == nil {
		t.Fatalf("expected active 2 Work tree edit after insert")
	}
	if got := workTM.Edit().Value(); !strings.Contains(got, "Review https://docs.example.com/auth") {
		t.Fatalf("2 Work edit buffer = %q, want Review + context URL", got)
	}
	d.Keys("esc")
	if a.Mode() != ModeNormal {
		t.Fatalf("esc did not commit 2 Work inline add: mode=%q", a.Mode())
	}
}

func TestInsertPickerDirectOpenInOutlineComposerAndForm(t *testing.T) {
	a, d, _, workDir := setupInsertPickerFixture(t)

	// 1. ModeOutlineEdit: direct ctrl+l opens Insert Picker chooser -> tasks -> @task:proj-insert.
	d.Keys("2", "o")
	d.Type("Linked ")
	d.Keys("ctrl+l")
	if a.Modal() != insertPickerModalID {
		t.Fatalf("ctrl+l in ModeOutlineEdit did not open Insert Picker chooser: modal=%q", a.Modal())
	}
	selectInsertSourceInPicker(t, a, d, "tasks")
	d.Keys("enter")
	if a.Mode() != ModeOutlineEdit {
		t.Fatalf("expected ModeOutlineEdit after task insert, got %q", a.Mode())
	}
	if tm, _, ok := a.activeEditTree(); !ok || !strings.Contains(tm.Edit().Value(), "@task:proj-insert") {
		t.Fatalf("expected @task:proj-insert in outline edit buffer")
	}
	d.Keys("ctrl+c") // discard draft

	// 2. ModeComposer: ctrl+l inserts skills/recipes; ctrl+f attaches file chips AND live-switches to agents!
	d.Keys("1", "c")
	cm := a.activeRichComposer()
	if cm == nil || a.Mode() != ModeComposer {
		t.Fatalf("expected ModeComposer, got modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Type("Run ")

	// Direct ctrl+l -> Skills -> inserts "$tdd-loop ".
	d.Keys("ctrl+l")
	if a.Modal() != insertPickerModalID {
		t.Fatalf("ctrl+l in ModeComposer did not open Insert Picker: modal=%q", a.Modal())
	}
	selectInsertSourceInPicker(t, a, d, "skills")
	d.Keys("enter")
	if a.Mode() != ModeComposer || a.Modal() != "composer" {
		t.Fatalf("expected return to composer after skill insert: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	if !strings.Contains(cm.in.Value(), "$tdd-loop ") {
		t.Fatalf("composer value = %q, want $tdd-loop ", cm.in.Value())
	}

	// ctrl+f in composer -> live switch via ctrl+l to Agents -> filter claude -> inserts "@agent:claude " into input!
	d.Keys("ctrl+f")
	if a.Modal() != insertPickerModalID {
		t.Fatalf("ctrl+f in composer did not open insert file picker: modal=%q", a.Modal())
	}
	d.Keys("ctrl+l")
	selectInsertSourceInPicker(t, a, d, "agents")
	d.Keys("/")
	d.Type("claude")
	d.Keys("enter")
	if a.Mode() != ModeComposer || a.Modal() != "composer" {
		t.Fatalf("expected return to composer after agent switch insert: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	if !strings.Contains(cm.in.Value(), "@agent:claude ") {
		t.Fatalf("composer value = %q, want @agent:claude ", cm.in.Value())
	}

	// ctrl+f in composer -> select alpha.go -> STILL attaches file chip!
	d.Keys("ctrl+f")
	selectPathEntryInInsertPicker(t, a, d, "alpha.go")
	if a.Mode() != ModeComposer || a.Modal() != "composer" {
		t.Fatalf("expected return to composer after file attach: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	if len(cm.attachments) != 1 || cm.attachments[0].Path != filepath.Join(workDir, "alpha.go") {
		t.Fatalf("composer attachments = %+v, want [alpha.go]", cm.attachments)
	}
	d.Keys("esc")

	// 3. ModeFormEdit: ctrl+l inserts document path and ctrl+f inserts @alpha.go while staying in ModeFormEdit!
	d.Keys("2", "n")
	if a.Modal() != "form" || a.Mode() != ModeForm {
		t.Fatalf("n did not open create form: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Keys("i")
	if a.Mode() != ModeFormEdit {
		t.Fatalf("i in form did not enter ModeFormEdit: mode=%q", a.Mode())
	}
	d.Type("Doc: ")
	d.Keys("ctrl+l")
	if a.Modal() != insertPickerModalID {
		t.Fatalf("ctrl+l in ModeFormEdit did not open Insert Picker: modal=%q", a.Modal())
	}
	selectInsertSourceInPicker(t, a, d, "documents")
	d.Keys("enter")
	if a.Modal() != "form" || a.Mode() != ModeFormEdit {
		t.Fatalf("after document insert in form: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	fm := a.topFormModal()
	if fm == nil || !fm.Model().Editing {
		t.Fatalf("expected active editing FormModal after insert")
	}
	wantDocPath := filepath.Join(workDir, "README.md")
	gotTitle, _ := fm.Model().Values["title"].(string)
	if !strings.Contains(gotTitle, wantDocPath) {
		t.Fatalf("form title value = %q, want %q", gotTitle, wantDocPath)
	}

	// Also test ctrl+f in ModeFormEdit.
	d.Type(" ")
	d.Keys("ctrl+f")
	if a.Modal() != insertPickerModalID {
		t.Fatalf("ctrl+f in ModeFormEdit did not open file insert picker: modal=%q", a.Modal())
	}
	d.Keys("ctrl+l")
	selectInsertSourceInPicker(t, a, d, "recipes")
	d.Keys("enter")
	if a.Modal() != "form" || a.Mode() != ModeFormEdit {
		t.Fatalf("after recipe insert in form: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	gotTitle, _ = fm.Model().Values["title"].(string)
	if !strings.Contains(gotTitle, "go test ./app") {
		t.Fatalf("form title value = %q, want recipe command 'go test ./app'", gotTitle)
	}
}
