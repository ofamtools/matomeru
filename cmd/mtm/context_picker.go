package main

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/contextitems"
	"github.com/unixsuperhero/matomeru/internal/history"
)

// The live picker: ctrl+a c in an attached pane (terminal, agent or vim,
// the main pane or its side) opens a chooser of context items: this pane's
// newest first, then this pane's prompts from the journal (newest first,
// each with its turn's answer previewed beside the rows), then the rest
// of its project's items, then the unassociated ones. Typing filters.
// enter copies the highlighted item (a prompt's whole text), ctrl+o opens
// it (a prompt's turn in the Turns tab), ctrl+r reveals a file or dir,
// esc returns to the live pane (the chooser's own cancel). ctrl+o and
// ctrl+r are rows on the picker's modal noun, so the footer and ? list
// them with the chooser's keys. Items and turns that land while it is
// open join its rows.

const contextPicker = "context-picker"

func (a *App) defineContextPicker() {
	a.r.Define(contextNoun).
		Verb("pick", a.pickContext, "Choose a context item or prompt of this pane, its project or no project: enter copies, ctrl+o opens, ctrl+r reveals").
		Verb("copy-prompt", a.copyPrompt, "Copy a journaled turn's whole prompt to the clipboard")
	a.parameters(contextNoun+".copy-prompt", map[string]ochazuke.Parameter{
		"window": {Type: "string", Required: true, Description: "The pane's window id"},
		"turn":   {Type: "string", Required: true, Description: "The turn's id (its prompt entry, as history lists it)"},
	})
	a.r.Define(contextPicker).
		Verb("open", a.pickerOpen, "Open the highlighted item with the default app").
		Verb("reveal", a.pickerReveal, "Reveal the highlighted file or dir in Finder").
		Mode("").
		Bind("ctrl+o", ochazuke.Do(contextPicker+".open")).
		Bind("ctrl+r", ochazuke.Do(contextPicker+".reveal"))
}

// pickedPane is the pane a live terminal instance streams: the attached
// pane, or the side pane shown beside it.
func (a *App) pickedPane(inst *ochazuke.Instance) (pane, error) {
	if inst != nil {
		if p, ok := inst.Data.(pane); ok && p.ID != "" {
			return p, nil
		}
		if inst.ID == paneTermID && a.attached.ID != "" {
			return a.attached, nil
		}
	}
	if a.attached.ID != "" {
		return a.attached, nil
	}
	return pane{}, errors.New("ctrl+a c works in a live pane")
}

// pickContext is context-item.pick (ctrl+a c).
func (a *App) pickContext(ctx ochazuke.Ctx) ochazuke.Result {
	p, err := a.pickedPane(ctx.Instance)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.reloadContextItems(); err != nil {
		return ochazuke.Failed(err)
	}
	if a.cx.turns, err = a.hist.Turns(p.ID); err != nil {
		return ochazuke.Failed(err)
	}
	a.cx.picking = p
	_, err = a.choosers.Open(ChooserSpec{
		Kind:  contextPicker,
		Title: "Context · " + p.label(),
		Rows:  a.pickerRows(p),
		// Every typed word is in the row: a long path must not match a
		// query by scattering its letters.
		Filter: func(query string, row ChooserRow) bool { return wordsMatch(query, row.Label) },
		Empty:  "no context yet: paths, URLs, PRs, branches and commits appear as panes print them",
		Choose: func(row ChooserRow) ochazuke.Result {
			if pr, ok := row.Data.(promptRow); ok {
				return a.runOnPrompt(pr)
			}
			return a.runOnItem("copy", row.ID)
		},
		Preview: pickerPreview,
	})
	return resultError(err)
}

