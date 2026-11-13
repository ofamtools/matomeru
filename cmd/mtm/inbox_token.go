package main

import (
	"fmt"
	"hash/fnv"
	"slices"
	"strconv"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/actionitems"
)

// inboxChangeToken is the action-item store's change token: it moves only
// when what the Items rows show changes. A list whose token holds is not
// read again on a refresh (ochazuke's windowed lists), so a change
// elsewhere (a task toggled on Work) costs the Inbox nothing.
//
// It is the items' version, then a hash of what the rows project from
// elsewhere: the grouping and the pane names (inboxGroupSig), whether the
// pane listing is known, the project names, each task an item names (its
// title and project), and how many snoozes have ended, the one change the
// clock makes to which rows are open. The ages a row shows are redrawn
// from the rows on the inbox timer (redrawInboxClock), never read again.
func (a *App) inboxChangeToken() (string, error) {
	token, err := a.inbox.items.Version()
	if err != nil {
		return "", err
	}
	h := fnv.New64a()
	write := func(parts ...string) {
		for _, p := range parts {
			h.Write([]byte(p))
			h.Write([]byte{0})
		}
	}
	items := a.inbox.all
	write(a.inboxGroupSig(items), strconv.FormatBool(len(a.panes) > 0 && a.paneErr == ""))
	for _, p := range a.projects {
		write(p.ID, projectName(p))
	}
	var tasks []string
	now, woke := a.inbox.items.Now(), 0
	for _, it := range items {
		if it.TaskID != "" {
			tasks = append(tasks, it.TaskID)
		}
		if it.SnoozedUntil > 0 && it.SnoozedUntil <= now {
			woke++
		}
	}
	slices.Sort(tasks)
	for _, id := range slices.Compact(tasks) {
		if t, err := a.work.Task(id); err == nil {
			write(id, t.Title, t.ProjectID)
		} else {
			write(id)
		}
	}
	write(strconv.Itoa(woke))
	return fmt.Sprintf("%s/%x", token, h.Sum64()), nil
}

// inboxClockProps are the action-item props whose cells read the clock: the
// state glyph and the age or snooze deadline.
var inboxClockProps = []string{"item", "when"}

// redrawInboxClock recomputes the clock cells of the Items rows from the
// things they hold, without reading the store; rows that read the same are
// left alone.
func (a *App) redrawInboxClock() error {
	l := a.inbox.list
	if l == nil {
		return nil
	}
	n := a.r.Noun(actionItemNoun)
	cols := map[int]*ochazuke.PropSpec{}
	for i, c := range l.Columns() {
		for j := range n.Props {
			if p := &n.Props[j]; p.Name == c.Title && p.Cell != nil && slices.Contains(inboxClockProps, p.Name) {
				cols[i] = p
			}
		}
	}
	items := l.Items()
	changed := false
	c := ochazuke.Ctx{Runtime: a.r}
	for i := range items {
		th, ok := items[i].Data.(ochazuke.Thing)
		if !ok {
			continue
		}
		if _, ok := th["_item"].(actionitems.Item); !ok {
			continue // a row that holds no item (a group's heading)
		}
		for col, p := range cols {
			if col < len(items[i].Cells) {
				if cell := p.Cell(c, th); cell != items[i].Cells[col] {
					items[i].Cells[col], changed = cell, true
				}
			}
		}
	}
	if !changed {
		return nil
	}
	return l.SetItems(items)
}
