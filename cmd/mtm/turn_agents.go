package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/turns"
)

// Subagents and task notifications on the Turns tab (internal/turns
// agent.go reads them):
//
//   - A spawn call (Agent, Task) is a tool step titled "agent <type>",
//     its summary the description, the state (running, done, failed,
//     stopped), background or foreground, how long it took and its
//     tokens; its body the model, the brief's first lines, the result and
//     where its transcript is ("transcript missing" when neither the
//     session's subagents/ directory nor the parent's log holds it).
//   - Opened (or stepped onto), the step reads the subagent's own log off
//     the loop (turns.AgentLogs: cached by mtime, a large one from its
//     end) and its turns follow it as steps nested one level, each title
//     led by "│ ": its brief, its thinking, text and tool calls, its
//     answer. A nested agent nests again, to agentNestingCap levels; one
//     deeper shows its call only, and o opens its transcript.
//   - A task notification is an event, not a prompt: a compact, muted
//     card ("⤷ <description> · finished · 20m43s · 204k tokens"), closed,
//     whose body is the agent's result and, after it, what the agent did
//     about it. It links back to the spawning step, which shows the same
//     state. A notification that landed in a running turn is a card of
//     its own after that turn's; repeats of one task fold into its first
//     card. The tree style draws the same as rows.
//   - Verbs on a subagent's step (and on its notification's card): open
//     its transcript (o), copy its brief, copy its result.
//
// Nothing reads a subagent's log until its step is opened or stepped
// onto; a running one is read again once per listing (pollTurns).

// agentNestingCap is how deep subagents nest on the Turns tab: the pane's
// agent's own (depth 1) and theirs (depth 2). A deeper one shows its call
// only.
const agentNestingCap = 2

// How many lines of a brief and of a result a subagent's step shows (its
// notification's card shows the whole result; the copy verbs take all).
const (
	agentBriefShown  = 6
	agentResultShown = 12
)

// subagents is the Turns tab's subagent state, the loop's own; logs is
// safe for the jobs that read off the loop.
type subagents struct {
	logs  *turns.AgentLogs
	views map[string]*agentView // a transcript's path → what was read of it
	gen   uint64                // a listing's generation: a running agent's log is read again once per listing
	// What the shown fill drew: per card its agent steps, per tree row its
	// agent, the expanded agent steps it built nested steps for, and the
	// agents by the id of their call.
	cards    map[string][]agentStepRef
	rows     map[string]*turns.Agent
	expanded map[string]bool
	calls    map[string]*turns.Agent
}

// agentView is what was read of a subagent's log.
type agentView struct {
	tr      turns.AgentTranscript
	err     string
	loading bool
	read    bool
	gen     uint64
	state   turns.AgentState // the agent's state when it was read
}

// agentStepRef is an agent's step in a card: its fold id, its title line
// (what the step's text starts with) and its agent.
type agentStepRef struct {
	id, title string
	agent     *turns.Agent
}

func (s *subagents) init() {
	if s.logs == nil {
		s.logs = &turns.AgentLogs{}
	}
	if s.views == nil {
		s.views = map[string]*agentView{}
	}
}

// reset forgets what the last fill drew (a new fill builds it again).
func (s *subagents) reset() {
	s.init()
	s.cards, s.rows, s.calls = map[string][]agentStepRef{}, map[string]*turns.Agent{}, map[string]*turns.Agent{}
	s.expanded = map[string]bool{}
}

// view is what was read of the agent's log, nil when nothing was.
func (s *subagents) view(ag *turns.Agent) *agentView {
	if ag == nil || ag.Transcript == "" {
		return nil
	}
	return s.views[ag.Transcript]
}

// agentStateWord is the agent's state as its step says it.
func agentStateWord(ag *turns.Agent) string {
	return string(ag.State)
}

