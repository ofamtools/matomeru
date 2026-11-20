package main

import (
	_ "embed"
	"encoding/json"

	"ofam.dev/ochazuke"
)

// The outline's rows are tasks and projects (owner tasks 32e009b9 and
// abb20f4f: "there should be one noun for outline and then all of the
// other nouns that could be displayed within that outline are already
// nouns"). A row's verbs are its noun's: x on a task row is task.toggle,
// d d on a project row project.delete, from keys, the palette and the CLI
// alike (the row is the focused thing of its noun; its key is in the row's
// Props). The outline keeps its own keys first (Tree.RowKeysLast): its
// rows of its own (o, i, d d, tab, J, K, u, ...) and the rows below answer
// before the row's noun, which answers the keys nobody on the way binds
// (tree.row-keys). The keys only the outline has are rows of the outline's
// modal (tasks/normal and tasks/visual-line in bindings/rows.json), each
// guarded on its kind of row (outlineRowGuards) and a capture row, so it
// answers before the outline's own row on the same key (tab indents,
// shift+tab outdents, o adds beside) and before the row's noun. On
// ochazuke.outline, which every tree shares, they would move aside other
// trees' generated rows on the same keys. Writes through the rows'
// verbs are steps of the outline's undo (Tree.RecordWrites).

// outlineNamesSince is when the outline's own nouns became their things'.
const outlineNamesSince = "mtm v0.18.0 (2026-11-19)"

// outlineRenames are the outline's old nouns, each now the noun of what its
// rows draw, with the verbs whose names changed (the others keep theirs on
// the new noun): outline_renames.json. Kept as aliases for one release.
//
//go:embed outline_renames.json
var outlineRenames []byte

// renameOutlineNouns registers outlineRenames.
func (a *App) renameOutlineNouns() {
	var renames []ochazuke.NounRename
	if err := json.Unmarshal(outlineRenames, &renames); err != nil {
		panic(err)
	}
	for _, n := range renames {
		n.Since, n.RemoveAfter = outlineNamesSince, oldNamesRemoveAfter
		if err := a.r.RenameNoun(n); err != nil {
			panic(err)
		}
	}
}

// The outline's row guards: which kind of row the focus is on.
const (
	outlineTaskRow    = "outline-task-row"
	outlineProjectRow = "outline-project-row" // a project of the all view, or the root of a project's outline
	outlineRootRow    = "outline-root-row"    // the one project of a project's outline
	outlineNoneRow    = "outline-none-row"
)

// outlineRowGuards are the row guards, each a check on the open outline's
// row at the focus: its node, and whether the outline shows every project.
var outlineRowGuards = map[string]func(n ochazuke.TreeNode, all bool) bool{
	outlineTaskRow:    func(n ochazuke.TreeNode, _ bool) bool { return n.Parent != "" },
	outlineProjectRow: func(n ochazuke.TreeNode, _ bool) bool { return n.Parent == "" && n.ID != projectNode("") },
	outlineRootRow:    func(n ochazuke.TreeNode, all bool) bool { return n.Parent == "" && !all && n.ID != projectNode("") },
	outlineNoneRow:    func(n ochazuke.TreeNode, _ bool) bool { return n.ID == projectNode("") },
}

// defineOutlineRowGuards registers outlineRowGuards.
func (a *App) defineOutlineRowGuards() {
	for name, holds := range outlineRowGuards {
		a.r.DefineGuard(name, func(r *ochazuke.Runtime, ev ochazuke.KeyEvent) bool {
			o := a.outline
			if o == nil || r.Tree(outlineTree) != o.tree {
				return false
			}
			for _, n := range o.tree.Nodes() {
				if n.ID == ev.Focus {
					return holds(n, o.scope.all)
				}
			}
			return false
		})
	}
}

// outlineUnoffered are the verbs the outline keeps off its keys
// (Runtime.Unoffer): the list's collection.apply, which would run the rows'
// own delete over a selection where the outline deletes its rows (d). The
// palette and the CLI still run it.
var outlineUnoffered = []string{"ochazuke.collection.apply"}
