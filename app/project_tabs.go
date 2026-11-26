package app

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/table"
	"ofam.dev/ochazuke/widgets/toast"
)

// Space, tab prefix, widget ID, and command constants for the 5 Projects space
// and dynamic 6+ Project Tabs.
const (
	SpaceProjects    = "projects"
	ProjectTabPrefix = "project:"

	projectsDirTable = "projects.dir.table"

	CmdProjectTabOpen        = "project.tab.open"
	CmdProjectTabClose       = "project.tab.close"
	CmdProjectChildPaneClose = "project.child_pane.close"
	CmdProjectAddNote        = "project.note.add"
)

type topTabDef struct {
	id        string
	title     string
	projectID string
	dynamic   bool
}

func isProjectTab(pageID string) bool {
	return strings.HasPrefix(pageID, ProjectTabPrefix) && len(pageID) > len(ProjectTabPrefix)
}

func projectIDFromTab(pageID string) string {
	id, ok := strings.CutPrefix(pageID, ProjectTabPrefix)
	if !ok {
		return ""
	}
	return strings.TrimSpace(id)
}

func projectTabID(projectID string) string {
	return ProjectTabPrefix + strings.TrimSpace(projectID)
}

func (a *App) projectsTopNavTabEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetProjectsTopNavTab, true)
}

func (a *App) projectsDynamicTabsEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetProjectsDynamicTabs, true)
}

func (a *App) projectChildPaneViewEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetProjectsChildPaneView, true)
}

// topTabs returns the ordered top-bar tabs: 1 Panes, 2 Work, 3 Runs, 4 Extensions,
// optional 5 Projects, and any open dynamic 6+ Project Tabs (auto-pruning deleted tasks).
func (a *App) topTabs() []topTabDef {
	out := make([]topTabDef, 0, len(spaces)+1+len(a.projectCtrl().openTabs))
	for _, sp := range spaces {
		out = append(out, topTabDef{id: sp.id, title: sp.title})
	}
	if a.projectsTopNavTabEnabled() {
		out = append(out, topTabDef{id: SpaceProjects, title: "Projects"})
	}
	if a.featuresPageActive() {
		out = append(out, topTabDef{id: SpaceFeatures, title: "Features"})
	}
	if !a.projectsDynamicTabsEnabled() || a.Store == nil {
		return out
	}
	pc := a.projectCtrl()
	taskSchema := a.schema(data.KindTask)
	kept := make([]string, 0, len(pc.openTabs))
	seen := make(map[string]bool, len(pc.openTabs))
	for _, pid := range pc.openTabs {
		pid = strings.TrimSpace(pid)
		if pid == "" || seen[pid] {
			continue
		}
		rec, err := a.Store.Get(core.Ref{Kind: data.KindTask, ID: pid})
		if err != nil || rec == nil {
			delete(pc.childPaneByProject, pid)
			continue
		}
		seen[pid] = true
		kept = append(kept, pid)
		title := strings.TrimSpace(views.Title(taskSchema, *rec))
		if title == "" {
			title = pid
		}
		out = append(out, topTabDef{
			id:        projectTabID(pid),
			title:     clip(title, 24),
			projectID: pid,
			dynamic:   true,
		})
	}
	pc.openTabs = kept
	return out
}

func (a *App) isValidTopTab(id string) bool {
	return slices.ContainsFunc(a.topTabs(), func(tb topTabDef) bool { return tb.id == id })
}

func (a *App) openTabSlot(s core.State, slot int) core.State {
	tabs := a.topTabs()
	if slot < 1 || slot > len(tabs) {
		a.say("no tab in slot %d", slot)
		return s
	}
	return a.openSpace(s, tabs[slot-1].id)
}

func intArg(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(x))
		if err == nil {
			return n
		}
	}
	return 0
}

