package main

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/markdown"

	"github.com/unixsuperhero/matomeru/internal/history"
	"github.com/unixsuperhero/matomeru/internal/turns"
)

// The Turns tab's styles (turns.style):
//
//   - cards, the default: each turn is a box on ochazuke's card timeline
//     (AddTimeline with FollowTail). The header is the whole prompt,
//     wrapped and never cut; the meta line under it says when, how it
//     ended, its steers and tool calls and how long it took. Opening a
//     card shows its body as steps (ochazuke's Card.Steps): the prompt
//     (the header), the steers, each thinking and tool call folded as the
//     settings say (an edit's call as a diff), each paragraph of the
//     agent's text, and the answer, paragraph by paragraph (a shell
//     turn's output). l on an open card steps inside, onto the prompt;
//     j/k walk the steps, l opens a folded one, y copies the step, h
//     folds the open section the step is in (an answer, a tool call)
//     and otherwise goes back to the card. The timeline is NoSpill: j
//     on the newest turn scrolls to its end and stays (half the window
//     at most per press, ochazuke R3.14); }, tab and i reach the prompt
//     box below. The running card is the bright one ("●" in its border),
//     older cards are muted, and a chained session starts under a labelled
//     rule. The timeline follows its end like a live terminal: the newest
//     card is selected and a growing answer keeps its bottom in view, until
//     the owner moves or scrolls up; G, or reaching the bottom again,
//     follows again. Each pane has its own timeline, so a pane left and
//     come back to keeps its place, its open cards and its folds.
//   - tree: the accordion tree (turns.go, turns_model.go), as it was. It
//     keeps its selection in view but does not follow the end.
//
// Both styles share the turn verbs (open, back, copy, copy-all, prompt),
// the prompt box, the turns.* settings, omp branch following and the
// session separators; switching the setting rebuilds the tab at once.
//
// The timeline's own keys open and fold (l opens through turn.open, enter
// opens through turn.open and closes through turn.close, h closes, space toggles, z R and z M open and close all, z 1
// to z 9 fold a section, J and K scroll, G follows). Opening a card by any
// of them runs turn.review once (syncOpenedCards), so a journal turn is
// marked reviewed and a shell turn reads its output however it opened.

// The turns.style setting and its choices.
const (
	settingTurnsStyle = "turns.style"
	turnsStyleCards   = "cards"
	turnsStyleTree    = "tree"
)

// turnCardsID prefixes each pane's timeline id (turnCardsID/<window>).
const turnCardsID = "panes/turn-cards"

// turnCards reports whether the Turns tab draws cards (turns.style).
func (a *App) turnCards() bool {
	return a.r.Settings().String(settingTurnsStyle) != turnsStyleTree
}

// cardFill is what a pane's timeline was last filled with.
type cardFill struct {
	window string
	back   turnRef
	model  bool               // turns read from the agent's log or the shell (turns_model.go)
	mturns []turns.Turn       // those turns
	jturns []history.PaneTurn // else the journal's
	source *paneTurns
	opened map[string]bool // open cards whose opening ran turn.review
}

// turnCardID is a card's id: the window keeps two panes' turns apart, the
// prefix keeps them apart from the tree's rows.
func turnCardID(window, id string) string { return "turn-card:" + window + "/" + id }

// turnsTimeline is the window's timeline, made on first use; timelines of
// panes no longer listed are dropped then.
func (a *App) turnsTimeline(window string) (*ochazuke.Timeline, error) {
	if tl := a.pv.timelines[window]; tl != nil {
		return tl, nil
	}
	if a.pv.timelines == nil {
		a.pv.timelines, a.pv.cardFills = map[string]*ochazuke.Timeline{}, map[string]*cardFill{}
	}
	listed := map[string]bool{}
	for _, p := range a.panes {
		listed[p.ID] = true
	}
	for w, tl := range a.pv.timelines {
		if !listed[w] && tl != a.pv.cards {
			_ = a.r.RemoveInstance(tl.ID())
			delete(a.pv.cardFills, tl.ID())
			delete(a.pv.timelines, w)
		}
	}
	// NoSpill: j at the newest turn scrolls to its end and stays there
	// (ochazuke R13.2); the prompt box below is reached by i, } or tab.
	tl, err := a.r.AddTimeline(turnCardsID+"/"+window, nil, ochazuke.Parent(panesPage), ochazuke.FollowTail(), ochazuke.NoSpill())
	if err != nil {
		return nil, err
	}
	a.pv.timelines[window] = tl
	a.pv.cardFills[tl.ID()] = &cardFill{window: window, opened: map[string]bool{}}
	return tl, nil
}

