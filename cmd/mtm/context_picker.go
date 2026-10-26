package main

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/contextitems"
)

// The live picker: ctrl+a c in an attached pane (terminal, agent or vim,
// the main pane or its side) opens a chooser of context items: this pane's
// newest first, then the rest of its project's, then the unassociated
// ones. Typing filters. enter copies the highlighted item, ctrl+o opens
// it, ctrl+r reveals a file or dir, esc returns to the live pane (the
// chooser's own cancel). ctrl+o and ctrl+r are rows on the picker's modal
// noun, so the footer and ? list them with the chooser's keys. Items that
// land while it is open join its rows.

const contextPicker = "context-picker"

func (a *App) defineContextPicker() {
	a.r.Define(contextNoun).Verb("pick", a.pickContext, "Choose a context item of this pane, its project or no project: enter copies, ctrl+o opens, ctrl+r reveals")
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
			return a.runOnItem("copy", row.ID)
		},
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
	add(theirs, a.ownerLabel(project))
	add(loose, "no project")
	return rows
}

// contextChanged refreshes the open picker's rows when items land.
func (a *App) contextChanged() {
	if c := a.choosers.Get(contextPicker); c != nil {
		c.SetRows(a.pickerRows(a.cx.picking))
	}
}

// pickerItem is the picker's highlighted item.
func (a *App) pickerItem() (contextitems.Item, *Chooser, error) {
	c := a.choosers.Get(contextPicker)
	if c == nil {
		return contextitems.Item{}, nil, errors.New("the context picker is not open")
	}
	row, ok := c.Highlighted()
	if !ok {
		return contextitems.Item{}, c, errors.New("no item is highlighted")
	}
	it, _ := row.Data.(contextitems.Item)
	return it, c, nil
}

// pickerOpen is context-picker.open (ctrl+o): close and open the item.
func (a *App) pickerOpen(ochazuke.Ctx) ochazuke.Result {
	it, c, err := a.pickerItem()
	if err != nil {
		return ochazuke.Failed(err)
	}
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
	it, c, err := a.pickerItem()
	if err != nil {
		return ochazuke.Failed(err)
	}
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
