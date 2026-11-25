package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/modal"
)

const (
	ComposerModeSend      = "send"
	ComposerModeQueue     = "queue"
	ComposerModeInterrupt = "interrupt"

	defaultInterruptDelay = 120 * time.Millisecond
	composerMaxFiles      = 500
	composerMaxDepth      = 4
	composerFileCacheTTL  = 15 * time.Second
)

var composerDeliveryModes = []string{ComposerModeSend, ComposerModeQueue, ComposerModeInterrupt}

var builtinSlashCommands = []composerCompletion{
	{Trigger: "/", Label: "/plan", Insert: "/plan", Detail: "step-by-step planning", Kind: "command"},
	{Trigger: "/", Label: "/compact", Insert: "/compact", Detail: "compact conversation context", Kind: "command"},
	{Trigger: "/", Label: "/clear", Insert: "/clear", Detail: "clear conversation", Kind: "command"},
	{Trigger: "/", Label: "/review", Insert: "/review", Detail: "review changes", Kind: "command"},
	{Trigger: "/", Label: "/help", Insert: "/help", Detail: "show available commands", Kind: "command"},
}

type composerCompletion struct {
	Trigger string // "@", "$", "/"
	Label   string
	Insert  string
	Detail  string
	Kind    string // "file", "skill", "agent", "command"
}

type cachedFileList struct {
	files    []string
	loadedAt time.Time
	loading  bool
}

// composerModal implements modal.Modal for the multi-line prompt composer overlay.
type composerModal struct {
	a *App

	paneID    string
	paneTitle string
	cwd       string
	in        *input.Model
	delivery  string
	draftKey  string
	draftIdx  int // -1 = live buffer, 0..N-1 = draft from DraftStore
	liveStash string

	compTrigger string
	compStart   int // rune offset of trigger character
	compQuery   string
	compItems   []composerCompletion
	compSel     int
	compOpen    bool
}

var _ modal.Modal = (*composerModal)(nil)

func (a *App) activeComposer() *composerModal {
	if a.modals == nil {
		return nil
	}
	m, _ := a.modals.Get("composer").(*composerModal)
	return m
}

func composerDraftKey(paneID string) string {
	return "composer:" + paneID
}

func bracketedPasteBytes(text string) []byte {
	return []byte("\x1b[200~" + text + "\x1b[201~\r")
}

// SetComposerSkills sets cached skills/agents for composer $ and / completion
// (useful for testing or external refresh without blocking the UI).
func (a *App) SetComposerSkills(items []composerCompletion) {
	a.panes.aiSkillsMu.Lock()
	a.panes.aiSkills = slices.Clone(items)
	a.panes.aiSkillsLoaded = true
	a.panes.aiSkillsLoading = false
	a.panes.aiSkillsMu.Unlock()
	if cm := a.activeComposer(); cm != nil {
		cm.updateCompletions()
	}
	a.viewMemo.gen++
}

func (a *App) queuedPromptCount(paneID string) int {
	a.panes.queuedMu.Lock()
	defer a.panes.queuedMu.Unlock()
	return len(a.panes.queuedPrompts[paneID])
}

func (a *App) queuedPromptsFor(paneID string) []string {
	a.panes.queuedMu.Lock()
	defer a.panes.queuedMu.Unlock()
	return slices.Clone(a.panes.queuedPrompts[paneID])
}

// paneCompose opens the on-demand multi-line prompt composer overlay for the
// selected pane.
func (a *App) paneCompose(s core.State, _ core.Command) core.StepResult {
	pane, ok := a.termPane(s)
	if !ok {
		a.say("no pane is selected")
		return core.Next(s)
	}
	in := input.New("", true)
	in.HandleKey("i") // start in insert mode ready to draft immediately

	cm := &composerModal{
		a:         a,
		paneID:    pane.ID,
		paneTitle: views.Title(a.schema(data.KindPane), *pane),
		cwd:       expandHome(pane.Text("cwd")),
		in:        in,
		delivery:  ComposerModeSend,
		draftKey:  composerDraftKey(pane.ID),
		draftIdx:  -1,
	}

	// Kick off non-blocking async prefetch for @file and $skill / /command.
	if cm.cwd != "" {
		a.ensureComposerFilesAsync(cm.cwd)
	}
	a.ensureAICatalogAsync()

	a.say("composer (%s) · ctrl+s submits · shift+tab mode · ctrl+p/n drafts · @file $skill /cmd", cm.delivery)
	return core.Next(a.pushModal(s, cm))
}

