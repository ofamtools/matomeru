package work

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/store"
)

func actorStore(t *testing.T) (*Store, *store.Store) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}

func agent(pane, task string) Actor {
	return Actor{Kind: ActorAgent, Harness: "claude", Pane: pane, Session: "s", Task: task}
}

// A new task is stamped with the store's actor as its maker, once; every
// write stamps the writer.
func TestTaskWritesStampTheMakerOnceAndTheChangerEveryTime(t *testing.T) {
	s, _ := actorStore(t)
	owned, err := s.AddTask("p", "owner's")
	if err != nil {
		t.Fatal(err)
	}
	if owned.MadeBy() != ActorOwner || owned.ChangedBy.Kind != ActorOwner {
		t.Fatalf("owner create: %+v %+v", owned.CreatedBy, owned.ChangedBy)
	}
	s.SetActor(agent("w1", ""))
	made, err := s.AddTask("p", "agent's")
	if err != nil {
		t.Fatal(err)
	}
	if made.MadeBy() != ActorAgent || made.Maker().Pane != "w1" || made.Maker().Harness != "claude" {
		t.Fatalf("agent create: %+v", made.CreatedBy)
	}
	// The owner's later write keeps the maker and records the owner.
	s.SetActor(Owner())
	got, err := s.SetStatus(made.ID, StatusDone)
	if err != nil {
		t.Fatalf("the owner is never refused: %v", err)
	}
	if got.MadeBy() != ActorAgent || got.ChangedBy.Kind != ActorOwner {
		t.Fatalf("after the owner's write: %+v %+v", got.CreatedBy, got.ChangedBy)
	}
	// An agent cannot claim a task by writing a maker; the owner can.
	s.SetActor(agent("w1", ""))
	forged := got
	forged.CreatedBy = Actor{Kind: ActorOwner}
	if err := s.PutTask(forged); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Task(made.ID); again.MadeBy() != ActorAgent {
		t.Fatalf("an agent rewrote the maker: %+v", again.CreatedBy)
	}
	s.SetActor(Owner())
	if err := s.PutTask(forged); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Task(made.ID); again.MadeBy() != ActorOwner {
		t.Fatalf("the owner's claim: %+v", again.CreatedBy)
	}
}

// A row written before makers were recorded reads unknown and is not
// rewritten by being read (the backfill, Owner decision 13).
func TestOldTasksReadUnknown(t *testing.T) {
	s, db := actorStore(t)
	old := map[string]any{"id": "old", "project_id": "p", "title": "from the TODO file", "status": "todo"}
	if err := db.Tx([]string{"tasks"}, func(tx *store.Tx) error { return tx.Put("tasks", "old", old) }); err != nil {
		t.Fatal(err)
	}
	got, err := s.Task("old")
	if err != nil {
		t.Fatal(err)
	}
	if got.MadeBy() != ActorUnknown || got.CreatedBy.Kind != "" || got.ChangedBy.Kind != "" {
		t.Fatalf("old row: %+v", got)
	}
	var raw map[string]any
	err = db.Get("tasks", "old", &raw)
	if _, stamped := raw["created_by"]; err != nil || stamped {
		t.Fatalf("a read rewrote the row: %s %v", raw, err)
	}
}

