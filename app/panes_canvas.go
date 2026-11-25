package app

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/handlers"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

// Commands for interactive canvas widgets (tables, Markdown links, checkboxes).
const (
	CmdCanvasActivate    = "canvas.activate"
	CmdCanvasToggleCheck = "canvas.check.toggle"
	CmdCanvasLinkNext    = "canvas.link.next"
	CmdCanvasLinkPrev    = "canvas.link.prev"
	CmdCanvasYank        = "canvas.yank"
)

const (
	ScopeCanvasTable    = "canvas.table"
	ScopeCanvasMarkdown = "canvas.markdown"
	ScopeCanvasTree     = "canvas.tree"
)

type canvasWidgetState struct {
	row         int
	activeLink  int
	activeCheck int
	initialized bool
}

type canvasState struct {
	widgets    map[string]*canvasWidgetState
	tabsActive map[string]any
	headingIdx int
}

func canvasSpecs() []core.CommandSpec {
	out := []core.CommandSpec{
		spec(CmdCanvasActivate, "Open the active link, toggle the focused checkbox, or inspect the selected canvas table row"),
		spec(CmdCanvasToggleCheck, "Toggle and persist the focused Markdown checkbox on the active canvas"),
		spec(CmdCanvasLinkNext, "Focus the next inline or Markdown link on the active canvas"),
		spec(CmdCanvasLinkPrev, "Focus the previous inline or Markdown link on the active canvas"),
		spec(CmdCanvasYank, "Yank the active link URL, selected table row, or code block from the active canvas"),
	}
	return append(out, canvasLinkSpecs()...)
}

func (a *App) interactiveCanvasEnabled() bool {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return true
	}
	return a.cfg.Settings.Bool(SetInteractiveCanvas, true)
}

func (a *App) canvasStateFor(pageID string) *canvasState {
	if a.panes == nil || pageID == "" {
		return nil
	}
	if a.panes.canvasState == nil {
		a.panes.canvasState = map[string]*canvasState{}
	}
	cs := a.panes.canvasState[pageID]
	if cs == nil {
		cs = &canvasState{
			widgets:    map[string]*canvasWidgetState{},
			tabsActive: map[string]any{},
			headingIdx: -1,
		}
		a.panes.canvasState[pageID] = cs
	}
	return cs
}

func (a *App) canvasWidgetStateFor(pageID, nodeID string) *canvasWidgetState {
	cs := a.canvasStateFor(pageID)
	if cs == nil || nodeID == "" {
		return nil
	}
	ws := cs.widgets[nodeID]
	if ws == nil {
		ws = &canvasWidgetState{activeLink: -1, activeCheck: -1}
		cs.widgets[nodeID] = ws
	}
	return ws
}

func (a *App) canvasViewKey(pageID string) string {
	if a.panes == nil || a.panes.canvasState == nil || pageID == "" {
		return ""
	}
	cs := a.panes.canvasState[pageID]
	if cs == nil || (len(cs.widgets) == 0 && len(cs.tabsActive) == 0 && cs.headingIdx < 0) {
		return ""
	}
	var b strings.Builder
	if len(cs.widgets) > 0 {
		keys := make([]string, 0, len(cs.widgets))
		for k := range cs.widgets {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			ws := cs.widgets[k]
			fmt.Fprintf(&b, "|cw:%s:%d:%d:%d", k, ws.row, ws.activeLink, ws.activeCheck)
		}
	}
	if len(cs.tabsActive) > 0 {
		keys := make([]string, 0, len(cs.tabsActive))
		for k := range cs.tabsActive {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "|ct:%s:%v", k, cs.tabsActive[k])
		}
	}
	if cs.headingIdx >= 0 {
		fmt.Fprintf(&b, "|ch:%d", cs.headingIdx)
	}
	return b.String()
}

func canvasRowsLen(n *ui.ViewNode) int {
	if n == nil || n.Props == nil {
		return 0
	}
	switch x := n.Props["rows"].(type) {
	case []any:
		return len(x)
	case []ui.TableRowSpec:
		return len(x)
	case [][]string:
		return len(x)
	case [][]any:
		return len(x)
	}
	return 0
}

