package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/toast"
)

// Modes, modal IDs, and commands for the read-only Vim code viewer.
const (
	ModeCodeView       = "code-view"
	ModeCodeViewSearch = "code-view-search"

	codeViewerModalID     = "code.viewer"
	codeAnnotateModalID   = "code.annotate.form"
	codeCustomizeModalID  = "code.customize.form"
	codeAnnotationKindVal = "code_hunk"
	codeSourceStyleKind   = "code_source_style"

	CmdCodeOpen            = "code.open"
	CmdCodeBack            = "code.back"
	CmdCodeMotionWordNext  = "code.motion.word.next"
	CmdCodeMotionWordPrev  = "code.motion.word.prev"
	CmdCodeMotionWordEnd   = "code.motion.word.end"
	CmdCodeMotionLineStart = "code.motion.line.start"
	CmdCodeMotionLineFirst = "code.motion.line.first"
	CmdCodeMotionLineEnd   = "code.motion.line.end"
	CmdCodeVisualToggle    = "code.visual.toggle"
	CmdCodeVisualClear     = "code.visual.clear"
	CmdCodeAnnotate        = "code.annotate"
	CmdCodeHunkCustomize   = "code.hunk.customize"
	CmdCodeHunkToTask      = "code.hunk.toTask"
	CmdCodeHunkNext        = "code.hunk.next"
	CmdCodeHunkPrev        = "code.hunk.prev"
	CmdCodeYank            = "code.yank"
	CmdCodeSearchStart     = "code.search.start"
	CmdCodeSearchInsert    = "code.search.insert"
	CmdCodeSearchBackspace = "code.search.backspace"
	CmdCodeSearchCommit    = "code.search.commit"
	CmdCodeSearchCancel    = "code.search.cancel"
	CmdCodeSearchNext      = "code.search.next"
	CmdCodeSearchPrev      = "code.search.prev"
)

// CodeOpenRequest describes a file and optional source line highlight to open
// in the read-only Vim code viewer.
type CodeOpenRequest struct {
	File      string
	StartLine int
	EndLine   int
	Title     string
	Label     string
	Note      string
	Color     string
	SourceRef *core.Ref
	TaskRef   *core.Ref
}

func codeViewerSpecs() []core.CommandSpec {
	text := []core.ParamSpec{{Name: "text", Type: core.ParamString}}
	return []core.CommandSpec{
		{
			ID:          CmdCodeOpen,
			Description: "Open a file in the read-only Vim code viewer with optional highlighted source lines",
			Params: []core.ParamSpec{
				{Name: "file", Type: core.ParamPath, Description: "File path to open (defaults to focused item's code location)"},
				{Name: "startLine", Type: core.ParamInt, Description: "Optional 1-based start line to highlight"},
				{Name: "endLine", Type: core.ParamInt, Description: "Optional 1-based end line to highlight"},
				{Name: "title", Type: core.ParamString, Description: "Optional viewer title override"},
				{Name: "label", Type: core.ParamString, Description: "Optional label for the source highlight"},
				{Name: "note", Type: core.ParamString, Description: "Optional context note for the source highlight"},
				{Name: "color", Type: core.ParamString, Description: "Optional highlight color preset or #RRGGBB"},
			},
		},
		spec(CmdCodeBack, "Close the code viewer and return to the previous page"),
		{ID: CmdCodeMotionWordNext, Description: "Move forward to the next word in the code viewer", Meta: map[string]any{"internal": true}},
		{ID: CmdCodeMotionWordPrev, Description: "Move backward to the previous word in the code viewer", Meta: map[string]any{"internal": true}},
		{ID: CmdCodeMotionWordEnd, Description: "Move to the end of the word in the code viewer", Meta: map[string]any{"internal": true}},
		{ID: CmdCodeMotionLineStart, Description: "Move to column 0 of the current line in the code viewer", Meta: map[string]any{"internal": true}},
		{ID: CmdCodeMotionLineFirst, Description: "Move to the first non-blank character on the line", Meta: map[string]any{"internal": true}},
		{ID: CmdCodeMotionLineEnd, Description: "Move to the end of the current line in the code viewer", Meta: map[string]any{"internal": true}},
		{
			ID:          CmdCodeVisualToggle,
			Description: "Toggle visual character or line selection in the code viewer",
			Params:      []core.ParamSpec{{Name: "mode", Type: core.ParamString, Enum: []string{views.CodeVisualChar, views.CodeVisualLine}, Default: views.CodeVisualChar}},
			Meta:        map[string]any{"internal": true},
		},
		{ID: CmdCodeVisualClear, Description: "Clear visual selection in the code viewer", Meta: map[string]any{"internal": true}},
		spec(CmdCodeAnnotate, "Add a durable code annotation on the selected range or current line", "editing"),
		spec(CmdCodeHunkCustomize, "Customize the highlight color, label, and note of the hunk at cursor", "editing"),
		spec(CmdCodeHunkToTask, "Promote the highlighted code hunk at cursor into a to-do task", "editing"),
		{ID: CmdCodeHunkNext, Description: "Jump to the next highlighted hunk in the file", Meta: map[string]any{"internal": true}},
		{ID: CmdCodeHunkPrev, Description: "Jump to the previous highlighted hunk in the file", Meta: map[string]any{"internal": true}},
		spec(CmdCodeYank, "Yank the visual selection or current line from the code viewer"),
		{ID: CmdCodeSearchStart, Description: "Start searching in the code viewer", Meta: map[string]any{"internal": true}},
		{ID: CmdCodeSearchInsert, Description: "Type into the code viewer search bar", Params: text, Meta: map[string]any{"internal": true}},
		{ID: CmdCodeSearchBackspace, Description: "Erase in the code viewer search bar", Meta: map[string]any{"internal": true}},
		{ID: CmdCodeSearchCommit, Description: "Commit search and jump to match in the code viewer", Meta: map[string]any{"internal": true}},
		{ID: CmdCodeSearchCancel, Description: "Cancel search input in the code viewer", Meta: map[string]any{"internal": true}},
		{ID: CmdCodeSearchNext, Description: "Jump to next search match in the code viewer", Meta: map[string]any{"internal": true}},
		{ID: CmdCodeSearchPrev, Description: "Jump to previous search match in the code viewer", Meta: map[string]any{"internal": true}},
	}
}

