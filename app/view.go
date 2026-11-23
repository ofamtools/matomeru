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
	key := a.viewKey(s)
	if key == a.memoKey && key != "" {
		return a.memo
	}
	root := a.build(s)
	if key == a.viewKey(s) { // building may itself have noted an error
		a.memoKey, a.memo = key, root
	}
	return root
}

func (a *App) viewKey(s core.State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d|%s|%s|", a.gen, s.ActivePage, a.status)
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
		}
		tabs.Children = append(tabs.Children, tab)
	}
	root := ui.ViewNode{ID: "root", Kind: ui.KindVStack, Children: []ui.ViewNode{tabs}}
	var overlays []ui.ViewNode
	for _, m := range s.Modals {
		overlays = append(overlays, a.modal(m))
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
	return root
}

func (a *App) statusLine(s core.State, chain []core.ContextFrame) string {
	scopes := make([]string, 0, len(chain))
	for _, f := range chain {
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
	if onPages := typing && a.filterTarget == "toc"; s.ActivePage == SpacePanes && (a.tocFilter != "" || onPages) {
		out += " · pages /" + a.tocFilter + cursor[onPages]
	}
	list := a.filterTarget
	if !typing {
		list = s.ActiveFrame().FocusID
	}
	if tbl := a.tables[list]; tbl != nil && (tbl.Query != "" || typing) {
		field := ""
		if tbl.FilterField != views.AnyField && tbl.FilterField != "" {
			field = tbl.FilterField + " "
		}
		shownQuery := tbl.Query
		if typing {
			a.filterLine.Sync(tbl.Query)
			rs := []rune(tbl.Query)
			ci := min(max(a.filterLine.Cursor, 0), len(rs))
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

// activeBindings lists the bindings a key press would reach in this state:
// right mode, a scope on the context chain, a true When, and not shadowed by
// an earlier binding on the same keys. In the terminal it also lists what
// the leader reaches, with the leader's key in front.
func (a *App) activeBindings(s core.State) []activeBinding {
	chain := ui.ContextChain(a.View(s), s.ActiveFrame().FocusID, a.Shell.AppFacts(s))
	out := a.bindingsFor(s, chain, a.Keymap.ModeChain(a.Engine.Mode(s)), nil, "")
	if a.Engine.Mode(s) == ModeTerminal {
		var modes []string
		for _, m := range leader.Modes {
			modes = append(modes, a.Keymap.ModeChain(m)...)
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
	env := func(level int) map[string]any {
		return map[string]any{"mode": a.Engine.Mode(s), "scope": chain[level].Scope, "facts": facts[level],
			"modal": map[string]any{"open": len(s.Modals) > 0, "id": topModal(s)}, "count": nil, "layer": nil}
	}
	var out []activeBinding
	seen := map[string]bool{}
	add := func(level int, scope string) {
		for _, mode := range append(slices.Clone(modes), "") {
			for _, b := range a.bindings {
				if b.Scope != scope || b.Mode != mode || b.Unbind || (b.Command == "" && b.Layer == nil) {
					continue
				}
				if b.Command != "" && a.Keymap.Commands.HasAnyTag(b.Command, exclude) {
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
					for _, v := range b.Args {
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
	modes := a.Keymap.ModeChain(a.Engine.Mode(s))
	exclude, lead := []string(nil), ""
	if l := s.Input.Layer; l != nil {
		modes, exclude, lead = nil, l.ExcludeTags, l.Name+": "
		for _, m := range l.Modes {
			modes = append(modes, a.Keymap.ModeChain(m)...)
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
	badges := []ui.Badge{{Text: fmt.Sprint(len(tbl.Filtered()))}}
	if tbl.Query != "" {
		badges = append(badges, ui.Badge{Text: "/" + tbl.Query, Tone: "accent"})
	}
	rail := ui.ViewNode{ID: "panes.rail", Kind: ui.KindPanel, Scope: ScopeRail, Title: "Panes", Border: true, Badges: badges,
		Children: []ui.ViewNode{node}}

	site := ui.ViewNode{ID: "panes.none", Kind: ui.KindPanel, Border: true, Title: "No pane",
		Children: []ui.ViewNode{text("", "No pane is selected. Panes appear here as they are recorded.", "muted")}}
	if pane, ok := a.SelectedPane(); ok {
		ref := pane.Ref()
		res, err := a.Store.Query(data.Query{Kind: data.KindPage, Parent: &ref, ParentField: "pane", IncludeArchived: a.showArchived})
		if err != nil {
			a.fail("read pages", err)
		}
		opts := views.SiteOptions{Store: a.Store, ShowLaneBar: a.Settings.Bool(SetLaneBar, true), LaneHint: laneBarHint}
		if a.Shell != nil {
			opts.Width = a.Shell.State.Layout.Rects["site"].W // 0 until the site was drawn once
		}
		site = views.PaneSiteViewWith(*pane, res.Items, a.activePage[pane.ID], a.tocFilter, opts)
		a.live.shown = ""
		if screen := site.Find(siteScreen); screen != nil {
			if lines, cursor, badges, ok := a.liveScreen(*pane, siteScreen); ok {
				screen.Props["lines"], screen.Props["cursor"] = lines, cursor
				content := site.Find("site.content")
				content.Badges = badges
				if f := a.live.frames[pane.ID]; f.PID > 0 && !f.Exited {
					content.Title = fmt.Sprintf("Screen · pid %d", f.PID)
				}
			} else {
				typing := s.Pages[SpacePanes].Mode == ModeTerminal
				screen.Props["lines"] = a.termLines(*pane, typing)
				site.Find("site.content").Badges = []ui.Badge{{Text: "not attached", Tone: "muted"}}
			}
		}
	}
	if a.zoom && site.ID == "site" {
		// Zoomed: only the pane's page, still inside the pane's context chain.
		if split, content := site.Find("site.pane"), site.Find("site.content"); split != nil && content != nil {
			page := *content
			page.Badges = append(slices.Clone(page.Badges), ui.Badge{Text: "zoomed", Tone: "accent"})
			split.Kind, split.Ratio, split.Children = ui.KindVStack, 0, []ui.ViewNode{page}
		}
		return site
	}
	ratio := railRatio
	if a.Settings.Bool(SetRailAutoWiden, false) && s.Pages[SpacePanes].FocusID == railTable {
		ratio = railRatioWidened
	}
	return ui.ViewNode{ID: "panes.split", Kind: ui.KindSplit, Ratio: ratio, Gap: 1, Children: []ui.ViewNode{rail, site}}
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
		return views.IndexView(a.schema(kind), a.records(workTable, kind), SpaceWork)
	}
	crumbs := []string{a.schema(kind).Plural}
	for _, h := range a.history[:a.histPos+1] {
		crumbs = append(crumbs, a.title(h.ref))
	}
	if len(crumbs) > 4 {
		crumbs = []string{crumbs[0], "…", crumbs[len(crumbs)-2], crumbs[len(crumbs)-1]}
	}
	trail := " " + strings.Join(crumbs, " › ")
	if n := len(a.history) - a.histPos - 1; n > 0 {
		trail += fmt.Sprintf("   (%d forward)", n)
	}
	page, err := views.ShowViewWith(a.Store, ref, s.Pages[SpaceWork].FocusID, a.showOptions())
	if header := page.Find(showHeader); err == nil && header != nil {
		// The Show page's record opens with "o" as its row in a list does.
		if rec, gerr := a.Store.Get(ref); gerr == nil && a.openable(*rec) {
			facts := maps.Clone(header.Facts)
			if facts == nil {
				facts = map[string]any{}
			}
			facts[factOpenable] = true
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

func overlay(id, title string, widthPct, heightPct int, children ...ui.ViewNode) ui.ViewNode {
	props := map[string]any{"widthPct": widthPct}
	if heightPct > 0 {
		props["heightPct"] = heightPct
	}
	return ui.ViewNode{ID: "modal." + id, Kind: ui.KindOverlay, Scope: "modal", Title: title, Props: props, Children: children}
}

func (a *App) modal(m core.Frame) ui.ViewNode {
	switch m.ID {
	case modalOutline:
		nodes, err := a.Store.TaskTree(a.showArchived)
		if err != nil {
			a.fail("outline", err)
		}
		panel := views.OutlineView(nodes, a.outlineSel, modalOutline)
		ov := overlay(m.ID, panel.Title, 64, 80, panel.Children...)
		ov.Scope, ov.Badges, ov.Facts = panel.Scope, panel.Badges, panel.Facts
		return ov
	case modalPath:
		if a.path == nil {
			break
		}
		panel := a.path.View(modalPath)
		ov := overlay(m.ID, panel.Title, 70, 70, panel.Children...)
		ov.Badges = panel.Badges
		return ov
	case modalForm:
		fs := a.form
		if fs == nil {
			break
		}
		var rec *data.Record
		if fs.ref != nil {
			rec, _ = a.Store.Get(*fs.ref)
		}
		fs.fm.SubmitLabel = "change field · written as you go"
		panel := views.EditView(a.schema(fs.kind), rec, fs.fm, modalForm)
		ov := overlay(m.ID, panel.Title, 70, 0, panel.Children...)
		ov.Scope, ov.Ref, ov.Badges, ov.Facts = panel.Scope, panel.Ref, panel.Badges, panel.Facts
		return ov
	case modalSettings:
		if a.settings == nil {
			break
		}
		ov := overlay(m.ID, a.settings.fm.Title, 70, 0, a.settings.fm.View(modalSettings+".form"))
		ov.Scope = "settings"
		return ov
	case modalBindings:
		if a.bindEd == nil {
			break
		}
		return a.bindingsModal()
	case modalReader:
		if a.reader == nil {
			break
		}
		return a.readerModal()
	case modalScratch:
		if a.scratch == "" {
			break
		}
		return a.scratchModal()
	case modalInspect:
		pm := a.pickers[m.ID]
		if pm == nil {
			break
		}
		ov := overlay(m.ID, "Inspect "+a.inspecting, 80, 80, pm.View(m.ID+".list"))
		ov.Scope = "inspect"
		ov.Facts = map[string]any{"inspecting": a.inspecting}
		ov.Badges = []ui.Badge{{Text: fmt.Sprintf("%d of %d", len(pm.Filtered()), len(pm.Items))}}
		if pm.Query != "" {
			ov.Badges = append(ov.Badges, ui.Badge{Text: "/" + pm.Query, Tone: "accent"})
		}
		return ov
	case modalLaunch:
		if a.launch == nil {
			break
		}
		ov := overlay(m.ID, a.launch.fm.Title, 70, 0, a.launch.fm.View(modalLaunch+".form"))
		ov.Scope = "launch"
		return ov
	case modalAnswer:
		if a.answer == nil {
			break
		}
		ov := overlay(m.ID, a.answer.fm.Title, 70, 0, a.answer.fm.View(modalAnswer+".form"))
		ov.Scope = "answer"
		return ov
	default:
		if pm := a.pickers[m.ID]; pm != nil {
			return overlay(m.ID, pm.Title, 64, 70, pm.View(m.ID+".list"))
		}
	}
	return overlay(m.ID, m.ID, 40, 0, text("", "nothing to show", "muted"))
}

// noSession says why the Screen shows no live session.
func (a *App) noSession() string {
	if a.Host == nil {
		return "No live terminal is attached in this build."
	}
	return "This pane has no live session yet. Enter starts one."
}