func canvasTreeItemsLen(n *ui.ViewNode) int {
	if n == nil || n.Props == nil {
		return 0
	}
	if cnt := canvasTreeNodesLen(n.Props["nodes"]); cnt > 0 {
		return cnt
	}
	switch xs := n.Props["items"].(type) {
	case []any:
		return len(xs)
	case []string:
		return len(xs)
	}
	return 0
}

func ensureCanvasNodeID(n *ui.ViewNode, path string) string {
	if n.ID == "" {
		n.ID = "canvas.node." + path
	}
	return n.ID
}

// enhanceCanvasSite enriches a non-screen canvas ViewNode tree so that tables,
// Markdown links/checkboxes, and trees published by any agent are automatically
// focusable and interactive without requiring special agent-side flags.
func (a *App) enhanceCanvasSite(site *ui.ViewNode, pageID string) {
	if site == nil || pageID == "" || pageID == views.ScreenPageID {
		return
	}
	content := site.Find("site.content")
	if content == nil {
		return
	}
	interactive := a.interactiveCanvasEnabled()
	hasQuestions := false

	var walk func(n *ui.ViewNode, path string)
	walk = func(n *ui.ViewNode, path string) {
		if n.Scope == mcpui.ScopeQuestion {
			hasQuestions = true
			return
		}
		if n.Kind == ui.KindTabs && n.ID != views.PageScrollID {
			id := ensureCanvasNodeID(n, path)
			if a.panes != nil && a.panes.canvasState != nil {
				if cs := a.panes.canvasState[pageID]; cs != nil {
					if act, ok := cs.tabsActive[id]; ok {
						if n.Props == nil {
							n.Props = map[string]any{}
						}
						n.Props["active"] = act
					}
				}
			}
		}
		if interactive && n.ID != views.PageScrollID {
			switch n.Kind {
			case ui.KindTable:
				if rows := canvasRowsLen(n); rows > 0 {
					id := ensureCanvasNodeID(n, path)
					ws := a.canvasWidgetStateFor(pageID, id)
					ws.row = min(max(ws.row, 0), rows-1)
					ws.initialized = true
					n.Focusable = true
					if n.Scope == "" {
						n.Scope = ScopeCanvasTable
					}
					if n.Props == nil {
						n.Props = map[string]any{}
					}
					n.Props["selected"] = ws.row
					if n.Facts == nil {
						n.Facts = map[string]any{}
					}
					n.Facts["canvasTable"] = true
					n.Facts["rowCount"] = rows
					n.Facts["selectedRow"] = ws.row
					syncCanvasWidgetLinks(n, ws, ui.NodeLinks(n, ws.row))
				}
			case ui.KindText:
				if links := ui.NodeLinks(n, -1); len(links) > 0 {
					id := ensureCanvasNodeID(n, path)
					ws := a.canvasWidgetStateFor(pageID, id)
					ws.initialized = true
					n.Focusable = true
					if n.Scope == "" {
						n.Scope = ScopeCanvasText
					}
					if n.Facts == nil {
						n.Facts = map[string]any{}
					}
					n.Facts["canvasText"] = true
					syncCanvasWidgetLinks(n, ws, links)
				}
			case ui.KindMarkdown:
				md, _ := n.Props["markdown"].(string)
				links := ui.MarkdownLinks(md)
				checks := ui.MarkdownCheckboxes(md)
				if len(links) > 0 || len(checks) > 0 {
					id := ensureCanvasNodeID(n, path)
					ws := a.canvasWidgetStateFor(pageID, id)
					if !ws.initialized {
						ws.initialized = true
						if len(checks) > 0 {
							ws.activeCheck = 0
							ws.activeLink = -1
						} else if len(links) > 0 {
							ws.activeLink = 0
							ws.activeCheck = -1
						}
					}
					if len(checks) == 0 {
						ws.activeCheck = -1
					} else if ws.activeCheck >= len(checks) {
						ws.activeCheck = len(checks) - 1
					}
					if len(links) == 0 {
						ws.activeLink = -1
					} else if ws.activeLink >= len(links) {
						ws.activeLink = len(links) - 1
					}
					if ws.activeCheck < 0 && ws.activeLink < 0 {
						if len(checks) > 0 {
							ws.activeCheck = 0
						} else if len(links) > 0 {
							ws.activeLink = 0
						}
					}
					n.Focusable = true
					if n.Scope == "" {
						n.Scope = ScopeCanvasMarkdown
					}
					if n.Props == nil {
						n.Props = map[string]any{}
					}
					n.Props["activeLink"] = ws.activeLink
					n.Props["activeCheck"] = ws.activeCheck
					if n.Facts == nil {
						n.Facts = map[string]any{}
					}
					n.Facts["canvasMarkdown"] = true
					n.Facts["hasLinks"] = len(links) > 0
					n.Facts["hasCheckboxes"] = len(checks) > 0
					n.Facts["linkCount"] = len(links)
					n.Facts["checkboxCount"] = len(checks)
					n.Facts["onCheckbox"] = ws.activeCheck >= 0
					n.Facts["onLink"] = ws.activeLink >= 0
					if ws.activeLink >= 0 && ws.activeLink < len(links) {
						n.Facts["activeLinkUrl"] = links[ws.activeLink].Href
						n.Facts["activeLinkLabel"] = links[ws.activeLink].Label
					}
				} else if len(ui.MarkdownHeadings(md)) > 0 {
					ensureCanvasNodeID(n, path)
				}
			case ui.KindTree:
				if items := canvasTreeItemsLen(n); items > 0 {
					id := ensureCanvasNodeID(n, path)
					ws := a.canvasWidgetStateFor(pageID, id)
					ws.row = min(max(ws.row, 0), items-1)
					ws.initialized = true
					n.Focusable = true
					if n.Scope == "" {
						n.Scope = ScopeCanvasTree
					}
					if n.Props == nil {
						n.Props = map[string]any{}
					}
					n.Props["selected"] = ws.row
					if n.Facts == nil {
						n.Facts = map[string]any{}
					}
					n.Facts["canvasTree"] = true
					n.Facts["rowCount"] = items
					syncCanvasWidgetLinks(n, ws, ui.NodeLinks(n, ws.row))
				}
			}
		}
		for i := range n.Children {
			childPath := fmt.Sprintf("%d", i)
			if path != "" {
				childPath = path + "." + childPath
			}
			walk(&n.Children[i], childPath)
		}
	}
	walk(content, "")
	if content.Facts == nil {
		content.Facts = map[string]any{}
	}
	content.Facts["hasQuestions"] = hasQuestions
}

