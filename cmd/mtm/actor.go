package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/unixsuperhero/matomeru/internal/actionitems"
	"github.com/unixsuperhero/matomeru/internal/mcp"
	"github.com/unixsuperhero/matomeru/internal/work"
	"github.com/unixsuperhero/ochazuke"
)

// Who a write runs as (the tasks plan, section 6, lane A). The actor is
// decided once, where the call comes in, and set on the work store, which
// stamps it on every task write and checks there what an agent may
// change (internal/work/actor.go):
//
//   - the TUI is the owner (the store's default);
//   - the MCP server is the agent of its pane (mcpBackend.Run, from the
//     tool call's Identity: pane, session, task, harness, model);
//   - the CLI (shell Prepare) is MTM_ACTOR when it is set (owner, agent,
//     or an actor as JSON), else the agent of the pane it runs in when
//     that pane runs one (the pane's recipe names a harness, or a
//     harness's agentEnv variable is set), else the owner;
//   - an extension mtm runs is told the actor in MTM_ACTOR, so its own
//     mtm calls run as the same one.

// The settings of what agents may change (work.AgentPolicy).
const (
	settingAgentsWriteScope     = "agents.write-scope"
	settingAgentsWriteMakers    = "agents.write-makers"
	settingAgentsOwnerTaskProps = "agents.owner-task-props"
	settingAgentsAssociateScope = "agents.associate-scope"
)

// agentSettings are the agents.* settings.
func agentSettings() []ochazuke.Setting {
	def := work.DefaultAgentPolicy()
	return []ochazuke.Setting{
		{Name: settingAgentsWriteScope, Type: ochazuke.SettingString, Default: def.WriteScope, Enum: work.WriteScopes,
			Description: "Which tasks an agent may change besides those its pane or lane made: subtree (its lane task and the tasks under it), own (none), project (its pane's project) or any; the owner is never limited"},
		{Name: settingAgentsWriteMakers, Type: ochazuke.SettingList, Default: def.WriteMakers,
			Description: "Whose tasks inside agents.write-scope an agent may change wholly, by made-by: agent, unknown (made before mtm recorded makers), owner; any other maker's tasks change only in agents.owner-task-props"},
		{Name: settingAgentsOwnerTaskProps, Type: ochazuke.SettingList, Default: def.OwnerTaskProps,
			Description: "The props an agent may change on a task inside agents.write-scope that the owner made (or that is unknown): status by default (done, active, blocked); title, project, parent, position, easy, snoozed-until, private and theme can be added; a refused agent is told to ask the owner or the coordinator"},
		{Name: settingAgentsAssociateScope, Type: ochazuke.SettingString, Default: def.AssociateScope, Enum: work.AssociateScopes,
			Description: "Which panes an agent may associate with a task or project: own-pane (its own and the panes it launched or opened beside it), any-pane or none"},
	}
}

// agentPolicy is the agents.* settings as the store reads them.
func (a *App) agentPolicy() work.AgentPolicy {
	s := a.r.Settings()
	return work.AgentPolicy{
		WriteScope:     s.String(settingAgentsWriteScope),
		WriteMakers:    s.Strings(settingAgentsWriteMakers),
		OwnerTaskProps: s.Strings(settingAgentsOwnerTaskProps),
		AssociateScope: s.String(settingAgentsAssociateScope),
	}
}

// actorEnv is the variable that names the CLI's actor.
const actorEnv = "MTM_ACTOR"

// cliActor is who a CLI call runs as (see the top of this file). A
// MTM_ACTOR that does not parse is reported and ignored.
func (a *App) cliActor() (work.Actor, error) {
	var bad error
	if v := strings.TrimSpace(a.getenv(actorEnv)); v != "" {
		act, err := work.ParseActor(v)
		if err == nil {
			if act.IsAgent() {
				return a.paneAgent(act), nil
			}
			return act, nil
		}
		bad = fmt.Errorf("%s: %w; using the pane's actor", actorEnv, err)
	}
	pane := a.envPane()
	if pane == "" {
		return work.Owner(), bad
	}
	act := work.Actor{Kind: work.ActorAgent}
	if r, err := a.work.PaneRecipe(pane); err == nil && r.Harness != "" {
		act.Harness, act.Model = r.Harness, r.Model
	} else if a.harnesses != nil {
		act.Harness = a.harnesses.AgentFromEnv(a.getenv)
	}
	if act.Harness == "" {
		return work.Owner(), bad // a pane with no agent in it: the owner's shell
	}
	return a.paneAgent(act), bad
}

