package app

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ext"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/runs"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/toast"
)

// --- 1. Unified `'` Pages Picker ([app], [pane], [noun]) --------------------------

const (
	pagePickAppPrefix    = "app:"
	pagePickNounPrefix   = "noun:"
	pagePickPagePrefix   = "page:"
	pagePickScreenPrefix = "screen:"
)

// coreNounOrder places the primary daily-driver global nouns first in the
// `[noun]` group of the unified `'` Pages picker.
var coreNounOrder = []string{
	data.KindTask,
	data.KindLane,
	data.KindDocument,
	data.KindNote,
	data.KindQuestion,
	data.KindCommand,
	data.KindContext,
	data.KindSchedule,
	data.KindSignal,
	data.KindPage,
}

func (a *App) schemaCount(kind string) int {
	if a.isExternalKind(kind) && a.readCache != nil {
		if snap, ok := a.readCache.Get(kind); ok {
			return len(snap.Items)
		}
	}
	if res, err := a.Store.Query(data.Query{Kind: kind, IncludeArchived: a.showArchived, Limit: 1}); err == nil {
		return res.Total
	}
	return 0
}

// pagesPick opens the unified `'` Pages picker containing three clearly badged
// groups: [app] App pages, [pane] Pane site & MCP pages, and [noun] Global noun
// pages (including all mounted extension nouns). Never empty.
func (a *App) pagesPick(s core.State, _ core.Command) core.StepResult {
	var items []picker.Item

	// Group 1: [app] App pages (spaces + core overlays).
	appPages := []struct {
		id, title, subtitle, key string
		keywords                 []string
	}{
		{pagePickAppPrefix + "space:" + SpacePanes, "Panes", "Live terminals, pane sites, and project overviews", "1", []string{"app", "space", "panes", "terminals"}},
		{pagePickAppPrefix + "space:" + SpaceWork, "Work", "Tasks tree and global noun views", "2", []string{"app", "space", "work", "tasks"}},
		{pagePickAppPrefix + "space:" + SpaceRuns, "Runs", "Command runs and captured output", "3", []string{"app", "space", "runs"}},
		{pagePickAppPrefix + "space:" + SpaceExtensions, "Extensions", "Discovered o-* tools and setup", "4", []string{"app", "space", "extensions"}},
		{pagePickAppPrefix + "about", "About", "Build versions, daemon PIDs, storage paths, and harnesses", "g A", []string{"app", "about", "diagnostics", "version"}},
		{pagePickAppPrefix + "settings", "Settings", "Theme, layout, confirmations, and preferences", "S", []string{"app", "settings", "config"}},
		{pagePickAppPrefix + "bindings", "Bindings", "Browse, filter, rebind, and reset key bindings", "B", []string{"app", "bindings", "keys", "keymap"}},
		{pagePickAppPrefix + "outline", "Outline", "Full task hierarchy outline overlay", "T", []string{"app", "outline", "tree"}},
		{pagePickAppPrefix + "inspector", "Inspector", "Inspect registered actions, parameters, and modes", ": inspect", []string{"app", "inspector", "actions", "modes"}},
	}
	for _, ap := range appPages {
		badges := []ui.Badge{{Text: "[app]", Tone: "info"}}
		if ap.key != "" {
			badges = append(badges, ui.Badge{Text: ap.key, Tone: "muted"})
		}
		items = append(items, picker.Item{
			ID:       ap.id,
			Title:    ap.title,
			Subtitle: ap.subtitle,
			Badges:   badges,
			Keywords: ap.keywords,
		})
	}

	// Group 2: [pane] Pane site & MCP pages (unread/open questions first, plus each pane's Screen).
	if res, err := a.Router.ListPages(mcpui.ListPagesArgs{}); err == nil {
		for _, p := range res.Pages {
			ref := core.Ref{Kind: data.KindPage, ID: p.ID}
			sub := p.PageKind
			if p.PaneRef != nil {
				paneTitle := a.title(*p.PaneRef)
				if sub != "" {
					sub = paneTitle + " · " + sub
				} else {
					sub = paneTitle
				}
			}
			badges := []ui.Badge{{Text: "[pane]", Tone: "accent"}}
			open := 0
			for _, q := range p.Questions {
				if !q.Answered {
					open++
				}
			}
			if open > 0 {
				badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d open", open), Tone: "warning"})
			}
			items = append(items, picker.Item{
				ID:       pagePickPagePrefix + p.ID,
				Title:    p.Title,
				Subtitle: sub,
				Unread:   p.Unread,
				Ref:      &ref,
				Scope:    data.KindPage,
				Badges:   badges,
				Keywords: []string{"pane", p.Slug, p.PageKind, sub},
			})
		}
	}
	if panesRes, err := a.Store.Query(data.Query{Kind: data.KindPane, IncludeArchived: a.showArchived}); err == nil {
		paneSchema := a.schema(data.KindPane)
		for _, pane := range panesRes.Items {
			ref := pane.Ref()
			paneTitle := views.Title(paneSchema, pane)
			items = append(items, picker.Item{
				ID:       pagePickScreenPrefix + pane.ID,
				Title:    "Screen · " + paneTitle,
				Subtitle: pane.Text("harness"),
				Ref:      &ref,
				Scope:    data.KindPane,
				Badges:   []ui.Badge{{Text: "[pane]", Tone: "accent"}, {Text: "screen", Tone: "muted"}},
				Keywords: []string{"pane", "screen", "terminal", "screen " + paneTitle, paneTitle, pane.Text("harness")},
			})
		}
	}

	// Group 3: [noun] Global noun pages (core daily nouns + every registered & mounted extension noun).
	schemas := a.Store.Registry().List()
	byKind := make(map[string]data.Schema, len(schemas))
	for _, sc := range schemas {
		byKind[sc.Kind] = sc
	}
	addedKind := map[string]bool{}
	appendNoun := func(sc data.Schema) {
		if addedKind[sc.Kind] {
			return
		}
		addedKind[sc.Kind] = true
		title := sc.Plural
		if title == "" {
			title = sc.Title
		}
		if title == "" {
			title = sc.Kind
		}
		n := a.schemaCount(sc.Kind)
		badges := []ui.Badge{{Text: "[noun]", Tone: "success"}, {Text: fmt.Sprint(n), Tone: "muted"}}
		sub := sc.Kind
		if a.isExternalKind(sc.Kind) {
			badges = append(badges, ui.Badge{Text: "ext", Tone: "info"})
			sub = sc.Kind + " · extension"
		}
		items = append(items, picker.Item{
			ID:       pagePickNounPrefix + sc.Kind,
			Title:    title,
			Subtitle: sub,
			Badges:   badges,
			Keywords: []string{"noun", sc.Kind, sc.Title, sc.Plural},
		})
	}
	for _, kind := range coreNounOrder {
		if sc, ok := byKind[kind]; ok {
			appendNoun(sc)
		}
	}
	for _, sc := range schemas {
		appendNoun(sc)
	}

	pm := picker.New("Pages", items)
	pm.Placeholder = "search [app], [pane], and [noun] pages"
	return core.Next(a.openPicker(s, modalPages, pm))
}

