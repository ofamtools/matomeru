package main

import "ofam.dev/ochazuke/namecheck"

// mtmRemovedVerbs are mtm's own verbs that its R1 adoption (ochazuke
// v0.92.0, DESIGN-001) purged with no alias, each with what to write
// instead. The runtime adds them to ochazuke's removed list
// (Runtime.AddRemovedVerbs), so the palette, the CLI, bindings check,
// actions check and the verb check name the replacement.
var mtmRemovedVerbs = []namecheck.Removed{
	purged("dirs", "list", "dirs.refresh"),
	purged("extension", "list", "extension.all"),
	purged("harness", "list", "harness.all"),
	purged("run", "list", "run.all"),
	purged("setup", "list", "setup.all"),
	purged("side", "list", "side.all"),
	purged("store-table", "list", "store-table.all"),
	purged("quick-note", "list", "quick-note.open-notes"),
	purged("outline-project", "delete", "outline-project.delete-project"),
	purged("outline-root", "delete", "outline-root.delete-project"),
	purged("project-dir", "edit", "project-dir.retarget"),
	purged("pane", "rename", `edit {field: "@label"} (r), or pane set ID @label=NAME`),
	purged("session", "rename", `edit {field: "@label"} (r), or session set ID @label=NAME`),
	purged("saved", "rename", `edit {field: "@label"} (r), or saved set ID @label=NAME`),
	{Noun: "projects", Verb: "list", Replacement: "projects.all",
		Since: "mtm ochazuke v0.93.0 bump", Note: "the alias kept for extension catalogs; none names it any more"},
}

// purged is one row of mtmRemovedVerbs: removed in mtm's R1 adoption.
func purged(noun, verb, replacement string) namecheck.Removed {
	return namecheck.Removed{Noun: noun, Verb: verb, Replacement: replacement,
		Since: "mtm R1 (ochazuke v0.92.0)", Note: "DESIGN-001: purged with no alias"}
}
