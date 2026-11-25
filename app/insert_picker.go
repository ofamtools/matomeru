package app

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/pathpicker"
	"ofam.dev/ochazuke/widgets/picker"
	"ofam.dev/ochazuke/widgets/tree"
)

const (
	CmdInsertPickerFile   = "input.picker.file"
	CmdInsertPickerOpen   = "input.picker.open"
	CmdInsertPickerSwitch = "input.picker.switch"

	insertPickerModalID       = "insert.picker"
	insertPickerSwitchModalID = "insert.picker.switch"
)

// insertPickerSession binds a live insert picker (and any picker switched to
// via ctrl+l / alt+p) to the text field that opened it.
type insertPickerSession struct {
	cwd           string
	contextTaskID string
	insert        func(text string)
	onFileEntry   func(entry pathpicker.DirEntry) modal.Result
}

type insertSourceSpec struct {
	ID          string
	Title       string
	Subtitle    string
	Badge       string
	Mode        string   // "files", "context", "official", "skills", "commands", "agents", or "records"
	Kind        string   // data.Kind* when Mode == "records"
	Prefix      string   // fallback "@<kind>:" prefix when ValueFields is empty/unset
	ValueFields []string // record text fields checked before falling back to Prefix+ID
	BadgeField  string   // optional record field rendered as item badge
}

var insertSourceRegistry = []insertSourceSpec{
	{ID: "files", Title: "Files", Subtitle: "Browse files and directories (@path)", Badge: "Files", Mode: "files"},
	{ID: "context", Title: "Context", Subtitle: "Project and workspace context URLs, files, and refs", Badge: "Context", Mode: "context"},
	{ID: "official", Title: "Official Context", Subtitle: "Curated official project context items", Badge: "Official", Mode: "official"},
	{ID: "skills", Title: "Skills", Subtitle: "o-ai skills ($skill)", Badge: "Skills", Mode: "skills"},
	{ID: "commands", Title: "Slash Commands", Subtitle: "Built-in slash commands and skills (/cmd)", Badge: "Commands", Mode: "commands"},
	{ID: "agents", Title: "Agents", Subtitle: "Configured agent harnesses and catalog agents (@agent:id)", Badge: "Agents", Mode: "agents"},
	{ID: "tasks", Title: "Tasks", Subtitle: "Active tasks and projects (@task:id)", Badge: "Tasks", Mode: "records", Kind: data.KindTask, Prefix: "@task:", BadgeField: "workStatus"},
	{ID: "documents", Title: "Documents", Subtitle: "Active documents (file path or @document:id)", Badge: "Docs", Mode: "records", Kind: data.KindDocument, Prefix: "@document:", ValueFields: []string{"path", "filePath"}},
	{ID: "notes", Title: "Notes", Subtitle: "Active notes (@note:id)", Badge: "Notes", Mode: "records", Kind: data.KindNote, Prefix: "@note:", BadgeField: "authorKind"},
	{ID: "panes", Title: "Panes", Subtitle: "Active panes (@pane:id)", Badge: "Panes", Mode: "records", Kind: data.KindPane, Prefix: "@pane:", BadgeField: "harness"},
	{ID: "lanes", Title: "Lanes", Subtitle: "Active lanes (@lane:id)", Badge: "Lanes", Mode: "records", Kind: data.KindLane, Prefix: "@lane:", BadgeField: "laneStatus"},
	{ID: "recipes", Title: "Recipes", Subtitle: "Saved commands and recipes (command text)", Badge: "Recipes", Mode: "records", Kind: data.KindCommand, ValueFields: []string{"command"}},
}

func insertSourceIDs() []string {
	ids := make([]string, len(insertSourceRegistry))
	for i, sp := range insertSourceRegistry {
		ids[i] = sp.ID
	}
	return ids
}

func lookupInsertSource(id string) (insertSourceSpec, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, sp := range insertSourceRegistry {
		if sp.ID == id {
			return sp, true
		}
	}
	return insertSourceSpec{}, false
}

func insertPickerSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		{ID: CmdInsertPickerFile, Description: "Open the file insert picker for the active text field", Meta: map[string]any{"internal": true}},
		{
			ID:          CmdInsertPickerOpen,
			Description: "Open the universal insert picker chooser (or a named source) for the active text field",
			Params:      []core.ParamSpec{{Name: "source", Type: core.ParamString, Enum: insertSourceIDs(), Description: "Optional picker source to open directly"}},
			Meta:        map[string]any{"internal": true},
		},
		{ID: CmdInsertPickerSwitch, Description: "Switch the active insert picker to a different source while keeping the same target field", Meta: map[string]any{"internal": true}},
	}
}

func insertPickerBindings() []core.Binding {
	var out []core.Binding
	for _, mode := range []string{ModeOutlineEdit, ModeFormEdit} {
		out = append(out,
			bind(mode, "", "ctrl+f", CmdInsertPickerFile, "file picker"),
			bind(mode, "", "ctrl+l", CmdInsertPickerOpen, "insert picker"),
			hidden(bind(mode, "", "alt+p", CmdInsertPickerOpen, "insert picker")),
		)
	}
	out = append(out,
		bind(ModeComposer, "", "ctrl+l", CmdInsertPickerOpen, "insert picker"),
		hidden(bind(ModeComposer, "", "alt+p", CmdInsertPickerOpen, "insert picker")),
	)
	for _, mode := range []string{ModePicker, ModePickerFilter} {
		out = append(out,
			when(bind(mode, "", "ctrl+l", CmdInsertPickerSwitch, "switch picker"), "facts.insertPicker"),
			hidden(when(bind(mode, "", "alt+p", CmdInsertPickerSwitch, "switch picker"), "facts.insertPicker")),
		)
	}
	return out
}

func (a *App) registerInsertPickerHandlers() {
	a.handle(CmdInsertPickerFile, a.insertPickerFile)
	a.handle(CmdInsertPickerOpen, a.insertPickerOpen)
	a.handle(CmdInsertPickerSwitch, a.handleModal)
}

func (a *App) insertPickerFile(s core.State, _ core.Command) core.StepResult {
	return a.openInsertSourceOrChooser(s, "files", "no active text field for file picker")
}

func (a *App) insertPickerOpen(s core.State, c core.Command) core.StepResult {
	return a.openInsertSourceOrChooser(s, strings.TrimSpace(c.Arg("source")), "no active text field for insert picker")
}

func (a *App) openInsertSourceOrChooser(s core.State, src, emptyMsg string) core.StepResult {
	sess := a.resolveInsertTarget()
	if sess == nil {
		a.say("%s", emptyMsg)
		return core.Next(s)
	}
	if src != "" {
		mod, status, ok := a.buildInsertPickerModal(sess, src)
		if !ok {
			if _, known := lookupInsertSource(src); !known {
				a.say("unknown insert picker source %q", src)
			}
			return core.Next(s)
		}
		a.say("%s", status)
		return core.Next(a.pushModal(s, mod))
	}
	a.say("insert picker · choose a source · esc cancels")
	return core.Next(a.pushModal(s, a.newInsertSourceChooserModal(sess, "", insertPickerModalID, "insert.chooser", "Insert Picker")))
}

// openComposerAttachFilePicker routes Rich Composer ctrl+f through the shared
// insertPickerSession so file attachment preserves chip behavior while ctrl+l /
// alt+p live-switches to Context, Skills, Agents, Tasks, etc.
func (a *App) openComposerAttachFilePicker(cm *richComposerModal) modal.Result {
	if a == nil || cm == nil {
		return modal.Result{Handled: true}
	}
	mod, status, ok := a.buildInsertPickerModal(a.composerInsertSession(cm, true), "files")
	if !ok {
		return modal.Result{Handled: true}
	}
	return modal.Result{Handled: true, Push: mod, Status: status, SetStatus: true}
}

func (a *App) resolveInsertTarget() *insertPickerSession {
	if a == nil {
		return nil
	}
	if a.modals != nil {
		if cm, ok := a.modals.Top().(*richComposerModal); ok {
			return a.composerInsertSession(cm, false)
		}
	}
	if fm := a.topFormModal(); fm != nil && fm.Model() != nil && fm.Model().Editing {
		return a.formInsertSession(fm)
	}
	if tm, meta, ok := a.activeEditTree(); ok {
		return a.treeInsertSession(tm, meta)
	}
	if cm := a.activeRichComposer(); cm != nil {
		return a.composerInsertSession(cm, false)
	}
	return nil
}

