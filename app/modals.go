package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/pathpicker"
	"ofam.dev/ochazuke/widgets/picker"
)

// --- modal stack plumbing -------------------------------------------------------

func (a *App) pushModal(s core.State, m modal.Modal) core.State {
	if a.modals == nil {
		a.modals = modal.NewStack()
	}
	return a.modals.Push(s, m)
}

func (a *App) popModal(s core.State, reason modal.CloseReason) core.State {
	if a.modals == nil {
		return s
	}
	s, _ = a.modals.Pop(s, reason)
	return s
}

func (a *App) afterModal(fn func(core.State) core.State) {
	if fn != nil {
		a.pendingState = append(a.pendingState, fn)
	}
}

func (a *App) handleModal(s core.State, c core.Command) core.StepResult {
	if a.modals == nil {
		return core.Next(s)
	}
	top := a.modals.Top()
	if top == nil || top.ID() != topModal(s) {
		return core.Next(s)
	}
	res := top.Handle(c)
	if !res.Handled {
		return core.Next(s)
	}
	return a.applyModalResult(s, res)
}

func (a *App) applyModalResult(s core.State, res modal.Result) core.StepResult {
	if res.Close && res.CloseReason == modal.CloseCancel && !res.SetStatus {
		a.say("")
	}
	if a.modals != nil {
		s = a.modals.ApplyResult(s, res)
	}
	if res.SetStatus {
		a.say("%s", res.Status)
	}
	if res.Toast != nil {
		if res.Toast.TTL > 0 {
			a.pushToast(res.Toast.Text, res.Toast.Level, res.Toast.TTL)
		} else {
			a.pushToast(res.Toast.Text, res.Toast.Level)
		}
	}
	for len(a.pendingState) > 0 {
		pending := a.pendingState
		a.pendingState = nil
		for _, fn := range pending {
			s = fn(s)
		}
	}
	effects := slices.Clone(res.Effects)
	for _, cmd := range res.Emit {
		if cmd.Actor.ID == "" && cmd.Actor.Kind == "" {
			cmd.Actor = User
		}
		a.gen++
		var facts map[string]any
		if a.Shell != nil && a.Shell.AppFacts != nil {
			facts = a.Shell.AppFacts(s)
		}
		step := a.Engine.Step(s, ui.ContextChain(a.View(s), s.ActiveFrame().FocusID, facts), core.CommandEvent{Command: cmd})
		s = step.State
		effects = append(effects, step.Effects...)
	}
	return core.Next(s, effects...)
}

// --- typed active modal accessors -----------------------------------------------

func (a *App) activeMarkdown(id string) *modal.MarkdownModal {
	if a.modals == nil {
		return nil
	}
	m, _ := a.modals.Get(id).(*modal.MarkdownModal)
	return m
}

func (a *App) activePathPicker() *modal.PathPickerModal {
	if a.modals == nil {
		return nil
	}
	m, _ := a.modals.Get(modalPath).(*modal.PathPickerModal)
	return m
}

func (a *App) activePalette() *modal.PaletteModal {
	if a.modals == nil {
		return nil
	}
	m, _ := a.modals.Get(modalPalette).(*modal.PaletteModal)
	return m
}

func (a *App) activeConfirm() *modal.ConfirmModal {
	if a.modals == nil {
		return nil
	}
	m, _ := a.modals.Get(modalConfirm).(*modal.ConfirmModal)
	return m
}

func (a *App) activeCommandLine() *modal.CommandLineModal {
	if a.modals == nil {
		return nil
	}
	m, _ := a.modals.Get(modalCommandLine).(*modal.CommandLineModal)
	return m
}

func (a *App) activePicker(id string) *modal.PickerModal {
	if a.modals == nil {
		return nil
	}
	m, _ := a.modals.Get(id).(*modal.PickerModal)
	return m
}

func (a *App) topPicker() *modal.PickerModal {
	if a.modals == nil {
		return nil
	}
	m, _ := a.modals.Top().(*modal.PickerModal)
	return m
}

// recordFormModal wraps modal.FormModal with record metadata for create/edit forms.
type recordFormModal struct {
	*modal.FormModal
	kind     string
	ref      *core.Ref // nil for a new record
	about    *core.Ref // a new note is attached to this record
	draftKey string
	isNew    bool
}

// answerFormModal wraps modal.FormModal with question/batch metadata for modalAnswer.
type answerFormModal struct {
	*modal.FormModal
	ref        core.Ref // KindQuestion ref when answering a global KindQuestion record
	pageID     string
	cardID     string
	questionID string
	batch      *batchQuestionInfo
	batchPos   int
	draftKey   string
}

