package app

import (
	"strings"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/bindings"
	"ofam.dev/ochazuke/widgets/modal"
)

// The binding editor's keyboard-layout view: the editor model owns the view
// mode, the prefix layer and shift; the app only binds keys, registers specs
// and routes the commands to the modal.

const (
	bindingsKeyboard = "bindings.keyboard" // the keyboard view's focusable header
	whenBindingsKbd  = `facts.viewMode == "keyboard"`
	whenBindingsRows = `facts.viewMode != "keyboard"`
)

// bindingsKeyboardCommands are the editor commands the modal forwards to the
// editor model.
var bindingsKeyboardCommands = []string{
	bindings.CmdViewToggle, bindings.CmdPrefixNext, bindings.CmdPrefixPrev, bindings.CmdPrefixSelect,
	bindings.CmdPrefixClear, bindings.CmdShiftToggle, bindings.CmdKeySelect,
}

func bindingsKeyboardBindings() []core.Binding {
	m, all := ModeBindings, ""
	return []core.Binding{
		bind(m, all, "v", bindings.CmdViewToggle, "table / keyboard"),
		when(bind(m, all, "]", bindings.CmdPrefixNext, "next layer"), whenBindingsKbd),
		when(bind(m, all, "[", bindings.CmdPrefixPrev, "previous layer"), whenBindingsKbd),
		when(bind(m, all, "0", bindings.CmdPrefixClear, "root layer"), whenBindingsKbd),
		when(bind(m, all, "s", bindings.CmdShiftToggle, "shift"), whenBindingsKbd),
	}
}

func bindingsKeyboardSpecs() []core.CommandSpec {
	internal := map[string]any{"internal": true}
	str := func(name string) []core.ParamSpec { return []core.ParamSpec{{Name: name, Type: core.ParamString}} }
	return []core.CommandSpec{
		{ID: bindings.CmdViewToggle, Description: "Switch the binding editor between the table and the keyboard layout", Meta: internal},
		{ID: bindings.CmdPrefixNext, Description: "Show the next prefix layer on the keyboard", Meta: internal},
		{ID: bindings.CmdPrefixPrev, Description: "Show the previous prefix layer on the keyboard", Meta: internal},
		{ID: bindings.CmdPrefixSelect, Description: "Show one prefix layer on the keyboard", Params: str("prefix"), Meta: internal},
		{ID: bindings.CmdPrefixClear, Description: "Show the keyboard's root layer", Meta: internal},
		{ID: bindings.CmdShiftToggle, Description: "Show the shifted keys on the keyboard", Meta: internal},
		{ID: bindings.CmdKeySelect, Description: "Inspect one key on the keyboard", Params: str("key"), Meta: internal},
	}
}

// bindingsKeyboardOn reports whether the editor shows the keyboard layout.
func bindingsKeyboardOn(ed *bindings.Model) bool {
	return ed != nil && ed.ViewMode == bindings.ViewKeyboard
}

// bindingsViewBadge tells the owner which view is on and how to switch.
func bindingsViewBadge(ed *bindings.Model) ui.Badge {
	if bindingsKeyboardOn(ed) {
		return ui.Badge{Text: "keyboard · [ ] layer · s shift · 0 root · v table", Tone: "accent"}
	}
	return ui.Badge{Text: "v keyboard"}
}

func (a *App) registerBindingsKeyboard() {
	for _, id := range bindingsKeyboardCommands {
		a.handle(id, a.handleModal)
	}
}

func (m *bindingsModal) handleEditorCommand(ed *bindings.Model, c core.Command) modal.Result {
	if c.ID == bindings.CmdViewToggle {
		ed.HandleCommand(c)
		return modal.Result{Handled: true, Mode: m.Mode(), FocusID: m.FocusID()}
	}
	if ed.HandleCommand(c) || strings.HasPrefix(c.ID, "motion.") {
		return modal.Result{Handled: true}
	}
	return modal.Result{}
}