// showTurnCards fills the row's pane's timeline and makes it the one the
// tab draws (reading the turns first when none were read).
func (a *App) showTurnCards(row string) error {
	p, err := a.paneAt(a.r.Instances[row])
	if err != nil {
		return err
	}
	tl, err := a.turnsTimeline(p.ID)
	if err != nil {
		return err
	}
	a.pv.cards, a.pv.turnsFor = tl, row
	st := a.pv.cardFills[tl.ID()]
	st.back = turnRef{Back: row}
	if a.turnKind(p) == turnsJournal {
		ts, err := a.hist.Turns(p.ID)
		if err != nil {
			return err
		}
		st.model, st.jturns, st.mturns, st.source = false, ts, nil, nil
		return a.pushCards(tl, st)
	}
	pt := a.paneTurnsOf(p.ID)
	// A read that failed waits for the next listing (pollTurns) to try again.
	if !pt.loaded && !pt.loading && pt.err == "" {
		if _, err := a.r.Execute(ochazuke.Request{Verb: "read-turns", Noun: "pane", Target: row}); err != nil {
			return err
		}
	}
	st.model, st.mturns, st.jturns, st.source = true, liveTurns(pt, p), nil, pt
	return a.pushCards(tl, st)
}

// pushCards sets the timeline's cards from its fill.
func (a *App) pushCards(tl *ochazuke.Timeline, st *cardFill) error {
	if st.model {
		return tl.SetCards(a.modelCards(st))
	}
	return tl.SetCards(a.journalCards(st))
}

// modelCards are a model fill's cards.
func (a *App) modelCards(st *cardFill) []ochazuke.Card {
	loc := a.location()
	active, last := activeTurn(st.mturns), len(st.mturns)-1
	group := ""
	cards := make([]ochazuke.Card, 0, len(st.mturns))
	a.subs.reset()
	tl := a.pv.timelines[st.window]
	known := knownSteps(tl)
	calls := agentCalls(st.mturns)
	repeats := map[string][]turns.Turn{}
	for _, t := range st.mturns {
		if t.Notice != nil && t.Notice.RepeatOf != "" {
			repeats[t.Notice.RepeatOf] = append(repeats[t.Notice.RepeatOf], t)
		}
	}
	for i, t := range st.mturns {
		if t.NewSession {
			group = "new session"
			if !t.StartedAt.IsZero() {
				group += " · " + t.StartedAt.In(loc).Format("Jan 2 15:04")
			}
		}
		if t.Notice != nil {
			if t.Notice.RepeatOf != "" {
				continue // folded into its task's first card
			}
			cards = append(cards, a.noticeCard(st, t, repeats[t.ID], calls, i == active, group, tl, known, loc))
			continue
		}
		if t.NewSession {
			group = "new session"
			if !t.StartedAt.IsZero() {
				group += " · " + t.StartedAt.In(loc).Format("Jan 2 15:04")
			}
		}
		ref := st.back
		ref.Window, ref.ID, ref.Line, ref.Model, ref.Card = st.window, t.ID, -1, true, true
		prompt := promptText(t.Prompt)
		cid := turnCardID(st.window, t.ID)
		cards = append(cards, ochazuke.Card{
			ID:       cid,
			Noun:     "turn",
			Label:    orNoPrompt(firstLine(prompt)),
			Data:     ref,
			Header:   orNoPrompt(cardText(prompt)),
			Meta:     modelTurnMeta(t, loc),
			Steps:    a.modelStepsIn(cid, t, st.source, loc, tl, known[cid]),
			Open:     i == active,
			Active:   i == active,
			Muted:    i != last,
			Group:    group,
			Revision: a.modelRevision(t, st.source),
		})
		for _, n := range t.Events {
			cards = append(cards, a.eventCard(st, t, n, calls, group, loc))
		}
	}
	return cards
}

