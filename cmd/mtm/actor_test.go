package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/work"
)

// envOf is a fake environment.
func envOf(env map[string]string) func(string) string {
	return func(k string) string { return env[k] }
}

// agentPane makes window w a pane of session matomeru on task, launched
// with harness (none: a pane mtm did not launch).
func actorPane(t *testing.T, a *App, w, task, harness string) {
	t.Helper()
	if err := a.work.SetPaneOwner(work.Assignment{Window: w, Session: "matomeru", ProjectID: "matomeru", TaskID: task}); err != nil {
		t.Fatal(err)
	}
	if harness != "" {
		if err := a.work.PutPaneRecipe(work.PaneRecipe{Window: w, Session: "matomeru", Harness: harness, Model: "m1", TaskID: task, ProjectID: "matomeru"}); err != nil {
			t.Fatal(err)
		}
	}
}

// createdThing runs mtm task create and answers the task it printed.
func createdThing(t *testing.T, a *App, title string) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(mustCLI(t, a, "task", "create", "--json", `{"title":"`+title+`","project":"matomeru"}`)), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func makerOf(t *testing.T, a *App, id string) work.Actor {
	t.Helper()
	return stored(t, a, id).Maker()
}

// The TUI is the owner, even when it runs inside a pane an agent runs in.
func TestTUICreateStampsTheOwner(t *testing.T) {
	t.Parallel()
	app := open(t)
	app.getenv = envOf(map[string]string{"TUIOS_PANE_ID": "w1", "TUIOS_SESSION": "matomeru", "CLAUDECODE": "1", actorEnv: "agent"})
	press(t, app, "j", "enter", "q")
	press(t, app, "n")
	typeText(t, app, "typed by me")
	press(t, app, "enter", "q")
	var made work.Task
	for _, task := range app.tasks {
		if task.Title == "typed by me" {
			made = task
		}
	}
	if made.ID == "" {
		t.Fatalf("no new task in %+v", app.tasks)
	}
	if got := stored(t, app, made.ID); got.MadeBy() != work.ActorOwner || got.ChangedBy.Kind != work.ActorOwner {
		t.Fatalf("TUI create: %+v %+v", got.CreatedBy, got.ChangedBy)
	}
	// x on a row the owner did not make is never refused.
	if err := app.work.PutTask(work.Task{ID: "agents", ProjectID: "matomeru", Title: "an agent's", Status: work.StatusTodo, CreatedBy: work.Actor{Kind: work.ActorAgent, Pane: "w9"}}); err != nil {
		t.Fatal(err)
	}
	if err := app.refreshTasks(); err != nil {
		t.Fatal(err)
	}
	executeA(t, app, "task", "toggle", "agents", nil)
	if got := stored(t, app, "agents"); got.Status != work.StatusDone || got.MadeBy() != work.ActorAgent || got.ChangedBy.Kind != work.ActorOwner {
		t.Fatalf("owner's toggle of an agent's task: %+v", got)
	}
}

