package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/recorder"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/toast"
)

// Session recorder commands (reachable via the command palette, inspect
// actions, and user bindings.json).
const (
	CmdSessionRecordSave   = "session.record.save"
	CmdSessionRecordToggle = "session.record.toggle"
	CmdSessionRecordClear  = "session.record.clear"
	CmdSessionRecordStatus = "session.record.status"
)

func sessionRecorderSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		{
			ID:          CmdSessionRecordSave,
			Description: "Save the buffered session recording as an o-sandbox/v1 JSONL file",
			Params: []core.ParamSpec{
				{Name: "dir", Type: core.ParamPath, Description: "output directory (defaults to recordings under dataDir)"},
			},
		},
		spec(CmdSessionRecordToggle, "Toggle in-memory session recording on or off"),
		spec(CmdSessionRecordClear, "Clear buffered session recording events"),
		spec(CmdSessionRecordStatus, "Show session recorder status and buffer usage"),
	}
}

func excludeWake(ev recorder.Event) bool {
	return !(ev.Kind == recorder.KindCommand && ev.Command == CmdWake)
}

func (a *App) initSessionRecorder() {
	if a.Shell == nil {
		return
	}
	enabled := true
	if a.cfg != nil && a.cfg.Settings != nil {
		enabled = a.cfg.Settings.Bool(SetSessionRecord, true)
	}
	a.Shell.EnableRecorder(recorder.Options{
		App:       "matomeru",
		Version:   daemon.Version,
		MaxEvents: recorder.DefaultMaxEvents,
		MaxBytes:  recorder.DefaultMaxBytes,
		Enabled:   enabled,
		Now:       a.now,
		Filter:    excludeWake,
	})
	a.syncSessionRecorder()
}

func (a *App) syncSessionRecorder() {
	if a == nil || a.Shell == nil || a.Shell.Recorder == nil {
		return
	}
	enabled := true
	if a.cfg != nil && a.cfg.Settings != nil {
		enabled = a.cfg.Settings.Bool(SetSessionRecord, true)
	}
	a.Shell.Recorder.SetEnabled(enabled)
}

func (a *App) shouldPromptSaveRecording() bool {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return false
	}
	if !a.cfg.Settings.Bool(SetSessionPromptSaveOnExit, false) {
		return false
	}
	return a.Shell != nil && a.Shell.Recorder != nil && a.Shell.Recorder.Len() > 0
}

func (a *App) sessionRecordingsDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir != "" {
		return dir
	}
	base := a.dataDir()
	if base == "" {
		return ""
	}
	return filepath.Join(base, "recordings")
}

func (a *App) saveSessionRecording(dir string) (string, error) {
	if a.Shell == nil || a.Shell.Recorder == nil {
		return "", errors.New("session recorder is not initialized")
	}
	return a.Shell.Recorder.SaveFile(a.sessionRecordingsDir(dir), "matomeru")
}

func (a *App) sessionRecordSave(s core.State, c core.Command) core.StepResult {
	path, err := a.saveSessionRecording(c.Arg("dir"))
	if err != nil {
		a.fail("save session recording", err)
		a.pushToast("Save session recording failed: "+err.Error(), toast.Err)
		return core.Next(s)
	}
	a.say("saved session recording: %s", path)
	a.pushToast("Saved recording: "+path, toast.Ok)
	return core.Next(s)
}

func (a *App) sessionRecordToggle(s core.State, _ core.Command) core.StepResult {
	next := !a.cfg.Settings.Bool(SetSessionRecord, true)
	a.cfg.userSettings[SetSessionRecord] = next
	a.reloadSettings()
	a.syncSessionRecorder()
	state := "off"
	if next {
		state = "on"
	}
	msg := fmt.Sprintf("session recording: %s", state)
	a.say("%s", msg)
	a.pushToast(msg, toast.Info)
	return core.Next(s)
}

func (a *App) sessionRecordClear(s core.State, _ core.Command) core.StepResult {
	if a.Shell == nil || a.Shell.Recorder == nil {
		a.say("session recorder is not initialized")
		return core.Next(s)
	}
	a.Shell.Recorder.Clear()
	msg := "cleared session recording buffer"
	a.say("%s", msg)
	a.pushToast(msg, toast.Info)
	return core.Next(s)
}

func formatSessionRecorderStatus(st recorder.Stats) string {
	state := "off"
	if st.Enabled {
		state = "on"
	}
	return fmt.Sprintf("session recording: %s · %d/%d events · %s (%d dropped)",
		state, st.Buffered, st.MaxEvents, humanBytes(int64(st.Bytes)), st.Dropped)
}

func (a *App) sessionRecordStatus(s core.State, _ core.Command) core.StepResult {
	if a.Shell == nil || a.Shell.Recorder == nil {
		a.say("session recorder is not initialized")
		return core.Next(s)
	}
	msg := formatSessionRecorderStatus(a.Shell.Recorder.Stats())
	a.say("%s", msg)
	a.pushToast(msg, toast.Info)
	return core.Next(s)
}

func (a *App) newSaveRecordingConfirmModal() *modal.ConfirmModal {
	var st recorder.Stats
	if a.Shell != nil && a.Shell.Recorder != nil {
		st = a.Shell.Recorder.Stats()
	}
	return modal.NewConfirmModal(modal.ConfirmOptions{
		ID:           "confirm.session.save",
		Scope:        "confirm",
		Mode:         ModeConfirm,
		Tone:         "info",
		Title:        "Save Session Recording",
		Message:      fmt.Sprintf("Save session recording (%d events, %s) before quitting?", st.Buffered, humanBytes(int64(st.Bytes))),
		ConfirmLabel: "Save & Quit",
		CancelLabel:  "Quit without saving",
		OnConfirm: func() modal.Result {
			path, err := a.saveSessionRecording("")
			if err != nil {
				a.fail("save session recording", err)
				a.pushToast("Save session recording failed: "+err.Error(), toast.Err)
				return modal.Result{}
			}
			a.say("saved session recording: %s", path)
			a.pushToast("Saved recording: "+path, toast.Ok)
			return modal.Result{Effects: []core.Effect{core.QuitEffect{}}}
		},
		OnCancel: func() modal.Result {
			return modal.Result{Effects: []core.Effect{core.QuitEffect{}}}
		},
	})
}