func (a *App) jumpToUnifiedPage(s core.State, pickedID string) core.State {
	switch {
	case strings.HasPrefix(pickedID, pagePickAppPrefix):
		target := strings.TrimPrefix(pickedID, pagePickAppPrefix)
		switch {
		case strings.HasPrefix(target, "space:"):
			return a.openSpace(s, strings.TrimPrefix(target, "space:"))
		case target == "about":
			return a.aboutOpen(s, core.Command{}).State
		case target == "settings":
			return a.settingsOpen(s, core.Command{}).State
		case target == "bindings":
			return a.bindingsOpen(s, core.Command{}).State
		case target == "outline":
			tm := a.trees.outlineModel()
			a.syncTreeModel(tm, data.KindTask, modalOutline+".tree")
			return openModal(a.dropModals(s), modalOutline, ModeOutline, modalOutline+".tree")
		case target == "inspector":
			return a.inspectOpen(s, core.Command{Args: map[string]any{"what": InspectActions}}).State
		}
	case strings.HasPrefix(pickedID, pagePickNounPrefix):
		return a.browse(s, strings.TrimPrefix(pickedID, pagePickNounPrefix))
	case strings.HasPrefix(pickedID, pagePickScreenPrefix):
		paneID := strings.TrimPrefix(pickedID, pagePickScreenPrefix)
		paneRef := core.Ref{Kind: data.KindPane, ID: paneID}
		rail := a.rail()
		if !selectRow(rail, data.RefText(paneRef)) {
			rail.Query = ""
			selectRow(rail, data.RefText(paneRef))
		}
		s = a.dropModals(s)
		s.ActivePage = SpacePanes
		a.tocFilter = ""
		a.activePage[paneID] = views.ScreenPageID
		return withPage(s, SpacePanes, tocPrefix+views.ScreenPageID, ModeNormal)
	case strings.HasPrefix(pickedID, pagePickPagePrefix):
		return a.jumpToPage(s, strings.TrimPrefix(pickedID, pagePickPagePrefix))
	default:
		return a.jumpToPage(s, pickedID)
	}
	return s
}

