package main

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"ofam.dev/ochazuke"
)

// copyBuf is copy mode's read-only buffer: plain lines (the pane's
// scrollback, then its screen), a cursor, the view's top line and the
// visual anchor. Columns count runes. It knows nothing of the runtime: the
// copy noun's verbs (copymode.go) call its motions, and the global mode says
// which visual shape is active.
type copyBuf struct {
	lines    []string
	row, col int
	goal     int // the column j and k aim for; -1 is the cursor's own
	top      int // the first line on screen
	height   int // the view's height when it was last drawn
	anchor   copyPos
	blockEnd bool // $ in a block selection: every row runs to its end
	search   string
	back     bool // the last search ran upward (?)
	find     copyFind
}

type copyPos struct{ row, col int }

// copyFind is the last f, F, t or T, which ; and , repeat.
type copyFind struct {
	kind string
	char rune
}

func (b *copyBuf) line(row int) []rune {
	if row < 0 || row >= len(b.lines) {
		return nil
	}
	return []rune(b.lines[row])
}

func (b *copyBuf) last() int { return max(0, len(b.lines)-1) }

// clamp keeps the cursor on a character (normal-mode rule: never past the
// last one) and the view around it.
func (b *copyBuf) clamp() {
	b.row = min(max(0, b.row), b.last())
	b.col = min(max(0, b.col), max(0, len(b.line(b.row))-1))
	b.show()
}

// show scrolls the view the least that puts the cursor on screen.
func (b *copyBuf) show() {
	h := max(1, b.height)
	if b.row < b.top {
		b.top = b.row
	}
	if b.row >= b.top+h {
		b.top = b.row - h + 1
	}
	b.top = min(max(0, b.top), max(0, len(b.lines)-1))
}

// setLines replaces the buffer, moving the cursor, anchor and view by
// shift lines (the lines added above them) so they stay on the same text;
// a cursor on the last line follows new output to the new last line.
func (b *copyBuf) setLines(lines []string, shift int) {
	following := b.row >= b.last() && len(b.lines) > 0
	b.lines = lines
	b.row += shift
	b.anchor.row += shift
	b.top += shift
	if following {
		b.row = b.last()
	}
	b.anchor.row = min(max(0, b.anchor.row), b.last())
	b.clamp()
}

// The character classes of w, b and e: blank, a word character, other
// punctuation; W, B and E see only blank and non-blank.
func charClass(r rune, big bool) int {
	switch {
	case unicode.IsSpace(r):
		return 0
	case big:
		return 1
	case r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
		return 1
	}
	return 2
}

// at is the character at p; a line end (and an empty line) reads as a
// newline, which is blank.
func (b *copyBuf) at(p copyPos) rune {
	l := b.line(p.row)
	if p.col >= len(l) {
		return '\n'
	}
	return l[p.col]
}

// next and prev step one position: each character, and the end of every
// line but the last (a newline, blank); an empty line is its own end. ok is
// false at the buffer's ends.
func (b *copyBuf) next(p copyPos) (copyPos, bool) {
	n := len(b.line(p.row))
	switch {
	case p.col < n-1:
		return copyPos{p.row, p.col + 1}, true
	case p.col < n && p.row < b.last():
		return copyPos{p.row, n}, true
	case p.row < b.last():
		return copyPos{p.row + 1, 0}, true
	}
	return p, false
}

func (b *copyBuf) prev(p copyPos) (copyPos, bool) {
	if p.col > 0 {
		return copyPos{p.row, min(p.col, len(b.line(p.row))) - 1}, true
	}
	if p.row == 0 {
		return p, false
	}
	return copyPos{p.row - 1, len(b.line(p.row - 1))}, true
}

func (b *copyBuf) emptyLine(p copyPos) bool { return len(b.line(p.row)) == 0 && p.col == 0 }

// wordForward is w (W with big): past this word, then past blanks, stopping
// on an empty line.
func (b *copyBuf) wordForward(p copyPos, big bool) copyPos {
	start := p
	ok := true
	if c := charClass(b.at(p), big); c != 0 {
		for ok && charClass(b.at(p), big) == c {
			p, ok = b.next(p)
		}
	}
	for ok && charClass(b.at(p), big) == 0 && !(b.emptyLine(p) && p != start) {
		p, ok = b.next(p)
	}
	if !ok {
		return b.lastChar()
	}
	return p
}

