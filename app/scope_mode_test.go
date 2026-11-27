package app

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/modal"
)

type scopeModeFixture struct {
	t        *testing.T
	dir      string
	store    *data.Store
	app      *App
	driver   *Driver
	now      time.Time
	apollo   data.Record
	borealis data.Record
	subA     data.Record
	subB     data.Record
}

func setupScopeModeFixture(t *testing.T) *scopeModeFixture {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// The store stamps records with the real clock, so the fixture clock starts
	// on the real day: "tomorrow" is then never the day the records were made.
	today := time.Now()
	f := &scopeModeFixture{
		t:     t,
		dir:   filepath.Join(t.TempDir(), "matomeru-next"),
		store: store,
		now:   time.Date(today.Year(), today.Month(), today.Day(), 14, 0, 0, 0, time.Local),
	}
	mk := func(kind string, fields map[string]any) data.Record {
		t.Helper()
		rec, _, err := store.Create(data.CreateInput{Kind: kind, Fields: fields}, User)
		if err != nil {
			t.Fatal(err)
		}
		return *rec
	}
	f.apollo = mk(data.KindTask, map[string]any{"title": "Apollo", "workStatus": "todo"})
	f.borealis = mk(data.KindTask, map[string]any{"title": "Borealis", "workStatus": "todo"})
	f.subA = mk(data.KindTask, map[string]any{"title": "Apollo Engine", "parent": f.apollo.Ref(), "workStatus": "active"})
	f.subB = mk(data.KindTask, map[string]any{"title": "Borealis Docs", "parent": f.borealis.Ref(), "workStatus": "todo"})

	f.reopen()
	return f
}

func (f *scopeModeFixture) reopen() {
	f.t.Helper()
	a, err := New(f.store, Options{
		ConfigDir: f.dir,
		Now:       func() time.Time { return f.now },
	})
	if err != nil {
		f.t.Fatal(err)
	}
	f.app = a
	f.driver = NewDriver(a, 120, 36)
}

func (f *scopeModeFixture) mk(kind string, fields map[string]any) data.Record {
	f.t.Helper()
	rec, _, err := f.store.Create(data.CreateInput{Kind: kind, Fields: fields}, User)
	if err != nil {
		f.t.Fatal(err)
	}
	return *rec
}

func (f *scopeModeFixture) setting(name string, value any) {
	f.app.cfg.userSettings[name] = value
	f.app.reloadSettings()
	f.app.applySettings()
}