func (m *composerModal) ID() string      { return "composer" }
func (m *composerModal) Scope() string   { return "composer" }
func (m *composerModal) Mode() string    { return ModeComposer }
func (m *composerModal) FocusID() string { return composerInput }

// OnClose persists non-empty prompt drafts when the composer is cancelled or dropped.
func (m *composerModal) OnClose(reason modal.CloseReason) {
	switch reason {
	case modal.CloseCancel:
		if m.a.saveComposerDraft(m) {
			m.a.say("composer closed · draft saved (ctrl+p restores)")
		} else {
			m.a.say("composer closed")
		}
	case modal.CloseDropAll:
		m.a.saveComposerDraft(m)
	}
}

// Handle dispatches all commands while the composer modal is active.
func (m *composerModal) Handle(c core.Command) modal.Result {
	switch c.ID {
	case modal.CmdCancel:
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseCancel}

	case CmdComposerCancel:
		if m.compOpen {
			m.compOpen = false
			m.compItems = nil
			return modal.Result{Handled: true}
		}
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseCancel}

	case CmdComposerSubmit:
		text := strings.TrimSpace(m.in.Value())
		if text == "" {
			return modal.Result{Handled: true, Status: "prompt is empty", SetStatus: true}
		}
		pane, err := m.a.Store.Get(core.Ref{Kind: data.KindPane, ID: m.paneID})
		if err != nil {
			m.a.fail("composer pane", err)
			return modal.Result{Handled: true}
		}
		mode := m.delivery
		rawText := m.in.Value()
		if m.draftIdx >= 0 && m.a.cfg.drafts != nil {
			if m.a.cfg.drafts.Remove(m.draftKey, m.draftIdx) {
				m.a.saveDrafts()
			}
		}
		rec := *pane
		m.a.afterModal(func(st core.State) core.State {
			m.a.deliverComposerPrompt(rec, rawText, mode)
			return st
		})
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}

	case CmdComposerModeCycle:
		m.cycleDeliveryMode()
		return modal.Result{Handled: true}

	case CmdComposerComplete:
		if m.compOpen && len(m.compItems) > 0 {
			m.acceptCompletion()
			return modal.Result{Handled: true}
		}
		m.cycleDeliveryMode()
		return modal.Result{Handled: true}

	case CmdComposerEnter:
		if m.compOpen && len(m.compItems) > 0 {
			m.acceptCompletion()
			return modal.Result{Handled: true}
		}
		m.in.HandleKey("enter")
		m.updateCompletions()
		return modal.Result{Handled: true}

	case CmdComposerInsert:
		if txt := c.Arg("text"); txt != "" {
			m.in.HandleCommand(core.Command{ID: input.CmdInsert, Args: map[string]any{"text": txt}})
			m.updateCompletions()
		}
		return modal.Result{Handled: true}

	case CmdComposerBackspace:
		m.in.HandleKey("backspace")
		m.updateCompletions()
		return modal.Result{Handled: true}

	case CmdComposerUp:
		if m.compOpen && len(m.compItems) > 0 {
			m.compSel = (m.compSel - 1 + len(m.compItems)) % len(m.compItems)
			return modal.Result{Handled: true}
		}
		m.moveVertical(-1)
		return modal.Result{Handled: true}

	case CmdComposerDown:
		if m.compOpen && len(m.compItems) > 0 {
			m.compSel = (m.compSel + 1) % len(m.compItems)
			return modal.Result{Handled: true}
		}
		m.moveVertical(1)
		return modal.Result{Handled: true}

	case CmdComposerDraftPrev:
		if m.compOpen && len(m.compItems) > 0 {
			m.compSel = (m.compSel - 1 + len(m.compItems)) % len(m.compItems)
			return modal.Result{Handled: true}
		}
		m.cycleDraft(1)
		return modal.Result{Handled: true}

	case CmdComposerDraftNext:
		if m.compOpen && len(m.compItems) > 0 {
			m.compSel = (m.compSel + 1) % len(m.compItems)
			return modal.Result{Handled: true}
		}
		m.cycleDraft(-1)
		return modal.Result{Handled: true}

	default:
		if input.IsReadlineCommand(c.ID) {
			m.in.HandleCommand(c)
			m.updateCompletions()
			return modal.Result{Handled: true}
		}
		return modal.Result{}
	}
}

