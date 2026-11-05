//go:build monogatari

package main

import (
	"ofam.dev/matomeru/internal/work"
	"ofam.dev/monogatari"
)

func buildScheduleReferences(env monogatari.Env) (monogatari.Built, error) {
	built, err := buildEmpty(env)
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
