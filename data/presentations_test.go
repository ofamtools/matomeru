package data

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke/core"
)

func TestPresentationDefaultsCoverBuiltInNouns(t *testing.T) {
	reg := DefaultPresentations()
	for _, kind := range []string{KindTask, KindPane, KindLane, KindDocument, KindNote, KindQuestion, KindCommand, KindPage,
		KindRun, KindSignal, KindSchedule, KindTriageRule, KindExtension, "directory", KindContext} {
		for _, form := range PresentationForms {
			if spec, ok := reg.Declared(kind, form); !ok || len(spec.TitleFields) == 0 {
				t.Errorf("%s %s: declared = %v, title fields %v", kind, form, ok, spec.TitleFields)
			}
		}
	}

	task, _ := DefaultSchemas().Get(KindTask)
	rec := Record{Kind: KindTask, ID: "86218e2dfd81", Fields: map[string]any{"title": "Ship zero-field", "workStatus": "active"}}
	chip := reg.Resolve(rec, task, FormChip)
	if chip.Label != "Ship zero-field" || chip.Icon != "▶" || chip.Tone != "state.working" || chip.Ref != rec.Ref() || chip.Missing {
		t.Fatalf("task chip = %+v", chip)
	}
	card := reg.Resolve(rec, task, FormCard)
	if len(card.Badges) == 0 || card.Badges[0] != (PresentationBadge{Text: "Active", Tone: "state.working"}) {
		t.Fatalf("task card badges = %+v", card.Badges)
	}
}

