package app

import (
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/table"
)

// Scope modes and commands for B3: Pinned Project & Scope Modes.
const (
	ScopeModeGlobal  = data.ScopeModeGlobal
	ScopeModeToday   = data.ScopeModeToday
	ScopeModeProject = data.ScopeModeProject

	CmdScopeCycle         = "scope.cycle"
	CmdScopeSet           = "scope.set"
	CmdScopeToggleProject = "scope.toggleProject"
	CmdProjectPin         = "project.pin"
	CmdProjectUnpin       = "project.unpin"
	CmdProjectTogglePin   = "project.togglePin"
	CmdProjectPinPick     = "project.pin.pick"
	CmdProjectJumpPinned  = "project.jumpPinned"

	projectPinPickerID     = "project.pin.picker"
	projectPinUnpinPickKey = "__unpin__"
)

func scopeModeCommandSpecs() []core.CommandSpec {
	projParam := []core.ParamSpec{{Name: "project", Type: core.ParamString, Description: "optional project task ID (defaults to focused or active project)"}}
	return []core.CommandSpec{
		spec(CmdScopeCycle, "Cycle active scope mode (global -> today -> pinned project)"),
		{
			ID:          CmdScopeSet,
			Description: "Set active scope mode (global, today, or project)",
			Params: []core.ParamSpec{{
				Name:        "mode",
				Type:        core.ParamString,
				Enum:        data.ScopeModes,
				Required:    true,
				Description: "Target scope mode (global, today, or project)",
			}},
		},
		spec(CmdScopeToggleProject, "Toggle between Pinned Project scope mode and Global scope mode"),
		{
			ID:          CmdProjectPin,
			Description: "Pin the focused/active project (or pick a project to pin)",
			Params:      projParam,
			Meta:        map[string]any{"record": true, "kinds": []string{data.KindTask}},
		},
		spec(CmdProjectUnpin, "Unpin the currently pinned project"),
		{
			ID:          CmdProjectTogglePin,
			Description: "Toggle pin on the focused/active project (or open the project pin picker)",
			Params:      projParam,
			Meta:        map[string]any{"record": true, "kinds": []string{data.KindTask}},
		},
		spec(CmdProjectPinPick, "Pick a project to pin from all top-level projects"),
		spec(CmdProjectJumpPinned, "Jump directly to the pinned project's Portal tab"),
	}
}

func scopeModeBindings() []core.Binding {
	n, all := ModeNormal, ""
	return []core.Binding{
		// Global anywhere bindings (automatically mirrored via ctrl+a leader in live terminal/editor).
		bind(n, all, "g E", CmdScopeCycle, "cycle scope mode"),
		bind(n, all, "g w", CmdScopeToggleProject, "toggle project/global scope"),
		bind(n, all, "g J", CmdProjectTogglePin, "pin/unpin project"),
		bind(n, all, "g H", CmdProjectJumpPinned, "jump to pinned project"),

		// Contextual p on project surfaces (Projects directory, Project Portal).
		// Outline and task trees keep p for paste.after.
		bind(n, views.ScopeProjectsDir, "p", CmdProjectTogglePin, "pin/unpin project"),
		when(bind(n, views.ScopeProjectTab, "p", CmdProjectTogglePin, "pin/unpin project"), "!facts.inChildPaneView"),
	}
}

func (a *App) registerScopeMode() {
	a.handle(CmdScopeCycle, a.scopeCycle)
	a.handle(CmdScopeSet, a.scopeSet)
	a.handle(CmdScopeToggleProject, a.scopeToggleProject)
	a.handle(CmdProjectPin, a.projectPin)
	a.handle(CmdProjectUnpin, a.projectUnpin)
	a.handle(CmdProjectTogglePin, a.projectTogglePin)
	a.handle(CmdProjectPinPick, a.projectPinPick)
	a.handle(CmdProjectJumpPinned, a.projectJumpPinned)
}

// --- Persisted state & facts ---------------------------------------------------

func (a *App) pinnedProjectID() string {
	if a == nil || a.focus == nil {
		return ""
	}
	pid := strings.TrimSpace(a.focus.cfg.PinnedProjectID)
	if pid == "" || a.Store == nil {
		return pid
	}
	rec, err := a.Store.Get(core.Ref{Kind: data.KindTask, ID: pid})
	if err != nil || rec == nil {
		return ""
	}
	return pid
}