func (a *App) treeInsertSession(tm *tree.Model, meta *treeEditMeta) *insertPickerSession {
	var taskID string
	if meta != nil && meta.kind == data.KindTask {
		if ed := tm.Edit(); ed != nil {
			taskID = firstNonEmpty(ed.TargetID, ed.ParentID)
		}
		if taskID == "" {
			if row, ok := tm.Focused(); ok && !row.Draft {
				taskID = row.ID
			}
		}
		taskID = firstNonEmpty(taskID, a.trees.projectIDForModel(tm))
	}
	cwd, contextTaskID := a.resolveTaskDirAndProject(taskID)
	return &insertPickerSession{
		cwd:           cwd,
		contextTaskID: contextTaskID,
		insert: func(text string) {
			if text != "" {
				tm.HandleEditCommand(core.Command{ID: input.CmdInsert, Args: map[string]any{"text": text}})
			}
		},
	}
}

func (a *App) composerInsertSession(cm *richComposerModal, attachFile bool) *insertPickerSession {
	cwd := cm.cwd
	if cwd == "" && a.opts.FS == nil {
		cwd, _ = os.Getwd()
	}
	sess := &insertPickerSession{
		cwd:           cwd,
		contextTaskID: cm.spec.ContextTaskID,
		insert: func(text string) {
			if text != "" && cm.in != nil {
				cm.in.HandleCommand(core.Command{ID: input.CmdInsert, Args: map[string]any{"text": text}})
				cm.updateCompletions()
			}
		},
	}
	if attachFile {
		sess.onFileEntry = func(entry pathpicker.DirEntry) modal.Result {
			if entry.IsDir {
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit, Status: "select a file to attach (not a directory)", SetStatus: true}
			}
			att, err := attachmentFromPath(entry.Path, "picker")
			if err != nil {
				kind, mimeType := attachmentKindAndMime(entry.Path)
				att = data.Attachment{Kind: kind, Path: entry.Path, Label: filepath.Base(entry.Path), Mime: mimeType, Source: "picker"}
			}
			cm.AddAttachment(att)
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit, Status: "attached " + att.Label, SetStatus: true}
		}
	}
	return sess
}

func (a *App) formInsertSession(fm *modal.FormModal) *insertPickerSession {
	var taskID string
	if a.modals != nil {
		if rf, ok := a.modals.Top().(*recordFormModal); ok {
			if rf.kind == data.KindTask && rf.ref != nil {
				taskID = rf.ref.ID
			} else if rf.about != nil && rf.about.Kind == data.KindTask {
				taskID = rf.about.ID
			}
		}
	}
	cwd, contextTaskID := a.resolveTaskDirAndProject(taskID)
	return &insertPickerSession{
		cwd:           cwd,
		contextTaskID: contextTaskID,
		insert: func(text string) {
			if text != "" && fm != nil && fm.Model() != nil {
				fm.Model().HandleCommand(core.Command{ID: form.CmdInsert, Args: map[string]any{"text": text}})
			}
		},
	}
}

func (a *App) resolveTaskDirAndProject(taskID string) (cwd, projectID string) {
	if a != nil && a.Store != nil && strings.TrimSpace(taskID) != "" {
		if rec, err := a.Store.Get(core.Ref{Kind: data.KindTask, ID: taskID}); err == nil && rec != nil {
			cwd = expandHome(strings.TrimSpace(rec.Text("defaultDir")))
		}
		projectID = firstNonEmpty(a.Store.RootProjectID(taskID), taskID)
		if cwd == "" && projectID != taskID {
			if rootRec, err := a.Store.Get(core.Ref{Kind: data.KindTask, ID: projectID}); err == nil && rootRec != nil {
				cwd = expandHome(strings.TrimSpace(rootRec.Text("defaultDir")))
			}
		}
	}
	if cwd == "" && (a == nil || a.opts.FS == nil) {
		cwd, _ = os.Getwd()
	}
	return cwd, projectID
}

