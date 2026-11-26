package app

import (
	"fmt"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/toast"
)

const (
	CmdComposerContextToggle = "composer.context.toggle"
	CmdComposerContextPick   = "composer.context.pick"

	composerContextPickerID = "composer.context"
)

func composerContextSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		{
			ID:          CmdComposerContextToggle,
			Description: "Toggle curated official project context injection for this prompt",
			Meta:        map[string]any{"internal": true},
		},
		{
			ID:          CmdComposerContextPick,
			Description: "Cherry-pick which project context items to include in this prompt",
			Meta:        map[string]any{"internal": true},
		},
	}
}

func composerContextBindings() []core.Binding {
	return []core.Binding{
		bind(ModeComposer, "", "ctrl+g", CmdComposerContextToggle, "toggle project context"),
		bind(ModeComposer, "", "ctrl+o", CmdComposerContextPick, "pick project context"),
		bind(ModePicker, composerContextPickerID, "space", picker.CmdConfirm, "toggle context item"),
	}
}

func (a *App) registerComposerContextHandlers() {
	for _, id := range []string{CmdComposerContextToggle, CmdComposerContextPick} {
		a.handle(id, a.handleModal)
	}
}

// composerProjectContext manages official and cherry-picked project context
// state for a single richComposerModal session.
type composerProjectContext struct {
	projectID     string
	allProjectCtx []data.Record
	officialIDs   map[string]bool
	selectedIDs   map[string]bool
	enabled       bool
	firstPrompt   bool
}

func newComposerProjectContext(a *App, spec richComposerSpec) *composerProjectContext {
	if !spec.EnableProjectContext {
		return nil
	}
	c := &composerProjectContext{
		officialIDs: map[string]bool{},
		selectedIDs: map[string]bool{},
		firstPrompt: spec.IsFirstPrompt,
	}
	if a == nil || a.Store == nil || strings.TrimSpace(spec.ContextTaskID) == "" {
		return c
	}
	projectID := a.Store.RootProjectID(spec.ContextTaskID)
	if projectID == "" {
		return c
	}
	c.projectID = projectID
	all, err := a.Store.ProjectContextAll(projectID)
	if err != nil || len(all) == 0 {
		return c
	}
	c.allProjectCtx = all
	for _, rec := range all {
		if rec.Bool("official") {
			c.officialIDs[rec.ID] = true
		}
	}
	autoInject := true
	if a.cfg != nil && a.cfg.Settings != nil {
		autoInject = a.cfg.Settings.Bool(SetContextAutoInject, true)
	}
	if autoInject && len(c.officialIDs) > 0 {
		for id := range c.officialIDs {
			c.selectedIDs[id] = true
		}
		c.enabled = true
	}
	return c
}

func (c *composerProjectContext) isSelected(id string) bool {
	return c != nil && c.enabled && c.selectedIDs[id]
}

// selectedRecords returns the ordered selected KindContext records when
// project context injection is enabled, or nil when opted out.
func (c *composerProjectContext) selectedRecords() []data.Record {
	if c == nil || !c.enabled || len(c.selectedIDs) == 0 {
		return nil
	}
	out := make([]data.Record, 0, len(c.selectedIDs))
	for _, rec := range c.allProjectCtx {
		if c.selectedIDs[rec.ID] {
			out = append(out, rec)
		}
	}
	return out
}

func (c *composerProjectContext) isDefaultOfficialSelection() bool {
	if c == nil || !c.enabled || len(c.officialIDs) == 0 || len(c.selectedIDs) != len(c.officialIDs) {
		return false
	}
	for id := range c.officialIDs {
		if !c.selectedIDs[id] {
			return false
		}
	}
	return true
}

// toggle flips project context inclusion for this prompt. Re-enabling when no
// items are currently selected restores the curated official set.
func (c *composerProjectContext) toggle() bool {
	if c == nil {
		return false
	}
	if c.enabled {
		c.enabled = false
		return false
	}
	if len(c.selectedIDs) == 0 {
		for id := range c.officialIDs {
			c.selectedIDs[id] = true
		}
	}
	c.enabled = len(c.selectedIDs) > 0
	return c.enabled
}

