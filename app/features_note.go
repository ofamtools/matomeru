package app

import (
	"errors"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/toast"
)

// Mode, modal, and command constants for inline and modal Features raw note capture.
const (
	ModeFeaturesNote = "features-note"

	featuresCaptureModalID = "features.capture.modal"

	CmdFeaturesCaptureModal  = "features.capture.modal"
	CmdFeaturesNoteEdit      = "features.note.edit"
	CmdFeaturesNoteInsert    = "features.note.insert"
	CmdFeaturesNoteBackspace = "features.note.backspace"
	CmdFeaturesNoteNewline   = "features.note.newline"
	CmdFeaturesNoteUp        = "features.note.up"
	CmdFeaturesNoteDown      = "features.note.down"
	CmdFeaturesNoteSubmit    = "features.note.submit"
	CmdFeaturesNoteCommit    = "features.note.commit"
	CmdFeaturesNoteCancel    = "features.note.cancel"
	CmdFeaturesNoteExpand    = "features.note.expand"
)

func featuresNoteSpecs() []core.CommandSpec {
	internal := func(id, desc string, params ...core.ParamSpec) core.CommandSpec {
		return core.CommandSpec{
			ID:          id,
			Description: desc,
			Params:      params,
			Meta:        map[string]any{"internal": true, "selfHosted": true},
		}
	}
	return []core.CommandSpec{
		{
			ID:          CmdFeaturesCaptureModal,
			Description: "Open the centered Quick Capture modal to record a raw feature thought",
			Tags:        []string{"editing"},
			Meta:        map[string]any{"selfHosted": true},
		},
		{
			ID:          CmdFeaturesNoteEdit,
			Description: "Focus and type directly in the inline Raw Captured Thoughts note box",
			Tags:        []string{"editing"},
			Meta:        map[string]any{"selfHosted": true},
		},
		internal(CmdFeaturesNoteInsert, "Insert text into the Features inline raw note box",
			core.ParamSpec{Name: "text", Type: core.ParamString, Description: "text to insert"}),
		internal(CmdFeaturesNoteBackspace, "Delete character before cursor in the Features inline raw note box"),
		internal(CmdFeaturesNoteNewline, "Insert a newline in the Features inline raw note box"),
		internal(CmdFeaturesNoteUp, "Move cursor up one line in the Features inline raw note box"),
		internal(CmdFeaturesNoteDown, "Move cursor down one line in the Features inline raw note box"),
		internal(CmdFeaturesNoteSubmit, "Save the Features inline raw note into Features Wishlist"),
		internal(CmdFeaturesNoteCommit, "Save any non-empty Features inline raw note and exit typing mode"),
		internal(CmdFeaturesNoteCancel, "Discard the current Features inline raw note draft and exit typing mode"),
		internal(CmdFeaturesNoteExpand, "Open the current Features inline raw note draft in the centered capture modal"),
	}
}

