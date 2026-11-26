package app

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/tree"
)

const (
	CmdProjectNavMarkRead        = "project.nav.mark_read"
	CmdProjectNavMarkSectionRead = "project.nav.mark_section_read"
	CmdProjectNavNewItemsToggle  = "project.nav.new_items.toggle"
)

func projectNavUnreadSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		spec(CmdProjectNavMarkRead, "Mark the focused Project Page left-nav item (or section) as read"),
		spec(CmdProjectNavMarkSectionRead, "Mark all unread items in the focused Project Page left-nav section (or project) as read"),
		spec(CmdProjectNavNewItemsToggle, "Turn the Project left nav's listing of new (unread) items on or off (projects.navShowNewItems); unread counts stay"),
	}
}

func (a *App) projectNavSurfaceUnreadEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetProjectsNavSurfaceUnread, true)
}

// projectNavShowNewItemsEnabled: the left nav opens collapsed sections and
// parents onto their new (unread) items. Off keeps them closed; the unread
// count badges on the sections stay either way.
func (a *App) projectNavShowNewItemsEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetProjectNavShowNewItems, true)
}

// projectNavNewItemsToggle flips projects.navShowNewItems from the Project
// Home row, so the nav can be quieted without leaving the portal.
func (a *App) projectNavNewItemsToggle(s core.State, _ core.Command) core.StepResult {
	if on := a.toggleSetting(SetProjectNavShowNewItems, true); on {
		a.say("left nav lists new items again · projects.navShowNewItems on")
	} else {
		a.say("left nav no longer opens onto new items; counts stay · projects.navShowNewItems off")
	}
	return core.Next(s)
}

// projectNavPeeksUnread: both settings agree to list new items under a
// collapsed nav section.
func (a *App) projectNavPeeksUnread() bool {
	return a.projectNavTreeEnabled() && a.projectNavSurfaceUnreadEnabled() && a.projectNavShowNewItemsEnabled()
}

func (a *App) projectNavSurfaceContextUnreadEnabled() bool {
	return a != nil && a.cfg != nil && a.cfg.Settings != nil && a.cfg.Settings.Bool(SetProjectsNavSurfaceContextUnread, false)
}

func (a *App) projectNavUnreadDotOnlyEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetProjectsNavUnreadDotOnly, true)
}

func (a *App) projectNavLaneProgressBarEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetProjectsNavLaneProgressBar, true)
}

func (a *App) railEnterLiveEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.String(SetRailEnterMode, RailEnterModeLive) == RailEnterModeLive
}

func (a *App) projectNavRecordFacts(schema data.Schema, rec data.Record) map[string]any {
	facts := views.RecordFacts(schema, rec, a.now())
	if a.openable(rec) {
		facts[factOpenable] = true
	}
	if rec.Kind == data.KindPane && rec.ID != "" {
		facts["hasPendingApproval"] = len(a.pendingApprovalsFor(rec.ID)) > 0
	}
	return facts
}

func (a *App) projectNavUnreadCutoff() time.Time {
	if a == nil {
		return time.Time{}
	}
	return a.navUnreadCutoff
}

func (a *App) markOwnerCreated(ref core.Ref) {
	if a == nil || ref.ID == "" {
		return
	}
	pc := a.projectCtrl()
	if pc.ownerCreated == nil {
		pc.ownerCreated = map[core.Ref]bool{}
	}
	pc.ownerCreated[ref] = true
}

func (a *App) isOwnerCreated(ref core.Ref) bool {
	if a == nil || a.proj == nil || a.proj.ownerCreated == nil {
		return false
	}
	return a.proj.ownerCreated[ref]
}

