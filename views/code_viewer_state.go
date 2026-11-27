package views

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Highlight kinds and visual selection modes for the read-only Vim code viewer.
const (
	CodeHighlightSource     = "source"
	CodeHighlightAnnotation = "annotation"

	CodeVisualNone = ""
	CodeVisualChar = "char"
	CodeVisualLine = "line"

	CodeColorAmber   = "amber"
	CodeColorCyan    = "cyan"
	CodeColorBlue    = "blue"
	CodeColorGreen   = "green"
	CodeColorMagenta = "magenta"
	CodeColorRose    = "rose"

	// Distinct default highlight colors: source mentions from the previous page
	// use cool cyan-blue while user annotations use warm amber-gold.
	DefaultSourceHighlightColor     = CodeColorCyan
	DefaultAnnotationHighlightColor = CodeColorAmber
)

// CodeHighlightColorNames lists the built-in highlight color presets offered
// by the annotation and hunk-customization forms.
var CodeHighlightColorNames = []string{
	CodeColorAmber,
	CodeColorCyan,
	CodeColorBlue,
	CodeColorGreen,
	CodeColorMagenta,
	CodeColorRose,
}

// CodeHighlightStyle defines the declarative theme tokens used to paint a
// highlighted code hunk and its gutter marker.
type CodeHighlightStyle struct {
	Name      string
	Tone      string // semantic fg tone or #RRGGBB
	Bg        string // semantic bg tone or #RRGGBB
	BadgeTone string // semantic badge tone
}

var codeHighlightPalette = map[string]CodeHighlightStyle{
	CodeColorCyan:    {Name: CodeColorCyan, Tone: "info", Bg: "#143040", BadgeTone: "info"},
	CodeColorBlue:    {Name: CodeColorBlue, Tone: "accent", Bg: "#1a2b49", BadgeTone: "accent"},
	CodeColorAmber:   {Name: CodeColorAmber, Tone: "warning", Bg: "#3c2d12", BadgeTone: "warning"},
	CodeColorGreen:   {Name: CodeColorGreen, Tone: "success", Bg: "#193320", BadgeTone: "success"},
	CodeColorMagenta: {Name: CodeColorMagenta, Tone: "magenta", Bg: "#311e42", BadgeTone: "state.review"},
	CodeColorRose:    {Name: CodeColorRose, Tone: "error", Bg: "#3e1c27", BadgeTone: "error"},
}

// IsHexColor reports whether s is a #RRGGBB hex color literal.
func IsHexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	_, err := strconv.ParseUint(s[1:], 16, 32)
	return err == nil
}

// ResolveCodeHighlightStyle resolves a named color preset, semantic tone, or
// #RRGGBB hex string into a CodeHighlightStyle.
func ResolveCodeHighlightStyle(colorName, fallbackColor string) CodeHighlightStyle {
	key := strings.ToLower(strings.TrimSpace(colorName))
	if st, ok := codeHighlightPalette[key]; ok {
		return st
	}
	if IsHexColor(key) {
		return CodeHighlightStyle{Name: key, Tone: key, Bg: "surface.focus", BadgeTone: "accent"}
	}
	if st, ok := codeHighlightPalette[strings.ToLower(strings.TrimSpace(fallbackColor))]; ok {
		return st
	}
	return codeHighlightPalette[DefaultSourceHighlightColor]
}

