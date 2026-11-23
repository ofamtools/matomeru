package app

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ptyhost"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/input"
)

// browseTo opens the Work index of a record's noun and puts the selection
// on the record, as "g n", the noun and j/k would.
func browseTo(t *testing.T, a *App, d *Driver, rec data.Record) {
	t.Helper()
	d.Keys("g", "n")
	d.Type(strings.ToLower(a.schema(rec.Kind).Plural))
	d.Keys("enter")
	if a.Modal() != "" || a.workKind() != rec.Kind {
		t.Fatalf("g n did not open %s (modal %q, noun %q)", rec.Kind, a.Modal(), a.workKind())
	}
	if !selectRow(a.tables[workTable], data.RefText(rec.Ref())) {
		t.Fatalf("%s is not in its index", data.RefText(rec.Ref()))
	}
	a.gen++
}

func mkRec(t *testing.T, a *App, kind string, fields map[string]any) data.Record {
	t.Helper()
	rec, _, err := a.Store.Create(data.CreateInput{Kind: kind, Fields: fields}, User)
	if err != nil {
		t.Fatal(err)
	}
	return *rec
}

func pickerIDs(a *App, modal string) []string {
	var ids []string
	if pm := a.pickers[modal]; pm != nil {
		for _, it := range pm.Items {
			ids = append(ids, it.ID)
		}
	}
	return ids
}

func panesWithRole(t *testing.T, a *App, role string) []data.Record {
	t.Helper()
	res, err := a.Store.Query(data.Query{Kind: data.KindPane, IncludeArchived: true})
	if err != nil {
		t.Fatal(err)
	}
	return slices.DeleteFunc(res.Items, func(r data.Record) bool { return r.Text("role") != role })
}

// y lists every value of the focused record, whatever its noun, each shown
// as it will be copied; y y copies the main one without the picker.
func TestYankPicker(t *testing.T) {
	a, d := demo(t)
	task := titled(t, a, data.KindTask, "ochazuke-v2")
	browseTo(t, a, d, mkRec(t, a, data.KindNote, map[string]any{"title": "placeholder"})) // any noun but tasks, then back
	browseTo(t, a, d, task)

	d.Keys("y")
	if a.Modal() != modalYank || a.Mode() != ModePicker {
		t.Fatalf("y did not open the yank picker (modal %q, mode %q)", a.Modal(), a.Mode())
	}
	ids := pickerIDs(a, modalYank)
	for _, want := range []string{"title", "defaultDir", "repo", "workStatus", "ref", "id", "json", "markdown"} {
		if !slices.Contains(ids, want) {
			t.Errorf("the yank picker of a task has no %q: %v", want, ids)
		}
	}
	if ids[0] != "title" {
		t.Fatalf("the main value is not first: %v", ids)
	}
	if frame := d.Frame(); !strings.Contains(frame, "Yank from ochazuke-v2") || !strings.Contains(frame, "~/proj/ochazuke-next") {
		t.Fatalf("the picker does not show what it copies:\n%s", frame)
	}
	d.Type("repo")
	if it, ok := a.pickers[modalYank].Focused(); !ok || it.ID != "repo" {
		t.Fatalf("typing repo focuses %+v", it)
	}
	// The preview is exactly the text that is copied.
	if !strings.Contains(d.Frame(), "ochazuke") {
		t.Fatalf("no preview:\n%s", d.Frame())
	}
	d.Keys("enter")
	if a.Modal() != "" || a.Register(core.RegUnnamed) != "ochazuke" || a.Register(core.RegYank) != "ochazuke" || a.Register(core.RegClipboard) != "ochazuke" {
		t.Fatalf("picking repo yanked %q / %q / %q (modal %q)", a.Register(core.RegUnnamed), a.Register(core.RegYank), a.Register(core.RegClipboard), a.Modal())
	}
	if a.live.clipboard != "ochazuke" || !slices.ContainsFunc(a.Shell.LastEffects, func(e core.Effect) bool {
		c, ok := e.(core.CustomEffect)
		return ok && c.Kind == effectClipboard && c.Data == "ochazuke"
	}) {
		t.Fatalf("the yank did not go to the clipboard: %+v", a.Shell.LastEffects)
	}

	d.Keys("y", "y")
	if a.Modal() != "" || a.Register(core.RegUnnamed) != "ochazuke-v2" || !strings.Contains(a.Status(), "yanked title") {
		t.Fatalf("y y yanked %q (modal %q, status %q)", a.Register(core.RegUnnamed), a.Modal(), a.Status())
	}
	d.Keys("y")
	d.Type("json")
	d.Keys("enter")
	if text := a.Register(core.RegUnnamed); !strings.Contains(text, `"kind": "task"`) || !strings.Contains(text, task.ID) {
		t.Fatalf("json = %s", text)
	}
	d.Keys("y")
	d.Type("markdown")
	d.Keys("enter")
	if text := a.Register(core.RegUnnamed); !strings.HasPrefix(text, "# ochazuke-v2\n") || !strings.Contains(text, "- repo: ochazuke") {
		t.Fatalf("markdown = %s", text)
	}

	// A pane: the same key, the pane's own values.
	d.Keys("1", "y")
	ids = pickerIDs(a, modalYank)
	for _, want := range []string{"title", "cwd", "pid", "ref"} {
		if !slices.Contains(ids, want) {
			t.Errorf("the yank picker of a pane has no %q: %v", want, ids)
		}
	}
	d.Type("pid")
	d.Keys("enter")
	if a.Register(core.RegUnnamed) != "48211" {
		t.Fatalf("pid = %q", a.Register(core.RegUnnamed))
	}
}

