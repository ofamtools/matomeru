package views

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// Threads page scope and node IDs.
const (
	// ScopeThreads is the page's own scope, apart from any noun's.
	ScopeThreads           = "threads.page"
	ThreadsPageID          = "threads.page"
	ThreadsListID          = "threads.list"
	ThreadsIndexScrollID   = "threads.index.scroll"
	ThreadsItemPrefix      = "threads.item."
	ThreadsTranscriptID    = "threads.transcript"
	ThreadsMsgPrefix       = "threads.msg."
	ThreadsComposerInputID = "threads.composer.input"
	ThreadsUnreadMarkID    = "threads.unread"
	ThreadsHintID          = "threads.hint"
	threadsRailRatio       = 0.42
	// ThreadsHint names the page's verbs under the legacy transcript.
	ThreadsHint = "r reply · n new thread · d dispatch to New or Existing Agent · m mark read"
	// ThreadsIndexHint names the page's verbs under the Google-style Index feed.
	ThreadsIndexHint = "enter open chat · r reply · n new thread · / filter · p switch · d dispatch · m mark read"
)

// ThreadComposerSpec is the pure data contract for the pinned bottom inline
// composer on the full-width Thread Show page.
type ThreadComposerSpec struct {
	Enabled     bool
	Editing     bool
	Value       string
	Cursor      int
	Placeholder string
	Wrap        bool
	TargetLabel string
}

// ThreadsPageSpec is the pure data contract of ThreadsPageView.
type ThreadsPageSpec struct {
	// Threads are the visible rows in display order (already filtered by the
	// caller); SelectedIdx picks the one whose transcript is shown in legacy
	// mode or highlighted in Index mode.
	Threads     []data.AgentThread
	SelectedIdx int
	FilterQuery string
	// Now is the clock recency is told against.
	Now time.Time
	// IMessageStyle shows the split rail and chat transcript in legacy mode,
	// or Markdown message bodies in Show mode; off uses plain text bodies.
	IMessageStyle bool
	// IndexShow renders Threads as a full-width Google-style snippet Index
	// that drills into a full-width Chat Show page with an inline composer.
	IndexShow bool
	// OpenTaskID, when non-empty in IndexShow mode, renders the full-width
	// Chat Show page for that thread instead of the Index feed.
	OpenTaskID string
	// Composer carries the pure value-object state of the pinned bottom
	// multiline reply box on the Thread Show page.
	Composer ThreadComposerSpec
	// ProjectTitle is set when the page is scoped to one project.
	ProjectTitle string
	// Section, when set, is the Project Portal section the page is embedded
	// in; it is published as the "section" fact.
	Section string
}

