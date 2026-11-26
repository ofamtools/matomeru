package app

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/expr"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/table"
)

func text(id, s, tone string) ui.ViewNode {
	return ui.ViewNode{ID: id, Kind: ui.KindText, Tone: tone, Props: map[string]any{"text": s}}
}

func line(id, s, tone string) ui.ViewNode {
	n := text(id, s, tone)
	n.Box = layout.Constraints{FixedH: 1}
	return n
}

func clip(s string, w int) string {
	if r := []rune(s); w > 1 && len(r) > w {
		return string(r[:w-1]) + "…"
	}
	return s
}

// View builds the whole screen for a state. It is rebuilt only when the
// state or the app's data changed since the last call.
func (a *App) View(s core.State) ui.ViewNode {
	a.syncLiveView(s)
	defer a.syncShownPanes(s)
	if a.focusHintsActive() {
		return a.build(s)
	}
	key := a.viewKey(s)
	if key == a.viewMemo.memoKey && key != "" {
		return a.viewMemo.memo
	}
	prevStatus := a.status
	prevGen := a.viewMemo.gen
	root := a.build(s)
	if a.status == prevStatus && a.viewMemo.gen == prevGen {
		// Save under the post-build key so harmless projection sync (such as
		// renderProjectWorkspace updating pc.activeSection for the new focus)
		// lets the subsequent render hit the memoized tree instead of rebuilding.
		a.viewMemo.memoKey, a.viewMemo.memo = a.viewKey(s), root
	}
	return root
}

func (a *App) viewKey(s core.State) string {
	var b strings.Builder
	b.WriteString(a.tabsMemoKey(s))
	fmt.Fprintf(&b, "|cg:%d|st:%s|", a.viewMemo.chromeGen, a.status)
	for _, m := range s.Modals {
		fmt.Fprintf(&b, "%s:%s:%s|", m.ID, m.FocusID, m.Mode)
	}
	if l := s.Input.Layer; l != nil {
		b.WriteString("layer:" + l.Name + "|")
	}
	b.WriteString(strings.Join(s.Input.Prefix, " "))
	if tk := a.toastViewKey(); tk != "" {
		b.WriteString("|toast:" + tk)
	}
	if gk := a.escapeGuardViewKey(); gk != "" {
		b.WriteString("|eg:" + gk)
	}
	if a.panes.hints != nil && a.panes.hints.session != nil && a.panes.hints.session.Active() {
		fmt.Fprintf(&b, "|hints:%t:%s:%d:%s:%d:%s",
			a.panes.hints.focusJump,
			a.focusHintsStyle(),
			len(a.panes.hints.focusStack),
			a.panes.hints.session.Prefix(),
			len(a.panes.hints.session.Remaining()),
			a.panes.hints.session.ActiveAction(),
		)
	}
	if ss := a.chromeSysStatsKey(); ss != "" {
		b.WriteString("|sys:" + ss)
	}
	return b.String()
}

func (a *App) chromeSystemStatsEnabled() bool {
	return a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetChromeSystemStats, true)
}

func (a *App) chromeSysStatsKey() string {
	if !a.chromeSystemStatsEnabled() || a.rt.live.sysStats == nil {
		return ""
	}
	return FormatChromeSysStats(a.rt.live.sysStats.Snapshot(), 200)
}

