package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

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
	CmdProjectNavToggle      = "project.nav.toggle"
	CmdProjectPageBack       = "project.page.back"
	CmdProjectContentOpen    = "project.content.open"
	CmdContextOfficialToggle = "context.official.toggle"
	CmdContextAddOfficial    = "context.official.add"

	projectNavPrefix              = views.ProjectNavPrefix
	projectNavNodePrefix          = views.ProjectNavNodePrefix
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

type cachedProjectWorkspace struct {
	rev          int64
	telemetrySeq uint64
	showArchived bool
	ctxGen       uint64
	groupName    string
	projID       string
	railPanesKey string
	data         projectWorkspaceData
}

// ProjectController owns per-project workspace navigation state: active left
// nav section, expandable nav tree state, locked/previewed right-pane target,
// open inline ui.page, and per-section table/tree models.
type ProjectController struct {
	sectionByProject     map[string]string
	openPageByProject    map[string]string
	openPageReturnFocus  map[string]string
	childPaneByProject   map[string]string
	childPaneReturnFocus map[string]string
	expandedSections     map[string]map[string]bool
	expandedNodes        map[string]map[string]bool
	activeItemByProject  map[string]*projectNavTarget
	navTargets           map[string]map[string]projectNavTarget
	homeTargets          map[string]map[string]projectHomeTarget
	ownerCreated         map[core.Ref]bool
	openTabs             []string
	lastUsedAt           map[string]time.Time
	lastUsedGen          uint64
	tables               map[string]*table.Model
	trees                map[string]*tree.Model
	wsCache              map[string]cachedProjectWorkspace
	dirCache             []projectDirEntry
	dirCacheRev          int64
	dirCacheArch         bool
	dirCacheGen          uint64
	dirCacheValid        bool
	activeProjectKey     string
	activeProjectID      string
	activeGroupName      string
	activeProjectRef     *core.Ref
}

func newProjectController() *ProjectController {
	return &ProjectController{
		sectionByProject:     map[string]string{},
		openPageByProject:    map[string]string{},
		openPageReturnFocus:  map[string]string{},
		childPaneByProject:   map[string]string{},
		childPaneReturnFocus: map[string]string{},
		expandedSections:     map[string]map[string]bool{},
		expandedNodes:        map[string]map[string]bool{},
		activeItemByProject:  map[string]*projectNavTarget{},
		navTargets:           map[string]map[string]projectNavTarget{},
		homeTargets:          map[string]map[string]projectHomeTarget{},
		ownerCreated:         map[core.Ref]bool{},
		lastUsedAt:           map[string]time.Time{},
		tables:               map[string]*table.Model{},
		trees:                map[string]*tree.Model{},
		wsCache:              map[string]cachedProjectWorkspace{},
	}
}

func sortedTrueKeys(m map[string]bool) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if v {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	slices.Sort(keys)
	return strings.Join(keys, ",")
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
	return append([]core.CommandSpec{
		{
			ID:          CmdProjectSectionSelect,
			Description: "Select a Project Page left-navigation section and focus its content",
			Params:      []core.ParamSpec{{Name: "section", Type: core.ParamString, Description: "section key (home, tasks, pages, context, ...)"}},
		},
		spec(CmdProjectNavToggle, "Expand or collapse the focused Project Page left-nav section or tree node"),
		spec(CmdProjectPageBack, "Return from an open project page to the Pages index or Project nav"),
		spec(CmdProjectContentOpen, "Open the focused item inside the Project Page content area"),
		spec(CmdContextOfficialToggle, "Promote or demote the focused context item as Official Project Context", "editing"),
		spec(CmdContextAddOfficial, "Add an Official Project Context link, Google Doc/Sheet, PR, or file", "editing"),
	}, projectNavUnreadSpecs()...)
}

