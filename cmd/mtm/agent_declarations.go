package main

import (
	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// mtmAbout and mtmAgentRules are quoted into the MCP instructions. ochazuke
// caps each at AgentTextLimit tokens (chars/4), so the gateway's routing
// (mounted tools reached through run) lives in About and the rules here.
const mtmAbout = "Matomeru keeps projects, tasks, notes and context beside TUIOS panes. As the family MCP gateway it runs o-lane, o-md, o-git, question.question.ask|answer|follow-up and cmd.cmd.add; use no other MCP server."
const mtmAgentRules = "Track work as lanes in mtm projects (project.new if needed): lane.lane.define|set|associate (set status+reason; associate your pane). Tasks are the owner's: set status only if asked. Findings: md.doc.new, then document_report; notes: jots."

// Declarations cover app policy, not transport policy. The task and pane stores
// retain their per-record AgentPolicy checks on every request path.
var mtmAgentDeclarations = []struct {
	Verb, Right string
	Destructive bool
}{
	{"task.create", ochazuke.AgentAllow, false}, {"task.set", ochazuke.AgentAllow, false},
	{"task.done", ochazuke.AgentAllow, false}, {"task.status", ochazuke.AgentAllow, false},
	{"task.delete", "", true}, {"task.make-project", ochazuke.AgentAllow, false},
	{"project.create", ochazuke.AgentAllow, false}, {"project.new", ochazuke.AgentAllow, false},
	{"project.set", ochazuke.AgentAllow, false}, {"project.edit", ochazuke.AgentAllow, false},
	{"project.delete", "", true}, {"project.make-task", ochazuke.AgentAllow, false},
	{"project.directory", ochazuke.AgentAllow, false}, {"project.default", ochazuke.AgentAllow, false},
	{"project.remove-directory", ochazuke.AgentAllow, false}, {"project.edit-directory", ochazuke.AgentAllow, false},
	{"relation.create", ochazuke.AgentAllow, false}, {"relation.set", ochazuke.AgentAllow, false}, {"relation.move", ochazuke.AgentAllow, false},
	{"relation.archive", ochazuke.AgentAllow, false}, {"relation.follow", ochazuke.AgentAllow, false}, {"relation.yank", ochazuke.AgentAllow, false},
	{"relation.delete", "", true},
	{"outline-project.delete-project", "", true},
	{"outline-project.confirm-delete", "", true}, {"outline-root.delete-project", "", true}, {"outline-root.confirm-delete", "", true}, {"outline-root.demote", ochazuke.AgentAllow, false}, {"outline-project.demote", ochazuke.AgentAllow, false},
	{"outline-task.promote", ochazuke.AgentAllow, false},
	{"note.delete", "", true},
	{"daemon.ensure", ochazuke.AgentAllow, false}, {"daemon.start", ochazuke.AgentAllow, false}, {"daemon.status", ochazuke.AgentAllow, false},
	{"daemon.show-log", ochazuke.AgentAllow, false},
	{"daemon.stop", ochazuke.AgentNever, false}, {"daemon.restart", ochazuke.AgentNever, false}, {"daemon.serve", ochazuke.AgentNever, false}, {"daemon.import-state", ochazuke.AgentNever, false},
	{"context-item.delete", "", true},
	{"pane.kill", ochazuke.AgentConfirm, false}, {"session.kill", ochazuke.AgentConfirm, false},
	{"pane.delete", ochazuke.AgentConfirm, false}, {"session.delete", ochazuke.AgentConfirm, false},
	{"session.set", ochazuke.AgentAllow, false}, {"session.associate", ochazuke.AgentAllow, false}, {"session.choose-move", ochazuke.AgentAllow, false},
	{"saved.delete", "", true}, {"vim.delete", "", true}, {"run.forget", "", true},
	{"extension.register", ochazuke.AgentNever, false}, {"extension.refresh", ochazuke.AgentAllow, false}, {"extension.remove", ochazuke.AgentNever, true},
	{"extension.peer-rows", ochazuke.AgentAllow, false},
	{"extension.peers", ochazuke.AgentAllow, false},
	{"extension.setup", ochazuke.AgentAllow, false}, {"extension.setup-undo", ochazuke.AgentAllow, false},
	{"extension.setup-plan", ochazuke.AgentAllow, false}, {"extension.setup-status", ochazuke.AgentAllow, false},
	{"setup.install", ochazuke.AgentAllow, false}, {"setup.uninstall", ochazuke.AgentAllow, false},
	{"project-dir.remove", ochazuke.AgentAllow, false}, {"project-base.remove", ochazuke.AgentAllow, false}, {"dirs.remove-base", ochazuke.AgentAllow, false},
	{"dirs.add-base", ochazuke.AgentAllow, false}, {"project-dir.default", ochazuke.AgentAllow, false}, {"project-dir.retarget", ochazuke.AgentAllow, false},
	{"schedule.add", ochazuke.AgentAllow, false}, {"schedule.create", ochazuke.AgentAllow, false}, {"schedule.set", ochazuke.AgentAllow, false}, {"schedule.edit", ochazuke.AgentAllow, false}, {"schedule.duplicate", ochazuke.AgentAllow, false}, {"schedule.delete", "", true}, {"schedule.toggle", ochazuke.AgentAllow, false}, {"schedule.set-interval", ochazuke.AgentAllow, false}, {"schedule.reset", ochazuke.AgentAllow, false},
	{"reminder.check", ochazuke.AgentAllow, false}, {"reminder.uncheck", ochazuke.AgentAllow, false}, {"reminder.toggle", ochazuke.AgentAllow, false},
	{"reminder.delete", "", true},
}

func (a *App) defineAgentDeclarations() {
	for noun, about := range map[string]string{
		"project":          "A work context with a repository, directories, launch settings and privacy.",
		"task":             "A nested unit of work, with status, maker and last writer. Agent writes follow its lane and record policy.",
		"pane":             "A TUIOS terminal window and its task assignment, launch recipe and agent progress.",
		"note":             "Short jots, closing lines and evidence, associated with a task or project, or unassociated. Longer findings belong in a document.",
		relationNoun:       "Anything pointing to anything: noun:key ends, a kind (any word), text, a JSON data object, an order. A project's directories are relations of kind directory; unrelate archives first.",
		work.DirectoryNoun: "A directory path, the end of a project's directory relation.",
		contextNoun:        "Links, files, directories, commits, prompts and commands kept from a pane or supplied text.",
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
		a.r.Operations.Set(op)
	}

	a.bindCatalogActors()
}

// A transport's actor becomes Runtime.Actor only while the request executes.
// Map it at that boundary, including standard verbs, undo and mounted proxies;
// the pane stays bound while its current lane is looked up for every request.
func (a *App) bindCatalogActors() {
	for _, op := range a.r.Operations.Entries() {
		if handler := op.AppHandler; handler != nil {
			op.AppHandler = func(c ochazuke.Ctx) ochazuke.Result {
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
		a.r.Operations.Set(op)
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