func (a *App) tabsMemoKey(s core.State) string {
	var rev int64
	if a.Store != nil {
		rev, _ = a.Store.LatestChangeSetNumber()
	}
	telSeq := a.telemetryViewSeq()
	f := s.Pages[s.ActivePage]
	var b strings.Builder
	fmt.Fprintf(&b, "%d|%d|%d|%s|%s|%s|%t|", a.viewMemo.gen, rev, telSeq, s.ActivePage, f.FocusID, f.Mode, a.nav.showArchived)
	if a.Shell != nil {
		fmt.Fprintf(&b, "%dx%d|", a.Shell.W, a.Shell.H)
	}
	if a.nav != nil && a.nav.showEmptyFoldExpanded {
		b.WriteString("|sf:1")
	}
	fmt.Fprintf(&b, "|tc:%t", a.topChromeRedesignEnabled())
	if ak := a.archiveAnimCacheKey(); ak != "" {
		b.WriteString("|aa:" + ak)
	}
	if a.proj != nil {
		fmt.Fprintf(&b, "|pt:%t:%t:%t:%s:%s",
			a.projectsTopNavTabEnabled(),
			a.projectsDynamicTabsEnabled(),
			a.projectChildPaneViewEnabled(),
			strings.Join(a.proj.openTabs, ","),
			a.proj.activeProjectKey,
		)
	}
	if s.ActivePage == SpacePanes || isProjectTab(s.ActivePage) {
		if s.ActivePage == SpacePanes && a.nav != nil {
			if r := a.nav.tables[railTable]; r != nil {
				fmt.Fprintf(&b, "|rail:%d", r.SelectedIdx)
			}
		}
		if pageID := a.activeSitePageID(); pageID != "" {
			fmt.Fprintf(&b, "|ps:%s:%d", pageID, a.pageScrollOffset(pageID))
			if ck := a.canvasViewKey(pageID); ck != "" {
				b.WriteString(ck)
			}
		}
		if childID := a.activeChildPaneID(s); childID != "" {
			childPage := views.ScreenPageID
			if a.panes != nil && a.panes.activePage[childID] != "" {
				childPage = a.panes.activePage[childID]
			}
			fmt.Fprintf(&b, "|cp:%s:%s:%d", childID, childPage, a.pageScrollOffset(childPage))
			if ck := a.canvasViewKey(childPage); ck != "" {
				b.WriteString(ck)
			}
		}
		if a.panes != nil && a.panes.tocCollapsed["pages"] {
			b.WriteString("|toc:collapsed")
		}
		if pane, ok := a.activePane(s); ok && pane != nil && a.panes != nil {
			if sideID := a.panes.sideSplit[pane.ID]; sideID != "" {
				b.WriteString("|side:" + sideID)
			}
		}
		if a.proj != nil && a.proj.activeProjectKey != "" {
			sec := a.proj.activeSection(a.proj.activeProjectKey)
			openPg := a.proj.openPageID(a.proj.activeProjectKey)
			fmt.Fprintf(&b, "|proj:%s:%s:%s:%d", a.proj.activeProjectKey, sec, openPg, a.contextIndexGen())
		}
		if nk := a.projectNavViewKey(s); nk != "" {
			b.WriteString(nk)
		}
	}
	if s.ActivePage == SpaceProjects {
		if tbl := a.nav.tables[projectsDirTable]; tbl != nil {
			if row, ok := tbl.FocusedRow(); ok {
				b.WriteString("|pdir:" + row.ID)
			}
		}
	}
	if fk := a.featuresViewKey(s); fk != "" {
		b.WriteString("|feat:" + fk)
	}
	if a.nav != nil && (a.nav.showRelatedOff != nil || a.nav.showPropsOff != nil) {
		fmt.Fprintf(&b, "|ss:%s:%s:%s", data.RefText(a.nav.showScrollRef), showScrollOffsetText(a.nav.showRelatedOff), showScrollOffsetText(a.nav.showPropsOff))
	}
	if f, fid := a.activeFilterableNoBuild(s); f != nil && (f.IsFiltered() || a.Engine.Mode(s) == ModeFilter) {
		fmt.Fprintf(&b, "|flt:%s:%q:%s", fid, f.FilterQuery(), f.FilterMode())
		if a.Engine.Mode(s) == ModeFilter {
			fmt.Fprintf(&b, ":%d:%t", a.nav.filterLine.Cursor, a.filterBarBottomLeftEnabled())
		}
	}
	return b.String()
}

