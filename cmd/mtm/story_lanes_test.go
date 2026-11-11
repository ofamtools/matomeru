//go:build monogatari

package main

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"ofam.dev/monogatari"
	"ofam.dev/ochazuke"
	ocli "ofam.dev/ochazuke/cli"
)

func TestLaneDocNotesWaitsForRowAfterChoices(t *testing.T) {
	t.Parallel()
	story, err := monogatari.Parse([]byte(`{
		"name": "lane doc notes waits for page rows",
		"fixture": "lane-doc-notes",
		"width": 140,
		"height": 36,
		"script": [
			":", {"type": "page open ext/lane"}, "enter",
			{"job": "catalog-load"}, {"job": "catalog-load"},
			{"job": "catalog-load"}, {"job": "catalog-load"},
			{"job": "catalog-load"}, {"job": "catalog-load"},
			{"job": "catalog-load"}, "g", "s", {"job": "md.doc.send-notes"}
		],
		"expect": [{
			"contains": ["Implementation", "doc send-notes --target /docs/review.md", "--json {\\\"pane\\\":\\\"w1\\\"}"],
			"lacks": ["name a doc", "no such verb"]
		}]
	}`), "lane-doc-notes-inline")
	if err != nil {
		t.Fatal(err)
	}
	app := monogatari.App{Name: "mtm", Fixtures: []monogatari.Fixture{{Name: "lane-doc-notes", Build: buildLaneDocNotes}}, Expected: storyExpected}
	ss, err := (monogatari.Library{Apps: []monogatari.App{app}}).Build(story, monogatari.Options{Mono: true})
	if err != nil {
		t.Fatal(err)
	}
	loads := &storyRowsAfterChoices{Model: ss.Program, runtime: ss.R, source: "lane.page-rows", ready: make(chan struct{}), stop: make(chan struct{})}
	ss.Program = loads
	driver := monogatari.NewDriver(ss, monogatari.Options{Mono: true})
	defer driver.Close()
	defer close(loads.stop)
	if err := driver.Play(story.Script, nil); err != nil {
		t.Fatalf("%v\ntrace: %+v\njobs: %+v\n%s", err, driver.Trace, ss.R.Jobs(), ss.Frame())
	}
	for _, expected := range story.Expect {
		if failures := monogatari.Check(ss, expected); len(failures) != 0 {
			t.Fatal(failures)
		}
	}
}

// Deliver every chooser before the page rows, independent of subprocess timing.
// The story must wait for the row itself before pressing its peer binding.
type storyRowsAfterChoices struct {
	tea.Model
	runtime *ochazuke.Runtime
	source  string
	row     uint64
	choices map[uint64]bool
	ready   chan struct{}
	stop    chan struct{}
}

func (m *storyRowsAfterChoices) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.Model.Update(msg)
	m.Model = model
	if m.row == 0 {
		m.choices = map[uint64]bool{}
		for _, job := range m.runtime.Jobs() {
			if job.Verb != ocli.LoadVerb || job.Status != ochazuke.JobRunning {
				continue
			}
			if job.Label == m.source {
				m.row = job.Job
			} else {
				m.choices[job.Job] = true
			}
		}
	}
	if done, ok := msg.(ochazuke.Completion); ok && m.choices[done.Job] {
		delete(m.choices, done.Job)
		if len(m.choices) == 0 {
			close(m.ready)
		}
	}
	return m, m.wrap(cmd, m.row)
}

func (m *storyRowsAfterChoices) wrap(cmd tea.Cmd, row uint64) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			wrapped := make(tea.BatchMsg, len(batch))
			for i, child := range batch {
				wrapped[i] = m.wrap(child, row)
			}
			return wrapped
		}
		if done, ok := msg.(ochazuke.Completion); ok && done.Job == row {
			select {
			case <-m.ready:
			case <-m.stop:
			}
		}
		return msg
	}
}
