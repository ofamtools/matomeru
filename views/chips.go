package views

import (
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"ofam.dev/ochazuke/core"
	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

const ScopePointerChip = "pointer-chip"

// ChipNodeSpec contains the resolved identity, display and context of one chip.
type ChipNodeSpec struct {
	ID                   string
	Ref                  core.Ref
	TargetKind, TargetID string
	Label, Glyph, Tone   string
	Compact              bool
	Lineage              []string
	VerbCount            int
	Meta                 map[string]string
}

// ChipStripView renders each chip as a separate focus target, including glyphs
// that occupy only one cell.
func ChipStripView(id string, chips []ChipNodeSpec) ui.ViewNode {
	strip := ui.ViewNode{ID: id, Kind: ui.KindHStack, Gap: 1}
	if len(chips) == 0 {
		return strip
	}
	strip.Box.FixedH = 1
	strip.Children = make([]ui.ViewNode, 0, len(chips))
	for i, chip := range chips {
		chipID := chip.ID
		if chipID == "" {
			chipID = strconv.Itoa(i)
		}
		label := strings.Join(strings.Fields(chip.Label), " ")
		glyph := strings.TrimSpace(chip.Glyph)
		text := firstChipText(label, chip.TargetID)
		if chip.Compact && glyph != "" {
			text = glyph
		} else if glyph != "" {
			text = strings.TrimSpace(glyph + " " + text)
		}
		ref := chip.Ref
		box := layout.Constraints{FixedH: 1}
		if chip.Compact && glyph != "" {
			box.FixedW = utf8.RuneCountInString(glyph)
		} else {
			box.MinW, box.MaxW = 3, 44
		}
		strip.Children = append(strip.Children, ui.ViewNode{
			ID: id + "." + chipID, Kind: ui.KindText, Scope: ScopePointerChip,
			Focusable: true, Ref: &ref, Title: label, Tone: chip.Tone, Box: box,
			Props: map[string]any{"text": text},
			Facts: map[string]any{
				"chipKind": chip.TargetKind, "chipTarget": chip.TargetID,
				"chipLineage": slices.Clone(chip.Lineage), "chipCompact": chip.Compact,
				"chipVerbCount": chip.VerbCount, "chipLabel": chip.Label, "chipMeta": maps.Clone(chip.Meta),
			},
		})
	}
	return strip
}

// ChipDetailsView displays the supplied target details alongside an action list.
func ChipDetailsView(id string, details []string) ui.ViewNode {
	rows := make([]ui.ViewNode, 0, len(details))
	for i, detail := range details {
		rows = append(rows, homeLine(id+"."+strconv.Itoa(i), detail, ""))
	}
	return ui.ViewNode{
		ID: id, Kind: ui.KindPanel, Border: true, Title: "Target",
		Children: []ui.ViewNode{{ID: id + ".scroll", Kind: ui.KindScroll, Children: []ui.ViewNode{
			{ID: id + ".rows", Kind: ui.KindVStack, Children: rows},
		}}},
	}
}

func firstChipText(label, fallback string) string {
	if label != "" {
		return label
	}
	return strings.Join(strings.Fields(fallback), " ")
}
