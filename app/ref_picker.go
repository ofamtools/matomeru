package app

import (
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/widgets/form"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
)

func parsePickedRef(raw any) (core.Ref, bool) {
	if r, ok := data.AsRef(raw); ok {
		return r, r.Kind != "" && r.ID != ""
	}
	s := strings.TrimSpace(data.Text(raw))
	if kind, id, ok := strings.Cut(s, ":"); ok && kind != "" && id != "" {
		return core.Ref{Kind: kind, ID: id}, true
	}
	return core.Ref{}, false
}

func currentPickedRefs(v any) []core.Ref {
	var out []core.Ref
	seen := map[core.Ref]bool{}
	add := func(r core.Ref) {
		if r.Kind == "" || r.ID == "" || seen[r] {
			return
		}
		seen[r] = true
		out = append(out, r)
	}
	addRaw := func(raw any) {
		if r, ok := parsePickedRef(raw); ok {
			add(r)
		}
	}
	switch x := v.(type) {
	case []core.Ref:
		for _, r := range x {
			add(r)
		}
	case []string:
		for _, s := range x {
			addRaw(s)
		}
	case []any:
		for _, item := range x {
			addRaw(item)
		}
	default:
		addRaw(v)
	}
	return out
}

func (a *App) formPickRef(getForm func() *modal.FormModal) func(form.RefRequest) modal.Result {
	return func(req form.RefRequest) modal.Result {
		field := req.Field
		if req.Repeatable {
			var selected []core.Ref
			if fm := getForm(); fm != nil && fm.Model() != nil {
				selected = currentPickedRefs(fm.Model().Values[field])
			}
			return modal.Result{
				Handled: true,
				Push: a.newRefPickerModalWith(req, selected, nil, func(refs []core.Ref) {
					if fm := getForm(); fm != nil {
						vals := make([]string, 0, len(refs))
						for _, r := range refs {
							vals = append(vals, data.RefText(r))
						}
						fm.SetValue(field, vals)
						if fm.Model().ActiveFieldName() != field {
							fm.FocusField(field)
						}
					}
				}),
			}
		}
		return modal.Result{
			Handled: true,
			Push: a.newRefPickerModal(req, func(r core.Ref) {
				if fm := getForm(); fm != nil {
					fm.SetValue(field, r)
					if fm.Model().ActiveFieldName() != field {
						fm.FocusField(field)
					}
				}
			}),
		}
	}
}

func (a *App) newRefPickerModal(req form.RefRequest, onPick func(core.Ref)) *modal.PickerModal {
	return a.newRefPickerModalWith(req, nil, onPick, nil)
}

func (a *App) queryRefPickerRecords(kinds []string) ([]string, []data.Record) {
	if len(kinds) == 0 {
		for _, sc := range a.Store.Registry().List() {
			kinds = append(kinds, sc.Kind)
		}
	}
	var recs []data.Record
	for _, kind := range kinds {
		res, err := a.Store.Query(data.Query{Kind: kind})
		if err != nil {
			a.fail("read "+kind, err)
			continue
		}
		recs = append(recs, res.Items...)
	}
	return kinds, recs
}

func markedPickerRefs(pm *picker.Model, fallback *core.Ref) []core.Ref {
	var refs []core.Ref
	seen := map[core.Ref]bool{}
	for _, it := range pm.MarkedItems() {
		if it.Ref != nil && !seen[*it.Ref] {
			seen[*it.Ref] = true
			refs = append(refs, *it.Ref)
		}
	}
	if len(refs) == 0 && fallback != nil {
		refs = append(refs, *fallback)
	}
	return refs
}

func (a *App) refPickerCreateHook(kinds []string, repeatable bool, pm *picker.Model, onPick func(core.Ref), onPickMany func([]core.Ref)) *modal.PickerCreateHook {
	if len(kinds) != 1 {
		return nil
	}
	if _, known := a.Store.Registry().Get(kinds[0]); !known {
		return nil
	}
	if repeatable {
		return a.pickerCreateHook(kinds[0], func(rec data.Record) modal.Result {
			refs := markedPickerRefs(pm, nil)
			createdRef := rec.Ref()
			if !slices.Contains(refs, createdRef) {
				refs = append(refs, createdRef)
			}
			if onPickMany != nil {
				onPickMany(refs)
			}
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		})
	}
	return a.pickerCreateHook(kinds[0], func(rec data.Record) modal.Result {
		if onPick != nil {
			onPick(rec.Ref())
		}
		return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
	})
}

func (a *App) newRefPickerModalWith(req form.RefRequest, selected []core.Ref, onPick func(core.Ref), onPickMany func([]core.Ref)) *modal.PickerModal {
	kinds, recs := a.queryRefPickerRecords(req.RefKinds)
	pm := views.PickerFromRecordsWith(a.Store.Registry(), "Pick "+views.Label(req.Field), recs, a.styleOptions())
	if req.Repeatable {
		pm.MultiSelect = true
		pm.Marked = make(map[string]bool, len(selected))
		for _, r := range selected {
			pm.Marked[data.RefText(r)] = true
		}
		pm.Placeholder = "filter or create · space/ctrl+space marks · enter confirms"
	}
	opts := modal.PickerOptions{
		ID:            "ref",
		PageSize:      10,
		StartInFilter: req.Repeatable,
		Create:        a.refPickerCreateHook(kinds, req.Repeatable, pm, onPick, onPickMany),
		OnConfirm: func(picked picker.Item) modal.Result {
			if req.Repeatable {
				if onPickMany != nil {
					onPickMany(markedPickerRefs(pm, picked.Ref))
				}
				return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
			}
			if picked.Ref != nil && onPick != nil {
				onPick(*picked.Ref)
			}
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	}
	return a.newPickerModal(pm, opts)
}