// noticeCard is a turn a task notification opened, as an event: compact,
// muted, closed and drawn as an event card (indented, dashed border), its body the task's result and then what the agent
// did about it (each repeat's too).
func (a *App) noticeCard(st *cardFill, t turns.Turn, again []turns.Turn, calls map[string]*turns.Agent, running bool, group string, tl *ochazuke.Timeline, known map[string]map[string]bool, loc *time.Location) ochazuke.Card {
	n := t.Notice
	ag := calls[n.ToolUseID]
	ref := st.back
	ref.Window, ref.ID, ref.Line, ref.Model, ref.Card, ref.Event = st.window, t.ID, -1, true, true, n.ToolUseID
	id := turnCardID(st.window, t.ID)
	steps := []ochazuke.CardStep{{Header: true}, noticeResultStep(n, ag)}
	sig := []any{"notice", n.State, len(n.Result), n.Repeats, a.modelRevision(t, st.source)}
	for k, rt := range append([]turns.Turn{t}, again...) {
		inner := a.modelStepsIn(id, rt, st.source, loc, tl, known[id])
		for _, s := range inner[1:] { // past the header
			if k > 0 {
				s.ID = fmt.Sprintf("again-%d/%s", k, s.ID)
			}
			if s.ID == "answer" || strings.HasSuffix(s.ID, "/answer") {
				s.Title = "response"
			}
			steps = append(steps, s)
		}
		if k > 0 {
			sig = append(sig, a.modelRevision(rt, st.source))
		}
	}
	if ag != nil {
		sig = append(sig, ag.Description, ag.State, len(ag.Result))
	}
	header := noticeHeader(n, ag)
	return ochazuke.Card{
		ID:       id,
		Noun:     "turn",
		Label:    header,
		Data:     ref,
		Header:   header,
		Meta:     noticeMeta(n, spawnPrompt(st.mturns, n.Turn), loc),
		Steps:    steps,
		Active:   running,
		Muted:    true,
		Event:    true,
		Group:    group,
		Revision: revision(sig...),
	}
}

// eventCard is a notification that landed in a running turn: a compact
// card of its own after the turn's, its body the task's result.
func (a *App) eventCard(st *cardFill, t turns.Turn, n turns.Notice, calls map[string]*turns.Agent, group string, loc *time.Location) ochazuke.Card {
	ag := calls[n.ToolUseID]
	key := n.ToolUseID
	if key == "" {
		key = "task-" + n.TaskID
	}
	ref := st.back
	ref.Window, ref.ID, ref.Line, ref.Model, ref.Card, ref.Event = st.window, t.ID, -1, true, true, n.ToolUseID
	header := noticeHeader(&n, ag)
	sig := []any{"event", n.State, len(n.Result), n.Repeats}
	if ag != nil {
		sig = append(sig, ag.Description, ag.State, len(ag.Result))
	}
	return ochazuke.Card{
		ID:       turnCardID(st.window, t.ID+"/event/"+key),
		Noun:     "turn",
		Label:    header,
		Data:     ref,
		Header:   header,
		Meta:     noticeMeta(&n, spawnPrompt(st.mturns, n.Turn), loc),
		Steps:    []ochazuke.CardStep{{Header: true}, noticeResultStep(&n, ag)},
		Muted:    true,
		Event:    true,
		Group:    group,
		Revision: revision(sig...),
	}
}