func TestPinnedProjectPinUnpinToggleAndPersist(t *testing.T) {
	f := setupScopeModeFixture(t)
	a, d := f.app, f.driver

	// Outside a project record (2 Runs), g J opens the project pin picker.
	d.Keys("2")
	d.Keys("g", "J")
	if a.Modal() != projectPinPickerID {
		t.Fatalf("g J outside project modal = %q, want %q", a.Modal(), projectPinPickerID)
	}
	// Filter to Apollo in the picker and confirm.
	if _, ok := a.modals.Top().(*modal.PickerModal); !ok {
		t.Fatalf("top modal is not a PickerModal")
	}
	if a.Mode() != ModePickerFilter {
		d.Keys("/")
	}
	d.Type("Apollo")
	d.Keys("enter")
	if got := a.pinnedProjectID(); got != f.apollo.ID {
		t.Fatalf("pinnedProjectID after picker = %q, want %q", got, f.apollo.ID)
	}

	// Switch to Project Scope mode and verify persistence across restart.
	d.Keys("g", "w")
	if a.activeScopeMode() != ScopeModeProject {
		t.Fatalf("scopeMode after g w = %q, want %q", a.activeScopeMode(), ScopeModeProject)
	}
	f.reopen()
	a, d = f.app, f.driver
	if a.pinnedProjectID() != f.apollo.ID || a.activeScopeMode() != ScopeModeProject {
		t.Fatalf("after restart pinned=%q mode=%q, want (%q, %q)", a.pinnedProjectID(), a.activeScopeMode(), f.apollo.ID, ScopeModeProject)
	}

	// g H jumps directly to the pinned project's Portal tab.
	d.Keys("2")
	d.Keys("g", "H")
	if a.Space() != projectTabID(f.apollo.ID) {
		t.Fatalf("g H landed on space %q, want %q", a.Space(), projectTabID(f.apollo.ID))
	}

	// Inside Project Portal tab, p unpins the active pinned project and falls back to global.
	d.Keys("p")
	if a.pinnedProjectID() != "" || a.activeScopeMode() != ScopeModeGlobal {
		t.Fatalf("after p unpin in portal: pinned=%q mode=%q, want (\"\", global)", a.pinnedProjectID(), a.activeScopeMode())
	}

	// In 1 Projects directory, p pins the selected project and decorates its row.
	d.Keys("1")
	tbl := a.buildProjectsDirTable()
	selectRow(tbl, data.RefText(f.borealis.Ref()))
	d.Keys("p")
	if a.pinnedProjectID() != f.borealis.ID {
		t.Fatalf("p in 1 Projects pinned=%q, want %q", a.pinnedProjectID(), f.borealis.ID)
	}
	tbl = a.buildProjectsDirTable()
	foundPinBadge := false
	for _, row := range tbl.Rows {
		if row.Ref != nil && row.Ref.ID == f.borealis.ID && row.Facts["pinnedProject"] == true {
			foundPinBadge = true
		}
	}
	if !foundPinBadge {
		t.Fatalf("expected pinnedProject fact and badge on Borealis row in 1 Projects")
	}

	// Explicit project.unpin clears pinned project.
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdProjectUnpin, Actor: User}})
	if a.pinnedProjectID() != "" {
		t.Fatalf("after project.unpin pinned=%q, want empty", a.pinnedProjectID())
	}
}

func TestScopeModeCycleToggleAndFacts(t *testing.T) {
	f := setupScopeModeFixture(t)
	a, d := f.app, f.driver

	// Start in 2 Runs with no pinned project: g E cycles global -> today -> global.
	d.Keys("2")
	facts := a.Shell.AppFacts(a.State())
	if facts["scopeMode"] != ScopeModeGlobal || facts["pinnedProject"] != "" || facts["pinnedProjectTitle"] != "" {
		t.Fatalf("initial AppFacts = %+v", facts)
	}

	d.Keys("g", "E")
	if a.activeScopeMode() != ScopeModeToday {
		t.Fatalf("first g E = %q, want today", a.activeScopeMode())
	}
	d.Keys("g", "E")
	if a.activeScopeMode() != ScopeModeGlobal {
		t.Fatalf("second g E (no pinned project) = %q, want global", a.activeScopeMode())
	}

	// Pin Apollo explicitly; now g E cycles global -> today -> project -> global.
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdProjectPin, Args: map[string]any{"project": f.apollo.ID}, Actor: User}})
	d.Keys("g", "E")
	if a.activeScopeMode() != ScopeModeToday {
		t.Fatalf("g E 1 = %q, want today", a.activeScopeMode())
	}
	d.Keys("g", "E")
	if a.activeScopeMode() != ScopeModeProject {
		t.Fatalf("g E 2 = %q, want project", a.activeScopeMode())
	}
	facts = a.Shell.AppFacts(a.State())
	if facts["scopeMode"] != ScopeModeProject || facts["pinnedProject"] != f.apollo.ID || facts["pinnedProjectTitle"] != "Apollo" {
		t.Fatalf("project-mode AppFacts = %+v", facts)
	}
	d.Keys("g", "E")
	if a.activeScopeMode() != ScopeModeGlobal {
		t.Fatalf("g E 3 = %q, want global", a.activeScopeMode())
	}

	// g w toggles directly between global and project modes.
	d.Keys("g", "w")
	if a.activeScopeMode() != ScopeModeProject {
		t.Fatalf("g w into project = %q", a.activeScopeMode())
	}
	d.Keys("g", "w")
	if a.activeScopeMode() != ScopeModeGlobal {
		t.Fatalf("g w back to global = %q", a.activeScopeMode())
	}

	// scope.set sets mode directly.
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdScopeSet, Args: map[string]any{"mode": ScopeModeToday}, Actor: User}})
	if a.activeScopeMode() != ScopeModeToday {
		t.Fatalf("scope.set today = %q", a.activeScopeMode())
	}

	// ui.scopeModeDefault applies when no explicit ScopeMode is persisted.
	a.focus.cfg.ScopeMode = ""
	f.setting(SetScopeModeDefault, ScopeModeToday)
	if a.activeScopeMode() != ScopeModeToday {
		t.Fatalf("scopeModeDefault=today produced %q", a.activeScopeMode())
	}
	f.setting(SetScopeModeDefault, ScopeModeGlobal)
}

