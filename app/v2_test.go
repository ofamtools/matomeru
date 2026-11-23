package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

// configured is a demo app reading and writing a configuration directory.
func configured(t *testing.T, dir string) (*App, *Driver) {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := SeedDemo(store); err != nil {
		t.Fatal(err)
	}
	a, err := New(store, Options{ConfigDir: dir, FS: smokeFS()})
	if err != nil || len(a.Warnings) != 0 {
		t.Fatalf("New: %v, warnings %v", err, a.Warnings)
	}
	return a, NewDriver(a, 120, 36)
}

// The tutorial workspace has every mechanic it teaches.
func TestTutorial(t *testing.T) {
	var out bytes.Buffer
	if err := RunTutorialSmoke(&out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if md := TutorialMarkdown(); strings.ContainsAny(md, "—–") || !strings.Contains(md, "mtm --tutorial") {
		t.Fatal("the tutorial text has a dash it should not, or does not say how to start")
	}
	// docs/TUTORIAL.md is the generated text: regenerate it when the steps change.
	disk, err := os.ReadFile(filepath.Join("..", "docs", "TUTORIAL.md"))
	if err != nil || string(disk) != TutorialMarkdown() {
		t.Fatalf("docs/TUTORIAL.md is not TutorialMarkdown() (err %v): write it again", err)
	}
}

// Every declared setting has a default and a form field; a change applies at
// once, is saved to settings.json, and is there on the next start.
func TestSettingsApplyAndPersist(t *testing.T) {
	want := map[string]any{SetTheme: "ochazuke-dark", SetDensity: "balanced", SetBorderStyle: "rounded", SetShowMetadata: true, SetShowEmpty: true,
		SetFlagsColumn: true, SetMatchMode: "substring", SetEditUniversal: true, SetEditExtra: true, SetLaneBar: true, SetRailAutoWiden: false, SetShowTOC: true,
		SetPanesGroup: PanesGroupProjectPane, SetToast: true, SetTreeGuides: true, SetRowSemanticStyling: true, SetStatusIcons: true, SetTreeTopLevelGap: true,
		SetConfirmQuit: true, SetConfirmDaemonReset: true, SetWorkTasksLayout: WorkTasksLayoutTree, SetWorkHierarchyLayout: WorkHierarchyLayoutAuto,
		SetAutoArchiveExited: true, SetScrollOff: 5, SetPickerDefaultMode: PickerDefaultModeNormal}
	defaults := DefaultSettings()
	specs := SettingSpecs()
	if len(specs) != len(want) {
		t.Fatalf("%d declared settings, want %d", len(specs), len(want))
	}
	for name, def := range want {
		if defaults[name] != def {
			t.Errorf("default of %s = %v, want %v", name, defaults[name], def)
		}
	}
	if !slices.Equal(specs[0].Enum, ThemeNames()) || len(ThemeNames()) < 3 {
		t.Fatalf("ui.theme offers %v, presets are %v", specs[0].Enum, ThemeNames())
	}

	dir := filepath.Join(t.TempDir(), "matomeru-next")
	a, d := configured(t, dir)
	set := func(name string, keys ...string) {
		t.Helper()
		d.Keys("S")
		if a.Modal() != "settings" {
			t.Fatalf("S did not open the settings (modal %q)", a.Modal())
		}
		for i := range specs {
			if specs[i].Name == name {
				break
			}
			d.Keys("j")
		}
		d.Keys(keys...)
		d.Keys("esc")
	}
	d.Keys("3")
	if !strings.Contains(d.Frame(), "Flags") {
		t.Fatal("no flags column to begin with")
	}
	set(SetFlagsColumn, "space")
	if strings.Contains(d.Frame(), "Flags") {
		t.Fatalf("index.flagsColumn off still shows the column:\n%s", d.Frame())
	}
	set(SetBorderStyle, "l")
	d.Keys("3") // two panels: the one without the focus shows the plain frame
	if a.Shell.Theme.BorderStyle != ui.BorderSharp || strings.Contains(d.Frame(), "╭") || !strings.Contains(d.Frame(), "┌") {
		t.Fatalf("border style %q:\n%s", a.Shell.Theme.BorderStyle, d.Frame())
	}
	d.Keys("2")
	set(SetMatchMode, "l")
	if a.nav.tables[workTable].MatchMode != "fuzzy" {
		t.Fatalf("match mode of the open table = %q", a.nav.tables[workTable].MatchMode)
	}
	// Show and Edit follow their settings.
	d.Keys("enter")
	if !strings.Contains(d.Frame(), "Revision") || !strings.Contains(d.Frame(), "Lanes") {
		t.Fatalf("show page before:\n%s", d.Frame())
	}
	set(SetShowMetadata, "space")
	set(SetShowEmpty, "space")
	if frame := d.Frame(); strings.Contains(frame, "Revision") || strings.Contains(frame, "Lanes") || !strings.Contains(frame, "Subtasks") {
		t.Fatalf("show.metadata and show.emptyRelations off:\n%s", frame)
	}
	set(SetEditUniversal, "space")
	d.Keys("e")
	if a.Modal() != "form" || slices.ContainsFunc(a.activeRecordForm().Model().Specs, func(p core.ParamSpec) bool { return views.IsUniversalField(p.Name) }) {
		t.Fatal("edit.showUniversalFields off still offers them")
	}
	d.Keys("esc")
	set(SetRailAutoWiden, "space")
	d.Keys("1")
	if w := a.Shell.State.Layout.Rects["panes.rail"].W; w < 45 {
		t.Fatalf("the focused rail is %d wide, want it widened", w)
	}
	d.Keys("enter")
	if w := a.Shell.State.Layout.Rects["panes.rail"].W; w > 40 {
		t.Fatalf("the rail stays %d wide when it lost the focus", w)
	}

	raw, err := os.ReadFile(filepath.Join(dir, settingsFileName))
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	for name, v := range map[string]any{SetFlagsColumn: false, SetBorderStyle: "sharp", SetMatchMode: "fuzzy", SetShowMetadata: false,
		SetShowEmpty: false, SetEditUniversal: false, SetRailAutoWiden: true} {
		if saved[name] != v {
			t.Errorf("settings.json %s = %v, want %v", name, saved[name], v)
		}
	}
	if len(saved) != 7 {
		t.Errorf("settings.json holds %d keys, want only the 7 that were changed: %v", len(saved), saved)
	}
	again, _ := configured(t, dir)
	if again.Shell.Theme.BorderStyle != ui.BorderSharp || again.cfg.Settings.Bool(SetFlagsColumn, true) || again.matchMode() != "fuzzy" {
		t.Fatalf("the next start: border %q, flags %v, match %q", again.Shell.Theme.BorderStyle, again.cfg.Settings.Bool(SetFlagsColumn, true), again.matchMode())
	}
	// A theme preset brings its own density; a density set after it wins.
	set(SetTheme, "l")
	if a.Shell.Theme.Name != ThemeNames()[1] || a.Shell.Theme.Density != ui.ThemePresets()[1].Density || a.Shell.Theme.BorderStyle != ui.ThemePresets()[1].BorderStyle {
		t.Fatalf("theme %q density %q border %q", a.Shell.Theme.Name, a.Shell.Theme.Density, a.Shell.Theme.BorderStyle)
	}
}

// The binding editor filters, rebinds, unbinds, resets and undoes; each
// change is the live keymap and is saved as overrides the next start reads.
func TestBindingEditor(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "matomeru-next")
	a, d := configured(t, dir)
	task := titled(t, a, data.KindTask, "ochazuke-v2")
	starred := func() bool { rec, _ := a.Store.Get(task.Ref()); return rec.IsStarred() }
	saved := func() []core.Binding {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, bindingsFileName))
		if err != nil {
			t.Fatal(err)
		}
		var rows []core.Binding
		if err := json.Unmarshal(raw, &rows); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	filter := func(q string, want int) {
		t.Helper()
		d.Keys("/", "esc", "/") // esc clears what was typed before
		d.Type(q)
		d.Keys("enter")
		if got := len(a.bind.bindEd.Filtered()); got != want || a.Mode() != ModeBindings {
			t.Fatalf("filter %q shows %d bindings, want %d (mode %s)", q, got, want, a.Mode())
		}
	}
	d.Keys("2", "B")
	if a.Modal() != "bindings" || a.Mode() != ModeBindings || len(a.bind.bindEd.Filtered()) != len(DefaultBindings()) {
		t.Fatalf("B: modal %q mode %s", a.Modal(), a.Mode())
	}
	filter("key:Z verb:record", 2)            // snooze and unsnooze
	filter("mode:terminal-leader key:d", 1)   // detach
	filter("verb:record.star mode:normal", 1) // one binding
	if frame := d.Frame(); !strings.Contains(frame, "same-key-diff-when") {
		t.Fatalf("the conflict note of s is not shown:\n%s", frame)
	}
	filter("conflict:same-key-diff-when key:s verb:record", 2)
	filter("verb:record.star", 1)

	// Rebind to a two-key sequence.
	d.Keys("r", "g", "x", "enter")
	if rows := saved(); len(rows) != 2 || !slices.Equal(rows[0].Keys, []string{"g", "x"}) || rows[0].Command != views.CmdStar ||
		!rows[1].Unbind || !slices.Equal(rows[1].Keys, []string{"s"}) || rows[1].When != rows[0].When {
		t.Fatalf("bindings.json after the rebind = %+v", rows)
	}
	d.Keys("esc", "s")
	if starred() {
		t.Fatal("s still stars after it was moved")
	}
	d.Keys("g", "x")
	if !starred() {
		t.Fatal("g x does not star")
	}
	d.Keys("s") // unstar is another binding and still on s
	if starred() {
		t.Fatal("s no longer unstars")
	}
	// The next start reads the overrides.
	b, bd := configured(t, dir)
	bd.Keys("2", "g", "x")
	if rec, _ := b.Store.Get(titled(t, b, data.KindTask, "ochazuke-v2").Ref()); !rec.IsStarred() || len(b.bind.userBindings) != 2 {
		t.Fatalf("the next start does not have g x (%d user rows)", len(b.bind.userBindings))
	}

	// Reset puts it back; esc during a rebind leaves it alone.
	d.Keys("B")
	filter("verb:record.star", 1)
	d.Keys("r", "Q", "esc")
	if a.Mode() != ModeBindings || len(saved()) != 2 {
		t.Fatalf("esc in a rebind changed something (mode %s)", a.Mode())
	}
	d.Keys("R")
	if rows := saved(); len(rows) != 0 || len(a.bind.userBindings) != 0 {
		t.Fatalf("after reset bindings.json = %+v", rows)
	}
	// Unbind, undo, redo.
	d.Keys("x")
	if rows := saved(); len(rows) != 1 || !rows[0].Unbind || rows[0].Command != views.CmdStar {
		t.Fatalf("after unbind bindings.json = %+v", rows)
	}
	d.Keys("esc", "s")
	if starred() {
		t.Fatal("the unbound s still stars")
	}
	d.Keys("B", "u")
	if len(saved()) != 0 {
		t.Fatal("u did not take the unbind back")
	}
	d.Keys("ctrl+r")
	if len(saved()) != 1 {
		t.Fatal("ctrl+r did not unbind again")
	}
	d.Keys("u", "esc", "s")
	if !starred() || a.Modal() != "" {
		t.Fatal("s does not star after the undo")
	}
	if got := bindingOverrides(DefaultBindings(), DefaultBindings(), 0); len(got) != 0 {
		t.Fatalf("an untouched keymap has %d overrides", len(got))
	}
}

