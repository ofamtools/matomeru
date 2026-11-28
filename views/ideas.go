package views

import (
	"strings"
	"time"

	"ofam.dev/ochazuke/layout"
	"ofam.dev/ochazuke/ui"
)

// Ideas view scope and node IDs.
const (
	ScopeIdeas    = "ideas.page"
	IdeasPageID   = "ideas.page"
	IdeasListID   = "ideas.list"
	IdeasHeaderID = "ideas.header"

	// IdeaBadge marks every idea as exploration only: agents never pick it up.
	IdeaBadge = "✧ idea · exploration only · agents skip"
)

// IdeaRow is one idea, pre-formatted by the caller.
type IdeaRow struct {
	ID      string
	Title   string
	Summary string
	Project string
	Updated time.Time
}

// IdeasViewSpec is the pure data contract of IdeasView.
type IdeasViewSpec struct {
	Ideas        []IdeaRow
	SelectedIdx  int
	ProjectTitle string
	Now          time.Time
}

// IdeasView presents the owner's ideas with the non-actionable badge. It
// reads nothing but its spec.
func IdeasView(spec IdeasViewSpec) ui.ViewNode {
	title := " Ideas"
	if p := strings.TrimSpace(spec.ProjectTitle); p != "" {
		title += " · " + p
	}
	var line spanLine
	line.write(title+"  ", "accent")
	line.write(IdeaBadge, "badge.muted")
	header := fixed(ui.ViewNode{
		ID:    IdeasHeaderID,
		Kind:  ui.KindText,
		Props: map[string]any{"text": line.String(), "spans": line.spans},
	}, 1)

	rows := make([][]any, 0, len(spec.Ideas))
	for _, idea := range spec.Ideas {
		rows = append(rows, []any{"✧ " + idea.Title, idea.Summary, idea.Project, formatRelativeAge(idea.Updated, spec.Now)})
	}
	selected := -1
	if spec.SelectedIdx >= 0 && spec.SelectedIdx < len(rows) {
		selected = spec.SelectedIdx
	}
	body := ui.ViewNode{
		ID:        IdeasListID,
		Kind:      ui.KindTable,
		Focusable: true,
		Filter:    &ui.FilterSpec{ID: IdeasListID, Prefix: "ideas ", Fields: []string{"title", "summary", "project"}},
		Box:       layout.Constraints{Flex: 1},
		Props: map[string]any{
			"columns":  []any{"Idea", "Summary", "Project", "Updated"},
			"rows":     rows,
			"selected": selected,
		},
	}
	if len(rows) == 0 {
		body.Kind = ui.KindText
		body.Tone = "muted"
		body.Props = map[string]any{"text": " No ideas yet · idea.capture records one · agents never pick ideas up"}
	}

	facts := map[string]any{
		"empty":        len(rows) == 0,
		"count":        len(rows),
		"ideaOnly":     true,
		"hasSelection": selected >= 0,
		"selectedIdea": "",
	}
	if selected >= 0 {
		facts["selectedIdea"] = spec.Ideas[selected].ID
	}
	return ui.ViewNode{
		ID:       IdeasPageID,
		Kind:     ui.KindVStack,
		Scope:    ScopeIdeas,
		Box:      layout.Constraints{Flex: 1},
		Facts:    facts,
		Children: []ui.ViewNode{header, body},
	}
}
