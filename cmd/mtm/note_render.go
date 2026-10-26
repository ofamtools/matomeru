package main

import (
	"sort"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
	"github.com/unixsuperhero/ochazuke/markdown"
)

// Notes are written as plain lines, so the Markdown view keeps every
// newline a line break (goldmark's hard wraps). ochazuke/markdown has no
// option for it: markdown.Block and markdown.Doc take the text alone, and
// Glamour v2 without WithPreservedNewLines joins every line of a paragraph
// into one, hard breaks ("  ", "\") included. So mtm renders the note for
// display only: a marker rune goes at the end of each line that runs on
// into the next one, Glamour renders that, and each rendered line is cut
// again at its markers. Task items get a start and end marker the same
// way, which give the lines an item covers for the highlight. The stored
// note is never touched; the Doc toggles the stored text by its own byte
// offsets.

// Marker runes, in plane 16 (ochazuke's own task markers are in plane 15).
const (
	noteBreakMark = rune(0x10FFFD)
	noteTaskStart = rune(0x100000)
	noteTaskEnd   = rune(0x108000)
	noteTaskMax   = 0x7000
)

func isTaskMark(r rune) bool { return r >= noteTaskStart && r < noteTaskEnd+noteTaskMax }

// renderNote is the note's text rendered width wide, one line per source
// line within paragraphs, and per task the first and last line it covers
// ({-1, -1} when it is not drawn).
func renderNote(text string, tasks []markdown.Task, width int, ap ochazuke.Appearance) ([]string, [][2]int) {
	rendered := markdown.Block(markNote(text, tasks), width, ap).Text
	spans := make([][2]int, len(tasks))
	for i := range spans {
		spans[i] = [2]int{-1, -1}
	}
	if rendered == "" {
		return nil, spans
	}
	var out []string
	for _, line := range strings.Split(rendered, "\n") {
		for _, piece := range splitBreaks(line) {
			n := len(out)
			var b strings.Builder
			for _, r := range piece {
				switch {
				case r >= noteTaskEnd && r < noteTaskEnd+noteTaskMax:
					if k := int(r - noteTaskEnd); k < len(spans) {
						spans[k][1] = n
					}
				case r >= noteTaskStart && r < noteTaskStart+noteTaskMax:
					if k := int(r - noteTaskStart); k < len(spans) && spans[k][0] < 0 {
						spans[k][0] = n
					}
				default:
					b.WriteRune(r)
				}
			}
			out = append(out, b.String())
		}
	}
	for i := range spans {
		if spans[i][0] >= 0 && spans[i][1] < spans[i][0] {
			spans[i][1] = spans[i][0]
		}
	}
	return out, spans
}

// markNote is text with a break marker at the end of each line that runs
// on into the next (not in a code fence), and each task's first line
// between its markers.
func markNote(text string, tasks []markdown.Task) string {
	type mark struct {
		at    int
		order int
		r     rune
	}
	var marks []mark
	lines := strings.Split(text, "\n")
	at := 0
	fence := ""
	for i, line := range lines {
		end := at + len(strings.TrimSuffix(line, "\r"))
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		switch m := fenceOf(trimmed); {
		case m != "" && fence == "":
			fence = m
		case m != "" && strings.HasPrefix(trimmed, fence):
			fence = ""
		case fence == "" && trimmed != "" && !strings.HasPrefix(trimmed, "|") && i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "":
			marks = append(marks, mark{end, 1, noteBreakMark})
		}
		at += len(line) + 1
	}
	for i, t := range tasks {
		if i >= noteTaskMax {
			break
		}
		start := t.Box + 2
		for start < len(text) && (text[start] == ' ' || text[start] == '\t') {
			start++
		}
		eol := len(text)
		if j := strings.IndexByte(text[start:], '\n'); j >= 0 {
			eol = start + j
		}
		if eol > start && text[eol-1] == '\r' {
			eol--
		}
		marks = append(marks, mark{start, 2, noteTaskStart + rune(i)}, mark{eol, 0, noteTaskEnd + rune(i)})
	}
	sort.SliceStable(marks, func(i, j int) bool {
		if marks[i].at != marks[j].at {
			return marks[i].at < marks[j].at
		}
		return marks[i].order < marks[j].order
	})
	var b strings.Builder
	prev := 0
	for _, m := range marks {
		b.WriteString(text[prev:m.at])
		b.WriteRune(m.r)
		prev = m.at
	}
	b.WriteString(text[prev:])
	return b.String()
}

func fenceOf(trimmed string) string {
	for _, m := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmed, m) {
			return m
		}
	}
	return ""
}

// splitBreaks cuts a rendered line at its break markers. Glamour joined
// the lines with a space; each later piece loses it and takes the line's
// indent (its margin, and a list item's bullet width). A piece with
// nothing to show stays on the line before it.
func splitBreaks(line string) []string {
	if !strings.ContainsRune(line, noteBreakMark) {
		return []string{line}
	}
	parts := strings.Split(line, string(noteBreakMark))
	indent := hangingPrefix(ansi.Strip(parts[0]))
	out := []string{parts[0]}
	for _, part := range parts[1:] {
		part = dropFirstSpace(part)
		if strings.TrimFunc(ansi.Strip(part), func(r rune) bool { return r == ' ' || isTaskMark(r) }) == "" {
			out[len(out)-1] += part
			continue
		}
		out = append(out, indent+part)
	}
	return out
}

// hangingPrefix is what a line's later pieces start with: its leading
// spaces, a block quote's bars, and as many spaces as a list bullet or
// task box after them.
func hangingPrefix(plain string) string {
	var b strings.Builder
	rest := plain
	for {
		switch {
		case strings.HasPrefix(rest, " "):
			b.WriteString(" ")
			rest = rest[1:]
			continue
		case strings.HasPrefix(rest, "│"), strings.HasPrefix(rest, "┃"):
			r, size := utf8.DecodeRuneInString(rest)
			b.WriteRune(r)
			rest = rest[size:]
			continue
		}
		break
	}
	word, _, ok := strings.Cut(rest, " ")
	if !ok {
		return b.String()
	}
	bullet := word == "•" || word == "-" || word == "*" || word == "+" ||
		strings.HasPrefix(word, "[") && strings.HasSuffix(word, "]") && utf8.RuneCountInString(word) == 3
	if !bullet && len(word) > 1 && (word[len(word)-1] == '.' || word[len(word)-1] == ')') {
		bullet = strings.Trim(word[:len(word)-1], "0123456789") == ""
	}
	if bullet {
		b.WriteString(strings.Repeat(" ", ansi.StringWidth(word)+1))
	}
	return b.String()
}

// dropFirstSpace removes the first visible character of s when it is a
// space, keeping the escape sequences around it.
func dropFirstSpace(s string) string {
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
					j++
				}
			}
			i = min(len(s), j+1)
			continue
		}
		if s[i] == ' ' {
			return s[:i] + s[i+1:]
		}
		return s
	}
	return s
}

// highlightLine draws a rendered line's text in reverse video, its indent
// and trailing padding plain, at the same width.
func highlightLine(line string) string {
	plain := ansi.Strip(line)
	width := ansi.StringWidth(plain)
	body := strings.TrimLeft(plain, " ")
	indent := len(plain) - len(body)
	body = strings.TrimRight(body, " ")
	if body == "" {
		return line
	}
	out := strings.Repeat(" ", indent) + lipgloss.NewStyle().Reverse(true).Render(body)
	return out + strings.Repeat(" ", max(0, width-indent-ansi.StringWidth(body)))
}
