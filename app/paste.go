package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/tree"
)

// Contextual paste verbs over Ochazuke's typed core.RegisterValue registers.
const (
	CmdPasteAfter  = "paste.after"
	CmdPasteBefore = "paste.before"
	CmdPasteInto   = "paste.into"
)

type pastePlacement int

const (
	pasteAfter pastePlacement = iota
	pasteBefore
	pasteInto
)

func (p pastePlacement) preposition() string {
	switch p {
	case pasteBefore:
		return "above"
	case pasteInto:
		return "into"
	default:
		return "below"
	}
}

func pasteCommandSpecs() []core.CommandSpec {
	regParam := []core.ParamSpec{
		{Name: "reg", Type: core.ParamString, Description: "register name (default unnamed register \")"},
	}
	return []core.CommandSpec{
		{
			ID:          CmdPasteAfter,
			Description: "Contextually paste the register after/at the focused item (clones or moves outline/lane items, relates on Show, enqueues in Queue, or inserts into terminal/input)",
			Params:      regParam,
		},
		{
			ID:          CmdPasteBefore,
			Description: "Contextually paste the register before the focused item",
			Tags:        []string{"editing"},
			Params:      regParam,
		},
		{
			ID:          CmdPasteInto,
			Description: "Contextually paste the register inside/as a child of the focused item",
			Tags:        []string{"editing"},
			Params:      regParam,
		},
	}
}

func pasteBindings() []core.Binding {
	n := ModeNormal
	out := []core.Binding{
		// Outline overlay: p after, P before, g p as child.
		bind(ModeOutline, "", "p", CmdPasteAfter, "paste after"),
		bind(ModeOutline, "", "P", CmdPasteBefore, "paste before"),
		bind(ModeOutline, "", "g p", CmdPasteInto, "paste as child"),

		// Tree indices (2 Work tasks/lanes tree, embedded Project Outline).
		when(bind(n, views.ScopeIndex, "p", CmdPasteAfter, "paste after"), "facts.isTree"),
		hidden(when(bind(n, views.ScopeIndex, "g O", CmdPasteBefore, "paste before"), "facts.isTree")),
		when(bind(n, views.ScopeIndex, "g p", CmdPasteInto, "paste as child"), "facts.isTree"),

		// Project Portal hierarchical section trees (e.g. Lanes; panes keeps p for pin/unpin).
		when(bind(n, views.ScopeProjectContent, "p", CmdPasteAfter, "paste after"), "facts.isTree && facts.section != \"panes\""),
		hidden(when(bind(n, views.ScopeProjectContent, "g O", CmdPasteBefore, "paste before"), "facts.isTree && facts.section != \"panes\"")),
		when(bind(n, views.ScopeProjectContent, "g p", CmdPasteInto, "paste as child"), "facts.isTree && facts.section != \"panes\""),

		// Show page: p and g p relate yanked refs to the open record.
		bind(n, views.ScopeShow, "p", CmdPasteAfter, "paste / relate"),
		hidden(bind(n, views.ScopeShow, "g p", CmdPasteInto, "paste / relate")),

		// Queue page: p enqueues yanked task/capture refs or text prompt.
		bind(n, views.ScopeQueue, "p", CmdPasteAfter, "paste / enqueue"),

		// Terminal screen: g p pastes contextually (and mirrors as ctrl+a g p inside ModeTerminal).
		bind(n, "terminal", "g p", CmdPasteAfter, "paste register"),
	}
	for _, mode := range pasteInputModes {
		out = append(out, bind(mode, "", "alt+y", CmdPasteAfter, "paste register"))
	}
	return out
}

var pasteInputModes = []string{
	ModeComposer,
	ModeFormEdit,
	ModeOutlineEdit,
	ModeFeaturesNote,
	ModeThreadsCompose,
	ModeFilter,
	ModePickerFilter,
}

func (a *App) registerPaste() {
	a.handle(CmdPasteAfter, a.pasteCommand(pasteAfter))
	a.handle(CmdPasteBefore, a.pasteCommand(pasteBefore))
	a.handle(CmdPasteInto, a.pasteCommand(pasteInto))
}

// --- Dispatch -------------------------------------------------------------------

type pasteScopeHandler func(*App, core.State, core.Command, core.RegisterValue, pastePlacement) core.StepResult