// firstCanvasWidgetID returns the first interactive table, markdown, or tree
// widget ID inside site.content, or "" when the canvas only has static content
// (or bottom-of-page question cards).
func (a *App) firstCanvasWidgetID(s core.State) string {
	if !a.interactiveCanvasEnabled() {
		return ""
	}
	root := a.View(s)
	content := root.Find("site.content")
	if content == nil {
		return ""
	}
	var found string
	var walk func(n *ui.ViewNode)
	walk = func(n *ui.ViewNode) {
		if found != "" {
			return
		}
		if n.Focusable && n.ID != "" && n.ID != views.PageScrollID {
			if n.Facts["canvasTable"] == true || n.Facts["canvasMarkdown"] == true || n.Facts["canvasTree"] == true || n.Facts["canvasText"] == true {
				found = n.ID
				return
			}
		}
		for i := range n.Children {
			walk(&n.Children[i])
		}
	}
	walk(content)
	return found
}

// mdItem represents one interactive row in a Markdown node (either a checkbox
// line or a line containing one or more links).
type mdItem struct {
	line     int
	checkIdx int // >=0 when this line is a checkbox
	linkIdxs []int
}

func buildMarkdownItems(links []ui.MarkdownLink, checks []ui.MarkdownCheckbox) []mdItem {
	byLine := map[int]*mdItem{}
	var lines []int
	ensure := func(ln int) *mdItem {
		if it, ok := byLine[ln]; ok {
			return it
		}
		it := &mdItem{line: ln, checkIdx: -1}
		byLine[ln] = it
		lines = append(lines, ln)
		return it
	}
	for _, ck := range checks {
		it := ensure(ck.Line)
		it.checkIdx = ck.Index
	}
	for _, lk := range links {
		it := ensure(lk.Line)
		it.linkIdxs = append(it.linkIdxs, lk.Index)
	}
	slices.Sort(lines)
	out := make([]mdItem, 0, len(lines))
	for _, ln := range lines {
		out = append(out, *byLine[ln])
	}
	return out
}

