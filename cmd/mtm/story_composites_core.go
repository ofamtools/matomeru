//go:build monogatari

package main

import (
	"ofam.dev/monogatari"
)

func areaFixturesCore() []monogatari.Fixture {
	return []monogatari.Fixture{
		{Name: "area-work", Summary: "Nested project tasks for the Work and outline walkthrough", Build: buildNested},
		{Name: "area-context", Summary: "An empty curated Context page, populated through the walkthrough", Build: buildEmpty},
		{Name: "area-items", Summary: "An empty inbox with sandbox panes and an established notification baseline", Build: buildInboxNotice},
		{Name: "area-notes", Summary: "Project and task reference notes, plus panes for quick notes", Build: buildReference},
		{Name: "area-projects", Summary: "A project directory and configured bases sharing the story file tree", Build: buildAreaProjects},
	}
}

func buildAreaProjects(env monogatari.Env) (monogatari.Built, error) {
	built, err := buildProjectTree(env)
	if err != nil || env.FilesDir == "" {
		return built, err
	}
	a := built.Program.(*App)
	// The same tree is acme-web's directory and the base for other projects.
	_, err = a.changeBase(env.FilesDir, 1, true)
	if err != nil {
		built.Close()
		return monogatari.Built{}, err
	}
	return built, nil
}
