package ptyhost

import (
	"os/exec"
	"strings"
	"sync"
	"unicode/utf8"
)

// Mode is how the Controller reads a session's next key.
type Mode int

const (
	// ModePassthrough encodes keys and writes them to the session.
	ModePassthrough Mode = iota
	// ModePrefix waits for the key after the prefix (ctrl+a).
	ModePrefix
	// ModeCopy moves over a frozen copy of the session's history.
	ModeCopy
)

func (m Mode) String() string {
	switch m {
	case ModePrefix:
		return "prefix"
	case ModeCopy:
		return "copy"
	}
	return "passthrough"
}

// Action is what a key did, for the app to act on.
type Action interface{ isAction() }

// ActionNone: the key was consumed and the app has nothing to do beyond
// redrawing a changed CopyView.
type ActionNone struct{}

// ActionSent: the key was encoded and written to the session.
type ActionSent struct{ Data []byte }

// ActionMode: the session's input mode changed.
type ActionMode struct{ Mode Mode }

// ActionSpawn asks the app to open a side pane running Command ("" is the
// default shell). Where the pane goes is the app's call.
type ActionSpawn struct{ Command string }

// ActionYank carries the text Copy Mode yanked; the app owns the clipboard.
// Copy Mode is left.
type ActionYank struct{ Text string }

// ActionUnhandled: the key has no encoding; the app may use it.
type ActionUnhandled struct{ Key string }

// ActionError: the key could not be acted on.
type ActionError struct{ Err error }

func (ActionNone) isAction()      {}
func (ActionSent) isAction()      {}
func (ActionMode) isAction()      {}
func (ActionSpawn) isAction()     {}
func (ActionYank) isAction()      {}
func (ActionUnhandled) isAction() {}
func (ActionError) isAction()     {}

// Range is an inclusive span of CopyView lines.
type Range struct{ Start, End int }

// CopyView is Copy Mode's state for the app to render.
type CopyView struct {
	// Lines are the frozen history (scrollback then screen) as plain text.
	Lines []string
	// Styled are the same lines for display: ANSI for the screen rows,
	// plain for the scrollback.
	Styled []string
	// Height is the page the view scrolls by (the session's rows).
	Height int
	// Top is the first line shown; Cursor is the current line.
	Top, Cursor int
	// Selection is the selected lines while Selecting.
	Selecting bool
	Selection Range
	// Search is the query; Searching is set while it is being typed.
	Search    string
	Searching bool
	// Matches are the lines holding Search, ascending.
	Matches []int
}

// Controller turns named keys into session input. Keys are Bubble Tea style
// names (see Encode). In passthrough every key goes to the session except the
// prefix: Prefix t asks for a side shell pane, Prefix v for a side editor
// pane, Prefix [ enters Copy Mode and Prefix Prefix sends the prefix itself.
type Controller struct {
	Host Host
	// Prefix is the prefix key ("ctrl+a" when empty).
	Prefix string
	// LookPath finds the editor for Prefix v (exec.LookPath when nil).
	LookPath func(file string) (string, error)

	mu  sync.Mutex
	ins map[string]*input
}

type input struct {
	mode     Mode
	view     CopyView
	anchor   int
	pendingG bool
	prevQ    string
}

// NewController returns a Controller over h.
func NewController(h Host) *Controller { return &Controller{Host: h} }

func (c *Controller) prefix() string {
	if c.Prefix == "" {
		return "ctrl+a"
	}
	return c.Prefix
}

func (c *Controller) in(id string) *input {
	if c.ins == nil {
		c.ins = map[string]*input{}
	}
	in, ok := c.ins[id]
	if !ok {
		in = &input{}
		c.ins[id] = in
	}
	return in
}

// Mode reports the session's input mode.
func (c *Controller) Mode(id string) Mode {
	c.mu.Lock()
	defer c.mu.Unlock()
	if in, ok := c.ins[id]; ok {
		return in.mode
	}
	return ModePassthrough
}

// Copy returns the session's Copy Mode view, or false outside Copy Mode.
func (c *Controller) Copy(id string) (CopyView, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	in, ok := c.ins[id]
	if !ok || in.mode != ModeCopy {
		return CopyView{}, false
	}
	return in.view, true
}

// Forget drops a session's input state, for a closed session.
func (c *Controller) Forget(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.ins, id)
}

// Key handles one key for session id.
func (c *Controller) Key(id, key string) Action {
	c.mu.Lock()
	defer c.mu.Unlock()
	in := c.in(id)
	switch in.mode {
	case ModePrefix:
		in.mode = ModePassthrough
		switch key {
		case c.prefix():
			return c.send(id, key)
		case "t":
			return ActionSpawn{}
		case "v":
			return ActionSpawn{Command: c.editor()}
		case "[":
			return c.enterCopy(id, in)
		}
		return ActionMode{Mode: ModePassthrough}
	case ModeCopy:
		return c.copyKey(in, key)
	}
	if key == c.prefix() {
		in.mode = ModePrefix
		return ActionMode{Mode: ModePrefix}
	}
	return c.send(id, key)
}

