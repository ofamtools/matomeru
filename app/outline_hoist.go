package app

import (
	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/tree"
)

const cleanSlatePlaceholderTitle = "Untitled"

func (a *App) isCleanSlateDraft(kind, id string) bool {
	if a.trees == nil || id == "" || a.trees.cleanSlateRootID == "" {
		return false
	}
	if a.trees.cleanSlateRootID != id {
		return false
	}
	return a.trees.cleanSlateKind == "" || kind == "" || a.trees.cleanSlateKind == kind
}

func (a *App) clearCleanSlateDraft() {
	if a.trees == nil {
		return
	}
	a.trees.cleanSlateRootID = ""
	a.trees.cleanSlateKind = ""
}

func (a *App) treeRecordHasChildren(kind, id string) bool {
	if a.Store == nil || kind == "" || id == "" {
		return false
	}
	nodes, err := a.recordTreeNodes(kind)
	if err != nil {
		return false
	}
	kids, ok := findTreeChildren(nodes, id)
	return ok && len(kids) > 0
}

// cleanupCleanSlateDraft archives an untouched clean-slate root placeholder
// (zero children) so cancelling or submitting empty leaves zero stray records
// while never altering any prior outlines.
func (a *App) cleanupCleanSlateDraft(tm *tree.Model, kind, treeID string) bool {
	if a.trees == nil || a.trees.cleanSlateRootID == "" {
		return false
	}
	draftID := a.trees.cleanSlateRootID
	draftKind := a.trees.cleanSlateKind
	if draftKind == "" {
		draftKind = kind
	}
	if draftKind == "" {
		draftKind = data.KindTask
	}
	if kind != "" && draftKind != kind {
		return false
	}
	a.clearCleanSlateDraft()

	ref := core.Ref{Kind: draftKind, ID: draftID}
	rec, err := a.Store.Get(ref)
	if err != nil || rec == nil || rec.IsArchived() {
		return false
	}
	if a.treeRecordHasChildren(draftKind, draftID) {
		return false
	}
	if _, _, err := a.Store.Archive(ref, User); err != nil {
		return false
	}
	if tm != nil {
		if tm.HoistID() == draftID {
			tm.Unhoist()
		}
		a.syncTreeModel(tm, draftKind, treeID)
	}
	return true
}

// outlineCleanSlate opens an instant zero-noise canvas: creates a fresh
// top-level root record, hoists visually into it without modifying or archiving
// any prior outlines, and enters inline rename with a cleared buffer ready to
// type immediately.
func (a *App) outlineCleanSlate(s core.State, _ core.Command) core.StepResult {
	// If already in an inline edit, close it cleanly first.
	if a.trees.editing() {
		a.runOutlineEditCancel()
	}

	var (
		tm         *tree.Model
		kind       = data.KindTask
		treeID     = outlineTree
		isWork     = false
		openModal  = false
		returnMode = ModeOutline
	)
	switch {
	case a.Modal() == "outline":
		tm = a.trees.outlineModel()
	case a.inWorkTree(s):
		kind = a.workKind()
		tm = a.trees.workModel(kind)
		treeID = workTable
		isWork = true
		returnMode = ModeNormal
	default:
		tm = a.trees.outlineModel()
		openModal = true
	}

	schema := a.schema(kind)
	field := summaryField(schema)
	created, _, err := a.Store.Create(data.CreateInput{
		Kind:   kind,
		Fields: map[string]any{field: cleanSlatePlaceholderTitle},
	}, User)
	if err != nil {
		a.fail("clean slate", err)
		return core.Next(s)
	}

	a.trees.cleanSlateRootID = created.ID
	a.trees.cleanSlateKind = kind

	a.syncTreeModel(tm, kind, treeID)
	tm.Hoist(created.ID)
	a.trees.folded[kind] = tm.Folded

	prefix := a.defaultEditPrefix(schema)
	if row, has := tm.Focused(); has && row.Node.Icon != "" {
		prefix = row.Node.Icon + " "
	}
	if !tm.StartRename(prefix) {
		a.cleanupCleanSlateDraft(tm, kind, treeID)
		a.say("could not start clean-slate outline")
		return core.Next(s)
	}
	tm.HandleEditCommand(core.Command{ID: input.CmdLineStart})
	tm.HandleEditCommand(core.Command{ID: input.CmdKillLineForward})

	a.trees.editMeta = &treeEditMeta{
		kind:       kind,
		returnMode: returnMode,
		isWorkTree: isWork,
	}
	if isWork {
		a.syncWorkTreeToTable(kind, tm)
	}
	a.say("clean slate · type title · enter adds first bullet · esc saves · ctrl+c discards")

	if openModal {
		return core.Next(a.pushModal(s, a.newOutlineModal()))
	}
	return core.Next(withMode(s, ModeOutlineEdit))
}
