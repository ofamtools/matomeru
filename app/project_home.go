package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/toast"
	"ofam.dev/ochazuke/widgets/tree"
)

// Commands and widget IDs for the Project Page workspace shell.
const (
	CmdProjectSectionSelect  = "project.section.select"
	CmdProjectPageBack       = "project.page.back"
	CmdProjectContentOpen    = "project.content.open"
	CmdContextOfficialToggle = "context.official.toggle"
	CmdContextAddOfficial    = "context.official.add"

	projectNavPrefix              = views.ProjectNavPrefix
	projectHomeSummaryID          = "project.home.summary"
	projectSignalsNodeID          = "project.signals.placeholder"
	projectPagesTable             = "project.pages.table"
	projectPagesOutlineRowID      = "project.pages.outline"
	projectPinnedTable            = "project.pinned.table"
	projectPanesTree              = "project.panes.tree"
	projectLanesTree              = "project.lanes.tree"
	projectWorktreesTable         = "project.worktrees.table"
	projectPRsTable               = "project.prs.table"
	projectDocumentsTable         = "project.documents.table"
	projectNotesTable             = "project.notes.table"
	projectQuestionsTable         = "project.questions.table"
	projectCommandsTable          = "project.commands.table"
	projectTasksFlatTable         = "project.tasks.table"
	projectContextOfficialModalID = "context.official.add"

	scopeProjectPages     = "project.pages"
	scopeProjectPanes     = "project.panes"
	scopeProjectDocuments = "project.documents"
	scopeProjectWorktrees = "project.worktrees"
)

type projectNavDef struct {
	Key, Label, Icon, Group string
}

var defaultProjectNavSections = []projectNavDef{
	{Key: "home", Label: "Home", Icon: "⌂", Group: "Core"},
	{Key: "tasks", Label: "Tasks", Icon: "☐", Group: "Core"},
	{Key: "pages", Label: "Pages", Icon: "◫", Group: "Core"},
	{Key: "signals", Label: "Needs Attention", Icon: "!", Group: "Core"},
	{Key: "context", Label: "Context", Icon: "◈", Group: "Context"},
	{Key: "pinned", Label: "Pinned Links & Files", Icon: "★", Group: "Context"},
	{Key: "panes", Label: "Panes", Icon: "▣", Group: "Workspace"},
	{Key: "lanes", Label: "Lanes", Icon: "⎇", Group: "Workspace"},
	{Key: "worktrees", Label: "Worktrees", Icon: "⌘", Group: "Workspace"},
	{Key: "prs", Label: "PRs", Icon: "⇅", Group: "Workspace"},
	{Key: "documents", Label: "Documents", Icon: "▤", Group: "Knowledge"},
	{Key: "notes", Label: "Notes", Icon: "✎", Group: "Knowledge"},
	{Key: "questions", Label: "Questions & Decisions", Icon: "?", Group: "Knowledge"},
	{Key: "commands", Label: "Commands", Icon: "$", Group: "Knowledge"},
}

// ProjectController owns per-project workspace navigation state: active left
// nav section, open inline ui.page, and per-section table/tree models.
type ProjectController struct {
	sectionByProject  map[string]string
	openPageByProject map[string]string
	tables            map[string]*table.Model
	trees             map[string]*tree.Model
	activeProjectKey  string
	activeProjectID   string
	activeGroupName   string
	activeProjectRef  *core.Ref
}

func newProjectController() *ProjectController {
	return &ProjectController{
		sectionByProject:  map[string]string{},
		openPageByProject: map[string]string{},
		tables:            map[string]*table.Model{},
		trees:             map[string]*tree.Model{},
	}
}

func (a *App) projectCtrl() *ProjectController {
	if a.proj == nil {
		a.proj = newProjectController()
	}
	return a.proj
}

func (pc *ProjectController) activeSection(projectID string) string {
	if pc != nil {
		if sec := strings.TrimSpace(pc.sectionByProject[projectID]); sec != "" {
			return sec
		}
	}
	return "home"
}

