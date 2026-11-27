package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"ofam.dev/matomeru/contextitems"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
)

const (
	projectSectionsModalID     = "project.sections"
	CmdProjectSectionPickDrill = "project.section.pick.drill"
	CmdProjectSectionPickBack  = "project.section.pick.back"
)

func (a *App) projectSectionPickerNestedEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetProjectSectionPickerNested, true)
}

func projectSectionPickerSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		{
			ID:          CmdProjectSectionPickDrill,
			Description: "Drill down into items of the focused Project Portal section",
			Meta:        map[string]any{"internal": true},
		},
		{
			ID:          CmdProjectSectionPickBack,
			Description: "Step back to Project Portal sections inside the picker",
			Meta:        map[string]any{"internal": true},
		},
	}
}

func projectSectionPickerBindings() []core.Binding {
	sc := projectSectionsModalID
	return []core.Binding{
		when(bind(ModePicker, sc, "l", CmdProjectSectionPickDrill, "drill into items"), "facts.canDrillDown"),
		hidden(when(bind(ModePicker, sc, "right", CmdProjectSectionPickDrill, "drill into items"), "facts.canDrillDown")),
		hidden(when(bind(ModePicker, sc, "tab", CmdProjectSectionPickDrill, "drill into items"), "facts.canDrillDown")),
		when(bind(ModePicker, sc, "h", CmdProjectSectionPickBack, "back to sections"), "facts.canStepBack"),
		hidden(when(bind(ModePicker, sc, "left", CmdProjectSectionPickBack, "back to sections"), "facts.canStepBack")),
		hidden(when(bind(ModePicker, sc, "shift+tab", CmdProjectSectionPickBack, "back to sections"), "facts.canStepBack")),
		hidden(when(bind(ModePicker, sc, "backspace", CmdProjectSectionPickBack, "back to sections"), "facts.canStepBack")),

		when(bind(ModePickerFilter, sc, "tab", CmdProjectSectionPickDrill, "drill into items"), "facts.canDrillDown"),
		hidden(when(bind(ModePickerFilter, sc, "right", CmdProjectSectionPickDrill, "drill into items"), "facts.canDrillDown")),
		when(bind(ModePickerFilter, sc, "shift+tab", CmdProjectSectionPickBack, "back to sections"), "facts.canStepBack"),
		hidden(when(bind(ModePickerFilter, sc, "left", CmdProjectSectionPickBack, "back to sections"), "facts.canStepBack && facts.query == \"\"")),
		hidden(when(bind(ModePickerFilter, sc, "backspace", CmdProjectSectionPickBack, "back to sections"), "facts.canStepBack && facts.query == \"\"")),
	}
}

func (a *App) registerProjectSectionPickerHandlers() {
	a.handle(CmdProjectSectionPickDrill, a.handleModal)
	a.handle(CmdProjectSectionPickBack, a.handleModal)
}

// --- Reusable nested drill-down picker modal ------------------------------------

type nestedPickerLevel struct {
	title       string
	placeholder string
	items       []picker.Item
	focusedID   string
}

type nestedPickerSpec struct {
	id           string
	scope        string
	rootTitle    string
	rootHint     string
	pageSize     int
	widthPct     int
	heightPct    int
	matchMode    picker.MatchMode
	nested       bool
	drillOnEnter bool
	rootItems    []picker.Item
	canDrill     func(it picker.Item) bool
	loadLevel    func(it picker.Item) (title, placeholder string, items []picker.Item, ok bool)
	onConfirm    func(depth int, it picker.Item) modal.Result
}

type nestedPickerState struct {
	spec   nestedPickerSpec
	levels []nestedPickerLevel
}

func (st *nestedPickerState) facts(pm *picker.Model) map[string]any {
	canDrill := false
	if st.spec.nested && len(st.levels) == 0 && st.spec.canDrill != nil {
		if focused, ok := pm.Focused(); ok {
			canDrill = st.spec.canDrill(focused)
		}
	}
	return map[string]any{
		"nestedPicker": st.spec.nested,
		"pickerDepth":  len(st.levels),
		"canDrillDown": canDrill,
		"canStepBack":  len(st.levels) > 0,
		"query":        pm.Query,
		"queryEmpty":   pm.Query == "",
	}
}

