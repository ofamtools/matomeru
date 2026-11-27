package views

import (
	"fmt"
	"strings"

	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// CodeLocation identifies a source file, 1-based line range, and optional symbol.
type CodeLocation struct {
	File      string
	StartLine int
	EndLine   int
	Symbol    string
}

// Valid reports whether the location names a source file.
func (loc CodeLocation) Valid() bool {
	return strings.TrimSpace(loc.File) != ""
}

// Format renders the canonical file:start-end display form.
func (loc CodeLocation) Format() string {
	file := strings.TrimSpace(loc.File)
	if file == "" {
		return ""
	}
	if loc.StartLine <= 0 {
		return file
	}
	if loc.EndLine > loc.StartLine {
		return fmt.Sprintf("%s:%d-%d", file, loc.StartLine, loc.EndLine)
	}
	return fmt.Sprintf("%s:%d", file, loc.StartLine)
}

// DomainOccurrence describes one cataloged or scanned recurring codebase concept
// (Indicator, Component, Badge, Signal, Verb, Mode, Guard, View, etc.).
type DomainOccurrence struct {
	ID             string
	Classification string
	Name           string
	Glyph          string
	Tone           string
	Summary        string
	Location       CodeLocation
	RelatedPages   []string
	RelatedObjects []string
	Tags           []string
}

// DomainGroup groups DomainOccurrences under one classification heading.
type DomainGroup struct {
	Key     string
	Title   string
	Icon    string
	Summary string
	Items   []DomainOccurrence
}

func featuresDomainSections(groups []DomainGroup) []ui.ViewNode {
	if len(groups) == 0 {
		return nil
	}
	out := make([]ui.ViewNode, 0, len(groups))
	for i, group := range groups {
		out = append(out, featuresDomainGroupSection(group, i))
	}
	return out
}

func featuresDomainGroupSection(group DomainGroup, idx int) ui.ViewNode {
	key := strings.TrimSpace(group.Key)
	if key == "" {
		key = fmt.Sprintf("group-%d", idx)
	}
	secID := "features.section.domain." + key
	heading := strings.TrimSpace(group.Title)
	if icon := strings.TrimSpace(group.Icon); icon != "" {
		heading = icon + " " + heading
	}
	children := make([]ui.ViewNode, 0, len(group.Items)*2+2)
	children = append(children, featuresSectionHeading(secID+".title", heading, len(group.Items), "accent"))
	if sum := strings.TrimSpace(group.Summary); sum != "" {
		children = append(children, ui.ViewNode{
			ID:    secID + ".summary",
			Kind:  ui.KindText,
			Tone:  "muted",
			Box:   layout.Constraints{FixedH: 1},
			Props: map[string]any{"text": "  " + sum},
		})
	}
	if len(group.Items) == 0 {
		children = append(children, ui.ViewNode{
			ID:    secID + ".empty",
			Kind:  ui.KindText,
			Tone:  "muted",
			Box:   layout.Constraints{FixedH: 1},
			Props: map[string]any{"text": "  No occurrences indexed."},
		})
	} else {
		for i, item := range group.Items {
			children = append(children, featuresDomainOccurrenceNodes(key, i, item)...)
		}
	}
	return ui.ViewNode{
		ID:       secID,
		Kind:     ui.KindVStack,
		Children: children,
	}
}

func featuresDomainOccurrenceNodes(groupKey string, idx int, item DomainOccurrence) []ui.ViewNode {
	rowID := domainOccurrenceNodeID(groupKey, idx, item.ID)
	line := domainOccurrenceLine(item)
	facts := domainOccurrenceFacts(item)
	tone := strings.TrimSpace(item.Tone)
	nodes := []ui.ViewNode{{
		ID:        rowID,
		Kind:      ui.KindText,
		Scope:     ScopeFeaturesContent,
		Focusable: true,
		Tone:      tone,
		Box:       layout.Constraints{FixedH: 1},
		Props: map[string]any{
			"text":        line,
			"focusedText": "▸ " + strings.TrimSpace(line),
		},
		Facts: facts,
	}}
	if meta := domainOccurrenceMetaLine(item); meta != "" {
		nodes = append(nodes, ui.ViewNode{
			ID:    rowID + ".meta",
			Kind:  ui.KindText,
			Tone:  "muted",
			Box:   layout.Constraints{FixedH: 1},
			Props: map[string]any{"text": "    " + meta},
		})
	}
	return nodes
}

func domainOccurrenceNodeID(groupKey string, idx int, id string) string {
	clean := strings.TrimSpace(id)
	if clean == "" {
		return fmt.Sprintf("features.domain.%s.%d", groupKey, idx)
	}
	return fmt.Sprintf("features.domain.%s.%s", groupKey, clean)
}

func domainOccurrenceLine(item DomainOccurrence) string {
	glyph := strings.TrimSpace(item.Glyph)
	if glyph == "" {
		glyph = "·"
	}
	locText := item.Location.Format()
	if locText != "" {
		return fmt.Sprintf("  %s %-26s  [%s]", glyph, item.Name, locText)
	}
	return fmt.Sprintf("  %s %s", glyph, item.Name)
}

func domainOccurrenceMetaLine(item DomainOccurrence) string {
	var parts []string
	if sym := strings.TrimSpace(item.Location.Symbol); sym != "" {
		parts = append(parts, sym)
	}
	if sum := strings.TrimSpace(item.Summary); sum != "" {
		parts = append(parts, sum)
	}
	if len(item.Tags) > 0 {
		parts = append(parts, "#"+strings.Join(item.Tags, " #"))
	}
	return strings.Join(parts, " · ")
}

func domainOccurrenceFacts(item DomainOccurrence) map[string]any {
	facts := map[string]any{
		"domainOccurrenceId":   item.ID,
		"domainClassification": item.Classification,
		"hasCodeLocation":      item.Location.Valid(),
	}
	if item.Location.Valid() {
		facts["codeFile"] = strings.TrimSpace(item.Location.File)
		facts["codeStartLine"] = item.Location.StartLine
		facts["codeEndLine"] = item.Location.EndLine
		facts["codeSymbol"] = strings.TrimSpace(item.Location.Symbol)
		facts["codeTitle"] = strings.TrimSpace(item.Name)
		facts["codeSummary"] = strings.TrimSpace(item.Summary)
	}
	return facts
}