// g n opens any noun's index; n, e and enter work there as on tasks, and the
// edit form writes star, archive and snooze through the store's own verbs.
func TestBrowseAnyNounAndAllProps(t *testing.T) {
	a, d := demo(t)
	d.Keys("g", "n")
	if a.Modal() != "nouns" {
		t.Fatalf("g n: modal %q", a.Modal())
	}
	var kinds []string
	for _, it := range a.activePicker("nouns").Model().Items {
		kinds = append(kinds, it.ID)
	}
	for _, s := range a.Store.Registry().List() {
		if !slices.Contains(kinds, s.Kind) {
			t.Errorf("g n does not list %s", s.Kind)
		}
	}
	if frame := d.Frame(); !strings.Contains(frame, "Lanes") || !strings.Contains(frame, "7") {
		t.Fatalf("no live counts:\n%s", frame)
	}
	// A schema registered later is a noun like any other.
	a.Store.Registry().Register(data.Schema{Kind: "link", Title: "Link", Plural: "Links", SummaryField: "title",
		Fields: []data.FieldSpec{{Name: "title", Type: core.ParamString, Required: true}, {Name: "url", Type: core.ParamString}}})
	d.Keys("esc", "g", "n", "/")
	d.Type("links")
	d.Keys("enter")
	if a.nav.browseKind != "link" || a.Space() != SpaceWork || !strings.Contains(d.Frame(), "Links") {
		t.Fatalf("browsing a custom noun: kind %q\n%s", a.nav.browseKind, d.Frame())
	}
	d.Keys("n", "i")
	d.Type("Ochazuke")
	d.Keys("ctrl+s")
	link, err := a.Store.GetByField("link", "title", "Ochazuke")
	if err != nil || !strings.Contains(d.Frame(), "Ochazuke") {
		t.Fatalf("n did not make a link: %v\n%s", err, d.Frame())
	}
	if _, has := link.Value(data.ColStarStatus); has {
		t.Fatalf("the universal fields were stored as fields: %v", link.Fields)
	}

	// Notes: the edit form has the extra fields a lane.log note carries.
	d.Keys("g", "n", "/")
	d.Type("notes")
	d.Keys("enter", "/")
	d.Type("decision:")
	d.Keys("enter", "e")
	names := func() []string {
		var out []string
		for _, sp := range a.activeRecordForm().Model().Specs {
			out = append(out, sp.Name)
		}
		return out
	}
	if got := strings.Join(names(), " "); got != "title body task starStatus archiveStatus snoozedUntil logKind loggedAt" {
		t.Fatalf("a logged note's form fields = %q", got)
	}
	note := *a.activeRecordForm().ref
	field := func(name string) {
		t.Helper()
		d.Keys("g", "g")
		for _, n := range names() {
			if n == name {
				return
			}
			d.Keys("j")
		}
		t.Fatalf("no field %s", name)
	}
	rec := func() data.Record { r, _ := a.Store.Get(note); return *r }
	field(data.ColStarStatus)
	d.Keys("l")
	field(data.ColSnoozedUntil)
	d.Keys("i")
	d.Type("2099-01-02 03:04")
	d.Keys("ctrl+s")
	want := time.Date(2099, 1, 2, 3, 4, 0, 0, time.Local)
	if r := rec(); !r.IsStarred() || r.SnoozedUntil == nil || !r.SnoozedUntil.Equal(want) {
		t.Fatalf("starred %v, snoozed until %v, status %q", r.IsStarred(), r.SnoozedUntil, a.Status())
	}
	if _, has := rec().Value(data.ColSnoozedUntil); has {
		t.Fatal("snoozedUntil was patched in as a field")
	}
	d.Keys("e")
	field(data.ColSnoozedUntil)
	d.Keys("i")
	d.Type("x")
	d.Keys("ctrl+s")
	if !strings.Contains(a.Status(), "not written") || !rec().SnoozedUntil.Equal(want) || a.Modal() != "form" {
		t.Fatalf("a time that does not parse: status %q, modal %q", a.Status(), a.Modal())
	}
	d.Keys("esc", "e")
	field(data.ColArchiveStatus)
	d.Keys("l", "ctrl+s")
	if !rec().IsArchived() {
		t.Fatal("archiveStatus in the form did not archive")
	}
	d.Keys("u", "u", "u")
	if r := rec(); r.IsArchived() || r.IsStarred() || r.SnoozedUntil != nil {
		t.Fatalf("u did not undo them: archived %v starred %v snoozed %v", r.IsArchived(), r.IsStarred(), r.SnoozedUntil)
	}
	// Back from another noun's index is the task index.
	d.Keys("H")
	if a.nav.browseKind != "" || !strings.Contains(d.Frame(), "ochazuke-v2") {
		t.Fatalf("H from the notes index: kind %q", a.nav.browseKind)
	}
}