func (a *App) activeForm(id string) *modal.FormModal {
	if a.modals == nil {
		return nil
	}
	switch m := a.modals.Get(id).(type) {
	case *modal.FormModal:
		return m
	case *recordFormModal:
		return m.FormModal
	case *answerFormModal:
		return m.FormModal
	}
	return nil
}

func (a *App) activeRecordForm() *recordFormModal {
	if a.modals == nil {
		return nil
	}
	m, _ := a.modals.Get(modalForm).(*recordFormModal)
	return m
}

func (a *App) activeAnswerForm() *answerFormModal {
	if a.modals == nil {
		return nil
	}
	m, _ := a.modals.Get(modalAnswer).(*answerFormModal)
	return m
}

func (a *App) activeLaunchForm() *modal.FormModal {
	return a.activeForm(modalLaunch)
}

func (a *App) activeSettingsForm() *modal.FormModal {
	return a.activeForm(modalSettings)
}

// --- form modal builders --------------------------------------------------------

func (a *App) formPickPath(getForm func() *modal.FormModal) func(form.PathRequest, any) modal.Result {
	return func(req form.PathRequest, current any) modal.Result {
		field := req.Field
		pm, ok := a.newPathPickerModal(current, func(entry pathpicker.DirEntry) modal.Result {
			if fm := getForm(); fm != nil {
				fm.SetValue(field, entry.Path)
				fm.FocusField(field)
			}
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		})
		if !ok {
			return modal.Result{Handled: true}
		}
		return modal.Result{
			Handled:   true,
			Push:      pm,
			Status:    "enter opens · tab chooses · left goes up",
			SetStatus: true,
		}
	}
}

func (a *App) formPickRef(getForm func() *modal.FormModal) func(form.RefRequest) modal.Result {
	return func(req form.RefRequest) modal.Result {
		field := req.Field
		return modal.Result{
			Handled: true,
			Push: a.newRefPickerModal(req, func(r core.Ref) {
				if fm := getForm(); fm != nil {
					fm.SetValue(field, r)
					fm.FocusField(field)
				}
			}),
		}
	}
}

func (a *App) formDraftDiscard(draftKey string) func(int) modal.Result {
	return func(idx int) modal.Result {
		if draftKey != "" && a.drafts != nil {
			a.drafts.Remove(draftKey, idx)
			a.saveDrafts()
		}
		return modal.Result{
			Handled:   true,
			Mode:      ModeForm,
			Status:    "discarded draft",
			SetStatus: true,
		}
	}
}

func (a *App) formCancelDraftClose(getModel func() *form.Model, draftKey string) func(modal.CloseReason) {
	return func(reason modal.CloseReason) {
		if reason != modal.CloseCancel {
			return
		}
		if draftKey != "" && a.saveFormDraft(getModel(), draftKey) {
			a.say("closed · draft saved ([ restores)")
		} else {
			a.say("closed")
		}
	}
}

func (a *App) newRecordFormModal(kind string, ref, about *core.Ref, fm *form.Model) *recordFormModal {
	var refCopy, aboutCopy *core.Ref
	if ref != nil {
		r := *ref
		refCopy = &r
	}
	if about != nil {
		r := *about
		aboutCopy = &r
	}
	rf := &recordFormModal{
		kind:     kind,
		ref:      refCopy,
		about:    aboutCopy,
		draftKey: recordDraftKey(kind, refCopy),
		isNew:    refCopy == nil,
	}
	schema := a.schema(kind)
	fm.SubmitRow = true
	if rf.isNew {
		fm.SubmitLabel = "Create " + strings.ToLower(schema.Title)
	} else {
		fm.SubmitLabel = "Save " + strings.ToLower(schema.Title)
	}
	a.applyHarnessChoicesToForm(kind, refCopy, fm)
	a.attachFormDrafts(fm, rf.draftKey)

	var mod *modal.FormModal
	mod = modal.NewFormModal(fm, modal.FormOptions{
		ID:          modalForm,
		Scope:       views.ScopeEdit,
		DraftBadges: true,
		RenderBody: func(m *form.Model, _ string) ui.ViewNode {
			var rec *data.Record
			if rf.ref != nil {
				rec, _ = a.Store.Get(*rf.ref)
			}
			panel := views.EditView(a.schema(rf.kind), rec, m, modalForm)
			if len(panel.Badges) > 0 {
				badges := make([]ui.Badge, 0, len(panel.Badges))
				for _, b := range panel.Badges {
					if b.Text == "changed" {
						continue
					}
					badges = append(badges, b)
				}
				panel.Badges = badges
			}
			return panel
		},
		OnPickPath:     a.formPickPath(func() *modal.FormModal { return mod }),
		OnPickRef:      a.formPickRef(func() *modal.FormModal { return mod }),
		OnDraftDiscard: a.formDraftDiscard(rf.draftKey),
		OnSubmit: func(values map[string]any) modal.Result {
			if a.submitForm(rf, values) {
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
			}
			return modal.Result{Handled: true}
		},
		OnClose: a.formCancelDraftClose(func() *form.Model { return mod.Model() }, rf.draftKey),
	})
	rf.FormModal = mod
	return rf
}

