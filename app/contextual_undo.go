package app

import (
	"errors"
	"slices"
	"strings"
	"unicode"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/toast"
)

// UndoOrigin records the screen, region, and context chain where a TUI
// mutation originated so contextual undo and redo stay scoped to the active
// screen and prefer the active region and context chain.
type UndoOrigin struct {
	ChangeSetNumber int64    `json:"changeSetNumber"`
	Space           string   `json:"space"`
	SpaceLabel      string   `json:"spaceLabel,omitempty"`
	Region          string   `json:"region,omitempty"`
	ContextChain    []string `json:"contextChain,omitempty"`
	Label           string   `json:"label,omitempty"`
	ShowRef         string   `json:"showRef,omitempty"`
	ProjectKey      string   `json:"projectKey,omitempty"`
	Subjects        []string `json:"subjects,omitempty"`
}

// UndoController tracks the UI origin of user-initiated change sets and undos
// so u and ctrl+r operate contextually on the active screen/region.
type UndoController struct {
	origins     map[int64]UndoOrigin
	redoOrigins map[int64]UndoOrigin
}

func newUndoController() *UndoController {
	return &UndoController{
		origins:     map[int64]UndoOrigin{},
		redoOrigins: map[int64]UndoOrigin{},
	}
}

func (a *App) undoCtrlOrInit() *UndoController {
	if a.undoCtrl == nil {
		a.undoCtrl = newUndoController()
	}
	return a.undoCtrl
}

// UndoOrigin returns the recorded UI origin for a change set, when known.
func (a *App) UndoOrigin(changeSetNum int64) (UndoOrigin, bool) {
	if a == nil || a.undoCtrl == nil {
		return UndoOrigin{}, false
	}
	orig, ok := a.undoCtrl.origins[changeSetNum]
	return orig, ok
}

func (a *App) registerContextualUndoHandlers() {
	a.handle(CmdUndoGlobal, a.globalUndoHandler(false))
	a.handle(CmdRedoGlobal, a.globalUndoHandler(true))
}

func (a *App) beforeCommandUndoRev() int64 {
	if a == nil || a.Store == nil {
		return 0
	}
	rev, _ := a.Store.LatestChangeSetNumber()
	return rev
}

func (a *App) recordCommandUndoOrigins(beforeRev int64, afterState core.State) {
	if a == nil || a.Store == nil {
		return
	}
	afterRev, err := a.Store.LatestChangeSetNumber()
	if err != nil || afterRev <= beforeRev {
		return
	}
	added, err := a.Store.ChangeSetsSince(beforeRev)
	if err != nil || len(added) == 0 {
		return
	}
	uc := a.undoCtrlOrInit()
	var ctx *UndoOrigin
	for _, cs := range added {
		if !isUserEditChangeSet(cs) {
			continue
		}
		if ctx == nil {
			c := a.captureUndoContext(afterState)
			ctx = &c
		}
		orig := cloneUndoOrigin(*ctx)
		orig.ChangeSetNumber = cs.Number
		orig.Label = undoOriginLabel(cs, a.status, len(added) == 1)
		orig.Subjects = mergeUndoSubjects(orig.Subjects, cs)
		uc.origins[cs.Number] = orig
	}
}

func isUserEditChangeSet(cs data.ChangeSet) bool {
	if cs.Actor.Kind != User.Kind || cs.Actor.ID != User.ID {
		return false
	}
	return cs.Action != "undo" && cs.Action != "redo"
}

func undoOriginLabel(cs data.ChangeSet, status string, single bool) string {
	if single {
		cleaned := strings.TrimSpace(strings.TrimSuffix(status, " · u undoes"))
		if cleaned != "" {
			return cleaned
		}
	}
	if cs.Summary != "" {
		return cs.Summary
	}
	return cs.Action
}

func mergeUndoSubjects(base []string, cs data.ChangeSet) []string {
	out := slices.Clone(base)
	for _, tgt := range cs.Targets() {
		refText := data.RefText(tgt.Ref)
		if refText != "" && !slices.Contains(out, refText) {
			out = append(out, refText)
		}
	}
	return out
}

