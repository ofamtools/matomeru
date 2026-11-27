package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ptyhost"
	"ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/pathpicker"
)

type captureTestHost struct {
	sessions map[string]bool
	writes   map[string][]string
	writeErr error
}

func (h *captureTestHost) Open(id, _, _ string, _, _ int) error {
	if h.sessions == nil {
		h.sessions = map[string]bool{}
	}
	h.sessions[id] = true
	return nil
}
func (h *captureTestHost) Write(id string, p []byte) error {
	if h.writeErr != nil {
		return h.writeErr
	}
	if h.writes == nil {
		h.writes = map[string][]string{}
	}
	h.writes[id] = append(h.writes[id], string(p))
	return nil
}
func (h *captureTestHost) Resize(string, int, int) error { return nil }
func (h *captureTestHost) Snapshot(id string) (ptyhost.Frame, bool) {
	if !h.sessions[id] {
		return ptyhost.Frame{}, false
	}
	return ptyhost.Frame{ID: id}, true
}
func (h *captureTestHost) Close(id string) error { delete(h.sessions, id); return nil }
func (h *captureTestHost) CloseAll()             { clear(h.sessions) }

type captureFixture struct {
	t      *testing.T
	app    *App
	driver *Driver
	host   *captureTestHost
	now    time.Time
	proj   data.Record
	paneA  data.Record
	paneB  data.Record
}