// isNavRecordUnread reports whether rec should be surfaced as unread/new in
// the Project Page left-navigation tree.
func (a *App) isNavRecordUnread(kind string, rec data.Record, ws *projectWorkspaceData) bool {
	if rec.ID == "" || rec.IsArchived() {
		return false
	}
	if kind == "" {
		kind = rec.Kind
	}
	switch kind {
	case data.KindPage:
		return rec.IsUnread()
	case data.KindNote:
		author := strings.TrimSpace(rec.Text("authorKind"))
		return author != "" && author != "owner" && rec.IsUnreadBy("readAt")
	case data.KindQuestion:
		return rec.Text("status") == "open" && rec.IsUnreadBy("readAt")
	case data.KindPane:
		if a.paneHasAttention(rec.ID) {
			return true
		}
		if ws != nil && ws.unreadPagePanes != nil {
			return ws.unreadPagePanes[rec.ID]
		}
		return a.paneHasUnreadPage(rec.ID)
	case data.KindTask:
		if ws != nil && ws.unreadNoteTasks != nil && ws.unreadNoteTasks[rec.ID] {
			return true
		}
		return a.isGenericNavRecordUnread(rec)
	case data.KindContext:
		if !rec.Bool("official") && !rec.Bool("pinned") {
			src := strings.ToLower(strings.TrimSpace(rec.Text("source")))
			if src == "" || src == "screen" || src == "prompt" || src == "response" {
				return false
			}
		}
		return a.isGenericNavRecordUnread(rec)
	default:
		return a.isGenericNavRecordUnread(rec)
	}
}

func (a *App) isGenericNavRecordUnread(rec data.Record) bool {
	readAt := rec.Time("readAt")
	if !readAt.IsZero() {
		return rec.UpdatedAt.After(readAt)
	}
	if a.isOwnerCreated(rec.Ref()) {
		return false
	}
	cutoff := a.projectNavUnreadCutoff()
	return !cutoff.IsZero() && rec.CreatedAt.After(cutoff)
}

func (a *App) paneHasUnreadPage(paneID string) bool {
	if a == nil || a.Router == nil || paneID == "" {
		return false
	}
	res, err := a.Router.ListPages(mcpui.ListPagesArgs{Pane: paneID})
	if err != nil {
		return false
	}
	return slices.ContainsFunc(res.Pages, func(p mcpui.PageSummary) bool { return p.Unread })
}

// populateProjectWorkspaceActivity computes unread indices, total unread items,
// and live pane activity counts once per cached workspace snapshot.
func (a *App) populateProjectWorkspaceActivity(ws *projectWorkspaceData) {
	if ws == nil {
		return
	}
	ws.unreadPagePanes = map[string]bool{}
	ws.unreadNoteTasks = map[string]bool{}
	ws.runningPanes = 0
	ws.needsYouPanes = 0
	ws.unreadTotal = 0

	for _, p := range ws.pages {
		if p.IsUnread() {
			if pRef, ok := p.RefField("pane"); ok && pRef.Kind == data.KindPane && pRef.ID != "" {
				ws.unreadPagePanes[pRef.ID] = true
			}
		}
	}

	unreadNotes := map[string]bool{}
	for _, n := range ws.notes {
		if !a.isNavRecordUnread(data.KindNote, n, ws) {
			continue
		}
		unreadNotes[n.ID] = true
		if tRef, ok := n.RefField("task"); ok && tRef.Kind == data.KindTask && ws.taskIDs[tRef.ID] {
			ws.unreadNoteTasks[tRef.ID] = true
		}
	}
	for _, rel := range ws.relations {
		if rel.Kind == data.RelAbout && rel.From.Kind == data.KindNote && unreadNotes[rel.From.ID] &&
			rel.To.Kind == data.KindTask && ws.taskIDs[rel.To.ID] {
			ws.unreadNoteTasks[rel.To.ID] = true
		}
	}

	countRecs := func(kind string, recs []data.Record) int {
		n := 0
		for _, rec := range recs {
			if a.isNavRecordUnread(kind, rec, ws) {
				n++
			}
		}
		return n
	}
	baseUnread := 0
	baseUnread += countRecs(data.KindTask, ws.tasks)
	baseUnread += countRecs(data.KindPage, ws.pages)
	baseUnread += countRecs(data.KindLane, ws.allLanes)
	baseUnread += countRecs(data.KindDocument, ws.documents)
	baseUnread += countRecs(data.KindNote, ws.notes)
	baseUnread += countRecs(data.KindQuestion, ws.questions)
	baseUnread += countRecs(data.KindCommand, ws.commands)
	baseUnread += len(ws.reviewContext)
	ws.baseUnreadTotal = baseUnread
	a.populateProjectLiveTelemetry(ws)
}