// --- 2. Global KindQuestion answering synced to pane ui.page ----------------------

func parseQuestionChoices(rec data.Record) []string {
	raw, has := rec.Value("choices")
	if !has || raw == nil {
		return nil
	}
	var out []string
	switch v := raw.(type) {
	case []string:
		return slices.Clone(v)
	case []any:
		for _, elem := range v {
			switch item := elem.(type) {
			case string:
				if s := strings.TrimSpace(item); s != "" {
					out = append(out, s)
				}
			case map[string]any:
				if id := strings.TrimSpace(data.Text(item["id"])); id != "" {
					out = append(out, id)
				} else if lbl := strings.TrimSpace(data.Text(item["label"])); lbl != "" {
					out = append(out, lbl)
				}
			default:
				if s := strings.TrimSpace(data.Text(elem)); s != "" {
					out = append(out, s)
				}
			}
		}
	case string:
		var parsed []any
		if err := json.Unmarshal([]byte(v), &parsed); err == nil {
			recCopy := rec
			recCopy.Fields = map[string]any{"choices": parsed}
			return parseQuestionChoices(recCopy)
		}
	}
	return out
}

func (a *App) openAnswerFormForQuestionRecord(s core.State, rec data.Record) core.State {
	choices := parseQuestionChoices(rec)
	var specs []core.ParamSpec
	values := map[string]any{}
	existing := strings.TrimSpace(rec.Text("answer"))
	if len(choices) > 0 {
		specs = []core.ParamSpec{
			{Name: "choice", Type: core.ParamString, Required: true, Enum: choices, Default: choices[0], Description: "select an answer"},
			{Name: "notes", Type: core.ParamString, Description: "optional notes"},
		}
		if existing != "" && slices.Contains(choices, existing) {
			values["choice"] = existing
		} else {
			values["choice"] = choices[0]
		}
	} else {
		specs = []core.ParamSpec{
			{Name: "answer", Type: core.ParamString, Required: true, Description: "your answer"},
		}
		if existing != "" {
			values["answer"] = existing
		}
	}
	promptTitle := rec.Text("prompt")
	if promptTitle == "" {
		promptTitle = rec.ID
	}
	fm := form.New(promptTitle, specs, values)
	fm.SubmitRow = true
	fm.SubmitLabel = "Send answer"
	fs := &formState{
		fm:         fm,
		questionID: rec.ID,
	}
	a.attachFormDrafts(fs, "answer:question:"+rec.ID)
	a.answer = fs
	a.say("space/h/l choose · i types · ctrl+s sends and syncs to pane")
	return openModal(s, modalAnswer, ModeForm, modalAnswer+".form")
}

func formatQuestionAnswerValues(values map[string]any) string {
	if choice := strings.TrimSpace(data.Text(values["choice"])); choice != "" {
		if notes := strings.TrimSpace(data.Text(values["notes"])); notes != "" {
			return choice + " (" + notes + ")"
		}
		return choice
	}
	if ans := strings.TrimSpace(data.Text(values["answer"])); ans != "" {
		return ans
	}
	b, err := json.Marshal(values)
	if err != nil {
		return data.Text(values)
	}
	return string(b)
}

// --- 3. Global KindCommand run & edit-before-run ----------------------------------

