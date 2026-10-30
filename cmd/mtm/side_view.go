package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
)

const sideTermID = "pane-side-terminal"

type sideState struct {
	SideVim, SideTerm string
	ShowVim, ShowTerm bool
	SideFront         string
}

func (a *App) sideFields(p pane) pane {
	if s, ok := a.sides[p.ID]; ok {
		p.SideVim, p.SideTerm = s.SideVim, s.SideTerm
		p.ShowVim, p.ShowTerm, p.SideFront = s.ShowVim, s.ShowTerm, s.SideFront
	}
	return p
}

func (s *sideState) front() string {
	s.ShowVim = s.ShowVim && s.SideVim != ""
	s.ShowTerm = s.ShowTerm && s.SideTerm != ""
	switch {
	case s.SideFront == "vim" && s.ShowVim:
		return s.SideVim
	case s.SideFront == "term" && s.ShowTerm:
		return s.SideTerm
	case s.ShowVim:
		s.SideFront = "vim"
		return s.SideVim
	case s.ShowTerm:
		s.SideFront = "term"
		return s.SideTerm
	default:
		s.SideFront = ""
		return ""
	}
}

// showSide changes only the secondary stream; the parent's lease stays live.
func (a *App) showSide(parent pane, kind string) error {
	if a.attached.ID != parent.ID || a.r.Terminal(paneTermID) == nil {
		return errors.New("attach the parent pane before opening its side")
	}
	parent = a.sideFields(parent)
	s := sideState{parent.SideVim, parent.SideTerm, parent.ShowVim, parent.ShowTerm, parent.SideFront}
	if kind != "vim" && kind != "term" {
		return fmt.Errorf("unknown side kind %q", kind)
	}
	if a.r.Focus == sideTermID && s.SideFront == kind {
		return a.r.SetFocus(paneTermID)
	}
	id := s.SideVim
	if kind == "term" {
		id = s.SideTerm
	}
	if _, ok := a.companionPanes[id]; !ok || id == "" {
		return fmt.Errorf("side %s is no longer running", kind)
	}
	if kind == "vim" {
		s.ShowVim = true
	} else {
		s.ShowTerm = true
	}
	s.SideFront = kind
	if a.sides == nil {
		a.sides = map[string]sideState{}
	}
	a.sides[parent.ID] = s
	if err := a.syncSideView(); err != nil {
		return err
	}
	return a.r.SetFocus(sideTermID)
}

func (a *App) closeSideTerminal() error {
	if a.r.Instances[sideTermID] == nil {
		return nil
	}
	return a.r.RemoveInstance(sideTermID)
}

// syncSideView follows daemon reconciliation, including an exited front side.
func (a *App) syncSideView() error {
	if a.attached.ID == "" || a.r.Terminal(paneTermID) == nil {
		return a.closeSideTerminal()
	}
	// The attached pane's facts (state, cwd, meta) follow each listing, so
	// the live header stays current; the stream keeps its own copy.
	for _, q := range a.panes {
		if q.ID == a.attached.ID {
			a.attached = q
			break
		}
	}
	p := a.sideFields(a.attached)
	s := sideState{p.SideVim, p.SideTerm, p.ShowVim, p.ShowTerm, p.SideFront}
	if _, ok := a.companionPanes[s.SideVim]; !ok {
		s.SideVim = ""
	}
	if _, ok := a.companionPanes[s.SideTerm]; !ok {
		s.SideTerm = ""
	}
	id := s.front()
	if a.sides == nil {
		a.sides = map[string]sideState{}
	}
	a.sides[p.ID] = s
	a.attached = a.sideFields(p)
	if id == "" {
		return a.closeSideTerminal()
	}
	if inst := a.r.Instances[sideTermID]; inst != nil {
		if current, ok := inst.Data.(pane); ok && current.ID == id {
			inst.Data = a.companionPanes[id]
			return nil
		}
	}
	focused := a.r.Focus == sideTermID
	if err := a.closeSideTerminal(); err != nil {
		return err
	}
	side := a.companionPanes[id]
	stream := a.openStream(side)
	// The stream dials on its first read, which can come before the first
	// frame draws the side: give it the side's region now, so it opens
	// with that lease instead of at the window's size.
	if cols, rows := a.sideContentSize(); cols > 0 && rows > 0 {
		if err := stream.Resize(cols, rows); err != nil {
			stream.Release()
			return err
		}
	}
	a.trackStream(sideTermID, stream)
	if _, err := a.r.AddTerminal(sideTermID, stream, ochazuke.Parent(panesPage), ochazuke.FocusOnClose(paneTermID)); err != nil {
		stream.Release()
		return err
	}
	a.r.Instances[sideTermID].Data = side
	if focused {
		return a.r.SetFocus(sideTermID)
	}
	return nil
}

