package views

import (
	"strings"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/ui"
)

// The presenters of the declared noun forms. Each takes a resolved
// presentation (data decided which fields fill it) and returns pure UI
// data with the record's Ref attached, so a caller never formats a
// reference or fishes a field out of a record.

// presentationTone is p's tone, muted for a missing record.
func presentationTone(p data.ResolvedPresentation) string {
	if p.Missing {
		return "muted"
	}
	return p.Tone
}

// presentationBadges converts p's badges to UI badges.
func presentationBadges(p data.ResolvedPresentation) []ui.Badge {
	out := make([]ui.Badge, 0, len(p.Badges))
	for _, b := range p.Badges {
		out = append(out, ui.Badge{Text: b.Text, Tone: badgeTone(b.Tone)})
	}
	return out
}

// presentationSummary is p's subtitle followed by its meta.
func presentationSummary(p data.ResolvedPresentation) string {
	parts := make([]string, 0, 1+len(p.Meta))
	if p.Subtitle != "" {
		parts = append(parts, p.Subtitle)
	}
	parts = append(parts, p.Meta...)
	return strings.Join(parts, "  ·  ")
}

// PresentChipRun is p as a tinted pill: icon and label in one badge run.
func PresentChipRun(p data.ResolvedPresentation) []ui.InlineRun {
	ref := p.Ref
	return []ui.InlineRun{{Kind: "badge", Label: p.Display(), Tone: presentationTone(p), Ref: &ref}}
}

// PresentInlineRun is p as a link inside prose.
func PresentInlineRun(p data.ResolvedPresentation) ui.InlineRun {
	ref := p.Ref
	return ui.InlineRun{Kind: "link", Label: p.Display(), Tone: presentationTone(p), Ref: &ref}
}

// PresentCardNode is p as a focusable card.
func PresentCardNode(id string, p data.ResolvedPresentation) ui.ViewNode {
	ref := p.Ref
	card := ui.ViewNode{ID: id, Kind: ui.KindCard, Scope: ref.Kind, Ref: &ref, Focusable: true, Border: true,
		Title: p.Display(), Tone: presentationTone(p), Badges: presentationBadges(p),
		Facts: map[string]any{"missing": p.Missing, "form": p.Form}}
	if s := presentationSummary(p); s != "" {
		card.Props = map[string]any{"summary": s}
	}
	return card
}

// PresentDetailHeader is p as the header strip of a detail page.
func PresentDetailHeader(id string, p data.ResolvedPresentation) ui.ViewNode {
	header := PresentCardNode(id, p)
	header.Facts["detail"] = true
	return header
}