func (a *App) pinnedProjectTitle() string {
	pid := a.pinnedProjectID()
	if pid == "" {
		return ""
	}
	return a.title(core.Ref{Kind: data.KindTask, ID: pid})
}

func (a *App) activeScopeMode() string {
	raw := ""
	if a != nil && a.focus != nil {
		raw = strings.TrimSpace(a.focus.cfg.ScopeMode)
	}
	if raw == "" {
		raw = a.scopeModeDefault()
	}
	mode := data.NormalizeScopeMode(raw)
	if mode == ScopeModeProject && a.pinnedProjectID() == "" {
		return ScopeModeGlobal
	}
	return mode
}

func (a *App) setPinnedProject(projectID string) {
	if a == nil || a.focus == nil {
		return
	}
	a.focusUpdate(data.SetPinnedProject(a.focus.cfg, projectID))
	a.invalidateScopeViews()
}

func (a *App) setScopeMode(mode string) {
	if a == nil || a.focus == nil {
		return
	}
	a.focusUpdate(data.SetScopeMode(a.focus.cfg, mode))
	a.invalidateScopeViews()
}

func (a *App) invalidateScopeViews() {
	if a.trees != nil {
		clear(a.trees.syncCache)
	}
	if a.nav != nil {
		clear(a.nav.recCacheKeys)
	}
	if a.proj != nil {
		a.proj.dirCacheValid = false
	}
	a.invalidateQueueViews()
	a.viewMemo.gen++
}

// --- Target resolution ---------------------------------------------------------

func (a *App) resolvePinProjectID(s core.State, c core.Command) string {
	if a == nil || a.Store == nil {
		return ""
	}
	for _, key := range []string{"project", "id"} {
		if id := strings.TrimSpace(c.Arg(key)); id != "" {
			return a.Store.RootProjectID(id)
		}
	}
	if c.Target != nil && data.IsTaskRef(*c.Target) {
		return a.Store.RootProjectID(c.Target.ID)
	}
	if pid := a.focusActiveProject(s); pid != "" {
		return a.Store.RootProjectID(pid)
	}
	if ref, ok := a.subject(s, c); ok && data.IsTaskRef(ref) {
		return a.Store.RootProjectID(ref.ID)
	}
	return ""
}

func (a *App) recordRootProjectID(rec data.Record) string {
	if a == nil || a.Store == nil {
		return ""
	}
	if data.IsTaskKind(rec.Kind) {
		return a.Store.RootProjectID(rec.ID)
	}
	for _, field := range []string{"project", "task"} {
		if ref, ok := rec.RefField(field); ok && data.IsTaskRef(ref) {
			return a.Store.RootProjectID(ref.ID)
		}
	}
	return ""
}

// --- Command handlers ----------------------------------------------------------

func (a *App) scopeCycle(s core.State, c core.Command) core.StepResult {
	cur := a.activeScopeMode()
	pid := a.pinnedProjectID()
	if cur == ScopeModeToday && pid == "" {
		if active := a.resolvePinProjectID(s, c); active != "" {
			a.setPinnedProject(active)
			pid = active
		}
	}
	next := data.NextScopeMode(cur, pid != "")
	a.setScopeMode(next)
	a.announceScopeMode(next)
	return core.Next(s)
}

func (a *App) scopeSet(s core.State, c core.Command) core.StepResult {
	raw := strings.TrimSpace(c.Arg("mode"))
	if !data.IsValidScopeMode(raw) {
		a.say("unknown scope mode %q (use global, today, or project)", raw)
		return core.Next(s)
	}
	mode := data.NormalizeScopeMode(raw)
	if mode == ScopeModeProject && a.pinnedProjectID() == "" {
		if active := a.resolvePinProjectID(s, c); active != "" {
			a.setPinnedProject(active)
		} else {
			return a.openProjectPinPicker(s, true)
		}
	}
	a.setScopeMode(mode)
	a.announceScopeMode(mode)
	return core.Next(s)
}