// agentSummary is a subagent step's summary: its description, state,
// background or foreground, duration and tokens when known.
func agentSummary(ag *turns.Agent) string {
	shape := "foreground"
	if ag.Background {
		shape = "background"
	}
	parts := []string{oneLine(ag.Description), agentStateWord(ag), shape}
	if ag.Duration > 0 {
		parts = append(parts, shortDuration(ag.Duration))
	}
	if ag.Tokens > 0 {
		parts = append(parts, tokenCount(ag.Tokens))
	}
	if ag.Missing {
		parts = append(parts, "transcript missing")
	}
	return joinNonEmpty(parts...)
}

// tokenCount is a token count in a few characters: 950 tokens, 204k tokens, 1.2M tokens.
func tokenCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM tokens", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%dk tokens", (n+500)/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk tokens", float64(n)/1000)
	}
	return plural(int(n), "token")
}

// agentTitle is a subagent step's title.
func agentTitle(ag *turns.Agent) string {
	if ag.Type == "" {
		return "agent"
	}
	return "agent " + ag.Type
}

// agentBody is a subagent step's body: model and type, the brief's first
// lines, the result and where its transcript is.
func (a *App) agentBody(ag *turns.Agent, depth int) string {
	var lines []string
	meta := joinNonEmpty(ag.Type, ag.Model)
	if ag.Depth > 1 {
		meta = joinNonEmpty(meta, fmt.Sprintf("depth %d", ag.Depth))
	}
	if meta != "" {
		lines = append(lines, meta)
	}
	if ag.ToolUses > 0 {
		lines = append(lines, plural(ag.ToolUses, "tool use"))
	}
	if ag.Summary != "" && ag.State != turns.AgentDone {
		lines = append(lines, ag.Summary)
	}
	if ag.Brief != "" {
		lines = append(lines, "brief:", firstLines(cardText(ag.Brief), agentBriefShown, "copy-brief copies it all"))
	}
	switch {
	case ag.Result != "":
		lines = append(lines, "result:", firstLines(cardText(ag.Result), agentResultShown, "copy-result copies it all"))
	case ag.State == turns.AgentRunning:
		lines = append(lines, "result: (running)")
	}
	switch {
	case ag.Transcript != "":
		lines = append(lines, "transcript: "+tildePath(ag.Transcript, homeDir())+" (o opens it)")
	case len(ag.Inline) > 0:
		lines = append(lines, "transcript: in the session's own log")
	default:
		lines = append(lines, "transcript missing")
	}
	if depth >= agentNestingCap && ag.Transcript != "" {
		lines = append(lines, fmt.Sprintf("(nesting stops at %d levels: o opens this agent's transcript)", agentNestingCap))
	}
	return strings.Join(lines, "\n")
}

// agentTurns are the subagent's own turns as far as they were read, and
// what the steps say while they are not: loading, unreadable, cut.
func (a *App) agentTurns(ag *turns.Agent) ([]turns.Turn, string, bool) {
	if len(ag.Inline) > 0 {
		return ag.Inline, "", false
	}
	v := a.subs.view(ag)
	switch {
	case ag.Transcript == "":
		return nil, "", false
	case v == nil || !v.read && v.loading:
		return nil, "loading…", false
	case v.err != "" && !v.read:
		return nil, v.err, false
	}
	return v.tr.Turns, "", v.tr.Truncated
}