func (c *Controller) send(id, key string) Action {
	app := false
	if _, ok := cursorFinal[key]; ok {
		if f, ok := c.Host.Snapshot(id); ok {
			app = f.AppCursorKeys
		}
	}
	b, ok := Encode(key, app)
	if !ok {
		return ActionUnhandled{Key: key}
	}
	if err := c.Host.Write(id, b); err != nil {
		return ActionError{Err: err}
	}
	return ActionSent{Data: b}
}

func (c *Controller) editor() string {
	look := c.LookPath
	if look == nil {
		look = exec.LookPath
	}
	for _, name := range []string{"nvim", "vim"} {
		if _, err := look(name); err == nil {
			return name
		}
	}
	return "vi"
}

func (c *Controller) enterCopy(id string, in *input) Action {
	f, ok := c.Host.Snapshot(id)
	if !ok {
		return ActionError{Err: ErrUnknown}
	}
	styled := append([]string(nil), f.Scrollback...)
	if len(f.Lines) == len(f.Plain) {
		styled = append(styled, f.Lines...)
	} else {
		styled = append(styled, f.Plain...)
	}
	n := len(f.Scrollback)
	in.mode = ModeCopy
	in.pendingG = false
	in.view = CopyView{Lines: f.History(), Styled: styled, Height: max(f.Rows, 1), Top: n, Cursor: n + f.Cursor.Y}
	in.move(0)
	return ActionMode{Mode: ModeCopy}
}

func (in *input) leave() Action {
	in.mode = ModePassthrough
	in.view = CopyView{}
	return ActionMode{Mode: ModePassthrough}
}

// move steps the cursor by d lines, clamped, and scrolls it into view.
func (in *input) move(d int) {
	v := &in.view
	v.Cursor = min(max(v.Cursor+d, 0), max(len(v.Lines)-1, 0))
	if v.Cursor < v.Top {
		v.Top = v.Cursor
	}
	if v.Cursor >= v.Top+v.Height {
		v.Top = v.Cursor - v.Height + 1
	}
	v.Top = min(max(v.Top, 0), max(len(v.Lines)-v.Height, 0))
	if in.view.Selecting {
		v.Selection = Range{Start: min(in.anchor, v.Cursor), End: max(in.anchor, v.Cursor)}
	}
}

func (c *Controller) copyKey(in *input, key string) Action {
	v := &in.view
	if v.Searching {
		return in.searchKey(key)
	}
	if in.pendingG {
		in.pendingG = false
		if key == "g" {
			in.move(-len(v.Lines))
			return ActionNone{}
		}
	}
	switch key {
	case "j", "down":
		in.move(1)
	case "k", "up":
		in.move(-1)
	case "ctrl+d":
		in.move(max(v.Height/2, 1))
	case "ctrl+u":
		in.move(-max(v.Height/2, 1))
	case "g":
		in.pendingG = true
	case "G":
		in.move(len(v.Lines))
	case "/":
		in.prevQ = v.Search
		v.Search, v.Searching = "", true
	case "n":
		in.jump(true)
	case "N":
		in.jump(false)
	case "v":
		v.Selecting = !v.Selecting
		in.anchor = v.Cursor
		v.Selection = Range{Start: v.Cursor, End: v.Cursor}
	case "y":
		if len(v.Lines) == 0 {
			return in.leave()
		}
		r := Range{Start: v.Cursor, End: v.Cursor}
		if v.Selecting {
			r = v.Selection
		}
		text := strings.Join(v.Lines[r.Start:r.End+1], "\n")
		in.leave()
		return ActionYank{Text: text}
	case "esc":
		if v.Selecting {
			v.Selecting = false
			return ActionNone{}
		}
		return in.leave()
	case "q":
		return in.leave()
	}
	return ActionNone{}
}

func (in *input) searchKey(key string) Action {
	v := &in.view
	switch key {
	case "enter":
		v.Searching = false
		v.Matches = matches(v.Lines, v.Search)
		in.jump(true)
	case "esc":
		v.Search, v.Searching = in.prevQ, false
	case "backspace":
		if _, n := utf8.DecodeLastRuneInString(v.Search); n > 0 {
			v.Search = v.Search[:len(v.Search)-n]
		}
	case "space":
		v.Search += " "
	default:
		if utf8.RuneCountInString(key) == 1 {
			v.Search += key
		}
	}
	return ActionNone{}
}

// matches lists the lines holding q, ignoring case unless q has a capital.
func matches(lines []string, q string) []int {
	if q == "" {
		return nil
	}
	fold := strings.ToLower(q) == q
	var out []int
	for i, l := range lines {
		if fold {
			l = strings.ToLower(l)
		}
		if strings.Contains(l, q) {
			out = append(out, i)
		}
	}
	return out
}

// jump moves to the next (or previous) match, wrapping around.
func (in *input) jump(next bool) {
	m := in.view.Matches
	if len(m) == 0 {
		return
	}
	cur := in.view.Cursor
	to := m[0]
	if !next {
		to = m[len(m)-1]
	}
	for i := range m {
		if next && m[i] > cur {
			to = m[i]
			break
		}
		if !next && m[len(m)-1-i] < cur {
			to = m[len(m)-1-i]
			break
		}
	}
	in.move(to - cur)
}
