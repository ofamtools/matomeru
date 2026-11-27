package app

import (
	"fmt"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/toast"
)

// Commands and modal identifiers for B1: Global Dictation / Compose Capture Modal.
const (
	CmdCaptureCompose    = "capture.compose"
	CmdCaptureSave       = "capture.save"
	CmdCaptureSendPinned = "capture.send.pinned"
	CmdCapturePickSend   = "capture.send.pick"

	captureComposeModalID    = "capture.compose"
	ScopeCaptureCompose      = "capture.compose"
	captureAgentPickerID     = "capture.agent.picker"
	captureComposeDraftKeyID = "capture:global"
)

func (a *App) captureComposeEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetCaptureComposeEnabled, true)
}

func captureModalCommandSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		{
			ID:          CmdCaptureCompose,
			Description: "Open the global Dictation / Compose Capture modal from anywhere",
			Params: []core.ParamSpec{
				{Name: "pane", Type: core.ParamString, Description: "optional target agent pane ID (defaults to active pinned agent)"},
				{Name: "captureId", Type: core.ParamString, Description: "optional existing capture ID to view or edit"},
				{Name: "source", Type: core.ParamString, Enum: data.CaptureSources, Default: data.CaptureSourceCompose, Description: "capture source (compose or dictation)"},
				{Name: "initialText", Type: core.ParamString, Description: "optional initial text to pre-fill"},
			},
		},
		{ID: CmdCaptureSave, Description: "Save capture to the persistent Captures log", Meta: map[string]any{"internal": true}},
		{ID: CmdCaptureSendPinned, Description: "Send capture to the active pinned agent and log it", Meta: map[string]any{"internal": true}},
		{ID: CmdCapturePickSend, Description: "Pick an agent pane, send capture, and log it", Meta: map[string]any{"internal": true}},
	}
}

func captureModalBindings() []core.Binding {
	n, all := ModeNormal, ""
	return []core.Binding{
		// Global anywhere triggers (g i automatically mirrors via ctrl+a g i in live terminal/editor).
		bind(n, all, "g i", CmdCaptureCompose, "dictation / capture"),
		bind(ModeGlobal, all, "alt+c", CmdCaptureCompose, "dictation / capture"),

		// Scoped actions inside the Capture Compose modal (reusing ModeComposer).
		bind(ModeComposer, ScopeCaptureCompose, "alt+s", CmdCaptureSave, "save to log"),
		bind(ModeComposer, ScopeCaptureCompose, "alt+g", CmdCaptureSendPinned, "send to pinned agent"),
		bind(ModeComposer, ScopeCaptureCompose, "alt+a", CmdCapturePickSend, "pick agent & send"),
	}
}

func (a *App) registerCaptureModal() {
	a.handle(CmdCaptureCompose, a.openCaptureCompose)
	a.handle(CmdCaptureSave, a.handleModal)
	a.handle(CmdCaptureSendPinned, a.handleModal)
	a.handle(CmdCapturePickSend, a.handleModal)
}

// captureComposeModal wraps richComposerModal so B1 reuses Ochazuke/Matomeru's
// multi-line input, readline editing, paste/attachments, @/$// completions,
// drafts, and modal.Overlay chrome with capture-specific submit controls.
type captureComposeModal struct {
	*richComposerModal
	captureID    string
	targetPaneID string
	projectID    string
	source       string
}

var _ modal.Modal = (*captureComposeModal)(nil)

func (m *captureComposeModal) richComposer() *richComposerModal { return m.richComposerModal }

func (m *captureComposeModal) Handle(c core.Command) modal.Result {
	switch c.ID {
	case CmdComposerSubmit:
		return m.submitDefault()
	case CmdCaptureSave:
		return m.submitSaveOnly()
	case CmdCaptureSendPinned:
		return m.submitSendToTarget()
	case CmdCapturePickSend:
		return m.submitPickAndSend()
	default:
		return m.richComposerModal.Handle(c)
	}
}

func (m *captureComposeModal) currentPayload() (string, bool) {
	raw := m.in.Value()
	if strings.TrimSpace(raw) == "" && len(m.attachments) == 0 {
		return "", false
	}
	return formatPromptWithContext(raw, m.attachments, m.selectedProjectContext()), true
}

