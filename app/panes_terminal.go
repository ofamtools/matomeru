package app

import (
	"slices"
	"strings"
	"sync"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/ptyhost"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
)

type termBuf struct {
	lines []string
	typed string
}

// PaneController owns PTY host/controller, synthetic screen buffers, active
// pane pages, zoom/escape state, rail grouping cache, project overview context,
// hints, composer completions/queue, and staged batch question answers.
type PaneController struct {
	Host            ptyhost.Host
	Term            *ptyhost.Controller
	term            map[string]*termBuf
	activePage      map[string]string
	sideSplit       map[string]string
	pageScroll      map[string]int
	tocCollapsed    map[string]bool
	zoom            bool
	escapes         int
	railCacheKey    string
	railProjectRefs map[string]core.Ref
	railGroupPanes  map[string][]data.Record
	railCustomGroup string
	railGroupSet    bool
	projectCtxRows  map[string]CollectedContextItem
	projectCtxGroup string
	projectCtxRef   *core.Ref
	hints           *hintsState
	composerFilesMu sync.RWMutex
	composerFiles   map[string]cachedFileList
	aiSkillsMu      sync.RWMutex
	aiSkills        []composerCompletion
	aiSkillsLoaded  bool
	aiSkillsLoading bool
	queuedMu        sync.Mutex
	queuedPrompts   map[string][]string
	interruptDelay  time.Duration
	stagedAnswers   map[string]map[string]map[string]any
}

func newPaneController() *PaneController {
	return &PaneController{
		term:         map[string]*termBuf{},
		activePage:   map[string]string{},
		sideSplit:    map[string]string{},
		pageScroll:   map[string]int{},
		tocCollapsed: map[string]bool{},
	}
}

// jumpToPage shows a page in its pane's site: the pane is selected in the
// rail, the page opened and marked as viewed.
func (a *App) jumpToPage(s core.State, pageID string) core.State {
	ref := core.Ref{Kind: data.KindPage, ID: pageID}
	page, err := a.Store.Get(ref)
	if err != nil {
		a.fail("open page", err)
		return s
	}
	pane, ok := page.RefField("pane")
	if !ok {
		return a.show(s, ref) // a page with no pane has only its Show page
	}
	rail := a.rail()
	if !selectRow(rail, data.RefText(pane)) {
		rail.Query = "" // the rail's filter hides the pane: drop it
		selectRow(rail, data.RefText(pane))
	}
	s = a.dropModals(s)
	s.ActivePage = SpacePanes
	a.nav.tocFilter = ""
	return a.openPage(withPage(s, SpacePanes, tocPrefix+pageID, ModeNormal), pane.ID, *page)
}

// openPage makes page the one a pane's site shows. Looking at it marks it viewed.
func (a *App) openPage(s core.State, paneID string, page data.Record) core.State {
	a.panes.activePage[paneID] = page.ID
	if page.IsUnread() {
		if _, err := a.Router.MarkViewed(page.ID, viewer); err != nil {
			a.fail("mark viewed", err)
			return s
		}
	}
	a.say("%s", page.Text("title"))
	return s
}

func (a *App) sitePageOpen(s core.State, c core.Command) core.StepResult {
	if strings.HasPrefix(s.ActiveFrame().FocusID, views.TOCGroupPrefix) {
		return a.tocGroupToggle(s, c)
	}
	ref, ok := a.subject(s, c)
	if ok && ref.Kind == data.KindLane {
		return core.Next(a.show(s, ref))
	}
	pane, hasPane := a.SelectedPane()
	if !ok || !hasPane || ref.Kind != data.KindPage {
		a.say("no page here to open")
		return core.Next(s)
	}
	page, err := a.Store.Get(ref)
	if err != nil || page.Text("pageKind") == "screen" {
		// The Screen: the live terminal. Enter goes straight into it.
		a.panes.activePage[pane.ID] = ref.ID
		return core.Next(a.enterScreen(withFocus(s, siteScreen), *pane))
	}
	s = a.openPage(s, pane.ID, *page)
	a.setPageScrollOffset(page.ID, 0)
	a.viewMemo.gen++
	if a.cfg.Settings != nil && a.cfg.Settings.Bool(SetPageLockScroll, true) {
		focus := views.PageScrollID
		if page.Text("pageKind") == "questions" {
			for _, n := range a.focusables(s) {
				if n.region == "site.content" && n.id != views.PageScrollID {
					focus = n.id
					break
				}
			}
		}
		a.say("%s · j/k/ctrl+d/ctrl+u scroll · tab/n/p controls · esc leaves", page.Text("title"))
		return core.Next(withMode(withFocus(s, focus), ModePage))
	}
	for _, n := range a.focusables(s) {
		if n.region == "site.content" && n.id != views.PageScrollID {
			return core.Next(withFocus(s, n.id))
		}
	}
	return core.Next(withFocus(s, views.PageScrollID))
}

