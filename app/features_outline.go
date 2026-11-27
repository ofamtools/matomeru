package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/tree"
)

const (
	featuresOutlineTree  = "features.outline.tree"
	featuresOutlinePanel = "features.outline"
	featuresOutlineTitle = "Interactive Outline · o/O add · r comment · t threads · b clean-slate · ( / ) views"

	CmdFeaturesOutlineAdd          = "features.outline.add"
	CmdFeaturesOutlineComment      = "features.outline.comment"
	CmdFeaturesOutlineToggleThread = "features.outline.thread.toggle"

	featSecFeatures     = "featsec.features"
	featSecVerbsImpl    = "featsec.verbs.impl"
	featSecVerbsPlanned = "featsec.verbs.planned"
	featSecCriteria     = "featsec.criteria"
	featSecViews        = "featsec.views"
	featSecProps        = "featsec.props"
	featSecConcepts     = "featsec.concepts"
	featSecGroups       = "featsec.groups"
	featSecLanes        = "featsec.lanes"
	featSecIdeas        = "featsec.ideas"

	featCommentIDPrefix  = "featnote."
	featPropNounIDPrefix = "featsec.props."
	featCommentModalID   = "features.outline.comment.modal"
)

type featuresOutlineNodeMeta struct {
	section   string
	label     string
	ref       *core.Ref
	isSection bool
	isTask    bool
	isConcept bool
	isNote    bool
	nounKind  string
}

type featuresOutlineState struct {
	model         *tree.Model
	activeKey     string
	folded        map[string]map[string]bool
	meta          map[string]featuresOutlineNodeMeta
	cleanSlate    cleanSlateState
	expandedNotes map[string]bool
}

func newFeaturesOutlineState() *featuresOutlineState {
	return &featuresOutlineState{
		model:         tree.New(nil),
		folded:        map[string]map[string]bool{},
		meta:          map[string]featuresOutlineNodeMeta{},
		expandedNotes: map[string]bool{},
	}
}

func (tc *treeController) featuresOutlineState() *featuresOutlineState {
	if tc.featuresOutline == nil {
		tc.featuresOutline = newFeaturesOutlineState()
	}
	return tc.featuresOutline
}

func (a *App) featuresOutlineEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetFeaturesOutline, true)
}

func (a *App) inFeaturesOutlineTree(s *core.State) bool {
	return a != nil && s != nil && s.ActiveFrame().FocusID == featuresOutlineTree && !a.hasOpenModal()
}

func featuresOutlineSpecs() []core.CommandSpec {
	sh := func(id, desc string, tags ...string) core.CommandSpec {
		return core.CommandSpec{ID: id, Description: desc, Tags: tags, Meta: map[string]any{"selfHosted": true}}
	}
	return []core.CommandSpec{
		sh(CmdFeaturesOutlineAdd, "Add a child idea or item inline in the unified Features outline", "editing"),
		sh(CmdFeaturesOutlineComment, "Comment or add a note on the focused Feature, Concept, View, Prop, or Raw Idea", "editing"),
		sh(CmdFeaturesOutlineToggleThread, "Toggle inline comment threads on the focused Features outline item"),
	}
}

func featuresOutlineBindings() []core.Binding {
	n, active := ModeNormal, "facts.featuresPageActive && facts.featuresOutline"
	return []core.Binding{
		when(bind(n, views.ScopeFeaturesContent, "o", CmdFeaturesOutlineAdd, "add inline idea"), active+" && !facts.isTree"),
		when(bind(n, views.ScopeFeaturesContent, "O", CmdFeaturesOutlineAdd, "add inline idea"), active+" && !facts.isTree"),
		when(bind(n, views.ScopeFeaturesContent, "a", CmdFeaturesOutlineAdd, "add inline idea"), active+" && !facts.isTree"),
		when(bind(n, views.ScopeFeaturesContent, "i", CmdFeaturesOutlineAdd, "add inline idea"), active+" && !facts.isTree"),
		when(bind(n, views.ScopeFeaturesNav, "o", CmdFeaturesOutlineAdd, "add inline idea"), active),
		when(bind(n, views.ScopeFeaturesNav, "O", CmdFeaturesOutlineAdd, "add inline idea"), active),
		when(bind(n, views.ScopeFeaturesNav, "a", CmdFeaturesOutlineAdd, "add inline idea"), active),
		when(bind(n, views.ScopeFeaturesNav, "i", CmdFeaturesOutlineAdd, "add inline idea"), active),
		when(bind(n, views.ScopeIndex, "r", CmdFeaturesOutlineComment, "comment / note"), active+" && facts.isTree"),
		when(bind(n, views.ScopeIndex, "t", CmdFeaturesOutlineToggleThread, "toggle comments"), active+" && facts.isTree"),
	}
}