// projectHomeBindings returns key bindings strictly scoped to the Project Page
// workspace regions so work.table and panes.rail footer hints stay clean.
func projectHomeBindings() []core.Binding {
	n := ModeNormal
	return append([]core.Binding{
		bind(n, views.ScopeProjectNav, "enter", CmdProjectSectionSelect, "open section"),
		when(bind(n, views.ScopeProjectNav, "space", CmdProjectNavToggle, "expand / collapse"), "facts.navExpandable"),
		when(bind(n, views.ScopeProjectNav, "r", CmdProjectNavMarkRead, "mark read"), "facts.unread || facts.sectionUnread"),
		when(bind(n, views.ScopeProjectNav, "R", CmdProjectNavMarkSectionRead, "mark section read"), "facts.unread || facts.sectionUnread || (facts.section == \"home\" && facts.projectUnread)"),
		when(bind(n, views.ScopeProjectContent, "esc", CmdProjectPageBack, "back to pages"), "facts.projectOpenPage && !facts.inChildPaneView"),
		hidden(when(bind(n, views.ScopeProjectContent, "h", CmdProjectPageBack, "back to pages"), "facts.projectOpenPage && !facts.inChildPaneView")),
		hidden(when(bind(n, views.ScopeProjectContent, "esc", CmdProjectPageBack, "back to nav"), "!facts.projectOpenPage && !facts.inChildPaneView && !facts.visualActive && !(facts.markedCount > 0)")),
		bind(n, scopeProjectPages, "enter", CmdProjectContentOpen, "open page"),
		bind(n, scopeProjectPanes, "enter", CmdProjectContentOpen, "jump to pane"),
		bind(n, scopeProjectDocuments, "enter", CmdProjectContentOpen, "read document"),
		bind(n, scopeProjectWorktrees, "enter", CmdProjectContentOpen, "open worktree"),
		when(bind(n, ScopeProjectContext, "p", CmdContextOfficialToggle, "toggle official"), "facts.contextItem"),
		hidden(when(bind(n, ScopeProjectOverview, "p", CmdContextOfficialToggle, "toggle official"), "facts.contextItem")),
		when(bind(n, views.ScopeProjectNav, "m", CmdProjectContextStar, "star context item"), "facts.contextItem"),
		hidden(when(bind(n, views.ScopeProjectNav, "*", CmdProjectContextStar, "star context item"), "facts.contextItem")),
		when(bind(n, views.ScopeProjectContent, "N", CmdContextAddOfficial, "add official context"), "facts.section == \"context\" || facts.section == \"pinned\" || facts.contextItem"),
		hidden(bind(n, ScopeProjectOverview, "N", CmdContextAddOfficial, "add official context")),
		hidden(bind(n, SpacePanes, "P", CmdProjectPageOpen, "project page")),
	}, projectHomeDashboardBindings()...)
}

// registerProjectHomeHandlers registers all Project Page command handlers.
func (a *App) registerProjectHomeHandlers() {
	a.handle(CmdProjectSectionSelect, a.projectSectionSelect)
	a.handle(CmdProjectNavToggle, a.projectNavToggle)
	a.handle(CmdProjectNavMarkRead, a.projectMarkRead)
	a.handle(CmdProjectNavMarkSectionRead, a.projectMarkSectionRead)
	a.handle(CmdProjectPageBack, a.projectPageBack)
	a.handle(CmdProjectContentOpen, a.projectContentOpen)
	a.handle(CmdContextOfficialToggle, a.contextOfficialToggle)
	a.handle(CmdContextAddOfficial, a.contextAddOfficial)
}

// projectOverviewView renders the right side of 1 Panes when a Project header
// row is selected in the left rail.
func (a *App) projectOverviewView(s core.State, groupName string, projRef *core.Ref) ui.ViewNode {
	if !a.projectWorkspaceEnabled() {
		pc := a.projectCtrl()
		projKey := projectKeyFor(groupName, projRef)
		pc.activeProjectKey, pc.activeGroupName = projKey, groupName
		if projRef != nil {
			r := *projRef
			pc.activeProjectRef, pc.activeProjectID = &r, projRef.ID
		} else {
			pc.activeProjectRef, pc.activeProjectID = nil, ""
		}
		return a.projectOverviewFallbackView(groupName, projRef)
	}
	return a.renderProjectWorkspace(s, groupName, projRef, 0.24, "", false)
}

