package app

import (
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
)

// O3-Trial (Interface-First Experiment): Cross-Project Task Add Picker in Any Outline.
//
// This file is the Neon-authored interface contract. The implementing worker
// MUST ONLY implement these interfaces in NEW files (e.g. app/cross_project_add.go
// and app/cross_project_add_test.go) and MUST NOT modify existing app/outline/
// modal/picker behavior. If a capability cannot be expressed through this
// contract, stop and report the exact seam gap to Neon for a Rejection Report.

const (
	CmdOutlineAddToProject    = "outline.add.to_project"
	SetOutlineCrossProjectAdd = "ui.outlineCrossProjectAdd"
)

// CrossProjectTarget describes a project root a task may be added to.
type CrossProjectTarget struct {
	ID        string
	Title     string
	IsCurrent bool
	TaskCount int
}

// CrossProjectTaskDraft is the pure value object submitted to create a task
// under a chosen project root without altering the active outline's hoist/scope.
type CrossProjectTaskDraft struct {
	ProjectID   string
	Title       string
	Description string
}

// CrossProjectCatalog lists candidate target projects for cross-project task add.
type CrossProjectCatalog interface {
	ListTargetProjects(currentProjectID string) ([]CrossProjectTarget, error)
}

// CrossProjectTaskCreator persists a new task under the chosen project root
// and returns the created ref and ChangeSet (for undo via a.wrote).
type CrossProjectTaskCreator interface {
	CreateTaskInProject(draft CrossProjectTaskDraft, actor core.Actor) (core.Ref, *data.ChangeSet, error)
}

// CrossProjectAddFeature is the narrow feature boundary registered via
// crossProjectAddFactory in a separate implementation file.
type CrossProjectAddFeature interface {
	Catalog() CrossProjectCatalog
	Creator() CrossProjectTaskCreator
	OpenProjectPicker(s core.State, currentProjectID string) core.StepResult
}

var crossProjectAddFactory func(*App) CrossProjectAddFeature

func crossProjectAddCommandSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		spec(CmdOutlineAddToProject, "Pick a project and add a new task to it from any outline", "editing"),
	}
}

func crossProjectAddBindings() []core.Binding {
	n := ModeNormal
	return []core.Binding{
		bind(ModeOutline, "", "g o", CmdOutlineAddToProject, "add task to another project"),
		when(bind(n, views.ScopeIndex, "g o", CmdOutlineAddToProject, "add task to another project"), "facts.isTree"),
		when(bind(n, views.ScopeProjectContent, "g o", CmdOutlineAddToProject, "add task to another project"), "facts.isTree"),
	}
}

func (a *App) registerCrossProjectAdd() {
	a.handle(CmdOutlineAddToProject, func(s core.State, _ core.Command) core.StepResult {
		if !a.outlineCrossProjectAddEnabled() {
			a.say("cross-project outline add is disabled (ui.outlineCrossProjectAdd=false)")
			return core.Next(s)
		}
		if crossProjectAddFactory == nil {
			a.say("cross-project outline add is not registered")
			return core.Next(s)
		}
		feat := crossProjectAddFactory(a)
		if feat == nil {
			a.say("cross-project outline add is unavailable")
			return core.Next(s)
		}
		return feat.OpenProjectPicker(s, a.activePortalProjectID(s))
	})
}
