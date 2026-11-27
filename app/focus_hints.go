package app

import (
	"fmt"
	"hash/fnv"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/hints"
)

// focusHintFrame captures one level in the outside-in focus hints stack.
// At Level 1 (panelID == ""), targets represent space tabs and top-level
// panels; picking a multi-child panel pushes a Level 2 frame whose targets are
// solely that panel's focusable children.
type focusHintFrame struct {
	session         *hints.Session
	panelID         string
	panelTitle      string
	childrenByPanel map[string][]hints.Target
	tabSpaceByID    map[string]string
}

func (a *App) focusHintsActive() bool {
	return a != nil && a.panes != nil && a.panes.hints != nil &&
		a.panes.hints.focusJump && a.panes.hints.session != nil && a.panes.hints.session.Active()
}

func (a *App) focusHintsStyle() string {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return FocusHintsInline
	}
	switch v := a.cfg.Settings.String(SetFocusHintsStyle, FocusHintsInline); v {
	case FocusHintsInline, FocusHintsOverlay, FocusHintsBoth:
		return v
	default:
		return FocusHintsInline
	}
}

func (a *App) focusHintsDrilldown() bool {
	if a == nil || a.cfg == nil || a.cfg.Settings == nil {
		return false
	}
	return a.cfg.Settings.Bool(SetFocusHintsDrilldown, false)
}

func isTitledContainer(n *ui.ViewNode) bool {
	if n == nil || strings.TrimSpace(n.Title) == "" {
		return false
	}
	return n.Kind == ui.KindPanel || n.Kind == ui.KindCard || n.Kind == ui.KindOverlay || n.Border
}

func isLeadingHintGlyph(r rune) bool {
	switch r {
	case '◆', '●', '▣', '≡', '?', '★', '▪', '◈', '▸', '▾', '•', '·':
		return true
	}
	return false
}

// cleanFocusHintText strips leading TOC/status marker and icon glyphs plus
// surrounding whitespace from a KindText row so its display name matches the
// rendered item title (e.g. "◆ ▣ Screen" -> "Screen").
func cleanFocusHintText(s string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	trimmed := strings.TrimSpace(first)
	if trimmed == "" {
		return ""
	}
	cur := trimmed
	for cur != "" {
		r, sz := utf8.DecodeRuneInString(cur)
		if unicode.IsSpace(r) || isLeadingHintGlyph(r) {
			cur = cur[sz:]
			continue
		}
		break
	}
	cur = strings.TrimSpace(cur)
	if cur == "" {
		return trimmed
	}
	return cur
}

func nodeOwnFocusLabel(n *ui.ViewNode) string {
	if n == nil {
		return ""
	}
	if t := strings.TrimSpace(n.Title); t != "" {
		return t
	}
	if n.Kind == ui.KindText && n.Props != nil {
		if raw, ok := n.Props["text"].(string); ok && strings.TrimSpace(raw) != "" {
			return cleanFocusHintText(raw)
		}
		if raw, ok := n.Props["focusedText"].(string); ok && strings.TrimSpace(raw) != "" {
			return cleanFocusHintText(raw)
		}
	}
	return ""
}