// ThreadsPageView presents the aggregated agent threads and the selected or
// open thread's transcript. It reads nothing but its spec.
func ThreadsPageView(spec ThreadsPageSpec) ui.ViewNode {
	filtering := strings.TrimSpace(spec.FilterQuery) != ""
	totalUnread := 0
	for _, th := range spec.Threads {
		totalUnread += th.UnreadCount
	}
	var openThread *data.AgentThread
	if spec.IndexShow && spec.OpenTaskID != "" {
		for i := range spec.Threads {
			if spec.Threads[i].TaskID == spec.OpenTaskID {
				openThread = &spec.Threads[i]
				break
			}
		}
	}
	facts := map[string]any{
		"count":            len(spec.Threads),
		"hasFilter":        filtering,
		"hasSelection":     false,
		"threadTaskId":     "",
		"threadPaneId":     "",
		"unreadCount":      0,
		"messageCount":     0,
		"totalUnread":      totalUnread,
		"imessageStyle":    spec.IMessageStyle,
		"threadsIndexShow": spec.IndexShow,
		"threadOpen":       openThread != nil,
		"threadComposing":  spec.Composer.Editing,
	}
	if spec.Section != "" {
		facts["section"] = spec.Section
	}
	var selected *data.AgentThread
	selectedIdx := -1
	if openThread != nil {
		selected = openThread
		facts["hasSelection"] = true
		facts["threadTaskId"] = openThread.TaskID
		facts["threadPaneId"] = openThread.PaneID
		facts["unreadCount"] = openThread.UnreadCount
		facts["messageCount"] = len(openThread.Messages)
	} else if i := spec.SelectedIdx; i >= 0 && i < len(spec.Threads) {
		selected, selectedIdx = &spec.Threads[i], i
		facts["hasSelection"] = true
		facts["threadTaskId"] = selected.TaskID
		facts["threadPaneId"] = selected.PaneID
		facts["unreadCount"] = selected.UnreadCount
		facts["messageCount"] = len(selected.Messages)
	}

	var children []ui.ViewNode
	switch {
	case spec.IndexShow && openThread != nil:
		children = threadsShowChildren(spec, *openThread)
	case spec.IndexShow:
		children = threadsIndexChildren(spec, totalUnread, filtering)
	default:
		rail := threadsRail(spec, selectedIdx, filtering)
		transcript := threadsTranscript(selected, spec.Now, spec.IMessageStyle)
		children = []ui.ViewNode{threadsHeader(spec, totalUnread)}
		if filtering {
			children = append(children, fixed(text("threads.filter", " /"+spec.FilterQuery, "accent"), 1))
		}
		if spec.IMessageStyle {
			children = append(children, ui.ViewNode{
				ID: "threads.split", Kind: ui.KindSplit, Ratio: threadsRailRatio, Gap: 1,
				Box:      layout.Constraints{Flex: 1},
				Children: []ui.ViewNode{rail, transcript},
			})
		} else {
			children = append(children, rail, transcript)
		}
	}
	return ui.ViewNode{
		ID:       ThreadsPageID,
		Kind:     ui.KindVStack,
		Scope:    ScopeThreads,
		Box:      layout.Constraints{Flex: 1},
		Facts:    facts,
		Children: children,
	}
}

func threadsIndexChildren(spec ThreadsPageSpec, totalUnread int, filtering bool) []ui.ViewNode {
	children := []ui.ViewNode{threadsHeader(spec, totalUnread)}
	if filtering {
		children = append(children, fixed(text("threads.filter", " /"+spec.FilterQuery, "accent"), 1))
	}
	filterSpec := &ui.FilterSpec{
		ID:     ThreadsListID,
		Prefix: "threads ",
		Fields: []string{"title", "agent", "project", "message"},
	}
	if len(spec.Threads) == 0 {
		empty := "No threads yet · n starts a new thread with a new or existing agent"
		if filtering {
			empty = "No threads match the filter."
		}
		children = append(children, ui.ViewNode{
			ID:        ThreadsListID,
			Kind:      ui.KindText,
			Scope:     ScopeThreads,
			Focusable: true,
			Filter:    filterSpec,
			Tone:      "muted",
			Box:       layout.Constraints{Flex: 1},
			Props:     map[string]any{"text": " " + empty},
		})
	} else {
		cards := make([]ui.ViewNode, 0, len(spec.Threads))
		for _, th := range spec.Threads {
			cards = append(cards, threadIndexCard(th, spec.Now))
		}
		children = append(children, ui.ViewNode{
			ID:     ThreadsIndexScrollID,
			Kind:   ui.KindScroll,
			Box:    layout.Constraints{Flex: 1},
			Filter: filterSpec,
			Props: map[string]any{
				"scrollKey": "threads.index|" + spec.Section + "|" + spec.ProjectTitle,
			},
			Children: []ui.ViewNode{{
				ID:       "threads.index.list",
				Kind:     ui.KindVStack,
				Gap:      1,
				Children: cards,
			}},
		})
	}
	children = append(children, fixed(text(ThreadsHintID, " "+ThreadsIndexHint, "muted"), 1))
	return children
}