// agentSteps are a subagent's step and, opened, its own turns nested
// under it: prefix leads each nested title, depth is how deep the agent
// is (1 for the pane's agent's own).
func (a *App) agentSteps(card, id, prefix string, depth int, s turns.Step, folded func(id string, def bool) bool) []ochazuke.CardStep {
	ag := s.Agent
	title := prefix + agentTitle(ag)
	summary := agentSummary(ag)
	role := ochazuke.CardRoleText
	if ag.State == turns.AgentFailed {
		role = ochazuke.CardRoleError
	}
	def := !a.stepOpen(string(turns.StepTool))
	out := []ochazuke.CardStep{{ID: id, Title: title, Summary: summary, Role: role, Folded: def, Body: ochazuke.TextBody(a.agentBody(ag, depth))}}
	a.subs.cards[card] = append(a.subs.cards[card], agentStepRef{id: id, title: title + " · " + summary, agent: ag})
	a.subs.calls[s.ToolID] = ag
	if folded(id, def) || depth > agentNestingCap {
		return out
	}
	a.subs.expanded[card+"\x00"+id] = true
	if depth == agentNestingCap {
		return out
	}
	list, note, cut := a.agentTurns(ag)
	inner := prefix + "│ "
	if note != "" {
		return append(out, ochazuke.CardStep{ID: id + "/note", Title: inner + "transcript", Summary: note, Role: ochazuke.CardRoleMuted})
	}
	if cut {
		out = append(out, ochazuke.CardStep{ID: id + "/cut", Title: inner + "earlier steps not read", Summary: "a large transcript is read from its last MiB; o opens it all", Role: ochazuke.CardRoleMuted})
	}
	for ti, t := range list {
		base := fmt.Sprintf("%s/%d", strings.Replace(id, "tool-", "sub-", 1), ti)
		if p := strings.TrimSpace(t.Prompt); p != "" {
			brief := "brief"
			if ti > 0 {
				brief = "message"
			}
			out = append(out, ochazuke.CardStep{ID: base + "/brief", Title: inner + brief, Summary: plural(len(strings.Split(p, "\n")), "line"),
				Role: ochazuke.CardRoleMuted, Folded: true, Body: ochazuke.TextBody(cardText(p))})
		}
		final := finalStep(t)
		for k, st := range t.Steps {
			if k == final || !a.stepShown(st.Kind) {
				continue
			}
			sid := fmt.Sprintf("%s/%s-%d", base, st.Kind, k)
			switch {
			case st.Agent != nil:
				out = append(out, a.agentSteps(card, fmt.Sprintf("%s/tool-%d", base, k), inner, depth+1, st, folded)...)
			case st.Kind == turns.StepText:
				out = append(out, ochazuke.CardStep{ID: sid, Title: inner + "text", Summary: oneLine(firstLine(st.Text)), Paragraphs: true, Body: markdownBody(st.Text)})
			case st.Kind == turns.StepThinking:
				out = append(out, ochazuke.CardStep{ID: sid, Title: inner + "thinking", Summary: plural(len(strings.Split(st.Text, "\n")), "line"),
					Role: ochazuke.CardRoleMuted, Folded: folded(sid, !a.stepOpen(string(turns.StepThinking))), Body: ochazuke.TextBody(cardText(st.Text))})
			default:
				ts := a.toolStep(k, st)
				ts.ID, ts.Title, ts.Folded = sid, inner+ts.Title, true
				out = append(out, ts)
			}
		}
		if t.Final != "" && strings.TrimSpace(t.Final) != strings.TrimSpace(ag.Result) {
			out = append(out, ochazuke.CardStep{ID: base + "/answer", Title: inner + "answer", Paragraphs: true, Body: markdownBody(t.Final)})
		}
	}
	return out
}

// foldedIn is a timeline card's fold state of its steps: a step the
// timeline knows folds as it says, a new one as def; a closed card's are
// all folded (nothing nested is built for it).
func (a *App) foldedIn(tl *ochazuke.Timeline, card string, known map[string]bool) func(string, bool) bool {
	return func(id string, def bool) bool {
		if tl == nil || !tl.IsOpen(card) {
			return true
		}
		if known[id] {
			return tl.Folded(card, id)
		}
		return def
	}
}

// knownSteps are the step ids of each card the timeline holds.
func knownSteps(tl *ochazuke.Timeline) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	if tl == nil {
		return out
	}
	for _, c := range tl.Cards() {
		m := map[string]bool{}
		for _, s := range c.Steps {
			m[s.ID] = true
		}
		out[c.ID] = m
	}
	return out
}

// agentRevision is what an agent step draws, for its card's Revision.
func (a *App) agentRevision(t turns.Turn) []any {
	var sig []any
	for _, s := range t.Steps {
		if ag := s.Agent; ag != nil {
			sig = append(sig, ag.State, len(ag.Result), ag.Tokens, ag.Missing, ag.Transcript, len(ag.Inline))
			if v := a.subs.view(ag); v != nil {
				sig = append(sig, v.loading, v.read, v.err, v.tr.Size, len(v.tr.Turns))
			}
		}
	}
	return sig
}

