package app

import (
	"strconv"
	"strings"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

// The Show page's scroll containers (show.related, and show.props.scroll in
// classic layout) are followed by the renderer: it remembers each column's
// drawn offset and reveals the focused node with ui.scrolloff padding. The app
// only holds an explicit offset while the user scrolls with the scroll keys
// (showScrollMotion); a focus move drops it, so the renderer continues from
// where the column was drawn.

// showScrollSlot returns the explicit offset slot of one of the Show page's
// scroll containers, or nil for any other container.
func (a *App) showScrollSlot(scrollID string) **int {
	switch scrollID {
	case "show.related":
		return &a.nav.showRelatedOff
	case "show.props.scroll":
		return &a.nav.showPropsOff
	}
	return nil
}

// clearShowScroll drops both explicit Show offsets, handing the columns back
// to the renderer's focus follow. It reports whether anything was dropped.
func (a *App) clearShowScroll() bool {
	if a.nav.showRelatedOff == nil && a.nav.showPropsOff == nil {
		return false
	}
	a.nav.showRelatedOff, a.nav.showPropsOff = nil, nil
	return true
}

// showScrollOffsetText renders an explicit offset for the view memo key.
func showScrollOffsetText(off *int) string {
	if off == nil {
		return "-"
	}
	return strconv.Itoa(*off)
}

func (a *App) showFoldToggle(s core.State, _ core.Command) core.StepResult {
	a.nav.showEmptyFoldExpanded = !a.nav.showEmptyFoldExpanded
	a.viewMemo.gen++
	if a.nav.showEmptyFoldExpanded {
		a.say("showing empty relations")
	} else {
		a.say("hiding empty relations")
	}
	return core.Next(s)
}

func (a *App) showYank(s core.State, _ core.Command) core.StepResult {
	root := a.View(s)
	focusID := s.ActiveFrame().FocusID
	if n := root.Find(focusID); n != nil {
		if text, ok := n.Facts["copy"].(string); ok && text != "" {
			what, _ := n.Facts["label"].(string)
			if what == "" {
				what = "value"
			}
			a.yank(text, what)
			return core.Next(s)
		}
	}
	return a.yankPick(s, core.Command{})
}

func (a *App) showYankBody(s core.State, _ core.Command) core.StepResult {
	root := a.View(s)
	var bodies []string
	var walk func(n ui.ViewNode)
	walk = func(n ui.ViewNode) {
		if strings.HasPrefix(n.ID, "show.content.") && n.Scope == "show.content" {
			if raw, ok := n.Facts["copy"].(string); ok && strings.TrimSpace(raw) != "" {
				bodies = append(bodies, raw)
			}
		}
		for _, ch := range n.Children {
			walk(ch)
		}
	}
	walk(root)
	if len(bodies) == 0 {
		a.say("no body content to yank")
		return core.Next(s)
	}
	a.yank(strings.Join(bodies, "\n\n"), "body")
	return core.Next(s)
}

func (a *App) showRegionJump(delta int) func(core.State, core.Command) core.StepResult {
	return func(s core.State, _ core.Command) core.StepResult {
		root := a.View(s)
		var anchors []string
		var firstIn func(n ui.ViewNode) string
		firstIn = func(n ui.ViewNode) string {
			if n.Focusable && n.ID != "" {
				return n.ID
			}
			for _, ch := range n.Children {
				if id := firstIn(ch); id != "" {
					return id
				}
			}
			return ""
		}
		var collect func(n ui.ViewNode)
		collect = func(n ui.ViewNode) {
			if n.ID == showHeader || n.ID == "show.props" || (strings.HasPrefix(n.ID, "show.content.") && n.Scope == "show.content") || n.ID == "show.connections" || n.ID == "show.timeline" {
				if id := firstIn(n); id != "" {
					anchors = append(anchors, id)
				}
				return
			}
			for _, ch := range n.Children {
				collect(ch)
			}
		}
		collect(root)
		if len(anchors) == 0 {
			return core.Next(s)
		}
		cur := s.ActiveFrame().FocusID
		idx := 0
		for i, id := range anchors {
			if id == cur {
				idx = i
				break
			}
		}
		nextIdx := (idx + delta + len(anchors)) % len(anchors)
		a.clearShowScroll()
		return core.Next(withFocus(s, anchors[nextIdx]))
	}
}
