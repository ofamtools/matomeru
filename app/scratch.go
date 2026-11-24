package app

import (
	"fmt"
	"os"
	"path/filepath"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/handlers"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
)

// The two global scratches: one terminal and one editor on a file Matomeru
// keeps, each a pane record like any other (its role says which), made the
// first time it is asked for. A scratch opens as an overlay over whatever
// space is shown and closes the same way, so the space and its focus are
// where they were.

const (
	scratchFileName = "scratch.md"
	dataDirName     = "matomeru"
)

// scratchModal implements modal.Modal for the global scratch terminal/notes overlay.
type scratchModal struct {
	a      *App
	paneID string
	role   string
}

var _ modal.Modal = (*scratchModal)(nil)

func (a *App) activeScratch() *scratchModal {
	if a.modals == nil {
		return nil
	}
	m, _ := a.modals.Get("scratch").(*scratchModal)
	return m
}

func (m *scratchModal) ID() string      { return "scratch" }
func (m *scratchModal) Scope() string   { return data.KindPane }
func (m *scratchModal) Mode() string    { return ModeTerminal }
func (m *scratchModal) FocusID() string { return scratchScreen }
func (m *scratchModal) OnClose(modal.CloseReason) {
}

func (m *scratchModal) Handle(cmd core.Command) modal.Result {
	switch cmd.ID {
	case CmdTermDetach:
		return modal.Result{
			Handled:     true,
			Close:       true,
			CloseReason: modal.CloseSubmit,
			Status:      "scratch hidden",
			SetStatus:   true,
		}
	case modal.CmdCancel:
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseCancel}
	}
	return modal.Result{}
}

// View draws the shown scratch: its live session, or, without one, the lines
// typed into it.
func (m *scratchModal) View(_, _ int) ui.ViewNode {
	ref := core.Ref{Kind: data.KindPane, ID: m.paneID}
	pane, err := m.a.Store.Get(ref)
	if err != nil {
		return modal.Overlay(modal.OverlaySpec{
			ID:        "scratch",
			Scope:     data.KindPane,
			Title:     "Scratch",
			WidthPct:  90,
			HeightPct: 85,
			Children:  []ui.ViewNode{text("", "The scratch pane is gone: "+err.Error(), "warning")},
		})
	}
	screen := ui.ViewNode{ID: scratchScreen, Kind: ui.KindTerminal, Scope: "terminal", Focusable: true, Props: map[string]any{"streamId": pane.ID}}
	badges := []ui.Badge{{Text: "not attached", Tone: "muted"}}
	title := pane.Text("title")
	if lines, cursor, live, ok := m.a.liveScreen(*pane, scratchScreen); ok {
		screen.Props["lines"], screen.Props["cursor"] = lines, cursor
		badges = live
		if f := m.a.rt.live.frames[pane.ID]; f.PID > 0 && !f.Exited {
			title = fmt.Sprintf("%s · pid %d", title, f.PID)
		}
	} else {
		screen.Props["lines"] = m.a.termLines(*pane, true)
	}
	return modal.Overlay(modal.OverlaySpec{
		ID:        "scratch",
		Scope:     data.KindPane,
		Title:     title,
		WidthPct:  90,
		HeightPct: 85,
		Ref:       &ref,
		Facts:     map[string]any{"role": pane.Text("role"), "scratch": true},
		Badges:    append(badges, ui.Badge{Text: "global scratch", Tone: "accent"}),
		Children:  []ui.ViewNode{screen},
	})
}