// noticeHeader is a notification card's one line: what the task is and
// how it stands.
func noticeHeader(n *turns.Notice, ag *turns.Agent) string {
	title := n.Title
	if ag != nil && ag.Description != "" {
		title = ag.Description
	}
	return "⤷ " + joinNonEmpty(oneLine(title), n.State)
}

// noticeTitle is a notification card's header: what the task is, and
// the detail of its state when the state has one ("failed: the API
// errored" → the API errored). The state's word goes on the meta line
// (noticeCardMeta), where it is toned.
func noticeTitle(n *turns.Notice, ag *turns.Agent) string {
	title := n.Title
	if ag != nil && ag.Description != "" {
		title = ag.Description
	}
	_, detail := noticeWord(n.State)
	return "⤷ " + joinNonEmpty(oneLine(title), oneLine(detail))
}

// noticeWord splits a notice's state into its word and its detail at the
// first colon ("failed: the API errored"); a state without one is all
// word.
func noticeWord(state string) (string, string) {
	word, detail, _ := strings.Cut(state, ":")
	return strings.TrimSpace(word), strings.TrimSpace(detail)
}

// noticeCardMeta is a notification card's meta line with its status:
// when, how the task stands (Notice.State, its kind from noticeStatus),
// then noticeMeta's how long, tokens, tool uses and the turn that
// started it.
func noticeCardMeta(n *turns.Notice, from string, loc *time.Location) statusLine {
	var parts []string
	if !n.At.IsZero() {
		parts = append(parts, n.At.In(loc).Format("Jan 2 15:04"))
	}
	line := statusLine{at: -1}
	if word, _ := noticeWord(n.State); word != "" {
		line = statusLine{at: len(parts), kind: noticeStatus(word, n.Status)}
		parts = append(parts, word)
	}
	rest := *n
	rest.At = time.Time{}
	if tail := noticeMeta(&rest, from, loc); tail != "" {
		parts = append(parts, tail)
	}
	line.parts = parts
	return line
}

// noticeMeta is a notification card's meta line: when, how long the task
// took, its tokens and tool uses, the turn that started it.
func noticeMeta(n *turns.Notice, from string, loc *time.Location) string {
	var parts []string
	if !n.At.IsZero() {
		parts = append(parts, n.At.In(loc).Format("Jan 2 15:04"))
	}
	if n.Duration > 0 {
		parts = append(parts, shortDuration(n.Duration))
	}
	if n.Tokens > 0 {
		parts = append(parts, tokenCount(n.Tokens))
	}
	if n.ToolUses > 0 {
		parts = append(parts, plural(n.ToolUses, "tool use"))
	}
	if from != "" {
		parts = append(parts, "from “"+oneLine(from)+"”")
	}
	if n.Repeats > 0 {
		parts = append(parts, plural(n.Repeats+1, "notice"))
	}
	return strings.Join(parts, " · ")
}

// noticeResultStep is a notification card's result: the agent's report,
// paragraph by paragraph.
func noticeResultStep(n *turns.Notice, ag *turns.Agent) ochazuke.CardStep {
	result := n.Result
	if ag != nil && ag.Result != "" {
		result = ag.Result
	}
	if strings.TrimSpace(result) == "" {
		return ochazuke.CardStep{ID: "result", Title: "result", Summary: "none", Role: ochazuke.CardRoleMuted}
	}
	return ochazuke.CardStep{ID: "result", Title: "result", Paragraphs: true, Body: markdownBody(result)}
}

// spawnPrompt is the first line of the prompt of the turn with id.
func spawnPrompt(list []turns.Turn, id string) string {
	if id == "" {
		return ""
	}
	for _, t := range list {
		if t.ID == id {
			return orNoPrompt(firstLine(promptText(t.Prompt)))
		}
	}
	return ""
}

