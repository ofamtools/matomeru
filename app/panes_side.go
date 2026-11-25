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
)

const sideScreen = "site.side.screen"

func (a *App) sideTermSplitEnabled() bool {
	return a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetSideTermSplit, true)
}

// resolveSideParent walks parentPane links when from is itself a side split
// child so opening side vim from a side term swaps cleanly beside the same parent.
func (a *App) resolveSideParent(from data.Record) data.Record {
	cur := from
	seen := map[string]bool{cur.ID: true}
	for range 8 {
		role := cur.Text("role")
		if role != data.RoleSideTerm && role != data.RoleSideVim {
			return cur
		}
		parentRef, ok := cur.RefField("parentPane")
		if !ok || parentRef.Kind != data.KindPane || parentRef.ID == "" || seen[parentRef.ID] {
			return cur
		}
		parent, err := a.Store.Get(parentRef)
		if err != nil || parent == nil {
			return cur
		}
		seen[parent.ID] = true
		cur = *parent
	}
	return cur
}

func (a *App) findActiveSideChild(parentID, role string) (*data.Record, bool) {
	if parentID == "" || role == "" {
		return nil, false
	}
	res, err := a.Store.Query(data.Query{Kind: data.KindPane})
	if err != nil {
		return nil, false
	}
	for i := range res.Items {
		rec := res.Items[i]
		if rec.Text("role") != role || rec.IsArchived() {
			continue
		}
		if pRef, ok := rec.RefField("parentPane"); ok && pRef.Kind == data.KindPane && pRef.ID == parentID {
			return &rec, true
		}
	}
	return nil, false
}

func (a *App) activeSideSplit(parent data.Record) (*data.Record, bool) {
	if a.panes == nil || len(a.panes.sideSplit) == 0 {
		return nil, false
	}
	sideID := a.panes.sideSplit[parent.ID]
	if sideID == "" {
		return nil, false
	}
	rec, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: sideID})
	if err != nil || rec == nil || rec.IsArchived() {
		delete(a.panes.sideSplit, parent.ID)
		return nil, false
	}
	return rec, true
}

func (a *App) sideScreenSize() (int, int) {
	if a.Shell != nil {
		if r, ok := a.Shell.State.Layout.InnerRects[sideScreen]; ok && r.W > 0 && r.H > 0 {
			return r.W, r.H
		}
	}
	cols, rows := a.screenSize()
	return max(20, cols*45/100), rows
}

func (a *App) attachSide(side data.Record) error {
	cols, rows := a.sideScreenSize()
	prevShown := a.rt.live.shown
	err := a.attachSized(side, cols, rows)
	a.rt.live.shown = prevShown
	if err == nil && a.panes.Host != nil {
		a.rt.live.shownSide = side.ID
	}
	return err
}

// openSideSplit opens or reuses a per-pane side terminal or side editor split
// beside parent without switching the left rail away from parent.
func (a *App) openSideSplit(s core.State, from data.Record, command string) core.State {
	parent := a.resolveSideParent(from)
	name, role, kindLabel := command, data.RoleSideVim, "editor"
	if strings.TrimSpace(command) == "" {
		name, role, kindLabel = "shell", data.RoleSideTerm, "term"
		if sh := os.Getenv("SHELL"); sh != "" {
			name = filepath.Base(sh)
		}
	}

	side, found := a.findActiveSideChild(parent.ID, role)
	if !found {
		title, cwd := name, parent.Text("cwd")
		if cwd != "" {
			title = name + ": " + filepath.Base(cwd)
		}
		harness, _, _ := strings.Cut(name, " ")
		fields := map[string]any{
			"title":      title,
			"harness":    harness,
			"runStatus":  "running",
			"role":       role,
			"parentPane": parent.Ref(),
		}
		if cwd != "" && cwd != "." {
			fields["cwd"] = cwd
		}
		if command != "" {
			fields["command"] = command
		}
		if task, ok := parent.RefField("task"); ok {
			fields["task"] = task
		}
		rec, _, err := a.Store.Create(data.CreateInput{Kind: data.KindPane, Fields: fields}, User)
		if err != nil {
			a.fail("open side "+kindLabel, err)
			return s
		}
		side = rec
	}

	s = a.dropModals(s)
	targetSpace := SpacePanes
	if isProjectTab(s.ActivePage) && a.activeChildPaneID(s) == parent.ID {
		targetSpace = s.ActivePage
	} else if sel, ok := a.SelectedPane(); !ok || sel == nil || sel.ID != parent.ID {
		rail := a.rail()
		if !selectRow(rail, data.RefText(parent.Ref())) {
			rail.Query = ""
			selectRow(rail, data.RefText(parent.Ref()))
		}
	}
	a.panes.activePage[parent.ID] = views.ScreenPageID
	if a.panes.sideSplit == nil {
		a.panes.sideSplit = map[string]string{}
	}
	a.panes.sideSplit[parent.ID] = side.ID
	s.ActivePage = targetSpace
	if err := a.attachSide(*side); err != nil {
		a.fail("open "+side.Text("title"), err)
		return withPage(s, targetSpace, sideScreen, ModeNormal)
	}
	a.say("side %s split · ctrl+a o switches · ctrl+a x closes · ctrl+a d detaches", kindLabel)
	return withPage(s, targetSpace, sideScreen, ModeTerminal)
}

