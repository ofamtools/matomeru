package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
)

// Mode, modal, and command constants for the full-width Thread Show chat view,
// pinned inline composer, and fuzzy thread switcher picker.
const (
	ModeThreadsCompose = "threads-compose"

	threadsPickerModalID = "threads.picker"

	cmdThreadsOpenThread       = "threads.show"
	cmdThreadsBack             = "threads.back"
	cmdThreadsPick             = "threads.pick"
	cmdThreadsComposeStart     = "threads.compose.start"
	cmdThreadsComposeInsert    = "threads.compose.insert"
	cmdThreadsComposeBackspace = "threads.compose.backspace"
	cmdThreadsComposeNewline   = "threads.compose.newline"
	cmdThreadsComposeUp        = "threads.compose.up"
	cmdThreadsComposeDown      = "threads.compose.down"
	cmdThreadsComposeSend      = "threads.compose.send"
	cmdThreadsComposeExit      = "threads.compose.exit"
	cmdThreadsComposeCancel    = "threads.compose.cancel"
	cmdThreadsComposeExpand    = "threads.compose.expand"
)

func threadsChatSpecs() []core.CommandSpec {
	internal := func(id, desc string, params ...core.ParamSpec) core.CommandSpec {
		return core.CommandSpec{
			ID:          id,
			Description: desc,
			Params:      params,
			Meta:        map[string]any{"internal": true},
		}
	}
	return []core.CommandSpec{
		spec(cmdThreadsOpenThread, "Open the selected thread in full-width chat Show view"),
		spec(cmdThreadsBack, "Return from the open thread Show view to the Threads index"),
		spec(cmdThreadsPick, "Fuzzy-switch to any thread in the current scope"),
		spec(cmdThreadsComposeStart, "Focus and type a reply in the pinned inline thread composer", "editing"),
		internal(cmdThreadsComposeInsert, "Insert text into the inline thread composer",
			core.ParamSpec{Name: "text", Type: core.ParamString, Description: "text to insert"}),
		internal(cmdThreadsComposeBackspace, "Delete character before cursor in the inline thread composer"),
		internal(cmdThreadsComposeNewline, "Insert a newline in the inline thread composer"),
		internal(cmdThreadsComposeUp, "Move cursor up one line in the inline thread composer"),
		internal(cmdThreadsComposeDown, "Move cursor down one line in the inline thread composer"),
		internal(cmdThreadsComposeSend, "Send the inline thread composer reply to the thread and its agent"),
		internal(cmdThreadsComposeExit, "Leave typing mode and browse the chat while keeping the draft"),
		internal(cmdThreadsComposeCancel, "Clear the inline thread composer draft and leave typing mode"),
		internal(cmdThreadsComposeExpand, "Expand the inline thread composer draft into the rich composer modal"),
	}
}