// agentCalls are the turns' spawn calls' agents by call id (an agent's
// notification card takes its description and result from it).
func agentCalls(list []turns.Turn) map[string]*turns.Agent {
	out := map[string]*turns.Agent{}
	for _, t := range list {
		for _, s := range t.Steps {
			if s.Agent != nil {
				out[s.ToolID] = s.Agent
			}
		}
	}
	return out
}

// wantAgents reads the logs of the subagents whose steps are open or
// stepped onto in the shown turns, and rebuilds the shown turns when the
// steps opened or closed since they were built. Called after every
// update; it reads no file itself.
func (a *App) wantAgents() {
	if !a.turnsHistoryActive() {
		return
	}
	if a.pv.tab != tabTurns || a.pv.turnsFor == "" {
		return
	}
	a.subs.init()
	expanded := map[string]bool{}
	var need []*turns.Agent
	var owner string
	if a.turnCards() {
		tl := a.pv.cards
		if tl == nil {
			return
		}
		owner = tl.ID()
		current := ""
		if text, ok := tl.StepText(); ok {
			current = text
		}
		in, _, _ := tl.Step()
		for card, refs := range a.subs.cards {
			if !tl.IsOpen(card) {
				continue
			}
			for _, ref := range refs {
				open := !tl.Folded(card, ref.id)
				if open {
					expanded[card+"\x00"+ref.id] = true
				}
				if open || card == in && strings.HasPrefix(current, ref.title) {
					need = append(need, ref.agent)
				}
			}
		}
	} else {
		tree := a.pv.turns
		owner = tree.ID()
		for id, ag := range a.subs.rows {
			if a.r.Instances[id] != nil && !tree.Collapsed(id) {
				expanded[id] = true
				need = append(need, ag)
			}
		}
	}
	for _, ag := range need {
		a.loadAgent(owner, ag)
	}
	if !sameSet(expanded, a.subs.expanded) {
		a.redrawAgents()
	}
}

// sameSet reports two sets alike.
func sameSet(x, y map[string]bool) bool {
	if len(x) != len(y) {
		return false
	}
	for k := range x {
		if !y[k] {
			return false
		}
	}
	return true
}

// loadAgent reads a subagent's log off the loop when it was not read, or
// it runs (once per listing), or its state changed since it was read.
func (a *App) loadAgent(owner string, ag *turns.Agent) {
	if ag == nil || ag.Transcript == "" || len(ag.Inline) > 0 {
		return
	}
	v := a.subs.views[ag.Transcript]
	switch {
	case v == nil:
		v = &agentView{}
		a.subs.views[ag.Transcript] = v
	case v.loading:
		return
	case v.read && v.state == ag.State && (ag.State != turns.AgentRunning || v.gen >= a.subs.gen):
		return
	case !v.read && v.err != "" && v.gen >= a.subs.gen:
		return
	}
	v.loading, v.gen, v.state = true, a.subs.gen, ag.State
	path, logs := ag.Transcript, a.subs.logs
	res := ochazuke.Pending(func(context.Context) (any, error) {
		return logs.Read(path)
	}).Label("subagent " + oneLine(ag.Description)).Then(func(_ ochazuke.Ctx, val any, err error) {
		v.loading = false
		if err != nil {
			v.err = err.Error()
		} else {
			v.tr, v.read, v.err = val.(turns.AgentTranscript), true, ""
		}
		a.redrawAgents()
	})
	if _, err := a.r.Launch(owner, "turn.read-subagent", res, ochazuke.Ctx{Runtime: a.r}); err != nil {
		v.loading = false
		v.err = err.Error()
	}
}

// redrawAgents rebuilds the shown turns from what was read.
func (a *App) redrawAgents() {
	if a.turnCards() {
		tl := a.pv.cards
		if tl == nil {
			return
		}
		if st := a.pv.cardFills[tl.ID()]; st != nil && st.model {
			if err := a.pushCards(tl, st); err != nil {
				a.r.UI().Error(err.Error())
			}
		}
		return
	}
	st := a.turnFillOf(a.pv.turns)
	if st.model {
		if err := a.refillModelTurns(a.pv.turns, st); err != nil {
			a.r.UI().Error(err.Error())
		}
	}
}