// wordEnd is e (E): to the last character of this or the next word.
func (b *copyBuf) wordEnd(p copyPos, big bool) copyPos {
	p, ok := b.next(p)
	for ok && charClass(b.at(p), big) == 0 {
		p, ok = b.next(p)
	}
	if !ok {
		return b.lastChar()
	}
	c := charClass(b.at(p), big)
	for {
		n, ok := b.next(p)
		if !ok || n.row != p.row || charClass(b.at(n), big) != c {
			return p
		}
		p = n
	}
}

// wordBackward is b (B): to the start of this or the previous word.
func (b *copyBuf) wordBackward(p copyPos, big bool) copyPos {
	p, ok := b.prev(p)
	for ok && charClass(b.at(p), big) == 0 && !b.emptyLine(p) {
		p, ok = b.prev(p)
	}
	if !ok {
		return copyPos{}
	}
	if b.emptyLine(p) {
		return p
	}
	c := charClass(b.at(p), big)
	for p.col > 0 && charClass(b.line(p.row)[p.col-1], big) == c {
		p.col--
	}
	return p
}

// wordEndBackward is ge (gE): to the end of the previous word.
func (b *copyBuf) wordEndBackward(p copyPos, big bool) copyPos {
	ok := true
	if c := charClass(b.at(p), big); c != 0 {
		for ok && charClass(b.at(p), big) == c {
			p, ok = b.prev(p)
		}
	}
	for ok && charClass(b.at(p), big) == 0 && !b.emptyLine(p) {
		p, ok = b.prev(p)
	}
	if !ok {
		return copyPos{}
	}
	return p
}

func (b *copyBuf) lastChar() copyPos {
	return copyPos{b.last(), max(0, len(b.line(b.last()))-1)}
}

// firstBlank is the column of the line's first non-blank character (^).
func firstBlank(l []rune) int {
	for i, r := range l {
		if !unicode.IsSpace(r) {
			return i
		}
	}
	return max(0, len(l)-1)
}

// lastBlank is the column of the line's last non-blank character (g_).
func lastBlank(l []rune) int {
	for i := len(l) - 1; i >= 0; i-- {
		if !unicode.IsSpace(l[i]) {
			return i
		}
	}
	return 0
}

// vertical motions keep the goal column and a block selection's $.
var verticalMotions = map[string]bool{"up": true, "down": true, "first-line": true, "last-line": true, "screen-top": true, "screen-middle": true, "screen-bottom": true, "paragraph-forward": true, "paragraph-backward": true}

// move runs one motion steps times; line is a count given to G and gg (0
// is none).
func (b *copyBuf) move(motion string, steps, line int, block bool) error {
	steps = max(1, steps)
	if len(b.lines) == 0 {
		return nil
	}
	if !verticalMotions[motion] {
		b.goal = -1
		if block {
			b.blockEnd = motion == "line-end"
		}
	}
	p := copyPos{b.row, b.col}
	h := max(1, b.height)
	switch motion {
	case "left":
		p.col -= steps
	case "right":
		p.col += steps
	case "up", "down":
		if b.goal < 0 {
			b.goal = b.col
		}
		if motion == "up" {
			p.row -= steps
		} else {
			p.row += steps
		}
		p.col = b.goal
	case "word-forward", "WORD-forward", "word-end", "WORD-end", "word-backward", "WORD-backward", "word-end-backward", "WORD-end-backward":
		big := strings.HasPrefix(motion, "WORD")
		for range steps {
			switch strings.TrimPrefix(strings.TrimPrefix(motion, "word"), "WORD") {
			case "-forward":
				p = b.wordForward(p, big)
			case "-end":
				p = b.wordEnd(p, big)
			case "-backward":
				p = b.wordBackward(p, big)
			case "-end-backward":
				p = b.wordEndBackward(p, big)
			}
		}
	case "line-start":
		p.col = 0
	case "first-blank":
		p.col = firstBlank(b.line(p.row))
	case "line-end":
		p.row += steps - 1
		p.col = max(0, len(b.line(min(p.row, b.last())))-1)
	case "last-blank":
		p.row += steps - 1
		p.col = lastBlank(b.line(min(p.row, b.last())))
	case "first-line", "last-line":
		switch {
		case line > 0:
			p.row = line - 1
		case motion == "first-line":
			p.row = 0
		default:
			p.row = b.last()
		}
		p.col = firstBlank(b.line(min(max(0, p.row), b.last())))
	case "screen-top":
		p.row = b.top + steps - 1
		p.col = firstBlank(b.line(p.row))
	case "screen-middle":
		p.row = b.top + (min(h, len(b.lines)-b.top)-1)/2
		p.col = firstBlank(b.line(p.row))
	case "screen-bottom":
		p.row = min(b.top+h, len(b.lines)) - steps
		p.col = firstBlank(b.line(min(max(0, p.row), b.last())))
	case "paragraph-forward", "paragraph-backward":
		dir := 1
		if motion == "paragraph-backward" {
			dir = -1
		}
		for range steps {
			r := p.row + dir
			for r > 0 && r < b.last() && strings.TrimSpace(b.lines[r]) != "" {
				r += dir
			}
			p.row = min(max(0, r), b.last())
		}
		p.col = 0
	case "match":
		q, ok := b.matchPair(p)
		if !ok {
			return errors.New("no bracket on this line")
		}
		p = q
	default:
		return fmt.Errorf("unknown copy motion %q", motion)
	}
	b.row, b.col = p.row, p.col
	b.clamp()
	return nil
}