// An agent may change what its pane or lane made and, by default, the
// agent-made tasks under its lane task; it is refused the owner's tasks,
// the unknown ones and other lanes', with a message that names who to
// ask and the setting. The owner is never refused.
func TestAgentChangeRights(t *testing.T) {
	s, _ := actorStore(t)
	lane, _ := s.AddTask("p", "lane (the owner's)")
	ownerSub, _ := s.AddSubtask("p", lane.ID, "owner's subtask")
	s.SetActor(agent("coord", ""))
	coordLane, _ := s.AddTask("p", "lane the coordinator made")
	other, _ := s.AddTask("p", "elsewhere")

	s.SetActor(agent("w1", coordLane.ID))
	mine, err := s.AddSubtask("p", coordLane.ID, "my subtask")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Retitle(mine.ID, "mine, renamed"); err != nil {
		t.Fatalf("own task: %v", err)
	}
	// subtree (the default): the lane task, made by an agent, is in reach.
	if _, err := s.SetStatus(coordLane.ID, StatusActive); err != nil {
		t.Fatalf("lane task made by an agent: %v", err)
	}
	// Another agent's task outside the lane is not.
	if _, err := s.SetStatus(other.ID, StatusDone); !IsRefused(err) || !strings.Contains(err.Error(), "pane coord") || !strings.Contains(err.Error(), "agents.write-scope") {
		t.Fatalf("other lane: %v", err)
	}
	// The owner's tasks are refused, naming who to ask.
	for _, id := range []string{lane.ID, ownerSub.ID} {
		_, err := s.SetStatus(id, StatusDone)
		if !IsRefused(err) || !strings.Contains(err.Error(), "made by owner") || !strings.Contains(err.Error(), "the owner or the coordinator") {
			t.Fatalf("owner's task %s: %v", id, err)
		}
	}
	if _, err := s.Delete("task", lane.ID); !IsRefused(err) {
		t.Fatalf("delete of the owner's task: %v", err)
	}
	if got, _ := s.Task(lane.ID); got.Status != StatusTodo {
		t.Fatalf("a refused write landed: %+v", got)
	}
	// A second agent on the same lane task reaches what the lane made.
	s.SetActor(agent("w2", coordLane.ID))
	if _, err := s.Retitle(mine.ID, "the lane's"); err != nil {
		t.Fatalf("same lane: %v", err)
	}
	// The settings widen it: any reaches other lanes; owner in the makers
	// reaches the owner's tasks.
	s.SetAgentPolicy(func() AgentPolicy {
		return AgentPolicy{WriteScope: ScopeAny, WriteMakers: []string{ActorAgent, ActorOwner}}
	})
	if _, err := s.SetStatus(other.ID, StatusDone); err != nil {
		t.Fatalf("write-scope any: %v", err)
	}
	if _, err := s.SetStatus(lane.ID, StatusDone); err != nil {
		t.Fatalf("write-makers owner: %v", err)
	}
	// own: only what its pane or lane made.
	s.SetAgentPolicy(func() AgentPolicy { return AgentPolicy{WriteScope: ScopeOwn} })
	if _, err := s.SetStatus(coordLane.ID, StatusTodo); !IsRefused(err) {
		t.Fatalf("write-scope own reached the lane task: %v", err)
	}
	if _, err := s.SetStatus(mine.ID, StatusDone); err != nil {
		t.Fatalf("write-scope own, the lane's task: %v", err)
	}
	// The owner is never refused, whatever the policy.
	s.SetActor(Owner())
	s.SetAgentPolicy(func() AgentPolicy { return AgentPolicy{WriteScope: ScopeOwn, WriteMakers: []string{}} })
	for _, id := range []string{lane.ID, other.ID, mine.ID, coordLane.ID} {
		if _, err := s.Retitle(id, "the owner's word"); err != nil {
			t.Fatalf("owner refused on %s: %v", id, err)
		}
	}
}

// Privacy only tightens without the owner: an agent may make a task
// private but not shared again, and never makes a project family.
func TestAgentsOnlyTightenPrivacy(t *testing.T) {
	s, _ := actorStore(t)
	p, _ := s.AddProject("work")
	s.SetActor(agent("w1", ""))
	task, _ := s.AddTask(p.ID, "mine")
	task.Private = true
	if err := s.PutTask(task); err != nil {
		t.Fatalf("make private: %v", err)
	}
	task.Private = false
	if err := s.PutTask(task); !IsRefused(err) {
		t.Fatalf("agent shared a private task: %v", err)
	}
	if _, err := s.SetProjectPrivacy(p.ID, PrivacyFamily); !IsRefused(err) {
		t.Fatalf("agent made a project family: %v", err)
	}
	if _, err := s.SetProjectPrivacy(p.ID, PrivacyPrivate); err != nil {
		t.Fatalf("agent made a project private: %v", err)
	}
	s.SetActor(Owner())
	if _, err := s.SetProjectPrivacy(p.ID, PrivacyFamily); err != nil {
		t.Fatal(err)
	}
	if err := s.PutTask(task); err != nil {
		t.Fatalf("owner shared it: %v", err)
	}
}

// A task leaves mtm only in a family project, and a private task keeps
// every task under it in (the flag subtasks inherit). A project with no
// setting is private (fail closed).
func TestExportFollowsProjectPrivacyAndInheritedPrivateFlags(t *testing.T) {
	s, _ := actorStore(t)
	fam, _ := s.AddProject("family")
	unset, _ := s.AddProject("new")
	if _, err := s.SetProjectPrivacy(fam.ID, PrivacyFamily); err != nil {
		t.Fatal(err)
	}
	open, _ := s.AddTask(fam.ID, "shared")
	secret, _ := s.AddTask(fam.ID, "secret")
	secret.Private = true
	if err := s.PutTask(secret); err != nil {
		t.Fatal(err)
	}
	child, _ := s.AddSubtask(fam.ID, secret.ID, "under the secret")
	grand, _ := s.AddSubtask(fam.ID, child.ID, "further under")
	hidden, _ := s.AddTask(unset.ID, "in a project with no setting")
	loose, _ := s.AddTask("", "unassociated")
	got, err := s.Exports()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{open.ID: true, secret.ID: false, child.ID: false, grand.ID: false, hidden.ID: false, loose.ID: false}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("exported[%s] = %v, want %v", id, got[id], w)
		}
	}
	if PrivacyOf(unset) != PrivacyPrivate {
		t.Fatal("a project with no setting is not private")
	}
	if list, _ := s.UnsetPrivacy(); len(list) != 1 || list[0].ID != unset.ID {
		t.Fatalf("unset = %+v", list)
	}
}