func insertPickerBadges(label string) []ui.Badge {
	return []ui.Badge{{Text: label, Tone: "accent"}, {Text: "ctrl+l / alt+p switch", Tone: "muted"}}
}

func insertPickerFacts(sourceID string) map[string]any {
	return map[string]any{"insertPicker": true, "insertSource": sourceID}
}

func (a *App) buildInsertPickerModal(sess *insertPickerSession, sourceID string) (modal.Modal, string, bool) {
	spec, ok := lookupInsertSource(sourceID)
	if !ok {
		return nil, "", false
	}
	if spec.Mode == "files" {
		return a.buildFileInsertPicker(sess)
	}
	return a.buildListInsertPicker(sess, spec)
}

func (a *App) openInsertPickerSwitcher(sess *insertPickerSession, currentSource string) modal.Result {
	sw := a.newInsertSourceChooserModal(sess, currentSource, insertPickerSwitchModalID, "insert.switch", "Switch Picker")
	return modal.Result{Handled: true, Push: sw, Status: "switch picker · enter switches source · esc returns to current picker", SetStatus: true}
}

func (a *App) newInsertSourceChooserModal(sess *insertPickerSession, currentSource, modalID, scope, title string) *modal.PickerModal {
	items := make([]picker.Item, len(insertSourceRegistry))
	selected := 0
	for i, sp := range insertSourceRegistry {
		itemTitle, badges := sp.Title, []ui.Badge{{Text: sp.Badge, Tone: "muted"}}
		if currentSource != "" && sp.ID == currentSource {
			itemTitle, badges, selected = sp.Title+" (current)", []ui.Badge{{Text: "current", Tone: "accent"}}, i
		}
		items[i] = picker.Item{ID: sp.ID, Title: itemTitle, Subtitle: sp.Subtitle, Badges: badges, Keywords: []string{sp.ID, sp.Title, sp.Badge, sp.Subtitle}}
	}
	pm := picker.New(title, items)
	pm.SelectedIdx = selected
	if currentSource != "" {
		pm.Placeholder = "switch picker source · enter replaces picker · esc returns"
	} else {
		pm.Placeholder = "choose insert picker source · enter opens · esc cancels"
	}
	return a.newPickerModal(pm, modal.PickerOptions{
		ID: modalID, Scope: scope, Title: title, PageSize: 12,
		OnConfirm: func(picked picker.Item) modal.Result {
			if currentSource != "" && picked.ID == currentSource {
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
			}
			nextMod, status, ok := a.buildInsertPickerModal(sess, picked.ID)
			if !ok {
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseCancel, Status: "could not open " + picked.Title, SetStatus: true}
			}
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit, Push: nextMod, Status: status, SetStatus: true}
		},
	})
}

func (a *App) buildFileInsertPicker(sess *insertPickerSession) (modal.Modal, string, bool) {
	fs := a.opts.FS
	if fs == nil {
		fs = pathpicker.OSFS{}
	}
	home, _ := fs.HomeDir()
	var tries []string
	if dir := sess.cwd; dir != "" {
		if rest, ok := strings.CutPrefix(dir, "~"); ok && home != "" {
			dir = filepath.Join(home, rest)
		}
		if filepath.IsAbs(dir) {
			tries = append(tries, dir, filepath.Dir(dir))
		}
	}
	if a.opts.FS == nil {
		if cwd, err := os.Getwd(); err == nil {
			tries = append(tries, cwd)
		}
	}
	var pp *pathpicker.Model
	for _, dir := range append(tries, home, string(filepath.Separator)) {
		if dir == "" {
			continue
		}
		if p, err := pathpicker.New(fs, dir, pathpicker.PickAny); err == nil {
			pp = p
			break
		}
	}
	if pp == nil {
		a.say("no directory could be read")
		return nil, "", false
	}
	mod := modal.NewPathPickerModal(pp, modal.PathPickerOptions{
		ID: insertPickerModalID, Scope: "path", Mode: ModePicker, FilterMode: ModePickerFilter,
		StartInFilter: a.pickerStartInFilter(false), FocusID: insertPickerModalID, WidthPct: 72, HeightPct: 72,
		Badges: func(_ *pathpicker.Model) []ui.Badge { return insertPickerBadges("Files") },
		Facts:  func(_ *pathpicker.Model) map[string]any { return insertPickerFacts("files") },
		OnCommand: func(_ *pathpicker.Model, cmd core.Command) (modal.Result, bool) {
			if cmd.ID == CmdInsertPickerSwitch {
				return a.openInsertPickerSwitcher(sess, "files"), true
			}
			return modal.Result{}, false
		},
		OnPick: func(entry pathpicker.DirEntry) modal.Result {
			if sess.onFileEntry != nil {
				res := sess.onFileEntry(entry)
				res.Handled, res.Close, res.CloseReason = true, true, modal.CloseSubmit
				return res
			}
			formatted := formatInsertedPath(sess.cwd, entry.Path)
			if sess.insert != nil {
				sess.insert(formatted)
			}
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit, Status: "inserted " + formatted, SetStatus: true}
		},
	})
	status := "insert file · " + pathPickerStatus + " · ctrl+l / alt+p switch"
	if sess.onFileEntry != nil {
		status = "pick a file to attach · " + pathPickerStatus + " · ctrl+l / alt+p switch"
	}
	return mod, status, true
}

