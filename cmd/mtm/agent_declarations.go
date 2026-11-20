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
	{"note.delete", "", true},
	{"daemon.ensure", ochazuke.AgentAllow, false}, {"daemon.start", ochazuke.AgentAllow, false}, {"daemon.status", ochazuke.AgentAllow, false},
	{"daemon.show-log", ochazuke.AgentAllow, false},
	{"daemon.stop", ochazuke.AgentNever, false}, {"daemon.restart", ochazuke.AgentNever, false}, {"daemon.upgrade", ochazuke.AgentConfirm, false}, {"daemon.serve", ochazuke.AgentNever, false}, {"daemon.import-state", ochazuke.AgentNever, false},
	{"context-item.delete", "", true},
	// pane.kill, session.associate and session.choose-move declare theirs
	// in their noun files; session.kill's stays here, set after the
	// standard session.delete is made from it.
	{"session.kill", ochazuke.AgentConfirm, false},
	{"pane.delete", ochazuke.AgentConfirm, false}, {"session.delete", "", true},
	{"session.set", ochazuke.AgentAllow, false},
	{"saved.delete", "", true}, {"vim.delete", "", true}, {"run.forget", "", true},
	{"extension.register", ochazuke.AgentNever, false}, {"extension.refresh", ochazuke.AgentAllow, false}, {"extension.remove", ochazuke.AgentNever, true},
	{"extension.peer-rows", ochazuke.AgentAllow, false},
	{"extension.peers", ochazuke.AgentAllow, false},
	{"extension.setup", ochazuke.AgentAllow, false}, {"extension.setup-undo", ochazuke.AgentAllow, false},
	{"extension.setup-plan", ochazuke.AgentAllow, false}, {"extension.setup-status", ochazuke.AgentAllow, false},
	{"setup.install", ochazuke.AgentAllow, false}, {"setup.uninstall", ochazuke.AgentAllow, false},
	{"project-dir.remove", ochazuke.AgentAllow, false}, {"project-base.remove", ochazuke.AgentAllow, false}, {"dirs.remove-base", ochazuke.AgentAllow, false},
	{"dirs.add-base", ochazuke.AgentAllow, false}, {"project-dir.default", ochazuke.AgentAllow, false}, {"project-dir.retarget", ochazuke.AgentAllow, false},
	{"directory.attach", ochazuke.AgentAllow, false}, {"directory.default", ochazuke.AgentAllow, false},
	{"schedule.add", ochazuke.AgentAllow, false}, {"schedule.create", ochazuke.AgentAllow, false}, {"schedule.set", ochazuke.AgentAllow, false}, {"schedule.edit", ochazuke.AgentAllow, false}, {"schedule.duplicate", ochazuke.AgentAllow, false}, {"schedule.delete", "", true}, {"schedule.toggle", ochazuke.AgentAllow, false}, {"schedule.set-interval", ochazuke.AgentAllow, false}, {"schedule.reset", ochazuke.AgentAllow, false},
	{"reminder.check", ochazuke.AgentAllow, false}, {"reminder.uncheck", ochazuke.AgentAllow, false}, {"reminder.toggle", ochazuke.AgentAllow, false},
	{"reminder.delete", "", true},
}

func (a *App) defineAgentDeclarations() {
	for noun, about := range map[string]string{
		"project":          "A work context with a repository, directories, launch settings and privacy.",
		"task":             "A nested unit of work, with status, maker and last writer. Agent writes follow its lane and record policy.",
		"note":             "Short jots, closing lines and evidence, associated with a task or project, or unassociated. Longer findings belong in a document.",
		relationNoun:       "Anything pointing to anything: noun:key ends, a kind (any word), text, a JSON data object, an order. A project's directories are relations of kind directory; unrelate archives first.",
		work.DirectoryNoun: "The saved directory list: ordinary paths and scan bases, metadata and project associations. Operations never create, move or remove files.",
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

// A transport's actor becomes Runtime.Actor only while the request executes
// (ochazuke scopes it, and restores a job's own actor around its
// callbacks). The work store reads it through requestActor, never a
// store-wide copy, so it ends with the request: the owner's next key,
// refresh or editor write is the owner's again. Each operation, including
// standard verbs, undo and mounted proxies, maps the actor once at its
// boundary (the pane's current lane looked up for every request) and keeps
// that mapping for the request's duration only.
func (a *App) bindCatalogActors() {
	for _, op := range a.r.Operations.Entries() {
		if handler := op.AppHandler; handler != nil {
			op.AppHandler = func(c ochazuke.Ctx) ochazuke.Result {
				restore, err := a.useRequestActor(c.Actor())
				if err != nil {
					return ochazuke.Failed(err)
				}
				defer restore()
				return handler(c)
			}
		} else {
			handler := op.Run
			op.Run = func(r *ochazuke.Runtime, c ochazuke.Context, args ochazuke.Arguments) (any, error) {
				restore, err := a.useRequestActor(r.Actor())
				if err != nil {
					return nil, err
				}
				defer restore()
				return handler(r, c, args)
			}
		}
		a.r.Operations.Set(op)
	}
}

// requestScope is the running operation's actor as the work store sees it.
type requestScope struct {
	from ochazuke.Actor // the runtime's actor it was mapped from
	act  work.Actor
	set  bool
}

// useRequestActor maps actor for the operation that is starting and
// answers the restore its caller defers (so an error or a panic in the
// handler cannot leave it set).
func (a *App) useRequestActor(actor ochazuke.Actor) (restore func(), err error) {
	act, err := a.workActor(actor)
	if err != nil {
		return nil, err
	}
	before := a.request
	a.request = requestScope{from: actor, act: act, set: true}
	return func() { a.request = before }, nil
}

// workActor is a runtime actor as the work store's: an agent's lane task
// is its pane's current lane.
// On an error it answers the actor without its lane.
func (a *App) workActor(actor ochazuke.Actor) (work.Actor, error) {
	act := work.Actor{Kind: actor.Kind, Harness: actor.Harness, Pane: actor.Pane, Session: actor.Session, Model: actor.Model}
	if !actor.IsAgent() {
		return act, nil
	}
	task, _, err := journalOwner(a.walk, a.work)(a.resolveSession(act.Session), act.Pane)
	act.Task = task
	return act, err
}

// requestActor is the work store's actor source (work.SetActorSource): the
// running operation's mapping while the runtime's actor is still the one
// it mapped; else an agent the runtime names outside an operation (a job
// callback ochazuke runs as the agent that started the job); else none,
// and the store uses the process's actor (setActor: the TUI's owner, a CLI
// or MCP server's agent). It only reads.
func (a *App) requestActor() (work.Actor, bool) {
	current := a.r.Actor()
	if a.request.set && a.request.from == current {
		return a.request.act, true
	}
	if !current.IsAgent() {
		return work.Actor{}, false
	}
	act, _ := a.workActor(current) // a lane that cannot be read: still this agent
	return act, true
}
