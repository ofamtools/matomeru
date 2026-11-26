package app

import (
	"slices"
	"strings"

	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
)

// ModePage is the pane-locked reading and scrolling mode for non-screen
// ui.page views (analogous to ModeTerminal for live PTY panes).
const ModePage = "page"

// Commands for pane-locked ui.page reading and compact TOC groups.
const (
	CmdPageLeave      = "page.leave"
	CmdPageItemNext   = "page.item.next"
	CmdPageItemPrev   = "page.item.prev"
	CmdTOCGroupToggle = "site.toc.group.toggle"
)

func pageModeSpecs() []core.CommandSpec {
	out := []core.CommandSpec{
		spec(CmdPageLeave, "Leave the locked canvas reading view and return to the pane TOC"),
		spec(CmdPageItemNext, "Focus the next interactive block or control inside the active canvas"),
		spec(CmdPageItemPrev, "Focus the previous interactive block or control inside the active canvas"),
		spec(CmdTOCGroupToggle, "Collapse or expand the focused pane TOC group"),
	}
	return append(out, canvasSpecs()...)
}

func pageModeBindings() []core.Binding {
	all := ""
	return []core.Binding{
		// Pane-locked canvas interaction & scrolling (zero g-prefixed keys).
		bind(ModePage, all, "j", "motion.down", "next row / scroll down"),
		bind(ModePage, all, "k", "motion.up", "prev row / scroll up"),
		hidden(bind(ModePage, all, "down", "motion.down", "next row / scroll down")),
		hidden(bind(ModePage, all, "up", "motion.up", "prev row / scroll up")),
		when(bind(ModePage, all, "l", CmdCanvasLinkNext, "next link"), "facts.hasLinks"),
		when(bind(ModePage, all, "h", CmdCanvasLinkPrev, "prev link"), "facts.hasLinks"),
		hidden(when(bind(ModePage, all, "right", CmdCanvasLinkNext, "next link"), "facts.hasLinks")),
		hidden(when(bind(ModePage, all, "left", CmdCanvasLinkPrev, "prev link"), "facts.hasLinks")),
		bind(ModePage, all, "]", CmdCanvasHeadingNext, "next heading"),
		bind(ModePage, all, "[", CmdCanvasHeadingPrev, "prev heading"),
		bind(ModePage, all, "H", CmdCanvasHeadingPick, "headings"),
		bind(ModePage, all, "ctrl+e", "motion.scroll.down", "scroll down"),
		bind(ModePage, all, "ctrl+y", "motion.scroll.up", "scroll up"),
		bind(ModePage, all, "ctrl+d", "motion.half.down", "half page down"),
		bind(ModePage, all, "ctrl+u", "motion.half.up", "half page up"),
		bind(ModePage, all, "ctrl+f", "motion.page.down", "page down"),
		bind(ModePage, all, "ctrl+b", "motion.page.up", "page up"),
		when(bind(ModePage, all, "space", CmdCanvasToggleCheck, "toggle checkbox"), "facts.canvasMarkdown && facts.onCheckbox"),
		hidden(when(bind(ModePage, all, "space", "motion.page.down", "page down"), "!(facts.canvasMarkdown && facts.onCheckbox)")),
		when(bind(ModePage, all, "x", CmdCanvasToggleCheck, "toggle checkbox"), "facts.canvasMarkdown && facts.hasCheckboxes"),
		bind(ModePage, all, "T", "motion.top", "top"),
		hidden(bind(ModePage, all, "home", "motion.top", "top")),
		bind(ModePage, all, "G", "motion.bottom", "bottom"),
		hidden(bind(ModePage, all, "end", "motion.bottom", "bottom")),
		bind(ModePage, all, "tab", CmdPageItemNext, "next block"),
		bind(ModePage, all, "shift+tab", CmdPageItemPrev, "prev block"),
		when(bind(ModePage, all, "n", CmdQuestionNext, "next question"), "facts.hasQuestions"),
		when(bind(ModePage, all, "p", CmdQuestionPrev, "prev question"), "facts.hasQuestions"),
		when(bind(ModePage, all, "n", CmdCanvasLinkNext, "next link"), "!facts.hasQuestions && facts.hasLinks"),
		when(bind(ModePage, all, "p", CmdCanvasLinkPrev, "prev link"), "!facts.hasQuestions && facts.hasLinks"),
		bind(ModePage, "question", "enter", CmdAnswer, "answer"),
		bind(ModePage, "question", "a", CmdAnswer, "answer"),
		when(bind(ModePage, all, "enter", CmdCanvasActivate, "open / inspect"), "facts.canvasTable || facts.canvasMarkdown || facts.canvasTree || facts.canvasText"),
		when(bind(ModePage, all, "o", CmdCanvasActivate, "open / inspect"), "facts.canvasTable || facts.canvasMarkdown || facts.canvasTree || facts.canvasText"),
		when(bind(ModePage, all, "y", CmdCanvasYank, "yank"), "facts.canvasTable || facts.canvasMarkdown || facts.canvasTree || facts.canvasText"),
		when(bind(ModePage, all, "enter", CmdAnswer, "answer"), "facts.hasQuestions && !facts.canvasTable && !facts.canvasMarkdown && !facts.canvasTree && !facts.canvasText"),
		when(bind(ModePage, all, "a", CmdAnswer, "answer"), "facts.hasQuestions"),
		when(bind(ModePage, all, "ctrl+s", CmdQuestionSubmitAll, "submit all answers"), "facts.hasQuestions"),
		bind(ModePage, all, "esc", CmdPageLeave, "leave canvas"),
		bind(ModePage, all, "q", CmdPageLeave, "leave canvas"),
		hidden(when(bind(ModePage, all, "h", CmdPageLeave, "leave canvas"), "!facts.hasLinks && facts.projectOpenPage")),
		hidden(bind(ModePage, all, "backspace", CmdPageLeave, "leave canvas")),
		{Mode: ModePage, Keys: keys("ctrl+a"), Layer: &leader, Hint: "leader"},

		// Collapsible TOC group toggle.
		when(bind(ModeNormal, views.ScopeTOC, "space", CmdTOCGroupToggle, "fold/unfold TOC group"), "facts.tocGroup != null"),
	}
}

