package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ext"
	"ofam.dev/matomeru/runs"
)

func TestExtensionInstallBindingsClean(t *testing.T) {
	rep, err := CheckBindings("")
	if err != nil {
		t.Fatalf("CheckBindings: %v", err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings errors: %+v", rep.Errors)
	}
	for _, w := range rep.Warnings {
		if (w.Command == CmdExtInstall || w.Command == CmdExtInstallAll || w.Scope == data.KindExtension || w.Scope == SpaceExtensions) &&
			strings.TrimSpace(w.Message) != "" {
			t.Fatalf("unexpected extension binding warning: %+v", w)
		}
	}
}

func TestExtensionCatalogEnterAndUpdate(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	binDir := t.TempDir()
	goDir := t.TempDir()

	script(t, binDir, "o-git", `case "$1" in
--version) echo "o-git 1.2.3" ;;
capabilities) echo '{"summary":"git views","capabilities":["git.repo"],"commands":["status"]}' ;;
esac
`)
	fakeGo := filepath.Join(goDir, "fake-go")
	script(t, goDir, "fake-go", `
echo "GOPRIVATE=$GOPRIVATE ARGS=$*"
if [ "$1" = "install" ]; then
  case "$2" in
  *o-csv*)
    cat > "`+binDir+`/o-csv" <<'EOF'
#!/bin/sh
case "$1" in
--version) echo "o-csv 1.5.0" ;;
capabilities) echo '{"summary":"CSV tools installed","capabilities":["csv.view"],"commands":["view"]}' ;;
esac
EOF
    chmod +x "`+binDir+`/o-csv"
    exit 0
    ;;
  esac
fi
exit 0
`)

	catalog := []ext.CatalogEntry{
		{Name: "o-git", Package: "ofam.dev/o-git@latest", Summary: "Git tools", Category: ext.CategoryCore},
		{Name: "o-csv", Package: "ofam.dev/o-csv@latest", Summary: "CSV tools", Category: ext.CategoryCore},
	}
	mgr := ext.New(ext.Options{
		Tools:          []string{"o-git"},
		Catalog:        catalog,
		IncludeCatalog: true,
		GoBinary:       fakeGo,
		GoPrivate:      "ofam.dev",
	})
	a, d := liveApp(t, false, Options{Live: true, Extensions: mgr, ExtensionDirs: []string{binDir}})

	d.Keys("4", "R")
	a.WaitBackground()
	wake(d)

	csvRec, err := a.Store.GetByField(data.KindExtension, "name", "o-csv")
	if err != nil || csvRec.Text("runStatus") != ext.StatusAvailable {
		t.Fatalf("expected available o-csv after R: rec=%+v err=%v status=%q", csvRec, err, a.Status())
	}
	gitRec, err := a.Store.GetByField(data.KindExtension, "name", "o-git")
	if err != nil || gitRec.Text("runStatus") != ext.StatusHealthy {
		t.Fatalf("expected healthy o-git after R: rec=%+v err=%v", gitRec, err)
	}

	// Focus healthy o-git using real navigation and press enter: must open run launch modal.
	focusExt := func(name string) {
		t.Helper()
		d.Keys("g", "g")
		for range 10 {
			tbl := a.records(extTable, data.KindExtension)
			if row, ok := tbl.FocusedRow(); ok && row.Cells["name"] == name {
				return
			}
			d.Keys("j")
		}
		t.Fatalf("could not navigate to extension %q", name)
	}
	focusExt("o-git")
	d.Keys("enter")
	if a.Modal() != "launch" || !strings.Contains(d.Frame(), "Run o-git") {
		t.Fatalf("enter on healthy extension did not open run form: modal=%q status=%q\n%s", a.Modal(), a.Status(), d.Frame())
	}
	d.Keys("esc", "esc")
	if a.Modal() != "" {
		t.Fatalf("modal stayed open: %q", a.Modal())
	}

	// Navigate to available o-csv and press enter: must trigger install and transition to healthy.
	focusExt("o-csv")
	d.Keys("enter")
	if a.Modal() != "" {
		t.Fatalf("enter on available extension opened modal %q instead of installing", a.Modal())
	}
	a.WaitBackground()
	wake(d)

	csvAfter, err := a.Store.Get(csvRec.Ref())
	if err != nil || csvAfter.Text("runStatus") != ext.StatusHealthy || csvAfter.Text("version") != "o-csv 1.5.0" {
		t.Fatalf("o-csv after install = %+v err=%v status=%q", csvAfter, err, a.Status())
	}
	if !strings.Contains(a.Status(), "installed o-csv · ready") {
		t.Fatalf("status = %q, want installed o-csv · ready", a.Status())
	}

	runRec, err := a.Store.GetByField(data.KindRun, "title", "install o-csv")
	if err != nil || runRec.Text("runStatus") != runs.StatusDone || !strings.Contains(runRec.Text("output"), "GOPRIVATE=ofam.dev") {
		t.Fatalf("install run record = %+v err=%v", runRec, err)
	}

	// Press 'i' on healthy (still installable) o-csv to update it.
	focusExt("o-csv")
	d.Keys("i")
	a.WaitBackground()
	wake(d)
	if q, _ := a.Store.Query(data.Query{Kind: data.KindRun}); q.Total < 2 {
		t.Fatalf("expected second install run on 'i', got %d run(s); status=%q", q.Total, a.Status())
	}
}

func TestExtensionShowCatalogOptOut(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	cfgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfgDir, "settings.json"), []byte(`{"extensions.showCatalog": false}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a, d := liveApp(t, false, Options{ConfigDir: cfgDir})
	if _, _, err := a.Store.Create(data.CreateInput{Kind: data.KindExtension, Fields: map[string]any{
		"name": "o-git", "binary": "/usr/local/bin/o-git", "runStatus": ext.StatusHealthy, "summary": "Git tools",
	}}, User); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Store.Create(data.CreateInput{Kind: data.KindExtension, Fields: map[string]any{
		"name": "o-csv", "runStatus": ext.StatusAvailable, "installPkg": "ofam.dev/o-csv@latest", "category": ext.CategoryCore, "summary": "CSV tools",
	}}, User); err != nil {
		t.Fatal(err)
	}

	d.Keys("4")
	tbl := a.records(extTable, data.KindExtension)
	if len(tbl.Rows) != 1 || tbl.Rows[0].Cells["name"] != "o-git" {
		t.Fatalf("expected available rows hidden when extensions.showCatalog=false, got %d rows: %+v", len(tbl.Rows), tbl.Rows)
	}
	frame := d.Frame()
	if !strings.Contains(frame, "o-git") || strings.Contains(frame, "o-csv") {
		t.Fatalf("frame should show o-git and hide o-csv:\n%s", frame)
	}

	// Re-enabling extensions.showCatalog brings available rows back immediately.
	a.cfg.userSettings[SetExtensionsShowCatalog] = true
	a.reloadSettings()
	a.applySettings()
	tbl = a.records(extTable, data.KindExtension)
	if len(tbl.Rows) != 2 {
		t.Fatalf("expected 2 rows after enabling extensions.showCatalog, got %d", len(tbl.Rows))
	}
}

func TestExtensionInstallAllCore(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	binDir := t.TempDir()
	goDir := t.TempDir()

	script(t, binDir, "o-git", `case "$1" in --version) echo "o-git 1.0" ;; esac`)
	fakeGo := filepath.Join(goDir, "fake-go")
	script(t, goDir, "fake-go", `
if [ "$1" = "install" ]; then
  case "$2" in
  *o-json*)
    printf '#!/bin/sh\necho "o-json 1.0"\n' > "`+binDir+`/o-json"
    chmod +x "`+binDir+`/o-json"
    exit 0
    ;;
  esac
