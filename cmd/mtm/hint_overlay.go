package main

import (
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"ofam.dev/ochazuke"
)

// The hints overlay over a screen mtm draws itself (a pane row's preview
// in the Screen tab), in the shape ochazuke draws over a live terminal
// (TerminalOverlay). ochazuke's splicer (Terminal.drawOverlay, overlayCells,
// dim and modalCells in terminal.go and surface.go, v0.14.0) is private, so
// its logic is copied here; exporting it from ochazuke would remove the
// copy.

// overlayText draws o over the first shown lines of text (each padded or
// cut to width cells): those lines dimmed with fade (faint when fade draws
// nothing), then each mark over its cells. Lines after them (a position
// marker) are left as they are.
func overlayText(text string, width, shown int, o *ochazuke.TerminalOverlay, fade lipgloss.Style) string {
	if o == nil || width <= 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	shown = min(shown, len(lines))
	for i := range shown {
		lines[i] = padCells(lines[i], width)
	}
	if o.Dim {
		if fade.Render("x") == "x" {
			fade = lipgloss.NewStyle().Faint(true)
		}
		for i := range shown {
			lines[i] = dimLine(lines[i], fade)
		}
	}
	for _, mark := range o.Marks {
		if mark.Row < 0 || mark.Row >= shown {
			continue
		}
		lines[mark.Row] = markCells(lines[mark.Row], width, mark)
	}
	return strings.Join(lines, "\n")
}

// dimLine draws a line in fade, re-opened after every reset in it so the
// line's own styles do not end the fade (ochazuke's dim).
func dimLine(line string, fade lipgloss.Style) string {
	sample := fade.Render("x")
	open := sample[:strings.Index(sample, "x")]
	if open == "" {
		return line
	}
	line = strings.ReplaceAll(line, "\x1b[0m", "\x1b[0m"+open)
	line = strings.ReplaceAll(line, "\x1b[m", "\x1b[m"+open)
	return open + line + "\x1b[m"
}

// padCells pads or cuts a line to exactly width cells, closing its styles so
// an unclosed one cannot color what follows (ochazuke's modalCells).
func padCells(line string, width int) string {
	text := ansi.Truncate(line, width, "")
	if strings.ContainsRune(text, '\x1b') {
		text += "\x1b[0m"
	}
	return text + strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
}

// markCells is a line of exactly width cells with a mark drawn over the
// cells from its column: the cells before keep their styles, the text is
// cut at the edge, and the cells after start with every sequence that came
// before them so their styles carry on. A wide rune the mark cuts through
// becomes a space on that side (ochazuke's overlayCells).
func markCells(line string, width int, mark ochazuke.OverlayMark) string {
	text := ansi.Truncate(markLabel(mark.Text), width-mark.Col, "")
	w := ansi.StringWidth(text)
	if mark.Col < 0 || w == 0 {
		return line
	}
	style := mark.Style
	if style.Render("x") == "x" {
		style = lipgloss.NewStyle().Reverse(true).Bold(true)
	}
	left := ansi.Truncate(line, mark.Col, "")
	left += strings.Repeat(" ", mark.Col-ansi.StringWidth(left))
	end := mark.Col + w
	rest := ansi.TruncateLeft(line, end, "")
	for n := end; ansi.StringWidth(rest) > width-end; n++ {
		// A wide rune straddling end is kept whole by TruncateLeft.
		rest = ansi.TruncateLeft(line, n+1, "")
	}
	rest = strings.Repeat(" ", width-end-ansi.StringWidth(rest)) + rest
	return left + "\x1b[m" + style.Render(text) + "\x1b[m" + rest
}

// markLabel is a mark's text as plain cells: controls and line or
// paragraph separators become spaces (ochazuke's modalLabel).
func markLabel(label string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return ' '
		}
		return r
	}, ansi.Strip(label))
}