func (a *App) scopeToggleProject(s core.State, c core.Command) core.StepResult {
	if a.activeScopeMode() == ScopeModeProject {
		a.setScopeMode(ScopeModeGlobal)
		a.announceScopeMode(ScopeModeGlobal)
		return core.Next(s)
	}
	if a.pinnedProjectID() == "" {
		if active := a.resolvePinProjectID(s, c); active != "" {
			a.setPinnedProject(active)
		} else {
			return a.openProjectPinPicker(s, true)
		}
	}
	a.setScopeMode(ScopeModeProject)
	a.announceScopeMode(ScopeModeProject)
	return core.Next(s)
}

func (a *App) announceScopeMode(mode string) {
	switch mode {
	case ScopeModeToday:
		a.say("scope: ◎ Today · g E cycles")
	case ScopeModeProject:
		a.say("scope: ⌖ Project: %s · g w returns to global", a.pinnedProjectTitle())
	default:
		if title := a.pinnedProjectTitle(); title != "" {
			a.say("scope: Global (pinned: %s · g w enters project mode)", title)
		} else {
			a.say("scope: Global · g J pins a project")
		}
	}
}

func (a *App) projectPin(s core.State, c core.Command) core.StepResult {
	pid := a.resolvePinProjectID(s, c)
	if pid == "" {
		return a.openProjectPinPicker(s, false)
	}
	a.setPinnedProject(pid)
	a.say("⌖ pinned project: %s · g w toggles project mode", a.title(core.Ref{Kind: data.KindTask, ID: pid}))
	return core.Next(s)
}

func (a *App) projectUnpin(s core.State, _ core.Command) core.StepResult {
	prev := a.pinnedProjectTitle()
	if prev == "" {
		a.say("no project is pinned")
		return core.Next(s)
	}
	a.setPinnedProject("")
	a.say("unpinned project: %s", prev)
	return core.Next(s)
}

func (a *App) projectTogglePin(s core.State, c core.Command) core.StepResult {
	pid := a.resolvePinProjectID(s, c)
	if pid == "" {
		return a.openProjectPinPicker(s, false)
	}
	title := a.title(core.Ref{Kind: data.KindTask, ID: pid})
	if a.pinnedProjectID() == pid {
		a.setPinnedProject("")
		a.say("unpinned project: %s", title)
		return core.Next(s)
	}
	a.setPinnedProject(pid)
	a.say("⌖ pinned project: %s · g w toggles project mode", title)
	return core.Next(s)
}

func (a *App) projectPinPick(s core.State, _ core.Command) core.StepResult {
	return a.openProjectPinPicker(s, false)
}

func (a *App) projectJumpPinned(s core.State, _ core.Command) core.StepResult {
	pid := a.pinnedProjectID()
	if pid == "" {
		a.say("no pinned project · pick one to pin")
		return a.openProjectPinPicker(s, false)
	}
	return a.projectTabOpen(s, core.Command{Args: map[string]any{"project": pid}})
}

