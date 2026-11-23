package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/picker"
)

// outlineLoc describes where a task sits in the current TaskTree.
type outlineLoc struct {
	siblings    []data.TaskNode
	index       int
	parent      *data.TaskNode
	parentSibs  []data.TaskNode
	parentIndex int
	grandparent *data.TaskNode
}

func findOutlineLoc(nodes []data.TaskNode, id string, parent *data.TaskNode, parentSibs []data.TaskNode, parentIdx int, grandparent *data.TaskNode) (outlineLoc, bool) {
	for i := range nodes {
		if nodes[i].Record.ID == id {
			return outlineLoc{
				siblings:    nodes,
				index:       i,
				parent:      parent,
				parentSibs:  parentSibs,
				parentIndex: parentIdx,
				grandparent: grandparent,
			}, true
		}
		if loc, ok := findOutlineLoc(nodes[i].Children, id, &nodes[i], nodes, i, parent); ok {
			return loc, true
		}
	}
	return outlineLoc{}, false
}

func (a *App) outlineLoc() (outlineLoc, bool) {
	if a.outlineSel == "" {
		return outlineLoc{}, false
	}
	nodes, err := a.Store.TaskTree(a.showArchived)
	if err != nil {
		a.fail("outline", err)
		return outlineLoc{}, false
	}
	return findOutlineLoc(nodes, a.outlineSel, nil, nil, -1, nil)
}

func (a *App) outlineMoveSibling(delta int) func(core.State, core.Command) core.StepResult {
	return func(s core.State, c core.Command) core.StepResult {
		loc, ok := a.outlineLoc()
		if !ok {
			a.say("no task selected in outline")
			return core.Next(s)
		}
		n := len(loc.siblings)
		if n < 2 {
			a.say("no siblings to reorder")
			return core.Next(s)
		}
		target := min(max(loc.index+delta*c.Times(), 0), n-1)
		if target == loc.index {
			a.say("already at edge")
			return core.Next(s)
		}
		reordered := make([]data.TaskNode, 0, n)
		cur := loc.siblings[loc.index]
		without := slices.Delete(slices.Clone(loc.siblings), loc.index, loc.index+1)
		reordered = append(reordered, without[:target]...)
		reordered = append(reordered, cur)
		reordered = append(reordered, without[target:]...)

		patches := make([]data.PatchInput, len(reordered))
		for i, node := range reordered {
			patches[i] = data.PatchInput{
				Ref:    node.Record.Ref(),
				Fields: map[string]any{"order": int64(i + 1)},
			}
		}
		dir := "down"
		if delta < 0 {
			dir = "up"
		}
		cs, err := a.Store.PatchMany(patches, "outline.reorder", "move "+a.title(cur.Record.Ref())+" "+dir, User)
		a.wrote("moved "+a.title(cur.Record.Ref())+" "+dir, cs, err)
		return core.Next(s)
	}
}

func (a *App) outlineIndent(s core.State, _ core.Command) core.StepResult {
	loc, ok := a.outlineLoc()
	if !ok {
		a.say("no task selected in outline")
		return core.Next(s)
	}
	if loc.index == 0 {
		a.say("no preceding sibling to indent under")
		return core.Next(s)
	}
	cur := loc.siblings[loc.index]
	prev := loc.siblings[loc.index-1]
	order := int64(len(prev.Children) + 1)
	_, cs, err := a.Store.Patch(data.PatchInput{
		Ref:     cur.Record.Ref(),
		Fields:  map[string]any{"parent": prev.Record.Ref(), "order": order},
		Summary: "indent " + a.title(cur.Record.Ref()) + " under " + a.title(prev.Record.Ref()),
	}, User)
	a.wrote("indented "+a.title(cur.Record.Ref())+" under "+a.title(prev.Record.Ref()), cs, err)
	return core.Next(s)
}