var pasteScopeHandlers = map[string]pasteScopeHandler{
	views.ScopeShow:  (*App).pasteIntoShow,
	views.ScopeQueue: (*App).pasteIntoQueue,
}

func (a *App) pasteCommand(placement pastePlacement) func(core.State, core.Command) core.StepResult {
	return func(s core.State, c core.Command) core.StepResult {
		regName, reg, ok := a.registerForPaste(c)
		if !ok {
			a.say("register is empty · y y yanks an item")
			return core.Next(s)
		}
		return a.dispatchPaste(s, c, regName, reg, placement)
	}
}

func (a *App) registerForPaste(c core.Command) (rune, core.RegisterValue, bool) {
	name := regOf(c)
	if name == 0 {
		name = core.RegUnnamed
	}
	v, ok := a.nav.Registers.Get(name)
	if !ok || (v.Text == "" && !v.HasRefs()) {
		return name, core.RegisterValue{}, false
	}
	return name, v, true
}

func (a *App) dispatchPaste(s core.State, c core.Command, regName rune, reg core.RegisterValue, placement pastePlacement) core.StepResult {
	mode := a.Engine.Mode(s)
	if isPasteInputMode(mode) {
		return a.pasteIntoInput(s, reg)
	}
	chain := ui.ContextChain(a.View(s), s.ActiveFrame().FocusID, a.Shell.AppFacts(s))
	if mode == ModeTerminal || chainHasScope(chain, "terminal") {
		return a.pasteIntoTerminal(s, reg)
	}
	if !a.pasteContextualEnabled() {
		a.say("contextual paste is off (paste.contextual=false)")
		return core.Next(s)
	}
	if tgt, ok := a.pasteTreeTarget(s); ok {
		return a.pasteIntoTree(s, tgt, regName, reg, placement)
	}
	for _, f := range chain {
		if fn, ok := pasteScopeHandlers[f.Scope]; ok {
			return fn(a, s, c, reg, placement)
		}
	}
	a.say("nothing here accepts paste")
	return core.Next(s)
}

func isPasteInputMode(mode string) bool {
	return slices.Contains(pasteInputModes, mode) || mode == ModeCommandLine
}

func chainHasScope(chain []core.ContextFrame, scope string) bool {
	for _, f := range chain {
		if f.Scope == scope {
			return true
		}
	}
	return false
}

// --- Live Terminal & Text Input Paste -------------------------------------------

func (a *App) formatRegisterForTextPaste(reg core.RegisterValue) string {
	if !a.pasteContextualEnabled() || !reg.HasRefs() {
		return reg.Text
	}
	tokens := make([]string, 0, len(reg.Refs))
	for _, ref := range reg.Refs {
		tokens = append(tokens, a.refPasteToken(ref))
	}
	return strings.Join(tokens, " ")
}

func (a *App) refPasteToken(ref core.Ref) string {
	if a.Store != nil {
		if rec, err := a.Store.Get(ref); err == nil && rec != nil {
			if fp := strings.TrimSpace(rec.Text("filePath")); fp != "" {
				return formatAttachmentRef(fp)
			}
		}
	}
	return data.RefText(ref)
}

func (a *App) pasteIntoTerminal(s core.State, reg core.RegisterValue) core.StepResult {
	pane, ok := a.termPane(s)
	if !ok {
		pane, ok = a.activePane(s)
	}
	if !ok || pane == nil {
		a.say("no active terminal to paste into")
		return core.Next(s)
	}
	payload := a.formatRegisterForTextPaste(reg)
	if payload == "" {
		a.say("nothing to paste")
		return core.Next(s)
	}
	res := a.termInput(s, core.Command{
		ID:     CmdTermInput,
		Args:   map[string]any{"key": core.PasteKey, "text": payload},
		Source: "paste",
		Actor:  User,
	})
	title := views.Title(a.schema(pane.Kind), *pane)
	if a.pasteContextualEnabled() && reg.HasRefs() {
		a.say("pasted %d ref(s) into %s", len(reg.Refs), title)
	} else {
		a.say("pasted into %s", title)
	}
	return res
}

