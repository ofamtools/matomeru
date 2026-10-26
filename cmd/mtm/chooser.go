package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"
)

// A chooser is a command-palette-shaped modal: a search input focused in
// insert mode over a filtered list. Enter takes the top match unless the
// highlight was moved, and creates from the query when nothing matches. No
// key creates: with CreateRow, creating is a row the highlight moves onto.
// It knows nothing of the application: rows, the
// filter and the choose/create callbacks come from the caller, and its keys
// are rows on its own noun, so it can move into ochazuke as a component.

// ChooserRow is one choice. Trailing rows (a "browse…" entry) follow the
// matches. Enter takes one without moving onto it only when the query is
// not empty and no ordinary row matches, so their filter should be strict
// (a substring of the label); an empty query with no ordinary rows creates.
// Fixed rows (a "no pin" entry) sit at the bottom whatever the query: the
// filter never sees them and Enter takes one only once the highlight is
// moved onto it.
type ChooserRow struct {
	ID       string
	Label    string
	Data     any
	Trailing bool
	Fixed    bool
	// Cells, when set, draw the row as aligned columns (each column as
	// wide as its widest cell among the rows); Label stays what the
	// filter reads.
	Cells []string
}

// ChooserSpec opens a chooser. Kind is the modal's kind (one open chooser
// per kind). Filter defaults to a fuzzy match on Label; matches whose label
// holds the query whole come first, otherwise in Rows order. Create is optional;
// without it Enter on no match fails. CreateLabel names what Enter would
// create from the query. CreateRow shows that as a "+ <label>" row while
// the query is not empty, after the matches and trailing rows and before
// the fixed rows; typing never matches or ranks it, it is the default only
// when nothing else matches, and Enter on it creates. Empty is the line
// shown while there are no rows at all ("loading…").
type ChooserSpec struct {
	Kind        string
	Title       string
	Rows        []ChooserRow
	Filter      func(query string, row ChooserRow) bool
	Choose      func(ChooserRow) ochazuke.Result
	Create      func(query string) ochazuke.Result
	CreateLabel func(query string) string
	CreateRow   bool
	Empty       string
	// Keep names the columns cut last when the cells do not fit (a
	// directory chooser's path): the others narrow to their minimum first.
	Keep []int
	// Preview, when set, draws the highlighted row's text beside the rows
	// (wrapped, cut at the bottom) once the chooser is chooserPreviewMin
	// cells wide; narrower, the rows take the whole width.
	Preview func(ChooserRow) string
}

// chooserPreviewMin is the width from which a chooser with a Preview
// draws it beside the rows.
const chooserPreviewMin = 60

// chooserCreateRow is the ID of the create row (see ChooserSpec.CreateRow).
const chooserCreateRow = "chooser:create"

// Chooser is one open chooser.
type Chooser struct {
	spec    ChooserSpec
	input   *ochazuke.Input
	seen    string
	matches []ChooserRow
	// cursor indexes matches: the highlighted row, -1 for none. Until the
	// user moves it, it is the top match (see ChooserRow on trailing rows).
	cursor int
	moved  bool
}

// Choosers registers the chooser noun once per runtime and tracks the open
// choosers by kind; views is the model's modal view table.
type Choosers struct {
	r     *ochazuke.Runtime
	views map[string]ochazuke.ModalView
	open  map[string]*Chooser
}

const chooserNoun = "chooser"

func NewChoosers(r *ochazuke.Runtime, views map[string]ochazuke.ModalView) *Choosers {
	cs := &Choosers{r: r, views: views, open: map[string]*Chooser{}}
	verb := func(fn func(*Chooser, ochazuke.Ctx) ochazuke.Result) ochazuke.AppHandler {
		return func(ctx ochazuke.Ctx) ochazuke.Result {
			c := cs.at(ctx.Instance)
			if c == nil {
				return ochazuke.Failed(errors.New("no chooser is open"))
			}
			c.sync()
			return fn(c, ctx)
		}
	}
	r.Define(chooserNoun).Pin("insert").
		Verb("pick", verb(cs.pick), "Take the highlighted row, else the top match, else create from the query").
		Verb("move", verb(func(c *Chooser, ctx ochazuke.Ctx) ochazuke.Result {
			c.move(ochazuke.IntArg(ctx.Args, "delta", 0))
			return ochazuke.Done(c.state())
		}), "Move the highlight").Motion().
		Verb("cancel", verb(cs.cancel), "Close without choosing").
		Mode("").
		Bind("enter", ochazuke.Do("chooser.pick")).
		Bind("esc", ochazuke.Do("chooser.cancel")).
		Bind("down", ochazuke.Do("chooser.move", ochazuke.Arguments{"delta": 1})).
		Bind("ctrl+j", ochazuke.Do("chooser.move", ochazuke.Arguments{"delta": 1})).
		Bind("ctrl+n", ochazuke.Do("chooser.move", ochazuke.Arguments{"delta": 1})).
		Bind("up", ochazuke.Do("chooser.move", ochazuke.Arguments{"delta": -1})).
		Bind("ctrl+p", ochazuke.Do("chooser.move", ochazuke.Arguments{"delta": -1})).
		Bind("ctrl+k", ochazuke.Do("chooser.move", ochazuke.Arguments{"delta": -1})).
		Pattern(ochazuke.Printable, ochazuke.Do("input.insert").FromPayload("text", ochazuke.SourceText)).
		Pattern(ochazuke.Paste, ochazuke.Do("input.insert").FromPayload("text", ochazuke.SourceText))
	move := r.Operations["chooser.move"]
	move.Parameters = map[string]ochazuke.Parameter{"delta": {Type: "integer", Required: true}}
	r.Operations["chooser.move"] = move
	return cs
}