func setupCaptureFixture(t *testing.T) *captureFixture {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	host := &captureTestHost{sessions: map[string]bool{}, writes: map[string][]string{}}
	f := &captureFixture{
		t:    t,
		host: host,
		now:  time.Date(2026, 11, 27, 15, 0, 0, 0, time.UTC),
	}
	a, err := New(store, Options{
		FS:        pathpicker.OSFS{},
		Now:       func() time.Time { return f.now },
		DataDir:   t.TempDir(),
		ConfigDir: t.TempDir(),
		Host:      host,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.app = a
	f.driver = NewDriver(a, 120, 36)
	f.proj = f.mk(data.KindTask, map[string]any{"title": "Apollo", "defaultDir": "/tmp"})
	f.paneA = f.mk(data.KindPane, map[string]any{"title": "claude-alpha", "harness": "claude", "runStatus": "idle", "task": f.proj.Ref()})
	f.paneB = f.mk(data.KindPane, map[string]any{"title": "codex-beta", "harness": "codex", "runStatus": "idle", "task": f.proj.Ref()})
	host.sessions[f.paneA.ID] = true
	host.sessions[f.paneB.ID] = true
	return f
}

func (f *captureFixture) mk(kind string, fields map[string]any) data.Record {
	f.t.Helper()
	rec, _, err := f.app.Store.Create(data.CreateInput{Kind: kind, Fields: fields}, User)
	if err != nil {
		f.t.Fatalf("create %s: %v", kind, err)
	}
	return *rec
}

func (f *captureFixture) setting(name string, value any) {
	f.app.cfg.userSettings[name] = value
	f.app.reloadSettings()
	f.app.applySettings()
}

func TestCaptureBindingsAndSpecsValid(t *testing.T) {
	diags := config.CheckBindings(Modes(), DefaultBindings(), CommandSpecs()...)
	for _, d := range diags {
		if d.Severity == config.SevError {
			t.Errorf("binding error: %+v", d)
		}
	}
}

func TestCaptureComposeSaveToLogFromAnywhereAndLeaderInTerminal(t *testing.T) {
	f := setupCaptureFixture(t)

	// Open Capture modal from 2 Work via g i.
	f.driver.Keys("2")
	f.driver.Keys("g", "i")
	if f.app.Modal() != captureComposeModalID {
		t.Fatalf("g i modal = %q, want %q", f.app.Modal(), captureComposeModalID)
	}

	// Type multi-line dictation/compose text and press ctrl+s (no pinned agent -> Save to Log).
	f.driver.Type("First dictated line")
	f.driver.Keys("enter")
	f.driver.Type("Second line with details")
	f.driver.Keys("ctrl+s")
	if f.app.Modal() != "" {
		t.Fatalf("expected modal closed after ctrl+s, still %q", f.app.Modal())
	}

	res, err := f.app.Store.Query(data.Query{Kind: data.KindCapture})
	if err != nil || len(res.Items) != 1 {
		t.Fatalf("captures = %d (err=%v), want 1", len(res.Items), err)
	}
	cap1 := res.Items[0]
	if cap1.Text("captureStatus") != data.CaptureStatusCaptured {
		t.Fatalf("captureStatus = %q, want captured", cap1.Text("captureStatus"))
	}
	if cap1.Text("title") != "First dictated line" || !strings.Contains(cap1.Text("body"), "Second line with details") {
		t.Fatalf("unexpected capture title/body: title=%q body=%q", cap1.Text("title"), cap1.Text("body"))
	}

	// Verify leader mirroring inside live ModeTerminal: ctrl+a g i opens Capture modal!
	f.driver.Keys("1")
	f.driver.Keys("enter")
	if f.app.Mode() != ModeTerminal {
		t.Fatalf("mode = %q, want %q", f.app.Mode(), ModeTerminal)
	}
	f.driver.Keys("ctrl+a", "g", "i")
	if f.app.Modal() != captureComposeModalID {
		t.Fatalf("ctrl+a g i in ModeTerminal did not open capture modal (got %q)", f.app.Modal())
	}
	f.driver.Keys("esc")
}

func TestCaptureSendToPinnedSaveOverrideAndPickAgent(t *testing.T) {
	f := setupCaptureFixture(t)

	// Pin paneA.
	f.driver.Send(core.CommandEvent{Command: core.Command{ID: CmdPanePin, Args: map[string]any{"pane": f.paneA.ID}, Actor: User}})

	// 1) Default submit (ctrl+s) sends directly to active pinned agent & logs status=sent.
	f.driver.Keys("g", "i")
	f.driver.Type("Ship the pinned prompt")
	f.driver.Keys("ctrl+s")

	if got := strings.Join(f.host.writes[f.paneA.ID], ""); !strings.Contains(got, "Ship the pinned prompt") {
		t.Fatalf("expected pinned paneA to receive prompt, got %q", got)
	}
	res, err := f.app.Store.Query(data.Query{Kind: data.KindCapture})
	if err != nil || len(res.Items) != 1 {
		t.Fatalf("captures = %d (err=%v), want 1", len(res.Items), err)
	}
	sent := res.Items[0]
	if sent.Text("captureStatus") != data.CaptureStatusSent {
		t.Fatalf("status = %q, want sent", sent.Text("captureStatus"))
	}
	if pRef, ok := sent.RefField("pane"); !ok || pRef.ID != f.paneA.ID {
		t.Fatalf("pane ref = %+v (ok=%v), want %s", pRef, ok, f.paneA.ID)
	}
	if sent.Time("sentAt").IsZero() {
		t.Fatal("expected sentAt to be recorded")
	}

	// 2) alt+s saves to log even when an agent is pinned (without sending).
	writesBefore := len(f.host.writes[f.paneA.ID])
	f.driver.Keys("alt+c")
	if f.app.Modal() != captureComposeModalID {
		t.Fatalf("alt+c modal = %q, want %q", f.app.Modal(), captureComposeModalID)
	}
	f.driver.Type("Save only note")
	f.driver.Keys("alt+s")
	if len(f.host.writes[f.paneA.ID]) != writesBefore {
		t.Fatal("alt+s should save without sending to pinned pane")
	}

	// 3) alt+a opens agent picker, picks paneB, sends & logs status=sent to paneB.
	f.driver.Keys("g", "i")
	f.driver.Type("Send to picked beta")
	f.driver.Keys("alt+a")
	if f.app.Modal() != captureAgentPickerID {
		t.Fatalf("alt+a modal = %q, want %q", f.app.Modal(), captureAgentPickerID)
	}
	// paneA is pinned so it is first; j selects paneB, enter confirms.
	f.driver.Keys("j", "enter")
	if got := strings.Join(f.host.writes[f.paneB.ID], ""); !strings.Contains(got, "Send to picked beta") {
		t.Fatalf("expected picked paneB to receive prompt, got %q", got)
	}

	// 4) Failed delivery preserves capture with status=failed and error text.
	f.host.writeErr = errors.New("pty broken pipe")
	f.driver.Keys("g", "i")
	f.driver.Type("Will fail cleanly")
	f.driver.Keys("alt+g")
	f.host.writeErr = nil

	res, _ = f.app.Store.Query(data.Query{Kind: data.KindCapture})
	foundFailed := false
	for _, rec := range res.Items {
		if rec.Text("title") == "Will fail cleanly" {
			foundFailed = true
			if rec.Text("captureStatus") != data.CaptureStatusFailed || !strings.Contains(rec.Text("error"), "pty broken pipe") {
				t.Fatalf("expected failed capture with error, got status=%q err=%q", rec.Text("captureStatus"), rec.Text("error"))
			}
		}
	}
	if !foundFailed {
		t.Fatal("failed capture record was not saved")
	}
}

func TestCapturesReviewPageNavigationFilterEditResendYankArchive(t *testing.T) {
	f := setupCaptureFixture(t)

	// Create two captures at distinct timestamps.
	f.now = time.Date(2026, 11, 27, 15, 1, 0, 0, time.UTC)
	_, err := f.app.saveCaptureRecord("", "Older idea", data.CaptureStatusCaptured, data.CaptureSourceCompose, nil, f.proj.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	f.now = time.Date(2026, 11, 27, 15, 5, 0, 0, time.UTC)
	_, err = f.app.saveCaptureRecord("", "Newer dictation\nLine two", data.CaptureStatusCaptured, data.CaptureSourceDictation, nil, f.proj.ID, "")
	if err != nil {
		t.Fatal(err)
	}

	// Open Captures Review page via g L.
	f.driver.Keys("g", "L")
	if f.app.Space() != SpaceCaptures {
		t.Fatalf("g L space = %q, want %q", f.app.Space(), SpaceCaptures)
	}
	// Newest-first: first row must be Newer dictation.
	row, ok := f.app.selectedCaptureRow()
	if !ok || row.Title != "Newer dictation" {
		t.Fatalf("first capture = %+v, want Newer dictation", row)
	}

	// j moves to Older idea, k moves back.
	f.driver.Keys("j")
	row, _ = f.app.selectedCaptureRow()
	if row.Title != "Older idea" {
		t.Fatalf("after j selected %q, want Older idea", row.Title)
	}
	f.driver.Keys("k")

	// y yanks full body to clipboard.
	f.driver.Keys("y")
	if !strings.Contains(f.app.Status(), "yanked") {
		t.Fatalf("expected yank confirmation, got status %q", f.app.Status())
	}

	// enter opens edit modal pre-filled, editing updates the existing capture.
	f.driver.Keys("enter")
	if f.app.Modal() != captureComposeModalID {
		t.Fatalf("enter on capture modal = %q, want %q", f.app.Modal(), captureComposeModalID)
	}
	f.driver.Type(" + edited")
	f.driver.Keys("alt+s")
	row, _ = f.app.selectedCaptureRow()
	if !strings.Contains(row.Body, "+ edited") {
		t.Fatalf("expected edited capture body, got %q", row.Body)
	}

	// s with a pinned agent sends/re-sends immediately.
	f.driver.Send(core.CommandEvent{Command: core.Command{ID: CmdPanePin, Args: map[string]any{"pane": f.paneA.ID}, Actor: User}})
	f.driver.Keys("s")
	if got := strings.Join(f.host.writes[f.paneA.ID], ""); !strings.Contains(got, "Newer dictation") {
		t.Fatalf("expected s on review page to send to pinned paneA, got %q", got)
	}

	// S opens pick-agent modal.
	f.driver.Keys("S")
	if f.app.Modal() != captureAgentPickerID {
		t.Fatalf("S on review page modal = %q, want %q", f.app.Modal(), captureAgentPickerID)
	}
	f.driver.Keys("esc", "esc")

	// / filters the Captures table universally.
	f.driver.Keys("/")
	f.driver.Type("Older")
	f.driver.Keys("enter")
	f.app.syncCaptures()
	if len(f.app.captures.visible) != 1 || f.app.captures.visible[0].Title != "Older idea" {
		t.Fatalf("filtered captures = %+v, want only Older idea", f.app.captures.visible)
	}

	// d archives the selected capture.
	f.driver.Keys("d")
	f.app.syncCaptures()
	if len(f.app.captures.visible) != 0 {
		t.Fatalf("expected archived capture hidden from active filtered list, got %d", len(f.app.captures.visible))
	}
}

func TestCaptureComposeOptOutSetting(t *testing.T) {
	f := setupCaptureFixture(t)
	f.setting(SetCaptureComposeEnabled, false)
	f.driver.Keys("g", "i")
	if f.app.Modal() != "" {
		t.Fatalf("expected modal blocked when ui.captureComposeEnabled=false, got %q", f.app.Modal())
	}
	if !strings.Contains(f.app.Status(), "disabled") {
		t.Fatalf("expected disabled status message, got %q", f.app.Status())
	}
}
