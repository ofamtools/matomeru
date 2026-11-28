package views

import (
	"strings"
	"testing"
	"time"
)

func TestIdeasViewBadgeAndFacts(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	empty := IdeasView(IdeasViewSpec{SelectedIdx: -1, Now: now})
	if empty.Facts["empty"] != true || empty.Facts["count"] != 0 || empty.Facts["ideaOnly"] != true || empty.Facts["hasSelection"] != false {
		t.Fatalf("empty facts: %v", empty.Facts)
	}
	header := empty.Find(IdeasHeaderID)
	if header == nil || !strings.Contains(header.Props["text"].(string), "✧ idea · exploration only · agents skip") {
		t.Fatalf("header missing badge: %+v", header)
	}

	node := IdeasView(IdeasViewSpec{Now: now, SelectedIdx: 1, Ideas: []IdeaRow{
		{ID: "a", Title: "Offline", Updated: now.Add(-2 * time.Hour)},
		{ID: "b", Title: "Plugins", Summary: "extensions"},
	}})
	if node.Facts["empty"] != false || node.Facts["count"] != 2 || node.Facts["selectedIdea"] != "b" || node.Scope != ScopeIdeas {
		t.Fatalf("facts: %v scope %q", node.Facts, node.Scope)
	}
	rows := node.Find(IdeasListID).Props["rows"].([][]any)
	if rows[0][0] != "✧ Offline" || rows[0][3] == "" || rows[1][1] != "extensions" {
		t.Fatalf("rows: %v", rows)
	}
}
