package views

import (
	"fmt"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// Threads page scope and node IDs.
const (
	// ScopeThreads is the page's own scope, apart from any noun's.
	ScopeThreads        = "threads.page"
	ThreadsPageID       = "threads.page"
	ThreadsListID       = "threads.list"
	ThreadsTranscriptID = "threads.transcript"
	ThreadsUnreadMarkID = "threads.unread"
	ThreadsHintID       = "threads.hint"
	threadsRailRatio    = 0.42
	// ThreadsHint names the page's verbs under the transcript.
	ThreadsHint = "r reply · n new thread · d dispatch to New or Existing Agent · m mark read"
)

// ThreadsPageSpec is the pure data contract of ThreadsPageView.
type ThreadsPageSpec struct {
	// Threads are the rail's rows in display order (already filtered by the
	// caller); SelectedIdx picks the one whose transcript is shown.
	Threads     []data.AgentThread
	SelectedIdx int
	FilterQuery string
	// Now is the clock recency is told against.
	Now time.Time
	// IMessageStyle shows the split rail and chat transcript; off, the page
	// is one compact linear list with the selected thread's messages under it.
	IMessageStyle bool
	// ProjectTitle is set when the page is scoped to one project.
	ProjectTitle string
	// Section, when set, is the Project Portal section the page is embedded
	// in; it is published as the "section" fact.
	Section string
}

// ThreadsPageView presents the aggregated agent threads and the selected
// thread's transcript. It reads nothing but its spec.
func ThreadsPageView(spec ThreadsPageSpec) ui.ViewNode {
	filtering := strings.TrimSpace(spec.FilterQuery) != ""
	totalUnread := 0
	for _, th := range spec.Threads {
		totalUnread += th.UnreadCount
	}
	facts := map[string]any{
		"count":         len(spec.Threads),
		"hasFilter":     filtering,
		"hasSelection":  false,
		"threadTaskId":  "",
		"threadPaneId":  "",
		"unreadCount":   0,
		"messageCount":  0,
		"totalUnread":   totalUnread,
		"imessageStyle": spec.IMessageStyle,
	}
	if spec.Section != "" {
		facts["section"] = spec.Section
	}
	var selected *data.AgentThread
	selectedIdx := -1
	if i := spec.SelectedIdx; i >= 0 && i < len(spec.Threads) {
		selected, selectedIdx = &spec.Threads[i], i
		facts["hasSelection"] = true
		facts["threadTaskId"] = selected.TaskID
		facts["threadPaneId"] = selected.PaneID
		facts["unreadCount"] = selected.UnreadCount
		facts["messageCount"] = len(selected.Messages)
	}

	rail := threadsRail(spec, selectedIdx, filtering)
	transcript := threadsTranscript(selected, spec.Now, spec.IMessageStyle)

	children := []ui.ViewNode{threadsHeader(spec, totalUnread)}
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
	return ui.ViewNode{
		ID:       ThreadsPageID,
		Kind:     ui.KindVStack,
		Scope:    ScopeThreads,
		Box:      layout.Constraints{Flex: 1},
		Facts:    facts,
		Children: children,
	}
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

// threadMessageNodes draws one message. In chat style the owner's messages
// carry an accent "You" badge and the agent's its harness or pane label, each
// over a Markdown body with a blank line between messages; compact style is
// one plain header line and the body text.
func threadMessageNodes(i int, m data.ThreadMessage, th data.AgentThread, now time.Time, chat bool) []ui.ViewNode {
	id := fmt.Sprintf("threads.msg.%d", i)
	when, _ := ThreadRecency(m.CreatedAt, now)
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