func (pc *ProjectController) setSection(projectID, key string) {
	if pc == nil || key == "" {
		return
	}
	if pc.sectionByProject == nil {
		pc.sectionByProject = map[string]string{}
	}
	pc.sectionByProject[projectID] = key
}

func (pc *ProjectController) openPageID(projectID string) string {
	if pc == nil {
		return ""
	}
	return pc.openPageByProject[projectID]
}

func (pc *ProjectController) setOpenPageID(projectID, id string) {
	if pc == nil {
		return
	}
	if pc.openPageByProject == nil {
		pc.openPageByProject = map[string]string{}
	}
	if id == "" {
		delete(pc.openPageByProject, projectID)
		return
	}
	pc.openPageByProject[projectID] = id
}

func (a *App) projectWorkspaceEnabled() bool {
	return a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetProjectWorkspaceInPanes, true)
}

func (a *App) projectOutlineAsPageEnabled() bool {
	return a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetProjectOutlineAsPage, true)
}

func (a *App) projectNavSectionDefs() []projectNavDef {
	raw := ""
	if a.cfg != nil && a.cfg.Settings != nil {
		raw = strings.TrimSpace(a.cfg.Settings.String(SetProjectNavSections, ""))
	}
	if raw == "" {
		return slices.Clone(defaultProjectNavSections)
	}
	byKey := make(map[string]projectNavDef, len(defaultProjectNavSections))
	for _, d := range defaultProjectNavSections {
		byKey[d.Key] = d
	}
	var out []projectNavDef
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		k := strings.ToLower(strings.TrimSpace(part))
		if d, ok := byKey[k]; ok && !seen[k] {
			seen[k] = true
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		return slices.Clone(defaultProjectNavSections)
	}
	return out
}

func projectKeyFor(groupName string, projRef *core.Ref) string {
	if projRef != nil && projRef.ID != "" {
		return projRef.ID
	}
	return "group:" + groupName
}

// projectHomeSpecs returns the CommandSpecs owned by the Project Page workspace.
func projectHomeSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		{
			ID:          CmdProjectSectionSelect,
			Description: "Select a Project Page left-navigation section and focus its content",
			Params:      []core.ParamSpec{{Name: "section", Type: core.ParamString, Description: "section key (home, tasks, pages, context, ...)"}},
		},
		spec(CmdProjectPageBack, "Return from an open project page to the Pages index or Project nav"),
		spec(CmdProjectContentOpen, "Open the focused item inside the Project Page content area"),
		spec(CmdContextOfficialToggle, "Promote or demote the focused context item as Official Project Context", "editing"),
		spec(CmdContextAddOfficial, "Add an Official Project Context link, Google Doc/Sheet, PR, or file", "editing"),
	}
}

// projectHomeBindings returns key bindings strictly scoped to the Project Page
// workspace regions so work.table and panes.rail footer hints stay clean.
func projectHomeBindings() []core.Binding {
	n := ModeNormal
	return []core.Binding{
		bind(n, views.ScopeProjectNav, "enter", CmdProjectSectionSelect, "open section"),
		when(bind(n, views.ScopeProjectContent, "esc", CmdProjectPageBack, "back to pages"), "facts.projectOpenPage"),
		hidden(when(bind(n, views.ScopeProjectContent, "h", CmdProjectPageBack, "back to pages"), "facts.projectOpenPage")),
		hidden(when(bind(n, views.ScopeProjectContent, "esc", CmdProjectPageBack, "back to nav"), "!facts.projectOpenPage && !facts.visualActive && !(facts.markedCount > 0)")),
		bind(n, scopeProjectPages, "enter", CmdProjectContentOpen, "open page"),
		bind(n, scopeProjectPanes, "enter", CmdProjectContentOpen, "jump to pane"),
		bind(n, scopeProjectDocuments, "enter", CmdProjectContentOpen, "read document"),
		bind(n, scopeProjectWorktrees, "enter", CmdProjectContentOpen, "open worktree"),
		when(bind(n, ScopeProjectContext, "p", CmdContextOfficialToggle, "toggle official"), "facts.contextItem"),
		hidden(when(bind(n, ScopeProjectOverview, "p", CmdContextOfficialToggle, "toggle official"), "facts.contextItem")),
		when(bind(n, views.ScopeProjectContent, "N", CmdContextAddOfficial, "add official context"), "facts.section == \"context\" || facts.section == \"pinned\" || facts.contextItem"),
		hidden(bind(n, ScopeProjectOverview, "N", CmdContextAddOfficial, "add official context")),
		hidden(bind(n, SpacePanes, "P", CmdProjectPageOpen, "project page")),
	}
}

