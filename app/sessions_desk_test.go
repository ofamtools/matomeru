package app

import (
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/pathpicker"
	"ofam.dev/ochazuke/widgets/picker"
)

type sessionsFixture struct {
	t      *testing.T
	app    *App
	driver *Driver
	apollo data.Record // project of the parser session
	parser data.Record // running claude session with a checklist and a claimed lane
	docs   data.Record // idle codex session in another project
	shell  data.Record // a shell pane: not an AI session
	lane   data.Record
}

func (f *sessionsFixture) mk(kind string, fields map[string]any) data.Record {
	f.t.Helper()
	rec, _, err := f.app.Store.Create(data.CreateInput{Kind: kind, Fields: fields}, User)
	if err != nil {
		f.t.Fatalf("create %s: %v", kind, err)
	}
	return *rec
}

func (f *sessionsFixture) run(id string, args map[string]any) {
	f.t.Helper()
	f.driver.Send(core.CommandEvent{Command: core.Command{ID: id, Args: args, Actor: User}})
}

func (f *sessionsFixture) fact(key string) any {
	f.t.Helper()
	root := f.app.View(f.app.State())
	node := root.Find(views.SessionsDeskPageID)
	if node == nil {
		f.t.Fatal("AI Sessions desk missing from the rendered view")
	}
	return node.Facts[key]
}

func (f *sessionsFixture) delivered(paneID string) string {
	return strings.Join(f.app.queuedPromptsFor(paneID), "\n") + strings.Join(f.app.termOf(paneID).lines, "\n")
}

// selectSession puts the table's selection on paneID with j/k.
func (f *sessionsFixture) selectSession(paneID string) {
	f.t.Helper()
	for _, key := range []string{"j", "k"} {
		for range 6 {
			if f.fact("sessionPaneId") == paneID {
				return
			}
			f.driver.Keys(key)
		}
	}
	f.t.Fatalf("could not select session %s (on %v)", paneID, f.fact("sessionPaneId"))
}

func (f *sessionsFixture) setTelemetry(tel map[string]*daemon.PaneTelemetry) {
	a := f.app
	a.rt.live.telemetryMu.Lock()
	a.rt.live.telemetry = &daemon.TelemetryResult{Telemetry: tel}
	a.rt.live.telemetryMu.Unlock()
	a.rt.live.telemetrySeq.Add(1)
}