// Open opens a chooser modal with its search input focused in insert mode.
func (cs *Choosers) Open(spec ChooserSpec) (*Chooser, error) {
	if spec.Kind == "" {
		return nil, errors.New("chooser kind is required")
	}
	if spec.Filter == nil {
		spec.Filter = func(query string, row ChooserRow) bool { return fuzzyMatch(query, row.Label) }
	}
	c := &Chooser{spec: spec, input: ochazuke.NewInput(spec.Kind+"/query", "", false)}
	c.refilter()
	cs.views[spec.Kind] = ochazuke.ModalView{Title: spec.Title, Render: func(_ *ochazuke.Runtime, _ *ochazuke.Instance, w, h int) ochazuke.Block {
		return c.View(w, h)
	}}
	if _, err := cs.r.OpenModal(spec.Kind, ochazuke.Payload{}, ochazuke.WithInput(c.input, ochazuke.Kind(chooserNoun))); err != nil {
		return nil, err
	}
	cs.open[spec.Kind] = c
	return c, nil
}

// Get is the open chooser of kind, nil when none is open.
func (cs *Choosers) Get(kind string) *Chooser {
	if cs.r.Instances[kind] == nil {
		delete(cs.open, kind)
		return nil
	}
	return cs.open[kind]
}

// at is the chooser whose input inst is, else the top modal's.
func (cs *Choosers) at(inst *ochazuke.Instance) *Chooser {
	if inst != nil {
		if c := cs.Get(inst.Parent); c != nil {
			return c
		}
	}
	return cs.Get(cs.r.TopModal())
}

// close pops the chooser's modal so a callback may open the next one.
func (cs *Choosers) close(c *Chooser) error {
	delete(cs.open, c.spec.Kind)
	if cs.r.TopModal() != c.spec.Kind {
		return fmt.Errorf("chooser %s is not the top modal", c.spec.Kind)
	}
	return cs.r.Pop()
}

func (cs *Choosers) pick(c *Chooser, _ ochazuke.Ctx) ochazuke.Result {
	row, ok := c.Highlighted()
	if !ok || row.ID == chooserCreateRow {
		return cs.create(c, ochazuke.Ctx{})
	}
	if err := cs.close(c); err != nil {
		return ochazuke.Failed(err)
	}
	return c.spec.Choose(row)
}

func (cs *Choosers) create(c *Chooser, _ ochazuke.Ctx) ochazuke.Result {
	if c.spec.Create == nil {
		return ochazuke.Failed(errors.New("nothing matches"))
	}
	query := c.Query()
	if err := cs.close(c); err != nil {
		return ochazuke.Failed(err)
	}
	return c.spec.Create(query)
}

func (cs *Choosers) cancel(c *Chooser, _ ochazuke.Ctx) ochazuke.Result {
	return resultError(cs.close(c))
}

// SetRows replaces the rows (a background load landing); the highlight
// stays on the same row id when the user had moved it.
func (c *Chooser) SetRows(rows []ChooserRow) {
	keep := ""
	if row, ok := c.Highlighted(); ok && c.moved {
		keep = row.ID
	}
	c.spec.Rows = rows
	c.refilter()
	for i, row := range c.matches {
		if keep != "" && row.ID == keep {
			c.cursor = i
		}
	}
}

// Query is the search input's text.
func (c *Chooser) Query() string { return string(c.input.Text) }