// sideDetach hides the front side, returns to its predecessor, then detaches.
func (a *App) sideDetach(ochazuke.Ctx) ochazuke.Result {
	p := a.sideFields(a.attached)
	s := sideState{p.SideVim, p.SideTerm, p.ShowVim, p.ShowTerm, p.SideFront}
	if s.front() != "" {
		if s.SideFront == "vim" {
			s.ShowVim = false
		} else {
			s.ShowTerm = false
		}
		s.front()
		a.sides[p.ID] = s
		if err := a.syncSideView(); err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(s.SideFront)
	}
	if err := a.closeSideTerminal(); err != nil {
		return ochazuke.Failed(err)
	}
	out, err := a.r.Execute(ochazuke.Request{Noun: "terminal", Verb: "detach", Args: ochazuke.Arguments{"id": paneTermID}})
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(out)
}

// sideSplit is the detail width from which a side pane sits right of its
// parent: each then keeps 80 columns. Narrower, the side stacks under the
// parent, both at the detail's full width.
const sideSplit = 161

// sideArea is how the detail area splits between the parent and its side:
// side by side (beside) or the side under the parent, and each panel's
// outer size.
type sideArea struct {
	beside                         bool
	parentW, parentH, sideW, sideH int
}

// The side-layout setting's values: auto splits at sideSplit, beside
// keeps the side right of its parent down to sideBesideMin columns each,
// and below always stacks it.
const (
	sideAuto   = "auto"
	sideBeside = "beside"
	sideBelow  = "below"
)

// sideBesideMin is the narrowest panel beside keeps side by side: two
// panels of 40 columns (36 of content each) and the gap between them.
// A narrower detail stacks the side under its parent even in beside.
const sideBesideMin = 40

// sideLayout is the side-layout setting, auto when unset or unknown.
func (a *App) sideLayout() string {
	switch v := a.r.Settings().String(settingSideLayout); v {
	case sideBeside, sideBelow:
		return v
	}
	return sideAuto
}

func (a *App) sideArea(width, height int) sideArea {
	beside := width >= sideSplit
	switch a.sideLayout() {
	case sideBeside:
		beside = width >= 2*sideBesideMin+1
	case sideBelow:
		beside = false
	}
	if beside {
		leftWidth := (width - 1) / 2
		return sideArea{beside: true, parentW: leftWidth, parentH: height, sideW: width - leftWidth - 1, sideH: height}
	}
	topHeight := max(1, height/2)
	return sideArea{parentW: width, parentH: topHeight, sideW: width, sideH: height - topHeight}
}

// focusOther is terminal.other (leader o, tmux's prefix o): the focus
// moves to the next live terminal shown, the parent then its side,
// wrapping. With no side shown there is no other, and it does nothing.
func (a *App) focusOther(ochazuke.Ctx) ochazuke.Result {
	if a.r.Terminal(sideTermID) == nil || a.r.Terminal(paneTermID) == nil {
		return ochazuke.Done(nil)
	}
	next := sideTermID
	if a.r.Focus == sideTermID {
		next = paneTermID
	}
	if err := a.r.SetFocus(next); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(next)
}

// flipLayout is terminal.layout (leader R, after tuios's rotate split):
// beside becomes below and below beside; from auto it takes the
// orientation opposite the one shown. It saves the side-layout setting,
// and the next frame sizes both terminals to their new regions.
func (a *App) flipLayout(ctx ochazuke.Ctx) ochazuke.Result {
	next := sideBeside
	switch a.sideLayout() {
	case sideBeside:
		next = sideBelow
	case sideAuto:
		if a.pv.detailW >= sideSplit {
			next = sideBelow
		}
	}
	if err := a.r.Settings().Set(settingSideLayout, next); err != nil {
		return ochazuke.Failed(err)
	}
	ctx.Notify("side layout: " + next)
	return ochazuke.Done(next)
}

// sideContentSize is the side panel's body in the detail area the last
// frame drew (0, 0 before one): what the side's pane is sized to.
func (a *App) sideContentSize() (cols, rows int) {
	if a.pv.detailW <= 0 || a.pv.detailH <= 0 {
		return 0, 0
	}
	area := a.sideArea(a.pv.detailW, a.pv.detailH)
	return ochazuke.PanelContentSize(area.sideW, area.sideH)
}

