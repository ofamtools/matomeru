package app

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/table"
)

func demo(t *testing.T) (*App, *Driver) {
	t.Helper()
	a, err := NewDemo()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Store.Close() })
	return a, NewDriver(a, 120, 36)
}

func titled(t *testing.T, a *App, kind, title string) data.Record {
	t.Helper()
	rec, err := a.Store.GetByField(kind, "title", title)
	if err != nil {
		t.Fatalf("%s %q: %v", kind, title, err)
	}
	return *rec
}

// The whole scripted run: every space, the pane site, the terminal, Show
// pages, forms, undo and the overlays.
func TestSmoke(t *testing.T) {
	var out bytes.Buffer
	if err := RunSmoke(&out, false); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
}

// Seeding twice changes nothing, and the demo has every noun the spaces show.
func TestSeedDemo(t *testing.T) {
	a, _ := demo(t)
	count := func() map[string]int {
		out := map[string]int{}
		for _, s := range a.Store.Registry().List() {
			res, err := a.Store.Query(data.Query{Kind: s.Kind})
			if err != nil {
				t.Fatal(err)
			}
			out[s.Kind] = res.Total
		}
		return out
	}
	before := count()
	for kind, min := range map[string]int{data.KindTask: 10, data.KindPane: 2, data.KindPage: 3, data.KindNote: 3, data.KindDocument: 2,
		data.KindSchedule: 2, data.KindOccurrence: 2, data.KindRun: 3, data.KindExtension: 3} {
		if before[kind] < min {
			t.Errorf("%d %s records, want at least %d", before[kind], kind, min)
		}
	}
	roots, _ := a.Store.Query(data.Query{Kind: data.KindTask, Roots: true})
	if roots.Total != 2 {
		t.Fatalf("%d root tasks, want the 2 projects", roots.Total)
	}
	if err := SeedDemo(a.Store); err != nil {
		t.Fatal(err)
	}
	for kind, n := range count() {
		if n != before[kind] {
			t.Errorf("seeding again changed %s: %d, was %d", kind, n, before[kind])
		}
	}
}

// The rail follows the owner's prompts and nothing else.
func TestRailFollowsOwnerPrompts(t *testing.T) {
	a, d := demoLegacy(t)
	first := titled(t, a, data.KindPane, "claude: wave 5B")
	second := titled(t, a, data.KindPane, "nvim: core/keymap.go")
	order := func() []string {
		var ids []string
		for _, row := range a.rail().Rows {
			ids = append(ids, row.Ref.ID)
		}
		return ids
	}
	if got := order(); got[0] != first.ID || got[1] != second.ID {
		t.Fatalf("rail = %v, want the pane prompted last first", got)
	}
	if _, _, err := a.Store.RecordPaneInput(second.Ref(), core.Actor{Kind: "agent", ID: "claude"}); err != nil {
		t.Fatal(err)
	}
	if got := order(); got[0] != first.ID {
		t.Fatal("an agent's input reordered the rail")
	}
	// The owner types a prompt into the second pane: it moves to the top,
	// and stays the selected pane. (With project-pane grouping, j lands on the
	// second project's header first, then j again selects its pane.)
	d.Keys("j", "j", "enter", "enter")
	if a.Mode() != ModeTerminal {
		t.Fatalf("mode = %s, want terminal", a.Mode())
	}
	d.Type("go on")
	d.Keys("enter", "ctrl+a", "d")
	sel, _ := a.SelectedPane()
	if got := order(); got[0] != second.ID || sel == nil || sel.ID != second.ID || a.Mode() != ModeNormal {
		t.Fatalf("after the owner's prompt: rail %v, selected %v, mode %s", got, sel, a.Mode())
	}
	// A prompt is not something "u" takes back.
	d.Keys("u")
	if now, _ := a.Store.Get(second.Ref()); now.Text("promptedAt") == second.Text("promptedAt") {
		t.Fatal("undo took the prompt time back")
	}
}