// Matches are the rows the query keeps: matches first, then trailing rows,
// then the create row, then the fixed rows.
func (c *Chooser) Matches() []ChooserRow {
	c.sync()
	return append([]ChooserRow(nil), c.matches...)
}

// Moved reports whether the user moved the highlight since the last edit.
func (c *Chooser) Moved() bool { return c.moved }

// Highlighted is the row Enter takes: the moved-to row, else the top
// match; none means Enter creates.
func (c *Chooser) Highlighted() (ChooserRow, bool) {
	c.sync()
	if c.cursor < 0 || c.cursor >= len(c.matches) {
		return ChooserRow{}, false
	}
	return c.matches[c.cursor], true
}

// sync refilters when the query changed; typing resets the highlight to the
// top match.
func (c *Chooser) sync() {
	if c.Query() != c.seen {
		c.refilter()
	}
}

func (c *Chooser) refilter() {
	c.seen = c.Query()
	var head, tail, fixed []ChooserRow
	for _, row := range c.spec.Rows {
		if row.Fixed {
			fixed = append(fixed, row)
			continue
		}
		if !c.spec.Filter(c.seen, row) {
			continue
		}
		if row.Trailing {
			tail = append(tail, row)
		} else {
			head = append(head, row)
		}
	}
	// A row whose label holds the query whole outranks a scattered fuzzy
	// match, so the top match is the one the query spells.
	needle := strings.ToLower(c.seen)
	slices.SortStableFunc(head, func(x, y ChooserRow) int {
		return cmpBool(strings.Contains(strings.ToLower(y.Label), needle), strings.Contains(strings.ToLower(x.Label), needle))
	})
	// A trailing row the query spells from the start of one of its words
	// ("unp", "no pin") is what the user asked for: it leads, unless an
	// ordinary row is spelled as well ("no" for a project "notes").
	spelled := false
	for _, row := range head {
		spelled = spelled || spellsWords(c.seen, row.Label)
	}
	for i, row := range tail {
		if !spelled && spellsWords(c.seen, row.Label) {
			head = append([]ChooserRow{row}, head...)
			tail = append(tail[:i:i], tail[i+1:]...)
			break
		}
	}
	var create []ChooserRow
	if label, ok := c.createRow(); ok {
		create = []ChooserRow{{ID: chooserCreateRow, Label: label}}
	}
	c.matches = slices.Concat(head, tail, create, fixed)
	c.moved = false
	c.cursor = -1
	// A fixed row the query spells ("unp", "unpin", "no pin" for "no pin
	// (unpin)") is what was asked for, ahead of the create row; it keeps
	// its place at the end (UX review 2 F1).
	spelledFixed := -1
	for i, row := range fixed {
		if !spelled && spellsWords(c.seen, row.Label) {
			spelledFixed = len(head) + len(tail) + len(create) + i
			break
		}
	}
	switch {
	case spelledFixed >= 0:
		c.cursor = spelledFixed
	case len(create) > 0 && !c.holds(head):
		// Only scattered matches ("unpin" in "tuios-inbox ~/proj/…"):
		// Enter creates what was typed rather than guess.
		c.cursor = len(head) + len(tail)
	case len(head) > 0 || c.seen != "" && len(tail) > 0:
		c.cursor = 0
	}
}

// holds reports whether the first row is a strong match for the query: it
// holds the query as a substring, ignoring case and, failing that,
// punctuation and spaces ("tuiosinbox" in "tuios-inbox"), spells it from
// word starts, or matches it compactly inside one word (compactMatch:
// "chz" in "ochazuke"). A match strewn across words ("unpin" in
// "tuios-inbox  ~/proj/tuios-inbox") is not.
func (c *Chooser) holds(rows []ChooserRow) bool {
	if len(rows) == 0 {
		return false
	}
	label := rows[0].Label
	if strings.Contains(strings.ToLower(label), strings.ToLower(c.seen)) || spellsWords(c.seen, label) {
		return true
	}
	squash := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return unicode.ToLower(r)
			}
			return -1
		}, s)
	}
	q := squash(c.seen)
	return q != "" && strings.Contains(squash(label), q) || compactMatch(c.seen, label)
}

// compactMatch reports whether query's runes appear in order inside one
// whitespace-separated word of label, ignoring case, spanning at most
// twice the query's length plus one.
func compactMatch(query, label string) bool {
	q := []rune(strings.ToLower(strings.TrimSpace(query)))
	if len(q) == 0 {
		return false
	}
	for _, word := range strings.Fields(strings.ToLower(label)) {
		w := []rune(word)
		for start := range w {
			if w[start] != q[0] {
				continue
			}
			i, j := 1, start+1
			for ; i < len(q) && j < len(w); j++ {
				if w[j] == q[i] {
					i++
				}
			}
			if i == len(q) && j-start <= 2*len(q)+1 {
				return true
			}
		}
	}
	return false
}

