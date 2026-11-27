package app

import (
	"maps"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/modal"
)

const missingArgsModalID = "missing.args"

func staticBindingArgs(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	var out map[string]any
	for k, v := range src {
		if str, ok := v.(string); ok && strings.HasPrefix(str, "$") {
			continue
		}
		if out == nil {
			out = make(map[string]any, len(src))
		}
		out[k] = v
	}
	return out
}

func (a *App) lookupCommandSpec(id string) (core.CommandSpec, bool) {
	if a == nil || a.bind == nil || a.bind.Keymap == nil || a.bind.Keymap.Commands == nil {
		return core.CommandSpec{}, false
	}
	return a.bind.Keymap.Commands.Lookup(id)
}

func isInteractiveCommandSource(source string) bool {
	switch source {
	case "palette", "help", "picker-picker", "record.actions", "binding":
		return true
	}
	return false
}

func isEmptyParamValue(p core.ParamSpec, v any) bool {
	if v == nil {
		return true
	}
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x) == ""
	case core.Ref:
		return x.ID == ""
	case *core.Ref:
		return x == nil || x.ID == ""
	case []string:
		return len(x) == 0
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return p.Type == core.ParamString && strings.TrimSpace(data.Text(v)) == ""
}

func missingRequiredParams(params []core.ParamSpec, args map[string]any) []core.ParamSpec {
	var missing []core.ParamSpec
	for _, p := range params {
		if !p.Required {
			continue
		}
		if args != nil {
			if v, ok := args[p.Name]; ok && !isEmptyParamValue(p, v) {
				continue
			}
		}
		if p.Default != nil && !isEmptyParamValue(p, p.Default) {
			continue
		}
		missing = append(missing, p)
	}
	return missing
}

func hasParamNamed(params []core.ParamSpec, name string) bool {
	for _, p := range params {
		if p.Name == name {
			return true
		}
	}
	return false
}

func (a *App) interceptMissingRequiredArgs(s core.State, c core.Command) (core.StepResult, bool) {
	if c.Source == "mcp" || c.Source == "cli" {
		return core.StepResult{}, false
	}
	spec, ok := a.lookupCommandSpec(c.ID)
	if !ok || internal(spec) || len(spec.Params) == 0 {
		return core.StepResult{}, false
	}
	missing := missingRequiredParams(spec.Params, c.Args)
	if len(missing) == 0 {
		return core.StepResult{}, false
	}
	baseArgs, promptSpecs, allow := a.prepareMissingArgPrompt(s, c, spec, missing)
	if !allow {
		return core.StepResult{}, false
	}
	return core.Next(a.openMissingArgsForm(s, spec, c, baseArgs, promptSpecs)), true
}

func (a *App) prepareMissingArgPrompt(s core.State, c core.Command, spec core.CommandSpec, missing []core.ParamSpec) (map[string]any, []core.ParamSpec, bool) {
	baseArgs := maps.Clone(c.Args)
	if baseArgs == nil {
		baseArgs = map[string]any{}
	}
	promptSpecs := slices.Clone(missing)
	if !hasParamNamed(spec.Params, "table") {
		return baseArgs, a.enrichMissingArgSpecs(spec.ID, "", baseArgs, promptSpecs), true
	}
	tbl, tableID := a.focusedTableForPreset(s, c)
	if tbl == nil || tableID == "" {
		if !isInteractiveCommandSource(c.Source) {
			return nil, nil, false
		}
		promptSpecs = append([]core.ParamSpec{a.tableParamSpec()}, promptSpecs...)
		return baseArgs, a.enrichMissingArgSpecs(spec.ID, "", baseArgs, promptSpecs), true
	}
	if strings.TrimSpace(data.Text(baseArgs["table"])) == "" {
		baseArgs["table"] = tableID
	}
	return baseArgs, a.enrichMissingArgSpecs(spec.ID, tableID, baseArgs, promptSpecs), true
}

