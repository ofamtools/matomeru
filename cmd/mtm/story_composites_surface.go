//go:build monogatari

package main

import (
	"ofam.dev/matomeru/internal/history"
	"ofam.dev/matomeru/internal/work"
	"ofam.dev/monogatari"
	"ofam.dev/ochazuke"
)

func areaFixturesSurface() []monogatari.Fixture {
	return []monogatari.Fixture{
		{Name: "area-panes", Summary: "Seeded shells followed through editing, live input, copying and closure", Build: buildSeeded},
		{Name: "area-schedules", Summary: "Readable project references and a reminder spanning local midnight", Build: buildAreaSchedules},
		{Name: "area-reviews", Summary: "An agent's running subagents and a completed, unreviewed checkout turn", Build: buildAreaReviews},
		{Name: "area-nouns", Summary: "Generated indexes and mounted peer, text and row results", Build: buildAreaNouns},
		{Name: "area-pickers", Summary: "Pane, project and unassociated context alongside a journaled prompt", Build: buildContextPicker},
	}
}

func buildAreaSchedules(env monogatari.Env) (monogatari.Built, error) {
	built, err := buildReminderRollover(env)
	if err != nil {
		return built, err
	}
	a := built.Program.(*App)
	for _, project := range []work.Project{
		{ID: "6f7a9d1c0b2245f6a8e30d4c5b219076", Name: "alpha"},
		{ID: "a9e25f04781c46bda30f6d529b8170ce", Name: "beta"},
	} {
		if err := a.work.PutProject(project); err != nil {
			built.Close()
			return monogatari.Built{}, err
		}
	}
	if err := a.refreshProjects(); err != nil {
		built.Close()
		return monogatari.Built{}, err
	}
	return built, nil
}

func buildAreaReviews(env monogatari.Env) (monogatari.Built, error) {
	built, err := buildSeeded(env)
	if err != nil {
		return built, err
	}
	a := built.Program.(*App)
	if err := a.work.SetPaneOwner(work.Assignment{Window: "w1", Session: "acme-web", ProjectID: "acme-web", TaskID: "t1"}); err != nil {
		built.Close()
		return monogatari.Built{}, err
	}
	t := history.Turn{ID: "checkout-review", Session: "acme-web", Window: "w1", Harness: "claude-code", ConversationID: "checkout", Prompt: "Review checkout", StartedAt: 1}
	if _, err = a.hist.Capture(t); err == nil {
		_, err = a.hist.Complete(t, "Shipping estimate is ready", 2)
	}
	if err == nil {
		_, err = a.work.EnsureReview(work.Review{ID: "checkout-review-record", Session: "acme-web", Window: "w1", Pane: "acme-web/claude", ProjectID: "acme-web", TaskID: "t1", Harness: "claude-code", Status: work.ReviewPending, Created: 1})
	}
	if err != nil {
		built.Close()
		return monogatari.Built{}, err
	}
	return built, nil
}

func buildAreaNouns(env monogatari.Env) (monogatari.Built, error) {
	built, err := buildPeerFixture(env, false, `{"schema":1,"name":"o-alpha","nouns":[{"name":"item","verbs":[{"name":"list","kind":"application","rows":"item"},{"name":"output","kind":"application","result":"text"},{"name":"history","kind":"application","result":"rows"},{"name":"delete","kind":"application","confirm":"Remove machine {{name}}?","params":{"name":{"type":"string"}}}]}],"bindings":[{"noun":"item","mode":"normal","keys":"enter","request":{"noun":"item","verb":"output"}},{"noun":"item","mode":"normal","keys":"H","request":{"noun":"item","verb":"history"}},{"noun":"item","mode":"normal","keys":"d d","request":{"noun":"item","verb":"delete"},"from":{"name":"row:name"}}],"peerBindings":[{"noun":"alpha.item","mode":"normal","keys":"g s","request":{"noun":"beta.doc","verb":"send"},"from":{"target":"row:doc","pane":"row:pane"}}]}`)
	if err != nil {
		return built, err
	}
	a := built.Program.(*App)
	_, err = a.r.Execute(ochazuke.Request{Noun: relationNoun, Verb: "create", Args: ochazuke.Arguments{
		"kind": "links", "from": "project:acme-web", "to": "project:billing-api", "text": "scaffold relation",
	}})
	if err == nil {
		err = a.work.PutNote(work.Note{ID: "surface-row-note", Text: "Source row needs a rerun", About: ochazuke.AnyRef("alpha.item", "i1"), Created: 20, Updated: 20})
	}
	if err == nil {
		err = a.r.EnsureNounsPage()
	}
	if err != nil {
		built.Close()
		return monogatari.Built{}, err
	}
	a.m.View()
	return built, nil
}