// populateProjectLiveTelemetry refreshes only the in-memory daemon telemetry
// counts (runningPanes, needsYouPanes, and pane attention contribution to
// unreadTotal) so telemetrySeq ticks never invalidate SQLite workspace data.
func (a *App) populateProjectLiveTelemetry(ws *projectWorkspaceData) {
	if ws == nil {
		return
	}
	ws.runningPanes = 0
	ws.needsYouPanes = 0
	unreadPanes := 0
	for _, p := range ws.panes {
		hookState, _, pending := a.effectiveHookState(p.ID)
		effState := hookState
		if effState == "" {
			effState = strings.ToLower(strings.TrimSpace(p.Text("runStatus")))
		}
		switch {
		case len(pending) > 0 || effState == daemon.StateNeedsYou || effState == "waiting":
			ws.needsYouPanes++
		case effState == daemon.StateRunning || effState == "working":
			ws.runningPanes++
		}
		if a.isNavRecordUnread(data.KindPane, p, ws) {
			unreadPanes++
		}
	}
	ws.unreadTotal = ws.baseUnreadTotal + unreadPanes
}

func (a *App) projectTabHasUnread(projectID, title string) bool {
	if a == nil || a.Store == nil || projectID == "" {
		return false
	}
	ref := core.Ref{Kind: data.KindTask, ID: projectID}
	ws := a.collectProjectWorkspaceData(title, &ref)
	return ws.unreadTotal > 0 || ws.needsYouPanes > 0
}

// --- Auto-ack & explicit mark-read verbs ----------------------------------------

func (a *App) unreadNavTargetAt(s core.State) (projectNavTarget, bool) {
	if a == nil || len(s.Modals) > 0 {
		return projectNavTarget{}, false
	}
	focus := s.ActiveFrame().FocusID
	if !strings.HasPrefix(focus, projectNavNodePrefix) {
		return projectNavTarget{}, false
	}
	projKey := a.activeProjectWorkspaceKey(s)
	if projKey == "" {
		return projectNavTarget{}, false
	}
	pc := a.projectCtrl()
	target, ok := pc.navTarget(projKey, focus)
	if !ok {
		return projectNavTarget{}, false
	}
	if !target.Unread {
		return projectNavTarget{}, false
	}
	return target, true
}

