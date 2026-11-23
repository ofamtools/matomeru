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
			if fs := a.formOf(parentModal); fs != nil {
				fs.fm.SetValue(field, entry.Path)
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
