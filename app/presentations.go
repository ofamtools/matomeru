package app

import (
	"fmt"
	"io"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/config"
	"ofam.dev/ochazuke/core"
)

// presentationsFileName is the user's file of noun presentation forms. It is
// read when present and never written.
const presentationsFileName = "presentations.json"

// loadPresentations lays the user's presentations.json (JSONC) over the
// embedded defaults and hands the result to the store's snapshots.
func (a *App) loadPresentations(b []byte) {
	reg, err := data.LoadPresentations(config.StripJSONC(b))
	if err != nil {
		a.warn("%s: %v", presentationsFileName, err)
		return
	}
	a.cfg.Presentations = reg
	a.Store.SetPresentations(reg)
}

// presentations is the registry in effect.
func (a *App) presentations() *data.PresentationRegistry {
	if a.cfg != nil && a.cfg.Presentations != nil {
		return a.cfg.Presentations
	}
	return a.Store.Presentations()
}

// declarativePresentations reports whether references draw from their
// declared presentation forms (ui.declarativePresentations).
func (a *App) declarativePresentations() bool {
	return a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetDeclarativePresentations, true)
}

// refCellLabels names every reference the reference fields of items hold:
// the target's chip label read in one batch, or, with the setting off, its
// title through the store one by one.
func (a *App) refCellLabels(schema data.Schema, items []data.Record) map[core.Ref]string {
	var refs []core.Ref
	for _, f := range schema.Fields {
		if f.Type != core.ParamRef {
			continue
		}
		for _, rec := range items {
			if ref, ok := rec.RefField(f.Name); ok {
				refs = append(refs, ref)
			}
		}
	}
	names := make(map[core.Ref]string, len(refs))
	if !a.declarativePresentations() {
		for _, ref := range refs {
			if _, seen := names[ref]; !seen {
				names[ref] = a.title(ref)
			}
		}
		return names
	}
	resolved := a.Store.ResolveRefsFor(refs)
	resolved.Presentations = a.presentations()
	for _, ref := range refs {
		names[ref] = resolved.Presentation(ref, data.FormChip).Label
	}
	return names
}

// presentationsText writes one row per noun and form.
func presentationsText(w io.Writer, rows []data.NounPresentations) {
	fmt.Fprintln(w, "KIND\tFORM\tICON\tTITLE FIELDS\tTONE FIELD\tBADGES\tMAX")
	for _, r := range rows {
		for _, form := range data.PresentationForms {
			spec := r.Forms[form]
			icon := spec.Icon
			if spec.IconField != "" {
				icon = strings.TrimSpace(icon + " ‹" + spec.IconField + "›")
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%d\n", r.Kind, form, icon, strings.Join(spec.TitleFields, " "),
				spec.ToneField, strings.Join(spec.BadgeFields, " "), spec.MaxLen)
		}
	}
}