// registerProjectHomeHandlers registers all Project Page command handlers.
func (a *App) registerProjectHomeHandlers() {
	a.handle(CmdProjectSectionSelect, a.projectSectionSelect)
	a.handle(CmdProjectPageBack, a.projectPageBack)
	a.handle(CmdProjectContentOpen, a.projectContentOpen)
	a.handle(CmdContextOfficialToggle, a.contextOfficialToggle)
	a.handle(CmdContextAddOfficial, a.contextAddOfficial)
}

// projectOverviewView renders the right side of 1 Panes when a Project header
// row is selected in the left rail.
func (a *App) projectOverviewView(s core.State, groupName string, projRef *core.Ref) ui.ViewNode {
	pc := a.projectCtrl()
	projKey := projectKeyFor(groupName, projRef)
	pc.activeProjectKey, pc.activeGroupName = projKey, groupName
	if projRef != nil {
		r := *projRef
		pc.activeProjectRef, pc.activeProjectID = &r, projRef.ID
	} else {
		pc.activeProjectRef, pc.activeProjectID = nil, ""
	}
	if !a.projectWorkspaceEnabled() {
		return a.projectOverviewFallbackView(groupName, projRef)
	}

	defs := a.projectNavSectionDefs()
	activeKey := pc.activeSection(projKey)
	if s.ActivePage == SpacePanes {
		if secKey, ok := strings.CutPrefix(s.Pages[SpacePanes].FocusID, projectNavPrefix); ok && secKey != "" {
			if slices.ContainsFunc(defs, func(d projectNavDef) bool { return d.Key == secKey }) {
				activeKey = secKey
				pc.setSection(projKey, secKey)
			}
		}
	}
	if !slices.ContainsFunc(defs, func(d projectNavDef) bool { return d.Key == activeKey }) && len(defs) > 0 {
		activeKey = defs[0].Key
		pc.setSection(projKey, activeKey)
	}

	ws := a.collectProjectWorkspaceData(groupName, projRef)
	badges := make([]ui.Badge, 0, 5)
	if ws.projRec != nil {
		badges = append(badges, views.StatusBadges(a.schema(data.KindTask), *ws.projRec)...)
	}
	badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d panes", len(ws.panes))})
	if ws.officialCount > 0 {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("★ %d official", ws.officialCount), Tone: "accent"})
	}
	if ws.openQuestions > 0 {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d open Q", ws.openQuestions), Tone: "warning"})
	}

	var subParts []string
	if ws.projRec != nil {
		if repo := strings.TrimSpace(ws.projRec.Text("repo")); repo != "" {
			subParts = append(subParts, "repo: "+repo)
		}
		if dir := strings.TrimSpace(ws.projRec.Text("defaultDir")); dir != "" {
			subParts = append(subParts, "dir: "+dir)
		}
		if desc := strings.TrimSpace(ws.projRec.Text("description")); desc != "" {
			subParts = append(subParts, clip(desc, 64))
		}
	} else {
		subParts = append(subParts, "Panes not assigned to any project task (L associates a pane)")
	}

	return views.ProjectWorkspaceView(views.ProjectWorkspaceSpec{
		ProjectID: ws.projectID, Title: groupName, Subtitle: strings.Join(subParts, " · "),
		Ref: projRef, Badges: badges, NavRatio: 0.24, ActiveKey: activeKey,
		NavItems:   a.buildProjectNavItems(defs, ws, activeKey),
		ActiveBody: a.buildProjectSectionBody(s, ws, activeKey),
		Facts: map[string]any{
			"projectGroup": groupName, "paneCount": len(ws.panes),
			"contextCount": len(ws.contextItems), "officialCount": ws.officialCount,
		},
	})
}

