package app

import (
	"fmt"
	"maps"
	"slices"
	"strings"

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
	if a.focusHintsActive() {
		return a.build(s)
	}
	key := a.viewKey(s)
	if key == a.viewMemo.memoKey && key != "" {
		return a.viewMemo.memo
	}
	root := a.build(s)
	if key == a.viewKey(s) { // building may itself have noted an error
		a.viewMemo.memoKey, a.viewMemo.memo = key, root
	}
	return root
}

func (a *App) viewKey(s core.State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d|%s|%s|", a.viewMemo.gen, s.ActivePage, a.status)
	f := s.Pages[s.ActivePage]
	fmt.Fprintf(&b, "%s|%s|", f.FocusID, f.Mode)
	for _, m := range s.Modals {
		fmt.Fprintf(&b, "%s:%s:%s|", m.ID, m.FocusID, m.Mode)
	}
	if l := s.Input.Layer; l != nil {
		b.WriteString("layer:" + l.Name + "|")
	}
	b.WriteString(strings.Join(s.Input.Prefix, " "))
	if a.Shell != nil {
		fmt.Fprintf(&b, "|%dx%d", a.Shell.W, a.Shell.H)
	}
	if tk := a.toastViewKey(); tk != "" {
		b.WriteString("|toast:" + tk)
	}
	if gk := a.escapeGuardViewKey(); gk != "" {
		b.WriteString("|eg:" + gk)
	}
	if ak := a.archiveAnimCacheKey(); ak != "" {
		b.WriteString("|aa:" + ak)
	}
	if a.panes.hints != nil && a.panes.hints.session != nil && a.panes.hints.session.Active() {
		fmt.Fprintf(&b, "|hints:%t:%s:%d:%s:%d",
			a.panes.hints.focusJump,
			a.focusHintsStyle(),
			len(a.panes.hints.focusStack),
			a.panes.hints.session.Prefix(),
			len(a.panes.hints.session.Remaining()),
		)
	}
	if s.ActivePage == SpacePanes {
		if pageID := a.activeSitePageID(); pageID != "" {
			fmt.Fprintf(&b, "|ps:%s:%d", pageID, a.pageScrollOffset(pageID))
			if ck := a.canvasViewKey(pageID); ck != "" {
				b.WriteString(ck)
			}
		}
		if a.panes != nil && a.panes.tocCollapsed["pages"] {
			b.WriteString("|toc:collapsed")
		}
		if pane, ok := a.SelectedPane(); ok && pane != nil && a.panes != nil {
			if sideID := a.panes.sideSplit[pane.ID]; sideID != "" {
				b.WriteString("|side:" + sideID)
			}
		}
	}
	return b.String()
}

