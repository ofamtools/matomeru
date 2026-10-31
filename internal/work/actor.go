package work

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Who wrote a task (the tasks plan, section 6, lane A). Every write the
// store makes runs as one Actor, decided once where the call came from
// (the TUI is the owner, the MCP server the agent of its pane, the CLI
// the agent of the pane it runs in or else the owner; cmd/mtm/actor.go)
// and set on the Store. The store stamps it into a task's CreatedBy once
// and its ChangedBy on every write, and it checks what an agent may
// change in this one place, the write path every surface (the outline,
// the Work page's verbs, the CLI, MCP) goes through. The owner is never
// refused.
//
// This file holds the decisions only, over values and lookups, with no
// store: actor_store.go applies them in the work store's transactions.

// Actor kinds. MadeBy reads a task's CreatedBy as one of them.
const (
	ActorOwner   = "owner"
	ActorAgent   = "agent"
	ActorUnknown = "unknown"
)

// Actor is who made a write: the owner, an agent (with the pane it runs
// in, its tuios session, the task its pane is on, its harness and model),
// or unknown (a task written before mtm recorded its maker).
type Actor struct {
	Kind    string `json:"kind"`
	Harness string `json:"harness,omitempty"`
	Pane    string `json:"pane,omitempty"`
	Session string `json:"session,omitempty"`
	Task    string `json:"task,omitempty"`
	Model   string `json:"model,omitempty"`
}

// Owner is the owner as an actor.
func Owner() Actor { return Actor{Kind: ActorOwner} }

// IsAgent reports whether the actor is an agent.
func (a Actor) IsAgent() bool { return a.Kind == ActorAgent }

// Kind is the actor's kind, unknown for an empty or unrecognised one.
func (a Actor) kind() string {
	switch a.Kind {
	case ActorOwner, ActorAgent:
		return a.Kind
	}
	return ActorUnknown
}

// String is the actor for people: "owner", "agent claude-code (pane P)",
// "unknown".
func (a Actor) String() string {
	if a.kind() != ActorAgent {
		return a.kind()
	}
	out := "agent"
	if a.Harness != "" {
		out += " " + a.Harness
	}
	if a.Pane != "" {
		out += " (pane " + a.Pane + ")"
	}
	return out
}

// ParseActor reads an actor as MTM_ACTOR and task set created_by give it:
// "owner", "agent", "unknown" or the actor as JSON.
func ParseActor(s string) (Actor, error) {
	s = strings.TrimSpace(s)
	switch s {
	case ActorOwner, ActorAgent, ActorUnknown:
		return Actor{Kind: s}, nil
	}
	var a Actor
	if err := json.Unmarshal([]byte(s), &a); err != nil || !slices.Contains([]string{ActorOwner, ActorAgent, ActorUnknown}, a.Kind) {
		return Actor{}, fmt.Errorf("actor %q is not owner, agent, unknown or an actor as JSON ({\"kind\":\"agent\",...})", s)
	}
	return a, nil
}

// MadeBy is the flat kind of the task's maker (owner, agent or unknown),
// the prop --where and the Work page's filter match. A task written
// before mtm recorded makers reads unknown (the backfill, Owner decision
// 13: nothing is rewritten until the owner claims it).
func (t Task) MadeBy() string {
	return t.CreatedBy.kind()
}

// Maker is the task's CreatedBy, unknown when none was recorded.
func (t Task) Maker() Actor {
	if t.CreatedBy.Kind == "" {
		return Actor{Kind: ActorUnknown}
	}
	return t.CreatedBy
}

// What an agent may change is data (the agents.* settings; Owner
// decisions 12 and 14). WriteScope says where: own (the tasks its pane or
// its lane made), subtree (also its lane task and the tasks under it; the
// default), project (its pane's project) or any. WriteMakers says whose
// tasks the wider scopes reach, by MadeBy (agent by default; add unknown
// for the backfilled tasks, or owner to let agents change the owner's).
// The tasks its own pane or lane made are always its own. AssociateScope
// says which panes it may associate: own-pane (its pane and the panes it
// launched or opened beside it; the default), any-pane, or none.
type AgentPolicy struct {
	WriteScope     string
	WriteMakers    []string
	AssociateScope string
}