// agentNodes are a subagent step's tree rows: its row, its lines, and,
// expanded, its own turns' steps as child rows (to agentNestingCap).
func (a *App) agentNodes(parent string, j int, s turns.Step, ref turnRef, width, depth int, expanded func(string) bool) []ochazuke.TreeNode {
	ag := s.Agent
	id := fmt.Sprintf("%s/step/%d", parent, j)
	ref.Step = "agent"
	label := agentTitle(ag) + ": " + agentSummary(ag)
	nodes := []ochazuke.TreeNode{{ID: id, Parent: parent, Label: label, Noun: "turn-step", Data: ref}}
	a.subs.rows[id] = ag
	a.subs.calls[s.ToolID] = ag
	b := &turnBody{nodes: &nodes, parent: id, ref: ref, width: max(0, width-2)}
	for _, l := range strings.Split(a.agentBody(ag, depth), "\n") {
		b.line(l)
	}
	if depth >= agentNestingCap || !expanded(id) {
		return nodes
	}
	a.subs.expanded[id] = true
	list, note, cut := a.agentTurns(ag)
	if note != "" {
		b.line("transcript: " + note)
		return nodes
	}
	if cut {
		b.line("(earlier steps not read: a large transcript is read from its last MiB)")
	}
	for ti, t := range list {
		tid := fmt.Sprintf("%s/turn/%d", id, ti)
		head := "brief"
		if ti > 0 {
			head = "message"
		}
		nodes = append(nodes, ochazuke.TreeNode{ID: tid, Parent: id, Label: "│ " + head + ": " + oneLine(firstLine(t.Prompt)), Noun: "turn-step", Data: ref})
		a.subs.rows[tid] = ag
		final := finalStep(t)
		for k, st := range t.Steps {
			if k == final || !a.stepShown(st.Kind) {
				continue
			}
			switch {
			case st.Agent != nil:
				nodes = append(nodes, a.agentNodes(id, len(t.Steps)*ti+k+1000, st, ref, width-2, depth+1, expanded)...)
			case st.Kind == turns.StepText:
				nodes = append(nodes, ochazuke.TreeNode{ID: fmt.Sprintf("%s/text/%d", tid, k), Parent: id, Label: "│ " + oneLine(firstLine(st.Text)), Noun: "turn-line", Data: ref})
			default:
				for _, n := range a.stepNode(tid, k, st, ref, width-2) {
					if n.Parent == tid {
						n.Parent, n.Label = id, "│ "+n.Label
					}
					nodes = append(nodes, n)
				}
			}
		}
		if t.Final != "" && strings.TrimSpace(t.Final) != strings.TrimSpace(ag.Result) {
			nodes = append(nodes, ochazuke.TreeNode{ID: tid + "/answer", Parent: id, Label: "│ answer: " + oneLine(firstLine(t.Final)), Noun: "turn-line", Data: ref})
		}
	}
	return nodes
}

// The subagent verbs: on a subagent's step (inside a card, or its tree
// row) and on its notification's card.

// focusedAgent is the subagent the focus is on: the step the focus is
// inside a card on, the tree row, or a notification card's agent.
func (a *App) focusedAgent() *turns.Agent {
	focus := a.r.Focus
	if tl := a.pv.cards; tl != nil && focus == tl.BodyID() {
		card, _, ok := tl.Step()
		text, _ := tl.StepText()
		if !ok {
			return nil
		}
		refs := a.subs.cards[card]
		// The longest title that leads the step's text: a nested agent's
		// title is longer than the "│ " lines of its parent.
		sort.SliceStable(refs, func(i, k int) bool { return len(refs[i].title) > len(refs[k].title) })
		for _, ref := range refs {
			if strings.HasPrefix(text, ref.title) {
				return ref.agent
			}
		}
		return nil
	}
	if ag := a.subs.rows[focus]; ag != nil {
		return ag
	}
	if inst := a.r.Instances[focus]; inst != nil {
		if ref, ok := inst.Data.(turnRef); ok && ref.Event != "" {
			return a.subs.calls[ref.Event]
		}
	}
	return nil
}

