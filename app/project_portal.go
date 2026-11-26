package app

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/pathpicker"
	"ofam.dev/ochazuke/widgets/picker"
)

const (
	CmdProjectEditDefaultContext = "project.context.default.edit"
	CmdProjectPickDefaultDir     = "project.dir.pick"
	CmdProjectPickTheme          = "project.theme.pick"
	CmdProjectSpawnAgent         = "project.agent.spawn"
)

func (a *App) projectPortalOnPEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetProjectPortalOnP, true)
}

func projectPortalSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		spec(CmdProjectEditDefaultContext, "Edit the project's default context Markdown prose (auto-injected into agent panes)", "editing"),
		spec(CmdProjectPickDefaultDir, "Pick and set the project's default/base directory", "editing"),
		spec(CmdProjectPickTheme, "Pick an optional theme override for this project portal", "editing"),
		spec(CmdProjectSpawnAgent, "Start a new agent pane in this project portal with its default context injected", "editing"),
	}
}

func projectPortalBindings() []core.Binding {
	n := ModeNormal
	return []core.Binding{
		bind(n, "", "P", CmdProjectPageOpen, "open project portal"),
		when(bind(n, views.ScopeProjectContent, "c", CmdProjectEditDefaultContext, "edit default context"), "facts.section == \"home\""),
		when(bind(n, views.ScopeProjectContent, "d", CmdProjectPickDefaultDir, "set default dir"), "facts.section == \"home\""),
		when(bind(n, views.ScopeProjectContent, "t", CmdProjectPickTheme, "set project theme"), "facts.section == \"home\""),
		bind(n, views.ScopeProjectTab, "A", CmdProjectSpawnAgent, "start project agent"),
		bind(n, views.ScopeProjectNav, "A", CmdProjectSpawnAgent, "start project agent"),
	}
}

func (a *App) registerProjectPortalHandlers() {
	a.handle(CmdProjectEditDefaultContext, a.projectEditDefaultContext)
	a.handle(CmdProjectPickDefaultDir, a.projectPickDefaultDir)
	a.handle(CmdProjectPickTheme, a.projectPickTheme)
	a.handle(CmdProjectSpawnAgent, a.projectSpawnAgent)
}

// projectPortalOpen handles P and ctrl+a P: opens the focused item's Project
// Portal Tab. When invoked from a live terminal (ctrl+a P), opens that live
// pane inline in the Project Portal and stays attached in ModeTerminal.
func (a *App) projectPortalOpen(s core.State, c core.Command) core.StepResult {
	if !a.projectPortalOnPEnabled() || !a.projectsDynamicTabsEnabled() || !a.projectWorkspaceEnabled() {
		return a.projectHomeOpen(s, c)
	}
	wasTerminal := a.Engine.Mode(s) == ModeTerminal
	var livePane *data.Record
	if p, ok := a.activePane(s); ok && p != nil {
		cp := *p
		livePane = &cp
	}

	projRef, ok := a.resolvePortalProjectRef(s, c, livePane)
	if !ok {
		return a.projectHomeOpen(s, c)
	}

	a.ensureProjectTabOpen(projRef.ID)
	tabID := projectTabID(projRef.ID)
	pc := a.projectCtrl()

	if wasTerminal && livePane != nil && a.projectChildPaneViewEnabled() {
		if pc.childPaneByProject == nil {
			pc.childPaneByProject = map[string]string{}
		}
		pc.childPaneByProject[projRef.ID] = livePane.ID
		pc.setSection(projRef.ID, "panes")
		a.panes.activePage[livePane.ID] = views.ScreenPageID
		s = a.openSpace(s, tabID)
		s = a.enterScreen(withFocus(s, siteScreen), *livePane)
		a.viewMemo.gen++
		a.say("project portal: %s (live pane)", a.title(projRef))
		return core.Next(s)
	}

	s = a.openSpace(s, tabID)
	if s.Pages[tabID].FocusID == "" {
		sec := pc.activeSection(projRef.ID)
		s = withPage(s, tabID, projectNavPrefix+sec, ModeNormal)
	}
	a.viewMemo.gen++
	a.say("project portal: %s", a.title(projRef))
	return core.Next(s)
}

