package main

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/daemon"
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

// With launch.option-pickers off the launch fields are free text, and a
// stray word left in one (the owner's "but" in fast) does not stop the ask form's agent: it starts with the harness's default for
// that setting, and a notice says the value was not applied.
func TestAskFormStartsTheAgentPastAValueTheHarnessDoesNotTake(t *testing.T) {
	t.Parallel()
	a := open(t)
	if err := a.r.Settings().Set(settingLaunchPickers, "false"); err != nil {
		t.Fatal(err)
	}
	d := &fakeDaemon{panes: a.panesd.(*fakePanes)}
	a.launcher = d
	press(t, a, "g", "q")
	typeText(t, a, "why")
	for a.r.FocusID() != askFormID+"/fast" {
		press(t, a, "tab")
	}
	press(t, a, "i")
	typeText(t, a, "but")
	press(t, a, "esc")
	_, cmd := a.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModAlt}) // form.run
	settle(a, cmd)
	if len(d.launches) != 1 {
		t.Fatalf("launches %d, want 1; notices %q", len(d.launches), a.m.Notices)
	}
	if got := strings.Join(d.launches[0].Command, " "); strings.Contains(got, "fast") {
		t.Fatalf("argv %q carries the stray fast", got)
	}
	if !slices.ContainsFunc(a.m.Notices, func(n string) bool { return strings.Contains(n, `fast="but" not applied`) }) {
		t.Fatalf("no notice for the stray fast: %q", a.m.Notices)
	}
}

// altEnter presses alt+enter, the ask form's send key (form.run), and
// lets the launch it starts finish.
func altEnter(a *App) {
	_, cmd := a.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	settle(a, cmd)
}

// askOpen opens the ask form over a fake launcher.
func askOpen(t *testing.T) (*App, *fakeDaemon) {
	t.Helper()
	a := open(t)
	d := &fakeDaemon{panes: a.panesd.(*fakePanes)}
	a.launcher = d
	press(t, a, "g", "q")
	return a, d
}

// Typing in the question never sends it (the owner's 2026-11-19 report:
// enter on the harness field sent the form): enter in the question is a
// new line, and enter on any other field does that field's own thing (a
// choice field opens its chooser, a text field moves on), never a send.
func TestAskFormEnterNeverSends(t *testing.T) {
	t.Parallel()
	a, d := askOpen(t)
	typeText(t, a, "one")
	press(t, a, "enter")
	typeText(t, a, "two")
	for _, field := range askFields[1:] {
		// Close a chooser enter opened: esc leaves its filter's insert,
		// q closes it (esc is cooling down after leaving insert).
		for _, key := range []string{"esc", "q"} {
			if a.r.TopModal() != askFormID {
				press(t, a, key)
			}
		}
		for i := 0; a.r.FocusID() != askFormID+"/"+field; i++ {
			if i > len(askFields) {
				t.Fatalf("tab never reached %s: focus %q, top modal %q", field, a.r.FocusID(), a.r.TopModal())
			}
			press(t, a, "tab")
		}
		press(t, a, "enter")
		if len(d.launches) != 0 || a.r.RecordEditor(askFormID) == nil {
			t.Fatalf("enter on %s sent the form: launches %d, top modal %q", field, len(d.launches), a.r.TopModal())
		}
	}
	if got := a.r.RecordEditor(askFormID).Values()["question"]; got != "one\ntwo" {
		t.Fatalf("question %q, want two lines", got)
	}
}

// alt+enter sends the ask form once from any field, in insert or normal.
func TestAskFormAltEnterSendsOnceFromAnyField(t *testing.T) {
	t.Parallel()
	for _, field := range askFields {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			a, d := askOpen(t)
			typeText(t, a, "why")
			for a.r.FocusID() != askFormID+"/"+field {
				press(t, a, "tab")
			}
			altEnter(a)
			if len(d.launches) != 1 || a.r.TopModal() != "" {
				t.Fatalf("from %s: launches %d, top modal %q; notices %q", field, len(d.launches), a.r.TopModal(), a.m.Notices)
			}
		})
	}
}

// ask.follow off (the default) leaves the page and focus where they were
// before the form opened, the new pane in the list unfocused, and a
// notice names it; on opens the new pane, as agent.start does.
func TestAskFollowSetting(t *testing.T) {
	t.Parallel()
	for _, follow := range []bool{false, true} {
		t.Run(strconv.FormatBool(follow), func(t *testing.T) {
			t.Parallel()
			a := open(t)
			if err := a.r.Settings().Set(settingAskFollow, strconv.FormatBool(follow)); err != nil {
				t.Fatal(err)
			}
			d := &fakeDaemon{panes: a.panesd.(*fakePanes)}
			a.launcher = d
			page, focus := a.r.Page, a.r.FocusID()
			press(t, a, "g", "q")
			typeText(t, a, "why")
			altEnter(a)
			if len(d.launches) != 1 {
				t.Fatalf("launches %d; notices %q", len(d.launches), a.m.Notices)
			}
			row := pane{Session: d.launches[0].Session, Pane: daemon.Pane{ID: d.launches[0].ID}}.rowID()
			stayed := a.r.Page == page && a.r.FocusID() == focus
			if !follow {
				if !stayed || a.r.Instances[row] == nil {
					t.Fatalf("stay: page %q focus %q (before %q %q), row %s listed %v", a.r.Page, a.r.FocusID(), page, focus, row, a.r.Instances[row] != nil)
				}
				if !slices.ContainsFunc(a.m.Notices, func(n string) bool { return strings.Contains(n, "Agent started in pane") }) {
					t.Fatalf("no notice names the pane: %q", a.m.Notices)
				}
				return
			}
			if a.r.Page != panesPage || a.r.FocusID() == focus {
				t.Fatalf("follow: page %q focus %q, want the new pane on %s", a.r.Page, a.r.FocusID(), panesPage)
			}
		})
	}
}

// ask.enter-sends on brings back enter sending the form once the
// question has text (here from the harness field).
func TestAskEnterSendsSettingOn(t *testing.T) {
	t.Parallel()
	a := open(t)
	if err := a.r.Settings().Set(settingAskEnterSends, "true"); err != nil {
		t.Fatal(err)
	}
	d := &fakeDaemon{panes: a.panesd.(*fakePanes)}
	a.launcher = d
	press(t, a, "g", "q")
	typeText(t, a, "why")
	press(t, a, "tab")
	_, cmd := a.Update(keyMsg("enter"))
	settle(a, cmd)
	if len(d.launches) != 1 {
		t.Fatalf("enter with ask.enter-sends on: launches %d", len(d.launches))
	}
}
