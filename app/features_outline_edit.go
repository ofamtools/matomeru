package app

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/toast"
	"ofam.dev/ochazuke/widgets/tree"
)

func (a *App) resyncActiveFeaturesOutline() *tree.Model {
	snap := a.collectFeaturesSnapshot()
	activeKey := a.featuresCtrl().activeItemKey()
	detail := a.buildFeaturesDetailBody(snap, activeKey)
	return a.syncFeaturesOutlineTree(snap, activeKey, detail)
}

func (a *App) featuresOutlineTreeMove(c core.Command) {
	tm := a.resyncActiveFeaturesOutline()
	tm.HandleCommand(c)
}

func (a *App) featuresOutlineStartEdit(atEnd bool) bool {
	tm := a.resyncActiveFeaturesOutline()
	row, ok := tm.Focused()
	if !ok || row.Draft {
		a.say("no item selected")
		return false
	}
	fo := a.trees.featuresOutlineState()
	meta := fo.meta[row.ID]
	if meta.isSection {
		tm.ToggleFold()
		return false
	}
	if !meta.isTask && !meta.isConcept {
		return a.featuresOutlineStartAdd(false)
	}
	prefix := ""
	if row.Node.Icon != "" {
		prefix = row.Node.Icon + " "
	}
	if !tm.StartRename(prefix) {
		return false
	}
	if !atEnd {
		tm.HandleEditCommand(core.Command{ID: "input.line.start"})
	}
	kind := data.KindTask
	if meta.isConcept {
		kind = data.KindConcept
	}
	a.trees.editMeta = &treeEditMeta{kind: kind, returnMode: ModeNormal, treeID: featuresOutlineTree}
	a.say("inline edit · enter saves & adds · esc saves · ctrl+c discards")
	return true
}

func (a *App) featuresOutlineStartAdd(above bool) bool {
	tm := a.resyncActiveFeaturesOutline()
	editKind := tree.EditAddBelow
	if above {
		editKind = tree.EditAddAbove
	}
	if row, ok := tm.Focused(); ok && !row.Draft {
		if a.trees.featuresOutlineState().meta[row.ID].isSection {
			editKind = tree.EditAddChild
		}
	}
	tm.StartAdd(editKind, a.defaultEditPrefix(a.schema(data.KindTask)))
	a.trees.editMeta = &treeEditMeta{kind: data.KindTask, returnMode: ModeNormal, treeID: featuresOutlineTree}
	a.say("new idea · tab indents · shift+tab outdents · enter saves & adds · esc saves")
	return true
}

func (a *App) featuresOutlineAddCmd(s core.State, _ core.Command) core.StepResult {
	if !a.featuresPageActive() {
		a.say("Features is inactive")
		return core.Next(s)
	}
	tm := a.resyncActiveFeaturesOutline()
	if _, ok := tm.Focused(); !ok || !a.inFeaturesOutlineTree(&s) {
		tm.Select(featSecIdeas)
	}
	a.featuresOutlineStartAdd(false)
	return core.Next(withPage(s, SpaceFeatures, featuresOutlineTree, ModeOutlineEdit))
}

func (a *App) featuresOutlineCleanSlate(s core.State) core.StepResult {
	fo := a.trees.featuresOutlineState()
	fo.cleanSlate = cleanSlateState{active: true, roots: map[string]bool{}}
	tm := a.resyncActiveFeaturesOutline()
	tm.ClearHoist()
	tm.StartAdd(tree.EditAddBelow, a.defaultEditPrefix(a.schema(data.KindTask)))
	a.trees.editMeta = &treeEditMeta{kind: data.KindTask, returnMode: ModeNormal, treeID: featuresOutlineTree}
	a.say("clean slate · type idea · enter adds next · tab indents · esc exits edit")
	return core.Next(withPage(s, SpaceFeatures, featuresOutlineTree, ModeOutlineEdit))
}

func (a *App) commitFeaturesOutlineEdit() (string, bool) {
	fo := a.trees.featuresOutlineState()
	tm := fo.model
	meta := a.trees.editMeta
	ret := ModeNormal
	if meta != nil && meta.returnMode != "" {
		ret = meta.returnMode
	}
	res, committed := tm.CommitEdit()
	a.trees.editMeta = nil
	if !committed {
		return ret, true
	}
	title := strings.TrimSpace(res.Value)
	if res.Kind == tree.EditRename {
		return a.commitFeaturesOutlineRename(res, meta, title, ret)
	}
	if title == "" {
		if fo.cleanSlate.active && len(fo.cleanSlate.roots) == 0 {
			fo.cleanSlate = cleanSlateState{}
			a.resyncActiveFeaturesOutline()
			a.say("discarded empty clean-slate outline")
		}
		return ret, true
	}
	return a.commitFeaturesOutlineCreate(res, title, ret)
}

