package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/recorder"
	"ofam.dev/ochazuke/widgets/toast"
)

func newRecorderTestApp(t *testing.T, settings map[string]any, dataDir string) (*App, *Driver) {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := SeedDemo(store); err != nil {
		t.Fatalf("seed demo: %v", err)
	}
	cfgDir := filepath.Join(t.TempDir(), "config")
	if len(settings) > 0 {
		if err := os.MkdirAll(cfgDir, 0o755); err != nil {
			t.Fatalf("mkdir config: %v", err)
		}
		raw, err := json.Marshal(settings)
		if err != nil {
			t.Fatalf("marshal settings: %v", err)
		}
		if err := os.WriteFile(filepath.Join(cfgDir, settingsFileName), raw, 0o600); err != nil {
			t.Fatalf("write settings.json: %v", err)
		}
	}
	if dataDir == "" {
		dataDir = filepath.Join(t.TempDir(), "data")
	}
	now := time.Date(2026, 11, 23, 21, 0, 0, 0, time.UTC)
	a, err := New(store, Options{
		ConfigDir: cfgDir,
		DataDir:   dataDir,
		FS:        smokeFS(),
		Now: func() time.Time {
			now = now.Add(10 * time.Millisecond)
			return now
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	enableLegacyPagesForTest(a, SpacePanes)
	d := NewDriver(a, 120, 40)
	return a, d
}

func readSavedRecording(t *testing.T, path string) (recorder.Header, []recorder.Event) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open recording %s: %v", path, err)
	}
	defer f.Close()
	hdr, events, err := recorder.ReadJSONL(f)
	if err != nil {
		t.Fatalf("ReadJSONL(%s): %v", path, err)
	}
	return hdr, events
}

func TestSessionRecorderSettingsAndLiveToggle(t *testing.T) {
	// Default: enabled on startup and buffers events.
	a, d := newRecorderTestApp(t, nil, "")
	if a.Shell.Recorder == nil || !a.Shell.Recorder.Enabled() {
		t.Fatalf("expected recorder enabled by default, got %+v", a.Shell.Recorder)
	}
	if a.cfg.Settings.Bool(SetSessionPromptSaveOnExit, true) {
		t.Fatalf("expected %s default false", SetSessionPromptSaveOnExit)
	}
	before := a.Shell.Recorder.Len()
	d.Keys("2")
	if a.Shell.Recorder.Len() <= before {
		t.Fatalf("expected key event to buffer: before=%d after=%d", before, a.Shell.Recorder.Len())
	}

	// Live toggle via Settings modal immediately disables and re-enables recorder.
	specs := SettingSpecs()
	toggleInSettings := func() {
		t.Helper()
		d.Keys("S")
		if a.Modal() != "settings" {
			t.Fatalf("expected settings modal, got %q", a.Modal())
		}
		for i := range specs {
			if specs[i].Name == SetSessionRecord {
				break
			}
			d.Keys("j")
		}
		d.Keys("space", "esc", "esc")
	}

	toggleInSettings()
	if a.Shell.Recorder.Enabled() {
		t.Fatal("flipping session.record off in Settings did not disable recorder")
	}
	nOff := a.Shell.Recorder.Len()
	d.Keys("3", "1")
	if got := a.Shell.Recorder.Len(); got != nOff {
		t.Fatalf("disabled recorder buffered events: before=%d after=%d", nOff, got)
	}

	toggleInSettings()
	if !a.Shell.Recorder.Enabled() {
		t.Fatal("flipping session.record on in Settings did not re-enable recorder")
	}
	nOn := a.Shell.Recorder.Len()
	d.Keys("2")
	if got := a.Shell.Recorder.Len(); got <= nOn {
		t.Fatalf("re-enabled recorder did not buffer events: before=%d after=%d", nOn, got)
	}

	// Configured off at startup via settings.json: stays disabled from the start.
	aOff, dOff := newRecorderTestApp(t, map[string]any{SetSessionRecord: false}, "")
	if aOff.Shell.Recorder == nil {
		t.Fatal("expected non-nil recorder even when disabled at startup")
	}
	if aOff.Shell.Recorder.Enabled() {
		t.Fatal("expected recorder disabled when session.record=false in settings.json")
	}
	dOff.Keys("2", "3", "1")
	if got := aOff.Shell.Recorder.Len(); got != 0 {
		t.Fatalf("expected 0 buffered events when disabled at startup, got %d", got)
	}
}

func TestSessionRecorderExcludesWake(t *testing.T) {
	a, d := newRecorderTestApp(t, nil, "")
	a.Shell.Recorder.Clear()

	for i := 0; i < 5; i++ {
		d.Send(core.CommandEvent{Command: core.Command{ID: CmdWake, Source: "watcher"}})
	}
	d.Keys("2")

	_, events := a.Shell.Recorder.Snapshot()
	var sawKey, sawPageOpen bool
	for _, ev := range events {
		if ev.Kind == recorder.KindCommand && ev.Command == CmdWake {
			t.Fatalf("unexpected %s event in recording: %+v", CmdWake, ev)
		}
		if ev.Kind == recorder.KindKey && ev.Key == "2" {
			sawKey = true
		}
		if ev.Kind == recorder.KindCommand && ev.Command == core.CmdPageOpen {
			sawPageOpen = true
		}
	}
	if !sawKey || !sawPageOpen {
		t.Fatalf("expected normal key+command events recorded (sawKey=%v sawPageOpen=%v); events=%+v", sawKey, sawPageOpen, events)
	}
}

func TestSessionRecorderVerbs(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "mtm-data")
	a, d := newRecorderTestApp(t, nil, dataDir)
	d.Keys("2", "3")

	// 1. session.record.status
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdSessionRecordStatus, Source: "palette", Actor: User}})
	st := a.Status()
	if !strings.Contains(st, "on") || !strings.Contains(st, "/4096 events") || !strings.Contains(st, "dropped)") {
		t.Fatalf("unexpected status output: %q", st)
	}
	activeToasts := a.rt.toasts.Active()
	if len(activeToasts) == 0 || activeToasts[len(activeToasts)-1].Level != toast.Info {
		t.Fatalf("expected info toast from status, got %+v", activeToasts)
	}

	// 2. session.record.save (default dir under DataDir/recordings)
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdSessionRecordSave, Source: "palette", Actor: User}})
	defaultDir := filepath.Join(dataDir, "recordings")
	matches, err := filepath.Glob(filepath.Join(defaultDir, "matomeru-*.jsonl"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected 1 recording in %s, got %v (err=%v, status=%q)", defaultDir, matches, err, a.Status())
	}
	if !strings.Contains(a.Status(), matches[0]) {
		t.Fatalf("expected status to contain saved path %q, got %q", matches[0], a.Status())
	}
	hdr, events := readSavedRecording(t, matches[0])
	if hdr.Format != recorder.FormatV1 || hdr.App != "matomeru" || hdr.Version != daemon.Version {
		t.Fatalf("unexpected recording header: %+v", hdr)
	}
	if len(events) == 0 {
		t.Fatal("expected recorded events in saved JSONL")
	}
	activeToasts = a.rt.toasts.Active()
	if len(activeToasts) == 0 || activeToasts[len(activeToasts)-1].Level != toast.Ok {
		t.Fatalf("expected ok toast from save, got %+v", activeToasts)
	}

	// 3. session.record.save with explicit dir param
	customDir := filepath.Join(t.TempDir(), "custom-recordings")
	d.Send(core.CommandEvent{Command: core.Command{
		ID:     CmdSessionRecordSave,
		Args:   map[string]any{"dir": customDir},
		Source: "palette",
		Actor:  User,
	}})
	customMatches, err := filepath.Glob(filepath.Join(customDir, "matomeru-*.jsonl"))
	if err != nil || len(customMatches) != 1 {
		t.Fatalf("expected 1 recording in %s, got %v (err=%v)", customDir, customMatches, err)
	}
	customHdr, customEvents := readSavedRecording(t, customMatches[0])
	if customHdr.Format != recorder.FormatV1 || len(customEvents) == 0 {
		t.Fatalf("invalid custom dir recording: hdr=%+v len=%d", customHdr, len(customEvents))
	}

	// 4. session.record.toggle (off then on)
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdSessionRecordToggle, Source: "palette", Actor: User}})
	if a.Shell.Recorder.Enabled() || a.cfg.Settings.Bool(SetSessionRecord, true) {
		t.Fatal("toggle did not disable session recording")
	}
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdSessionRecordStatus, Source: "palette", Actor: User}})
	if !strings.Contains(a.Status(), "off") {
		t.Fatalf("expected status to show off after toggle, got %q", a.Status())
	}

	// 5. session.record.clear (while disabled so Shell.Update doesn't re-buffer post-step events)
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdSessionRecordClear, Source: "palette", Actor: User}})
	if got := a.Shell.Recorder.Len(); got != 0 {
		t.Fatalf("expected 0 buffered events after clear, got %d", got)
	}

	// Toggle back on.
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdSessionRecordToggle, Source: "palette", Actor: User}})
	if !a.Shell.Recorder.Enabled() || !a.cfg.Settings.Bool(SetSessionRecord, false) {
		t.Fatal("second toggle did not re-enable session recording")
	}
}

