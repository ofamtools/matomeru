package app

import (
	"fmt"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// confirmState holds the active reusable confirmation dialog state.
type confirmState struct {
	title        string
	message      string
	confirmLabel string
	onConfirm    func(core.State) core.State
}

// openConfirm opens the single shared confirmation overlay in ModeConfirm.
func (a *App) openConfirm(s core.State, cs confirmState) core.State {
	if cs.title == "" {
		cs.title = "Confirm"
	}
	if cs.confirmLabel == "" {
		cs.confirmLabel = "Confirm"
	}
	cp := cs
	a.confirm = &cp
	a.say("y/enter confirms · n/esc cancels")
	return openModal(s, modalConfirm, ModeConfirm, modalConfirm+".body")
}

func (a *App) confirmModal() ui.ViewNode {
	cs := a.confirm
	if cs == nil {
		return overlay(modalConfirm, "Confirm", 56, 0, text(modalConfirm+".body", "Nothing to confirm.", "muted"))
	}
	label := cs.confirmLabel
	if label == "" {
		label = "Confirm"
	}
	body := ui.ViewNode{
		ID:        modalConfirm + ".body",
		Kind:      ui.KindVStack,
		Focusable: true,
		Gap:       1,
		Children: []ui.ViewNode{
			text(modalConfirm+".message", cs.message, ""),
			{
				ID:    modalConfirm + ".actions",
				Kind:  ui.KindText,
				Tone:  "accent",
				Box:   layout.Constraints{FixedH: 1},
				Props: map[string]any{"text": fmt.Sprintf("[y/enter] %s   [n/esc] Cancel", label)},
			},
		},
	}
	ov := overlay(modalConfirm, cs.title, 70, 0, body)
	ov.Scope = "confirm"
	ov.Tone = "warning"
	return ov
}

func (a *App) confirmAccept(s core.State, _ core.Command) core.StepResult {
	cs := a.confirm
	a.confirm = nil
	s = closeModal(s, modalConfirm)
	if cs != nil && cs.onConfirm != nil {
		s = cs.onConfirm(s)
	}
	return core.Next(s)
}

func (a *App) confirmCancel(s core.State, _ core.Command) core.StepResult {
	a.confirm = nil
	s = closeModal(s, modalConfirm)
	a.say("cancelled")
	return core.Next(s)
}

// appQuit handles core.CmdAppQuit (Q / ctrl+c): prompts with the shared
// confirmation dialog when confirm.quit is enabled, otherwise quits at once.
func (a *App) appQuit(s core.State, _ core.Command) core.StepResult {
	if !a.Settings.Bool(SetConfirmQuit, true) {
		return core.Next(s, core.QuitEffect{})
	}
	return core.Next(a.openConfirm(s, confirmState{
		title:        "Quit Matomeru",
		message:      "Quit Matomeru? Your daemon panes will keep running in the background.",
		confirmLabel: "Confirm",
		onConfirm: func(st core.State) core.State {
			a.effect(core.QuitEffect{})
			return st
		},
	}))
}
