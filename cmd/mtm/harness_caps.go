package main

import "ofam.dev/matomeru/internal/harness"

// A pane's harness capabilities (where its turns and settings come from,
// whether it has subagents, its completions) are fields of its harness's
// row, never a test of the harness's name: a user's harness named
// claude-work that gives claude's fields (or "extends": "claude") is
// Claude Code everywhere.

// paneHarness is the harness row a pane's capabilities come from: the row
// whose ids cover the harness id tuios reports (the row mtm launched the
// pane with first), else, while tuios reports none, the row mtm launched
// it with.
func paneHarness(reg *harness.Registry, p pane) (harness.Harness, bool) {
	if reg == nil {
		return harness.Harness{}, false
	}
	if p.Harness != "" {
		return reg.ForAgent(p.Harness, p.LaunchHarness)
	}
	return reg.Get(p.LaunchHarness)
}

// paneLog is the session log format of the pane's harness ("" none).
func paneLog(reg *harness.Registry, p pane) string {
	h, _ := paneHarness(reg, p)
	return h.Log
}

// logFormat is what mtm reads a session log format for.
type logFormat struct {
	// turns is the Turns tab's source for the format (turnsClaude,
	// turnsOmp); turnsJournal when its log gives no turns.
	turns int
	// from names the log on the settings line.
	from string
	// rollout: the settings line reads the log itself (pane.read-settings
	// on each listing), as the Turns tab does not.
	rollout bool
}

// logFormats are the formats mtm has readers for (harness.Harness.Log).
var logFormats = map[string]logFormat{
	harness.LogClaude: {turns: turnsClaude, from: "Claude transcript"},
	harness.LogOmp:    {turns: turnsOmp, from: "omp session log"},
	harness.LogCodex:  {turns: turnsJournal, from: "codex rollout", rollout: true},
}

// logFormatOf is the pane's log format; the zero format (journal turns,
// no settings log) when its harness names none mtm reads.
func (a *App) logFormatOf(p pane) logFormat {
	return logFormats[paneLog(a.harnesses, p)]
}