func currentMarkdownItemIdx(items []mdItem, ws *canvasWidgetState) int {
	for i, it := range items {
		if ws.activeCheck >= 0 && it.checkIdx == ws.activeCheck {
			return i
		}
		if ws.activeLink >= 0 && slices.Contains(it.linkIdxs, ws.activeLink) {
			return i
		}
	}
	return 0
}

func applyMarkdownItem(ws *canvasWidgetState, it mdItem) {
	if it.checkIdx >= 0 {
		ws.activeCheck = it.checkIdx
		ws.activeLink = -1
		return
	}
	if len(it.linkIdxs) > 0 {
		ws.activeCheck = -1
		ws.activeLink = it.linkIdxs[0]
	}
}

func (a *App) canvasRevealNodeLine(s core.State, pageID, nodeID string, relY int) {
	info, ok := s.Layout.Scrolls[views.PageScrollID]
	if !ok {
		return
	}
	baseY, ok := info.ChildY[nodeID]
	if !ok {
		return
	}
	targetY := baseY + max(relY, 0)
	cur := a.pageScrollOffset(pageID)
	vh := max(info.ViewH, 1)
	if targetY < cur {
		cur = targetY
	} else if targetY >= cur+vh-2 {
		cur = max(0, targetY-vh+3)
	}
	a.setPageScrollOffset(pageID, min(cur, info.MaxOff))
}

func (a *App) canvasSayMarkdownFocus(links []ui.MarkdownLink, checks []ui.MarkdownCheckbox, ws *canvasWidgetState) {
	if ws.activeLink >= 0 && ws.activeLink < len(links) {
		lk := links[ws.activeLink]
		a.say("link %d/%d · %s → %s", ws.activeLink+1, len(links), lk.Label, lk.Href)
		return
	}
	if ws.activeCheck >= 0 && ws.activeCheck < len(checks) {
		ck := checks[ws.activeCheck]
		state := "[ ]"
		if ck.Checked {
			state = "[x]"
		}
		a.say("item %d/%d · %s %s · space/x toggles", ws.activeCheck+1, len(checks), state, clip(ck.Text, 64))
	}
}

