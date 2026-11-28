package app

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
)

// Composable inline links, interactive canvas tabs, and direct heading traversal.
const (
	ScopeCanvasText = "canvas.text"

	CmdCanvasTabSelect   = "canvas.tabs.select"
	CmdCanvasHeadingNext = "canvas.heading.next"
	CmdCanvasHeadingPrev = "canvas.heading.prev"
	CmdCanvasHeadingPick = "canvas.heading.pick"
)

func canvasLinkSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		{
			ID:          CmdCanvasTabSelect,
			Description: "Select a tab on an interactive canvas tabs panel",
			Params: []core.ParamSpec{
				{Name: "id", Type: core.ParamString, Description: "ID of the canvas tabs node"},
				{Name: "active", Type: core.ParamString, Description: "Tab index, ID, or title to activate"},
			},
		},
		spec(CmdCanvasHeadingNext, "Jump to the next Markdown heading on the active canvas"),
		spec(CmdCanvasHeadingPrev, "Jump to the previous Markdown heading on the active canvas"),
		spec(CmdCanvasHeadingPick, "Pick a Markdown heading to jump to on the active canvas"),
	}
}

func canvasTreeNodesLen(raw any) int {
	switch xs := raw.(type) {
	case []ui.TreeNode:
		var count func(ns []ui.TreeNode) int
		count = func(ns []ui.TreeNode) int {
			total := 0
			for _, nd := range ns {
				total++
				exp := nd.Expanded == nil || *nd.Expanded
				if exp && len(nd.Children) > 0 {
					total += count(nd.Children)
				}
			}
			return total
		}
		return count(xs)
	case []any:
		var count func(ns []any) int
		count = func(ns []any) int {
			total := 0
			for _, elem := range ns {
				switch nd := elem.(type) {
				case ui.TreeNode:
					total++
					exp := nd.Expanded == nil || *nd.Expanded
					if exp && len(nd.Children) > 0 {
						total += canvasTreeNodesLen(nd.Children)
					}
				case map[string]any:
					total++
					exp := true
					if b, ok := nd["expanded"].(bool); ok {
						exp = b
					}
					if exp {
						if kids, ok := nd["children"].([]any); ok && len(kids) > 0 {
							total += count(kids)
						}
					}
				}
			}
			return total
		}
		return count(xs)
	}
	return 0
}

