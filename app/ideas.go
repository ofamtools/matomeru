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
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/toast"
)

// Space and commands of the Ideas page: things the owner wants to explore
// that are not set in stone. Ideas are concepts with conceptStatus "idea";
// agents never pick them up until the owner promotes one into a task.
const (
	SpaceIdeas = "ideas"

	CmdIdeaCapture   = "idea.capture"
	CmdIdeaPromote   = "idea.promote"
	CmdIdeaList      = "idea.list"
	cmdIdeaRowsMove  = "idea.rows.move"
	cmdIdeaOpen      = "idea.open"
	ideaCaptureForm  = "idea.capture.form"
	ideasFilterField = "search"
)

// ideasController owns the Ideas page's filterable table state.
type ideasController struct {
	list    *table.Model
	visible []data.Record
}

func (a *App) initIdeas() {
	list := table.New(nil, nil)
	list.MatchMode = a.matchMode()
	list.FilterField = ideasFilterField
	a.ideas = &ideasController{list: list}
	a.registerFilterable(views.IdeasListID, list)
}

func ideasCommandSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		{ID: CmdIdeaCapture, Description: "Capture an idea: exploration only, never queued or dispatched to agents", Tags: []string{"editing"}, Params: []core.ParamSpec{
			{Name: "title", Type: core.ParamString, Description: "the idea; omitted opens the capture form"},
			{Name: "summary", Type: core.ParamString, Description: "short description"},
			{Name: "project", Type: core.ParamString, Description: "project (root task) ID; default is the active project"},
		}},
		{ID: CmdIdeaPromote, Description: "Promote an idea into an actionable task linked back to the idea", Tags: []string{"editing"}, Params: []core.ParamSpec{
			{Name: "id", Type: core.ParamString, Description: "idea (concept) ID; default is the selected idea"},
			{Name: "selected", Type: core.ParamBool, Default: false, Description: "promote the Ideas page's selected idea"},
		}},
		spec(CmdIdeaList, "Open the Ideas page: big-picture ideas agents never pick up"),
		spec(cmdIdeaOpen, "Open the selected idea"),
		{ID: cmdIdeaRowsMove, Description: "Move the Ideas selection", Params: []core.ParamSpec{
			{Name: "direction", Type: core.ParamString, Enum: []string{"up", "down"}},
		}, Meta: map[string]any{"internal": true}},
	}
}

func ideasBindings() []core.Binding {
	n, scope := ModeNormal, views.ScopeIdeas
	sel := "facts.hasSelection"
	return []core.Binding{
		bind(n, "", "g I", CmdIdeaList, "ideas"),
		bind(n, scope, "n", CmdIdeaCapture, "capture idea"),
		hidden(bind(n, scope, "c", CmdIdeaCapture, "capture idea")),
		when(with(bind(n, scope, "P", CmdIdeaPromote, "promote to task"), map[string]any{"selected": true}), sel),
		when(bind(n, scope, "enter", cmdIdeaOpen, "open idea"), sel),
		with(bind(n, scope, "j", cmdIdeaRowsMove, "next idea"), map[string]any{"direction": "down"}),
		with(bind(n, scope, "k", cmdIdeaRowsMove, "previous idea"), map[string]any{"direction": "up"}),
		hidden(with(bind(n, scope, "down", cmdIdeaRowsMove, "next idea"), map[string]any{"direction": "down"})),
		hidden(with(bind(n, scope, "up", cmdIdeaRowsMove, "previous idea"), map[string]any{"direction": "up"})),
	}
}

func (a *App) registerIdeas() {
	a.handle(CmdIdeaList, func(s core.State, _ core.Command) core.StepResult {
		return core.Next(a.openSpace(s, SpaceIdeas))
	})
	a.handle(CmdIdeaCapture, a.ideaCapture)
	a.handle(CmdIdeaPromote, a.ideaPromote)
	a.handle(cmdIdeaOpen, func(s core.State, _ core.Command) core.StepResult {
		rec, ok := a.selectedIdea()
		if !ok {
			return core.Next(s)
		}
		return core.Next(a.show(s, rec.Ref()))
	})
	a.handle(cmdIdeaRowsMove, func(s core.State, c core.Command) core.StepResult {
		a.syncIdeas()
		c.ID = "motion." + c.Arg("direction")
		a.ideas.list.HandleCommand(c)
		return core.Next(s)
	})
}

// syncIdeas loads the open ideas, newest first, into the page's table.
func (a *App) syncIdeas() {
	if a.ideas == nil || a.Store == nil {
		return
	}
	ideas, err := a.Store.Ideas("")
	if err != nil {
		a.fail("load ideas", err)
		return
	}
	rows := make([]table.Row, 0, len(ideas))
	for _, rec := range ideas {
		rows = append(rows, table.Row{ID: rec.ID, Cells: map[string]any{
			ideasFilterField: rec.Text("title") + " " + rec.Text("summary"),
		}})
	}
	shown := syncTableSelection(a.ideas.list, rows, "")
	a.ideas.visible = a.ideas.visible[:0]
	for _, r := range shown {
		if i := slices.IndexFunc(ideas, func(rec data.Record) bool { return rec.ID == r.ID }); i >= 0 {
			a.ideas.visible = append(a.ideas.visible, ideas[i])
		}
	}
}