func (m *captureComposeModal) clearDraftIfLoaded() {
	if m.draftIdx >= 0 && m.draftKey != "" && m.a.cfg.drafts != nil {
		if m.a.cfg.drafts.Remove(m.draftKey, m.draftIdx) {
			m.a.saveDrafts()
		}
	}
}

func (m *captureComposeModal) submitDefault() modal.Result {
	if m.resolveTargetPane() != nil {
		return m.submitSendToTarget()
	}
	return m.submitSaveOnly()
}

func (m *captureComposeModal) submitSaveOnly() modal.Result {
	payload, ok := m.currentPayload()
	if !ok {
		return modal.Result{Handled: true, Status: "capture is empty", SetStatus: true}
	}
	m.clearDraftIfLoaded()
	captureID, projectID, source := m.captureID, m.projectID, m.source
	m.a.afterModal(func(st core.State) core.State {
		m.a.saveCaptureRecord(captureID, payload, data.CaptureStatusCaptured, source, nil, projectID, "")
		return st
	})
	return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
}

func (m *captureComposeModal) submitSendToTarget() modal.Result {
	payload, ok := m.currentPayload()
	if !ok {
		return modal.Result{Handled: true, Status: "capture is empty", SetStatus: true}
	}
	target := m.resolveTargetPane()
	if target == nil {
		return m.submitPickAndSend()
	}
	m.clearDraftIfLoaded()
	paneCopy := *target
	captureID, projectID, source := m.captureID, m.projectID, m.source
	m.a.afterModal(func(st core.State) core.State {
		m.a.sendAndLogCapture(captureID, payload, source, paneCopy, projectID)
		return st
	})
	return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
}

func (m *captureComposeModal) submitPickAndSend() modal.Result {
	payload, ok := m.currentPayload()
	if !ok {
		return modal.Result{Handled: true, Status: "capture is empty", SetStatus: true}
	}
	m.clearDraftIfLoaded()
	captureID, projectID, source := m.captureID, m.projectID, m.source
	m.a.afterModal(func(st core.State) core.State {
		return m.a.openCaptureAgentPicker(st, captureID, payload, source, projectID)
	})
	return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
}

func (m *captureComposeModal) resolveTargetPane() *data.Record {
	if m.a == nil || m.a.Store == nil {
		return nil
	}
	if m.targetPaneID != "" {
		if rec, err := m.a.Store.Get(core.Ref{Kind: data.KindPane, ID: m.targetPaneID}); err == nil && rec != nil {
			return rec
		}
	}
	pane, _, _ := m.a.activePinnedPane()
	return pane
}

// --- Opening the Capture Modal -------------------------------------------------

func (a *App) openCaptureCompose(s core.State, c core.Command) core.StepResult {
	if !a.captureComposeEnabled() {
		a.say("capture compose is disabled (ui.captureComposeEnabled=false)")
		return core.Next(s)
	}
	captureID := strings.TrimSpace(c.Arg("captureId"))
	source := normalizeCaptureSource(c.Arg("source"))
	initialText := c.Arg("initialText")
	targetPaneID := strings.TrimSpace(c.Arg("pane"))
	projectID := a.activePortalProjectID(s)

	if captureID != "" && a.Store != nil {
		if rec, err := a.Store.Get(core.Ref{Kind: data.KindCapture, ID: captureID}); err == nil && rec != nil {
			if initialText == "" {
				initialText = rec.Text("body")
			}
			source = normalizeCaptureSource(rec.Text("source"))
			if targetPaneID == "" {
				if pRef, ok := rec.RefField("pane"); ok {
					targetPaneID = pRef.ID
				}
			}
			if projectID == "" {
				if projRef, ok := rec.RefField("project"); ok {
					projectID = projRef.ID
				}
			}
		}
	}

	targetPane := a.resolveCaptureInitialPane(targetPaneID)
	spec := a.buildCaptureComposerSpec(captureID, initialText, targetPane, projectID)
	cm := &captureComposeModal{
		richComposerModal: newRichComposerModal(a, spec),
		captureID:         captureID,
		targetPaneID:      paneRecordID(targetPane),
		projectID:         projectID,
		source:            source,
	}
	a.say("%s", captureModalStatusHint(targetPane, a.schema(data.KindPane)))
	return core.Next(a.pushModal(s, cm))
}