func featuresNoteBindings() []core.Binding {
	m, all := ModeFeaturesNote, ""
	return []core.Binding{
		{Mode: m, Class: core.ClassPrintable, Command: CmdFeaturesNoteInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		{Mode: m, Class: core.ClassPaste, Command: CmdFeaturesNoteInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		bind(m, all, "backspace", CmdFeaturesNoteBackspace, "erase"),
		hidden(bind(m, all, "shift+backspace", CmdFeaturesNoteBackspace, "erase")),
		bind(m, all, "enter", CmdFeaturesNoteNewline, "newline"),
		bind(m, all, "up", CmdFeaturesNoteUp, "up"),
		bind(m, all, "down", CmdFeaturesNoteDown, "down"),
		bind(m, all, "ctrl+s", CmdFeaturesNoteSubmit, "save note"),
		hidden(bind(m, all, "ctrl+enter", CmdFeaturesNoteSubmit, "save note")),
		bind(m, all, "esc", CmdFeaturesNoteCommit, "save & exit"),
		bind(m, all, "ctrl+c", CmdFeaturesNoteCancel, "cancel"),
		bind(m, all, "ctrl+g", CmdFeaturesNoteExpand, "bigger modal"),
	}
}

func (a *App) registerFeaturesNoteHandlers() {
	a.handle(CmdFeaturesCaptureModal, a.featuresCaptureModal)
	a.handle(CmdFeaturesNoteEdit, a.featuresNoteEdit)
	a.handle(CmdFeaturesNoteInsert, a.featuresNoteInsert)
	a.handle(CmdFeaturesNoteBackspace, a.featuresNoteBackspace)
	a.handle(CmdFeaturesNoteNewline, a.featuresNoteNewline)
	a.handle(CmdFeaturesNoteUp, a.featuresNoteMoveVertical(-1))
	a.handle(CmdFeaturesNoteDown, a.featuresNoteMoveVertical(1))
	a.handle(CmdFeaturesNoteSubmit, a.featuresNoteSubmit)
	a.handle(CmdFeaturesNoteCommit, a.featuresNoteCommit)
	a.handle(CmdFeaturesNoteCancel, a.featuresNoteCancel)
	a.handle(CmdFeaturesNoteExpand, a.featuresNoteExpand)
}

func (fc *FeaturesController) ensureNoteInput() *input.Model {
	if fc.noteInput == nil {
		in := input.New("", true)
		in.HandleKey("i")
		fc.noteInput = in
	}
	if fc.noteInput.Mode() != input.ModeInsert {
		fc.noteInput.HandleKey("i")
	}
	return fc.noteInput
}

func (a *App) bumpFeaturesNote() {
	a.featuresCtrl().noteGen++
	a.viewMemo.gen++
}

func (a *App) resetFeaturesNoteInput() {
	fc := a.featuresCtrl()
	in := input.New("", true)
	in.HandleKey("i")
	fc.noteInput = in
	a.bumpFeaturesNote()
}

func (a *App) featuresNoteBoxSpec(_ string, targetTitle string) views.FeaturesNoteBoxSpec {
	if a.featuresCaptureDockedEnabled() {
		return views.FeaturesNoteBoxSpec{}
	}
	fc := a.featuresCtrl()
	val, cur := "", 0
	if fc.noteInput != nil {
		val = fc.noteInput.Value()
		cur = fc.noteInput.Cursor()
	}
	wrap := a.wrapInputs()
	if a.cfg != nil && a.cfg.Settings != nil {
		wrap = a.cfg.Settings.Bool(SetComposerWrap, wrap)
	}
	return views.FeaturesNoteBoxSpec{
		Enabled:     true,
		Editing:     fc.noteEditing,
		Value:       val,
		Cursor:      cur,
		Wrap:        wrap,
		TargetLabel: strings.TrimSpace(targetTitle),
	}
}

func (a *App) featuresTargetLabel(activeKey string) string {
	section, target, _ := strings.Cut(strings.TrimSpace(activeKey), ":")
	switch section {
	case "page":
		if cat, ok := lookupFeaturesPageCatalog(target); ok && cat.Title != "" {
			return cat.Title
		}
		return views.Label(target)
	case "noun":
		if a.Store != nil {
			if sc, ok := a.Store.Registry().Get(target); ok {
				if lbl := firstNonEmpty(sc.Plural, sc.Title); lbl != "" {
					return lbl
				}
			}
		}
		if cat, ok := lookupFeaturesNounCatalog(target); ok && cat.Title != "" {
			return cat.Title
		}
		return views.Label(target)
	case "component":
		if comp, ok := lookupFeaturesComponent(target); ok && comp.View.Name != "" {
			return comp.View.Name
		}
		return target
	case "concept":
		return views.Label(target) + " concepts"
	case "group":
		if a.Store != nil && target != "" {
			if rec, err := a.Store.Get(core.Ref{Kind: data.KindTask, ID: target}); err == nil && rec != nil {
				return views.Title(a.schema(data.KindTask), *rec)
			}
		}
		return target
	default:
		return FeaturesWishlistTitle
	}
}

func splitThoughtTitleAndBody(raw string) (title, body string) {
	normalized := strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(normalized, "\n")
	titleIdx := -1
	for i, ln := range lines {
		if t := strings.TrimSpace(ln); t != "" {
			title = t
			titleIdx = i
			break
		}
	}
	if titleIdx < 0 {
		return "", ""
	}
	if titleIdx+1 < len(lines) {
		body = strings.TrimSpace(strings.Join(lines[titleIdx+1:], "\n"))
	}
	return title, body
}

// saveFeaturesRawThought creates a raw feature thought task under Features Wishlist,
// auto-tagging it to activeKey so it immediately appears under Raw Captured Thoughts.
func (a *App) saveFeaturesRawThought(activeKey, text string, attachments []data.Attachment) (*data.Record, error) {
	if a.Store == nil {
		return nil, errors.New("store is unavailable")
	}
	fullText := strings.TrimSpace(formatPromptWithAttachments(text, attachments))
	if fullText == "" {
		return nil, errors.New("note is empty")
	}
	title, body := splitThoughtTitleAndBody(fullText)
	if title == "" {
		return nil, errors.New("note is empty")
	}
	wid := a.ensureFeaturesWishlistTask()
	if wid == "" {
		return nil, fmt.Errorf("could not initialize %s", FeaturesWishlistTitle)
	}

	fields := map[string]any{
		"title":      title,
		"parent":     core.Ref{Kind: data.KindTask, ID: wid},
		"workStatus": "active",
	}
	if body != "" {
		fields["description"] = body
	}

	section, target, _ := strings.Cut(strings.TrimSpace(activeKey), ":")
	target = strings.TrimSpace(target)
	switch section {
	case "page":
		if target != "" {
			fields["targetPages"] = []string{target}
		}
	case "noun":
		if target != "" {
			fields["targetNouns"] = []string{target}
		}
	case "component":
		if target != "" {
			fields["targetComponents"] = []string{target}
		}
	case "concept":
		if target != "" {
			fields["targetConcepts"] = []string{target}
		}
	}

	rec, _, err := a.Store.Create(data.CreateInput{
		Kind:   data.KindTask,
		Fields: fields,
	}, User)
	if err != nil || rec == nil {
		if err == nil {
			err = errors.New("failed to create raw feature thought")
		}
		return nil, err
	}

	if section == "group" && target != "" {
		groupRef := core.Ref{Kind: data.KindTask, ID: target}
		if gRec, gErr := a.Store.Get(groupRef); gErr == nil && gRec != nil {
			_, _, _ = a.Store.Relate(data.RelateInput{
				From: rec.Ref(),
				Kind: data.RelAbout,
				To:   groupRef,
			}, User)
		}
	}

	a.viewMemo.gen++
	return rec, nil
}

func (a *App) openFeaturesCaptureModal(s core.State, initialText string) core.StepResult {
	if !a.featuresPageActive() {
		a.say("Features is inactive")
		return core.Next(s)
	}
	fc := a.featuresCtrl()
	activeKey := fc.activeItemKey()
	targetLabel := a.featuresTargetLabel(activeKey)

	_, cwd := a.detectMatomeruCheckout()
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}

	title := "Quick Capture · " + FeaturesWishlistTitle
	subtitle := "Saved under " + FeaturesWishlistTitle
	if targetLabel != "" && targetLabel != FeaturesWishlistTitle {
		title = "Quick Capture · " + targetLabel
		subtitle = fmt.Sprintf("Tagged for %s (%s) · saved under %s", targetLabel, activeKey, FeaturesWishlistTitle)
	}

	cm := newRichComposerModal(a, richComposerSpec{
		ID:                featuresCaptureModalID,
		Scope:             "composer",
		Title:             title,
		Subtitle:          subtitle,
		InputTitle:        "Raw Feature Thought",
		Placeholder:       "Capture raw feature thought… (enter newline · ctrl+s save · ctrl+v/f attach · esc close)",
		Cwd:               cwd,
		DraftKey:          "features.capture:" + activeKey,
		InitialText:       initialText,
		WidthPct:          78,
		HeightPct:         60,
		ShowDeliveryModes: false,
		OnSubmit: func(text string, attachments []data.Attachment, _ ComposerMode) (tea.Cmd, error) {
			rec, err := a.saveFeaturesRawThought(activeKey, text, attachments)
			if err != nil {
				return nil, err
			}
			savedTitle := views.Title(a.schema(data.KindTask), *rec)
			a.afterModal(func(st core.State) core.State {
				a.viewMemo.gen++
				msg := fmt.Sprintf("Captured raw thought %q", clip(savedTitle, 48))
				a.say("%s", msg)
				a.pushToast(msg, toast.Ok)
				return st
			})
			return nil, nil
		},
	})
	a.say("quick capture modal · type thought · ctrl+s saves · esc closes")
	return core.Next(a.pushModal(s, cm))
}

func (a *App) featuresCaptureModal(s core.State, _ core.Command) core.StepResult {
	return a.openFeaturesCaptureModal(s, "")
}

func (a *App) featuresNoteEdit(s core.State, _ core.Command) core.StepResult {
	if !a.featuresPageActive() {
		a.say("Features is inactive")
		return core.Next(s)
	}
	if a.featuresCaptureDockedEnabled() {
		return a.featuresQuickCapture(s, core.Command{})
	}
	fc := a.featuresCtrl()
	fc.ensureNoteInput()
	fc.noteEditing = true
	a.bumpFeaturesNote()
	s = withPage(s, SpaceFeatures, views.FeaturesThoughtInputID, ModeFeaturesNote)
	a.say("typing raw note · enter newline · ctrl+s save · esc save & exit · ctrl+c cancel · ctrl+g bigger modal")
	return core.Next(s)
}

func (a *App) featuresNoteInsertText(text string) {
	if text == "" {
		return
	}
	in := a.featuresCtrl().ensureNoteInput()
	in.HandleCommand(core.Command{ID: input.CmdInsert, Args: map[string]any{"text": text}})
	a.bumpFeaturesNote()
}

func (a *App) featuresNoteReadline(c core.Command) {
	in := a.featuresCtrl().ensureNoteInput()
	in.HandleCommand(c)
	a.bumpFeaturesNote()
}

func (a *App) featuresNoteInsert(s core.State, c core.Command) core.StepResult {
	a.featuresNoteInsertText(c.Arg("text"))
	return core.Next(s)
}

func (a *App) featuresNoteBackspace(s core.State, _ core.Command) core.StepResult {
	in := a.featuresCtrl().ensureNoteInput()
	in.HandleKey("backspace")
	a.bumpFeaturesNote()
	return core.Next(s)
}

func (a *App) featuresNoteNewline(s core.State, _ core.Command) core.StepResult {
	in := a.featuresCtrl().ensureNoteInput()
	in.HandleKey("enter")
	a.bumpFeaturesNote()
	return core.Next(s)
}

func (a *App) featuresNoteMoveVertical(delta int) func(core.State, core.Command) core.StepResult {
	return func(s core.State, _ core.Command) core.StepResult {
		in := a.featuresCtrl().ensureNoteInput()
		in.HandleKey("esc")
		if delta < 0 {
			in.HandleKey("k")
		} else {
			in.HandleKey("j")
		}
		in.HandleKey("i")
		a.bumpFeaturesNote()
		return core.Next(s)
	}
}

func (a *App) featuresNoteSubmit(s core.State, _ core.Command) core.StepResult {
	fc := a.featuresCtrl()
	raw := ""
	if fc.noteInput != nil {
		raw = fc.noteInput.Value()
	}
	if strings.TrimSpace(raw) == "" {
		a.say("note is empty")
		return core.Next(s)
	}
	rec, err := a.saveFeaturesRawThought(fc.activeItemKey(), raw, nil)
	if err != nil {
		a.fail("save raw note", err)
		return core.Next(s)
	}
	fc.noteEditing = false
	a.resetFeaturesNoteInput()
	msg := fmt.Sprintf("Captured raw thought %q", clip(views.Title(a.schema(data.KindTask), *rec), 48))
	a.say("%s", msg)
	a.pushToast(msg, toast.Ok)
	return core.Next(withMode(s, ModeNormal))
}

func (a *App) featuresNoteCommit(s core.State, _ core.Command) core.StepResult {
	fc := a.featuresCtrl()
	raw := ""
	if fc.noteInput != nil {
		raw = fc.noteInput.Value()
	}
	if strings.TrimSpace(raw) != "" {
		rec, err := a.saveFeaturesRawThought(fc.activeItemKey(), raw, nil)
		if err != nil {
			a.fail("save raw note", err)
			return core.Next(s)
		}
		msg := fmt.Sprintf("Captured raw thought %q", clip(views.Title(a.schema(data.KindTask), *rec), 48))
		a.say("%s", msg)
		a.pushToast(msg, toast.Ok)
	} else {
		a.say("")
	}
	fc.noteEditing = false
	a.resetFeaturesNoteInput()
	return core.Next(withMode(s, ModeNormal))
}

func (a *App) featuresNoteCancel(s core.State, _ core.Command) core.StepResult {
	fc := a.featuresCtrl()
	fc.noteEditing = false
	a.resetFeaturesNoteInput()
	a.say("raw note draft discarded")
	return core.Next(withMode(s, ModeNormal))
}

func (a *App) featuresNoteExpand(s core.State, _ core.Command) core.StepResult {
	fc := a.featuresCtrl()
	draft := ""
	if fc.noteInput != nil {
		draft = fc.noteInput.Value()
	}
	fc.noteEditing = false
	a.resetFeaturesNoteInput()
	s = withMode(s, ModeNormal)
	return a.openFeaturesCaptureModal(s, draft)
}