func cloneUndoOrigin(orig UndoOrigin) UndoOrigin {
	orig.ContextChain = slices.Clone(orig.ContextChain)
	orig.Subjects = slices.Clone(orig.Subjects)
	return orig
}

func (a *App) captureUndoContext(s core.State) UndoOrigin {
	space := s.ActivePage
	region := s.ActiveFrame().FocusID
	if region == "" {
		region = s.Pages[space].FocusID
	}
	root := a.View(s)
	chain := ui.ContextChain(root, region, nil)
	scopes, subjects := extractContextScopesAndSubjects(chain)
	showRef, showRec := a.activeShowRefInChain(scopes)
	label := a.spaceDisplayLabel(space, showRef, showRec)
	return UndoOrigin{
		Space:        space,
		SpaceLabel:   label,
		Region:       region,
		ContextChain: scopes,
		ShowRef:      showRef,
		ProjectKey:   a.activeProjectWorkspaceKey(s),
		Subjects:     subjects,
	}
}

func extractContextScopesAndSubjects(chain []core.ContextFrame) ([]string, []string) {
	scopes := make([]string, 0, len(chain))
	subjects := make([]string, 0, 2)
	for i := len(chain) - 1; i >= 0; i-- {
		fr := chain[i]
		if sc := fr.Scope; sc != "" && sc != "app" && !strings.HasPrefix(sc, "trait.") {
			if len(scopes) == 0 || scopes[len(scopes)-1] != sc {
				scopes = append(scopes, sc)
			}
		}
		if fr.Subject != nil && fr.Subject.ID != "" {
			refText := data.RefText(*fr.Subject)
			if !slices.Contains(subjects, refText) {
				subjects = append(subjects, refText)
			}
		}
	}
	return scopes, subjects
}

func (a *App) activeShowRefInChain(scopes []string) (string, core.Ref) {
	if !slices.Contains(scopes, views.ScopeShow) {
		return "", core.Ref{}
	}
	ref, ok := a.Showing()
	if !ok {
		return "", core.Ref{}
	}
	return data.RefText(ref), ref
}

func (a *App) spaceDisplayLabel(space, showRef string, showRec core.Ref) string {
	if showRef != "" {
		return "Show: " + a.title(showRec)
	}
	for _, tb := range a.topTabs() {
		if tb.id != space {
			continue
		}
		if tb.dynamic {
			return "Project: " + tb.projectTitle()
		}
		return tb.title
	}
	if page, ok := standalonePages[space]; ok && page.title != "" {
		return page.title
	}
	return titleCaseWord(space)
}

func titleCaseWord(s string) string {
	if s == "" {
		return "this page"
	}
	rs := []rune(s)
	rs[0] = unicode.ToUpper(rs[0])
	return string(rs)
}

func (a *App) contextualUndo(s core.State, redo bool) core.StepResult {
	if !a.contextualUndoEnabled() {
		return a.globalUndo(s, redo)
	}
	st, err := a.Store.ActorUndoState(User)
	if err != nil {
		a.fail(undoWord(redo), err)
		return core.Next(s)
	}
	cur := a.captureUndoContext(s)
	if redo {
		return a.runContextualRedo(s, st, cur)
	}
	return a.runContextualUndo(s, st, cur)
}

func (a *App) globalUndoHandler(redo bool) func(core.State, core.Command) core.StepResult {
	return func(s core.State, _ core.Command) core.StepResult {
		return a.globalUndo(s, redo)
	}
}

func (a *App) globalUndo(s core.State, redo bool) core.StepResult {
	do := a.Store.UndoLast
	if redo {
		do = a.Store.RedoLast
	}
	cs, err := do(User)
	if !redo && err == nil && cs != nil {
		a.undoCtrlOrInit().redoOrigins[cs.Number] = a.captureUndoContext(s)
	}
	a.finishUndo(cs, err, redo)
	return core.Next(s)
}

