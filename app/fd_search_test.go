package app

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

func TestResolveFDBinary(t *testing.T) {
	t.Run("configured explicit file path", func(t *testing.T) {
		dir := t.TempDir()
		bin := filepath.Join(dir, "custom-fd")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		got, err := ResolveFDBinary(bin, func(string) (string, error) {
			t.Fatal("lookPath should not be called for explicit path")
			return "", nil
		})
		if err != nil || got != bin {
			t.Fatalf("ResolveFDBinary(%q) = %q, %v; want %q, nil", bin, got, err, bin)
		}
	})

	t.Run("configured directory rejected", func(t *testing.T) {
		dir := t.TempDir()
		_, err := ResolveFDBinary(dir, nil)
		if err == nil || !strings.Contains(err.Error(), SetToolsFDBinary) {
			t.Fatalf("ResolveFDBinary(dir) err = %v, want error mentioning %s", err, SetToolsFDBinary)
		}
	})

	t.Run("configured command name uses lookPath", func(t *testing.T) {
		got, err := ResolveFDBinary("my-fd", func(name string) (string, error) {
			if name == "my-fd" {
				return "/usr/local/bin/my-fd", nil
			}
			return "", errors.New("not found")
		})
		if err != nil || got != "/usr/local/bin/my-fd" {
			t.Fatalf("ResolveFDBinary(my-fd) = %q, %v", got, err)
		}
	})

	t.Run("fallback fd then fdfind", func(t *testing.T) {
		got, err := ResolveFDBinary("", func(name string) (string, error) {
			if name == "fd" {
				return "/opt/homebrew/bin/fd", nil
			}
			return "", errors.New("not found")
		})
		if err != nil || got != "/opt/homebrew/bin/fd" {
			t.Fatalf("fd fallback = %q, %v", got, err)
		}

		got, err = ResolveFDBinary("", func(name string) (string, error) {
			if name == "fdfind" {
				return "/usr/bin/fdfind", nil
			}
			return "", errors.New("not found")
		})
		if err != nil || got != "/usr/bin/fdfind" {
			t.Fatalf("fdfind fallback = %q, %v", got, err)
		}
	})

	t.Run("missing binary returns actionable error", func(t *testing.T) {
		_, err := ResolveFDBinary("", func(string) (string, error) {
			return "", errors.New("not found")
		})
		if err == nil {
			t.Fatal("expected error when neither fd nor fdfind exists")
		}
		msg := err.Error()
		for _, want := range []string{"fd", "fdfind", SetToolsFDBinary} {
			if !strings.Contains(msg, want) {
				t.Errorf("error %q missing %q", msg, want)
			}
		}
	})
}