var pairs = map[rune]rune{'(': ')', '[': ']', '{': '}', ')': '(', ']': '[', '}': '{'}

// matchPair is %: the bracket under or after the cursor on its line and
// the one that matches it, across lines.
func (b *copyBuf) matchPair(p copyPos) (copyPos, bool) {
	l := b.line(p.row)
	for p.col < len(l) && pairs[l[p.col]] == 0 {
		p.col++
	}
	if p.col >= len(l) {
		return p, false
	}
	open := l[p.col]
	want := pairs[open]
	forward := strings.ContainsRune("([{", open)
	depth := 0
	step := b.next
	if !forward {
		step = b.prev
	}
	for q, ok := p, true; ok; q, ok = step(q) {
		switch b.at(q) {
		case open:
			depth++
		case want:
			depth--
			if depth == 0 {
				return q, true
			}
		}
	}
	return p, false
}

// findChar is f, F, t and T: the count-th occurrence of char on the line,
// on it (f, F) or next to it (t, T). A repeat (; and ,) of t or T skips a
// match right beside the cursor, as Vim's does, so it moves on.
func (b *copyBuf) findChar(kind string, char rune, steps int, repeat bool) error {
	l := b.line(b.row)
	col := b.col
	forward := kind == "f" || kind == "t"
	for n := range max(1, steps) {
		i := col
		for {
			if forward {
				i++
			} else {
				i--
			}
			if i < 0 || i >= len(l) {
				return fmt.Errorf("%q is not on this line", string(char))
			}
			skip := (repeat || n > 0) && (kind == "t" && i == col+1 || kind == "T" && i == col-1)
			if l[i] == char && !skip {
				break
			}
		}
		col = i
	}
	switch kind {
	case "t":
		col--
	case "T":
		col++
	}
	b.goal = -1
	b.col = col
	b.clamp()
	return nil
}

// repeatFind is ; (same direction) and , (reversed).
func (b *copyBuf) repeatFind(reverse bool, steps int) error {
	if b.find.kind == "" {
		return errors.New("no f, F, t or T to repeat")
	}
	kind := b.find.kind
	if reverse {
		kind = map[string]string{"f": "F", "F": "f", "t": "T", "T": "t"}[kind]
	}
	return b.findChar(kind, b.find.char, steps, true)
}

// scroll moves the view by lines (negative is up) and the cursor with it
// when it would leave the screen (ctrl+e, ctrl+y); moveCursor moves the
// cursor by the same lines too (ctrl+d, ctrl+u, ctrl+f, ctrl+b).
func (b *copyBuf) scroll(lines int, moveCursor bool) {
	h := max(1, b.height)
	b.top = min(max(0, b.top+lines), max(0, len(b.lines)-1))
	if moveCursor {
		b.row += lines
		if b.row < 0 || b.row > b.last() {
			b.row = min(max(0, b.row), b.last())
		}
	}
	b.row = min(max(b.row, b.top), b.top+h-1)
	if b.goal < 0 {
		b.goal = b.col
	}
	b.col = b.goal
	b.row = min(max(0, b.row), b.last())
	b.col = min(max(0, b.col), max(0, len(b.line(b.row))-1))
}

// searchFrom finds text from the cursor, down (or up with back), wrapping
// at the buffer's ends; a pattern with no capital letter ignores case.
func (b *copyBuf) searchFrom(text string, back bool, steps int) error {
	if text == "" {
		return errors.New("no search pattern")
	}
	fold := strings.ToLower(text) == text
	needle := []rune(text)
	match := func(p copyPos) bool {
		l := b.line(p.row)
		if p.col < 0 || p.col+len(needle) > len(l) {
			return false
		}
		for i, r := range needle {
			c := l[p.col+i]
			if fold {
				c = unicode.ToLower(c)
			}
			if c != r {
				return false
			}
		}
		return true
	}
	p := copyPos{b.row, b.col}
	for range max(1, steps) {
		q, ok := b.scanFrom(p, back, match)
		if !ok {
			return fmt.Errorf("pattern not found: %s", text)
		}
		p = q
	}
	b.row, b.col, b.goal = p.row, p.col, -1
	b.clamp()
	return nil
}