// View renders the prompt composer overlay with multi-line input, delivery mode
// tabs, draft badge, and @file/$skill//command completion drawer.
func (m *composerModal) View(_, _ int) ui.ViewNode {
	var modeParts []string
	for _, mode := range composerDeliveryModes {
		if mode == m.delivery {
			modeParts = append(modeParts, "[● "+mode+"]")
		} else {
			modeParts = append(modeParts, "  "+mode+" ")
		}
	}
	modeBar := line("composer.modes", " Delivery: "+strings.Join(modeParts, "  ")+"   (shift+tab cycles)", "accent")

	compact := m.a == nil || m.a.cfg == nil || m.a.cfg.Settings == nil || m.a.cfg.Settings.Bool(SetComposerCompact, true)
	wrap := m.a == nil || m.a.cfg == nil || m.a.cfg.Settings == nil || m.a.cfg.Settings.Bool(SetComposerWrap, true)

	inNode := m.in.View(composerInput)
	inNode.Border = true
	inNode.Title = "Prompt"
	inNode.Props["placeholder"] = "Type prompt… (@file, $skill, /command · enter newline · ctrl+s submit)"
	inNode.Props["wrap"] = wrap
	widthPct, heightPct, gap, compMaxH := 80, 70, 1, 8
	if compact {
		inNode.Box = layout.Constraints{Flex: 1, MinH: 4}
		widthPct, heightPct, gap, compMaxH = 62, 34, 0, 4
	} else {
		inNode.Box = layout.Constraints{Flex: 1, MinH: 6}
	}

	children := []ui.ViewNode{modeBar, inNode}

	if m.compOpen && len(m.compItems) > 0 {
		var compLines []ui.ViewNode
		for i, item := range m.compItems {
			prefix := "  "
			tone := ""
			if i == m.compSel {
				prefix = "▸ "
				tone = "accent"
			}
			lbl := fmt.Sprintf("%s%-24s %s", prefix, item.Label, clip(item.Detail, 36))
			compLines = append(compLines, line(fmt.Sprintf("composer.comp.%d", i), lbl, tone))
		}
		compPanel := ui.ViewNode{
			ID:       "composer.completions",
			Kind:     ui.KindPanel,
			Title:    fmt.Sprintf("Completions (%s)", m.compTrigger),
			Border:   true,
			Box:      layout.Constraints{FixedH: min(len(compLines)+2, compMaxH)},
			Children: []ui.ViewNode{{Kind: ui.KindVStack, Children: compLines}},
		}
		children = append(children, compPanel)
	}

	helpBar := line("composer.help", " ctrl+s send/submit · shift+tab mode · ctrl+p/n drafts · tab complete · esc close & save draft", "muted")
	children = append(children, helpBar)

	var badges []ui.Badge
	badges = append(badges, ui.Badge{Text: m.delivery, Tone: "accent"})
	if qn := m.a.queuedPromptCount(m.paneID); qn > 0 {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d queued", qn), Tone: "warning"})
	}
	if m.a.cfg.drafts != nil {
		if n := len(m.a.cfg.drafts.List(m.draftKey)); n > 0 {
			if m.draftIdx >= 0 {
				badges = append(badges, ui.Badge{Text: fmt.Sprintf("draft %d/%d", m.draftIdx+1, n), Tone: "warning"})
			} else {
				badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d drafts", n), Tone: "info"})
			}
		}
	}

	return modal.Overlay(modal.OverlaySpec{
		ID:        "composer",
		Scope:     "composer",
		Title:     "Compose · " + m.paneTitle,
		WidthPct:  widthPct,
		HeightPct: heightPct,
		Badges:    badges,
		Children:  []ui.ViewNode{{Kind: ui.KindVStack, Gap: gap, Children: children}},
	})
}

// saveComposerDraft saves non-empty prompt text into the persistent DraftStore.
func (a *App) saveComposerDraft(cm *composerModal) bool {
	if cm == nil || cm.in == nil || cm.draftKey == "" {
		return false
	}
	val := strings.TrimSpace(cm.in.Value())
	if val == "" {
		return false
	}
	if a.cfg.drafts == nil {
		a.cfg.drafts = form.NewDraftStore()
	}
	label := clip(strings.ReplaceAll(val, "\n", " "), 40)
	if !a.cfg.drafts.Save(cm.draftKey, map[string]any{"prompt": cm.in.Value(), "mode": cm.delivery}, label, a.now()) {
		return false
	}
	a.saveDrafts()
	return true
}