func (a *App) activeSitePageID() string {
	if a.Shell != nil {
		return a.activeSitePageIDFor(a.Shell.State)
	}
	pane, ok := a.SelectedPane()
	if !ok {
		return ""
	}
	id := a.panes.activePage[pane.ID]
	if id == "" {
		return views.ScreenPageID
	}
	return id
}

func (a *App) activeSitePageIDFor(s core.State) string {
	if pid := a.activeProjectPageID(s); pid != "" {
		return pid
	}
	pane, ok := a.activePane(s)
	if !ok {
		return ""
	}
	id := a.panes.activePage[pane.ID]
	if id == "" {
		return views.ScreenPageID
	}
	return id
}

func (a *App) pageScrollOffset(pageID string) int {
	if a.panes == nil || a.panes.pageScroll == nil || pageID == "" {
		return 0
	}
	return a.panes.pageScroll[pageID]
}

func (a *App) setPageScrollOffset(pageID string, offset int) {
	if a.panes == nil || pageID == "" {
		return
	}
	if a.panes.pageScroll == nil {
		a.panes.pageScroll = map[string]int{}
	}
	if offset < 0 {
		offset = 0
	}
	a.panes.pageScroll[pageID] = offset
}

// scrollPageReveal scrolls the page toward targetID under the shared scroll
// config and reports whether focus should move to the target now. Under the
// half-page long-jump policy a target that is completely off-screen only
// scrolls the page half a page toward it and returns false, so the caller
// keeps focus where it is until the target is in view.
func (a *App) scrollPageReveal(s core.State, pageID, targetID string) bool {
	if pageID == "" || targetID == "" {
		return true
	}
	info, ok := s.Layout.Scrolls[views.PageScrollID]
	if !ok {
		return true
	}
	span, ok := scrollSpanOf(info, targetID)
	if !ok {
		return true
	}
	st := scrollStateFromInfo(a.pageScrollOffset(pageID), info, true)
	res := traits.ScrollStepToward(st, a.scrollConfig(), span)
	a.setPageScrollOffset(pageID, res.State.OffsetY)
	return res.FocusTarget
}

// pageScrollAliases maps page-only motions onto the scroll trait's commands.
var pageScrollAliases = map[string]string{
	"motion.block.next": "motion.half.down",
	"motion.block.prev": "motion.half.up",
}