func findTargetPath(root *ui.ViewNode, id string) []*ui.ViewNode {
	if root == nil || id == "" {
		return nil
	}
	var path []*ui.ViewNode
	var walk func(n *ui.ViewNode) bool
	walk = func(n *ui.ViewNode) bool {
		if n.Kind == ui.KindOverlay && n.ID == "focus.hints.overlay" {
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

func nearestTitledAncestor(path []*ui.ViewNode) *ui.ViewNode {
	for i := len(path) - 2; i >= 0; i-- {
		if isTitledContainer(path[i]) {
			return path[i]
		}
	}
	return nil
}

// outermostTitledPanel returns the outermost titled KindPanel ancestor along
// path (starting after any KindOverlay so modal panels stay scoped to the
// active modal), or nil if none exists.
func outermostTitledPanel(path []*ui.ViewNode) *ui.ViewNode {
	start := 0
	for i, n := range path {
		if n.Kind == ui.KindOverlay {
			start = i + 1
		}
	}
	for i := start; i < len(path); i++ {
		if path[i].Kind == ui.KindPanel && strings.TrimSpace(path[i].Title) != "" {
			return path[i]
		}
	}
	return nil
}

func isLayoutContainerKind(kind string) bool {
	switch kind {
	case ui.KindScroll, ui.KindVStack, ui.KindHStack, ui.KindSplit, ui.KindPanel:
		return true
	}
	return false
}

func hasFocusableDescendant(n *ui.ViewNode, focusSet map[string]bool) bool {
	if n == nil {
		return false
	}
	for i := range n.Children {
		ch := &n.Children[i]
		if ch.Kind == ui.KindOverlay {
			continue
		}
		if focusSet[ch.ID] || hasFocusableDescendant(ch, focusSet) {
			return true
		}
	}
	return false
}

func isRedundantFocusContainer(node *ui.ViewNode, focusSet map[string]bool) bool {
	return node != nil && isLayoutContainerKind(node.Kind) && hasFocusableDescendant(node, focusSet)
}

func assignFocusTargetName(t *hints.Target, node, anc *ui.ViewNode) {
	own := nodeOwnFocusLabel(node)
	parentTitle := ""
	if anc != nil {
		parentTitle = strings.TrimSpace(anc.Title)
	}
	switch {
	case own == "" && parentTitle != "":
		t.Kind, t.Value = "", parentTitle
	case own != "" && parentTitle != "":
		if strings.EqualFold(own, parentTitle) {
			t.Kind, t.Value = "", own
		} else {
			t.Kind, t.Value = parentTitle, own
		}
	case own != "":
		t.Kind, t.Value = "", own
	default:
		t.Kind, t.Value = "", t.ID
	}
}

// focusTargetsFromView enriches hints.TargetsFromFocusOrder(solved) with real
// rendered names derived generically from tree (panel titles, card titles, and
// cleaned KindText rows) rather than raw internal node IDs. Redundant focusable
// layout containers that already have focusable descendants are skipped so hints
// land directly on interactive content components.
func focusTargetsFromView(tree ui.ViewNode, solved layout.Solved) []hints.Target {
	raw := hints.TargetsFromFocusOrder(solved)
	if len(raw) == 0 {
		return nil
	}
	focusSet := make(map[string]bool, len(raw))
	for _, t := range raw {
		focusSet[t.ID] = true
	}
	out := make([]hints.Target, 0, len(raw))
	for _, t := range raw {
		path := findTargetPath(&tree, t.ID)
		if len(path) == 0 {
			t.Kind, t.Value = "", t.ID
			out = append(out, t)
			continue
		}
		node := path[len(path)-1]
		if isRedundantFocusContainer(node, focusSet) {
			continue
		}
		assignFocusTargetName(&t, node, nearestTitledAncestor(path))
		out = append(out, t)
	}
	return out
}

func collectFocusHintTabs(root *ui.ViewNode, solved layout.Solved, tabSpaceByID map[string]string) []hints.Target {
	var out []hints.Target
	var walk func(n *ui.ViewNode)
	walk = func(n *ui.ViewNode) {
		if n.Kind == ui.KindOverlay {
			return
		}
		if n.Kind == ui.KindTabs {
			for i := range n.Children {
				tab := &n.Children[i]
				title := strings.TrimSpace(tab.Title)
				if tab.ID == "" || tab.Scope == "" || title == "" {
					continue
				}
				out = append(out, hints.Target{
					ID:    tab.ID,
					Rect:  solved.Rects[tab.ID],
					Value: title,
				})
				tabSpaceByID[tab.ID] = tab.Scope
			}
			return
		}
		for i := range n.Children {
			walk(&n.Children[i])
		}
	}
	walk(root)
	return out
}

// buildFocusHintFlatFrame builds a single-step focus-hints frame covering space
// tabs (when no modal is open) and every focusable content node on screen.
func buildFocusHintFlatFrame(tree ui.ViewNode, solved layout.Solved, includeTabs bool) focusHintFrame {
	frame := focusHintFrame{
		tabSpaceByID: map[string]string{},
	}
	var targets []hints.Target
	if includeTabs {
		targets = append(targets, collectFocusHintTabs(&tree, solved, frame.tabSpaceByID)...)
	}
	targets = append(targets, focusTargetsFromView(tree, solved)...)
	for i := range targets {
		targets[i].Label = ""
	}
	frame.session = hints.NewSession(targets)
	return frame
}

// buildFocusHintRootFrame builds Level 1 of outside-in hierarchical focus
// hints: space tabs (when no modal is open) plus each visible top-level
// KindPanel that holds at least one focusable descendant in solved.FocusOrder.
func buildFocusHintRootFrame(tree ui.ViewNode, solved layout.Solved, includeTabs bool) focusHintFrame {
	frame := focusHintFrame{
		childrenByPanel: map[string][]hints.Target{},
		tabSpaceByID:    map[string]string{},
	}
	var level1 []hints.Target
	if includeTabs {
		level1 = append(level1, collectFocusHintTabs(&tree, solved, frame.tabSpaceByID)...)
	}

	flat := focusTargetsFromView(tree, solved)
	seenPanel := map[string]bool{}
	for _, t := range flat {
		path := findTargetPath(&tree, t.ID)
		panel := outermostTitledPanel(path)
		if panel != nil && panel.ID != "" {
			if !seenPanel[panel.ID] {
				seenPanel[panel.ID] = true
				level1 = append(level1, hints.Target{
					ID:    panel.ID,
					Rect:  solved.Rects[panel.ID],
					Value: strings.TrimSpace(panel.Title),
				})
			}
			frame.childrenByPanel[panel.ID] = append(frame.childrenByPanel[panel.ID], t)
			continue
		}
		level1 = append(level1, t)
	}

	for i := range level1 {
		level1[i].Label = ""
	}
	frame.session = hints.NewSession(level1)
	return frame
}

// focusTargetDisplayName formats a hint target's real rendered display name
// for status lines and legends.
func focusTargetDisplayName(t hints.Target) string {
	val := strings.TrimSpace(t.Value)
	kind := strings.TrimSpace(t.Kind)
	if val == "" {
		return t.ID
	}
	if kind == "" || kind == "focus" {
		return val
	}
	return kind + ": " + val
}

// focusHintsLegend builds a concise status line listing the active focus hint
// labels and their real rendered target titles.
func focusHintsLegend(hs *hintsState) string {
	if hs == nil || hs.session == nil {
		return ""
	}
	sess := hs.session
	rem := sess.Remaining()
	panelTitle := ""
	drilled := len(hs.focusStack) > 1
	if len(hs.focusStack) > 0 {
		panelTitle = hs.focusStack[len(hs.focusStack)-1].panelTitle
	}

	const maxShown = 6
	items := make([]string, 0, min(len(rem), maxShown))
	for i, t := range rem {
		if i >= maxShown {
			items = append(items, fmt.Sprintf("+%d more", len(rem)-maxShown))
			break
		}
		name := focusTargetDisplayName(t)
		if drilled && panelTitle != "" && strings.EqualFold(t.Kind, panelTitle) && strings.TrimSpace(t.Value) != "" {
			name = strings.TrimSpace(t.Value)
		}
		items = append(items, fmt.Sprintf("[%s] %s", t.Label, name))
	}

	head := "focus hints"
	if drilled && panelTitle != "" {
		head += " › " + panelTitle
	}
	if p := sess.Prefix(); p != "" {
		head += " " + p + "_"
	}
	tail := "esc/q cancels"
	if drilled {
		tail = "enter focuses · bksp back · esc cancels"
	}
	if len(items) == 0 {
		return fmt.Sprintf("%s (%d) · %s", head, len(rem), tail)
	}
	return fmt.Sprintf("%s (%d) · %s · %s", head, len(rem), strings.Join(items, " · "), tail)
}

func mixFocusHintCacheKey(base uint64, salt string) uint64 {
	if base == 0 {
		return 0
	}
	h := fnv.New64a()
	fmt.Fprintf(h, "%d|focus-hint|%s", base, salt)
	return h.Sum64() | 1
}

func bumpPathCacheKeys(path []*ui.ViewNode, salt string) {
	for _, n := range path {
		if n != nil && n.CacheKey != 0 {
			n.CacheKey = mixFocusHintCacheKey(n.CacheKey, salt)
		}
	}
}

func stampTabHint(n *ui.ViewNode, label string) {
	badge := "[" + label + "]"
	if strings.TrimSpace(n.Title) != "" {
		n.Title = badge + " " + n.Title
	} else {
		n.Title = badge
	}
}

func stampChromeHint(n *ui.ViewNode, label string) {
	badge := "[" + label + "]"
	if strings.TrimSpace(n.Title) != "" {
		n.Title = badge + " " + n.Title
	} else {
		n.Title = badge
	}
	n.Badges = append([]ui.Badge{{Text: badge, Tone: "match"}}, slices.Clone(n.Badges)...)
}

func stampTextHint(n *ui.ViewNode, label string) {
	badge := "[" + label + "]"
	prefix := badge + " "
	shift := utf8.RuneCountInString(prefix)
	badgeLen := utf8.RuneCountInString(badge)

	props := maps.Clone(n.Props)
	if props == nil {
		props = map[string]any{}
	}
	rawText, _ := props["text"].(string)
	props["text"] = prefix + rawText
	if ft, ok := props["focusedText"].(string); ok && ft != "" {
		props["focusedText"] = prefix + ft
	}

	var spans []ui.ToneSpan
	if existing, ok := props["spans"].([]ui.ToneSpan); ok && len(existing) > 0 {
		spans = make([]ui.ToneSpan, 0, len(existing)+1)
		spans = append(spans, ui.ToneSpan{From: 0, To: badgeLen, Tone: "match"})
		for _, sp := range existing {
			shifted := sp
			shifted.From += shift
			if shifted.To > 0 {
				shifted.To += shift
			}
			spans = append(spans, shifted)
		}
	} else {
		spans = []ui.ToneSpan{{From: 0, To: badgeLen, Tone: "match"}}
	}
	props["spans"] = spans
	n.Props = props
}

// applyFocusHintBadges stamps [label] badges directly onto the rendered nodes
// in root corresponding to sess.Placements(). Typing a prefix automatically
// hides non-matching badges on screen in real time.
func applyFocusHintBadges(root *ui.ViewNode, sess *hints.Session) {
	if root == nil || sess == nil || !sess.Active() {
		return
	}
	placements := sess.Placements()
	if len(placements) == 0 {
		return
	}
	prefix := sess.Prefix()
	stampedPanels := map[*ui.ViewNode]bool{}

	for _, p := range placements {
		path := findTargetPath(root, p.Target.ID)
		if len(path) == 0 {
			continue
		}
		node := path[len(path)-1]
		var parent *ui.ViewNode
		if len(path) >= 2 {
			parent = path[len(path)-2]
		}
		salt := p.Target.ID + ":" + p.Target.Label + ":" + prefix

		switch {
		case parent != nil && parent.Kind == ui.KindTabs:
			stampTabHint(node, p.Target.Label)
			bumpPathCacheKeys(path, salt)

		case node.Kind == ui.KindText:
			stampTextHint(node, p.Target.Label)
			bumpPathCacheKeys(path, salt)

		case node.Kind == ui.KindPanel || node.Kind == ui.KindCard || node.Kind == ui.KindOverlay || node.Border:
			stampChromeHint(node, p.Target.Label)
			stampedPanels[node] = true
			bumpPathCacheKeys(path, salt)

		default:
			if anc := nearestTitledAncestor(path); anc != nil && !stampedPanels[anc] {
				stampChromeHint(anc, p.Target.Label)
				stampedPanels[anc] = true
				bumpPathCacheKeys(path, salt)
			} else {
				badge := "[" + p.Target.Label + "]"
				if strings.TrimSpace(node.Title) != "" {
					node.Title = badge + " " + node.Title
				}
				node.Badges = append([]ui.Badge{{Text: badge, Tone: "match"}}, slices.Clone(node.Badges)...)
				bumpPathCacheKeys(path, salt)
			}
		}
	}
}