func (a *App) outlineOutdent(s core.State, _ core.Command) core.StepResult {
	loc, ok := a.outlineLoc()
	if !ok {
		a.say("no task selected in outline")
		return core.Next(s)
	}
	if loc.parent == nil {
		a.say("already at root")
		return core.Next(s)
	}
	cur := loc.siblings[loc.index]
	var newParent any // nil removes parent (promotes to root)
	where := "root"
	if loc.grandparent != nil {
		newParent = loc.grandparent.Record.Ref()
		where = a.title(loc.grandparent.Record.Ref())
	}

	// Place cur immediately after its former parent in parentSibs, in one undoable batch.
	insertAt := loc.parentIndex + 1
	newSibs := make([]data.TaskNode, 0, len(loc.parentSibs)+1)
	newSibs = append(newSibs, loc.parentSibs[:insertAt]...)
	newSibs = append(newSibs, cur)
	newSibs = append(newSibs, loc.parentSibs[insertAt:]...)

	patches := make([]data.PatchInput, len(newSibs))
	for i, node := range newSibs {
		fields := map[string]any{"order": int64(i + 1)}
		if node.Record.ID == cur.Record.ID {
			fields["parent"] = newParent
		}
		patches[i] = data.PatchInput{Ref: node.Record.Ref(), Fields: fields}
	}
	cs, err := a.Store.PatchMany(patches, "outline.outdent", "outdent "+a.title(cur.Record.Ref())+" to "+where, User)
	a.wrote("outdented "+a.title(cur.Record.Ref())+" to "+where, cs, err)
	return core.Next(s)
}

func (a *App) outlineStatusCycle(s core.State, _ core.Command) core.StepResult {
	if a.outlineSel == "" {
		a.say("no task selected in outline")
		return core.Next(s)
	}
	ref := core.Ref{Kind: data.KindTask, ID: a.outlineSel}
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("status", err)
		return core.Next(s)
	}
	schema := a.schema(data.KindTask)
	col, ok := schema.StatusColumn("workStatus")
	names := []string{"todo", "active", "blocked", "done"}
	if ok && len(col.Values) > 0 {
		names = col.Names()
	}
	cur := rec.Text("workStatus")
	idx := slices.Index(names, cur)
	nextStatus := names[(idx+1)%len(names)]
	_, cs, err := a.Store.Patch(data.PatchInput{
		Ref:     ref,
		Fields:  map[string]any{"workStatus": nextStatus},
		Summary: a.title(ref) + " status -> " + nextStatus,
	}, User)
	a.wrote(a.title(ref)+" -> "+nextStatus, cs, err)
	return core.Next(s)
}

func (a *App) outlineRename(s core.State, _ core.Command) core.StepResult {
	if a.outlineSel == "" {
		a.say("no task selected in outline")
		return core.Next(s)
	}
	ref := core.Ref{Kind: data.KindTask, ID: a.outlineSel}
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("rename", err)
		return core.Next(s)
	}
	fm := views.FormForRecordWith(a.schema(data.KindTask), rec, a.editOptions())
	for i, sp := range fm.Specs {
		if sp.Name == "title" {
			fm.ActiveIdx = i
			break
		}
	}
	_, _ = fm.HandleCommand(core.Command{ID: form.CmdEdit})
	a.form = &formState{kind: data.KindTask, ref: &ref, fm: fm}
	a.say("rename %s · enter commits · esc cancels", a.title(ref))
	return core.Next(openModal(s, modalForm, ModeFormEdit, modalForm+".form"))
}

// --- global newest-first Recents picker --------------------------------------

func relativeTime(now, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

func (a *App) recentsPick(s core.State, _ core.Command) core.StepResult {
	res, err := a.Store.Query(data.Query{
		IncludeArchived: a.showArchived,
		SortField:       "updatedAt",
		SortDesc:        true,
	})
	if err != nil {
		a.fail("recents", err)
		return core.Next(s)
	}
	now := a.now()
	items := make([]picker.Item, 0, len(res.Items))
	for _, rec := range res.Items {
		ref := rec.Ref()
		schema := a.schema(rec.Kind)
		nounLabel := schema.Title
		if nounLabel == "" {
			nounLabel = rec.Kind
		}
		rel := relativeTime(now, rec.UpdatedAt)
		badges := []ui.Badge{{Text: nounLabel, Tone: "accent"}}
		if rel != "" {
			badges = append(badges, ui.Badge{Text: rel, Tone: "muted"})
		}
		items = append(items, picker.Item{
			ID:       data.RefText(ref),
			Title:    views.Title(schema, rec),
			Subtitle: strings.TrimSpace(rec.Kind + " · " + rel),
			Ref:      &ref,
			Scope:    rec.Kind,
			Badges:   badges,
			Keywords: []string{rec.Kind, nounLabel, schema.Plural, rec.ID},
		})
	}
	pm := picker.New("Recents", items)
	pm.Placeholder = "recently updated records across all nouns"
	return core.Next(a.openPicker(s, modalRecents, pm))
}