// journalCards are a journal fill's cards.
func (a *App) journalCards(st *cardFill) []ochazuke.Card {
	loc := a.location()
	last := len(st.jturns) - 1
	cards := make([]ochazuke.Card, 0, len(st.jturns))
	for i, t := range st.jturns {
		ref := st.back
		ref.Window, ref.ID, ref.Line, ref.Card = st.window, t.ID, -1, true
		steps := []ochazuke.CardStep{{Header: true}}
		for k, r := range t.Responses {
			title := "answer"
			if len(t.Responses) > 1 {
				title = fmt.Sprintf("answer %d", k+1)
			}
			steps = append(steps, ochazuke.CardStep{ID: fmt.Sprintf("answer-%d", k+1), Title: title, Paragraphs: true, Body: ochazuke.TextBody(cardText(r.Text))})
		}
		if len(t.Responses) == 0 {
			steps = append(steps, ochazuke.CardStep{ID: "answer-1", Title: "answer", Summary: "none yet", Role: ochazuke.CardRoleMuted})
		}
		meta := []string{time.UnixMilli(t.StartedAt).In(loc).Format("Jan 2 15:04")}
		switch {
		case !t.Done():
			meta = append(meta, "working")
		case t.ReviewedAt == 0:
			meta = append(meta, plural(len(t.Responses), "answer"), "new")
		default:
			meta = append(meta, plural(len(t.Responses), "answer"))
		}
		if t.StartedAt == 0 {
			meta = meta[1:]
		}
		cards = append(cards, ochazuke.Card{
			ID:       turnCardID(st.window, t.ID),
			Noun:     "turn",
			Label:    orNoPrompt(firstLine(t.Prompt)),
			Data:     ref,
			Header:   orNoPrompt(cardText(t.Prompt)),
			Meta:     strings.Join(meta, " · "),
			Steps:    steps,
			Active:   !t.Done() && i == last,
			Muted:    i != last,
			Revision: revision(t.Prompt, len(t.Responses), len(t.Response()), t.CompletedAt, t.ReviewedAt),
		})
	}
	return cards
}

// orNoPrompt is text, or "(no prompt)" when it is empty.
func orNoPrompt(text string) string {
	if text == "" {
		return "(no prompt)"
	}
	return text
}

