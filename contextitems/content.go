package contextitems

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// KindRef identifies Matomeru record IDs (e.g. task:abc123) and mtm:// deep links.
const KindRef = "ref"

var (
	mtmLinkRe   = regexp.MustCompile(`mtm://[^\s<>"{}|\\^` + "`" + `]+`)
	recordRefRe = regexp.MustCompile(`\b(?:task|note|pane|lane|document|doc|page|question|command|context|run|schedule|signal|extension):[A-Za-z0-9][A-Za-z0-9._-]{2,}\b`)
)

// ExtractContentMatches scans visible rendered screen lines for all actionable
// content spans: OSC-8 hyperlinks, URLs, PR refs, file paths, Matomeru record
// references/deep links, and visible inline code spans.
func ExtractContentMatches(lines []string) []Match {
	if len(lines) == 0 {
		return nil
	}
	base := ExtractVisibleMatches(lines)
	byRow := make(map[int][]Match, len(lines))
	occByRow := make(map[int][][2]int, len(lines))
	for _, m := range base {
		byRow[m.Row] = append(byRow[m.Row], m)
		occByRow[m.Row] = append(occByRow[m.Row], [2]int{m.StartCol, m.EndCol})
	}

	var all []Match
	for r, raw := range lines {
		plain, _ := parseLineANSI(raw)
		rowMatches := byRow[r]
		occupied := occByRow[r]

		for _, extra := range scanContentExtras(plain, r) {
			if extra.Kind == KindCode && strings.ContainsAny(extra.Value, " \t") {
				rowMatches, occupied = dropContainedMatches(rowMatches, occupied, extra.StartCol, extra.EndCol)
			}
			if overlapsCols(occupied, extra.StartCol, extra.EndCol) {
				continue
			}
			rowMatches = append(rowMatches, extra)
			occupied = append(occupied, [2]int{extra.StartCol, extra.EndCol})
		}
		if len(rowMatches) == 0 {
			continue
		}
		slices.SortFunc(rowMatches, func(a, b Match) int {
			return cmp.Compare(a.StartCol, b.StartCol)
		})
		all = append(all, rowMatches...)
	}
	return all
}

func dropContainedMatches(matches []Match, occ [][2]int, startCol, endCol int) ([]Match, [][2]int) {
	kept := matches[:0]
	keptOcc := occ[:0]
	for i, m := range matches {
		if m.StartCol >= startCol && m.EndCol <= endCol {
			continue
		}
		kept = append(kept, m)
		keptOcc = append(keptOcc, occ[i])
	}
	return kept, keptOcc
}

func scanContentExtras(line string, row int) []Match {
	if strings.TrimSpace(line) == "" {
		return nil
	}
	var matches []Match
	var byteSpans [][2]int

	for _, sp := range mtmLinkRe.FindAllStringIndex(line, -1) {
		cleaned := trimURLTrailing(line[sp[0]:sp[1]])
		if cleaned == "" {
			continue
		}
		endByte := sp[0] + len(cleaned)
		matches = append(matches, Match{
			Row:      row,
			StartCol: byteOffsetToCol(line, sp[0]),
			EndCol:   byteOffsetToCol(line, endByte),
			Kind:     KindRef,
			Value:    cleaned,
		})
		byteSpans = append(byteSpans, [2]int{sp[0], endByte})
	}

	for _, sp := range recordRefRe.FindAllStringIndex(line, -1) {
		if overlapsCols(byteSpans, sp[0], sp[1]) {
			continue
		}
		if sp[0] > 0 {
			prev, _ := utf8.DecodeLastRuneInString(line[:sp[0]])
			if prev == '/' || prev == ':' || unicode.IsLetter(prev) || unicode.IsDigit(prev) {
				continue
			}
		}
		val := trimPathTrailing(line[sp[0]:sp[1]])
		if val == "" {
			continue
		}
		endByte := sp[0] + len(val)
		matches = append(matches, Match{
			Row:      row,
			StartCol: byteOffsetToCol(line, sp[0]),
			EndCol:   byteOffsetToCol(line, endByte),
			Kind:     KindRef,
			Value:    val,
		})
		byteSpans = append(byteSpans, [2]int{sp[0], endByte})
	}

	matches = append(matches, scanVisibleInlineCode(line, row, byteSpans)...)
	return matches
}

func scanVisibleInlineCode(line string, row int, occupied [][2]int) []Match {
	if !strings.ContainsRune(line, '`') {
		return nil
	}
	var out []Match
	for i := 0; i < len(line); {
		if line[i] != '`' {
			i++
			continue
		}
		j := i
		for j < len(line) && line[j] == '`' {
			j++
		}
		tickLen := j - i
		closeStart, closeEnd := findClosingBacktickRun(line, j, tickLen)
		if closeStart < 0 {
			i = j
			continue
		}
		body := strings.TrimSpace(line[j:closeStart])
		if len([]rune(body)) >= 2 && !overlapsCols(occupied, i, closeEnd) {
			out = append(out, Match{
				Row:      row,
				StartCol: byteOffsetToCol(line, i),
				EndCol:   byteOffsetToCol(line, closeEnd),
				Kind:     KindCode,
				Value:    body,
			})
			occupied = append(occupied, [2]int{i, closeEnd})
		}
		i = closeEnd
	}
	return out
}