fi
exit 1
`)

	catalog := []ext.CatalogEntry{
		{Name: "o-git", Package: "ofam.dev/o-git@latest", Summary: "Git", Category: ext.CategoryCore},
		{Name: "o-json", Package: "ofam.dev/o-json@latest", Summary: "JSON", Category: ext.CategoryCore},
		{Name: "o-koe", Package: "ofam.dev/o-koe/cmd/o-koe@latest", Summary: "Voice", Category: ext.CategoryOptional},
	}
	mgr := ext.New(ext.Options{
		Tools:          []string{"o-git"},
		Catalog:        catalog,
		IncludeCatalog: true,
		GoBinary:       fakeGo,
		GoPrivate:      "ofam.dev",
	})
	a, d := liveApp(t, false, Options{Live: true, Extensions: mgr, ExtensionDirs: []string{binDir}})

	d.Keys("4", "R")
	a.WaitBackground()
	wake(d)

	d.Keys("D")
	a.WaitBackground()
	wake(d)

	if !strings.Contains(a.Status(), "installed 1 of 1 core OFAM extensions") {
		t.Fatalf("status after D = %q", a.Status())
	}
	jsonRec, err := a.Store.GetByField(data.KindExtension, "name", "o-json")
	if err != nil || jsonRec.Text("runStatus") != ext.StatusHealthy {
		t.Fatalf("o-json after D = %+v err=%v", jsonRec, err)
	}
	koeRec, err := a.Store.GetByField(data.KindExtension, "name", "o-koe")
	if err != nil || koeRec.Text("runStatus") != ext.StatusAvailable {
		t.Fatalf("optional o-koe should remain available after D: %+v err=%v", koeRec, err)
	}

	// Running D again reports all core extensions already installed.
	d.Keys("D")
	if !strings.Contains(a.Status(), "all core OFAM extensions are already installed") {
		t.Fatalf("status on second D = %q", a.Status())
	}
}