// envPane is the pane this process runs in (MTM_WINDOW overrides
// TUIOS_PANE_ID, as for the MCP server).
func (a *App) envPane() string {
	if w := a.getenv("MTM_WINDOW"); w != "" {
		return w
	}
	return a.getenv("TUIOS_PANE_ID")
}

// paneAgent fills an agent actor's pane, session and lane task from the
// environment and the pane's association where it gives none.
func (a *App) paneAgent(act work.Actor) work.Actor {
	if act.Pane == "" {
		act.Pane = a.envPane()
	}
	if act.Session == "" {
		act.Session = a.getenv("MTM_SESSION")
		if act.Session == "" {
			act.Session = a.getenv("TUIOS_SESSION")
		}
	}
	if act.Task == "" && act.Pane != "" {
		act.Task, _, _ = journalOwner(a.work)(act.Session, act.Pane)
	}
	if act.Harness == "" || act.Model == "" {
		if r, err := a.work.PaneRecipe(act.Pane); err == nil {
			act.Harness = cmpOr(act.Harness, r.Harness)
			act.Model = cmpOr(act.Model, r.Model)
		}
	}
	return act
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// mcpActor is the agent an MCP tool call runs as: its pane's identity.
func mcpActor(id mcp.Identity) work.Actor {
	return work.Actor{Kind: work.ActorAgent, Harness: id.Harness, Pane: id.Window, Session: id.Session, Task: id.TaskID, Model: id.Model}
}

// setActor makes the app's writes run as act from now on.
func (a *App) setActor(act work.Actor) { a.work.SetActor(act) }

// actorEnvValue is MTM_ACTOR for an extension: the actor as JSON.
func (a *App) actorEnvValue() string {
	raw, err := json.Marshal(a.work.Actor())
	if err != nil {
		return work.ActorOwner
	}
	return string(raw)
}

// The Work page and the tasks modal show who made each task (opt-outs
// below). The framework filters task rows by their props on every list.
const (
	settingTasksColumns      = "tasks.columns"
	settingTasksAgentGlyph   = "tasks.agent-glyph"
	settingTasksPrivateGlyph = "tasks.private-glyph"
	settingTasksMadeBy       = "tasks.made-by"
	settingWorkTasksFilter   = "work.tasks-filter"
	settingPrivacyInbox      = "projects.privacy-inbox"

	columnBy = "by"

	madeByAll    = "all"
	madeByMine   = "mine"
	madeByAgents = "agents"

	agentGlyph   = "✦"
	privateGlyph = "🔒"
)

// taskViewSettings are the settings of how task rows show who made them.
func taskViewSettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingTasksColumns, Type: ochazuke.SettingList, Default: []string{columnBy},
			Description: "The extra columns of the Work page's tasks and the tasks outline: by (who made the task: me, the agent's harness, or ? for unknown); [] shows none"},
		{Name: settingTasksAgentGlyph, Type: ochazuke.SettingBoolean, Default: true,
			Description: "Mark tasks an agent made with " + agentGlyph + " before their title (Work page and tasks outline)"},
		{Name: settingTasksPrivateGlyph, Type: ochazuke.SettingBoolean, Default: true,
			Description: "Mark private tasks (their own flag or a parent's) with " + privateGlyph + " before their title (Work page and tasks outline)"},
		{Name: settingWorkTasksFilter, Deprecated: []string{settingTasksMadeBy}, Type: ochazuke.SettingString, Default: madeByAll,
			Description: "The saved task filter applied when the Work page first lists tasks: all (default), mine (owner), agents, or a user-defined saved filter; z ] and z [ cycle for this session"},
		{Name: settingPrivacyInbox, Type: ochazuke.SettingBoolean, Default: true,
			Description: "Add one quiet Inbox item for a new project with no privacy (private until marked family, so not exported), offering project make-family"},
	}
}