// projectTabsSpecs returns CommandSpecs owned by 5 Projects and dynamic Project Tabs.
func projectTabsSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		{
			ID:          CmdProjectTabOpen,
			Description: "Open or focus the selected project as a dynamic top-bar Project Tab",
			Params:      []core.ParamSpec{{Name: "project", Type: core.ParamString, Description: "optional project task ID"}},
		},
		{
			ID:          CmdProjectTabClose,
			Description: "Close the active dynamic Project Tab and return to 5 Projects",
			Params:      []core.ParamSpec{{Name: "project", Type: core.ParamString, Description: "optional project task ID"}},
		},
		spec(CmdProjectChildPaneClose, "Close the inline child pane view and return to the Project Tab's Panes list"),
		{
			ID:          CmdProjectAddNote,
			Description: "Compose and attach a quick note directly onto the active project",
			Tags:        []string{"editing"},
			Params:      []core.ParamSpec{{Name: "project", Type: core.ParamString, Description: "optional project task ID"}},
		},
	}
}

// projectTabsBindings returns key bindings for 5-9 tab switching, 5 Projects,
// dynamic Project Tabs, inline child pane exit, and project quick add-note.
func projectTabsBindings() []core.Binding {
	n, all := ModeNormal, ""
	return []core.Binding{
		with(bind(n, all, "5", core.CmdPageOpen, "projects"), map[string]any{"slot": 5}),
		with(bind(n, all, "6", core.CmdPageOpen, "tab 6"), map[string]any{"slot": 6}),
		with(bind(n, all, "7", core.CmdPageOpen, "tab 7"), map[string]any{"slot": 7}),
		with(bind(n, all, "8", core.CmdPageOpen, "tab 8"), map[string]any{"slot": 8}),
		with(bind(n, all, "9", core.CmdPageOpen, "tab 9"), map[string]any{"slot": 9}),

		bind(n, views.ScopeProjectsDir, "enter", CmdProjectTabOpen, "open project tab"),

		when(bind(n, views.ScopeProjectTab, "x", CmdProjectTabClose, "close tab"), "facts.canCloseTab && !facts.inChildPaneView"),
		hidden(when(bind(n, views.ScopeProjectTab, "W", CmdProjectTabClose, "close tab"), "facts.canCloseTab && !facts.inChildPaneView")),

		when(bind(n, views.ScopeProjectContent, "esc", CmdProjectChildPaneClose, "back to panes"), "facts.inChildPaneView && !facts.visualActive && !(facts.markedCount > 0)"),
		when(bind(n, views.ScopeTOC, "h", CmdProjectChildPaneClose, "back to panes"), "facts.inChildPaneView"),

		when(bind(n, views.ScopeProjectOverview, "n", CmdProjectAddNote, "add note"), "(facts.section == \"home\" || facts.section == \"notes\") && !facts.inChildPaneView"),
		when(bind(n, views.ScopeProjectOverview, "r", CmdProjectAddNote, "add note"), "facts.section == \"home\" && !facts.inChildPaneView && !facts.unread"),

		when(bind(n, views.ScopeProjectTab, "c", CmdPaneCompose, "compose prompt"), "facts.inChildPaneView"),
		when(bind(n, views.ScopeProjectTab, "t", CmdSiteTOCToggle, "toggle pages"), "layer == nil && facts.inChildPaneView"),
		when(bind(n, views.ScopeProjectTab, "Y", CmdPaneApprove, "approve"), "facts.inChildPaneView && facts.hasPendingApproval"),
		when(bind(n, views.ScopeProjectTab, "D", CmdPaneDeny, "deny"), "facts.inChildPaneView && facts.hasPendingApproval"),
	}
}

func (a *App) registerProjectTabsHandlers() {
	a.handle(CmdProjectTabOpen, a.projectTabOpen)
	a.handle(CmdProjectTabClose, a.projectTabClose)
	a.handle(CmdProjectChildPaneClose, a.closeProjectChildPane)
	a.handle(CmdProjectAddNote, a.projectAddNote)
}

// --- 5 Projects Directory Space -------------------------------------------------

type projectDirEntry struct {
	rec          data.Record
	lastActivity time.Time
	descendants  int
	panes        int
}

