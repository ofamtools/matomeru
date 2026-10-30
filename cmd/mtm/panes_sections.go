package main

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/actionitems"
	"github.com/unixsuperhero/matomeru/internal/panesignals"
)

// The panes page's left side is three ochazuke trees stacked top to
// bottom (matomeru-old B-09's rail sections, rebuilt as real lists):
//
//   - the queue (top, "Waiting · Review"): the open inbox items, in the
//     order panes.queue-order gives their categories: waiting (a pane's
//     needs-input signal), errored, unread (a finished turn not yet looked
//     at: a pane's done signal while it is unread), then outside items
//     (o-gh, Slack, any mtm action-item add); oldest first in each. Every
//     project's, whatever the pin.
//   - Panes (middle): every other pane, grouped by panes.group and
//     following the pin, as the tree always was.
//   - Working (bottom): agent panes while they work, oldest submission
//     first, so the newest sits at the very bottom. Every project's.
//
// Which section a pane's row lives in is data: panes.sections maps each
// signal (waiting, errored, unread, working) to queue, panes or working.
// A pane sent to the queue is drawn there by its first item's row, which
// takes the pane's row id, so every pane verb, the detail and the jumps
// (W, `, ctrl+a j) find it as they find any pane row; a pane sent to the
// queue with no item there stays in Panes (record first: a pane never
// vanishes). A terminal raises a signal only from its command items (a
// failed command is errored, a long one unread: pane_signals.go).
//
// Order is stable (panes.stable-order, on by default): a row keeps the
// rank and time it entered its section with until it leaves (tuios's
// order-stable-within-group rule). With panes.sticky-rows (off by
// default) the row under the cursor, while the cursor is on one of the
// lists, stays in its section until the cursor moves off it
// (matomeru-old reviewSticky).
//
// The unread source is the pane-signal inbox items (internal/panesignals):
// a done item is raised when a turn ends and marked read once the pane is
// attached or moves on, so the queue's rows and the Inbox page are one
// source of truth.

const (
	queueTreeID   = "panes/queue"
	workingTreeID = "panes/working"
	queueRowStart = "queue:" // a queue row that does not stand for a pane: "queue:" + item id
)

// Sections, top to bottom.
const (
	sectionQueue   = "queue"
	sectionPanes   = "panes"
	sectionWorking = "working"
)

var sectionNames = []string{sectionQueue, sectionPanes, sectionWorking}

// Signals a pane raises, and the queue's categories (the signals an item
// carries, and outside for every other source).
const (
	signalWaiting = "waiting"
	signalErrored = "errored"
	signalUnread  = "unread"
	signalWorking = "working"
	queueOutside  = "outside"
)

var paneSignals = []string{signalWaiting, signalErrored, signalUnread, signalWorking}

var queueCategories = []string{signalWaiting, signalErrored, signalUnread, queueOutside}

// Layouts of the three lists' heights.
const (
	layoutBudget = "budget"
	layoutThirds = "thirds"
)

const (
	settingPaneSections    = "panes.sections"
	settingQueueOrder      = "panes.queue-order"
	settingSectionLayout   = "panes.section-layout"
	settingQueuePercent    = "panes.queue-percent"
	settingPanesPercent    = "panes.panes-percent"
	settingWorkingPercent  = "panes.working-percent"
	defaultQueuePercent    = 25
	defaultPanesPercent    = 60
	defaultWorkingPercent  = 15
	sectionTitleQueue      = "Waiting · Review"
	sectionTitlePanes      = "Panes"
	sectionTitleWorking    = "Working"
	sectionAssignSeparator = "="
)

var defaultPaneSections = []string{"waiting=queue", "errored=queue", "unread=queue", "working=working"}