func (a *App) execSuggestedCommand(s core.State, rec data.Record, cmdLine, dir, title string) core.State {
	cmdLine = strings.TrimSpace(cmdLine)
	if cmdLine == "" {
		cmdLine = strings.TrimSpace(rec.Text("scriptPath"))
	}
	if cmdLine == "" {
		a.say("command record has no command or scriptPath")
		return s
	}
	if title == "" {
		title = rec.Text("title")
	}
	if title == "" {
		title = cmdLine
	}
	var (
		runRec *data.Record
		err    error
	)
	if a.Runner != nil {
		spec := runs.StartSpec{
			Title:   title,
			Command: cmdLine,
			Dir:     expandHome(dir),
			Actor:   runActor,
		}
		if taskRef, ok := rec.RefField("task"); ok {
			spec.TaskRef = taskRef
		}
		runRec, err = a.Runner.Start(context.Background(), spec)
		s = a.started(s, "ran "+title, runRec, err)
	} else {
		fields := map[string]any{"title": title, "command": cmdLine, "runStatus": "queued"}
		if dir != "" {
			fields["cwd"] = dir
		}
		if taskRef, ok := rec.RefField("task"); ok {
			fields["task"] = taskRef
		}
		var cs *data.ChangeSet
		runRec, cs, err = a.Store.Create(data.CreateInput{Kind: data.KindRun, Fields: fields}, User)
		a.wrote("ran "+title, cs, err)
		if err == nil && runRec != nil {
			a.gen++
			selectRow(a.records(runsTable, data.KindRun), data.RefText(runRec.Ref()))
		}
	}
	if err != nil {
		a.pushToast("Command failed: "+err.Error(), toast.Err)
		return s
	}
	patchFields := map[string]any{"status": "ran"}
	if runRec != nil {
		patchFields["lastRun"] = runRec.Ref()
	}
	if _, _, perr := a.Store.Patch(data.PatchInput{
		Ref:     rec.Ref(),
		Fields:  patchFields,
		Summary: "ran suggested command",
	}, User); perr != nil {
		a.fail("update command status", perr)
	}
	a.pushToast("Running "+title+" in 3 Runs", toast.Ok)
	s = a.openSpace(s, SpaceRuns)
	if runRec != nil {
		a.gen++
		selectRow(a.records(runsTable, data.KindRun), data.RefText(runRec.Ref()))
	}
	return s
}

func (a *App) commandRun(s core.State, c core.Command) core.StepResult {
	// On a Show page ref field, Enter follows the reference link rather than running.
	if raw, isMap := c.Args["ref"].(map[string]any); isMap {
		if r, ok := data.AsRef(raw); ok {
			return core.Next(a.show(s, r))
		}
	}
	ref, ok := a.subject(s, c)
	if !ok || ref.Kind != data.KindCommand {
		a.say("no command here to run")
		return core.Next(s)
	}
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("run command", err)
		return core.Next(s)
	}
	return core.Next(a.execSuggestedCommand(s, *rec, rec.Text("command"), rec.Text("cwd"), rec.Text("title")))
}

func (a *App) commandEditRun(s core.State, c core.Command) core.StepResult {
	ref, ok := a.subject(s, c)
	if !ok || ref.Kind != data.KindCommand {
		a.say("no command here to edit and run")
		return core.Next(s)
	}
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("edit command", err)
		return core.Next(s)
	}
	cmdStr := rec.Text("command")
	if cmdStr == "" {
		cmdStr = rec.Text("scriptPath")
	}
	specs := []core.ParamSpec{
		{Name: "command", Type: core.ParamString, Required: true, Description: "shell command to run"},
		{Name: "args", Type: core.ParamString, Description: "optional extra arguments appended to command"},
		{Name: "cwd", Type: core.ParamPath, Description: "working directory"},
		{Name: "title", Type: core.ParamString, Description: "run label"},
	}
	values := map[string]any{
		"command": cmdStr,
		"cwd":     rec.Text("cwd"),
		"title":   rec.Text("title"),
	}
	recCopy := *rec
	next := a.openLaunch(s, "Edit & Run · "+views.Title(a.schema(data.KindCommand), recCopy), specs, values, func(st core.State, v map[string]any) (core.State, bool) {
		full := strings.TrimSpace(data.Text(v["command"]))
		if extra := strings.TrimSpace(data.Text(v["args"])); extra != "" {
			full = full + " " + extra
		}
		if full == "" {
			a.say("command is required")
			return st, false
		}
		dir := strings.TrimSpace(data.Text(v["cwd"]))
		title := strings.TrimSpace(data.Text(v["title"]))
		return a.execSuggestedCommand(st, recCopy, full, dir, title), true
	})
	a.launch.fm.SubmitLabel = "Run command"
	a.say("edit command/args/cwd · ctrl+s runs in 3 Runs")
	return core.Next(next)
}

// --- 4. pane.associate + data.BackfillPaneTask ------------------------------------

