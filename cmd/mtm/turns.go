package main

import (
	"fmt"
	"strings"
	"time"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/history"
)

// Turns: one row per agent turn of a pane, projected from the history
// journal (journal.go, internal/history), oldest first, all collapsed when
// the tab opens on a pane. A row reads the prompt's first line, how many
// answers the turn holds and when it finished ("working" while the agent
// is on it, "new" until it is read). Opening a row (l or enter) shows the
// whole prompt and every answer, line by line with no cut, and marks a
// finished turn reviewed (review_marks). A turn keeps the owner its prompt
// was journaled with, so a later move does not move it.

const turnsTreeID = "panes/turns"

// addTurns adds the Turns tab's tree and the turn noun.
func (a *App) addTurns() error {
	a.r.Define("turn").
		Verb("open", a.openTurn, "Expand the turn and mark it reviewed").
		// pane.attach's handler on the pane the turns are of (no key:
		// enter opens the turn); ctrl+a d comes back to the turn.
		Verb("live", a.attachPane, "Attach the pane the turns are of live; ctrl+a d comes back to this turn").
		Mode("normal").
		Bind("l", ochazuke.Do("turn.open")).
		Bind("right", ochazuke.Do("turn.open")).
		Bind("enter", ochazuke.Do("turn.open"))
	// l on a pane row while the Turns tab shows moves into the turns, as
	// } does (on the Screen tab it is copy mode, copymode.go).
	a.r.Define("pane").
		Verb("turns", a.focusTurns, "Focus the Turns tab's turns").
		Mode("normal").
		Bind("l", ochazuke.Do("pane.turns").Guard("pane-tab-turns").Hinted("turns"))
	a.r.DefineGuard("pane-tab-turns", func(*ochazuke.Runtime, ochazuke.KeyEvent) bool { return a.pv.tab == tabTurns })
	// esc in the tree goes back up to the pane row the turns belong to, as
	// the note body's esc goes back to the notes list.
	for _, noun := range []string{"turn", "turn-line", "turn-step", "turn-separator"} {
		a.r.Define(noun).
			Verb("back", a.turnsBack, "Go back to the pane row the turns belong to").
			Mode("normal").Bind("esc", ochazuke.Do(noun+".back"))
	}
	// y copies a turn's response, Y its prompt and response, from its row
	// or any line of it; too large for the clipboard, or with no clipboard
	// program, it is offered as a file or a pager (turns_model.go).
	for _, noun := range []string{"turn", "turn-line", "turn-step"} {
		a.r.Define(noun).
			Verb("copy", a.copyTurn(false), "Copy the turn's response (a shell turn's whole output)").
			Verb("copy-all", a.copyTurn(true), "Copy the turn's prompt and response").
			Mode("normal").
			Bind("y", ochazuke.Do(noun+".copy")).
			Bind("Y", ochazuke.Do(noun+".copy-all"))
	}
	// A thinking or tool step opens and closes as the tree's rows do (l, h);
	// enter flips it.
	a.r.Define("turn-step").Mode("normal").Bind("enter", ochazuke.Do("outline.fold"))
	for _, name := range []string{settingTurnsThinking, settingTurnsThinkingOpen, settingTurnsTools, settingTurnsToolsOpen} {
		a.r.Settings().Observe(name, func(string, any) { a.turnSettingsChanged() })
	}
	a.r.Settings().Observe(settingTurnsStyle, func(string, any) { a.turnStyleChanged() })
	// J and K on a pane row scroll the cards, as they scroll the other
	// tabs' text (the timeline windows itself, so the page detail does not).
	a.r.DefineGuard("pane-turn-cards", func(_ *ochazuke.Runtime, ev ochazuke.KeyEvent) bool {
		return a.pv.tab == tabTurns && a.turnCards() && a.pv.cards != nil && a.pv.turnsFor == ev.Focus && a.pv.cards.List().Len() > 0
	})
	a.r.Define("pane").
		Verb("scroll-turns", a.scrollTurns, "Scroll the Turns tab's cards by lines times count; the pane row keeps the focus; up stops following the newest").Motion().When("pane-turn-cards").
		Mode("normal").
		Bind("J", ochazuke.Do("pane.scroll-turns", ochazuke.Arguments{"lines": 1}).FromPayload("count", ochazuke.SourceCount).Guard("pane-turn-cards").Hide()).
		Bind("K", ochazuke.Do("pane.scroll-turns", ochazuke.Arguments{"lines": -1}).FromPayload("count", ochazuke.SourceCount).Guard("pane-turn-cards").Hide())
	a.parameters("pane.scroll-turns", map[string]ochazuke.Parameter{
		"lines": {Type: "integer", Description: "Lines to move, down when positive (default 1)"},
		"count": {Type: "integer", Description: "Repeat count (default 1)"},
	})
	tree, err := a.r.AddTree(turnsTreeID, nil, ochazuke.Parent(panesPage))
	if err != nil {
		return err
	}
	a.pv.turns = tree
	if err := a.addPromptBox(); err != nil {
		return err
	}
	// A stop only while the Turns tab draws it: ochazuke skips a stop the
	// last frame did not place.
	return nil
}