func setInputText(in *input.Model, value string) *input.Model {
	fresh := input.New(value, true)
	fresh.HandleKey("i")
	// Move cursor to end of text.
	rs := []rune(value)
	if len(rs) > 0 {
		fresh = input.New("", true)
		fresh.HandleKey("i")
		fresh.HandleCommand(core.Command{ID: input.CmdInsert, Args: map[string]any{"text": value}})
	}
	return fresh
}

func (m *composerModal) cycleDraft(delta int) {
	if m.a.cfg.drafts == nil {
		return
	}
	drafts := m.a.cfg.drafts.List(m.draftKey)
	if len(drafts) == 0 {
		m.a.say("no saved drafts for this pane")
		return
	}
	if m.draftIdx < 0 {
		if delta < 0 {
			m.a.say("already on current draft")
			return
		}
		m.liveStash = m.in.Value()
		m.draftIdx = 0
	} else {
		nextIdx := m.draftIdx + delta
		if nextIdx < 0 {
			m.draftIdx = -1
			m.in = setInputText(m.in, m.liveStash)
			m.updateCompletions()
			m.a.say("back to current edits")
			return
		}
		if nextIdx >= len(drafts) {
			nextIdx = len(drafts) - 1
		}
		m.draftIdx = nextIdx
	}
	d := drafts[m.draftIdx]
	text := data.Text(d.Values["prompt"])
	if dm := data.Text(d.Values["mode"]); slices.Contains(composerDeliveryModes, dm) {
		m.delivery = dm
	}
	m.in = setInputText(m.in, text)
	m.updateCompletions()
	m.a.say("draft %d of %d", m.draftIdx+1, len(drafts))
}

func (m *composerModal) cycleDeliveryMode() {
	i := slices.Index(composerDeliveryModes, m.delivery)
	m.delivery = composerDeliveryModes[(i+1)%len(composerDeliveryModes)]
	m.a.say("delivery mode: %s", m.delivery)
}

func (m *composerModal) moveVertical(delta int) {
	if m.in == nil {
		return
	}
	// Briefly step via normal-mode vertical motion then return to insert mode.
	m.in.HandleKey("esc")
	if delta < 0 {
		m.in.HandleKey("k")
	} else {
		m.in.HandleKey("j")
	}
	m.in.HandleKey("i")
}

// --- Delivery modes: send, queue, interrupt ------------------------------------

func (a *App) deliverComposerPrompt(pane data.Record, text, mode string) {
	switch mode {
	case ComposerModeQueue:
		a.enqueuePrompt(pane, text)
	case ComposerModeInterrupt:
		a.deliverPromptInterrupt(pane, text)
	default:
		a.deliverPromptNow(pane, text, "sent")
	}
}

func (a *App) deliverPromptNow(pane data.Record, text, verb string) {
	if a.panes.Host != nil && a.hasSession(pane.ID) {
		if err := a.panes.Host.Write(pane.ID, bracketedPasteBytes(text)); err != nil {
			a.fail("send prompt", err)
			return
		}
	} else {
		buf := a.termOf(pane.ID)
		for _, ln := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
			buf.lines = append(buf.lines, "❯ "+ln)
		}
		buf.lines = buf.lines[max(0, len(buf.lines)-200):]
	}
	if _, _, err := a.Store.RecordPaneInput(pane.Ref(), prompter); err != nil {
		a.fail("record prompt", err)
	}
	a.harvestPrompt(pane, text)
	a.say("%s prompt to %s", verb, views.Title(a.schema(data.KindPane), pane))
}

func (a *App) enqueuePrompt(pane data.Record, text string) {
	a.panes.queuedMu.Lock()
	if a.panes.queuedPrompts == nil {
		a.panes.queuedPrompts = map[string][]string{}
	}
	a.panes.queuedPrompts[pane.ID] = append(a.panes.queuedPrompts[pane.ID], text)
	count := len(a.panes.queuedPrompts[pane.ID])
	a.panes.queuedMu.Unlock()

	if a.isPaneReadyForQueue(pane.ID) {
		a.flushQueuedPrompts(pane.ID)
		return
	}
	a.say("queued prompt #%d for %s (delivers when idle/done)", count, views.Title(a.schema(data.KindPane), pane))
}

