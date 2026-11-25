package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/modal"
)

// ComposerMode names how a pane prompt is delivered.
type ComposerMode = string

const (
	ComposerModeSend      ComposerMode = "send"
	ComposerModeQueue     ComposerMode = "queue"
	ComposerModeInterrupt ComposerMode = "interrupt"

	defaultInterruptDelay = 120 * time.Millisecond
	composerMaxFiles      = 500
	composerMaxDepth      = 4
	composerFileCacheTTL  = 15 * time.Second

	effectTeaCmd = "tea_cmd"
)

var composerDeliveryModes = []ComposerMode{ComposerModeSend, ComposerModeQueue, ComposerModeInterrupt}

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
	Kind    string // "file", "context", "skill", "agent", "command"
}

type cachedFileList struct {
	files    []string
	loadedAt time.Time
	loading  bool
}

// richComposerSpec configures a reusable multi-line composer modal with
// @file/@ctx/@agent, $skill, and /command completion plus attachment chips.
type richComposerSpec struct {
	ID                   string
	Scope                string
	Title                string
	Subtitle             string
	Placeholder          string
	InputTitle           string
	Cwd                  string
	DraftKey             string
	PaneID               string
	ShowDeliveryModes    bool
	EnableProjectContext bool
	IsFirstPrompt        bool
	ContextTaskID        string
	OnSubmit             func(text string, attachments []data.Attachment, mode ComposerMode) (tea.Cmd, error)
	OnSubmitWithContext  func(text string, attachments []data.Attachment, projectCtx []data.Record, mode ComposerMode) (tea.Cmd, error)
}

// richComposerModal implements modal.Modal for both pane prompt composition and
// task note thread replies.
type richComposerModal struct {
	a *App

	spec        richComposerSpec
	paneID      string
	cwd         string
	in          *input.Model
	delivery    ComposerMode
	draftKey    string
	draftIdx    int // -1 = live buffer, 0..N-1 = draft from DraftStore
	liveStash   string
	attachments []data.Attachment
	projCtx     *composerProjectContext

	compTrigger string
	compStart   int // rune offset of trigger character
	compQuery   string
	compItems   []composerCompletion
	compSel     int
	compOpen    bool
}

var _ modal.Modal = (*richComposerModal)(nil)

func newRichComposerModal(a *App, spec richComposerSpec) *richComposerModal {
	if spec.ID == "" {
		spec.ID = "composer"
	}
	if spec.Scope == "" {
		spec.Scope = "composer"
	}
	if spec.InputTitle == "" {
		if spec.ShowDeliveryModes {
			spec.InputTitle = "Prompt"
		} else {
			spec.InputTitle = "Note"
		}
	}
	if spec.Placeholder == "" {
		spec.Placeholder = "Type here… (@file, @ctx, @agent, $skill, /command · enter newline · ctrl+s submit)"
	}

	in := input.New("", true)
	in.HandleKey("i") // start in insert mode ready to draft immediately

	m := &richComposerModal{
		a:        a,
		spec:     spec,
		paneID:   spec.PaneID,
		cwd:      expandHome(spec.Cwd),
		in:       in,
		delivery: ComposerModeSend,
		draftKey: spec.DraftKey,
		draftIdx: -1,
		projCtx:  newComposerProjectContext(a, spec),
	}
	if a != nil {
		if m.cwd != "" {
			a.ensureComposerFilesAsync(m.cwd)
		}
		a.ensureAICatalogAsync()
	}
	return m
}

func (a *App) activeRichComposer() *richComposerModal {
	if a == nil || a.modals == nil {
		return nil
	}
	if top, ok := a.modals.Top().(*richComposerModal); ok {
		return top
	}
	if m, ok := a.modals.Get("composer").(*richComposerModal); ok {
		return m
	}
	if m, ok := a.modals.Get(taskNoteComposerModalID).(*richComposerModal); ok {
		return m
	}
	return nil
}

func (a *App) activeComposer() *richComposerModal {
	return a.activeRichComposer()
}

