package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// A link a program wrapped itself (Claude Code's indented continuation)
// is one item: one label, on its first piece, every piece highlighted,
// and the label copies the joined text without the indent.
func TestHintsJoinAHardWrappedLink(t *testing.T) {
	url := "https://example.com/" + strings.Repeat("w", 100) + "/tail/end"
	lead := "see "
	cut := hintsWidth - len(lead)
	rows := padRows(lead+url[:cut], "    "+url[cut:]+" for review", "", "$ ")
	app, s, sys := hintsApp(t, rows)
	o := startHints(t, app, s)
	label := labelOf(t, app, url)
	var got []string
	for _, m := range o.Marks {
		got = append(got, fmt.Sprintf("%d:%d %s", m.Row, m.Col, m.Text))
	}
	want := []string{
		"0:4 " + url[:cut],
		"1:4 " + url[cut:],
		"0:4 " + label,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("marks:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// Drawn: the label over the first piece, the second piece as it reads
	// (the pane is drawn narrower than its frame: the first piece's start).
	view := plainView(app)
	if !drawnLabel(view, label, url[:40]) || !strings.Contains(view, "    "+url[cut:]+" for review") {
		t.Fatalf("view:\n%s", view)
	}
	pressAll(t, app, strings.Split(label, "")...)
	if !slices.Equal(sys.copied, []string{url}) {
		t.Fatalf("copied %q, want %q", sys.copied, url)
	}
}