func (a *App) isPaneReadyForQueue(paneID string) bool {
	if len(a.pendingApprovalsFor(paneID)) > 0 {
		return false
	}
	pt := a.paneTelemetry(paneID)
	if pt == nil {
		return false
	}
	return pt.State == daemon.StateIdle || pt.State == daemon.StateDone
}

func (a *App) flushQueuedPrompts(paneID string) int {
	if !a.isPaneReadyForQueue(paneID) {
		return 0
	}
	a.panes.queuedMu.Lock()
	list := a.panes.queuedPrompts[paneID]
	if len(list) == 0 {
		a.panes.queuedMu.Unlock()
		return 0
	}
	delete(a.panes.queuedPrompts, paneID)
	a.panes.queuedMu.Unlock()

	pane, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: paneID})
	if err != nil {
		return 0
	}
	for _, prompt := range list {
		a.deliverPromptNow(*pane, prompt, "delivered queued")
	}
	return len(list)
}

func (a *App) flushAllQueuedPrompts() int {
	a.panes.queuedMu.Lock()
	ids := make([]string, 0, len(a.panes.queuedPrompts))
	for id, list := range a.panes.queuedPrompts {
		if len(list) > 0 {
			ids = append(ids, id)
		}
	}
	a.panes.queuedMu.Unlock()
	delivered := 0
	for _, id := range ids {
		delivered += a.flushQueuedPrompts(id)
	}
	return delivered
}

func (a *App) deliverPromptInterrupt(pane data.Record, text string) {
	a.panes.escapes++
	if a.panes.Host != nil && a.hasSession(pane.ID) {
		if err := a.panes.Host.Write(pane.ID, []byte{0x1b}); err != nil {
			a.fail("interrupt pane", err)
			return
		}
		delay := a.panes.interruptDelay
		if delay <= 0 {
			delay = defaultInterruptDelay
		}
		a.say("interrupting %s…", views.Title(a.schema(data.KindPane), pane))
		a.background(func(ctx context.Context) func(core.State) core.State {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return nil
			case <-timer.C:
			}
			_ = a.panes.Host.Write(pane.ID, bracketedPasteBytes(text))
			return func(s core.State) core.State {
				if _, _, err := a.Store.RecordPaneInput(pane.Ref(), prompter); err != nil {
					a.fail("record prompt", err)
				}
				a.harvestPrompt(pane, text)
				a.say("interrupted and sent prompt to %s", views.Title(a.schema(data.KindPane), pane))
				a.viewMemo.gen++
				return s
			}
		})
		return
	}
	// Synthetic pane: record immediately.
	a.deliverPromptNow(pane, text, "interrupted and sent")
}

// --- Async @file / $skill / /command completion --------------------------------

func detectComposerTrigger(value string, cursor int) (trigger string, start int, query string, ok bool) {
	rs := []rune(value)
	ci := min(max(cursor, 0), len(rs))
	// Scan backward to whitespace or start of text.
	i := ci
	for i > 0 && !unicode.IsSpace(rs[i-1]) {
		i--
	}
	if i >= ci {
		return "", 0, "", false
	}
	first := rs[i]
	if first != '@' && first != '$' && first != '/' {
		return "", 0, "", false
	}
	// "/" only triggers at start of line or after whitespace (avoid matching paths like foo/bar).
	return string(first), i, string(rs[i+1 : ci]), true
}

func (m *composerModal) updateCompletions() {
	if m.in == nil {
		return
	}
	trig, start, query, ok := detectComposerTrigger(m.in.Value(), m.in.Cursor())
	if !ok {
		m.compTrigger, m.compStart, m.compQuery, m.compItems, m.compSel, m.compOpen = "", 0, "", nil, 0, false
		return
	}
	m.compTrigger, m.compStart, m.compQuery = trig, start, query

	var candidates []composerCompletion
	switch trig {
	case "@":
		if m.cwd != "" {
			m.a.ensureComposerFilesAsync(m.cwd)
			candidates = m.a.cachedFileCompletions(m.cwd)
		}
	case "$":
		m.a.ensureAICatalogAsync()
		candidates = m.a.cachedSkillCompletions("$")
	case "/":
		m.a.ensureAICatalogAsync()
		candidates = m.a.cachedSkillCompletions("/")
	}

	lq := strings.ToLower(query)
	var filtered []composerCompletion
	for _, c := range candidates {
		if lq == "" || strings.Contains(strings.ToLower(c.Label), lq) || strings.Contains(strings.ToLower(c.Detail), lq) {
			filtered = append(filtered, c)
			if len(filtered) >= 12 {
				break
			}
		}
	}
	m.compItems = filtered
	m.compOpen = len(filtered) > 0
	if m.compSel >= len(filtered) {
		m.compSel = 0
	}
}