func (a *App) registerFeaturesOutlineHandlers() {
	a.handle(CmdFeaturesOutlineAdd, a.featuresOutlineAddCmd)
	a.handle(CmdFeaturesOutlineComment, a.featuresOutlineCommentCmd)
	a.handle(CmdFeaturesOutlineToggleThread, a.featuresOutlineToggleThreadCmd)
}

func (a *App) featuresOutlineNotes(activeKey string) (byNode map[string][]data.Record, byRef map[core.Ref][]data.Record) {
	byNode = map[string][]data.Record{}
	byRef = map[core.Ref][]data.Record{}
	if a.Store == nil {
		return byNode, byRef
	}
	res, err := a.Store.Query(data.Query{Kind: data.KindNote, IncludeArchived: a.nav.showArchived})
	if err != nil {
		return byNode, byRef
	}
	for _, note := range res.Items {
		if nodeID := strings.TrimSpace(note.Text("featureNode")); nodeID != "" {
			target := strings.TrimSpace(note.Text("featureTarget"))
			if target == "" || target == activeKey {
				byNode[nodeID] = append(byNode[nodeID], note)
			}
		}
		if rels, rerr := a.Store.Related(note.Ref(), data.RelAbout, data.Outgoing); rerr == nil {
			for _, rel := range rels {
				byRef[rel.To] = append(byRef[rel.To], note)
			}
		}
	}
	return byNode, byRef
}

func (a *App) featuresNodeChildIdeas(snap featuresSnapshot) map[string][]data.Record {
	out := map[string][]data.Record{}
	for _, rt := range snap.rawThoughts {
		if nodeID := strings.TrimSpace(rt.Text("featureNode")); nodeID != "" {
			out[nodeID] = append(out[nodeID], rt)
		}
	}
	return out
}

func (a *App) notesForFeaturesNode(nodeID string, ref *core.Ref, byNode map[string][]data.Record, byRef map[core.Ref][]data.Record) []data.Record {
	seen := map[string]bool{}
	var out []data.Record
	add := func(list []data.Record) {
		for _, n := range list {
			if n.ID != "" && !seen[n.ID] {
				seen[n.ID] = true
				out = append(out, n)
			}
		}
	}
	add(byNode[nodeID])
	if ref != nil {
		add(byRef[*ref])
	}
	return out
}

func (a *App) attachFeaturesComments(nd *tree.Node, notes []data.Record, expanded bool) {
	if nd == nil || len(notes) == 0 {
		return
	}
	if nd.Facts == nil {
		nd.Facts = map[string]any{}
	}
	nd.Facts["noteCount"] = len(notes)
	nd.Facts["hasNotes"] = true
	nd.Facts["threadExpanded"] = expanded
	nd.Meta = append(nd.Meta, ui.TreeMeta{Text: fmt.Sprintf("💬 %d", len(notes)), Tone: "info"})
	if !expanded {
		return
	}
	noteChildren := make([]tree.Node, 0, len(notes))
	for _, note := range notes {
		ref := note.Ref()
		noteChildren = append(noteChildren, tree.Node{
			ID:         featCommentIDPrefix + note.ID,
			Scope:      data.KindNote,
			Ref:        &ref,
			Gutter:     "│",
			GutterTone: "info",
			Icon:       "●",
			IconTone:   "info",
			Label:      formatThreadNoteLabel(note),
			Facts: map[string]any{
				"isFeatureComment": true,
				"parentNodeID":     nd.ID,
			},
		})
	}
	nd.Children = append(noteChildren, nd.Children...)
}

type featuresOutlineBuilder struct {
	a           *App
	fo          *featuresOutlineState
	snap        featuresSnapshot
	detail      views.FeaturesDetailSpec
	activeKey   string
	notesByNode map[string][]data.Record
	notesByRef  map[core.Ref][]data.Record
	childIdeas  map[string][]data.Record
	taskThreads map[string][]data.Record
	taskSchema  data.Schema
	allTasks    []data.TreeNode
	viewMode    string
}