// paneSectionSettings are the sections' settings.
func paneSectionSettings() []ochazuke.Setting {
	percent := func(name string, def int, what string) ochazuke.Setting {
		return ochazuke.Setting{Name: name, Type: ochazuke.SettingInteger, Default: def,
			Description: what + " (budget layout); an empty list gives its room to the others",
			Validate: func(v any) error {
				if n, _ := v.(int); n < 0 || n > 100 {
					return fmt.Errorf("a percent from 0 to 100")
				}
				return nil
			}}
	}
	return []ochazuke.Setting{
		{Name: settingPaneSections, Type: ochazuke.SettingList, Default: defaultPaneSections,
			Description: "Which list a pane's row goes to, as signal=section: signals waiting, errored, unread, working; sections queue (top), panes (middle), working (bottom). A signal not listed stays in panes",
			Validate:    validatePaneSections},
		{Name: settingQueueOrder, Type: ochazuke.SettingList, Default: queueCategories,
			Description: "The top list's item order by category: waiting, errored, unread, outside (every other source); oldest first in each; a category left out is not in the list (the Inbox page still has it)",
			Validate: func(v any) error {
				for _, c := range listValue(v) {
					if !slices.Contains(queueCategories, c) {
						return fmt.Errorf("category %q: one of %s", c, strings.Join(queueCategories, ", "))
					}
				}
				return nil
			}},
		{Name: settingSectionLayout, Type: ochazuke.SettingString, Default: layoutBudget, Enum: []string{layoutBudget, layoutThirds},
			Description: "Heights of the Panes page's three lists: budget (the percents below; the middle takes most) or thirds"},
		percent(settingQueuePercent, defaultQueuePercent, "Share of the Panes page's left side for the top list (waiting and review)"),
		percent(settingPanesPercent, defaultPanesPercent, "Share of the Panes page's left side for the middle list (panes)"),
		percent(settingWorkingPercent, defaultWorkingPercent, "Share of the Panes page's left side for the bottom list (working)"),
	}
}

func listValue(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			s, _ := x.(string)
			out = append(out, s)
		}
		return out
	}
	return nil
}

func validatePaneSections(v any) error {
	for _, entry := range listValue(v) {
		signal, section, ok := strings.Cut(entry, sectionAssignSeparator)
		if !ok || !slices.Contains(paneSignals, signal) || !slices.Contains(sectionNames, section) {
			return fmt.Errorf("%q: signal=section, signals %s, sections %s", entry, strings.Join(paneSignals, ", "), strings.Join(sectionNames, ", "))
		}
	}
	return nil
}

// sectionFor is the panes.sections map: signal → section.
func (a *App) sectionFor() map[string]string {
	out := map[string]string{}
	for _, entry := range a.r.Settings().Strings(settingPaneSections) {
		if signal, section, ok := strings.Cut(entry, sectionAssignSeparator); ok && slices.Contains(sectionNames, section) {
			out[signal] = section
		}
	}
	return out
}

// queueRanks is panes.queue-order: category → rank.
func (a *App) queueRanks() map[string]int {
	out := map[string]int{}
	for i, c := range a.r.Settings().Strings(settingQueueOrder) {
		if _, ok := out[c]; !ok {
			out[c] = i
		}
	}
	return out
}

// itemCategory is an open item's queue category: a pane signal's own
// (needs-input is waiting; a done item only while unread), outside for
// every other source; "" for an item the queue never shows.
func itemCategory(it actionitems.Item) string {
	if it.Source != panesignals.Source {
		return queueOutside
	}
	switch it.Kind {
	case panesignals.NeedsInput:
		return signalWaiting
	case panesignals.Errored:
		return signalErrored
	case panesignals.Done:
		if !it.Read() {
			return signalUnread
		}
	case panesignals.Command:
		// A terminal command: a failed one is errored while open, one
		// that finished well (a long one) is unread until it is read.
		if panesignals.CommandFailed(it) {
			return signalErrored
		}
		if !it.Read() {
			return signalUnread
		}
	}
	return ""
}

// signalRank orders the signals an item gives its pane: the first wins.
var signalRank = []string{signalWaiting, signalErrored, signalUnread}

// paneSignal is the signal a pane raises now, "" for none: a waiting,
// errored or working agent by its state, else the signal its open items
// give it (fromItems: an unread done turn, a terminal's failed or unread
// command). An exited pane raises none.
func paneSignal(p pane, fromItems map[string]string) string {
	if p.Exited {
		return ""
	}
	if p.isAgent() {
		switch {
		case p.needsInput():
			return signalWaiting
		case p.errored():
			return signalErrored
		case p.working():
			return signalWorking
		}
	}
	return fromItems[p.ID]
}

