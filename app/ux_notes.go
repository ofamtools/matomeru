package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/recorder"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/toast"
)

// In-context UX note capture and o-sandbox launcher commands.
const (
	CmdSessionNoteCapture = "session.note.capture"
	CmdSessionNoteList    = "session.note.list"
	CmdSessionSandboxOpen = "session.sandbox.open"

	uxNoteModalID     = "ux.note"
	uxNoteListModalID = "ux.notes"
)

var uxNoteSeverities = []string{"note", "bug", "polish", "idea"}

// frozenUXContext captures live UI state BEFORE opening the quick-note modal
// so the recorded UX note reflects exactly what the owner was looking at.
type frozenUXContext struct {
	Frame   recorder.FrameSnapshot
	Space   string
	Page    string
	Modal   string
	Mode    string
	FocusID string
	Scope   string
	Status  string
}

func uxNotesSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		spec(CmdSessionNoteCapture, "Drop an in-context UX note freezing the current screen, focus, mode, and keystroke trail"),
		spec(CmdSessionNoteList, "Browse UX notes captured during this session"),
		{
			ID:          CmdSessionSandboxOpen,
			Description: "Open the active session recording in o-sandbox for replay and triage",
			Params: []core.ParamSpec{
				{Name: "path", Type: core.ParamPath, Description: "optional recording JSONL path (defaults to active session recording)"},
			},
		},
	}
}

func uxNotesBindings() []core.Binding {
	modes := []string{
		ModeNormal,
		ModeFilter,
		ModePicker,
		ModePickerFilter,
		ModeForm,
		ModeFormEdit,
		ModeOutlineEdit,
		ModeBindings,
		ModeBindingsFilter,
		ModeReader,
		ModeInspect,
		ModeCommandLine,
		ModeComposer,
		ModeConfirm,
		ModeAbout,
		ModePage,
	}
	out := make([]core.Binding, 0, len(modes)+5)
	for _, m := range modes {
		b := bind(m, "", "alt+n", CmdSessionNoteCapture, "UX note")
		if m != ModeNormal {
			b = hidden(b)
		}
		out = append(out, b)
	}
	// Quick-note modal ergonomics: enter or ctrl+s submits immediately, esc cancels,
	// tab advances to optional tag/severity fields.
	out = append(out,
		bind(ModeFormEdit, uxNoteModalID, "enter", form.CmdSubmit, "save UX note"),
		bind(ModeFormEdit, uxNoteModalID, "esc", CmdModalCancel, "cancel UX note"),
		bind(ModeFormEdit, uxNoteModalID, "tab", "motion.down", "next field"),
		bind(ModeForm, uxNoteModalID, "enter", form.CmdSubmit, "save UX note"),
	)
	return out
}

func (a *App) uxNotesEnabled() bool {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return true
	}
	return a.cfg.Settings.Bool(SetSessionUXNotes, true)
}

func (a *App) captureFrozenUXContext(s core.State) frozenUXContext {
	var snap recorder.FrameSnapshot
	if a.Shell != nil {
		snap = a.Shell.LastFrame()
	}
	space := s.ActivePage
	page := space
	if space == SpacePanes {
		if pane, ok := a.SelectedPane(); ok {
			if p := strings.TrimSpace(a.panes.activePage[pane.ID]); p != "" {
				page = space + "/" + p
			}
		}
	} else if space == SpaceWork {
		if ref, ok := a.Showing(); ok {
			page = space + "/show:" + ref.Kind
		}
	}

	focusID := s.ActiveFrame().FocusID
	var facts map[string]any
	if a.Shell != nil && a.Shell.AppFacts != nil {
		facts = a.Shell.AppFacts(s)
	}
	scope := ""
	chain := ui.ContextChain(a.View(s), focusID, facts)
	for _, f := range chain {
		if f.Scope != "" && f.Scope != "app" {
			scope = f.Scope
			break
		}
	}

	return frozenUXContext{
		Frame:   snap,
		Space:   space,
		Page:    page,
		Modal:   a.Modal(),
		Mode:    a.Engine.Mode(s),
		FocusID: focusID,
		Scope:   scope,
		Status:  a.status,
	}
}

