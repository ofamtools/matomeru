package work

import (
	"strings"
	"testing"
)

// These tests use only the decisions in actor.go (no store), so the file
// and its tests can be copied whole.

func TestParseActor(t *testing.T) {
	for in, want := range map[string]string{"owner": ActorOwner, " agent ": ActorAgent, "unknown": ActorUnknown, `{"kind":"agent","pane":"w1"}`: ActorAgent} {
		got, err := ParseActor(in)
		if err != nil || got.Kind != want {
			t.Errorf("ParseActor(%q) = %+v, %v", in, got, err)
		}
	}
	if _, err := ParseActor("me"); err == nil {
		t.Error("me parsed")
	}
}

func lookupOf(tasks ...Task) TaskLookup {
	byID := map[string]Task{}
	for _, t := range tasks {
		byID[t.ID] = t
	}
	return func(id string) (Task, bool) { t, ok := byID[id]; return t, ok }
}

func TestMayChangeTaskDecisions(t *testing.T) {
	me := Actor{Kind: ActorAgent, Pane: "w1", Task: "lane"}
	lane := Task{ID: "lane", ProjectID: "p", Title: "lane", CreatedBy: Owner()}
	under := Task{ID: "u", ParentID: "lane", CreatedBy: Actor{Kind: ActorAgent, Pane: "w9"}}
	unknown := Task{ID: "old", ParentID: "lane", Title: "old"}
	elsewhere := Task{ID: "e", ProjectID: "p", CreatedBy: Actor{Kind: ActorAgent, Pane: "w9"}}
	ownerElsewhere := Task{ID: "oe", ProjectID: "p", CreatedBy: Owner()}
	mine := Task{ID: "m", ProjectID: "q", CreatedBy: Actor{Kind: ActorAgent, Pane: "w1"}}
	get := lookupOf(lane, under, unknown, elsewhere, ownerElsewhere, mine)
	status := func(t Task) *Task { t.Status = StatusDone; return &t }
	rename := func(t Task) *Task { t.Title = "new words"; return &t }
	def := DefaultAgentPolicy()
	for _, c := range []struct {
		name string
		p    AgentPolicy
		old  Task
		new  *Task
		ok   bool
	}{
		{"own pane anywhere", def, mine, rename(mine), true},
		{"agent-made under the lane, wholly", def, under, rename(under), true},
		{"owner's lane task: status", def, lane, status(lane), true},
		{"owner's lane task: wording", def, lane, rename(lane), false},
		{"owner's lane task: delete", def, lane, nil, false},
		{"unknown under the lane: status", def, unknown, status(unknown), true},
		{"unknown under the lane: wording", def, unknown, rename(unknown), false},
		{"owner's outside the lane: status", def, ownerElsewhere, status(ownerElsewhere), false},
		{"other lane", def, elsewhere, status(elsewhere), false},
		{"title added to the props", AgentPolicy{WriteScope: ScopeSubtree, WriteMakers: []string{ActorAgent}, OwnerTaskProps: []string{"status", "title"}}, lane, rename(lane), true},
		{"owner among the makers", AgentPolicy{WriteScope: ScopeSubtree, WriteMakers: []string{ActorAgent, ActorOwner}}, lane, nil, true},
		{"project scope", AgentPolicy{WriteScope: ScopeProject, WriteMakers: []string{ActorAgent}}, elsewhere, rename(elsewhere), true},
		{"own scope", AgentPolicy{WriteScope: ScopeOwn, WriteMakers: []string{ActorAgent}}, under, status(under), false},
	} {
		err := MayChangeTask(me, MergePolicy(c.p), c.old, c.new, get)
		if (err == nil) != c.ok || err != nil && !IsRefused(err) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	err := MayChangeTask(me, def, lane, rename(lane), get)
	if err == nil || !strings.Contains(err.Error(), "the owner or the coordinator") || !strings.Contains(err.Error(), "agents.owner-task-props is status, not title") {
		t.Errorf("refusal does not name who to ask and the setting: %v", err)
	}
	if err := MayChangeTask(Owner(), AgentPolicy{WriteScope: ScopeOwn}, lane, nil, get); err != nil {
		t.Errorf("owner refused: %v", err)
	}
}

func TestStampTask(t *testing.T) {
	agent := Actor{Kind: ActorAgent, Pane: "w1"}
	var fresh Task
	if err := StampTask(agent, &fresh, nil); err != nil || fresh.CreatedBy != agent || fresh.ChangedBy != agent {
		t.Fatalf("new: %+v %v", fresh, err)
	}
	old := Task{CreatedBy: Owner(), Private: true}
	forged := Task{CreatedBy: agent, Private: true}
	if err := StampTask(agent, &forged, &old); err != nil || forged.CreatedBy != Owner() {
		t.Fatalf("agent kept its forged maker: %+v %v", forged, err)
	}
	shared := Task{}
	if err := StampTask(agent, &shared, &old); !IsRefused(err) {
		t.Fatalf("agent shared a private task: %v", err)
	}
	claim := Task{CreatedBy: Owner()}
	if err := StampTask(Owner(), &claim, &Task{}); err != nil || claim.CreatedBy != Owner() || claim.ChangedBy != Owner() {
		t.Fatalf("owner's claim: %+v %v", claim, err)
	}
}

func TestMayChangePaneAndShareProject(t *testing.T) {
	me := Actor{Kind: ActorAgent, Pane: "w1"}
	def := DefaultAgentPolicy()
	if MayChangePane(me, def, "w1", PaneRelation{}) != nil || MayChangePane(me, def, "c", PaneRelation{LaunchedBy: "w1"}) != nil || MayChangePane(me, def, "s", PaneRelation{Parent: "w1"}) != nil {
		t.Fatal("own and child panes refused")
	}
	if !IsRefused(MayChangePane(me, def, "w9", PaneRelation{})) || !IsRefused(MayChangePane(me, AgentPolicy{AssociateScope: ScopeNone}, "w1", PaneRelation{})) {
		t.Fatal("another pane, or scope none, allowed")
	}
	if MayChangePane(Owner(), AgentPolicy{AssociateScope: ScopeNone}, "w9", PaneRelation{}) != nil {
		t.Fatal("owner refused")
	}
	fam := Project{Settings: ProjectSettings{Privacy: PrivacyFamily}}
	if !IsRefused(MayShareProject(me, fam, "")) || MayShareProject(me, fam, PrivacyFamily) != nil || MayShareProject(Owner(), fam, "") != nil {
		t.Fatal("share project decisions")
	}
}