func (a *App) sortedProjectsDirEntries() []projectDirEntry {
	if a.Store == nil {
		return nil
	}
	rev, _ := a.Store.LatestChangeSetNumber()
	pc := a.projectCtrl()
	if pc.dirCacheValid && pc.dirCacheRev == rev && pc.dirCacheArch == a.nav.showArchived && pc.dirCacheGen == pc.lastUsedGen {
		return pc.dirCache
	}
	var (
		roots []data.TaskNode
		err   error
	)
	if !a.hasAnimatingKind(data.KindTask) {
		roots, err = a.recordTreeNodes(data.KindTask)
	} else {
		roots, err = a.Store.TaskTree(a.nav.showArchived)
	}
	if err != nil {
		a.fail("read projects", err)
		return nil
	}
	if len(roots) == 0 {
		pc.dirCache, pc.dirCacheRev, pc.dirCacheArch, pc.dirCacheGen, pc.dirCacheValid = nil, rev, a.nav.showArchived, pc.lastUsedGen, true
		return nil
	}
	entries := make([]projectDirEntry, len(roots))
	idxByProj := make(map[string]int, len(roots))
	taskToProj := make(map[string]string, len(roots)*4)

	bump := func(projID string, t time.Time) {
		if t.IsZero() {
			return
		}
		if idx, ok := idxByProj[projID]; ok && t.After(entries[idx].lastActivity) {
			entries[idx].lastActivity = t
		}
	}

	var walk func(projID string, children []data.TaskNode) int
	walk = func(projID string, children []data.TaskNode) int {
		count := 0
		for _, ch := range children {
			count++
			taskToProj[ch.Record.ID] = projID
			bump(projID, ch.Record.UpdatedAt)
			count += walk(projID, ch.Children)
		}
		return count
	}

	for i, root := range roots {
		pid := root.Record.ID
		entries[i] = projectDirEntry{
			rec:          root.Record,
			lastActivity: root.Record.UpdatedAt,
		}
		idxByProj[pid] = i
		taskToProj[pid] = pid
		if used, ok := pc.lastUsedAt[pid]; ok {
			bump(pid, used)
		}
		entries[i].descendants = walk(pid, root.Children)
	}

	if panesRes, err := a.Store.Query(data.Query{Kind: data.KindPane, IncludeArchived: a.nav.showArchived}); err == nil {
		for _, p := range panesRes.Items {
			if tRef, ok := p.RefField("task"); ok && tRef.Kind == data.KindTask {
				if pid, ok := taskToProj[tRef.ID]; ok {
					bump(pid, p.UpdatedAt)
					bump(pid, p.Time("promptedAt"))
					entries[idxByProj[pid]].panes++
				}
			}
		}
	}
	if !a.nav.showArchived {
		if threads, err := a.taskNoteThreads(); err == nil {
			for tid, notes := range threads {
				if pid, ok := taskToProj[tid]; ok {
					for _, n := range notes {
						bump(pid, n.UpdatedAt)
					}
				}
			}
		}
	} else if notesRes, err := a.Store.Query(data.Query{Kind: data.KindNote, IncludeArchived: true}); err == nil {
		for _, n := range notesRes.Items {
			matched := false
			if tRef, ok := n.RefField("task"); ok && tRef.Kind == data.KindTask {
				if pid, ok := taskToProj[tRef.ID]; ok {
					bump(pid, n.UpdatedAt)
					matched = true
				}
			}
			if !matched {
				if rels, rerr := a.Store.Related(n.Ref(), data.RelAbout, data.Outgoing); rerr == nil {
					for _, rel := range rels {
						if rel.To.Kind == data.KindTask {
							if pid, ok := taskToProj[rel.To.ID]; ok {
								bump(pid, n.UpdatedAt)
							}
						}
					}
				}
			}
		}
	}

	taskSchema := a.schema(data.KindTask)
	slices.SortStableFunc(entries, func(x, y projectDirEntry) int {
		if cmp := y.lastActivity.Compare(x.lastActivity); cmp != 0 {
			return cmp
		}
		if cmp := y.rec.UpdatedAt.Compare(x.rec.UpdatedAt); cmp != 0 {
			return cmp
		}
		return strings.Compare(
			strings.ToLower(views.Title(taskSchema, x.rec)),
			strings.ToLower(views.Title(taskSchema, y.rec)),
		)
	})
	pc.dirCache, pc.dirCacheRev, pc.dirCacheArch, pc.dirCacheGen, pc.dirCacheValid = entries, rev, a.nav.showArchived, pc.lastUsedGen, true
	return entries
}