func (a *App) commitFeaturesOutlineRename(res tree.EditResult, meta *treeEditMeta, title, ret string) (string, bool) {
	if title == "" {
		a.say("title cannot be empty")
		return ret, true
	}
	kind := data.KindTask
	if meta != nil && meta.kind != "" {
		kind = meta.kind
	}
	ref := core.Ref{Kind: kind, ID: res.TargetID}
	_, cs, err := a.Store.Patch(data.PatchInput{
		Ref:     ref,
		Fields:  map[string]any{summaryField(a.schema(kind)): title},
		Summary: "rename " + title,
	}, User)
	a.resyncActiveFeaturesOutline()
	a.wrote("renamed to "+title, cs, err)
	return ret, true
}

func isPropSectionParent(parentID string) bool {
	return parentID == featSecProps || strings.HasPrefix(parentID, featPropNounIDPrefix)
}

func (a *App) commitFeaturesOutlineCreate(res tree.EditResult, title, ret string) (string, bool) {
	fo := a.trees.featuresOutlineState()
	activeKey := a.featuresCtrl().activeItemKey()
	parentMeta := fo.meta[res.ParentID]
	fk, _ := parseFeatureKey(activeKey)
	switch {
	case res.ParentID == featSecConcepts || parentMeta.isConcept:
		rec, err := a.createFeaturesInlineConcept(activeKey, "", "", title)
		return a.finishFeaturesOutlineCreate(rec, title, err, false, ret)
	case res.ParentID == featSecViews:
		rec, err := a.createFeaturesInlineConcept(activeKey, data.ConceptKindView, fk.Name, title)
		return a.finishFeaturesOutlineCreate(rec, title, err, false, ret)
	case isPropSectionParent(res.ParentID):
		target := firstNonEmpty(parentMeta.nounKind, fk.Name)
		rec, err := a.createFeaturesInlineConcept(activeKey, data.ConceptKindProp, target, title)
		return a.finishFeaturesOutlineCreate(rec, title, err, false, ret)
	case parentMeta.isTask:
		rec, err := a.createFeaturesSubtaskIdea(activeKey, res.ParentID, title)
		return a.finishFeaturesOutlineCreate(rec, title, err, false, ret)
	default:
		featureNodeID := ""
		if res.ParentID != "" && res.ParentID != featSecIdeas {
			featureNodeID = res.ParentID
		}
		rec, err := a.createFeaturesWishlistIdea(activeKey, featureNodeID, title)
		addRoot := fo.cleanSlate.active && res.ParentID == ""
		return a.finishFeaturesOutlineCreate(rec, title, err, addRoot, ret)
	}
}

func (a *App) finishFeaturesOutlineCreate(rec *data.Record, title string, err error, addCSRoot bool, ret string) (string, bool) {
	if err != nil || rec == nil {
		if err != nil {
			a.fail("capture idea", err)
		}
		return ret, true
	}
	fo := a.trees.featuresOutlineState()
	if addCSRoot {
		if fo.cleanSlate.roots == nil {
			fo.cleanSlate.roots = map[string]bool{}
		}
		fo.cleanSlate.roots[rec.ID] = true
	}
	tm := a.resyncActiveFeaturesOutline()
	tm.Reveal(rec.ID)
	a.viewMemo.gen++
	a.say("added %s", title)
	return ret, true
}

func (a *App) createFeaturesInlineConcept(activeKey, conceptKind, targetKind, title string) (*data.Record, error) {
	fk, _ := parseFeatureKey(activeKey)
	if conceptKind == "" {
		conceptKind = data.ConceptKindFeature
		if fk.Kind == featureKeyConcept && fk.Name != "" {
			conceptKind = fk.Name
		}
	}
	if targetKind == "" && fk.Kind != featureKeyConcept {
		targetKind = fk.Name
	}
	status := data.ConceptStatusIdea
	if conceptKind == data.ConceptKindProp || conceptKind == data.ConceptKindView {
		status = data.ConceptStatusProposed
	}
	rec, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindConcept,
		Fields: map[string]any{
			"title":                     title,
			data.FieldConceptKind:       conceptKind,
			data.FieldConceptTargetKind: targetKind,
			data.FieldConceptStatus:     status,
		},
	}, User)
	return rec, err
}

