package app

import (
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
)

func newIdeasApp(t *testing.T) (*App, *Driver, *data.Store) {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	a, err := New(store, Options{Now: func() time.Time { return now }, DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a, NewDriver(a, 140, 40), store
}

func ideasFacts(t *testing.T, a *App) map[string]any {
	t.Helper()
	root := a.View(a.State())
	node := root.Find(views.IdeasPageID)
	if node == nil {
		t.Fatal("Ideas page missing from the rendered view")
	}
	return node.Facts
}

func TestIdeaCaptureListAndPromote(t *testing.T) {
	a, d, store := newIdeasApp(t)
	d.Keys("g", "I")
	if a.Space() != SpaceIdeas || a.Focus() != views.IdeasListID {
		t.Fatalf("g I opened %q, focus %q", a.Space(), a.Focus())
	}
	if f := ideasFacts(t, a); f["empty"] != true || f["ideaOnly"] != true {
		t.Fatalf("empty facts: %v", f)
	}

	d.Send(core.CommandEvent{Command: core.Command{ID: CmdIdeaCapture, Args: map[string]any{"title": "Offline mode", "summary": "explore sync"}, Actor: User}})
	frame := d.Frame()
	for _, want := range []string{"Ideas", views.IdeaBadge, "✧ Offline mode", "explore sync"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("Ideas page missing %q:\n%s", want, frame)
		}
	}
	f := ideasFacts(t, a)
	ideaID, _ := f["selectedIdea"].(string)
	if f["count"] != 1 || ideaID == "" {
		t.Fatalf("facts after capture: %v", f)
	}
	if items, _ := store.QueryAgentQueue(data.AgentQueueFilter{Status: "all"}); len(items) != 0 {
		t.Fatalf("an idea reached the agent queue: %v", items)
	}

	d.Keys("P")
	if f := ideasFacts(t, a); f["count"] != 0 {
		t.Fatalf("promoted idea still listed: %v", f)
	}
	idea, err := store.Get(core.Ref{Kind: data.KindConcept, ID: ideaID})
	if err != nil {
		t.Fatal(err)
	}
	taskRef, ok := idea.RefField(data.FieldIdeaPromotedTask)
	if !ok || idea.Text(data.FieldConceptStatus) != data.ConceptStatusActive {
		t.Fatalf("idea after promote: %+v", idea)
	}
	task, err := store.Get(taskRef)
	if err != nil || task.Text("workStatus") != "todo" || task.Bool("forAgent") {
		t.Fatalf("promoted task: %+v %v", task, err)
	}
}

func TestIdeaTriageSkipsIdeaTasks(t *testing.T) {
	a, d, store := newIdeasApp(t)
	if _, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{
		"title": "Someday", data.IdeaMarker: true, "order": int64(1),
	}}, User); err != nil {
		t.Fatal(err)
	}
	d.Keys("T", "X")
	if !strings.Contains(a.Status(), data.IdeaSkipReason) {
		t.Fatalf("triage of an idea: status %q", a.Status())
	}
}
