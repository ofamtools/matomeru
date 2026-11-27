package app

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
)

const (
	CmdSplitWiden  = "split.widen"
	CmdSplitNarrow = "split.narrow"
	CmdSplitGrow   = "split.grow"
	CmdSplitShrink = "split.shrink"
	CmdSplitReset  = "split.reset"

	splitRatioDefaultStep = 0.05
	splitRatioMin         = 0.12
	splitRatioMax         = 0.85
)

func splitResizeSpecs() []core.CommandSpec {
	stepParam := []core.ParamSpec{{
		Name:        "step",
		Type:        core.ParamInt,
		Default:     5,
		Description: "Percentage points to adjust the split by (default 5)",
	}}
	return []core.CommandSpec{
		{ID: CmdSplitWiden, Description: "Widen the focused split pane horizontally", Params: stepParam},
		{ID: CmdSplitNarrow, Description: "Narrow the focused split pane horizontally", Params: stepParam},
		{ID: CmdSplitGrow, Description: "Grow the focused split pane vertically (falls back to horizontal split)", Params: stepParam},
		{ID: CmdSplitShrink, Description: "Shrink the focused split pane vertically (falls back to horizontal split)", Params: stepParam},
		spec(CmdSplitReset, "Reset resized UI splits on the current view to their default ratios"),
	}
}

func splitResizeBindings() []core.Binding {
	n, all := ModeNormal, ""
	return []core.Binding{
		when(bind(n, all, "ctrl+w >", CmdSplitWiden, "widen split"), "facts.splitResize"),
		when(bind(n, all, "ctrl+w <", CmdSplitNarrow, "narrow split"), "facts.splitResize"),
		when(bind(n, all, "ctrl+w +", CmdSplitGrow, "grow split"), "facts.splitResize"),
		when(bind(n, all, "ctrl+w -", CmdSplitShrink, "shrink split"), "facts.splitResize"),
		when(bind(n, all, "ctrl+w =", CmdSplitReset, "reset splits"), "facts.splitResize"),
	}
}

func (a *App) registerSplitResizeHandlers() {
	a.handle(CmdSplitWiden, a.splitResize(1, false))
	a.handle(CmdSplitNarrow, a.splitResize(-1, false))
	a.handle(CmdSplitGrow, a.splitResize(1, true))
	a.handle(CmdSplitShrink, a.splitResize(-1, true))
	a.handle(CmdSplitReset, a.splitReset)
}

func splitStepArg(c core.Command) float64 {
	if raw, ok := c.Args["step"]; ok {
		switch v := raw.(type) {
		case float64:
			if v > 0 && v < 0.5 {
				return v
			}
		case int:
			if v > 0 && v < 50 {
				return float64(v) / 100.0
			}
		case string:
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && f > 0 && f < 0.5 {
				return f
			}
		}
	}
	return splitRatioDefaultStep
}

func isVerticalSplitNode(n *ui.ViewNode) bool {
	if n == nil || n.Props == nil {
		return false
	}
	dir, _ := n.Props["direction"].(string)
	return strings.EqualFold(strings.TrimSpace(dir), "vertical")
}

func findSplitNodePath(root *ui.ViewNode, id string) []*ui.ViewNode {
	if root == nil || id == "" {
		return nil
	}
	var path []*ui.ViewNode
	var walk func(n *ui.ViewNode) bool
	walk = func(n *ui.ViewNode) bool {
		if n.Kind == ui.KindOverlay {
			return false
		}
		path = append(path, n)
		if n.ID == id {
			return true
		}
		for i := range n.Children {
			if walk(&n.Children[i]) {
				return true
			}
		}
		path = path[:len(path)-1]
		return false
	}
	if walk(root) {
		return path
	}
	return nil
}

func firstVisibleSplit(root *ui.ViewNode, wantVertical bool) *ui.ViewNode {
	if root == nil {
		return nil
	}
	var fallback *ui.ViewNode
	var walk func(n *ui.ViewNode) *ui.ViewNode
	walk = func(n *ui.ViewNode) *ui.ViewNode {
		if n.Kind == ui.KindOverlay {
			return nil
		}
		if n.Kind == ui.KindSplit && n.ID != "" && len(n.Children) >= 2 {
			if isVerticalSplitNode(n) == wantVertical {
				return n
			}
			if fallback == nil {
				fallback = n
			}
		}
		for i := range n.Children {
			if found := walk(&n.Children[i]); found != nil {
				return found
			}
		}
		return nil
	}
	if exact := walk(root); exact != nil {
		return exact
	}
	return fallback
}