func (a *App) createFeaturesSubtaskIdea(activeKey, parentTaskID, title string) (*data.Record, error) {
	fields := map[string]any{
		"title":      title,
		"parent":     core.Ref{Kind: data.KindTask, ID: parentTaskID},
		"workStatus": "active",
	}
	applyFeatureTargetFields(fields, activeKey)
	rec, _, err := a.Store.Create(data.CreateInput{Kind: data.KindTask, Fields: fields}, User)
	return rec, err
}

func (a *App) createFeaturesWishlistIdea(activeKey, featureNodeID, title string) (*data.Record, error) {
	rec, err := a.saveFeaturesRawThought(activeKey, title, nil)
	if err != nil || rec == nil || featureNodeID == "" {
		return rec, err
	}
	_, _, _ = a.Store.Patch(data.PatchInput{
		Ref:     rec.Ref(),
		Fields:  map[string]any{"featureNode": featureNodeID},
		Summary: "link idea to " + featureNodeID,
	}, User)
	return rec, nil
}

func applyFeatureTargetFields(fields map[string]any, activeKey string) {
	fk, _ := parseFeatureKey(activeKey)
	switch fk.Kind {
	case featureKeyPage:
		fields["targetPages"] = []string{fk.Name}
	case featureKeyNoun:
		fields["targetNouns"] = []string{fk.Name}
	case featureKeyComponent:
		fields["targetComponents"] = []string{fk.Name}
	case featureKeyConcept:
		fields["targetConcepts"] = []string{fk.Name}
	}
}

func (a *App) featuresOutlineNewline() (string, bool) {
	fo := a.trees.featuresOutlineState()
	tm := fo.model
	ed := tm.Edit()
	prevParentID, prevDepth := "", 0
	wasEmptyCS := false
	if ed != nil {
		prevParentID, prevDepth = ed.ParentID, ed.Depth
		if ed.Kind != tree.EditRename && strings.TrimSpace(ed.Value()) == "" && fo.cleanSlate.active && len(fo.cleanSlate.roots) == 0 {
			wasEmptyCS = true
		}
	}
	ret, ok := a.commitFeaturesOutlineEdit()
	if !ok || wasEmptyCS {
		return ret, ok
	}
	a.featuresOutlineStartAdd(false)
	if prevParentID != "" {
		if nextEd := tm.Edit(); nextEd != nil && nextEd.ParentID != prevParentID {
			delete(tm.Folded, prevParentID)
			nextEd.ParentID = prevParentID
			nextEd.Depth = prevDepth
			tm.Clamp()
		}
	}
	return ModeOutlineEdit, true
}

func (a *App) featuresOutlineToggleThreadCmd(s core.State, c core.Command) core.StepResult {
	tm := a.resyncActiveFeaturesOutline()
	row, ok := tm.Focused()
	if !ok || row.Draft {
		a.say("no item selected")
		return core.Next(s)
	}
	fo := a.trees.featuresOutlineState()
	meta := fo.meta[row.ID]
	if meta.isTask {
		return a.taskThreadToggle(s, c)
	}
	fo.expandedNotes[row.ID] = !fo.expandedNotes[row.ID]
	delete(tm.Folded, row.ID)
	a.viewMemo.gen++
	if fo.expandedNotes[row.ID] {
		a.say("expanded comments on %s", firstNonEmpty(meta.label, row.ID))
	} else {
		a.say("collapsed comments on %s", firstNonEmpty(meta.label, row.ID))
	}
	return core.Next(s)
}