func (b *featuresOutlineBuilder) recordMeta(id string, m featuresOutlineNodeMeta) {
	b.fo.meta[id] = m
}

func (b *featuresOutlineBuilder) decorateCatalogNode(nd tree.Node, section, nounKind string) tree.Node {
	b.recordMeta(nd.ID, featuresOutlineNodeMeta{section: section, label: nd.Label, ref: nd.Ref, nounKind: nounKind})
	for _, idea := range b.childIdeas[nd.ID] {
		nd.Children = append(nd.Children, b.rawIdeaTreeNode(idea))
	}
	notes := b.a.notesForFeaturesNode(nd.ID, nd.Ref, b.notesByNode, b.notesByRef)
	b.a.attachFeaturesComments(&nd, notes, b.fo.expandedNotes[nd.ID])
	if nd.SearchText == "" {
		nd.SearchText = traits.FormatFilterableText("section", section, "title", nd.Label)
	}
	return nd
}

func (b *featuresOutlineBuilder) sectionNode(id, section, icon, label string, children []tree.Node) tree.Node {
	b.recordMeta(id, featuresOutlineNodeMeta{section: section, label: label, isSection: true})
	nd := tree.Node{
		ID:         id,
		Scope:      views.ScopeFeaturesContent,
		Icon:       icon,
		IconTone:   "accent",
		Tone:       "accent",
		Label:      label,
		Meta:       []ui.TreeMeta{{Text: fmt.Sprintf("%d", len(children)), Tone: "muted"}},
		Facts:      map[string]any{"featuresSection": section, "isSection": true},
		SearchText: traits.FormatFilterableText("section", section, "title", label),
		Children:   children,
	}
	notes := b.a.notesForFeaturesNode(id, nil, b.notesByNode, b.notesByRef)
	b.a.attachFeaturesComments(&nd, notes, b.fo.expandedNotes[id])
	return nd
}

func (b *featuresOutlineBuilder) buildFeaturesSection() (tree.Node, bool) {
	var kids []tree.Node
	for i, h := range b.detail.Highlights {
		id := fmt.Sprintf("featitem.highlight.%d", i)
		kids = append(kids, b.decorateCatalogNode(tree.Node{
			ID:       id,
			Scope:    views.ScopeFeaturesContent,
			Icon:     "◆",
			IconTone: "accent",
			Label:    h,
			Facts:    map[string]any{"featuresSection": "features"},
		}, "features", ""))
	}
	for i, c := range b.detail.AcceptanceCriteria {
		id := fmt.Sprintf("featitem.criteria.%d", i)
		kids = append(kids, b.decorateCatalogNode(tree.Node{
			ID:       id,
			Scope:    views.ScopeFeaturesContent,
			Icon:     "☐",
			IconTone: "info",
			Label:    c,
			Facts:    map[string]any{"featuresSection": "features"},
		}, "features", ""))
	}
	for _, v := range b.detail.ImplementedVerbs {
		id := "featitem.verb.impl." + v.ID
		keys := firstNonEmpty(strings.TrimSpace(v.Keys), "(unbound)")
		kids = append(kids, b.decorateCatalogNode(tree.Node{
			ID:       id,
			Scope:    views.ScopeFeaturesContent,
			Icon:     "▸",
			IconTone: "success",
			Label:    fmt.Sprintf("%s  %s", v.ID, v.Description),
			Meta:     []ui.TreeMeta{{Text: keys, Tone: "muted"}},
			Facts:    map[string]any{"featuresSection": "features", "verbId": v.ID, "implemented": true},
		}, "features", ""))
	}
	for _, v := range b.detail.PlannedVerbs {
		id := "featitem.verb.planned." + v.ID
		kids = append(kids, b.decorateCatalogNode(tree.Node{
			ID:       id,
			Scope:    views.ScopeFeaturesContent,
			Icon:     "◌",
			IconTone: "warning",
			Tone:     "muted",
			Label:    fmt.Sprintf("%s  %s", v.ID, v.Description),
			Badges:   []ui.Badge{{Text: "planned", Tone: "warning"}},
			Facts:    map[string]any{"featuresSection": "features", "verbId": v.ID, "planned": true},
		}, "features", ""))
	}
	if len(kids) == 0 {
		return tree.Node{}, false
	}
	return b.sectionNode(featSecFeatures, "features", "✦", "Features", kids), true
}