func (a *App) sideSplitSwitch(s core.State, _ core.Command) core.StepResult {
	pane, ok := a.activePane(s)
	if !ok {
		a.say("no pane is selected")
		return core.Next(s)
	}
	side, hasSide := a.activeSideSplit(*pane)
	if !hasSide {
		a.say("no side split open · ctrl+a t opens side term")
		return core.Next(s)
	}
	if s.ActiveFrame().FocusID == sideScreen {
		_ = a.attach(*pane)
		a.say("focused main screen · %s", pane.Text("title"))
		return core.Next(withMode(withFocus(s, siteScreen), ModeTerminal))
	}
	if err := a.attachSide(*side); err != nil {
		a.fail("open "+side.Text("title"), err)
		return core.Next(s)
	}
	a.say("focused side split · %s", side.Text("title"))
	return core.Next(withMode(withFocus(s, sideScreen), ModeTerminal))
}

func (a *App) sideSplitClose(s core.State, _ core.Command) core.StepResult {
	pane, ok := a.activePane(s)
	if !ok {
		a.say("no pane is selected")
		return core.Next(s)
	}
	if a.panes == nil || a.panes.sideSplit[pane.ID] == "" {
		a.say("no side split open")
		return core.Next(s)
	}
	delete(a.panes.sideSplit, pane.ID)
	a.rt.live.shownSide = ""
	if s.ActiveFrame().FocusID == sideScreen {
		s = withFocus(s, siteScreen)
	}
	a.say("side split closed")
	return core.Next(s)
}

// applySideSplit transforms site.content into a horizontal KindSplit when the
// selected pane is on its Screen page and has an active side split pane.
func (a *App) applySideSplit(site *ui.ViewNode, pane data.Record, s core.State) {
	side, ok := a.activeSideSplit(pane)
	if !ok {
		a.rt.live.shownSide = ""
		return
	}
	content := site.Find("site.content")
	screen := site.Find(siteScreen)
	if content == nil || screen == nil {
		return
	}

	mainPanel := ui.ViewNode{
		ID:       "site.screen.panel",
		Kind:     ui.KindPanel,
		Scope:    content.Scope,
		Ref:      content.Ref,
		Border:   true,
		Title:    content.Title,
		Badges:   slices.Clone(content.Badges),
		Facts:    content.Facts,
		Children: []ui.ViewNode{*screen},
	}

	sideTerm := ui.ViewNode{
		ID:        sideScreen,
		Kind:      ui.KindTerminal,
		Scope:     "terminal",
		Focusable: true,
		Props:     map[string]any{"streamId": side.ID},
	}
	sideTitle := views.Title(a.schema(data.KindPane), *side)
	roleLabel := "side term"
	if side.Text("role") == data.RoleSideVim {
		roleLabel = "side editor"
	}
	roleBadge := ui.Badge{Text: roleLabel, Tone: "info"}
	var sideBadges []ui.Badge
	if lines, cursor, live, hasLive := a.liveScreen(*side, sideScreen); hasLive {
		sideTerm.Props["lines"], sideTerm.Props["cursor"] = lines, cursor
		sideBadges = append([]ui.Badge{roleBadge}, live...)
		if f := a.rt.live.frames[side.ID]; f.PID > 0 && !f.Exited {
			sideTitle = fmt.Sprintf("%s · pid %d", sideTitle, f.PID)
		}
	} else {
		frame := s.Pages[SpacePanes]
		if isProjectTab(s.ActivePage) {
			frame = s.Pages[s.ActivePage]
		}
		typing := frame.Mode == ModeTerminal && frame.FocusID == sideScreen
		sideTerm.Props["lines"] = a.termLines(*side, typing)
		sideBadges = []ui.Badge{roleBadge, {Text: "not attached", Tone: "muted"}}
	}

	sideRef := side.Ref()
	sidePanel := ui.ViewNode{
		ID:       "site.side.panel",
		Kind:     ui.KindPanel,
		Scope:    data.KindPane,
		Ref:      &sideRef,
		Border:   true,
		Title:    sideTitle,
		Badges:   sideBadges,
		Facts:    map[string]any{"role": side.Text("role"), "sideSplit": true},
		Children: []ui.ViewNode{sideTerm},
	}

	content.Kind = ui.KindVStack
	content.Border = false
	content.Title = ""
	content.Badges = nil
	content.Children = []ui.ViewNode{
		{
			ID:       "site.screen.split",
			Kind:     ui.KindSplit,
			Ratio:    0.55,
			Gap:      1,
			Children: []ui.ViewNode{mainPanel, sidePanel},
		},
	}
}
