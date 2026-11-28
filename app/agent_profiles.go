package app

import (
	agentcfg "ofam.dev/matomeru/internal/config"
)

// AgentCatalogs returns the standard agent profiles and prompt segments: the
// built-ins overlaid by agent_profiles.json and prompt_segments.json in the
// config directory. The files are read on each call so an edit applies to
// the next dispatch; a file that fails to parse keeps its built-ins and adds
// a warning.
func (a *App) AgentCatalogs() agentcfg.AgentCatalogs {
	cats, err := agentcfg.LoadAgentCatalogs(a.opts.ConfigDir)
	if err != nil {
		a.warn("agent catalogs: %v", err)
	}
	return cats
}

// ComposeDispatch resolves a prompt segment plus tag tweaks over rawPrompt to
// the profile, instructions and final prompt an agent should be spawned with.
// Queues, the refiner and subagent role pickers share it.
func (a *App) ComposeDispatch(segmentID string, tagIDs []string, rawPrompt string) (agentcfg.ComposedDispatch, error) {
	return a.AgentCatalogs().Compose(segmentID, tagIDs, rawPrompt)
}
