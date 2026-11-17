package main

import (
	"testing"

	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/work"
)

// An agent's request relayed to the running app (CallMsg: an MCP or CLI
// call, or an extension's host call) runs as that agent only while it
// runs: afterwards the owner's reads, tokens and writes are the owner's
// again, and a job the agent started still runs its callback as the agent.
func TestAgentRequestActorEndsWithTheRequest(t *testing.T) {
	t.Parallel()
	a := open(t)
	for _, task := range []work.Task{
		{ID: "lane", ProjectID: "matomeru", Title: "the agent's lane", Status: work.StatusTodo},
		{ID: "other", ProjectID: "matomeru", Title: "the owner's", Status: work.StatusTodo},
	} {
		if err := a.work.PutTask(task); err != nil {
			t.Fatal(err)
		}
	}
	actorPane(t, a, "w1", "lane", "claude")
	agent := ochazuke.Actor{Kind: ochazuke.ActorAgent, Harness: "claude", Pane: "w1", Session: "matomeru"}

	reply := make(chan ochazuke.Response, 1)
	a.r.HandleCall(ochazuke.CallMsg{Actor: agent, Headless: true, Source: "mcp", Reply: reply,
		Request: ochazuke.Request{Noun: "task", Verb: "status", Target: "lane", Args: ochazuke.Arguments{"status": work.StatusBlocked}}})
	if res := <-reply; res.Error != "" {
		t.Fatalf("agent request: %s", res.Error)
	}
	if got := stored(t, a, "lane"); got.ChangedBy.Kind != work.ActorAgent || got.ChangedBy.Pane != "w1" {
		t.Fatalf("the agent's write: %+v", got.ChangedBy)
	}

	// After the request: the store's actor, the task token and the task
	// list are the owner's.
	if got := a.work.Actor(); got.IsAgent() {
		t.Fatalf("actor after the agent's request: %+v", got)
	}
	if token, err := a.taskChangeToken(); err != nil || token == "" {
		t.Fatalf("task token after the agent's request: %q %v", token, err)
	}
	// The task store's list, as a page reads it outside any request.
	rows, err := a.taskStore().List(ochazuke.Ctx{Runtime: a.r}, ochazuke.ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, row := range rows {
		listed = listed || row["id"] == "other"
	}
	if !listed {
		t.Fatal("the owner's task list kept the agent's lane filter")
	}
	// The owner's write outside any verb (a debounced editor commit, an
	// observer) is the owner's: stamped so, and never checked as an agent.
	if _, err := a.work.Retitle("other", "renamed by me"); err != nil {
		t.Fatalf("owner write after the agent's request: %v", err)
	}
	if got := stored(t, a, "other"); got.ChangedBy.Kind != work.ActorOwner {
		t.Fatalf("owner write stamped %+v", got.ChangedBy)
	}

	// A job's callback runs under the actor that started it (ochazuke
	// restores it around the callback, Deliver): the agent, with its lane,
	// even after the owner's own requests.
	executeA(t, a, "task", "status", "other", ochazuke.Arguments{"status": work.StatusBlocked})
	restore := a.r.WithActor(agent)
	got := a.work.Actor()
	restore()
	if !got.IsAgent() || got.Pane != "w1" || got.Task != "lane" {
		t.Fatalf("a job callback's actor: %+v", got)
	}
	if got := a.work.Actor(); got.IsAgent() {
		t.Fatalf("actor after the callback: %+v", got)
	}
}