// A CLI call from a pane an agent runs in is that agent: the pane's
// recipe names the harness, or (a pane mtm did not launch) a harness's
// agentEnv variable does; the lane task is the pane's. A pane with no
// agent, or no pane, is the owner. MTM_ACTOR overrides both ways.
func TestCLIActorFromThePane(t *testing.T) {
	t.Parallel()
	app, _ := cliApp(t)
	actorPane(t, app, "w7", "t1", "codex")
	actorPane(t, app, "w8", "t2", "")

	app.getenv = envOf(map[string]string{"TUIOS_PANE_ID": "w7", "TUIOS_SESSION": "matomeru"})
	got := createdThing(t, app, "from codex")
	if by := makerOf(t, app, got["id"].(string)); by.Kind != work.ActorAgent || by.Harness != "codex" || by.Pane != "w7" || by.Task != "t1" || by.Model != "m1" || by.Session != "matomeru" {
		t.Fatalf("recipe pane: %+v", by)
	}
	if got["made-by"] != work.ActorAgent {
		t.Fatalf("made-by = %v", got["made-by"])
	}

	app.getenv = envOf(map[string]string{"TUIOS_PANE_ID": "w8", "TUIOS_SESSION": "matomeru", "CLAUDECODE": "1"})
	got = createdThing(t, app, "from claude")
	if by := makerOf(t, app, got["id"].(string)); by.Kind != work.ActorAgent || by.Harness != "claude" || by.Pane != "w8" || by.Task != "t2" {
		t.Fatalf("agentEnv pane: %+v", by)
	}

	app.getenv = envOf(map[string]string{"TUIOS_PANE_ID": "w8", "TUIOS_SESSION": "matomeru"})
	got = createdThing(t, app, "from a shell")
	if by := makerOf(t, app, got["id"].(string)); by.Kind != work.ActorOwner {
		t.Fatalf("shell pane: %+v", by)
	}

	app.getenv = envOf(map[string]string{"TUIOS_PANE_ID": "w7", "TUIOS_SESSION": "matomeru", actorEnv: "owner"})
	got = createdThing(t, app, "typed in the agent's pane")
	if by := makerOf(t, app, got["id"].(string)); by.Kind != work.ActorOwner {
		t.Fatalf("MTM_ACTOR=owner: %+v", by)
	}

	app.getenv = envOf(map[string]string{actorEnv: `{"kind":"agent","harness":"omp","pane":"w5"}`})
	got = createdThing(t, app, "an extension's")
	if by := makerOf(t, app, got["id"].(string)); by.Kind != work.ActorAgent || by.Harness != "omp" || by.Pane != "w5" {
		t.Fatalf("MTM_ACTOR JSON: %+v", by)
	}

	app.getenv = envOf(map[string]string{actorEnv: "nobody"})
	out, errOut, err := cli(app, "", "task", "create", "--json", `{"title":"bad actor"}`)
	if err != nil || !strings.Contains(errOut, "MTM_ACTOR") {
		t.Fatalf("a bad MTM_ACTOR is reported, never a block: %v %q %q", err, out, errOut)
	}
}

// An agent's CLI write to the owner's lane task may change its status
// (agents.owner-task-props, status by default) but not its wording, and a
// refused write leaves nothing; a prop added to the setting may change;
// the owner is never refused.
func TestCLIAgentChangesOnlyTheStatusOfTheOwnersTask(t *testing.T) {
	t.Parallel()
	app, _ := cliApp(t)
	actorPane(t, app, "w7", "t1", "claude")
	app.getenv = envOf(map[string]string{"TUIOS_PANE_ID": "w7", "TUIOS_SESSION": "matomeru"})
	mustCLI(t, app, "task", "done", "t1")
	if got := stored(t, app, "t1"); got.Status != work.StatusDone || got.MadeBy() != work.ActorOwner || got.ChangedBy.Pane != "w7" {
		t.Fatalf("agent's done on the owner's lane task: %+v", got)
	}
	_, _, err := cli(app, "", "task", "set", "t1", "title", "the agent's words")
	if err == nil || !strings.Contains(err.Error(), "made by owner") || !strings.Contains(err.Error(), "the owner or the coordinator") || !strings.Contains(err.Error(), "not title") {
		t.Fatalf("agent's rename of the owner's task: %v", err)
	}
	if got := stored(t, app, "t1"); got.Title != "write the brief" {
		t.Fatalf("a refused write landed: %+v", got)
	}
	// t2 is the owner's and outside the lane: not even its status.
	if _, _, err := cli(app, "", "task", "set", "t2", "status", "done"); err == nil || !strings.Contains(err.Error(), "agents.write-scope") {
		t.Fatalf("outside the lane: %v", err)
	}
	// Its own subtask under the lane is its to change wholly.
	got := createdThing(t, app, "my step")
	id := got["id"].(string)
	mustCLI(t, app, "task", "set", id, "title", "my step, renamed")
	// A prop added to agents.owner-task-props may change.
	if err := app.r.Settings().Set(settingAgentsOwnerTaskProps, []string{"status", "easy"}); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, app, "task", "set", "t1", "easy", "true")
	if !stored(t, app, "t1").Easy {
		t.Fatal("easy in agents.owner-task-props was not written")
	}
	// The owner is never refused.
	app.getenv = envOf(nil)
	mustCLI(t, app, "task", "set", "t1", "title", "the owner's words")
	mustCLI(t, app, "task", "set", id, "title", "renamed by me")
	if got := stored(t, app, id); got.Title != "renamed by me" || got.MadeBy() != work.ActorAgent || got.ChangedBy.Kind != work.ActorOwner {
		t.Fatalf("owner's write: %+v", got)
	}
}