func (a *App) buildListInsertPicker(sess *insertPickerSession, spec insertSourceSpec) (modal.Modal, string, bool) {
	pm := picker.New("Insert · "+spec.Title, a.insertItemsForSource(sess, spec))
	pm.Placeholder = fmt.Sprintf("filter %s · enter inserts · ctrl+l / alt+p switch", strings.ToLower(spec.Title))
	mod := a.newPickerModal(pm, modal.PickerOptions{
		ID: insertPickerModalID, Scope: "insert." + spec.ID, Title: "Insert · " + spec.Title, PageSize: 10,
		Badges: func(_ *picker.Model) []ui.Badge { return insertPickerBadges(spec.Badge) },
		Facts:  func(_ *picker.Model) map[string]any { return insertPickerFacts(spec.ID) },
		OnCommand: func(_ *picker.Model, cmd core.Command) (modal.Result, bool) {
			if cmd.ID == CmdInsertPickerSwitch {
				return a.openInsertPickerSwitcher(sess, spec.ID), true
			}
			return modal.Result{}, false
		},
		OnConfirm: func(picked picker.Item) modal.Result {
			val, _ := picked.Data.(string)
			val = firstNonEmpty(val, picked.Title)
			if sess.insert != nil {
				sess.insert(val)
			}
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit, Status: "inserted " + clip(strings.TrimSpace(val), 48), SetStatus: true}
		},
	})
	status := fmt.Sprintf("insert %s · enter inserts · ctrl+l / alt+p switch · esc cancels", strings.ToLower(spec.Title))
	return mod, status, true
}

func formatInsertedPath(cwd, fullPath string) string {
	if fullPath == "" {
		return ""
	}
	if cwd != "" {
		if rel, err := filepath.Rel(cwd, fullPath); err == nil && rel != "" && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "@" + filepath.ToSlash(rel)
		}
	}
	return "@" + filepath.ToSlash(fullPath)
}

func (a *App) insertItemsForSource(sess *insertPickerSession, spec insertSourceSpec) []picker.Item {
	switch spec.Mode {
	case "context":
		return a.insertContextItems(sess, false)
	case "official":
		return a.insertContextItems(sess, true)
	case "skills":
		a.ensureAICatalogAsync()
		return completionPickerItems(a.cachedSkillCompletions("$"), "$", "skill", "accent")
	case "commands":
		a.ensureAICatalogAsync()
		return completionPickerItems(a.cachedSkillCompletions("/"), "/", "command", "info")
	case "agents":
		a.ensureAICatalogAsync()
		return completionPickerItems(a.agentHarnessCompletions(), "@agent:", "agent", "accent")
	case "records":
		return a.insertRecordItems(spec)
	default:
		return nil
	}
}

