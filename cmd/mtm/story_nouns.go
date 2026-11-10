//go:build monogatari

package main

import (
	"ofam.dev/monogatari"
	"ofam.dev/ochazuke"
)

// buildNouns is peer-rows (two sandbox extensions, alpha with an item noun,
// beta with a doc noun) with the Nouns page registered at the end, as
// ochazuke's Model.Init registers it for a running program (a story does not
// run Init): the page is the last tab, after the mounted extensions' pages.
func buildNouns(env monogatari.Env) (monogatari.Built, error) {
	built, err := buildPeerRows(env)
	if err != nil {
		return built, err
	}
	a := built.Program.(*App)
	if err := a.r.EnsureNounsPage(); err != nil {
		built.Close()
		return monogatari.Built{}, err
	}
	a.m.View()
	return built, nil
}

func buildScaffold(env monogatari.Env) (monogatari.Built, error) {
	panes := append([]storyPane(nil), seededPanes...)
	for i := range panes {
		panes[i].SessionID = "id-" + panes[i].Session
	}
	built, err := buildStory(env, writeSeeded, panes)
	if err != nil {
		return built, err
	}
	a := built.Program.(*App)
	_, err = a.r.Execute(ochazuke.Request{Noun: relationNoun, Verb: "create", Args: ochazuke.Arguments{
		"kind": "links", "from": "project:acme-web", "to": "project:billing-api", "text": "scaffold relation",
	}})
	if err != nil {
		built.Close()
		return monogatari.Built{}, err
	}
	return built, nil
}
