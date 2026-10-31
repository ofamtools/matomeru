package hints

import (
	"testing"

	"ofam.dev/matomeru/internal/contextitems"
)

// The same item twice on a screen is one label; the item nearest the cursor
// (bottom row) gets the shortest.
func TestSpansOfOneItemShareALabel(t *testing.T) {
	rows := []string{"https://x.io/ and https://y.io", "", "https://x.io again"}
	spans := contextitems.ExtractSpans(rows, contextitems.SpanOptions{Width: 40})
	targets := make([]Target, len(spans))
	for i, s := range spans {
		at := s.Parts[0]
		targets[i] = Target{Text: s.Item.Text, Key: s.Key, Distance: Distance(at.Row, at.Col, 2, 0, 40)}
	}
	labels := Assign(targets, "ab")
	if len(labels) != 3 || labels[0] != labels[2] || labels[0] == labels[1] || labels[2] != "a" {
		t.Fatalf("labels = %q", labels)
	}
}