func threadIndexCard(th data.AgentThread, now time.Time) ui.ViewNode {
	var badges []ui.Badge
	if th.UnreadCount > 0 {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("● %d unread", th.UnreadCount), Tone: "unread"})
	}
	if p := strings.TrimSpace(th.ProjectTitle); p != "" {
		badges = append(badges, ui.Badge{Text: p, Tone: "info"})
	}
	if agent := ThreadAgentLabel(th); agent != "" {
		badges = append(badges, ui.Badge{Text: agent, Tone: "accent"})
	} else {
		badges = append(badges, ui.Badge{Text: "no agent yet", Tone: "muted"})
	}
	badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d msgs", len(th.Messages)), Tone: "muted"})
	if recency, recencyTone := ThreadRecency(th.LastAt, now); recency != "" {
		badges = append(badges, ui.Badge{Text: recency, Tone: recencyTone})
	}

	var refCopy *core.Ref
	if th.TaskRef.ID != "" {
		r := th.TaskRef
		refCopy = &r
	}
	tone := ""
	if th.UnreadCount > 0 {
		tone = "unread"
	}
	return ui.ViewNode{
		ID:        ThreadsItemPrefix + th.TaskID,
		Kind:      ui.KindCard,
		Scope:     ScopeThreads,
		Ref:       refCopy,
		Focusable: true,
		Unread:    th.UnreadCount > 0,
		Title:     th.Title,
		Tone:      tone,
		Badges:    badges,
		Props:     map[string]any{"summary": buildThreadSnippet(th, 260)},
		Facts: map[string]any{
			"threadItem":   true,
			"threadTaskId": th.TaskID,
			"threadPaneId": th.PaneID,
			"unreadCount":  th.UnreadCount,
			"messageCount": len(th.Messages),
			"hasSelection": true,
		},
	}
}

func threadsShowChildren(spec ThreadsPageSpec, th data.AgentThread) []ui.ViewNode {
	var line spanLine
	line.write(" ← esc/h Threads  ", "muted")
	line.write("· ", "muted")
	line.write(th.Title, "accent")
	if agent := ThreadAgentLabel(th); agent != "" {
		line.write(" · ", "muted")
		line.write(agent, "state.working")
	} else {
		line.write(" · no agent yet", "muted")
	}
	line.write(fmt.Sprintf(" · %d msgs", len(th.Messages)), "muted")
	if th.UnreadCount > 0 {
		line.write("  ", "")
		line.write(fmt.Sprintf(" %d unread ", th.UnreadCount), "badge.warning")
	}
	header := fixed(ui.ViewNode{
		ID:    "threads.show.header",
		Kind:  ui.KindText,
		Props: map[string]any{"text": line.String(), "spans": line.spans},
	}, 1)

	var panelBadges []ui.Badge
	if agent := ThreadAgentLabel(th); agent != "" {
		panelBadges = append(panelBadges, ui.Badge{Text: agent, Tone: "accent"})
	} else {
		panelBadges = append(panelBadges, ui.Badge{Text: "no agent yet", Tone: "muted"})
	}
	panelBadges = append(panelBadges, ui.Badge{Text: fmt.Sprintf("%d msgs", len(th.Messages)), Tone: "muted"})
	if th.UnreadCount > 0 {
		panelBadges = append(panelBadges, ui.Badge{Text: fmt.Sprintf("%d unread", th.UnreadCount), Tone: "warning"})
	}

	var msgNodes []ui.ViewNode
	dividerShown := false
	for i, m := range th.Messages {
		if m.Unread && !dividerShown {
			dividerShown = true
			msgNodes = append(msgNodes, fixed(text(ThreadsUnreadMarkID, " ── new ─────────────────────────", "unread"), 1))
		}
		msgNodes = append(msgNodes, threadShowMessageCard(i, m, th, spec.Now, spec.IMessageStyle))
	}
	if len(msgNodes) == 0 {
		msgNodes = append(msgNodes, ui.ViewNode{
			ID:    "threads.transcript.empty",
			Kind:  ui.KindText,
			Tone:  "muted",
			Props: map[string]any{"text": " No messages yet · type a reply below."},
		})
	}

	transcript := ui.ViewNode{
		ID:     ThreadsTranscriptID,
		Kind:   ui.KindPanel,
		Border: true,
		Title:  th.Title,
		Badges: panelBadges,
		Box:    layout.Constraints{Flex: 1},
		Children: []ui.ViewNode{{
			ID:    ThreadsTranscriptID + ".scroll",
			Kind:  ui.KindScroll,
			Box:   layout.Constraints{Flex: 1},
			Props: map[string]any{"scrollKey": "threads.show|" + th.TaskID},
			Children: []ui.ViewNode{{
				ID:       ThreadsTranscriptID + ".list",
				Kind:     ui.KindVStack,
				Gap:      1,
				Children: msgNodes,
			}},
		}},
	}

	children := []ui.ViewNode{header, transcript}
	if spec.Composer.Enabled || spec.IndexShow {
		children = append(children, threadShowComposerNode(spec.Composer))
	}
	return children
}