func (a *App) openProjectPinPicker(s core.State, activateProjectMode bool) core.StepResult {
	projects := a.focusProjects()
	if len(projects) == 0 {
		a.say("no top-level projects to pin")
		return core.Next(s)
	}
	curPinned := a.pinnedProjectID()
	items := buildProjectPinPickerItems(projects, curPinned)
	title := "Pin Project"
	if activateProjectMode {
		title = "Pin Project & Enter Project Scope"
	}
	pm := picker.New(title, items)
	pm.MatchMode = a.matchMode()
	pm.Placeholder = "pick a project to pin"
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:            projectPinPickerID,
		StartInFilter: a.pickerStartInFilter(true),
		PageSize:      10,
		OnConfirm: func(picked picker.Item) modal.Result {
			a.afterModal(func(st core.State) core.State {
				a.applyProjectPinChoice(picked.ID, activateProjectMode)
				return st
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	a.say("pick a project to pin · enter confirms · esc cancels")
	return core.Next(a.pushModal(s, m))
}

func buildProjectPinPickerItems(projects []data.FocusProject, curPinned string) []picker.Item {
	items := make([]picker.Item, 0, len(projects)+1)
	if curPinned != "" {
		items = append(items, picker.Item{
			ID:       projectPinUnpinPickKey,
			Title:    "Unpin current project",
			Subtitle: "Return to unscoped global mode",
			Badges:   []ui.Badge{{Text: "clear", Tone: "muted"}},
			Keywords: []string{"unpin", "clear", "global", "none"},
		})
	}
	for _, p := range projects {
		ref := core.Ref{Kind: data.KindTask, ID: p.ID}
		var badges []ui.Badge
		if p.ID == curPinned {
			badges = append(badges, ui.Badge{Text: "⌖ pinned", Tone: "accent"})
		}
		items = append(items, picker.Item{
			ID:       p.ID,
			Title:    p.Title,
			Ref:      &ref,
			Scope:    data.KindTask,
			Badges:   badges,
			Keywords: []string{p.Title, p.ID, "project"},
		})
	}
	return items
}

func (a *App) applyProjectPinChoice(choiceID string, activateProjectMode bool) {
	if choiceID == projectPinUnpinPickKey || choiceID == "" {
		a.setPinnedProject("")
		a.say("unpinned project")
		return
	}
	a.setPinnedProject(choiceID)
	if activateProjectMode {
		a.setScopeMode(ScopeModeProject)
		a.announceScopeMode(ScopeModeProject)
		return
	}
	a.say("⌖ pinned project: %s · g w toggles project mode", a.title(core.Ref{Kind: data.KindTask, ID: choiceID}))
}

// --- Top-chrome indicator chip & banner labels ---------------------------------

// scopeModeChromeChip returns the subtle top-chrome indicator when scope != global
// or a project is pinned, or "" when ui.scopeModeChrome is off or unscoped global.
func (a *App) scopeModeChromeChip() string {
	if !a.scopeModeChromeEnabled() {
		return ""
	}
	mode := a.activeScopeMode()
	title := a.pinnedProjectTitle()
	switch mode {
	case ScopeModeProject:
		if title != "" {
			return "⌖ Project: " + clip(title, 22)
		}
		return ""
	case ScopeModeToday:
		if title != "" {
			return "◎ Today · ⌖ " + clip(title, 16)
		}
		return "◎ Today"
	default:
		if title != "" {
			return "⌖ " + clip(title, 20)
		}
		return ""
	}
}

// scopeModeBannerLabel returns a concise scope indicator for cross-project pages
// (Captures, Queue, Pages Picker), or "" in global mode.
func (a *App) scopeModeBannerLabel() string {
	switch a.activeScopeMode() {
	case ScopeModeProject:
		if title := a.pinnedProjectTitle(); title != "" {
			return "⌖ Project: " + clip(title, 24)
		}
	case ScopeModeToday:
		return "◎ Today"
	}
	return ""
}

// --- Declarative screen scoping helpers ----------------------------------------

func (a *App) scopeOutlineRoots(nodes []data.TreeNode, kind string) ([]data.TreeNode, bool) {
	if !data.IsTaskKind(kind) {
		return nodes, false
	}
	switch a.activeScopeMode() {
	case ScopeModeProject:
		if pid := a.pinnedProjectID(); pid != "" {
			return data.FilterTreeNodesByProject(nodes, pid), true
		}
	case ScopeModeToday:
		if a.focusHidesOutlineRoots(kind) {
			return nodes, false
		}
		return data.FilterTreeNodesToday(nodes, a.focus.cfg, a.now()), true
	}
	return nodes, false
}

func (a *App) scopeOutlineKey(kind string) string {
	if !data.IsTaskKind(kind) {
		return ""
	}
	mode := a.activeScopeMode()
	switch mode {
	case ScopeModeProject:
		return "|scope:project:" + a.pinnedProjectID()
	case ScopeModeToday:
		return "|scope:today:" + data.FocusDate(a.now())
	default:
		return ""
	}
}

func (a *App) scopeOutlineBadges(kind string) []ui.Badge {
	if !data.IsTaskKind(kind) {
		return nil
	}
	switch a.activeScopeMode() {
	case ScopeModeProject:
		if title := a.pinnedProjectTitle(); title != "" {
			return []ui.Badge{{Text: "⌖ Project: " + clip(title, 24), Tone: "accent"}}
		}
	case ScopeModeToday:
		return []ui.Badge{{Text: "◎ Today", Tone: "accent"}}
	}
	return nil
}

func (a *App) scopeTableCacheKey(kind string) string {
	mode := a.activeScopeMode()
	if mode == ScopeModeGlobal {
		return ""
	}
	if !data.SchemaHasProjectScope(a.schema(kind)) {
		return ""
	}
	switch mode {
	case ScopeModeProject:
		return "|scope:project:" + a.pinnedProjectID()
	case ScopeModeToday:
		return "|scope:today:" + data.FocusDate(a.now())
	default:
		return ""
	}
}

func (a *App) scopeTableRecords(schema data.Schema, items []data.Record) []data.Record {
	mode := a.activeScopeMode()
	if mode == ScopeModeGlobal || !data.SchemaHasProjectScope(schema) {
		return items
	}
	switch mode {
	case ScopeModeProject:
		return a.filterRecordsByProject(items, a.pinnedProjectID())
	case ScopeModeToday:
		return a.filterRecordsForToday(items)
	default:
		return items
	}
}

func (a *App) filterRecordsByProject(items []data.Record, projectID string) []data.Record {
	if projectID == "" {
		return items
	}
	out := make([]data.Record, 0, len(items))
	for _, rec := range items {
		if a.recordRootProjectID(rec) == projectID {
			out = append(out, rec)
		}
	}
	return out
}

func (a *App) filterRecordsForToday(items []data.Record) []data.Record {
	now := a.now()
	var priority []string
	if a.focus != nil {
		priority = data.TodaysPriorityProjects(a.focus.cfg, now)
	}
	out := make([]data.Record, 0, len(items))
	for _, rec := range items {
		if a.recordMatchesToday(rec, priority, now) {
			out = append(out, rec)
		}
	}
	return out
}

func (a *App) recordMatchesToday(rec data.Record, priority []string, now time.Time) bool {
	if len(priority) > 0 && a.focus != nil {
		if rootID := a.recordRootProjectID(rec); rootID != "" && data.IsPriorityProject(a.focus.cfg, rootID, now) {
			return true
		}
		if data.IsTaskKind(rec.Kind) {
			return false
		}
	}
	return data.IsRecordActiveToday(rec, now)
}

func (a *App) todaysPriorityProjectIDs() []string {
	if a == nil || a.focus == nil {
		return nil
	}
	return data.TodaysPriorityProjects(a.focus.cfg, a.now())
}

// --- Captures scoping & defaults -----------------------------------------------

func (a *App) scopeCaptureRecords(records []data.Record) []data.Record {
	return data.FilterCapturesByScope(records, a.activeScopeMode(), a.pinnedProjectID(), a.todaysPriorityProjectIDs(), a.now())
}

// defaultCaptureProjectID is the project a new capture starts in: the open
// project tab, then the pinned project while Project scope is on, then the
// project the surrounding page implies. The pin outranks the implied project
// because that one is remembered from the Projects page or the rail and stays
// set on pages that show no project at all.
func (a *App) defaultCaptureProjectID(s core.State) string {
	if isProjectTab(s.ActivePage) {
		return projectIDFromTab(s.ActivePage)
	}
	if a.activeScopeMode() == ScopeModeProject {
		if pid := a.pinnedProjectID(); pid != "" {
			return pid
		}
	}
	return a.activePortalProjectID(s)
}

// --- Queue scoping -------------------------------------------------------------

func (a *App) standaloneQueueProjectScope() string {
	if a.activeScopeMode() == ScopeModeProject {
		return a.pinnedProjectID()
	}
	return ""
}

func (a *App) scopeQueueCacheSuffix() string {
	switch a.activeScopeMode() {
	case ScopeModeProject:
		return "|scope:project:" + a.pinnedProjectID()
	case ScopeModeToday:
		return "|scope:today:" + data.FocusDate(a.now())
	default:
		return ""
	}
}

func (a *App) scopeQueueBoardInput(in *data.QueueBoardInput, projectID string) {
	if in == nil || projectID != "" || a.activeScopeMode() != ScopeModeToday {
		return
	}
	now := a.now()
	priority := a.todaysPriorityProjectIDs()
	filtered := make([]data.AgentQueueItem, 0, len(in.Items))
	for _, it := range in.Items {
		if a.recordMatchesToday(it.Task, priority, now) {
			filtered = append(filtered, it)
		}
	}
	in.Items = filtered
}

// --- Pages picker scoping & prioritization -------------------------------------

func (a *App) scopePagesPickerTitle() string {
	if lbl := a.scopeModeBannerLabel(); lbl != "" {
		return "Pages · " + lbl
	}
	return "Pages"
}

func (a *App) scopePagesPickerItems(items []picker.Item) []picker.Item {
	mode := a.activeScopeMode()
	if mode == ScopeModeGlobal {
		return items
	}
	if mode == ScopeModeProject {
		return a.scopePagesPickerForProject(items, a.pinnedProjectID())
	}
	return a.scopePagesPickerForToday(items)
}

func (a *App) scopePagesPickerForProject(items []picker.Item, projectID string) []picker.Item {
	if projectID == "" {
		return items
	}
	pinnedTabID := pagePickAppPrefix + "space:" + projectTabID(projectID)
	var prioritized, rest []picker.Item
	for _, it := range items {
		if it.ID == pinnedTabID {
			it.Badges = append(slices.Clone(it.Badges), ui.Badge{Text: "⌖ pinned", Tone: "accent"})
			prioritized = append(prioritized, it)
			continue
		}
		if isPagesPickerPaneItem(it.ID) {
			if a.pickerItemProjectID(it) == projectID {
				prioritized = append(prioritized, it)
			}
			continue
		}
		rest = append(rest, it)
	}
	return append(prioritized, rest...)
}

func (a *App) scopePagesPickerForToday(items []picker.Item) []picker.Item {
	now := a.now()
	priority := a.todaysPriorityProjectIDs()
	out := make([]picker.Item, 0, len(items))
	for _, it := range items {
		if isPagesPickerPaneItem(it.ID) && !a.pickerItemMatchesToday(it, priority, now) {
			continue
		}
		out = append(out, it)
	}
	return out
}

func isPagesPickerPaneItem(id string) bool {
	return strings.HasPrefix(id, pagePickPagePrefix) || strings.HasPrefix(id, pagePickScreenPrefix)
}

func (a *App) pickerItemProjectID(it picker.Item) string {
	if a == nil || a.Store == nil || it.Ref == nil || it.Ref.ID == "" {
		return ""
	}
	rec, err := a.Store.Get(*it.Ref)
	if err != nil || rec == nil {
		return ""
	}
	if pid := a.recordRootProjectID(*rec); pid != "" {
		return pid
	}
	if pRef, ok := rec.RefField("pane"); ok && data.IsPaneRef(pRef) {
		if pane, err := a.Store.Get(pRef); err == nil && pane != nil {
			return a.recordRootProjectID(*pane)
		}
	}
	return ""
}

func (a *App) pickerItemMatchesToday(it picker.Item, priority []string, now time.Time) bool {
	if it.Unread {
		return true
	}
	if len(priority) > 0 && a.focus != nil {
		if pid := a.pickerItemProjectID(it); pid != "" && data.IsPriorityProject(a.focus.cfg, pid, now) {
			return true
		}
	}
	if a.Store == nil || it.Ref == nil || it.Ref.ID == "" {
		return false
	}
	rec, err := a.Store.Get(*it.Ref)
	if err != nil || rec == nil {
		return false
	}
	return data.IsRecordActiveToday(*rec, now)
}

// --- New Task / New Note / New Record creation defaults ------------------------

func (a *App) applyScopeCreateDefaults(kind string, schema data.Schema, getField func(string) any, setField func(string, any)) {
	if a.activeScopeMode() != ScopeModeProject {
		return
	}
	pid := a.pinnedProjectID()
	if pid == "" {
		return
	}
	projRef := core.Ref{Kind: data.KindTask, ID: pid}
	if data.IsTaskKind(kind) {
		if getField("parent") == nil {
			setField("parent", projRef)
		}
		return
	}
	for _, field := range []string{"project", "task"} {
		if _, has := schema.Field(field); has && getField(field) == nil {
			setField(field, projRef)
		}
	}
}

// --- Projects Directory decoration ---------------------------------------------

func (a *App) decoratePinnedProjectRow(row *table.Row, projectID string) {
	if row == nil || projectID == "" || a.pinnedProjectID() != projectID {
		return
	}
	row.Facts["pinnedProject"] = true
	row.Badges = append(row.Badges, ui.Badge{Text: "⌖ pinned", Tone: "accent"})
}