func (st *nestedPickerState) drillFocused(pm *picker.Model) (modal.Result, bool) {
	if !st.spec.nested || len(st.levels) > 0 {
		return modal.Result{}, false
	}
	it, ok := pm.Focused()
	if !ok {
		return modal.Result{Handled: true}, true
	}
	return st.drillInto(pm, it)
}

func (st *nestedPickerState) drillInto(pm *picker.Model, it picker.Item) (modal.Result, bool) {
	if !st.spec.nested || st.spec.loadLevel == nil {
		return modal.Result{}, false
	}
	if st.spec.canDrill != nil && !st.spec.canDrill(it) {
		return modal.Result{Handled: true}, true
	}
	title, placeholder, items, ok := st.spec.loadLevel(it)
	if !ok || len(items) == 0 {
		return modal.Result{Handled: true}, true
	}
	st.levels = append(st.levels, nestedPickerLevel{
		title:       pm.Title,
		placeholder: pm.Placeholder,
		items:       pm.Items,
		focusedID:   it.ID,
	})
	pm.Title = title
	pm.Placeholder = placeholder
	pm.SetItems(items)
	pm.SetQuery("")
	return modal.Result{Handled: true}, true
}

func (st *nestedPickerState) stepBack(pm *picker.Model) (modal.Result, bool) {
	if len(st.levels) == 0 {
		return modal.Result{}, false
	}
	prev := st.levels[len(st.levels)-1]
	st.levels = st.levels[:len(st.levels)-1]
	pm.Title = prev.title
	pm.Placeholder = prev.placeholder
	pm.SetItems(prev.items)
	pm.SetQuery("")
	selectPickerItemByID(pm, prev.focusedID)
	return modal.Result{Handled: true}, true
}

func selectPickerItemByID(pm *picker.Model, id string) {
	if pm == nil || id == "" {
		return
	}
	for i, it := range pm.Filtered() {
		if it.ID == id {
			pm.SelectedIdx = i
			return
		}
	}
}