func (b *featuresOutlineBuilder) buildViewsSection() (tree.Node, bool) {
	if len(b.detail.Views) == 0 {
		return tree.Node{}, false
	}
	conceptByTitle := map[string]data.Record{}
	for _, c := range b.snap.concepts {
		if c.Text(data.FieldConceptKind) == data.ConceptKindView {
			conceptByTitle[strings.ToLower(strings.TrimSpace(c.Text("title")))] = c
		}
	}
	kids := make([]tree.Node, 0, len(b.detail.Views))
	for i, vr := range b.detail.Views {
		id := fmt.Sprintf("featitem.view.%d", i)
		var ref *core.Ref
		scope := views.ScopeFeaturesContent
		isConcept := false
		if vr.Suggested {
			if rec, ok := conceptByTitle[strings.ToLower(strings.TrimSpace(vr.Name))]; ok {
				r := rec.Ref()
				ref, scope, id, isConcept = &r, data.KindConcept, rec.ID, true
			}
		}
		var meta []ui.TreeMeta
		if vr.Kind != "" {
			meta = append(meta, ui.TreeMeta{Text: vr.Kind, Tone: "muted"})
		}
		if vr.OptOutSetting != "" {
			meta = append(meta, ui.TreeMeta{Text: vr.OptOutSetting, Tone: "muted"})
		}
		var badges []ui.Badge
		if vr.Suggested {
			badges = append(badges, ui.Badge{Text: "suggested", Tone: "info"})
		}
		label := vr.Name
		if vr.Summary != "" {
			label += " — " + clip(vr.Summary, 64)
		}
		nd := b.decorateCatalogNode(tree.Node{
			ID:       id,
			Scope:    scope,
			Ref:      ref,
			Icon:     "◫",
			IconTone: "info",
			Label:    label,
			Badges:   badges,
			Meta:     meta,
			Facts:    map[string]any{"featuresSection": "views"},
		}, "views", "")
		if isConcept {
			m := b.fo.meta[id]
			m.isConcept = true
			b.fo.meta[id] = m
		}
		kids = append(kids, nd)
	}
	return b.sectionNode(featSecViews, "views", "◫", "Views", kids), true
}

func (b *featuresOutlineBuilder) propRowNode(idPrefix, nounKind string, i int, row views.FeaturesPropRow) tree.Node {
	var meta []ui.TreeMeta
	if row.Type != "" {
		meta = append(meta, ui.TreeMeta{Text: row.Type, Tone: "muted"})
	}
	if row.Role != "" {
		meta = append(meta, ui.TreeMeta{Text: row.Role, Tone: "muted"})
	}
	var badges []ui.Badge
	if row.Suggested {
		badges = append(badges, ui.Badge{Text: "suggested", Tone: "info"})
	}
	label := row.Name
	if row.Description != "" {
		label += " — " + clip(row.Description, 60)
	}
	return b.decorateCatalogNode(tree.Node{
		ID:       fmt.Sprintf("%s.%d", idPrefix, i),
		Scope:    views.ScopeFeaturesContent,
		Icon:     "·",
		IconTone: "muted",
		Label:    label,
		Badges:   badges,
		Meta:     meta,
		Facts:    map[string]any{"featuresSection": "props"},
	}, "props", nounKind)
}

func (b *featuresOutlineBuilder) buildPropsSection() (tree.Node, bool) {
	fk, _ := parseFeatureKey(b.activeKey)
	var kids []tree.Node
	for i, row := range b.detail.Props {
		kids = append(kids, b.propRowNode("featitem.prop", fk.Name, i, row))
	}
	for _, noun := range b.detail.ItemizedNouns {
		subID := featPropNounIDPrefix + noun.Kind
		subKids := make([]tree.Node, 0, len(noun.Props))
		for i, row := range noun.Props {
			subKids = append(subKids, b.propRowNode("featitem.prop."+noun.Kind, noun.Kind, i, row))
		}
		b.recordMeta(subID, featuresOutlineNodeMeta{section: "props", label: noun.Title + " props", isSection: true, nounKind: noun.Kind})
		kids = append(kids, tree.Node{
			ID:       subID,
			Scope:    views.ScopeFeaturesContent,
			Icon:     firstNonEmpty(noun.Icon, "◆"),
			IconTone: "info",
			Label:    noun.Title + " props",
			Meta:     []ui.TreeMeta{{Text: fmt.Sprintf("%d", len(subKids)), Tone: "muted"}},
			Facts:    map[string]any{"featuresSection": "props", "isSection": true},
			Children: subKids,
		})
	}
	if len(kids) == 0 {
		return tree.Node{}, false
	}
	return b.sectionNode(featSecProps, "props", "⊞", "Props", kids), true
}

