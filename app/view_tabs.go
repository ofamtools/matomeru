package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// Scope, tab prefix, and command constants for non-destructive closable
// top-chrome Show/Record view tabs.
const (
	ScopeViewTab    = "view.tab"
	CmdViewTabClose = "view.tab.close"
	ViewTabPrefix   = "showtab."
)

// viewTab holds one open closable top-chrome tab displaying a full-page Show
// view, along with its local back/forward Show stack and the originating page
// and focus to restore when closed.
type viewTab struct {
	id          string
	ref         core.Ref
	history     []historyEntry
	histPos     int
	returnPage  string
	returnFocus string
	returnMode  string
}

func (vt *viewTab) currentRef() core.Ref {
	if vt == nil {
		return core.Ref{}
	}
	if vt.histPos >= 0 && vt.histPos < len(vt.history) {
		return vt.history[vt.histPos].ref
	}
	return vt.ref
}

func (vt *viewTab) currentFocus() string {
	if vt != nil && vt.histPos >= 0 && vt.histPos < len(vt.history) {
		if f := vt.history[vt.histPos].focus; f != "" {
			return f
		}
	}
	return showHeader
}

func isViewTabID(pageID string) bool {
	return strings.HasPrefix(pageID, ViewTabPrefix) && len(pageID) > len(ViewTabPrefix)
}

func (a *App) openShowInTabEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetOpenShowInTab, true)
}

func (a *App) viewTabByID(pageID string) *viewTab {
	if a == nil || a.nav == nil || !isViewTabID(pageID) {
		return nil
	}
	for _, vt := range a.nav.viewTabs {
		if vt.id == pageID {
			return vt
		}
	}
	return nil
}

func (a *App) isActiveViewTab(s core.State) bool {
	return a.viewTabByID(s.ActivePage) != nil
}

func (a *App) isStandaloneOrViewTab(id string) bool {
	return standalonePages[id].render != nil || a.viewTabByID(id) != nil
}

func (a *App) findViewTabForRef(ref core.Ref) *viewTab {
	if a == nil || a.nav == nil || ref.ID == "" {
		return nil
	}
	for _, vt := range a.nav.viewTabs {
		if vt.currentRef() == ref {
			return vt
		}
	}
	return nil
}

func (a *App) allocViewTabID(ref core.Ref) string {
	base := ViewTabPrefix + ref.Kind + "." + ref.ID
	if a.viewTabByID(base) == nil {
		return base
	}
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s.%d", base, i)
		if a.viewTabByID(cand) == nil {
			return cand
		}
	}
}

func (a *App) viewTabTitle(ref core.Ref) string {
	label := strings.TrimSpace(a.schema(ref.Kind).Title)
	if label == "" {
		label = "Show"
	}
	title := strings.TrimSpace(a.title(ref))
	if title == "" {
		title = ref.ID
	}
	return "• " + clip(label+" · "+title, 26)
}

func (a *App) viewTabsCacheKey() string {
	if a == nil || a.nav == nil || len(a.nav.viewTabs) == 0 {
		return ""
	}
	parts := make([]string, len(a.nav.viewTabs))
	for i, vt := range a.nav.viewTabs {
		cur := vt.currentRef()
		parts[i] = fmt.Sprintf("%s=%s/%s@%d", vt.id, cur.Kind, cur.ID, vt.histPos)
	}
	return strings.Join(parts, ",")
}

func (a *App) viewTabsMemoKey() string {
	k := a.viewTabsCacheKey()
	if k == "" {
		return ""
	}
	return "|vt:" + k
}

func (a *App) buildDynamicViewTabs() []topTabDef {
	if a == nil || a.nav == nil || len(a.nav.viewTabs) == 0 {
		return nil
	}
	out := make([]topTabDef, 0, len(a.nav.viewTabs))
	for _, vt := range a.nav.viewTabs {
		cur := vt.currentRef()
		out = append(out, topTabDef{
			id:      vt.id,
			title:   a.viewTabTitle(cur),
			viewTab: true,
		})
	}
	return out
}

func (a *App) viewTabCrumbs(vt *viewTab) string {
	cur := vt.currentRef()
	rootLabel := strings.TrimSpace(a.schema(cur.Kind).Plural)
	if len(vt.history) > 0 {
		if pl := strings.TrimSpace(a.schema(vt.history[0].ref.Kind).Plural); pl != "" {
			rootLabel = pl
		}
	}
	crumbs := []string{rootLabel}
	end := min(vt.histPos+1, len(vt.history))
	for _, h := range vt.history[:end] {
		crumbs = append(crumbs, a.title(h.ref))
	}
	if len(crumbs) > 4 {
		crumbs = []string{crumbs[0], "…", crumbs[len(crumbs)-2], crumbs[len(crumbs)-1]}
	}
	trail := " " + strings.Join(crumbs, " › ")
	if n := len(vt.history) - vt.histPos - 1; n > 0 {
		trail += fmt.Sprintf("   (%d forward)", n)
	}
	return trail + "   · q close"
}

