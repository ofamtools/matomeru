package app

import (
	"fmt"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/pathpicker"
)

type pickerTestFS struct {
	dirs     map[string][]pathpicker.DirEntry
	previews map[string]string
	home     string
}

func (f pickerTestFS) ReadDir(dir string) ([]pathpicker.DirEntry, error) {
	es, ok := f.dirs[dir]
	if !ok {
		return nil, fmt.Errorf("%s: no such directory", dir)
	}
	return es, nil
}

func (f pickerTestFS) ReadPreview(path string, _ int) (string, error) {
	if s, ok := f.previews[path]; ok {
		return s, nil
	}
	return "", nil
}

func (f pickerTestFS) HomeDir() (string, error) { return f.home, nil }

func newPickerTestApp(t *testing.T, fs pathpicker.FS) (*App, *Driver) {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := SeedDemo(store); err != nil {
		t.Fatal(err)
	}
	a, err := New(store, Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a, NewDriver(a, 120, 36)
}

func TestPickerNormalModeNavigationAndFilterTransitions(t *testing.T) {
	a, d := demo(t)

	// Standard picker (g n) starts in ModePicker (Normal Mode).
	d.Keys("g", "n")
	if a.Modal() != "nouns" || a.Mode() != ModePicker {
		t.Fatalf("g n: modal=%q mode=%q, want nouns/%s", a.Modal(), a.Mode(), ModePicker)
	}
	pm := a.activePicker("nouns").Model()
	if pm.Filtering {
		t.Fatal("picker should not start in filtering state under default normal mode")
	}
	total := len(pm.Filtered())
	if total < 4 {
		t.Fatalf("expected at least 4 nouns in picker, got %d", total)
	}

	// j / k / G / g g navigate in Normal Mode without modifying query.
	d.Keys("j", "j")
	if pm.SelectedIdx != 2 || pm.Query != "" {
		t.Fatalf("after j j: idx=%d query=%q, want idx=2 query=\"\"", pm.SelectedIdx, pm.Query)
	}
	d.Keys("k")
	if pm.SelectedIdx != 1 {
		t.Fatalf("after k: idx=%d, want 1", pm.SelectedIdx)
	}
	d.Keys("G")
	if pm.SelectedIdx != total-1 {
		t.Fatalf("after G: idx=%d, want %d", pm.SelectedIdx, total-1)
	}
	d.Keys("g", "g")
	if pm.SelectedIdx != 0 {
		t.Fatalf("after g g: idx=%d, want 0", pm.SelectedIdx)
	}

	// q closes immediately from ModePicker.
	d.Keys("q")
	if a.Modal() != "" || a.Mode() != ModeNormal {
		t.Fatalf("q in ModePicker did not close: modal=%q mode=%q", a.Modal(), a.Mode())
	}

	// esc closes immediately from ModePicker.
	d.Keys("g", "n", "esc")
	if a.Modal() != "" || a.Mode() != ModeNormal {
		t.Fatalf("esc in ModePicker did not close: modal=%q mode=%q", a.Modal(), a.Mode())
	}

	// `/` enters ModePickerFilter, typing filters, first esc returns to ModePicker preserving query,
	// second esc closes the picker.
	d.Keys("g", "n", "/")
	if a.Mode() != ModePickerFilter {
		t.Fatalf("/ did not enter ModePickerFilter: mode=%q", a.Mode())
	}
	pm = a.activePicker("nouns").Model()
	if !pm.Filtering {
		t.Fatal("pm.Filtering should be true in ModePickerFilter")
	}
	d.Type("task")
	if pm.Query != "task" {
		t.Fatalf("query = %q, want \"task\"", pm.Query)
	}
	d.Keys("esc")
	if a.Modal() != "nouns" || a.Mode() != ModePicker || pm.Filtering || pm.Query != "task" {
		t.Fatalf("first esc from filter: modal=%q mode=%q filtering=%v query=%q", a.Modal(), a.Mode(), pm.Filtering, pm.Query)
	}
	d.Keys("esc")
	if a.Modal() != "" || a.Mode() != ModeNormal {
		t.Fatalf("second esc did not close picker: modal=%q mode=%q", a.Modal(), a.Mode())
	}

	// `i` also enters ModePickerFilter.
	d.Keys("g", "n", "i")
	if a.Modal() != "nouns" || a.Mode() != ModePickerFilter {
		t.Fatalf("i did not enter ModePickerFilter: modal=%q mode=%q", a.Modal(), a.Mode())
	}
	d.Type("lanes")
	d.Keys("enter")
	if a.Modal() != "" || a.workKind() != data.KindLane {
		t.Fatalf("i lanes enter: modal=%q workKind=%q", a.Modal(), a.workKind())
	}
}

func TestPathPickerNormalModeOverhaul(t *testing.T) {
	fs := pickerTestFS{
		home: "/home/owner",
		dirs: map[string][]pathpicker.DirEntry{
			"/":     {{Name: "home", Path: "/home", IsDir: true}},
			"/home": {{Name: "owner", Path: "/home/owner", IsDir: true}},
			"/home/owner": {
				{Name: ".config", Path: "/home/owner/.config", IsDir: true},
				{Name: "proj", Path: "/home/owner/proj", IsDir: true},
				{Name: "notes.md", Path: "/home/owner/notes.md", Size: 42},
			},
			"/home/owner/proj": {
				{Name: ".env", Path: "/home/owner/proj/.env", Size: 16},
				{Name: "matomeru-next", Path: "/home/owner/proj/matomeru-next", IsDir: true},
				{Name: "ochazuke-next", Path: "/home/owner/proj/ochazuke-next", IsDir: true},
			},
			"/home/owner/proj/ochazuke-next": {
				{Name: "go.mod", Path: "/home/owner/proj/ochazuke-next/go.mod", Size: 128},
			},
			"/home/owner/proj/matomeru-next": {
				{Name: "go.mod", Path: "/home/owner/proj/matomeru-next/go.mod", Size: 256},
			},
		},
		previews: map[string]string{
			"/home/owner/proj/ochazuke-next/go.mod": "module ofam.dev/ochazuke\n",
			"/home/owner/proj/matomeru-next/go.mod": "module ofam.dev/matomeru\n",
		},
	}
	a, d := newPickerTestApp(t, fs)
	task := titled(t, a, data.KindTask, "ochazuke-v2")

	// Open path picker on ochazuke-v2 (defaultDir ~/proj/ochazuke-next).
	d.Keys("2", "ctrl+o")
	ppMod := a.activePathPicker()
	if a.Modal() != "path" || a.Mode() != ModePicker || ppMod == nil {
		t.Fatalf("ctrl+o: modal=%q mode=%q pp=%v", a.Modal(), a.Mode(), ppMod)
	}
	pp := ppMod.Model()
	if pp.Dir != "/home/owner/proj/ochazuke-next" {
		t.Fatalf("initial dir = %q, want /home/owner/proj/ochazuke-next", pp.Dir)
	}

	// `p` toggles path preview on and renders file preview text.
	if pp.ShowPreview {
		t.Fatal("preview should start off")
	}
	d.Keys("p")
	if !pp.ShowPreview || !strings.Contains(d.Frame(), "module ofam.dev/ochazuke") {
		t.Fatalf("p did not enable preview: showPreview=%v\n%s", pp.ShowPreview, d.Frame())
	}
	d.Keys("p")
	if pp.ShowPreview {
		t.Fatal("second p did not disable preview")
	}

	// `h` moves to parent directory (/home/owner/proj).
	d.Keys("h")
	if pp.Dir != "/home/owner/proj" {
		t.Fatalf("h dir = %q, want /home/owner/proj", pp.Dir)
	}

	// `.` toggles hidden files (.env appears).
	if pp.ShowHidden {
		t.Fatal("hidden should start off")
	}
	d.Keys(".")
	if !pp.ShowHidden || !strings.Contains(d.Frame(), ".env") {
		t.Fatalf(". did not show hidden files: showHidden=%v\n%s", pp.ShowHidden, d.Frame())
	}
	d.Keys(".")
	if pp.ShowHidden {
		t.Fatal("second . did not hide dotfiles")
	}

	// `-` moves to parent directory (/home/owner).
	d.Keys("-")
	if pp.Dir != "/home/owner" {
		t.Fatalf("- dir = %q, want /home/owner", pp.Dir)
	}

	// `l` enters highlighted directory (proj).
	d.Keys("l")
	if pp.Dir != "/home/owner/proj" {
		t.Fatalf("l dir = %q, want /home/owner/proj", pp.Dir)
	}

	// `~` jumps to home directory (/home/owner).
	d.Keys("~")
	if pp.Dir != "/home/owner" {
		t.Fatalf("~ dir = %q, want /home/owner", pp.Dir)
	}

	// `l` into proj, `/` filters to mato, `esc` returns to ModePicker, `enter` chooses path!
	d.Keys("l", "/")
	if a.Mode() != ModePickerFilter {
		t.Fatalf("/ in path picker: mode=%q, want %s", a.Mode(), ModePickerFilter)
	}
	d.Type("mato")
	d.Keys("esc")
	if a.Mode() != ModePicker || pp.Query != "mato" {
		t.Fatalf("esc from path filter: mode=%q query=%q", a.Mode(), pp.Query)
	}
	d.Keys("enter")
	if a.Modal() != "" {
		t.Fatalf("enter in path ModePicker did not submit: modal=%q", a.Modal())
	}
	dirs, err := a.Store.Related(task.Ref(), data.RelDirectory, data.Outgoing)
	if err != nil || len(dirs) != 1 || dirs[0].To != (core.Ref{Kind: "directory", ID: "/home/owner/proj/matomeru-next"}) {
		t.Fatalf("related dirs after enter = %+v (err=%v)", dirs, err)
	}

	// Undo, reopen, and verify `Tab` submits directly from ModePicker too.
	d.Keys("u", "ctrl+o", "h", "tab")
	if a.Modal() != "" {
		t.Fatalf("tab in path ModePicker did not submit: modal=%q", a.Modal())
	}
	dirs, _ = a.Store.Related(task.Ref(), data.RelDirectory, data.Outgoing)
	if len(dirs) != 1 {
		t.Fatalf("expected 1 related directory after tab submit, got %+v", dirs)
	}
}

func TestPickerDefaultModeSetting(t *testing.T) {
	a, d := demo(t)

	// 1. Default ("normal"): standard pickers start in ModePicker, Palette starts in ModePickerFilter.
	if got := a.cfg.Settings.String(SetPickerDefaultMode, ""); got != PickerDefaultModeNormal {
		t.Fatalf("default %s = %q, want %q", SetPickerDefaultMode, got, PickerDefaultModeNormal)
	}
	d.Keys("g", "n")
	if a.Mode() != ModePicker {
		t.Fatalf("normal: g n mode = %q, want %q", a.Mode(), ModePicker)
	}
	d.Keys("esc", ":")
	if a.Mode() != ModePickerFilter {
		t.Fatalf("normal: palette mode = %q, want %q", a.Mode(), ModePickerFilter)
	}
	d.Keys("esc", "esc")

	// 2. "all-normal": BOTH standard pickers and Palette start in ModePicker.
	a.cfg.userSettings[SetPickerDefaultMode] = PickerDefaultModeAllNormal
	a.reloadSettings()
	d.Keys("g", "n")
	if a.Mode() != ModePicker {
		t.Fatalf("all-normal: g n mode = %q, want %q", a.Mode(), ModePicker)
	}
	d.Keys("esc", ":")
	if a.Mode() != ModePicker {
		t.Fatalf("all-normal: palette mode = %q, want %q", a.Mode(), ModePicker)
	}
	// Single esc closes palette when already in ModePicker.
	d.Keys("esc")
	if a.Modal() != "" {
		t.Fatalf("all-normal: single esc did not close palette: modal=%q", a.Modal())
	}

	// 3. "filter": ALL pickers (standard, path, palette) start in ModePickerFilter.
	a.cfg.userSettings[SetPickerDefaultMode] = PickerDefaultModeFilter
	a.reloadSettings()
	d.Keys("g", "n")
	if a.Mode() != ModePickerFilter {
		t.Fatalf("filter: g n mode = %q, want %q", a.Mode(), ModePickerFilter)
	}
	d.Type("lanes")
	d.Keys("enter")
	if a.Modal() != "" || a.workKind() != data.KindLane {
		t.Fatalf("filter: direct typing in g n failed: modal=%q kind=%q", a.Modal(), a.workKind())
	}
	d.Keys("ctrl+o")
	if a.Modal() != "path" || a.Mode() != ModePickerFilter {
		t.Fatalf("filter: ctrl+o mode = %q, want %q", a.Mode(), ModePickerFilter)
	}
	d.Keys("esc", "esc")
	if a.Modal() != "" {
		t.Fatalf("filter: two escs did not close path picker: modal=%q", a.Modal())
	}
	d.Keys(":")
	if a.Mode() != ModePickerFilter {
		t.Fatalf("filter: palette mode = %q, want %q", a.Mode(), ModePickerFilter)
	}
	d.Keys("esc", "esc")
}

func TestPickerModeBindingsCheckZeroErrors(t *testing.T) {
	rep, err := CheckBindings("")
	if err != nil {
		t.Fatalf("CheckBindings error: %v", err)
	}
	if len(rep.Errors) != 0 {
		t.Fatalf("CheckBindings(\"\") reported %d errors: %+v", len(rep.Errors), rep.Errors)
	}
}
