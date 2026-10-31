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
	lane := Task{ID: "lane", ProjectID: "p", CreatedBy: Actor{Kind: ActorAgent, Pane: "coord"}}
	ownerLane := Task{ID: "olane", ProjectID: "p", CreatedBy: Owner()}
	under := Task{ID: "u", ParentID: "lane", CreatedBy: Actor{Kind: ActorAgent, Pane: "w9"}}
	unknown := Task{ID: "old", ParentID: "lane"}
	elsewhere := Task{ID: "e", ProjectID: "p", CreatedBy: Actor{Kind: ActorAgent, Pane: "w9"}}
	mine := Task{ID: "m", ProjectID: "q", CreatedBy: Actor{Kind: ActorAgent, Pane: "w1"}}
	get := lookupOf(lane, ownerLane, under, unknown, elsewhere, mine)
	def := DefaultAgentPolicy()
	for _, c := range []struct {
		name string
		p    AgentPolicy
		t    Task
		ok   bool
	}{
		{"own pane anywhere", def, mine, true},
		{"lane task", def, lane, true},
		{"under the lane", def, under, true},
		{"unknown maker", def, unknown, false},
		{"owner's", def, ownerLane, false},
		{"other lane", def, elsewhere, false},
		{"project scope", AgentPolicy{WriteScope: ScopeProject, WriteMakers: []string{ActorAgent}}, elsewhere, true},
		{"unknown allowed", AgentPolicy{WriteScope: ScopeSubtree, WriteMakers: []string{ActorAgent, ActorUnknown}}, unknown, true},
		{"own scope", AgentPolicy{WriteScope: ScopeOwn, WriteMakers: []string{ActorAgent}}, lane, false},
	} {
		err := MayChangeTask(me, c.p, c.t, get)
		if (err == nil) != c.ok || err != nil && !IsRefused(err) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if err := MayChangeTask(me, def, ownerLane, get); !strings.Contains(err.Error(), "the owner or the coordinator") {
		t.Errorf("refusal does not name who to ask: %v", err)
	}
	if err := MayChangeTask(Owner(), AgentPolicy{WriteScope: ScopeOwn}, ownerLane, get); err != nil {
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
