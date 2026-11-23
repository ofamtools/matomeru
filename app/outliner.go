package app

import (
	"fmt"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/input"
)

type outlineInlineEdit struct {
	kind       string    // "rename", "below", "above"
	targetID   string    // anchor task ID (for rename, below, above)
	parentRef  *core.Ref // parent of the virtual new task (nil = top-level root)
	returnMode string    // ModeOutline or ModeNormal
	isWorkTree bool
	line       input.Line
}

func (a *App) scrollOff() int {
	if a.Settings == nil {
		return 5
	}
	return max(0, a.Settings.Int(SetScrollOff, 5))
}

func (a *App) treeViewportHeight(treeID string) int {
	if a.Shell != nil {
		if r, ok := a.Shell.State.Layout.InnerRects[treeID]; ok && r.H > 0 {
			return r.H
		}
		if a.Shell.H > 6 {
			return a.Shell.H - 6
		}
	}
	return 20
}

type visibleTreeRow struct {
	id        string
	depth     int
	visualRow int
}

// visibleTaskRows flattens the unfolded task tree into logical rows alongside
// their exact rendered visual row index (accounting for 1 blank spacer row
// between top-level items when gapTopLevel is enabled).
func (a *App) visibleTaskRows() ([]visibleTreeRow, int) {
	nodes, err := a.Store.TaskTree(a.showArchived)
	if err != nil {
		a.fail("tasks", err)
		return nil, 0
	}
	var out []visibleTreeRow
	vis := 0
	var walk func(ns []data.TaskNode, depth int)
	walk = func(ns []data.TaskNode, depth int) {
		for i, n := range ns {
			if depth == 0 && i > 0 {
				vis++ // blank spacer line between top-level items
			}
			out = append(out, visibleTreeRow{id: n.Record.ID, depth: depth, visualRow: vis})
			vis++
			if len(n.Children) > 0 && !a.taskFolded[n.Record.ID] {
				walk(n.Children, depth+1)
			}
		}
	}
	walk(nodes, 0)
	return out, vis
}

// moveTreeViewport handles cursor and viewport motions (including scrolloff
// padding, ctrl+e/ctrl+y viewport scrolling, and {/} top-level jumps) for both
// T Outline and 2 Work Tasks Tree through one shared implementation.
func (a *App) moveTreeViewport(treeID string, selID *string, offset *int, c core.Command) {
	rows, totalVis := a.visibleTaskRows()
	if len(rows) == 0 {
		*selID = ""
		*offset = 0
		return
	}
	cur := 0
	for i, r := range rows {
		if r.id == *selID {
			cur = i
			break
		}
	}
	h := a.treeViewportHeight(treeID)
	so := a.scrollOff()
	times := c.Times()

	switch c.ID {
	case "motion.scroll.down", "motion.scroll.up":
		delta := times
		if c.ID == "motion.scroll.up" {
			delta = -times
		}
		newOff, newVisSel := core.ViewportScroll(*offset, rows[cur].visualRow, totalVis, h, so, delta)
		*offset = newOff
		// Map the target visual row back to a logical task row, breaking ties
		// on blank top-level spacer rows in the scroll direction so ViewportClamp
		// does not pull the viewport back across the spacer.
		best := cur
		if delta > 0 {
			best = len(rows) - 1
			for i, r := range rows {
				if r.visualRow >= newVisSel {
					best = i
					break
				}
			}
		} else if delta < 0 {
			best = 0
			for i := len(rows) - 1; i >= 0; i-- {
				if rows[i].visualRow <= newVisSel {
					best = i
					break
				}
			}
		}
		*selID = rows[best].id
		return
	case "motion.down":
		cur += times
	case "motion.up":
		cur -= times
	case "motion.half.down":
		cur += max(h/2, 1) * times
	case "motion.half.up":
		cur -= max(h/2, 1) * times
	case "motion.page.down":
		cur += max(h, 1) * times
	case "motion.page.up":
		cur -= max(h, 1) * times
	case "motion.block.next":
		for range times {
			for i := cur + 1; i < len(rows); i++ {
				if rows[i].depth == 0 {
					cur = i
					break
				}
			}
		}
	case "motion.block.prev":
		for range times {
			for i := cur - 1; i >= 0; i-- {
				if rows[i].depth == 0 {
					cur = i
					break
				}
			}
		}
	case "motion.top":
		cur = 0
	case "motion.bottom":
		cur = len(rows) - 1
	}

	cur = min(max(cur, 0), len(rows)-1)
	*selID = rows[cur].id
	*offset = core.ViewportClamp(*offset, rows[cur].visualRow, totalVis, h, so)
}