// paneSection is one of the three lists and the rows it shows.
type paneSection struct {
	name  string
	title string
	tree  *ochazuke.Tree
	nodes []ochazuke.TreeNode
	rows  int // the rows it holds (pane and item rows; group rows not counted)
}

func (s *paneSection) listID() string { return s.tree.ID() + "/list" }

// sectionPlace is where a row sits in its section: the rank and time it
// entered with, kept until it leaves.
type sectionPlace struct {
	section string
	rank    int
	at      int64
}

// addPaneSections adds the top and bottom lists around the Panes tree,
// in drawing order, so j and k spill between them.
func (a *App) addPaneSections() error {
	queue, err := a.r.AddTree(queueTreeID, nil, ochazuke.Parent(panesPage), ochazuke.Columns(flatSectionColumns...), ochazuke.ClearFilterOnEnter())
	if err != nil {
		return err
	}
	middle, err := a.r.AddTree(paneTreeID, nil, ochazuke.Parent(panesPage), ochazuke.Columns(paneTreeColumns...), ochazuke.ClearFilterOnEnter())
	if err != nil {
		return err
	}
	working, err := a.r.AddTree(workingTreeID, nil, ochazuke.Parent(panesPage), ochazuke.Columns(flatSectionColumns...), ochazuke.ClearFilterOnEnter())
	if err != nil {
		return err
	}
	a.pv.sections = []*paneSection{
		{name: sectionQueue, title: sectionTitleQueue, tree: queue},
		{name: sectionPanes, title: sectionTitlePanes, tree: middle},
		{name: sectionWorking, title: sectionTitleWorking, tree: working},
	}
	a.pv.active = 1
	for _, s := range []*ochazuke.Tree{queue, working} {
		a.r.Instances[s.ID()+"/list"].Unfocusable = true // empty until a listing fills it
	}
	a.pv.tree = middle
	a.paneList = middle.List()
	return nil
}

// flatSectionColumns are the top and bottom lists' columns: the pane's
// (or item's) name, its state, and its use (an item's source). No titles,
// so no header line: the section title says what the list is.
var flatSectionColumns = []ochazuke.Column{
	{Min: 16},
	{Priority: 1},
	{Priority: 2},
}

// sectionListIDs are the three lists' instance ids, top to bottom.
func (a *App) sectionListIDs() []string {
	out := make([]string, 0, len(a.pv.sections))
	for _, s := range a.pv.sections {
		out = append(out, s.listID())
	}
	return out
}

// syncActiveSection makes the section whose list holds the focus the
// active one; with the focus elsewhere (the detail, live) it stays.
func (a *App) syncActiveSection() {
	for i, s := range a.pv.sections {
		if s.tree.List().Focused() {
			a.pv.active = i
			return
		}
	}
}

// activeSection is the section the detail follows.
func (a *App) activeSection() *paneSection {
	a.syncActiveSection()
	if a.pv.active < 0 || a.pv.active >= len(a.pv.sections) {
		return nil
	}
	return a.pv.sections[a.pv.active]
}

// sectionOf is the index of the section whose list holds row, else -1.
func (a *App) sectionOf(row string) int {
	inst := a.r.Instances[row]
	if inst == nil {
		return -1
	}
	for i, s := range a.pv.sections {
		if inst.Parent == s.listID() {
			return i
		}
	}
	return -1
}

// selectPaneRow selects row in whichever list holds it and makes that list
// the active one; when the focus is on one of the lists it moves to the
// row, so a jump (W, `) shows where it landed.
func (a *App) selectPaneRow(row string) error {
	i := a.sectionOf(row)
	if i < 0 {
		return fmt.Errorf("unknown tree node %q", row)
	}
	inLists := false
	for _, s := range a.pv.sections {
		inLists = inLists || s.tree.List().Focused()
	}
	if err := a.pv.sections[i].tree.Select(row); err != nil {
		return err
	}
	a.pv.active = i
	if inLists && !a.pv.sections[i].tree.List().Focused() {
		return a.r.SetFocus(row)
	}
	return nil
}