func (a *App) projectOverviewFallbackView(groupName string, projRef *core.Ref) ui.ViewNode {
	ws := a.collectProjectWorkspaceData(groupName, projRef)
	ctxNode := a.projectContextTableModel(ws).View(projectContextTable)
	ctxNode.Scope = ScopeProjectContext
	if ctxNode.Facts == nil {
		ctxNode.Facts = map[string]any{}
	}
	ctxNode.Facts["projectOverview"] = true
	summary := fmt.Sprintf(" %d panes · %d tasks · %d lanes · %d docs · %d context",
		len(ws.panes), len(ws.tasks), len(ws.activeLanes), len(ws.documents), len(ws.contextItems))
	badges := []ui.Badge{{Text: fmt.Sprintf("%d panes", len(ws.panes))}, {Text: fmt.Sprintf("%d context", len(ws.contextItems)), Tone: "accent"}}
	if len(ws.documents) > 0 {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d docs", len(ws.documents)), Tone: "info"})
	}
	return ui.ViewNode{
		ID: "project.overview", Kind: ui.KindPanel, Scope: ScopeProjectOverview, Ref: projRef,
		Title: "Project · " + groupName, Border: true, Badges: badges,
		Facts: map[string]any{"projectGroup": groupName, "paneCount": len(ws.panes), "contextCount": len(ws.contextItems)},
		Children: []ui.ViewNode{{
			Kind: ui.KindVStack, Gap: 1,
			Children: []ui.ViewNode{
				line("project.overview.summary", summary, "muted"),
				{ID: "project.context", Kind: ui.KindPanel, Scope: ScopeProjectContext, Title: "Context Items", Border: true, Box: layout.Constraints{Flex: 1}, Children: []ui.ViewNode{ctxNode}},
			},
		}},
	}
}

// projectHomeOpen jumps to the Project Page workspace in 1 Panes (or the Show
// page when project.workspaceInPanes is false).
func (a *App) projectHomeOpen(s core.State, c core.Command) core.StepResult {
	projRef, ok := a.resolveProjectTargetRef(s, c)
	if !ok {
		a.say("no project task assigned")
		return core.Next(s)
	}
	if !a.projectWorkspaceEnabled() {
		return core.Next(a.show(s, projRef))
	}
	s = a.openSpace(s, SpacePanes)
	rail := a.rail()
	groupName := a.findRailGroupForProject(projRef.ID)
	if groupName == "" {
		groupName = a.title(projRef)
	}
	selected := selectGroupHeader(rail, groupName)
	if !selected && rail.Query != "" {
		rail.Query = ""
		selected = selectGroupHeader(rail, groupName)
	}
	if !selected {
		return core.Next(a.show(s, projRef))
	}
	pc := a.projectCtrl()
	projKey := projectKeyFor(groupName, &projRef)
	if secArg := strings.TrimSpace(c.Arg("section")); secArg != "" {
		pc.setSection(projKey, secArg)
	}
	sec := pc.activeSection(projKey)
	defs := a.projectNavSectionDefs()
	if !slices.ContainsFunc(defs, func(d projectNavDef) bool { return d.Key == sec }) && len(defs) > 0 {
		sec = defs[0].Key
		pc.setSection(projKey, sec)
	}
	a.say("project: %s", groupName)
	return core.Next(withPage(s, SpacePanes, projectNavPrefix+sec, ModeNormal))
}