func TestSessionRecorderQuitMatrix(t *testing.T) {
	t.Run("case1_noConfirm_noPrompt", func(t *testing.T) {
		a, d := newRecorderTestApp(t, map[string]any{
			SetConfirmQuit:             false,
			SetSessionPromptSaveOnExit: false,
		}, "")
		d.Keys("2")
		d.Keys("Q")
		if a.Modal() != "" {
			t.Fatalf("expected no modal, got %q", a.Modal())
		}
		if !hasQuitEffect(a) {
			t.Fatalf("expected immediate QuitEffect, got %+v", a.Shell.LastEffects)
		}
	})

	t.Run("case1b_noConfirm_promptEnabled_emptyBuffer", func(t *testing.T) {
		a, d := newRecorderTestApp(t, map[string]any{
			SetConfirmQuit:             false,
			SetSessionRecord:           false,
			SetSessionPromptSaveOnExit: true,
		}, "")
		if a.Shell.Recorder.Len() != 0 {
			t.Fatalf("expected empty buffer, got %d", a.Shell.Recorder.Len())
		}
		d.Keys("Q")
		if a.Modal() != "" {
			t.Fatalf("expected no modal with empty buffer, got %q", a.Modal())
		}
		if !hasQuitEffect(a) {
			t.Fatalf("expected immediate QuitEffect, got %+v", a.Shell.LastEffects)
		}
	})

	t.Run("case2a_noConfirm_promptSave_saveAndQuit", func(t *testing.T) {
		dataDir := filepath.Join(t.TempDir(), "data")
		a, d := newRecorderTestApp(t, map[string]any{
			SetConfirmQuit:             false,
			SetSessionPromptSaveOnExit: true,
		}, dataDir)
		d.Keys("2")
		d.Keys("Q")
		if a.Modal() != "confirm.session.save" {
			t.Fatalf("expected confirm.session.save modal, got %q", a.Modal())
		}
		if hasQuitEffect(a) {
			t.Fatal("must not quit before user confirms save prompt")
		}
		frame := d.Frame()
		if !strings.Contains(frame, "Save Session Recording") || !strings.Contains(frame, "Save & Quit") || !strings.Contains(frame, "Quit without saving") {
			t.Fatalf("unexpected save prompt frame:\n%s", frame)
		}
		d.Keys("y")
		if !hasQuitEffect(a) {
			t.Fatalf("expected QuitEffect after Save & Quit, got %+v", a.Shell.LastEffects)
		}
		matches, err := filepath.Glob(filepath.Join(dataDir, "recordings", "matomeru-*.jsonl"))
		if err != nil || len(matches) != 1 {
			t.Fatalf("expected 1 saved recording, got %v (err=%v)", matches, err)
		}
		hdr, events := readSavedRecording(t, matches[0])
		if hdr.Format != recorder.FormatV1 || len(events) == 0 {
			t.Fatalf("invalid saved recording: hdr=%+v events=%d", hdr, len(events))
		}
	})

	t.Run("case2b_noConfirm_promptSave_quitWithoutSaving", func(t *testing.T) {
		dataDir := filepath.Join(t.TempDir(), "data")
		a, d := newRecorderTestApp(t, map[string]any{
			SetConfirmQuit:             false,
			SetSessionPromptSaveOnExit: true,
		}, dataDir)
		d.Keys("2")
		d.Keys("Q")
		if a.Modal() != "confirm.session.save" {
			t.Fatalf("expected confirm.session.save modal, got %q", a.Modal())
		}
		d.Keys("n")
		if !hasQuitEffect(a) {
			t.Fatalf("expected QuitEffect after Quit without saving, got %+v", a.Shell.LastEffects)
		}
		matches, _ := filepath.Glob(filepath.Join(dataDir, "recordings", "*.jsonl"))
		if len(matches) != 0 {
			t.Fatalf("expected no recording written on cancel, got %v", matches)
		}
	})

	t.Run("case2c_noConfirm_promptSave_errorAbortsQuit", func(t *testing.T) {
		// Point DataDir at a regular file so MkdirAll(<DataDir>/recordings) fails.
		blocker := filepath.Join(t.TempDir(), "not-a-dir")
		if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		a, d := newRecorderTestApp(t, map[string]any{
			SetConfirmQuit:             false,
			SetSessionPromptSaveOnExit: true,
		}, blocker)
		d.Keys("2")
		beforeEvents := a.Shell.Recorder.Len()
		d.Keys("Q")
		if a.Modal() != "confirm.session.save" {
			t.Fatalf("expected confirm.session.save modal, got %q", a.Modal())
		}
		d.Keys("y")
		if hasQuitEffect(a) {
			t.Fatalf("save failure must abort quit, got QuitEffect: %+v", a.Shell.LastEffects)
		}
		if !strings.Contains(a.Status(), "save session recording:") {
			t.Fatalf("expected error status on save failure, got %q", a.Status())
		}
		activeToasts := a.rt.toasts.Active()
		if len(activeToasts) == 0 || activeToasts[len(activeToasts)-1].Level != toast.Err {
			t.Fatalf("expected error toast on save failure, got %+v", activeToasts)
		}
		if a.Shell.Recorder.Len() < beforeEvents {
			t.Fatalf("expected buffered events preserved on save error: before=%d after=%d", beforeEvents, a.Shell.Recorder.Len())
		}
	})

	t.Run("case3_confirmQuit_noPrompt", func(t *testing.T) {
		a, d := newRecorderTestApp(t, map[string]any{
			SetConfirmQuit:             true,
			SetSessionPromptSaveOnExit: false,
		}, "")
		d.Keys("2")
		d.Keys("Q")
		if a.Modal() != "confirm" {
			t.Fatalf("expected quit confirm modal, got %q", a.Modal())
		}
		d.Keys("y")
		if a.Modal() != "" {
			t.Fatalf("expected modal closed after confirm, got %q", a.Modal())
		}
		if !hasQuitEffect(a) {
			t.Fatalf("expected QuitEffect after Quit Matomeru confirm, got %+v", a.Shell.LastEffects)
		}
	})

	t.Run("case4_confirmQuit_promptSave_chain", func(t *testing.T) {
		dataDir := filepath.Join(t.TempDir(), "data")
		a, d := newRecorderTestApp(t, map[string]any{
			SetConfirmQuit:             true,
			SetSessionPromptSaveOnExit: true,
		}, dataDir)
		d.Keys("2")
		d.Keys("Q")
		if a.Modal() != "confirm" {
			t.Fatalf("expected quit confirm modal first, got %q", a.Modal())
		}
		d.Keys("y")
		if a.Modal() != "confirm.session.save" {
			t.Fatalf("expected chained confirm.session.save modal, got %q", a.Modal())
		}
		if hasQuitEffect(a) {
			t.Fatal("must not quit before save recording prompt resolves")
		}
		d.Keys("y")
		if !hasQuitEffect(a) {
			t.Fatalf("expected QuitEffect after chained Save & Quit, got %+v", a.Shell.LastEffects)
		}
		matches, err := filepath.Glob(filepath.Join(dataDir, "recordings", "matomeru-*.jsonl"))
		if err != nil || len(matches) != 1 {
			t.Fatalf("expected 1 saved recording, got %v (err=%v)", matches, err)
		}
		hdr, events := readSavedRecording(t, matches[0])
		if hdr.Format != recorder.FormatV1 || len(events) == 0 {
			t.Fatalf("invalid saved recording: hdr=%+v events=%d", hdr, len(events))
		}
	})
}