// rebuildPanes builds the three lists from the listing and the inbox and
// pushes them, keeping each list's selection.
func (a *App) rebuildPanes() error {
	if len(a.pv.sections) == 0 {
		return nil
	}
	a.stampStates(a.panes)
	row, inst := a.selectedRow()
	a.pv.view = a.buildPaneSections(a.panes, time.Now())
	if err := a.applyPaneSections(); err != nil {
		return err
	}
	if r, err := activityAt(inst); err == nil && a.sectionOf(row) < 0 {
		row = r.Row // a child row that dropped off leaves the cursor on its pane
	}
	return a.followRow(row)
}

// applyPaneSections pushes the built nodes to the trees. A row moving
// from one list to another is first taken out of the list it leaves (row
// ids are instances, held by one list at a time); then every list gets its
// nodes, keeping its selection by id.
func (a *App) applyPaneSections() error {
	ss := a.pv.sections
	goingTo := map[string]int{}
	for i, s := range ss {
		for _, n := range s.nodes {
			goingTo[n.ID] = i
		}
	}
	for i, s := range ss {
		old := s.tree.Nodes()
		kept := make([]ochazuke.TreeNode, 0, len(old))
		gone := map[string]bool{}
		for _, n := range old {
			// A row leaving takes its child rows (a pane's subagents) with it.
			if to, ok := goingTo[n.ID]; ok && to != i || gone[n.Parent] {
				gone[n.ID] = true
				continue
			}
			kept = append(kept, n)
		}
		if len(kept) < len(old) {
			if err := s.tree.SetNodes(kept); err != nil {
				return err
			}
		}
	}
	a.paneList.SetColumns(a.pv.view.columns()...)
	empty := len(a.pv.view.nodes) == 0
	for _, s := range ss {
		if err := s.tree.SetNodes(s.nodes); err != nil {
			return err
		}
		// A list is a stop while it has rows, so } and j never land in an
		// empty one; with every list empty, the list the "no panes" hint
		// is drawn for stays one (n opens a shell from it).
		if inst := a.r.Instances[s.listID()]; inst != nil {
			inst.Unfocusable = len(s.nodes) == 0 && !(empty && s.listID() == paneListID)
		}
	}
	return nil
}

// releaseHold rebuilds the lists once the cursor has left a row that was
// held in a section it no longer belongs to.
func (a *App) releaseHold() error {
	if !a.pv.held {
		return nil
	}
	if row, _ := a.selectedRow(); row == a.pv.hold {
		return nil
	}
	return a.rebuildPanes()
}