func (a *App) runContextualUndo(s core.State, st data.UndoState, cur UndoOrigin) core.StepResult {
	match, other, hasUntracked := a.resolveContextualUndo(st, cur)
	if match == nil {
		a.reportNoContextualUndo(other, hasUntracked, false)
		return core.Next(s)
	}
	cs, err := a.Store.UndoChangeSet(match.Number, User)
	if err == nil && cs != nil {
		a.undoCtrlOrInit().redoOrigins[cs.Number] = cur
	}
	a.finishUndo(cs, err, false)
	return core.Next(s)
}

func (a *App) runContextualRedo(s core.State, st data.UndoState, cur UndoOrigin) core.StepResult {
	match, other, hasUntracked := a.resolveContextualRedo(st, cur)
	if match == nil {
		a.reportNoContextualUndo(other, hasUntracked, true)
		return core.Next(s)
	}
	cs, err := a.Store.RedoChangeSet(match.Number, User)
	a.finishUndo(cs, err, true)
	return core.Next(s)
}

func (a *App) resolveContextualUndo(st data.UndoState, cur UndoOrigin) (*data.ChangeSet, *UndoOrigin, bool) {
	uc := a.undoCtrlOrInit()
	var best *data.ChangeSet
	bestRank := 0
	var firstOther *UndoOrigin
	hasUntracked := false
	for i := range st.History {
		cs := &st.History[i]
		if cs.Action == "undo" || cs.Action == "redo" || st.Undone[cs.Number] > 0 {
			continue
		}
		orig, known := uc.origins[cs.Number]
		rank := matchUndoRank(orig, known, cs, cur)
		if rank > bestRank {
			best, bestRank = cs, rank
			continue
		}
		if rank == 0 {
			firstOther, hasUntracked = noteOtherOrigin(firstOther, hasUntracked, orig, known)
		}
	}
	return best, firstOther, hasUntracked
}

func (a *App) resolveContextualRedo(st data.UndoState, cur UndoOrigin) (*data.ChangeSet, *UndoOrigin, bool) {
	uc := a.undoCtrlOrInit()
	var best *data.ChangeSet
	bestRank := 0
	var firstOther *UndoOrigin
	hasUntracked := false
	curScreenBlocked := false
	for i := range st.History {
		cs := &st.History[i]
		if cs.Action == "redo" || (cs.Action == "undo" && st.Redone[cs.Number]) {
			continue
		}
		if cs.Action != "undo" {
			orig, known := uc.origins[cs.Number]
			if matchUndoRank(orig, known, cs, cur) > 0 {
				curScreenBlocked = true
			}
			continue
		}
		orig, known := uc.lookupRedoOrigin(cs, st.UndoTarget)
		rank := matchUndoRank(orig, known, cs, cur)
		if rank > 0 {
			if !curScreenBlocked && rank > bestRank {
				best, bestRank = cs, rank
			}
			continue
		}
		firstOther, hasUntracked = noteOtherOrigin(firstOther, hasUntracked, orig, known)
	}
	return best, firstOther, hasUntracked
}

func (uc *UndoController) lookupRedoOrigin(undoCS *data.ChangeSet, undoTarget map[int64]int64) (UndoOrigin, bool) {
	if orig, ok := uc.redoOrigins[undoCS.Number]; ok {
		return orig, true
	}
	targetNum := undoCS.UndoOf
	if targetNum == 0 {
		targetNum = undoTarget[undoCS.Number]
	}
	orig, ok := uc.origins[targetNum]
	return orig, ok
}

func noteOtherOrigin(first *UndoOrigin, hasUntracked bool, orig UndoOrigin, known bool) (*UndoOrigin, bool) {
	if known {
		if first == nil {
			cp := orig
			return &cp, hasUntracked
		}
		return first, hasUntracked
	}
	return first, true
}