func normalizeCaptureSource(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), data.CaptureSourceDictation) {
		return data.CaptureSourceDictation
	}
	return data.CaptureSourceCompose
}

func (a *App) resolveCaptureInitialPane(paneID string) *data.Record {
	if paneID != "" && a.Store != nil {
		if rec, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: paneID}); err == nil && rec != nil {
			return rec
		}
	}
	pane, _, _ := a.activePinnedPane()
	return pane
}

func paneRecordID(pane *data.Record) string {
	if pane == nil {
		return ""
	}
	return pane.ID
}

func (a *App) buildCaptureComposerSpec(captureID, initialText string, target *data.Record, projectID string) richComposerSpec {
	title := "✦ Dictation / Compose Capture"
	if captureID != "" {
		title = "✦ Edit Capture"
	}
	subtitle := "No pinned agent · ctrl+s save log · alt+a pick agent & send · esc close"
	cwd := ""
	if target != nil {
		paneTitle := views.Title(a.schema(data.KindPane), *target)
		subtitle = fmt.Sprintf("★ Target: %s · ctrl+s send to pinned · alt+s save log · alt+a pick agent", paneTitle)
		cwd = target.Text("cwd")
	}
	return richComposerSpec{
		ID:            captureComposeModalID,
		Scope:         ScopeCaptureCompose,
		Title:         title,
		Subtitle:      subtitle,
		InputTitle:    "Capture / Dictation",
		Placeholder:   "Dictate or type multi-line text… (ctrl+s submit · alt+s save log · alt+g send pinned · alt+a pick agent)",
		Cwd:           cwd,
		DraftKey:      captureComposeDraftKeyID,
		PaneID:        paneRecordID(target),
		InitialText:   initialText,
		ContextTaskID: projectID,
	}
}

func captureModalStatusHint(target *data.Record, paneSchema data.Schema) string {
	if target != nil {
		return fmt.Sprintf("capture (★ %s) · ctrl+s send · alt+s save log · alt+a pick agent · esc draft", views.Title(paneSchema, *target))
	}
	return "capture · ctrl+s save log · alt+a pick agent & send · esc draft"
}

// --- Persistent Capture Log & Declarative Prompt Delivery ----------------------

func (a *App) saveCaptureRecord(captureID, body, status, source string, paneRef *core.Ref, projectID, errText string) (*data.Record, error) {
	if a.Store == nil {
		return nil, fmt.Errorf("store unavailable")
	}
	fields := map[string]any{
		"title":         data.CaptureTitleFromBody(body),
		"body":          body,
		"captureStatus": status,
		"source":        normalizeCaptureSource(source),
		"error":         strings.TrimSpace(errText),
	}
	if paneRef != nil && paneRef.ID != "" {
		fields["pane"] = *paneRef
	}
	if strings.TrimSpace(projectID) != "" {
		fields["project"] = core.Ref{Kind: data.KindTask, ID: projectID}
	}
	if status == data.CaptureStatusSent {
		fields["sentAt"] = a.now().UTC().Format(time.RFC3339)
	}
	var (
		rec *data.Record
		err error
	)
	if captureID != "" {
		rec, _, err = a.Store.Patch(data.PatchInput{Ref: core.Ref{Kind: data.KindCapture, ID: captureID}, Fields: fields}, User)
	} else {
		rec, _, err = a.Store.Create(data.CreateInput{Kind: data.KindCapture, Fields: fields}, User)
	}
	if err != nil {
		a.fail("save capture", err)
		return nil, err
	}
	a.viewMemo.gen++
	if status == data.CaptureStatusCaptured {
		a.say("saved capture: %s", rec.Text("title"))
		a.pushToast("capture saved to log", toast.Ok)
	}
	return rec, nil
}

// sendAndLogCapture delivers payload to pane via Matomeru's declarative prompt
// delivery path (reusing S4's attachWithPrompt / deliverPromptNow) and persists
// the capture record with status sent (or failed with error detail).
func (a *App) sendAndLogCapture(captureID, payload, source string, pane data.Record, projectID string) {
	paneRef := pane.Ref()
	if err := a.deliverCaptureToPane(pane, payload); err != nil {
		_, _ = a.saveCaptureRecord(captureID, payload, data.CaptureStatusFailed, source, &paneRef, projectID, err.Error())
		a.fail("send capture", err)
		return
	}
	_, _ = a.saveCaptureRecord(captureID, payload, data.CaptureStatusSent, source, &paneRef, projectID, "")
	paneTitle := views.Title(a.schema(data.KindPane), pane)
	a.say("sent capture to %s", paneTitle)
	a.pushToast("sent to "+paneTitle, toast.Ok)
}