func (a *App) newLaunchModal(title, submitLabel string, specs []core.ParamSpec, values map[string]any, draftKey string, onSubmit func(core.State, map[string]any) (core.State, bool)) *modal.FormModal {
	fm := form.New(title, specs, values)
	fm.SubmitRow = true
	if submitLabel == "" {
		submitLabel = "Run"
	}
	fm.SubmitLabel = submitLabel
	if draftKey != "" {
		a.attachFormDrafts(fm, draftKey)
	}
	var mod *modal.FormModal
	mod = modal.NewFormModal(fm, modal.FormOptions{
		ID:             modalLaunch,
		Scope:          modalLaunch,
		DraftBadges:    draftKey != "",
		DisableDrafts:  draftKey == "",
		OnPickPath:     a.formPickPath(func() *modal.FormModal { return mod }),
		OnPickRef:      a.formPickRef(func() *modal.FormModal { return mod }),
		OnDraftDiscard: a.formDraftDiscard(draftKey),
		OnSubmit: func(vals map[string]any) modal.Result {
			if onSubmit == nil {
				a.clearSubmittedDraft(fm, draftKey)
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
			}
			a.afterModal(func(st core.State) core.State {
				next, done := onSubmit(st, vals)
				if done {
					a.clearSubmittedDraft(fm, draftKey)
					next, _ = a.modals.Close(next, modalLaunch, modal.CloseSubmit)
				}
				return next
			})
			return modal.Result{Handled: true}
		},
		OnClose: a.formCancelDraftClose(func() *form.Model { return mod.Model() }, draftKey),
	})
	return mod
}

// --- record reference picker modal ----------------------------------------------