func (b *featuresOutlineBuilder) linkedItemNode(section, icon string, item views.FeaturesLinkedItem, isConcept, isTask bool) tree.Node {
	var ref *core.Ref
	scope := views.ScopeFeaturesContent
	if item.Ref != nil {
		r := *item.Ref
		ref = &r
		scope = r.Kind
	}
	label := item.Title
	if item.Subtitle != "" {
		label += " — " + clip(item.Subtitle, 64)
	}
	var meta []ui.TreeMeta
	if item.Status != "" {
		meta = append(meta, ui.TreeMeta{Text: item.Status, Tone: item.Tone})
	}
	nd := tree.Node{
		ID:         item.ID,
		Scope:      scope,
		Ref:        ref,
		Icon:       icon,
		IconTone:   firstNonEmpty(item.Tone, "info"),
		Label:      label,
		Badges:     item.Badges,
		Meta:       meta,
		Facts:      map[string]any{"featuresSection": section, "status": item.Status},
		SearchText: traits.FormatFilterableText("section", section, "title", item.Title, "status", item.Status),
	}
	if ref != nil && b.a.Store != nil {
		if rec, err := b.a.Store.Get(*ref); err == nil && rec != nil {
			nd.Facts = views.RecordFacts(b.a.schema(ref.Kind), *rec, b.a.now())
			nd.Facts["featuresSection"] = section
			if rec.IsStarred() {
				nd.Meta = append(nd.Meta, ui.TreeMeta{Text: "★", Tone: "icon.star"})
			}
		}
	}
	b.recordMeta(nd.ID, featuresOutlineNodeMeta{
		section:   section,
		label:     item.Title,
		ref:       ref,
		isConcept: isConcept,
		isTask:    isTask,
	})
	notes := b.a.notesForFeaturesNode(nd.ID, ref, b.notesByNode, b.notesByRef)
	b.a.attachFeaturesComments(&nd, notes, b.fo.expandedNotes[nd.ID])
	return nd
}

func (b *featuresOutlineBuilder) buildConceptsSection() (tree.Node, bool) {
	if len(b.detail.Concepts) == 0 && b.detail.Kind != string(featureKeyConcept) {
		return tree.Node{}, false
	}
	kids := make([]tree.Node, 0, len(b.detail.Concepts))
	for _, c := range b.detail.Concepts {
		kids = append(kids, b.linkedItemNode("concepts", "◇", c, true, false))
	}
	return b.sectionNode(featSecConcepts, "concepts", "◇", "Concepts", kids), true
}

func (b *featuresOutlineBuilder) buildLinkedRecordsSection(id, section, icon, title string, items []views.FeaturesLinkedItem, isTask bool) (tree.Node, bool) {
	if len(items) == 0 {
		return tree.Node{}, false
	}
	kids := make([]tree.Node, 0, len(items))
	for _, it := range items {
		kids = append(kids, b.linkedItemNode(section, icon, it, false, isTask))
	}
	return b.sectionNode(id, section, icon, title, kids), true
}

func (b *featuresOutlineBuilder) rawIdeaTreeNode(rec data.Record) tree.Node {
	if nd, ok := findProjectTreeNode(b.allTasks, rec.ID); ok {
		built, _ := b.a.buildTreeNodes(b.taskSchema, data.KindTask, []data.TreeNode{nd})
		if len(built) > 0 {
			b.registerTaskSubtreeMeta(built[0])
			return built[0]
		}
	}
	ref := rec.Ref()
	nd := tree.Node{
		ID:         rec.ID,
		Scope:      data.KindTask,
		Ref:        &ref,
		Icon:       "○",
		Label:      views.Title(b.taskSchema, rec),
		Facts:      views.RecordFacts(b.taskSchema, rec, b.a.now()),
		SearchText: views.RecordFilterableText(b.taskSchema, rec),
	}
	b.recordMeta(nd.ID, featuresOutlineNodeMeta{section: "ideas", label: nd.Label, ref: &ref, isTask: true})
	return nd
}

