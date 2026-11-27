package views

import (
	"fmt"
	"strings"

	"ofam.dev/ochazuke/ui"
)

// FeaturesPropRow describes a schema field or a proposed field.
type FeaturesPropRow struct {
	Name, Type, Role, Default, Enum, Description string
	Suggested                                    bool
}

// FeaturesViewRow describes a page or component view.
type FeaturesViewRow struct {
	Name, Kind, Summary, OptOutSetting string
	Suggested                          bool
}

// FeaturesItemizedNoun groups schema fields under their noun.
type FeaturesItemizedNoun struct {
	Kind, Title, Icon string
	Props             []FeaturesPropRow
}

func featuresItemizedSections(detail FeaturesDetailSpec) []ui.ViewNode {
	var sections []ui.ViewNode
	if len(detail.Props) > 0 {
		sections = append(sections, renderItemizedProps("features.section.props", "Props", detail.Props))
	}
	for _, noun := range detail.ItemizedNouns {
		label := strings.TrimSpace(noun.Icon + " " + noun.Title)
		sections = append(sections, renderItemizedProps("features.section.props."+noun.Kind, label+" props", noun.Props))
	}
	if len(detail.Views) > 0 {
		sections = append(sections, renderItemizedViews(detail.Views))
	}
	return sections
}

func renderItemizedProps(id, title string, rows []FeaturesPropRow) ui.ViewNode {
	children := []ui.ViewNode{featuresSectionHeading(id+".title", title, len(rows), "accent")}
	for i, row := range rows {
		var metadata []string
		for _, value := range []string{row.Type, row.Role} {
			if value != "" {
				metadata = append(metadata, value)
			}
		}
		if row.Default != "" {
			metadata = append(metadata, "default "+row.Default)
		}
		if row.Enum != "" {
			metadata = append(metadata, "choices "+row.Enum)
		}
		children = append(children, featuresItemizedRow(id, i, row.Name, row.Description, row.Suggested, metadata))
	}
	return ui.ViewNode{ID: id, Kind: ui.KindVStack, Children: children}
}

func renderItemizedViews(rows []FeaturesViewRow) ui.ViewNode {
	const id = "features.section.views"
	children := []ui.ViewNode{featuresSectionHeading(id+".title", "Views", len(rows), "accent")}
	for i, row := range rows {
		var metadata []string
		if row.Kind != "" {
			metadata = append(metadata, row.Kind)
		}
		if row.OptOutSetting != "" {
			metadata = append(metadata, "setting "+row.OptOutSetting)
		}
		children = append(children, featuresItemizedRow(id, i, row.Name, row.Summary, row.Suggested, metadata))
	}
	return ui.ViewNode{ID: id, Kind: ui.KindVStack, Children: children}
}

func featuresItemizedRow(section string, index int, name, description string, suggested bool, metadata []string) ui.ViewNode {
	parts := append([]string{name}, metadata...)
	tone := ""
	if suggested {
		parts = append(parts, "suggested")
		tone = "info"
	}
	if description != "" {
		parts = append(parts, description)
	}
	return ui.ViewNode{
		ID:        fmt.Sprintf("%s.row.%d", section, index),
		Kind:      ui.KindText,
		Scope:     ScopeFeaturesContent,
		Focusable: true,
		Tone:      tone,
		Props:     map[string]any{"text": "  " + strings.Join(parts, " · "), "focusedText": "▸ " + strings.Join(parts, " · ")},
	}
}