func TestBuildFDArgs(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("full flags repeatable fields and tilde expansion", func(t *testing.T) {
		argv, workDir, err := BuildFDArgs(map[string]any{
			"pattern":       "config",
			"paths":         []string{"~/proj/alpha", "/tmp/beta"},
			"type":          "file",
			"extension":     []string{".go", "..md", "json"},
			"exclude":       []string{"vendor", "node_modules"},
			"maxDepth":      5,
			"minDepth":      2,
			"size":          "+10k",
			"changedWithin": "2d",
			"caseMode":      "sensitive",
			"glob":          true,
			"fullPath":      true,
			"hidden":        true,
			"noIgnore":      true,
			"follow":        true,
			"maxResults":    50,
		})
		if err != nil {
			t.Fatalf("BuildFDArgs error: %v", err)
		}
		wantDir := filepath.Join(home, "proj/alpha")
		if workDir != wantDir {
			t.Fatalf("workDir = %q, want %q", workDir, wantDir)
		}
		wantArgv := []string{
			"--color=never",
			"-t", "f",
			"-e", "go",
			"-e", "md",
			"-e", "json",
			"-S", "+10k",
			"-E", "vendor",
			"-E", "node_modules",
			"--max-depth", "5",
			"--min-depth", "2",
			"--changed-within", "2d",
			"-s",
			"-g", "-p", "-H", "-I", "-L",
			"--max-results", "50",
			"--search-path", wantDir,
			"--search-path", "/tmp/beta",
			"--", "config",
		}
		if !slices.Equal(argv, wantArgv) {
			t.Fatalf("argv =\n  %q\nwant\n  %q", argv, wantArgv)
		}
	})

	t.Run("reactive suppression of extension and size when type is dir", func(t *testing.T) {
		argv, _, err := BuildFDArgs(map[string]any{
			"type":      "dir",
			"extension": []string{"go", "md"},
			"size":      "+1m",
			"caseMode":  "insensitive",
		})
		if err != nil {
			t.Fatalf("BuildFDArgs error: %v", err)
		}
		want := []string{"--color=never", "-t", "d", "-i"}
		if !slices.Equal(argv, want) {
			t.Fatalf("argv = %q, want %q", argv, want)
		}
	})

	t.Run("all entry types", func(t *testing.T) {
		cases := map[string]string{
			"file":       "f",
			"dir":        "d",
			"symlink":    "l",
			"executable": "x",
			"empty":      "e",
		}
		for typ, flag := range cases {
			argv, _, err := BuildFDArgs(map[string]any{"type": typ})
			if err != nil {
				t.Fatalf("type %q: %v", typ, err)
			}
			if !slices.Equal(argv, []string{"--color=never", "-t", flag}) {
				t.Errorf("type %q -> %q", typ, argv)
			}
		}
	})

	t.Run("validation errors", func(t *testing.T) {
		bad := []struct {
			name string
			vals map[string]any
		}{
			{"invalid type", map[string]any{"type": "socket"}},
			{"invalid caseMode", map[string]any{"caseMode": "weird"}},
			{"negative maxDepth", map[string]any{"maxDepth": -1}},
			{"negative minDepth", map[string]any{"minDepth": -2}},
			{"minDepth > maxDepth", map[string]any{"minDepth": 4, "maxDepth": 2}},
			{"negative maxResults", map[string]any{"maxResults": -5}},
		}
		for _, tc := range bad {
			if _, _, err := BuildFDArgs(tc.vals); err == nil {
				t.Errorf("%s: expected error, got nil", tc.name)
			}
		}
	})
}