func (a *App) build(s core.State) ui.ViewNode {
	active := slices.IndexFunc(spaces, func(sp spaceDef) bool { return sp.id == s.ActivePage })
	tabs := ui.ViewNode{ID: "spaces", Kind: ui.KindTabs, Props: map[string]any{"active": max(active, 0)}, Box: layout.Constraints{Flex: 1}}
	for i, sp := range spaces {
		tab := ui.ViewNode{ID: "space." + sp.id, Kind: ui.KindVStack, Scope: sp.id, Title: fmt.Sprintf("%d %s", i+1, sp.title)}
		if i == active {
			tab.Children = []ui.ViewNode{a.space(s, sp)}
		}
		if sp.id == SpacePanes {
			tab.Unread = slices.ContainsFunc(a.rail().Rows, func(r table.Row) bool { return r.Unread })
			if pane, ok := a.SelectedPane(); ok && len(a.pendingApprovalsFor(pane.ID)) > 0 {
				tab.Facts = map[string]any{"hasPendingApproval": true}
			}
		}
		tabs.Children = append(tabs.Children, tab)
	}
	root := ui.ViewNode{ID: "root", Kind: ui.KindVStack, Children: []ui.ViewNode{tabs}}
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
	footer := []ui.ViewNode{
		line("status", clip(a.statusLine(s, chain), w), "accent"),
		line("hints", clip(a.hintLine(s, chain), w), "muted"),
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

func (a *App) statusLine(s core.State, chain []core.ContextFrame) string {
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
	// A filter is shown while it is typed and for as long as it narrows a list.
	typing, cursor := a.Engine.Mode(s) == ModeFilter, map[bool]string{true: "▏"}
	if onPages := typing && a.nav.filterTarget == "toc"; s.ActivePage == SpacePanes && (a.nav.tocFilter != "" || onPages) {
		out += " · pages /" + a.nav.tocFilter + cursor[onPages]
	}
	list := a.nav.filterTarget
	if !typing {
		list = s.ActiveFrame().FocusID
	}
	if tbl := a.nav.tables[list]; tbl != nil && (tbl.Query != "" || typing) {
		field := ""
		if tbl.FilterField != views.AnyField && tbl.FilterField != "" {
			field = tbl.FilterField + " "
		}
		shownQuery := tbl.Query
		if typing {
			a.nav.filterLine.Sync(tbl.Query)
			rs := []rune(tbl.Query)
			ci := min(max(a.nav.filterLine.Cursor, 0), len(rs))
			shownQuery = string(rs[:ci]) + "█" + string(rs[ci:])
		}
		out += " · " + field + "/" + shownQuery + " (" + string(tbl.MatchMode) + ")"
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

func (a *App) bindingsFor(s core.State, chain []core.ContextFrame, modes, exclude []string, prefix string) []activeBinding {
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
	env := func(level int) map[string]any {
		return map[string]any{"mode": a.Engine.Mode(s), "scope": chain[level].Scope, "facts": facts[level],
			"modal": map[string]any{"open": len(s.Modals) > 0, "id": topModalID}, "count": nil, "layer": nil}
	}
	var out []activeBinding
	seen := map[string]bool{}
	add := func(level int, scope string) {
		for _, mode := range append(slices.Clone(modes), "") {
			for _, b := range a.bind.bindings {
				if b.Scope != scope || b.Mode != mode || b.Unbind || (b.Command == "" && b.Layer == nil) {
					continue
				}
				if b.Command != "" && a.bind.Keymap.Commands.HasAnyTag(b.Command, exclude) {
					continue
				}
				if cond, err := expr.Parse(b.When); err != nil || !cond.Eval(env(level)) {
					continue
				}
				keys := strings.Join(b.Keys, " ")
				if keys == "" {
					keys = "‹" + b.Class + "›"
				}
				if seen[keys] {
					continue
				}
				seen[keys] = true
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
				out = append(out, activeBinding{keys: prefix + keys, command: command, hint: b.Hint, scope: scope, mode: mode,
					hidden: b.Hidden || b.Class != ""})
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
	var order []string
	keysOf := map[string][]string{}
	for _, b := range a.bindingsFor(s, full, modes, exclude, "") {
		if b.hidden {
			continue
		}
		if _, seen := keysOf[b.command]; !seen {
			order = append(order, b.command)
		}
		keysOf[b.command] = append(keysOf[b.command], b.keys)
	}
	parts := make([]string, len(order))
	for i, command := range order {
		parts[i] = strings.Join(keysOf[command], "/") + " " + command
	}
	return " " + lead + strings.Join(parts, "  ·  ")
}

// --- spaces ---------------------------------------------------------------------

func (a *App) space(s core.State, sp spaceDef) ui.ViewNode {
	switch sp.id {
	case SpacePanes:
		return a.panesSpace(s)
	case SpaceWork:
		return a.workSpace(s)
	}
	return a.listSpace(sp)
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
		ref := pane.Ref()
		res, err := a.Store.Query(data.Query{Kind: data.KindPage, Parent: &ref, ParentField: "pane", IncludeArchived: a.nav.showArchived})
		if err != nil {
			a.fail("read pages", err)
		}
		shownPageID, previewing := a.displayedSitePageID(s, pane.ID, res.Items)
		opts := views.SiteOptions{
			Store:            a.Store,
			ShowLaneBar:      a.cfg.Settings.Bool(SetLaneBar, false),
			LaneInTOC:        a.cfg.Settings.Bool(SetLaneInTOC, true),
			CompactTOC:       a.cfg.Settings.Bool(SetCompactTOC, true),
			TOCGrouped:       a.cfg.Settings.Bool(SetTOCGrouped, true),
			TOCCollapsed:     a.panes.tocCollapsed,
			PageScrollOffset: a.pageScrollOffset(shownPageID),
			PageScrollLocked: s.Pages[SpacePanes].Mode == ModePage,
			WrapPageScroll:   true,
			HideTOC:          !a.cfg.Settings.Bool(SetShowTOC, true),
			LaneHint:         laneBarHint,
		}
		if a.Shell != nil {
			opts.Width = a.Shell.State.Layout.Rects["site"].W // 0 until the site was drawn once
		}
		site = views.PaneSiteViewWith(*pane, res.Items, shownPageID, a.nav.tocFilter, opts)
		a.enhanceQuestionsSite(&site, *pane, res.Items)
		a.enhanceCanvasSite(&site, shownPageID)
		if previewing && shownPageID != views.ScreenPageID {
			if content := site.Find("site.content"); content != nil {
				content.Badges = append(slices.Clone(content.Badges), ui.Badge{Text: "preview · enter opens", Tone: "muted"})
			}
		}
		var extraTop []ui.ViewNode
		if strip, ok := a.paneHeaderStrip(*pane); ok {
			extraTop = append(extraTop, strip)
		}
		if banner, ok := a.paneApprovalBanner(*pane); ok {
			extraTop = append(extraTop, banner)
			if site.Facts == nil {
				site.Facts = map[string]any{}
			}
			site.Facts["hasPendingApproval"] = true
		}
		if len(extraTop) > 0 {
			site.Children = append(extraTop, site.Children...)
		}
		a.rt.live.shown = ""
		a.rt.live.shownSide = ""
		if screen := site.Find(siteScreen); screen != nil {
			var shownLines []string
			if lines, cursor, badges, ok := a.liveScreen(*pane, siteScreen); ok {
				shownLines = lines
				screen.Props["lines"], screen.Props["cursor"] = lines, cursor
				content := site.Find("site.content")
				content.Badges = badges
				if f := a.rt.live.frames[pane.ID]; f.PID > 0 && !f.Exited {
					content.Title = fmt.Sprintf("Screen · pid %d", f.PID)
				}
			} else {
				typing := s.Pages[SpacePanes].Mode == ModeTerminal && s.Pages[SpacePanes].FocusID != sideScreen
				shownLines = a.termLines(*pane, typing)
				screen.Props["lines"] = shownLines
				site.Find("site.content").Badges = []ui.Badge{{Text: "not attached", Tone: "muted"}}
			}
			if a.panes.hints != nil && a.panes.hints.session != nil && a.panes.hints.session.Active() && a.panes.hints.paneID == pane.ID {
				w, h := a.screenSize()
				screen.Props["lines"] = a.hintsRenderLines(shownLines, w, h)
				delete(screen.Props, "cursor")
				if content := site.Find("site.content"); content != nil {
					content.Badges = append(slices.Clone(content.Badges),
						ui.Badge{Text: fmt.Sprintf("hints %d", len(a.panes.hints.session.Remaining())), Tone: "accent"})
				}
				site.Children = append(site.Children, a.panes.hints.session.View("site.hints.bar"))
			}
			a.applySideSplit(&site, *pane, s)
		}
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
	if s.ActivePage != SpacePanes {
		return active, false
	}
	focus := s.Pages[SpacePanes].FocusID
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
	page, err := views.ShowViewWith(a.Store, ref, s.Pages[SpaceWork].FocusID, a.showOptions())
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
		case f.Type == core.ParamList:
			// Capabilities and commands: one line each.
			var items []string
			if list, ok := v.([]any); ok {
				for _, e := range list {
					items = append(items, "• "+data.Text(e))
				}
			}
			long = append(long, text("", views.Label(f.Name), "accent"), text("", strings.Join(items, "\n"), ""))
		case f.Name == "output" || f.Name == "body":
			long = append(long, text("", views.Label(f.Name), "accent"), text("", data.Text(v), ""))
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
		Children: []ui.ViewNode{{ID: sp.id + ".detail.scroll", Kind: ui.KindScroll, Children: []ui.ViewNode{{Kind: ui.KindVStack, Children: body}}}}}
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