func (a *App) resolveProjectTargetRef(s core.State, c core.Command) (core.Ref, bool) {
	if a.Store == nil {
		return core.Ref{}, false
	}
	for _, key := range []string{"project", "id"} {
		if id := strings.TrimSpace(c.Arg(key)); id != "" {
			if rootID := a.Store.RootProjectID(id); rootID != "" {
				return core.Ref{Kind: data.KindTask, ID: rootID}, true
			}
		}
	}
	if ref, ok := a.railActiveProjectTask(s); ok {
		return ref, true
	}
	if pc := a.projectCtrl(); pc.activeProjectID != "" {
		return core.Ref{Kind: data.KindTask, ID: pc.activeProjectID}, true
	}
	if ref, ok := a.subject(s, c); ok {
		if ref.Kind == data.KindTask {
			return core.Ref{Kind: data.KindTask, ID: a.Store.RootProjectID(ref.ID)}, true
		}
		if rec, err := a.Store.Get(ref); err == nil && rec != nil {
			if tRef, has := rec.RefField("task"); has && tRef.Kind == data.KindTask {
				return core.Ref{Kind: data.KindTask, ID: a.Store.RootProjectID(tRef.ID)}, true
			}
		}
	}
	return core.Ref{}, false
}

func (a *App) findRailGroupForProject(projectID string) string {
	if a.panes == nil || projectID == "" {
		return ""
	}
	for group, ref := range a.panes.railProjectRefs {
		if ref.ID == projectID {
			return group
		}
	}
	return ""
}

func (a *App) projectSectionSelect(s core.State, c core.Command) core.StepResult {
	pc := a.projectCtrl()
	sec := strings.TrimSpace(c.Arg("section"))
	if sec == "" {
		if k, ok := strings.CutPrefix(s.ActiveFrame().FocusID, projectNavPrefix); ok && k != "" {
			sec = k
		}
	}
	if sec == "" {
		sec = pc.activeSection(pc.activeProjectKey)
	}
	if pc.activeSection(pc.activeProjectKey) != sec {
		pc.setOpenPageID(pc.activeProjectKey, "")
	}
	pc.setSection(pc.activeProjectKey, sec)
	s = withFocus(s, projectNavPrefix+sec)
	a.viewMemo.gen++
	for _, n := range a.focusables(s) {
		if n.region == "project.content" {
			return core.Next(withFocus(s, n.id))
		}
	}
	return core.Next(a.column(s, 1))
}

func (a *App) projectPageBack(s core.State, _ core.Command) core.StepResult {
	pc := a.projectCtrl()
	if pc.openPageID(pc.activeProjectKey) != "" {
		pc.setOpenPageID(pc.activeProjectKey, "")
		a.viewMemo.gen++
		a.say("pages index")
		return core.Next(withFocus(s, projectPagesTable))
	}
	target := projectNavPrefix + pc.activeSection(pc.activeProjectKey)
	for _, n := range a.focusables(s) {
		if n.id == target {
			return core.Next(withFocus(s, target))
		}
	}
	return core.Next(a.column(s, -1))
}

