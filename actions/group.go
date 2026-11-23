package actions

import (
	"cmp"
	"slices"
	"strings"

	"ofam.dev/ochazuke/core"
)

// GroupedAction is one CommandSpec positioned inside a namespace group for the
// focused-record actions picker (;).
type GroupedAction struct {
	Group        string           // normalized display group name (e.g. "pane", "gh.pr", "gh", "record", "yank")
	Namespace    string           // namespace extracted from Spec via NamespaceOf
	FocusedMatch bool             // true when this action belongs to the focused kind's top group
	FirstInGroup bool             // true for the first action of each contiguous group
	Spec         core.CommandSpec // the command specification
}

// ActionGroup is a contiguous group of actions sharing one Group name.
type ActionGroup struct {
	Name         string
	FocusedMatch bool
	Actions      []GroupedAction
}

// NamespaceOf extracts the command's namespace from explicit metadata
// (Meta["noun"], then Meta["scope"]) or from its dotted ID.
//
// Examples:
//   - "pane.approve"      -> "pane"
//   - "panes.split"       -> "panes"
//   - "gh.pr.merge"       -> "gh.pr"
//   - "gh.open"           -> "gh"
//   - "cmd.cmd.run-term"  -> "cmd.cmd"
//   - "record.star"       -> "record"
//   - "yank"              -> "yank"
func NamespaceOf(spec core.CommandSpec) string {
	if spec.Meta != nil {
		if noun, ok := spec.Meta[MetaNoun].(string); ok {
			if n := normalizeNamespace(noun); n != "" && n != "*" {
				return n
			}
		}
		if scope, ok := spec.Meta[MetaScope].(string); ok {
			if n := normalizeNamespace(scope); n != "" && n != "*" && n != "app" {
				return n
			}
		}
	}
	id := normalizeNamespace(spec.ID)
	if id == "" {
		return ""
	}
	if dot := strings.LastIndexByte(id, '.'); dot > 0 {
		return id[:dot]
	}
	return id
}

// GroupForFocus groups and sorts applicable actions for a focused record kind:
//
//  1. Actions whose namespace matches focusedKind (exact, singular/plural alias
//     such as pane/panes or gh.pr/gh.prs, or sub-namespace such as pane.*)
//     ALWAYS appear first as the top focused group.
//  2. Actions in a dot-separated parent namespace of focusedKind (e.g. gh.*
//     when focused on gh.pr) appear next, most specific ancestor first.
//  3. Actions in other namespaces explicitly scoped to focusedKind via
//     Meta["kinds"] (e.g. triage.rule.from-signal on signal) appear next,
//     grouped by their namespace.
//  4. Generic "record" actions appear next as one clean group.
//  5. All remaining actions (open, yank, relate, custom namespaces, etc.) are
//     grouped by namespace alphabetically and sorted deterministically within
//     each group.
//
// GroupForFocus never mutates specs.
func GroupForFocus(focusedKind string, specs []core.CommandSpec) []GroupedAction {
	if len(specs) == 0 {
		return nil
	}
	kind := normalizeNamespace(focusedKind)
	ancestors := parentNamespaces(kind)

	type entry struct {
		tier         int // 0=focused, 1=ancestor, 2=kind-scoped other, 3=record, 4=other
		groupOrder   int // secondary group rank (e.g. ancestor distance)
		itemOrder    int // rank within group (0=exact, 1=alias, 2=sub-namespace)
		group        string
		namespace    string
		focusedMatch bool
		spec         core.CommandSpec
	}

	entries := make([]entry, len(specs))
	for i, sp := range specs {
		ns := NamespaceOf(sp)
		e := entry{
			tier:      4,
			group:     ns,
			namespace: ns,
			spec:      sp,
		}

		if kind != "" {
			if matchRank, ok := matchFocused(kind, ns); ok {
				e.tier = 0
				e.itemOrder = matchRank
				e.group = kind
				e.focusedMatch = true
			} else if dist, anc, ok := matchAncestor(ancestors, ns); ok {
				e.tier = 1
				e.groupOrder = dist
				e.itemOrder = aliasSubRank(anc, ns)
				e.group = anc
			} else if isScopedToKind(sp, kind) {
				e.tier = 2
			}
		}

		if e.tier == 4 && isRecordNamespace(ns) {
			e.tier = 3
			e.group = "record"
		}

		entries[i] = e
	}

	slices.SortStableFunc(entries, func(a, b entry) int {
		if c := cmp.Compare(a.tier, b.tier); c != 0 {
			return c
		}
		if c := cmp.Compare(a.groupOrder, b.groupOrder); c != 0 {
			return c
		}
		if c := cmp.Compare(a.group, b.group); c != 0 {
			return c
		}
		if c := cmp.Compare(a.itemOrder, b.itemOrder); c != 0 {
			return c
		}
		if c := cmp.Compare(a.spec.ID, b.spec.ID); c != 0 {
			return c
		}
		return cmp.Compare(a.spec.Description, b.spec.Description)
	})

	out := make([]GroupedAction, len(entries))
	for i, e := range entries {
		first := i == 0 || entries[i-1].group != e.group || entries[i-1].focusedMatch != e.focusedMatch
		out[i] = GroupedAction{
			Group:        e.group,
			Namespace:    e.namespace,
			FocusedMatch: e.focusedMatch,
			FirstInGroup: first,
			Spec:         e.spec,
		}
	}
	return out
}