func (a *App) build(s core.State) ui.ViewNode {
	a.viewMemo.builds++
	var tabs ui.ViewNode
	canReuseTabs := !a.focusHintsActive() && (len(s.Modals) == 0 || s.Modals[len(s.Modals)-1].ID != "settings")
	tKey := a.tabsMemoKey(s)
	if s.ActivePage == SpaceJobs {
		tabs = a.jobsView()
	} else if canReuseTabs && tKey != "" && tKey == a.viewMemo.tabsKey && a.viewMemo.tabs.ID != "" {
		tabs = a.viewMemo.tabs
	} else {
		a.viewMemo.tabsBuilds++
		topTabs := a.topTabs()
		active := slices.IndexFunc(topTabs, func(tb topTabDef) bool { return tb.id == s.ActivePage })
		standalone, isStandalone := standalonePages[s.ActivePage]
		if isStandalone {
			active = len(topTabs)
		} else if active < 0 {
			active = 0
		}
		topChrome := a.topChromeRedesignEnabled()
		tabs = ui.ViewNode{ID: "spaces", Kind: ui.KindTabs, Props: map[string]any{"active": active}, Box: layout.Constraints{Flex: 1}}
		for i, tb := range topTabs {
			tabTitle := fmt.Sprintf("%d %s", i+1, tb.title)
			if topChrome {
				tabTitle = tb.title
			}
			tab := ui.ViewNode{ID: "space." + tb.id, Kind: ui.KindVStack, Scope: tb.id, Title: tabTitle}
			if i == active {
				child := a.spaceForTab(s, tb)
				if tb.dynamic {
					inChild := a.activeChildPaneID(s) != "" && a.projectChildPaneViewEnabled()
					child = ui.ViewNode{
						ID:       "project.tab." + tb.projectID,
						Kind:     ui.KindVStack,
						Scope:    views.ScopeProjectTab,
						Box:      layout.Constraints{Flex: 1},
						Facts:    map[string]any{"isProjectTab": true, "canCloseTab": true, "inChildPaneView": inChild, "projectId": tb.projectID},
						Children: []ui.ViewNode{child},
					}
				}
				tab.Children = []ui.ViewNode{child}
			}
			if tb.id == SpacePanes {
				tab.Unread = slices.ContainsFunc(a.rail().Rows, func(r table.Row) bool { return r.Unread })
				if pane, ok := a.SelectedPane(); ok && len(a.pendingApprovalsFor(pane.ID)) > 0 {
					tab.Facts = map[string]any{"hasPendingApproval": true}
				}
			} else if tb.dynamic && tb.projectID != "" {
				tab.Unread = a.projectTabHasUnread(tb.projectID, tb.title)
			}
			tabs.Children = append(tabs.Children, tab)
		}
		if isStandalone {
			tabs.Children = append(tabs.Children, ui.ViewNode{
				ID:       "space." + s.ActivePage,
				Kind:     ui.KindVStack,
				Title:    standalone.title,
				Children: []ui.ViewNode{standalone.render(a)},
			})
		}
		if !a.focusHintsActive() {
			a.viewMemo.tabsKey, a.viewMemo.tabs = a.tabsMemoKey(s), tabs
		}
	}
	root := ui.ViewNode{
		ID:       "root",
		Kind:     ui.KindVStack,
		Facts:    map[string]any{"enterOpensShow": a.enterOpensShow(), "hlFolds": a.treeHLFolds()},
		Children: []ui.ViewNode{tabs},
	}
	var overlays []ui.ViewNode
	for i, m := range s.Modals {
		ov := a.modal(m)
		if i == len(s.Modals)-1 {
			ov = a.decorateEscapeGuardOverlay(ov)
		}
		overlays = append(overlays, ov)
	}
	if a.focusHintsActive() {
		if style := a.focusHintsStyle(); style == FocusHintsOverlay || style == FocusHintsBoth {
			overlays = append(overlays, a.panes.hints.session.ViewOverlay("focus.hints.overlay"))
		}
	}
	if a.screenHintsActive() {
		w, h := 120, 36
		if a.Shell != nil && a.Shell.W > 0 && a.Shell.H > 0 {
			w, h = a.Shell.W, a.Shell.H
		}
		overlays = append(overlays, a.screenHintsOverlay(w, h))
	}
	if toastOv, ok := a.toastOverlay(); ok {
		overlays = append(overlays, toastOv)
	}
	root.Children = append(root.Children, overlays...)

	// The footer reads the context chain of the tree above it.
	chain := ui.ContextChain(root, s.ActiveFrame().FocusID, nil)
	w := 0
	if a.Shell != nil {
		w = a.Shell.W - 1
	}
	var footer []ui.ViewNode
	if a.topChromeRedesignEnabled() {
		sep := a.chromeSeparatorNode(s, chain, w)
		// Insert the separator (CPU/RAM/disk + context chain + MODE) cleanly
		// between the top tab bar and the active page content.
		if activeTab := slices.IndexFunc(tabs.Children, func(ch ui.ViewNode) bool { return len(ch.Children) > 0 }); activeTab >= 0 {
			tabs.Children = slices.Clone(tabs.Children)
			ch := tabs.Children[activeTab].Children
			if len(ch) > 0 && ch[0].ID == "chrome.sep" {
				next := make([]ui.ViewNode, len(ch))
				copy(next, ch)
				next[0] = sep
				tabs.Children[activeTab].Children = next
			} else {
				tabs.Children[activeTab].Children = append([]ui.ViewNode{sep}, ch...)
			}
		}
		hintsLine := line("hints", clip(a.hintLine(s, chain), w), "muted")
		bottomLine, ok := a.filterBarNode(s, root, w)
		if !ok {
			bottomLine = line("status", a.bottomStatusLine(s, root, w), "accent")
		}
		footer = []ui.ViewNode{hintsLine, bottomLine}
	} else {
		leftStatus := a.statusLine(s, root, chain)
		statusText := clip(leftStatus, w)
		if a.chromeSystemStatsEnabled() && a.rt.live.sysStats != nil {
			if rightStats := FormatChromeSysStats(a.rt.live.sysStats.Snapshot(), w); rightStats != "" {
				if w > 0 {
					statusText = MergeChromeStatusLine(leftStatus, rightStats, w)
				} else {
					statusText = leftStatus + "  " + rightStats
				}
			}
		}
		bottomLine, ok := a.filterBarNode(s, root, w)
		if !ok {
			bottomLine = line("hints", clip(a.hintLine(s, chain), w), "muted")
		}
		footer = []ui.ViewNode{
			line("status", statusText, "accent"),
			bottomLine,
		}
	}
	root.Children = append(append([]ui.ViewNode{tabs}, footer...), overlays...)
	applyTreeGuides(&root, a.cfg.Settings.Bool(SetTreeGuides, true))
	if a.focusHintsActive() {
		if style := a.focusHintsStyle(); style == FocusHintsInline || style == FocusHintsBoth {
			applyFocusHintBadges(&root, a.panes.hints.session)
		}
	}
	return root
}