func (b *featuresOutlineBuilder) registerTaskSubtreeMeta(nd tree.Node) {
	isNote := strings.HasPrefix(nd.ID, threadNoteIDPrefix)
	b.recordMeta(nd.ID, featuresOutlineNodeMeta{
		section: "ideas",
		label:   nd.Label,
		ref:     nd.Ref,
		isTask:  !isNote,
		isNote:  isNote,
	})
	for _, ch := range nd.Children {
		b.registerTaskSubtreeMeta(ch)
	}
}

func (b *featuresOutlineBuilder) buildIdeasSection() tree.Node {
	matchedIDs := map[string]bool{}
	for _, item := range b.detail.RawThoughts {
		matchedIDs[item.ID] = true
	}
	var rootIdeas []data.TreeNode
	if b.snap.wishlistRoot != nil {
		if wNode, ok := findProjectTreeNode(b.allTasks, b.snap.wishlistRoot.ID); ok {
			for _, ch := range wNode.Children {
				if matchedIDs[ch.Record.ID] {
					rootIdeas = append(rootIdeas, ch)
				}
			}
		}
	}
	rootIdeas = b.a.applyOutlineViewMode(rootIdeas, data.KindTask, featuresOutlineTree)
	built, _ := b.a.buildTreeNodes(b.taskSchema, data.KindTask, rootIdeas)
	for _, nd := range built {
		b.registerTaskSubtreeMeta(nd)
	}
	for _, item := range b.detail.RawThoughts {
		if slices.ContainsFunc(built, func(nd tree.Node) bool { return nd.ID == item.ID }) {
			continue
		}
		if item.Ref != nil && item.Ref.Kind != b.taskSchema.Kind {
			built = append(built, b.linkedItemNode("ideas", "✎", item, false, false))
		}
	}
	return b.sectionNode(featSecIdeas, "ideas", "✎", "Raw Ideas", built)
}

func (b *featuresOutlineBuilder) buildCleanSlateNodes(cs *cleanSlateState) []tree.Node {
	if cs == nil || len(cs.roots) == 0 || b.snap.wishlistRoot == nil {
		return nil
	}
	wNode, ok := findProjectTreeNode(b.allTasks, b.snap.wishlistRoot.ID)
	if !ok {
		return nil
	}
	var kept []data.TreeNode
	for _, ch := range wNode.Children {
		if cs.roots[ch.Record.ID] {
			kept = append(kept, ch)
		}
	}
	built, _ := b.a.buildTreeNodes(b.taskSchema, data.KindTask, kept)
	for _, nd := range built {
		b.registerTaskSubtreeMeta(nd)
	}
	return built
}

func filterFeaturesOutlineNodesByView(nodes []tree.Node, mode string) []tree.Node {
	if mode == OutlineViewAll {
		return nodes
	}
	var out []tree.Node
	for _, nd := range nodes {
		kids := filterFeaturesOutlineNodesByView(nd.Children, mode)
		if keepFeaturesOutlineNode(nd, mode, len(kids) > 0) {
			cp := nd
			cp.Children = kids
			out = append(out, cp)
		}
	}
	return out
}

func keepFeaturesOutlineNode(nd tree.Node, mode string, hasKeptChild bool) bool {
	if mode == OutlineViewActive {
		if st, _ := nd.Facts["status"].(string); doneOutlineStatuses[strings.ToLower(strings.TrimSpace(st))] {
			return false
		}
		if st, _ := nd.Facts["workStatus"].(string); doneOutlineStatuses[strings.ToLower(strings.TrimSpace(st))] {
			return false
		}
		return true
	}
	if mode == OutlineViewAgentThreads {
		if hasNotes, _ := nd.Facts["hasNotes"].(bool); hasNotes {
			return true
		}
		if forAgent, _ := nd.Facts["forAgent"].(bool); forAgent {
			return true
		}
		if isComment, _ := nd.Facts["isFeatureComment"].(bool); isComment {
			return true
		}
		return hasKeptChild
	}
	return true
}