func threadShowMessageCard(i int, m data.ThreadMessage, th data.AgentThread, now time.Time, rich bool) ui.ViewNode {
	id := fmt.Sprintf("%s%d", ThreadsMsgPrefix, i)
	body := threadMessageBody(m)
	var badges []ui.Badge
	if when, whenTone := ThreadRecency(m.CreatedAt, now); when != "" {
		badges = append(badges, ui.Badge{Text: when, Tone: whenTone})
	}
	if m.Unread {
		badges = append(badges, ui.Badge{Text: "new", Tone: "unread"})
	}
	bodyNode := ui.ViewNode{
		ID:    id + ".body",
		Kind:  ui.KindText,
		Props: map[string]any{"text": body, "wrap": true},
	}
	if rich {
		bodyNode = ui.ViewNode{
			ID:    id + ".body",
			Kind:  ui.KindMarkdown,
			Props: map[string]any{"markdown": body, "wrap": true},
		}
	}
	return ui.ViewNode{
		ID:        id,
		Kind:      ui.KindCard,
		Scope:     ScopeThreads,
		Focusable: true,
		Unread:    m.Unread,
		Title:     threadAuthorName(m, th),
		Tone:      threadAuthorTone(m.AuthorKind),
		Badges:    badges,
		Facts: map[string]any{
			"threadMessage": true,
			"threadOpen":    true,
			"messageIndex":  i,
			"authorKind":    m.AuthorKind,
			"unread":        m.Unread,
			"messageId":     m.ID,
		},
		Children: []ui.ViewNode{bodyNode},
	}
}

func threadShowComposerNode(c ThreadComposerSpec) ui.ViewNode {
	title := "Reply"
	if target := strings.TrimSpace(c.TargetLabel); target != "" {
		title = "Reply → " + target
	}
	placeholder := strings.TrimSpace(c.Placeholder)
	var badges []ui.Badge
	if c.Editing {
		badges = []ui.Badge{
			{Text: "TYPING", Tone: "accent"},
			{Text: "ctrl+s send · enter newline · esc browse chat · ctrl+c clear · ctrl+g rich modal", Tone: "muted"},
		}
		if placeholder == "" {
			placeholder = "Write a reply… (enter newline · ctrl+s send · esc browse chat · ctrl+g rich modal)"
		}
	} else {
		badges = []ui.Badge{
			{Text: "enter / i to type · k/↑ scroll chat · esc back", Tone: "muted"},
		}
		if placeholder == "" {
			placeholder = "Press enter or i to write a reply…"
		}
	}
	return ui.ViewNode{
		ID:        ThreadsComposerInputID,
		Kind:      ui.KindInput,
		Scope:     ScopeThreads,
		Focusable: true,
		Border:    true,
		Title:     title,
		Badges:    badges,
		Box:       layout.Constraints{FixedH: 6},
		Props: map[string]any{
			"value":       c.Value,
			"cursor":      c.Cursor,
			"multiline":   true,
			"wrap":        c.Wrap,
			"placeholder": placeholder,
		},
		Facts: map[string]any{
			"threadComposer":  true,
			"threadComposing": c.Editing,
			"threadOpen":      true,
		},
	}
}

// ThreadSnippet builds a concise multi-message snippet paragraph for Index
// cards and switchers.
func ThreadSnippet(th data.AgentThread, maxChars int) string {
	return buildThreadSnippet(th, maxChars)
}