func codeViewerBindings() []core.Binding {
	m, sm, all := ModeCodeView, ModeCodeViewSearch, ""
	return []core.Binding{
		when(bind(ModeNormal, all, "g k", CmdCodeOpen, "view in code"), "facts.hasCodeLocation"),

		// Numeric counts so 5j, 12G, 3w work like real Vim.
		with(hidden(bind(m, all, "1", core.CmdCountPush, "count")), map[string]any{"digit": 1}),
		with(hidden(bind(m, all, "2", core.CmdCountPush, "count")), map[string]any{"digit": 2}),
		with(hidden(bind(m, all, "3", core.CmdCountPush, "count")), map[string]any{"digit": 3}),
		with(hidden(bind(m, all, "4", core.CmdCountPush, "count")), map[string]any{"digit": 4}),
		with(hidden(bind(m, all, "5", core.CmdCountPush, "count")), map[string]any{"digit": 5}),
		with(hidden(bind(m, all, "6", core.CmdCountPush, "count")), map[string]any{"digit": 6}),
		with(hidden(bind(m, all, "7", core.CmdCountPush, "count")), map[string]any{"digit": 7}),
		with(hidden(bind(m, all, "8", core.CmdCountPush, "count")), map[string]any{"digit": 8}),
		with(hidden(bind(m, all, "9", core.CmdCountPush, "count")), map[string]any{"digit": 9}),
		when(with(hidden(bind(m, all, "0", core.CmdCountPush, "count")), map[string]any{"digit": 0}), "count != null"),

		// Vim motions.
		bind(m, all, "j", "motion.down", "down"),
		bind(m, all, "k", "motion.up", "up"),
		hidden(bind(m, all, "down", "motion.down", "down")),
		hidden(bind(m, all, "up", "motion.up", "up")),
		bind(m, all, "h", "motion.left", "left"),
		bind(m, all, "l", "motion.right", "right"),
		hidden(bind(m, all, "left", "motion.left", "left")),
		hidden(bind(m, all, "right", "motion.right", "right")),
		hidden(bind(m, all, "w", CmdCodeMotionWordNext, "next word")),
		hidden(bind(m, all, "b", CmdCodeMotionWordPrev, "prev word")),
		hidden(bind(m, all, "e", CmdCodeMotionWordEnd, "word end")),
		when(hidden(bind(m, all, "0", CmdCodeMotionLineStart, "line start")), "count == null"),
		hidden(bind(m, all, "^", CmdCodeMotionLineFirst, "first non-blank")),
		hidden(bind(m, all, "$", CmdCodeMotionLineEnd, "line end")),
		bind(m, all, "g g", "motion.top", "top"),
		bind(m, all, "G", "motion.bottom", "bottom"),
		bind(m, all, "ctrl+d", "motion.half.down", "half page down"),
		bind(m, all, "ctrl+u", "motion.half.up", "half page up"),
		bind(m, all, "ctrl+f", "motion.page.down", "page down"),
		bind(m, all, "ctrl+b", "motion.page.up", "page up"),
		bind(m, all, "ctrl+e", "motion.scroll.down", "scroll down"),
		bind(m, all, "ctrl+y", "motion.scroll.up", "scroll up"),
		bind(m, all, "] h", CmdCodeHunkNext, "next highlight"),
		bind(m, all, "[ h", CmdCodeHunkPrev, "prev highlight"),

		// Visual selection, annotation, customization, task promotion, yank.
		with(bind(m, all, "v", CmdCodeVisualToggle, "visual select"), map[string]any{"mode": views.CodeVisualChar}),
		with(bind(m, all, "V", CmdCodeVisualToggle, "visual line"), map[string]any{"mode": views.CodeVisualLine}),
		bind(m, all, "a", CmdCodeAnnotate, "annotate"),
		when(bind(m, all, "n", CmdCodeSearchNext, "next match"), "facts.hasSearch"),
		when(bind(m, all, "n", CmdCodeAnnotate, "annotate"), "!facts.hasSearch"),
		when(bind(m, all, "N", CmdCodeSearchPrev, "prev match"), "facts.hasSearch"),
		when(bind(m, all, "c", CmdCodeHunkCustomize, "customize highlight"), "facts.hasHighlightAtCursor"),
		when(bind(m, all, "T", CmdCodeHunkToTask, "promote to task"), "facts.hasHighlightAtCursor"),
		bind(m, all, "y", CmdCodeYank, "yank"),
		bind(m, all, "/", CmdCodeSearchStart, "search"),

		// Back navigation: ctrl+o, q, backspace, and esc (when not clearing visual).
		bind(m, all, "ctrl+o", CmdCodeBack, "back"),
		bind(m, all, "q", CmdCodeBack, "back"),
		bind(m, all, "backspace", CmdCodeBack, "back"),
		when(bind(m, all, "esc", CmdCodeVisualClear, "clear visual"), "facts.visualActive"),
		when(bind(m, all, "esc", CmdCodeBack, "back"), "!facts.visualActive"),

		// Search input mode.
		{Mode: sm, Class: core.ClassPrintable, Command: CmdCodeSearchInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		{Mode: sm, Class: core.ClassPaste, Command: CmdCodeSearchInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		bind(sm, all, "backspace", CmdCodeSearchBackspace, "erase"),
		hidden(bind(sm, all, "shift+backspace", CmdCodeSearchBackspace, "erase")),
		bind(sm, all, "enter", CmdCodeSearchCommit, "search"),
		bind(sm, all, "esc", CmdCodeSearchCancel, "cancel"),
	}
}

func (a *App) registerCodeViewerHandlers() {
	a.handle(CmdCodeOpen, a.codeOpenCmd)
	for _, id := range []string{
		CmdCodeBack,
		CmdCodeMotionWordNext, CmdCodeMotionWordPrev, CmdCodeMotionWordEnd,
		CmdCodeMotionLineStart, CmdCodeMotionLineFirst, CmdCodeMotionLineEnd,
		CmdCodeVisualToggle, CmdCodeVisualClear,
		CmdCodeAnnotate, CmdCodeHunkCustomize, CmdCodeHunkToTask,
		CmdCodeHunkNext, CmdCodeHunkPrev, CmdCodeYank,
		CmdCodeSearchStart, CmdCodeSearchInsert, CmdCodeSearchBackspace,
		CmdCodeSearchCommit, CmdCodeSearchCancel, CmdCodeSearchNext, CmdCodeSearchPrev,
	} {
		a.handle(id, a.handleModal)
	}
}

func (a *App) activeCodeViewer() *codeViewerModal {
	if a.modals == nil {
		return nil
	}
	m, _ := a.modals.Get(codeViewerModalID).(*codeViewerModal)
	return m
}

func (a *App) codeOpenCmd(s core.State, c core.Command) core.StepResult {
	req, ok := a.resolveCodeOpenRequest(s, c)
	if !ok {
		a.say("code.open needs a file path")
		return core.Next(s)
	}
	if ref, ok := a.subject(s, c); ok && ref.ID != "" {
		r := ref
		req.SourceRef = &r
	}
	return core.Next(a.openCodeViewer(s, req))
}

func (a *App) featuresViewInCodeCmd(s core.State, c core.Command) core.StepResult {
	return a.codeOpenCmd(s, c)
}

func (a *App) resolveCodeOpenRequest(s core.State, c core.Command) (CodeOpenRequest, bool) {
	if file := strings.TrimSpace(c.Arg("file")); file != "" {
		return CodeOpenRequest{
			File:      a.resolveCodeTargetPath(file),
			StartLine: intArg(c.Args["startLine"]),
			EndLine:   intArg(c.Args["endLine"]),
			Title:     strings.TrimSpace(c.Arg("title")),
			Label:     strings.TrimSpace(c.Arg("label")),
			Note:      strings.TrimSpace(c.Arg("note")),
			Color:     strings.TrimSpace(c.Arg("color")),
		}, true
	}
	facts := a.focusedCodeFacts(s)
	if facts == nil {
		return CodeOpenRequest{}, false
	}
	file, _ := facts["codeFile"].(string)
	file = strings.TrimSpace(file)
	if file == "" {
		return CodeOpenRequest{}, false
	}
	title, _ := facts["codeTitle"].(string)
	sym, _ := facts["codeSymbol"].(string)
	note, _ := facts["codeSummary"].(string)
	return CodeOpenRequest{
		File:      a.resolveCodeTargetPath(file),
		StartLine: intArg(facts["codeStartLine"]),
		EndLine:   intArg(facts["codeEndLine"]),
		Title:     strings.TrimSpace(title),
		Label:     firstNonEmpty(strings.TrimSpace(sym), strings.TrimSpace(title)),
		Note:      strings.TrimSpace(note),
	}, true
}

func (a *App) focusedCodeFacts(s core.State) map[string]any {
	focus := s.ActiveFrame().FocusID
	if focus == "" {
		return nil
	}
	root := a.View(s)
	node := root.Find(focus)
	if node == nil {
		return nil
	}
	return node.Facts
}

func (a *App) resolveCodeTargetPath(file string) string {
	clean := cleanLocalTargetPath(strings.TrimSpace(file))
	if clean == "" {
		return ""
	}
	expanded := expandHome(clean)
	if filepath.IsAbs(expanded) {
		return filepath.Clean(expanded)
	}
	if _, repo := a.detectMatomeruCheckout(); repo != "" {
		return filepath.Join(repo, filepath.FromSlash(expanded))
	}
	return filepath.Clean(expanded)
}

func normalizeCodePath(path string) string {
	cleaned := cleanLocalTargetPath(strings.TrimSpace(path))
	if cleaned == "" {
		return ""
	}
	return filepath.Clean(expandHome(cleaned))
}

// openCodeViewer reads req.File, hydrates saved annotations and any saved
// source-highlight style customization from the SQLite store, and pushes the
// read-only Vim code viewer overlay onto the modal stack.
func (a *App) openCodeViewer(s core.State, req CodeOpenRequest) core.State {
	normPath := normalizeCodePath(req.File)
	if normPath == "" {
		a.say("code viewer: empty file path")
		return s
	}
	content, err := a.readFile(normPath)
	if err != nil {
		a.fail("read "+normPath, err)
		return s
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = filepath.Base(normPath)
	}
	highlights := a.loadCodeHighlights(normPath, req)
	st := views.NewCodeViewerState(normPath, title, content, highlights)
	st.Back = views.CodeBackTarget{
		Page:    s.ActivePage,
		FocusID: s.ActiveFrame().FocusID,
		Title:   s.ActivePage,
	}
	mod := &codeViewerModal{
		a:         a,
		state:     st,
		mode:      ModeCodeView,
		sourceRef: req.SourceRef,
		taskRef:   a.resolveCodeTaskRef(req),
	}
	a.say("%s · vim motions · v/V select · a annotate · c customize · T to-do · ctrl+o/q back", title)
	return a.pushModal(s, mod)
}

func (a *App) resolveCodeTaskRef(req CodeOpenRequest) *core.Ref {
	if req.TaskRef != nil && req.TaskRef.ID != "" {
		cp := *req.TaskRef
		return &cp
	}
	if req.SourceRef == nil || a.Store == nil {
		return nil
	}
	rec, err := a.Store.Get(*req.SourceRef)
	if err != nil || rec == nil {
		return nil
	}
	if t, ok := rec.RefField("task"); ok && t.ID != "" {
		return &t
	}
	return nil
}

func (a *App) loadCodeHighlights(normPath string, req CodeOpenRequest) []views.CodeHighlight {
	var out []views.CodeHighlight
	var savedSource *views.CodeHighlight
	saved := a.querySavedCodeNotes(normPath)
	for _, h := range saved {
		if h.Kind == views.CodeHighlightSource {
			cp := h
			if matchesSourceBounds(cp, req.StartLine, req.EndLine) {
				savedSource = &cp
			}
			continue
		}
		out = append(out, h)
	}
	if req.StartLine > 0 {
		src := buildSourceHighlight(normPath, req, savedSource)
		out = append([]views.CodeHighlight{src}, out...)
	}
	return out
}

func matchesSourceBounds(h views.CodeHighlight, startLine, endLine int) bool {
	if startLine <= 0 {
		return false
	}
	if endLine <= 0 {
		endLine = startLine
	}
	s, e := h.NormalizedBounds()
	return s == startLine && e == endLine
}

func buildSourceHighlight(normPath string, req CodeOpenRequest, saved *views.CodeHighlight) views.CodeHighlight {
	endLine := req.EndLine
	if endLine <= 0 {
		endLine = req.StartLine
	}
	h := views.CodeHighlight{
		ID:        fmt.Sprintf("source:%s:%d-%d", normPath, req.StartLine, endLine),
		Kind:      views.CodeHighlightSource,
		StartLine: req.StartLine,
		EndLine:   endLine,
		Color:     firstNonEmpty(req.Color, views.DefaultSourceHighlightColor),
		Label:     firstNonEmpty(req.Label, "Referenced lines"),
		Note:      req.Note,
	}
	if saved != nil {
		h.RecordID = saved.RecordID
		if saved.Color != "" {
			h.Color = saved.Color
		}
		if saved.Label != "" {
			h.Label = saved.Label
		}
		if saved.Note != "" {
			h.Note = saved.Note
		}
	}
	return h
}

func (a *App) querySavedCodeNotes(normPath string) []views.CodeHighlight {
	if a.Store == nil {
		return nil
	}
	res, err := a.Store.Query(data.Query{Kind: data.KindNote})
	if err != nil {
		return nil
	}
	var out []views.CodeHighlight
	for _, rec := range res.Items {
		if rec.IsArchived() || normalizeCodePath(rec.Text("filePath")) != normPath {
			continue
		}
		if h, ok := highlightFromNoteRecord(rec); ok {
			out = append(out, h)
		}
	}
	return out
}

func highlightFromNoteRecord(rec data.Record) (views.CodeHighlight, bool) {
	ak := rec.Text("annotationKind")
	kind := ""
	switch ak {
	case codeAnnotationKindVal:
		kind = views.CodeHighlightAnnotation
	case codeSourceStyleKind:
		kind = views.CodeHighlightSource
	default:
		return views.CodeHighlight{}, false
	}
	startLine := recordIntField(rec, "startLine")
	endLine := recordIntField(rec, "endLine")
	if startLine <= 0 {
		return views.CodeHighlight{}, false
	}
	if endLine <= 0 {
		endLine = startLine
	}
	color := firstNonEmpty(rec.Text("highlightColor"), views.DefaultColorForKind(kind))
	label := firstNonEmpty(rec.Text("annotationLabel"), rec.Text("title"))
	return views.CodeHighlight{
		ID:        "ann:" + rec.ID,
		Kind:      kind,
		StartLine: startLine,
		EndLine:   endLine,
		Color:     color,
		Label:     label,
		Note:      rec.Text("body"),
		RecordID:  rec.ID,
	}, true
}

// --- Modal Implementation -------------------------------------------------------

type codeViewerModal struct {
	a         *App
	state     *views.CodeViewerState
	mode      string
	sourceRef *core.Ref
	taskRef   *core.Ref
}

func (m *codeViewerModal) ID() string      { return codeViewerModalID }
func (m *codeViewerModal) Scope() string   { return views.ScopeCodeView }
func (m *codeViewerModal) FocusID() string { return views.CodeViewerBodyID }
func (m *codeViewerModal) Mode() string    { return m.mode }
func (m *codeViewerModal) OnClose(_ modal.CloseReason) {
	if m.state != nil {
		m.state.ClearVisual()
		m.state.CancelSearch()
	}
}

func (m *codeViewerModal) View(w, h int) ui.ViewNode {
	if w <= 0 {
		w = 120
	}
	if h <= 0 {
		h = 36
	}
	innerW := max(w*94/100-4, 40)
	innerH := max(h*92/100-4, 12)
	body := views.CodeViewerView(m.state, innerW, innerH)
	badges := m.headerBadges()
	return modal.BuildOverlay(modal.Frame{
		ID:        codeViewerModalID,
		Scope:     views.ScopeCodeView,
		Title:     m.frameTitle(),
		WidthPct:  94,
		HeightPct: 92,
		Badges:    badges,
		Facts:     views.CodeViewerFacts(m.state),
	}, body)
}

func (m *codeViewerModal) frameTitle() string {
	if m.state == nil {
		return "Code Viewer"
	}
	if m.state.Title != "" && m.state.Title != m.state.FilePath {
		return fmt.Sprintf("Code · %s (%s)", m.state.Title, m.state.FilePath)
	}
	return "Code · " + m.state.FilePath
}

func (m *codeViewerModal) headerBadges() []ui.Badge {
	out := []ui.Badge{{Text: "RO · VIM", Tone: "muted"}}
	if m.state == nil {
		return out
	}
	if m.state.VisualActive() {
		s, e, _ := m.state.SelectedRange()
		out = append(out, ui.Badge{Text: fmt.Sprintf("SEL L%d-L%d", s, e), Tone: "warning"})
	}
	if h, ok := m.state.HighlightAt(m.state.CursorLine); ok {
		st := h.Style()
		out = append(out, ui.Badge{Text: fmt.Sprintf("%s · %s", h.Kind, h.EffectiveColor()), Tone: st.BadgeTone})
	}
	return out
}

func (m *codeViewerModal) Handle(c core.Command) modal.Result {
	if res, ok := m.handleMotion(c); ok {
		return res
	}
	if res, ok := m.handleSearch(c); ok {
		return res
	}
	return m.handleAction(c)
}

var codeMotionHandlers = map[string]func(*views.CodeViewerState, core.Command){
	"motion.down":          func(st *views.CodeViewerState, c core.Command) { st.MoveDown(c.Times()) },
	"motion.up":            func(st *views.CodeViewerState, c core.Command) { st.MoveUp(c.Times()) },
	"motion.left":          func(st *views.CodeViewerState, c core.Command) { st.MoveLeft(c.Times()) },
	"motion.right":         func(st *views.CodeViewerState, c core.Command) { st.MoveRight(c.Times()) },
	"motion.top":           func(st *views.CodeViewerState, c core.Command) { st.MoveTop(c.Count) },
	"motion.bottom":        func(st *views.CodeViewerState, c core.Command) { st.MoveBottom(c.Count) },
	"motion.half.down":     func(st *views.CodeViewerState, c core.Command) { st.HalfPageDown(c.Times()) },
	"motion.half.up":       func(st *views.CodeViewerState, c core.Command) { st.HalfPageUp(c.Times()) },
	"motion.page.down":     func(st *views.CodeViewerState, c core.Command) { st.PageDown(c.Times()) },
	"motion.page.up":       func(st *views.CodeViewerState, c core.Command) { st.PageUp(c.Times()) },
	"motion.scroll.down":   func(st *views.CodeViewerState, c core.Command) { st.ScrollViewport(c.Times()) },
	"motion.scroll.up":     func(st *views.CodeViewerState, c core.Command) { st.ScrollViewport(-c.Times()) },
	CmdCodeMotionWordNext:  func(st *views.CodeViewerState, c core.Command) { st.MoveWordForward(c.Times()) },
	CmdCodeMotionWordPrev:  func(st *views.CodeViewerState, c core.Command) { st.MoveWordBackward(c.Times()) },
	CmdCodeMotionWordEnd:   func(st *views.CodeViewerState, c core.Command) { st.MoveWordEnd(c.Times()) },
	CmdCodeMotionLineStart: func(st *views.CodeViewerState, _ core.Command) { st.MoveLineStart() },
	CmdCodeMotionLineFirst: func(st *views.CodeViewerState, _ core.Command) { st.MoveFirstNonBlank() },
	CmdCodeMotionLineEnd:   func(st *views.CodeViewerState, _ core.Command) { st.MoveLineEnd() },
	CmdCodeHunkNext:        func(st *views.CodeViewerState, _ core.Command) { st.NextHighlight(1) },
	CmdCodeHunkPrev:        func(st *views.CodeViewerState, _ core.Command) { st.NextHighlight(-1) },
}

func (m *codeViewerModal) handleMotion(c core.Command) (modal.Result, bool) {
	fn, ok := codeMotionHandlers[c.ID]
	if !ok {
		return modal.Result{}, false
	}
	fn(m.state, c)
	return modal.Result{Handled: true}, true
}

func (m *codeViewerModal) handleSearch(c core.Command) (modal.Result, bool) {
	switch c.ID {
	case CmdCodeSearchStart:
		m.state.StartSearch()
		m.mode = ModeCodeViewSearch
		return modal.Result{Handled: true, Mode: ModeCodeViewSearch, Status: "/ search · enter finds · esc cancels", SetStatus: true}, true
	case CmdCodeSearchInsert:
		m.state.SearchAppend(c.Arg("text"))
		return modal.Result{Handled: true}, true
	case CmdCodeSearchBackspace:
		m.state.SearchBackspace()
		return modal.Result{Handled: true}, true
	case CmdCodeSearchCancel:
		m.state.CancelSearch()
		m.mode = ModeCodeView
		return modal.Result{Handled: true, Mode: ModeCodeView, Status: "search cancelled", SetStatus: true}, true
	case CmdCodeSearchCommit:
		found := m.state.CommitSearch()
		m.mode = ModeCodeView
		msg := fmt.Sprintf("/%s · L%d:%d", m.state.SearchQuery, m.state.CursorLine, m.state.CursorCol+1)
		if !found && m.state.SearchQuery != "" {
			msg = fmt.Sprintf("pattern not found: %s", m.state.SearchQuery)
		}
		return modal.Result{Handled: true, Mode: ModeCodeView, Status: msg, SetStatus: true}, true
	case CmdCodeSearchNext:
		return m.stepSearchResult(1), true
	case CmdCodeSearchPrev:
		return m.stepSearchResult(-1), true
	default:
		return modal.Result{}, false
	}
}

func (m *codeViewerModal) stepSearchResult(dir int) modal.Result {
	if !m.state.FindNext(dir) {
		return modal.Result{Handled: true, Status: fmt.Sprintf("pattern not found: %s", m.state.SearchQuery), SetStatus: true}
	}
	return modal.Result{Handled: true, Status: fmt.Sprintf("/%s · L%d:%d", m.state.SearchQuery, m.state.CursorLine, m.state.CursorCol+1), SetStatus: true}
}

func (m *codeViewerModal) handleAction(c core.Command) modal.Result {
	switch c.ID {
	case CmdCodeBack, CmdModalCancel:
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseCancel, Status: "returned from code viewer", SetStatus: true}
	case CmdCodeVisualToggle:
		m.state.ToggleVisual(c.Arg("mode"))
		return m.visualStatusResult()
	case CmdCodeVisualClear:
		m.state.ClearVisual()
		return modal.Result{Handled: true, Status: "visual selection cleared", SetStatus: true}
	case CmdCodeAnnotate:
		return m.openAnnotateForm()
	case CmdCodeHunkCustomize:
		return m.openCustomizeForm()
	case CmdCodeHunkToTask:
		return m.promoteHunkToTask()
	case CmdCodeYank:
		return m.yankSelection()
	}
	return modal.Result{}
}

func (m *codeViewerModal) visualStatusResult() modal.Result {
	if !m.state.VisualActive() {
		return modal.Result{Handled: true, Status: "visual selection cleared", SetStatus: true}
	}
	s, e, _ := m.state.SelectedRange()
	return modal.Result{
		Handled:   true,
		Status:    fmt.Sprintf("visual %s L%d-L%d · a annotates · y yanks · esc clears", m.state.VisualMode, s, e),
		SetStatus: true,
	}
}

func (m *codeViewerModal) yankSelection() modal.Result {
	s, e, _ := m.state.SelectedRange()
	text := m.state.Snippet(s, e, 0)
	m.a.yank(text, fmt.Sprintf("%s:L%d-L%d", filepath.Base(m.state.FilePath), s, e))
	m.state.ClearVisual()
	return modal.Result{Handled: true}
}

// --- Annotation & Customization Forms -------------------------------------------

func (m *codeViewerModal) openAnnotateForm() modal.Result {
	startLine, endLine, _ := m.state.SelectedRange()
	specs := []core.ParamSpec{
		{Name: "label", Type: core.ParamString, Description: "Short label for this line/hunk annotation"},
		{Name: "note", Type: core.ParamString, Required: true, Description: "Annotation note (persisted in SQLite store)"},
		{Name: "color", Type: core.ParamString, Enum: views.CodeHighlightColorNames, Default: views.DefaultAnnotationHighlightColor, Description: "Highlight color preset"},
		{Name: "customColor", Type: core.ParamString, Description: "Optional #RRGGBB hex color override"},
	}
	vals := map[string]any{
		"color": views.DefaultAnnotationHighlightColor,
	}
	rangeText := fmt.Sprintf("L%d", startLine)
	if endLine > startLine {
		rangeText = fmt.Sprintf("L%d-L%d", startLine, endLine)
	}
	fm := form.New(fmt.Sprintf("Annotate %s (%s)", filepath.Base(m.state.FilePath), rangeText), specs, vals)
	fm.SubmitRow = true
	fm.SubmitLabel = "Save Annotation"
	fm.Wrap = m.a.wrapInputs()
	mod := modal.NewFormModal(fm, modal.FormOptions{
		ID:            codeAnnotateModalID,
		Scope:         codeAnnotateModalID,
		NormalMode:    ModeForm,
		EditMode:      ModeFormEdit,
		DisableDrafts: true,
		OnSubmit: func(values map[string]any) modal.Result {
			return m.saveNewAnnotation(startLine, endLine, values)
		},
	})
	return modal.Result{Handled: true, Push: mod}
}

func (m *codeViewerModal) saveNewAnnotation(startLine, endLine int, values map[string]any) modal.Result {
	note := strings.TrimSpace(data.Text(values["note"]))
	if note == "" {
		return modal.Result{Handled: true, Status: "annotation note cannot be empty", SetStatus: true}
	}
	label := strings.TrimSpace(data.Text(values["label"]))
	color := pickHighlightColor(values, views.DefaultAnnotationHighlightColor)
	snippet := m.state.Snippet(startLine, endLine, 20)
	title := label
	if title == "" {
		title = fmt.Sprintf("%s:L%d-L%d", filepath.Base(m.state.FilePath), startLine, endLine)
	}
	fields := map[string]any{
		"title":           title,
		"body":            note,
		"filePath":        m.state.FilePath,
		"authorKind":      "owner",
		"annotationKind":  codeAnnotationKindVal,
		"annotationLabel": label,
		"startLine":       startLine,
		"endLine":         endLine,
		"highlightColor":  color,
		"snippet":         snippet,
	}
	if m.taskRef != nil && m.taskRef.ID != "" {
		fields["task"] = *m.taskRef
	}
	rec, cs, err := m.a.Store.Create(data.CreateInput{Kind: data.KindNote, Fields: fields}, User)
	if err != nil {
		m.a.fail("annotate", err)
		return modal.Result{Handled: true}
	}
	if m.sourceRef != nil && m.sourceRef.ID != "" {
		_, _, _ = m.a.Store.Relate(data.RelateInput{From: rec.Ref(), Kind: data.RelAbout, To: *m.sourceRef}, User)
	}
	m.a.wrote(fmt.Sprintf("annotated L%d-L%d (%s)", startLine, endLine, color), cs, nil)
	m.state.UpsertHighlight(views.CodeHighlight{
		ID:        "ann:" + rec.ID,
		Kind:      views.CodeHighlightAnnotation,
		StartLine: startLine,
		EndLine:   endLine,
		Color:     color,
		Label:     firstNonEmpty(label, title),
		Note:      note,
		RecordID:  rec.ID,
	})
	m.state.ClearVisual()
	return modal.Result{
		Handled:     true,
		Close:       true,
		CloseReason: modal.CloseSubmit,
		Toast:       &modal.ToastSpec{Text: fmt.Sprintf("Saved annotation L%d-L%d", startLine, endLine), Level: toast.Ok},
	}
}

func (m *codeViewerModal) openCustomizeForm() modal.Result {
	h, ok := m.state.HighlightAt(m.state.CursorLine)
	if !ok {
		return modal.Result{Handled: true, Status: "move cursor onto a highlighted hunk to customize", SetStatus: true}
	}
	curColor := h.EffectiveColor()
	presetColor, customHex := splitHighlightColor(curColor, views.DefaultColorForKind(h.Kind))
	specs := []core.ParamSpec{
		{Name: "color", Type: core.ParamString, Enum: views.CodeHighlightColorNames, Default: presetColor, Description: "Highlight color preset"},
		{Name: "customColor", Type: core.ParamString, Description: "Optional #RRGGBB hex color override"},
		{Name: "label", Type: core.ParamString, Description: "Hunk label"},
		{Name: "note", Type: core.ParamString, Description: "Annotation / context note text"},
	}
	vals := map[string]any{
		"color":       presetColor,
		"customColor": customHex,
		"label":       h.Label,
		"note":        h.Note,
	}
	fm := form.New(fmt.Sprintf("Customize %s Highlight (%s)", h.Kind, h.LineRangeText()), specs, vals)
	fm.SubmitRow = true
	fm.SubmitLabel = "Apply Highlight"
	fm.Wrap = m.a.wrapInputs()
	target := h
	mod := modal.NewFormModal(fm, modal.FormOptions{
		ID:            codeCustomizeModalID,
		Scope:         codeCustomizeModalID,
		NormalMode:    ModeForm,
		EditMode:      ModeFormEdit,
		DisableDrafts: true,
		OnSubmit: func(values map[string]any) modal.Result {
			return m.applyHunkCustomization(target, values)
		},
	})
	return modal.Result{Handled: true, Push: mod}
}

func (m *codeViewerModal) applyHunkCustomization(h views.CodeHighlight, values map[string]any) modal.Result {
	color := pickHighlightColor(values, h.EffectiveColor())
	label := strings.TrimSpace(data.Text(values["label"]))
	note := strings.TrimSpace(data.Text(values["note"]))
	updated := h
	updated.Color = color
	updated.Label = label
	updated.Note = note

	recID, err := m.persistHighlightUpdate(updated)
	if err != nil {
		m.a.fail("customize highlight", err)
		return modal.Result{Handled: true}
	}
	if recID != "" {
		updated.RecordID = recID
	}
	m.state.UpsertHighlight(updated)
	return modal.Result{
		Handled:     true,
		Close:       true,
		CloseReason: modal.CloseSubmit,
		Status:      fmt.Sprintf("updated %s highlight (%s)", updated.LineRangeText(), color),
		SetStatus:   true,
	}
}

func (m *codeViewerModal) persistHighlightUpdate(h views.CodeHighlight) (string, error) {
	if m.a.Store == nil {
		return h.RecordID, nil
	}
	s, e := h.NormalizedBounds()
	title := firstNonEmpty(h.Label, fmt.Sprintf("%s:%s", filepath.Base(m.state.FilePath), h.LineRangeText()))
	if h.RecordID != "" {
		patch := map[string]any{
			"title":           title,
			"body":            h.Note,
			"annotationLabel": h.Label,
			"highlightColor":  h.Color,
		}
		_, cs, err := m.a.Store.Patch(data.PatchInput{Ref: core.Ref{Kind: data.KindNote, ID: h.RecordID}, Fields: patch}, User)
		if err != nil {
			return "", err
		}
		m.a.wrote(fmt.Sprintf("updated highlight %s", h.LineRangeText()), cs, nil)
		return h.RecordID, nil
	}
	ak := codeAnnotationKindVal
	if h.Kind == views.CodeHighlightSource {
		ak = codeSourceStyleKind
	}
	rec, cs, err := m.a.Store.Create(data.CreateInput{
		Kind: data.KindNote,
		Fields: map[string]any{
			"title":           title,
			"body":            h.Note,
			"filePath":        m.state.FilePath,
			"authorKind":      "owner",
			"annotationKind":  ak,
			"annotationLabel": h.Label,
			"startLine":       s,
			"endLine":         e,
			"highlightColor":  h.Color,
		},
	}, User)
	if err != nil {
		return "", err
	}
	m.a.wrote(fmt.Sprintf("saved highlight style %s", h.LineRangeText()), cs, nil)
	return rec.ID, nil
}

// --- Promote Hunk to Task -------------------------------------------------------

func (m *codeViewerModal) promoteHunkToTask() modal.Result {
	h, ok := m.state.HighlightAt(m.state.CursorLine)
	if !ok {
		return modal.Result{Handled: true, Status: "move cursor onto a highlighted hunk to promote to a task", SetStatus: true}
	}
	if m.a.Store == nil {
		return modal.Result{Handled: true, Status: "no store available", SetStatus: true}
	}
	s, e := h.NormalizedBounds()
	loc := fmt.Sprintf("%s:%d-%d", m.state.FilePath, s, e)
	if s == e {
		loc = fmt.Sprintf("%s:%d", m.state.FilePath, s)
	}
	taskTitle := firstNonEmpty(strings.TrimSpace(h.Label), firstLineText(h.Note), fmt.Sprintf("Review %s (%s)", filepath.Base(m.state.FilePath), h.LineRangeText()))
	desc := formatHunkTaskDescription(loc, h, m.state.Snippet(s, e, 24))
	fields := map[string]any{
		"title":       taskTitle,
		"description": desc,
		"workStatus":  "todo",
		"filePath":    m.state.FilePath,
		"startLine":   s,
		"endLine":     e,
	}
	if m.taskRef != nil && m.taskRef.ID != "" {
		fields["parent"] = *m.taskRef
	}
	taskRec, cs, err := m.a.Store.Create(data.CreateInput{Kind: data.KindTask, Fields: fields}, User)
	if err != nil {
		m.a.fail("promote to task", err)
		return modal.Result{Handled: true}
	}
	if h.RecordID != "" {
		noteRef := core.Ref{Kind: data.KindNote, ID: h.RecordID}
		_, _, _ = m.a.Store.Relate(data.RelateInput{From: noteRef, Kind: data.RelAbout, To: taskRec.Ref()}, User)
	}
	msg := fmt.Sprintf("created task %q from %s", taskTitle, loc)
	m.a.wrote(msg, cs, nil)
	return modal.Result{
		Handled:   true,
		Status:    msg,
		SetStatus: true,
		Toast:     &modal.ToastSpec{Text: "Task created: " + taskTitle, Level: toast.Ok},
	}
}

func formatHunkTaskDescription(loc string, h views.CodeHighlight, snippet string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**Location:** `%s`\n", loc)
	if strings.TrimSpace(h.Note) != "" {
		fmt.Fprintf(&b, "\n%s\n", strings.TrimSpace(h.Note))
	}
	if strings.TrimSpace(snippet) != "" {
		fmt.Fprintf(&b, "\n```\n%s\n```\n", snippet)
	}
	return b.String()
}

func firstLineText(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if rs := []rune(line); len(rs) > 72 {
		return string(rs[:71]) + "…"
	}
	return line
}

func pickHighlightColor(values map[string]any, fallback string) string {
	if custom := strings.TrimSpace(data.Text(values["customColor"])); views.IsHexColor(custom) {
		return custom
	}
	if preset := strings.TrimSpace(data.Text(values["color"])); preset != "" {
		return preset
	}
	return fallback
}

func splitHighlightColor(cur, fallback string) (preset, customHex string) {
	if views.IsHexColor(cur) {
		return fallback, cur
	}
	for _, c := range views.CodeHighlightColorNames {
		if c == cur {
			return cur, ""
		}
	}
	return fallback, ""
}