func (a *App) pasteIntoInput(s core.State, reg core.RegisterValue) core.StepResult {
	payload := a.formatRegisterForTextPaste(reg)
	if payload == "" {
		a.say("nothing to paste")
		return core.Next(s)
	}
	if next, label, ok := a.insertIntoComposerModal(s, payload); ok {
		a.say("pasted into %s", label)
		return core.Next(next)
	}
	next, label, ok := a.insertTextIntoActiveInput(s, payload)
	if !ok {
		a.say("no active text input")
		return core.Next(s)
	}
	a.say("pasted into %s", label)
	return core.Next(next)
}

func (a *App) insertIntoComposerModal(s core.State, text string) (core.State, string, bool) {
	if a.modals == nil {
		return s, "", false
	}
	top := a.modals.Top()
	h, ok := top.(richComposerHolder)
	if !ok {
		return s, "", false
	}
	res := top.Handle(core.Command{ID: CmdComposerInsert, Args: map[string]any{"text": text}})
	if !res.Handled {
		return s, "", false
	}
	label := h.richComposer().spec.InputTitle
	if label == "" {
		label = "composer"
	}
	return a.applyModalResult(s, res).State, label, true
}

// --- Tree / Outline Paste (Tasks, Lanes, any hierarchical schema) ---------------

type treePasteTarget struct {
	tm         *tree.Model
	kind       string
	treeID     string
	isWorkTree bool
	syncable   bool
}

type treePasteAnchor struct {
	parentRef   *core.Ref
	afterID     string
	beforeID    string
	anchorTitle string
	inheritTask *core.Ref
}

func (a *App) pasteTreeTarget(s core.State) (treePasteTarget, bool) {
	if tm, kind, treeID, isWork, ok := a.activeTree(s); ok {
		if a.schema(kind).EffectiveHierarchy() != nil {
			return treePasteTarget{tm: tm, kind: kind, treeID: treeID, isWorkTree: isWork, syncable: true}, true
		}
	}
	if tm, kind, treeID, isWork, syncable, ok := a.activeSelectionTree(s); ok {
		if a.schema(kind).EffectiveHierarchy() != nil {
			return treePasteTarget{tm: tm, kind: kind, treeID: treeID, isWorkTree: isWork, syncable: syncable}, true
		}
	}
	return treePasteTarget{}, false
}

func (a *App) pasteIntoTree(s core.State, tgt treePasteTarget, regName rune, reg core.RegisterValue, placement pastePlacement) core.StepResult {
	schema := a.schema(tgt.kind)
	h := schema.EffectiveHierarchy()
	if h == nil {
		a.say("paste is not supported in this tree")
		return core.Next(s)
	}
	if tgt.syncable {
		a.syncTreeModel(tgt.tm, tgt.kind, tgt.treeID)
	}
	anchor := a.resolveTreePasteAnchor(tgt, h, placement)
	matching := filterRefsByKind(reg.Refs, tgt.kind)
	if reg.HasRefs() && len(matching) > 0 {
		if reg.Cut {
			return a.pasteMoveTreeRefs(s, tgt, schema, h, anchor, regName, reg, matching, placement)
		}
		return a.pasteCloneTreeRefs(s, tgt, schema, h, anchor, matching, placement)
	}
	return a.pasteTextIntoTree(s, tgt, schema, h, anchor, reg.Text, placement)
}

func filterRefsByKind(refs []core.Ref, kind string) []core.Ref {
	var out []core.Ref
	for _, r := range refs {
		if r.Kind == kind && r.ID != "" {
			out = append(out, r)
		}
	}
	return out
}

func (a *App) resolveTreePasteAnchor(tgt treePasteTarget, h *data.HierarchySpec, placement pastePlacement) treePasteAnchor {
	row, has := tgt.tm.Focused()
	if !has || row.Draft || row.ID == "" {
		return a.hoistedTreeAnchor(tgt)
	}
	focusedRef := core.Ref{Kind: tgt.kind, ID: row.ID}
	rec, _ := a.Store.Get(focusedRef)
	anchor := treePasteAnchor{
		anchorTitle: a.title(focusedRef),
		inheritTask: recordTaskRef(rec),
	}
	if placement == pasteInto || row.ID == tgt.tm.HoistID() {
		anchor.parentRef = &focusedRef
		delete(tgt.tm.Folded, row.ID)
		return anchor
	}
	anchor.parentRef = recordParentRef(rec, h.ParentField, tgt.kind)
	if placement == pasteBefore {
		anchor.beforeID = row.ID
	} else {
		anchor.afterID = row.ID
	}
	return anchor
}