func (a *App) viewTabSpace(s core.State, pageID string) ui.ViewNode {
	vt := a.viewTabByID(pageID)
	if vt == nil {
		return ui.ViewNode{
			ID:     "view.tab.missing",
			Kind:   ui.KindPanel,
			Scope:  ScopeViewTab,
			Title:  "View",
			Border: true,
			Facts:  map[string]any{"closableTab": true, "canCloseTab": true, "empty": true},
			Children: []ui.ViewNode{
				text("view.tab.missing.msg", "This view tab is closed. Press q to return.", "muted"),
			},
		}
	}
	cur := vt.currentRef()
	return ui.ViewNode{
		ID:    "view.tab." + vt.id,
		Kind:  ui.KindVStack,
		Scope: ScopeViewTab,
		Box:   layout.Constraints{Flex: 1},
		Facts: map[string]any{"closableTab": true, "canCloseTab": true, "isViewTab": true},
		Children: []ui.ViewNode{
			line("view.crumbs", a.viewTabCrumbs(vt), "muted"),
			a.showPage(cur, s.Pages[vt.id].FocusID),
		},
	}
}

func (a *App) saveViewTabFocus(s core.State) {
	vt := a.viewTabByID(s.ActivePage)
	if vt == nil || vt.histPos < 0 || vt.histPos >= len(vt.history) {
		return
	}
	if f := s.Pages[vt.id].FocusID; f != "" {
		vt.history[vt.histPos].focus = f
	}
}

func (a *App) openShowViewTab(s core.State, ref core.Ref) core.State {
	from := a.currentJump(s)
	originPage := s.ActivePage
	originFocus := s.Pages[originPage].FocusID
	originMode := s.Pages[originPage].Mode
	s = a.dropModals(s)

	if curTab := a.viewTabByID(originPage); curTab != nil {
		return a.pushViewTabHistory(s, curTab, ref, from)
	}
	if existing := a.findViewTabForRef(ref); existing != nil {
		return a.focusExistingViewTab(s, existing, originPage, originFocus, originMode, from)
	}

	vt := &viewTab{
		id:          a.allocViewTabID(ref),
		ref:         ref,
		history:     []historyEntry{{ref: ref, focus: showHeader}},
		histPos:     0,
		returnPage:  originPage,
		returnFocus: originFocus,
		returnMode:  originMode,
	}
	a.nav.viewTabs = append(a.nav.viewTabs, vt)
	a.resetShowScroll(ref)
	s.ActivePage = vt.id
	next := withPage(s, vt.id, showHeader, ModeNormal)
	a.say("%s · q close", a.title(ref))
	a.recordJump(from, a.currentJump(next))
	return next
}

func (a *App) pushViewTabHistory(s core.State, vt *viewTab, ref core.Ref, from jumpEntry) core.State {
	if vt.currentRef() != ref {
		a.saveViewTabFocus(s)
		vt.history = append(vt.history[:vt.histPos+1], historyEntry{ref: ref, focus: showHeader})
		vt.histPos = len(vt.history) - 1
		vt.ref = ref
		a.resetShowScroll(ref)
	}
	s.ActivePage = vt.id
	next := withPage(s, vt.id, vt.currentFocus(), ModeNormal)
	a.say("%s · q close", a.title(ref))
	a.recordJump(from, a.currentJump(next))
	return next
}

func (a *App) focusExistingViewTab(s core.State, vt *viewTab, originPage, originFocus, originMode string, from jumpEntry) core.State {
	if originPage != vt.id {
		vt.returnPage = originPage
		vt.returnFocus = originFocus
		vt.returnMode = originMode
	}
	s.ActivePage = vt.id
	next := withPage(s, vt.id, vt.currentFocus(), ModeNormal)
	a.say("%s · q close", a.title(vt.currentRef()))
	a.recordJump(from, a.currentJump(next))
	return next
}

func (a *App) viewTabTravel(s core.State, vt *viewTab, delta int) core.State {
	to := vt.histPos + delta
	if to < 0 {
		return a.closeViewTabByID(s, vt.id)
	}
	if to >= len(vt.history) {
		a.say("nothing to go forward to")
		return s
	}
	a.saveViewTabFocus(s)
	vt.histPos = to
	ref := vt.currentRef()
	vt.ref = ref
	a.resetShowScroll(ref)
	a.say("%s", a.title(ref))
	return withPage(s, vt.id, vt.currentFocus(), ModeNormal)
}

func (a *App) canReturnToPage(pageID string) bool {
	if pageID == "" {
		return false
	}
	if standalonePages[pageID].render != nil {
		return true
	}
	return a.isValidTopTab(pageID)
}

func (a *App) resolveReturnTarget(vt *viewTab, removedIdx int) string {
	if vt != nil && a.canReturnToPage(vt.returnPage) {
		return vt.returnPage
	}
	if a.nav != nil && len(a.nav.viewTabs) > 0 {
		pick := max(0, min(removedIdx-1, len(a.nav.viewTabs)-1))
		return a.nav.viewTabs[pick].id
	}
	return spaces[0].id
}