func (a *App) paneEnterLive(s core.State, _ core.Command) core.StepResult {
	pane, ok := a.SelectedPane()
	if !ok {
		return a.siteFocus(s, core.Command{})
	}
	a.panes.activePage[pane.ID] = views.ScreenPageID
	return core.Next(a.enterScreen(withFocus(s, siteScreen), *pane))
}

func (a *App) siteFocus(s core.State, _ core.Command) core.StepResult {
	if _, _, onProj := a.selectedRailProject(); onProj {
		return core.Next(withFocus(s, projectContextTable))
	}
	for _, n := range a.focusables(s) {
		if !strings.HasPrefix(n.id, tocPrefix) {
			continue
		}
		root := a.View(s)
		if entry := root.Find(n.id); entry != nil && entry.Facts["active"] == true {
			return core.Next(withFocus(s, n.id))
		}
	}
	return core.Next(a.column(s, 1))
}

func (a *App) siteTOCToggle(s core.State, _ core.Command) core.StepResult {
	show := !a.cfg.Settings.Bool(SetShowTOC, true)
	a.cfg.userSettings[SetShowTOC] = show
	a.reloadSettings()
	if !show && strings.HasPrefix(s.ActiveFrame().FocusID, tocPrefix) {
		a.viewMemo.gen++
		target := ""
		nodes := a.focusables(s)
		if slices.ContainsFunc(nodes, func(n fnode) bool { return n.id == siteScreen }) {
			target = siteScreen
		} else {
			for _, n := range nodes {
				if n.region == "site.content" {
					target = n.id
					break
				}
			}
		}
		if target != "" {
			s = withFocus(s, target)
		}
	}
	if show {
		a.say("pages rail shown")
	} else {
		a.say("pages rail hidden · t brings it back")
	}
	return core.Next(s)
}

func (a *App) termOf(pane string) *termBuf {
	if a.panes.term[pane] == nil {
		a.panes.term[pane] = &termBuf{}
	}
	return a.panes.term[pane]
}

// enterScreen goes into the pane's terminal, opening its live session first
// when the app has a host.
func (a *App) enterScreen(s core.State, pane data.Record) core.State {
	if err := a.attach(pane); err != nil {
		a.fail("open "+pane.Text("title"), err)
		return s
	}
	a.say("typing goes to the pane · ctrl+a d leaves")
	return withMode(s, ModeTerminal)
}

// termInput takes one key typed into the pane. With a live session the key
// goes to its program (or to Copy Mode); without one it is only echoed. Either
// way a submitted line is the owner's prompt: it moves the pane's promptedAt,
// and with it the rail.
func (a *App) termInput(s core.State, c core.Command) core.StepResult {
	pane, ok := a.termPane(s)
	if !ok {
		return core.Next(s)
	}
	key, text := c.Arg("key"), c.Arg("text")
	if a.panes.Term == nil || !a.hasSession(pane.ID) {
		a.typed(*pane, key, text, true)
		return core.Next(s)
	}
	if f := a.rt.live.frames[pane.ID]; f.Exited && key == "enter" {
		return core.Next(a.enterScreen(s, *pane)) // the program ended: start it again
	}
	var action ptyhost.Action
	if key == core.PasteKey {
		action = a.panes.Term.Paste(pane.ID, text)
	} else {
		action = a.panes.Term.Key(pane.ID, key)
		if _, unknown := action.(ptyhost.ActionUnhandled); unknown && text != "" {
			action = ptyhost.ActionSent{Data: []byte(text)}
			if err := a.panes.Host.Write(pane.ID, []byte(text)); err != nil {
				action = ptyhost.ActionError{Err: err}
			}
		}
	}
	next, effects, sent := a.act(s, *pane, action)
	if sent {
		a.typed(*pane, key, text, false)
	}
	return core.Next(next, effects...)
}

// typed keeps the line being typed into a pane and records a submitted one as
// the owner's prompt. echo also keeps the lines, for the Screen of a pane with
// no live session.
func (a *App) typed(pane data.Record, key, text string, echo bool) {
	buf := a.termOf(pane.ID)
	switch {
	case key == "enter":
		if echo {
			buf.lines = append(buf.lines, "❯ "+buf.typed)
			buf.lines = buf.lines[max(0, len(buf.lines)-200):]
		}
		if strings.TrimSpace(buf.typed) != "" {
			a.harvestPrompt(pane, buf.typed)
			if _, _, err := a.Store.RecordPaneInput(pane.Ref(), prompter); err != nil {
				a.fail("prompt", err)
			}
		}
		buf.typed = ""
	case key == "backspace":
		if r := []rune(buf.typed); len(r) > 0 {
			buf.typed = string(r[:len(r)-1])
		}
	case key == "space":
		buf.typed += " "
	case text != "":
		buf.typed += text
	case key == "ctrl+c" || key == "ctrl+u":
		buf.typed = ""
	}
}