// toggleItem cherry-picks or removes a single project context item (official
// or non-official) for this prompt and updates enabled accordingly.
func (c *composerProjectContext) toggleItem(id string) bool {
	if c == nil || strings.TrimSpace(id) == "" {
		return false
	}
	if !c.enabled {
		c.selectedIDs = map[string]bool{}
	}
	if c.selectedIDs == nil {
		c.selectedIDs = map[string]bool{}
	}
	if c.selectedIDs[id] {
		delete(c.selectedIDs, id)
		c.enabled = len(c.selectedIDs) > 0
		return false
	}
	c.selectedIDs[id] = true
	c.enabled = true
	return true
}

func (c *composerProjectContext) headerBadge() *ui.Badge {
	if c == nil || len(c.allProjectCtx) == 0 {
		return nil
	}
	if sel := len(c.selectedRecords()); sel > 0 {
		return &ui.Badge{Text: fmt.Sprintf("★ %d ctx", sel), Tone: "accent"}
	}
	return &ui.Badge{Text: "ctx off", Tone: "muted"}
}

func (c *composerProjectContext) bannerNode() *ui.ViewNode {
	if c == nil || len(c.allProjectCtx) == 0 {
		return nil
	}
	firstHint := ""
	if c.firstPrompt {
		firstHint = "first prompt · "
	}
	selected := c.selectedRecords()
	var text, tone string
	if c.enabled && len(selected) > 0 {
		titles := make([]string, 0, len(selected))
		for _, rec := range selected {
			titles = append(titles, clip(contextRecordDisplayTitle(rec), 24))
		}
		summary := strings.Join(titles, " · ")
		if len(titles) > 3 {
			summary = fmt.Sprintf("%s · +%d more", strings.Join(titles[:3], " · "), len(titles)-3)
		}
		if c.isDefaultOfficialSelection() {
			text = fmt.Sprintf(" ★ Project Context (%d/%d official): %s  [%sctrl+g off · ctrl+o pick]",
				len(selected), len(c.officialIDs), summary, firstHint)
		} else {
			text = fmt.Sprintf(" ★ Project Context (%d selected): %s  [%sctrl+g off · ctrl+o pick]",
				len(selected), summary, firstHint)
		}
		tone = "accent"
	} else {
		if len(c.officialIDs) > 0 {
			text = fmt.Sprintf(" ○ Project Context: off (%d official available)  [%sctrl+g on · ctrl+o pick]",
				len(c.officialIDs), firstHint)
		} else {
			text = fmt.Sprintf(" ○ Project Context: off (%d project items)  [%sctrl+o pick]",
				len(c.allProjectCtx), firstHint)
		}
		tone = "muted"
	}
	n := line("composer.project_context", text, tone)
	return &n
}

func (m *richComposerModal) handleContextToggle() modal.Result {
	if !m.spec.EnableProjectContext || m.projCtx == nil || len(m.projCtx.allProjectCtx) == 0 {
		return modal.Result{Handled: true, Status: "no project context items available", SetStatus: true}
	}
	on := m.projCtx.toggle()
	if on {
		n := len(m.projCtx.selectedRecords())
		msg := fmt.Sprintf("project context enabled (%d item%s)", n, pluralS(n))
		return modal.Result{Handled: true, Status: msg, SetStatus: true}
	}
	if len(m.projCtx.officialIDs) == 0 && len(m.projCtx.selectedIDs) == 0 {
		return modal.Result{Handled: true, Status: "no official project context · press ctrl+o to pick", SetStatus: true}
	}
	return modal.Result{Handled: true, Status: "project context off for this prompt", SetStatus: true}
}

func (m *richComposerModal) handleContextPick() modal.Result {
	if !m.spec.EnableProjectContext || m.projCtx == nil || len(m.projCtx.allProjectCtx) == 0 {
		return modal.Result{Handled: true, Status: "no project context items available", SetStatus: true}
	}
	pm := m.newComposerContextPickerModal()
	if pm == nil {
		return modal.Result{Handled: true, Status: "no project context items available", SetStatus: true}
	}
	return modal.Result{
		Handled:   true,
		Push:      pm,
		Status:    "project context · enter/space toggles item · esc returns to composer",
		SetStatus: true,
	}
}

func (m *richComposerModal) openComposerContextPicker(s *core.State) {
	if m == nil || m.a == nil || s == nil {
		return
	}
	pm := m.newComposerContextPickerModal()
	if pm == nil {
		return
	}
	*s = m.a.pushModal(*s, pm)
}