func (a *App) statusLine(s core.State, root ui.ViewNode, chain []core.ContextFrame) string {
	scopes := make([]string, 0, len(chain))
	for _, f := range chain {
		if strings.HasPrefix(f.Scope, "trait.") {
			continue
		}
		scopes = append(scopes, f.Scope)
	}
	slices.Reverse(scopes)
	mode := strings.ToUpper(a.Engine.Mode(s))
	if l := s.Input.Layer; l != nil {
		mode += " · " + l.Name
	}
	out := fmt.Sprintf(" %s · %s", mode, strings.Join(scopes, " › "))
	if p := s.Input.Prefix; len(p) > 0 {
		out += " · " + strings.Join(p, " ") + "…"
	}
	// A filter is shown in the status line while kept in Normal mode, or while
	// typing when the fixed bottom-left Filter Bar is disabled.
	typing := a.Engine.Mode(s) == ModeFilter
	if !typing || !a.filterBarBottomLeftEnabled() {
		if f, spec, _ := a.activeFilterTarget(s, &root); f != nil && (f.IsFiltered() || typing) {
			prefix := activeFilterPrefix(f, spec)
			q := f.FilterQuery()
			shownQuery := q
			if typing {
				a.nav.filterLine.Sync(q)
				rs := []rune(q)
				ci := min(max(a.nav.filterLine.Cursor, 0), len(rs))
				shownQuery = string(rs[:ci]) + "█" + string(rs[ci:])
			}
			out += " · " + prefix + "/" + shownQuery + " (" + string(f.FilterMode()) + ")"
		}
	}
	if a.status != "" {
		out += " · " + a.status
	}
	return out
}

// --- bindings that work here ------------------------------------------------------

type activeBinding struct {
	keys, command, hint, scope, mode string
	hidden                           bool // left out of the footer, listed by help
}

// activeKeyBindings lists the bindings a key press would reach in this state:
// right mode, a scope on the context chain, a true When, and not shadowed by
// an earlier binding on the same keys. In the terminal it also lists what
// the leader reaches, with the leader's key in front.
func (a *App) activeKeyBindings(s core.State) []activeBinding {
	chain := ui.ContextChain(a.View(s), s.ActiveFrame().FocusID, a.Shell.AppFacts(s))
	out := a.bindingsFor(s, chain, a.bind.Keymap.ModeChain(a.Engine.Mode(s)), nil, "")
	if a.Engine.Mode(s) == ModeTerminal {
		var modes []string
		for _, m := range leader.Modes {
			modes = append(modes, a.bind.Keymap.ModeChain(m)...)
		}
		out = append(out, a.bindingsFor(s, chain, modes, leader.ExcludeTags, "ctrl+a ")...)
	}
	return out
}

type bindingBucketKey struct {
	scope, mode string
}

type compiledBinding struct {
	keys, command, rawCommand, hint, scope, mode string
	hidden, hasWhen                              bool
	cond                                         expr.Expr
}

type compiledBindingsIndex struct {
	keymap   *core.Keymap
	firstPtr *core.Binding
	length   int
	buckets  map[bindingBucketKey][]compiledBinding
}

func (a *App) compiledBindings() *compiledBindingsIndex {
	var firstPtr *core.Binding
	if len(a.bind.bindings) > 0 {
		firstPtr = &a.bind.bindings[0]
	}
	if idx := a.viewMemo.binds; idx != nil && idx.keymap == a.bind.Keymap && idx.firstPtr == firstPtr && idx.length == len(a.bind.bindings) {
		return idx
	}
	buckets := make(map[bindingBucketKey][]compiledBinding, 64)
	for _, b := range a.bind.bindings {
		if b.Unbind || (b.Command == "" && b.Layer == nil) {
			continue
		}
		var cond expr.Expr
		hasWhen := strings.TrimSpace(b.When) != ""
		if hasWhen {
			var err error
			cond, err = expr.Parse(b.When)
			if err != nil {
				continue
			}
		}
		keys := strings.Join(b.Keys, " ")
		if keys == "" {
			keys = "‹" + b.Class + "›"
		}
		command := b.Command
		if b.Layer != nil {
			command = "layer." + b.Layer.Name
		}
		// One fixed argument tells two bindings of a command apart: "record.new note".
		if len(b.Args) == 1 {
			for k, v := range b.Args {
				if k == "via" {
					continue
				}
				if str := fmt.Sprint(v); !strings.HasPrefix(str, "$") {
					command += " " + str
				}
			}
		}
		bk := bindingBucketKey{scope: b.Scope, mode: b.Mode}
		buckets[bk] = append(buckets[bk], compiledBinding{
			keys:       keys,
			command:    command,
			rawCommand: b.Command,
			hint:       b.Hint,
			scope:      b.Scope,
			mode:       b.Mode,
			hidden:     b.Hidden || b.Class != "",
			hasWhen:    hasWhen,
			cond:       cond,
		})
	}
	idx := &compiledBindingsIndex{
		keymap:   a.bind.Keymap,
		firstPtr: firstPtr,
		length:   len(a.bind.bindings),
		buckets:  buckets,
	}
	a.viewMemo.binds = idx
	return idx
}

