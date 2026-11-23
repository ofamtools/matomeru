package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/recorder"
	"ofam.dev/ochazuke/widgets/toast"
)

func TestUXNoteCaptureFreezesContextAndPiles22Notes(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "mtm-data")
	a, d := newRecorderTestApp(t, nil, dataDir)

	if !a.cfg.Settings.Bool(SetSessionUXNotes, false) {
		t.Fatalf("expected %s default true", SetSessionUXNotes)
	}
	specs := SettingSpecs()
	if specs[len(specs)-1].Name != SetSessionUXNotes {
		t.Fatalf("expected %s at END of SettingSpecs, got %q", SetSessionUXNotes, specs[len(specs)-1].Name)
	}

	// Move to 2 Work so we have a distinct space, focus, and live frame.
	d.Keys("2")
	beforeSpace := a.Space()
	beforeFocus := a.Focus()
	beforeMode := a.Mode()
	beforeFrame := a.Shell.LastFrame()
	if beforeFrame.Empty() {
		t.Fatal("expected non-empty Shell.LastFrame() before alt+n")
	}

	// Pile on 22 rapid UX notes (alternating enter and ctrl+s) without interruption.
	for i := 1; i <= 22; i++ {
		d.Keys("alt+n")
		if a.Modal() != uxNoteModalID {
			t.Fatalf("note #%d: expected modal %q, got %q", i, uxNoteModalID, a.Modal())
		}
		if a.Mode() != ModeFormEdit {
			t.Fatalf("note #%d: expected autofocused ModeFormEdit, got %q", i, a.Mode())
		}
		d.Type(fmt.Sprintf("UX issue #%d on work space", i))
		if i%2 == 1 {
			d.Keys("enter")
		} else {
			d.Keys("ctrl+s")
		}
		if a.Modal() != "" {
			t.Fatalf("note #%d: modal did not close on submit: %q", i, a.Modal())
		}
		if a.Space() != beforeSpace || a.Focus() != beforeFocus || a.Mode() != beforeMode {
			t.Fatalf("note #%d: did not drop user back where they were: got (%s,%s,%s), want (%s,%s,%s)",
				i, a.Space(), a.Focus(), a.Mode(), beforeSpace, beforeFocus, beforeMode)
		}
		activeToasts := a.rt.toasts.Active()
		wantToast := fmt.Sprintf("UX note #%d saved", i)
		if len(activeToasts) == 0 || activeToasts[len(activeToasts)-1].Text != wantToast || activeToasts[len(activeToasts)-1].Level != toast.Ok {
			t.Fatalf("note #%d: expected ok toast %q, got %+v", i, wantToast, activeToasts)
		}
	}

	if a.rt.uxNoteSessionPath == "" {
		t.Fatal("expected active uxNoteSessionPath to be created")
	}
	if !strings.HasPrefix(a.rt.uxNoteSessionPath, filepath.Join(dataDir, "recordings")) {
		t.Fatalf("unexpected recording path: %s", a.rt.uxNoteSessionPath)
	}

	hdr, evs := readSavedRecording(t, a.rt.uxNoteSessionPath)
	if hdr.Format != recorder.FormatV1 {
		t.Fatalf("unexpected format: %+v", hdr)
	}
	// Verify zero duplicate seq numbers in the incrementally flushed file.
	seenSeq := map[int]bool{}
	for _, ev := range evs {
		if seenSeq[ev.Seq] {
			t.Fatalf("duplicate Seq %d in incrementally flushed file", ev.Seq)
		}
		seenSeq[ev.Seq] = true
	}
	notes := recorder.FilterNotes(evs)
	if len(notes) != 22 {
		t.Fatalf("expected 22 notes persisted on disk, got %d", len(notes))
	}
	first := notes[0]
	if first.Note != "UX issue #1 on work space" || first.Mode != beforeMode || first.Page != beforeSpace || first.FocusID != beforeFocus {
		t.Fatalf("first note frozen context mismatch: %+v", first)
	}
	if first.ANSI != beforeFrame.ANSI || first.FrameHash != beforeFrame.Hash {
		t.Fatalf("first note did not freeze pre-modal frame (got hash=%q len=%d, want hash=%q len=%d)",
			first.FrameHash, len(first.ANSI), beforeFrame.Hash, len(beforeFrame.ANSI))
	}
}