func (a *App) paneAssociate(s core.State, c core.Command) core.StepResult {
	var pane *data.Record
	if ref, ok := a.subject(s, c); ok && ref.Kind == data.KindPane {
		if rec, err := a.Store.Get(ref); err == nil {
			pane = rec
		}
	}
	if pane == nil {
		if sel, ok := a.SelectedPane(); ok {
			pane = sel
		}
	}
	if pane == nil {
		a.say("no pane selected to associate")
		return core.Next(s)
	}
	nodes, err := a.Store.TaskTree(a.showArchived)
	if err != nil {
		a.fail("associate pane", err)
		return core.Next(s)
	}
	current, _ := pane.RefField("task")
	schema := a.schema(data.KindTask)
	var items []picker.Item
	var walk func(ns []data.TaskNode, path string)
	walk = func(ns []data.TaskNode, path string) {
		for _, n := range ns {
			title := views.Title(schema, n.Record)
			r := n.Record.Ref()
			item := picker.Item{
				ID:       r.ID,
				Title:    strings.Repeat("  ", n.Depth) + title,
				Subtitle: path,
				Ref:      &r,
				Scope:    data.KindTask,
				Badges:   views.StatusBadges(schema, n.Record),
				Keywords: []string{title, path, r.ID},
			}
			if r == current {
				item.Badges = append(item.Badges, ui.Badge{Text: "now", Tone: "accent"})
			}
			items = append(items, item)
			nextPath := title
			if path != "" {
				nextPath = path + " › " + title
			}
			walk(n.Children, nextPath)
		}
	}
	walk(nodes, "")
	paneRef := pane.Ref()
	a.associatePaneRef = &paneRef
	pm := picker.New("Associate "+a.title(paneRef)+" with task", items)
	pm.Placeholder = "pick an existing task or type a new task title"
	a.enablePickerCreate(modalPaneAssociate, data.KindTask, pm)
	return core.Next(a.openPicker(s, modalPaneAssociate, pm))
}

func (a *App) paneAssociatePicked(picked *picker.Item) {
	if a.associatePaneRef == nil {
		return
	}
	paneRef := *a.associatePaneRef
	a.associatePaneRef = nil
	taskRef, ok := a.resolveOrCreatePick(picked)
	if !ok || taskRef.Kind != data.KindTask || taskRef.ID == "" {
		a.say("no task selected")
		return
	}
	if _, _, err := a.Store.Patch(data.PatchInput{
		Ref:     paneRef,
		Fields:  map[string]any{"task": taskRef},
		Summary: "associate pane with " + a.title(taskRef),
	}, User); err != nil {
		a.fail("associate pane", err)
		return
	}
	linked, err := data.BackfillPaneTask(a.Store, paneRef.ID, taskRef.ID, User)
	if err != nil {
		a.fail("backfill pane records", err)
		return
	}
	a.updatePaneContextProject(paneRef.ID)
	msg := fmt.Sprintf("Associated %s with %s · %d record(s) linked", a.title(paneRef), a.title(taskRef), linked)
	a.say("%s", msg)
	a.pushToast(msg, toast.Ok)
}

func (a *App) updatePaneContextProject(paneID string) {
	if paneID == "" || a.ctxIdx == nil {
		return
	}
	pane, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: paneID})
	if err != nil || pane == nil {
		return
	}
	projTitle, projRef := a.resolvePaneProject(*pane)
	projID := ""
	if projRef != nil {
		projID = projRef.ID
	}
	a.ctxIdx.mu.Lock()
	for _, it := range a.ctxIdx.items {
		if it.PaneID == paneID {
			it.Project = projTitle
			it.ProjectID = projID
		}
	}
	a.ctxIdx.mu.Unlock()
}

func recordIntField(rec data.Record, name string) int {
	v, ok := rec.Value(name)
	if !ok || v == nil {
		return 0
	}
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		var n int
		_, _ = fmt.Sscan(strings.TrimSpace(x), &n)
		return n
	}
	return 0
}

// --- 6. extension.setup binding on 4 Extensions -----------------------------------

func extensionAdvertisesSetup(rec data.Record) bool {
	for _, field := range []string{"commands", "capabilities"} {
		raw, ok := rec.Value(field)
		if !ok || raw == nil {
			continue
		}
		switch list := raw.(type) {
		case []string:
			for _, item := range list {
				s := strings.ToLower(strings.TrimSpace(item))
				if s == "setup" || strings.Contains(s, "setup") {
					return true
				}
			}
		case []any:
			for _, item := range list {
				s := strings.ToLower(strings.TrimSpace(data.Text(item)))
				if s == "setup" || strings.Contains(s, "setup") {
					return true
				}
			}
		}
	}
	return false
}