// SetComposerSkills sets cached skills/agents for composer $, /, and @agent
// completion (useful for testing or external refresh without blocking the UI).
func (a *App) SetComposerSkills(items []composerCompletion) {
	a.panes.aiSkillsMu.Lock()
	a.panes.aiSkills = slices.Clone(items)
	a.panes.aiSkillsLoaded = true
	a.panes.aiSkillsLoading = false
	a.panes.aiSkillsMu.Unlock()
	if cm := a.activeRichComposer(); cm != nil {
		cm.updateCompletions()
	}
	a.viewMemo.gen++
}

func (m *richComposerModal) ID() string      { return m.spec.ID }
func (m *richComposerModal) Scope() string   { return m.spec.Scope }
func (m *richComposerModal) Mode() string    { return ModeComposer }
func (m *richComposerModal) FocusID() string { return composerInput }

func (m *richComposerModal) selectedProjectContext() []data.Record {
	if m == nil || m.projCtx == nil {
		return nil
	}
	return m.projCtx.selectedRecords()
}

// AddAttachment appends an attachment chip if not already attached by path.
func (m *richComposerModal) AddAttachment(att data.Attachment) bool {
	if att.Path == "" && att.Ref == "" {
		return false
	}
	for _, existing := range m.attachments {
		if existing.Path != "" && existing.Path == att.Path {
			return false
		}
	}
	if att.Label == "" && att.Path != "" {
		att.Label = filepath.Base(att.Path)
	}
	if att.Kind == "" {
		att.Kind = "file"
	}
	m.attachments = append(m.attachments, att)
	return true
}

// RemoveLastAttachment pops the most recently attached chip.
func (m *richComposerModal) RemoveLastAttachment() (data.Attachment, bool) {
	if len(m.attachments) == 0 {
		return data.Attachment{}, false
	}
	last := m.attachments[len(m.attachments)-1]
	m.attachments = m.attachments[:len(m.attachments)-1]
	return last, true
}

// OnClose persists non-empty drafts when the composer is cancelled or dropped.
func (m *richComposerModal) OnClose(reason modal.CloseReason) {
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

// Handle dispatches all commands while the rich composer modal is active.
func (m *richComposerModal) Handle(c core.Command) modal.Result {
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
		rawText := m.in.Value()
		trimmed := strings.TrimSpace(rawText)
		if trimmed == "" && len(m.attachments) == 0 {
			emptyMsg := "prompt is empty"
			if !m.spec.ShowDeliveryModes {
				emptyMsg = "note is empty"
			}
			return modal.Result{Handled: true, Status: emptyMsg, SetStatus: true}
		}
		mode := m.delivery
		atts := slices.Clone(m.attachments)
		var submitCmd tea.Cmd
		if m.spec.OnSubmitWithContext != nil {
			var err error
			submitCmd, err = m.spec.OnSubmitWithContext(rawText, atts, m.selectedProjectContext(), mode)
			if err != nil {
				m.a.fail("submit", err)
				return modal.Result{Handled: true}
			}
		} else if m.spec.OnSubmit != nil {
			var err error
			submitCmd, err = m.spec.OnSubmit(rawText, atts, mode)
			if err != nil {
				m.a.fail("submit", err)
				return modal.Result{Handled: true}
			}
		}
		if m.draftIdx >= 0 && m.draftKey != "" && m.a.cfg.drafts != nil {
			if m.a.cfg.drafts.Remove(m.draftKey, m.draftIdx) {
				m.a.saveDrafts()
			}
		}
		res := modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		if submitCmd != nil {
			res.Effects = append(res.Effects, core.CustomEffect{Kind: effectTeaCmd, Data: submitCmd})
		}
		return res

	case CmdComposerModeCycle:
		if m.spec.ShowDeliveryModes {
			m.cycleDeliveryMode()
		}
		return modal.Result{Handled: true}

	case CmdComposerComplete:
		if m.compOpen && len(m.compItems) > 0 {
			m.acceptCompletion()
			return modal.Result{Handled: true}
		}
		if m.spec.ShowDeliveryModes {
			m.cycleDeliveryMode()
		}
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

	case CmdComposerAttachClipboard:
		return m.handleAttachClipboard()

	case CmdComposerAttachFile:
		return m.handleAttachFile()

	case CmdComposerAttachRemove:
		if removed, ok := m.RemoveLastAttachment(); ok {
			return modal.Result{Handled: true, Status: "removed attachment " + removed.Label, SetStatus: true}
		}
		return modal.Result{Handled: true, Status: "no attachments to remove", SetStatus: true}

	case CmdComposerContextToggle:
		return m.handleContextToggle()

	case CmdComposerContextPick:
		return m.handleContextPick()

	default:
		if input.IsReadlineCommand(c.ID) {
			m.in.HandleCommand(c)
			m.updateCompletions()
			return modal.Result{Handled: true}
		}
		return modal.Result{}
	}
}