func inlineCellPlain(v any) string {
	if runs, ok := ui.DecodeInlineRuns(v); ok {
		plain, _, _ := ui.CompileInlineRuns(runs, -1)
		return plain
	}
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func syncCanvasWidgetLinks(n *ui.ViewNode, ws *canvasWidgetState, links []ui.InlineLink) {
	if n.Props == nil {
		n.Props = map[string]any{}
	}
	if n.Facts == nil {
		n.Facts = map[string]any{}
	}
	if len(links) == 0 {
		ws.activeLink = -1
		delete(n.Props, "activeLink")
		n.Facts["hasLinks"] = false
		n.Facts["linkCount"] = 0
		n.Facts["onLink"] = false
		return
	}
	if ws.activeLink < 0 {
		ws.activeLink = 0
	} else if ws.activeLink >= len(links) {
		ws.activeLink = len(links) - 1
	}
	n.Props["activeLink"] = ws.activeLink
	n.Facts["hasLinks"] = true
	n.Facts["linkCount"] = len(links)
	n.Facts["onLink"] = ws.activeLink >= 0
	if ws.activeLink >= 0 && ws.activeLink < len(links) {
		lnk := links[ws.activeLink]
		if lnk.Href != "" {
			n.Facts["activeLinkUrl"] = lnk.Href
		}
		if lnk.Label != "" {
			n.Facts["activeLinkLabel"] = lnk.Label
		}
	}
}

func canvasNodeRowForLinks(node *ui.ViewNode, ws *canvasWidgetState) int {
	if node == nil || ws == nil {
		return -1
	}
	if node.Kind == ui.KindTable || node.Kind == ui.KindTree {
		return ws.row
	}
	return -1
}

func canvasLinkTargetDesc(lnk ui.InlineLink) string {
	if lnk.Href != "" {
		return lnk.Href
	}
	if lnk.Ref != nil && lnk.Ref.ID != "" {
		return data.RefText(*lnk.Ref)
	}
	if lnk.Command != "" {
		return lnk.Command
	}
	return lnk.Label
}

func (a *App) canvasSayInlineLink(links []ui.InlineLink, idx int) {
	if idx < 0 || idx >= len(links) {
		return
	}
	lnk := links[idx]
	dest := canvasLinkTargetDesc(lnk)
	if dest != "" && dest != lnk.Label {
		a.say("link %d/%d · %s → %s", idx+1, len(links), lnk.Label, dest)
		return
	}
	a.say("link %d/%d · %s", idx+1, len(links), lnk.Label)
}

func (a *App) activateInlineLink(s core.State, lnk ui.InlineLink) core.StepResult {
	if lnk.Command != "" && lnk.Command != CmdCanvasActivate {
		cmd := core.Command{
			ID:     lnk.Command,
			Args:   lnk.Args,
			Source: "canvas",
			Actor:  User,
		}
		var appFacts map[string]any
		if a.Shell != nil && a.Shell.AppFacts != nil {
			appFacts = a.Shell.AppFacts(s)
		}
		chain := ui.ContextChain(a.View(s), s.ActiveFrame().FocusID, appFacts)
		return a.Engine.Step(s, chain, core.CommandEvent{Command: cmd})
	}
	if lnk.Ref != nil && lnk.Ref.ID != "" {
		ref := *lnk.Ref
		if ref.Kind == data.KindPage {
			return core.Next(a.openCanvasRefPage(s, ref.ID))
		}
		return core.Next(a.show(withMode(s, ModeNormal), ref))
	}
	if lnk.Href != "" {
		return core.Next(a.openCanvasURL(s, lnk.Href))
	}
	return core.Next(s)
}

func (a *App) openCanvasRefPage(s core.State, pageID string) core.State {
	s, inPane := a.navigateToPage(s, pageID)
	if !inPane {
		return s
	}
	a.setPageScrollOffset(pageID, 0)
	a.viewMemo.gen++
	if a.cfg != nil && a.cfg.Settings != nil && a.cfg.Settings.Bool(SetPageLockScroll, true) {
		focus := views.PageScrollID
		if first := a.firstCanvasWidgetID(s); first != "" {
			focus = first
		}
		return withMode(withFocus(s, focus), ModePage)
	}
	return withMode(withFocus(s, tocPrefix+pageID), ModeNormal)
}

func (a *App) canvasTabSelect(s core.State, c core.Command) core.StepResult {
	pageID := a.activeSitePageID()
	if pageID == "" || pageID == views.ScreenPageID {
		return core.Next(s)
	}
	id := strings.TrimSpace(c.Arg("id"))
	active, hasActive := c.Args["active"]
	if !hasActive || active == nil {
		return core.Next(s)
	}
	if id == "" {
		root := a.View(s)
		if content := root.Find("site.content"); content != nil {
			var findTabs func(n *ui.ViewNode) string
			findTabs = func(n *ui.ViewNode) string {
				if n.Kind == ui.KindTabs && n.ID != "" {
					return n.ID
				}
				for i := range n.Children {
					if found := findTabs(&n.Children[i]); found != "" {
						return found
					}
				}
				return ""
			}
			id = findTabs(content)
		}
	}
	if id == "" {
		return core.Next(s)
	}
	cs := a.canvasStateFor(pageID)
	if cs == nil {
		return core.Next(s)
	}
	if cs.tabsActive == nil {
		cs.tabsActive = map[string]any{}
	}
	cs.tabsActive[id] = active
	a.viewMemo.gen++
	a.say("tab → %v", active)
	return core.Next(s)
}

// --- Direct Canvas Heading / Section Traversal ----------------------------------

type canvasHeading struct {
	Index  int
	NodeID string
	Level  int
	Text   string
	Line   int
}

func activeCanvasTabIndex(n *ui.ViewNode) int {
	if n == nil || len(n.Children) == 0 {
		return -1
	}
	v, ok := n.Props["active"]
	if !ok || v == nil {
		return 0
	}
	switch x := v.(type) {
	case int:
		return min(max(x, 0), len(n.Children)-1)
	case float64:
		return min(max(int(x), 0), len(n.Children)-1)
	case string:
		if i, err := strconv.Atoi(x); err == nil {
			return min(max(i, 0), len(n.Children)-1)
		}
		for i := range n.Children {
			if n.Children[i].ID == x || n.Children[i].Title == x {
				return i
			}
		}
	}
	return 0
}

func collectCanvasHeadings(root ui.ViewNode) []canvasHeading {
	content := root.Find("site.content")
	if content == nil {
		return nil
	}
	var out []canvasHeading
	var walk func(n *ui.ViewNode)
	walk = func(n *ui.ViewNode) {
		if n.Kind == ui.KindMarkdown {
			md, _ := n.Props["markdown"].(string)
			for _, h := range ui.MarkdownHeadings(md) {
				out = append(out, canvasHeading{
					Index:  len(out),
					NodeID: n.ID,
					Level:  h.Level,
					Text:   h.Text,
					Line:   h.Line,
				})
			}
		}
		if n.Kind == ui.KindTabs {
			if idx := activeCanvasTabIndex(n); idx >= 0 && idx < len(n.Children) {
				walk(&n.Children[idx])
			}
			return
		}
		for i := range n.Children {
			walk(&n.Children[i])
		}
	}
	walk(content)
	return out
}

func (a *App) canvasHeadingTargetY(s core.State, h canvasHeading) (int, bool) {
	info, ok := s.Layout.Scrolls[views.PageScrollID]
	if !ok {
		return max(h.Line, 0), false
	}
	baseY := 0
	if h.NodeID != "" {
		if y, found := info.ChildY[h.NodeID]; found {
			baseY = y
		}
	}
	target := max(baseY+h.Line, 0)
	if target > info.MaxOff {
		target = info.MaxOff
	}
	return target, true
}

func (a *App) jumpCanvasHeading(s core.State, pageID string, headings []canvasHeading, idx int) core.State {
	if len(headings) == 0 {
		return s
	}
	idx = min(max(idx, 0), len(headings)-1)
	h := headings[idx]
	if cs := a.canvasStateFor(pageID); cs != nil {
		cs.headingIdx = idx
	}
	targetY, _ := a.canvasHeadingTargetY(s, h)
	a.setPageScrollOffset(pageID, targetY)
	a.say("heading %d/%d · %s", idx+1, len(headings), h.Text)
	return s
}

func (a *App) canvasHeadingStep(delta int) func(core.State, core.Command) core.StepResult {
	return func(s core.State, c core.Command) core.StepResult {
		pageID := a.activeSitePageID()
		if pageID == "" || pageID == views.ScreenPageID {
			return core.Next(s)
		}
		headings := collectCanvasHeadings(a.View(s))
		if len(headings) == 0 {
			a.say("no headings on this canvas")
			return core.Next(s)
		}
		cs := a.canvasStateFor(pageID)
		step := delta * max(c.Times(), 1)
		n := len(headings)
		nextIdx := 0
		if cs != nil && cs.headingIdx >= 0 && cs.headingIdx < n {
			nextIdx = ((cs.headingIdx+step)%n + n) % n
		} else {
			cur := a.pageScrollOffset(pageID)
			if delta >= 0 {
				nextIdx = 0
				for i, h := range headings {
					if y, ok := a.canvasHeadingTargetY(s, h); ok && y > cur {
						nextIdx = i
						break
					}
				}
			} else {
				nextIdx = n - 1
				for i := n - 1; i >= 0; i-- {
					if y, ok := a.canvasHeadingTargetY(s, headings[i]); ok && y < cur {
						nextIdx = i
						break
					}
				}
			}
		}
		return core.Next(a.jumpCanvasHeading(s, pageID, headings, nextIdx))
	}
}

func (a *App) canvasHeadingPick(s core.State, _ core.Command) core.StepResult {
	pageID := a.activeSitePageID()
	if pageID == "" || pageID == views.ScreenPageID {
		return core.Next(s)
	}
	headings := collectCanvasHeadings(a.View(s))
	if len(headings) == 0 {
		a.say("no headings on this canvas")
		return core.Next(s)
	}
	items := make([]picker.Item, len(headings))
	for i, h := range headings {
		indent := strings.Repeat("  ", max(0, h.Level-1))
		items[i] = picker.Item{
			ID:       strconv.Itoa(i),
			Title:    indent + h.Text,
			Badges:   []ui.Badge{{Text: fmt.Sprintf("H%d", h.Level), Tone: "accent"}},
			Keywords: []string{h.Text, fmt.Sprintf("h%d", h.Level)},
		}
	}
	pm := picker.New("Headings", items)
	pm.Placeholder = "jump to heading on canvas"
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       "canvas.headings",
		PageSize: 12,
		OnConfirm: func(picked picker.Item) modal.Result {
			idx, err := strconv.Atoi(picked.ID)
			if err == nil && idx >= 0 && idx < len(headings) {
				targetPage := pageID
				list := slices.Clone(headings)
				a.afterModal(func(st core.State) core.State {
					return a.jumpCanvasHeading(st, targetPage, list, idx)
				})
			}
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	return core.Next(a.pushModal(s, m))
}