func (a *App) buildProjectsDirTable() *table.Model {
	entries := a.sortedProjectsDirEntries()
	recs := make([]data.Record, len(entries))
	for i, e := range entries {
		recs[i] = e.rec
	}
	schema := a.schema(data.KindTask)
	fresh := views.TableFromRecordsWith(schema, recs, a.styleOptions())
	fresh.Columns = []table.Column{{Key: "title", Title: "Projects"}}

	tabSlots := map[string]int{}
	for i, tb := range a.topTabs() {
		if tb.dynamic && tb.projectID != "" {
			tabSlots[tb.projectID] = i + 1
		}
	}
	for i, e := range entries {
		row := &fresh.Rows[i]
		row.Scope = views.ScopeProjectsDir
		if row.Facts == nil {
			row.Facts = map[string]any{}
		}
		row.Facts["projectId"] = e.rec.ID
		row.Facts["projectsDir"] = true
		row.Cells["panes"] = e.panes
		row.Cells["tasks"] = e.descendants
		if slot := tabSlots[e.rec.ID]; slot > 0 {
			row.Facts["openTabSlot"] = slot
			row.Badges = append(row.Badges, ui.Badge{Text: fmt.Sprintf("%d", slot), Tone: "accent"})
		}
		if a.openable(e.rec) {
			row.Facts[factOpenable] = true
		}
	}

	old := a.nav.tables[projectsDirTable]
	tbl := keep(old, fresh)
	if old == nil {
		tbl.MatchMode = a.matchMode()
	}
	a.pageSize(tbl, projectsDirTable)
	a.nav.tables[projectsDirTable] = tbl
	return tbl
}

func (a *App) selectedProjectsDirRef() (core.Ref, bool) {
	tbl := a.nav.tables[projectsDirTable]
	if tbl == nil {
		tbl = a.buildProjectsDirTable()
	}
	row, ok := tbl.FocusedRow()
	if !ok || row.Ref == nil || row.Ref.ID == "" {
		return core.Ref{}, false
	}
	return *row.Ref, true
}

func (a *App) projectsSpace(_ core.State) ui.ViewNode {
	tbl := a.buildProjectsDirTable()
	listView := tbl.View(projectsDirTable)
	listView.Scope = views.ScopeProjectsDir
	if listView.Facts == nil {
		listView.Facts = map[string]any{}
	}
	listView.Facts["projectsDir"] = true

	badges := []ui.Badge{{Text: fmt.Sprint(len(tbl.Filtered()))}}
	if tbl.Query != "" {
		badges = append(badges, ui.Badge{Text: "/" + tbl.Query, Tone: "accent"})
	}
	listNode := ui.ViewNode{
		ID:       "projects.dir",
		Kind:     ui.KindPanel,
		Scope:    views.ScopeProjectsDir,
		Title:    "Projects",
		Border:   true,
		Badges:   badges,
		Facts:    map[string]any{"projectsDir": true, "projectCount": len(tbl.Rows)},
		Children: []ui.ViewNode{listView},
	}

	var previewNode ui.ViewNode
	if row, ok := tbl.FocusedRow(); ok && row.Ref != nil && row.Ref.ID != "" {
		projRef := *row.Ref
		title := a.title(projRef)
		pc := a.projectCtrl()
		pc.activeProjectKey = projRef.ID
		pc.activeProjectID = projRef.ID
		pc.activeGroupName = title
		r := projRef
		pc.activeProjectRef = &r

		ws := a.collectProjectWorkspaceData(title, &projRef)
		previewNode = a.buildProjectHomeSection(ws, false)
		previewNode.Title = title
		previewNode.Ref = &r
		hint := "enter open tab"
		if !a.projectsDynamicTabsEnabled() {
			hint = "enter open project"
		}
		previewNode.Badges = append(slices.Clone(previewNode.Badges), ui.Badge{Text: hint, Tone: "muted"})
	} else {
		previewNode = ui.ViewNode{
			ID:     "projects.preview.empty",
			Kind:   ui.KindPanel,
			Scope:  views.ScopeProjectsPreview,
			Title:  "No Projects",
			Border: true,
			Facts:  map[string]any{"empty": true},
			Children: []ui.ViewNode{
				text("projects.preview.empty.msg", "No top-level projects yet. Press n to create a project task.", "muted"),
			},
		}
	}

	return views.ProjectsSpaceView(views.ProjectsSpaceSpec{
		ListNode:    listNode,
		PreviewNode: previewNode,
		NavRatio:    0.20,
	})
}

