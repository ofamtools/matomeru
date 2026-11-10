//go:build monogatari

package main

import "ofam.dev/monogatari"

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
