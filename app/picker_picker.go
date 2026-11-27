package app

import (
	"fmt"
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
)

const (
	CmdPickerPicker      = "picker.picker"
	pickerPickerModalID  = "picker.picker"
	pickerVerbsOptionID  = "__verbs__"
	pickerVerbsVerbModal = "picker.picker.verbs"
)

func pickerPickerSpecs() []core.CommandSpec {
	return []core.CommandSpec{
		spec(CmdPickerPicker, "Picker-Picker: pick any noun (or Verbs) then pick a verb and fill its arguments"),
	}
}

func pickerPickerBindings() []core.Binding {
	return []core.Binding{
		bind(ModeNormal, "", "g V", CmdPickerPicker, "picker-picker (noun/verb)"),
	}
}

func (a *App) registerPickerPickerHandlers() {
	a.handle(CmdPickerPicker, a.openPickerPicker)
}

func (a *App) openGenericPresetPicker(s core.State, presets []data.Preset) core.StepResult {
	items := make([]picker.Item, 0, len(presets))
	byID := map[string]data.Preset{}
	for _, p := range presets {
		byID[p.ID] = p
		sub := p.Kind
		if p.Scope != "" {
			sub += " · " + p.Scope
		}
		items = append(items, picker.Item{ID: p.ID, Title: p.Name, Subtitle: sub})
	}
	pm := picker.New("Generic Presets", items)
	pm.MatchMode = a.matchMode()
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       "presets.generic",
		PageSize: 12,
		OnConfirm: func(it picker.Item) modal.Result {
			if p, ok := byID[it.ID]; ok {
				a.applyGenericPresetToFocused(a.State(), p)
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit, Status: "applied preset " + p.Name, SetStatus: true}
			}
			return modal.Result{Handled: true, Close: true}
		},
	})
	return core.Next(a.pushModal(s, m))
}

// openPickerPicker opens Level 1 of the Picker-Picker: choose "Verbs" (including
// cycle verbs & all command verbs) OR any registered Noun, then drill into its
// verbs and open an argument form when the chosen verb declares parameters.
func (a *App) openPickerPicker(s core.State, _ core.Command) core.StepResult {
	items := []picker.Item{
		{
			ID:       pickerVerbsOptionID,
			Title:    "Verbs (all actions & cyclers)",
			Subtitle: "browse every verb directly (e.g. cycle, sort, filter, preset)",
			Badges:   []ui.Badge{{Text: "verbs", Tone: "accent"}},
		},
	}
	for _, sc := range data.DefaultSchemas().List() {
		items = append(items, picker.Item{
			ID:       sc.Kind,
			Title:    sc.Title,
			Subtitle: fmt.Sprintf("Noun %s · pick verb", sc.Kind),
			Badges:   []ui.Badge{{Text: sc.Kind, Tone: "info"}},
		})
	}
	pm := picker.New("Picker-Picker · Choose Noun or Verbs", items)
	pm.MatchMode = a.matchMode()
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       pickerPickerModalID,
		PageSize: 14,
		OnConfirm: func(it picker.Item) modal.Result {
			target := it.ID
			a.afterModal(func(st core.State) core.State {
				return a.openPickerPickerVerbs(st, target)
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	return core.Next(a.pushModal(s, m))
}

func (a *App) openPickerPickerVerbs(s core.State, target string) core.State {
	specs := CommandSpecs()
	byID := map[string]core.CommandSpec{}
	var items []picker.Item
	title := "Verbs"
	if target != pickerVerbsOptionID {
		title = "Verbs for " + a.schema(target).Title
	}
	for _, sp := range specs {
		if internal(sp) {
			continue
		}
		if target != pickerVerbsOptionID && !verbMatchesNoun(sp, target) {
			continue
		}
		byID[sp.ID] = sp
		var badges []ui.Badge
		if len(sp.Params) > 0 {
			badges = append(badges, ui.Badge{Text: fmt.Sprintf("%d args", len(sp.Params)), Tone: "info"})
		}
		if strings.Contains(sp.ID, "cycle") {
			badges = append(badges, ui.Badge{Text: "cycle", Tone: "accent"})
		}
		items = append(items, picker.Item{
			ID:       sp.ID,
			Title:    sp.ID,
			Subtitle: sp.Description,
			Badges:   badges,
		})
	}
	pm := picker.New(title, items)
	pm.MatchMode = a.matchMode()
	m := a.newPickerModal(pm, modal.PickerOptions{
		ID:       pickerVerbsVerbModal,
		PageSize: 14,
		OnConfirm: func(it picker.Item) modal.Result {
			sp, ok := byID[it.ID]
			if !ok {
				return modal.Result{Handled: true, Close: true}
			}
			if len(sp.Params) == 0 {
				return modal.Result{
					Handled:     true,
					Close:       true,
					CloseReason: modal.CloseSubmit,
					Emit:        []core.Command{{ID: sp.ID, Source: "picker-picker", Actor: User}},
				}
			}
			a.afterModal(func(st core.State) core.State {
				return a.openPickerVerbArgForm(st, sp)
			})
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	return a.pushModal(s, m)
}

func (a *App) openPickerVerbArgForm(s core.State, sp core.CommandSpec) core.State {
	fm := form.New(sp.ID, sp.Params, nil)
	fm.SubmitRow = true
	fm.SubmitLabel = "Run " + sp.ID
	mod := modal.NewFormModal(fm, modal.FormOptions{
		ID:       "picker.verb.args",
		Title:    "Arguments · " + sp.ID,
		WidthPct: 60,
		OnSubmit: func(vals map[string]any) modal.Result {
			return modal.Result{
				Handled:     true,
				Close:       true,
				CloseReason: modal.CloseSubmit,
				Emit:        []core.Command{{ID: sp.ID, Args: vals, Source: "picker-picker", Actor: User}},
			}
		},
	})
	return a.pushModal(s, mod)
}

func verbMatchesNoun(sp core.CommandSpec, noun string) bool {
	if strings.HasPrefix(sp.ID, noun+".") {
		return true
	}
	if sp.Meta != nil {
		if kinds, ok := sp.Meta["kinds"].([]string); ok && len(kinds) > 0 {
			return slices.Contains(kinds, noun)
		}
		if rec, _ := sp.Meta["record"].(bool); rec {
			// Generic record verbs (open, edit, star, archive, yank, ...) apply to every noun!
			return true
		}
	}
	return false
}