func TestFDSearchFormReactiveAndRepeatable(t *testing.T) {
	a, d := demo(t)

	// Open fd search form with g F.
	d.Keys("2", "g", "F")
	if a.Modal() != fdSearchModalID || a.Mode() != ModeForm {
		t.Fatalf("g F did not open fd.search form: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	fm := a.activeForm(fdSearchModalID).Model()
	hasVisible := func(name string) bool {
		return slices.ContainsFunc(fm.VisibleFields(), func(p core.ParamSpec) bool { return p.Name == name })
	}

	// Initially type == "any", so extension and size are visible.
	if !hasVisible("extension") || !hasVisible("size") {
		t.Fatalf("expected extension and size visible when type=any; visible=%+v", fm.VisibleFields())
	}

	// Cycle type: any -> file -> dir; extension and size must hide when type == "dir".
	fm.FocusField("type")
	a.viewMemo.gen++
	d.Keys("l") // file
	if fm.Values["type"] != "file" || !hasVisible("extension") || !hasVisible("size") {
		t.Fatalf("with type=file: val=%v ext=%v size=%v", fm.Values["type"], hasVisible("extension"), hasVisible("size"))
	}
	d.Keys("l") // dir
	if fm.Values["type"] != "dir" || hasVisible("extension") || hasVisible("size") {
		t.Fatalf("with type=dir expected extension/size hidden: val=%v ext=%v size=%v", fm.Values["type"], hasVisible("extension"), hasVisible("size"))
	}

	// Disabling edit.reactiveFields shows all fields even when type == "dir".
	d.Keys("esc", "esc")
	a.cfg.userSettings[SetFormReactiveFields] = false
	a.reloadSettings()
	a.applySettings()
	d.Keys("g", "F")
	fmOff := a.activeForm(fdSearchModalID).Model()
	if !fmOff.ShowAllFields {
		t.Fatal("fd.search form did not honor edit.reactiveFields=false")
	}
	fmOff.FocusField("type")
	a.viewMemo.gen++
	d.Keys("l", "l") // dir
	if !slices.ContainsFunc(fmOff.VisibleFields(), func(p core.ParamSpec) bool { return p.Name == "extension" }) ||
		!slices.ContainsFunc(fmOff.VisibleFields(), func(p core.ParamSpec) bool { return p.Name == "size" }) {
		t.Fatal("with edit.reactiveFields=false, extension/size must remain visible when type=dir")
	}
	d.Keys("esc", "esc")

	// Restore edit.reactiveFields = true and test repeatable paths (+/x + p path picker) and extension (+/x).
	a.cfg.userSettings[SetFormReactiveFields] = true
	a.reloadSettings()
	a.applySettings()
	d.Keys("g", "F")
	fm = a.activeForm(fdSearchModalID).Model()

	// paths starts with 1 default directory item; focus paths item 0 and pick via p.
	fm.FocusField("paths")
	a.viewMemo.gen++
	d.Keys("p")
	if a.Modal() != "path" {
		t.Fatalf("p on repeatable paths did not open path picker: modal=%q", a.Modal())
	}
	d.Keys("enter") // picks /home/owner/proj from smokeFS
	if a.Modal() != fdSearchModalID || fm.ActiveFieldName() != "paths" || fm.ActiveItemIndex() != 0 {
		t.Fatalf("after picking path 0: modal=%q field=%q itemIdx=%d", a.Modal(), fm.ActiveFieldName(), fm.ActiveItemIndex())
	}
	// Add a second path with +, pick 2nd entry, add a third and remove it with x.
	d.Keys("+")
	if fm.ActiveFieldName() != "paths" || fm.ActiveItemIndex() != 1 {
		t.Fatalf("+ did not add path item 1: field=%q idx=%d", fm.ActiveFieldName(), fm.ActiveItemIndex())
	}
	if a.Mode() == ModeFormEdit {
		d.Keys("esc")
	}
	d.Keys("p", "j", "enter")
	d.Keys("+")
	if a.Mode() == ModeFormEdit {
		d.Keys("esc")
	}
	d.Keys("x")
	gotPaths, _ := fm.Values["paths"].([]string)
	if !slices.Equal(gotPaths, []string{"/home/owner/proj", "/home/owner/notes.md"}) {
		t.Fatalf("paths = %v, want [/home/owner/proj /home/owner/notes.md]", gotPaths)
	}

	// Test repeatable extension field with + and x.
	fm.FocusField("extension")
	a.viewMemo.gen++
	d.Keys("enter") // on [+ Add extension] -> creates item 0 in edit mode
	d.Type("go")
	d.Keys("enter")
	d.Keys("+")
	d.Type("md")
	d.Keys("enter")
	d.Keys("+")
	d.Type("tmp")
	d.Keys("enter")
	d.Keys("x") // remove "tmp"
	gotExt, _ := fm.Values["extension"].([]string)
	if !slices.Equal(gotExt, []string{"go", "md"}) {
		t.Fatalf("extension = %v, want [go md]", gotExt)
	}
	d.Keys("esc", "esc")
}

func TestFDSearchEndToEndWithFakeBinary(t *testing.T) {
	tmp := t.TempDir()
	searchDir := filepath.Join(tmp, "repo")
	subDir := filepath.Join(searchDir, "docs")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	readmePath := filepath.Join(subDir, "README.md")
	if err := os.WriteFile(readmePath, []byte("# Wave P5\n\nHello from fake fd!\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mainGoPath := filepath.Join(searchDir, "main.go")
	if err := os.WriteFile(mainGoPath, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	argsLog := filepath.Join(tmp, "fd_args.log")
	fakeFD := filepath.Join(tmp, "fake-fd")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > " + shellQuoteTest(argsLog) + "\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$arg\" = \"nomatch\" ]; then\n" +
		"    exit 0\n" +
		"  fi\n" +
		"done\n" +
		"printf '%s\\n' " + shellQuoteTest(mainGoPath) + " " + shellQuoteTest(readmePath) + " " + shellQuoteTest(subDir+"/") + "\n"
	if err := os.WriteFile(fakeFD, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	a, d := demo(t)
	a.cfg.userSettings[SetToolsFDBinary] = fakeFD
	a.reloadSettings()
	a.applySettings()

	// 1. Open fd.search, configure pattern, path, extension, hidden, and submit.
	d.Keys("2", "g", "F")
	if a.Modal() != fdSearchModalID {
		t.Fatalf("g F modal = %q, want %q", a.Modal(), fdSearchModalID)
	}
	fm := a.activeForm(fdSearchModalID).Model()
	fm.SetValue("pattern", "wave")
	fm.SetValue("paths", []string{searchDir})
	fm.SetValue("extension", []string{".md", "go"})
	fm.SetValue("hidden", true)
	fm.SetValue("maxResults", 25)
	a.viewMemo.gen++

	d.Keys("ctrl+s")

	// Assert exact argv logged by fake fd script.
	rawArgs, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatalf("read fake fd args log: %v", err)
	}
	gotArgs := strings.Split(strings.TrimSpace(string(rawArgs)), "\n")
	wantArgs := []string{
		"--color=never",
		"-e", "md",
		"-e", "go",
		"-H",
		"--max-results", "25",
		"--search-path", searchDir,
		"--", "wave",
	}
	if !slices.Equal(gotArgs, wantArgs) {
		t.Fatalf("fake fd argv =\n  %q\nwant\n  %q", gotArgs, wantArgs)
	}

	// 2. Results picker opens in Normal Mode (ModePicker) with 3 items.
	if a.Modal() != fdResultsModalID || a.Mode() != ModePicker {
		t.Fatalf("expected fd.results in ModePicker, got modal=%q mode=%q status=%q", a.Modal(), a.Mode(), a.Status())
	}
	pm := a.activePicker(fdResultsModalID).Model()
	if len(pm.Items) != 3 {
		t.Fatalf("expected 3 results, got %d", len(pm.Items))
	}

	// 3. Test j/k navigation in ModePicker.
	d.Keys("j")
	if cur, ok := pm.Focused(); !ok || cur.ID != readmePath {
		t.Fatalf("j did not focus README.md: %+v", cur)
	}
	d.Keys("k")
	if cur, ok := pm.Focused(); !ok || cur.ID != mainGoPath {
		t.Fatalf("k did not return to main.go: %+v", cur)
	}

	// 4. Test / filter (verify typing "readme" does NOT trigger r refine in filter mode!).
	d.Keys("/")
	if a.Mode() != ModePickerFilter {
		t.Fatalf("/ did not enter ModePickerFilter: %q", a.Mode())
	}
	d.Type("readme")
	d.Keys("esc") // back to ModePicker normal mode
	if a.Mode() != ModePicker {
		t.Fatalf("esc from filter did not return to ModePicker: %q", a.Mode())
	}
	if cur, ok := pm.Focused(); !ok || cur.ID != readmePath {
		t.Fatalf("filtered focused item = %+v, want %q", cur, readmePath)
	}

	// 5. Test y yank copies full path and closes picker.
	d.Keys("y")
	if a.Modal() != "" || a.Register('"') != readmePath {
		t.Fatalf("y yank failed: modal=%q reg=%q want=%q", a.Modal(), a.Register('"'), readmePath)
	}

	// 6. Test search.files.again and r refine preserve previous form values.
	a.Shell.State = a.fdSearchAgain(a.State(), core.Command{ID: CmdFDSearchAgain}).State
	a.viewMemo.gen++
	if a.Modal() != fdSearchModalID {
		t.Fatalf("search.files.again did not open fd.search: modal=%q", a.Modal())
	}
	fmAgain := a.activeForm(fdSearchModalID).Model()
	if fmAgain.Values["pattern"] != "wave" || fmAgain.Values["hidden"] != true {
		t.Fatalf("search.files.again did not restore values: %+v", fmAgain.Values)
	}
	d.Keys("ctrl+s")
	if a.Modal() != fdResultsModalID {
		t.Fatalf("re-submit did not open fd.results: modal=%q", a.Modal())
	}

	// Test o (open with) pushes openwith picker, esc returns to fd.results.
	d.Keys("o")
	if a.Modal() != "openwith" {
		t.Fatalf("o in fd.results did not push openwith: modal=%q", a.Modal())
	}
	d.Keys("esc", "esc")
	if a.Modal() != fdResultsModalID {
		t.Fatalf("esc from openwith did not return to fd.results: modal=%q", a.Modal())
	}

	// Test a (attach) attaches result via RelContext to focused task.
	d.Keys("a")
	if a.Modal() != "" || !strings.Contains(a.Status(), "attached") {
		t.Fatalf("a attach failed: modal=%q status=%q", a.Modal(), a.Status())
	}

	// Re-run via search.files.again, press r in fd.results to refine, verify pre-filled values.
	a.Shell.State = a.fdSearchAgain(a.State(), core.Command{ID: CmdFDSearchAgain}).State
	a.viewMemo.gen++
	d.Keys("ctrl+s")
	if a.Modal() != fdResultsModalID {
		t.Fatalf("expected fd.results: modal=%q", a.Modal())
	}
	d.Keys("r")
	if a.Modal() != fdSearchModalID {
		t.Fatalf("r refine did not open fd.search: modal=%q", a.Modal())
	}
	fmRefined := a.activeForm(fdSearchModalID).Model()
	if fmRefined.Values["pattern"] != "wave" || !slices.Equal(fmRefined.Values["extension"].([]string), []string{".md", "go"}) {
		t.Fatalf("r refine did not pre-fill previous values: %+v", fmRefined.Values)
	}

	// 7. Submit again, filter to README.md, press Enter -> opens built-in Markdown reader!
	d.Keys("ctrl+s", "/")
	d.Type("readme")
	d.Keys("enter")
	if a.Modal() != "reader" || a.Mode() != ModeReader {
		t.Fatalf("Enter on README.md did not open markdown reader: modal=%q mode=%q status=%q", a.Modal(), a.Mode(), a.Status())
	}
	if frame := d.Frame(); !strings.Contains(frame, "Hello from fake fd!") {
		t.Fatalf("markdown reader did not display README.md content:\n%s", frame)
	}
	d.Keys("esc", "esc")

	// 8. Verify 0 matches shows info toast and does not push an empty picker.
	d.Keys("g", "F")
	fmZero := a.activeForm(fdSearchModalID).Model()
	fmZero.SetValue("pattern", "nomatch")
	fmZero.SetValue("paths", []string{searchDir})
	a.viewMemo.gen++
	d.Keys("ctrl+s")
	if a.Modal() != "" || !strings.Contains(a.Status(), "no files matched") {
		t.Fatalf("0 matches should not push picker: modal=%q status=%q", a.Modal(), a.Status())
	}

	// Sanity check: attached context relation exists in store.
	if rec, ok := a.subject(a.State(), core.Command{}); ok {
		rels, err := a.Store.Related(rec, data.RelContext, data.Outgoing)
		if err != nil || len(rels) == 0 {
			t.Fatalf("expected RelContext relation attached to %v, got %v (err=%v)", rec, rels, err)
		}
	}
}

func shellQuoteTest(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