// location is the zone the app shows times in: loc, else time.Local.
func (a *App) location() *time.Location {
	if a.loc != nil {
		return a.loc
	}
	return time.Local
}

// turnLabel is a turn's row: the prompt's first line, its answers and
// when it finished, shown in loc.
func turnLabel(t history.PaneTurn, loc *time.Location) string {
	prompt := firstLine(t.Prompt)
	if prompt == "" {
		prompt = "(no prompt)"
	}
	switch {
	case !t.Done():
		return prompt + " · working"
	case t.ReviewedAt == 0:
		return fmt.Sprintf("%s · %s · %s · new", prompt, plural(len(t.Responses), "answer"), time.UnixMilli(t.CompletedAt).In(loc).Format("Jan 2 15:04"))
	}
	return fmt.Sprintf("%s · %s · %s", prompt, plural(len(t.Responses), "answer"), time.UnixMilli(t.CompletedAt).In(loc).Format("Jan 2 15:04"))
}

// firstLine is a text's first non-empty line, trimmed.
func firstLine(text string) string {
	for _, line := range strings.Split(xansi.Strip(text), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	return ""
}

// turnLines are an opened turn's lines: the whole prompt, then every
// answer, every line of them.
func turnLines(t history.PaneTurn) []string {
	lines := []string{"prompt:"}
	lines = append(lines, textLines(t.Prompt)...)
	for i, r := range t.Responses {
		head := "answer:"
		if len(t.Responses) > 1 {
			head = fmt.Sprintf("answer %d:", i+1)
		}
		lines = append(lines, "", head)
		lines = append(lines, textLines(r.Text)...)
	}
	if len(t.Responses) == 0 {
		lines = append(lines, "", "(no answer yet)")
	}
	return lines
}

// textLines splits a text into lines without escapes or trailing blanks,
// each indented under its heading.
func textLines(text string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(xansi.Strip(text), "\n"), "\n") {
		// A tab is four spaces, so a wrapped line measures what it draws.
		line = strings.ReplaceAll(strings.TrimRight(line, " \t\r"), "\t", "    ")
		out = append(out, "  "+line)
	}
	return out
}

// turnRef is a turn tree row's data: the turn, its pane and the row esc
// goes back to.
type turnRef struct {
	Window, ID string
	Back       string // the pane row (Panes) or review row (Review) the tree shows
	Review     bool   // the row is in the Review page's tree
	Line       int    // a turn-line row's source line in turnLines (-1 on the turn's row)
	Offset     int    // the rune offset in that line where the row's piece starts (turn_wrap.go)
	Model      bool   // a turn read from the agent's log or the shell (turns_model.go), not the journal
	Step       string // a turn-step row's kind (thinking, tool)
	Card       bool   // a card of the Turns tab's timeline (turn_cards.go), not a tree row
}

// fillTurns fills a turns tree with a window's turns, each row's id
// prefixed (the Panes and Review trees hold the same turns), their lines
// wrapped at the width the tree was last drawn at (turn_wrap.go); fresh
// collapses every turn. It returns how many turns the tree holds.
func (a *App) fillTurns(tree *ochazuke.Tree, prefix, window string, back turnRef, fresh bool) (int, error) {
	turns, err := a.hist.Turns(window)
	if err != nil {
		return 0, err
	}
	st := a.turnFillOf(tree)
	st.prefix, st.window, st.back, st.turns = prefix, window, back, turns
	if err := a.setTurnNodes(tree, st); err != nil {
		return 0, err
	}
	if fresh {
		return len(turns), tree.CollapseAll()
	}
	return len(turns), nil
}