// Sections partitions the flat ordered slice returned by GroupForFocus into
// contiguous ActionGroups preserving order.
func Sections(grouped []GroupedAction) []ActionGroup {
	if len(grouped) == 0 {
		return nil
	}
	var groups []ActionGroup
	for _, ga := range grouped {
		if len(groups) == 0 || ga.FirstInGroup || groups[len(groups)-1].Name != ga.Group {
			groups = append(groups, ActionGroup{
				Name:         ga.Group,
				FocusedMatch: ga.FocusedMatch,
				Actions:      []GroupedAction{ga},
			})
			continue
		}
		last := &groups[len(groups)-1]
		last.Actions = append(last.Actions, ga)
	}
	return groups
}

// matchFocused reports whether ns matches focused kind directly (rank 0),
// via singular/plural alias (rank 1), or as a sub-namespace of kind (rank 2).
func matchFocused(kind, ns string) (int, bool) {
	if kind == "" || ns == "" {
		return 0, false
	}
	if ns == kind {
		return 0, true
	}
	if sameNamespaceAlias(kind, ns) {
		return 1, true
	}
	// Sub-namespace match: e.g. "pane.attention" when focused on "pane",
	// or "panes.layout" when focused on "pane".
	if isSubNamespace(ns, kind) {
		return 2, true
	}
	return 0, false
}

// matchAncestor checks whether ns matches any dot-separated parent namespace
// of focusedKind (e.g. "gh" for "gh.pr"). Returns ancestor distance (0 = closest),
// the canonical ancestor name, and ok.
func matchAncestor(ancestors []string, ns string) (int, string, bool) {
	if ns == "" || len(ancestors) == 0 {
		return 0, "", false
	}
	for i, anc := range ancestors {
		if ns == anc || sameNamespaceAlias(anc, ns) {
			return i, anc, true
		}
	}
	return 0, "", false
}

func aliasSubRank(canonical, ns string) int {
	if ns == canonical {
		return 0
	}
	return 1
}

// parentNamespaces returns dot-separated ancestors of kind from most specific
// to least specific. E.g. "a.b.c" -> ["a.b", "a"]; "gh.pr" -> ["gh"].
func parentNamespaces(kind string) []string {
	if !strings.Contains(kind, ".") {
		return nil
	}
	var out []string
	cur := kind
	for {
		dot := strings.LastIndexByte(cur, '.')
		if dot <= 0 {
			break
		}
		cur = cur[:dot]
		out = append(out, cur)
	}
	return out
}

func isSubNamespace(ns, kind string) bool {
	if strings.HasPrefix(ns, kind+".") {
		return true
	}
	// Also support pluralized prefix: e.g. "panes.layout" for kind "pane".
	kindParts := strings.Split(kind, ".")
	nsParts := strings.Split(ns, ".")
	if len(nsParts) <= len(kindParts) {
		return false
	}
	prefix := strings.Join(nsParts[:len(kindParts)], ".")
	return sameNamespaceAlias(kind, prefix)
}

func isScopedToKind(spec core.CommandSpec, kind string) bool {
	if spec.Meta == nil || kind == "" {
		return false
	}
	switch ks := spec.Meta[MetaKinds].(type) {
	case []string:
		for _, k := range ks {
			nk := normalizeNamespace(k)
			if nk == kind || sameNamespaceAlias(kind, nk) {
				return true
			}
		}
	case []any:
		for _, raw := range ks {
			if k, ok := raw.(string); ok {
				nk := normalizeNamespace(k)
				if nk == kind || sameNamespaceAlias(kind, nk) {
					return true
				}
			}
		}
	}
	return false
}

func isRecordNamespace(ns string) bool {
	return ns == "record" || ns == "records"
}

func normalizeNamespace(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	return strings.Trim(s, ".")
}

// sameNamespaceAlias reports whether a and b refer to the same namespace
// accounting for singular/plural forms on the final segment (e.g. pane/panes,
// task/tasks, gh.pr/gh.prs, git.branch/git.branches).
func sameNamespaceAlias(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	return canonicalNamespace(a) == canonicalNamespace(b)
}

func canonicalNamespace(ns string) string {
	dot := strings.LastIndexByte(ns, '.')
	if dot < 0 {
		return singularizeSegment(ns)
	}
	return ns[:dot+1] + singularizeSegment(ns[dot+1:])
}

func singularizeSegment(seg string) string {
	if len(seg) <= 2 {
		return seg
	}
	if strings.HasSuffix(seg, "ies") && len(seg) > 3 {
		return seg[:len(seg)-3] + "y"
	}
	for _, suf := range []string{"ches", "shes", "xes", "zes", "sses", "uses"} {
		if strings.HasSuffix(seg, suf) && len(seg) > len(suf) {
			return seg[:len(seg)-2]
		}
	}
	if strings.HasSuffix(seg, "s") && !strings.HasSuffix(seg, "ss") && !strings.HasSuffix(seg, "us") && !strings.HasSuffix(seg, "is") {
		return seg[:len(seg)-1]
	}
	return seg
}