func (a *App) deliverCaptureToPane(pane data.Record, payload string) error {
	if strings.TrimSpace(payload) == "" {
		return fmt.Errorf("empty capture payload")
	}
	if a.panes.Host != nil && !a.hasSession(pane.ID) {
		delivered, err := a.attachWithPrompt(pane, payload)
		if err != nil {
			return err
		}
		if delivered {
			return nil
		}
	}
	if a.panes.Host != nil && a.hasSession(pane.ID) {
		if err := a.panes.Host.Write(pane.ID, bracketedPasteBytes(payload)); err != nil {
			return err
		}
		_, _, _ = a.Store.RecordPaneInput(pane.Ref(), prompter)
		a.harvestPrompt(pane, payload)
		return nil
	}
	a.deliverPromptNow(pane, payload, "sent")
	return nil
}

// openCaptureAgentPicker opens a picker over live agent panes to send a capture.
func (a *App) openCaptureAgentPicker(s core.State, captureID, payload, source, projectID string) core.State {
	panes := a.capturePickerPanes()
	if len(panes) == 0 {
		_, _ = a.saveCaptureRecord(captureID, payload, data.CaptureStatusCaptured, source, nil, projectID, "")
		a.say("no agent panes found · capture saved to log")
		return s
	}
	items := a.captureAgentPickerItems(panes)
	pm := picker.New("Send Capture to Agent", items)
	pm.MatchMode = a.matchMode()
	pm.Placeholder = "pick an agent pane to receive the capture"
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       captureAgentPickerID,
		PageSize: 10,
		OnConfirm: func(picked picker.Item) modal.Result {
			pane, ok := picked.Data.(data.Record)
			if !ok {
				return modal.Result{Handled: true}
			}
			a.afterModal(func(st core.State) core.State {
				a.sendAndLogCapture(captureID, payload, source, pane, projectID)
				return st
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	a.say("pick agent pane · enter sends & logs capture · esc cancels")
	return a.pushModal(s, m)
}

func (a *App) capturePickerPanes() []data.Record {
	live := a.liveAgentPanes()
	if len(live) > 0 {
		return sortCapturePickerPanes(live)
	}
	if a.Store == nil {
		return nil
	}
	res, err := a.Store.Query(data.Query{Kind: data.KindPane, IncludeArchived: false})
	if err != nil {
		return nil
	}
	return sortCapturePickerPanes(res.Items)
}

func sortCapturePickerPanes(panes []data.Record) []data.Record {
	pinned := data.SortPinnedPanes(panes)
	seen := make(map[string]bool, len(pinned))
	out := make([]data.Record, 0, len(panes))
	for _, p := range pinned {
		seen[p.ID] = true
		out = append(out, p)
	}
	for _, p := range panes {
		if !seen[p.ID] {
			out = append(out, p)
		}
	}
	return out
}

func (a *App) captureAgentPickerItems(panes []data.Record) []picker.Item {
	paneSchema := a.schema(data.KindPane)
	items := make([]picker.Item, 0, len(panes))
	for _, p := range panes {
		ref := p.Ref()
		title := views.Title(paneSchema, p)
		badges := views.StatusBadges(paneSchema, p)
		if p.Bool("pinned") {
			title = "★ " + title
		}
		var sub []string
		if h := strings.TrimSpace(p.Text("harness")); h != "" {
			sub = append(sub, h)
		}
		if tRef, ok := p.RefField("task"); ok {
			sub = append(sub, a.title(tRef))
		}
		items = append(items, picker.Item{
			ID:       p.ID,
			Title:    title,
			Subtitle: strings.Join(sub, " · "),
			Ref:      &ref,
			Scope:    data.KindPane,
			Badges:   badges,
			Keywords: []string{title, p.ID, p.Text("harness")},
			Data:     p,
		})
	}
	return items
}