// --- Dynamic 6+ Project Tab Space -----------------------------------------------

func (a *App) projectTabSpace(s core.State, projectID string) ui.ViewNode {
	projRef := core.Ref{Kind: data.KindTask, ID: projectID}
	rec, err := a.Store.Get(projRef)
	if err != nil || rec == nil {
		return ui.ViewNode{
			ID:     "project.tab.missing",
			Kind:   ui.KindPanel,
			Scope:  views.ScopeProjectTab,
			Title:  "Project",
			Border: true,
			Facts:  map[string]any{"empty": true, "canCloseTab": true},
			Children: []ui.ViewNode{
				text("project.tab.missing.msg", "This project no longer exists. Press x to close the tab.", "muted"),
			},
		}
	}
	projectTitle := strings.TrimSpace(views.Title(a.schema(data.KindTask), *rec))
	if projectTitle == "" {
		projectTitle = projectID
	}
	return a.renderProjectWorkspace(s, projectTitle, &projRef, 0.20, projectTitle, true)
}

// --- Project Tab & Quick Note Handlers ------------------------------------------

func (a *App) touchProjectUsed(projectID string) {
	if projectID == "" {
		return
	}
	pc := a.projectCtrl()
	if pc.lastUsedAt == nil {
		pc.lastUsedAt = map[string]time.Time{}
	}
	pc.lastUsedAt[projectID] = a.now()
	pc.lastUsedGen++
}

func (a *App) ensureProjectTabOpen(projectID string) {
	if projectID == "" {
		return
	}
	pc := a.projectCtrl()
	if !slices.Contains(pc.openTabs, projectID) {
		pc.openTabs = append(pc.openTabs, projectID)
	}
	a.touchProjectUsed(projectID)
}

func (a *App) projectTabOpen(s core.State, c core.Command) core.StepResult {
	projRef, ok := a.resolveProjectTargetRef(s, c)
	if !ok {
		a.say("no project selected")
		return core.Next(s)
	}
	if !a.projectsDynamicTabsEnabled() {
		return a.projectHomeOpen(s, c)
	}
	if a.focusRefuseProject(projRef.ID) {
		return core.Next(s)
	}
	a.ensureProjectTabOpen(projRef.ID)
	tabID := projectTabID(projRef.ID)
	s = a.openSpace(s, tabID)
	if s.Pages[tabID].FocusID == "" {
		sec := a.projectCtrl().activeSection(projRef.ID)
		s = withPage(s, tabID, projectNavPrefix+sec, ModeNormal)
	}
	a.say("project: %s", a.title(projRef))
	return core.Next(s)
}