// buildPaneSections sorts the panes and the open inbox items into the
// three sections and builds each one's nodes; the view it answers holds
// the counts and the drawing order of every pane row.
func (a *App) buildPaneSections(panes []pane, now time.Time) paneView {
	sectionOf := a.sectionFor()
	ranks := a.queueRanks()
	nowMs := now.UnixMilli()
	byWindow := map[string]pane{}
	for _, p := range panes {
		if p.ID != "" {
			byWindow[p.ID] = p
		}
	}
	// The queue's items: open ones in a category panes.queue-order lists.
	type queued struct {
		it       actionitems.Item
		category string
	}
	var items []queued
	fromItems := map[string]string{} // window → the first-ranked signal its open items give it
	hasItem := map[string]bool{}     // window → an item of the queue names it
	if a.inbox != nil {
		for _, it := range a.inbox.all {
			if it.State(nowMs) != actionitems.StateOpen {
				continue
			}
			c := itemCategory(it)
			if r := slices.Index(signalRank, c); r >= 0 && it.Window != "" {
				if was, ok := fromItems[it.Window]; !ok || r < slices.Index(signalRank, was) {
					fromItems[it.Window] = c
				}
			}
			if _, ok := ranks[c]; !ok || c == "" {
				continue
			}
			items = append(items, queued{it: it, category: c})
			if it.Window != "" {
				hasItem[it.Window] = true
			}
		}
	}
	cur := ""
	if s := a.activeSection(); s != nil && a.holdsRows() {
		if n, ok := s.tree.Selected(); ok {
			cur = n.ID
		}
	}
	held := false
	if a.pv.places == nil {
		a.pv.places = map[string]sectionPlace{}
	}
	places := map[string]sectionPlace{}
	stable := a.r.Settings().Bool(settingStableOrder)
	// place is a row's rank and time in section: what it entered with
	// while it stays, else rank and at now.
	place := func(id, section string, rank int, at int64) sectionPlace {
		if old, ok := a.pv.places[id]; ok && old.section == section && stable {
			places[id] = old
			return old
		}
		p := sectionPlace{section: section, rank: rank, at: at}
		places[id] = p
		return p
	}
	// Each pane's section: its signal's, a queue without its item falling
	// back to panes; the row under the cursor stays where it was.
	section := map[string]string{}
	var middle []pane
	var working []pane
	var heldQueue []pane // panes held in the queue with no item there
	for _, p := range panes {
		if p.ID == "" {
			middle = append(middle, p)
			continue
		}
		s := cmp.Or(sectionOf[paneSignal(p, fromItems)], sectionPanes)
		if s == sectionQueue && !hasItem[p.ID] {
			s = sectionPanes
		}
		if p.rowID() == cur {
			// The row under the cursor stays in the list it is in.
			if i := a.sectionOf(cur); i >= 0 && a.pv.sections[i].name != s {
				s, held = a.pv.sections[i].name, true
				a.pv.hold = cur
			}
		}
		section[p.ID] = s
		switch s {
		case sectionWorking:
			working = append(working, p)
		case sectionQueue:
			if !hasItem[p.ID] {
				heldQueue = append(heldQueue, p)
			}
		default:
			middle = append(middle, p)
		}
	}
	a.pv.held = held
	names := uniqueNames(panes)
	// The middle: the grouped tree, following the pin.
	view := a.buildPaneTree(middle, now)
	for _, p := range panes {
		if p.ID != "" {
			view.owners[p.ID] = a.paneOwner(p).ProjectID
		}
	}
	// The queue: items, a pane's first item standing for the pane.
	type row struct {
		node ochazuke.TreeNode
		at   sectionPlace
		pane bool
	}
	var queue []row
	stands := map[string]bool{}
	for _, q := range items {
		it := q.it
		p, listed := byWindow[it.Window]
		id := queueRowStart + it.ID
		standsFor := listed && section[p.ID] == sectionQueue && !stands[p.ID]
		if standsFor {
			id, stands[p.ID] = p.rowID(), true
		}
		at := place(id, sectionQueue, ranks[q.category], it.Created)
		node := ochazuke.TreeNode{ID: id, Label: it.Label(), Noun: actionItemNoun, Data: it}
		if listed {
			node.Cells, node.Tones = a.flatPaneCells(p, names[p.ID], now)
			view.usage = view.usage || p.usageCell() != ""
		} else {
			cell := inboxGlyph(it, nowMs) + " " + it.Label()
			node.Cells = []string{cell, cmp.Or(it.Why, it.Kind), it.Source}
		}
		queue = append(queue, row{node: node, at: at, pane: standsFor})
	}
	for _, p := range heldQueue {
		at := place(p.rowID(), sectionQueue, len(queueCategories), nowMs)
		queue = append(queue, row{node: a.flatPaneNode(p, names[p.ID], now), at: at, pane: true})
	}
	slices.SortStableFunc(queue, func(x, y row) int {
		return cmp.Or(cmp.Compare(x.at.rank, y.at.rank), cmp.Compare(x.at.at, y.at.at))
	})
	var bottom []row
	for _, p := range working {
		at := place(p.rowID(), sectionWorking, 0, cmp.Or(p.StateAt, nowMs))
		bottom = append(bottom, row{node: a.flatPaneNode(p, names[p.ID], now), at: at, pane: true})
	}
	slices.SortStableFunc(bottom, func(x, y row) int { return cmp.Compare(x.at.at, y.at.at) })
	a.pv.places = places
	ss := a.pv.sections
	ss[1].nodes, ss[1].rows = view.nodes, len(view.order)
	ss[0].nodes, ss[0].rows = nil, len(queue)
	ss[2].nodes, ss[2].rows = nil, len(bottom)
	var order []string
	// children are a pane row's child rows (its subagents: pane_activity.go).
	children := func(r row) []ochazuke.TreeNode {
		if p, ok := r.node.Data.(pane); r.pane && ok {
			return a.activityNodes(p, r.node.ID, now)
		}
		if it, ok := r.node.Data.(actionitems.Item); r.pane && ok {
			return a.activityNodes(byWindow[it.Window], r.node.ID, now)
		}
		return nil
	}
	for _, r := range queue {
		ss[0].nodes = append(ss[0].nodes, r.node)
		ss[0].nodes = append(ss[0].nodes, children(r)...)
		if r.pane {
			order = append(order, r.node.ID)
		}
	}
	order = append(order, view.order...)
	for _, r := range bottom {
		ss[2].nodes = append(ss[2].nodes, r.node)
		ss[2].nodes = append(ss[2].nodes, children(r)...)
		order = append(order, r.node.ID)
		view.usage = view.usage || r.node.Cells[2] != ""
	}
	view.order = order
	view.queued, view.working = len(queue), len(bottom)
	view.nodes = slices.Concat(ss[0].nodes, ss[1].nodes, ss[2].nodes)
	view.panes, view.waiting = 0, 0
	for _, p := range panes {
		if p.ID == "" {
			continue
		}
		if section[p.ID] != sectionPanes || a.inScope(view.owners[p.ID]) || p.OwnerUnsaved || a.attached.ID == p.ID {
			view.panes++
		}
		if p.needsInput() {
			view.waiting++
			if section[p.ID] != sectionQueue {
				view.queued++ // it needs you too, though no item shows it
			}
		}
	}
	return view
}