func (a *App) bindingsFor(s core.State, chain []core.ContextFrame, modes, exclude []string, prefix string) []activeBinding {
	idx := a.compiledBindings()
	// Facts visible at each level: the level's own over those of the levels outside it.
	facts := make([]map[string]any, len(chain)+1)
	for i := len(chain) - 1; i >= 0; i-- {
		facts[i] = map[string]any{}
		for k, v := range facts[i+1] {
			facts[i][k] = v
		}
		for k, v := range chain[i].Facts {
			facts[i][k] = v
		}
	}
	topModalID := ""
	if n := len(s.Modals); n > 0 {
		topModalID = s.Modals[n-1].ID
	}
	curMode := a.Engine.Mode(s)
	overlayState := map[string]any{"open": len(s.Modals) > 0, "id": topModalID}
	envs := make([]map[string]any, len(chain))
	envFor := func(level int) map[string]any {
		if level < 0 || level >= len(chain) {
			return map[string]any{"mode": curMode, "scope": "", "facts": map[string]any{}, "modal": overlayState, "count": nil, "layer": nil}
		}
		if envs[level] == nil {
			envs[level] = map[string]any{"mode": curMode, "scope": chain[level].Scope, "facts": facts[level],
				"modal": overlayState, "count": nil, "layer": nil}
		}
		return envs[level]
	}
	modesWithAny := make([]string, len(modes)+1)
	copy(modesWithAny, modes)
	var out []activeBinding
	seen := map[string]bool{}
	hasExclude := len(exclude) > 0
	add := func(level int, scope string) {
		for _, mode := range modesWithAny {
			for _, cb := range idx.buckets[bindingBucketKey{scope: scope, mode: mode}] {
				if hasExclude && cb.rawCommand != "" && a.bind.Keymap.Commands.HasAnyTag(cb.rawCommand, exclude) {
					continue
				}
				if cb.hasWhen && !cb.cond.Eval(envFor(level)) {
					continue
				}
				if seen[cb.keys] {
					continue
				}
				seen[cb.keys] = true
				keys := cb.keys
				if prefix != "" {
					keys = prefix + keys
				}
				out = append(out, activeBinding{keys: keys, command: cb.command, hint: cb.hint, scope: cb.scope, mode: cb.mode, hidden: cb.hidden})
			}
		}
	}
	for i, f := range chain {
		if f.Scope != "" {
			add(i, f.Scope)
		}
	}
	add(0, "")
	return out
}

// hintLine is the footer's "key noun.verb" list: the innermost scope's
// bindings first.
func (a *App) hintLine(s core.State, chain []core.ContextFrame) string {
	if a.Shell == nil {
		return ""
	}
	modes := a.bind.Keymap.ModeChain(a.Engine.Mode(s))
	exclude, lead := []string(nil), ""
	if l := s.Input.Layer; l != nil {
		modes, exclude, lead = nil, l.ExcludeTags, l.Name+": "
		for _, m := range l.Modes {
			modes = append(modes, a.bind.Keymap.ModeChain(m)...)
		}
	}
	full := append(slices.Clone(chain[:len(chain)-1]), core.ContextFrame{Scope: "app", Facts: a.Shell.AppFacts(s)})
	// Keys that run the same command share one hint: "backspace/H history.back".
	type hintGroup struct {
		command string
		rank    int
		keys    []string
	}
	var groups []hintGroup
	groupIdx := map[string]int{}
	for _, b := range a.bindingsFor(s, full, modes, exclude, "") {
		if b.hidden {
			continue
		}
		rank := hintScopeRank(b.scope, full)
		if idx, seen := groupIdx[b.command]; !seen {
			groupIdx[b.command] = len(groups)
			groups = append(groups, hintGroup{command: b.command, rank: rank, keys: []string{b.keys}})
		} else {
			if rank < groups[idx].rank {
				groups[idx].rank = rank
			}
			groups[idx].keys = append(groups[idx].keys, b.keys)
		}
	}
	if a.hintsNounFirstEnabled() {
		slices.SortStableFunc(groups, func(x, y hintGroup) int {
			return x.rank - y.rank
		})
	}
	parts := make([]string, len(groups))
	for i, g := range groups {
		parts[i] = strings.Join(g.keys, "/") + " " + g.command
	}
	return " " + lead + strings.Join(parts, "  ·  ")
}

// --- spaces ---------------------------------------------------------------------

func (a *App) spaceForTab(s core.State, tb topTabDef) ui.ViewNode {
	if tb.dynamic {
		return a.projectTabSpace(s, tb.projectID)
	}
	switch tb.id {
	case SpacePanes:
		return a.panesSpace(s)
	case SpaceWork:
		return a.workSpace(s)
	case SpaceProjects:
		return a.projectsSpace(s)
	case SpaceFeatures:
		return a.featuresSpace(s)
	}
	for _, sp := range spaces {
		if sp.id == tb.id {
			return a.listSpace(sp)
		}
	}
	return a.panesSpace(s)
}