// dataDir is where Matomeru keeps its own files.
func (a *App) dataDir() string {
	if a.opts.DataDir != "" {
		return a.opts.DataDir
	}
	if base := os.Getenv("XDG_DATA_HOME"); base != "" {
		return filepath.Join(base, dataDirName)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", dataDirName)
}

// ScratchFile is the file the global scratch editor opens.
func (a *App) ScratchFile() string { return filepath.Join(a.dataDir(), scratchFileName) }

// scratchPane returns the pane of a global scratch, recording it the first
// time. There is one per role: an archived one is found again, not doubled.
func (a *App) scratchPane(role string) (*data.Record, error) {
	res, err := a.Store.Query(data.Query{Kind: data.KindPane, IncludeArchived: true})
	if err != nil {
		return nil, err
	}
	for i := range res.Items {
		if res.Items[i].Text("role") == role {
			return &res.Items[i], nil
		}
	}
	fields := map[string]any{"role": role, "runStatus": "unknown"}
	switch role {
	case data.RoleScratchVim:
		file := a.ScratchFile()
		fields["title"], fields["harness"] = "Scratch notes", editorCommand()
		fields["command"] = editorCommand() + " " + handlers.Quote(file)
		fields["cwd"], fields["filePath"] = filepath.Dir(file), file
	default:
		fields["title"], fields["harness"] = "Scratch terminal", "shell"
		if home, err := os.UserHomeDir(); err == nil {
			fields["cwd"] = home
		}
	}
	rec, _, err := a.Store.Create(data.CreateInput{Kind: data.KindPane, Fields: fields}, scratchActor)
	return rec, err
}

// scratchActor records the scratch panes. They are not the owner's edits:
// "u" never takes one back.
var scratchActor = core.Actor{Kind: "system", ID: "mtm.scratch", Label: "scratch"}

// scratchSize is the size a scratch session starts at: the overlay's.
func (a *App) scratchSize() (int, int) {
	if a.Shell != nil {
		if r, ok := a.Shell.State.Layout.InnerRects[scratchScreen]; ok && r.W > 0 && r.H > 0 {
			return r.W, r.H
		}
		if a.Shell.W > 20 && a.Shell.H > 10 {
			return a.Shell.W*90/100 - 4, a.Shell.H*85/100 - 4
		}
	}
	return 80, 24
}

// scratchToggle shows a global scratch over the current space, or hides it.
// Asking for the other scratch while one is shown switches to it.
func (a *App) scratchToggle(s core.State, c core.Command) core.StepResult {
	role := c.Arg("role")
	if role != data.RoleScratchVim {
		role = data.RoleScratchTerm
	}
	if sm := a.activeScratch(); sm != nil {
		same := sm.role == role
		s, _ = a.modals.Close(s, "scratch", modal.CloseCancel)
		if same {
			a.say("scratch hidden")
			return core.Next(s)
		}
	}
	pane, err := a.scratchPane(role)
	if err != nil {
		a.fail("scratch", err)
		return core.Next(s)
	}
	if a.panes.Host != nil && role == data.RoleScratchVim {
		// The editor opens a file Matomeru keeps: it is there before it does.
		file := pane.Text("filePath")
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			a.fail("scratch", err)
			return core.Next(s)
		}
		if f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			f.Close()
		}
	}
	cols, rows := a.scratchSize()
	if err := a.attachSized(*pane, cols, rows); err != nil {
		a.fail("open "+pane.Text("title"), err)
		return core.Next(s)
	}
	key := map[string]string{data.RoleScratchTerm: "S", data.RoleScratchVim: "V"}[role]
	a.say("%s · ctrl+a %s hides it", pane.Text("title"), key)
	return core.Next(a.pushModal(s, &scratchModal{a: a, paneID: pane.ID, role: role}))
}

// termPane is the pane keys typed in terminal mode go to: the scratch that
// is shown, the active side split pane when sideScreen is focused, or the pane
// the rail has selected.
func (a *App) termPane(s core.State) (*data.Record, bool) {
	if sm := a.activeScratch(); sm != nil && sm.paneID != "" {
		rec, err := a.Store.Get(core.Ref{Kind: data.KindPane, ID: sm.paneID})
		return rec, err == nil
	}
	if s.ActiveFrame().FocusID == sideScreen {
		if parent, ok := a.SelectedPane(); ok {
			if side, hasSide := a.activeSideSplit(*parent); hasSide {
				return side, true
			}
		}
	}
	return a.SelectedPane()
}