// agentHandler runs do on the focused subagent.
func (a *App) agentHandler(do func(ochazuke.Ctx, *turns.Agent) ochazuke.Result) ochazuke.AppHandler {
	return func(ctx ochazuke.Ctx) ochazuke.Result {
		ag := a.focusedAgent()
		if ag == nil {
			return ochazuke.Failed(errors.New("not on a subagent's step"))
		}
		return do(ctx, ag)
	}
}

// openAgentTranscript opens the subagent's own log in a pager (a new pane
// of the turns' pane's session; else the system's opener).
func (a *App) openAgentTranscript(_ ochazuke.Ctx, ag *turns.Agent) ochazuke.Result {
	if ag.Transcript == "" {
		if len(ag.Inline) > 0 {
			return ochazuke.Failed(errors.New("this agent's transcript is inside the session's own log"))
		}
		return ochazuke.Failed(errors.New("transcript missing"))
	}
	path, sys := ag.Transcript, a.cx.sys
	opener, _ := a.panesd.(pagerOpener)
	var p pane
	perr := errors.New("no pane")
	if row := a.r.Instances[a.pv.turnsFor]; row != nil {
		p, perr = a.paneAt(row)
	}
	return ochazuke.Pending(func(c context.Context) (any, error) {
		if opener != nil && perr == nil {
			return nil, opener.OpenPager(c, p, path)
		}
		return nil, sys.Open(c, path)
	}).Then(func(ctx ochazuke.Ctx, _ any, err error) {
		if err == nil {
			ctx.Notify("opened " + tildePath(path, homeDir()))
		}
	})
}

// copyAgentText copies a subagent's brief or result as y copies a turn.
func (a *App) copyAgentText(what string, text func(*turns.Agent) string) func(ochazuke.Ctx, *turns.Agent) ochazuke.Result {
	return func(_ ochazuke.Ctx, ag *turns.Agent) ochazuke.Result {
		body := text(ag)
		if strings.TrimSpace(body) == "" {
			return ochazuke.Failed(fmt.Errorf("the agent has no %s yet", what))
		}
		ref := turnRef{Window: a.pv.turnsWindow(), ID: "agent-" + ag.ID}
		return a.copyLoaded(ref, func(context.Context) (string, error) { return body, nil }, "the "+what)
	}
}

// turnsWindow is the window the Turns tab shows.
func (pv *paneUI) turnsWindow() string {
	if pv.cards != nil {
		for w, tl := range pv.timelines {
			if tl == pv.cards {
				return w
			}
		}
	}
	return ""
}

// addAgentVerbs defines the subagent verbs on the nouns a subagent's step
// is focused as.
func (a *App) addAgentVerbs() {
	a.r.DefineGuard("on-agent", func(*ochazuke.Runtime, ochazuke.KeyEvent) bool { return a.focusedAgent() != nil })
	for _, noun := range []string{ochazuke.TimelineBodyNoun, "turn-step", "turn"} {
		a.r.Define(noun).
			Verb("open-transcript", a.agentHandler(a.openAgentTranscript), "Open the subagent's own transcript file in a pager").When("on-agent").
			Verb("copy-brief", a.agentHandler(a.copyAgentText("brief", func(ag *turns.Agent) string { return ag.Brief })), "Copy the brief the subagent was given").When("on-agent").
			Verb("copy-result", a.agentHandler(a.copyAgentText("result", func(ag *turns.Agent) string { return ag.Result })), "Copy the subagent's result").When("on-agent").
			Mode("normal").
			Bind("o", ochazuke.Do(noun+".open-transcript").Guard("on-agent").Hinted("transcript"))
	}
}

// firstLines is at most n lines of text, then how many more there are
// and how to get them.
func firstLines(text string, n int, more string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n… %s more (%s)", plural(len(lines)-n, "line"), more)
}