// q and a letter records the keys pressed, @ and the letter replays them
// through the engine, and the special registers follow what the owner does.
func TestRegistersAndMacros(t *testing.T) {
	a, d := demo(t)
	d.Keys("2")
	tbl := func() int { return a.tables[workTable].SelectedIdx }
	start := tbl()
	d.Keys("q", "a")
	if reg, on := a.Macros.Recording(); !on || reg != 'a' || !strings.Contains(a.Status(), "recording @a") {
		t.Fatalf("q a did not start recording (status %q)", a.Status())
	}
	d.Keys("j", "j", "q")
	if _, on := a.Macros.Recording(); on {
		t.Fatal("q did not stop the recording")
	}
	if keys, ok := a.Macros.Macro('a'); !ok || len(keys) != 2 || keys[0].Key != "j" {
		t.Fatalf("macro a = %+v", keys)
	}
	if tbl() != start+2 {
		t.Fatalf("recording did not run the keys: row %d", tbl())
	}
	d.Keys("@", "a")
	if tbl() != start+4 {
		t.Fatalf("@ a moved to row %d, want %d", tbl(), start+4)
	}
	d.Keys("@", "@")
	if tbl() != start+6 {
		t.Fatalf("@ @ moved to row %d, want %d", tbl(), start+6)
	}
	d.Keys("@", "z")
	if !strings.Contains(a.Status(), "no macro in @z") || tbl() != start+6 {
		t.Fatalf("@ z: %q", a.Status())
	}
	// A macro that opens an overlay and types into it does so again.
	d.Keys("q", "b", "/")
	d.Type("tier")
	d.Keys("enter", "q")
	if a.tables[workTable].Query != "tier" || a.Register(core.RegSearch) != "tier" {
		t.Fatalf("filter %q, \"/ = %q", a.tables[workTable].Query, a.Register(core.RegSearch))
	}
	d.Keys("/", "esc")
	if a.tables[workTable].Query != "" {
		t.Fatal("esc did not clear the filter")
	}
	d.Keys("@", "b")
	if a.tables[workTable].Query != "tier" || a.Mode() != ModeNormal {
		t.Fatalf("@ b: filter %q, mode %q", a.tables[workTable].Query, a.Mode())
	}
	d.Keys("/", "esc")

	// "% is the focused record: its reference, or its file when it has one.
	row, _ := a.tables[workTable].FocusedRow()
	if a.Register(core.RegPath) != row.ID || !strings.HasPrefix(row.ID, "task:") {
		t.Fatalf("\"%% = %q, focused %q", a.Register(core.RegPath), row.ID)
	}
	doc := mkRec(t, a, data.KindDocument, map[string]any{"title": "Plan", "filePath": "/docs/plan.md"})
	browseTo(t, a, d, doc)
	d.Keys("j", "k") // any command: the register follows the focus
	browseTo(t, a, d, doc)
	d.Keys("x")
	if a.Register(core.RegPath) != "/docs/plan.md" {
		t.Fatalf("\"%% on a document = %q", a.Register(core.RegPath))
	}
	// ": is the last command run from the palette.
	d.Keys(":")
	d.Type("archived.toggle")
	d.Keys("enter")
	if a.Register(core.RegCommand) != "archived.toggle" || !a.showArchived {
		t.Fatalf("\": = %q", a.Register(core.RegCommand))
	}
	// The outline is based on the normal mode and keeps q for closing: it
	// does not wait for a register.
	d.Keys("T", "q")
	if a.Modal() != "" || len(a.State().Input.Prefix) != 0 {
		t.Fatalf("q in the outline: modal %q, pending %v", a.Modal(), a.State().Input.Prefix)
	}
	// q is no longer quit: Q is.
	if b := a.activeBindings(a.State()); !slices.ContainsFunc(b, func(x activeBinding) bool { return x.keys == "Q" && x.command == core.CmdAppQuit }) {
		t.Fatal("Q does not quit")
	}
}

const guideDoc = "# Guide\n\nRead [the other one](other.md#second-part) or [the site](https://ofam.dev/guide).\n\n" +
	"## Install\n\n```sh\ngo install ofam.dev/matomeru/cmd/mtm@latest\n```\n\n## Use\n\nPress `o`.\n"