func (a *App) panesSpace(s core.State) ui.ViewNode {
	tbl := a.rail()
	node := tbl.View(railTable)
	if node.Scope == "" {
		node.Scope, node.Ref = data.KindPane, &core.Ref{Kind: data.KindPane}
	}
	railEnterLive := a.cfg.Settings.String(SetRailEnterMode, RailEnterModeLive) == RailEnterModeLive
	onPane := false
	if it, ok := tbl.Focused(); ok && !it.Header {
		onPane = true
	}
	if node.Facts == nil {
		node.Facts = map[string]any{}
	}
	node.Facts["railEnterLive"] = railEnterLive
	node.Facts["onPane"] = onPane
	badges := []ui.Badge{{Text: fmt.Sprint(len(tbl.Filtered()))}}
	if tbl.Query != "" {
		badges = append(badges, ui.Badge{Text: "/" + tbl.Query, Tone: "accent"})
	}
	rail := ui.ViewNode{ID: "panes.rail", Kind: ui.KindPanel, Scope: ScopeRail, Title: "Panes", Border: true, Badges: badges,
		Facts:    map[string]any{"railEnterLive": railEnterLive, "onPane": onPane},
		Children: []ui.ViewNode{node}}

	site := ui.ViewNode{ID: "panes.none", Kind: ui.KindPanel, Border: true, Title: "No pane",
		Children: []ui.ViewNode{text("", "No pane is selected. Panes appear here as they are recorded.", "muted")}}
	if groupName, projRef, onProject := a.selectedRailProject(); onProject {
		site = a.projectOverviewView(s, groupName, projRef)
	} else if pane, ok := a.SelectedPane(); ok {
		site = a.buildPaneSiteNode(s, *pane, false)
	}
	if a.panes.zoom && site.ID == "site" {
		// Zoomed: only the pane's page, still inside the pane's context chain.
		if split, content := site.Find("site.pane"), site.Find("site.content"); split != nil && content != nil {
			page := *content
			page.Badges = append(slices.Clone(page.Badges), ui.Badge{Text: "zoomed", Tone: "accent"})
			split.Kind, split.Ratio, split.Children = ui.KindVStack, 0, []ui.ViewNode{page}
		}
		return site
	}
	ratio := railRatio
	if a.cfg.Settings.Bool(SetRailAutoWiden, false) && s.Pages[SpacePanes].FocusID == railTable {
		ratio = railRatioWidened
	}
	return ui.ViewNode{ID: "panes.split", Kind: ui.KindSplit, Ratio: ratio, Gap: 1, Children: []ui.ViewNode{rail, site}}
}

func (a *App) displayedSitePageID(s core.State, paneID string, pages []data.Record) (string, bool) {
	active := a.panes.activePage[paneID]
	if active == "" {
		active = views.ScreenPageID
	}
	if a.cfg != nil && a.cfg.Settings != nil && !a.cfg.Settings.Bool(SetTOCLivePreview, true) {
		return active, false
	}
	if s.ActivePage != SpacePanes && !(isProjectTab(s.ActivePage) && a.activeChildPaneID(s) == paneID) {
		return active, false
	}
	focus := s.Pages[s.ActivePage].FocusID
	pageID, ok := strings.CutPrefix(focus, tocPrefix)
	if !ok || pageID == "" || strings.HasPrefix(focus, views.TOCGroupPrefix) || strings.HasPrefix(focus, views.LaneTOCPrefix) {
		return active, false
	}
	if pageID == views.ScreenPageID {
		return views.ScreenPageID, active != views.ScreenPageID
	}
	for _, p := range pages {
		if p.ID == pageID {
			return pageID, pageID != active
		}
	}
	return active, false
}

// termLines is what the Screen page shows of a pane with no live session:
// what is typed is recorded as the owner's prompts.
func (a *App) termLines(pane data.Record, live bool) []string {
	buf := a.termOf(pane.ID)
	lines := []string{
		fmt.Sprintf("%s · %s · %s", views.Title(a.schema(data.KindPane), pane), pane.Text("harness"), pane.Text("cwd")),
		a.noSession(),
		"A line typed here is recorded as your prompt.",
		"",
	}
	lines = append(lines, buf.lines[max(0, len(buf.lines)-20):]...)
	prompt := "❯ " + buf.typed
	if live {
		prompt += "█"
	}
	return append(lines, prompt)
}

const maxContentFileCacheEntries = 64

type contentFileCacheEntry struct {
	modTime time.Time
	size    int64
	content string
}

type contentFileCache struct {
	entries map[string]contentFileCacheEntry
	order   []string
}