func (a *App) outlineStartEdit(atEnd bool) func(core.State, core.Command) core.StepResult {
	return func(s core.State, _ core.Command) core.StepResult {
		id, ok := a.focusedTreeTaskID(s)
		if !ok {
			a.say("no task selected")
			return core.Next(s)
		}
		rec, err := a.Store.Get(core.Ref{Kind: data.KindTask, ID: id})
		if err != nil {
			a.fail("edit", err)
			return core.Next(s)
		}
		title := views.Title(a.schema(data.KindTask), *rec)
		var line input.Line
		line.Sync(title)
		if atEnd {
			line.Cursor = len([]rune(title))
		} else {
			line.Cursor = 0
		}
		a.outlineEdit = &outlineInlineEdit{
			kind:       "rename",
			targetID:   id,
			returnMode: s.ActiveFrame().Mode,
			isWorkTree: topModal(s) != modalOutline,
			line:       line,
		}
		a.say("inline edit · enter saves · esc cancels")
		return core.Next(withMode(s, ModeOutlineEdit))
	}
}

func (a *App) outlineStartAdd(above bool) func(core.State, core.Command) core.StepResult {
	return func(s core.State, _ core.Command) core.StepResult {
		id, ok := a.focusedTreeTaskID(s)
		var parentRef *core.Ref
		if ok {
			if rec, err := a.Store.Get(core.Ref{Kind: data.KindTask, ID: id}); err == nil {
				if pr, hasParent := rec.RefField("parent"); hasParent {
					r := pr
					parentRef = &r
				}
			}
		}
		kind := "below"
		if above {
			kind = "above"
		}
		a.outlineEdit = &outlineInlineEdit{
			kind:       kind,
			targetID:   id,
			parentRef:  parentRef,
			returnMode: s.ActiveFrame().Mode,
			isWorkTree: topModal(s) != modalOutline,
		}
		a.say("new task · tab indents · shift+tab outdents · enter saves · esc cancels")
		return core.Next(withMode(s, ModeOutlineEdit))
	}
}

func (a *App) outlineEditInsert(s core.State, c core.Command) core.StepResult {
	if a.outlineEdit == nil {
		return core.Next(s)
	}
	a.outlineEdit.line.Insert(c.Arg("text"))
	return core.Next(s)
}

func (a *App) outlineEditBackspace(s core.State, _ core.Command) core.StepResult {
	if a.outlineEdit == nil {
		return core.Next(s)
	}
	a.outlineEdit.line.HandleCommand(core.Command{ID: input.CmdBackspace})
	return core.Next(s)
}

func (a *App) outlineEditIndent(s core.State, _ core.Command) core.StepResult {
	ed := a.outlineEdit
	if ed == nil || ed.kind == "rename" {
		return core.Next(s)
	}
	nodes, err := a.Store.TaskTree(a.showArchived)
	if err != nil {
		return core.Next(s)
	}
	// Determine the node immediately above the virtual row at its current parent level.
	var newParent *core.Ref
	if ed.targetID != "" {
		if loc, ok := findOutlineLoc(nodes, ed.targetID, nil, nil, -1, nil); ok {
			sameParent := (loc.parent == nil && ed.parentRef == nil) ||
				(loc.parent != nil && ed.parentRef != nil && loc.parent.Record.ID == ed.parentRef.ID)
			if sameParent {
				if ed.kind == "below" {
					r := loc.siblings[loc.index].Record.Ref()
					newParent = &r
				} else if loc.index > 0 {
					r := loc.siblings[loc.index-1].Record.Ref()
					newParent = &r
				}
			} else if ed.parentRef == nil && loc.parent != nil {
				// Outdented to root earlier; Tab moves back under loc.parent (or its root ancestor).
				r := loc.parent.Record.Ref()
				newParent = &r
			}
		}
	}
	if newParent != nil {
		ed.parentRef = newParent
		delete(a.taskFolded, newParent.ID)
		a.say("indent under %s · enter saves", a.title(*newParent))
	} else {
		a.say("no item above to indent under")
	}
	return core.Next(s)
}

