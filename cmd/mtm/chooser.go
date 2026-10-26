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
}

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
		}), "Move the highlight").
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
	switch {
	case len(head) > 0 || c.seen != "" && len(tail) > 0:
		c.cursor = 0
	case len(create) > 0: // nothing else matches: Enter creates
		c.cursor = len(head) + len(tail)
	}
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
// after the matches (before the fixed rows).
func (c *Chooser) View(width, height int) ochazuke.Block {
	c.sync()
	const prefix = "› "
	line := ochazuke.InputBlock(c.input, max(8, width-ansi.StringWidth(prefix)), 1).Offset(ansi.StringWidth(prefix), 0)
	lines := []string{prefix + line.Text}
	rowsHeight := max(1, height-2)
	switch {
	case len(c.spec.Rows) == 0 && c.spec.Empty != "":
		lines = append(lines, "  "+c.spec.Empty)
	case c.matched() == 0:
		lines = append(lines, "  no matches")
	}
	hint := ""
	if c.cursor < 0 && c.spec.Create != nil && c.spec.CreateLabel != nil {
		hint = ansi.Truncate("  enter: "+c.spec.CreateLabel(c.Query()), width, "…")
	}
	offset := 0
	if c.cursor >= rowsHeight {
		offset = c.cursor - rowsHeight + 1
	}
	widths := c.cellWidths()
	for i := offset; i < len(c.matches) && i < offset+rowsHeight; i++ {
		if hint != "" && c.matches[i].Fixed {
			lines, hint = append(lines, hint), ""
		}
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
	if hint != "" {
		lines = append(lines, hint)
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

// alignCells pads each cell but the last to its column's width, two
// spaces apart; trailing empty cells are dropped.
func alignCells(cells []string, widths []int) string {
	for len(cells) > 0 && cells[len(cells)-1] == "" {
		cells = cells[:len(cells)-1]
	}
	var b strings.Builder
	for i, cell := range cells {
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