func (a *App) insertContextItems(sess *insertPickerSession, officialOnly bool) []picker.Item {
	if a == nil || a.Store == nil {
		return nil
	}
	var ordered []data.Record
	seen := map[string]bool{}
	if sess != nil && sess.contextTaskID != "" {
		projectID := firstNonEmpty(a.Store.RootProjectID(sess.contextTaskID), sess.contextTaskID)
		var scoped []data.Record
		if officialOnly {
			scoped, _ = a.Store.OfficialProjectContext(projectID)
		} else {
			scoped, _ = a.Store.ProjectContextAll(projectID)
		}
		for _, rec := range scoped {
			if !seen[rec.ID] {
				seen[rec.ID] = true
				ordered = append(ordered, rec)
			}
		}
	}
	if !officialOnly || len(ordered) == 0 {
		if res, err := a.Store.Query(data.Query{Kind: data.KindContext}); err == nil {
			var rest []data.Record
			for _, rec := range res.Items {
				if seen[rec.ID] || (officialOnly && !rec.Bool("official")) {
					continue
				}
				seen[rec.ID] = true
				rest = append(rest, rec)
			}
			slices.SortStableFunc(rest, compareContextOfficialFirst)
			ordered = append(ordered, rest...)
		}
	}
	items := make([]picker.Item, len(ordered))
	for i, rec := range ordered {
		title, kind, val := contextRecordDisplayTitle(rec), contextRecordKind(rec), strings.TrimSpace(rec.Text("value"))
		insert := firstNonEmpty(val, strings.TrimSpace(rec.Text("title")), rec.ID)
		label, keywords := fmt.Sprintf("%s (%s)", title, kind), []string{title, val, kind, rec.Text("notes"), rec.ID}
		var badges []ui.Badge
		if rec.Bool("official") {
			label = "★ " + label
			badges = append(badges, ui.Badge{Text: "official", Tone: "accent"})
			keywords = append(keywords, "official")
		}
		if kind != "" {
			badges = append(badges, ui.Badge{Text: kind, Tone: "info"})
		}
		items[i] = picker.Item{ID: rec.ID, Title: label, Subtitle: val, Badges: badges, Keywords: keywords, Data: insert}
	}
	return items
}

func compareContextOfficialFirst(x, y data.Record) int {
	for _, p := range [][2]bool{{x.Bool("official"), y.Bool("official")}, {x.Bool("pinned"), y.Bool("pinned")}, {x.IsStarred(), y.IsStarred()}} {
		if p[0] != p[1] {
			if p[0] {
				return -1
			}
			return 1
		}
	}
	return y.UpdatedAt.Compare(x.UpdatedAt)
}

func completionPickerItems(list []composerCompletion, prefix, badge, tone string) []picker.Item {
	items := make([]picker.Item, 0, len(list))
	for _, c := range list {
		raw := strings.TrimPrefix(strings.TrimLeft(c.Insert, "$/@"), "agent:")
		if raw == "" {
			continue
		}
		tok := prefix + raw
		items = append(items, picker.Item{ID: badge + ":" + raw, Title: tok, Subtitle: c.Detail, Badges: []ui.Badge{{Text: badge, Tone: tone}}, Keywords: []string{raw, c.Detail, badge}, Data: tok + " "})
	}
	return items
}

func (a *App) insertRecordItems(spec insertSourceSpec) []picker.Item {
	if a == nil || a.Store == nil {
		return nil
	}
	res, err := a.Store.Query(data.Query{Kind: spec.Kind})
	if err != nil {
		return nil
	}
	schema := a.schema(spec.Kind)
	items := make([]picker.Item, len(res.Items))
	for i, rec := range res.Items {
		title := firstNonEmpty(views.Title(schema, rec), clip(strings.TrimSpace(rec.Text("body")), 48), rec.ID)
		var val string
		for _, f := range spec.ValueFields {
			if val = strings.TrimSpace(rec.Text(f)); val != "" {
				break
			}
		}
		if val == "" {
			val = title
			if spec.Prefix != "" {
				val = spec.Prefix + rec.ID
			}
		}
		var badges []ui.Badge
		if spec.BadgeField != "" {
			if b := strings.TrimSpace(rec.Text(spec.BadgeField)); b != "" {
				badges = append(badges, ui.Badge{Text: b, Tone: "muted"})
			}
		}
		items[i] = picker.Item{ID: rec.ID, Title: title, Subtitle: val, Badges: badges, Keywords: []string{rec.ID, title, val}, Data: val}
	}
	return items
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