func (a *App) hoistedTreeAnchor(tgt treePasteTarget) treePasteAnchor {
	hid := tgt.tm.HoistID()
	if hid == "" {
		return treePasteAnchor{}
	}
	r := core.Ref{Kind: tgt.kind, ID: hid}
	return treePasteAnchor{parentRef: &r, anchorTitle: a.title(r)}
}

func recordTaskRef(rec *data.Record) *core.Ref {
	if rec == nil {
		return nil
	}
	if tr, ok := rec.RefField("task"); ok && tr.ID != "" {
		return &tr
	}
	return nil
}

func recordParentRef(rec *data.Record, parentField, kind string) *core.Ref {
	if rec == nil {
		return nil
	}
	if pRef, ok := rec.RefField(parentField); ok && pRef.Kind == kind && pRef.ID != "" {
		return &pRef
	}
	return nil
}

func (a *App) pasteMoveTreeRefs(
	s core.State,
	tgt treePasteTarget,
	schema data.Schema,
	h *data.HierarchySpec,
	anchor treePasteAnchor,
	regName rune,
	reg core.RegisterValue,
	refs []core.Ref,
	placement pastePlacement,
) core.StepResult {
	valid := a.filterMovableTreeRefs(tgt.kind, h.ParentField, refs, anchor.parentRef)
	if len(valid) == 0 {
		a.say("cannot move an item inside itself")
		return core.Next(s)
	}
	var parentVal any
	if anchor.parentRef != nil {
		parentVal = *anchor.parentRef
	}
	patches := make([]data.PatchInput, len(valid))
	for i, r := range valid {
		patches[i] = data.PatchInput{Ref: r, Fields: map[string]any{h.ParentField: parentVal}}
	}
	cs, err := a.Store.PatchMany(patches, "paste.move", fmt.Sprintf("move %d %s", len(valid), a.yankPluralLabel(valid)), User)
	if err != nil {
		a.fail("paste move", err)
		return core.Next(s)
	}
	a.positionPastedTreeRecords(tgt.kind, h, anchor, valid, placement)
	a.finishTreePaste(tgt, valid)
	reg.Cut = false
	a.nav.Registers.SetValue(regName, reg)
	a.wrote(formatTreePasteStatus("moved", len(valid), schema, anchor, placement), cs, nil)
	return core.Next(s)
}