func TestPresentationUserOverrideMerges(t *testing.T) {
	reg, err := LoadPresentations([]byte(`{"nouns":[
		{"kind":"task","forms":{"chip":{"icon":"T","maxLen":6,"iconField":"none","toneMap":{"active":"accent"}}}},
		{"kind":"culprit","forms":{"row":{"titleFields":["alias"]}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	task, _ := DefaultSchemas().Get(KindTask)
	spec := reg.Spec(task, FormChip)
	if spec.Icon != "T" || spec.MaxLen != 6 || len(spec.TitleFields) != 1 || spec.TitleFields[0] != "title" || spec.ToneField != "workStatus" {
		t.Fatalf("merged task chip = %+v", spec)
	}
	rec := Record{Kind: KindTask, ID: "x1", Fields: map[string]any{"title": "Ship zero-field", "workStatus": "active"}}
	if p := reg.Resolve(rec, task, FormChip); p.Label != "Ship…" || p.Icon != "T" || p.Tone != "accent" {
		t.Fatalf("overridden chip = %+v", p)
	}
	if _, err := LoadPresentations([]byte(`{"nouns":[{"forms":{}}]}`)); err == nil {
		t.Fatal("a noun with no kind loaded")
	}
}

func TestPresentationUniversalFallbackForUnknownNoun(t *testing.T) {
	reg := DefaultPresentations()
	mood := StatusColumn{Name: "mood", PrimaryRowStyle: true, Values: []StatusValue{{Name: "sly", Label: "Sly", Tone: "warning", Icon: "◆"}}}
	culprit := Schema{Kind: "culprit", Title: "Culprit", SummaryField: "alias", StatusColumns: []StatusColumn{mood}}

	rec := Record{Kind: "culprit", ID: "c0ffee1234ab", Fields: map[string]any{"alias": "The Fox", "mood": "sly", "partner": map[string]any{"kind": "culprit", "id": "beef"}}}
	for _, form := range PresentationForms {
		p := reg.Resolve(rec, culprit, form)
		if p.Label != "The Fox" || p.Icon != "◆" || p.Tone != "warning" || p.Missing {
			t.Errorf("%s = %+v", form, p)
		}
	}
	if card := reg.Resolve(rec, culprit, FormCard); len(card.Badges) != 1 || card.Badges[0].Text != "Sly" {
		t.Fatalf("card badges = %+v", card.Badges)
	}

	untitled := reg.Resolve(Record{Kind: "culprit", ID: "c0ffee1234ab"}, culprit, FormChip)
	if untitled.Label != "Untitled culprit" || strings.Contains(untitled.Label, "c0ffee") {
		t.Fatalf("untitled = %+v", untitled)
	}
	// A reference field never renders as kind:id.
	byRef := reg.Resolve(rec, Schema{Kind: "culprit", SummaryField: "partner"}, FormChip)
	if strings.Contains(byRef.Label, "beef") {
		t.Fatalf("ref-titled label = %q", byRef.Label)
	}

	// An extension catalog declares its noun's forms on the schema itself.
	culprit.Presentations = map[string]PresentationSpec{FormChip: {Prefix: "@", MaxLen: 4}}
	if p := reg.Resolve(rec, culprit, FormChip); p.Label != "@The…" {
		t.Fatalf("catalog chip = %+v", p)
	}
}

func TestPresentationDanglingRefs(t *testing.T) {
	s := openTest(t)
	task := create(t, s, KindTask, map[string]any{"title": "Real task"})
	dead := core.Ref{Kind: KindTask, ID: "2b161fe055c9"}
	pane := create(t, s, KindPane, map[string]any{"title": "worker", "task": dead})

	got := s.ResolveRefs([]core.Ref{task.Ref(), dead, task.Ref(), {}})
	if len(got) != 1 || got[task.Ref()].Text("title") != "Real task" {
		t.Fatalf("ResolveRefs = %+v", got)
	}

	snap, err := s.LoadShowSnapshot(pane.Ref())
	if err != nil {
		t.Fatal(err)
	}
	link := snap.FieldLinks["task"]
	if !link.Presentation.Missing || link.Presentation.Label != "(missing task)" || link.Presentation.Tone != "muted" ||
		link.Presentation.Ref != dead || strings.Contains(link.Presentation.Label, dead.ID) {
		t.Fatalf("dead link = %+v", link.Presentation)
	}
	if snap.Presentation.Label != "worker" || snap.Presentation.Form != FormDetail {
		t.Fatalf("detail = %+v", snap.Presentation)
	}

	// A directory titles itself by its path: not missing without a record.
	dir := s.ResolveRefsFor(nil).Presentation(core.Ref{Kind: "directory", ID: "/src/app"}, FormChip)
	if dir.Missing || dir.Label != "/src/app" {
		t.Fatalf("directory = %+v", dir)
	}
}

func TestFieldOptionsResolvedKeepsMatchValues(t *testing.T) {
	s := openTest(t)
	task := create(t, s, KindTask, map[string]any{"title": "Ship zero-field"})
	dead := core.Ref{Kind: KindTask, ID: "86218e2dfd81"}
	panes := []Record{
		*create(t, s, KindPane, map[string]any{"title": "a", "task": task.Ref()}),
		*create(t, s, KindPane, map[string]any{"title": "b", "task": dead}),
	}
	schema, _ := s.Registry().Get(KindPane)
	refs := s.ResolveRefsFor(FieldRefs(panes, "task"))
	opts := FieldOptionsResolved(schema, "task", panes, refs, 0)
	labels := map[string]QueryOption{}
	for _, o := range opts {
		labels[o.Value] = o
	}
	live, gone := labels[RefText(task.Ref())], labels[RefText(dead)]
	if live.Ref == nil || *live.Ref != task.Ref() || live.Presentation.Label != "Ship zero-field" {
		t.Fatalf("live option = %+v", live)
	}
	if gone.Ref == nil || !gone.Presentation.Missing || gone.Presentation.Label != "(missing task)" {
		t.Fatalf("dead option = %+v", gone)
	}
	for _, o := range opts {
		if strings.Contains(o.Presentation.Label, ":") {
			t.Errorf("label %q shows a raw reference", o.Presentation.Label)
		}
	}

	title := FieldOptionsResolved(schema, "title", panes, refs, 0)
	if len(title) != 2 || title[0].Value != "a" || title[0].Presentation.Label != "a" || title[0].Ref != nil {
		t.Fatalf("plain options = %+v", title)
	}
}