// cardText is a text as a card draws it: no escapes, no trailing blanks.
func cardText(text string) string {
	lines := strings.Split(strings.TrimRight(xansi.Strip(text), "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t\r")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

// modelTurnMeta is a card's meta line: when it started, how it ended (or
// that it runs), its steers and tool calls and how long it took.
func modelTurnMeta(t turns.Turn, loc *time.Location) string {
	var parts []string
	if !t.StartedAt.IsZero() {
		parts = append(parts, t.StartedAt.In(loc).Format("Jan 2 15:04"))
	}
	switch {
	case t.State == turns.StateRunning && t.Source == turns.SourceShell:
		parts = append(parts, "running")
	case t.State == turns.StateRunning:
		parts = append(parts, "working")
	case t.Source == turns.SourceShell && t.Exit != nil:
		parts = append(parts, fmt.Sprintf("exit %d", *t.Exit))
	case t.State == turns.StateError:
		parts = append(parts, "interrupted")
	default:
		parts = append(parts, "done")
	}
	if n := len(t.Steers); n > 0 {
		parts = append(parts, plural(n, "steer"))
	}
	if n := turnToolCalls(t); n > 0 {
		parts = append(parts, plural(n, "tool call"))
	}
	if t.Duration > 0 && t.State != turns.StateRunning {
		parts = append(parts, shortDuration(t.Duration))
	}
	return strings.Join(parts, " · ")
}

func turnToolCalls(t turns.Turn) int {
	n := 0
	for _, s := range t.Steps {
		if s.Kind == turns.StepTool {
			n++
		}
	}
	return n
}

// finalStep is the index of the step that is the turn's answer, -1 none.
func finalStep(t turns.Turn) int {
	for j := len(t.Steps) - 1; j >= 0; j-- {
		if t.Steps[j].Kind == turns.StepText {
			if t.Steps[j].Text == t.Final {
				return j
			}
			break
		}
	}
	return -1
}

// modelSteps are an open card's steps, in the turn's order: the prompt
// (the card's header), the steers, then each step of the turn as the tree
// style's rows show them (a thinking or a tool call folds, a text is a
// step per paragraph), and the answer paragraph by paragraph; a shell
// turn's output is one step.
func (a *App) modelSteps(t turns.Turn, pt *paneTurns, loc *time.Location) []ochazuke.CardStep {
	return a.modelStepsIn("", t, pt, loc, nil, nil)
}

// modelStepsIn are modelSteps of the card with id in the timeline tl
// (whose steps known are): a subagent's step nests its own turns when
// the timeline has it open (turn_agents.go).
func (a *App) modelStepsIn(card string, t turns.Turn, pt *paneTurns, loc *time.Location, tl *ochazuke.Timeline, known map[string]bool) []ochazuke.CardStep {
	if a.subs.cards == nil {
		a.subs.reset()
	}
	folded := a.foldedIn(tl, card, known)
	out := []ochazuke.CardStep{{Header: true}}
	if n := len(t.Steers); n > 0 {
		var b strings.Builder
		for k, s := range t.Steers {
			if k > 0 {
				b.WriteString("\n\n")
			}
			b.WriteString(s.At.In(loc).Format("15:04:05") + "\n" + cardText(s.Text))
		}
		out = append(out, ochazuke.CardStep{ID: "steers", Title: "steers", Summary: plural(n, "steer"), Role: ochazuke.CardRoleAccent, Body: ochazuke.TextBody(b.String())})
	}
	if t.Source == turns.SourceShell {
		body, summary := shellOutputText(pt, t)
		return append(out, ochazuke.CardStep{ID: "output", Title: "output", Summary: summary, Body: ochazuke.TextBody(body)})
	}
	final := finalStep(t)
	for j, s := range t.Steps {
		if j == final || !a.stepShown(s.Kind) {
			continue
		}
		switch s.Kind {
		case turns.StepText:
			out = append(out, ochazuke.CardStep{ID: fmt.Sprintf("text-%d", j), Paragraphs: true, Body: markdownBody(s.Text)})
		case turns.StepThinking:
			out = append(out, ochazuke.CardStep{ID: fmt.Sprintf("thinking-%d", j), Title: "thinking", Summary: plural(len(strings.Split(s.Text, "\n")), "line"),
				Role: ochazuke.CardRoleMuted, Folded: !a.stepOpen(string(turns.StepThinking)), Body: ochazuke.TextBody(cardText(s.Text))})
		case turns.StepTool:
			if s.Agent != nil {
				out = append(out, a.agentSteps(card, fmt.Sprintf("tool-%d", j), "", 1, s, folded)...)
				continue
			}
			out = append(out, a.toolStep(j, s))
		}
	}
	answer := ochazuke.CardStep{ID: "answer", Title: "answer"}
	switch {
	case t.Final != "":
		answer.Paragraphs, answer.Body = true, markdownBody(t.Final)
	case t.State == turns.StateRunning:
		answer.Summary, answer.Role = "working", ochazuke.CardRoleMuted
	default:
		answer.Summary, answer.Role = "none", ochazuke.CardRoleMuted
	}
	return append(out, answer)
}

// toolStep is a tool call as one step: its name and summary as the title,
// its input (an edit's as a diff) and its result as the body.
func (a *App) toolStep(j int, s turns.Step) ochazuke.CardStep {
	summary := s.Summary
	switch {
	case !s.HasResult:
		summary = joinNonEmpty(summary, "running")
	case s.IsError:
		summary = joinNonEmpty(summary, "error")
	}
	var body string
	if diff, ok := editDiff(s.Input); ok {
		body = "diff:\n" + cappedText(diff, turnInputShown)
	} else {
		body = "input:\n" + cappedText(prettyJSON(s.Input), turnInputShown)
	}
	if s.HasResult {
		body += "\nresult:\n" + cappedText(cardText(s.Result), turnResultShown)
	}
	role := ochazuke.CardRoleText
	if s.IsError {
		role = ochazuke.CardRoleError
	}
	return ochazuke.CardStep{ID: fmt.Sprintf("tool-%d", j), Title: "tool " + s.Tool, Summary: summary,
		Role: role, Folded: !a.stepOpen(string(turns.StepTool)), Body: ochazuke.TextBody(body)}
}

// editDiff is an edit tool call's input as a diff: a file_path with an
// old_string and new_string (Edit), a list of them (edits, MultiEdit), or
// a content (Write, every line added). false for any other input.
func editDiff(input string) (string, bool) {
	var in struct {
		Path    string  `json:"file_path"`
		Old     string  `json:"old_string"`
		New     string  `json:"new_string"`
		Content *string `json:"content"`
		Edits   []struct {
			Old string `json:"old_string"`
			New string `json:"new_string"`
		} `json:"edits"`
	}
	if err := json.Unmarshal([]byte(input), &in); err != nil || in.Path == "" {
		return "", false
	}
	type hunk struct{ old, new string }
	var hunks []hunk
	switch {
	case len(in.Edits) > 0:
		for _, e := range in.Edits {
			hunks = append(hunks, hunk{e.Old, e.New})
		}
	case in.Old != "" || in.New != "":
		hunks = append(hunks, hunk{in.Old, in.New})
	case in.Content != nil:
		hunks = append(hunks, hunk{"", *in.Content})
	default:
		return "", false
	}
	lines := []string{"--- " + in.Path, "+++ " + in.Path}
	for k, h := range hunks {
		if k > 0 {
			lines = append(lines, "@@")
		}
		for _, l := range diffLines(h.old) {
			lines = append(lines, "-"+l)
		}
		for _, l := range diffLines(h.new) {
			lines = append(lines, "+"+l)
		}
	}
	return strings.Join(lines, "\n"), true
}

// diffLines are a text's lines for a diff, none for an empty text.
func diffLines(text string) []string {
	text = strings.TrimRight(xansi.Strip(text), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(text, "\t", "    "), "\n")
}

// joinNonEmpty joins the non-empty parts with " · ".
func joinNonEmpty(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// cappedText is at most n lines of text, then how many more there are.
func cappedText(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n… %s more (y copies the turn)", plural(len(lines)-n, "line"))
}

// markdownBody renders text as Markdown at the section's width. The card
// styles each section's text by its role, so the render's colour is
// stripped; the dark style's emphasis is colour rather than asterisks, so
// the words read plain. Glamour's two-cell margin is taken off: the
// section already indents its text.
func markdownBody(text string) func(int) string {
	return func(width int) string {
		block := markdown.Block(text, max(12, width+2), ochazuke.Appearance{Theme: "dark", Dark: true})
		lines := strings.Split(strings.ReplaceAll(xansi.Strip(block.Text), " ", " "), "\n")
		for i, l := range lines {
			lines[i] = strings.TrimRight(strings.TrimPrefix(l, "  "), " ")
		}
		return strings.Trim(strings.Join(lines, "\n"), "\n")
	}
}

// shellOutputText is a shell turn's output as its section shows it, and
// the section's summary.
func shellOutputText(pt *paneTurns, t turns.Turn) (string, string) {
	var out *turnOutput
	if pt != nil {
		out = pt.outputs[t.ID]
	}
	switch {
	case t.State == turns.StateRunning:
		return "(running: the output shows once it finishes)", "running"
	case out == nil || out.loading:
		return "loading output…", "loading"
	case out.err != "":
		return out.err, "unavailable"
	case len(out.lines) == 0:
		return "(no output)", "empty"
	}
	lines := out.lines
	head := ""
	if n := len(lines) - turnOutputShown; n > 0 {
		head = fmt.Sprintf("… %s earlier (y copies them all)\n", plural(n, "line"))
		lines = lines[n:]
	}
	return head + cardText(strings.Join(lines, "\n")), plural(len(out.lines), "line")
}

// modelRevision changes whenever what a model card draws changes: the
// logs only grow, so the lengths of what they hold say when.
func (a *App) modelRevision(t turns.Turn, pt *paneTurns) uint64 {
	sig := []any{t.State, len(t.Prompt), len(t.Steers), len(t.Final), t.Duration, a.stepShown(turns.StepThinking), a.stepShown(turns.StepTool), len(a.subs.expanded)}
	sig = append(sig, a.agentRevision(t)...)
	for _, s := range t.Steps {
		sig = append(sig, s.Kind, len(s.Text), len(s.Result), s.HasResult, s.IsError)
	}
	if pt != nil {
		if out := pt.outputs[t.ID]; out != nil {
			sig = append(sig, out.loading, out.err, len(out.lines))
		}
	}
	return revision(sig...)
}

// revision hashes what a card shows into its Revision.
func revision(parts ...any) uint64 {
	h := fnv.New64a()
	fmt.Fprint(h, parts...)
	return h.Sum64()
}

// cardsBody is the Turns tab's cards at width x height.
func (a *App) cardsBody(width, height int) ochazuke.Block {
	a.detail.panes.hide() // the timeline windows itself
	tl := a.pv.cards
	if tl == nil || tl.List().Len() == 0 {
		st := &turnFill{}
		if tl != nil {
			a.r.Placed(tl.ID() + "/list")
			if cf := a.pv.cardFills[tl.ID()]; cf != nil {
				st.model, st.source = cf.model, cf.source
			}
		}
		return ochazuke.Text(a.noTurnsText(st))
	}
	return ochazuke.Text(tl.View(width, height))
}

// turnsList is the list of whichever style the tab draws.
func (a *App) turnsList() *ochazuke.List {
	if a.turnCards() && a.pv.cards != nil {
		return a.pv.cards.List()
	}
	return a.pv.turns.List()
}

// focusTurnsView focuses the selected turn of the style the tab draws.
func (a *App) focusTurnsView() error {
	if a.turnCards() && a.pv.cards != nil {
		return a.pv.cards.Focus()
	}
	return a.pv.turns.Focus()
}

// turnRowID is the id of a pane's turn's row in the style the tab draws.
func (a *App) turnRowID(window, id string) string {
	if a.turnCards() {
		return turnCardID(window, id)
	}
	return "turn:" + id
}

// selectTurnRow selects a turn row of the style the tab draws.
func (a *App) selectTurnRow(id string) error {
	if a.turnCards() && a.pv.cards != nil {
		return a.pv.cards.Select(id)
	}
	return a.pv.turns.Select(id)
}

// cardTimeline is the timeline a card belongs to, or nil.
func (a *App) cardTimeline(card string) *ochazuke.Timeline {
	inst := a.r.Instances[card]
	if inst == nil {
		return nil
	}
	return a.r.Timeline(strings.TrimSuffix(inst.Parent, "/list"))
}

// openTurnCard is turn.open on a card: open it, mark a journal turn
// reviewed, read a shell turn's output.
func (a *App) openTurnCard(ctx ochazuke.Ctx, ref turnRef) ochazuke.Result {
	tl := a.cardTimeline(ctx.Instance.ID)
	if tl == nil {
		return ochazuke.Failed(fmt.Errorf("no timeline holds %s", ctx.Instance.ID))
	}
	if err := tl.SetOpen(ctx.Instance.ID, true); err != nil {
		return ochazuke.Failed(err)
	}
	return a.reviewTurnCard(ctx.Instance.ID, ref)
}

// reviewTurnCard is turn.review on a card: mark a journal turn reviewed,
// read a shell turn's output, and remember the card's opening was seen.
func (a *App) reviewTurnCard(id string, ref turnRef) ochazuke.Result {
	if tl := a.cardTimeline(id); tl != nil {
		if st := a.pv.cardFills[tl.ID()]; st != nil {
			st.opened[id] = true
		}
	}
	if !ref.Model {
		if err := a.markTurn(ref.Window, ref.ID); err != nil {
			return ochazuke.Failed(err)
		}
		if a.pv.turnsFor != "" && a.r.Instances[a.pv.turnsFor] != nil {
			if err := a.showTurns(a.pv.turnsFor); err != nil {
				return ochazuke.Failed(err)
			}
		}
		return ochazuke.Done(ref.ID)
	}
	return a.loadTurnOutput(ref)
}

// syncOpenedCards runs turn.review once on every card of the shown timeline
// that opened by the timeline's own keys (space, z o, z R), so however a
// card opens it is marked reviewed or reads its output.
func (a *App) syncOpenedCards() {
	tl := a.pv.cards
	if tl == nil || !a.turnCards() || a.pv.tab != tabTurns {
		return
	}
	st := a.pv.cardFills[tl.ID()]
	if st == nil {
		return
	}
	var run []string
	for _, c := range tl.Cards() {
		switch open := tl.IsOpen(c.ID); {
		case open && !st.opened[c.ID]:
			st.opened[c.ID] = true
			run = append(run, c.ID)
		case !open:
			delete(st.opened, c.ID)
		}
	}
	for _, id := range run {
		if a.r.Instances[id] == nil {
			continue
		}
		if _, err := a.r.Execute(ochazuke.Request{Verb: "review", Noun: "turn", Target: id}); err != nil {
			a.r.UI().Error(err.Error())
		}
	}
}

// refoldCards gives every thinking and tool section of every card the
// open or folded state its setting says, and redraws the cards.
func (a *App) refoldCards() {
	for _, tl := range a.pv.timelines {
		st := a.pv.cardFills[tl.ID()]
		if st == nil || !st.model {
			continue
		}
		if err := a.pushCards(tl, st); err != nil {
			a.r.UI().Error(err.Error())
			continue
		}
		for _, c := range tl.Cards() {
			for _, s := range c.Steps {
				switch {
				case strings.HasPrefix(s.ID, "thinking-"):
					_ = tl.SetFolded(c.ID, s.ID, !a.stepOpen(string(turns.StepThinking)))
				case strings.HasPrefix(s.ID, "tool-") && !strings.Contains(s.ID, "/"):
					_ = tl.SetFolded(c.ID, s.ID, !a.stepOpen(string(turns.StepTool)))
				}
			}
		}
	}
}

// turnStyleChanged rebuilds the Turns tab in the new style, the focus
// moving to the same turn when it was on one.
func (a *App) turnStyleChanged() {
	var was turnRef
	focused := false
	if inst := a.r.Instances[a.r.Focus]; inst != nil {
		if ref, ok := inst.Data.(turnRef); ok && !ref.Review && (inst.Noun == "turn" || strings.HasPrefix(inst.Noun, "turn-")) {
			was, focused = ref, true
		}
	}
	row := a.pv.turnsFor
	if row == "" || a.r.Instances[row] == nil {
		return
	}
	a.pv.turnsFor = "" // a fresh fill: the tree starts closed
	if err := a.showTurns(row); err != nil {
		a.r.UI().Error(err.Error())
		return
	}
	if focused {
		target := a.turnRowID(was.Window, was.ID)
		if a.r.Instances[target] != nil {
			_ = a.selectTurnRow(target)
		}
		if err := a.focusTurnsView(); err != nil {
			_ = a.r.SetFocus(row)
		}
	}
	if a.turnCards() {
		// The tree is not drawn: its rows go, and its fill starts over.
		delete(a.turnFills, a.pv.turns.ID())
		if err := a.pv.turns.SetNodes(nil); err != nil {
			a.r.UI().Error(err.Error())
		}
	}
}

// scrollTurns is pane.scroll-turns (J and K on a pane row while the Turns
// tab draws cards): the timeline scrolls, the row keeps the focus.
func (a *App) scrollTurns(ctx ochazuke.Ctx) ochazuke.Result {
	if a.pv.cards == nil {
		return ochazuke.Failed(fmt.Errorf("no turns shown"))
	}
	args := ochazuke.Arguments{"id": a.pv.cards.ID(), "lines": ochazuke.IntArg(ctx.Args, "lines", 1), "count": max(1, ochazuke.IntArg(ctx.Args, "count", 1))}
	// The timeline's scroll verb is guarded at its focus (it overflows):
	// it runs as from the selected card, the pane row keeping the focus.
	req := ochazuke.Request{Noun: "ochazuke.timeline", Verb: "scroll", Args: args, Target: a.pv.cards.SelectedID()}
	if _, err := a.r.Execute(req); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(nil)
}

// copyStep is y inside a card: the timeline yanks the current step into
// the registers (timeline.yank-step), then it goes to the clipboard as
// copy mode's yank does (OSC 52 when no clipboard program answers).
func (a *App) copyStep(ctx ochazuke.Ctx) ochazuke.Result {
	res, err := a.r.Execute(ochazuke.Request{Noun: "ochazuke.timeline", Verb: "yank-step", Target: a.r.Focus})
	if err != nil {
		return ochazuke.Failed(err)
	}
	state, _ := res.(map[string]any)
	text, _ := state["text"].(string)
	sys := a.cx.sys
	return ochazuke.Pending(func(c context.Context) (any, error) {
		return sys.Copy(c, text)
	}).Then(func(ctx ochazuke.Ctx, v any, err error) {
		switch osc, _ := v.(bool); {
		case err != nil:
		case osc:
			if err := a.copyOSC52(text); err != nil {
				ctx.Error(err.Error())
			}
		default:
			ctx.Notify("copied the step")
		}
	})
}

// copyStepTurn is Y inside a card: turn.copy-all on the card.
func (a *App) copyStepTurn(ctx ochazuke.Ctx) ochazuke.Result {
	tl := a.r.Timeline(strings.TrimSuffix(strings.TrimSuffix(a.r.Focus, "/body"), "/list"))
	if tl == nil || tl.SelectedID() == "" {
		return ochazuke.Failed(fmt.Errorf("not inside a turn"))
	}
	if _, err := a.r.Execute(ochazuke.Request{Noun: "turn", Verb: "copy-all", Target: tl.SelectedID()}); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(nil)
}
