package main

import (
	"testing"

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