func (a *App) outlineEditOutdent(s core.State, _ core.Command) core.StepResult {
	ed := a.outlineEdit
	if ed == nil || ed.kind == "rename" {
		return core.Next(s)
	}
	if ed.parentRef == nil {
		a.say("already at top level")
		return core.Next(s)
	}
	rec, err := a.Store.Get(*ed.parentRef)
	if err != nil {
		ed.parentRef = nil
		a.say("outdented to top level")
		return core.Next(s)
	}
	if gp, ok := rec.RefField("parent"); ok {
		r := gp
		ed.parentRef = &r
		a.say("outdented under %s", a.title(r))
	} else {
		ed.parentRef = nil
		a.say("outdented to top level")
	}
	return core.Next(s)
}

func (a *App) outlineEditCancel(s core.State, _ core.Command) core.StepResult {
	if a.outlineEdit == nil {
		return core.Next(s)
	}
	ret := a.outlineEdit.returnMode
	a.outlineEdit = nil
	a.say("")
	if ret == "" {
		ret = ModeNormal
	}
	return core.Next(withMode(s, ret))
}

func (a *App) outlineEditCommit(s core.State, _ core.Command) core.StepResult {
	ed := a.outlineEdit
	if ed == nil {
		return core.Next(s)
	}
	ret := ed.returnMode
	if ret == "" {
		ret = ModeNormal
	}
	title := strings.TrimSpace(ed.line.Value())
	a.outlineEdit = nil

	if ed.kind == "rename" {
		if title == "" {
			a.say("title cannot be empty · cancelled")
			return core.Next(withMode(s, ret))
		}
		ref := core.Ref{Kind: data.KindTask, ID: ed.targetID}
		_, cs, err := a.Store.Patch(data.PatchInput{
			Ref:     ref,
			Fields:  map[string]any{"title": title},
			Summary: "rename " + title,
		}, User)
		a.wrote("renamed to "+title, cs, err)
		return core.Next(withMode(s, ret))
	}

	if title == "" {
		a.say("cancelled")
		return core.Next(withMode(s, ret))
	}

	fields := map[string]any{"title": title}
	if ed.parentRef != nil {
		fields["parent"] = *ed.parentRef
	}
	created, cs, err := a.Store.Create(data.CreateInput{Kind: data.KindTask, Fields: fields}, User)
	if err != nil {
		a.fail("new task", err)
		return core.Next(withMode(s, ret))
	}

	// Reorder siblings so the new item lands in the exact below/above position.
	if nodes, terr := a.Store.TaskTree(a.showArchived); terr == nil {
		a.positionCreatedTask(nodes, *created, ed)
	}

	if ed.isWorkTree {
		a.workTreeSel = created.ID
		if tbl := a.tables[workTable]; tbl != nil {
			refID := data.RefText(created.Ref())
			if selectRow(tbl, refID) {
				a.workTreeTableRowID = refID
			}
		}
	} else {
		a.outlineSel = created.ID
	}
	a.wrote("created "+title, cs, nil)
	return core.Next(withMode(s, ret))
}

func (a *App) positionCreatedTask(nodes []data.TaskNode, created data.Record, ed *outlineInlineEdit) {
	if ed.targetID == "" {
		return
	}
	loc, ok := findOutlineLoc(nodes, ed.targetID, nil, nil, -1, nil)
	if !ok {
		return
	}
	// Only reorder when the new task shares the same parent as targetID (sibling insert).
	sameParent := (loc.parent == nil && ed.parentRef == nil) ||
		(loc.parent != nil && ed.parentRef != nil && loc.parent.Record.ID == ed.parentRef.ID)
	if !sameParent {
		return
	}
	var others []data.TaskNode
	for _, sib := range loc.siblings {
		if sib.Record.ID == created.ID {
			continue
		}
		others = append(others, sib)
	}
	insertAt := len(others)
	for i, sib := range others {
		if sib.Record.ID == ed.targetID {
			if ed.kind == "above" {
				insertAt = i
			} else {
				insertAt = i + 1
			}
			break
		}
	}
	ordered := make([]data.Record, 0, len(others)+1)
	for i := 0; i < insertAt; i++ {
		ordered = append(ordered, others[i].Record)
	}
	ordered = append(ordered, created)
	for i := insertAt; i < len(others); i++ {
		ordered = append(ordered, others[i].Record)
	}
	patches := make([]data.PatchInput, len(ordered))
	for i, rec := range ordered {
		patches[i] = data.PatchInput{Ref: rec.Ref(), Fields: map[string]any{"order": int64(i + 1)}}
	}
	_, _ = a.Store.PatchMany(patches, "outline.order", fmt.Sprintf("order %s", created.Text("title")), daemonSyncActor)
}