func (a *App) featuresOutlineCommentCmd(s core.State, c core.Command) core.StepResult {
	tm := a.resyncActiveFeaturesOutline()
	row, ok := tm.Focused()
	if !ok || row.Draft {
		a.say("no item selected to comment on")
		return core.Next(s)
	}
	fo := a.trees.featuresOutlineState()
	meta := fo.meta[row.ID]
	if meta.isTask || meta.isNote {
		return a.taskNoteReply(s, c)
	}
	activeKey := a.featuresCtrl().activeItemKey()
	label := firstNonEmpty(meta.label, row.Node.Label, row.ID)
	targetRef := meta.ref
	cm := newRichComposerModal(a, richComposerSpec{
		ID:                featCommentModalID,
		Scope:             "composer",
		Title:             "Comment · " + clip(label, 52),
		Subtitle:          "Saved as a note on " + label + " (" + activeKey + ")",
		InputTitle:        "Comment / Note",
		Placeholder:       "Type comment or note… (ctrl+s save · esc cancel)",
		DraftKey:          "features.comment:" + row.ID,
		WidthPct:          72,
		HeightPct:         50,
		ShowDeliveryModes: false,
		OnSubmit: func(text string, attachments []data.Attachment, _ ComposerMode) (tea.Cmd, error) {
			return nil, a.saveFeaturesNodeComment(activeKey, row.ID, label, targetRef, text, attachments)
		},
	})
	a.say("commenting on %s · ctrl+s saves · esc closes", clip(label, 40))
	return core.Next(a.pushModal(s, cm))
}

func (a *App) saveFeaturesNodeComment(activeKey, nodeID, label string, targetRef *core.Ref, text string, attachments []data.Attachment) error {
	body := strings.TrimSpace(formatPromptWithAttachments(text, attachments))
	if body == "" {
		return fmt.Errorf("comment is empty")
	}
	rec, _, err := a.Store.Create(data.CreateInput{
		Kind: data.KindNote,
		Fields: map[string]any{
			"title":         "Comment: " + clip(label, 64),
			"body":          body,
			"authorKind":    "owner",
			"featureTarget": activeKey,
			"featureNode":   nodeID,
		},
	}, User)
	if err != nil || rec == nil {
		return err
	}
	if targetRef != nil {
		_, _, _ = a.Store.Relate(data.RelateInput{From: rec.Ref(), Kind: data.RelAbout, To: *targetRef}, User)
	}
	fo := a.trees.featuresOutlineState()
	fo.expandedNotes[nodeID] = true
	delete(fo.model.Folded, nodeID)
	a.afterModal(func(st core.State) core.State {
		a.viewMemo.gen++
		msg := fmt.Sprintf("Commented on %s", clip(label, 42))
		a.say("%s", msg)
		a.pushToast(msg, toast.Ok)
		return st
	})
	return nil
}

func (a *App) saveFeaturesOutlineFolded(tm *tree.Model) {
	if a.trees != nil && a.trees.featuresOutline != nil && a.trees.featuresOutline.activeKey != "" && tm != nil {
		a.trees.featuresOutline.folded[a.trees.featuresOutline.activeKey] = tm.Folded
	}
}

func isScopedOutlineTreeID(treeID string) bool {
	return treeID == projectOutlineTree || treeID == featuresCaptureTree
}

func (a *App) isEditingFeaturesOutline() bool {
	return a != nil && a.trees != nil && a.trees.editMeta != nil && a.trees.editMeta.treeID == featuresOutlineTree
}

func (a *App) cancelFeaturesOutlineEdit() (string, bool) {
	fo := a.trees.featuresOutlineState()
	tm := fo.model
	meta := a.trees.editMeta
	ret := ModeNormal
	if meta != nil && meta.returnMode != "" {
		ret = meta.returnMode
	}
	ed := tm.Edit()
	wasAdd := ed != nil && ed.Kind != tree.EditRename
	tm.CancelEdit()
	a.trees.editMeta = nil
	if wasAdd && fo.cleanSlate.active && len(fo.cleanSlate.roots) == 0 {
		fo.cleanSlate = cleanSlateState{}
		a.resyncActiveFeaturesOutline()
		a.say("discarded empty clean-slate outline")
		return ret, true
	}
	a.say("")
	return ret, true
}

func (a *App) handleFeaturesTreeMotion(s core.State, c core.Command) bool {
	if a.inFeaturesOutlineTree(&s) {
		a.featuresOutlineTreeMove(c)
		return true
	}
	if a.inFeaturesCaptureTree(&s) {
		a.featuresCaptureTreeMove(s, c)
		return true
	}
	return false
}

func (a *App) featuresOutlineViewKeySuffix() string {
	if a.trees == nil || a.trees.featuresOutline == nil {
		return a.outlineViewMode(featuresOutlineTree)
	}
	fo := a.trees.featuresOutline
	return fmt.Sprintf("%s:%s:%t:%s:%s", a.outlineViewMode(featuresOutlineTree), fo.model.SelectedID, fo.cleanSlate.active,
		sortedTrueKeys(fo.cleanSlate.roots), sortedTrueKeys(fo.expandedNotes))
}