// flatPaneNode is a pane's row in the top or bottom list.
func (a *App) flatPaneNode(p pane, name string, now time.Time) ochazuke.TreeNode {
	cells, tones := a.flatPaneCells(p, name, now)
	return ochazuke.TreeNode{ID: p.rowID(), Label: p.label(), Noun: "pane", Data: p, Cells: cells, Tones: tones}
}

// flatPaneCells are a pane's cells in a flat list (its own row, or its
// signal's item row): the middle's name cell (gutter, name, W), its state
// with its project after it, since these lists hold every project's
// panes, and its use.
func (a *App) flatPaneCells(p pane, name string, now time.Time) ([]string, []ochazuke.ToneSpan) {
	name = markerGutter(p) + " " + name
	if p.Queued > 0 {
		name += " " + queuedMarker
	}
	state := p.stateCell(now)
	if owner := a.paneOwner(p).ProjectID; owner != "" {
		state += " · " + a.ownerLabel(owner)
	}
	return []string{name, state, p.usageCell()}, paneTones(p, name)
}

// sectionHeights splits height lines between the three lists: each
// wants its rows and a title line (need, 0 for an empty list, which is
// not drawn), up to its share; what a list does not use goes to the
// others, the middle first. shares are percents.
func sectionHeights(height int, need, shares [3]int) [3]int {
	var out [3]int
	total := 0
	for i := range need {
		if need[i] > 0 {
			total += shares[i]
		}
	}
	if total == 0 {
		total = 1
	}
	left := height
	for i := range need {
		if need[i] == 0 {
			continue
		}
		out[i] = min(need[i], max(2, height*shares[i]/total))
		left -= out[i]
	}
	// Over the height (a tiny page): take lines back, the middle last.
	for _, i := range []int{2, 0, 1} {
		for left < 0 && out[i] > min(2, need[i]) {
			out[i]--
			left++
		}
	}
	for _, i := range []int{1, 0, 2} {
		if left <= 0 {
			break
		}
		more := min(left, need[i]-out[i])
		if more > 0 {
			out[i] += more
			left -= more
		}
	}
	return out
}

// sectionShares are the lists' percents for the panes.section-layout.
func (a *App) sectionShares() [3]int {
	if a.r.Settings().String(settingSectionLayout) == layoutThirds {
		return [3]int{1, 1, 1}
	}
	s := a.r.Settings()
	return [3]int{s.Int(settingQueuePercent), s.Int(settingPanesPercent), s.Int(settingWorkingPercent)}
}

// sectionNeed is the lines a list would fill: its title, a table's
// header and its rows (Tree.Height: the breadcrumb line while zoomed and
// the filter line while filtering count too); 0 when it is empty.
func (a *App) sectionNeed(s *paneSection) int {
	if len(s.nodes) == 0 {
		return 0
	}
	n := 1 + s.tree.Height()
	if s.name == sectionPanes {
		n++ // the column header
	}
	return n
}