// canvasMotion handles j/k/T/G on interactive canvas widgets (KindTable,
// KindMarkdown, KindTree) inside ModePage. Returns (result, true) if handled.
func (a *App) canvasMotion(s core.State, c core.Command) (core.StepResult, bool) {
	if !a.interactiveCanvasEnabled() {
		return core.Next(s), false
	}
	switch c.ID {
	case "motion.down", "motion.up", "motion.top", "motion.bottom":
	default:
		return core.Next(s), false
	}
	pageID := a.activeSitePageID()
	focusID := s.ActiveFrame().FocusID
	if pageID == "" || pageID == views.ScreenPageID || focusID == "" || focusID == views.PageScrollID {
		return core.Next(s), false
	}
	root := a.View(s)
	node := root.Find(focusID)
	if node == nil {
		return core.Next(s), false
	}
	times := max(c.Times(), 1)
	switch {
	case node.Facts["canvasTable"] == true || node.Facts["canvasTree"] == true:
		count := canvasRowsLen(node)
		if node.Facts["canvasTree"] == true {
			count = canvasTreeItemsLen(node)
		}
		if count <= 0 {
			return core.Next(s), false
		}
		ws := a.canvasWidgetStateFor(pageID, focusID)
		cur := min(max(ws.row, 0), count-1)
		switch c.ID {
		case "motion.down":
			if cur >= count-1 {
				if nextID, ok := a.canvasAdjacentWidget(s, focusID, 1); ok {
					a.scrollPageReveal(s, pageID, nextID)
					return core.Next(withFocus(s, nextID)), true
				}
				return core.Next(s), true
			}
			ws.row = min(count-1, cur+times)
		case "motion.up":
			if cur <= 0 {
				if prevID, ok := a.canvasAdjacentWidget(s, focusID, -1); ok {
					a.scrollPageReveal(s, pageID, prevID)
					return core.Next(withFocus(s, prevID)), true
				}
				return core.Next(s), true
			}
			ws.row = max(0, cur-times)
		case "motion.top":
			ws.row = 0
		case "motion.bottom":
			ws.row = count - 1
		}
		ws.activeLink = 0
		a.canvasRevealNodeLine(s, pageID, focusID, ws.row+2)
		if links := ui.NodeLinks(node, ws.row); len(links) > 0 {
			a.canvasSayInlineLink(links, 0)
		} else {
			ws.activeLink = -1
			a.say("row %d/%d · enter inspects · y yanks · tab next block", ws.row+1, count)
		}
		return core.Next(s), true

	case node.Facts["canvasText"] == true:
		switch c.ID {
		case "motion.down":
			if nextID, ok := a.canvasAdjacentWidget(s, focusID, 1); ok {
				a.scrollPageReveal(s, pageID, nextID)
				return core.Next(withFocus(s, nextID)), true
			}
		case "motion.up":
			if prevID, ok := a.canvasAdjacentWidget(s, focusID, -1); ok {
				a.scrollPageReveal(s, pageID, prevID)
				return core.Next(withFocus(s, prevID)), true
			}
		}
		return core.Next(s), false

	case node.Facts["canvasMarkdown"] == true:
		md, _ := node.Props["markdown"].(string)
		links := ui.MarkdownLinks(md)
		checks := ui.MarkdownCheckboxes(md)
		items := buildMarkdownItems(links, checks)
		if len(items) == 0 {
			return core.Next(s), false
		}
		ws := a.canvasWidgetStateFor(pageID, focusID)
		cur := currentMarkdownItemIdx(items, ws)
		switch c.ID {
		case "motion.down":
			if cur >= len(items)-1 {
				if nextID, ok := a.canvasAdjacentWidget(s, focusID, 1); ok {
					a.scrollPageReveal(s, pageID, nextID)
					return core.Next(withFocus(s, nextID)), true
				}
				return core.Next(s), true
			}
			cur = min(len(items)-1, cur+times)
		case "motion.up":
			if cur <= 0 {
				if prevID, ok := a.canvasAdjacentWidget(s, focusID, -1); ok {
					a.scrollPageReveal(s, pageID, prevID)
					return core.Next(withFocus(s, prevID)), true
				}
				return core.Next(s), true
			}
			cur = max(0, cur-times)
		case "motion.top":
			cur = 0
		case "motion.bottom":
			cur = len(items) - 1
		}
		applyMarkdownItem(ws, items[cur])
		a.canvasRevealNodeLine(s, pageID, focusID, items[cur].line)
		a.canvasSayMarkdownFocus(links, checks, ws)
		return core.Next(s), true
	}
	return core.Next(s), false
}

func (a *App) canvasAdjacentWidget(s core.State, curID string, delta int) (string, bool) {
	items := a.pageInteractiveNodes(s)
	idx := slices.Index(items, curID)
	if idx < 0 {
		return "", false
	}
	next := idx + delta
	if next < 0 || next >= len(items) {
		return "", false
	}
	return items[next], true
}

