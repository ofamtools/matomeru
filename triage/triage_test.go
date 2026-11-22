package triage

import (
	"slices"
	"strings"
	"testing"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/expr"
)

var bot = core.Actor{Kind: "system", ID: "triage"}

func open(t *testing.T) *data.Store {
	t.Helper()
	store, err := data.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func signal(t *testing.T, store *data.Store, fields map[string]any) data.Record {
	t.Helper()
	rec, _, err := store.Create(data.CreateInput{Kind: data.KindSignal, Fields: fields}, bot)
	if err != nil {
		t.Fatal(err)
	}
	return *rec
}

func rule(t *testing.T, store *data.Store, fields map[string]any) data.Record {
	t.Helper()
	rec, _, err := store.Create(data.CreateInput{Kind: data.KindTriageRule, Fields: fields}, bot)
	if err != nil {
		t.Fatal(err)
	}
	return *rec
}

func get(t *testing.T, store *data.Store, rec data.Record) data.Record {
	t.Helper()
	fresh, err := store.Get(rec.Ref())
	if err != nil {
		t.Fatal(err)
	}
	return *fresh
}

// Every action does what it says, rules run by priority, and a rule that is
// off or does not parse changes nothing.
func TestEvaluateActions(t *testing.T) {
	store := open(t)
	at := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	Now = func() time.Time { return at }
	t.Cleanup(func() { Now = time.Now })
	task, _, err := store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "Ship v2"}}, bot)
	if err != nil {
		t.Fatal(err)
	}

	rule(t, store, map[string]any{"title": "bots", "when": `author == "dependabot[bot]"`, "action": data.ActionMarkNoise})
	rule(t, store, map[string]any{"title": "reviews", "when": `source == "github" && eventKind == "review_request"`, "action": data.ActionMarkActionable, "priority": 5})
	rule(t, store, map[string]any{"title": "label", "when": `source == "github"`, "action": data.ActionTag, "actionValue": "gh", "priority": 9})
	rule(t, store, map[string]any{"title": "ci later", "when": `source == "ci"`, "action": data.ActionSnooze, "actionValue": "2h"})
	rule(t, store, map[string]any{"title": "v2", "when": `container == "ofam/matomeru"`, "action": data.ActionRelateTask, "actionValue": task.ID})
	rule(t, store, map[string]any{"title": "off", "when": `source == "github"`, "action": data.ActionMarkNoise, "enabled": false})
	rule(t, store, map[string]any{"title": "broken", "when": `source ==`, "action": data.ActionMarkNoise})

	rules, problems, err := Rules(store)
	if err != nil || len(rules) != 5 || len(problems) != 1 || !strings.Contains(problems[0], "broken") {
		t.Fatalf("rules %d, problems %v, err %v", len(rules), problems, err)
	}
	if order := []string{rules[0].Record.Text("title"), rules[1].Record.Text("title")}; !slices.Equal(order, []string{"label", "reviews"}) {
		t.Fatalf("priority order starts %v", order)
	}

	noise := signal(t, store, map[string]any{"title": "Bump x", "source": "github", "author": "dependabot[bot]"})
	res, err := Evaluate(store, noise, bot)
	if err != nil || res.Evaluated != 1 || res.Matched != 1 || len(res.Applied) != 2 {
		t.Fatalf("noise: %+v, %v", res, err)
	}
	if got := get(t, store, noise); got.Text("triageState") != data.TriageNoise || !got.IsArchived() {
		t.Fatalf("noise signal: state %q, archived %v", got.Text("triageState"), got.IsArchived())
	}
	if tags, _ := store.Tags(noise.Ref()); !slices.Equal(tags, []string{"gh"}) {
		t.Fatalf("tags = %v", tags)
	}

	review := signal(t, store, map[string]any{"title": "Review wanted", "source": "github", "eventKind": "review_request", "author": "kmo", "container": "ofam/matomeru"})
	if _, err := Evaluate(store, review, bot); err != nil {
		t.Fatal(err)
	}
	got := get(t, store, review)
	if ref, _ := got.RefField("task"); got.Text("triageState") != data.TriageActionable || got.IsArchived() || ref != task.Ref() {
		t.Fatalf("review signal: state %q, task %v", got.Text("triageState"), ref)
	}

	ci := signal(t, store, map[string]any{"title": "build failed", "source": "ci"})
	if _, err := Evaluate(store, ci, bot); err != nil {
		t.Fatal(err)
	}
	if got := get(t, store, ci); got.SnoozedUntil == nil || !got.SnoozedUntil.Equal(at.Add(2*time.Hour)) || got.Text("triageState") != data.TriageUnreviewed {
		t.Fatalf("ci signal: snoozed until %v, state %q", got.SnoozedUntil, got.Text("triageState"))
	}

	quiet := signal(t, store, map[string]any{"title": "hello", "source": "mail"})
	if res, _ := Evaluate(store, quiet, bot); res.Matched != 0 || len(res.Applied) != 0 {
		t.Fatalf("a signal no rule holds for: %+v", res)
	}
	if _, err := Evaluate(store, *task, bot); err == nil {
		t.Fatal("a task was evaluated as a signal")
	}
}