// m moves any record to a task, and a task to another task or the root; a
// task is never offered itself or its own subtasks.
func TestAssign(t *testing.T) {
	a, d := demo(t)
	tier := titled(t, a, data.KindTask, "Tier 1: core, ui, tea")
	d.Keys("2", "/")
	d.Type("Tier 1:")
	d.Keys("enter", "m")
	if a.Modal() != "assign" {
		t.Fatalf("m: modal %q, status %q", a.Modal(), a.Status())
	}
	var offered []string
	for _, it := range a.activePicker("assign").Model().Items {
		offered = append(offered, strings.TrimSpace(it.Title))
	}
	if offered[0] != "(None / root)" || slices.Contains(offered, "Tier 1: core, ui, tea") || slices.Contains(offered, "Leader layer falls through to normal") ||
		!slices.Contains(offered, "matomeru-v2") {
		t.Fatalf("m offers %v", offered)
	}
	d.Keys("enter") // (None / root)
	if rec, _ := a.Store.Get(tier.Ref()); rec.Text("parent") != "" {
		t.Fatalf("parent after promoting = %q", rec.Text("parent"))
	}
	d.Keys("u")
	if rec, _ := a.Store.Get(tier.Ref()); rec.Text("parent") == "" {
		t.Fatal("u did not put the task back under its parent")
	}
	// A run: its task changes.
	run := titled(t, a, data.KindRun, "gate: data")
	d.Keys("esc", "3", "g", "g", "m", "/")
	d.Type("ochazuke-v2")
	d.Keys("enter")
	got, _ := a.Store.Get(run.Ref())
	if task, _ := got.RefField("task"); task != titled(t, a, data.KindTask, "ochazuke-v2").Ref() || !strings.Contains(a.Status(), "moved gate: data to ochazuke-v2") {
		t.Fatalf("run task = %v, status %q", task, a.Status())
	}
	// The other nouns the key works on.
	for _, kind := range []string{data.KindPane, data.KindNote, data.KindDocument, data.KindSchedule, data.KindLane} {
		d.Keys("g", "n", "/")
		d.Type(kind)
		d.Keys("enter", "m")
		if pm := a.activePicker("assign"); a.Modal() != "assign" || pm == nil || len(pm.Model().Items) == 0 || pm.Model().Items[0].Subtitle != "no task" {
			t.Fatalf("m on a %s: modal %q picker %v", kind, a.Modal(), pm)
		}
		d.Keys("esc")
	}
}

