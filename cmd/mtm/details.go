package main

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"
)

// Page details: the text beside or below a page's lists about the selected
// row, an ochazuke Detail each (AddDetail), so J and K scroll it while the
// focus stays in the list (counts work: 10J) and the footer reads J/K
// detail only while it overflows. A new item starts at the top; a tail
// detail (a screen, a transcript: the newest at the bottom) starts at its
// end instead and follows the end while it is there, so new lines show
// until K scrolls back.

// The page details.
const (
	panesDetailID   = "panes/detail"
	workDetailID    = "work/detail"
	notesDetailID   = "notes/detail"
	reviewsDetailID = "reviews/detail"
	dirsDetailID    = "directories/detail"
)

// pageDetails are the pages' details.
type pageDetails struct {
	panes, work, notes, reviews, ext *pageDetail
	// The linked details (detail_links.go).
	dirs, saved, vim *linkedDetail
}

// detailEnd scrolls a detail to its end; Scroll clamps it.
const detailEnd = 1 << 30

// pageDetail is one page's detail and its tail state.
type pageDetail struct {
	d *ochazuke.Detail
	// tail reports whether the shown text is newest-at-the-bottom: it
	// starts at the end and follows it.
	tail func() bool
	// item is the item of the last frame; last the end offset it measured.
	item string
	last int
}

// addDetail adds a detail under page; item names what it shows now, tail
// (optional) says whether that is newest-at-the-bottom text.
func (a *App) addDetail(id, page string, item func() string, tail func() bool) (*pageDetail, error) {
	d, err := a.r.AddDetail(id, item, ochazuke.Parent(page))
	if err != nil {
		return nil, err
	}
	return &pageDetail{d: d, tail: tail}, nil
}

// window is the detail's lines of text in width by height (wrapped to
// width when width > 0) and the position marker ("" when it fits).
func (p *pageDetail) window(text string, width, height int) (string, string) {
	tail := p.tail != nil && p.tail()
	// At the end since the last frame (no K in between): stay there.
	follow := tail && p.d.Offset() >= p.last
	body, marker := p.d.Window(text, width, height)
	if tail && (p.d.Item() != p.item || follow) {
		p.d.Scroll(detailEnd)
		body, marker = p.d.Window(text, width, height)
	}
	p.item = p.d.Item()
	off := p.d.Offset()
	p.last = p.d.Scroll(detailEnd)
	p.d.Scroll(off - p.last)
	return body, marker
}

// lines is fit padded to exactly height lines.
func (p *pageDetail) lines(text string, wrap, width, height int) string {
	return fillLines(p.fit(text, wrap, width, height), height, width)
}

// fit is the window of text (wrapped to wrap when wrap > 0) with each
// line cut to width, the position marker taking the last line when the
// text overflows; at most height lines.
func (p *pageDetail) fit(text string, wrap, width, height int) string {
	if height <= 1 {
		body, _ := p.window(text, wrap, height)
		return cutLines(body, width)
	}
	// Windowed one line short first, so a scroll to the end is not
	// clamped a line early; text that fits the whole height takes it.
	body, marker := p.window(text, wrap, height-1)
	if marker == "" || p.last+height-1 <= height {
		body, _ = p.window(text, wrap, height)
		return cutLines(body, width)
	}
	return fillLines(body, height-1, width) + "\n" + ansi.Truncate(marker, width, "")
}

// cutLines cuts each line of text to width.
func cutLines(text string, width int) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "")
	}
	return strings.Join(lines, "\n")
}

// hide records that the page draws no detail this frame, so J and K do
// not answer for text that is not on screen; a tail detail shown again
// opens at its end.
func (p *pageDetail) hide() {
	p.d.Window("", 0, 0)
	p.last = 0
}

// scroll moves the detail by lines and returns the new offset.
func (p *pageDetail) scroll(lines int) int { return p.d.Scroll(lines) }

// fillLines is text as exactly height lines, each cut to width.
func fillLines(text string, height, width int) string {
	lines := strings.Split(text, "\n")
	if text == "" {
		lines = nil
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	lines = lines[:max(0, height)]
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, width, "")
	}
	return strings.Join(lines, "\n")
}

// trimBlankTail drops a screen capture's blank last lines, so a tail
// detail ends on the last line holding text, not the empty rows below a
// shell's prompt.
func trimBlankTail(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}
