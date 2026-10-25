package main

import (
	"fmt"

	"github.com/unixsuperhero/ochazuke"
)

func (a *App) definePin() {
	a.r.Define("project").
		Verb("pin", a.pinProject, "Pin a project; omit project to pick one").Positional("project").
		Verb("pick", func(ochazuke.Ctx) ochazuke.Result { return a.pinProject(ochazuke.Ctx{}) }, "Choose a pin from any page").
		Verb("unpin", a.unpinProject, "Clear the pin, including after restart")
	a.parameters("project.pin", map[string]ochazuke.Parameter{"project": {Type: "string", Source: "projects.list"}})
	a.r.Define("scope").Verb("all", a.toggleAll, "Toggle all projects without changing the pin").
		Mode("normal").Bind("S", ochazuke.Do("scope.all"))
	a.r.Define("app").Mode("normal").Bind("P", ochazuke.Do("project.pick")).Bind("S", ochazuke.Do("scope.all"))
	a.r.Define("pin-choice").Verb("choose", func(ctx ochazuke.Ctx) ochazuke.Result {
		id, _ := ctx.Instance.Data.(string)
		if err := a.r.Pop(); err != nil {
			return ochazuke.Failed(err)
		}
		return a.setPin(id)
	}).Mode("normal").Bind("enter", ochazuke.Do("pin-choice.choose"))
	a.m.ModalViews["pin-picker"] = ochazuke.ModalView{Title: "Pin project", Render: func(r *ochazuke.Runtime, _ *ochazuke.Instance, w, h int) ochazuke.Block {
		if list := r.List("pin-picker/list"); list != nil {
			return ochazuke.Text(list.View(w, h))
		}
		return ochazuke.Text("")
	}}
	a.ownedBy("scope", "work")
}

func (a *App) pinProject(ctx ochazuke.Ctx) ochazuke.Result {
	if id, ok := ctx.Args["project"].(string); ok {
		return a.setPin(id)
	}
	if ctx.Instance != nil && ctx.Instance.Noun == "project" {
		return a.setPin(ctx.Instance.ID)
	}
	if _, err := a.r.OpenModal("pin-picker", ochazuke.Payload{}); err != nil {
		return ochazuke.Failed(err)
	}
	items := []ochazuke.ListItem{{ID: "pin-choice:none", Noun: "pin-choice", Label: "unpin", Data: ""}}
	for _, p := range a.projects {
		items = append(items, ochazuke.ListItem{ID: "pin-choice:" + p.ID, Noun: "pin-choice", Label: projectLabel(p), Data: p.ID})
	}
	list, err := a.r.AddList("pin-picker/list", items, ochazuke.Parent("pin-picker"))
	if err == nil {
		err = list.Focus()
	}
	if err != nil {
		_ = a.r.Pop()
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(nil)
}

func (a *App) unpinProject(ochazuke.Ctx) ochazuke.Result { return a.setPin("") }

func (a *App) setPin(id string) ochazuke.Result {
	if id != "" {
		if _, ok := a.project(id); !ok {
			return ochazuke.Failed(fmt.Errorf("unknown project %s", id))
		}
	}
	if err := a.work.SetPin(id); err != nil {
		return ochazuke.Failed(err)
	}
	a.pin = id
	a.pinVersion++
	if err := a.refreshScope(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(id)
}

func (a *App) toggleAll(ochazuke.Ctx) ochazuke.Result {
	a.all = !a.all
	if err := a.refreshScope(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(a.all)
}

func (a *App) ownerLabel(id string) string {
	if id == "" {
		return "unassociated"
	}
	if p, ok := a.project(id); ok {
		if p.Name != "" {
			return p.Name
		}
		return "untitled"
	}
	return id
}

func (a *App) refreshScope() error {
	a.m.Title = "Matomeru"
	if a.pin != "" {
		a.m.Title += " · " + a.ownerLabel(a.pin)
	}
	if a.all {
		a.m.Title += " · all"
	}
	a.preview = preview{}
	if a.attached.ID != "" && !a.inScope(a.paneOwner(a.attached).ProjectID) {
		if err := a.closeTerminal(); err != nil {
			return err
		}
	}
	if !a.inScope(a.current) {
		a.current, a.tasksOpen = a.pin, a.pin != ""
	}
	if err := a.refreshTasks(); err != nil {
		return err
	}
	a.setPanes(a.panes, nil)
	if err := a.refreshReviews(); err != nil {
		return err
	}
	if _, ok := a.openedReview(); !ok {
		a.opened = ""
		return a.setAnswerText("")
	}
	return nil
}