// The MCP server's writes are the agent of its pane, from the call's
// identity (harness, pane, session, task, model), and a tool on a task the
// owner made is refused with who to ask.
func TestMCPWritesAreThePanesAgent(t *testing.T) {
	t.Parallel()
	app, _, _ := mcpApp(t, map[string]string{"TUIOS_SESSION": "matomeru", "TUIOS_PANE_ID": "w1"})
	server, err := app.catalogMCP(strings.NewReader(""), io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	result, err := server.Call(context.Background(), "run", map[string]any{"verb": "task.create", "args": map[string]any{"title": "a step", "parent": "t1"}})
	if err != nil {
		t.Fatal(err)
	}
	got := result.(map[string]any)["result"].(map[string]any)
	made, err := app.work.Task(got["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if by := made.Maker(); by.Kind != work.ActorAgent || by.Harness != "claude" || by.Pane != "w1" || by.Task != "t1" || by.Model != "opus" || by.Session != "matomeru" {
		t.Fatalf("MCP create: %+v", by)
	}
	// A task the owner made in the pane's lane: task_done and task_status
	// change its status; a rename through the same path is refused, with
	// who to ask.
	app.work.SetActor(work.Owner())
	if err := app.work.PutTask(work.Task{ID: "mine", ProjectID: "matomeru", ParentID: "t1", Title: "the owner's step", Status: work.StatusTodo}); err != nil {
		t.Fatal(err)
	}
	out := rpc(t, app, toolCall(1, "task_done", map[string]any{"task_id": "mine"}), toolCall(2, "task_status", map[string]any{"status": "blocked"}))
	for _, resp := range out {
		if result, _ := resp["result"].(map[string]any); result["isError"] == true {
			t.Fatalf("status tool on the owner's lane task: %v", resp)
		}
	}
	if got, _ := app.work.Task("mine"); got.Status != work.StatusDone || got.MadeBy() != work.ActorOwner || got.ChangedBy.Pane != "w1" {
		t.Fatalf("task_done: %+v", got)
	}
	if got, _ := app.work.Task("t1"); got.Status != work.StatusBlocked {
		t.Fatalf("task_status on the lane task: %+v", got)
	}
	result, err = server.Call(context.Background(), "run", map[string]any{"verb": "task.set", "args": map[string]any{"id": "mine", "prop": "title", "value": "the agent's words"}})
	if err != nil {
		t.Fatal(err)
	}
	if reply := result.(map[string]any); reply["status"] != "failed" || !strings.Contains(reply["reason"].(string), "the owner or the coordinator") {
		t.Fatalf("MCP rename refusal: %v", result)
	}
	if got, _ := app.work.Task("mine"); got.Title != "the owner's step" {
		t.Fatalf("refused write landed: %+v", got)
	}
}

// Privacy verbs on both nouns: a new project is private (fail closed) and
// gets one quiet Inbox item; make-family exports its tasks and archives
// the item; a private task keeps its subtasks private too.
func TestPrivacyVerbsAndTheQuietInboxItem(t *testing.T) {
	t.Parallel()
	app, _ := cliApp(t)
	var p map[string]any
	if err := json.Unmarshal([]byte(mustCLI(t, app, "project", "create", "--json", `{"name":"newbie"}`)), &p); err != nil {
		t.Fatal(err)
	}
	pid := p["id"].(string)
	if p["privacy"] != work.PrivacyPrivate {
		t.Fatalf("new project privacy = %v", p["privacy"])
	}
	openItem := func() bool {
		items, err := app.inbox.items.All()
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, it := range items {
			if it.Key == privacyKey(pid) && it.ArchivedAt == 0 {
				n++
			}
		}
		if n > 1 {
			t.Fatalf("%d privacy items", n)
		}
		return n == 1
	}
	if !openItem() {
		t.Fatal("no privacy item for the new project")
	}
	var task map[string]any
	if err := json.Unmarshal([]byte(mustCLI(t, app, "task", "create", "--json", `{"title":"parent","project":"`+pid+`"}`)), &task); err != nil {
		t.Fatal(err)
	}
	parent := task["id"].(string)
	if task["exported"] != false {
		t.Fatal("a private project's task is exported")
	}
	mustCLI(t, app, "project", "make-family", "--target", pid)
	if openItem() {
		t.Fatal("make-family left the privacy item open")
	}
	if err := json.Unmarshal([]byte(mustCLI(t, app, "task", "create", "--json", `{"title":"child","parent":"`+parent+`"}`)), &task); err != nil {
		t.Fatal(err)
	}
	child := task["id"].(string)
	exported := func(id string) bool {
		var got map[string]any
		if err := json.Unmarshal([]byte(mustCLI(t, app, "task", "get", "--target", id)), &got); err != nil {
			t.Fatal(err)
		}
		return got["exported"] == true
	}
	if !exported(parent) || !exported(child) {
		t.Fatal("a family project's tasks are not exported")
	}
	mustCLI(t, app, "task", "make-private", "--target", parent)
	if exported(parent) || exported(child) {
		t.Fatal("make-private did not keep the task and its subtask in")
	}
	if !stored(t, app, parent).Private || stored(t, app, child).Private {
		t.Fatal("the flag is the parent's; the child inherits it")
	}
	mustCLI(t, app, "task", "make-family", "--target", parent)
	if !exported(child) {
		t.Fatal("make-family did not share the subtree again")
	}
	// The setting turns the Inbox item off.
	if err := app.r.Settings().Set(settingPrivacyInbox, false); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(mustCLI(t, app, "project", "create", "--json", `{"name":"quiet"}`)), &p); err != nil {
		t.Fatal(err)
	}
	pid = p["id"].(string)
	if openItem() {
		t.Fatal("projects.privacy-inbox off still added an item")
	}
}

// The Work page's by column, the agent and private marks, and the made-by
// filter; each has its off setting. The outline shows the same.
func TestWorkPageShowsWhoMadeEachTask(t *testing.T) {
	t.Parallel()
	a := open(t)
	a.work.SetActor(work.Actor{Kind: work.ActorAgent, Harness: "claude", Pane: "w1"})
	if _, err := a.work.AddTask("matomeru", "agent wrote this"); err != nil {
		t.Fatal(err)
	}
	a.work.SetActor(work.Owner())
	secret := stored(t, a, "t2")
	secret.Private = true
	if err := a.work.PutTask(secret); err != nil {
		t.Fatal(err)
	}
	executeA(t, a, "project", "pin", "", ochazuke.Arguments{"project": "matomeru"})
	f := frameAt(a, 120)
	for _, want := range []string{agentGlyph + " agent wrote this", privateGlyph + " lift the store", " by ", "claude", "me"} {
		if !strings.Contains(f, want) {
			t.Fatalf("frame lacks %q:\n%s", want, f)
		}
	}
	set := func(name string, v any) {
		t.Helper()
		if err := a.r.Settings().Set(name, v); err != nil {
			t.Fatal(err)
		}
	}
	set(settingTasksMadeBy, madeByAgents)
	if f := frameAt(a, 120); !strings.Contains(f, "agent wrote this") || strings.Contains(f, "lift the store") {
		t.Fatalf("agents' tasks:\n%s", f)
	}
	set(settingTasksMadeBy, madeByMine)
	if f := frameAt(a, 120); strings.Contains(f, "agent wrote this") || !strings.Contains(f, "lift the store") {
		t.Fatalf("mine lists owner tasks:\n%s", f)
	}
	set(settingTasksMadeBy, madeByAll)
	set(settingTasksColumns, []string{})
	set(settingTasksAgentGlyph, false)
	set(settingTasksPrivateGlyph, false)
	f = frameAt(a, 120)
	for _, gone := range []string{agentGlyph, privateGlyph, " by "} {
		if strings.Contains(f, gone) {
			t.Fatalf("frame still has %q with its setting off:\n%s", gone, f)
		}
	}
	set(settingTasksColumns, []string{columnBy})
	set(settingTasksAgentGlyph, true)
	executeA(t, a, "project", "outline", "matomeru", nil)
	f = frameAt(a, 120)
	if !strings.Contains(f, agentGlyph+" agent wrote this") || !strings.Contains(f, "claude") {
		t.Fatalf("outline lacks the mark or the by column:\n%s", f)
	}
}