// W goes to the pane that waits; ctrl+a L opens the lane of the pane, and
// says so when it has none.
func TestAttentionAndLaneKeys(t *testing.T) {
	a, d := demo(t)
	pane := titled(t, a, data.KindPane, "claude: wave 5B")
	d.Keys("2", "W")
	if sel, _ := a.SelectedPane(); a.Space() != SpacePanes || sel == nil || sel.ID != pane.ID || a.Focus() != "q-live-terminal" {
		t.Fatalf("W: space %s focus %s", a.Space(), a.Focus())
	}
	d.Keys("a", "space", "ctrl+s", "h", "h", "j", "j") // answered; past second project header on to the other pane
	other, _ := a.SelectedPane()
	if other == nil || other.ID == pane.ID {
		t.Fatalf("not on the other pane: %v", other)
	}
	d.Keys("ctrl+a", "L")
	if _, showing := a.Showing(); showing || !strings.Contains(a.Status(), "no active lane") {
		t.Fatalf("a pane with no lane: status %q", a.Status())
	}
	d.Keys("W") // the first pane's program is still waiting
	if sel, _ := a.SelectedPane(); sel == nil || sel.ID != pane.ID || a.Focus() != railTable {
		t.Fatalf("W with no open question: focus %s", a.Focus())
	}
	d.Keys("enter", "g", "g", "enter") // the pages, the Screen, into the terminal
	if a.Mode() != ModeTerminal {
		t.Fatalf("mode %s", a.Mode())
	}
	d.Keys("ctrl+a", "L")
	lane, showing := a.Showing()
	if !showing || lane.Kind != data.KindLane || a.Mode() != ModeNormal || a.Space() != SpaceWork {
		t.Fatalf("ctrl+a L from the terminal: showing %v %v in %s/%s", showing, lane, a.Space(), a.Mode())
	}
	if rec, _ := a.Store.Get(lane); rec.Text("title") != "Matomeru v2 Step 2" {
		t.Fatalf("the lane shown is %q, want the root", rec.Text("title"))
	}
}