// pickerRows are the picker's rows for pane p: its own items newest
// first, then the rest of its project's, then the unassociated ones.
// Other projects' items are left out.
func (a *App) pickerRows(p pane) []ChooserRow {
	project := a.contextOwner(p).ProjectID
	if project == "" {
		project = a.paneOwner(p).ProjectID
	}
	tasks := a.contextProjects()
	fromPane := func(it contextitems.Item) bool {
		return it.Window == p.ID || slices.ContainsFunc(it.Sources, func(s contextitems.Source) bool { return s.Window == p.ID })
	}
	var own, theirs, loose []contextitems.Item
	for _, it := range a.cx.items { // newest sighting first
		owner := itemProject(it, tasks)
		switch {
		case fromPane(it):
			own = append(own, it)
		case owner != "" && owner == project:
			theirs = append(theirs, it)
		case owner == "":
			loose = append(loose, it)
		}
	}
	now := time.Now()
	var rows []ChooserRow
	add := func(items []contextitems.Item, where string) {
		for _, it := range items {
			rows = append(rows, ChooserRow{ID: it.ID, Label: it.Kind + " " + it.Text, Data: it, Cells: []string{it.Kind, a.itemCell(it), where, age(now.Sub(time.UnixMilli(it.Seen())))}})
		}
	}
	add(own, "this pane")
	for i := len(a.cx.turns) - 1; i >= 0; i-- { // newest first
		t := a.cx.turns[i]
		if t.Window != p.ID || strings.TrimSpace(t.Prompt) == "" {
			continue
		}
		rows = append(rows, ChooserRow{ID: "prompt:" + t.ID, Label: "prompt " + t.Prompt, Data: promptRow{turn: t, pane: p},
			Cells: []string{"prompt", firstLine(t.Prompt), "this pane", age(now.Sub(time.UnixMilli(t.StartedAt)))}})
	}
	add(theirs, a.ownerLabel(project))
	add(loose, "no project")
	return rows
}

// promptRow is a prompt row's data: the turn and the pane it was asked in.
type promptRow struct {
	turn history.PaneTurn
	pane pane
}

// pickerPreview is what the picker shows beside the rows: a prompt's
// answer, an item's whole text and where it was seen.
func pickerPreview(row ChooserRow) string {
	switch d := row.Data.(type) {
	case promptRow:
		if len(d.turn.Responses) == 0 {
			return "(no answer yet)"
		}
		return d.turn.Response()
	case contextitems.Item:
		text := d.Text
		for _, src := range d.Sources {
			text += "\n· " + cmp.Or(src.Pane, src.Window, src.Session) + " (" + src.Kind + ")"
		}
		return text
	}
	return ""
}

// pickerTurnsChanged reads the picked pane's turns again while the picker
// is open (the journal took a burst), and its rows with them.
func (a *App) pickerTurnsChanged() error {
	if a.choosers.Get(contextPicker) == nil {
		return nil
	}
	turns, err := a.hist.Turns(a.cx.picking.ID)
	if err != nil {
		return err
	}
	a.cx.turns = turns
	a.contextChanged()
	return nil
}

// contextChanged refreshes the open picker's rows when items land.
func (a *App) contextChanged() {
	if c := a.choosers.Get(contextPicker); c != nil {
		c.SetRows(a.pickerRows(a.cx.picking))
	}
}

// pickerItem is the picker's highlighted row's data: a context item or
// a promptRow.
func (a *App) pickerItem() (any, *Chooser, error) {
	c := a.choosers.Get(contextPicker)
	if c == nil {
		return nil, nil, errors.New("the context picker is not open")
	}
	row, ok := c.Highlighted()
	if !ok {
		return nil, c, errors.New("no item is highlighted")
	}
	return row.Data, c, nil
}

// pickerOpen is context-picker.open (ctrl+o): close and open the item; a
// prompt's turn opens in the Turns tab.
func (a *App) pickerOpen(ochazuke.Ctx) ochazuke.Result {
	data, c, err := a.pickerItem()
	if err != nil {
		return ochazuke.Failed(err)
	}
	if pr, ok := data.(promptRow); ok {
		if err := a.choosers.close(c); err != nil {
			return ochazuke.Failed(err)
		}
		return a.openPromptTurn(pr)
	}
	it, _ := data.(contextitems.Item)
	switch it.Kind {
	case "file", "dir", "link", "pr":
	default:
		return ochazuke.Failed(fmt.Errorf("a %s has nothing to open; enter copies it", it.Kind))
	}
	if err := a.choosers.close(c); err != nil {
		return ochazuke.Failed(err)
	}
	return a.runOnItem("open", it.ID)
}