func (a *App) canvasStepLink(delta int) func(core.State, core.Command) core.StepResult {
	return func(s core.State, c core.Command) core.StepResult {
		pageID := a.activeSitePageID()
		focusID := s.ActiveFrame().FocusID
		root := a.View(s)
		node := root.Find(focusID)
		if node == nil {
			return core.Next(s)
		}
		ws := a.canvasWidgetStateFor(pageID, focusID)
		if ws == nil {
			return core.Next(s)
		}
		step := delta * max(c.Times(), 1)
		if node.Facts["canvasMarkdown"] == true {
			md, _ := node.Props["markdown"].(string)
			links := ui.MarkdownLinks(md)
			if len(links) == 0 {
				a.say("no links in this block")
				return core.Next(s)
			}
			checks := ui.MarkdownCheckboxes(md)
			if ws.activeLink < 0 {
				if delta >= 0 {
					ws.activeLink = 0
				} else {
					ws.activeLink = len(links) - 1
				}
			} else {
				ws.activeLink = ((ws.activeLink+step)%len(links) + len(links)) % len(links)
			}
			ws.activeCheck = -1
			a.canvasRevealNodeLine(s, pageID, focusID, links[ws.activeLink].Line)
			a.canvasSayMarkdownFocus(links, checks, ws)
			return core.Next(s)
		}
		links := ui.NodeLinks(node, canvasNodeRowForLinks(node, ws))
		if len(links) == 0 {
			a.say("no links in this block")
			return core.Next(s)
		}
		if ws.activeLink < 0 {
			if delta >= 0 {
				ws.activeLink = 0
			} else {
				ws.activeLink = len(links) - 1
			}
		} else {
			ws.activeLink = ((ws.activeLink+step)%len(links) + len(links)) % len(links)
		}
		a.canvasSayInlineLink(links, ws.activeLink)
		return core.Next(s)
	}
}

func (a *App) canvasToggleCheck(s core.State, _ core.Command) core.StepResult {
	pageID := a.activeSitePageID()
	focusID := s.ActiveFrame().FocusID
	if pageID == "" || pageID == views.ScreenPageID {
		return core.Next(s)
	}
	root := a.View(s)
	node := root.Find(focusID)
	if node == nil || node.Facts["canvasMarkdown"] != true {
		a.say("no checkbox focused")
		return core.Next(s)
	}
	md, _ := node.Props["markdown"].(string)
	checks := ui.MarkdownCheckboxes(md)
	if len(checks) == 0 {
		a.say("no checkboxes in this block")
		return core.Next(s)
	}
	ws := a.canvasWidgetStateFor(pageID, focusID)
	idx := ws.activeCheck
	if idx < 0 || idx >= len(checks) {
		idx = 0
	}
	updated, checked, ok := ui.ToggleMarkdownCheckbox(md, idx)
	if !ok {
		return core.Next(s)
	}
	if err := a.persistCanvasMarkdown(pageID, focusID, updated); err != nil {
		a.fail("save checkbox", err)
		return core.Next(s)
	}
	ws.activeCheck = idx
	ws.activeLink = -1
	a.viewMemo.gen++
	verb := "unchecked"
	if checked {
		verb = "checked"
	}
	a.say("%s: %s", verb, clip(checks[idx].Text, 64))
	return core.Next(s)
}