func (a *App) readContentFileCached(expanded string) (string, error) {
	info, statErr := os.Stat(expanded)
	if statErr != nil {
		if c := a.viewMemo.fileCache; c != nil {
			if _, existed := c.entries[expanded]; existed {
				delete(c.entries, expanded)
				c.order = slices.DeleteFunc(c.order, func(p string) bool { return p == expanded })
			}
		}
		if a.opts.ReadFile != nil {
			return a.readFile(expanded)
		}
		return "", statErr
	}
	modTime, size := info.ModTime(), info.Size()
	if c := a.viewMemo.fileCache; c != nil {
		if ent, ok := c.entries[expanded]; ok && ent.modTime.Equal(modTime) && ent.size == size {
			return ent.content, nil
		}
	}
	content, err := a.readFile(expanded)
	if err != nil {
		return "", err
	}
	if a.viewMemo.fileCache == nil {
		a.viewMemo.fileCache = &contentFileCache{entries: make(map[string]contentFileCacheEntry, 16)}
	}
	c := a.viewMemo.fileCache
	if _, existed := c.entries[expanded]; !existed {
		for len(c.entries) >= maxContentFileCacheEntries && len(c.order) > 0 {
			evict := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, evict)
		}
		c.order = append(c.order, expanded)
	}
	c.entries[expanded] = contentFileCacheEntry{modTime: modTime, size: size, content: content}
	return content, nil
}

func (a *App) resolveContentFiles(schema data.Schema, rec data.Record) map[string]string {
	var resolved map[string]string
	for _, f := range schema.Fields {
		if !f.ContentFile {
			continue
		}
		fp := strings.TrimSpace(rec.Text(f.Name))
		if fp == "" {
			continue
		}
		content, err := a.readContentFileCached(expandHome(fp))
		if err != nil || strings.TrimSpace(content) == "" {
			continue
		}
		if resolved == nil {
			resolved = map[string]string{}
		}
		resolved[f.Name] = content
		if strings.TrimSpace(rec.Text("body")) == "" && resolved["body"] == "" {
			resolved["body"] = content
		}
	}
	return resolved
}

func (a *App) showOptionsFor(ref core.Ref) views.ShowOptions {
	opts := a.showOptions()
	if a.Shell != nil && a.Shell.W > 0 {
		opts.Width = a.Shell.W
	}
	if a.nav != nil {
		if a.nav.showScrollRef == ref {
			opts.RelatedScrollOffset = a.nav.showRelatedOff
			opts.PropsScrollOffset = a.nav.showPropsOff
		}
		opts.EmptyFoldExpanded = a.nav.showEmptyFoldExpanded
	}
	if opts.ShowContent && a.Store != nil {
		if rec, err := a.Store.Get(ref); err == nil && rec != nil {
			opts.ResolvedFiles = a.resolveContentFiles(a.schema(ref.Kind), *rec)
		}
	}
	return opts
}

func (a *App) workSpace(s core.State) ui.ViewNode {
	ref, showing := a.Showing()
	kind := a.workKind()
	if !showing {
		if a.inWorkTree(s) {
			return a.workTreeView()
		}
		return views.IndexView(a.schema(kind), a.records(workTable, kind), SpaceWork)
	}
	crumbs := []string{a.schema(kind).Plural}
	for _, h := range a.nav.history[:a.nav.histPos+1] {
		crumbs = append(crumbs, a.title(h.ref))
	}
	if len(crumbs) > 4 {
		crumbs = []string{crumbs[0], "…", crumbs[len(crumbs)-2], crumbs[len(crumbs)-1]}
	}
	trail := " " + strings.Join(crumbs, " › ")
	if n := len(a.nav.history) - a.nav.histPos - 1; n > 0 {
		trail += fmt.Sprintf("   (%d forward)", n)
	}
	page, err := views.ShowViewWith(a.Store, ref, s.Pages[SpaceWork].FocusID, a.showOptionsFor(ref))
	if header := page.Find(showHeader); err == nil && header != nil {
		// The Show page's record opens with "o" as its row in a list does.
		if rec, gerr := a.Store.Get(ref); gerr == nil {
			facts := maps.Clone(header.Facts)
			if facts == nil {
				facts = map[string]any{}
			}
			if a.openable(*rec) {
				facts[factOpenable] = true
			}
			a.applyExtensionFacts(*rec, facts)
			header.Facts = facts
		}
	}
	if err != nil {
		page = ui.ViewNode{ID: "show", Kind: ui.KindPanel, Border: true, Title: data.RefText(ref), Children: []ui.ViewNode{
			text("", "This record is not in the store any more ("+err.Error()+"). Backspace goes back.", "warning")}}
	}
	return ui.ViewNode{ID: "work.show", Kind: ui.KindVStack, Children: []ui.ViewNode{line("work.crumbs", trail, "muted"), page}}
}

