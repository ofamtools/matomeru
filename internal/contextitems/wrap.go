package contextitems

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Hard wraps: a program that wraps its own output (Claude Code, omp, a
// box drawn around a tool's output) breaks a link longer than its row at
// the row's edge and goes on with it on the next row, after an indent, a
// gutter or a box border. The terminal knows nothing of it: no soft-wrap
// flag, the rows are hard newlines. hardJoin finds those breaks from the
// screen's own shape, the text margins its rows share, and joins the
// pieces with the padding between them left out.
//
// A link's last piece on a row is joined to the start of the next row when
// all of these hold:
//
//   - it is a URL, or a path with a slash, and runs to the end of the row's
//     text (trailing spaces and a box border do not count);
//   - the row's text reaches its right margin (within wrapSlack cells): a
//     box's inner edge, else the widest text on the screen. A program
//     breaks a link only where the row is full; a row that ends short
//     ended there on purpose;
//   - the next row has the same left border (none, or the same │), and
//     its text starts with characters that can go on with the link, not
//     with a new URL or path;
//   - the continuation is alone on its row, or it reads as a link's
//     (a slash, ?, #, =, &, % or a dot inside it, or the piece before it
//     ends at a separator), so a URL ending a row is not run into the
//     next row's first word;
//   - the joined text is still a link of that kind (a URL with a host; a
//     path classifyPath accepts, and not a missing path where the piece
//     alone is a file that exists).
//
// A link is followed over at most maxWrapRows rows.
//
// The rule for which rows continue is tuios' (internal/app/link_hover.go,
// bareLinkAt and its linkWrapRows bound, which follow a URL across the
// emulator's soft-wrap flags); the margins and the continuation tests are
// mtm's, since these rows have no flag to read.

// maxWrapRows bounds how many rows one link is followed across.
const maxWrapRows = 4

// wrapSlack is how far short of its right margin a row's text may end and
// still be full.
const wrapSlack = 2

// borders are the box and gutter rules a program draws at a row's edges.
const borders = "│┃║|▏▕▌▐╎╏┆┇┊┋"

// rowShape is one screen row's layout: its left border (0 for none),
// where its text starts and ends (cells; end exclusive, trailing spaces
// and border left out) and the column of its right border (-1: none).
type rowShape struct {
	lead       rune
	start, end int
	border     int
	blank      bool
}

// layout is the screen's shapes, by row, and the margins learned from
// them: left is the column most rows' text starts at (the text column; a
// gutter or padding lies left of it), right the widest text of the rows
// with no right border.
type layout struct {
	rows        map[int]rowShape
	left, right int
}

func isBorder(r rune) bool { return strings.ContainsRune(borders, r) }

// learnLayout reads the shape of every row (lines as screenLine made them,
// one per row, before any join).
func learnLayout(lines []mline) layout {
	l := layout{rows: make(map[int]rowShape, len(lines))}
	starts := map[int]int{}
	for _, line := range lines {
		if len(line.at) == 0 {
			continue
		}
		row := line.at[0].Row
		sh := rowShape{border: -1}
		runes := []rune(line.s)
		cols := runeCols(line)
		i, j := 0, len(runes)
		for i < j && unicode.IsSpace(runes[i]) {
			i++
		}
		if i < j && isBorder(runes[i]) {
			sh.lead = runes[i]
			i++
			for i < j && unicode.IsSpace(runes[i]) {
				i++
			}
		}
		for j > i && unicode.IsSpace(runes[j-1]) {
			j--
		}
		if j > i && isBorder(runes[j-1]) && (sh.lead != 0 || j-1 > i) {
			sh.border = cols[j-1][0]
			j--
			for j > i && unicode.IsSpace(runes[j-1]) {
				j--
			}
		}
		if i >= j {
			sh.blank = true
			l.rows[row] = sh
			continue
		}
		sh.start, sh.end = cols[i][0], cols[j-1][1]
		l.rows[row] = sh
		if sh.lead == 0 {
			starts[sh.start]++
		}
		if sh.border < 0 {
			l.right = max(l.right, sh.end)
		}
	}
	best := -1
	for col, n := range starts {
		if best < 0 || n > starts[best] || n == starts[best] && col < best {
			best = col
		}
	}
	l.left = max(best, 0)
	return l
}

// runeCols is the cells [col, end) of each rune of a tracked line.
func runeCols(line mline) [][2]int {
	out := make([][2]int, 0, len(line.s))
	for i := range line.s {
		if !utf8.RuneStart(line.s[i]) {
			continue
		}
		c := line.at[i]
		out = append(out, [2]int{c.Col, c.End})
	}
	return out
}

// full reports whether text ending at cell end reaches the row's right
// margin.
func (l layout) full(row, end int) bool {
	sh := l.rows[row]
	limit := l.right
	if sh.border >= 0 {
		limit = sh.border - 1
	}
	return limit > 0 && end >= limit-wrapSlack
}

// hardJoin joins the links a program wrapped across lines (the rule
// above). Joined lines move down: the line holding the link's start is
// left empty and the next holds the joined text, so the lines keep their
// count, as unwrap did.
func hardJoin(lines []mline, l layout, cwd string, stat StatFunc) []mline {
	rows := 1
	for i := 0; i+1 < len(lines); i++ {
		joined, alone, ok := joinNext(lines[i], lines[i+1], l, cwd, stat)
		if !ok || rows >= maxWrapRows {
			rows = 1
			continue
		}
		lines[i+1], lines[i] = joined, mline{}
		rows++
		if !alone {
			rows = 1 // the row goes on with other text: the link ends there
		}
	}
	return lines
}

// The kinds of piece hardJoin follows: a URL or a path.
const (
	pieceURL = iota + 1
	piecePath
)

// joinNext joins cur's last link with the start of next, reporting
// whether the continuation was all of next's text.
func joinNext(cur, next mline, l layout, cwd string, stat StatFunc) (joined mline, alone, ok bool) {
	if len(cur.at) == 0 || len(next.at) == 0 {
		return mline{}, false, false
	}
	end := len(strings.TrimRightFunc(cur.s, unicode.IsSpace))
	if end == 0 {
		return mline{}, false, false
	}
	row := cur.at[end-1].Row
	sh, known := l.rows[row]
	if !known {
		return mline{}, false, false
	}
	if sh.border >= 0 && cur.at[end-1].Col == sh.border {
		_, n := utf8.DecodeLastRuneInString(cur.s[:end])
		end = len(strings.TrimRightFunc(cur.s[:end-n], unicode.IsSpace))
		if end == 0 {
			return mline{}, false, false
		}
	}
	if !l.full(row, cur.at[end-1].End) {
		return mline{}, false, false
	}
	start := 0
	if at := strings.LastIndexFunc(cur.s[:end], func(r rune) bool { return unicode.IsSpace(r) || isBorder(r) }); at >= 0 {
		_, n := utf8.DecodeRuneInString(cur.s[at:])
		start = at + n
	}
	piece, kind := linkPiece(cur.s[start:end])
	if kind == 0 {
		return mline{}, false, false
	}

	nrow := next.at[0].Row
	nsh, known := l.rows[nrow]
	if !known || nsh.blank || nsh.lead != sh.lead {
		return mline{}, false, false
	}
	k := len(next.s) - len(strings.TrimLeftFunc(next.s, unicode.IsSpace))
	if nsh.lead != 0 {
		_, n := utf8.DecodeRuneInString(next.s[k:])
		k += n
		k += len(next.s[k:]) - len(strings.TrimLeftFunc(next.s[k:], unicode.IsSpace))
	} else if k < len(next.s) && next.at[k].Col < l.left {
		// Text in the gutter, left of the screen's text column, is not
		// the response's.
		return mline{}, false, false
	}
	cEnd := k
	for cEnd < len(next.s) && continues(kind, next.s[cEnd]) {
		cEnd++
	}
	if cEnd == k {
		return mline{}, false, false
	}
	cont := next.s[k:cEnd]
	// A link goes on with a letter, a digit or a separator, and has a
	// letter or digit in it: "$ " (a prompt) or ")." is not its rest.
	if !opensContinuation(cont) || startsNewLink(kind, cont, cwd, stat) {
		return mline{}, false, false
	}
	rest := strings.TrimSpace(next.s[cEnd:])
	if nsh.border >= 0 {
		rest = strings.TrimSpace(strings.TrimRightFunc(rest, isBorder))
	}
	alone = rest == ""
	if !alone && !readsAsLink(piece, cont) {
		return mline{}, false, false
	}
	if !stillLink(kind, piece, piece+cont, cwd, stat) {
		return mline{}, false, false
	}
	return cur.cut(0, end).concat(next.cut(k, len(next.s))), alone, true
}

// linkPiece is the link a token ends with and its kind: a URL, else a
// path with a slash in it (a bare word is prose more often than a file).
func linkPiece(tok string) (string, int) {
	if at := linkPattern.FindAllStringIndex(tok, -1); len(at) > 0 && at[len(at)-1][1] == len(tok) {
		return tok[at[len(at)-1][0]:], pieceURL
	}
	if at := pathPattern.FindAllStringIndex(tok, -1); len(at) > 0 && at[len(at)-1][1] == len(tok) {
		if p := tok[at[len(at)-1][0]:]; strings.Contains(p, "/") && !strings.Contains(p, "://") {
			return p, piecePath
		}
	}
	return "", 0
}

// continues reports whether byte b can go on with a link of kind.
func continues(kind int, b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case kind == pieceURL:
		return strings.IndexByte("-._~:/?#[]@!$&()*+,;=%", b) >= 0
	}
	return strings.IndexByte("-._~/+@%=:,", b) >= 0
}