func (m *richComposerModal) handleAttachClipboard() modal.Result {
	filesRoot := ""
	if m.a != nil && m.a.Store != nil {
		filesRoot = m.a.Store.FilesRoot()
	}
	idPrefix := fmt.Sprintf("clip-%d", m.a.now().UnixNano())
	att, text, err := captureClipboardFunc(filesRoot, idPrefix)
	if err != nil {
		return modal.Result{Handled: true, Status: "clipboard: " + err.Error(), SetStatus: true}
	}
	if att.Path != "" || att.Ref != "" {
		if m.AddAttachment(att) {
			return modal.Result{Handled: true, Status: "attached " + att.Label, SetStatus: true}
		}
		return modal.Result{Handled: true, Status: "already attached " + att.Label, SetStatus: true}
	}
	if text != "" {
		m.in.HandleCommand(core.Command{ID: input.CmdInsert, Args: map[string]any{"text": text}})
		m.updateCompletions()
		return modal.Result{Handled: true, Status: "pasted clipboard text", SetStatus: true}
	}
	return modal.Result{Handled: true, Status: "clipboard is empty", SetStatus: true}
}

func (m *richComposerModal) handleAttachFile() modal.Result {
	return m.a.openComposerAttachFilePicker(m)
}

// View renders the rich composer overlay with multi-line input, optional
// delivery mode bar, attachment chip bar, and completion drawer.
func (m *richComposerModal) View(_, _ int) ui.ViewNode {
	compact := m.a == nil || m.a.cfg == nil || m.a.cfg.Settings == nil || m.a.cfg.Settings.Bool(SetComposerCompact, true)
	wrap := m.a == nil || m.a.cfg == nil || m.a.cfg.Settings == nil || m.a.cfg.Settings.Bool(SetComposerWrap, true)

	var children []ui.ViewNode
	if m.spec.ShowDeliveryModes {
		var modeParts []string
		for _, mode := range composerDeliveryModes {
			if mode == m.delivery {
				modeParts = append(modeParts, "[● "+mode+"]")
			} else {
				modeParts = append(modeParts, "  "+mode+" ")
			}
		}
		children = append(children, line("composer.modes", " Delivery: "+strings.Join(modeParts, "  ")+"   (shift+tab cycles)", "accent"))
	} else if m.spec.Subtitle != "" {
		children = append(children, line("composer.subtitle", " "+m.spec.Subtitle, "muted"))
	}

	if m.spec.EnableProjectContext && m.projCtx != nil {
		if banner := m.projCtx.bannerNode(); banner != nil {
			children = append(children, *banner)
		}
	}

	if len(m.attachments) > 0 {
		chips := make([]string, 0, len(m.attachments))
		for _, att := range m.attachments {
			lbl := att.Label
			if lbl == "" {
				lbl = filepath.Base(att.Path)
			}
			chips = append(chips, fmt.Sprintf("[📎 %s]", lbl))
		}
		children = append(children, line("composer.attachments", " Attachments: "+strings.Join(chips, " "), "info"))
	}

	inNode := m.in.View(composerInput)
	inNode.Border = true
	inNode.Title = m.spec.InputTitle
	inNode.Props["placeholder"] = m.spec.Placeholder
	inNode.Props["wrap"] = wrap
	widthPct, heightPct, gap, compMaxH := 80, 70, 1, 8
	if compact {
		inNode.Box = layout.Constraints{Flex: 1, MinH: 4}
		widthPct, heightPct, gap, compMaxH = 62, 34, 0, 4
	} else {
		inNode.Box = layout.Constraints{Flex: 1, MinH: 6}
	}
	children = append(children, inNode)

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

	helpText := " ctrl+s submit · ctrl+v attach/paste · ctrl+f file · ctrl+x drop attach · tab complete · esc close"
	if m.spec.ShowDeliveryModes {
		helpText = " ctrl+s send · shift+tab mode · ctrl+v/f/x attach · ctrl+p/n drafts · tab complete · esc close"
		if m.spec.EnableProjectContext && m.projCtx != nil && len(m.projCtx.allProjectCtx) > 0 {
			helpText = " ctrl+s send · shift+tab mode · ctrl+g/o ctx · ctrl+v/f/x attach · ctrl+p/n drafts · esc close"
		}
	}
	children = append(children, line("composer.help", helpText, "muted"))

	var badges []ui.Badge
	if m.spec.ShowDeliveryModes {
		badges = append(badges, ui.Badge{Text: m.delivery, Tone: "accent"})
		if m.paneID != "" && m.a != nil {
			if qn := m.a.queuedPromptCount(m.paneID); qn > 0 {
				badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d queued", qn), Tone: "warning"})
			}
		}
	}
	if len(m.attachments) > 0 {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("📎 %d", len(m.attachments)), Tone: "info"})
	}
	if m.spec.EnableProjectContext && m.projCtx != nil {
		if badge := m.projCtx.headerBadge(); badge != nil {
			badges = append(badges, *badge)
		}
	}
	if m.draftKey != "" && m.a != nil && m.a.cfg != nil && m.a.cfg.drafts != nil {
		if n := len(m.a.cfg.drafts.List(m.draftKey)); n > 0 {
			if m.draftIdx >= 0 {
				badges = append(badges, ui.Badge{Text: fmt.Sprintf("draft %d/%d", m.draftIdx+1, n), Tone: "warning"})
			} else {
				badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d drafts", n), Tone: "info"})
			}
		}
	}

	return modal.Overlay(modal.OverlaySpec{
		ID:        m.spec.ID,
		Scope:     m.spec.Scope,
		Title:     m.spec.Title,
		WidthPct:  widthPct,
		HeightPct: heightPct,
		Badges:    badges,
		Children:  []ui.ViewNode{{Kind: ui.KindVStack, Gap: gap, Children: children}},
	})
}