// An agent associates its own pane and its child panes (launched by it or
// opened beside it), not another's; any-pane and none are the settings'
// other values. The owner associates any pane.
func TestAgentPaneRights(t *testing.T) {
	s, _ := actorStore(t)
	s.SetActor(agent("w1", ""))
	if err := s.SetPaneOwner(Assignment{Window: "w1", ProjectID: "p"}); err != nil {
		t.Fatalf("own pane: %v", err)
	}
	if err := s.SetPaneOwner(Assignment{Window: "w9", ProjectID: "p"}); !IsRefused(err) || !strings.Contains(err.Error(), "agents.associate-scope") {
		t.Fatalf("another pane: %v", err)
	}
	if err := s.PutPaneRecipe(PaneRecipe{Window: "child", Harness: "claude"}); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.PaneRecipe("child"); r.LaunchedBy != "w1" {
		t.Fatalf("launched by = %q", r.LaunchedBy)
	}
	if err := s.SetPaneOwner(Assignment{Window: "child", ProjectID: "p"}); err != nil {
		t.Fatalf("launched child: %v", err)
	}
	if err := s.PutCompanion(Companion{Window: "side", Parent: "w1", Kind: "vim"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPaneOwner(Assignment{Window: "side", ProjectID: "p"}); err != nil {
		t.Fatalf("companion child: %v", err)
	}
	s.SetAgentPolicy(func() AgentPolicy { return AgentPolicy{AssociateScope: ScopeNone} })
	if err := s.SetPaneOwner(Assignment{Window: "w1"}); !IsRefused(err) {
		t.Fatalf("associate-scope none: %v", err)
	}
	s.SetAgentPolicy(func() AgentPolicy { return AgentPolicy{AssociateScope: ScopeAnyPane} })
	if err := s.SetPaneOwner(Assignment{Window: "w9"}); err != nil {
		t.Fatalf("associate-scope any-pane: %v", err)
	}
	s.SetActor(Owner())
	s.SetAgentPolicy(func() AgentPolicy { return AgentPolicy{AssociateScope: ScopeNone} })
	if err := s.SetPaneOwner(Assignment{Window: "w9", ProjectID: "q"}); err != nil {
		t.Fatalf("owner refused: %v", err)
	}
}

// Owner decision (2026-10-30): an agent may change the status of the
// owner's tasks in its lane, not their wording; the setting widens the
// props. Every store write path applies it.
func TestAgentChangesOnlyTheStatusOfTheOwnersLaneTasks(t *testing.T) {
	s, _ := actorStore(t)
	lane, _ := s.AddTask("p", "the owner's lane")
	step, _ := s.AddSubtask("p", lane.ID, "the owner's step")
	s.SetActor(agent("w1", lane.ID))
	if _, err := s.Done(lane.ID); err != nil {
		t.Fatalf("done on the owner's lane task: %v", err)
	}
	if _, err := s.ToggleDone(step.ID); err != nil {
		t.Fatalf("toggle on the owner's subtask: %v", err)
	}
	if _, err := s.Retitle(lane.ID, "the agent's words"); !IsRefused(err) || !strings.Contains(err.Error(), "not title") {
		t.Fatalf("rename: %v", err)
	}
	moved := step
	moved.ParentID = ""
	if err := s.PutTask(moved); !IsRefused(err) {
		t.Fatalf("move: %v", err)
	}
	if _, err := s.Delete("task", step.ID); !IsRefused(err) {
		t.Fatalf("delete: %v", err)
	}
	s.SetAgentPolicy(func() AgentPolicy { return AgentPolicy{OwnerTaskProps: []string{"status", "title"}} })
	if _, err := s.Retitle(lane.ID, "the agent's words"); err != nil {
		t.Fatalf("title in agents.owner-task-props: %v", err)
	}
	got, _ := s.Task(lane.ID)
	if got.Status != StatusDone || got.Title != "the agent's words" || got.MadeBy() != ActorOwner || got.ChangedBy.Kind != ActorAgent {
		t.Fatalf("lane task: %+v", got)
	}
}