// opensContinuation reports whether cont can be the rest of a link: it
// starts with a letter, a digit or a separator and holds a letter or a
// digit.
func opensContinuation(cont string) bool {
	alnum := func(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' }
	if !alnum(cont[0]) && strings.IndexByte("/?#&=%-_.~+", cont[0]) < 0 {
		return false
	}
	for i := range len(cont) {
		if alnum(cont[i]) {
			return true
		}
	}
	return false
}

// startsNewLink reports a continuation that is a link of its own: a URL,
// a path that starts a new one (/, ~/, ./, ../) after a path, or after a
// URL an absolute path that exists.
func startsNewLink(kind int, cont, cwd string, stat StatFunc) bool {
	if strings.HasPrefix(cont, "http://") || strings.HasPrefix(cont, "https://") {
		return true
	}
	if kind == piecePath {
		for _, p := range []string{"/", "~/", "./", "../"} {
			if strings.HasPrefix(cont, p) {
				return true
			}
		}
		return false
	}
	if strings.HasPrefix(cont, "/") && strings.Count(strings.Trim(cont, "/"), "/") > 0 {
		if _, _, exists, ok := classifyPath(trimPath(cont), cwd, stat); ok && exists {
			return true
		}
	}
	return false
}

// readsAsLink reports a continuation that reads as a link's rest rather
// than a word: a separator inside it or at its start, or a piece ending at
// one.
func readsAsLink(piece, cont string) bool {
	if strings.ContainsAny(piece[len(piece)-1:], "/-_.?&=#%+:~") {
		return true
	}
	if strings.ContainsAny(cont[:1], "/.-_?&#=%") {
		return true
	}
	inner := strings.TrimRight(cont, ".,;:!)]}'")
	return strings.ContainsAny(inner, "/?#=&%.")
}

// stillLink reports whether joined is a link of kind, as find would take
// it.
func stillLink(kind int, piece, joined, cwd string, stat StatFunc) bool {
	if kind == pieceURL {
		u, err := url.Parse(strings.TrimRight(joined, ".,;:!'"))
		return err == nil && u.Host != ""
	}
	raw := trimPath(joined)
	if raw == "" {
		return false
	}
	_, _, exists, ok := classifyPath(raw, cwd, stat)
	if !ok {
		return false
	}
	if !exists {
		if p := trimPath(piece); p != "" {
			if _, _, pieceExists, ok := classifyPath(p, cwd, stat); ok && pieceExists {
				return false
			}
		}
	}
	return true
}