func buildThreadSnippet(th data.AgentThread, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 260
	}
	entries := make([]string, 0, len(th.Messages))
	for _, m := range th.Messages {
		body := cleanSnippetText(m.Body)
		if body == "" {
			body = cleanSnippetText(m.Title)
		}
		if body == "" && len(m.Attachments) > 0 {
			label := m.Attachments[0].Label
			if label == "" {
				label = m.Attachments[0].Path
			}
			body = "📎 " + label
		}
		if body == "" {
			continue
		}
		entries = append(entries, threadAuthorName(m, th)+": "+body)
	}
	switch len(entries) {
	case 0:
		return "No messages yet."
	case 1:
		return clipRunes(entries[0], maxChars)
	case 2, 3:
		if joined := strings.Join(entries, " · "); len([]rune(joined)) <= maxChars {
			return joined
		}
		per := max(24, (maxChars-3*(len(entries)-1))/len(entries))
		parts := make([]string, len(entries))
		for i, e := range entries {
			parts[i] = clipRunes(e, per)
		}
		return clipRunes(strings.Join(parts, " · "), maxChars)
	default:
		picked := []string{entries[0], entries[len(entries)-2], entries[len(entries)-1]}
		full := picked[0] + " · … · " + picked[1] + " · " + picked[2]
		if len([]rune(full)) <= maxChars {
			return full
		}
		per := max(24, (maxChars-10)/3)
		return clipRunes(clipRunes(picked[0], per)+" · … · "+clipRunes(picked[1], per)+" · "+clipRunes(picked[2], per), maxChars)
	}
}

func cleanSnippetText(raw string) string {
	normalized := strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(normalized, "\n")
	parts := make([]string, 0, len(lines))
	for _, ln := range lines {
		s := strings.TrimSpace(ln)
		for len(s) > 0 {
			switch {
			case strings.HasPrefix(s, "#"):
				s = strings.TrimSpace(strings.TrimLeft(s, "#"))
			case strings.HasPrefix(s, ">"):
				s = strings.TrimSpace(strings.TrimLeft(s, ">"))
			case strings.HasPrefix(s, "- "), strings.HasPrefix(s, "* "), strings.HasPrefix(s, "+ "):
				s = strings.TrimSpace(s[2:])
			default:
				goto cleaned
			}
		}
	cleaned:
		if s != "" {
			parts = append(parts, s)
		}
	}
	joined := strings.Join(parts, " ")
	var b strings.Builder
	b.Grow(len(joined))
	inSpace := false
	for _, r := range joined {
		if unicode.IsSpace(r) {
			if !inSpace && b.Len() > 0 {
				b.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		inSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

func clipRunes(s string, maxChars int) string {
	r := []rune(s)
	if maxChars <= 1 || len(r) <= maxChars {
		return s
	}
	return strings.TrimRightFunc(string(r[:maxChars-1]), unicode.IsSpace) + "…"
}

func threadsHeader(spec ThreadsPageSpec, totalUnread int) ui.ViewNode {
	var line spanLine
	title := " Threads"
	if p := strings.TrimSpace(spec.ProjectTitle); p != "" {
		title += " · " + p
	}
	line.write(title+" ", "accent")
	line.write(fmt.Sprintf(" %d thread(s)", len(spec.Threads)), "muted")
	if totalUnread > 0 {
		line.write("  ", "")
		line.write(fmt.Sprintf(" %d unread ", totalUnread), "badge.warning")
	}
	return fixed(ui.ViewNode{
		ID:    "threads.header",
		Kind:  ui.KindText,
		Props: map[string]any{"text": line.String(), "spans": line.spans},
	}, 1)
}

// threadsRail is the filterable list of threads: title, agent/pane, unread
// count, message count and recency.
func threadsRail(spec ThreadsPageSpec, selected int, filtering bool) ui.ViewNode {
	rows := make([]any, 0, len(spec.Threads))
	for _, th := range spec.Threads {
		unread, unreadTone, titleTone := "", "", ""
		if th.UnreadCount > 0 {
			unread, unreadTone, titleTone = fmt.Sprintf("● %d", th.UnreadCount), "unread", "unread"
		}
		agent, agentTone := ThreadAgentLabel(th), ""
		if agent == "" {
			agent, agentTone = "no agent yet", "muted"
		}
		recency, recencyTone := ThreadRecency(th.LastAt, spec.Now)
		rows = append(rows, ui.TableRowSpec{
			Cells:     []any{th.Title, agent, unread, fmt.Sprintf("%d", len(th.Messages)), recency},
			CellTones: []string{titleTone, agentTone, unreadTone, "muted", recencyTone},
		})
	}
	rail := ui.ViewNode{
		ID:        ThreadsListID,
		Kind:      ui.KindTable,
		Focusable: true,
		Filter:    &ui.FilterSpec{ID: ThreadsListID, Prefix: "threads ", Fields: []string{"title", "agent", "project", "message"}},
		Box:       layout.Constraints{Flex: 1},
		Props: map[string]any{
			"columns":   []any{"Thread", "Agent / Pane", "Unread", "Msgs", "Last"},
			"rows":      rows,
			"selected":  selected,
			"scrollKey": spec.Section + "|" + spec.ProjectTitle,
		},
	}
	if len(rows) == 0 {
		empty := "No threads yet · n starts a new thread with a new or existing agent"
		if filtering {
			empty = "No threads match the filter."
		}
		rail.Kind = ui.KindText
		rail.Tone = "muted"
		rail.Props = map[string]any{"text": " " + empty}
	}
	return rail
}

// ThreadAgentLabel names the agent side of a thread: the pane's title, with
// its harness when the title does not already say it.
func ThreadAgentLabel(th data.AgentThread) string {
	name := strings.TrimSpace(th.PaneTitle)
	if name == "" {
		name = th.PaneID
	}
	h := strings.TrimSpace(th.Harness)
	switch {
	case name == "":
		return h
	case h != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(h)):
		return name + " (" + h + ")"
	}
	return name
}

// ThreadRecency tells how long ago at was, and the tone of that age: fresh
// within the hour, plain within the day, muted after it.
func ThreadRecency(at, now time.Time) (label, tone string) {
	if at.IsZero() {
		return "", "muted"
	}
	d := max(now.Sub(at), 0)
	switch {
	case d < time.Minute:
		return "just now", "state.working"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes())), "state.working"
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours())), ""
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24)), "muted"
}

