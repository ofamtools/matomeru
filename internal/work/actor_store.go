package work

import (
	"errors"

	"ofam.dev/matomeru/store"
)

// The work store's side of actor.go: who its writes run as, the policy it
// reads, and the decisions applied inside its transactions.

// SetActor makes every later write run as a; a zero actor is the owner.
func (s *Store) SetActor(a Actor) {
	if a.Kind == "" {
		a = Owner()
	}
	s.actor = a
}

// Actor is who the store's writes run as.
func (s *Store) Actor() Actor {
	if s.actor.Kind == "" {
		return Owner()
	}
	return s.actor
}

// SetAgentPolicy sets where the policy is read from (the settings, read
// at each check so a change applies at once); nil is the default policy.
func (s *Store) SetAgentPolicy(policy func() AgentPolicy) { s.policy = policy }

func (s *Store) agentPolicy() AgentPolicy {
	if s.policy == nil {
		return DefaultAgentPolicy()
	}
	return MergePolicy(s.policy())
}

// txLookup reads tasks inside tx.
func txLookup(tx *store.Tx) TaskLookup {
	return func(id string) (Task, bool) {
		var t Task
		if id == "" || tx.Get("tasks", id, &t) != nil {
			return Task{}, false
		}
		return t, true
	}
}

// mayChangeTask is MayChangeTask in a transaction over the tasks table;
// new is nil for a delete or a whole-row write.
func (s *Store) mayChangeTask(tx *store.Tx, old Task, new *Task) error {
	return MayChangeTask(s.Actor(), s.agentPolicy(), old, new, txLookup(tx))
}

// putTask is every task write the store makes for a caller: the rights
// check against the row as stored, then the stamps (StampTask).
func (s *Store) putTask(tx *store.Tx, t *Task) error {
	me := s.Actor()
	var old Task
	err := tx.Get("tasks", t.ID, &old)
	switch {
	case err == nil:
		if err := s.mayChangeTask(tx, old, t); err != nil {
			return err
		}
		if err := StampTask(me, t, &old); err != nil {
			return err
		}
	case errors.Is(err, store.ErrNotFound):
		if err := StampTask(me, t, nil); err != nil {
			return err
		}
	default:
		return err
	}
	return tx.Put("tasks", t.ID, *t)
}

// mayChangePane is MayChangePane with what the store knows of the pane:
// its recipe's LaunchedBy and its companion parent.
func (s *Store) mayChangePane(window string) error {
	me := s.Actor()
	if !me.IsAgent() {
		return nil
	}
	var rel PaneRelation
	var r PaneRecipe
	if s.db.Declared("pane_recipes") && s.db.Get("pane_recipes", window, &r) == nil {
		rel.LaunchedBy = r.LaunchedBy
	}
	var c Companion
	if s.db.Declared("companions") && s.db.Get("companions", window, &c) == nil {
		rel.Parent = c.Parent
	}
	return MayChangePane(me, s.agentPolicy(), window, rel)
}

// mayShareProject is MayShareProject against the stored project.
func (s *Store) mayShareProject(tx *store.Tx, p Project) error {
	var old Project
	_ = tx.Get("projects", p.ID, &old)
	return MayShareProject(s.Actor(), p, old.Settings.Privacy)
}