// pickerReveal is context-picker.reveal (ctrl+r): files and dirs only;
// for another kind it says so and the picker stays open.
func (a *App) pickerReveal(ochazuke.Ctx) ochazuke.Result {
	data, c, err := a.pickerItem()
	if err != nil {
		return ochazuke.Failed(err)
	}
	if _, ok := data.(promptRow); ok {
		return ochazuke.Failed(errors.New("ctrl+r reveals files and directories; this is a prompt (ctrl+o opens its turn, enter copies it)"))
	}
	it, _ := data.(contextitems.Item)
	if !it.Path() {
		return ochazuke.Failed(fmt.Errorf("ctrl+r reveals files and directories; this is a %s (ctrl+o opens it, enter copies it)", it.Kind))
	}
	if err := a.choosers.close(c); err != nil {
		return ochazuke.Failed(err)
	}
	return a.runOnItem("reveal", it.ID)
}

// runOnItem runs a context-item verb on item id for the Context list, an
// instance that outlives the picker, so the job's completion lands.
func (a *App) runOnItem(verb, id string) ochazuke.Result {
	out, err := a.r.Execute(ochazuke.Request{Verb: verb, Noun: contextNoun, Target: contextListID, Args: ochazuke.Arguments{"id": id}})
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(out)
}

// runOnPrompt copies a prompt through context-item.copy-prompt on the
// Context list, an instance that outlives the picker, so the job's
// completion lands.
func (a *App) runOnPrompt(pr promptRow) ochazuke.Result {
	out, err := a.r.Execute(ochazuke.Request{Verb: "copy-prompt", Noun: contextNoun, Target: contextListID,
		Args: ochazuke.Arguments{"window": pr.turn.Window, "turn": pr.turn.ID}})
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(out)
}

// copyPrompt is context-item.copy-prompt: the turn's whole prompt on the
// clipboard, as copy puts an item's text there.
func (a *App) copyPrompt(ctx ochazuke.Ctx) ochazuke.Result {
	window, id := ochazuke.StringArg(ctx.Args, "window", ""), ochazuke.StringArg(ctx.Args, "turn", "")
	turns, err := a.hist.Turns(window)
	if err != nil {
		return ochazuke.Failed(err)
	}
	i := slices.IndexFunc(turns, func(t history.PaneTurn) bool { return t.ID == id })
	if i < 0 {
		return ochazuke.Failed(fmt.Errorf("no turn %s in window %s", id, window))
	}
	return a.copyText(turns[i].Prompt)
}

// openPromptTurn leaves the live pane for the Panes page with the
// prompt's pane selected, the Turns tab showing, and the prompt's turn
// focused and open (turn.open, which marks it reviewed).
func (a *App) openPromptTurn(pr promptRow) ochazuke.Result {
	if a.r.Terminal(paneTermID) != nil {
		if _, err := a.r.Execute(ochazuke.Request{Noun: "terminal", Verb: "detach", Args: ochazuke.Arguments{"id": paneTermID}}); err != nil {
			return ochazuke.Failed(err)
		}
	}
	if _, err := a.r.Execute(ochazuke.Request{Verb: "open", Noun: "page", Args: ochazuke.Arguments{"id": panesPage}}); err != nil {
		return ochazuke.Failed(err)
	}
	row := pr.pane.rowID()
	if a.r.Instances[row] == nil {
		return ochazuke.Failed(fmt.Errorf("the pane list has no %s", row))
	}
	if err := a.pv.tree.Select(row); err != nil {
		return ochazuke.Failed(err)
	}
	a.pv.tab = tabTurns
	a.pv.followed = row + "|" + strconv.Itoa(tabTurns)
	if err := a.showTurns(row); err != nil {
		return ochazuke.Failed(err)
	}
	id := "turn:" + pr.turn.ID
	a.r.Instances[turnsTreeID+"/list"].Unfocusable = false
	if err := a.pv.turns.Select(id); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.r.SetFocus(id); err != nil {
		return ochazuke.Failed(err)
	}
	if _, err := a.r.Execute(ochazuke.Request{Verb: "open", Noun: "turn", Target: id}); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(id)
}