func (m *composerModal) acceptCompletion() {
	if !m.compOpen || len(m.compItems) == 0 {
		return
	}
	item := m.compItems[min(max(m.compSel, 0), len(m.compItems)-1)]
	rs := []rune(m.in.Value())
	ci := min(max(m.in.Cursor(), 0), len(rs))
	start := min(max(m.compStart, 0), ci)
	replacement := item.Insert + " "
	nextText := string(rs[:start]) + replacement + string(rs[ci:])
	nextCursor := start + len([]rune(replacement))

	fresh := input.New("", true)
	fresh.HandleKey("i")
	fresh.HandleCommand(core.Command{ID: input.CmdInsert, Args: map[string]any{"text": nextText}})
	// Position cursor right after replacement if there was trailing text.
	if tailLen := len([]rune(nextText)) - nextCursor; tailLen > 0 {
		for range tailLen {
			fresh.HandleCommand(core.Command{ID: input.CmdCharLeft})
		}
	}
	m.in = fresh
	m.compOpen = false
	m.compItems = nil
	m.compSel = 0
}

func (a *App) cachedFileCompletions(cwd string) []composerCompletion {
	a.panes.composerFilesMu.RLock()
	entry := a.panes.composerFiles[cwd]
	a.panes.composerFilesMu.RUnlock()
	out := make([]composerCompletion, len(entry.files))
	for i, rel := range entry.files {
		out[i] = composerCompletion{
			Trigger: "@",
			Label:   "@" + rel,
			Insert:  "@" + rel,
			Detail:  "file",
			Kind:    "file",
		}
	}
	return out
}

func (a *App) ensureComposerFilesAsync(cwd string) {
	if cwd == "" {
		return
	}
	now := a.now()
	a.panes.composerFilesMu.Lock()
	if a.panes.composerFiles == nil {
		a.panes.composerFiles = map[string]cachedFileList{}
	}
	entry := a.panes.composerFiles[cwd]
	if entry.loading || (!entry.loadedAt.IsZero() && now.Sub(entry.loadedAt) < composerFileCacheTTL) {
		a.panes.composerFilesMu.Unlock()
		return
	}
	entry.loading = true
	a.panes.composerFiles[cwd] = entry
	a.panes.composerFilesMu.Unlock()

	a.background(func(ctx context.Context) func(core.State) core.State {
		files := scanComposerFiles(ctx, cwd, composerMaxFiles, composerMaxDepth)
		return func(s core.State) core.State {
			a.panes.composerFilesMu.Lock()
			a.panes.composerFiles[cwd] = cachedFileList{files: files, loadedAt: a.now(), loading: false}
			a.panes.composerFilesMu.Unlock()
			if cm := a.activeComposer(); cm != nil && cm.cwd == cwd {
				cm.updateCompletions()
				a.viewMemo.gen++
			}
			return s
		}
	})
}

func scanComposerFiles(ctx context.Context, root string, maxFiles, maxDepth int) []string {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil
	}
	var out []string
	skipDirs := map[string]bool{
		".git": true, "node_modules": true, "vendor": true, ".hg": true, ".svn": true, ".cache": true,
	}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil || len(out) >= maxFiles {
			return fs.SkipAll
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			depth := strings.Count(rel, string(filepath.Separator)) + 1
			if depth > maxDepth {
				return fs.SkipDir
			}
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	slices.Sort(out)
	return out
}

func (a *App) cachedSkillCompletions(trigger string) []composerCompletion {
	a.panes.aiSkillsMu.RLock()
	skills := slices.Clone(a.panes.aiSkills)
	a.panes.aiSkillsMu.RUnlock()

	var out []composerCompletion
	seen := map[string]bool{}
	for _, s := range skills {
		name := strings.TrimLeft(s.Insert, "$/")
		if name == "" {
			continue
		}
		key := trigger + name
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, composerCompletion{
			Trigger: trigger,
			Label:   key,
			Insert:  key,
			Detail:  s.Detail,
			Kind:    s.Kind,
		})
	}
	if trigger == "/" {
		for _, b := range builtinSlashCommands {
			if !seen[b.Label] {
				seen[b.Label] = true
				out = append(out, b)
			}
		}
	}
	return out
}