func TestUXNoteCaptureAcrossModesAndNestedModals(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "mtm-data")
	a, d := newRecorderTestApp(t, map[string]any{
		SetSessionRecord: false, // passive recording paused: notes must still capture!
	}, dataDir)

	// 1. Inside filter mode
	d.Keys("2", "/")
	if a.Mode() != ModeFilter {
		t.Fatalf("expected ModeFilter, got %q", a.Mode())
	}
	d.Keys("alt+n")
	if a.Modal() != uxNoteModalID {
		t.Fatalf("alt+n from filter mode did not open quick note: %q", a.Modal())
	}
	d.Type("Filter bar needs wider input")
	d.Keys("enter")
	if a.Mode() != ModeFilter {
		t.Fatalf("did not restore ModeFilter after note submit: got %q", a.Mode())
	}
	d.Keys("esc")

	// 2. Inside a picker modal (Help picker '?')
	d.Keys("?")
	if a.Modal() != "help" {
		t.Fatalf("expected help modal, got %q", a.Modal())
	}
	d.Keys("alt+n")
	if a.Modal() != uxNoteModalID {
		t.Fatalf("alt+n from picker modal did not open quick note: %q", a.Modal())
	}
	d.Type("Help picker subtitle clips")
	d.Keys("ctrl+s")
	if a.Modal() != "help" {
		t.Fatalf("did not restore help modal after note submit: got %q", a.Modal())
	}
	d.Keys("esc")

	// 3. Inside a form modal (Settings 'S')
	d.Keys("S")
	if a.Modal() != "settings" {
		t.Fatalf("expected settings modal, got %q", a.Modal())
	}
	d.Keys("alt+n")
	if a.Modal() != uxNoteModalID {
		t.Fatalf("alt+n from form modal did not open quick note: %q", a.Modal())
	}
	d.Type("Settings form spacing")
	d.Keys("enter")
	if a.Modal() != "settings" {
		t.Fatalf("did not restore settings modal after note submit: got %q", a.Modal())
	}
	d.Keys("esc")

	_, evs := readSavedRecording(t, a.rt.uxNoteSessionPath)
	notes := recorder.FilterNotes(evs)
	if len(notes) != 3 {
		t.Fatalf("expected 3 notes captured across modes while paused, got %d", len(notes))
	}
	if notes[0].Mode != ModeFilter || notes[1].Modal != "help" || notes[2].Modal != "settings" {
		t.Fatalf("unexpected frozen modal/mode context: %+v", notes)
	}
	for i, n := range notes {
		if n.ANSI == "" {
			t.Errorf("note #%d missing frozen ANSI frame while passive recording paused", i+1)
		}
	}
}

func TestUXNotesOptOutListAndSandboxOpen(t *testing.T) {
	// Opt-out setting disables alt+n cleanly.
	aOff, dOff := newRecorderTestApp(t, map[string]any{SetSessionUXNotes: false}, "")
	dOff.Keys("alt+n")
	if aOff.Modal() != "" {
		t.Fatalf("expected alt+n disabled when session.uxNotes=false, got modal %q", aOff.Modal())
	}
	if !strings.Contains(aOff.Status(), SetSessionUXNotes) {
		t.Fatalf("expected disabled status hint, got %q", aOff.Status())
	}

	// Enabled: capture a note, open session.note.list, and open session.sandbox.open.
	dataDir := filepath.Join(t.TempDir(), "mtm-data")
	a, d := newRecorderTestApp(t, nil, dataDir)
	d.Keys("alt+n")
	d.Type("Inspect list and sandbox open")
	d.Keys("enter")

	// session.note.list opens normal-mode picker listing the captured note.
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdSessionNoteList, Source: "palette", Actor: User}})
	if a.Modal() != uxNoteListModalID {
		t.Fatalf("expected %q modal, got %q", uxNoteListModalID, a.Modal())
	}
	if a.Mode() != ModePicker {
		t.Fatalf("expected normal-mode picker (%q), got %q", ModePicker, a.Mode())
	}
	if !strings.Contains(d.Frame(), "Inspect list and sandbox open") {
		t.Fatalf("expected captured note text in session.note.list picker:\n%s", d.Frame())
	}
	d.Keys("esc")

	// session.sandbox.open emits ExecEffect pointing at the flushed recording file.
	d.Send(core.CommandEvent{Command: core.Command{ID: CmdSessionSandboxOpen, Source: "palette", Actor: User}})
	var execEff *core.ExecEffect
	for _, eff := range a.Shell.LastEffects {
		if ee, ok := eff.(core.ExecEffect); ok {
			copy := ee
			execEff = &copy
			break
		}
	}
	if execEff == nil {
		t.Fatalf("expected ExecEffect from %s, got %+v", CmdSessionSandboxOpen, a.Shell.LastEffects)
	}
	if !strings.HasSuffix(execEff.Name, "o-sandbox") {
		t.Fatalf("expected o-sandbox binary, got %q", execEff.Name)
	}
	if len(execEff.Args) != 1 || execEff.Args[0] != a.rt.uxNoteSessionPath {
		t.Fatalf("expected active session recording arg %q, got %v", a.rt.uxNoteSessionPath, execEff.Args)
	}
}