// createRow is the create row's label, when the chooser shows one.
func (c *Chooser) createRow() (string, bool) {
	if !c.spec.CreateRow || c.spec.Create == nil || c.spec.CreateLabel == nil || strings.TrimSpace(c.seen) == "" {
		return "", false
	}
	return "+ " + c.spec.CreateLabel(c.seen), true
}

// move steps the highlight, clamped; from none, down lands on the first row
// and up on the last.
func (c *Chooser) move(delta int) {
	if len(c.matches) == 0 || delta == 0 {
		return
	}
	switch {
	case c.cursor < 0 && delta > 0:
		c.cursor = 0
	case c.cursor < 0:
		c.cursor = len(c.matches) - 1
	default:
		c.cursor = min(max(c.cursor+delta, 0), len(c.matches)-1)
	}
	c.moved = true
}

func (c *Chooser) state() map[string]any {
	row, _ := c.Highlighted()
	return map[string]any{"query": c.Query(), "matches": len(c.matches), "highlighted": row.ID, "moved": c.moved}
}

// View draws the search line, then the rows with the highlighted one in
// reverse video, with what Enter would create when nothing is highlighted
// after the matches (before the fixed rows); with a Preview and the
// width for it, the highlighted row's preview on the right.
func (c *Chooser) View(width, height int) ochazuke.Block {
	c.sync()
	if c.spec.Preview == nil || width < chooserPreviewMin {
		return c.rowsView(width, height)
	}
	// The preview column keeps its width whatever it holds, so the panel
	// does not change size as the highlight moves.
	listWidth := width * 3 / 5
	previewWidth := width - listWidth - 3
	left := c.rowsView(listWidth, height)
	text := ""
	if row, ok := c.Highlighted(); ok {
		text = c.spec.Preview(row)
	}
	right := previewLines(text, previewWidth, height)
	lines := strings.Split(left.Text, "\n")
	for len(lines) < len(right) {
		lines = append(lines, "")
	}
	for i, line := range lines {
		side := ""
		if i < len(right) {
			side = right[i]
		}
		lines[i] = line + strings.Repeat(" ", max(0, listWidth-ansi.StringWidth(line))) + " │ " + side + strings.Repeat(" ", max(0, previewWidth-ansi.StringWidth(side)))
	}
	return left.WithText(strings.Join(lines, "\n"))
}

// previewLines is a preview's text wrapped to width, at most height lines,
// the last one marking a cut with ….
func previewLines(text string, width, height int) []string {
	if width < 1 || height < 1 || strings.TrimSpace(text) == "" {
		return nil
	}
	text = strings.ReplaceAll(strings.TrimRight(ansi.Strip(text), "\n"), "\t", "  ")
	wrapped := strings.Split(ansi.Wrap(text, width, " -/"), "\n")
	if len(wrapped) > height {
		wrapped = wrapped[:height]
		wrapped[height-1] = "…"
	}
	return wrapped
}

// rowsView is View's search line and rows, width cells wide.
func (c *Chooser) rowsView(width, height int) ochazuke.Block {
	const prefix = "› "
	line := ochazuke.InputBlock(c.input, max(8, width-ansi.StringWidth(prefix)), 1).Offset(ansi.StringWidth(prefix), 0)
	lines := []string{prefix + line.Text}
	rowsHeight := max(1, height-2)
	switch {
	case len(c.spec.Rows) == 0 && c.spec.Empty != "":
		// Padded to the width the rows will take, so the panel does not
		// jump wider when they land (test pass R4).
		lines = append(lines, ansi.Truncate("  "+c.spec.Empty+strings.Repeat(" ", width), width, ""))
	case c.matched() == 0:
		lines = append(lines, "  no matches")
	}
	hint := ""
	if c.cursor < 0 && c.spec.Create != nil && c.spec.CreateLabel != nil {
		hint = ansi.Truncate("  enter: "+c.spec.CreateLabel(c.Query()), width, "…")
	}
	// The fixed rows ("no pin") always show at the bottom: the rows above
	// them scroll in what is left, keeping the highlight in view.
	fixedFrom := len(c.matches)
	for fixedFrom > 0 && c.matches[fixedFrom-1].Fixed {
		fixedFrom--
	}
	scroll := max(1, rowsHeight-(len(lines)-1)-(len(c.matches)-fixedFrom))
	if hint != "" {
		scroll = max(1, scroll-1)
	}
	offset := 0
	switch {
	case c.cursor >= fixedFrom:
		offset = max(0, fixedFrom-scroll)
	case c.cursor >= scroll:
		offset = c.cursor - scroll + 1
	}
	widths := fitWidths(c.cellWidths(), width-2, c.spec.Keep...)
	draw := func(i int) {
		text := c.matches[i].Label
		if cells := c.matches[i].Cells; len(cells) > 0 {
			text = alignCells(cells, widths)
		}
		row := ansi.Truncate("  "+text, width, "…")
		if i == c.cursor {
			row = lipgloss.NewStyle().Reverse(true).Render(ansi.Truncate(row+strings.Repeat(" ", max(0, width-ansi.StringWidth(row))), width, ""))
		}
		lines = append(lines, row)
	}
	for i := offset; i < fixedFrom && i < offset+scroll; i++ {
		draw(i)
	}
	if hint != "" {
		lines = append(lines, hint)
	}
	for i := fixedFrom; i < len(c.matches); i++ {
		draw(i)
	}
	return line.WithText(strings.Join(lines, "\n"))
}