func (a *App) ensureAICatalogAsync() {
	a.panes.aiSkillsMu.Lock()
	if a.panes.aiSkillsLoaded || a.panes.aiSkillsLoading {
		a.panes.aiSkillsMu.Unlock()
		return
	}
	a.panes.aiSkillsLoading = true
	a.panes.aiSkillsMu.Unlock()

	dirs := append(slices.Clone(a.opts.ExtensionDirs), a.cfg.Settings.Strings("extensionDirs", nil)...)
	for i, d := range dirs {
		dirs[i] = expandHome(d)
	}
	storeBinary := ""
	if a.Store != nil {
		if rec, err := a.Store.GetByField(data.KindExtension, "name", "o-ai"); err == nil && rec != nil {
			storeBinary = rec.Text("binary")
		}
	}
	cache := a.ext.readCache

	a.background(func(ctx context.Context) func(core.State) core.State {
		var discovered []composerCompletion
		seen := map[string]bool{}
		add := func(name, detail, kind string) {
			name = strings.TrimSpace(name)
			if name == "" || seen[kind+":"+name] {
				return
			}
			seen[kind+":"+name] = true
			discovered = append(discovered, composerCompletion{
				Trigger: "$",
				Label:   "$" + name,
				Insert:  name,
				Detail:  detail,
				Kind:    kind,
			})
		}

		// 1. Read from ReadCache first when available.
		for _, spec := range []struct {
			kind  string
			label string
		}{{"ai.skill", "skill"}, {"ai.agent", "agent"}} {
			if cache != nil {
				if snap, ok := cache.Get(spec.kind); ok {
					for _, rec := range snap.Items {
						name := rec.Text("name")
						if name == "" {
							name = rec.Text("title")
						}
						if name == "" {
							name = rec.ID
						}
						detail := rec.Text("description")
						if detail == "" {
							detail = spec.label
						}
						add(name, detail, spec.label)
					}
				}
			}
		}

		// 2. Otherwise query o-ai asynchronously without ever blocking the UI loop.
		if len(discovered) == 0 {
			if bin := findOAIBinary(dirs, storeBinary); bin != "" {
				for _, spec := range []struct {
					kind  string
					label string
				}{{"ai.skill", "skill"}, {"ai.agent", "agent"}} {
					pctx, cancel := context.WithTimeout(ctx, oaiDiscoverTimeout)
					raw, err := exec.CommandContext(pctx, bin, "query", "--kind", spec.kind).Output()
					cancel()
					if err != nil {
						continue
					}
					for _, item := range parseOAICatalogItems(raw, spec.label) {
						add(item.Insert, item.Detail, item.Kind)
					}
				}
			}
		}

		return func(s core.State) core.State {
			a.panes.aiSkillsMu.Lock()
			a.panes.aiSkills = discovered
			a.panes.aiSkillsLoaded = true
			a.panes.aiSkillsLoading = false
			a.panes.aiSkillsMu.Unlock()
			if cm := a.activeComposer(); cm != nil {
				cm.updateCompletions()
				a.viewMemo.gen++
			}
			return s
		}
	})
}

func parseOAICatalogItems(raw []byte, kindLabel string) []composerCompletion {
	var env struct {
		Items []struct {
			ID     string         `json:"id"`
			Name   string         `json:"name"`
			Title  string         `json:"title"`
			Fields map[string]any `json:"fields"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil
	}
	out := make([]composerCompletion, 0, len(env.Items))
	for _, it := range env.Items {
		name := it.Name
		if name == "" && it.Fields != nil {
			name = data.Text(it.Fields["name"])
		}
		if name == "" && it.Fields != nil {
			name = data.Text(it.Fields["slug"])
		}
		if name == "" {
			name = it.Title
		}
		if name == "" {
			name = it.ID
		}
		detail := kindLabel
		if it.Fields != nil {
			if d := data.Text(it.Fields["description"]); d != "" {
				detail = d
			} else if d := data.Text(it.Fields["summary"]); d != "" {
				detail = d
			}
		}
		if name != "" {
			out = append(out, composerCompletion{
				Trigger: "$",
				Label:   "$" + name,
				Insert:  name,
				Detail:  detail,
				Kind:    kindLabel,
			})
		}
	}
	return out
}