// The settings and bindings in effect, as JSON and as JSON lines.
func TestExportConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, settingsFileName), []byte(`{"ui.density": "compact"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, bindingsFileName), []byte(`[{"mode":"normal","keys":["X"],"command":"record.star"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := ExportConfig(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	var got ConfigExport
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Settings[SetDensity] != "compact" || got.Settings[SetTheme] != defaultThemeName || len(got.UserSettings) != 1 ||
		len(got.UserBindings) != 1 || len(got.Bindings) != len(DefaultBindings())+1 || !slices.Equal(got.Bindings[0].Keys, []string{"X"}) {
		t.Fatalf("export = %d settings, %d user, %d bindings, %d user bindings", len(got.Settings), len(got.UserSettings), len(got.Bindings), len(got.UserBindings))
	}
	lines, err := ExportConfig(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(strings.TrimSpace(string(lines)), "\n")
	if len(rows) != len(DefaultSettings())+len(DefaultBindings())+1 || !strings.Contains(string(lines), `{"key":"ui.density","value":"compact"}`) {
		t.Fatalf("%d JSON lines:\n%s", len(rows), lines)
	}
	for i, row := range rows {
		if !json.Valid([]byte(row)) {
			t.Fatalf("line %d is not JSON: %s", i+1, row)
		}
	}
}