// A rule made after the signals arrived triages them; signals already
// reviewed are left alone.
func TestApplyAllIsRetroactive(t *testing.T) {
	store := open(t)
	var bots []data.Record
	for _, title := range []string{"Bump a", "Bump b", "Bump c"} {
		bots = append(bots, signal(t, store, map[string]any{"title": title, "source": "github", "author": "dependabot[bot]"}))
	}
	human := signal(t, store, map[string]any{"title": "Question", "source": "github", "author": "kmo"})
	kept := signal(t, store, map[string]any{"title": "Bump kept", "source": "github", "author": "dependabot[bot]", "triageState": data.TriageActionable})

	if res, err := ApplyAll(store, bot); err != nil || res.Evaluated != 4 || res.Matched != 0 {
		t.Fatalf("no rules yet: %+v, %v", res, err)
	}
	draft := DraftRuleFromSignal(bots[0])
	if draft["when"] != `source == "github" && author == "dependabot[bot]"` || draft["action"] != data.ActionMarkNoise || draft["enabled"] != true {
		t.Fatalf("draft = %+v", draft)
	}
	if _, err := expr.Parse(draft["when"].(string)); err != nil {
		t.Fatalf("the drafted expression does not parse: %v", err)
	}
	rule(t, store, draft)

	res, err := ApplyAll(store, bot)
	if err != nil || res.Evaluated != 4 || res.Matched != 3 || len(res.Applied) != 3 {
		t.Fatalf("ApplyAll: %+v, %v", res, err)
	}
	for _, sig := range bots {
		if got := get(t, store, sig); got.Text("triageState") != data.TriageNoise || !got.IsArchived() {
			t.Fatalf("%s was not cleaned up", sig.Text("title"))
		}
	}
	if got := get(t, store, human); got.Text("triageState") != data.TriageUnreviewed || got.IsArchived() {
		t.Fatal("a signal the rule does not hold for was changed")
	}
	if got := get(t, store, kept); got.Text("triageState") != data.TriageActionable || got.IsArchived() {
		t.Fatal("a signal already reviewed was changed")
	}
	// Nothing is left for a second pass.
	if res, _ := ApplyAll(store, bot); res.Evaluated != 1 || res.Matched != 0 {
		t.Fatalf("second pass: %+v", res)
	}
}

func TestDraftAndIngest(t *testing.T) {
	store := open(t)
	if d := DraftRuleFromSignal(data.Record{Kind: data.KindSignal, Fields: map[string]any{"eventKind": "failure", "container": "ofam/x", "title": "t"}}); d["when"] != `eventKind == "failure" && container == "ofam/x"` {
		t.Fatalf("draft without source and author = %v", d["when"])
	}
	if d := DraftRuleFromSignal(data.Record{Kind: data.KindSignal, Fields: map[string]any{"title": `say "hi"`}}); d["when"] != `title == "say \"hi\""` {
		t.Fatalf("draft from a title = %v", d["when"])
	}
	rule(t, store, map[string]any{"title": "bots", "when": `author == "renovate[bot]"`, "action": data.ActionMarkNoise})
	rec, res, err := Ingest(store, map[string]any{"source": "github", "eventKind": "pull_request", "author": "renovate[bot]", "body": ""}, bot)
	if err != nil || rec.Text("title") != "github pull_request" || rec.Text("occurredAt") == "" || res.Matched != 1 || !rec.IsArchived() {
		t.Fatalf("ingest: %+v, %+v, %v", rec, res, err)
	}
	if _, has := rec.Fields["body"]; has {
		t.Fatal("an empty field was stored")
	}
	if _, _, err := Ingest(store, map[string]any{"body": "no title"}, bot); err == nil {
		t.Fatal("a signal with nothing to call it was ingested")
	}
}
