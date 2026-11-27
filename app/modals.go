package app

import (
	"fmt"
	"maps"
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
	"ofam.dev/ochazuke/widgets/palette"
	"ofam.dev/ochazuke/widgets/pathpicker"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/toast"
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
	if top == nil {
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
		a.viewMemo.gen++
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
	m, _ := a.modals.Get("path").(*modal.PathPickerModal)
	return m
}

func (a *App) activePalette() *modal.PaletteModal {
	if a.modals == nil {
		return nil
	}
	m, _ := a.modals.Get("palette").(*modal.PaletteModal)
	return m
}

func (a *App) activeConfirm() *modal.ConfirmModal {
	if a.modals == nil {
		return nil
	}
	m, _ := a.modals.Get("confirm").(*modal.ConfirmModal)
	return m
}

func (a *App) activeCommandLine() *modal.CommandLineModal {
	if a.modals == nil {
		return nil
	}
	m, _ := a.modals.Get("commandline").(*modal.CommandLineModal)
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
	kind           string
	ref            *core.Ref // nil for a new record
	about          *core.Ref // a new note is attached to this record
	draftKey       string
	isNew          bool
	hiddenDefaults map[string]any
	choices        map[string][]string
	excludeChoices map[string][]string
}

type recordFormOptions struct {
	hiddenDefaults map[string]any
	choices        map[string][]string
	excludeChoices map[string][]string
}

// answerFormModal wraps modal.FormModal with question/batch metadata for the answer modal.
type answerFormModal struct {
	*modal.FormModal
	ref        core.Ref // KindQuestion ref when answering a global KindQuestion record
	pageID     string
	cardID     string
	questionID string
	batch      *batchQuestionInfo
	batchPos   int
	draftKey   string
	question   views.AnswerQuestion // what is being answered, shown above the fields
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
	m, _ := a.modals.Get("form").(*recordFormModal)
	return m
}

func (a *App) activeAnswerForm() *answerFormModal {
	if a.modals == nil {
		return nil
	}
	m, _ := a.modals.Get("answer").(*answerFormModal)
	return m
}

func (a *App) activeLaunchForm() *modal.FormModal {
	return a.activeForm("launch")
}

func (a *App) activeSettingsForm() *modal.FormModal {
	return a.activeForm("settings")
}

// --- form modal builders --------------------------------------------------------

const pathPickerStatus = "h/- up · l open · tab/enter choose · / filter"

func (a *App) pickerStartInFilter(isPalette bool) bool {
	mode := PickerDefaultModeNormal
	if a != nil && a.cfg != nil && a.cfg.Settings != nil {
		mode = a.cfg.Settings.String(SetPickerDefaultMode, PickerDefaultModeNormal)
	}
	switch mode {
	case PickerDefaultModeFilter:
		return true
	case PickerDefaultModeAllNormal:
		return false
	default:
		return isPalette
	}
}

func (a *App) newPickerModal(pm *picker.Model, opts modal.PickerOptions) *modal.PickerModal {
	if opts.Mode == "" {
		opts.Mode = ModePicker
	}
	if opts.FilterMode == "" {
		opts.FilterMode = ModePickerFilter
	}
	if !opts.StartInFilter {
		opts.StartInFilter = a.pickerStartInFilter(false)
	}
	return modal.NewPickerModal(pm, opts)
}

func (a *App) formPickPath(getForm func() *modal.FormModal) func(form.PathRequest, any) modal.Result {
	return func(req form.PathRequest, current any) modal.Result {
		field := req.Field
		pm, ok := a.newPathPickerModal(current, func(entry pathpicker.DirEntry) modal.Result {
			if fm := getForm(); fm != nil {
				fm.SetValue(field, entry.Path)
				if fm.Model().ActiveFieldName() != field {
					fm.FocusField(field)
				}
			}
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		})
		if !ok {
			return modal.Result{Handled: true}
		}
		return modal.Result{
			Handled:   true,
			Push:      pm,
			Status:    pathPickerStatus,
			SetStatus: true,
		}
	}
}

func (a *App) formDraftDiscard(draftKey string) func(int) modal.Result {
	return func(idx int) modal.Result {
		if draftKey != "" && a.cfg.drafts != nil {
			a.cfg.drafts.Remove(draftKey, idx)
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
		switch reason {
		case modal.CloseCancel:
			if draftKey != "" && a.saveFormDraft(getModel(), draftKey) {
				a.say("closed · draft saved ([ restores)")
			} else {
				a.say("closed")
			}
		case modal.CloseDropAll:
			if draftKey != "" {
				a.saveFormDraft(getModel(), draftKey)
			}
		}
	}
}

func (a *App) newRecordFormModal(kind string, ref, about *core.Ref, fm *form.Model) *recordFormModal {
	return a.newRecordFormModalWith(kind, ref, about, fm, recordFormOptions{})
}

func (a *App) newRecordFormModalWith(kind string, ref, about *core.Ref, fm *form.Model, opts recordFormOptions) *recordFormModal {
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
		kind:           kind,
		ref:            refCopy,
		about:          aboutCopy,
		draftKey:       recordDraftKey(kind, refCopy),
		isNew:          refCopy == nil,
		hiddenDefaults: maps.Clone(opts.hiddenDefaults),
		choices:        opts.choices,
		excludeChoices: opts.excludeChoices,
	}
	schema := a.schema(kind)
	fm.SubmitRow = true
	fm.Wrap = a.wrapInputs()
	if a.cfg != nil && a.cfg.Settings != nil {
		fm.ShowAllFields = !a.cfg.Settings.Bool(SetFormReactiveFields, true)
	}
	if rf.isNew {
		fm.SubmitLabel = "Create " + strings.ToLower(schema.Title)
	} else {
		fm.SubmitLabel = "Save " + strings.ToLower(schema.Title)
	}
	a.applyHarnessChoicesToForm(kind, refCopy, fm)
	applyFormChoiceFilters(fm, rf.choices, rf.excludeChoices)
	a.attachFormDrafts(fm, rf.draftKey)

	var mod *modal.FormModal
	mod = modal.NewFormModal(fm, modal.FormOptions{
		ID:          "form",
		Scope:       views.ScopeEdit,
		DraftBadges: true,
		RenderBody: func(m *form.Model, _ string) ui.ViewNode {
			var rec *data.Record
			if rf.ref != nil {
				rec, _ = a.Store.Get(*rf.ref)
			}
			panel := views.EditView(a.schema(rf.kind), rec, m, "form")
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
	fm.Wrap = a.wrapInputs()
	if a.cfg != nil && a.cfg.Settings != nil {
		fm.ShowAllFields = !a.cfg.Settings.Bool(SetFormReactiveFields, true)
	}
	if submitLabel == "" {
		submitLabel = "Run"
	}
	fm.SubmitLabel = submitLabel
	if draftKey != "" {
		a.attachFormDrafts(fm, draftKey)
	}
	var mod *modal.FormModal
	mod = modal.NewFormModal(fm, modal.FormOptions{
		ID:             "launch",
		Scope:          "launch",
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
					next, _ = a.modals.Close(next, "launch", modal.CloseSubmit)
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

// --- confirmation modal ---------------------------------------------------------

// openConfirm opens the shared confirmation overlay in ModeConfirm.
func (a *App) openConfirm(s core.State, opts modal.ConfirmOptions) core.State {
	if opts.ID == "" {
		opts.ID = "confirm"
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

// appQuit handles core.CmdAppQuit (Q / ctrl+c): honors the 4-case matrix of
// confirm.quit and session.promptSaveOnExit (when buffered events exist).
func (a *App) appQuit(s core.State, _ core.Command) core.StepResult {
	confirmQuit := a.cfg.Settings.Bool(SetConfirmQuit, true)
	if !confirmQuit {
		if a.shouldPromptSaveRecording() {
			a.say("y/enter saves & quits · n/esc quits without saving")
			return core.Next(a.pushModal(s, a.newSaveRecordingConfirmModal()))
		}
		return core.Next(s, core.QuitEffect{})
	}
	return core.Next(a.openConfirm(s, modal.ConfirmOptions{
		Title:        "Quit Matomeru",
		Message:      "Quit Matomeru? Your daemon panes will keep running in the background.",
		ConfirmLabel: "Confirm",
		OnConfirm: func() modal.Result {
			if a.shouldPromptSaveRecording() {
				return modal.Result{
					Push:      a.newSaveRecordingConfirmModal(),
					Status:    "y/enter saves & quits · n/esc quits without saving",
					SetStatus: true,
				}
			}
			return modal.Result{Effects: []core.Effect{core.QuitEffect{}}}
		},
	}))
}

// --- path picker modal ----------------------------------------------------------

// newPathPickerModal builds a PathPickerModal starting in the first readable
// directory among start (or its parent), the working directory, and home.
func (a *App) newPathPickerModal(start any, onPick func(entry pathpicker.DirEntry) modal.Result) (*modal.PathPickerModal, bool) {
	return a.newPathPickerModalFor(start, pathpicker.PickAny, onPick)
}

func (a *App) newPathPickerModalFor(start any, mode pathpicker.PickMode, onPick func(entry pathpicker.DirEntry) modal.Result) (*modal.PathPickerModal, bool) {
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
		if pp, err = pathpicker.New(fs, dir, mode); err == nil {
			break
		}
	}
	if pp == nil {
		a.say("no directory could be read")
		return nil, false
	}
	return modal.NewPathPickerModal(pp, modal.PathPickerOptions{
		ID:            "path",
		Scope:         "path",
		Mode:          ModePicker,
		FilterMode:    ModePickerFilter,
		StartInFilter: a.pickerStartInFilter(false),
		FocusID:       "path",
		WidthPct:      70,
		HeightPct:     70,
		OnPick: func(entry pathpicker.DirEntry) modal.Result {
			a.recordPathUse(entry)
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
	parentModal := a.Modal()
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
	a.say(pathPickerStatus)
	return a.pushModal(s, pm)
}

// createPickedRecord creates a new record of kind with title as its summary field
// for PickerCreateHook callbacks and reports a toast on success.
func (a *App) createPickedRecord(kind, title string) (data.Record, error) {
	schema := a.schema(kind)
	field := schema.SummaryField
	if field == "" {
		field = "title"
	}
	rec, _, err := a.Store.Create(data.CreateInput{
		Kind:   kind,
		Fields: map[string]any{field: title},
	}, User)
	if err != nil {
		a.fail("create "+kind, err)
		return data.Record{}, err
	}
	nounTitle := schema.Title
	if nounTitle == "" {
		nounTitle = kind
	}
	a.pushToast(fmt.Sprintf("Created %s %q", strings.ToLower(nounTitle), title), toast.Ok)
	return *rec, nil
}

// --- palette, help, and path modal openers --------------------------------------

func (a *App) paletteEntries() []palette.Entry {
	reg := core.DefaultRegistry()
	reg.Register(a.bind.specs...)
	schemas := a.Store.Registry().List()
	nounKinds := make([]string, 0, len(schemas))
	for _, sc := range schemas {
		nounKinds = append(nounKinds, sc.Kind)
	}
	var entries []palette.Entry
	inspectSpec := core.CommandSpec{ID: CmdInspectOpen, Description: "Inspect a registry the app runs on"}
	for _, sp := range reg.List() {
		if sp.ID == CmdInspectOpen {
			inspectSpec = sp
			if len(sp.Params) > 0 {
				inspectSpec.Params = slices.Clone(sp.Params)
			}
		}
		if internal(sp) {
			continue
		}
		if selfHosted, _ := sp.Meta["selfHosted"].(bool); selfHosted && !a.featuresPageActive() {
			continue
		}
		specCopy := sp
		if len(sp.Params) > 0 {
			specCopy.Params = slices.Clone(sp.Params)
		}
		if specCopy.ID == views.CmdNew {
			if len(specCopy.Params) == 0 {
				specCopy.Params = []core.ParamSpec{{Name: "kind", Type: core.ParamString, Default: data.KindTask}}
			}
			specCopy.Params[0].Enum = nounKinds
			if specCopy.Params[0].Description == "" {
				specCopy.Params[0].Description = "Noun kind to create"
			}
			if specCopy.Params[0].Default == nil {
				specCopy.Params[0].Default = data.KindTask
			}
		}
		item := picker.Item{ID: sp.ID, Title: sp.ID, Subtitle: sp.Description}
		var defaults map[string]any
		for _, b := range a.bind.bindings {
			if b.Command != sp.ID || len(b.Keys) == 0 {
				continue
			}
			dynamic := false
			for _, v := range b.Args {
				if str, ok := v.(string); ok && strings.HasPrefix(str, "$") {
					dynamic = true
				}
			}
			if !dynamic {
				defaults = maps.Clone(b.Args)
				item.Data = defaults
				item.Badges = []ui.Badge{{Text: strings.Join(b.Keys, " ")}}
				break
			}
		}
		entries = append(entries, palette.Entry{Spec: specCopy, Item: item, Defaults: defaults})
	}
	// The registries: "inspect actions", "inspect modes".
	for _, what := range []string{InspectActions, InspectModes} {
		defaults := map[string]any{"what": what}
		entries = append(entries, palette.Entry{
			Spec:     inspectSpec,
			Item:     picker.Item{ID: CmdInspectOpen + ":" + what, Title: "inspect " + what, Subtitle: "Look at the " + what + " the app runs on", Data: defaults},
			Defaults: defaults,
		})
	}
	// A space or dynamic Project Tab is a page.open with its ID: one palette row each.
	for i, tb := range a.topTabs() {
		defaults := map[string]any{"id": tb.id}
		title := "space." + tb.id
		desc := "Go to the " + tb.title + " space"
		if tb.dynamic {
			title = "tab." + tb.title
			desc = "Switch to project tab " + tb.title
		}
		entries = append(entries, palette.Entry{
			Spec: core.CommandSpec{ID: core.CmdPageOpen, Description: desc},
			Item: picker.Item{ID: "space." + tb.id, Title: title, Subtitle: desc,
				Data: defaults, Badges: []ui.Badge{{Text: fmt.Sprint(i + 1)}}},
			Defaults: defaults,
		})
	}
	return entries
}

func (a *App) paletteOpen(s core.State, _ core.Command) core.StepResult {
	pal := palette.New("Commands", a.paletteEntries())
	pal.Placeholder = "a command · !cmd runs a shell command"
	pal.Picker.PageSize = 10
	var pm *modal.PaletteModal
	pm = modal.NewPaletteModal(pal, modal.PaletteOptions{
		ID:               "palette",
		Scope:            "palette",
		WidthPct:         70,
		ExpandedWidthPct: 90,
		HeightPct:        75,
		PickerMode:       ModePicker,
		PickerFilterMode: ModePickerFilter,
		StartInFilter:    a.pickerStartInFilter(true),
		FormMode:         ModeForm,
		FormEditMode:     ModeFormEdit,
		OnRawQuery: func(query string) (modal.Result, bool) {
			line, shell := strings.CutPrefix(query, "!")
			if !shell {
				return modal.Result{}, false
			}
			line = strings.TrimSpace(line)
			if line == "" {
				return modal.Result{Handled: true, Status: "type a shell command after !", SetStatus: true}, true
			}
			a.afterModal(func(st core.State) core.State {
				return a.startShell(st, "", line, "")
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}, true
		},
		OnSubmit: func(entry palette.Entry, args map[string]any) modal.Result {
			a.nav.Registers.SetSpecial(core.RegCommand, entry.Item.Title)
			return modal.Result{
				Handled:     true,
				Close:       true,
				CloseReason: modal.CloseSubmit,
				Emit: []core.Command{{
					ID:     entry.Spec.ID,
					Args:   args,
					Source: "palette",
					Actor:  User,
				}},
			}
		},
		OnPickPath: func(req form.PathRequest, current any) modal.Result {
			field := req.Field
			pathMod, ok := a.newPathPickerModal(current, func(entry pathpicker.DirEntry) modal.Result {
				pm.SetPickedValue(field, entry.Path)
				return modal.Result{}
			})
			if !ok {
				return modal.Result{Handled: true}
			}
			return modal.Result{
				Handled:   true,
				Push:      pathMod,
				Status:    pathPickerStatus,
				SetStatus: true,
			}
		},
		OnPickRef: func(req form.RefRequest) modal.Result {
			field := req.Field
			return modal.Result{
				Handled: true,
				Push: a.newRefPickerModal(req, func(ref core.Ref) {
					pm.SetPickedValue(field, ref)
				}),
			}
		},
	})
	return core.Next(a.pushModal(s, pm))
}

func (a *App) helpOpen(s core.State, _ core.Command) core.StepResult {
	active := a.activeKeyBindings(s)
	items := make([]picker.Item, len(active))
	for i, b := range active {
		items[i] = picker.Item{ID: fmt.Sprint(i), Title: fmt.Sprintf("%-12s %s", b.keys, b.command), Subtitle: b.hint, Keywords: []string{b.scope, b.mode}, Data: b}
		if b.scope != "" {
			items[i].Badges = []ui.Badge{{Text: b.scope, Tone: "accent"}}
		}
	}
	pm := picker.New("Bindings · "+a.Engine.Mode(s), items)
	pm.Placeholder = "enter runs binding · / filters"
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       "help",
		PageSize: 10,
		OnConfirm: func(picked picker.Item) modal.Result {
			b, ok := picked.Data.(activeBinding)
			if !ok || b.rawCommand == "" {
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
			}
			return modal.Result{
				Handled:     true,
				Close:       true,
				CloseReason: modal.CloseSubmit,
				Emit: []core.Command{{
					ID:     b.rawCommand,
					Args:   maps.Clone(b.args),
					Source: "help",
					Actor:  User,
				}},
			}
		},
	})
	return core.Next(a.pushModal(s, m))
}

func (a *App) pathOpen(s core.State, c core.Command) core.StepResult {
	var target *core.Ref
	var start any
	if ref, ok := a.subject(s, c); ok {
		if rec, err := a.Store.Get(ref); err == nil {
			target = &ref
			for _, field := range []string{"defaultDir", "cwd", "filePath"} {
				if v := rec.Text(field); v != "" {
					start = v
					break
				}
			}
		}
	}
	return core.Next(a.openPath(s, "", target, start))
}