func (a *App) projectContentOpen(s core.State, c core.Command) core.StepResult {
	focus, pc := s.ActiveFrame().FocusID, a.projectCtrl()
	switch focus {
	case projectPagesTable:
		tbl := a.nav.tables[projectPagesTable]
		if tbl == nil {
			return core.Next(s)
		}
		if it, ok := tbl.Focused(); ok && it.Header {
			tbl.ToggleGroup()
			return core.Next(s)
		}
		row, ok := tbl.FocusedRow()
		if !ok {
			a.say("no page selected")
			return core.Next(s)
		}
		if row.ID == projectPagesOutlineRowID {
			pc.setOpenPageID(pc.activeProjectKey, "")
			pc.setSection(pc.activeProjectKey, "tasks")
			a.viewMemo.gen++
			for _, n := range a.focusables(s) {
				if n.region == "project.content" {
					return core.Next(withFocus(s, n.id))
				}
			}
			return core.Next(withFocus(s, projectNavPrefix+"tasks"))
		}
		if row.Ref == nil || row.Ref.Kind != data.KindPage {
			return core.Next(s)
		}
		page, err := a.Store.Get(*row.Ref)
		if err != nil {
			a.fail("open page", err)
			return core.Next(s)
		}
		if page.IsUnread() && a.Router != nil {
			_, _ = a.Router.MarkViewed(page.ID, viewer)
		}
		pc.setOpenPageID(pc.activeProjectKey, page.ID)
		a.setPageScrollOffset(page.ID, 0)
		a.viewMemo.gen++
		a.say("%s · esc / h back to pages", page.Text("title"))
		return core.Next(withFocus(s, views.PageScrollID))
	case projectPanesTree:
		if tm := pc.trees[projectPanesTree]; tm != nil {
			if row, ok := tm.Focused(); ok && row.Node.Ref != nil && row.Node.Ref.Kind == data.KindPane {
				return core.Next(a.jumpToPane(s, row.Node.Ref.ID, true))
			}
		}
		a.say("no pane selected")
		return core.Next(s)
	case projectDocumentsTable:
		if ref, ok := a.subject(s, c); ok && ref.Kind == data.KindDocument {
			if rec, err := a.Store.Get(ref); err == nil && a.openable(*rec) {
				return a.openTargetCmd(s, c)
			}
			return core.Next(a.show(s, ref))
		}
	case projectWorktreesTable:
		if tbl := a.nav.tables[projectWorktreesTable]; tbl != nil {
			if row, ok := tbl.FocusedRow(); ok {
				if p, _ := row.Cells["path"].(string); strings.TrimSpace(p) != "" {
					if row.Ref != nil && row.Ref.Kind != "directory" && row.Ref.ID != "" {
						return core.Next(a.show(s, *row.Ref))
					}
					return a.openContextValue(s, "file", p, nil)
				}
			}
		}
		return core.Next(s)
	}
	return a.openFocused(s, c)
}

func (a *App) contextOfficialToggle(s core.State, c core.Command) core.StepResult {
	if a.Store == nil {
		return core.Next(s)
	}
	pc, focus := a.projectCtrl(), s.ActiveFrame().FocusID
	ctxItem, hasItem := a.focusedProjectContextItemFor(focus)
	recID := ctxItem.RecordID
	if recID == "" {
		if ref, ok := a.subject(s, c); ok && ref.Kind == data.KindContext && ref.ID != "" {
			recID = ref.ID
		}
	}
	if recID != "" {
		if rec, err := a.Store.Get(core.Ref{Kind: data.KindContext, ID: recID}); err == nil && rec != nil {
			next := !rec.Bool("official")
			updated, err := a.Store.SetContextOfficial(rec.Ref(), next, nil, nil, User)
			if err != nil {
				a.fail("toggle official context", err)
				return core.Next(s)
			}
			a.invalidateContextIndex()
			label := clip(views.Title(a.schema(data.KindContext), updated), 44)
			msg, lvl := "Removed official flag: "+label, toast.Info
			if next {
				msg, lvl = "★ Official context: "+label+" (auto-included)", toast.Ok
			}
			a.say("%s", msg)
			a.pushToast(msg, lvl)
			return core.Next(s)
		}
	}
	if !hasItem {
		a.say("no context item focused to toggle official")
		return core.Next(s)
	}
	projectID := pc.activeProjectID
	if projectID == "" {
		if ref, ok := a.railActiveProjectTask(s); ok {
			projectID = ref.ID
		}
	}
	if projectID == "" {
		a.say("assign a project task before marking context official")
		return core.Next(s)
	}
	updated, _, err := a.Store.AddOfficialProjectContext(data.OfficialContextInput{
		ProjectID: projectID, Title: ctxItem.Label, Value: ctxItem.Value,
		ContextKind: mapContextRecordKind(ctxItem.Kind), Notes: ctxItem.Notes, Pinned: ctxItem.Pinned,
	}, User)
	if err != nil {
		a.fail("promote official context", err)
		return core.Next(s)
	}
	a.invalidateContextIndex()
	msg := fmt.Sprintf("★ Official context: %s (auto-included)", clip(views.Title(a.schema(data.KindContext), updated), 44))
	a.say("%s", msg)
	a.pushToast(msg, toast.Ok)
	return core.Next(s)
}