func (a *App) pageScrollMotion(s core.State, c core.Command) core.StepResult {
	if res, handled := a.canvasMotion(s, c); handled {
		return res
	}
	pageID := a.activeSitePageIDFor(s)
	if pageID == "" || pageID == views.ScreenPageID {
		return core.Next(s)
	}
	info, hasInfo := s.Layout.Scrolls[views.PageScrollID]
	if id, ok := pageScrollAliases[c.ID]; ok {
		c.ID = id
	}
	switch c.ID {
	case "motion.down", "motion.scroll.down", "motion.up", "motion.scroll.up",
		"motion.half.down", "motion.half.up", "motion.page.down", "motion.page.up",
		"motion.top":
	case "motion.bottom":
		if !hasInfo {
			return core.Next(s)
		}
	default:
		return core.Next(s)
	}
	next, _, _ := traits.ScrollStep(scrollStateFromInfo(a.pageScrollOffset(pageID), info, hasInfo), a.scrollConfig(), c)
	a.setPageScrollOffset(pageID, next.OffsetY)
	return core.Next(s)
}

func (a *App) pageLeave(s core.State, _ core.Command) core.StepResult {
	if a.Engine.Mode(s) != ModePage {
		return core.Next(s)
	}
	if next, handled := a.leaveProjectPage(s); handled {
		return core.Next(next)
	}
	pageID := a.activeSitePageIDFor(s)
	target := tocPrefix + pageID
	nodes := a.focusables(s)
	if !slices.ContainsFunc(nodes, func(n fnode) bool { return n.id == target }) {
		target = railTable
		if isProjectTab(s.ActivePage) {
			target = projectPanesTree
		}
		for _, n := range nodes {
			if strings.HasPrefix(n.id, tocPrefix) {
				target = n.id
				break
			}
		}
		if isProjectTab(s.ActivePage) && target == projectPanesTree && slices.ContainsFunc(nodes, func(n fnode) bool { return n.id == siteScreen }) {
			target = siteScreen
		}
	}
	a.say("left page")
	return core.Next(withMode(withFocus(s, target), ModeNormal))
}

func (a *App) pageInteractiveNodes(s core.State) []string {
	root := a.View(s)
	content := root.Find("site.content")
	if content == nil {
		return nil
	}
	var ids []string
	var walk func(n *ui.ViewNode)
	walk = func(n *ui.ViewNode) {
		if n.ID != "" && n.ID != views.PageScrollID && n.Focusable {
			ids = append(ids, n.ID)
			if n.Kind == ui.KindCard {
				return
			}
		}
		for i := range n.Children {
			walk(&n.Children[i])
		}
	}
	walk(content)
	return ids
}

func (a *App) pageStepItem(delta int) func(core.State, core.Command) core.StepResult {
	return func(s core.State, _ core.Command) core.StepResult {
		items := a.pageInteractiveNodes(s)
		if len(items) == 0 {
			return core.Next(withFocus(s, views.PageScrollID))
		}
		cur := slices.Index(items, s.ActiveFrame().FocusID)
		var nextID string
		if cur < 0 {
			if delta >= 0 {
				nextID = items[0]
			} else {
				nextID = items[len(items)-1]
			}
		} else {
			idx := ((cur+delta)%len(items) + len(items)) % len(items)
			nextID = items[idx]
		}
		if !a.scrollPageReveal(s, a.activeSitePageIDFor(s), nextID) {
			return core.Next(s)
		}
		return core.Next(withFocus(s, nextID))
	}
}

func (a *App) tocGroupToggle(s core.State, _ core.Command) core.StepResult {
	focus := s.ActiveFrame().FocusID
	group, ok := strings.CutPrefix(focus, views.TOCGroupPrefix)
	if !ok || group == "" {
		return core.Next(s)
	}
	if a.panes.tocCollapsed == nil {
		a.panes.tocCollapsed = map[string]bool{}
	}
	a.panes.tocCollapsed[group] = !a.panes.tocCollapsed[group]
	if a.panes.tocCollapsed[group] {
		a.say("collapsed %s group", group)
	} else {
		a.say("expanded %s group", group)
	}
	return core.Next(s)
}
