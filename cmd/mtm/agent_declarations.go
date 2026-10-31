package main

import (
	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// mtmAbout and mtmAgentRules are quoted into the MCP instructions. ochazuke
// caps each at AgentTextLimit tokens (chars/4), so the gateway's routing
// (mounted tools reached through run) lives in About and the rules here.
const mtmAbout = "Matomeru keeps projects, tasks, notes and context beside TUIOS panes. As the family MCP gateway it runs o-lane, o-md, o-git, questions.question.ask|answer|follow-up and cmds.cmd.add; use no other MCP server."
const mtmAgentRules = "Track work as lanes in mtm projects (project.new if needed): lane.lane.define|state|associate (to your pane). Tasks are the owner's: set status only if asked. Findings: md.doc.new, then document_report; notes are jots."

// Declarations cover app policy, not transport policy. The task and pane stores
// retain their per-record AgentPolicy checks on every request path.
var mtmAgentDeclarations = []struct {
	Verb, Right string
	Destructive bool
}{
	{"task.create", ochazuke.AgentAllow, false}, {"task.set", ochazuke.AgentAllow, false}, {"task.move", ochazuke.AgentAllow, false},
	{"task.done", ochazuke.AgentAllow, false}, {"task.status", ochazuke.AgentAllow, false},
	{"task.delete", "", true}, {"task.make-project", ochazuke.AgentNever, true},
	{"project.create", ochazuke.AgentAllow, false}, {"project.new", ochazuke.AgentAllow, false},
	{"project.set", ochazuke.AgentNever, false}, {"project.edit", ochazuke.AgentNever, false}, {"project.rename", ochazuke.AgentNever, false},
	{"project.duplicate", ochazuke.AgentNever, false},
	{"project.delete", ochazuke.AgentNever, true}, {"project.make-task", ochazuke.AgentNever, true},
	{"project.directory", ochazuke.AgentNever, false}, {"project.default", ochazuke.AgentNever, false},
	{"project.remove-directory", ochazuke.AgentNever, true}, {"project.edit-directory", ochazuke.AgentNever, false},
	{"outline-project.delete", ochazuke.AgentNever, true},
	{"outline-project.confirm-delete", ochazuke.AgentNever, true}, {"outline-root.delete", ochazuke.AgentNever, true}, {"outline-root.confirm-delete", ochazuke.AgentNever, true}, {"outline-root.demote", ochazuke.AgentNever, true}, {"outline-project.demote", ochazuke.AgentNever, true},
	{"outline-task.promote", ochazuke.AgentNever, true},
	{"note.delete", ochazuke.AgentNever, true},
	{"context-item.delete", ochazuke.AgentNever, true},
	{"pane.kill", "", true}, {"session.kill", ochazuke.AgentNever, true},
	{"session.set", ochazuke.AgentNever, false}, {"session.move", ochazuke.AgentNever, false}, {"session.rename", ochazuke.AgentNever, false}, {"session.associate", ochazuke.AgentNever, false}, {"session.choose-move", ochazuke.AgentNever, false},
	{"saved.forget", "", true}, {"vim.forget", "", true}, {"run.forget", "", true},
	{"extension.register", ochazuke.AgentNever, false}, {"extension.refresh", ochazuke.AgentNever, false}, {"extension.remove", ochazuke.AgentNever, true},
	{"extension.setup", ochazuke.AgentNever, false}, {"extension.setup-undo", ochazuke.AgentNever, true},
	{"extension.setup-plan", ochazuke.AgentAllow, false}, {"extension.setup-status", ochazuke.AgentAllow, false},
	{"setup.install", ochazuke.AgentNever, false}, {"setup.uninstall", ochazuke.AgentNever, true},
	{"project-dir.remove", ochazuke.AgentNever, true}, {"project-base.remove", ochazuke.AgentNever, true}, {"dirs.remove-base", ochazuke.AgentNever, true},
	{"dirs.add-base", ochazuke.AgentNever, false}, {"project-dir.default", ochazuke.AgentNever, false}, {"project-dir.edit", ochazuke.AgentNever, false},
	{"schedule.add", ochazuke.AgentNever, false}, {"schedule.create", ochazuke.AgentNever, false}, {"schedule.set", ochazuke.AgentNever, false}, {"schedule.edit", ochazuke.AgentNever, false}, {"schedule.duplicate", ochazuke.AgentNever, false}, {"schedule.delete", ochazuke.AgentNever, true}, {"schedule.toggle", ochazuke.AgentNever, false}, {"schedule.set-interval", ochazuke.AgentNever, false}, {"schedule.reset", ochazuke.AgentNever, true},
}

func (a *App) defineAgentDeclarations() {
	for noun, about := range map[string]string{
		"project":   "A work context with a repository, directories, launch settings and privacy.",
		"task":      "A nested unit of work, with status, maker and last writer. Agent writes follow its lane and record policy.",
		"pane":      "A TUIOS terminal window and its task assignment, launch recipe and agent progress.",
		"note":      "Short jots, closing lines and evidence, associated with a task or project, or unassociated. Longer findings belong in a document.",
		contextNoun: "Links, files, directories, commits, prompts and commands kept from a pane or supplied text.",
	} {
		a.r.Define(noun).About(about)
	}
	for _, row := range mtmAgentDeclarations {
		op, ok := a.r.Operation(row.Verb)
		if !ok {
			continue
		}
		if row.Right != "" {
			op.Agent = row.Right
		}
		op.Destructive = op.Destructive || row.Destructive
		a.r.Operations[op.Key()] = op
	}

	a.bindCatalogActors()
}

// A transport's actor becomes Runtime.Actor only while the request executes.
// Map it at that boundary, including standard verbs, undo and mounted proxies;
// the pane stays bound while its current lane is looked up for every request.
func (a *App) bindCatalogActors() {
	for key, op := range a.r.Operations {
		if handler := op.App; handler != nil {
			op.App = func(c ochazuke.Ctx) ochazuke.Result {
				if err := a.useRequestActor(c.Actor()); err != nil {
					return ochazuke.Failed(err)
				}
				return handler(c)
			}
		} else {
			handler := op.Run
			op.Run = func(r *ochazuke.Runtime, c ochazuke.Context, args ochazuke.Arguments) (any, error) {
				if err := a.useRequestActor(r.Actor()); err != nil {
					return nil, err
				}
				return handler(r, c, args)
			}
		}
		a.r.Operations[key] = op
	}
}

func (a *App) useRequestActor(actor ochazuke.Actor) error {
	act := work.Actor{Kind: actor.Kind, Harness: actor.Harness, Pane: actor.Pane, Session: actor.Session, Model: actor.Model}
	if actor.IsAgent() {
		var err error
		act.Task, _, err = journalOwner(a.work)(act.Session, act.Pane)
		if err != nil {
			return err
		}
	}
	a.setActor(act)
	return nil
}