// Write and associate scopes.
const (
	ScopeOwn     = "own"
	ScopeSubtree = "subtree"
	ScopeProject = "project"
	ScopeAny     = "any"

	ScopeOwnPane = "own-pane"
	ScopeAnyPane = "any-pane"
	ScopeNone    = "none"
)

// WriteScopes and AssociateScopes are the settings' values, the default
// first.
var (
	WriteScopes     = []string{ScopeSubtree, ScopeOwn, ScopeProject, ScopeAny}
	AssociateScopes = []string{ScopeOwnPane, ScopeAnyPane, ScopeNone}
)

// DefaultAgentPolicy is the policy with no settings (Owner decision 14).
func DefaultAgentPolicy() AgentPolicy {
	return AgentPolicy{WriteScope: ScopeSubtree, WriteMakers: []string{ActorAgent}, AssociateScope: ScopeOwnPane}
}

// RefusedError is a write an agent may not make. It names who made the
// thing and who to ask instead, and the setting that refused it, so the
// owner can widen it without a code change.
type RefusedError struct {
	What    string // "task t-1 \"Ship it\"", "pane P"
	MadeBy  Actor
	Setting string
	Ask     string
}

func (e *RefusedError) Error() string {
	msg := fmt.Sprintf("an agent may not change %s", e.What)
	if e.MadeBy.Kind != "" {
		msg += fmt.Sprintf(": it was made by %s", e.MadeBy)
	}
	return msg + fmt.Sprintf(" (%s); ask %s to change it", e.Setting, e.Ask)
}

// IsRefused reports whether err is an agent's refused write.
func IsRefused(err error) bool {
	var r *RefusedError
	return errors.As(err, &r)
}

// askFor is who an agent asks to change a thing another made.
func askFor(maker Actor) string {
	if maker.IsAgent() && maker.Pane != "" {
		return "the agent in pane " + maker.Pane + ", the owner or the coordinator"
	}
	return "the owner or the coordinator (send the change as a message)"
}

// ownedBy reports whether an agent's own pane or lane made the task: the
// maker is an agent of the same pane, or one whose pane was on the same
// lane task when it made it.
func ownedBy(me Actor, t Task) bool {
	m := t.Maker()
	if !m.IsAgent() {
		return false
	}
	return (me.Pane != "" && m.Pane == me.Pane) || (me.Task != "" && m.Task == me.Task)
}

// TaskLookup reads a task by id; ok is false when there is none.
type TaskLookup func(id string) (t Task, ok bool)

// TaskProject is the task's project, following parents for an old row
// that stored none.
func TaskProject(t Task, get TaskLookup) string {
	seen := map[string]bool{}
	for t.ProjectID == "" && t.ParentID != "" && !seen[t.ParentID] {
		seen[t.ParentID] = true
		p, ok := get(t.ParentID)
		if !ok {
			break
		}
		t = p
	}
	return t.ProjectID
}

// TaskUnder reports whether task id is root or under it.
func TaskUnder(id, root string, get TaskLookup) bool {
	seen := map[string]bool{}
	for cur := id; cur != "" && !seen[cur]; {
		if cur == root {
			return true
		}
		seen[cur] = true
		t, ok := get(cur)
		if !ok {
			return false
		}
		cur = t.ParentID
	}
	return false
}