// isProjectNavAckCommand reports whether cmdID represents a substantive action
// on the focused nav item (enter/open, yank, star, archive, status cycle, edit,
// compose, etc.) as opposed to navigation, scrolling, folding, or chrome actions.
func isProjectNavAckCommand(cmdID string) bool {
	if cmdID == "" || strings.HasPrefix(cmdID, "motion.") {
		return false
	}
	switch cmdID {
	case core.CmdFocusNext, core.CmdFocusPrev,
		core.CmdPageOpen, core.CmdPageNext, core.CmdPagePrev,
		core.CmdAppQuit,
		CmdHistoryBack, CmdHistoryForward,
		CmdProjectNavToggle,
		CmdProjectNavMarkRead, CmdProjectNavMarkSectionRead, CmdProjectNavNewItemsToggle,
		CmdProjectPageBack, CmdPageLeave, CmdTermDetach, CmdModalCancel, CmdWake,
		CmdProjectTabOpen, CmdProjectTabClose, CmdProjectPageOpen,
		CmdHelpOpen, CmdSettingsOpen, CmdBindingsOpen, CmdAppAbout,
		CmdMacroRecord, CmdMacroStop, CmdViewCycle, CmdArchivedToggle, CmdPanesGroupToggle,
		CmdFocusHints, CmdHintsKey, CmdHintsCancel, CmdPaneHints, CmdHintsActionCycle, CmdHintsActionSet,
		CmdSessionNoteCapture, CmdSessionNoteList, CmdSessionRecordStatus, CmdSessionRecordToggle,
		CmdScratchToggle, CmdPaletteOpen, CmdShellOpen, CmdRecentsPick, CmdPagesPick, CmdNounsPick,
		CmdFDSearch, CmdPaneSearch,
		CmdFilterStart, CmdFilterMode, CmdFilterField, CmdSortCycle, CmdSortField, CmdGroupField, CmdGroupToggle,
		CmdTaskFoldToggle,
		tree.CmdFoldToggle, tree.CmdFoldOpen, tree.CmdFoldClose,
		tree.CmdFoldOpenAll, tree.CmdFoldCloseAll, tree.CmdFoldOrStepIn, tree.CmdFoldOrParent:
		return false
	}
	return true
}

func (a *App) invalidateProjectUnreadCaches(projKey string) {
	if a == nil {
		return
	}
	if a.proj != nil && a.proj.wsCache != nil {
		if projKey != "" {
			delete(a.proj.wsCache, projKey)
		} else {
			clear(a.proj.wsCache)
		}
	}
	if a.trees != nil {
		a.trees.threadsCache = nil
	}
	a.clearProjectSectionCache()
	a.viewMemo.tabsKey = ""
	a.viewMemo.gen++
}

func (a *App) markRecordRead(ref core.Ref) bool {
	if a == nil || a.Store == nil || ref.ID == "" {
		return false
	}
	switch ref.Kind {
	case data.KindPage:
		page, err := a.Store.Get(ref)
		if err != nil || page == nil || !page.IsUnread() {
			return false
		}
		_, _, err = a.Store.Patch(data.PatchInput{Ref: ref, Fields: map[string]any{"viewedAt": data.WriteTime{}}}, viewer)
		return err == nil
	case data.KindPane:
		return a.markPanePagesRead(ref.ID)
	case data.KindTask:
		notesMarked := a.markTaskNotesRead(ref.ID)
		rec, err := a.Store.Get(ref)
		recMarked := false
		if err == nil && rec != nil && a.isGenericNavRecordUnread(*rec) {
			if _, _, perr := a.Store.Patch(data.PatchInput{Ref: ref, Fields: map[string]any{"readAt": data.WriteTime{}}}, viewer); perr == nil {
				recMarked = true
			}
		}
		return notesMarked || recMarked
	default:
		rec, err := a.Store.Get(ref)
		if err != nil || rec == nil {
			return false
		}
		unread := a.isNavRecordUnread(ref.Kind, *rec, nil)
		if ref.Kind == data.KindContext {
			unread = a.isProjectContextUnread(*rec)
		}
		if !unread {
			return false
		}
		_, _, err = a.Store.Patch(data.PatchInput{Ref: ref, Fields: map[string]any{"readAt": data.WriteTime{}}}, viewer)
		return err == nil
	}
}

func (a *App) markPanePagesRead(paneID string) bool {
	if a == nil || a.Store == nil || paneID == "" {
		return false
	}
	res, err := a.Store.Query(data.Query{Kind: data.KindPage, IncludeArchived: a.nav.showArchived})
	if err != nil {
		return false
	}
	var patches []data.PatchInput
	for _, p := range res.Items {
		if pRef, ok := p.RefField("pane"); ok && pRef.Kind == data.KindPane && pRef.ID == paneID && p.IsUnread() {
			patches = append(patches, data.PatchInput{Ref: p.Ref(), Fields: map[string]any{"viewedAt": data.WriteTime{}}})
		}
	}
	if len(patches) == 0 {
		return false
	}
	_, err = a.Store.PatchMany(patches, "pane.pages.read", fmt.Sprintf("mark %d pane pages viewed", len(patches)), viewer)
	return err == nil
}