// N on a record writes a note about it; the note shows on its Show page.
func TestNewNoteIsAboutTheFocusedRecord(t *testing.T) {
	a, d := demoLegacy(t)
	task := titled(t, a, data.KindTask, "matomeru-v2")
	d.Keys("2", "j", "N")
	if a.Modal() != "form" || !strings.Contains(d.Frame(), "New note") {
		t.Fatalf("N did not open a new note form (modal %q)", a.Modal())
	}
	d.Keys("i")
	d.Type("Ship it")
	d.Keys("ctrl+s")
	note := titled(t, a, data.KindNote, "Ship it")
	about, err := a.Store.Related(note.Ref(), data.RelAbout, data.Outgoing)
	if err != nil || len(about) != 1 || about[0].To != task.Ref() {
		t.Fatalf("about relations = %v, %v", about, err)
	}
	if ref, ok := note.RefField("task"); !ok || ref != task.Ref() {
		t.Fatalf("note.task = %v", ref)
	}
	d.Keys("E")
	root := a.View(a.State())
	if shown, _ := a.Showing(); shown != task.Ref() || root.Find("show.sec.note.task."+data.RefText(note.Ref())) == nil {
		t.Fatalf("Show page of %v does not list the note", shown)
	}
	// One undo removes the relation, the next the note.
	d.Keys("u", "u")
	if _, err := a.Store.Get(note.Ref()); err == nil {
		t.Fatal("two undos left the note")
	}
}

// A path field opens the path picker; changes stage in the form until ctrl+s.
func TestEditPathFieldUsesThePathPicker(t *testing.T) {
	a, d := demoLegacy(t)
	task := titled(t, a, data.KindTask, "ochazuke-v2")
	d.Keys("2", "e", "j", "j", "j", "j", "j", "enter")
	if a.Modal() != "path" || a.activePathPicker() == nil {
		t.Fatalf("enter on defaultDir: modal %q, pathPicker %v", a.Modal(), a.activePathPicker())
	}
	d.Keys("left", "/")
	d.Type("mato")
	d.Keys("tab")
	if got := a.activeRecordForm().Model().Values["defaultDir"]; got != "/home/owner/proj/matomeru-next" || a.Modal() != "form" {
		t.Fatalf("staged defaultDir = %v, modal %q", got, a.Modal())
	}
	// A reference field opens a picker over the records it may point at.
	d.Keys("k", "enter")
	if pm := a.activePicker("ref"); a.Modal() != "ref" || pm == nil || pm.Model().Title != "Pick Parent" {
		t.Fatalf("enter on parent: modal %q, picker %v", a.Modal(), pm)
	}
	d.Keys("/")
	d.Type("matomeru-v2")
	d.Keys("enter")
	if a.Modal() != "form" {
		t.Fatalf("after ref pick: modal %q", a.Modal())
	}
	d.Keys("ctrl+s")
	rec, _ := a.Store.Get(task.Ref())
	parent, _ := rec.RefField("parent")
	if got := rec.Text("defaultDir"); got != "/home/owner/proj/matomeru-next" || a.Modal() != "" {
		t.Fatalf("defaultDir = %q, modal %q", got, a.Modal())
	}
	if want := titled(t, a, data.KindTask, "matomeru-v2").Ref(); parent != want {
		t.Fatalf("parent = %v, want %v", parent, want)
	}
	// A field that does not parse is rejected on submit and not written.
	run := titled(t, a, data.KindRun, "gate: views")
	d.Keys("3", "j", "e", "j", "j", "j", "j", "i")
	d.Type("x")
	d.Keys("ctrl+s")
	if now, _ := a.Store.Get(run.Ref()); now.Text("exitCode") != "1" || a.Modal() != "form" || !strings.Contains(a.Status(), "still needed: exitCode") {
		t.Fatalf("exitCode = %q, modal %q, status %q", now.Text("exitCode"), a.Modal(), a.Status())
	}
}