// scanFrom visits every character after p (before it with back), wrapping
// once around the buffer, and returns the first that match accepts.
func (b *copyBuf) scanFrom(p copyPos, back bool, match func(copyPos) bool) (copyPos, bool) {
	n := len(b.lines)
	for i := 0; i <= n; i++ {
		row := (p.row + i) % n
		if back {
			row = ((p.row-i)%n + n) % n
		}
		l := b.line(row)
		lo, hi := 0, len(l)-1
		switch {
		case i == 0 && !back:
			lo = p.col + 1
		case i == 0 && back:
			hi = p.col - 1
		case i == n && !back:
			hi = p.col
		case i == n && back:
			lo = p.col
		}
		if back {
			for col := hi; col >= lo; col-- {
				if match(copyPos{row, col}) {
					return copyPos{row, col}, true
				}
			}
			continue
		}
		for col := lo; col <= hi; col++ {
			if match(copyPos{row, col}) {
				return copyPos{row, col}, true
			}
		}
	}
	return p, false
}

// selection is the selected text for a selection shape (ochazuke's
// SelectionChar, SelectionLine or SelectionBlock) and the yank's shape and
// width.
func (b *copyBuf) selection(sel string) (text, shape string, width int) {
	a, c := b.anchor, copyPos{b.row, b.col}
	switch sel {
	case ochazuke.SelectionLine:
		top, bottom := min(a.row, c.row), max(a.row, c.row)
		return strings.Join(b.lines[top:bottom+1], "\n") + "\n", "line", 0
	case ochazuke.SelectionBlock:
		top, bottom, left, right := b.blockBounds()
		rows := make([]string, 0, bottom-top+1)
		for r := top; r <= bottom; r++ {
			l := b.line(r)
			end := right + 1
			if b.blockEnd {
				end = len(l)
			}
			if left >= len(l) {
				rows = append(rows, "")
				continue
			}
			rows = append(rows, string(l[left:min(end, len(l))]))
		}
		width := right - left + 1
		if b.blockEnd {
			width = 0
		}
		return strings.Join(rows, "\n"), "block", width
	}
	if c.row < a.row || c.row == a.row && c.col < a.col {
		a, c = c, a
	}
	if a.row == c.row {
		l := b.line(a.row)
		return string(l[min(a.col, len(l)):min(c.col+1, len(l))]), "char", 0
	}
	var out []string
	first := b.line(a.row)
	out = append(out, string(first[min(a.col, len(first)):]))
	out = append(out, b.lines[a.row+1:c.row]...)
	lastLine := b.line(c.row)
	out = append(out, string(lastLine[:min(c.col+1, len(lastLine))]))
	return strings.Join(out, "\n"), "char", 0
}

// blockBounds is the block selection's rows and columns, inclusive.
// With $ the cursor's column is every line's end, so the anchor's column
// is the left edge.
func (b *copyBuf) blockBounds() (top, bottom, left, right int) {
	a, c := b.anchor, copyPos{b.row, b.col}
	if b.blockEnd {
		return min(a.row, c.row), max(a.row, c.row), a.col, max(a.col, c.col)
	}
	return min(a.row, c.row), max(a.row, c.row), min(a.col, c.col), max(a.col, c.col)
}

// selected reports the selected columns of a row, [lo, hi), for drawing;
// hi past the line's end marks its line end selected too.
func (b *copyBuf) selected(shape string, row int) (lo, hi int, ok bool) {
	a, c := b.anchor, copyPos{b.row, b.col}
	n := len(b.line(row))
	switch shape {
	case ochazuke.SelectionLine:
		if row < min(a.row, c.row) || row > max(a.row, c.row) {
			return 0, 0, false
		}
		return 0, n + 1, true
	case ochazuke.SelectionBlock:
		top, bottom, left, right := b.blockBounds()
		if row < top || row > bottom {
			return 0, 0, false
		}
		if b.blockEnd {
			return left, max(left+1, n), true
		}
		return left, right + 1, true
	case ochazuke.SelectionChar:
		if c.row < a.row || c.row == a.row && c.col < a.col {
			a, c = c, a
		}
		if row < a.row || row > c.row {
			return 0, 0, false
		}
		lo, hi = 0, n+1
		if row == a.row {
			lo = a.col
		}
		if row == c.row {
			hi = c.col + 1
		}
		return lo, hi, true
	}
	return 0, 0, false
}
