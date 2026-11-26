package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/input"
	"ofam.dev/ochazuke/widgets/modal"
)

const (
	ModeNoteDeck      = "note-deck"
	noteDeckModalID   = "note.deck"
	noteDeckOverlayID = "note.deck.overlay"

	CmdNoteDeckOpen     = "notedeck.open"
	CmdNoteDeckInsert   = "notedeck.insert"
	CmdNoteDeckBack     = "notedeck.backspace"
	CmdNoteDeckNewline  = "notedeck.newline"
	CmdNoteDeckPrev     = "notedeck.prev"
	CmdNoteDeckNext     = "notedeck.next"
	CmdNoteDeckMark     = "notedeck.mark"
	CmdNoteDeckMarkAll  = "notedeck.mark.all"
	CmdNoteDeckSave     = "notedeck.save"
	CmdNoteDeckDispatch = "notedeck.dispatch"
	CmdNoteDeckClose    = "notedeck.close"
)

func (a *App) pagedNoteComposerEnabled() bool {
	return a == nil || a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetPagedNoteComposer, true)
}

func (a *App) initNoteDeck() {
	path := ""
	if a.Store != nil {
		dsn := a.Store.DBPath()
		if dsn != "" && dsn != ":memory:" && !strings.HasPrefix(dsn, "file::memory:") && !strings.Contains(dsn, "mode=memory") {
			path = filepath.Join(a.Store.FilesRoot(), "note-deck.json")
		}
	}
	a.noteDeck = data.NewNoteDeckStore(path)
}

func noteDeckSpecs() []core.CommandSpec {
	text := []core.ParamSpec{{Name: "text", Type: core.ParamString}}
	return []core.CommandSpec{
		spec(CmdNoteDeckOpen, "Open the floating paged Note Composer deck for the focused item"),
		{ID: CmdNoteDeckInsert, Description: "Type into the note deck composer", Params: text, Meta: map[string]any{"internal": true}},
		{ID: CmdNoteDeckBack, Description: "Erase in the note deck composer", Meta: map[string]any{"internal": true}},
		{ID: CmdNoteDeckNewline, Description: "Insert newline in the note deck composer", Meta: map[string]any{"internal": true}},
		{ID: CmdNoteDeckPrev, Description: "Previous page in the note deck", Meta: map[string]any{"internal": true}},
		{ID: CmdNoteDeckNext, Description: "Next page in the note deck", Meta: map[string]any{"internal": true}},
		{ID: CmdNoteDeckMark, Description: "Toggle dispatch mark on the current note page", Meta: map[string]any{"internal": true}},
		{ID: CmdNoteDeckMarkAll, Description: "Toggle mark-all across the note deck", Meta: map[string]any{"internal": true}},
		{ID: CmdNoteDeckSave, Description: "Save the current note deck page", Meta: map[string]any{"internal": true}},
		{ID: CmdNoteDeckDispatch, Description: "Dispatch marked note pages to the active project agent", Meta: map[string]any{"internal": true}},
		{ID: CmdNoteDeckClose, Description: "Save draft and close the note deck", Meta: map[string]any{"internal": true}},
	}
}