// A table filters, sorts and groups on any field, shown or not.
func TestListsWorkOnAnyField(t *testing.T) {
	a, d := demoLegacy(t)
	a.cfg.userSettings[SetZeroFieldQueryDefault] = false
	a.reloadSettings()
	a.applySettings()
	d.Keys("2")
	// A record may hold a field no schema declares: it is offered too.
	task := titled(t, a, data.KindTask, "ochazuke-v2")
	if _, _, err := a.Store.Patch(data.PatchInput{Ref: task.Ref(), Fields: map[string]any{"sprint": "s42"}}, User); err != nil {
		t.Fatal(err)
	}
	every := []string{"title", "workStatus", "priority", "parent", "repo", "defaultHarness", "id", "kind", "createdAt", "updatedAt",
		data.ColStarStatus, data.ColArchiveStatus, data.ColSnoozedUntil, "sprint"}
	for _, keys := range [][]string{{"f"}, {"g", "s"}, {"g", "b"}} {
		d.Keys(keys...)
		if a.Modal() != "field" {
			t.Fatalf("%v did not open the field picker (modal %q)", keys, a.Modal())
		}
		var offered []string
		for _, it := range a.activePicker("field").Model().Items {
			offered = append(offered, it.ID)
		}
		for _, field := range every {
			if !slices.Contains(offered, field) {
				t.Errorf("%v does not offer %s (offers %v)", keys, field, offered)
			}
		}
		d.Keys("esc")
	}
	pick := func(field string, keys ...string) {
		t.Helper()
		d.Keys(keys...)
		d.Keys("/")
		d.Type(field)
		d.Keys("enter")
		if a.Modal() != "" {
			t.Fatalf("picking %s left %q open", field, a.Modal())
		}
	}
	pick("repo", "g", "b")
	if frame := d.Frame(); a.nav.tables[workTable].GroupField != "repo" || !strings.Contains(frame, "▾ ochazuke (1)") || !strings.Contains(frame, "▾ matomeru (1)") {
		t.Fatalf("grouped by a column that is not shown (%q):\n%s", a.nav.tables[workTable].GroupField, frame)
	}
	d.Keys("space")
	if !a.nav.tables[workTable].Collapsed["ochazuke"] {
		t.Fatal("space did not fold the focused group")
	}
	d.Keys("g", "b", "enter") // the first row: not grouped
	if a.nav.tables[workTable].GroupField != "" {
		t.Fatalf("still grouped by %q", a.nav.tables[workTable].GroupField)
	}
	pick("sprint", "g", "s")
	if tbl := a.nav.tables[workTable]; tbl.SortField != "sprint" || tbl.SortDir != table.SortAsc {
		t.Fatalf("sorted by %q (%v)", tbl.SortField, tbl.SortDir)
	}
	d.Keys("g", "s", "enter")
	if a.nav.tables[workTable].SortField != "" {
		t.Fatalf("still sorted by %q", a.nav.tables[workTable].SortField)
	}
	// Regex over every field on the flat table; an invalid one is reported and matched as text.
	a.cfg.userSettings[SetWorkTasksLayout] = WorkTasksLayoutTable
	a.reloadSettings()
	d.Keys("/", "tab", "tab")
	d.Type("wave 5[ab]: ")
	if got := len(a.nav.tables[workTable].Filtered()); got != 2 || a.nav.tables[workTable].MatchMode != "regex" {
		t.Fatalf("regex filter matched %d rows in mode %s", got, a.nav.tables[workTable].MatchMode)
	}
	// One field: the regex is anchored to that field's text.
	d.Keys("ctrl+x", "ctrl+t")
	d.Type("^tier [01]")
	if got := len(a.nav.tables[workTable].Filtered()); got != 2 || a.nav.tables[workTable].FilterField != "title" {
		t.Fatalf("regex on %s matched %d rows", a.nav.tables[workTable].FilterField, got)
	}
	d.Keys("ctrl+x")
	d.Type("[")
	if a.nav.tables[workTable].Filtered(); a.nav.tables[workTable].FilterErr == "" || !strings.Contains(d.Frame(), "invalid regex") {
		t.Fatal("an invalid regex is not reported")
	}
	d.Keys("esc")
	pick("sprint", "f")
	d.Keys("/")
	d.Type("s4")
	if tbl := a.nav.tables[workTable]; tbl.FilterField != "sprint" || len(tbl.Filtered()) != 1 {
		t.Fatalf("f filters on %q: %d rows", tbl.FilterField, len(tbl.Filtered()))
	}
	d.Keys("esc")
	// The pane's pages filter too.
	d.Keys("1", "l", "/")
	d.Type("arch")
	d.Keys("enter")
	if frame := d.Frame(); !strings.Contains(frame, "/arch") || strings.Contains(frame, "Questions") || !strings.HasPrefix(a.Focus(), tocPrefix) {
		t.Fatalf("pages filter (focus %s):\n%s", a.Focus(), frame)
	}
}