// threadsTranscript is the selected thread's messages, oldest first, with a
// divider before the first unread one and the reply hint underneath.
func threadsTranscript(th *data.AgentThread, now time.Time, chat bool) ui.ViewNode {
	panel := ui.ViewNode{
		ID:     ThreadsTranscriptID,
		Kind:   ui.KindPanel,
		Border: true,
		Box:    layout.Constraints{Flex: 1},
	}
	hint := fixed(text(ThreadsHintID, " "+ThreadsHint, "muted"), 1)
	if th == nil {
		panel.Title = "Thread"
		panel.Children = []ui.ViewNode{
			{ID: "threads.transcript.empty", Kind: ui.KindText, Tone: "muted", Box: layout.Constraints{Flex: 1},
				Props: map[string]any{"text": " Select a thread to read it, or press n to start one."}},
			hint,
		}
		return panel
	}
	panel.Title = th.Title
	if agent := ThreadAgentLabel(*th); agent != "" {
		panel.Badges = append(panel.Badges, ui.Badge{Text: agent, Tone: "accent"})
	} else {
		panel.Badges = append(panel.Badges, ui.Badge{Text: "no agent yet", Tone: "muted"})
	}
	if th.UnreadCount > 0 {
		panel.Badges = append(panel.Badges, ui.Badge{Text: fmt.Sprintf("%d unread", th.UnreadCount), Tone: "warning"})
	}

	var rows []ui.ViewNode
	dividerShown := false
	for i, m := range th.Messages {
		if m.Unread && !dividerShown {
			dividerShown = true
			rows = append(rows, fixed(text(ThreadsUnreadMarkID, " ── new ─────────────────────────", "unread"), 1))
		}
		rows = append(rows, threadMessageNodes(i, m, *th, now, chat)...)
	}
	panel.Children = []ui.ViewNode{
		{
			ID:   ThreadsTranscriptID + ".scroll",
			Kind: ui.KindScroll,
			Box:  layout.Constraints{Flex: 1},
			// A thread opens at its latest message and stays there as it grows.
			Props: map[string]any{"followTail": true, "scrollKey": fmt.Sprintf("%s|%d", th.TaskID, len(th.Messages))},
			Children: []ui.ViewNode{{
				ID: ThreadsTranscriptID + ".list", Kind: ui.KindVStack, Children: rows,
			}},
		},
		hint,
	}
	return panel
}

