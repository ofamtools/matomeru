package data

import (
	"errors"
	"testing"
)

func TestIdeaGuardRecognizesIdeas(t *testing.T) {
	for _, tc := range []struct {
		rec  Record
		idea bool
	}{
		{Record{Kind: KindConcept, Fields: map[string]any{FieldConceptStatus: ConceptStatusIdea}}, true},
		{Record{Kind: KindConcept, Fields: map[string]any{FieldConceptStatus: ConceptStatusActive}}, false},
		{Record{Kind: KindTask, Fields: map[string]any{"workStatus": IdeaMarker}}, true},
		{Record{Kind: KindTask, Fields: map[string]any{IdeaMarker: true}}, true},
		{Record{Kind: KindTask, Fields: map[string]any{"workStatus": "todo"}}, false},
	} {
		if IsIdea(tc.rec) != tc.idea || CanAutoDispatch(tc.rec) == tc.idea {
			t.Errorf("IsIdea(%v) = %v, want %v", tc.rec.Fields, IsIdea(tc.rec), tc.idea)
		}
	}
}

func TestIdeaCaptureIsNeverQueuedOrDispatched(t *testing.T) {
	s := openTest(t)
	proj := create(t, s, KindTask, map[string]any{"title": "Apollo"})
	idea, err := s.CaptureIdea(IdeaCaptureInput{Title: "  Big picture  ", Summary: "explore", ProjectID: proj.ID}, owner)
	if err != nil {
		t.Fatal(err)
	}
	if idea.Kind != KindConcept || idea.Text(FieldConceptStatus) != ConceptStatusIdea || idea.Text("title") != "Big picture" || idea.Bool("forAgent") {
		t.Fatalf("captured idea: %+v", idea)
	}
	if CanAutoDispatch(*idea) {
		t.Fatal("CanAutoDispatch accepted an idea")
	}
	if _, err := s.CaptureIdea(IdeaCaptureInput{Title: " "}, owner); err == nil {
		t.Fatal("captured an idea without a title")
	}

	// A task marked as an idea is refused at every agent boundary.
	ideaTask := create(t, s, KindTask, map[string]any{"title": "maybe", "parent": proj.Ref(), IdeaMarker: true})
	if _, err := s.SetTaskAgentFlag(ideaTask.Ref(), true, AgentStatusQueued, "", owner); !errors.Is(err, ErrIdeaNotDispatchable) {
		t.Fatalf("SetTaskAgentFlag on an idea: %v", err)
	}
	if _, err := s.AddTaskToQueue(ideaTask.ID, "review", nil, owner); !errors.Is(err, ErrIdeaNotDispatchable) {
		t.Fatalf("AddTaskToQueue on an idea: %v", err)
	}
	// Even an idea already flagged by a raw patch stays out of the queue.
	if _, _, err := s.Patch(PatchInput{Ref: ideaTask.Ref(), Fields: map[string]any{"forAgent": true, "agentStatus": AgentStatusQueued}}, owner); err != nil {
		t.Fatal(err)
	}
	real := queuedTask(t, s, map[string]any{"title": "real", "parent": proj.Ref()})
	items, err := s.QueryAgentQueue(AgentQueueFilter{Status: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Task.ID != real.ID {
		t.Fatalf("agent queue = %+v, want only %s", items, real.ID)
	}

	ideas, err := s.Ideas(proj.ID)
	if err != nil || len(ideas) != 1 || ideas[0].ID != idea.ID {
		t.Fatalf("Ideas(%s) = %v %v", proj.ID, ideas, err)
	}
	if other, _ := s.Ideas("nope"); len(other) != 0 {
		t.Fatalf("Ideas of another project = %v", other)
	}
}

func TestIdeaPromoteCreatesLinkedTask(t *testing.T) {
	s := openTest(t)
	proj := create(t, s, KindTask, map[string]any{"title": "Apollo"})
	idea, err := s.CaptureIdea(IdeaCaptureInput{Title: "Offline mode", Summary: "sync later", ProjectID: proj.ID}, owner)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.PromoteIdea(idea.ID, owner)
	if err != nil {
		t.Fatal(err)
	}
	parent, _ := task.RefField("parent")
	if task.Kind != KindTask || task.Text("workStatus") != "todo" || task.Text("title") != "Offline mode" ||
		task.Text("description") != "sync later" || parent.ID != proj.ID || task.Bool("forAgent") {
		t.Fatalf("promoted task: %+v", task)
	}
	links, err := s.RelatedRecords(task.Ref(), RelPromotedFrom, Outgoing)
	if err != nil || len(links) != 1 || links[0].ID != idea.ID {
		t.Fatalf("provenance links = %v %v", links, err)
	}
	after, err := s.Get(idea.Ref())
	if err != nil {
		t.Fatal(err)
	}
	promoted, _ := after.RefField(FieldIdeaPromotedTask)
	if after.Text(FieldConceptStatus) != ConceptStatusActive || promoted.ID != task.ID || IsIdea(*after) {
		t.Fatalf("idea after promote: %+v", after)
	}
	if _, err := s.PromoteIdea(idea.ID, owner); err == nil {
		t.Fatal("promoted an idea twice")
	}
	if ideas, _ := s.Ideas(""); len(ideas) != 0 {
		t.Fatalf("promoted idea still listed: %v", ideas)
	}
}

func TestPruneIdeasDropsIdeaDescendants(t *testing.T) {
	root := TreeNode{Record: Record{Kind: KindTask, ID: "r"}, Children: []TreeNode{
		{Record: Record{Kind: KindTask, ID: "keep"}, Children: []TreeNode{
			{Record: Record{Kind: KindTask, ID: "deep", Fields: map[string]any{IdeaMarker: true}}},
		}},
		{Record: Record{Kind: KindTask, ID: "drop", Fields: map[string]any{"workStatus": IdeaMarker}}},
	}}
	got := PruneIdeas(root)
	if len(got.Children) != 1 || got.Children[0].Record.ID != "keep" || len(got.Children[0].Children) != 0 {
		t.Fatalf("PruneIdeas = %+v", got)
	}
}
