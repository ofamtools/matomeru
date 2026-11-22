package mcpui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

func laneStore(t *testing.T) (*data.Store, *Toolbox, map[string]string) {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	env := map[string]string{}
	box := NewToolbox(store, nil)
	box.Getenv = func(k string) string { return env[k] }
	return store, box, env
}

func callAs[T any](t *testing.T, box *Toolbox, tool string, args any) T {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	out, err := box.Call(context.Background(), core.Actor{Kind: "agent", ID: "claude"}, tool, raw)
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	b, _ := json.Marshal(out)
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s result: %v", tool, err)
	}
	return v
}

// lane.start reads the pane and the task from the environment, claims the
// pane, and the other three tools build on the lane it made.
func TestLaneTools(t *testing.T) {
	store, box, env := laneStore(t)
	env[EnvPaneID], env[EnvTaskID] = "w7", "t1"

	started := callAs[LaneResult](t, box, ToolLaneStart, map[string]any{"title": "Wave 3", "branch": "wave-3", "agentModel": "opus"})
	lane := started.Lane
	if task, _ := lane.RefField("task"); lane.Text("laneStatus") != "planned" || task != taskRef("t1") || lane.Text("branch") != "wave-3" {
		t.Fatalf("lane = %+v", lane.Fields)
	}
	if started.Claim == nil || started.Claim.Kind != data.RelClaim || started.Claim.To != paneRef("w7") {
		t.Fatalf("claim = %+v", started.Claim)
	}
	// An explicit task and pane win over the environment; no pane, no claim.
	env[EnvPaneID] = ""
	child := callAs[LaneResult](t, box, ToolLaneStart, map[string]any{"title": "picker-worker", "task": "t2", "parentLane": lane.ID, "status": "running"})
	if task, _ := child.Lane.RefField("task"); task != taskRef("t2") || child.Claim != nil {
		t.Fatalf("child task %v, claim %v", task, child.Claim)
	}
	if parent, _ := child.Lane.RefField("parentLane"); parent != lane.Ref() {
		t.Fatalf("parentLane = %v", parent)
	}

	updated := callAs[LaneResult](t, box, ToolLaneUpdate, map[string]any{"lane": lane.ID, "status": "review", "summary": "picker done", "headCommit": "abc123", "branch": ""})
	if u := updated.Lane; u.Text("laneStatus") != "review" || u.Text("summary") != "picker done" || u.Text("headCommit") != "abc123" || u.Text("branch") != "" {
		t.Fatalf("updated = %+v", u.Fields)
	}

	logged := callAs[LaneLogResult](t, box, ToolLaneLog, map[string]any{"lane": lane.ID, "kind": "decision", "text": "keep the trie", "body": "it is fast enough"})
	if n := logged.Note; n.Text("title") != "decision: keep the trie" || n.Text("logKind") != "decision" || n.Time("loggedAt").IsZero() || n.Text("body") != "it is fast enough" {
		t.Fatalf("log note = %+v", n.Fields)
	}
	if rel := logged.Relation; rel.Kind != data.RelAbout || rel.From != logged.Note.Ref() || rel.To != lane.Ref() {
		t.Fatalf("log relation = %+v", rel)
	}

	for kind, want := range map[string]string{data.KindDocument: data.RelCreated, data.KindPage: data.RelCreated, data.KindRun: data.RelRun, data.KindPane: data.RelClaim} {
		att := callAs[LaneAttachResult](t, box, ToolLaneAttach, map[string]any{"lane": lane.ID, "kind": kind, "id": "x1"})
		if att.Relation == nil || att.Relation.Kind != want || att.Relation.From != lane.Ref() {
			t.Errorf("attach %s = %+v, want relation %s", kind, att.Relation, want)
		}
	}
	plan := callAs[LaneAttachResult](t, box, ToolLaneAttach, map[string]any{"lane": lane.ID, "kind": data.KindDocument, "id": "d9", "relation": "plan"})
	if plan.Relation.Kind != data.RelPlan {
		t.Fatalf("plan relation = %+v", plan.Relation)
	}

	// Writes are an agent's, and a missing lane or text is an error.
	sets, err := store.History(&core.Ref{Kind: data.KindLane, ID: lane.ID}, 0)
	if err != nil || len(sets) == 0 || sets[0].Actor.Kind != "agent" {
		t.Fatalf("history: %v %+v", err, sets)
	}
	for tool, args := range map[string]string{
		ToolLaneStart:     `{}`,
		ToolLaneUpdate:    `{"lane":"nope","status":"done"}`,
		ToolLaneLog:       `{"lane":"` + lane.ID + `"}`,
		ToolLaneAttach:    `{"lane":"` + lane.ID + `"}`,
		ToolLaneLog + " ": `{"lane":"` + lane.ID + `","text":"x","bogus":1}`,
	} {
		if _, err := box.Call(context.Background(), core.Actor{}, strings.TrimSpace(tool), json.RawMessage(args)); err == nil {
			t.Errorf("%s %s: no error", tool, args)
		}
	}
}