func TestScopeModeChromeChipAndOptOut(t *testing.T) {
	f := setupScopeModeFixture(t)
	a, d := f.app, f.driver

	// Default global with nothing pinned -> no chip.
	if chip := a.scopeModeChromeChip(); chip != "" {
		t.Fatalf("default unscoped chip = %q, want empty", chip)
	}

	// Today mode -> ◎ Today.
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdScopeSet, Args: map[string]any{"mode": ScopeModeToday}, Actor: User}})
	if chip := a.scopeModeChromeChip(); !strings.Contains(chip, "◎ Today") {
		t.Fatalf("today chip = %q, want ◎ Today", chip)
	}

	// Pin Apollo & enter project mode -> ⌖ Project: Apollo.
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdProjectPin, Args: map[string]any{"project": f.apollo.ID}, Actor: User}})
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdScopeSet, Args: map[string]any{"mode": ScopeModeProject}, Actor: User}})
	if chip := a.scopeModeChromeChip(); !strings.Contains(chip, "⌖ Project: Apollo") {
		t.Fatalf("project chip = %q, want ⌖ Project: Apollo", chip)
	}
	if frame := d.Frame(); !strings.Contains(frame, "⌖ Project: Apollo") {
		t.Fatalf("expected top chrome to render scope chip:\n%s", frame)
	}

	// Opt-out via ui.scopeModeChrome=false hides the chip cleanly.
	f.setting(SetScopeModeChrome, false)
	if chip := a.scopeModeChromeChip(); chip != "" {
		t.Fatalf("chip with ui.scopeModeChrome=false = %q, want empty", chip)
	}
}

func TestScopeModeOutlineAndWorkScoping(t *testing.T) {
	f := setupScopeModeFixture(t)
	a, d := f.app, f.driver
	enableLegacyPagesForTest(a, SpacePanes) // drives the legacy 2 Work tree/table by digit

	// Move clock to tomorrow so Borealis (todo, created yesterday) is not "today",
	// while subA (workStatus: "active") keeps Apollo in "today" scope!
	f.now = f.now.AddDate(0, 0, 1)

	roots := func() []string {
		nodes, err := a.recordTreeNodes(data.KindTask)
		if err != nil {
			t.Fatal(err)
		}
		var titles []string
		for _, nd := range a.visibleTreeRoots(nodes, nil, data.KindTask) {
			titles = append(titles, nd.Record.Text("title"))
		}
		return titles
	}

	// Global mode shows both Apollo and Borealis.
	if got := roots(); !slices.Equal(got, []string{"Apollo", "Borealis"}) {
		t.Fatalf("global outline roots = %v, want [Apollo Borealis]", got)
	}

	// Today mode (with no focus priority projects) keeps Apollo (has active subtask) and hides stale Borealis.
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdScopeSet, Args: map[string]any{"mode": ScopeModeToday}, Actor: User}})
	if got := roots(); !slices.Equal(got, []string{"Apollo"}) {
		t.Fatalf("today outline roots = %v, want [Apollo]", got)
	}
	d.Keys("T")
	if frame := d.Frame(); !strings.Contains(frame, "◎ Today") || !strings.Contains(frame, "Apollo") || strings.Contains(frame, "Borealis") {
		t.Fatalf("T outline in today mode:\n%s", frame)
	}
	d.Keys("q")

	// Pin Borealis & enter Project mode -> filters Outline and 2 Work (tree + table) to Borealis only.
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdProjectPin, Args: map[string]any{"project": f.borealis.ID}, Actor: User}})
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdScopeSet, Args: map[string]any{"mode": ScopeModeProject}, Actor: User}})
	if got := roots(); !slices.Equal(got, []string{"Borealis"}) {
		t.Fatalf("project outline roots = %v, want [Borealis]", got)
	}
	d.Keys("2")
	d.Keys("g", "t") // switch 2 Work to flat table view
	tbl := a.records(workTable, data.KindTask)
	for _, row := range tbl.Rows {
		if row.Ref != nil && a.Store.RootProjectID(row.Ref.ID) != f.borealis.ID {
			t.Fatalf("2 Work flat table leaked non-pinned task %s in project mode", row.Ref.ID)
		}
	}
	if len(tbl.Rows) != 2 { // Borealis + subB
		t.Fatalf("2 Work flat table row count in project mode = %d, want 2", len(tbl.Rows))
	}
}