func (a *App) enrichMissingArgSpecs(cmdID, tableID string, baseArgs map[string]any, specs []core.ParamSpec) []core.ParamSpec {
	out := slices.Clone(specs)
	for i := range out {
		if out[i].Name == "name" && len(out[i].Enum) == 0 {
			if choices := a.presetNameChoices(cmdID, tableID, baseArgs); len(choices) > 0 {
				out[i].Enum = choices
				out[i].Default = choices[0]
			}
		}
	}
	return out
}

func (a *App) presetNameChoices(cmdID, tableID string, baseArgs map[string]any) []string {
	switch cmdID {
	case CmdTablePresetApply, CmdTablePresetDelete:
		if a.nav == nil || a.nav.presets == nil || tableID == "" {
			return nil
		}
		list := a.nav.presets.List(tableID)
		out := make([]string, 0, len(list))
		for _, p := range list {
			out = append(out, p.Name)
		}
		return out
	case CmdPresetApply:
		if a.genericPresets == nil {
			a.initGenericPresets()
		}
		list := a.genericPresets.List(strings.TrimSpace(data.Text(baseArgs["kind"])), "")
		out := make([]string, 0, len(list))
		for _, p := range list {
			out = append(out, p.Name)
		}
		return out
	}
	return nil
}

func (a *App) tableParamSpec() core.ParamSpec {
	choices := a.knownTableChoices()
	spec := core.ParamSpec{
		Name:        "table",
		Type:        core.ParamString,
		Required:    true,
		Enum:        choices,
		Description: "Target list or table",
	}
	if len(choices) > 0 {
		spec.Default = choices[0]
	}
	return spec
}

func (a *App) openMissingArgsForm(s core.State, spec core.CommandSpec, orig core.Command, baseArgs map[string]any, missing []core.ParamSpec) core.State {
	title := spec.ID
	if spec.Description != "" {
		title = spec.ID + " · " + clip(spec.Description, 42)
	}
	fm := form.New(title, missing, baseArgs)
	fm.SubmitRow = true
	fm.SubmitLabel = "Run " + spec.ID
	fm.Wrap = a.wrapInputs()
	if len(missing) > 0 && len(missing[0].Enum) == 0 && missing[0].Type == core.ParamString {
		fm.HandleCommand(core.Command{ID: form.CmdEdit})
	}
	var mod *modal.FormModal
	mod = modal.NewFormModal(fm, modal.FormOptions{
		ID:            missingArgsModalID,
		Scope:         "launch",
		Title:         "Required Arguments · " + spec.ID,
		WidthPct:      64,
		DisableDrafts: true,
		OnPickPath:    a.formPickPath(func() *modal.FormModal { return mod }),
		OnPickRef:     a.formPickRef(func() *modal.FormModal { return mod }),
		OnSubmit: func(vals map[string]any) modal.Result {
			merged := maps.Clone(baseArgs)
			if merged == nil {
				merged = map[string]any{}
			}
			for k, v := range vals {
				merged[k] = v
			}
			return modal.Result{
				Handled:     true,
				Close:       true,
				CloseReason: modal.CloseSubmit,
				Emit: []core.Command{{
					ID:     orig.ID,
					Args:   merged,
					Target: orig.Target,
					Source: orig.Source,
					Actor:  orig.Actor,
				}},
			}
		},
	})
	a.say("enter arguments for %s · ctrl+s runs · esc cancels", spec.ID)
	return a.pushModal(s, mod)
}

func (a *App) promptListTargetForCommand(s core.State, orig core.Command) core.StepResult {
	spec, ok := a.lookupCommandSpec(orig.ID)
	if !ok {
		spec = core.CommandSpec{ID: orig.ID, Description: "Choose a list to act on"}
	}
	baseArgs := maps.Clone(orig.Args)
	if baseArgs == nil {
		baseArgs = map[string]any{}
	}
	return core.Next(a.openMissingArgsForm(s, spec, orig, baseArgs, []core.ParamSpec{a.tableParamSpec()}))
}