// A ui.* call that names no pane uses the pane (and task) of the environment.
func TestEnvDefaultsForPages(t *testing.T) {
	store, box, env := laneStore(t)
	env[EnvPaneID], env[EnvTaskID] = "w7", "t1"
	root := ui.ViewNode{Kind: ui.KindVStack}
	made := callAs[PageResult](t, box, ToolCreatePage, map[string]any{"title": "Compare", "root": root})
	page, err := store.Get(made.Ref)
	if err != nil {
		t.Fatal(err)
	}
	pane, _ := page.RefField("pane")
	task, _ := page.RefField("task")
	if pane != paneRef("w7") || task != taskRef("t1") {
		t.Fatalf("page pane %v task %v", pane, task)
	}
	// A slug resolves within the environment's pane.
	callAs[PageResult](t, box, ToolUpdatePage, map[string]any{"page": "compare", "mode": ModeAppend, "node": ui.ViewNode{Kind: ui.KindText, ID: "n1"}})
	asked := callAs[PageResult](t, box, ToolAsk, map[string]any{"title": "Ship?", "choices": []map[string]any{{"id": "yes"}, {"id": "no"}}})
	if q, _ := store.Get(asked.Ref); q == nil || q.Text("slug") != QuestionsSlug {
		t.Fatalf("question page = %+v", q)
	} else if p, _ := q.RefField("pane"); p != paneRef("w7") {
		t.Fatalf("question pane = %v", p)
	}
	// A pane given explicitly is kept.
	other := callAs[PageResult](t, box, ToolCreatePage, map[string]any{"title": "Other", "pane": "w9", "root": root})
	if rec, _ := store.Get(other.Ref); rec == nil {
		t.Fatal("no page")
	} else if p, _ := rec.RefField("pane"); p != paneRef("w9") {
		t.Fatalf("explicit pane = %v", p)
	}
	// With no environment nothing is added.
	env[EnvPaneID], env[EnvTaskID] = "", ""
	bare := callAs[PageResult](t, box, ToolCreatePage, map[string]any{"title": "Bare", "root": root})
	if rec, _ := store.Get(bare.Ref); rec == nil {
		t.Fatal("no page")
	} else if _, has := rec.RefField("pane"); has {
		t.Fatal("a pane appeared from nowhere")
	}
}

// triage.ingest records a signal and runs the rules on it; triage.evaluate
// runs them again, on one signal or on all that are unreviewed.
func TestTriageTools(t *testing.T) {
	store, box, _ := laneStore(t)
	if !box.HasTool(ToolTriageIngest) || !box.HasTool(ToolTriageEvaluate) {
		t.Fatal("the toolbox does not list the triage tools")
	}
	quiet := callAs[TriageResult](t, box, ToolTriageIngest, map[string]any{"source": "github", "eventKind": "pull_request", "author": "dependabot[bot]", "title": "Bump x", "task": "t1"})
	if s := quiet.Signal; s == nil || s.Kind != data.KindSignal || s.Text("triageState") != data.TriageUnreviewed || quiet.Matched != 0 || quiet.Evaluated != 1 {
		t.Fatalf("ingest with no rules = %+v", quiet)
	}
	if task, _ := quiet.Signal.RefField("task"); task != taskRef("t1") {
		t.Fatalf("task = %v", task)
	}
	if _, _, err := store.Create(data.CreateInput{Kind: data.KindTriageRule, Fields: map[string]any{
		"title": "bots", "when": `author == "dependabot[bot]"`, "action": data.ActionMarkNoise}}, core.Actor{Kind: "user", ID: "owner"}); err != nil {
		t.Fatal(err)
	}
	one := callAs[TriageResult](t, box, ToolTriageEvaluate, map[string]any{"signal": quiet.Signal.ID})
	if one.Matched != 1 || one.Signal.Text("triageState") != data.TriageNoise || !one.Signal.IsArchived() || one.Applied[0].Action != data.ActionMarkNoise {
		t.Fatalf("evaluate one = %+v", one)
	}
	noisy := callAs[TriageResult](t, box, ToolTriageIngest, map[string]any{"source": "github", "author": "dependabot[bot]", "title": "Bump y"})
	if noisy.Matched != 1 || !noisy.Signal.IsArchived() {
		t.Fatalf("ingest with a rule = %+v", noisy)
	}
	callAs[TriageResult](t, box, ToolTriageIngest, map[string]any{"source": "slack", "author": "kmo", "title": "ping"})
	all := callAs[TriageResult](t, box, ToolTriageEvaluate, map[string]any{})
	if all.Signal != nil || all.Evaluated != 1 || all.Matched != 0 {
		t.Fatalf("evaluate all = %+v", all)
	}
	// The writes are an agent's, never the owner's.
	noisyRef := noisy.Signal.Ref()
	history, err := store.History(&noisyRef, 10)
	if err != nil || len(history) == 0 || history[0].Actor.Kind != "agent" {
		t.Fatalf("history actor = %+v, %v", history, err)
	}
	if _, err := box.Call(context.Background(), core.Actor{}, ToolTriageEvaluate, json.RawMessage(`{"signal": "nope"}`)); err == nil {
		t.Fatal("evaluating a signal that is not there succeeded")
	}
}