// detailBlock keeps both live terminals inside the page's detail area.
func (a *App) detailBlock(layout ochazuke.Layout, width, height int) ochazuke.Block {
	a.pv.detailW, a.pv.detailH = width, height
	if !a.liveShown() || a.r.Terminal(sideTermID) == nil {
		return a.parentDetailBlock(layout, width, height)
	}
	area := a.sideArea(width, height)
	if area.beside {
		leftWidth := area.parentW
		left := a.parentDetailBlock(layout, leftWidth, height)
		right := a.sideDetailBlock(layout, area.sideW, height)
		leftLines, rightLines := strings.Split(left.Text, "\n"), strings.Split(right.Text, "\n")
		lines := make([]string, height)
		for i := range height {
			l, r := "", ""
			if i < len(leftLines) {
				l = leftLines[i]
			}
			if i < len(rightLines) {
				r = rightLines[i]
			}
			lines[i] = ansi.Truncate(l, leftWidth, "") + strings.Repeat(" ", max(0, leftWidth-ansi.StringWidth(l))+1) + r
		}
		block := ochazuke.Text(strings.Join(lines, "\n"))
		if left.Cursor != nil {
			block.Cursor = left.Cursor
		} else {
			block.Cursor = right.Offset(leftWidth+1, 0).Cursor
		}
		return block.Clip(width, height)
	}
	topHeight := area.parentH
	top := a.parentDetailBlock(layout, width, topHeight)
	bottom := a.sideDetailBlock(layout, width, area.sideH)
	block := ochazuke.Text(top.Text + "\n" + bottom.Text)
	if top.Cursor != nil {
		block.Cursor = top.Cursor
	} else {
		block.Cursor = bottom.Offset(0, topHeight).Cursor
	}
	return block.Clip(width, height)
}

func (a *App) sideDetailBlock(layout ochazuke.Layout, width, height int) ochazuke.Block {
	term := a.r.Terminal(sideTermID)
	p, _ := a.r.Instances[sideTermID].Data.(pane)
	title := a.streamTitle(sideTermID, "side "+a.attached.SideFront+" · "+p.label())
	if e := term.Err(); e != "" {
		title += ": " + e
	}
	cw, ch := ochazuke.PanelContentSize(width, height)
	if a.copyShown(sideTermID) {
		return a.panePanel(layout, a.copy.pane).PanelBlock(ansi.Truncate("side · "+a.copyTitle(), max(1, width-6), "…"), a.copyBlock(cw, ch), width, height, true)
	}
	body := term.ViewBlock(cw, ch)
	if !term.Focused() {
		body.Cursor = nil
	}
	return a.panePanel(layout, p).PanelBlock(ansi.Truncate(title, max(1, width-6), "…"), body, width, height, term.Focused())
}

const sideChooser = "side-chooser"

// sideChoice is one side chooser row: the parent and which side.
type sideChoice struct {
	parent pane
	kind   string
}

// chooseSide is panes.sides (leader w, live and on the Panes page): a
// chooser of every open side across every pane, "side vim · parent" or
// "side term · parent". The links come from the last listing
// (mergeCompanions), so only sides whose windows and parents this server
// lists are there; an exited parent or side is left out. Enter attaches
// the parent, shows that side and focuses it (jumpToSide).
func (a *App) chooseSide(ctx ochazuke.Ctx) ochazuke.Result {
	var rows []ochazuke.ChooserRow
	for _, p := range a.panes {
		s, ok := a.sides[p.ID]
		if !ok || p.Exited {
			continue
		}
		for _, side := range []struct{ kind, id string }{{"vim", s.SideVim}, {"term", s.SideTerm}} {
			if c, live := a.companionPanes[side.id]; side.id == "" || !live || c.Exited {
				continue
			}
			rows = append(rows, ochazuke.ChooserRow{ID: p.ID + "/" + side.kind, Label: "side " + side.kind + " · " + p.label(), Data: sideChoice{parent: p, kind: side.kind}})
		}
	}
	if len(rows) == 0 {
		ctx.Notify("no side panes are open")
		return ochazuke.Done(nil)
	}
	_, err := a.r.OpenChooser(ochazuke.ChooserSpec{
		Kind:  sideChooser,
		Title: "Side panes",
		Rows:  rows,
		Choose: func(row ochazuke.ChooserRow) ochazuke.Result {
			c := row.Data.(sideChoice)
			if err := a.jumpToSide(c.parent, c.kind); err != nil {
				return ochazuke.Failed(err)
			}
			return ochazuke.Done(row.Label)
		},
	})
	return resultError(err)
}

// jumpToSide selects the parent on the Panes page, attaches it live
// unless it already is, shows the side and focuses it.
func (a *App) jumpToSide(parent pane, kind string) error {
	if err := a.r.OpenPage(panesPage); err != nil {
		return err
	}
	if a.attached.ID != parent.ID || a.r.Terminal(paneTermID) == nil {
		if err := a.attachLive(parent, parent.rowID()); err != nil {
			return err
		}
	}
	if err := a.selectPaneRow(parent.rowID()); err != nil {
		return err
	}
	if a.r.Focus == sideTermID && a.attached.SideFront == kind {
		return nil // there already; showSide would hand the focus back
	}
	return a.showSide(a.attached, kind)
}
