package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/ochazuke/traits"
	"ofam.dev/ochazuke/ui"
)

func (a *App) ensureDynamicFilter(id string) *queryFilter {
	if a == nil || a.nav == nil || id == "" {
		return nil
	}
	if a.nav.dynamicFilterIDs == nil {
		a.nav.dynamicFilterIDs = map[string]bool{}
	}
	a.nav.dynamicFilterIDs[id] = true
	return a.ensureQueryFilter(id)
}

func (a *App) dynamicFilterFor(id string) traits.Filterable {
	if a == nil || a.nav == nil || !a.nav.dynamicFilterIDs[id] {
		return nil
	}
	return a.filterRegistry().Get(id)
}

func (a *App) dynamicFiltersMemoKey() string {
	if a == nil || a.nav == nil || len(a.nav.dynamicFilterIDs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(a.nav.dynamicFilterIDs))
	for k := range a.nav.dynamicFilterIDs {
		if f := a.filterRegistry().Get(k); f != nil && f.IsFiltered() {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	slices.Sort(keys)
	var b strings.Builder
	for _, k := range keys {
		f := a.filterRegistry().Get(k)
		fmt.Fprintf(&b, "|df:%s:%q:%s", k, f.FilterQuery(), f.FilterMode())
	}
	return b.String()
}

// applyDynamicViewFilters walks root and filters any ViewNode whose FilterSpec
// ID is backed by an active dynamic queryFilter (tables, trees, markdown, and
// recursive container children).
func (a *App) applyDynamicViewFilters(root *ui.ViewNode) {
	if a == nil || a.nav == nil || len(a.nav.dynamicFilterIDs) == 0 || root == nil {
		return
	}
	var walk func(n *ui.ViewNode)
	walk = func(n *ui.ViewNode) {
		if n.Filter != nil {
			id := n.Filter.ID
			if id == "" {
				id = n.ID
			}
			if f := a.dynamicFilterFor(id); f != nil && f.IsFiltered() {
				applyDynamicFilterToNode(n, f.FilterQuery(), f.FilterMode())
				return
			}
		}
		for i := range n.Children {
			walk(&n.Children[i])
		}
	}
	walk(root)
}

func applyDynamicFilterToNode(n *ui.ViewNode, query string, mode traits.MatchMode) {
	mm, _ := traits.CompileMultiTerm(query, mode, traits.FilterConfig{})
	if n.Facts == nil {
		n.Facts = map[string]any{}
	}
	n.Facts["hasFilter"] = true
	n.Facts["filterQuery"] = query
	if n.Kind == ui.KindPanel {
		n.Badges = append(slices.Clone(n.Badges), ui.Badge{Text: "/" + query, Tone: "accent"})
	}
	filterDynamicNodeContent(n, mm)
}

func filterDynamicNodeContent(n *ui.ViewNode, mm traits.MultiMatcher) bool {
	if matched, handled := filterDynamicLeafKind(n, mm, true); handled {
		return matched
	}
	if len(n.Children) > 0 {
		var kept []ui.ViewNode
		for i := range n.Children {
			ch := n.Children[i]
			if filterDynamicSubtree(&ch, mm) {
				kept = append(kept, ch)
			}
		}
		if len(kept) == 0 {
			kept = []ui.ViewNode{{
				ID:        n.ID + ".filter.empty",
				Kind:      ui.KindText,
				Scope:     n.Scope,
				Focusable: true,
				Tone:      "muted",
				Props:     map[string]any{"text": " No matches."},
				Facts:     map[string]any{"empty": true, "hasFilter": true},
			}}
		}
		n.Children = kept
		return true
	}
	return mm.Match(viewNodeFilterableText(*n))
}

func filterDynamicLeafKind(n *ui.ViewNode, mm traits.MultiMatcher, fallbackEmpty bool) (bool, bool) {
	switch n.Kind {
	case ui.KindTable:
		return filterDynamicTableProps(n, mm) > 0, true
	case ui.KindTree:
		return filterDynamicTreeProps(n, mm) > 0, true
	case ui.KindMarkdown:
		md, ok := n.Props["markdown"].(string)
		if !ok {
			return false, false
		}
		filtered, matched := filterMarkdownByMatcher(md, mm)
		if matched {
			n.Props["markdown"] = filtered
		} else if fallbackEmpty {
			n.Props["markdown"] = "_No matches._"
		}
		return matched, true
	}
	return false, false
}

func filterDynamicSubtree(n *ui.ViewNode, mm traits.MultiMatcher) bool {
	if matched, handled := filterDynamicLeafKind(n, mm, false); handled {
		return matched
	}
	if len(n.Children) > 0 {
		var kept []ui.ViewNode
		for i := range n.Children {
			ch := n.Children[i]
			if filterDynamicSubtree(&ch, mm) {
				kept = append(kept, ch)
			}
		}
		if len(kept) > 0 {
			n.Children = kept
			return true
		}
	}
	return mm.Match(viewNodeFilterableText(*n))
}

func viewNodeFilterableText(n ui.ViewNode) string {
	var parts []string
	if n.Title != "" {
		parts = append(parts, "title:"+n.Title)
	}
	for _, b := range n.Badges {
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	if n.Props != nil {
		for _, k := range []string{"text", "summary", "label", "subtitle", "markdown"} {
			if s, ok := n.Props[k].(string); ok && s != "" {
				parts = append(parts, k+":"+s)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func dynamicTableColKeys(props map[string]any) []string {
	raw, ok := props["columns"].([]any)
	if !ok {
		return nil
	}
	keys := make([]string, len(raw))
	for i, c := range raw {
		switch x := c.(type) {
		case map[string]any:
			for _, f := range []string{"key", "title", "label", "name"} {
				if s, ok := x[f].(string); ok && s != "" {
					keys[i] = strings.ToLower(s)
					break
				}
			}
		case string:
			keys[i] = strings.ToLower(x)
		}
	}
	return keys
}

func appendDynamicCells(parts *[]string, colKeys []string, cells []any) {
	for i, v := range cells {
		s := strings.TrimSpace(fmt.Sprint(v))
		if s == "" || s == "<nil>" {
			continue
		}
		if i < len(colKeys) && colKeys[i] != "" {
			*parts = append(*parts, colKeys[i]+":"+s)
		} else {
			*parts = append(*parts, s)
		}
	}
}

func appendDynamicStringCells(parts *[]string, colKeys []string, cells []string) {
	for i, s := range cells {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if i < len(colKeys) && colKeys[i] != "" {
			*parts = append(*parts, colKeys[i]+":"+s)
		} else {
			*parts = append(*parts, s)
		}
	}
}

func dynamicRowText(row any, colKeys []string) string {
	var parts []string
	switch r := row.(type) {
	case ui.TableRowSpec:
		appendDynamicCells(&parts, colKeys, r.Cells)
	case *ui.TableRowSpec:
		if r != nil {
			appendDynamicCells(&parts, colKeys, r.Cells)
		}
	case []any:
		appendDynamicCells(&parts, colKeys, r)
	case []string:
		appendDynamicStringCells(&parts, colKeys, r)
	case map[string]any:
		if cells, ok := r["cells"].([]any); ok {
			appendDynamicCells(&parts, colKeys, cells)
		} else {
			for k, v := range r {
				parts = append(parts, k+":"+fmt.Sprint(v))
			}
		}
	default:
		parts = append(parts, fmt.Sprint(row))
	}
	return strings.Join(parts, "\n")
}

func filterMatchingSlice[T any](rows []T, colKeys []string, mm traits.MultiMatcher) []T {
	out := make([]T, 0, len(rows))
	for _, r := range rows {
		if mm.Match(dynamicRowText(r, colKeys)) {
			out = append(out, r)
		}
	}
	return out
}

func filterDynamicTableProps(n *ui.ViewNode, mm traits.MultiMatcher) int {
	if n.Props == nil {
		return 0
	}
	colKeys := dynamicTableColKeys(n.Props)
	keptCount := 0
	switch rows := n.Props["rows"].(type) {
	case []any:
		out := filterMatchingSlice(rows, colKeys, mm)
		n.Props["rows"], keptCount = out, len(out)
	case []ui.TableRowSpec:
		out := filterMatchingSlice(rows, colKeys, mm)
		n.Props["rows"], keptCount = out, len(out)
	case [][]string:
		out := filterMatchingSlice(rows, colKeys, mm)
		n.Props["rows"], keptCount = out, len(out)
	case [][]any:
		out := filterMatchingSlice(rows, colKeys, mm)
		n.Props["rows"], keptCount = out, len(out)
	}
	clampDynamicSelection(n, keptCount)
	return keptCount
}

func clampDynamicSelection(n *ui.ViewNode, keptCount int) {
	if sel, ok := n.Props["selected"].(int); ok {
		if keptCount == 0 {
			n.Props["selected"] = -1
		} else if sel >= keptCount {
			n.Props["selected"] = keptCount - 1
		}
	}
	if n.Facts != nil {
		n.Facts["rowCount"] = keptCount
	}
}

func filterDynamicTreeProps(n *ui.ViewNode, mm traits.MultiMatcher) int {
	if n.Props == nil {
		return 0
	}
	if rawNodes, ok := n.Props["nodes"].([]any); ok {
		out := filterDynamicTreeMaps(rawNodes, mm)
		n.Props["nodes"] = out
		return len(out)
	}
	switch items := n.Props["items"].(type) {
	case []any:
		out := make([]any, 0, len(items))
		for _, it := range items {
			if mm.Match(fmt.Sprint(it)) {
				out = append(out, it)
			}
		}
		n.Props["items"] = out
		return len(out)
	case []string:
		out := make([]string, 0, len(items))
		for _, it := range items {
			if mm.Match(it) {
				out = append(out, it)
			}
		}
		n.Props["items"] = out
		return len(out)
	}
	return 0
}

func filterDynamicTreeMaps(nodes []any, mm traits.MultiMatcher) []any {
	var out []any
	for _, raw := range nodes {
		m, ok := raw.(map[string]any)
		if !ok {
			if mm.Match(fmt.Sprint(raw)) {
				out = append(out, raw)
			}
			continue
		}
		var visKids []any
		if kids, ok := m["children"].([]any); ok {
			visKids = filterDynamicTreeMaps(kids, mm)
		}
		if !mm.Match(dynamicTreeMapText(m)) && len(visKids) == 0 {
			continue
		}
		cp := make(map[string]any, len(m))
		for k, v := range m {
			cp[k] = v
		}
		if len(visKids) > 0 {
			cp["children"] = visKids
			cp["folded"] = false
		} else {
			delete(cp, "children")
		}
		out = append(out, cp)
	}
	return out
}

func dynamicTreeMapText(m map[string]any) string {
	var parts []string
	for _, k := range []string{"label", "title", "subtitle", "badge", "id", "text"} {
		if v, ok := m[k].(string); ok && v != "" {
			parts = append(parts, k+":"+v)
		}
	}
	return strings.Join(parts, "\n")
}

func filterMarkdownByMatcher(md string, mm traits.MultiMatcher) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var out []string
	var lastHeading string
	headingEmitted := false
	matched := false
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "#") {
			lastHeading = ln
			headingEmitted = false
			if mm.Match(ln) {
				out = append(out, ln)
				headingEmitted = true
				matched = true
			}
			continue
		}
		if trimmed == "" {
			continue
		}
		if mm.Match(ln) {
			if lastHeading != "" && !headingEmitted {
				out = append(out, lastHeading)
				headingEmitted = true
			}
			out = append(out, ln)
			matched = true
		}
	}
	return strings.Join(out, "\n"), matched
}
