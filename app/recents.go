package app

import (
	"fmt"
	"strings"
	"time"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/ui"
	"ofam.dev/ochazuke/widgets/modal"
	"ofam.dev/ochazuke/widgets/picker"
)

// --- global newest-first Recents picker --------------------------------------

func relativeTime(now, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

func (a *App) recentsPick(s core.State, _ core.Command) core.StepResult {
	res, err := a.Store.Query(data.Query{
		IncludeArchived: a.nav.showArchived,
		SortField:       "updatedAt",
		SortDesc:        true,
	})
	if err != nil {
		a.fail("recents", err)
		return core.Next(s)
	}
	now := a.now()
	items := make([]picker.Item, 0, len(res.Items))
	for _, rec := range res.Items {
		ref := rec.Ref()
		schema := a.schema(rec.Kind)
		nounLabel := schema.Title
		if nounLabel == "" {
			nounLabel = rec.Kind
		}
		rel := relativeTime(now, rec.UpdatedAt)
		badges := []ui.Badge{{Text: nounLabel, Tone: "accent"}}
		if rel != "" {
			badges = append(badges, ui.Badge{Text: rel, Tone: "muted"})
		}
		items = append(items, picker.Item{
			ID:       data.RefText(ref),
			Title:    views.Title(schema, rec),
			Subtitle: strings.TrimSpace(rec.Kind + " · " + rel),
			Ref:      &ref,
			Scope:    rec.Kind,
			Badges:   badges,
			Keywords: []string{rec.Kind, nounLabel, schema.Plural, rec.ID},
		})
	}
	pm := picker.New("Recents", items)
	pm.Placeholder = "recently updated records across all nouns"
	m := modal.NewPickerModal(pm, modal.PickerOptions{
		ID:       "recents",
		PageSize: 10,
		OnConfirm: func(picked picker.Item) modal.Result {
			if picked.Ref != nil {
				ref := *picked.Ref
				a.afterModal(func(st core.State) core.State {
					return a.show(st, ref)
				})
			}
			return modal.Result{Handled: true, Close: true, CloseReason: modal.CloseSubmit}
		},
	})
	return core.Next(a.pushModal(s, m))
}
