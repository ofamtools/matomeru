package contextitems

import (
	"strings"
	"unicode/utf8"
)

const maxCodeBlockBytes = 16 * 1024

type parsedCodeBlock struct {
	body     string
	language string
	inline   bool
}

// extractFencedCodeBlocks parses CommonMark fenced code blocks (backticks or
// tildes, >= 3 chars, 0..3 leading spaces, nested fences, and unclosed fences
// to EOF) from ANSI-stripped lines. It returns the extracted code blocks and
// the remaining lines outside fenced blocks (with blank separators inserted
// where fenced blocks were removed so soft-wrap joining and inline scanning
// never cross a fenced block boundary).
func extractFencedCodeBlocks(lines []string) ([]parsedCodeBlock, []string) {
	var (
		blocks  []parsedCodeBlock
		outside []string

		inFence     bool
		fenceChar   byte
		fenceLen    int
		fenceIndent int
		fenceLang   string
		bodyLines   []string
	)

	flushFence := func() {
		body := strings.TrimRight(strings.Join(bodyLines, "\n"), "\n")
		if strings.TrimSpace(body) != "" {
			blocks = append(blocks, parsedCodeBlock{
				body:     truncateUTF8Bytes(body, maxCodeBlockBytes),
				language: fenceLang,
				inline:   false,
			})
		}
		inFence = false
		fenceChar = 0
		fenceLen = 0
		fenceIndent = 0
		fenceLang = ""
		bodyLines = nil
		if len(outside) > 0 && outside[len(outside)-1] != "" {
			outside = append(outside, "")
		}
	}

	for _, line := range lines {
		if !inFence {
			if ch, n, indent, lang, ok := parseFenceOpen(line); ok {
				inFence = true
				fenceChar = ch
				fenceLen = n
				fenceIndent = indent
				fenceLang = lang
				bodyLines = nil
				continue
			}
			outside = append(outside, line)
			continue
		}

		if isFenceClose(line, fenceChar, fenceLen) {
			flushFence()
			continue
		}
		bodyLines = append(bodyLines, stripIndentSpaces(line, fenceIndent))
	}

	if inFence {
		flushFence()
	}
	return blocks, outside
}

// parseFenceOpen checks whether line opens a CommonMark fenced code block.
func parseFenceOpen(line string) (ch byte, fenceLen, indent int, lang string, ok bool) {
	indent = countLeadingSpaces(line)
	if indent > 3 || indent >= len(line) {
		return 0, 0, 0, "", false
	}
	ch = line[indent]
	if ch != '`' && ch != '~' {
		return 0, 0, 0, "", false
	}
	i := indent
	for i < len(line) && line[i] == ch {
		i++
	}
	fenceLen = i - indent
	if fenceLen < 3 {
		return 0, 0, 0, "", false
	}
	info := line[i:]
	// CommonMark §4.5: backtick fence info strings cannot contain backticks.
	if ch == '`' && strings.IndexByte(info, '`') >= 0 {
		return 0, 0, 0, "", false
	}
	if fields := strings.Fields(info); len(fields) > 0 {
		lang = fields[0]
	}
	return ch, fenceLen, indent, lang, true
}

// isFenceClose checks whether line closes an open CommonMark fenced code block.
func isFenceClose(line string, fenceChar byte, minLen int) bool {
	indent := countLeadingSpaces(line)
	if indent > 3 || indent >= len(line) || line[indent] != fenceChar {
		return false
	}
	i := indent
	for i < len(line) && line[i] == fenceChar {
		i++
	}
	if i-indent < minLen {
		return false
	}
	return strings.TrimSpace(line[i:]) == ""
}

func countLeadingSpaces(line string) int {
	n := 0
	for n < len(line) && line[n] == ' ' {
		n++
	}
	return n
}

func stripIndentSpaces(line string, maxStrip int) string {
	n := 0
	for n < maxStrip && n < len(line) && line[n] == ' ' {
		n++
	}
	return line[n:]
}

// extractInlineCodeSpans parses CommonMark inline code spans from lines
// outside fenced blocks. Spans never cross blank-line paragraph boundaries.
func extractInlineCodeSpans(lines []string) []parsedCodeBlock {
	var out []parsedCodeBlock
	var para []string

	flushPara := func() {
		if len(para) == 0 {
			return
		}
		out = append(out, scanInlineCodeParagraph(strings.Join(para, "\n"))...)
		para = nil
	}

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			flushPara()
			continue
		}
		para = append(para, line)
	}
	flushPara()
	return out
}

func scanInlineCodeParagraph(s string) []parsedCodeBlock {
	if !strings.ContainsRune(s, '`') {
		return nil
	}
	var out []parsedCodeBlock
	for i := 0; i < len(s); {
		if s[i] != '`' {
			i++
			continue
		}
		j := i
		for j < len(s) && s[j] == '`' {
			j++
		}
		tickLen := j - i

		closeStart, closeEnd := findClosingBacktickRun(s, j, tickLen)
		if closeStart < 0 {
			// Unmatched backtick run: literal text per CommonMark §6.1.
			i = j
			continue
		}

		// Per CommonMark §6.1: line endings become spaces, and if the span
		// starts and ends with a space (and is not all spaces), strip one space
		// from each end.
		raw := strings.ReplaceAll(s[j:closeStart], "\n", " ")
		if len(raw) >= 2 && raw[0] == ' ' && raw[len(raw)-1] == ' ' && strings.Trim(raw, " ") != "" {
			raw = raw[1 : len(raw)-1]
		}
		if strings.TrimSpace(raw) != "" {
			out = append(out, parsedCodeBlock{
				body:   truncateUTF8Bytes(raw, maxCodeBlockBytes),
				inline: true,
			})
		}
		i = closeEnd
	}
	return out
}

func findClosingBacktickRun(s string, from, wantLen int) (start, end int) {
	for k := from; k < len(s); {
		if s[k] != '`' {
			k++
			continue
		}
		m := k
		for m < len(s) && s[m] == '`' {
			m++
		}
		if m-k == wantLen {
			return k, m
		}
		k = m
	}
	return -1, -1
}

// isNavTargetCodeSpan reports whether an inline code span is solely a single
// navigable URL, PR ref, or file path already extracted by scanPlainLine.
func isNavTargetCodeSpan(body string, navValues map[string]bool) bool {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return true
	}
	unprefixed := strings.TrimPrefix(trimmed, "@")
	if navValues[trimmed] || navValues[unprefixed] {
		return true
	}
	if t := trimURLTrailing(trimmed); t != "" && navValues[t] {
		return true
	}
	if t := trimPathTrailing(unprefixed); t != "" && navValues[t] {
		return true
	}
	matches := scanPlainLine(trimmed, 0)
	if len(matches) == 1 {
		m := matches[0]
		if m.Value == trimmed || m.Value == unprefixed || m.Value == trimURLTrailing(trimmed) || m.Value == trimPathTrailing(unprefixed) {
			return true
		}
	}
	return false
}

func truncateUTF8Bytes(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