func (m *richComposerModal) newComposerContextPickerModal() *modal.PickerModal {
	if m == nil || m.a == nil || m.projCtx == nil || len(m.projCtx.allProjectCtx) == 0 {
		return nil
	}
	buildItems := func() []picker.Item {
		items := make([]picker.Item, len(m.projCtx.allProjectCtx))
		for i, rec := range m.projCtx.allProjectCtx {
			title := contextRecordDisplayTitle(rec)
			kind := contextRecordKind(rec)
			check := "[ ]"
			if m.projCtx.isSelected(rec.ID) {
				check = "[✓]"
			}
			label := fmt.Sprintf("%s %s (%s)", check, title, kind)
			var badges []ui.Badge
			keywords := []string{title, kind, rec.Text("value"), rec.Text("notes"), rec.Text("source")}
			if rec.Bool("official") {
				label = fmt.Sprintf("%s ★ %s (%s)", check, title, kind)
				badges = append(badges, ui.Badge{Text: "official", Tone: "accent"})
				keywords = append(keywords, "official")
			} else {
				src := strings.TrimSpace(rec.Text("source"))
				if src == "" {
					src = "scraped"
				}
				badges = append(badges, ui.Badge{Text: src, Tone: "muted"})
			}
			badges = append(badges, ui.Badge{Text: kind, Tone: "info"})
			sub := strings.TrimSpace(rec.Text("value"))
			if kind == "code" {
				sub = singleLineContextText(sub)
			}
			if notes := strings.TrimSpace(rec.Text("notes")); notes != "" {
				if sub != "" {
					sub += " · " + clip(notes, 48)
				} else {
					sub = clip(notes, 48)
				}
			}
			items[i] = picker.Item{
				ID:       rec.ID,
				Title:    label,
				Subtitle: sub,
				Badges:   badges,
				Keywords: keywords,
				Data:     rec,
			}
		}
		return items
	}

	pm := picker.New("Project Context for Prompt", buildItems())
	pm.Placeholder = "filter project context · enter/space toggle · esc/q done"
	return m.a.newPickerModal(pm, modal.PickerOptions{
		ID:       composerContextPickerID,
		Scope:    composerContextPickerID,
		Title:    "Project Context for Prompt",
		PageSize: 10,
		Badges: func(_ *picker.Model) []ui.Badge {
			sel := len(m.projCtx.selectedRecords())
			tone := "accent"
			if sel == 0 {
				tone = "muted"
			}
			return []ui.Badge{
				{Text: fmt.Sprintf("%d/%d selected", sel, len(m.projCtx.allProjectCtx)), Tone: tone},
			}
		},
		OnConfirm: func(picked picker.Item) modal.Result {
			selIdx := pm.SelectedIdx
			nowOn := m.projCtx.toggleItem(picked.ID)
			pm.SetItems(buildItems())
			if filtered := pm.Filtered(); len(filtered) > 0 {
				pm.SelectedIdx = min(max(selIdx, 0), len(filtered)-1)
			}
			title := picked.ID
			if rec, ok := picked.Data.(data.Record); ok {
				title = contextRecordDisplayTitle(rec)
			}
			action := "excluded"
			if nowOn {
				action = "included"
			}
			msg := fmt.Sprintf("%s %s in prompt context", action, title)
			return modal.Result{
				Handled:   true,
				Status:    msg,
				SetStatus: true,
				Toast:     &modal.ToastSpec{Text: msg, Level: toast.Info},
			}
		},
	})
}

// formatPromptWithContext appends curated official/selected project context
// Markdown (when non-empty) and [Attachments] (when non-empty) to prompt.
func formatPromptWithContext(prompt string, attachments []data.Attachment, ctxRecords []data.Record) string {
	ctxBlock := strings.TrimSpace(data.FormatProjectContextMarkdown(ctxRecords))
	if ctxBlock == "" {
		return formatPromptWithAttachments(prompt, attachments)
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(prompt, "\n"))
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	b.WriteString(ctxBlock)
	return formatPromptWithAttachments(b.String(), attachments)
}

func contextRecordDisplayTitle(rec data.Record) string {
	if t := singleLineContextText(rec.Text("title")); t != "" {
		return t
	}
	if v := strings.TrimSpace(rec.Text("value")); v != "" {
		if contextRecordKind(rec) == "code" {
			return data.CodeContextTitle(v, rec.Text("language"))
		}
		return singleLineContextText(v)
	}
	return rec.ID
}

func contextRecordKind(rec data.Record) string {
	if k := strings.TrimSpace(rec.Text("contextKind")); k != "" {
		return k
	}
	return data.ClassifyContextKind(rec.Text("value"), "")
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