func TestScopeModeCapturesQueueAndPagesPicker(t *testing.T) {
	f := setupScopeModeFixture(t)
	a, d := f.app, f.driver

	// Seed captures for Apollo and Borealis.
	capA := f.mk(data.KindCapture, map[string]any{"title": "Apollo capture", "body": "Apollo note", "project": f.apollo.Ref(), "captureStatus": data.CaptureStatusCaptured})
	capB := f.mk(data.KindCapture, map[string]any{"title": "Borealis capture", "body": "Borealis note", "project": f.borealis.Ref(), "captureStatus": data.CaptureStatusCaptured})

	// Seed queued tasks for Apollo and Borealis.
	_, _, err := f.store.Patch(data.PatchInput{Ref: f.subA.Ref(), Fields: map[string]any{"forAgent": true, "agentStatus": data.AgentStatusWorking}}, User)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = f.store.Patch(data.PatchInput{Ref: f.subB.Ref(), Fields: map[string]any{"forAgent": true, "agentStatus": data.AgentStatusQueued}}, User)
	if err != nil {
		t.Fatal(err)
	}

	// Seed panes for Apollo and Borealis.
	paneA := f.mk(data.KindPane, map[string]any{"title": "apollo-agent", "harness": "claude", "task": f.apollo.Ref()})
	paneB := f.mk(data.KindPane, map[string]any{"title": "borealis-agent", "harness": "codex", "task": f.borealis.Ref()})

	// Pin Apollo & enter project mode.
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdProjectPin, Args: map[string]any{"project": f.apollo.ID}, Actor: User}})
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdScopeSet, Args: map[string]any{"mode": ScopeModeProject}, Actor: User}})

	// 1. Captures Review page filters to Apollo capture only.
	d.Keys("g", "L")
	a.syncCaptures()
	if len(a.captures.visible) != 1 || a.captures.visible[0].ID != capA.ID {
		t.Fatalf("captures in project mode = %+v, want only %s (not %s)", a.captures.visible, capA.ID, capB.ID)
	}
	if frame := d.Frame(); !strings.Contains(frame, "⌖ Project: Apollo") {
		t.Fatalf("expected scope label in Captures banner:\n%s", frame)
	}

	// 2. Standalone Queue page filters to Apollo's queued tasks only.
	d.Keys("g", "q")
	a.syncQueue(a.queueScope(a.State()))
	if len(a.queue.visible) != 1 || a.queue.visible[0].ID != f.subA.ID {
		t.Fatalf("queue items in project mode = %+v, want only %s", a.queue.visible, f.subA.ID)
	}

	// 3. Pages Picker (') filters pane items to Apollo's pane only.
	d.Keys("'")
	pm, ok := a.modals.Top().(*modal.PickerModal)
	if !ok {
		t.Fatalf("expected PickerModal for ', got %T", a.modals.Top())
	}
	hasPaneA, hasPaneB := false, false
	for _, it := range pm.Model().Items {
		if it.ID == pagePickScreenPrefix+paneA.ID {
			hasPaneA = true
		}
		if it.ID == pagePickScreenPrefix+paneB.ID {
			hasPaneB = true
		}
	}
	if !hasPaneA || hasPaneB {
		t.Fatalf("Pages picker in project mode: hasPaneA=%v hasPaneB=%v, want true/false", hasPaneA, hasPaneB)
	}
	if !strings.Contains(pm.Model().Title, "⌖ Project: Apollo") {
		t.Fatalf("Pages picker title = %q, want scope indicator", pm.Model().Title)
	}
	d.Keys("esc", "esc")

	// 4. Today mode on Queue (next day): subA is working (today-active), subB is stale queued -> only subA shown.
	f.now = f.now.AddDate(0, 0, 1)
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdScopeSet, Args: map[string]any{"mode": ScopeModeToday}, Actor: User}})
	a.syncQueue(a.queueScope(a.State()))
	if len(a.queue.visible) != 1 || a.queue.visible[0].ID != f.subA.ID {
		t.Fatalf("queue items in today mode = %+v, want only active %s", a.queue.visible, f.subA.ID)
	}
}