// listSpace is a space that is one noun's table beside the focused record's
// details (Runs, Extensions).
func (a *App) listSpace(sp spaceDef) ui.ViewNode {
	schema := a.schema(sp.kind)
	tbl := a.records(sp.table, sp.kind)
	index := views.IndexView(schema, tbl, sp.id)
	detail := ui.ViewNode{ID: sp.id + ".detail", Kind: ui.KindPanel, Border: true, Title: "No " + strings.ToLower(schema.Title),
		Children: []ui.ViewNode{text("", "Nothing is selected.", "muted")}}
	if row, ok := tbl.FocusedRow(); ok && row.Ref != nil {
		if rec, err := a.Store.Get(*row.Ref); err == nil {
			detail = a.detail(sp, schema, *rec)
		}
	}
	return ui.ViewNode{ID: sp.id + ".split", Kind: ui.KindSplit, Ratio: 0.5, Gap: 1, Children: []ui.ViewNode{index, detail}}
}

func (a *App) detail(sp spaceDef, schema data.Schema, rec data.Record) ui.ViewNode {
	var rows []any
	var long []ui.ViewNode
	for _, f := range schema.Fields {
		v, has := rec.Value(f.Name)
		if !has || v == nil {
			continue
		}
		switch {
		case f.Type == core.ParamList || f.ShowFormat == data.ShowFormatList:
			// Capabilities, commands, args: one wrapped bullet per item.
			var items []string
			if list, ok := v.([]any); ok {
				for _, e := range list {
					if s := strings.TrimSpace(data.Text(e)); s != "" {
						items = append(items, "• "+s)
					}
				}
			}
			if len(items) > 0 {
				long = append(long, text("", views.Label(f.Name), "accent"), ui.ViewNode{Kind: ui.KindText, Props: map[string]any{"text": strings.Join(items, "\n"), "wrap": true}})
			}
		case f.ShowFormat == data.ShowFormatMarkdown:
			if raw := data.Text(v); strings.TrimSpace(raw) != "" {
				long = append(long, text("", views.Label(f.Name), "accent"), ui.ViewNode{Kind: ui.KindMarkdown, Props: map[string]any{"markdown": raw, "wrap": true}})
			}
		case f.ShowFormat == data.ShowFormatCode:
			raw := data.Text(v)
			if summary, ok := a.compactTestOutput(rec, f.Name, raw); ok {
				long = append(long, summary)
			} else if strings.TrimSpace(raw) != "" {
				long = append(long, text("", views.Label(f.Name), "accent"), ui.ViewNode{Kind: ui.KindText, Tone: "md.code", Props: map[string]any{"text": raw, "wrap": true}})
			}
		case f.ShowFormat == data.ShowFormatText:
			if raw := data.Text(v); strings.TrimSpace(raw) != "" {
				long = append(long, text("", views.Label(f.Name), "accent"), ui.ViewNode{Kind: ui.KindText, Props: map[string]any{"text": raw, "wrap": true}})
			}
		default:
			shown := data.Text(v)
			if ref, ok := data.AsRef(v); ok {
				shown = "→ " + a.title(ref)
			}
			rows = append(rows, []any{views.Label(f.Name), shown})
		}
	}
	props := ui.ViewNode{ID: sp.id + ".detail.props", Kind: ui.KindTable, Box: layout.Constraints{FixedH: 2 + len(rows)}, Props: map[string]any{
		"columns": []any{map[string]any{"key": "field", "title": "Field", "width": 14}, map[string]any{"key": "value", "title": "Value"}},
		"rows":    rows,
	}}
	body := append([]ui.ViewNode{props}, long...)
	return ui.ViewNode{ID: sp.id + ".detail", Kind: ui.KindPanel, Border: true, Title: views.Title(schema, rec),
		Badges:   views.StatusBadges(schema, rec),
		Children: []ui.ViewNode{{ID: sp.id + ".detail.scroll", Kind: ui.KindScroll, Props: map[string]any{"scrollKey": data.RefText(rec.Ref())}, Children: []ui.ViewNode{{Kind: ui.KindVStack, Children: body}}}}}
}

// --- overlays -------------------------------------------------------------------

func (a *App) modal(m core.Frame) ui.ViewNode {
	if mod := a.modals.Get(m.ID); mod != nil {
		w, h := 0, 0
		if a.Shell != nil {
			w, h = a.Shell.W, a.Shell.H
		}
		return mod.View(w, h)
	}
	return modal.Overlay(modal.OverlaySpec{ID: m.ID, Title: m.ID, WidthPct: 40, Children: []ui.ViewNode{text("", "nothing to show", "muted")}})
}

// noSession says why the Screen shows no live session.
func (a *App) noSession() string {
	if a.panes.Host == nil {
		return "No live terminal is attached in this build."
	}
	return "This pane has no live session yet. Enter starts one."
}

// applyTreeGuides sets the "guides" prop on every ui.KindTree node in the view
// tree from the ui.treeGuides setting (one general mechanism for Outline and
// any tree view).
func applyTreeGuides(n *ui.ViewNode, guides bool) {
	if n == nil {
		return
	}
	if n.Kind == ui.KindTree {
		if n.Props == nil {
			n.Props = map[string]any{}
		}
		n.Props["guides"] = guides
	}
	for i := range n.Children {
		applyTreeGuides(&n.Children[i], guides)
	}
}