func (a *App) resolvePortalProjectRef(s core.State, c core.Command, livePane *data.Record) (core.Ref, bool) {
	if ref, ok := a.resolveProjectTargetRef(s, c); ok {
		return ref, true
	}
	if livePane != nil {
		if _, projRef := a.resolvePaneProject(*livePane); projRef != nil && projRef.ID != "" {
			return *projRef, true
		}
	}
	if ref, ok := a.subject(s, c); ok && a.Store != nil {
		if ref.Kind == data.KindTask {
			if root, ok := a.topLevelTask(ref); ok {
				return root, true
			}
			return ref, true
		}
		if rec, err := a.Store.Get(ref); err == nil && rec != nil {
			if tref, hasTask := rec.RefField("task"); hasTask && tref.Kind == data.KindTask {
				if root, ok := a.topLevelTask(tref); ok {
					return root, true
				}
				return tref, true
			}
		}
	}
	return core.Ref{}, false
}

// buildProjectDefaultContext compiles the project's editable defaultContextMd
// prose together with its starred/official project context items so every new
// agent pane or dispatch inherits full project context automatically.
func (a *App) buildProjectDefaultContext(projID string) string {
	if a.Store == nil || strings.TrimSpace(projID) == "" {
		return ""
	}
	projRef := core.Ref{Kind: data.KindTask, ID: projID}
	projRec, err := a.Store.Get(projRef)
	if err != nil || projRec == nil {
		return ""
	}
	var b strings.Builder
	title := views.Title(a.schema(data.KindTask), *projRec)
	fmt.Fprintf(&b, "# Project Default Context: %s\n", title)
	if dir := strings.TrimSpace(projRec.Text("defaultDir")); dir != "" {
		fmt.Fprintf(&b, "- Base Directory: `%s`\n", dir)
	}
	if prose := strings.TrimSpace(projRec.Text("defaultContextMd")); prose != "" {
		b.WriteString("\n")
		b.WriteString(prose)
		b.WriteString("\n")
	}
	if items, err := a.Store.Query(data.Query{Kind: data.KindContext}); err == nil {
		var official []string
		for _, rec := range items.Items {
			tref, ok := rec.RefField("task")
			if !ok {
				continue
			}
			root, _ := a.topLevelTask(tref)
			if tref.ID != projID && root.ID != projID {
				continue
			}
			if !rec.IsStarred() && !rec.Bool("official") {
				continue
			}
			lbl := strings.TrimSpace(rec.Text("title"))
			val := strings.TrimSpace(rec.Text("value"))
			if lbl == "" {
				lbl = val
			}
			if val != "" && val != lbl {
				official = append(official, fmt.Sprintf("- **%s**: %s", lbl, val))
			} else if lbl != "" {
				official = append(official, "- "+lbl)
			}
		}
		if len(official) > 0 {
			b.WriteString("\n## Official Context Items\n\n")
			b.WriteString(strings.Join(official, "\n"))
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func (a *App) activePortalProjectID(s core.State) string {
	if isProjectTab(s.ActivePage) {
		return projectIDFromTab(s.ActivePage)
	}
	if key := a.activeProjectWorkspaceKey(s); key != "" {
		if id, ok := strings.CutPrefix(key, "task:"); ok {
			return id
		}
		return key
	}
	if ref, ok := a.resolvePortalProjectRef(s, core.Command{}, nil); ok {
		return ref.ID
	}
	return ""
}

func (a *App) projectEditDefaultContext(s core.State, _ core.Command) core.StepResult {
	projID := a.activePortalProjectID(s)
	if projID == "" || a.Store == nil {
		a.say("open a project portal first")
		return core.Next(s)
	}
	projRef := core.Ref{Kind: data.KindTask, ID: projID}
	rec, err := a.Store.Get(projRef)
	if err != nil || rec == nil {
		a.fail("edit default context", err)
		return core.Next(s)
	}
	spec := richComposerSpec{
		ID:          "project.default.context",
		Scope:       "composer",
		Title:       "Default Context Markdown · " + views.Title(a.schema(data.KindTask), *rec),
		Subtitle:    "Auto-injected into every new agent pane for this project · ctrl+s save · esc cancel",
		Placeholder: "Write prose context, conventions, links, or guidelines for agents...",
		InputTitle:  "Default Context (Markdown)",
		Cwd:         rec.Text("defaultDir"),
		OnSubmit: func(text string, _ []data.Attachment, _ ComposerMode) (tea.Cmd, error) {
			_, _, err := a.Store.Patch(data.PatchInput{Ref: projRef, Fields: map[string]any{"defaultContextMd": text}}, User)
			if err != nil {
				return nil, err
			}
			a.viewMemo.gen++
			a.say("saved project default context")
			return nil, nil
		},
	}
	cm := newRichComposerModal(a, spec)
	cm.in = setInputText(cm.in, rec.Text("defaultContextMd"))
	return core.Next(a.pushModal(s, cm))
}

func (a *App) projectPickDefaultDir(s core.State, _ core.Command) core.StepResult {
	projID := a.activePortalProjectID(s)
	if projID == "" || a.Store == nil {
		a.say("open a project portal first")
		return core.Next(s)
	}
	projRef := core.Ref{Kind: data.KindTask, ID: projID}
	rec, err := a.Store.Get(projRef)
	if err != nil || rec == nil {
		a.fail("set default dir", err)
		return core.Next(s)
	}
	pm, ok := a.newPathPickerModal(rec.Text("defaultDir"), func(entry pathpicker.DirEntry) modal.Result {
		if _, _, err := a.Store.Patch(data.PatchInput{Ref: projRef, Fields: map[string]any{"defaultDir": entry.Path}}, User); err != nil {
			return modal.Result{Handled: true, Status: err.Error(), SetStatus: true}
		}
		a.viewMemo.gen++
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit, Status: "default dir: " + entry.Path, SetStatus: true}
	})
	if !ok {
		return core.Next(s)
	}
	return core.Next(a.pushModal(s, pm))
}

func (a *App) projectPickTheme(s core.State, _ core.Command) core.StepResult {
	projID := a.activePortalProjectID(s)
	if projID == "" || a.Store == nil {
		a.say("open a project portal first")
		return core.Next(s)
	}
	projRef := core.Ref{Kind: data.KindTask, ID: projID}
	items := []picker.Item{{ID: "", Title: "(inherit global theme)", Subtitle: "default"}}
	for _, name := range ThemeNames() {
		items = append(items, picker.Item{ID: name, Title: name, Subtitle: "project theme override"})
	}
	pm := picker.New("Project Portal Theme", items)
	pm.MatchMode = a.matchMode()
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       "project.theme",
		PageSize: 10,
		OnConfirm: func(it picker.Item) modal.Result {
			if _, _, err := a.Store.Patch(data.PatchInput{Ref: projRef, Fields: map[string]any{"projectTheme": it.ID}}, User); err != nil {
				return modal.Result{Handled: true, Status: err.Error(), SetStatus: true}
			}
			a.viewMemo.gen++
			label := it.ID
			if label == "" {
				label = "global default"
			}
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit, Status: "project theme: " + label, SetStatus: true}
		},
	})
	return core.Next(a.pushModal(s, m))
}