func (a *App) pickerCreateHook(kind string, onCreated func(rec data.Record) modal.Result) *modal.PickerCreateHook {
	schema := a.schema(kind)
	label := schema.Title
	if label == "" {
		label = kind
	}
	return &modal.PickerCreateHook{
		Kind:  kind,
		Label: label,
		OnCreate: func(query string) modal.Result {
			rec, err := a.createPickedRecord(kind, query)
			if err != nil {
				return modal.Result{Handled: true}
			}
			if onCreated != nil {
				return onCreated(rec)
			}
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	}
}

func (a *App) newRefPickerModal(req form.RefRequest, onPick func(core.Ref)) *modal.PickerModal {
	kinds := req.RefKinds
	if len(kinds) == 0 {
		for _, sc := range a.Store.Registry().List() {
			kinds = append(kinds, sc.Kind)
		}
	}
	var recs []data.Record
	for _, kind := range kinds {
		res, err := a.Store.Query(data.Query{Kind: kind})
		if err != nil {
			a.fail("read "+kind, err)
			continue
		}
		recs = append(recs, res.Items...)
	}
	pm := views.PickerFromRecordsWith(a.Store.Registry(), "Pick "+views.Label(req.Field), recs, a.styleOptions())
	var createHook *modal.PickerCreateHook
	if len(kinds) == 1 {
		if _, known := a.Store.Registry().Get(kinds[0]); known {
			createHook = a.pickerCreateHook(kinds[0], func(rec data.Record) modal.Result {
				if onPick != nil {
					onPick(rec.Ref())
				}
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
			})
		}
	}
	return modal.NewPickerModal(pm, modal.PickerOptions{
		ID:       modalRef,
		PageSize: 10,
		Create:   createHook,
		OnConfirm: func(picked picker.Item) modal.Result {
			if picked.Ref != nil && onPick != nil {
				onPick(*picked.Ref)
			}
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
}

// --- confirmation modal ---------------------------------------------------------

// openConfirm opens the shared confirmation overlay in ModeConfirm.
func (a *App) openConfirm(s core.State, opts modal.ConfirmOptions) core.State {
	if opts.ID == "" {
		opts.ID = modalConfirm
	}
	if opts.Scope == "" {
		opts.Scope = "confirm"
	}
	if opts.Mode == "" {
		opts.Mode = ModeConfirm
	}
	a.say("y/enter confirms · n/esc cancels")
	return a.pushModal(s, modal.NewConfirmModal(opts))
}

// appQuit handles core.CmdAppQuit (Q / ctrl+c): prompts with the shared
// confirmation dialog when confirm.quit is enabled, otherwise quits at once.
func (a *App) appQuit(s core.State, _ core.Command) core.StepResult {
	if !a.Settings.Bool(SetConfirmQuit, true) {
		return core.Next(s, core.QuitEffect{})
	}
	return core.Next(a.openConfirm(s, modal.ConfirmOptions{
		Title:        "Quit Matomeru",
		Message:      "Quit Matomeru? Your daemon panes will keep running in the background.",
		ConfirmLabel: "Confirm",
		OnConfirm: func() modal.Result {
			return modal.Result{Effects: []core.Effect{core.QuitEffect{}}}
		},
	}))
}

// --- path picker modal ----------------------------------------------------------

// newPathPickerModal builds a PathPickerModal starting in the first readable
// directory among start (or its parent), the working directory, and home.
func (a *App) newPathPickerModal(start any, onPick func(entry pathpicker.DirEntry) modal.Result) (*modal.PathPickerModal, bool) {
	fs := a.opts.FS
	if fs == nil {
		fs = pathpicker.OSFS{}
	}
	home, _ := fs.HomeDir()
	var tries []string
	if dir, _ := start.(string); dir != "" {
		if rest, ok := strings.CutPrefix(dir, "~"); ok && home != "" {
			dir = filepath.Join(home, rest)
		}
		if filepath.IsAbs(dir) {
			tries = append(tries, dir, filepath.Dir(dir))
		}
	}
	if a.opts.FS == nil {
		if cwd, err := os.Getwd(); err == nil {
			tries = append(tries, cwd)
		}
	}
	tries = append(tries, home, string(filepath.Separator))
	var pp *pathpicker.Model
	for _, dir := range tries {
		if dir == "" {
			continue
		}
		var err error
		if pp, err = pathpicker.New(fs, dir, pathpicker.PickAny); err == nil {
			break
		}
	}
	if pp == nil {
		a.say("no directory could be read")
		return nil, false
	}
	return modal.NewPathPickerModal(pp, modal.PathPickerOptions{
		ID:        modalPath,
		Scope:     modalPath,
		Mode:      ModePicker,
		FocusID:   modalPath,
		WidthPct:  70,
		HeightPct: 70,
		OnPick: func(entry pathpicker.DirEntry) modal.Result {
			if onPick != nil {
				res := onPick(entry)
				res.Handled = true
				res.Close = true
				res.CloseReason = modal.CloseSubmit
				return res
			}
			return modal.Result{
				Handled:     true,
				Close:       true,
				CloseReason: modal.CloseSubmit,
				Status:      fmt.Sprintf("picked %s", entry.Path),
				SetStatus:   true,
			}
		},
	}), true
}

// openPath pushes a path picker overlay. With field set, picking fills that
// field of the parent form; otherwise it attaches the picked directory to target.
func (a *App) openPath(s core.State, field string, target *core.Ref, start any) core.State {
	var targetCopy *core.Ref
	if target != nil {
		r := *target
		targetCopy = &r
	}
	parentModal := topModal(s)
	pm, ok := a.newPathPickerModal(start, func(entry pathpicker.DirEntry) modal.Result {
		if field != "" {
			if fm := a.activeForm(parentModal); fm != nil {
				fm.SetValue(field, entry.Path)
			}
			return modal.Result{}
		}
		if targetCopy != nil {
			_, cs, err := a.Store.Relate(data.RelateInput{From: *targetCopy, Kind: data.RelDirectory, To: core.Ref{Kind: "directory", ID: entry.Path}}, User)
			a.wrote(fmt.Sprintf("%s: directory %s", a.title(*targetCopy), entry.Path), cs, err)
			return modal.Result{}
		}
		return modal.Result{Status: fmt.Sprintf("picked %s", entry.Path), SetStatus: true}
	})
	if !ok {
		return s
	}
	a.say("enter opens · tab chooses · left goes up")
	return a.pushModal(s, pm)
}