func (a *App) filterMovableTreeRefs(kind, parentField string, refs []core.Ref, targetParent *core.Ref) []core.Ref {
	var out []core.Ref
	for _, r := range refs {
		if targetParent != nil && a.isTreeAncestorOrSelf(r, *targetParent, parentField) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func (a *App) isTreeAncestorOrSelf(candidate, descendant core.Ref, parentField string) bool {
	cur := descendant
	for range 64 {
		if cur == candidate {
			return true
		}
		rec, err := a.Store.Get(cur)
		if err != nil || rec == nil {
			return false
		}
		p, ok := rec.RefField(parentField)
		if !ok || p.Kind != cur.Kind || p.ID == "" {
			return false
		}
		cur = p
	}
	return false
}

var nonCloneableTreeFields = map[string]bool{
	"agentDispatchedAt":   true,
	"agentDispatchedPane": true,
	"readAt":              true,
	"baseCommit":          true,
	"headCommit":          true,
	"worktree":            true,
}

func isSkipCloneField(name string, h *data.HierarchySpec) bool {
	return name == h.ParentField || name == h.OrderField || nonCloneableTreeFields[name]
}

func cloneTreeRecordFields(schema data.Schema, src data.Record, h *data.HierarchySpec, anchor treePasteAnchor) map[string]any {
	fields := map[string]any{}
	merged := src.MergedFields()
	for _, f := range schema.Fields {
		if isSkipCloneField(f.Name, h) {
			continue
		}
		if v := merged[f.Name]; v != nil {
			fields[f.Name] = v
		}
	}
	if v := merged["body"]; v != nil {
		fields["body"] = v
	}
	if anchor.parentRef != nil {
		fields[h.ParentField] = *anchor.parentRef
	}
	applyInheritedTaskField(fields, schema, anchor)
	return fields
}

func applyInheritedTaskField(fields map[string]any, schema data.Schema, anchor treePasteAnchor) {
	if _, hasTask := fields["task"]; hasTask || anchor.inheritTask == nil {
		return
	}
	if _, declared := schema.Field("task"); declared {
		fields["task"] = *anchor.inheritTask
	}
}

func (a *App) pasteCloneTreeRefs(
	s core.State,
	tgt treePasteTarget,
	schema data.Schema,
	h *data.HierarchySpec,
	anchor treePasteAnchor,
	refs []core.Ref,
	placement pastePlacement,
) core.StepResult {
	var createdRefs []core.Ref
	var lastCS *data.ChangeSet
	for _, ref := range refs {
		src, err := a.Store.Get(ref)
		if err != nil || src == nil {
			continue
		}
		fields := cloneTreeRecordFields(schema, *src, h, anchor)
		created, cs, err := a.Store.Create(data.CreateInput{Kind: tgt.kind, Fields: fields}, User)
		if err != nil {
			a.fail("paste", err)
			return core.Next(s)
		}
		if cs != nil {
			lastCS = cs
		}
		a.copyRecordTags(src.Ref(), created.Ref())
		createdRefs = append(createdRefs, created.Ref())
	}
	if len(createdRefs) == 0 {
		a.say("nothing to paste")
		return core.Next(s)
	}
	a.positionPastedTreeRecords(tgt.kind, h, anchor, createdRefs, placement)
	a.finishTreePaste(tgt, createdRefs)
	a.wrote(formatTreePasteStatus("pasted", len(createdRefs), schema, anchor, placement), lastCS, nil)
	return core.Next(s)
}

func (a *App) copyRecordTags(from, to core.Ref) {
	rels, err := a.Store.Related(from, data.RelTag, data.Outgoing)
	if err != nil {
		return
	}
	for _, rel := range rels {
		_, _, _ = a.Store.Relate(data.RelateInput{From: to, Kind: data.RelTag, To: rel.To}, daemonSyncActor)
	}
}

func (a *App) pasteTextIntoTree(
	s core.State,
	tgt treePasteTarget,
	schema data.Schema,
	h *data.HierarchySpec,
	anchor treePasteAnchor,
	text string,
	placement pastePlacement,
) core.StepResult {
	lines := splitNonEmptyLines(text)
	if len(lines) == 0 {
		a.say("register has no text to paste")
		return core.Next(s)
	}
	sumField := summaryField(schema)
	_, hasTaskField := schema.Field("task")
	createdRefs := make([]core.Ref, 0, len(lines))
	var lastCS *data.ChangeSet
	for _, line := range lines {
		fields := map[string]any{sumField: line}
		if anchor.parentRef != nil {
			fields[h.ParentField] = *anchor.parentRef
		}
		if hasTaskField && anchor.inheritTask != nil {
			fields["task"] = *anchor.inheritTask
		}
		created, cs, err := a.Store.Create(data.CreateInput{Kind: tgt.kind, Fields: fields}, User)
		if err != nil {
			a.fail("paste", err)
			return core.Next(s)
		}
		if cs != nil {
			lastCS = cs
		}
		createdRefs = append(createdRefs, created.Ref())
	}
	a.positionPastedTreeRecords(tgt.kind, h, anchor, createdRefs, placement)
	a.finishTreePaste(tgt, createdRefs)
	a.wrote(formatTreePasteStatus("pasted", len(createdRefs), schema, anchor, placement), lastCS, nil)
	return core.Next(s)
}

func splitNonEmptyLines(text string) []string {
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var out []string
	for _, line := range raw {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func (a *App) positionPastedTreeRecords(kind string, h *data.HierarchySpec, anchor treePasteAnchor, pasted []core.Ref, placement pastePlacement) {
	if h.OrderField == "" || len(pasted) == 0 {
		return
	}
	nodes, err := a.recordTreeNodes(kind)
	if err != nil {
		return
	}
	parentID := ""
	if anchor.parentRef != nil {
		parentID = anchor.parentRef.ID
	}
	siblings, ok := findTreeChildren(nodes, parentID)
	if !ok {
		return
	}
	ordered := buildOrderedSiblingRefs(siblings, pasted, anchor, placement)
	patches := make([]data.PatchInput, len(ordered))
	for i, ref := range ordered {
		patches[i] = data.PatchInput{Ref: ref, Fields: map[string]any{h.OrderField: int64(i + 1)}}
	}
	_, _ = a.Store.PatchMany(patches, "outline.order", "order pasted items", daemonSyncActor)
}

func buildOrderedSiblingRefs(siblings []data.TreeNode, pasted []core.Ref, anchor treePasteAnchor, placement pastePlacement) []core.Ref {
	pastedSet := make(map[string]bool, len(pasted))
	for _, r := range pasted {
		pastedSet[r.ID] = true
	}
	base := make([]core.Ref, 0, len(siblings))
	for _, sib := range siblings {
		if !pastedSet[sib.Record.ID] {
			base = append(base, sib.Record.Ref())
		}
	}
	insertAt := siblingInsertIndex(base, anchor, placement)
	out := make([]core.Ref, 0, len(base)+len(pasted))
	out = append(out, base[:insertAt]...)
	out = append(out, pasted...)
	out = append(out, base[insertAt:]...)
	return out
}

func siblingInsertIndex(base []core.Ref, anchor treePasteAnchor, placement pastePlacement) int {
	if anchor.beforeID != "" {
		if idx := slices.IndexFunc(base, func(r core.Ref) bool { return r.ID == anchor.beforeID }); idx >= 0 {
			return idx
		}
		return 0
	}
	if anchor.afterID != "" {
		if idx := slices.IndexFunc(base, func(r core.Ref) bool { return r.ID == anchor.afterID }); idx >= 0 {
			return idx + 1
		}
	}
	if placement == pasteBefore {
		return 0
	}
	return len(base)
}

func (a *App) finishTreePaste(tgt treePasteTarget, refs []core.Ref) {
	if len(refs) == 0 {
		return
	}
	if tgt.syncable {
		a.syncTreeModel(tgt.tm, tgt.kind, tgt.treeID)
		tgt.tm.Reveal(refs[0].ID)
		a.saveTreeFolded(tgt.kind, tgt.treeID, tgt.tm)
		if tgt.isWorkTree {
			a.syncWorkTreeToTable(tgt.kind, tgt.tm)
		}
	}
	a.viewMemo.gen++
}

func formatTreePasteStatus(verb string, n int, schema data.Schema, anchor treePasteAnchor, placement pastePlacement) string {
	noun := strings.ToLower(schema.Plural)
	if n == 1 {
		noun = strings.ToLower(schema.Title)
	}
	if noun == "" {
		noun = schema.Kind
	}
	if anchor.anchorTitle != "" {
		return fmt.Sprintf("%s %d %s %s %q", verb, n, noun, placement.preposition(), anchor.anchorTitle)
	}
	return fmt.Sprintf("%s %d %s", verb, n, noun)
}

// --- Show Page Paste (Relate) ---------------------------------------------------

func (a *App) pasteIntoShow(s core.State, c core.Command, reg core.RegisterValue, _ pastePlacement) core.StepResult {
	target, ok := a.Showing()
	if !ok {
		target, ok = a.subject(s, c)
	}
	if !ok {
		a.say("no record open to relate")
		return core.Next(s)
	}
	if !reg.HasRefs() {
		a.say("paste on Show requires yanked record ref(s) (y y)")
		return core.Next(s)
	}
	added, skipped, lastCS := a.relateRefsToTarget(target, reg.Refs)
	a.viewMemo.gen++
	title := a.title(target)
	switch {
	case added > 0:
		a.wrote(fmt.Sprintf("related %d %s to %q", added, a.yankPluralLabel(reg.Refs[:min(added, len(reg.Refs))]), title), lastCS, nil)
	case skipped > 0:
		a.say("already related to %q", title)
	default:
		a.say("cannot relate a record to itself")
	}
	return core.Next(s)
}

func (a *App) relateRefsToTarget(target core.Ref, refs []core.Ref) (added, skipped int, lastCS *data.ChangeSet) {
	for _, ref := range refs {
		if ref == target || ref.Kind == "" || ref.ID == "" {
			continue
		}
		_, cs, err := a.Store.Relate(data.RelateInput{From: target, Kind: data.RelRelated, To: ref}, User)
		if err != nil {
			a.fail("relate", err)
			return added, skipped, lastCS
		}
		if cs != nil {
			added++
			lastCS = cs
		} else {
			skipped++
		}
	}
	return added, skipped, lastCS
}

// --- Queue Page Paste (Enqueue) -------------------------------------------------

func (a *App) pasteIntoQueue(s core.State, _ core.Command, reg core.RegisterValue, _ pastePlacement) core.StepResult {
	projectID := a.queueScope(s)
	a.syncQueue(projectID)
	queueName := data.NormalizeQueueName(a.queue.active)
	if reg.HasRefs() {
		return a.pasteRefsIntoQueue(s, projectID, queueName, reg.Refs)
	}
	return a.pasteTextIntoQueue(s, projectID, queueName, reg.Text)
}

func (a *App) pasteRefsIntoQueue(s core.State, projectID, queueName string, refs []core.Ref) core.StepResult {
	enqueued := 0
	for _, ref := range refs {
		taskID, ok := a.queueTaskIDForPasteRef(ref, projectID)
		if !ok {
			continue
		}
		if err := a.enqueueTaskIntoNamedQueue(taskID, queueName); err != nil {
			a.fail("paste into queue", err)
			return core.Next(s)
		}
		enqueued++
	}
	if enqueued == 0 {
		a.say("paste on Queue accepts task/capture refs or text")
		return core.Next(s)
	}
	a.queue.active = queueName
	a.invalidateQueueViews()
	a.say("enqueued %d item(s) onto %q", enqueued, queueName)
	return core.Next(s)
}

func (a *App) queueTaskIDForPasteRef(ref core.Ref, projectID string) (string, bool) {
	if data.IsTaskRef(ref) {
		return ref.ID, true
	}
	if !data.IsCaptureRef(ref) {
		return "", false
	}
	rec, err := a.Store.Get(ref)
	if err != nil || rec == nil {
		return "", false
	}
	body := strings.TrimSpace(rec.Text("body"))
	title := strings.TrimSpace(rec.Text("title"))
	if title == "" {
		title = data.CaptureTitleFromBody(body)
	}
	if title == "" {
		return "", false
	}
	fields := map[string]any{"title": title}
	if body != "" && body != title {
		fields["description"] = body
	}
	if projectID != "" {
		fields["parent"] = core.Ref{Kind: data.KindTask, ID: projectID}
	}
	created, _, err := a.Store.Create(data.CreateInput{Kind: data.KindTask, Fields: fields}, User)
	if err != nil {
		return "", false
	}
	_, _, _ = a.Store.Relate(data.RelateInput{From: created.Ref(), Kind: data.RelRelated, To: ref}, User)
	return created.ID, true
}

func (a *App) pasteTextIntoQueue(s core.State, projectID, queueName, text string) core.StepResult {
	body := strings.TrimSpace(text)
	if body == "" {
		a.say("register is empty")
		return core.Next(s)
	}
	title := data.CaptureTitleFromBody(body)
	fields := map[string]any{"title": title}
	if body != title {
		fields["description"] = body
	}
	if projectID != "" {
		fields["parent"] = core.Ref{Kind: data.KindTask, ID: projectID}
	}
	created, _, err := a.Store.Create(data.CreateInput{Kind: data.KindTask, Fields: fields}, User)
	if err != nil {
		a.fail("paste into queue", err)
		return core.Next(s)
	}
	if err := a.enqueueTaskIntoNamedQueue(created.ID, queueName); err != nil {
		a.fail("paste into queue", err)
		return core.Next(s)
	}
	a.queue.active = queueName
	a.invalidateQueueViews()
	a.say("enqueued %q onto %q", title, queueName)
	return core.Next(s)
}

func (a *App) enqueueTaskIntoNamedQueue(taskID, queueName string) error {
	projectID := a.Store.RootTaskID(taskID)
	var queueRec *data.Record
	if queues, err := a.Store.NamedQueues(projectID); err == nil {
		queueRec = data.QueueRecordByName(queues, queueName)
	}
	req := data.EnqueueRequest{
		TaskID:    taskID,
		QueueName: queueName,
		Mode:      data.EnqueueModeAdd,
		ProjectID: projectID,
	}
	_, _, err := a.Store.EnqueueTask(req, queueRec, User)
	return err
}