func (a *App) syncFeaturesOutlineTree(snap featuresSnapshot, activeKey string, detail views.FeaturesDetailSpec) *tree.Model {
	fo := a.trees.featuresOutlineState()
	tm := fo.model
	if tm.Editing() && fo.activeKey == activeKey {
		tm.ScrollOff, tm.PageSize = a.scrollOff(), a.treeViewportHeight(featuresOutlineTree)
		return tm
	}
	fo.activeKey = activeKey
	fo.meta = map[string]featuresOutlineNodeMeta{}
	allTasks, _ := a.recordTreeNodes(data.KindTask)
	notesByNode, notesByRef := a.featuresOutlineNotes(activeKey)
	taskThreads, _ := a.taskNoteThreads()
	b := &featuresOutlineBuilder{
		a:           a,
		fo:          fo,
		snap:        snap,
		detail:      detail,
		activeKey:   activeKey,
		notesByNode: notesByNode,
		notesByRef:  notesByRef,
		childIdeas:  a.featuresNodeChildIdeas(snap),
		taskThreads: taskThreads,
		taskSchema:  a.schema(data.KindTask),
		allTasks:    allTasks,
		viewMode:    a.outlineViewMode(featuresOutlineTree),
	}

	var roots []tree.Node
	if fo.cleanSlate.active {
		roots = b.buildCleanSlateNodes(&fo.cleanSlate)
	} else {
		roots = b.buildFullOutlineRoots()
		roots = filterFeaturesOutlineNodesByView(roots, b.viewMode)
	}

	if fo.folded[activeKey] == nil {
		fo.folded[activeKey] = map[string]bool{featSecProps: true}
	}
	tm.DefaultFolded = false
	tm.Folded = fo.folded[activeKey]
	tm.GapTopLevel = a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetTreeTopLevelGap, true)
	tm.Guides = a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetTreeGuides, true)
	tm.ShowFoldCount = true
	tm.ShowGutter = a.outlineGutterEnabled()
	tm.Wrap = a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetWrapOutline, true)
	tm.ScrollOff, tm.PageSize = a.scrollOff(), a.treeViewportHeight(featuresOutlineTree)
	tm.SetNodes(roots)
	fo.folded[activeKey] = tm.Folded
	return tm
}

func (b *featuresOutlineBuilder) buildDomainSections() []tree.Node {
	if len(b.detail.DomainGroups) == 0 {
		return nil
	}
	out := make([]tree.Node, 0, len(b.detail.DomainGroups))
	for gi, group := range b.detail.DomainGroups {
		key := strings.TrimSpace(group.Key)
		if key == "" {
			key = fmt.Sprintf("group-%d", gi)
		}
		kids := make([]tree.Node, 0, len(group.Items))
		for i, item := range group.Items {
			kids = append(kids, b.domainOccurrenceTreeNode(key, i, item))
		}
		secID := "featsec.domain." + key
		out = append(out, b.sectionNode(secID, "domain", firstNonEmpty(group.Icon, "◆"), group.Title, kids))
	}
	return out
}

func (b *featuresOutlineBuilder) domainOccurrenceTreeNode(groupKey string, idx int, item views.DomainOccurrence) tree.Node {
	id := fmt.Sprintf("features.domain.%s.%d", groupKey, idx)
	if clean := strings.TrimSpace(item.ID); clean != "" {
		id = fmt.Sprintf("features.domain.%s.%s", groupKey, clean)
	}
	label := item.Name
	if item.Summary != "" {
		label += " — " + clip(item.Summary, 60)
	}
	var meta []ui.TreeMeta
	if loc := item.Location.Format(); loc != "" {
		meta = append(meta, ui.TreeMeta{Text: loc, Tone: "accent"})
	}
	if sym := strings.TrimSpace(item.Location.Symbol); sym != "" {
		meta = append(meta, ui.TreeMeta{Text: sym, Tone: "muted"})
	}
	facts := map[string]any{
		"featuresSection":      "domain",
		"domainOccurrenceId":   item.ID,
		"domainClassification": item.Classification,
		"hasCodeLocation":      item.Location.Valid(),
	}
	if item.Location.Valid() {
		facts["codeFile"] = strings.TrimSpace(item.Location.File)
		facts["codeStartLine"] = item.Location.StartLine
		facts["codeEndLine"] = item.Location.EndLine
		facts["codeSymbol"] = strings.TrimSpace(item.Location.Symbol)
		facts["codeTitle"] = strings.TrimSpace(item.Name)
		facts["codeSummary"] = strings.TrimSpace(item.Summary)
	}
	return b.decorateCatalogNode(tree.Node{
		ID:         id,
		Scope:      views.ScopeFeaturesContent,
		Icon:       firstNonEmpty(item.Glyph, "·"),
		IconTone:   firstNonEmpty(item.Tone, "info"),
		Label:      label,
		Meta:       meta,
		Facts:      facts,
		SearchText: traits.FormatFilterableText("section", "domain", "title", item.Name, "file", item.Location.File, "symbol", item.Location.Symbol),
	}, "domain", "")
}

