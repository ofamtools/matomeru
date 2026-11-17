package main

import (
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

// g q opens the ask form with the question first: a multi-line text area,
// focused and in insert, so typing goes straight into it.
func TestAskFormOpensOnTheQuestionTextAreaInInsert(t *testing.T) {
	t.Parallel()
	a := open(t)
	press(t, a, "g", "q")
	e := a.r.RecordEditor(askFormID)
	if e == nil {
		t.Fatalf("g q opened no ask form; top modal %q", a.r.TopModal())
	}
	focus := a.r.FocusID()
	in := a.r.Input(focus)
	if focus != e.FocusTarget() || in == nil || !in.Multiline {
		t.Fatalf("focus %q (form starts at %q), input %+v: not the question text area", focus, e.FocusTarget(), in)
	}
	if a.r.Mode.Name != "insert" {
		t.Fatalf("mode %q, want insert", a.r.Mode.Name)
	}
	typeText(t, a, "why")
	if got := e.Values()["question"]; got != "why" {
		t.Fatalf("typed into %q, question holds %q", focus, got)
	}
}

// agent.ask-new records the question with question.question.ask, then starts
// the chosen harness with its model and effort (codex's effort is a launch
// flag), the prompt carrying the question and its id, and relates the new
// pane to the question.
func TestAgentAskRecordsThenStartsTheChosenHarness(t *testing.T) {
	t.Parallel()
	path := seeded(t)
	catalog := `{"schema":1,"name":"o-questions","version":"1.0","nouns":[
 {"name":"question","verbs":[{"name":"ask","kind":"application","params":{"question":{"type":"string","required":true}}}]}],
 "bindings":[],"pages":[]}`
	bin, log := fakeExtension(t, catalog, `"question ask") echo '{"existed":false,"question":{"id":"q-1"}}' ;;`)
	a := registered(t, path, "question", bin)
	d := &fakeDaemon{panes: a.panesd.(*fakePanes)}
	a.launcher = d
	question := "Which harness?\nAnd why?"
	mustCLI(t, a, "agent", "ask-new", "--question", question, "--harness", "codex", "--model", "gpt-x", "--effort", "high", "--yes")
	// The fake's sh echo turns the JSON's \n into a line break.
	if got := strings.Join(workCalls(t, log), "\n"); !strings.Contains(got, `|question ask --json {"question":"Which harness?`) || !strings.Contains(got, `And why?"} --yes`) {
		t.Fatalf("ask calls %q", got)
	}
	if len(d.launches) != 1 {
		t.Fatalf("launches %d, want 1", len(d.launches))
	}
	w := d.launches[0]
	if got := strings.Join(w.Command, " "); !strings.HasPrefix(got, "codex --model gpt-x -c model_reasoning_effort=high") {
		t.Fatalf("argv %q", got)
	}
	if !strings.Contains(w.Prompt, question) || !strings.Contains(w.Prompt, "q-1") {
		t.Fatalf("prompt %q lacks the question or its id", w.Prompt)
	}
	rels := relationsOf(t, a, ochazuke.RelationQuery{Kind: aboutRelationKind, Thing: "pane:" + w.ID})
	if !slices.ContainsFunc(rels, func(r ochazuke.Relation) bool { return r.To == "question.question:q-1" }) {
		t.Fatalf("pane %s not related to the question: %+v", w.ID, rels)
	}
}