func (a *App) contextAddOfficial(s core.State, c core.Command) core.StepResult {
	if a.Store == nil {
		return core.Next(s)
	}
	projRef, ok := a.resolveProjectTargetRef(s, c)
	if !ok {
		a.say("no project selected for official context")
		return core.Next(s)
	}
	projectID := projRef.ID
	specs := []core.ParamSpec{
		{Name: "value", Type: core.ParamString, Required: true, Description: "URL, Google Doc/Sheet link, PR, or file path"},
		{Name: "title", Type: core.ParamString, Description: "optional display title (auto-derived if blank)"},
		{Name: "contextKind", Type: core.ParamString, Enum: []string{"auto", "url", "gdoc", "sheet", "file", "doc", "pr"}, Default: "auto", Description: "context kind (auto classifies Google Docs/Sheets, PRs, files)"},
		{Name: "notes", Type: core.ParamString, Description: "why this matters to agents and humans"},
	}
	fm := form.New("Add Official Project Context", specs, map[string]any{"contextKind": "auto"})
	fm.SubmitRow, fm.SubmitLabel, fm.Wrap = true, "Add Official Context", a.wrapInputs()

	mod := modal.NewFormModal(fm, modal.FormOptions{
		ID: projectContextOfficialModalID, Scope: projectContextOfficialModalID,
		Title: "Official Context · " + clip(a.title(projRef), 32), Tone: "accent", WidthPct: 66, DisableDrafts: true,
		OnSubmit: func(vals map[string]any) modal.Result {
			val := strings.TrimSpace(data.Text(vals["value"]))
			if val == "" {
				return modal.Result{Handled: true, Status: "value is required", SetStatus: true}
			}
			kind := strings.TrimSpace(data.Text(vals["contextKind"]))
			if kind == "auto" {
				kind = ""
			}
			rec, existed, err := a.Store.AddOfficialProjectContext(data.OfficialContextInput{
				ProjectID: projectID, Title: strings.TrimSpace(data.Text(vals["title"])),
				Value: val, ContextKind: kind, Notes: strings.TrimSpace(data.Text(vals["notes"])), Pinned: true,
			}, User)
			if err != nil {
				return modal.Result{Handled: true, Status: "add official context: " + err.Error(), SetStatus: true}
			}
			a.invalidateContextIndex()
			verb := "Added"
			if existed {
				verb = "Promoted"
			}
			msg := fmt.Sprintf("★ %s official context: %s", verb, clip(views.Title(a.schema(data.KindContext), rec), 44))
			a.pushToast(msg, toast.Ok)
			return modal.Result{
				Handled: true, Close: true, CloseReason: modal.CloseSubmit,
				Status: msg, SetStatus: true,
			}
		},
	})
	a.say("ctrl+s saves official context · esc cancels")
	return core.Next(a.pushModal(s, mod))
}

// projectSectionTree returns the tree.Model for a Project Page section tree
// (project.panes.tree or project.lanes.tree) when focused.
func (a *App) projectSectionTree(focusID string) (*tree.Model, bool) {
	if focusID != projectPanesTree && focusID != projectLanesTree {
		return nil, false
	}
	tm := a.projectCtrl().trees[focusID]
	return tm, tm != nil
}

// handleProjectTreeMotion routes vertical navigation inside Project Page
// section trees (Panes and Lanes) while leaving h/l for region navigation.
func (a *App) handleProjectTreeMotion(s core.State, c core.Command) bool {
	if c.ID == "motion.left" || c.ID == "motion.right" {
		return false
	}
	tm, ok := a.projectSectionTree(s.ActiveFrame().FocusID)
	if !ok {
		return false
	}
	tm.HandleCommand(c)
	return true
}