func (a *App) ensureUXNoteSessionPath() (string, error) {
	if a.rt.uxNoteSessionPath != "" {
		return a.rt.uxNoteSessionPath, nil
	}
	dir := a.sessionRecordingsDir("")
	if strings.TrimSpace(dir) == "" {
		return "", errors.New("recordings directory is not configured")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	base := fmt.Sprintf("matomeru-%s", a.now().UTC().Format("20060102-150405"))
	for suffix := 0; ; suffix++ {
		name := base + ".jsonl"
		if suffix > 0 {
			name = fmt.Sprintf("%s-%d.jsonl", base, suffix)
		}
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			a.rt.uxNoteSessionPath = candidate
			return candidate, nil
		}
	}
}

func (a *App) sessionNoteCapture(s core.State, c core.Command) core.StepResult {
	if a.pagedNoteComposerEnabled() {
		return a.openNoteDeckForFocus(s, c)
	}
	if !a.uxNotesEnabled() {
		a.say("UX note capture is disabled (%s)", SetSessionUXNotes)
		return core.Next(s)
	}
	if a.Modal() == uxNoteModalID {
		return core.Next(s)
	}
	if a.Shell == nil || a.Shell.Recorder == nil {
		a.say("session recorder is not initialized")
		return core.Next(s)
	}

	// Freeze live screen and UI context BEFORE pushing the quick-note overlay.
	frozen := a.captureFrozenUXContext(s)

	specs := []core.ParamSpec{
		{Name: "note", Type: core.ParamString, Required: true, Description: "UX note (enter or ctrl+s saves immediately)"},
		{Name: "tag", Type: core.ParamString, Description: "optional tag (layout, focus, keys, perf, ...)"},
		{Name: "severity", Type: core.ParamString, Enum: slices.Clone(uxNoteSeverities), Default: "note", Description: "note severity"},
	}
	fm := form.New("Quick UX Note", specs, nil)
	fm.SubmitRow = true
	fm.SubmitLabel = "Save Note"
	fm.Wrap = a.wrapInputs()
	// Autofocus the note field directly in edit mode so typing starts at once.
	fm.HandleCommand(core.Command{ID: form.CmdEdit})

	ctxBadge := frozen.Page
	if frozen.Modal != "" {
		ctxBadge += " · modal:" + frozen.Modal
	}
	if frozen.FocusID != "" {
		ctxBadge += " · " + frozen.FocusID
	}

	mod := modal.NewFormModal(fm, modal.FormOptions{
		ID:            uxNoteModalID,
		Scope:         uxNoteModalID,
		Title:         fmt.Sprintf("UX Note #%d", a.rt.uxNoteCount+1),
		Tone:          "accent",
		WidthPct:      68,
		DisableDrafts: true,
		Badges: func(_ *form.Model) []ui.Badge {
			badges := []ui.Badge{{Text: frozen.Mode, Tone: "info"}}
			if ctxBadge != "" {
				badges = append(badges, ui.Badge{Text: clip(ctxBadge, 44), Tone: "muted"})
			}
			if !frozen.Frame.Empty() {
				badges = append(badges, ui.Badge{Text: fmt.Sprintf("frame %dx%d", frozen.Frame.W, frozen.Frame.H), Tone: "ok"})
			}
			return badges
		},
		OnCommand: func(m *form.Model, cmd core.Command) (modal.Result, bool) {
			if cmd.ID == "motion.down" && m.Editing {
				m.HandleCommand(core.Command{ID: form.CmdStop})
				m.Down()
				if m.Editable() {
					m.HandleCommand(core.Command{ID: form.CmdEdit})
				}
				return modal.Result{Handled: true, Mode: uxNoteMode(m)}, true
			}
			return modal.Result{}, false
		},
		OnSubmit: func(vals map[string]any) modal.Result {
			text := strings.TrimSpace(fdString(vals, "note"))
			if text == "" {
				return modal.Result{Handled: true, Mode: ModeFormEdit, Status: "note text is required", SetStatus: true}
			}
			tag := strings.TrimSpace(fdString(vals, "tag"))
			severity := strings.TrimSpace(fdString(vals, "severity"))
			if severity == "" {
				severity = "note"
			}

			a.Shell.Recorder.RecordNote(recorder.NoteInput{
				Note:      text,
				Tag:       tag,
				Severity:  severity,
				Mode:      frozen.Mode,
				Page:      frozen.Page,
				Modal:     frozen.Modal,
				FocusID:   frozen.FocusID,
				Scope:     frozen.Scope,
				Status:    frozen.Status,
				W:         frozen.Frame.W,
				H:         frozen.Frame.H,
				FrameHash: frozen.Frame.Hash,
				ANSI:      frozen.Frame.ANSI,
			})

			path, err := a.ensureUXNoteSessionPath()
			if err == nil {
				err = a.Shell.Recorder.FlushToFile(path)
			}
			a.rt.uxNoteCount++
			toastMsg := fmt.Sprintf("UX note #%d saved", a.rt.uxNoteCount)
			if err != nil {
				a.fail("flush UX note", err)
				return modal.Result{
					Handled:     true,
					Close:       true,
					CloseReason: modal.CloseSubmit,
					Toast:       &modal.ToastSpec{Text: toastMsg + " (flush warning)", Level: toast.Warn},
				}
			}
			return modal.Result{
				Handled:     true,
				Close:       true,
				CloseReason: modal.CloseSubmit,
				Status:      fmt.Sprintf("%s · %s", toastMsg, filepath.Base(path)),
				SetStatus:   true,
				Toast:       &modal.ToastSpec{Text: toastMsg, Level: toast.Ok},
			}
		},
	})
	a.say("type UX note · enter / ctrl+s saves · tab tag/severity · esc cancels")
	return core.Next(a.pushModal(s, mod))
}