// renderProjectWorkspace renders the shared Project Page workspace shell used
// both in 1 Panes project overview (navRatio=0.24, header visible) and inside
// dynamic 6+ Project Tabs (navRatio=0.20, navTitle=projectName, header hidden).
func (a *App) renderProjectWorkspace(s core.State, groupName string, projRef *core.Ref, navRatio float64, navTitle string, hideHeader bool) ui.ViewNode {
	pc := a.projectCtrl()
	projKey := projectKeyFor(groupName, projRef)
	pc.activeProjectKey, pc.activeGroupName = projKey, groupName
	if projRef != nil {
		r := *projRef
		pc.activeProjectRef, pc.activeProjectID = &r, projRef.ID
	} else {
		pc.activeProjectRef, pc.activeProjectID = nil, ""
	}

	defs := a.projectNavSectionDefs()
	activeKey := pc.activeSection(projKey)
	focusID := ""
	if s.ActivePage == SpacePanes || isProjectTab(s.ActivePage) {
		focusID = s.Pages[s.ActivePage].FocusID
		if secKey, isNode := projectNavFocusSection(focusID); secKey != "" {
			if slices.ContainsFunc(defs, func(d projectNavDef) bool { return d.Key == secKey }) {
				if secKey != activeKey {
					pc.setOpenPageID(projKey, "")
					if pc.childPaneByProject != nil {
						delete(pc.childPaneByProject, projKey)
					}
				}
				if isNode {
					pc.setOpenPageID(projKey, "")
					if pc.childPaneByProject != nil {
						delete(pc.childPaneByProject, projKey)
					}
				}
				pc.setActiveNavItem(projKey, nil)
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
	navItems := a.buildProjectNavItems(defs, ws, activeKey, focusID)

	badges := make([]ui.Badge, 0, 8)
	if ws.projRec != nil {
		badges = append(badges, views.StatusBadges(a.schema(data.KindTask), *ws.projRec)...)
	}
	if ws.unreadTotal > 0 {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("● %d new", ws.unreadTotal), Tone: "warning"})
	}
	if ws.runningPanes > 0 {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("▶ %d running", ws.runningPanes), Tone: "info"})
	}
	if ws.needsYouPanes > 0 {
		badges = append(badges, ui.Badge{Text: fmt.Sprintf("! %d needs you", ws.needsYouPanes), Tone: "error"})
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

	var activeBody ui.ViewNode
	inChildPane := false
	if a.projectChildPaneViewEnabled() {
		if childPaneID := a.activeChildPaneID(s); childPaneID != "" {
			if childPane, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: childPaneID}); err == nil && childPane != nil {
				activeBody = a.buildPaneSiteNode(s, *childPane, true)
				inChildPane = true
			} else if pc.childPaneByProject != nil {
				delete(pc.childPaneByProject, projKey)
			}
		}
	}
	if !inChildPane {
		if strings.HasPrefix(focusID, projectNavNodePrefix) {
			if target, ok := pc.navTarget(projKey, focusID); ok {
				activeBody = a.buildProjectNavItemBody(s, ws, target, true)
			} else {
				activeBody = a.buildProjectSectionBody(s, ws, activeKey)
			}
		} else if locked := pc.activeNavItem(projKey); locked != nil {
			resolved := *locked
			if fresh, ok := pc.navTarget(projKey, locked.FocusID); ok {
				resolved = fresh
			}
			activeBody = a.buildProjectNavItemBody(s, ws, resolved, false)
		} else {
			activeBody = a.buildProjectSectionBody(s, ws, activeKey)
		}
	}

	return views.ProjectWorkspaceView(views.ProjectWorkspaceSpec{
		ProjectID: ws.projectID, Title: groupName, Subtitle: strings.Join(subParts, " · "),
		Ref: projRef, Badges: badges, NavRatio: navRatio, NavTitle: navTitle, HideHeader: hideHeader,
		ActiveKey:  activeKey,
		NavItems:   navItems,
		ActiveBody: activeBody,
		Facts: map[string]any{
			"projectGroup": groupName, "paneCount": len(ws.panes),
			"contextCount": len(ws.contextItems), "officialCount": ws.officialCount,
			"projectUnread":   ws.unreadTotal > 0,
			"unreadTotal":     ws.unreadTotal,
			"inChildPaneView": inChildPane,
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
	if isProjectTab(s.ActivePage) {
		if pid := projectIDFromTab(s.ActivePage); pid != "" {
			return core.Ref{Kind: data.KindTask, ID: pid}, true
		}
	}
	if s.ActivePage == SpaceProjects {
		if ref, ok := a.selectedProjectsDirRef(); ok {
			return ref, true
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
		focus := s.ActiveFrame().FocusID
		if strings.HasPrefix(focus, projectNavNodePrefix) {
			return a.projectNavNodeOpen(s, focus)
		}
		if k, ok := strings.CutPrefix(focus, projectNavPrefix); ok && k != "" {
			sec = k
		}
	}
	if sec == "" {
		sec = pc.activeSection(pc.activeProjectKey)
	}
	projKey := a.activeProjectWorkspaceKey(s)
	if projKey == "" {
		projKey = pc.activeProjectKey
	}
	if pc.activeSection(projKey) != sec {
		pc.setOpenPageID(projKey, "")
	}
	pc.setActiveNavItem(projKey, nil)
	if pc.childPaneByProject != nil {
		delete(pc.childPaneByProject, projKey)
	}
	pc.setSection(projKey, sec)
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
	projKey := a.activeProjectWorkspaceKey(s)
	if projKey == "" {
		projKey = pc.activeProjectKey
	}
	if pc.openPageID(projKey) != "" {
		pc.setOpenPageID(projKey, "")
		pc.setActiveNavItem(projKey, nil)
		ret := pc.takeOpenPageReturnFocus(projKey)
		if ret == "" {
			ret = projectPagesTable
		}
		a.viewMemo.gen++
		ret = a.resolveProjectNavReturnFocus(s, ret)
		a.say("pages index")
		return core.Next(withMode(withFocus(s, ret), ModeNormal))
	}
	if locked := pc.activeNavItem(projKey); locked != nil {
		ret := locked.FocusID
		pc.setActiveNavItem(projKey, nil)
		a.viewMemo.gen++
		ret = a.resolveProjectNavReturnFocus(s, ret)
		a.say("back to nav")
		return core.Next(withMode(withFocus(s, ret), ModeNormal))
	}
	target := projectNavPrefix + pc.activeSection(projKey)
	for _, n := range a.focusables(s) {
		if n.id == target {
			return core.Next(withFocus(s, target))
		}
	}
	return core.Next(a.column(s, -1))
}

func (a *App) projectContentOpen(s core.State, c core.Command) core.StepResult {
	focus, pc := s.ActiveFrame().FocusID, a.projectCtrl()
	if a.isProjectHomeFocus(focus) {
		return a.projectHomeItemOpen(s, c)
	}
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
		projKey := a.activeProjectWorkspaceKey(s)
		if projKey == "" {
			projKey = pc.activeProjectKey
		}
		return a.projectOpenInteractivePage(s, projKey, *page, nil, projectPagesTable)
	case projectPanesTree:
		if tm := pc.trees[projectPanesTree]; tm != nil {
			if row, ok := tm.Focused(); ok && row.Node.Ref != nil && row.Node.Ref.Kind == data.KindPane {
				if isProjectTab(s.ActivePage) && a.projectChildPaneViewEnabled() {
					return core.Next(a.openProjectChildPane(s, projectIDFromTab(s.ActivePage), row.Node.Ref.ID, a.railEnterLiveEnabled()))
				}
				return core.Next(a.jumpToPane(s, row.Node.Ref.ID, a.railEnterLiveEnabled()))
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
	a.markOwnerCreated(updated.Ref())
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
			a.markOwnerCreated(rec.Ref())
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

func (a *App) railGroupPanesKey(groupName string) string {
	if a.panes == nil {
		return ""
	}
	ps := a.panes.railGroupPanes[groupName]
	if len(ps) == 0 {
		return ""
	}
	var b strings.Builder
	for i, p := range ps {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(p.ID)
	}
	return b.String()
}

func flattenTreeDescendants(children []data.TreeNode) []data.Record {
	var out []data.Record
	var collect func([]data.TreeNode)
	collect = func(ns []data.TreeNode) {
		for _, n := range ns {
			out = append(out, n.Record)
			collect(n.Children)
		}
	}
	collect(children)
	return out
}

func (a *App) projectWorktreesFromWorkspace(ws projectWorkspaceData) []data.ProjectWorktree {
	byPath := map[string]data.ProjectWorktree{}
	add := func(wt data.ProjectWorktree) {
		p := strings.TrimSpace(wt.Path)
		if p == "" {
			return
		}
		p = filepath.Clean(p)
		wt.Path = p
		if cur, exists := byPath[p]; exists {
			if cur.Branch == "" {
				cur.Branch = wt.Branch
			}
			if cur.Repo == "" {
				cur.Repo = wt.Repo
			}
			byPath[p] = cur
			return
		}
		byPath[p] = wt
	}
	activeTaskIDs := map[string]bool{}
	var activeTasks []data.Record
	if ws.projRec != nil && !ws.projRec.IsArchived() {
		activeTaskIDs[ws.projRec.ID] = true
		activeTasks = append(activeTasks, *ws.projRec)
	}
	for _, t := range ws.tasks {
		if !t.IsArchived() {
			activeTaskIDs[t.ID] = true
			activeTasks = append(activeTasks, t)
		}
	}
	for _, lane := range ws.allLanes {
		if lane.IsArchived() {
			continue
		}
		if tRef, ok := lane.RefField("task"); !ok || tRef.Kind != data.KindTask || !activeTaskIDs[tRef.ID] {
			continue
		}
		if p := strings.TrimSpace(lane.Text("worktree")); p != "" {
			add(data.ProjectWorktree{Path: p, Branch: strings.TrimSpace(lane.Text("branch")), Repo: strings.TrimSpace(lane.Text("repo")), Source: "lane", Ref: lane.Ref()})
		}
	}
	for _, t := range activeTasks {
		if p := strings.TrimSpace(t.Text("defaultDir")); p != "" {
			add(data.ProjectWorktree{Path: p, Repo: strings.TrimSpace(t.Text("repo")), Source: "task", Ref: t.Ref()})
		}
		if a.Store != nil {
			if rels, err := a.Store.Related(t.Ref(), data.RelDirectory, data.Outgoing); err == nil {
				for _, rel := range rels {
					p := strings.TrimSpace(rel.To.ID)
					if rel.To.Kind != "directory" {
						if rec, gerr := a.Store.Get(rel.To); gerr == nil && rec != nil {
							for _, field := range []string{"path", "cwd", "worktree", "defaultDir"} {
								if v := strings.TrimSpace(rec.Text(field)); v != "" {
									p = v
									break
								}
							}
						}
					}
					if p != "" {
						add(data.ProjectWorktree{Path: p, Source: "directory", Ref: rel.To})
					}
				}
			}
		}
	}
	out := make([]data.ProjectWorktree, 0, len(byPath))
	for _, wt := range byPath {
		out = append(out, wt)
	}
	slices.SortStableFunc(out, func(x, y data.ProjectWorktree) int {
		if c := strings.Compare(x.Path, y.Path); c != 0 {
			return c
		}
		if c := strings.Compare(x.Source, y.Source); c != 0 {
			return c
		}
		return strings.Compare(data.RefText(x.Ref), data.RefText(y.Ref))
	})
	return out
}
