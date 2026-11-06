package main

import (
	"slices"

	"ofam.dev/ochazuke"
)

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

// commandVerbRenames are mtm's command-noun verbs (nouns with no store)
// that were named like a core verb, renamed in the R1 adoption so a word
// after a noun means one thing (Runtime.ValidateNouns). The old name stays
// one release as a deprecated alias (Keep actions); projects.list stays
// for good, since extension catalogs name it as a parameter source.
var commandVerbRenames = []struct {
	old, now string
	lasting  bool
}{
	{"dirs.list", "dirs.refresh", false},
	{"extension.list", "extension.all", false},
	{"harness.list", "harness.all", false},
	{"projects.list", "projects.all", true},
	{"quick-note.list", "quick-note.open-notes", false},
	{"run.list", "run.all", false},
	{"setup.list", "setup.all", false},
	{"side.list", "side.all", false},
	{"store-table.list", "store-table.all", false},
	{"outline-project.delete", "outline-project.delete-project", false},
	{"outline-root.delete", "outline-root.delete-project", false},
	{"project-dir.edit", "project-dir.retarget", false},
}

// commandRenameSince dates the renames; the deprecated names go after the
// next release.
const commandRenameSince = "mtm R1 adoption (2026-11-06)"

// defineCommandRenames adds the old names as aliases of the new.
func (a *App) defineCommandRenames() {
	for _, row := range commandVerbRenames {
		alias := ochazuke.ActionAlias{Alias: row.old, Action: row.now,
			Note: "renamed so a command verb is never named like a core verb (create, get, set, delete, archive, list, edit)"}
		if !row.lasting {
			alias.Since, alias.RemoveAfter = commandRenameSince, "the next mtm release"
		}
		if err := a.r.AddActionAlias(alias); err != nil {
			panic(err)
		}
	}
}