func setupSessionsFixture(t *testing.T) *sessionsFixture {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	a, err := New(store, Options{FS: pathpicker.OSFS{}, Now: func() time.Time { return now }, DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	f := &sessionsFixture{t: t, app: a, driver: NewDriver(a, 180, 40)}
	f.apollo = f.mk(data.KindTask, map[string]any{"title": "Apollo", "defaultDir": "/tmp/apollo", "defaultHarness": "claude"})
	borealis := f.mk(data.KindTask, map[string]any{"title": "Borealis"})
	parserTask := f.mk(data.KindTask, map[string]any{"title": "Parser", "parent": f.apollo.Ref()})
	f.parser = f.mk(data.KindPane, map[string]any{"title": "claude: parser", "harness": "claude", "runStatus": "running", "task": parserTask.Ref()})
	f.docs = f.mk(data.KindPane, map[string]any{"title": "codex: docs", "harness": "codex", "runStatus": "idle", "task": borealis.Ref()})
	f.shell = f.mk(data.KindPane, map[string]any{"title": "zsh", "harness": "zsh", "runStatus": "running"})
	f.lane = f.mk(data.KindLane, map[string]any{"title": "Parser lane", "laneStatus": "running", data.FieldQueueName: "bugs"})
	if _, _, err := store.Relate(data.RelateInput{From: f.lane.Ref(), Kind: data.RelClaim, To: f.parser.Ref()}, User); err != nil {
		t.Fatal(err)
	}
	f.setTelemetry(map[string]*daemon.PaneTelemetry{f.parser.ID: {
		PaneID: f.parser.ID, State: daemon.StateRunning,
		Checklist: &data.SessionChecklist{Source: data.ChecklistSourceClaude, Steps: []data.ChecklistStep{
			{Title: "Read grammar", Status: data.StepDone}, {Title: "Write parser", Status: data.StepInProgress},
			{Title: "Test", Status: data.StepPending},
		}},
	}})
	return f
}

func TestSessionsDeskOpensOnGSAndListsEverySession(t *testing.T) {
	f := setupSessionsFixture(t)
	a, d := f.app, f.driver
	topTabs := a.topTabs()

	d.Keys("g", "S")
	if a.Space() != SpaceSessions || a.Focus() != views.SessionsDeskTableID {
		t.Fatalf("g S opened %q, focus %q", a.Space(), a.Focus())
	}
	if !slices.Equal(a.topTabs(), topTabs) {
		t.Fatal("opening the AI Sessions desk changed the numbered top tabs")
	}
	// Sessions of both projects, and no shell pane.
	if f.fact("count") != 2 || f.fact("total") != 2 || f.fact("running") != 1 || f.fact("idle") != 1 || f.fact("needsYou") != 0 {
		t.Fatalf("facts: count %v total %v running %v idle %v needsYou %v",
			f.fact("count"), f.fact("total"), f.fact("running"), f.fact("idle"), f.fact("needsYou"))
	}
	if f.fact("sessionPaneId") != f.parser.ID || f.fact("sessionStatus") != data.SessionRunning {
		t.Fatalf("the running session is not first and selected: %v %v", f.fact("sessionPaneId"), f.fact("sessionStatus"))
	}
	frame := d.Frame()
	for _, want := range []string{"AI Sessions", "1 running", "2 total", "claude: parser", "codex: docs", "Apollo", "Borealis",
		"bugs ▸ Parser lane", "██░░░░ 1/3", "Write parser", "n new session"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("AI Sessions desk is missing %q:\n%s", want, frame)
		}
	}

	// A needs_you hook state moves the session to the front as "needs you".
	f.setTelemetry(map[string]*daemon.PaneTelemetry{f.docs.ID: {PaneID: f.docs.ID, State: daemon.StateNeedsYou}})
	d.Keys("j")
	if f.fact("needsYou") != 1 || a.sessions.visible[0].PaneID != f.docs.ID || !a.sessions.visible[0].NeedsYou {
		t.Fatalf("needs_you did not lead the desk: %+v", a.sessions.visible)
	}

	// The table declares ViewNode.Filter, so / filters it through trait.filter.
	d.Keys("/")
	d.Type("borealis")
	d.Keys("enter")
	if f.fact("hasFilter") != true || f.fact("count") != 1 || f.fact("total") != 2 || f.fact("sessionPaneId") != f.docs.ID {
		t.Fatalf("/borealis: hasFilter %v count %v total %v selected %v", f.fact("hasFilter"), f.fact("count"), f.fact("total"), f.fact("sessionPaneId"))
	}
	d.Keys("/", "ctrl+x", "enter")
	if f.fact("hasFilter") != false || f.fact("count") != 2 {
		t.Fatalf("/ ctrl+x did not clear the filter: count %v", f.fact("count"))
	}

	// b groups and s sorts.
	d.Keys("b")
	if f.fact("groupBy") != data.SessionGroupStatus {
		t.Fatalf("b grouped by %v", f.fact("groupBy"))
	}
	d.Keys("s", "s", "s")
	if f.fact("sortBy") != data.SessionSortTitle || a.sessions.visible[0].PaneID != f.docs.ID {
		t.Fatalf("s s s sorted by %v: %+v", f.fact("sortBy"), a.sessions.visible)
	}
}

func TestSessionsDeskBindingsAndCommandSpecs(t *testing.T) {
	var gS []core.Binding
	for _, b := range DefaultBindings() {
		if b.Mode == ModeNormal && slices.Equal(b.Keys, keys("g S")) {
			gS = append(gS, b)
		}
	}
	if len(gS) != 1 || gS[0].Command != CmdSessionsOpen || gS[0].Scope != "" {
		t.Fatalf("g S bindings = %+v, want exactly the AI Sessions desk", gS)
	}
	ids := map[string]bool{}
	for _, sp := range CommandSpecs() {
		ids[sp.ID] = true
	}
	bound := map[string]string{}
	for _, b := range sessionsBindings() {
		if !ids[b.Command] {
			t.Errorf("binding %v runs %q, which has no command spec", b.Keys, b.Command)
		}
		if b.Scope == views.ScopeSessionsDesk {
			bound[strings.Join(b.Keys, " ")] = b.Command
		}
	}
	for key, cmd := range map[string]string{"c": cmdSessionsCompose, "enter": cmdSessionsCompose, "n": cmdSessionsNew, "o": cmdSessionsOpenPane} {
		if bound[key] != cmd {
			t.Errorf("%s runs %q on the AI Sessions desk, want %q", key, bound[key], cmd)
		}
	}
}

func TestSessionsDeskPromptIsDeliveredWithoutLeavingTheDesk(t *testing.T) {
	f := setupSessionsFixture(t)
	a, d := f.app, f.driver
	d.Keys("g", "S")

	for key, pane := range map[string]data.Record{"c": f.docs, "enter": f.parser} {
		f.selectSession(pane.ID)
		d.Keys(key)
		if a.Modal() != "composer" {
			t.Fatalf("%s did not open the prompt composer (modal %q)", key, a.Modal())
		}
		if a.Space() != SpaceSessions {
			t.Fatalf("%s left the desk for %q", key, a.Space())
		}
		text := "ship it via " + key
		f.run(CmdComposerInsert, map[string]any{"text": text})
		f.run(CmdComposerSubmit, nil)
		if a.Modal() != "" {
			t.Fatalf("the composer stayed open after submit (modal %q)", a.Modal())
		}
		if !strings.Contains(f.delivered(pane.ID), text) {
			t.Fatalf("%q did not reach %s: %q", text, pane.ID, f.delivered(pane.ID))
		}
		if a.Space() != SpaceSessions || a.Focus() != views.SessionsDeskTableID {
			t.Fatalf("submitting moved the owner to %q (focus %q)", a.Space(), a.Focus())
		}
		if f.fact("sessionPaneId") != pane.ID {
			t.Fatalf("submitting moved the selection to %v", f.fact("sessionPaneId"))
		}
	}
	if strings.Contains(f.delivered(f.docs.ID), "via enter") || strings.Contains(f.delivered(f.parser.ID), "via c") {
		t.Fatal("a prompt reached the wrong session")
	}
}

func TestSessionsDeskNewSessionAndOpenTerminal(t *testing.T) {
	f := setupSessionsFixture(t)
	a, d := f.app, f.driver
	d.Keys("g", "S")

	// n opens the shared target picker; picking a live pane selects that
	// session and opens the composer on it, still on the desk.
	f.selectSession(f.parser.ID)
	d.Keys("n")
	pm := a.activePicker(dispatchTargetPickerID)
	if pm == nil || pm.Model().Items[0].ID != dispatchNewAgentItemID {
		t.Fatalf("n did not open the New-Agent-first target picker (modal %q)", a.Modal())
	}
	i := slices.IndexFunc(pm.Model().Items, func(it picker.Item) bool { return it.ID == f.docs.ID })
	if i < 0 {
		t.Fatalf("the target picker does not list the live session: %+v", pm.Model().Items)
	}
	pm.Model().SelectedIdx = i
	d.Keys("enter")
	if a.Modal() != "composer" || a.Space() != SpaceSessions {
		t.Fatalf("picking a session: modal %q on %q, want the composer on the desk", a.Modal(), a.Space())
	}
	f.run(CmdComposerInsert, map[string]any{"text": "hello docs"})
	f.run(CmdComposerSubmit, nil)
	if !strings.Contains(f.delivered(f.docs.ID), "hello docs") || f.fact("sessionPaneId") != f.docs.ID {
		t.Fatalf("the picked session did not get the prompt or the selection: %q, on %v", f.delivered(f.docs.ID), f.fact("sessionPaneId"))
	}

	// o is the one key that leaves for the terminal pane.
	d.Keys("o")
	if a.Space() != SpacePanes {
		t.Fatalf("o opened %q, want the Panes space", a.Space())
	}
	if row, ok := a.rail().FocusedRow(); !ok || row.Ref == nil || row.Ref.ID != f.docs.ID {
		t.Fatalf("o did not select the session's pane in the rail: %+v", row)
	}
}

func TestSessionsDeskChecklistSetting(t *testing.T) {
	f := setupSessionsFixture(t)
	a, d := f.app, f.driver
	d.Keys("g", "S")
	if f.fact("showChecklist") != true || !strings.Contains(d.Frame(), "Progress") {
		t.Fatal("the checklist columns are not shown by default")
	}
	a.cfg.userSettings[SetSessionsDeskChecklist] = false
	a.reloadSettings()
	a.applySettings()
	d.Keys("j") // redraw
	if frame := d.Frame(); f.fact("showChecklist") != false || strings.Contains(frame, "Progress") || strings.Contains(frame, "Write parser") {
		t.Fatalf("sessions.showChecklist=false still shows the checklist:\n%s", frame)
	}
}
