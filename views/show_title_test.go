package views

import (
	"strings"
	"testing"

	"ofam.dev/matomeru/data"
)

// A Show page whose record has a very long title that starts with an
// @/path attachment: at 100 columns the header wraps the prose inside the
// page with an even gutter and shows the attachment as a chip on its own
// line, in the ledger and the classic layout; the opt-out keeps the path in
// the title.
func TestShowHeaderLongTitleWithAttachmentPath(t *testing.T) {
	const attachment = "/Users/owner/.local/share/matomeru/attachments/2026-11-26/screenshot-of-the-broken-show-page.png"
	const prose = "the Show page header overflows its border when a task title is this long and starts with an attachment path, please wrap it cleanly instead of crushing the layout under it"
	w := build(t)
	rec, _, err := w.store.Create(data.CreateInput{Kind: data.KindTask, Fields: map[string]any{"title": "@" + attachment + " " + prose}}, owner)
	if err != nil {
		t.Fatal(err)
	}
	const width = 100
	opts := DefaultShowOptions()
	opts.Width = width

	root, err := ShowViewWith(w.store, rec.Ref(), "show.header", opts)
	if err != nil {
		t.Fatal(err)
	}
	header := root.Find("show.header")
	if header == nil || header.Facts["copy"] != "@"+attachment+" "+prose {
		t.Fatalf("header lost the full title for copying: %+v", header)
	}
	var wrapped []string
	for _, n := range header.Children {
		if strings.HasPrefix(n.ID, "show.header.title") {
			line, _ := n.Props["text"].(string)
			if !strings.HasPrefix(line, " ") || len([]rune(line)) > width-2 || strings.Contains(line, "@/") {
				t.Fatalf("title row %q: want a one-cell gutter, at most %d cells and no attachment path", line, width-2)
			}
			wrapped = append(wrapped, strings.TrimSpace(line))
		}
	}
	if len(wrapped) < 2 || len(wrapped) > showTitleMaxLines || strings.Join(wrapped, " ") != prose {
		t.Fatalf("wrapped title = %q, want the prose over 2..%d rows", wrapped, showTitleMaxLines)
	}
	chips := root.Find("show.header.attachments")
	if chips == nil || len(chips.Facts["attachments"].([]string)) != 1 || chips.Facts["attachments"].([]string)[0] != attachment {
		t.Fatalf("no attachment chip line carrying the path: %+v", chips)
	}
	if text, _ := chips.Props["text"].(string); !strings.Contains(text, "⎘ screenshot-of-the-broken-show-page") || strings.Contains(text, "/Users/") {
		t.Fatalf("chip line = %q, want the file name as a chip without its directory", text)
	}
	out := frame(t, root, "show.header", width, 30)
	if !strings.Contains(out, "⎘ screenshot-of-the-broken-show-page") || !strings.Contains(out, "instead of crushing the layout under it") || !strings.Contains(out, "1 PROPS") {
		t.Fatalf("ledger header at %d columns:\n%s", width, out)
	}

	// Classic layout: the bordered header card keeps every row inside its border.
	opts.Layout = ShowLayoutClassic
	classic, err := ShowViewWith(w.store, rec.Ref(), "show.header", opts)
	if err != nil {
		t.Fatal(err)
	}
	if h := classic.Find("show.header"); h == nil || h.Title != prose || classic.Find("show.header.attachments") == nil {
		t.Fatalf("classic header: title %q, want the prose and a chip line", h.Title)
	}
	lines := strings.Split(frame(t, classic, "show.header", width, 30), "\n")
	inCard := false
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "┏"):
			inCard = true
		case strings.HasPrefix(line, "┗"):
			inCard = false
		case inCard && (!strings.HasPrefix(line, "┃") || !strings.HasSuffix(strings.TrimRight(line, " "), "┃") || len([]rune(strings.TrimRight(line, " "))) > width):
			t.Fatalf("classic header row breaks its border: %q", line)
		}
	}

	// Opt-out (show.titleAttachmentChips off): the path stays in the title.
	opts.Layout, opts.TitleAttachmentChips = ShowLayoutLedger, false
	plain, err := ShowViewWith(w.store, rec.Ref(), "show.header", opts)
	if err != nil {
		t.Fatal(err)
	}
	if first := plain.Find("show.header.title"); plain.Find("show.header.attachments") != nil || first == nil || !strings.Contains(first.Props["text"].(string), "@/Users/owner") {
		t.Fatalf("TitleAttachmentChips off still lifts the attachment out of the title")
	}
}