func (a *App) markTaskNotesRead(taskID string) bool {
	if a == nil || a.Store == nil || strings.TrimSpace(taskID) == "" {
		return false
	}
	seen := map[string]bool{}
	var patches []data.PatchInput
	addNote := func(n data.Record) {
		if seen[n.ID] {
			return
		}
		seen[n.ID] = true
		if a.isNavRecordUnread(data.KindNote, n, nil) {
			patches = append(patches, data.PatchInput{Ref: n.Ref(), Fields: map[string]any{"readAt": data.WriteTime{}}})
		}
	}
	if notes, err := a.Store.TaskNotes(taskID); err == nil {
		for _, n := range notes {
			addNote(n)
		}
	}
	if rels, err := a.Store.Related(core.Ref{Kind: data.KindTask, ID: taskID}, data.RelAbout, data.Incoming); err == nil {
		for _, rel := range rels {
			if rel.From.Kind == data.KindNote {
				if n, gerr := a.Store.Get(rel.From); gerr == nil && n != nil {
					addNote(*n)
				}
			}
		}
	}
	if len(patches) == 0 {
		return false
	}
	_, err := a.Store.PatchMany(patches, "task.notes.read", fmt.Sprintf("mark %d notes read on task:%s", len(patches), taskID), viewer)
	return err == nil
}

func (a *App) markProjectNavTargetRead(target projectNavTarget) bool {
	if target.Ref == nil || target.Ref.ID == "" {
		return false
	}
	if !a.markRecordRead(*target.Ref) {
		return false
	}
	a.invalidateProjectUnreadCaches("")
	return true
}

func (a *App) settleNavFocusAfterRead(s core.State, target projectNavTarget) core.State {
	pageFocus := s.Pages[s.ActivePage].FocusID
	if pageFocus != target.FocusID {
		return s
	}
	a.viewMemo.gen++
	root := a.View(s)
	if root.Find(target.FocusID) != nil {
		return s
	}
	fallback := target.ParentFocusID
	if fallback == "" || root.Find(fallback) == nil {
		fallback = projectNavPrefix + target.Section
	}
	if root.Find(fallback) == nil {
		return s
	}
	return withPage(s, s.ActivePage, fallback, "")
}

func (a *App) resolveProjectNavReturnFocus(s core.State, ret string) string {
	if !strings.HasPrefix(ret, projectNavNodePrefix) {
		return ret
	}
	root := a.View(s)
	if root.Find(ret) != nil {
		return ret
	}
	if secKey, _ := projectNavFocusSection(ret); secKey != "" {
		secFocus := projectNavPrefix + secKey
		if root.Find(secFocus) != nil {
			return secFocus
		}
	}
	return ret
}

func (a *App) resolveProjectNavWorkspace(s core.State) (projectWorkspaceData, bool) {
	projKey := a.activeProjectWorkspaceKey(s)
	if projKey == "" {
		return projectWorkspaceData{}, false
	}
	pc := a.projectCtrl()
	if pc.activeProjectKey == projKey && (pc.activeGroupName != "" || pc.activeProjectRef != nil) {
		return a.collectProjectWorkspaceData(pc.activeGroupName, pc.activeProjectRef), true
	}
	if isProjectTab(s.ActivePage) {
		pid := projectIDFromTab(s.ActivePage)
		ref := core.Ref{Kind: data.KindTask, ID: pid}
		return a.collectProjectWorkspaceData(a.title(ref), &ref), true
	}
	if s.ActivePage == SpacePanes {
		if groupName, projRef, onProj := a.selectedRailProject(); onProj {
			return a.collectProjectWorkspaceData(groupName, projRef), true
		}
	}
	return projectWorkspaceData{}, false
}