func (b *featuresOutlineBuilder) buildFullOutlineRoots() []tree.Node {
	var roots []tree.Node
	if b.detail.Kind == string(featureKeyDomain) {
		roots = append(roots, b.buildDomainSections()...)
	}
	if sec, ok := b.buildFeaturesSection(); ok {
		roots = append(roots, sec)
	}
	if sec, ok := b.buildConceptsSection(); ok {
		roots = append(roots, sec)
	}
	if sec, ok := b.buildViewsSection(); ok {
		roots = append(roots, sec)
	}
	roots = append(roots, b.buildIdeasSection())
	if b.detail.Kind != string(featureKeyDomain) {
		roots = append(roots, b.buildDomainSections()...)
	}
	if sec, ok := b.buildLinkedRecordsSection(featSecGroups, "groups", "◈", "Organized Feature Groups", b.detail.LinkedGroups, true); ok {
		roots = append(roots, sec)
	}
	if sec, ok := b.buildLinkedRecordsSection(featSecLanes, "lanes", "▤", "Linked Lanes", b.detail.LinkedLanes, false); ok {
		roots = append(roots, sec)
	}
	if sec, ok := b.buildPropsSection(); ok {
		roots = append(roots, sec)
	}
	return roots
}

func (a *App) featuresOutlineNode(snap featuresSnapshot, activeKey string, detail views.FeaturesDetailSpec) ui.ViewNode {
	tm := a.syncFeaturesOutlineTree(snap, activeKey, detail)
	a.registerTreeFilterable(featuresOutlineTree, tm)
	fo := a.trees.featuresOutlineState()

	extra := a.outlineViewBadges(featuresOutlineTree)
	if fo.cleanSlate.active {
		extra = append(extra, ui.Badge{Text: "clean slate", Tone: "accent"})
	}
	if a.nav.showArchived {
		extra = append(extra, ui.Badge{Text: "archived", Tone: "muted"})
	}
	panel := views.TreeViewWith(a.schema(data.KindTask), tm, featuresOutlinePanel, featuresOutlineTree, views.ScopeIndex, a.stickyTreeBreadcrumbsEnabled(), extra...)
	panel.Title = featuresOutlineTitle
	panel.Badges = trimProjectRootBadges(panel.Badges)
	a.applyOutlineViewFacts(&panel, featuresOutlineTree)
	applyFeaturesOutlineFacts(&panel, fo.cleanSlate.active)
	return panel
}

func trimProjectRootBadges(badges []ui.Badge) []ui.Badge {
	out := make([]ui.Badge, 0, len(badges))
	for _, b := range badges {
		if strings.HasSuffix(b.Text, " project") || strings.HasSuffix(b.Text, " projects") {
			continue
		}
		if strings.HasSuffix(b.Text, " tasks") {
			b.Text = strings.TrimSuffix(b.Text, " tasks") + " items"
		} else if strings.HasSuffix(b.Text, " task") {
			b.Text = strings.TrimSuffix(b.Text, " task") + " item"
		}
		out = append(out, b)
	}
	return out
}

func applyFeaturesOutlineFacts(panel *ui.ViewNode, cleanSlate bool) {
	set := func(n *ui.ViewNode) {
		if n.Facts == nil {
			n.Facts = map[string]any{}
		}
		n.Facts["isTree"] = true
		n.Facts["featuresOutline"] = true
		n.Facts["cleanSlate"] = cleanSlate
	}
	set(panel)
	if len(panel.Children) > 0 {
		set(&panel.Children[0])
	}
}