func (a *App) newNestedPickerModal(spec nestedPickerSpec) *modal.PickerModal {
	st := &nestedPickerState{spec: spec}
	pm := picker.New(spec.rootTitle, spec.rootItems)
	pm.Scope = spec.scope
	pm.Placeholder = spec.rootHint
	pm.MatchMode = spec.matchMode
	return a.newPickerModal(pm, modal.PickerOptions{
		ID:        spec.id,
		Scope:     spec.scope,
		PageSize:  spec.pageSize,
		WidthPct:  spec.widthPct,
		HeightPct: spec.heightPct,
		Facts:     st.facts,
		OnCommand: func(m *picker.Model, cmd core.Command) (modal.Result, bool) {
			switch cmd.ID {
			case CmdProjectSectionPickDrill:
				res, ok := st.drillFocused(m)
				if ok {
					a.viewMemo.overlayGen++
				}
				return res, ok
			case CmdProjectSectionPickBack:
				res, ok := st.stepBack(m)
				if ok {
					a.viewMemo.overlayGen++
				}
				return res, ok
			}
			return modal.Result{}, false
		},
		OnConfirm: func(it picker.Item) modal.Result {
			if spec.drillOnEnter && spec.nested && len(st.levels) == 0 && spec.canDrill != nil && spec.canDrill(it) {
				if res, ok := st.drillInto(pm, it); ok {
					a.viewMemo.overlayGen++
					return res
				}
			}
			if spec.onConfirm != nil {
				return spec.onConfirm(len(st.levels), it)
			}
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
}

// --- Project Portal g s nested drill-down picker --------------------------------

type projectSectionPickEntry struct {
	section      string
	isOverview   bool
	nodeID       string
	ancestorKeys []string
	target       projectNavTarget
}

// projectSectionPick opens the Project Portal go-to modal picker. At Level 1 it
// lists all sections with item counts (enter opens the section directly; l /
// right / tab drills into that section's items). At Level 2 it fuzzy-filters
// and jumps directly to any leaf item while syncing the left-nav pointer.
func (a *App) projectSectionPick(s core.State, c core.Command) core.StepResult {
	ws, ok := a.resolveProjectNavWorkspace(s)
	if !ok {
		a.say("open a project workspace first")
		return core.Next(s)
	}
	defs := a.projectNavSectionDefs()
	modeArg := strings.ToLower(strings.TrimSpace(c.Arg("mode")))
	nested := a.projectSectionPickerNestedEnabled() && modeArg != "flat"
	drillOnEnter := modeArg == "nested" && nested
	activeSec := a.projectCtrl().activeSection(ws.projectKey)
	counts := a.projectSectionCounts(ws)
	defByKey := make(map[string]projectNavDef, len(defs))
	for _, d := range defs {
		defByKey[d.Key] = d
	}

	rootItems := a.projectSectionRootItems(ws, defs, counts, activeSec, nested)
	rootTitle := projectSectionRootTitle(ws.groupName, len(rootItems), nested)
	rootHint := "filter sections · enter open · l/tab drill into items"
	if !nested {
		rootHint = "filter sections · enter open"
	}

	m := a.newNestedPickerModal(nestedPickerSpec{
		id:           projectSectionsModalID,
		scope:        projectSectionsModalID,
		rootTitle:    rootTitle,
		rootHint:     rootHint,
		pageSize:     14,
		widthPct:     72,
		heightPct:    76,
		matchMode:    a.matchMode(),
		nested:       nested,
		drillOnEnter: drillOnEnter,
		rootItems:    rootItems,
		canDrill: func(it picker.Item) bool {
			def, exists := defByKey[it.ID]
			return exists && projectSectionDrillable(def, counts[def.Key])
		},
		loadLevel: func(it picker.Item) (string, string, []picker.Item, bool) {
			def, exists := defByKey[it.ID]
			if !exists {
				return "", "", nil, false
			}
			return a.projectSectionLevel2(ws, def)
		},
		onConfirm: func(_ int, it picker.Item) modal.Result {
			entry, ok := it.Data.(projectSectionPickEntry)
			if !ok {
				entry = projectSectionPickEntry{section: it.ID, isOverview: true}
			}
			projKey := ws.projectKey
			a.afterModal(func(st core.State) core.State {
				return a.openProjectSectionPickEntry(st, projKey, entry)
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})

	if secArg := strings.ToLower(strings.TrimSpace(c.Arg("section"))); secArg != "" && nested {
		selectPickerItemByID(m.Model(), secArg)
		_ = m.Handle(core.Command{ID: CmdProjectSectionPickDrill})
	}
	return core.Next(a.pushModal(s, m))
}

func projectSectionRootTitle(groupName string, count int, nested bool) string {
	if !nested {
		return fmt.Sprintf("Jump to Project Section (%d)", count)
	}
	if strings.TrimSpace(groupName) != "" {
		return fmt.Sprintf("Go to · %s (%d sections)", clip(groupName, 28), count)
	}
	return fmt.Sprintf("Go to · Project Sections (%d)", count)
}

func (a *App) projectSectionCounts(ws projectWorkspaceData) map[string]int {
	return map[string]int{
		"tasks":               len(ws.tasks),
		"pages":               len(ws.pages),
		"context":             len(ws.contextItems),
		"pinned":              len(ws.pinnedItems),
		"panes":               len(ws.panes),
		"lanes":               len(ws.allLanes),
		"worktrees":           len(ws.worktrees),
		"prs":                 len(ws.prItems),
		"documents":           len(ws.documents),
		"notes":               len(ws.notes),
		"questions":           len(ws.questions),
		"commands":            len(ws.commands),
		projectQueueSection:   a.projectOpenQueueItems(ws.projectID),
		projectThreadsSection: a.projectThreadCount(ws.projectID),
	}
}

func projectSectionDrillable(def projectNavDef, count int) bool {
	if def.Leaf || count <= 0 {
		return false
	}
	_, ok := projectSectionItemBuilders[def.Key]
	return ok
}

func (a *App) projectSectionRootItems(ws projectWorkspaceData, defs []projectNavDef, counts map[string]int, activeSec string, nested bool) []picker.Item {
	items := make([]picker.Item, 0, len(defs))
	for _, def := range defs {
		count := counts[def.Key]
		drillable := nested && projectSectionDrillable(def, count)
		title := formatSectionPickerTitle(def.Label, count, drillable)
		badges := sectionRootBadges(def, activeSec, count)
		items = append(items, picker.Item{
			ID:       def.Key,
			Title:    title,
			Subtitle: def.Key,
			Badges:   badges,
			Keywords: []string{def.Key, def.Label, def.Group},
			Data:     projectSectionPickEntry{section: def.Key, isOverview: true},
		})
	}
	return items
}

func formatSectionPickerTitle(label string, count int, drillable bool) string {
	if drillable {
		return fmt.Sprintf("%s (%d) ▸", label, count)
	}
	if count > 0 {
		return fmt.Sprintf("%s (%d)", label, count)
	}
	return label
}

func sectionRootBadges(def projectNavDef, activeSec string, count int) []ui.Badge {
	badge := def.Group
	tone := "muted"
	if def.Key == activeSec {
		badge = "active"
		tone = "accent"
	}
	out := []ui.Badge{{Text: badge, Tone: tone}}
	if def.Key == "questions" && count > 0 {
		out = append(out, ui.Badge{Text: fmt.Sprintf("%d", count), Tone: "muted"})
	}
	return out
}

// --- Level 2 section item builders ----------------------------------------------

type sectionItemsBuilder func(a *App, ws projectWorkspaceData) []picker.Item

var projectSectionItemBuilders = map[string]sectionItemsBuilder{
	"pages":     (*App).sectionPagePickItems,
	"panes":     (*App).sectionPanePickItems,
	"tasks":     (*App).sectionTaskPickItems,
	"lanes":     (*App).sectionLanePickItems,
	"documents": (*App).sectionDocumentPickItems,
	"notes":     (*App).sectionNotePickItems,
	"questions": (*App).sectionQuestionPickItems,
	"commands":  (*App).sectionCommandPickItems,
	"context": func(a *App, ws projectWorkspaceData) []picker.Item {
		return a.sectionContextPickItems(ws, "context", ws.contextItems)
	},
	"pinned": func(a *App, ws projectWorkspaceData) []picker.Item {
		return a.sectionContextPickItems(ws, "pinned", ws.pinnedItems)
	},
	"prs": func(a *App, ws projectWorkspaceData) []picker.Item {
		return a.sectionContextPickItems(ws, "prs", ws.prItems)
	},
	"worktrees": (*App).sectionWorktreePickItems,
}

func (a *App) projectSectionLevel2(ws projectWorkspaceData, def projectNavDef) (string, string, []picker.Item, bool) {
	builder, ok := projectSectionItemBuilders[def.Key]
	if !ok {
		return "", "", nil, false
	}
	leafItems := builder(a, ws)
	if len(leafItems) == 0 {
		return "", "", nil, false
	}
	all := make([]picker.Item, 0, len(leafItems)+1)
	all = append(all, sectionOverviewItem(def, len(leafItems)))
	all = append(all, leafItems...)
	title := fmt.Sprintf("Go to · %s (%d) · h/backspace back", def.Label, len(leafItems))
	placeholder := fmt.Sprintf("filter %s · enter open · h/backspace back", strings.ToLower(def.Label))
	return title, placeholder, all, true
}

func sectionOverviewItem(def projectNavDef, count int) picker.Item {
	return picker.Item{
		ID:       "overview." + def.Key,
		Title:    fmt.Sprintf("Open %s overview", def.Label),
		Subtitle: fmt.Sprintf("%s section (%d items)", def.Key, count),
		Badges:   []ui.Badge{{Text: "section", Tone: "muted"}},
		Keywords: []string{"overview", "section", def.Key, def.Label},
		Data:     projectSectionPickEntry{section: def.Key, isOverview: true},
	}
}

func (a *App) paneTitleMap(panes []data.Record) map[string]string {
	paneSchema := a.schema(data.KindPane)
	out := make(map[string]string, len(panes))
	for _, p := range panes {
		out[p.ID] = views.Title(paneSchema, p)
	}
	return out
}

func (a *App) resolvePaneLabel(paneTitles map[string]string, rec data.Record) string {
	pRef, ok := rec.RefField("pane")
	if !ok || pRef.ID == "" {
		return ""
	}
	if t, found := paneTitles[pRef.ID]; found {
		return t
	}
	t := a.title(pRef)
	paneTitles[pRef.ID] = t
	return t
}

func (a *App) sectionPagePickItems(ws projectWorkspaceData) []picker.Item {
	pages := sortRecordsNewestFirst(ws.pages)
	schema := a.schema(data.KindPage)
	paneTitles := a.paneTitleMap(ws.panes)
	parentFocus := projectNavPrefix + "pages"
	out := make([]picker.Item, 0, len(pages))
	for _, p := range pages {
		ref := p.Ref()
		title := nonEmptyTitle(views.Title(schema, p), p.ID)
		paneLabel := a.resolvePaneLabel(paneTitles, p)
		pageKind := strings.TrimSpace(p.Text("pageKind"))
		slug := strings.TrimSpace(p.Text("slug"))
		unread := p.IsUnread()
		sub := joinNonEmpty(" · ", paneLabel, pageKind)
		badges := pagePickBadges(unread, p.Bool("pinned"), pageKind)
		nodeFocus := projectNavNodePrefix + "pages." + p.ID
		out = append(out, picker.Item{
			ID:       "pages." + p.ID,
			Title:    title,
			Subtitle: sub,
			Unread:   unread,
			Ref:      &ref,
			Badges:   badges,
			Keywords: []string{p.ID, slug, pageKind, paneLabel, title},
			Data: projectSectionPickEntry{
				section: "pages",
				nodeID:  p.ID,
				target: projectNavTarget{
					FocusID:       nodeFocus,
					Section:       "pages",
					NodeID:        p.ID,
					ParentFocusID: parentFocus,
					Label:         title,
					Ref:           &ref,
					Unread:        unread,
				},
			},
		})
	}
	return out
}

func pagePickBadges(unread, pinned bool, pageKind string) []ui.Badge {
	var out []ui.Badge
	if unread {
		out = append(out, ui.Badge{Text: "Unread", Tone: "warning"})
	}
	if pinned {
		out = append(out, ui.Badge{Text: "pinned", Tone: "accent"})
	}
	if pageKind != "" {
		out = append(out, ui.Badge{Text: pageKind, Tone: "info"})
	}
	return out
}

func (a *App) sectionPanePickItems(ws projectWorkspaceData) []picker.Item {
	nodes := data.BuildTree(ws.panes, "parentPane", "", a.nav.showArchived)
	schema := a.schema(data.KindPane)
	var out []picker.Item
	var walk func(ns []data.TreeNode, depth int, parentFocus string, ancestors []string)
	walk = func(ns []data.TreeNode, depth int, parentFocus string, ancestors []string) {
		for _, nd := range ns {
			rec := nd.Record
			ref := rec.Ref()
			title := nonEmptyTitle(views.Title(schema, rec), rec.ID)
			display := indentPickTitle(title, depth)
			status := strings.TrimSpace(ws.live.paneStatus[rec.ID])
			if status == "" {
				status = strings.TrimSpace(rec.Text("runStatus"))
			}
			unread := a.isNavRecordUnread(data.KindPane, rec, &ws)
			harness := strings.TrimSpace(rec.Text("harness"))
			cwd := strings.TrimSpace(rec.Text("cwd"))
			sub := joinNonEmpty(" · ", harness, filepath.Base(cwd))
			if cwd == "" {
				sub = harness
			}
			nodeFocus := projectNavNodePrefix + "panes." + rec.ID
			out = append(out, picker.Item{
				ID:       "panes." + rec.ID,
				Title:    display,
				Subtitle: sub,
				Unread:   unread,
				Ref:      &ref,
				Badges:   panePickBadges(status, unread),
				Keywords: []string{rec.ID, harness, cwd, status, title},
				Data: projectSectionPickEntry{
					section:      "panes",
					nodeID:       rec.ID,
					ancestorKeys: slices.Clone(ancestors),
					target: projectNavTarget{
						FocusID:       nodeFocus,
						Section:       "panes",
						NodeID:        rec.ID,
						ParentFocusID: parentFocus,
						Label:         title,
						Ref:           &ref,
						Unread:        unread,
					},
				},
			})
			if len(nd.Children) > 0 {
				walk(nd.Children, depth+1, nodeFocus, append(ancestors, "panes."+rec.ID))
			}
		}
	}
	walk(nodes, 0, projectNavPrefix+"panes", nil)
	return out
}

func panePickBadges(status string, unread bool) []ui.Badge {
	var out []ui.Badge
	if status != "" {
		tone := "muted"
		switch status {
		case "needs_you", "waiting":
			tone = "error"
		case "running", "working":
			tone = "info"
		}
		out = append(out, ui.Badge{Text: status, Tone: tone})
	}
	if unread {
		out = append(out, ui.Badge{Text: "Unread", Tone: "warning"})
	}
	return out
}

func (a *App) sectionTaskPickItems(ws projectWorkspaceData) []picker.Item {
	return a.sectionTreePickItems(ws, "tasks", data.KindTask, ws.taskTree)
}

func (a *App) sectionLanePickItems(ws projectWorkspaceData) []picker.Item {
	nodes := data.BuildTree(ws.allLanes, "parentLane", "", a.treeIncludesArchived(data.KindLane))
	return a.sectionTreePickItems(ws, "lanes", data.KindLane, nodes)
}

func (a *App) sectionTreePickItems(ws projectWorkspaceData, secKey, kind string, nodes []data.TreeNode) []picker.Item {
	schema := a.schema(kind)
	styleOpts := a.styleOptions()
	var out []picker.Item
	var walk func(ns []data.TreeNode, depth int, parentFocus string, ancestors []string)
	walk = func(ns []data.TreeNode, depth int, parentFocus string, ancestors []string) {
		for _, nd := range ns {
			rec := nd.Record
			ref := rec.Ref()
			title := nonEmptyTitle(views.Title(schema, rec), rec.ID)
			rs := views.ResolveRecordStyle(schema, rec, styleOpts)
			display := indentPickTitle(joinNonEmpty(" ", rs.Icon, title), depth)
			unread := a.isNavRecordUnread(kind, rec, &ws)
			badges := recordPickBadges(schema, rec, unread)
			sub := clip(strings.TrimSpace(rec.Text("summary")), 52)
			nodeFocus := projectNavNodePrefix + secKey + "." + rec.ID
			out = append(out, picker.Item{
				ID:       secKey + "." + rec.ID,
				Title:    display,
				Subtitle: sub,
				Unread:   unread,
				Ref:      &ref,
				Badges:   badges,
				Keywords: []string{rec.ID, title, sub, rec.Text("workStatus"), rec.Text("laneStatus")},
				Data: projectSectionPickEntry{
					section:      secKey,
					nodeID:       rec.ID,
					ancestorKeys: slices.Clone(ancestors),
					target: projectNavTarget{
						FocusID:       nodeFocus,
						Section:       secKey,
						NodeID:        rec.ID,
						ParentFocusID: parentFocus,
						Label:         title,
						Ref:           &ref,
						Unread:        unread,
					},
				},
			})
			if len(nd.Children) > 0 {
				walk(nd.Children, depth+1, nodeFocus, append(ancestors, secKey+"."+rec.ID))
			}
		}
	}
	walk(nodes, 0, projectNavPrefix+secKey, nil)
	return out
}

func (a *App) sectionDocumentPickItems(ws projectWorkspaceData) []picker.Item {
	return a.sectionChronoRecordPickItems(ws, "documents", data.KindDocument, ws.documents, func(r data.Record) (string, []string) {
		fp := strings.TrimSpace(r.Text("filePath"))
		dk := strings.TrimSpace(r.Text("docKind"))
		return joinNonEmpty(" · ", dk, fp), []string{dk, fp}
	})
}

func (a *App) sectionNotePickItems(ws projectWorkspaceData) []picker.Item {
	refTitles := a.projectWorkspaceRefTitles(ws)
	aboutByNote := firstOutgoingRelByFrom(ws.relations, data.RelAbout, data.KindNote)
	return a.sectionChronoRecordPickItems(ws, "notes", data.KindNote, ws.notes, func(r data.Record) (string, []string) {
		target := a.projectNoteTargetLabelFrom(r, refTitles, aboutByNote)
		excerpt := clip(strings.ReplaceAll(strings.TrimSpace(r.Text("body")), "\n", " "), 44)
		return joinNonEmpty(" · ", target, excerpt), []string{target, r.Text("authorKind"), excerpt}
	})
}

func (a *App) sectionQuestionPickItems(ws projectWorkspaceData) []picker.Item {
	paneTitles := a.paneTitleMap(ws.panes)
	return a.sectionChronoRecordPickItems(ws, "questions", data.KindQuestion, ws.questions, func(r data.Record) (string, []string) {
		paneLabel := a.resolvePaneLabel(paneTitles, r)
		qk := strings.TrimSpace(r.Text("questionKind"))
		st := strings.TrimSpace(r.Text("status"))
		return joinNonEmpty(" · ", paneLabel, qk), []string{paneLabel, qk, st}
	})
}

func (a *App) sectionCommandPickItems(ws projectWorkspaceData) []picker.Item {
	return a.sectionChronoRecordPickItems(ws, "commands", data.KindCommand, ws.commands, func(r data.Record) (string, []string) {
		cmdText := clip(strings.TrimSpace(r.Text("command")), 56)
		return cmdText, []string{cmdText}
	})
}

func (a *App) sectionChronoRecordPickItems(ws projectWorkspaceData, secKey, kind string, recs []data.Record, metaFn func(data.Record) (string, []string)) []picker.Item {
	sorted := sortRecordsNewestFirst(recs)
	schema := a.schema(kind)
	styleOpts := a.styleOptions()
	parentFocus := projectNavPrefix + secKey
	out := make([]picker.Item, 0, len(sorted))
	for _, rec := range sorted {
		ref := rec.Ref()
		title := nonEmptyTitle(views.Title(schema, rec), rec.ID)
		rs := views.ResolveRecordStyle(schema, rec, styleOpts)
		unread := a.isNavRecordUnread(kind, rec, &ws)
		sub, extraKW := metaFn(rec)
		nodeFocus := projectNavNodePrefix + secKey + "." + rec.ID
		kw := append([]string{rec.ID, title}, extraKW...)
		out = append(out, picker.Item{
			ID:       secKey + "." + rec.ID,
			Title:    joinNonEmpty(" ", rs.Icon, title),
			Subtitle: sub,
			Unread:   unread,
			Ref:      &ref,
			Badges:   recordPickBadges(schema, rec, unread),
			Keywords: kw,
			Data: projectSectionPickEntry{
				section: secKey,
				nodeID:  rec.ID,
				target: projectNavTarget{
					FocusID:       nodeFocus,
					Section:       secKey,
					NodeID:        rec.ID,
					ParentFocusID: parentFocus,
					Label:         title,
					Ref:           &ref,
					Unread:        unread,
				},
			},
		})
	}
	return out
}

func (a *App) sectionContextPickItems(ws projectWorkspaceData, secKey string, items []CollectedContextItem) []picker.Item {
	parentFocus := projectNavPrefix + secKey
	seen := make(map[string]int, len(items))
	out := make([]picker.Item, 0, len(items))
	for i, it := range items {
		nodeID := uniqueContextPickNodeID(it, i, seen)
		label := contextPickLabel(it)
		var ref *core.Ref
		if it.RecordID != "" {
			r := core.Ref{Kind: data.KindContext, ID: it.RecordID}
			ref = &r
		}
		itemCopy := it
		nodeFocus := projectNavNodePrefix + secKey + "." + nodeID
		out = append(out, picker.Item{
			ID:       secKey + "." + nodeID,
			Title:    clip(label, 52),
			Subtitle: joinNonEmpty(" · ", it.PaneTitle, clip(singleLineContextText(it.Value), 44)),
			Ref:      ref,
			Badges:   contextPickBadges(it),
			Keywords: []string{it.Kind, it.Source, it.PaneTitle, it.Value, label},
			Data: projectSectionPickEntry{
				section: secKey,
				nodeID:  nodeID,
				target: projectNavTarget{
					FocusID:       nodeFocus,
					Section:       secKey,
					NodeID:        nodeID,
					ParentFocusID: parentFocus,
					Label:         label,
					Ref:           ref,
					ContextKey:    it.Key,
					ContextItem:   &itemCopy,
				},
			},
		})
	}
	return out
}

func uniqueContextPickNodeID(it CollectedContextItem, idx int, seen map[string]int) string {
	nodeID := it.RecordID
	if nodeID == "" {
		nodeID = projectNavSafeID(it.CanonKey, idx)
	}
	if n := seen[nodeID]; n > 0 {
		nodeID = fmt.Sprintf("%s_%d", nodeID, n)
	}
	seen[nodeID]++
	return nodeID
}

func contextPickLabel(it CollectedContextItem) string {
	if label := singleLineContextText(it.Label); label != "" {
		return label
	}
	if it.Kind == contextitems.KindCode {
		return data.CodeContextTitle(it.Value, it.Language)
	}
	return singleLineContextText(it.Value)
}

func contextPickBadges(it CollectedContextItem) []ui.Badge {
	var out []ui.Badge
	if it.Official {
		out = append(out, ui.Badge{Text: "★ official", Tone: "accent"})
	}
	if it.Kind != "" {
		out = append(out, ui.Badge{Text: it.Kind, Tone: "info"})
	}
	return out
}

func (a *App) sectionWorktreePickItems(ws projectWorkspaceData) []picker.Item {
	parentFocus := projectNavPrefix + "worktrees"
	seen := make(map[string]int, len(ws.worktrees))
	out := make([]picker.Item, 0, len(ws.worktrees))
	for i, wt := range ws.worktrees {
		nodeID := projectNavSafeID(wt.Path, i)
		if n := seen[nodeID]; n > 0 {
			nodeID = fmt.Sprintf("%s_%d", nodeID, n)
		}
		seen[nodeID]++
		label := nonEmptyTitle(strings.TrimSpace(wt.Branch), filepath.Base(wt.Path))
		if label == "." {
			label = wt.Path
		}
		var ref *core.Ref
		if wt.Ref.ID != "" && wt.Ref.Kind != "" {
			r := wt.Ref
			ref = &r
		}
		wtCopy := wt
		nodeFocus := projectNavNodePrefix + "worktrees." + nodeID
		out = append(out, picker.Item{
			ID:       "worktrees." + nodeID,
			Title:    label,
			Subtitle: wt.Path,
			Ref:      ref,
			Badges:   []ui.Badge{{Text: wt.Source, Tone: "info"}},
			Keywords: []string{wt.Branch, wt.Repo, wt.Path, wt.Source},
			Data: projectSectionPickEntry{
				section: "worktrees",
				nodeID:  nodeID,
				target: projectNavTarget{
					FocusID:       nodeFocus,
					Section:       "worktrees",
					NodeID:        nodeID,
					ParentFocusID: parentFocus,
					Label:         label,
					Ref:           ref,
					Worktree:      &wtCopy,
				},
			},
		})
	}
	return out
}

// --- Selection execution & left-nav pointer sync --------------------------------

func (a *App) openProjectSectionPickEntry(s core.State, projKey string, entry projectSectionPickEntry) core.State {
	if a.panes != nil {
		a.panes.zoom = false
	}
	if entry.isOverview || entry.target.FocusID == "" {
		return a.openProjectSectionOverview(s, entry.section)
	}
	pc := a.projectCtrl()
	pc.setSection(projKey, entry.section)
	pc.setOpenPageID(projKey, "")
	if entry.section != "panes" {
		if pc.childPaneByProject != nil {
			delete(pc.childPaneByProject, projKey)
		}
		if pc.childPaneReturnFocus != nil {
			delete(pc.childPaneReturnFocus, projKey)
		}
		s = withMode(s, ModeNormal)
	}
	if a.projectNavTreeEnabled() {
		pc.setNavSectionExpanded(projKey, entry.section, true)
		for _, ak := range entry.ancestorKeys {
			pc.setNavNodeExpanded(projKey, ak, true)
		}
		a.syncProjectNavFocus(entry.target.FocusID)
	} else {
		a.syncProjectNavPointer(entry.section)
	}
	pc.registerNavTarget(projKey, entry.target)
	a.viewMemo.gen++
	return a.projectNavNodeOpen(s, entry.target.FocusID).State
}

func (a *App) openProjectSectionOverview(s core.State, secKey string) core.State {
	s = withMode(s, ModeNormal)
	return a.projectSectionSelect(s, core.Command{
		ID:   CmdProjectSectionSelect,
		Args: map[string]any{"section": secKey},
	}).State
}

// --- Small pure formatting helpers ----------------------------------------------

func nonEmptyTitle(title, fallback string) string {
	if t := strings.TrimSpace(title); t != "" {
		return t
	}
	return fallback
}

func indentPickTitle(title string, depth int) string {
	if depth <= 0 {
		return title
	}
	return strings.Repeat("  ", depth) + "↳ " + title
}

func joinNonEmpty(sep string, parts ...string) string {
	var kept []string
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			kept = append(kept, s)
		}
	}
	return strings.Join(kept, sep)
}

func recordPickBadges(schema data.Schema, rec data.Record, unread bool) []ui.Badge {
	out := views.StatusBadges(schema, rec)
	if unread {
		out = append([]ui.Badge{{Text: "Unread", Tone: "warning"}}, out...)
	}
	return out
}
