package main

import (
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

// Privacy verbs on both nouns (the tasks plan, Owner decision 6): a
// project is private until it is marked family, and a task's private flag
// keeps it and its subtasks private inside a family project. Each verb is
// the standard set of the prop with its value preset, so the CLI, the
// palette, a binding row and history.undo treat them as any set; none has
// a default key.
func (a *App) definePrivacyVerbs() {
	for _, alias := range []ochazuke.ActionAlias{
		{Alias: "project.make-family", Action: "project.set", Args: ochazuke.Arguments{"prop": "privacy", "value": work.PrivacyFamily},
			Note: "the standard set of privacy with family: the project's tasks may be exported (owner only)"},
		{Alias: "project.make-private", Action: "project.set", Args: ochazuke.Arguments{"prop": "privacy", "value": work.PrivacyPrivate},
			Note: "the standard set of privacy with private: the project's tasks stay in mtm"},
		{Alias: "task.make-private", Action: "task.set", Args: ochazuke.Arguments{"prop": "private", "value": true},
			Note: "the standard set of private with true: the task and its subtasks stay in mtm"},
		{Alias: "task.make-family", Action: "task.set", Args: ochazuke.Arguments{"prop": "private", "value": false},
			Note: "the standard set of private with false: the task follows its project's privacy (owner only)"},
	} {
		if err := a.r.AddActionAlias(alias); err != nil {
			panic(err)
		}
	}
}