func uxNoteMode(m *form.Model) string {
	if m != nil && m.Editing {
		return ModeFormEdit
	}
	return ModeForm
}

func (a *App) sessionNoteList(s core.State, _ core.Command) core.StepResult {
	if a.Shell == nil || a.Shell.Recorder == nil {
		a.say("session recorder is not initialized")
		return core.Next(s)
	}
	_, events := a.Shell.Recorder.Snapshot()
	notes := recorder.FilterNotes(events)
	if len(notes) == 0 {
		msg := "no UX notes captured in this session yet · press alt+n anywhere to drop one"
		a.say("%s", msg)
		a.pushToast(msg, toast.Info)
		return core.Next(s)
	}

	items := make([]picker.Item, len(notes))
	for i := range notes {
		n := notes[len(notes)-1-i] // newest first
		num := len(notes) - i
		var badges []ui.Badge
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("#%d", num), Tone: "accent"})
		if n.Severity != "" && n.Severity != "note" {
			tone := "info"
			if n.Severity == "bug" {
				tone = "danger"
			} else if n.Severity == "polish" {
				tone = "warning"
			}
			badges = append(badges, ui.Badge{Text: n.Severity, Tone: tone})
		}
		if n.Tag != "" {
			badges = append(badges, ui.Badge{Text: n.Tag, Tone: "muted"})
		}
		subtitle := fmt.Sprintf("%s · mode:%s · focus:%s", n.Page, n.Mode, n.FocusID)
		if n.Modal != "" {
			subtitle += " · modal:" + n.Modal
		}
		items[i] = picker.Item{
			ID:       n.NoteID,
			Title:    n.Note,
			Subtitle: subtitle,
			Badges:   badges,
			Keywords: []string{n.NoteID, n.Tag, n.Severity, n.Page, n.FocusID, n.Modal},
			Data:     n,
		}
	}

	pm := picker.New(fmt.Sprintf("Session UX Notes (%d)", len(notes)), items)
	pm.Placeholder = "filter notes · enter inspect"
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       uxNoteListModalID,
		Scope:    uxNoteListModalID,
		PageSize: 12,
		OnConfirm: func(picked picker.Item) modal.Result {
			ev, ok := picked.Data.(recorder.Event)
			if !ok {
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
			}
			body := formatUXNoteMarkdown(ev, recorder.ContextTrail(events, ev.Seq, 15))
			a.afterModal(func(st core.State) core.State {
				return a.openReader(st, openTarget{title: "UX Note · " + ev.NoteID, body: body})
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	return core.Next(a.pushModal(s, m))
}

func formatUXNoteMarkdown(ev recorder.Event, trail []recorder.Event) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# UX Note %s\n\n", ev.NoteID)
	fmt.Fprintf(&b, "**Note:** %s\n\n", ev.Note)
	fmt.Fprintf(&b, "- **Page:** `%s`\n", ev.Page)
	fmt.Fprintf(&b, "- **Mode:** `%s`\n", ev.Mode)
	if ev.Modal != "" {
		fmt.Fprintf(&b, "- **Modal:** `%s`\n", ev.Modal)
	}
	if ev.FocusID != "" {
		fmt.Fprintf(&b, "- **Focus:** `%s`\n", ev.FocusID)
	}
	if ev.Scope != "" {
		fmt.Fprintf(&b, "- **Scope:** `%s`\n", ev.Scope)
	}
	if ev.Tag != "" {
		fmt.Fprintf(&b, "- **Tag:** `%s`\n", ev.Tag)
	}
	if ev.Severity != "" {
		fmt.Fprintf(&b, "- **Severity:** `%s`\n", ev.Severity)
	}
	if ev.Status != "" {
		fmt.Fprintf(&b, "- **Status:** %s\n", ev.Status)
	}
	if ev.W > 0 && ev.H > 0 {
		fmt.Fprintf(&b, "- **Frame:** %dx%d (`%s`)\n", ev.W, ev.H, ev.FrameHash)
	}
	if len(trail) > 0 {
		b.WriteString("\n## Preceding Events\n\n")
		for _, t := range trail {
			switch t.Kind {
			case recorder.KindKey:
				fmt.Fprintf(&b, "- `#%d` key `%s`\n", t.Seq, t.Key)
			case recorder.KindCommand:
				fmt.Fprintf(&b, "- `#%d` cmd `%s` (%s)\n", t.Seq, t.Command, t.Page)
			default:
				fmt.Fprintf(&b, "- `#%d` %s\n", t.Seq, t.Kind)
			}
		}
	}
	return b.String()
}

func resolveSandboxBinary(lookPath func(string) (string, error)) (string, error) {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if p, err := lookPath("o-sandbox"); err == nil && p != "" {
		return p, nil
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidate := filepath.Join(home, "bin", "o-sandbox")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", errors.New("o-sandbox binary not found in PATH or ~/bin/o-sandbox")
}

func latestRecordingInDir(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var newest string
	var newestMod int64
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".jsonl") {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		if mt := info.ModTime().UnixNano(); newest == "" || mt > newestMod {
			newest = filepath.Join(dir, ent.Name())
			newestMod = mt
		}
	}
	return newest
}

func (a *App) sessionSandboxOpen(s core.State, c core.Command) core.StepResult {
	path := strings.TrimSpace(c.Arg("path"))
	if path == "" {
		if a.Shell != nil && a.Shell.Recorder != nil && (a.rt.uxNoteSessionPath != "" || a.Shell.Recorder.Len() > 0) {
			p, err := a.ensureUXNoteSessionPath()
			if err != nil {
				a.fail("open o-sandbox", err)
				a.pushToast("Open o-sandbox failed: "+err.Error(), toast.Err)
				return core.Next(s)
			}
			if err := a.Shell.Recorder.FlushToFile(p); err != nil {
				a.fail("flush session recording", err)
				a.pushToast("Flush recording failed: "+err.Error(), toast.Err)
				return core.Next(s)
			}
			path = p
		} else {
			path = latestRecordingInDir(a.sessionRecordingsDir(""))
		}
	}

	bin, err := resolveSandboxBinary(exec.LookPath)
	if err != nil {
		// Fall back to bare binary name when running synthetic tests without installed o-sandbox.
		if a.opts.Live {
			a.fail("open o-sandbox", err)
			a.pushToast(err.Error(), toast.Err)
			return core.Next(s)
		}
		bin = "o-sandbox"
	}

	var args []string
	if path != "" {
		args = []string{path}
	}
	a.say("opening o-sandbox: %s", filepath.Base(path))
	return core.Next(s, core.ExecEffect{
		Name: bin,
		Args: args,
		Slot: "sandbox",
	})
}
