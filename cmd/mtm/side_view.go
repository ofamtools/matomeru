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
	stream := a.panesd.OpenStream(side)
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

// detailBlock keeps both live terminals inside the page's detail area.
func (a *App) detailBlock(layout ochazuke.Layout, width, height int) ochazuke.Block {
	if a.r.Terminal(paneTermID) == nil || a.r.Terminal(sideTermID) == nil {
		return a.parentDetailBlock(layout, width, height)
	}
	if width >= sideSplit {
		leftWidth := (width - 1) / 2
		left := a.parentDetailBlock(layout, leftWidth, height)
		right := a.sideDetailBlock(layout, width-leftWidth-1, height)
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
	topHeight := max(1, height/2)
	top := a.parentDetailBlock(layout, width, topHeight)
	bottom := a.sideDetailBlock(layout, width, height-topHeight)
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
	title := "side " + a.attached.SideFront + " · " + p.label()
	if e := term.Err(); e != "" {
		title += ": " + e
	}
	cw, ch := ochazuke.PanelContentSize(width, height)
	body := term.ViewBlock(cw, ch)
	if !term.Focused() {
		body.Cursor = nil
	}
	return layout.PanelBlock(ansi.Truncate(title, max(1, width-6), "…"), body, width, height, term.Focused())
}