// CodeHighlight represents one highlighted line range in the code viewer,
// either from the referring entry on the previous page ("source") or a
// user-authored durable annotation ("annotation").
type CodeHighlight struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`      // "source" | "annotation"
	StartLine int    `json:"startLine"` // 1-based inclusive
	EndLine   int    `json:"endLine"`   // 1-based inclusive
	Color     string `json:"color,omitempty"`
	Tone      string `json:"tone,omitempty"`
	Label     string `json:"label,omitempty"`
	Note      string `json:"note,omitempty"`
	RecordID  string `json:"recordId,omitempty"`
}

// DefaultColorForKind returns the distinct default color for a highlight kind.
func DefaultColorForKind(kind string) string {
	if kind == CodeHighlightAnnotation {
		return DefaultAnnotationHighlightColor
	}
	return DefaultSourceHighlightColor
}

// EffectiveColor returns the highlight's configured color/tone or the distinct
// default color for its kind.
func (h CodeHighlight) EffectiveColor() string {
	if c := strings.TrimSpace(h.Color); c != "" {
		return c
	}
	if t := strings.TrimSpace(h.Tone); t != "" {
		return t
	}
	return DefaultColorForKind(h.Kind)
}

// Style resolves the highlight's declarative visual style.
func (h CodeHighlight) Style() CodeHighlightStyle {
	return ResolveCodeHighlightStyle(h.EffectiveColor(), DefaultColorForKind(h.Kind))
}

// NormalizedBounds returns sorted 1-based inclusive [start, end] line bounds.
func (h CodeHighlight) NormalizedBounds() (int, int) {
	s, e := h.StartLine, h.EndLine
	if s <= 0 && e > 0 {
		s = e
	}
	if e <= 0 && s > 0 {
		e = s
	}
	if s > e {
		s, e = e, s
	}
	return s, e
}

// Covers reports whether 1-based line lies inside the highlight range.
func (h CodeHighlight) Covers(line int) bool {
	if line <= 0 {
		return false
	}
	s, e := h.NormalizedBounds()
	return s > 0 && line >= s && line <= e
}

// LineRangeText formats the highlight's 1-based line span (e.g. "L12" or "L12-L18").
func (h CodeHighlight) LineRangeText() string {
	s, e := h.NormalizedBounds()
	if s <= 0 {
		return ""
	}
	if s == e {
		return fmt.Sprintf("L%d", s)
	}
	return fmt.Sprintf("L%d-L%d", s, e)
}

// CodeBackTarget records display metadata about the surface that opened the viewer.
type CodeBackTarget struct {
	Page    string
	FocusID string
	Title   string
}

// CodeViewerState is the pure value-object state kernel for the read-only Vim
// code viewer and line/hunk annotator. It has zero store or UI dependencies.
type CodeViewerState struct {
	FilePath        string
	Title           string
	Lines           []string
	CursorLine      int    // 1-based line (1..len(Lines))
	CursorCol       int    // 0-based rune column
	ScrollTop       int    // 0-based top visible line index
	ViewportH       int    // visible code rows
	VisualMode      string // "", "char", "line"
	VisualAnchorRow int    // 1-based anchor line
	VisualAnchorCol int    // 0-based anchor column
	Highlights      []CodeHighlight
	Back            CodeBackTarget
	SearchQuery     string
	SearchDraft     string
	SearchActive    bool
	SearchMatchLine int // 1-based line of last match
	SearchMatchCol  int // 0-based col of last match
}

// NewCodeViewerState builds a CodeViewerState from raw file text and highlights.
// When a "source" highlight is present, the cursor opens on its start line.
func NewCodeViewerState(filePath, title, content string, highlights []CodeHighlight) *CodeViewerState {
	norm := strings.ReplaceAll(content, "\r\n", "\n")
	norm = strings.TrimSuffix(norm, "\n")
	lines := strings.Split(norm, "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	st := &CodeViewerState{
		FilePath:   filePath,
		Title:      title,
		Lines:      lines,
		CursorLine: 1,
		ViewportH:  24,
		Highlights: slices.Clone(highlights),
	}
	for _, h := range st.Highlights {
		if h.Kind == CodeHighlightSource {
			if s, _ := h.NormalizedBounds(); s > 0 {
				st.CursorLine = st.clampLine(s)
				st.centerOnCursor()
				break
			}
		}
	}
	return st
}

// LineCount returns the total number of lines in the buffer.
func (st *CodeViewerState) LineCount() int {
	if st == nil || len(st.Lines) == 0 {
		return 0
	}
	return len(st.Lines)
}

func (st *CodeViewerState) clampLine(line int) int {
	n := st.LineCount()
	if n == 0 {
		return 1
	}
	return min(max(line, 1), n)
}

func (st *CodeViewerState) lineRunes(line int) []rune {
	if st == nil || line < 1 || line > len(st.Lines) {
		return nil
	}
	return []rune(st.Lines[line-1])
}

func (st *CodeViewerState) clampCol() {
	rs := st.lineRunes(st.CursorLine)
	if len(rs) == 0 {
		st.CursorCol = 0
		return
	}
	st.CursorCol = min(max(st.CursorCol, 0), len(rs)-1)
}

func (st *CodeViewerState) effectiveViewport() int {
	if st != nil && st.ViewportH > 0 {
		return st.ViewportH
	}
	return 24
}

func (st *CodeViewerState) centerOnCursor() {
	vh := st.effectiveViewport()
	top := (st.CursorLine - 1) - vh/3
	maxTop := max(st.LineCount()-vh, 0)
	st.ScrollTop = min(max(top, 0), maxTop)
}

// EnsureCursorVisible adjusts ScrollTop so CursorLine stays inside the viewport
// with up to scrollOff context lines above and below.
func (st *CodeViewerState) EnsureCursorVisible(scrollOff int) {
	if st == nil || st.LineCount() == 0 {
		return
	}
	vh := st.effectiveViewport()
	off := min(max(scrollOff, 0), max(vh/2-1, 0))
	cur := st.CursorLine - 1
	if cur < st.ScrollTop+off {
		st.ScrollTop = max(cur-off, 0)
	}
	if cur > st.ScrollTop+vh-1-off {
		st.ScrollTop = cur - vh + 1 + off
	}
	maxTop := max(st.LineCount()-vh, 0)
	st.ScrollTop = min(max(st.ScrollTop, 0), maxTop)
}

func repeatCount(n int) int {
	if n <= 0 {
		return 1
	}
	return n
}

// --- Pure Vim Motions -----------------------------------------------------------

// MoveDown moves the cursor down by count lines (j).
func (st *CodeViewerState) MoveDown(count int) {
	st.CursorLine = st.clampLine(st.CursorLine + repeatCount(count))
	st.clampCol()
	st.EnsureCursorVisible(3)
}

// MoveUp moves the cursor up by count lines (k).
func (st *CodeViewerState) MoveUp(count int) {
	st.CursorLine = st.clampLine(st.CursorLine - repeatCount(count))
	st.clampCol()
	st.EnsureCursorVisible(3)
}

// MoveLeft moves the cursor left by count columns within the current line (h).
func (st *CodeViewerState) MoveLeft(count int) {
	st.CursorCol = max(st.CursorCol-repeatCount(count), 0)
	st.clampCol()
}

// MoveRight moves the cursor right by count columns within the current line (l).
func (st *CodeViewerState) MoveRight(count int) {
	st.CursorCol += repeatCount(count)
	st.clampCol()
}

// MoveLineStart jumps to column 0 of the current line (0).
func (st *CodeViewerState) MoveLineStart() {
	st.CursorCol = 0
}

// MoveFirstNonBlank jumps to the first non-whitespace rune on the current line (^).
func (st *CodeViewerState) MoveFirstNonBlank() {
	rs := st.lineRunes(st.CursorLine)
	for i, r := range rs {
		if !unicode.IsSpace(r) {
			st.CursorCol = i
			return
		}
	}
	st.CursorCol = 0
}

// MoveLineEnd jumps to the last rune of the current line ($).
func (st *CodeViewerState) MoveLineEnd() {
	rs := st.lineRunes(st.CursorLine)
	if len(rs) == 0 {
		st.CursorCol = 0
		return
	}
	st.CursorCol = len(rs) - 1
}

// MoveTop jumps to the first line, or to explicit 1-based line when > 0 (gg).
func (st *CodeViewerState) MoveTop(line int) {
	target := 1
	if line > 0 {
		target = line
	}
	st.CursorLine = st.clampLine(target)
	st.clampCol()
	st.EnsureCursorVisible(3)
}

// MoveBottom jumps to the last line, or to explicit 1-based line when > 0 (G).
func (st *CodeViewerState) MoveBottom(line int) {
	target := st.LineCount()
	if line > 0 {
		target = line
	}
	st.CursorLine = st.clampLine(target)
	st.clampCol()
	st.EnsureCursorVisible(3)
}

// HalfPageDown scrolls and moves down half a viewport (ctrl+d).
func (st *CodeViewerState) HalfPageDown(count int) {
	step := max(st.effectiveViewport()/2, 1) * repeatCount(count)
	st.CursorLine = st.clampLine(st.CursorLine + step)
	st.clampCol()
	st.EnsureCursorVisible(3)
}

// HalfPageUp scrolls and moves up half a viewport (ctrl+u).
func (st *CodeViewerState) HalfPageUp(count int) {
	step := max(st.effectiveViewport()/2, 1) * repeatCount(count)
	st.CursorLine = st.clampLine(st.CursorLine - step)
	st.clampCol()
	st.EnsureCursorVisible(3)
}

// PageDown moves down a full viewport (ctrl+f).
func (st *CodeViewerState) PageDown(count int) {
	step := max(st.effectiveViewport()-2, 1) * repeatCount(count)
	st.CursorLine = st.clampLine(st.CursorLine + step)
	st.clampCol()
	st.EnsureCursorVisible(3)
}

// PageUp moves up a full viewport (ctrl+b).
func (st *CodeViewerState) PageUp(count int) {
	step := max(st.effectiveViewport()-2, 1) * repeatCount(count)
	st.CursorLine = st.clampLine(st.CursorLine - step)
	st.clampCol()
	st.EnsureCursorVisible(3)
}

// ScrollViewport shifts ScrollTop by delta rows while keeping CursorLine visible (ctrl+e / ctrl+y).
func (st *CodeViewerState) ScrollViewport(delta int) {
	vh := st.effectiveViewport()
	maxTop := max(st.LineCount()-vh, 0)
	st.ScrollTop = min(max(st.ScrollTop+delta, 0), maxTop)
	if st.CursorLine-1 < st.ScrollTop {
		st.CursorLine = st.clampLine(st.ScrollTop + 1)
	} else if st.CursorLine-1 > st.ScrollTop+vh-1 {
		st.CursorLine = st.clampLine(st.ScrollTop + vh)
	}
	st.clampCol()
}

func wordCharClass(r rune) int {
	if unicode.IsSpace(r) {
		return 0
	}
	if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
		return 1
	}
	return 2
}

// MoveWordForward moves forward to the start of the next word (w).
func (st *CodeViewerState) MoveWordForward(count int) {
	for i := 0; i < repeatCount(count); i++ {
		st.stepWordForward()
	}
	st.EnsureCursorVisible(3)
}

func (st *CodeViewerState) stepWordForward() {
	rs := st.lineRunes(st.CursorLine)
	col := st.CursorCol
	if col < len(rs) {
		cls := wordCharClass(rs[col])
		for col < len(rs) && wordCharClass(rs[col]) == cls {
			col++
		}
		for col < len(rs) && wordCharClass(rs[col]) == 0 {
			col++
		}
		if col < len(rs) {
			st.CursorCol = col
			return
		}
	}
	st.advanceToNextLineWord()
}

func (st *CodeViewerState) advanceToNextLineWord() {
	for ln := st.CursorLine + 1; ln <= st.LineCount(); ln++ {
		rs := st.lineRunes(ln)
		for i, r := range rs {
			if !unicode.IsSpace(r) {
				st.CursorLine, st.CursorCol = ln, i
				return
			}
		}
		if len(rs) == 0 {
			st.CursorLine, st.CursorCol = ln, 0
			return
		}
	}
	st.MoveLineEnd()
}

// MoveWordBackward moves backward to the start of the previous word (b).
func (st *CodeViewerState) MoveWordBackward(count int) {
	for i := 0; i < repeatCount(count); i++ {
		st.stepWordBackward()
	}
	st.EnsureCursorVisible(3)
}

func (st *CodeViewerState) stepWordBackward() {
	rs := st.lineRunes(st.CursorLine)
	col := min(st.CursorCol, len(rs)) - 1
	for col >= 0 && wordCharClass(rs[col]) == 0 {
		col--
	}
	if col >= 0 {
		cls := wordCharClass(rs[col])
		for col > 0 && wordCharClass(rs[col-1]) == cls {
			col--
		}
		st.CursorCol = col
		return
	}
	st.retreatToPrevLineWord()
}

func (st *CodeViewerState) retreatToPrevLineWord() {
	for ln := st.CursorLine - 1; ln >= 1; ln-- {
		rs := st.lineRunes(ln)
		col := len(rs) - 1
		for col >= 0 && wordCharClass(rs[col]) == 0 {
			col--
		}
		if col >= 0 {
			cls := wordCharClass(rs[col])
			for col > 0 && wordCharClass(rs[col-1]) == cls {
				col--
			}
			st.CursorLine, st.CursorCol = ln, col
			return
		}
		st.CursorLine, st.CursorCol = ln, 0
		return
	}
	st.CursorCol = 0
}

// MoveWordEnd moves forward to the end of the current/next word (e).
func (st *CodeViewerState) MoveWordEnd(count int) {
	for i := 0; i < repeatCount(count); i++ {
		st.stepWordEnd()
	}
	st.EnsureCursorVisible(3)
}

func (st *CodeViewerState) stepWordEnd() {
	rs := st.lineRunes(st.CursorLine)
	col := st.CursorCol + 1
	for col < len(rs) && wordCharClass(rs[col]) == 0 {
		col++
	}
	if col < len(rs) {
		cls := wordCharClass(rs[col])
		for col+1 < len(rs) && wordCharClass(rs[col+1]) == cls {
			col++
		}
		st.CursorCol = col
		return
	}
	st.advanceToNextLineWordEnd()
}

func (st *CodeViewerState) advanceToNextLineWordEnd() {
	for ln := st.CursorLine + 1; ln <= st.LineCount(); ln++ {
		rs := st.lineRunes(ln)
		col := 0
		for col < len(rs) && wordCharClass(rs[col]) == 0 {
			col++
		}
		if col < len(rs) {
			cls := wordCharClass(rs[col])
			for col+1 < len(rs) && wordCharClass(rs[col+1]) == cls {
				col++
			}
			st.CursorLine, st.CursorCol = ln, col
			return
		}
	}
	st.MoveLineEnd()
}

// --- Visual Selection -----------------------------------------------------------

// ToggleVisual enters visual char ("char") or visual line ("line") mode, or
// exits visual mode if already in the requested mode.
func (st *CodeViewerState) ToggleVisual(mode string) {
	if mode != CodeVisualLine {
		mode = CodeVisualChar
	}
	if st.VisualMode == mode {
		st.ClearVisual()
		return
	}
	if st.VisualMode == CodeVisualNone {
		st.VisualAnchorRow = st.CursorLine
		st.VisualAnchorCol = st.CursorCol
	}
	st.VisualMode = mode
}

// ClearVisual exits visual selection mode.
func (st *CodeViewerState) ClearVisual() {
	st.VisualMode = CodeVisualNone
	st.VisualAnchorRow = 0
	st.VisualAnchorCol = 0
}

// VisualActive reports whether visual char or line selection is active.
func (st *CodeViewerState) VisualActive() bool {
	return st != nil && st.VisualMode != CodeVisualNone
}

// SelectedRange returns the sorted 1-based inclusive line range of the active
// visual selection, or (CursorLine, CursorLine, false) when not selecting.
func (st *CodeViewerState) SelectedRange() (startLine, endLine int, active bool) {
	if st == nil {
		return 1, 1, false
	}
	if !st.VisualActive() {
		return st.CursorLine, st.CursorLine, false
	}
	a, b := st.VisualAnchorRow, st.CursorLine
	if a > b {
		a, b = b, a
	}
	return st.clampLine(a), st.clampLine(b), true
}

// SelectedColRange returns the inclusive rune column interval [from, to)
// selected on 1-based line, or (0, 0, false) if line is outside visual selection.
func (st *CodeViewerState) SelectedColRange(line, lineLen int) (int, int, bool) {
	if !st.VisualActive() {
		return 0, 0, false
	}
	sLine, eLine, _ := st.SelectedRange()
	if line < sLine || line > eLine {
		return 0, 0, false
	}
	if st.VisualMode == CodeVisualLine {
		return 0, max(lineLen, 1), true
	}
	aRow, aCol := st.VisualAnchorRow, st.VisualAnchorCol
	bRow, bCol := st.CursorLine, st.CursorCol
	if aRow > bRow || (aRow == bRow && aCol > bCol) {
		aRow, aCol, bRow, bCol = bRow, bCol, aRow, aCol
	}
	from, to := 0, max(lineLen, 1)
	if line == aRow {
		from = min(max(aCol, 0), lineLen)
	}
	if line == bRow {
		to = min(max(bCol+1, from+1), max(lineLen, 1))
	}
	return from, to, true
}

// Snippet returns the joined code text for the 1-based inclusive line range,
// truncated to maxLines when maxLines > 0.
func (st *CodeViewerState) Snippet(startLine, endLine, maxLines int) string {
	if st == nil || st.LineCount() == 0 {
		return ""
	}
	if startLine > endLine {
		startLine, endLine = endLine, startLine
	}
	s := st.clampLine(startLine)
	e := st.clampLine(endLine)
	if maxLines > 0 && e-s+1 > maxLines {
		e = s + maxLines - 1
	}
	return strings.Join(st.Lines[s-1:e], "\n")
}

// --- Highlights -----------------------------------------------------------------

// HighlightsAt returns every highlight covering 1-based line.
func (st *CodeViewerState) HighlightsAt(line int) []CodeHighlight {
	if st == nil || line <= 0 {
		return nil
	}
	var out []CodeHighlight
	for _, h := range st.Highlights {
		if h.Covers(line) {
			out = append(out, h)
		}
	}
	return out
}

// HighlightAt returns the primary highlight covering 1-based line. When both a
// source highlight and an annotation cover the same line, the user's annotation
// takes precedence so it can be hovered, customized, and promoted to a task.
func (st *CodeViewerState) HighlightAt(line int) (CodeHighlight, bool) {
	hits := st.HighlightsAt(line)
	if len(hits) == 0 {
		return CodeHighlight{}, false
	}
	best := hits[0]
	for _, h := range hits[1:] {
		if highlightWins(h, best) {
			best = h
		}
	}
	return best, true
}

func highlightWins(cand, cur CodeHighlight) bool {
	candAnnot := cand.Kind == CodeHighlightAnnotation
	curAnnot := cur.Kind == CodeHighlightAnnotation
	if candAnnot != curAnnot {
		return candAnnot
	}
	cs, ce := cand.NormalizedBounds()
	bs, be := cur.NormalizedBounds()
	return (ce - cs) < (be - bs)
}

// UpsertHighlight inserts or replaces a highlight with matching ID.
func (st *CodeViewerState) UpsertHighlight(h CodeHighlight) {
	if st == nil {
		return
	}
	for i := range st.Highlights {
		if st.Highlights[i].ID == h.ID && h.ID != "" {
			st.Highlights[i] = h
			return
		}
	}
	st.Highlights = append(st.Highlights, h)
}

// NextHighlight jumps the cursor to the start of the next (dir >= 0) or
// previous (dir < 0) highlighted hunk in the file, wrapping around.
func (st *CodeViewerState) NextHighlight(dir int) bool {
	if st == nil || len(st.Highlights) == 0 {
		return false
	}
	starts := make([]int, 0, len(st.Highlights))
	for _, h := range st.Highlights {
		if s, _ := h.NormalizedBounds(); s > 0 {
			starts = append(starts, st.clampLine(s))
		}
	}
	if len(starts) == 0 {
		return false
	}
	slices.Sort(starts)
	starts = slices.Compact(starts)
	target := pickStepLine(starts, st.CursorLine, dir)
	st.CursorLine = target
	st.clampCol()
	st.EnsureCursorVisible(3)
	return true
}

func pickStepLine(sorted []int, cur, dir int) int {
	if dir >= 0 {
		for _, ln := range sorted {
			if ln > cur {
				return ln
			}
		}
		return sorted[0]
	}
	for i := len(sorted) - 1; i >= 0; i-- {
		if sorted[i] < cur {
			return sorted[i]
		}
	}
	return sorted[len(sorted)-1]
}

// --- Pure Vim Search (/, n, N) --------------------------------------------------

// StartSearch opens the inline / search bar.
func (st *CodeViewerState) StartSearch() {
	st.SearchActive = true
	st.SearchDraft = ""
}

// SearchAppend appends text to the active search draft.
func (st *CodeViewerState) SearchAppend(s string) {
	st.SearchDraft += s
}

// SearchBackspace erases the last rune of the active search draft.
func (st *CodeViewerState) SearchBackspace() {
	rs := []rune(st.SearchDraft)
	if len(rs) > 0 {
		st.SearchDraft = string(rs[:len(rs)-1])
	}
}

// CancelSearch closes the search input without changing the active search query.
func (st *CodeViewerState) CancelSearch() {
	st.SearchActive = false
	st.SearchDraft = ""
}

// CommitSearch applies SearchDraft as SearchQuery and jumps to the first match
// at or after the current cursor position.
func (st *CodeViewerState) CommitSearch() bool {
	st.SearchActive = false
	q := st.SearchDraft
	st.SearchDraft = ""
	st.SearchQuery = q
	if q == "" {
		st.SearchMatchLine, st.SearchMatchCol = 0, 0
		return false
	}
	return st.findFrom(st.CursorLine, st.CursorCol, 1)
}

// FindNext jumps to the next (dir >= 0, n) or previous (dir < 0, N) match of SearchQuery.
func (st *CodeViewerState) FindNext(dir int) bool {
	if st == nil || st.SearchQuery == "" || st.LineCount() == 0 {
		return false
	}
	if dir >= 0 {
		return st.findFrom(st.CursorLine, st.CursorCol+1, 1)
	}
	return st.findFrom(st.CursorLine, st.CursorCol-1, -1)
}

func (st *CodeViewerState) findFrom(startLine, startCol, dir int) bool {
	needle := lowerRunes(st.SearchQuery)
	if len(needle) == 0 || st.LineCount() == 0 {
		return false
	}
	n := st.LineCount()
	for step := 0; step <= n; step++ {
		ln := ((startLine-1)+dir*step%n+n)%n + 1
		rs := lowerRunes(st.Lines[ln-1])
		col := matchColInLine(rs, needle, ln, startLine, startCol, step, dir)
		if col >= 0 {
			st.CursorLine, st.CursorCol = ln, col
			st.SearchMatchLine, st.SearchMatchCol = ln, col
			st.EnsureCursorVisible(3)
			return true
		}
	}
	return false
}

func matchColInLine(hay, needle []rune, ln, startLine, startCol, step, dir int) int {
	if dir >= 0 {
		from := 0
		if step == 0 && ln == startLine {
			from = max(startCol, 0)
		}
		return indexRunesFrom(hay, needle, from)
	}
	limit := len(hay) - len(needle)
	if step == 0 && ln == startLine {
		limit = min(limit, startCol)
	}
	return lastIndexRunesUpTo(hay, needle, limit)
}

func lowerRunes(s string) []rune {
	rs := []rune(s)
	for i, r := range rs {
		rs[i] = unicode.ToLower(r)
	}
	return rs
}

func indexRunesFrom(hay, needle []rune, from int) int {
	if len(needle) == 0 || from < 0 || from+len(needle) > len(hay) {
		return -1
	}
	for i := from; i+len(needle) <= len(hay); i++ {
		if runesEqualAt(hay, needle, i) {
			return i
		}
	}
	return -1
}

func lastIndexRunesUpTo(hay, needle []rune, limit int) int {
	if len(needle) == 0 || limit < 0 || len(needle) > len(hay) {
		return -1
	}
	top := min(limit, len(hay)-len(needle))
	for i := top; i >= 0; i-- {
		if runesEqualAt(hay, needle, i) {
			return i
		}
	}
	return -1
}

func runesEqualAt(hay, needle []rune, at int) bool {
	for j, r := range needle {
		if hay[at+j] != r {
			return false
		}
	}
	return true
}

// FindAllMatchCols returns all 0-based rune start offsets where query matches line.
func FindAllMatchCols(line, query string) []int {
	needle := lowerRunes(query)
	if len(needle) == 0 {
		return nil
	}
	hay := lowerRunes(line)
	var cols []int
	for pos := 0; pos+len(needle) <= len(hay); {
		idx := indexRunesFrom(hay, needle, pos)
		if idx < 0 {
			break
		}
		cols = append(cols, idx)
		pos = idx + len(needle)
	}
	return cols
}