func (a *App) projectTabClose(s core.State, c core.Command) core.StepResult {
	pid := strings.TrimSpace(c.Arg("project"))
	if pid == "" {
		pid = strings.TrimSpace(c.Arg("id"))
	}
	if pid == "" && isProjectTab(s.ActivePage) {
		pid = projectIDFromTab(s.ActivePage)
	}
	if pid == "" {
		a.say("no project tab to close")
		return core.Next(s)
	}
	pc := a.projectCtrl()
	idx := slices.Index(pc.openTabs, pid)
	if idx >= 0 {
		pc.openTabs = slices.Delete(pc.openTabs, idx, idx+1)
	}
	delete(pc.childPaneByProject, pid)

	if s.ActivePage == projectTabID(pid) {
		nextSpace := SpacePanes
		if a.projectsTopNavTabEnabled() {
			nextSpace = SpaceProjects
		} else if len(pc.openTabs) > 0 && a.projectsDynamicTabsEnabled() {
			targetIdx := max(0, min(idx-1, len(pc.openTabs)-1))
			nextSpace = projectTabID(pc.openTabs[targetIdx])
		}
		s = a.openSpace(s, nextSpace)
	}
	a.say("closed project tab")
	return core.Next(s)
}

func (a *App) projectAddNote(s core.State, c core.Command) core.StepResult {
	projRef, ok := a.resolveProjectTargetRef(s, c)
	if !ok {
		a.say("no project selected for note")
		return core.Next(s)
	}
	return a.openTaskNoteComposer(s, projRef.ID, "Project Note", fmt.Sprintf("Project %s · attached note", projRef.ID))
}

// openTaskNoteComposer opens the shared Universal Rich Composer modal to
// compose and attach a note directly onto taskID (such as a top-level project).
func (a *App) openTaskNoteComposer(s core.State, taskID, titlePrefix, subtitle string) core.StepResult {
	if a.Store == nil || strings.TrimSpace(taskID) == "" {
		a.say("no task selected for note")
		return core.Next(s)
	}
	taskRef := core.Ref{Kind: data.KindTask, ID: taskID}
	taskRec, err := a.Store.Get(taskRef)
	if err != nil {
		a.fail("add note", err)
		return core.Next(s)
	}
	taskTitle := views.Title(a.schema(data.KindTask), *taskRec)
	cwd := a.defaultDirForTask(taskRef)
	if cwd == "" {
		if wd, wdErr := os.Getwd(); wdErr == nil {
			cwd = wd
		}
	}
	if strings.TrimSpace(titlePrefix) == "" {
		titlePrefix = "Note"
	}
	if strings.TrimSpace(subtitle) == "" {
		subtitle = fmt.Sprintf("Task %s · append-only thread", taskID)
	}

	cm := newRichComposerModal(a, richComposerSpec{
		ID:                taskNoteComposerModalID,
		Scope:             "composer",
		Title:             titlePrefix + " · " + taskTitle,
		Subtitle:          subtitle,
		InputTitle:        "Note",
		Placeholder:       "Write note… (@file, @ctx, @agent, $skill, /cmd · ctrl+v/f attach · ctrl+s save)",
		Cwd:               cwd,
		DraftKey:          "task.note:" + taskID,
		ShowDeliveryModes: false,
		ContextTaskID:     taskID,
		OnSubmit: func(text string, attachments []data.Attachment, _ ComposerMode) (tea.Cmd, error) {
			body := text
			if strings.TrimSpace(body) == "" && len(attachments) > 0 {
				body = "Attached " + attachments[0].Label
			}
			_, err := a.Store.AppendTaskNote(data.AppendTaskNoteInput{
				TaskID:      taskID,
				Body:        body,
				AuthorKind:  "owner",
				AuthorLabel: "owner",
				Attachments: attachments,
			}, User)
			if err != nil {
				return nil, err
			}
			a.touchProjectUsed(a.Store.RootProjectID(taskID))
			a.afterModal(func(st core.State) core.State {
				msg := fmt.Sprintf("Added note to %q", taskTitle)
				a.say("%s", msg)
				a.pushToast(msg, toast.Ok)
				return st
			})
			return nil, nil
		},
	})
	a.say("add note to %s · ctrl+s saves · ctrl+v/f attach · @file @ctx @agent $skill /cmd", taskTitle)
	return core.Next(a.pushModal(s, cm))
}