func (a *App) pruneViewTabJumps(tabID string) {
	if a.nav == nil || len(a.nav.jumps) == 0 {
		return
	}
	kept := make([]jumpEntry, 0, len(a.nav.jumps))
	newPos := a.nav.jumpPos
	for i, j := range a.nav.jumps {
		if j.page == tabID {
			if i <= a.nav.jumpPos {
				newPos--
			}
			continue
		}
		if len(kept) > 0 && sameJumpLocation(kept[len(kept)-1], j) {
			if i <= a.nav.jumpPos {
				newPos--
			}
			continue
		}
		kept = append(kept, j)
	}
	a.nav.jumps = kept
	if len(kept) == 0 {
		a.nav.jumpPos = -1
		return
	}
	a.nav.jumpPos = min(max(newPos, 0), len(kept)-1)
}

func (a *App) closeViewTabByID(s core.State, id string) core.State {
	if a.nav == nil {
		return s
	}
	idx := slices.IndexFunc(a.nav.viewTabs, func(vt *viewTab) bool { return vt.id == id })
	if idx < 0 {
		a.say("no view tab to close")
		return s
	}
	vt := a.nav.viewTabs[idx]
	a.nav.viewTabs = slices.Delete(a.nav.viewTabs, idx, idx+1)
	a.pruneViewTabJumps(vt.id)

	curPage := s.ActivePage
	if curPage != vt.id {
		a.say("closed tab")
		return s
	}
	targetPage := a.resolveReturnTarget(vt, idx)
	targetFocus := s.Pages[targetPage].FocusID
	if targetPage == vt.returnPage && vt.returnFocus != "" {
		targetFocus = vt.returnFocus
	}
	s.ActivePage = targetPage
	if targetFocus == "" {
		targetFocus = a.defaultFocus(s)
	}
	a.say("closed tab")
	return withPage(s, targetPage, targetFocus, ModeNormal)
}

func (a *App) viewTabClose(s core.State, c core.Command) core.StepResult {
	id := strings.TrimSpace(c.Arg("id"))
	if id == "" {
		id = s.ActivePage
	}
	return core.Next(a.closeViewTabByID(s, id))
}

func (a *App) prepareJumpViewTab(entry jumpEntry) string {
	if !isViewTabID(entry.page) {
		return entry.page
	}
	vt := a.viewTabByID(entry.page)
	if vt == nil {
		if entry.ref.ID == "" || a.Store == nil {
			return spaces[0].id
		}
		if _, err := a.Store.Get(entry.ref); err != nil {
			return spaces[0].id
		}
		vt = &viewTab{
			id:         entry.page,
			ref:        entry.ref,
			history:    []historyEntry{{ref: entry.ref, focus: showHeader}},
			histPos:    0,
			returnPage: spaces[0].id,
		}
		a.nav.viewTabs = append(a.nav.viewTabs, vt)
	}
	a.syncViewTabJumpEntry(vt, entry)
	return vt.id
}

func (a *App) syncViewTabJumpEntry(vt *viewTab, entry jumpEntry) {
	if vt == nil || entry.ref.ID == "" {
		return
	}
	if entry.histPos >= 0 && entry.histPos < len(vt.history) && vt.history[entry.histPos].ref == entry.ref {
		vt.histPos = entry.histPos
	} else if idx := slices.IndexFunc(vt.history, func(h historyEntry) bool { return h.ref == entry.ref }); idx >= 0 {
		vt.histPos = idx
	} else {
		vt.history = append(vt.history, historyEntry{ref: entry.ref, focus: showHeader})
		vt.histPos = len(vt.history) - 1
	}
	vt.ref = entry.ref
	a.resetShowScroll(entry.ref)
}

func (a *App) showHistoryLen(s core.State) int {
	if vt := a.viewTabByID(s.ActivePage); vt != nil {
		return len(vt.history)
	}
	return len(a.nav.history)
}

func viewTabsSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		{
			ID:          CmdViewTabClose,
			Description: "Close the active closable Show/view tab and return to the previous page",
			Params:      []core.ParamSpec{{Name: "id", Type: core.ParamString, Description: "optional view tab ID"}},
		},
	}
}

func viewTabsBindings() []core.Binding {
	n := ModeNormal
	return []core.Binding{
		when(bind(n, ScopeViewTab, "q", CmdViewTabClose, "close tab"), "facts.closableTab && !facts.recording"),
		when(bind(n, ScopeViewTab, "x", CmdViewTabClose, "close tab"), "facts.closableTab"),
		hidden(when(bind(n, ScopeViewTab, "ctrl+w c", CmdViewTabClose, "close tab"), "facts.closableTab")),
		when(bind(n, ScopeViewTab, "n", CmdNewHere, "new"), "!facts.hasFilter && !facts.hasSearch"),
		with(bind(n, ScopeViewTab, "backspace", CmdHistoryBack, "back"), map[string]any{"scope": "work"}),
		with(bind(n, ScopeViewTab, "H", CmdHistoryBack, "back"), map[string]any{"scope": "work"}),
		with(bind(n, ScopeViewTab, "L", CmdHistoryForward, "forward"), map[string]any{"scope": "work"}),
	}
}

func (a *App) registerViewTabsHandlers() {
	a.handle(CmdViewTabClose, a.viewTabClose)
}