// showTurns fills the Panes page's Turns tree for a pane row; a new pane
// starts with every turn collapsed, the same pane keeps what is open.
func (a *App) showTurns(row string) error {
	if a.turnCards() {
		return a.showTurnCards(row)
	}
	p, err := a.paneAt(a.r.Instances[row])
	if err != nil {
		return err
	}
	fresh := a.pv.turnsFor != row
	if a.turnKind(p) != turnsJournal {
		a.pv.turnsFor = row
		return a.showModelTurns(row, p, fresh)
	}
	a.turnFillOf(a.pv.turns).model = false
	if _, err := a.fillTurns(a.pv.turns, "turn:", p.ID, turnRef{Back: row}, fresh); err != nil {
		return err
	}
	a.pv.turnsFor = row
	return nil
}

// focusTurns is pane.turns (l on a pane row on the Turns tab): fill the
// row's turns when they are not the ones shown, then focus the tree's
// selected row.
func (a *App) focusTurns(ctx ochazuke.Ctx) ochazuke.Result {
	if ctx.Instance == nil {
		return ochazuke.Failed(fmt.Errorf("select a pane"))
	}
	if a.pv.turnsFor != ctx.Instance.ID {
		if err := a.showTurns(ctx.Instance.ID); err != nil {
			return ochazuke.Failed(err)
		}
	}
	if a.turnsList().Len() == 0 {
		return ochazuke.Failed(fmt.Errorf("no turns recorded yet"))
	}
	if err := a.focusTurnsView(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(a.r.Focus)
}

// turnsBack is turn.back (and turn-line.back): focus the row the tree
// shows the turns of, else its list.
func (a *App) turnsBack(ctx ochazuke.Ctx) ochazuke.Result {
	var ref turnRef
	if ctx.Instance != nil {
		ref, _ = ctx.Instance.Data.(turnRef)
	}
	target := paneListID
	if ref.Review {
		target = reviewListID
	}
	if a.r.Instances[ref.Back] != nil {
		target = ref.Back
	}
	if err := a.r.SetFocus(target); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(target)
}

// openTurn is turn.open: expand the row and mark a finished turn reviewed.
func (a *App) openTurn(ctx ochazuke.Ctx) ochazuke.Result {
	if ctx.Instance == nil {
		return ochazuke.Failed(fmt.Errorf("select a turn"))
	}
	ref, _ := ctx.Instance.Data.(turnRef)
	if ref.Card {
		return a.openTurnCard(ctx, ref)
	}
	if ref.Model {
		return a.openModelTurn(ctx, ref)
	}
	if err := a.markTurn(ref.Window, ref.ID); err != nil {
		return ochazuke.Failed(err)
	}
	tree := a.pv.turns
	if ref.Review {
		tree = a.reviewTurns
	}
	if err := tree.SetCollapsed(ctx.Instance.ID, false); err != nil {
		return ochazuke.Failed(err)
	}
	if ref.Review {
		return resultError(a.showReviewTurns(false))
	}
	if err := a.showTurns(a.pv.turnsFor); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(ref.ID)
}

// markTurn marks a finished turn of the window reviewed and pushes the
// Review page's rows again (its unreviewed count drops).
func (a *App) markTurn(window, id string) error {
	turns, err := a.hist.Turns(window)
	if err != nil {
		return err
	}
	for _, t := range turns {
		if t.ID == id && t.Done() && t.ReviewedAt == 0 {
			if _, err := a.hist.MarkReviewed(t); err != nil {
				return err
			}
			return a.refreshReviews()
		}
	}
	return nil
}

// recordTurns watches agent states between listings only as a fast
// signal: an agent that went to or left working reloads the Turns tab
// (its turns come from the journal, not from the listing).
func (a *App) recordTurns(panes []pane) {
	changed := false
	for _, p := range panes {
		if p.ID == "" || !p.isAgent() {
			continue
		}
		prev, seen := a.pv.lastState[p.ID]
		a.pv.lastState[p.ID] = p.AgentState
		if seen && (prev == "working") != (p.AgentState == "working") {
			changed = true
		}
	}
	if changed {
		a.historyChanged()
	}
}