func (a *App) extSetup(s core.State, c core.Command) core.StepResult {
	ref, ok := a.subject(s, c)
	if !ok || ref.Kind != data.KindExtension {
		a.say("no extension here to set up")
		return core.Next(s)
	}
	rec, err := a.Store.Get(ref)
	if err != nil {
		a.fail("extension setup", err)
		return core.Next(s)
	}
	name := rec.Text("name")
	if !extensionAdvertisesSetup(*rec) {
		msg := fmt.Sprintf("%s needs no setup (no setup command advertised)", name)
		a.say("%s", msg)
		a.pushToast(msg, toast.Info)
		return core.Next(s)
	}
	bin := strings.TrimSpace(rec.Text("binary"))
	if rec.Text("runStatus") == ext.StatusOffline || bin == "" {
		msg := fmt.Sprintf("%s is offline · R looks again", name)
		a.say("%s", msg)
		a.pushToast(msg, toast.Warn)
		return core.Next(s)
	}
	title := name + " setup"
	argv := []string{bin, "setup"}
	cmdLine := shellJoin(argv)
	if a.Runner != nil {
		runRec, err := a.Runner.Start(context.Background(), runs.StartSpec{
			Title:   title,
			Command: cmdLine,
			Args:    argv,
			Actor:   runActor,
		})
		s = a.started(s, "ran "+title, runRec, err)
		if err != nil {
			a.pushToast(title+" failed: "+err.Error(), toast.Err)
		} else {
			a.pushToast("Running "+title, toast.Ok)
		}
		return core.Next(s)
	}
	s = a.startShell(s, title, cmdLine, "")
	a.pushToast("Queued "+title, toast.Ok)
	return core.Next(s)
}

// --- 8. Dynamic "+ Create new <Title>: <Q>" across ALL record-reference pickers ---

const createRecordItemID = "__create_new_record__"

type refCreateSpec struct {
	kind      string
	baseItems []picker.Item
}

type createRecordPick struct {
	kind  string
	value string
}

func (a *App) enablePickerCreate(modalID, kind string, pm *picker.Model) {
	if modalID == "" || kind == "" || pm == nil {
		return
	}
	if a.pickerCreate == nil {
		a.pickerCreate = map[string]refCreateSpec{}
	}
	a.pickerCreate[modalID] = refCreateSpec{
		kind:      kind,
		baseItems: slices.Clone(pm.Items),
	}
}

func (a *App) syncPickerCreateItem(modalID string, pm *picker.Model) {
	if pm == nil || a.pickerCreate == nil {
		return
	}
	spec, ok := a.pickerCreate[modalID]
	if !ok || spec.kind == "" {
		return
	}
	q := strings.TrimSpace(pm.Query)
	items := slices.Clone(spec.baseItems)
	if q != "" {
		exactMatch := false
		for _, it := range spec.baseItems {
			if strings.EqualFold(strings.TrimSpace(it.Title), q) {
				exactMatch = true
				break
			}
		}
		if !exactMatch {
			schema := a.schema(spec.kind)
			nounTitle := schema.Title
			if nounTitle == "" {
				nounTitle = spec.kind
			}
			items = append(items, picker.Item{
				ID:       createRecordItemID,
				Title:    fmt.Sprintf("+ Create new %s: %q", nounTitle, q),
				Subtitle: "create " + strings.ToLower(nounTitle) + " and select it",
				Badges:   []ui.Badge{{Text: "new", Tone: "accent"}},
				Keywords: []string{q, nounTitle, spec.kind, "create", "new"},
				Data:     createRecordPick{kind: spec.kind, value: q},
			})
		}
	}
	pm.SetItems(items)
}

func (a *App) resolveOrCreatePick(picked *picker.Item) (core.Ref, bool) {
	if picked == nil {
		return core.Ref{}, false
	}
	if cp, ok := picked.Data.(createRecordPick); ok {
		schema := a.schema(cp.kind)
		field := schema.SummaryField
		if field == "" {
			field = "title"
		}
		rec, _, err := a.Store.Create(data.CreateInput{
			Kind:   cp.kind,
			Fields: map[string]any{field: cp.value},
		}, User)
		if err != nil {
			a.fail("create "+cp.kind, err)
			return core.Ref{}, false
		}
		nounTitle := schema.Title
		if nounTitle == "" {
			nounTitle = cp.kind
		}
		a.pushToast(fmt.Sprintf("Created %s %q", strings.ToLower(nounTitle), cp.value), toast.Ok)
		return rec.Ref(), true
	}
	if picked.Ref != nil {
		return *picked.Ref, true
	}
	return core.Ref{}, false
}
