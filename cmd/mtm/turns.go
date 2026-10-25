package main

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	xansi "github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// Turns: one row per agent turn of a pane, from the reviews table, oldest
// first, all collapsed when the tab opens on a pane. Opening a row (l or
// enter) shows the turn's captured screen and answer and marks its reviews
// reviewed, which the store keeps. A turn starts when a listing sees the
// agent go to working: the pane's launch review takes the turn when it has
// none yet, otherwise a new review is recorded with the owner the pane has
// at that moment (the prompt-time owner, which later moves do not
// change). When the agent stops working the turn's screen is captured into
// its review.

const turnsTreeID = "panes/turns"

// addTurns adds the Turns tab's tree and the turn noun.
func (a *App) addTurns() error {
	a.r.Define("turn").
		Verb("open", a.openTurn, "Expand the turn and mark it reviewed").
		Mode("normal").
		Bind("l", ochazuke.Do("turn.open")).
		Bind("right", ochazuke.Do("turn.open")).
		Bind("enter", ochazuke.Do("turn.open"))
	a.r.Define("turn-line")
	a.r.Define("pane").Verb("record", a.recordTurnScreen, "Capture the pane's screen into its latest turn's review").Quiet()
	tree, err := a.r.AddTree(turnsTreeID, nil, ochazuke.Parent(panesPage))
	if err != nil {
		return err
	}
	a.pv.turns = tree
	// A stop only while the Turns tab is drawn (detailBlock keeps it so).
	a.r.Instances[turnsTreeID+"/list"].Unfocusable = true
	return nil
}

// turnKey is the turn a review belongs to: its turn id, else the review
// itself (a launch that never started a turn).
func turnKey(r work.Review) string { return cmp.Or(r.TurnID, "review:"+r.ID) }

// paneTurns is the window's reviews grouped by turn, oldest first.
func (a *App) paneTurns(window string) ([][]work.Review, error) {
	reviews, err := a.work.Reviews()
	if err != nil {
		return nil, err
	}
	var order []string
	groups := map[string][]work.Review{}
	for _, r := range reviews {
		if r.Window != window {
			continue
		}
		k := turnKey(r)
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], r)
	}
	out := make([][]work.Review, 0, len(order))
	for _, k := range order {
		out = append(out, groups[k])
	}
	return out, nil
}

// showTurns fills the Turns tree for a pane row; a new pane starts with
// every turn collapsed, the same pane keeps what is open.
func (a *App) showTurns(row string) error {
	p, err := a.paneAt(a.r.Instances[row])
	if err != nil {
		return err
	}
	turns, err := a.paneTurns(p.ID)
	if err != nil {
		return err
	}
	var nodes []ochazuke.TreeNode
	for i, turn := range turns {
		id := "turn:" + turnKey(turn[0])
		first, last := turn[0], turn[len(turn)-1]
		reviewed := !slices.ContainsFunc(turn, func(r work.Review) bool { return r.ReviewedAt == 0 })
		label := fmt.Sprintf("turn %d · %s · %s", i+1, time.UnixMilli(first.Created).Format("Jan 2 15:04"), last.Status)
		if !reviewed {
			label += " · new"
		}
		nodes = append(nodes, ochazuke.TreeNode{ID: id, Label: label, Noun: "turn", Data: turnKey(first)})
		var lines []string
		for _, r := range turn {
			lines = append(lines, lastLines(r.Output, 12)...)
			if r.Answer != "" {
				lines = append(lines, "answer: "+r.Answer)
			}
		}
		if len(lines) == 0 {
			lines = []string{"(no screen captured yet)"}
		}
		for j, line := range lines {
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

// lastLines is the last n non-empty lines of a capture.
func lastLines(text string, n int) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimRight(xansi.Strip(text), "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, strings.TrimRight(line, " "))
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// openTurn is turn.open: expand the row and mark its reviews reviewed.
func (a *App) openTurn(ctx ochazuke.Ctx) ochazuke.Result {
	if ctx.Instance == nil {
		return ochazuke.Failed(fmt.Errorf("select a turn"))
	}
	key, _ := ctx.Instance.Data.(string)
	p, err := a.paneAt(a.r.Instances[a.pv.turnsFor])
	if err != nil {
		return ochazuke.Failed(err)
	}
	turns, err := a.paneTurns(p.ID)
	if err != nil {
		return ochazuke.Failed(err)
	}
	for _, turn := range turns {
		if turnKey(turn[0]) != key {
			continue
		}
		for _, r := range turn {
			if r.ReviewedAt == 0 {
				if _, err := a.work.MarkReviewed(r.ID); err != nil {
					return ochazuke.Failed(err)
				}
			}
		}
	}
	if err := a.pv.turns.SetCollapsed(ctx.Instance.ID, false); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.showTurns(a.pv.turnsFor); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(key)
}

// recordTurns compares agent states with the last listing: an agent that
// went to working starts a turn, one that stopped has its screen captured.
func (a *App) recordTurns(panes []pane) {
	for _, p := range panes {
		if p.ID == "" || !p.isAgent() {
			continue
		}
		prev, seen := a.pv.lastState[p.ID]
		a.pv.lastState[p.ID] = p.AgentState
		if !seen {
			continue
		}
		switch {
		case p.AgentState == "working" && prev != "working":
			if err := a.startTurn(p); err != nil {
				a.m.Notify("record turn: " + err.Error())
			}
		case prev == "working" && p.AgentState != "working":
			if a.r.Instances[p.rowID()] != nil {
				if _, err := a.r.Execute(ochazuke.Request{Verb: "record", Noun: "pane", Target: p.rowID()}); err != nil {
					a.m.Notify("record turn: " + err.Error())
				}
			}
		}
	}
}

// startTurn gives the pane's untouched launch review the new turn, or
// records a review for it with the pane's owner now.
func (a *App) startTurn(p pane) error {
	turns, err := a.paneTurns(p.ID)
	if err != nil {
		return err
	}
	turn := work.NewID()
	if len(turns) > 0 {
		last := turns[len(turns)-1]
		if r := last[len(last)-1]; r.TurnID == "" && r.CapturedAt == 0 {
			_, err := a.work.SetTurn(r.ID, turn)
			return err
		}
	}
	owner := a.paneOwner(p)
	_, err = a.work.AddReview(work.Review{ProjectID: owner.ProjectID, TaskID: owner.TaskID, Harness: p.Harness, Session: p.Session, Window: p.ID, Pane: p.label(), TurnID: turn})
	return err
}

// recordTurnScreen is pane.record: capture the screen off the loop into
// the pane's latest turn.
func (a *App) recordTurnScreen(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.paneAt(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	d := a.panesd
	return ochazuke.Pending(func(c context.Context) (any, error) {
		return d.CapturePane(c, p)
	}).Then(func(_ ochazuke.Ctx, v any, err error) {
		text, _ := v.(string)
		if err != nil {
			return
		}
		turns, terr := a.paneTurns(p.ID)
		if terr != nil || len(turns) == 0 {
			return
		}
		last := turns[len(turns)-1]
		if _, err := a.work.SaveOutput(last[len(last)-1].ID, text); err != nil {
			a.m.Notify("record turn: " + err.Error())
		}
	})
}