// matched counts the rows the query kept, leaving out the create and fixed
// rows.
func (c *Chooser) matched() int {
	n := 0
	for _, row := range c.matches {
		if !row.Fixed && row.ID != chooserCreateRow {
			n++
		}
	}
	return n
}

// cellWidths is each column's width over every row with cells, so the
// columns do not shift as the query narrows the rows.
func (c *Chooser) cellWidths() []int {
	var widths []int
	for _, row := range c.spec.Rows {
		for i, cell := range row.Cells {
			if i == len(widths) {
				widths = append(widths, 0)
			}
			widths[i] = max(widths[i], ansi.StringWidth(cell))
		}
	}
	return widths
}

// fitWidths narrows the widest columns, one cell at a time and never
// below 6, until the columns and their two-space gaps fit in avail, so no
// one wide column (a long branch or project list) pushes the rest off. The
// keep columns narrow only once every other column is at its minimum.
func fitWidths(widths []int, avail int, keep ...int) []int {
	widths = slices.Clone(widths)
	total := func() int {
		n := 2 * max(0, len(widths)-1)
		for _, w := range widths {
			n += w
		}
		return n
	}
	widest := func(skipKept bool) int {
		at := -1
		for i, w := range widths {
			if skipKept && slices.Contains(keep, i) {
				continue
			}
			if w > 6 && (at < 0 || w > widths[at]) {
				at = i
			}
		}
		return at
	}
	for total() > avail {
		at := widest(true)
		if at < 0 {
			at = widest(false)
		}
		if at < 0 {
			break
		}
		widths[at]--
	}
	return widths
}

// alignCells pads each cell but the last to its column's width, two
// spaces apart, cutting a cell longer than its column with …; trailing
// empty cells are dropped.
func alignCells(cells []string, widths []int) string {
	for len(cells) > 0 && cells[len(cells)-1] == "" {
		cells = cells[:len(cells)-1]
	}
	var b strings.Builder
	for i, cell := range cells {
		if i < len(widths) {
			cell = ansi.Truncate(cell, widths[i], "…")
		}
		b.WriteString(cell)
		if i < len(cells)-1 && i < len(widths) {
			b.WriteString(strings.Repeat(" ", max(0, widths[i]-ansi.StringWidth(cell))+2))
		}
	}
	return b.String()
}

// spellsWords reports whether query, ignoring case and punctuation, starts
// at the beginning of one of label's words and runs on through the words
// that follow ("unp" and "no pin" spell "no pin (unpin)").
func spellsWords(query, label string) bool {
	norm := func(s string) []string {
		return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
	}
	q := strings.Join(norm(query), " ")
	if q == "" {
		return false
	}
	words := norm(label)
	for i := range words {
		if strings.HasPrefix(strings.Join(words[i:], " "), q) {
			return true
		}
	}
	return false
}

// cmpBool orders false before true.
func cmpBool(x, y bool) int {
	switch {
	case x == y:
		return 0
	case x:
		return 1
	}
	return -1
}

// fuzzyMatch reports whether query's runes appear in text in order,
// ignoring case.
func fuzzyMatch(query, text string) bool {
	text = strings.ToLower(text)
	for _, char := range strings.ToLower(query) {
		pos := strings.IndexRune(text, char)
		if pos < 0 {
			return false
		}
		text = text[pos+len(string(char)):]
	}
	return true
}