// spawnProjectAgentPane creates a new agent pane assigned to projID in the
// project's defaultDir, automatically records/injects the project's Default
// Context (official items + editable Markdown prose), and returns the new pane.
func (a *App) spawnProjectAgentPane(s core.State, projID, harness, extraPrompt string) (core.State, *data.Record, error) {
	if a.Store == nil || strings.TrimSpace(projID) == "" {
		return s, nil, fmt.Errorf("no project selected")
	}
	projRef := core.Ref{Kind: data.KindTask, ID: projID}
	projRec, err := a.Store.Get(projRef)
	if err != nil || projRec == nil {
		return s, nil, err
	}
	if strings.TrimSpace(harness) == "" {
		harness = strings.TrimSpace(projRec.Text("defaultHarness"))
	}
	if harness == "" {
		harness = "claude"
	}
	cwd := strings.TrimSpace(projRec.Text("defaultDir"))
	title := fmt.Sprintf("%s: %s", harness, views.Title(a.schema(data.KindTask), *projRec))
	defaultCtx := a.buildProjectDefaultContext(projID)
	fields := map[string]any{
		"title":     title,
		"harness":   harness,
		"task":      projRef,
		"runStatus": "running",
	}
	if cwd != "" {
		fields["cwd"] = cwd
	}
	if defaultCtx != "" {
		fields["defaultContext"] = defaultCtx
	}
	created, _, err := a.Store.Create(data.CreateInput{Kind: data.KindPane, Fields: fields}, User)
	if err != nil {
		return s, nil, err
	}
	a.ensureProjectTabOpen(projID)
	pc := a.projectCtrl()
	if pc.childPaneByProject == nil {
		pc.childPaneByProject = map[string]string{}
	}
	pc.childPaneByProject[projID] = created.ID
	pc.setSection(projID, "panes")
	tabID := projectTabID(projID)
	s = a.openSpace(s, tabID)
	s = a.enterScreen(withFocus(s, siteScreen), *created)

	payload := strings.TrimSpace(strings.Join([]string{defaultCtx, strings.TrimSpace(extraPrompt)}, "\n\n"))
	if payload != "" {
		a.deliverComposerPrompt(*created, payload, ComposerModeQueue)
	}
	a.viewMemo.gen++
	return s, created, nil
}