func threadMessageBody(m data.ThreadMessage) string {
	body := strings.TrimRight(m.Body, "\n")
	if strings.TrimSpace(body) == "" {
		body = m.Title
	}
	for _, att := range m.Attachments {
		label := att.Label
		if label == "" {
			label = att.Path
		}
		body += "\n\n📎 `" + label + "`"
	}
	return body
}

// threadMessageNodes draws one message in legacy mode. In chat style the
// owner's messages carry an accent "You" badge and the agent's its harness or
// pane label, each over a Markdown body with a blank line between messages;
// compact style is one plain header line and the body text.
func threadMessageNodes(i int, m data.ThreadMessage, th data.AgentThread, now time.Time, chat bool) []ui.ViewNode {
	id := fmt.Sprintf("%s%d", ThreadsMsgPrefix, i)
	when, _ := ThreadRecency(m.CreatedAt, now)
	body := threadMessageBody(m)
	facts := map[string]any{"authorKind": m.AuthorKind, "unread": m.Unread, "messageId": m.ID}

	var line spanLine
	if !chat {
		line.write(" ["+threadAuthorName(m, th)+"]", threadAuthorTone(m.AuthorKind))
		if when != "" {
			line.write(" "+when, "muted")
		}
		return []ui.ViewNode{
			fixed(ui.ViewNode{ID: id + ".head", Kind: ui.KindText, Facts: facts, Props: map[string]any{"text": line.String(), "spans": line.spans}}, 1),
			{ID: id + ".body", Kind: ui.KindText, Props: map[string]any{"text": "   " + strings.ReplaceAll(body, "\n", "\n   ")}},
		}
	}

	switch m.AuthorKind {
	case "owner":
		line.write(" ", "")
		line.write(" You ", "badge.accent")
	case "agent":
		line.write(" ● ", "state.working")
		line.write(threadAuthorName(m, th), "state.working")
	default:
		line.write(" ◦ ", "muted")
		line.write(threadAuthorName(m, th), "muted")
	}
	if when != "" {
		line.write(" · "+when, "muted")
	}
	if m.Unread {
		line.write("  ", "")
		line.write("● new", "unread")
	}
	return []ui.ViewNode{
		fixed(ui.ViewNode{ID: id + ".head", Kind: ui.KindText, Facts: facts, Props: map[string]any{"text": line.String(), "spans": line.spans}}, 1),
		{ID: id + ".body", Kind: ui.KindMarkdown, Props: map[string]any{"markdown": body, "wrap": true}},
		fixed(text(id+".gap", "", ""), 1),
	}
}

// threadAuthorName is who a message is from: "You" for the owner, and for an
// agent its label, with the thread's pane when the message came from it.
func threadAuthorName(m data.ThreadMessage, th data.AgentThread) string {
	switch m.AuthorKind {
	case "owner":
		return "You"
	case "agent":
		name := strings.TrimSpace(m.AuthorLabel)
		if name == "" || name == "agent" {
			name = strings.TrimSpace(th.Harness)
		}
		if name == "" {
			name = "agent"
		}
		if pane := strings.TrimSpace(th.PaneTitle); pane != "" && m.PaneID == th.PaneID && !strings.Contains(strings.ToLower(pane), strings.ToLower(name)) {
			name += " · " + pane
		} else if pane != "" && m.PaneID == th.PaneID {
			name = pane
		}
		return name
	}
	if label := strings.TrimSpace(m.AuthorLabel); label != "" {
		return label
	}
	return "system"
}

func threadAuthorTone(kind string) string {
	switch kind {
	case "owner":
		return "accent"
	case "agent":
		return "state.working"
	}
	return "muted"
}