// sectionsBlock draws the non-empty lists top to bottom in height lines,
// each under its title: the count, and how many rows its window hides.
func (a *App) sectionsBlock(width, height int) ochazuke.Block {
	if a.panelsLayout() {
		return a.panelsBlock(width, height)
	}
	s := a.m.Styles()
	var need [3]int
	for i, sec := range a.pv.sections {
		need[i] = a.sectionNeed(sec)
	}
	heights := sectionHeights(height, need, a.sectionShares())
	var lines []string
	var cursor *ochazuke.Pos
	for i, sec := range a.pv.sections {
		h := heights[i]
		if h <= 0 {
			continue
		}
		title := fmt.Sprintf("%s (%d)", sec.title, sec.rows)
		if shown := h - 1; need[i] > h && shown > 0 {
			title += fmt.Sprintf(" · +%d more", need[i]-h)
		}
		lines = append(lines, s.Muted.Render(ansi.Truncate("─ "+title+" ─", width, "…")))
		block := sec.tree.Block(width, max(1, h-1))
		if block.Cursor != nil && cursor == nil {
			cursor = &ochazuke.Pos{X: block.Cursor.X, Y: block.Cursor.Y + len(lines)}
		}
		body := strings.Split(block.Text, "\n")
		for len(body) < h-1 {
			body = append(body, "")
		}
		lines = append(lines, body[:h-1]...)
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	out := ochazuke.Text(strings.Join(lines[:height], "\n"))
	out.Cursor = cursor
	return out
}

// sectionsHead is the header's count of the outer lists: "N need you · W
// working".
func (v paneView) sectionsHead() string {
	var parts []string
	if v.queued > 0 {
		parts = append(parts, fmt.Sprintf("%d need you", v.queued))
	}
	if v.working > 0 {
		parts = append(parts, fmt.Sprintf("%d working", v.working))
	}
	return strings.Join(parts, " · ")
}

// focusOpenList puts the focus where the page opens: on the top list's
// row when the queue has rows, else the middle list's. Only a focus on
// one of the lists moves; one in the detail or a live pane stays.
func (a *App) focusOpenList() error {
	if a.r.Page != panesPage || len(a.pv.sections) == 0 {
		return nil
	}
	inLists := false
	for _, s := range a.pv.sections {
		inLists = inLists || s.tree.List().Focused()
	}
	if !inLists {
		return nil
	}
	i := 1
	if len(a.pv.sections[0].nodes) > 0 {
		i = 0
	}
	s := a.pv.sections[i]
	target := s.listID()
	if n, ok := s.tree.Selected(); ok {
		target = n.ID
	}
	if a.r.Instances[target] == nil || a.r.Focus == target {
		return nil
	}
	a.pv.active = i
	return a.r.SetFocus(target)
}

// firstPaneRow is the middle list's first pane row, "" for none.
func (a *App) firstPaneRow() string {
	for _, n := range a.pv.sections[1].nodes {
		if n.Noun == "pane" {
			return n.ID
		}
	}
	return ""
}

// listedPane is the listed, live pane with window id, if any.
func (a *App) listedPane(window string) (pane, bool) {
	if window == "" {
		return pane{}, false
	}
	for _, p := range a.panes {
		if p.ID == window && !p.Exited {
			return p, true
		}
	}
	return pane{}, false
}

// openItemPane is action-item.open on a pane's signal: the pane attached
// live on the Panes page, detach returning to its row.
func (a *App) openItemPane(ctx ochazuke.Ctx, p pane) ochazuke.Result {
	if err := a.r.OpenPage(panesPage); err != nil {
		return ochazuke.Failed(err)
	}
	back := p.rowID()
	if a.r.Instances[back] == nil && ctx.Instance != nil {
		back = ctx.Instance.ID
	}
	if a.r.Instances[back] != nil && a.sectionOf(back) >= 0 {
		if err := a.selectPaneRow(back); err != nil {
			return ochazuke.Failed(err)
		}
	}
	if err := a.attachLive(p, back); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(p.label())
}