func (a *App) projectSpawnAgent(s core.State, c core.Command) core.StepResult {
	projID := a.activePortalProjectID(s)
	if projID == "" {
		a.say("open a project portal first")
		return core.Next(s)
	}
	next, pane, err := a.spawnProjectAgentPane(s, projID, c.Arg("harness"), "")
	if err != nil {
		a.fail("start project agent", err)
		return core.Next(s)
	}
	a.say("started agent pane %s with project default context", pane.ID)
	return core.Next(next)
}

// homePortalSettingsGroup adds the Project Portal Settings & Default Context
// group directly onto the Project Home dashboard.
func (a *App) homePortalSettingsGroup(ws projectWorkspaceData, interactive bool) views.ProjectHomeGroup {
	grp := views.ProjectHomeGroup{Key: "portal", Title: "PORTAL SETTINGS & DEFAULT CONTEXT", Icon: "⚙", Tone: "accent"}
	if ws.projRec == nil {
		return grp
	}
	dir := strings.TrimSpace(ws.projRec.Text("defaultDir"))
	if dir == "" {
		dir = "(not set · press d to pick)"
	}
	theme := strings.TrimSpace(ws.projRec.Text("projectTheme"))
	if theme == "" {
		theme = "(global default · press t to override)"
	}
	ctxPreview := "(empty · press c to edit default context Markdown)"
	if prose := strings.TrimSpace(ws.projRec.Text("defaultContextMd")); prose != "" {
		ctxPreview = clip(strings.ReplaceAll(prose, "\n", " "), 64)
	}
	rows := []struct {
		id, label, detail, glyph, tone string
	}{
		{"dir", "Base Directory", dir, "⌂", "info"},
		{"theme", "Portal Theme", theme, "◐", "accent"},
		{"ctx", fmt.Sprintf("Default Context (%d official)", len(ws.officialItems)), ctxPreview, "★", "accent"},
		{"spawn", "Start New Agent Pane", "press A to launch agent with default context injected", "▶", "success"},
	}
	for _, r := range rows {
		grp.Items = append(grp.Items, views.ProjectHomeItem{
			ID:       "portal." + r.id,
			Icon:     r.glyph,
			Tone:     r.tone,
			Title:    r.label,
			Subtitle: r.detail,
		})
	}
	grp.Count = len(grp.Items)
	return grp
}