// o opens what the focused record points at with the first handler whose
// rule matches; O offers every one that does. The reader has contents,
// links that go back through the handlers, and a code-block yank.
func TestOpenHandlersAndMarkdownReader(t *testing.T) {
	t.Setenv("EDITOR", "vim")
	a, d := demo(t)
	dir := t.TempDir()
	guide := filepath.Join(dir, "guide.md")
	for name, text := range map[string]string{"guide.md": guideDoc, "other.md": "# Other\n\nFirst.\n\n## Second part\n\nSecond.\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a.Handlers.IsDir = func(p string) bool { return p == dir || strings.HasSuffix(p, "-next") }
	doc := mkRec(t, a, data.KindDocument, map[string]any{"title": "Guide", "filePath": guide})
	note := mkRec(t, a, data.KindNote, map[string]any{"title": "Inline", "body": "# Inline note\n\nKept in the record.\n"})

	browseTo(t, a, d, doc)
	d.Keys("o")
	if a.Modal() != modalReader || a.Mode() != ModeReader || a.reader == nil {
		t.Fatalf("o on a document with a .md file did not open the reader (modal %q, status %q)", a.Modal(), a.Status())
	}
	md := a.reader.md
	if len(md.Headings) != 3 || len(md.Links) != 2 || len(md.CodeBlocks) != 1 || !md.ShowTOC {
		t.Fatalf("reader: %d headings, %d links, %d code blocks, toc %v", len(md.Headings), len(md.Links), len(md.CodeBlocks), md.ShowTOC)
	}
	if frame := d.Frame(); !strings.Contains(frame, "Contents") || !strings.Contains(frame, "Install") || !strings.Contains(frame, "guide.md") {
		t.Fatalf("the reader's frame:\n%s", frame)
	}
	d.Keys("t")
	if md.ShowTOC || strings.Contains(d.Frame(), "Contents") {
		t.Fatal("t did not hide the contents")
	}
	d.Keys("t", "]")
	if h, _ := md.CurrentHeading(); h.Text != "Install" {
		t.Fatalf("] went to %q", h.Text)
	}
	d.Keys("y")
	if got := a.Register(core.RegUnnamed); !strings.Contains(got, "go install ofam.dev/matomeru/cmd/mtm@latest") || !strings.Contains(a.Status(), "yanked code block") {
		t.Fatalf("y yanked %q (%q)", got, a.Status())
	}
	// A link to another Markdown file opens in the reader, at its anchor.
	d.Keys("enter")
	if a.reader.md.Title != "other.md" || len(a.reader.back) != 1 {
		t.Fatalf("enter on the first link: title %q, status %q", a.reader.md.Title, a.Status())
	}
	if h, _ := a.reader.md.CurrentHeading(); h.Text != "Second part" {
		t.Fatalf("the anchor went to %q", h.Text)
	}
	d.Keys("backspace")
	if a.reader.md.Title != "guide.md" || len(a.reader.back) != 0 {
		t.Fatalf("backspace: title %q", a.reader.md.Title)
	}
	// A link to a URL goes to the handler of URLs: the system's opener, as a run.
	d.Keys("tab", "enter")
	want := systemOpener() + " https://ofam.dev/guide"
	if run, err := a.Store.GetByField(data.KindRun, "command", want); err != nil || run.Text("title") != "open https://ofam.dev/guide" {
		t.Fatalf("the URL link did not start %q: %v (status %q)", want, err, a.Status())
	}
	d.Keys("q")
	if a.Modal() != "" || a.reader != nil || a.Mode() != ModeNormal {
		t.Fatalf("q did not close the reader (modal %q)", a.Modal())
	}

	// O: every handler that matches, and Copy Path.
	browseTo(t, a, d, doc)
	d.Keys("O")
	if ids := pickerIDs(a, modalOpenWith); a.Modal() != modalOpenWith || len(ids) != 3 || ids[2] != copyPathID {
		t.Fatalf("O offers %v (modal %q)", ids, a.Modal())
	}
	if frame := d.Frame(); !strings.Contains(frame, "markdown-reader") || !strings.Contains(frame, "side-vim") || !strings.Contains(frame, "Copy Path") {
		t.Fatalf("open with:\n%s", frame)
	}
	d.Type("copy")
	d.Keys("enter")
	if a.Register(core.RegUnnamed) != guide || a.Modal() != "" {
		t.Fatalf("Copy Path yanked %q", a.Register(core.RegUnnamed))
	}
	d.Keys("O")
	d.Type("side")
	d.Keys("enter")
	side := panesWithRole(t, a, data.RoleSideVim)
	if len(side) != 1 || side[0].Text("command") != "vim "+guide || side[0].Text("cwd") != dir || a.Space() != SpacePanes || a.Mode() != ModeTerminal {
		t.Fatalf("side-vim: %+v (space %q, mode %q, status %q)", side, a.Space(), a.Mode(), a.Status())
	}
	d.Keys("ctrl+a", "d")

	// A note kept inline opens in the reader too; it has no path to copy.
	browseTo(t, a, d, note)
	d.Keys("O")
	if ids := pickerIDs(a, modalOpenWith); len(ids) != 1 {
		t.Fatalf("O on an inline note offers %v", ids)
	}
	d.Keys("esc", "o")
	if a.Modal() != modalReader || a.reader.md.Title != "Inline" || !strings.Contains(d.Frame(), "Kept in the record.") {
		t.Fatalf("o on a note (modal %q):\n%s", a.Modal(), d.Frame())
	}
	d.Keys("esc")

	// A pane's directory goes to the path picker; a task, with nothing to
	// open, keeps o for sorting.
	d.Keys("1", "h", "h", "o")
	if a.Modal() != modalPath {
		t.Fatalf("o on a pane with a directory: modal %q, status %q", a.Modal(), a.Status())
	}
	d.Keys("esc")
	browseTo(t, a, d, titled(t, a, data.KindTask, "ochazuke-v2"))
	d.Keys("o")
	if a.Modal() != "" || a.tables[workTable].SortField != "title" {
		t.Fatalf("o on a task: modal %q, sort %q", a.Modal(), a.tables[workTable].SortField)
	}
}

// A handlers.json is laid over the default rules.
func TestHandlersConfig(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "matomeru-next")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rules := `[
  // a reader of my own, ahead of the built-in one
  {"name": "glow", "match": "*.md", "command": "glow -p {target}", "priority": 500},
]`
	if err := os.WriteFile(filepath.Join(dir, handlersFileName), []byte(rules), 0o600); err != nil {
		t.Fatal(err)
	}
	a, d := configured(t, dir)
	doc := mkRec(t, a, data.KindDocument, map[string]any{"title": "Guide", "filePath": "/docs/guide.md"})
	d.Keys("2")
	browseTo(t, a, d, doc)
	d.Keys("o")
	pane, err := a.Store.GetByField(data.KindPane, "command", "glow -p /docs/guide.md")
	if err != nil || pane.Text("title") != "glow: guide.md" || pane.Text("cwd") != "/docs" || a.Space() != SpacePanes {
		t.Fatalf("the exec handler did not open a pane: %v (status %q)", err, a.Status())
	}
	if task, has := pane.RefField("task"); has {
		t.Fatalf("a document with no task gave the pane %v", task)
	}

	// The daemon switch is read from settings.json; it has no row in the form.
	if a.Settings.Bool(setTerminalDaemon, false) {
		t.Fatal("terminal.daemon is on by default")
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"terminal.daemon": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if on, err := New(a.Store, Options{ConfigDir: dir}); err != nil || len(on.Warnings) != 0 || !on.Settings.Bool(setTerminalDaemon, false) {
		t.Fatalf("terminal.daemon from settings.json: %v", err)
	}

	// A file that cannot be read is a warning; the default rules still work.
	if err := os.WriteFile(filepath.Join(dir, handlersFileName), []byte(`[{"name": "nameless"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := New(a.Store, Options{ConfigDir: dir})
	if err != nil || len(b.Warnings) != 1 || !strings.Contains(b.Warnings[0], handlersFileName) || len(b.Handlers.Match("x.md")) != 2 {
		t.Fatalf("warnings %v, err %v", b.Warnings, err)
	}
}

// stubHost is a Host that only remembers what was opened and typed.
type stubHost struct {
	mu     sync.Mutex
	opened map[string][]string // id -> cmd, cwd
	typed  map[string]string
	size   map[string][2]int
}

func (h *stubHost) Open(id, cmd, cwd string, cols, rows int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.opened == nil {
		h.opened, h.typed, h.size = map[string][]string{}, map[string]string{}, map[string][2]int{}
	}
	if _, dup := h.opened[id]; dup {
		return errors.New("already open")
	}
	h.opened[id], h.size[id] = []string{cmd, cwd}, [2]int{cols, rows}
	return nil
}

func (h *stubHost) Write(id string, b []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.typed[id] += string(b)
	return nil
}

func (h *stubHost) Resize(id string, cols, rows int) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.size[id] = [2]int{cols, rows}
	return nil
}

func (h *stubHost) Snapshot(id string) (ptyhost.Frame, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.opened[id]; !ok {
		return ptyhost.Frame{}, false
	}
	return ptyhost.Frame{ID: id, Lines: []string{"session " + id + " $ " + h.typed[id]}, Plain: []string{"session " + id},
		Cols: h.size[id][0], Rows: h.size[id][1], PID: 4242, Revision: uint64(1 + len(h.typed[id]))}, true
}

func (h *stubHost) Close(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.opened, id)
	return nil
}

func (h *stubHost) CloseAll() {}

// The two global scratches are pane records made once, shown as an overlay
// over any space and hidden again with the space and its focus untouched.
func TestGlobalScratches(t *testing.T) {
	t.Setenv("EDITOR", "vim")
	host := &stubHost{}
	dataDir := filepath.Join(t.TempDir(), "share", "matomeru-next")
	a, d := liveApp(t, true, Options{Host: host, DataDir: dataDir})
	before := len(a.rail().Rows)
	d.Keys("2", "j", "j")
	focus, row := a.Focus(), a.tables[workTable].SelectedIdx

	d.Keys("ctrl+a", "S")
	if a.Modal() != modalScratch || a.Mode() != ModeTerminal || a.Space() != SpaceWork {
		t.Fatalf("ctrl+a S: modal %q, mode %q, space %q, status %q", a.Modal(), a.Mode(), a.Space(), a.Status())
	}
	terms := panesWithRole(t, a, data.RoleScratchTerm)
	if len(terms) != 1 || terms[0].Text("title") != "Scratch terminal" {
		t.Fatalf("scratch terminal panes: %+v", terms)
	}
	term := terms[0]
	if got := host.opened[term.ID]; len(got) != 2 || got[0] != "" {
		t.Fatalf("the scratch terminal runs %q, want the shell", got)
	}
	if frame := d.Frame(); !strings.Contains(frame, "Scratch terminal") || !strings.Contains(frame, "session "+term.ID) || !strings.Contains(frame, "global scratch") {
		t.Fatalf("the scratch overlay:\n%s", frame)
	}
	d.Type("ls")
	if host.typed[term.ID] != "ls" {
		t.Fatalf("typing went to %+v", host.typed)
	}

	// The other scratch replaces the one shown; its file is there before the editor is.
	d.Keys("ctrl+a", "V")
	vims := panesWithRole(t, a, data.RoleScratchVim)
	file := filepath.Join(dataDir, "scratch.md")
	if a.Modal() != modalScratch || len(vims) != 1 || a.scratch != vims[0].ID || len(a.State().Modals) != 1 {
		t.Fatalf("ctrl+a V: modal %q, scratch %q, vims %d", a.Modal(), a.scratch, len(vims))
	}
	if got := host.opened[vims[0].ID]; len(got) != 2 || got[0] != "vim "+file || got[1] != dataDir || vims[0].Text("filePath") != file {
		t.Fatalf("the scratch editor runs %q on %q", got, vims[0].Text("filePath"))
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("the managed scratch file was not made: %v", err)
	}
	d.Keys("ctrl+a", "V")
	if a.Modal() != "" || a.Space() != SpaceWork || a.Focus() != focus || a.tables[workTable].SelectedIdx != row || a.Mode() != ModeNormal {
		t.Fatalf("hiding the scratch lost the place: modal %q, space %q, focus %q, row %d, mode %q", a.Modal(), a.Space(), a.Focus(), a.tables[workTable].SelectedIdx, a.Mode())
	}

	// Asked for again, from another space: the same pane and the same session.
	d.Keys("3", "ctrl+a", "S")
	if again := panesWithRole(t, a, data.RoleScratchTerm); len(again) != 1 || a.scratch != term.ID || a.Space() != SpaceRuns {
		t.Fatalf("a second scratch terminal was made: %d (shown %q)", len(again), a.scratch)
	}
	if !strings.Contains(d.Frame(), "$ ls") {
		t.Fatalf("the session did not survive hiding:\n%s", d.Frame())
	}
	d.Keys("ctrl+a", "d")
	if a.Modal() != "" || a.Space() != SpaceRuns || a.Mode() != ModeNormal {
		t.Fatalf("ctrl+a d: modal %q, space %q, mode %q", a.Modal(), a.Space(), a.Mode())
	}
	// They are ordinary panes: two more rows in the rail, no new noun.
	if got := len(a.rail().Rows); got != before+2 {
		t.Fatalf("the rail has %d panes, want %d", got, before+2)
	}
	if _, ok := a.Store.Registry().Get("scratch"); ok {
		t.Fatal("a scratch noun was registered")
	}

	// The per-pane side panes say what they are and which pane they are beside.
	d.Keys("1")
	selectRow(a.rail(), data.RefText(titled(t, a, data.KindPane, "claude: wave 5B").Ref()))
	a.gen++
	parent, _ := a.SelectedPane()
	d.Keys("ctrl+a", "t")
	sideTerm := panesWithRole(t, a, data.RoleSideTerm)
	if len(sideTerm) != 1 || a.Mode() != ModeTerminal {
		t.Fatalf("ctrl+a t: %d side terminals, mode %q, status %q", len(sideTerm), a.Mode(), a.Status())
	}
	if ref, _ := sideTerm[0].RefField("parentPane"); ref != parent.Ref() || sideTerm[0].Text("cwd") != parent.Text("cwd") {
		t.Fatalf("side terminal: parentPane %v, cwd %q", ref, sideTerm[0].Text("cwd"))
	}
	if task, _ := sideTerm[0].RefField("task"); task.ID == "" {
		t.Fatal("the side terminal lost its pane's task")
	}
	d.Keys("ctrl+a", "v")
	sideVim := panesWithRole(t, a, data.RoleSideVim)
	if len(sideVim) != 1 || sideVim[0].Text("command") == "" {
		t.Fatalf("ctrl+a v: %+v", sideVim)
	}
	if ref, _ := sideVim[0].RefField("parentPane"); ref != sideTerm[0].Ref() {
		t.Fatalf("side editor: parentPane %v", ref)
	}
}

// Without a live terminal a scratch still opens: what is typed is kept.
func TestScratchWithoutHost(t *testing.T) {
	a, d := demo(t)
	d.Keys("ctrl+a", "S")
	if a.Modal() != modalScratch || a.Mode() != ModeTerminal {
		t.Fatalf("modal %q, mode %q", a.Modal(), a.Mode())
	}
	d.Type("ls")
	d.Keys("enter")
	if !slices.Contains(a.termOf(a.scratch).lines, "❯ ls") || !strings.Contains(d.Frame(), "❯ ls") {
		t.Fatalf("typed lines %v", a.termOf(a.scratch).lines)
	}
	d.Keys("ctrl+a", "S")
	if a.Modal() != "" || a.Space() != SpacePanes {
		t.Fatalf("modal %q, space %q", a.Modal(), a.Space())
	}
}

// R on a signal drafts a rule from it; sending the form makes the rule and
// applies every rule to what is still unreviewed.
func TestRuleFromSignal(t *testing.T) {
	a, d := demo(t)
	var bots []data.Record
	for _, title := range []string{"Bump a", "Bump b", "Bump c"} {
		bots = append(bots, mkRec(t, a, data.KindSignal, map[string]any{"title": title, "source": "github", "author": "dependabot[bot]", "eventKind": "pull_request"}))
	}
	human := mkRec(t, a, data.KindSignal, map[string]any{"title": "Can you look?", "source": "github", "author": "kmo"})
	browseTo(t, a, d, bots[1])
	if !strings.Contains(d.Frame(), "R triage.rule.from-signal") {
		t.Fatalf("the footer does not offer R on a signal:\n%s", d.Frame())
	}
	d.Keys("R")
	if a.Modal() != modalLaunch || a.launch == nil || len(a.launch.fm.Specs) != 3 {
		t.Fatalf("R did not open the three-line form (modal %q, status %q)", a.Modal(), a.Status())
	}
	fm := a.launch.fm
	if fm.Values["when"] != `source == "github" && author == "dependabot[bot]"` || fm.Values["action"] != data.ActionMarkNoise {
		t.Fatalf("the draft: %+v", fm.Values)
	}
	if frame := d.Frame(); !strings.Contains(frame, "Rule from Bump b") || !strings.Contains(frame, "dependabot[bot]") {
		t.Fatalf("the form:\n%s", frame)
	}
	d.Keys("ctrl+s")
	if a.Modal() != "" || !strings.Contains(a.Status(), "3 of 4 unreviewed signals cleaned up") {
		t.Fatalf("after ctrl+s: modal %q, status %q", a.Modal(), a.Status())
	}
	rules, err := a.Store.Query(data.Query{Kind: data.KindTriageRule})
	if err != nil || rules.Total != 1 || rules.Items[0].Text("title") != "Noise: github dependabot[bot]" || !rules.Items[0].Bool("enabled") {
		t.Fatalf("rules: %+v, %v", rules.Items, err)
	}
	for _, sig := range bots {
		got, _ := a.Store.Get(sig.Ref())
		if got.Text("triageState") != data.TriageNoise || !got.IsArchived() {
			t.Fatalf("%s was not cleaned up", sig.Text("title"))
		}
	}
	if got, _ := a.Store.Get(human.Ref()); got.Text("triageState") != data.TriageUnreviewed || got.IsArchived() {
		t.Fatal("a signal the rule does not hold for was changed")
	}
	if rows := a.tables[workTable].Filtered(); len(rows) != 1 || !strings.Contains(d.Frame(), "Can you look?") || strings.Contains(d.Frame(), "Bump a") {
		t.Fatalf("the index still lists the noise (%d rows):\n%s", len(rows), d.Frame())
	}
	// R anywhere else does nothing of the kind.
	d.Keys("R")
	if a.Modal() != modalLaunch {
		t.Fatalf("R on the remaining signal: modal %q", a.Modal())
	}
	d.Keys("esc")
	browseTo(t, a, d, titled(t, a, data.KindTask, "ochazuke-v2"))
	d.Keys("R")
	if a.Modal() != "" {
		t.Fatalf("R on a task opened %q", a.Modal())
	}
}

// The actions and the modes are tables to look at, from the palette and from
// g n, and an unbound action is bound to a free key from there.
func TestInspectActionsAndModes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "matomeru-next")
	a, d := configured(t, dir)
	d.Keys("2", ":")
	d.Type("inspect actions")
	d.Keys("enter")
	if a.Modal() != modalInspect || a.Mode() != ModeInspect || a.inspecting != InspectActions {
		t.Fatalf(": inspect actions: modal %q, mode %q", a.Modal(), a.Mode())
	}
	if frame := d.Frame(); !strings.Contains(frame, "Inspect actions") || !strings.Contains(frame, unboundBadge) {
		t.Fatalf("the actions inspector:\n%s", frame)
	}
	if a.Register(core.RegCommand) != "inspect actions" {
		t.Fatalf("\": = %q", a.Register(core.RegCommand))
	}
	pm := a.pickers[modalInspect]
	if len(pm.Items) != len(ActionsOf(a.bindings, a.specs)) {
		t.Fatalf("%d rows for %d actions", len(pm.Items), len(ActionsOf(a.bindings, a.specs)))
	}
	item := func(id string) (found bool, unbound bool, keys string) {
		for _, it := range a.pickers[modalInspect].Items {
			if it.ID == id {
				return true, it.Badges[0].Text == unboundBadge, it.Badges[0].Text
			}
		}
		return false, false, ""
	}
	if found, unbound, keys := item("record.star"); !found || unbound || keys != "s" {
		t.Fatalf("record.star: found %v, unbound %v, keys %q", found, unbound, keys)
	}
	if found, unbound, _ := item(CmdGroupField); !found || !unbound {
		t.Fatalf("%s should start unbound", CmdGroupField)
	}
	d.Keys("j", "j", "/")
	if a.Mode() != ModePicker {
		t.Fatalf("/ in the inspector: mode %q", a.Mode())
	}
	d.Type(CmdGroupField)
	d.Keys("enter")
	if focused, _ := pm.Focused(); a.Mode() != ModeInspect || a.Modal() != modalInspect || focused.ID != CmdGroupField {
		t.Fatalf("the filter: mode %q, focused %q", a.Mode(), focused.ID)
	}
	d.Keys("a")
	if len(a.userBindings) != 1 || a.userBindings[0].Command != CmdGroupField || !strings.Contains(a.Status(), "bound "+CmdGroupField+" to ") {
		t.Fatalf("a did not bind it: %+v (%q)", a.userBindings, a.Status())
	}
	keys := a.userBindings[0].Keys
	if _, unbound, shown := item(CmdGroupField); unbound || shown != strings.Join(keys, " ") {
		t.Fatalf("the row still reads %q after binding to %v", shown, keys)
	}
	saved, err := os.ReadFile(filepath.Join(dir, bindingsFileName))
	if err != nil || !strings.Contains(string(saved), CmdGroupField) {
		t.Fatalf("bindings.json: %v\n%s", err, saved)
	}
	// The key works at once, and is there on the next start.
	d.Keys("esc")
	d.Keys(keys...)
	if a.Modal() != "" || a.tables[workTable].GroupField == "" {
		t.Fatalf("%v did not group the list (status %q)", keys, a.Status())
	}
	if b, err := New(a.Store, Options{ConfigDir: dir}); err != nil || len(b.Warnings) != 0 || len(b.userBindings) != 1 || !slices.Equal(b.userBindings[0].Keys, keys) {
		t.Fatalf("the next start: %v", err)
	}

	// Modes, from the noun browser.
	d.Keys("g", "n")
	d.Type("modes")
	d.Keys("enter")
	if a.Modal() != modalInspect || a.inspecting != InspectModes || len(a.pickers[modalInspect].Items) != len(Modes()) {
		t.Fatalf("g n modes: modal %q, inspecting %q", a.Modal(), a.inspecting)
	}
	if frame := d.Frame(); !strings.Contains(frame, "Inspect modes") || !strings.Contains(frame, "passthrough") || !strings.Contains(frame, "base normal") {
		t.Fatalf("the modes inspector:\n%s", frame)
	}
	before := len(a.userBindings)
	d.Keys("a", "q")
	if len(a.userBindings) != before || a.Modal() != "" {
		t.Fatal("a bound something in the modes inspector, or q did not close it")
	}
	d.Keys("g", "n")
	d.Type("actions")
	d.Keys("enter")
	if a.Modal() != modalInspect || a.inspecting != InspectActions {
		t.Fatalf("g n actions: modal %q, inspecting %q", a.Modal(), a.inspecting)
	}

	modes := ModesOf(Modes(), DefaultBindings())
	byName := map[string]ModeInfo{}
	for _, m := range modes {
		byName[m.Name] = m
	}
	if !byName[ModeTerminal].Passthrough || byName[ModeOutline].Base != ModeNormal || byName[ModeNormal].Bindings < 50 || byName[ModeReader].Bindings == 0 {
		t.Fatalf("modes = %+v", modes)
	}
	for _, what := range InspectKinds {
		v, err := Inspect(what, "")
		if err != nil || !strings.Contains(InspectText(v), "\n") {
			t.Fatalf("Inspect(%s): %v", what, err)
		}
	}
	if _, err := Inspect("nope", ""); err == nil {
		t.Fatal("Inspect of something unknown succeeded")
	}
}

func TestShowEmptySectionNewAndBreadcrumbs(t *testing.T) {
	a, d := demo(t)
	task := mkRec(t, a, data.KindTask, map[string]any{"title": "schedule target"})
	sub := mkRec(t, a, data.KindTask, map[string]any{"title": "child step", "parent": task.Ref()})

	// Open the task's Show page via the Work index.
	browseTo(t, a, d, task)
	d.Keys("enter")
	if a.Focus() != showHeader {
		t.Fatalf("focus = %q, want %q", a.Focus(), showHeader)
	}
	// Step down to the empty Schedules section.
	for i := 0; i < 20 && a.Focus() != "show.sec.schedule.task"; i++ {
		d.Keys("j")
	}
	if a.Focus() != "show.sec.schedule.task" {
		t.Fatalf("could not focus empty Schedules section (landed on %q)", a.Focus())
	}
	// enter on an empty section explains that n adds.
	d.Keys("enter")
	if !strings.Contains(a.Status(), "press n to add") {
		t.Fatalf("enter on empty section status = %q", a.Status())
	}
	// n opens the New Schedule form pre-filled with task = this task.
	d.Keys("n")
	if a.Modal() != modalForm || a.form == nil || a.form.kind != data.KindSchedule {
		t.Fatalf("n on empty Schedules: modal=%q form=%+v", a.Modal(), a.form)
	}
	if got, ok := data.AsRef(a.form.fm.TypedValues()["task"]); !ok || got != task.Ref() {
		t.Fatalf("pre-filled schedule task = %v, want %v", a.form.fm.TypedValues()["task"], task.Ref())
	}
	d.Keys("esc")

	// Step down to empty Directories and press n -> opens pathpicker for this task.
	for i := 0; i < 20 && a.Focus() != "show.sec.rel.directory.out"; i++ {
		d.Keys("j")
	}
	if a.Focus() != "show.sec.rel.directory.out" {
		t.Fatalf("could not focus empty Directories section (landed on %q)", a.Focus())
	}
	d.Keys("n")
	if a.Modal() != modalPath || a.pathTarget == nil || *a.pathTarget != task.Ref() {
		t.Fatalf("n on empty Directories: modal=%q target=%v", a.Modal(), a.pathTarget)
	}
	d.Keys("esc")

	// Bouncing back and forth builds history > 4; breadcrumb collapses middle with ellipsis while H still walks back.
	d.Keys("g", "g") // back to show.header
	for i := 0; i < 2; i++ {
		// Jump into subtask card, then back up via parent link card.
		for j := 0; j < 10 && a.Focus() != "show.sec.task.parent."+data.RefText(sub.Ref()); j++ {
			d.Keys("j")
		}
		d.Keys("enter")
		for j := 0; j < 10 && a.Focus() != "show.field.parent"; j++ {
			d.Keys("j")
		}
		d.Keys("enter")
	}
	frame := d.Frame()
	if !strings.Contains(frame, "Tasks › … › child step › schedule target") {
		t.Fatalf("expected collapsed breadcrumb in frame:\n%s", frame)
	}
	d.Keys("H")
	if cur, ok := a.Showing(); !ok || cur != sub.Ref() {
		t.Fatalf("H after collapsed breadcrumb went to %v, want %v", cur, sub.Ref())
	}
}

func TestRecordActionsPicker(t *testing.T) {
	a, d := demo(t)
	task := mkRec(t, a, data.KindTask, map[string]any{"title": "focus action target"})
	browseTo(t, a, d, task)

	// ; opens a picker scoped strictly to commands for the focused task (no global graph commands).
	d.Keys(";")
	if a.Modal() != modalRecordActions || a.Mode() != ModePicker {
		t.Fatalf("; did not open record actions picker (modal %q, mode %q)", a.Modal(), a.Mode())
	}
	ids := pickerIDs(a, modalRecordActions)
	for _, want := range []string{views.CmdOpen, views.CmdEdit, views.CmdStar, views.CmdArchive, views.CmdSnooze, CmdAssign, CmdYankPick, CmdPathOpen} {
		if !slices.Contains(ids, want) {
			t.Errorf("record actions on task missing %q: %v", want, ids)
		}
	}
	// Opposite state commands, other nouns' commands, and global graph commands must NOT appear.
	for _, forbid := range []string{views.CmdUnstar, views.CmdRestore, views.CmdUnsnooze, CmdOpenTarget, CmdRunRerun, CmdTriageRule, CmdSettingsOpen, CmdBindingsOpen, CmdOutlineOpen, core.CmdAppQuit, core.CmdPageOpen} {
		if slices.Contains(ids, forbid) {
			t.Errorf("record actions on unstarred task unexpectedly included %q: %v", forbid, ids)
		}
	}
	// Picking record.star runs it on the focused task.
	d.Type("record.star")
	d.Keys("enter")
	rec, err := a.Store.Get(task.Ref())
	if err != nil || !rec.IsStarred() {
		t.Fatalf("record.star from record actions picker did not star task (err=%v, starred=%v)", err, rec != nil && rec.IsStarred())
	}
	// Re-opening ; now offers record.unstar instead of record.star.
	d.Keys(";")
	ids = pickerIDs(a, modalRecordActions)
	if !slices.Contains(ids, views.CmdUnstar) || slices.Contains(ids, views.CmdStar) {
		t.Fatalf("after starring, record actions = %v", ids)
	}
	d.Keys("esc")

	// On a signal, triage.rule.from-signal appears; on a doc with filePath, open.target appears.
	sig := mkRec(t, a, data.KindSignal, map[string]any{"title": "ci green", "source": "github"})
	browseTo(t, a, d, sig)
	d.Keys(";")
	if ids := pickerIDs(a, modalRecordActions); !slices.Contains(ids, CmdTriageRule) || slices.Contains(ids, CmdRunRerun) {
		t.Fatalf("signal record actions = %v", ids)
	}
	d.Keys("esc")

	doc := mkRec(t, a, data.KindDocument, map[string]any{"title": "Spec", "filePath": "/tmp/spec.md"})
	browseTo(t, a, d, doc)
	d.Keys(";")
	if ids := pickerIDs(a, modalRecordActions); !slices.Contains(ids, CmdOpenTarget) || !slices.Contains(ids, CmdOpenWith) {
		t.Fatalf("openable document record actions = %v", ids)
	}
	d.Keys("esc")
}

func TestReadlineBindingsAcrossInputs(t *testing.T) {
	a, d := demo(t)
	task := mkRec(t, a, data.KindTask, map[string]any{"title": "alpha beta gamma"})
	browseTo(t, a, d, task)

	// 1. Form edit mode: ctrl+a, alt+f, ctrl+k, ctrl+w, ctrl+y.
	d.Keys("e", "i")
	if a.Mode() != ModeFormEdit {
		t.Fatalf("mode = %q, want %q", a.Mode(), ModeFormEdit)
	}
	d.Keys("ctrl+w") // kills "gamma"
	if got := a.form.fm.Values["title"]; got != "alpha beta " {
		t.Fatalf("form ctrl+w -> %q", got)
	}
	d.Keys("ctrl+a")
	d.Type("top ")
	if got := a.form.fm.Values["title"]; got != "top alpha beta " {
		t.Fatalf("form ctrl+a + type -> %q", got)
	}
	d.Keys("ctrl+e", "ctrl+y", "ctrl+s")
	rec, err := a.Store.Get(task.Ref())
	if err != nil || rec.Text("title") != "top alpha beta gamma" {
		t.Fatalf("form commit after readline = %q (err=%v)", rec.Text("title"), err)
	}

	// 2. Table filter mode: ctrl+w, ctrl+a, ctrl+u, ctrl+y.
	d.Keys("/")
	d.Type("one two")
	d.Keys("ctrl+w")
	if got := a.tables[workTable].Query; got != "one " {
		t.Fatalf("filter ctrl+w -> %q", got)
	}
	d.Keys("ctrl+u")
	if got := a.tables[workTable].Query; got != "" {
		t.Fatalf("filter ctrl+u -> %q", got)
	}
	d.Keys("ctrl+y")
	if got := a.tables[workTable].Query; got != "one " {
		t.Fatalf("filter ctrl+y -> %q", got)
	}
	d.Keys("esc")

	// 3. Picker mode: ctrl+w / ctrl+a / ctrl+y edit query while ctrl+n moves selection.
	d.Keys(":")
	d.Type("record edit")
	d.Keys("ctrl+w")
	if got := a.pickers[modalPalette].Query; got != "record " {
		t.Fatalf("picker ctrl+w -> %q", got)
	}
	beforeSel := a.pickers[modalPalette].SelectedIdx
	d.Keys("ctrl+n")
	if a.pickers[modalPalette].SelectedIdx == beforeSel {
		t.Fatal("ctrl+n did not move selection in picker")
	}
	d.Keys("esc")

	// 4. Binding editor filter mode: ctrl+w works on filter query.
	d.Keys("B", "/")
	d.Type("verb:record.star extra")
	d.Keys("ctrl+w")
	if got := a.bindLine.Value(); got != "verb:record.star " {
		t.Fatalf("bindings filter ctrl+w -> %q", got)
	}
	if len(a.bindEd.Filtered()) != 1 {
		t.Fatalf("bindings filter did not apply query %q (got %d rows)", a.bindLine.Value(), len(a.bindEd.Filtered()))
	}
	d.Keys("esc", "esc")

	// 5. All Readline actions appear in the actions inspector with bound keys.
	actions := ActionsOf(a.bindings, a.specs)
	byID := map[string]ActionInfo{}
	for _, act := range actions {
		byID[act.ID] = act
	}
	for _, want := range []string{input.CmdLineStart, input.CmdLineEnd, input.CmdKillWordBack, input.CmdKillLineForward, input.CmdYankKill} {
		act, ok := byID[want]
		if !ok || !act.Bound {
			t.Fatalf("readline action %q in inspector: ok=%v act=%+v", want, ok, act)
		}
	}
}
