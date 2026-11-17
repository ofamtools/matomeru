package main

import (
	"testing"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/work"
)

// An agent's calls carry no scope by default (agents.pane-scope all), and
// carry one under own.
func TestWireScopeFollowsPaneScope(t *testing.T) {
	t.Parallel()
	app := open(t)
	app.setActor(work.Actor{Kind: work.ActorAgent, Pane: "w1"})
	if _, scoped := app.wireScope(); scoped {
		t.Fatal("default scopes an agent")
	}
	if err := app.r.Settings().Set(settingAgentsPaneScope, paneScopeOwn); err != nil {
		t.Fatal(err)
	}
	if _, scoped := app.wireScope(); !scoped {
		t.Fatal("own does not scope an agent")
	}
}

// Only agents.rights and the settings that can bypass it are protected: an
// agent changes agents.pane-scope and agents.widen-privacy as the owner does.
func TestAgentChangesItsReachSettings(t *testing.T) {
	t.Parallel()
	app := open(t)
	restore := app.r.WithActor(ochazuke.Actor{Kind: ochazuke.ActorAgent, Harness: "claude", Pane: "w1", Session: "matomeru"})
	defer restore()
	for name, value := range map[string]string{settingAgentsPaneScope: paneScopeOwn, settingAgentsWidenPrivacy: "true"} {
		if err := app.r.Settings().Set(name, value); err != nil {
			t.Errorf("agent set %s: %v", name, err)
		}
	}
	if !app.agentPolicy().WidenPrivacy {
		t.Fatal("agents.widen-privacy on does not reach the policy")
	}
}
