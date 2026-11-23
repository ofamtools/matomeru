package app

import (
	"slices"
	"strings"

	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
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
	return []core.CommandSpec{
		spec(CmdPageLeave, "Leave the locked ui.page reading view and return to the pane TOC"),
		spec(CmdPageItemNext, "Focus the next interactive control inside the active ui.page"),
		spec(CmdPageItemPrev, "Focus the previous interactive control inside the active ui.page"),
		spec(CmdTOCGroupToggle, "Collapse or expand the focused pane TOC group"),
	}
}

func pageModeBindings() []core.Binding {
	all := ""
	return []core.Binding{
		// Pane-locked reading & scrolling inside ui.page (zero g-prefixed keys).
		bind(ModePage, all, "j", "motion.down", "scroll down"),
		bind(ModePage, all, "k", "motion.up", "scroll up"),
		hidden(bind(ModePage, all, "down", "motion.down", "scroll down")),
		hidden(bind(ModePage, all, "up", "motion.up", "scroll up")),
		bind(ModePage, all, "ctrl+e", "motion.scroll.down", "scroll down"),
		bind(ModePage, all, "ctrl+y", "motion.scroll.up", "scroll up"),
		bind(ModePage, all, "ctrl+d", "motion.half.down", "half page down"),
		bind(ModePage, all, "ctrl+u", "motion.half.up", "half page up"),
		bind(ModePage, all, "ctrl+f", "motion.page.down", "page down"),
		bind(ModePage, all, "ctrl+b", "motion.page.up", "page up"),
		hidden(bind(ModePage, all, "space", "motion.page.down", "page down")),
		bind(ModePage, all, "T", "motion.top", "top"),
		hidden(bind(ModePage, all, "home", "motion.top", "top")),
		bind(ModePage, all, "G", "motion.bottom", "bottom"),
		hidden(bind(ModePage, all, "end", "motion.bottom", "bottom")),
		bind(ModePage, all, "tab", CmdPageItemNext, "next control"),
		bind(ModePage, all, "shift+tab", CmdPageItemPrev, "previous control"),
		bind(ModePage, all, "n", CmdQuestionNext, "next question"),
		bind(ModePage, all, "p", CmdQuestionPrev, "previous question"),
		bind(ModePage, all, "enter", CmdAnswer, "answer"),
		bind(ModePage, all, "a", CmdAnswer, "answer"),
		bind(ModePage, all, "ctrl+s", CmdQuestionSubmitAll, "submit all answers"),
		bind(ModePage, all, "esc", CmdPageLeave, "leave page"),
		bind(ModePage, all, "q", CmdPageLeave, "leave page"),
		hidden(bind(ModePage, all, "backspace", CmdPageLeave, "leave page")),
		{Mode: ModePage, Keys: keys("ctrl+a"), Layer: &leader, Hint: "leader"},

		// Collapsible TOC group toggle.
		when(bind(ModeNormal, views.ScopeTOC, "space", CmdTOCGroupToggle, "fold/unfold TOC group"), "facts.tocGroup != null"),
	}
}

func (a *App) activeSitePageID() string {
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

func (a *App) scrollPageReveal(s core.State, pageID, targetID string) {
	if pageID == "" || targetID == "" {
		return
	}
	info, ok := s.Layout.Scrolls[views.PageScrollID]
	if !ok {
		return
	}
	y, ok := info.ChildY[targetID]
	if !ok {
		return
	}
	cur := a.pageScrollOffset(pageID)
	vh := max(info.ViewH, 1)
	if y < cur {
		cur = y
	} else if y >= cur+vh-2 {
		cur = max(0, y-vh/3)
	}
	a.setPageScrollOffset(pageID, min(cur, info.MaxOff))
}

func (a *App) pageScrollMotion(s core.State, c core.Command) core.StepResult {
	pageID := a.activeSitePageID()
	if pageID == "" || pageID == views.ScreenPageID {
		return core.Next(s)
	}
	info, hasInfo := s.Layout.Scrolls[views.PageScrollID]
	cur := a.pageScrollOffset(pageID)
	viewH := 20
	maxOff := 10000
	if hasInfo {
		if info.ViewH > 0 {
			viewH = info.ViewH
		}
		maxOff = info.MaxOff
		cur = min(cur, maxOff)
	}
	times := max(c.Times(), 1)
	half := max(1, viewH/2)
	pageStep := max(1, viewH-2)

	switch c.ID {
	case "motion.down", "motion.scroll.down":
		cur += times
	case "motion.up", "motion.scroll.up":
		cur -= times
	case "motion.half.down", "motion.block.next":
		cur += half * times
	case "motion.half.up", "motion.block.prev":
		cur -= half * times
	case "motion.page.down":
		cur += pageStep * times
	case "motion.page.up":
		cur -= pageStep * times
	case "motion.top":
		cur = 0
	case "motion.bottom":
		if hasInfo {
			cur = maxOff
		}
	default:
		return core.Next(s)
	}
	if cur < 0 {
		cur = 0
	}
	if hasInfo && cur > maxOff {
		cur = maxOff
	}
	a.setPageScrollOffset(pageID, cur)
	return core.Next(s)
}

func (a *App) pageLeave(s core.State, _ core.Command) core.StepResult {
	if a.Engine.Mode(s) != ModePage {
		return core.Next(s)
	}
	pageID := a.activeSitePageID()
	target := tocPrefix + pageID
	nodes := a.focusables(s)
	if !slices.ContainsFunc(nodes, func(n fnode) bool { return n.id == target }) {
		target = railTable
		for _, n := range nodes {
			if strings.HasPrefix(n.id, tocPrefix) {
				target = n.id
				break
			}
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
		a.scrollPageReveal(s, a.activeSitePageID(), nextID)
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
