package main

import "slices"

// coreVerbAs are the core verbs (DESIGN-001: every thing noun has create,
// get, set, delete, archive, list and edit) whose work is a named verb mtm
// already has: a pane is created by opening a shell and deleted by
// killing its window (confirmed), a session is created by opening a shell
// in the project's session and deleted by killing it (confirmed), a vim
// session is created by opening Neovim, a review by launching an agent
// (each launch records one). The named verbs stay (Keep actions); the core
// verb runs the same handler with the same parameters and confirm.
var coreVerbAs = []struct{ noun, verb, from string }{
	{"pane", "create", "pane.new"},
	{"pane", "delete", "pane.kill"},
	{"session", "create", "pane.new"},
	{"session", "delete", "session.kill"},
	{"vim", "create", "vim.open"},
	{"review", "create", "agent.launch"},
}

// defineCoreVerbs registers each coreVerbAs row as the noun's own verb,
// replacing the generated core verb its store cannot run.
func (a *App) defineCoreVerbs() {
	for _, row := range coreVerbAs {
		src, ok := a.r.Operations[a.r.CanonicalKey(row.from)]
		if !ok {
			panic("core verb " + row.noun + "." + row.verb + ": no " + row.from)
		}
		op := src
		op.Noun, op.Verb, op.Standard, op.Unavailable = row.noun, row.verb, "", ""
		op.Description = src.Description + " (as " + row.from + ")"
		if op.App != nil {
			op.Run = nil
		}
		a.r.Register(op)
		if n := a.r.Noun(row.noun); n != nil && !slices.Contains(n.Verbs, row.verb) {
			a.r.Define(row.noun).Verbs(row.verb)
		}
	}
}

// The command verbs renamed in the R1 adoption (so no command verb is
// named like a core verb) are purged with no alias (DESIGN-001, owner
// decision): removed_verbs.go lists each with its replacement.
// projects.list, kept as an alias of projects.all while extension
// catalogs named it as a parameter source, is purged too (ochazuke
// v0.93.0 bump: no catalog names it any more).
func (a *App) defineCommandRenames() {
	if err := a.r.AddRemovedVerbs(mtmRemovedVerbs...); err != nil {
		panic(err)
	}
}
