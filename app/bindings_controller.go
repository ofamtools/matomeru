package app

import (
	"ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/bindings"
)

// BindingsController owns the compiled keymap, binding layers (default,
// extension, user), command specs, and the session-persistent binding editor.
type BindingsController struct {
	Keymap          *core.Keymap
	bindings        []core.Binding
	specs           []core.CommandSpec
	userBindings    []core.Binding
	userBindingsRaw []core.Binding
	extBindings     []core.Binding
	bindEd          *bindings.Model
	bindUser        int
}

func newBindingsController() *BindingsController {
	return &BindingsController{}
}

// baseBindings returns the default keymap followed by any mounted extension
// bindings. User bindings.json rows are merged ahead of baseBindings so user
// configuration always takes precedence.
func (a *App) baseBindings() []core.Binding {
	if len(a.bind.extBindings) == 0 {
		return DefaultBindings()
	}
	defs := DefaultBindings()
	out := make([]core.Binding, 0, len(defs)+len(a.bind.extBindings))
	out = append(out, defs...)
	out = append(out, a.bind.extBindings...)
	return out
}

func filterValidUserBindings(user []core.Binding, specs []core.CommandSpec) []core.Binding {
	bad := map[int]bool{}
	for _, d := range config.CheckBindings(Modes(), user, specs...) {
		if d.Severity == config.SevError {
			bad[d.Index] = true
		}
	}
	var good []core.Binding
	for i, row := range user {
		if !bad[i] {
			good = append(good, row)
		}
	}
	return good
}