func TestScopeModeCreateDefaults(t *testing.T) {
	f := setupScopeModeFixture(t)
	a, d := f.app, f.driver

	// Pin Apollo & enter project mode; navigate to 2 Runs (neutral space with no project subject).
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdProjectPin, Args: map[string]any{"project": f.apollo.ID}, Actor: User}})
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdScopeSet, Args: map[string]any{"mode": ScopeModeProject}, Actor: User}})
	d.Keys("2")

	// 1. New Task inherits pinned project as parent.
	d.Send(core.CommandEvent{Command: core.Command{ID: views.CmdNew, Args: map[string]any{"kind": data.KindTask}, Actor: User}})
	rf := a.activeRecordForm()
	if rf == nil {
		t.Fatalf("expected record form modal for New Task")
	}
	parentRef, ok := asRefValue(rf.Model().Values["parent"])
	if !ok || parentRef != f.apollo.Ref() {
		t.Fatalf("New Task parent default = %v (ok=%v), want %v", parentRef, ok, f.apollo.Ref())
	}
	d.Keys("esc", "esc")

	// 2. New Note inherits pinned project as task.
	d.Send(core.CommandEvent{Command: core.Command{ID: views.CmdNew, Args: map[string]any{"kind": data.KindNote}, Actor: User}})
	rf = a.activeRecordForm()
	if rf == nil {
		t.Fatalf("expected record form modal for New Note")
	}
	taskRef, ok := asRefValue(rf.Model().Values["task"])
	if !ok || taskRef != f.apollo.Ref() {
		t.Fatalf("New Note task default = %v (ok=%v), want %v", taskRef, ok, f.apollo.Ref())
	}
	d.Keys("esc", "esc")

	// 3. New Capture inherits pinned project as project.
	d.Keys("g", "i")
	cm, ok := a.modals.Top().(*captureComposeModal)
	if !ok {
		t.Fatalf("expected captureComposeModal on g i, got %T", a.modals.Top())
	}
	if cm.projectID != f.apollo.ID {
		t.Fatalf("New Capture projectID default = %q, want %q", cm.projectID, f.apollo.ID)
	}
	d.Keys("esc", "esc")

	// 4. Switching back to Global mode stops forcing pinned project on New Task.
	d.Keys("g", "w")
	d.Send(core.CommandEvent{Command: core.Command{ID: views.CmdNew, Args: map[string]any{"kind": data.KindTask}, Actor: User}})
	rf = a.activeRecordForm()
	if rf == nil {
		t.Fatalf("expected record form modal for New Task in global mode")
	}
	if v := rf.Model().Values["parent"]; v != nil && data.Text(v) != "" {
		t.Fatalf("New Task parent in global mode = %v, want empty", v)
	}
}