func noteDeckBindings() []core.Binding {
	m := ModeNoteDeck
	return []core.Binding{
		{Mode: m, Class: core.ClassPrintable, Command: CmdNoteDeckInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		{Mode: m, Class: core.ClassPaste, Command: CmdNoteDeckInsert, Args: map[string]any{"text": "$text"}, Hidden: true},
		bind(m, "", "backspace", CmdNoteDeckBack, "erase"),
		hidden(bind(m, "", "shift+backspace", CmdNoteDeckBack, "erase")),
		bind(m, "", "enter", CmdNoteDeckNewline, "newline"),
		bind(m, "", "ctrl+p", CmdNoteDeckPrev, "prev page"),
		hidden(bind(m, "", "alt+[", CmdNoteDeckPrev, "prev page")),
		bind(m, "", "ctrl+n", CmdNoteDeckNext, "next page"),
		hidden(bind(m, "", "alt+]", CmdNoteDeckNext, "next page")),
		bind(m, "", "ctrl+m", CmdNoteDeckMark, "mark page"),
		bind(m, "", "ctrl+a", CmdNoteDeckMarkAll, "mark all"),
		bind(m, "", "ctrl+s", CmdNoteDeckSave, "save"),
		bind(m, "", "ctrl+d", CmdNoteDeckDispatch, "dispatch marked"),
		bind(m, "", "esc", CmdNoteDeckClose, "save & close"),
	}
}

func (a *App) registerNoteDeckHandlers() {
	a.handle(CmdNoteDeckOpen, a.openNoteDeckForFocus)
	for _, id := range []string{
		CmdNoteDeckInsert, CmdNoteDeckBack, CmdNoteDeckNewline,
		CmdNoteDeckPrev, CmdNoteDeckNext, CmdNoteDeckMark, CmdNoteDeckMarkAll,
		CmdNoteDeckSave, CmdNoteDeckDispatch, CmdNoteDeckClose,
	} {
		a.handle(id, a.handleModal)
	}
}

func (a *App) focusNoteDeckTarget(s core.State) (title, deepLink, space, focusID string) {
	space = s.ActivePage
	focusID = s.ActiveFrame().FocusID
	if ref, ok := a.subject(s, core.Command{}); ok && ref.ID != "" {
		title = strings.TrimSpace(a.title(ref))
		if title == "" {
			title = data.RefText(ref)
		}
		deepLink = fmt.Sprintf("mtm://%s/%s", ref.Kind, ref.ID)
		return title, deepLink, space, focusID
	}
	root := a.View(s)
	if n := root.Find(focusID); n != nil {
		if t := strings.TrimSpace(n.Title); t != "" {
			title = t
		} else if n.ID != "" {
			title = n.ID
		}
	}
	if title == "" {
		title = fmt.Sprintf("%s (%s)", space, focusID)
	}
	deepLink = fmt.Sprintf("mtm://ui/%s/%s", space, focusID)
	return title, deepLink, space, focusID
}

func (a *App) openNoteDeckForFocus(s core.State, _ core.Command) core.StepResult {
	if a.noteDeck == nil {
		a.initNoteDeck()
	}
	title, deepLink, space, focusID := a.focusNoteDeckTarget(s)
	page, err := a.noteDeck.AddPage(title, deepLink, space, focusID, a.now())
	if err != nil {
		a.fail("note deck", err)
		return core.Next(s)
	}
	dm := newNoteDeckModal(a, page.ID, a.activePortalProjectID(s))
	a.say("added note page %d · ctrl+p/n flip · ctrl+m mark · ctrl+d dispatch", a.noteDeck.Len())
	return core.Next(a.pushModal(s, dm))
}

type noteDeckModal struct {
	a         *App
	projID    string
	pageIndex int
	line      input.Line
}

func newNoteDeckModal(a *App, selectID, projID string) *noteDeckModal {
	m := &noteDeckModal{a: a, projID: projID}
	pages := a.noteDeck.Pages()
	m.pageIndex = max(len(pages)-1, 0)
	for i, p := range pages {
		if p.ID == selectID {
			m.pageIndex = i
			break
		}
	}
	m.loadCurrentBody()
	return m
}

func (m *noteDeckModal) ID() string        { return noteDeckModalID }
func (m *noteDeckModal) Scope() string     { return noteDeckModalID }
func (m *noteDeckModal) FocusID() string   { return noteDeckModalID + ".input" }
func (m *noteDeckModal) Mode() string      { return ModeNoteDeck }
func (m *noteDeckModal) Backdrop() bool    { return true }
func (m *noteDeckModal) EscapeGuard() bool { return false }

func (m *noteDeckModal) currentPage() (data.NoteDeckPage, bool) {
	pages := m.a.noteDeck.Pages()
	if len(pages) == 0 {
		return data.NoteDeckPage{}, false
	}
	if m.pageIndex < 0 {
		m.pageIndex = 0
	}
	if m.pageIndex >= len(pages) {
		m.pageIndex = len(pages) - 1
	}
	return pages[m.pageIndex], true
}

func (m *noteDeckModal) loadCurrentBody() {
	if p, ok := m.currentPage(); ok {
		m.line.Set(p.Body)
	} else {
		m.line.Set("")
	}
}

func (m *noteDeckModal) saveCurrentBody() {
	if p, ok := m.currentPage(); ok {
		_ = m.a.noteDeck.UpdateBody(p.ID, m.line.Value(), m.a.now())
	}
}

func (m *noteDeckModal) Handle(c core.Command) modal.Result {
	switch c.ID {
	case CmdNoteDeckInsert:
		m.line.Insert(c.Arg("text"))
		m.saveCurrentBody()
		return modal.Result{Handled: true}
	case CmdNoteDeckBack:
		m.line.HandleCommand(core.Command{ID: input.CmdBackspace})
		m.saveCurrentBody()
		return modal.Result{Handled: true}
	case CmdNoteDeckNewline:
		m.line.Insert("\n")
		m.saveCurrentBody()
		return modal.Result{Handled: true}
	case CmdNoteDeckPrev:
		m.saveCurrentBody()
		if n := m.a.noteDeck.Len(); n > 0 {
			m.pageIndex = (m.pageIndex - 1 + n) % n
			m.loadCurrentBody()
		}
		return modal.Result{Handled: true}
	case CmdNoteDeckNext:
		m.saveCurrentBody()
		if n := m.a.noteDeck.Len(); n > 0 {
			m.pageIndex = (m.pageIndex + 1) % n
			m.loadCurrentBody()
		}
		return modal.Result{Handled: true}
	case CmdNoteDeckMark:
		m.saveCurrentBody()
		if p, ok := m.currentPage(); ok {
			marked, _ := m.a.noteDeck.ToggleMark(p.ID)
			state := "unmarked"
			if marked {
				state = "marked"
			}
			return modal.Result{Handled: true, Status: fmt.Sprintf("page %d %s (%d marked)", m.pageIndex+1, state, m.a.noteDeck.MarkedCount()), SetStatus: true}
		}
		return modal.Result{Handled: true}
	case CmdNoteDeckMarkAll:
		m.saveCurrentBody()
		allMarked := m.a.noteDeck.MarkedCount() == m.a.noteDeck.Len() && m.a.noteDeck.Len() > 0
		_ = m.a.noteDeck.MarkAll(!allMarked)
		return modal.Result{Handled: true, Status: fmt.Sprintf("%d/%d pages marked", m.a.noteDeck.MarkedCount(), m.a.noteDeck.Len()), SetStatus: true}
	case CmdNoteDeckSave:
		m.saveCurrentBody()
		return modal.Result{Handled: true, Status: fmt.Sprintf("saved note page %d/%d", m.pageIndex+1, m.a.noteDeck.Len()), SetStatus: true}
	case CmdNoteDeckClose:
		m.saveCurrentBody()
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit, Status: "saved note deck", SetStatus: true}
	case CmdNoteDeckDispatch:
		m.saveCurrentBody()
		curID := ""
		if p, ok := m.currentPage(); ok {
			curID = p.ID
		}
		compiled, count := m.a.noteDeck.CompileMarkedMarkdown(curID)
		if count == 0 || compiled == "" {
			return modal.Result{Handled: true, Status: "no notes to dispatch", SetStatus: true}
		}
		m.a.yank(compiled, fmt.Sprintf("%d note(s)", count))
		// Deliver to an active project agent pane, or spawn one with project default context.
		if pane, ok := m.a.findProjectAgentPane(m.projID); ok && pane != nil {
			m.a.deliverComposerPrompt(*pane, compiled, ComposerModeQueue)
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit, Status: fmt.Sprintf("dispatched %d note(s) to %s", count, pane.ID), SetStatus: true}
		}
		if m.projID != "" {
			if _, pane, err := m.a.spawnProjectAgentPane(m.a.State(), m.projID, "", compiled); err == nil && pane != nil {
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit, Status: fmt.Sprintf("spawned agent %s and dispatched %d note(s)", pane.ID, count), SetStatus: true}
			}
		}
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit, Status: fmt.Sprintf("copied %d compiled note(s) to clipboard", count), SetStatus: true}
	}
	if input.IsReadlineCommand(c.ID) {
		m.line.HandleCommand(c)
		m.saveCurrentBody()
		return modal.Result{Handled: true}
	}
	return modal.Result{}
}