func (a *App) persistCanvasMarkdown(pageID, targetNodeID, newMarkdown string) error {
	ref := core.Ref{Kind: data.KindPage, ID: pageID}
	page, err := a.Store.Get(ref)
	if err != nil {
		return err
	}
	rawRoot, ok := page.Value("root")
	if !ok || rawRoot == nil {
		return fmt.Errorf("page %s has no root", pageID)
	}
	b, err := json.Marshal(rawRoot)
	if err != nil {
		return err
	}
	var root ui.ViewNode
	if err := json.Unmarshal(b, &root); err != nil {
		return err
	}
	replaced := false
	var walk func(n *ui.ViewNode, path string)
	walk = func(n *ui.ViewNode, path string) {
		if replaced {
			return
		}
		id := n.ID
		if id == "" {
			id = "canvas.node." + path
		}
		if n.Kind == ui.KindMarkdown && id == targetNodeID {
			if n.Props == nil {
				n.Props = map[string]any{}
			}
			n.Props["markdown"] = newMarkdown
			replaced = true
			return
		}
		for i := range n.Children {
			childPath := fmt.Sprintf("%d", i)
			if path != "" {
				childPath = path + "." + childPath
			}
			walk(&n.Children[i], childPath)
		}
	}
	// Mirrors enhanceCanvasSite's path numbering inside site.content -> PageScrollID -> root.
	walk(&root, "0.0")
	if !replaced {
		walk(&root, "0")
	}
	if !replaced {
		walk(&root, "")
	}
	if !replaced {
		return fmt.Errorf("markdown node %s not found in page %s", targetNodeID, pageID)
	}
	_, err = a.Router.UpdatePage(User, mcpui.UpdatePageArgs{
		Page: pageID,
		Mode: mcpui.ModeReplace,
		Root: &root,
	})
	return err
}

var canvasURLRe = regexp.MustCompile(`https?://[^\s<>)\]]+`)

func tableRowValues(node *ui.ViewNode, rowIdx int) ([]string, []string) {
	if node == nil || node.Props == nil {
		return nil, nil
	}
	var cols []string
	if rawCols, ok := node.Props["columns"].([]any); ok {
		for _, c := range rawCols {
			switch x := c.(type) {
			case map[string]any:
				title := fmt.Sprint(x["title"])
				if title == "" || title == "<nil>" {
					title = fmt.Sprint(x["key"])
				}
				cols = append(cols, title)
			default:
				cols = append(cols, fmt.Sprint(c))
			}
		}
	}
	rows, ok := node.Props["rows"].([]any)
	if !ok || rowIdx < 0 || rowIdx >= len(rows) {
		return cols, nil
	}
	var vals []string
	switch x := rows[rowIdx].(type) {
	case map[string]any:
		if cells, ok := x["cells"].([]any); ok {
			for _, v := range cells {
				vals = append(vals, inlineCellPlain(v))
			}
		} else if rawCols, ok := node.Props["columns"].([]any); ok {
			for _, c := range rawCols {
				key := ""
				if m, ok := c.(map[string]any); ok {
					if k, ok := m["key"].(string); ok {
						key = k
					} else if k, ok := m["title"].(string); ok {
						key = k
					}
				} else {
					key = fmt.Sprint(c)
				}
				if v, exists := x[key]; exists && v != nil {
					vals = append(vals, inlineCellPlain(v))
				} else {
					vals = append(vals, "")
				}
			}
		}
	case []any:
		for _, v := range x {
			vals = append(vals, inlineCellPlain(v))
		}
	}
	return cols, vals
}

func (a *App) openCanvasURL(s core.State, href string) core.State {
	target := strings.TrimSpace(href)
	if target == "" {
		return s
	}
	if !handlers.IsURL(target) {
		target = expandHome(target)
	}
	rules := a.cfg.Handlers.Match(target)
	if len(rules) == 0 {
		a.say("no handler opens %s", target)
		return s
	}
	if rules[0].Kind == handlers.KindBuiltin && rules[0].Name == handlers.MarkdownReader {
		return a.openReader(s, openTarget{path: target, title: filepath.Base(target)})
	}
	return a.runHandler(s, rules[0], openTarget{path: target, title: target})
}