func threadsChatBindings() []core.Binding {
	n, m, scope, all := ModeNormal, ModeThreadsCompose, views.ScopeThreads, ""
	return []core.Binding{
		// Normal mode on Threads Index & Show (declarative facts guards).
		when(bind(n, scope, "enter", cmdThreadsOpenThread, "open chat"), "facts.threadsIndexShow && !facts.threadOpen && facts.hasSelection"),
		hidden(when(bind(n, scope, "l", cmdThreadsOpenThread, "open chat"), "facts.threadsIndexShow && !facts.threadOpen && facts.hasSelection")),
		when(bind(n, scope, "enter", cmdThreadsComposeStart, "type reply"), "facts.threadsIndexShow && facts.threadOpen && facts.threadComposer"),
		when(bind(n, scope, "i", cmdThreadsComposeStart, "type reply"), "facts.threadsIndexShow && facts.threadOpen"),
		hidden(when(bind(n, scope, "a", cmdThreadsComposeStart, "type reply"), "facts.threadsIndexShow && facts.threadOpen")),
		hidden(when(bind(n, scope, "o", cmdThreadsComposeStart, "type reply"), "facts.threadsIndexShow && facts.threadOpen")),
		when(bind(n, scope, "r", cmdThreadsComposeStart, "reply"), "facts.threadsIndexShow && (facts.threadOpen || facts.hasSelection)"),
		when(bind(n, scope, "R", cmdThreadsReply, "rich reply modal"), "facts.hasSelection"),
		when(bind(n, scope, "esc", cmdThreadsBack, "back to threads"), "facts.threadsIndexShow && facts.threadOpen"),
		hidden(when(bind(n, scope, "h", cmdThreadsBack, "back to threads"), "facts.threadsIndexShow && facts.threadOpen")),
		when(bind(n, scope, "p", cmdThreadsPick, "switch thread"), "facts.threadsIndexShow"),

		// Inline bottom composer typing mode.
		{Mode: m, Class: core.ClassPrintable, Command: cmdThreadsComposeInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		{Mode: m, Class: core.ClassPaste, Command: cmdThreadsComposeInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		bind(m, all, "backspace", cmdThreadsComposeBackspace, "erase"),
		hidden(bind(m, all, "shift+backspace", cmdThreadsComposeBackspace, "erase")),
		bind(m, all, "enter", cmdThreadsComposeNewline, "newline"),
		bind(m, all, "up", cmdThreadsComposeUp, "up"),
		bind(m, all, "down", cmdThreadsComposeDown, "down"),
		bind(m, all, "ctrl+s", cmdThreadsComposeSend, "send reply"),
		hidden(bind(m, all, "ctrl+enter", cmdThreadsComposeSend, "send reply")),
		bind(m, all, "esc", cmdThreadsComposeExit, "browse chat"),
		bind(m, all, "ctrl+c", cmdThreadsComposeCancel, "clear draft"),
		bind(m, all, "ctrl+g", cmdThreadsComposeExpand, "rich modal"),
	}
}

func (a *App) registerThreadsChat() {
	a.handle(cmdThreadsOpenThread, a.threadsOpenThread)
	a.handle(cmdThreadsBack, a.threadsBack)
	a.handle(cmdThreadsPick, a.threadsPick)
	a.handle(cmdThreadsComposeStart, a.threadsComposeStart)
	a.handle(cmdThreadsComposeInsert, a.threadsComposeInsert)
	a.handle(cmdThreadsComposeBackspace, a.threadsComposeBackspace)
	a.handle(cmdThreadsComposeNewline, a.threadsComposeNewline)
	a.handle(cmdThreadsComposeUp, a.threadsComposeMoveVertical(-1))
	a.handle(cmdThreadsComposeDown, a.threadsComposeMoveVertical(1))
	a.handle(cmdThreadsComposeSend, a.threadsComposeSend)
	a.handle(cmdThreadsComposeExit, a.threadsComposeExit)
	a.handle(cmdThreadsComposeCancel, a.threadsComposeCancel)
	a.handle(cmdThreadsComposeExpand, a.threadsComposeExpand)
}

func (a *App) threadsViewKey() string {
	if a == nil || a.threads == nil {
		return ""
	}
	tc := a.threads
	return fmt.Sprintf("%t:%t:%s:%t:%d:%d",
		a.threadsIndexShowEnabled(),
		a.threadsIMessageStyleEnabled(),
		tc.openTaskID,
		tc.composing,
		tc.composeGen,
		tc.list.SelectedIdx,
	)
}

func (tc *threadsController) ensureThreadsComposer() *input.Model {
	if tc.composer == nil {
		in := input.New("", true)
		in.HandleKey("i")
		tc.composer = in
	}
	if tc.composer.Mode() != input.ModeInsert {
		tc.composer.HandleKey("i")
	}
	return tc.composer
}

func (tc *threadsController) saveCurrentDraft() {
	if tc == nil || tc.openTaskID == "" {
		return
	}
	if tc.draftsByTask == nil {
		tc.draftsByTask = map[string]string{}
	}
	val := ""
	if tc.composer != nil {
		val = tc.composer.Value()
	}
	if val == "" {
		delete(tc.draftsByTask, tc.openTaskID)
	} else {
		tc.draftsByTask[tc.openTaskID] = val
	}
}

func (tc *threadsController) loadDraftFor(taskID string) {
	in := input.New("", true)
	in.HandleKey("i")
	if tc.draftsByTask != nil {
		if draft := tc.draftsByTask[taskID]; draft != "" {
			in = setInputText(in, draft)
		}
	}
	tc.composer = in
}

func (a *App) bumpThreadsCompose() {
	if a.threads != nil {
		a.threads.composeGen++
	}
	a.viewMemo.gen++
}

func (a *App) resetThreadsComposer(taskID string) {
	tc := a.threads
	in := input.New("", true)
	in.HandleKey("i")
	tc.composer = in
	if taskID != "" && tc.draftsByTask != nil {
		delete(tc.draftsByTask, taskID)
	}
	a.bumpThreadsCompose()
}

func (a *App) threadsComposerSpec(openThread *data.AgentThread) views.ThreadComposerSpec {
	if openThread == nil {
		return views.ThreadComposerSpec{}
	}
	tc := a.threads
	val, cur := "", 0
	if tc.composer != nil {
		val = tc.composer.Value()
		cur = tc.composer.Cursor()
	}
	wrap := a.wrapInputs()
	if a.cfg != nil && a.cfg.Settings != nil {
		wrap = a.cfg.Settings.Bool(SetComposerWrap, wrap)
	}
	return views.ThreadComposerSpec{
		Enabled:     true,
		Editing:     tc.composing,
		Value:       val,
		Cursor:      cur,
		Wrap:        wrap,
		TargetLabel: views.ThreadAgentLabel(*openThread),
	}
}

// openThreadView opens taskID in the full-width Chat Show view, marking its
// unread messages read and focusing either the inline composer or the latest
// message card.
func (a *App) openThreadView(s core.State, taskID string, focusComposer bool) core.State {
	if taskID == "" {
		return s
	}
	tc := a.threads
	projectID := a.threadsScope(s)
	if tc.openTaskID != "" && tc.openTaskID != taskID {
		tc.saveCurrentDraft()
	}
	if tc.openTaskID != taskID {
		tc.loadDraftFor(taskID)
	}
	tc.openTaskID = taskID
	if a.Store != nil {
		if _, err := a.Store.MarkTaskNotesRead(taskID, a.now(), User); err != nil {
			a.fail("mark thread read", err)
		}
	}
	a.selectThread(projectID, taskID)

	if focusComposer {
		tc.ensureThreadsComposer()
		tc.composing = true
		a.bumpThreadsCompose()
		a.say("typing reply · enter newline · ctrl+s send · esc browse chat · ctrl+c clear · ctrl+g rich modal")
		return withMode(withFocus(s, views.ThreadsComposerInputID), ModeThreadsCompose)
	}

	tc.composing = false
	a.bumpThreadsCompose()
	targetFocus := views.ThreadsComposerInputID
	if a.Store != nil {
		if th, ok, err := a.Store.AgentThreadByID(taskID); err == nil && ok && len(th.Messages) > 0 {
			targetFocus = fmt.Sprintf("%s%d", views.ThreadsMsgPrefix, len(th.Messages)-1)
		}
	}
	a.say("thread chat · j/k traverse messages · i reply · p switch · esc back")
	return withMode(withFocus(s, targetFocus), ModeNormal)
}

func (a *App) threadsOpenThread(s core.State, _ core.Command) core.StepResult {
	th, ok := a.selectedThread(s)
	if !ok {
		a.say("no thread selected · n starts one")
		return core.Next(s)
	}
	return core.Next(a.openThreadView(s, th.TaskID, false))
}

func (a *App) threadsBack(s core.State, _ core.Command) core.StepResult {
	tc := a.threads
	prevID := tc.openTaskID
	tc.saveCurrentDraft()
	tc.openTaskID = ""
	tc.composing = false
	a.bumpThreadsCompose()
	projectID := a.threadsScope(s)
	a.selectThread(projectID, prevID)
	targetFocus := views.ThreadsListID
	if prevID != "" && slices.ContainsFunc(tc.visible, func(th data.AgentThread) bool { return th.TaskID == prevID }) {
		targetFocus = views.ThreadsItemPrefix + prevID
	} else if len(tc.visible) > 0 {
		targetFocus = views.ThreadsItemPrefix + tc.visible[0].TaskID
	}
	a.say("threads index")
	return core.Next(withMode(withFocus(s, targetFocus), ModeNormal))
}

func (a *App) threadsPick(s core.State, _ core.Command) core.StepResult {
	projectID := a.threadsScope(s)
	threads := a.threadsSnapshot(projectID)
	if len(threads) == 0 {
		a.say("no threads yet · n starts one")
		return core.Next(s)
	}
	now := a.now()
	currentID := a.threads.openTaskID
	if currentID == "" {
		if cur, ok := a.selectedThread(s); ok {
			currentID = cur.TaskID
		}
	}
	items := make([]picker.Item, 0, len(threads))
	for _, th := range threads {
		var badges []ui.Badge
		if th.UnreadCount > 0 {
			badges = append(badges, ui.Badge{Text: fmt.Sprintf("● %d", th.UnreadCount), Tone: "unread"})
		}
		if agent := views.ThreadAgentLabel(th); agent != "" {
			badges = append(badges, ui.Badge{Text: agent, Tone: "accent"})
		} else {
			badges = append(badges, ui.Badge{Text: "no agent yet", Tone: "muted"})
		}
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d msgs", len(th.Messages)), Tone: "muted"})
		if recency, tone := views.ThreadRecency(th.LastAt, now); recency != "" {
			badges = append(badges, ui.Badge{Text: recency, Tone: tone})
		}
		kw := []string{th.TaskID, th.ProjectTitle, views.ThreadAgentLabel(th)}
		for _, m := range th.Messages {
			kw = append(kw, m.Body)
		}
		items = append(items, picker.Item{
			ID:       th.TaskID,
			Title:    th.Title,
			Subtitle: views.ThreadSnippet(th, 120),
			Badges:   badges,
			Keywords: kw,
		})
	}
	pm := picker.New("Switch Thread", items)
	selectPickerItemID(pm, currentID)
	pm.Placeholder = "fuzzy search threads · enter open chat · esc close"
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       threadsPickerModalID,
		Scope:    "threads.picker",
		PageSize: 12,
		OnConfirm: func(picked picker.Item) modal.Result {
			targetID := picked.ID
			a.afterModal(func(st core.State) core.State {
				return a.openThreadView(st, targetID, false)
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	a.say("switch thread · type to filter · enter opens chat")
	return core.Next(a.pushModal(s, m))
}

func (a *App) threadsComposeStart(s core.State, _ core.Command) core.StepResult {
	tc := a.threads
	if tc.openTaskID == "" {
		th, ok := a.selectedThread(s)
		if !ok {
			a.say("no thread selected · n starts one")
			return core.Next(s)
		}
		return core.Next(a.openThreadView(s, th.TaskID, true))
	}
	tc.ensureThreadsComposer()
	tc.composing = true
	a.bumpThreadsCompose()
	a.say("typing reply · enter newline · ctrl+s send · esc browse chat · ctrl+c clear · ctrl+g rich modal")
	return core.Next(withMode(withFocus(s, views.ThreadsComposerInputID), ModeThreadsCompose))
}

func (a *App) threadsComposeInsertText(text string) {
	if text == "" {
		return
	}
	in := a.threads.ensureThreadsComposer()
	in.HandleCommand(core.Command{ID: input.CmdInsert, Args: map[string]any{"text": text}})
	a.threads.saveCurrentDraft()
	a.bumpThreadsCompose()
}

func (a *App) threadsComposeReadline(c core.Command) {
	in := a.threads.ensureThreadsComposer()
	in.HandleCommand(c)
	a.threads.saveCurrentDraft()
	a.bumpThreadsCompose()
}

func (a *App) threadsComposeInsert(s core.State, c core.Command) core.StepResult {
	a.threadsComposeInsertText(c.Arg("text"))
	return core.Next(s)
}

func (a *App) threadsComposeBackspace(s core.State, _ core.Command) core.StepResult {
	in := a.threads.ensureThreadsComposer()
	in.HandleKey("backspace")
	a.threads.saveCurrentDraft()
	a.bumpThreadsCompose()
	return core.Next(s)
}

func (a *App) threadsComposeNewline(s core.State, _ core.Command) core.StepResult {
	in := a.threads.ensureThreadsComposer()
	in.HandleKey("enter")
	a.threads.saveCurrentDraft()
	a.bumpThreadsCompose()
	return core.Next(s)
}

func (a *App) threadsComposeMoveVertical(delta int) func(core.State, core.Command) core.StepResult {
	return func(s core.State, _ core.Command) core.StepResult {
		in := a.threads.ensureThreadsComposer()
		in.HandleKey("esc")
		if delta < 0 {
			in.HandleKey("k")
		} else {
			in.HandleKey("j")
		}
		in.HandleKey("i")
		a.bumpThreadsCompose()
		return core.Next(s)
	}
}

func (a *App) threadsComposeSend(s core.State, _ core.Command) core.StepResult {
	tc := a.threads
	taskID := tc.openTaskID
	if taskID == "" {
		if th, ok := a.selectedThread(s); ok {
			taskID = th.TaskID
		}
	}
	if taskID == "" {
		a.say("no thread open")
		return core.Next(s)
	}
	raw := ""
	if tc.composer != nil {
		raw = tc.composer.Value()
	}
	if strings.TrimSpace(raw) == "" {
		a.say("message is empty")
		return core.Next(s)
	}
	note, err := a.Store.AppendTaskNote(data.AppendTaskNoteInput{
		TaskID: taskID, Body: raw, AuthorKind: "owner", AuthorLabel: "owner",
	}, User)
	if err != nil {
		a.fail("send thread reply", err)
		return core.Next(s)
	}
	if _, err := a.Store.MarkTaskNotesRead(taskID, a.now(), User); err != nil {
		a.fail("mark thread read", err)
	}
	tc.composing = false
	a.resetThreadsComposer(taskID)

	focusTarget := views.ThreadsComposerInputID
	if th, ok, err := a.Store.AgentThreadByID(taskID); err == nil && ok && len(th.Messages) > 0 {
		focusTarget = fmt.Sprintf("%s%d", views.ThreadsMsgPrefix, len(th.Messages)-1)
	}
	st := withMode(withFocus(s, focusTarget), ModeNormal)
	return core.Next(a.sendThreadMessage(st, taskID, note.ID, raw))
}

func (a *App) threadsComposeExit(s core.State, _ core.Command) core.StepResult {
	tc := a.threads
	tc.saveCurrentDraft()
	tc.composing = false
	a.bumpThreadsCompose()
	a.say("browsing chat · k/↑ scroll messages · i resume typing · esc back to threads")
	return core.Next(withMode(withFocus(s, views.ThreadsComposerInputID), ModeNormal))
}

func (a *App) threadsComposeCancel(s core.State, _ core.Command) core.StepResult {
	tc := a.threads
	tc.composing = false
	a.resetThreadsComposer(tc.openTaskID)
	a.say("reply draft cleared")
	return core.Next(withMode(withFocus(s, views.ThreadsComposerInputID), ModeNormal))
}

func (a *App) threadsComposeExpand(s core.State, _ core.Command) core.StepResult {
	tc := a.threads
	th, ok := a.selectedThread(s)
	if !ok {
		a.say("no thread selected")
		return core.Next(s)
	}
	draft := ""
	if tc.composer != nil {
		draft = tc.composer.Value()
	}
	tc.saveCurrentDraft()
	tc.composing = false
	a.bumpThreadsCompose()
	s = withMode(s, ModeNormal)

	taskID := th.TaskID
	st := a.threadComposerWithInitial(s, "Reply · "+th.Title, threadReplySubtitle(taskID), "threads.reply:"+taskID, taskID, taskID, draft,
		func(text string, attachments []data.Attachment) error {
			note, err := a.Store.AppendTaskNote(data.AppendTaskNoteInput{
				TaskID: taskID, Body: text, AuthorKind: "owner", AuthorLabel: "owner", Attachments: attachments,
			}, User)
			if err != nil {
				return err
			}
			if _, err := a.Store.MarkTaskNotesRead(taskID, a.now(), User); err != nil {
				a.fail("mark thread read", err)
			}
			a.resetThreadsComposer(taskID)
			a.afterModal(func(st core.State) core.State { return a.sendThreadMessage(st, taskID, note.ID, text) })
			return nil
		})
	a.say("rich reply modal · ctrl+s sends it to the thread's agent")
	return core.Next(st)
}