// MayChangeTask is the one decision on a task write by me, against the
// task as stored: nil for the owner, for a task the agent's pane or lane
// made, and for one inside the agent's write scope whose maker is one of
// the write makers; else a RefusedError naming who to ask and the setting.
func MayChangeTask(me Actor, p AgentPolicy, t Task, get TaskLookup) error {
	if !me.IsAgent() || ownedBy(me, t) {
		return nil
	}
	refuse := func(setting string) error {
		return &RefusedError{What: fmt.Sprintf("task %s %q", t.ID, t.Title), MadeBy: t.Maker(), Setting: setting, Ask: askFor(t.Maker())}
	}
	if !slices.Contains(p.WriteMakers, t.MadeBy()) {
		return refuse(fmt.Sprintf("agents.write-makers is %s", strings.Join(p.WriteMakers, ", ")))
	}
	in := false
	switch p.WriteScope {
	case ScopeAny:
		in = true
	case ScopeProject:
		if lane, ok := get(me.Task); ok && me.Task != "" {
			if project := TaskProject(lane, get); project != "" {
				in = TaskProject(t, get) == project
			}
		}
	case ScopeSubtree:
		in = me.Task != "" && TaskUnder(t.ID, me.Task, get)
	}
	if !in {
		return refuse("agents.write-scope is " + p.WriteScope)
	}
	return nil
}

// StampTask sets the stamps of a write by me of t over old (nil for a new
// task). CreatedBy is set once: the maker of a new row is me; an existing
// row keeps its maker unless the owner writes one (how the owner claims
// or restores a task). ChangedBy is me. An agent may make a task private
// but never makes one shared again (Owner decision 6: privacy only
// tightens without the owner).
func StampTask(me Actor, t *Task, old *Task) error {
	if old != nil {
		if me.IsAgent() || t.CreatedBy.Kind == "" {
			t.CreatedBy = old.CreatedBy
		}
		if me.IsAgent() && old.Private && !t.Private {
			return &RefusedError{What: fmt.Sprintf("task %s %q back to shared", t.ID, t.Title), Setting: "only the owner shares a private task", Ask: "the owner"}
		}
	} else if me.IsAgent() || t.CreatedBy.Kind == "" {
		t.CreatedBy = me
	}
	t.ChangedBy = me
	return nil
}

// PaneRelation is what the store knows of a pane an agent would change:
// the pane of the agent that launched it (its recipe's LaunchedBy) and
// the pane it sits beside (a companion's parent).
type PaneRelation struct {
	LaunchedBy string
	Parent     string
}

// MayChangePane is the decision on a pane write (its association) by me:
// nil for the owner and for an agent's own pane; any-pane lets it reach
// every pane, none no pane, and own-pane (the default) also the panes it
// launched or opened beside it.
func MayChangePane(me Actor, p AgentPolicy, window string, rel PaneRelation) error {
	if !me.IsAgent() {
		return nil
	}
	refuse := &RefusedError{What: "pane " + window, Setting: "agents.associate-scope is " + p.AssociateScope, Ask: "that pane's owner or the coordinator (send the change as a message)"}
	switch p.AssociateScope {
	case ScopeNone:
		return refuse
	case ScopeAnyPane:
		return nil
	}
	if me.Pane != "" && (window == me.Pane || rel.LaunchedBy == me.Pane || rel.Parent == me.Pane) {
		return nil
	}
	return refuse
}

// MayShareProject refuses an agent making a project family (shared): a
// project becomes shared only by the owner (Owner decision 6). old is the
// stored privacy ("" for a new project); making one private is always
// allowed.
func MayShareProject(me Actor, p Project, old string) error {
	if !me.IsAgent() || p.Settings.Privacy != PrivacyFamily || old == PrivacyFamily {
		return nil
	}
	return &RefusedError{What: fmt.Sprintf("project %q to family", p.Name), Setting: "only the owner shares a project", Ask: "the owner"}
}

// MergePolicy is p over the defaults: an empty field keeps the default.
func MergePolicy(p AgentPolicy) AgentPolicy {
	out := DefaultAgentPolicy()
	if p.WriteScope != "" {
		out.WriteScope = p.WriteScope
	}
	if p.WriteMakers != nil {
		out.WriteMakers = p.WriteMakers
	}
	if p.AssociateScope != "" {
		out.AssociateScope = p.AssociateScope
	}
	return out
}