func (a *App) canvasActivate(s core.State, c core.Command) core.StepResult {
	pageID := a.activeSitePageID()
	focusID := s.ActiveFrame().FocusID
	root := a.View(s)
	node := root.Find(focusID)
	if node == nil {
		return core.Next(s)
	}
	switch {
	case node.Facts["canvasMarkdown"] == true:
		md, _ := node.Props["markdown"].(string)
		links := ui.MarkdownLinks(md)
		ws := a.canvasWidgetStateFor(pageID, focusID)
		if ws.activeLink >= 0 && ws.activeLink < len(links) {
			return core.Next(a.openCanvasURL(s, links[ws.activeLink].Href))
		}
		if ws.activeCheck >= 0 {
			return a.canvasToggleCheck(s, c)
		}
		if len(links) > 0 {
			return core.Next(a.openCanvasURL(s, links[0].Href))
		}
		return core.Next(s)

	case node.Facts["canvasTable"] == true:
		ws := a.canvasWidgetStateFor(pageID, focusID)
		if links := ui.NodeLinks(node, ws.row); len(links) > 0 {
			idx := ws.activeLink
			if idx < 0 || idx >= len(links) {
				idx = 0
			}
			return a.activateInlineLink(s, links[idx])
		}
		cols, vals := tableRowValues(node, ws.row)
		for _, v := range vals {
			if u := canvasURLRe.FindString(v); u != "" {
				return core.Next(a.openCanvasURL(s, u))
			}
		}
		if len(vals) == 0 {
			return core.Next(s)
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "# Row %d\n\n", ws.row+1)
		for i, v := range vals {
			label := fmt.Sprintf("Column %d", i+1)
			if i < len(cols) && cols[i] != "" {
				label = cols[i]
			}
			fmt.Fprintf(&sb, "- **%s:** %s\n", label, v)
		}
		return core.Next(a.openReader(s, openTarget{title: fmt.Sprintf("Row %d", ws.row+1), body: sb.String()}))

	case node.Facts["canvasTree"] == true || node.Facts["canvasText"] == true:
		ws := a.canvasWidgetStateFor(pageID, focusID)
		if links := ui.NodeLinks(node, canvasNodeRowForLinks(node, ws)); len(links) > 0 {
			idx := ws.activeLink
			if idx < 0 || idx >= len(links) {
				idx = 0
			}
			return a.activateInlineLink(s, links[idx])
		}
	}
	return core.Next(s)
}

func (a *App) canvasYank(s core.State, _ core.Command) core.StepResult {
	pageID := a.activeSitePageID()
	focusID := s.ActiveFrame().FocusID
	root := a.View(s)
	node := root.Find(focusID)
	if node == nil {
		return core.Next(s)
	}
	switch {
	case node.Facts["canvasMarkdown"] == true:
		md, _ := node.Props["markdown"].(string)
		links := ui.MarkdownLinks(md)
		ws := a.canvasWidgetStateFor(pageID, focusID)
		if ws.activeLink >= 0 && ws.activeLink < len(links) {
			a.yank(links[ws.activeLink].Href, "link url")
			return core.Next(s)
		}
		a.yank(md, "markdown")
		return core.Next(s)

	case node.Facts["canvasTable"] == true:
		ws := a.canvasWidgetStateFor(pageID, focusID)
		if links := ui.NodeLinks(node, ws.row); len(links) > 0 && ws.activeLink >= 0 && ws.activeLink < len(links) {
			a.yank(canvasLinkTargetDesc(links[ws.activeLink]), "link")
			return core.Next(s)
		}
		cols, vals := tableRowValues(node, ws.row)
		if len(vals) == 0 {
			return core.Next(s)
		}
		parts := make([]string, 0, len(vals))
		for i, v := range vals {
			if i < len(cols) && cols[i] != "" {
				parts = append(parts, cols[i]+": "+v)
			} else {
				parts = append(parts, v)
			}
		}
		a.yank(strings.Join(parts, " | "), "table row")
		return core.Next(s)

	case node.Facts["canvasTree"] == true || node.Facts["canvasText"] == true:
		ws := a.canvasWidgetStateFor(pageID, focusID)
		if links := ui.NodeLinks(node, canvasNodeRowForLinks(node, ws)); len(links) > 0 {
			idx := ws.activeLink
			if idx < 0 || idx >= len(links) {
				idx = 0
			}
			a.yank(canvasLinkTargetDesc(links[idx]), "link")
			return core.Next(s)
		}
	}
	return core.Next(s)
}