// matchUndoRank scores how well a candidate change set matches cur:
// 0 = different screen (never auto-undone), 1 = untracked subject match,
// 2 = same screen/space, 3 = same screen + shared sub-context, 4 = same screen + exact region.
func matchUndoRank(orig UndoOrigin, known bool, cs *data.ChangeSet, cur UndoOrigin) int {
	if !known {
		if untrackedMatchesCurrentFocus(cs, cur) {
			return 1
		}
		return 0
	}
	if !originSameScreen(orig, cs, cur) {
		return 0
	}
	if orig.Region != "" && orig.Region == cur.Region {
		return 4
	}
	if commonContextDepth(orig.ContextChain, cur.ContextChain) > 1 {
		return 3
	}
	return 2
}

func originSameScreen(orig UndoOrigin, cs *data.ChangeSet, cur UndoOrigin) bool {
	if orig.Space != cur.Space {
		return false
	}
	if orig.ProjectKey != cur.ProjectKey {
		return false
	}
	if orig.ShowRef != "" {
		return cur.ShowRef == orig.ShowRef
	}
	if cur.ShowRef != "" {
		return originTouchesSubject(orig, cs, cur.ShowRef)
	}
	return true
}

func originTouchesSubject(orig UndoOrigin, cs *data.ChangeSet, refText string) bool {
	if slices.Contains(orig.Subjects, refText) {
		return true
	}
	for _, tgt := range cs.Targets() {
		if data.RefText(tgt.Ref) == refText {
			return true
		}
	}
	return false
}

func untrackedMatchesCurrentFocus(cs *data.ChangeSet, cur UndoOrigin) bool {
	if cur.ShowRef != "" && originTouchesSubject(UndoOrigin{}, cs, cur.ShowRef) {
		return true
	}
	for _, subj := range cur.Subjects {
		if originTouchesSubject(UndoOrigin{}, cs, subj) {
			return true
		}
	}
	return false
}

func commonContextDepth(a, b []string) int {
	n := min(len(a), len(b))
	depth := 0
	for i := 0; i < n && a[i] == b[i]; i++ {
		depth++
	}
	return depth
}

func (a *App) reportNoContextualUndo(other *UndoOrigin, hasUntracked bool, redo bool) {
	verb := "undo"
	globalHint := "undo.global / U to undo globally"
	otherNoun := "edit"
	if redo {
		verb = "redo"
		globalHint = "redo.global to redo globally"
		otherNoun = "undo"
	}
	switch {
	case other != nil:
		where := other.SpaceLabel
		if where == "" {
			where = other.Space
		}
		a.say("nothing to %s on this page (last %s was on %s — use %s)", verb, otherNoun, where, globalHint)
		a.pushToast(a.status, toast.Info)
	case hasUntracked:
		a.say("nothing to %s on this page (last %s was outside this session — use %s)", verb, otherNoun, globalHint)
		a.pushToast(a.status, toast.Info)
	default:
		a.say("nothing to %s", verb)
	}
}

func undoWord(redo bool) string {
	if redo {
		return "redid"
	}
	return "undid"
}

func (a *App) finishUndo(cs *data.ChangeSet, err error, redo bool) {
	word := undoWord(redo)
	verb := map[bool]string{false: "undo", true: "redo"}[redo]
	switch {
	case errors.Is(err, data.ErrNotFound):
		a.say("nothing to %s", verb)
	case errors.Is(err, data.ErrUndoConflict):
		a.say("cannot %s on this page: %s", verb, strings.TrimPrefix(err.Error(), data.ErrUndoConflict.Error()+": "))
		a.pushToast(a.status, toast.Warn)
	case err != nil:
		a.fail(word, err)
	case cs == nil:
		a.say("%s: nothing left to change", word)
	default:
		a.say("%s", strings.Replace(cs.Summary, cs.Action, word, 1))
		if len(cs.Conflicts) > 0 {
			a.say("%s · %s", a.status, cs.Conflicts[0].Message)
		}
	}
	a.reconcileArchiveAnims()
	a.reloadForm()
}