func (a *App) markProjectNavSectionRead(ws projectWorkspaceData, secKey string) int {
	marked := 0
	markAll := func(recs []data.Record) {
		for _, rec := range recs {
			if a.markRecordRead(rec.Ref()) {
				marked++
			}
		}
	}
	switch secKey {
	case "tasks":
		markAll(ws.tasks)
	case "pages":
		markAll(ws.pages)
	case "panes":
		markAll(ws.panes)
	case "lanes":
		markAll(ws.allLanes)
	case "documents":
		markAll(ws.documents)
	case "notes":
		markAll(ws.notes)
	case "questions":
		markAll(ws.questions)
	case "commands":
		markAll(ws.commands)
	case "context", "pinned", "prs":
		items := ws.reviewContext
		if secKey == "pinned" {
			items = ws.pinnedItems
		} else if secKey == "prs" {
			items = ws.prItems
		}
		for _, it := range items {
			if it.RecordID != "" {
				if a.markRecordRead(core.Ref{Kind: data.KindContext, ID: it.RecordID}) {
					marked++
				}
			}
		}
	}
	if marked > 0 {
		a.invalidateProjectUnreadCaches(ws.projectKey)
	}
	return marked
}

func (a *App) markAllProjectNavRead(ws projectWorkspaceData) int {
	total := 0
	for _, sec := range []string{"tasks", "pages", "panes", "lanes", "documents", "notes", "questions", "commands", "context"} {
		total += a.markProjectNavSectionRead(ws, sec)
	}
	return total
}

func (a *App) projectNavMarkRead(s core.State, _ core.Command) core.StepResult {
	focus := s.ActiveFrame().FocusID
	projKey := a.activeProjectWorkspaceKey(s)
	if projKey == "" {
		return core.Next(s)
	}
	pc := a.projectCtrl()

	if strings.HasPrefix(focus, projectNavNodePrefix) {
		target, ok := pc.navTarget(projKey, focus)
		if !ok {
			_ = a.View(s)
			target, ok = pc.navTarget(projKey, focus)
		}
		if !ok {
			return core.Next(s)
		}
		if a.markProjectNavTargetRead(target) {
			s = a.settleNavFocusAfterRead(s, target)
			a.say("marked %s read", target.Label)
		} else {
			a.say("%s is already read", target.Label)
		}
		return core.Next(s)
	}

	if secKey, isNode := projectNavFocusSection(focus); secKey != "" && !isNode {
		ws, ok := a.resolveProjectNavWorkspace(s)
		if !ok {
			return core.Next(s)
		}
		n := a.markProjectNavSectionRead(ws, secKey)
		if n > 0 {
			a.say("marked %d read in %s", n, secKey)
		} else {
			a.say("nothing unread in %s", secKey)
		}
	}
	return core.Next(s)
}

func (a *App) projectNavMarkSectionRead(s core.State, _ core.Command) core.StepResult {
	focus := s.ActiveFrame().FocusID
	secKey, _ := projectNavFocusSection(focus)
	if secKey == "" {
		return core.Next(s)
	}
	ws, ok := a.resolveProjectNavWorkspace(s)
	if !ok {
		return core.Next(s)
	}
	if secKey == "home" {
		n := a.markAllProjectNavRead(ws)
		if n > 0 {
			a.say("marked %d items read in %s", n, ws.groupName)
		} else {
			a.say("nothing unread in %s", ws.groupName)
		}
		return core.Next(s)
	}
	n := a.markProjectNavSectionRead(ws, secKey)
	if n > 0 {
		a.say("marked %d read in %s", n, secKey)
		if strings.HasPrefix(focus, projectNavNodePrefix) {
			a.viewMemo.gen++
			root := a.View(s)
			if root.Find(focus) == nil {
				s = withFocus(s, projectNavPrefix+secKey)
			}
		}
	} else {
		a.say("nothing unread in %s", secKey)
	}
	return core.Next(s)
}
