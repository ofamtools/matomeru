package app

import (
	"slices"
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/views"
	"ofam.dev/ochazuke/ui"
)

const featuresConceptsGroup = "concepts"

func (a *App) featuresItemizationEnabled() bool {
	return a.cfg == nil || a.cfg.Settings == nil || a.cfg.Settings.Bool(SetFeaturesItemization, true)
}

func (a *App) featuresConceptNav(records []data.Record, activeKey string, validKeys map[string]bool) views.FeaturesNavGroup {
	group := views.FeaturesNavGroup{Key: featuresConceptsGroup, Title: "Concepts", Collapsed: a.featuresCtrl().collapsedGroups[featuresConceptsGroup]}
	if a.Store == nil {
		return group
	}
	field, _ := a.schema(data.KindConcept).Field(data.FieldConceptKind)
	counts := map[string]int{}
	for _, rec := range records {
		counts[rec.Text(data.FieldConceptKind)]++
	}
	for _, kind := range field.Enum {
		key := "concept:" + kind
		validKeys[key] = true
		group.Items = append(group.Items, views.FeaturesNavItem{
			Key: key, Section: group.Key, Label: views.Label(kind), Icon: "◇", Count: counts[kind], Active: key == activeKey,
		})
	}
	return group
}

func (a *App) featuresConceptItems(records []data.Record, matches func(data.Record) bool) []views.FeaturesLinkedItem {
	var items []views.FeaturesLinkedItem
	for _, rec := range records {
		if !matches(rec) {
			continue
		}
		item := a.linkedItemFromRecord(rec)
		item.Tone = statusToneFor(rec.Text(data.FieldConceptStatus))
		if kind := rec.Text(data.FieldConceptKind); kind != "" {
			item.Badges = append(item.Badges, ui.Badge{Text: kind, Tone: "info"})
		}
		if target := rec.Text(data.FieldConceptTargetKind); target != "" {
			if item.Subtitle == "" {
				item.Subtitle = target
			} else {
				item.Subtitle = target + " · " + item.Subtitle
			}
		}
		items = append(items, item)
	}
	return items
}

func featuresConceptTargets(rec data.Record, target, qualified string) bool {
	key := strings.TrimSpace(rec.Text(data.FieldConceptTargetKind))
	return key != "" && (strings.EqualFold(key, target) || strings.EqualFold(key, qualified))
}

func (a *App) featuresGroupConcepts(records []data.Record, group data.Record) []views.FeaturesLinkedItem {
	related := map[string]bool{}
	if a.Store != nil && len(records) > 0 {
		for _, kind := range []string{data.RelAbout, data.RelContext} {
			rels, err := a.Store.Related(group.Ref(), kind, data.Both)
			if err != nil {
				continue
			}
			for _, rel := range rels {
				if ref := rel.Other(group.Ref()); ref.Kind == data.KindConcept {
					related[ref.ID] = true
				}
			}
		}
	}
	return a.featuresConceptItems(records, func(rec data.Record) bool {
		return related[rec.ID] || featuresConceptTargets(rec, group.ID, "group:"+group.ID)
	})
}

func featuresPendingConcept(rec data.Record, kind, target, qualified string) bool {
	return rec.Text(data.FieldConceptKind) == kind &&
		rec.Text(data.FieldConceptStatus) == data.ConceptStatusProposed &&
		featuresConceptTargets(rec, target, qualified)
}

func featuresPropRows(sc data.Schema, kind string, concepts []data.Record) []views.FeaturesPropRow {
	rows := make([]views.FeaturesPropRow, 0, len(sc.Fields))
	for _, field := range sc.Fields {
		rows = append(rows, views.FeaturesPropRow{
			Name:        field.Name,
			Type:        string(field.Type),
			Role:        field.EffectiveRole(sc.SummaryField),
			Default:     data.Text(field.Default),
			Enum:        strings.Join(field.Enum, ", "),
			Description: field.Description,
		})
	}
	for _, rec := range concepts {
		if featuresPendingConcept(rec, data.ConceptKindProp, kind, "noun:"+kind) {
			rows = append(rows, views.FeaturesPropRow{Name: rec.Text("title"), Description: rec.Text("summary"), Suggested: true})
		}
	}
	return rows
}

func (a *App) featuresItemizedNouns(kinds []string, concepts []data.Record) []views.FeaturesItemizedNoun {
	if a.Store == nil || !a.featuresItemizationEnabled() {
		return nil
	}
	var nouns []views.FeaturesItemizedNoun
	seen := map[string]bool{}
	for _, kind := range kinds {
		kind = strings.ToLower(strings.TrimSpace(kind))
		if kind == "" || seen[kind] {
			continue
		}
		seen[kind] = true
		schema, _ := a.Store.Registry().Get(kind)
		rows := featuresPropRows(schema, kind, concepts)
		if len(rows) == 0 {
			continue
		}
		cat, _ := lookupFeaturesNounCatalog(kind)
		nouns = append(nouns, views.FeaturesItemizedNoun{
			Kind: kind, Title: firstNonEmpty(schema.Plural, schema.Title, cat.Title, views.Label(kind)), Icon: cat.Icon, Props: rows,
		})
	}
	return nouns
}

func featuresPageViews(key string, detail views.FeaturesDetailSpec) []views.FeaturesViewRow {
	rows := []views.FeaturesViewRow{{
		Name: detail.Title, Kind: "Page", Summary: detail.Summary, OptOutSetting: featuresPageOptOut[key],
	}}
	for _, component := range featuresComponentCatalog {
		if component.Page == key {
			rows = append(rows, component.View)
		}
	}
	return rows
}

func featuresViewsWithSuggestions(rows []views.FeaturesViewRow, concepts []data.Record, target, qualified string) []views.FeaturesViewRow {
	rows = slices.Clone(rows)
	for _, rec := range concepts {
		if featuresPendingConcept(rec, data.ConceptKindView, target, qualified) {
			rows = append(rows, views.FeaturesViewRow{Name: rec.Text("title"), Kind: "View", Summary: rec.Text("summary"), Suggested: true})
		}
	}
	return rows
}