func (a *App) findProjectAgentPane(projID string) (*data.Record, bool) {
	if a.Store == nil || projID == "" {
		return nil, false
	}
	res, err := a.Store.Query(data.Query{Kind: data.KindPane})
	if err != nil {
		return nil, false
	}
	for i := range res.Items {
		p := res.Items[i]
		if p.Text("runStatus") != "running" {
			continue
		}
		if _, pref := a.resolvePaneProject(p); pref != nil && pref.ID == projID {
			return &p, true
		}
	}
	return nil, false
}

func (m *noteDeckModal) OnClose(_ modal.CloseReason) {
	m.saveCurrentBody()
}

func (m *noteDeckModal) View(_, _ int) ui.ViewNode {
	pages := m.a.noteDeck.Pages()
	total := len(pages)
	markedTotal := m.a.noteDeck.MarkedCount()
	p, ok := m.currentPage()
	previewMD := "_No notes yet._"
	pageBadge := "0/0"
	markBadge := ui.Badge{Text: "unmarked", Tone: "muted"}
	if ok {
		p.Body = m.line.Value()
		previewMD = data.RenderPageMarkdown(p)
		pageBadge = fmt.Sprintf("Page %d/%d", m.pageIndex+1, total)
		if p.Marked {
			markBadge = ui.Badge{Text: "✓ marked", Tone: "accent"}
		}
	}

	previewPanel := ui.ViewNode{
		ID:     noteDeckModalID + ".preview",
		Kind:   ui.KindPanel,
		Title:  "Rendered Note Deck",
		Border: true,
		Box:    layout.Constraints{Flex: 2},
		Badges: []ui.Badge{
			{Text: pageBadge, Tone: "accent"},
			markBadge,
			{Text: fmt.Sprintf("%d marked total", markedTotal), Tone: "info"},
		},
		Children: []ui.ViewNode{{
			ID:    noteDeckModalID + ".md",
			Kind:  ui.KindMarkdown,
			Props: map[string]any{"markdown": previewMD, "wrap": true},
		}},
	}

	bodyVal := m.line.Value()
	rs := []rune(bodyVal)
	ci := min(max(m.line.Cursor, 0), len(rs))
	shownInput := string(rs[:ci]) + "█" + string(rs[ci:])
	composerPanel := ui.ViewNode{
		ID:        noteDeckModalID + ".input",
		Kind:      ui.KindPanel,
		Title:     "Note Composer",
		Border:    true,
		Focusable: true,
		Box:       layout.Constraints{Flex: 1},
		Children: []ui.ViewNode{
			{ID: noteDeckModalID + ".text", Kind: ui.KindText, Props: map[string]any{"text": shownInput, "wrap": true}},
			text(noteDeckModalID+".help", "ctrl+p/n flip pages · ctrl+m mark · ctrl+a mark all · ctrl+s save · ctrl+d dispatch · esc close", "muted"),
		},
	}

	content := ui.ViewNode{
		ID:       noteDeckModalID,
		Kind:     ui.KindVStack,
		Scope:    noteDeckModalID,
		Box:      layout.Constraints{MinW: 78, MaxW: 112, MinH: 22, MaxH: 30},
		Children: []ui.ViewNode{previewPanel, composerPanel},
	}
	return ui.ViewNode{
		ID:       noteDeckOverlayID,
		Kind:     ui.KindOverlay,
		Props:    map[string]any{"backdrop": true, "title": "Note Deck"},
		Children: []ui.ViewNode{content},
	}
}