// The user's JSONC files are laid over the defaults; a broken one is a
// warning, never a reason not to start.
func TestUserConfig(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("settings.json", `{
		// start on Work, snooze for a week
		"startSpace": "work", "snoozeHours": 168,
	}`)
	write("bindings.json", `[
		{"mode": "normal", "keys": ["*"], "command": "record.star", "hint": "star"}, // a new key
		{"mode": "normal", "keys": ["s"], "unbind": true},
	]`)
	write("themes.json", `{"name": "mine", "roles": {"accent": "#ff8800"}}`)
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := SeedDemo(store); err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	a, err := New(store, Options{ConfigDir: dir, FS: smokeFS(), Now: func() time.Time { return fixed }})
	if err != nil || len(a.Warnings) != 0 {
		t.Fatalf("New: %v, warnings %v", err, a.Warnings)
	}
	d := NewDriver(a, 120, 36)
	task := titled(t, a, data.KindTask, "ochazuke-v2")
	if a.Space() != SpaceWork || a.Shell.Theme.Name != "mine" || a.Shell.Theme.Roles["accent"] != "#ff8800" {
		t.Fatalf("space %s, theme %s", a.Space(), a.Shell.Theme.Name)
	}
	d.Keys("s")
	if rec, _ := store.Get(task.Ref()); rec.IsStarred() {
		t.Fatal("the unbound key still stars")
	}
	d.Keys("*", "Z")
	rec, _ := store.Get(task.Ref())
	if !rec.IsStarred() || rec.SnoozedUntil == nil || !rec.SnoozedUntil.Equal(fixed.Add(168*time.Hour)) {
		t.Fatalf("starred %v, snoozed until %v", rec.IsStarred(), rec.SnoozedUntil)
	}

	write("settings.json", `{"startSpace": `)
	write("bindings.json", `[{"mode": "nope", "keys": ["X"], "command": "no.such.command"}]`)
	b, err := New(store, Options{ConfigDir: dir})
	if err != nil || len(b.Warnings) < 2 || b.Space() != SpaceProjects || !strings.Contains(b.Status(), "settings.json") {
		t.Fatalf("broken config: err %v, warnings %v, space %s", err, b.Warnings, b.Space())
	}
}

// Every default binding names a known mode and a registered command.
func TestDefaultBindingsAreValid(t *testing.T) {
	a, _ := demo(t)
	for i, b := range DefaultBindings() {
		if b.Command == "" {
			continue
		}
		if _, ok := a.bind.Keymap.Commands.Lookup(b.Command); !ok {
			t.Errorf("binding %d (%s %v): command %q has no spec", i, b.Mode, b.Keys, b.Command)
		}
	}
	if len(a.Warnings) != 0 {
		t.Fatalf("the default keymap has problems: %v", a.Warnings)
	}
}