// observeTaskView redraws the task rows when one of those settings
// changes.
func (a *App) observeTaskView() {
	a.r.Settings().Observe(settingWorkTasksFilter, func(string, any) {
		a.taskFilterApplied = false
		if err := a.refreshTasks(); err != nil {
			a.r.UI().Notify(settingWorkTasksFilter + ": " + err.Error())
		}
	})
	for _, name := range []string{settingTasksColumns, settingTasksAgentGlyph, settingTasksPrivateGlyph} {
		a.r.Settings().Observe(name, func(string, any) {
			if err := a.refreshTasks(); err != nil {
				a.r.UI().Notify(name + ": " + err.Error())
			}
		})
	}
}

// showByColumn reports whether the by column shows.
func (a *App) showByColumn() bool {
	for _, c := range a.r.Settings().Strings(settingTasksColumns) {
		if c == columnBy {
			return true
		}
	}
	return false
}

// byCell is a task's by column: me, the agent's harness (or agent), or ?
// for a task made before mtm recorded makers.
func byCell(t work.Task) string {
	switch t.MadeBy() {
	case work.ActorOwner:
		return "me"
	case work.ActorAgent:
		if h := t.Maker().Harness; h != "" {
			return h
		}
		return "agent"
	}
	return "?"
}

// taskGlyphs are the marks before a task's title: private, agent-made,
// each with its setting.
func (a *App) taskGlyphs(t work.Task, private bool) string {
	out := ""
	if private && a.r.Settings().Bool(settingTasksPrivateGlyph) {
		out += privateGlyph + " "
	}
	if t.MadeBy() == work.ActorAgent && a.r.Settings().Bool(settingTasksAgentGlyph) {
		out += agentGlyph + " "
	}
	return out
}

// taskFilters are catalog data shared by task rows and the editable outline.
func taskFilters() []ochazuke.SavedFilter {
	return []ochazuke.SavedFilter{
		{Name: madeByMine, Where: map[string]any{"made-by": work.ActorOwner}},
		{Name: madeByAgents, Where: map[string]any{"made-by": work.ActorAgent}},
		{Name: madeByAll},
	}
}

// privacyKey is the Inbox key of a project's "mark as family" item.
func privacyKey(id string) string { return "privacy:" + id }

// notePrivacy keeps a project's one quiet Inbox item: open while it has no
// privacy (private by default, so not exported), archived once it has
// one. It never blocks: a failure is a notice.
func (a *App) notePrivacy(notify func(string), p work.Project) {
	if a.inbox == nil || a.inbox.items == nil || !a.r.Settings().Bool(settingPrivacyInbox) {
		return
	}
	if p.Settings.Privacy == "" {
		_, _, err := a.inbox.items.Add(actionitems.Spec{Source: "mtm", Key: privacyKey(p.ID), Kind: "privacy", ProjectID: p.ID,
			Title: fmt.Sprintf("Project %s is private: its tasks are not exported", projectName(p)),
			Why:   "mark it family to export its tasks: mtm project make-family " + p.ID})
		if err != nil {
			notify("privacy inbox item: " + err.Error())
		}
		return
	}
	items, err := a.inbox.items.All()
	if err != nil {
		return
	}
	for _, it := range items {
		if it.Source == "mtm" && it.Key == privacyKey(p.ID) && it.ArchivedAt == 0 {
			_, _ = a.inbox.items.Archive(it.ID)
		}
	}
}

// prepareActor is the CLI's entry point (shell Prepare): the actor of the
// call, unless one was set before (the MCP server's).
func (a *App) prepareActor(report func(string)) {
	if a.actorSet {
		return
	}
	act, err := a.cliActor()
	if err != nil {
		report(err.Error())
	}
	a.setActor(act)
}
