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
		Mode("normal").
		Bind("l", ochazuke.Do("turn.open")).
		Bind("right", ochazuke.Do("turn.open")).
		Bind("enter", ochazuke.Do("turn.open"))
	// esc in the tree goes back up to the pane row the turns belong to, as
	// the note body's esc goes back to the notes list.
	for _, noun := range []string{"turn", "turn-line"} {
		a.r.Define(noun).
			Verb("back", a.turnsBack, "Go back to the pane row the turns belong to").
			Mode("normal").Bind("esc", ochazuke.Do(noun+".back"))
	}
	tree, err := a.r.AddTree(turnsTreeID, nil, ochazuke.Parent(panesPage))
	if err != nil {
		return err
	}
	a.pv.turns = tree
	// A stop only while the Turns tab is drawn (detailBlock keeps it so).
	a.r.Instances[turnsTreeID+"/list"].Unfocusable = true
	return nil
}

// turnLabel is a turn's row: the prompt's first line, its answers and
// when it finished.
func turnLabel(t history.PaneTurn) string {
	prompt := firstLine(t.Prompt)
	if prompt == "" {
		prompt = "(no prompt)"
	}
	switch {
	case !t.Done():
		return prompt + " · working"
	case t.ReviewedAt == 0:
		return fmt.Sprintf("%s · %s · %s · new", prompt, plural(len(t.Responses), "answer"), time.UnixMilli(t.CompletedAt).Format("Jan 2 15:04"))
	}
	return fmt.Sprintf("%s · %s · %s", prompt, plural(len(t.Responses), "answer"), time.UnixMilli(t.CompletedAt).Format("Jan 2 15:04"))
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

// textLines splits a text into lines without escapes or trailing blanks.
func textLines(text string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(xansi.Strip(text), "\n"), "\n") {
		out = append(out, "  "+strings.TrimRight(line, " \t\r"))
	}
	return out
}

// showTurns fills the Turns tree for a pane row; a new pane starts with
// every turn collapsed, the same pane keeps what is open.
func (a *App) showTurns(row string) error {
	p, err := a.paneAt(a.r.Instances[row])
	if err != nil {
		return err
	}
	turns, err := a.hist.Turns(p.ID)
	if err != nil {
		return err
	}
	var nodes []ochazuke.TreeNode
	for _, t := range turns {
		id := "turn:" + t.ID
		nodes = append(nodes, ochazuke.TreeNode{ID: id, Label: turnLabel(t), Noun: "turn", Data: t.ID})
		for j, line := range turnLines(t) {
			nodes = append(nodes, ochazuke.TreeNode{ID: fmt.Sprintf("%s/%d", id, j), Parent: id, Label: line, Noun: "turn-line"})
		}
	}
	fresh := a.pv.turnsFor != row
	if err := a.pv.turns.SetNodes(nodes); err != nil {
		return err
	}
	a.pv.turnsFor = row
	if fresh {
		return a.pv.turns.CollapseAll()
	}
	return nil
}

// turnsBack is turn.back (and turn-line.back): focus the pane row the
// Turns tab shows, else the pane list.
func (a *App) turnsBack(ochazuke.Ctx) ochazuke.Result {
	target := paneListID
	if a.r.Instances[a.pv.turnsFor] != nil {
		target = a.pv.turnsFor
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
	id, _ := ctx.Instance.Data.(string)
	p, err := a.paneAt(a.r.Instances[a.pv.turnsFor])
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.markTurn(p.ID, id); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.pv.turns.SetCollapsed(ctx.Instance.ID, false); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.showTurns(a.pv.turnsFor); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(id)
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