// formatPromptWithAttachments appends a compact [Attachments] section to text
// when attachments are present.
func formatPromptWithAttachments(text string, attachments []data.Attachment) string {
	if len(attachments) == 0 {
		return text
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(text, "\n"))
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	b.WriteString("[Attachments]\n")
	for _, att := range attachments {
		target := att.Path
		if target == "" {
			target = att.Ref
		}
		if target == "" {
			target = att.Label
		}
		fmt.Fprintf(&b, "- %s\n", target)
	}
	return strings.TrimRight(b.String(), "\n")
}

// saveComposerDraft saves non-empty composer text into the persistent DraftStore.
func (a *App) saveComposerDraft(cm *richComposerModal) bool {
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

func setInputText(_ *input.Model, value string) *input.Model {
	fresh := input.New("", true)
	fresh.HandleKey("i")
	if len([]rune(value)) > 0 {
		fresh.HandleCommand(core.Command{ID: input.CmdInsert, Args: map[string]any{"text": value}})
	}
	return fresh
}

func (m *richComposerModal) cycleDraft(delta int) {
	if m.a.cfg.drafts == nil || m.draftKey == "" {
		return
	}
	drafts := m.a.cfg.drafts.List(m.draftKey)
	if len(drafts) == 0 {
		m.a.say("no saved drafts")
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
	if m.spec.ShowDeliveryModes {
		if dm := data.Text(d.Values["mode"]); slices.Contains(composerDeliveryModes, dm) {
			m.delivery = dm
		}
	}
	m.in = setInputText(m.in, text)
	m.updateCompletions()
	m.a.say("draft %d of %d", m.draftIdx+1, len(drafts))
}

func (m *richComposerModal) cycleDeliveryMode() {
	i := slices.Index(composerDeliveryModes, m.delivery)
	m.delivery = composerDeliveryModes[(i+1)%len(composerDeliveryModes)]
	m.a.say("delivery mode: %s", m.delivery)
}

func (m *richComposerModal) moveVertical(delta int) {
	if m.in == nil {
		return
	}
	m.in.HandleKey("esc")
	if delta < 0 {
		m.in.HandleKey("k")
	} else {
		m.in.HandleKey("j")
	}
	m.in.HandleKey("i")
}

// --- Shared @file/@ctx/@agent, $skill, /command completion engine ---------------

func detectComposerTrigger(value string, cursor int) (trigger string, start int, query string, ok bool) {
	rs := []rune(value)
	ci := min(max(cursor, 0), len(rs))
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
	return string(first), i, string(rs[i+1 : ci]), true
}

func (m *richComposerModal) updateCompletions() {
	if m.in == nil || m.a == nil {
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
			candidates = append(candidates, m.a.cachedFileCompletions(m.cwd)...)
		}
		candidates = append(candidates, m.a.contextRecordCompletions(m.spec.ContextTaskID)...)
		candidates = append(candidates, m.a.agentHarnessCompletions()...)
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
		if lq == "" ||
			strings.Contains(strings.ToLower(c.Label), lq) ||
			strings.Contains(strings.ToLower(c.Insert), lq) ||
			strings.Contains(strings.ToLower(c.Detail), lq) {
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

func (m *richComposerModal) acceptCompletion() {
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

func (a *App) contextRecordCompletions(contextTaskID string) []composerCompletion {
	if a == nil || a.Store == nil {
		return nil
	}
	res, err := a.Store.Query(data.Query{Kind: data.KindContext})
	if err != nil || len(res.Items) == 0 {
		return nil
	}
	var rootTaskID string
	if contextTaskID != "" {
		if rec, err := a.Store.Get(core.Ref{Kind: data.KindTask, ID: contextTaskID}); err == nil && rec != nil {
			_, rootRef := a.resolveTaskRoot(*rec)
			rootTaskID = rootRef.ID
		}
	}
	var prioritized, rest []composerCompletion
	for _, rec := range res.Items {
		title := strings.TrimSpace(rec.Text("title"))
		if title == "" {
			title = strings.TrimSpace(rec.Text("value"))
		}
		if title == "" {
			title = rec.ID
		}
		detail := strings.TrimSpace(rec.Text("value"))
		if detail == "" {
			detail = rec.Text("contextKind")
		}
		if detail == "" {
			detail = "context"
		}
		item := composerCompletion{
			Trigger: "@",
			Label:   "@ctx:" + clip(title, 32),
			Insert:  "@context:" + rec.ID,
			Detail:  detail,
			Kind:    "context",
		}
		matchedTask := false
		if contextTaskID != "" {
			if tRef, ok := rec.RefField("task"); ok && tRef.Kind == data.KindTask {
				if tRef.ID == contextTaskID || (rootTaskID != "" && tRef.ID == rootTaskID) {
					matchedTask = true
				}
			}
		}
		if matchedTask {
			prioritized = append(prioritized, item)
		} else {
			rest = append(rest, item)
		}
	}
	return append(prioritized, rest...)
}

func (a *App) agentHarnessCompletions() []composerCompletion {
	if a == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []composerCompletion
	for _, h := range a.Harnesses() {
		id := strings.TrimSpace(h.ID)
		if id == "" || h.Shell || knownShellBinaries[strings.ToLower(id)] || seen[strings.ToLower(id)] {
			continue
		}
		seen[strings.ToLower(id)] = true
		detail := strings.TrimSpace(h.Name)
		if detail == "" {
			detail = "agent harness"
		}
		out = append(out, composerCompletion{
			Trigger: "@",
			Label:   "@agent:" + id,
			Insert:  "@agent:" + id,
			Detail:  detail,
			Kind:    "agent",
		})
	}
	a.panes.aiSkillsMu.RLock()
	skills := slices.Clone(a.panes.aiSkills)
	a.panes.aiSkillsMu.RUnlock()
	for _, s := range skills {
		if s.Kind != "agent" {
			continue
		}
		name := strings.TrimLeft(s.Insert, "$/@")
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		detail := s.Detail
		if detail == "" {
			detail = "agent"
		}
		out = append(out, composerCompletion{
			Trigger: "@",
			Label:   "@agent:" + name,
			Insert:  "@agent:" + name,
			Detail:  detail,
			Kind:    "agent",
		})
	}
	return out
}