func findSplitTarget(root *ui.ViewNode, focusID string, wantVertical bool) (*ui.ViewNode, int) {
	path := findSplitNodePath(root, focusID)
	var fallback *ui.ViewNode
	fallbackChild := 0
	for i := len(path) - 2; i >= 0; i-- {
		cand := path[i]
		if cand.Kind != ui.KindSplit || cand.ID == "" || len(cand.Children) < 2 {
			continue
		}
		childIdx := 0
		if len(cand.Children) > 1 && path[i+1] == &cand.Children[1] {
			childIdx = 1
		}
		if isVerticalSplitNode(cand) == wantVertical {
			return cand, childIdx
		}
		if fallback == nil {
			fallback, fallbackChild = cand, childIdx
		}
	}
	if fallback != nil {
		return fallback, fallbackChild
	}
	return firstVisibleSplit(root, wantVertical), 0
}

func (a *App) splitResize(dir int, wantVertical bool) func(core.State, core.Command) core.StepResult {
	return func(s core.State, c core.Command) core.StepResult {
		if !a.splitResizeEnabled() {
			a.say("split resize is disabled (ui.splitResize=false)")
			return core.Next(s)
		}
		root := a.View(s)
		split, childIdx := findSplitTarget(&root, s.ActiveFrame().FocusID, wantVertical)
		if split == nil {
			a.say("no resizable split on this view")
			return core.Next(s)
		}
		step := splitStepArg(c) * float64(c.Times())
		delta := float64(dir) * step
		if childIdx == 1 {
			delta = -delta
		}
		cur := split.Ratio
		if cur <= 0 || cur >= 1 {
			cur = 0.5
		}
		next := math.Round(min(max(cur+delta, splitRatioMin), splitRatioMax)*100) / 100
		if a.nav.splitRatios == nil {
			a.nav.splitRatios = map[string]float64{}
		}
		a.nav.splitRatios[split.ID] = next
		a.viewMemo.gen++
		a.say("split %s: %d%% / %d%%", split.ID, int(math.Round(next*100)), int(math.Round((1-next)*100)))
		return core.Next(s)
	}
}

func collectSplitIDs(n *ui.ViewNode, out *[]string) {
	if n == nil || n.Kind == ui.KindOverlay {
		return
	}
	if n.Kind == ui.KindSplit && n.ID != "" {
		*out = append(*out, n.ID)
	}
	for i := range n.Children {
		collectSplitIDs(&n.Children[i], out)
	}
}

func (a *App) splitReset(s core.State, _ core.Command) core.StepResult {
	if a.nav == nil || len(a.nav.splitRatios) == 0 {
		a.say("splits already at default ratios")
		return core.Next(s)
	}
	root := a.View(s)
	var ids []string
	collectSplitIDs(&root, &ids)
	cleared := 0
	for _, id := range ids {
		if _, ok := a.nav.splitRatios[id]; ok {
			delete(a.nav.splitRatios, id)
			cleared++
		}
	}
	if cleared == 0 {
		clear(a.nav.splitRatios)
	}
	a.viewMemo.gen++
	a.say("reset split ratios to defaults")
	return core.Next(s)
}

func (a *App) applySplitRatios(root *ui.ViewNode) {
	if !a.splitResizeEnabled() || a.nav == nil || len(a.nav.splitRatios) == 0 {
		return
	}
	applySplitRatioOverrides(root, a.nav.splitRatios)
}

func applySplitRatioOverrides(n *ui.ViewNode, ratios map[string]float64) {
	if n == nil || n.Kind == ui.KindOverlay {
		return
	}
	if n.Kind == ui.KindSplit && n.ID != "" {
		if r, ok := ratios[n.ID]; ok && r >= splitRatioMin && r <= splitRatioMax {
			n.Ratio = r
		}
	}
	for i := range n.Children {
		applySplitRatioOverrides(&n.Children[i], ratios)
	}
}

func (a *App) splitRatiosMemoKey() string {
	if !a.splitResizeEnabled() || a.nav == nil || len(a.nav.splitRatios) == 0 {
		return ""
	}
	keys := make([]string, 0, len(a.nav.splitRatios))
	for k := range a.nav.splitRatios {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	b.WriteString("|sr:")
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s=%.2f", k, a.nav.splitRatios[k])
	}
	return b.String()
}