func (a *App) ideasSpace() ui.ViewNode {
	a.syncIdeas()
	rows := make([]views.IdeaRow, 0, len(a.ideas.visible))
	for _, rec := range a.ideas.visible {
		_, project := a.resolveRefTitle(rec, data.FieldIdeaProject)
		rows = append(rows, views.IdeaRow{
			ID: rec.ID, Title: rec.Text("title"), Summary: rec.Text("summary"), Project: project,
			Updated: ideaUpdated(rec),
		})
	}
	sel := a.ideas.list.SelectedIdx
	if len(rows) == 0 {
		sel = -1
	}
	return views.IdeasView(views.IdeasViewSpec{Ideas: rows, SelectedIdx: sel, Now: a.now()})
}

func ideaUpdated(rec data.Record) time.Time {
	if !rec.UpdatedAt.IsZero() {
		return rec.UpdatedAt
	}
	return rec.CreatedAt
}

func (a *App) selectedIdea() (data.Record, bool) {
	a.syncIdeas()
	row, ok := a.ideas.list.FocusedRow()
	if !ok || row.ID == "" {
		a.say("no idea selected")
		return data.Record{}, false
	}
	i := slices.IndexFunc(a.ideas.visible, func(r data.Record) bool { return r.ID == row.ID })
	if i < 0 {
		a.say("no idea selected")
		return data.Record{}, false
	}
	return a.ideas.visible[i], true
}

// ideaCapture records an idea from args, or opens a small form for it.
func (a *App) ideaCapture(s core.State, c core.Command) core.StepResult {
	project := strings.TrimSpace(c.Arg("project"))
	if project == "" {
		project = a.queueScope(s)
	}
	if title := strings.TrimSpace(c.Arg("title")); title != "" {
		_ = a.captureIdea(data.IdeaCaptureInput{Title: title, Summary: c.Arg("summary"), ProjectID: project})
		return core.Next(s)
	}
	fm := form.New("Capture idea", []core.ParamSpec{
		{Name: "title", Type: core.ParamString, Required: true, Description: "the idea"},
		{Name: "summary", Type: core.ParamString, Description: "short description (optional)"},
	}, nil)
	fm.SubmitRow = true
	fm.SubmitLabel = "Capture"
	fm.Wrap = a.wrapInputs()
	mod := modal.NewFormModal(fm, modal.FormOptions{
		ID: ideaCaptureForm, Scope: ideaCaptureForm, Title: "Capture idea · " + views.IdeaBadge,
		WidthPct: 64, HeightPct: 40, DisableDrafts: true,
		OnSubmit: func(vals map[string]any) modal.Result {
			in := data.IdeaCaptureInput{Title: data.Text(vals["title"]), Summary: data.Text(vals["summary"]), ProjectID: project}
			if err := a.captureIdea(in); err != nil {
				return modal.Result{Handled: true, Status: err.Error(), SetStatus: true}
			}
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	return core.Next(a.pushModal(s, mod))
}

func (a *App) captureIdea(in data.IdeaCaptureInput) error {
	rec, err := a.Store.CaptureIdea(in, User)
	if err != nil {
		a.fail("capture idea", err)
		return err
	}
	msg := fmt.Sprintf("Captured idea %q · agents skip it until you promote it", rec.Text("title"))
	a.say("%s", msg)
	a.pushToast(msg, toast.Ok)
	return nil
}

// ideaPromoteTarget is the idea named by the id arg, the Ideas page's
// selection (the page binding passes selected), or the command's subject.
func (a *App) ideaPromoteTarget(s core.State, c core.Command) (string, bool) {
	if id := strings.TrimSpace(c.Arg("id")); id != "" {
		return id, true
	}
	if c.Args["selected"] == true {
		rec, ok := a.selectedIdea()
		return rec.ID, ok
	}
	if ref, ok := a.subject(s, c); ok && ref.ID != "" {
		return ref.ID, true
	}
	a.say("no idea selected to promote")
	return "", false
}

// ideaPromote turns the named or selected idea into an actionable task.
func (a *App) ideaPromote(s core.State, c core.Command) core.StepResult {
	id, ok := a.ideaPromoteTarget(s, c)
	if !ok {
		return core.Next(s)
	}
	task, err := a.Store.PromoteIdea(id, User)
	if err != nil {
		a.fail("promote idea", err)
		return core.Next(s)
	}
	a.invalidateQueueViews()
	msg := fmt.Sprintf("Promoted idea to task %q (not queued for agents)", task.Text("title"))
	a.say("%s", msg)
	a.pushToast(msg, toast.Ok)
	return core.Next(s)
}
