package hints

import (
	"strings"
	"unicode"
)

// Action is what completing a label does. The caller decides it from the
// key: the plain letter copies, Shift and the letter types, Ctrl and the
// letter opens.
type Action int

const (
	// Copy copies the item (the plain letter).
	Copy Action = iota
	// Type copies the item and types it into the pane (Shift).
	Type
	// Open opens the item (Ctrl).
	Open
)

// State is what the keys typed so far amount to.
type State int

const (
	// NoMatch: the key starts no label. It is ignored (the typed keys are
	// unchanged), so a slip does not end hints.
	NoMatch State = iota
	// Prefix: the keys so far start a label; wait for more.
	Prefix
	// Complete: the keys so far are a whole label.
	Complete
)

// Result is the answer to one key.
type Result struct {
	State State
	// Index is the first target with the completed label (labels are in
	// the targets' order, so with Distance ordering that is the occurrence
	// Assign ranked by; every target with the label names the same item).
	// -1 unless Complete.
	Index int
	// Action is the strongest action any key of the label asked for:
	// Open beats Type beats Copy. Only meaningful when Complete.
	Action Action
}

// Matcher resolves typed keys against labels, as tuios' HintsPress does.
//
// The multi-letter rule (tuios'): the action is the strongest one asked on
// any key of the label, not only the last, so Shift or Ctrl held on either
// letter of a two-letter label counts, and Open (Ctrl) beats Type (Shift)
// beats Copy. Backspace takes the last key back; with nothing typed the
// action returns to Copy.
type Matcher struct {
	labels []string
	typed  string
	action Action
}

// NewMatcher matches against labels, one per target (Assign's output;
// targets that share a label name one item).
func NewMatcher(labels []string) *Matcher {
	return &Matcher{labels: labels}
}

// Press takes one key of a label with the action its modifier asks for.
// The letter is lowercased (a Shift letter may arrive uppercase). After
// Complete the matcher starts over.
func (m *Matcher) Press(letter rune, action Action) Result {
	typed := m.typed + string(unicode.ToLower(letter))
	hit, prefix := -1, false
	for i, label := range m.labels {
		switch {
		case label == typed:
			if hit < 0 {
				hit = i
			}
		case strings.HasPrefix(label, typed):
			prefix = true
		}
	}
	if hit < 0 && !prefix {
		return Result{State: NoMatch, Index: -1}
	}
	m.typed = typed
	m.action = max(m.action, action)
	if hit < 0 {
		return Result{State: Prefix, Index: -1, Action: m.action}
	}
	res := Result{State: Complete, Index: hit, Action: m.action}
	m.Reset()
	return res
}

// Backspace takes back the last key typed.
func (m *Matcher) Backspace() {
	if m.typed == "" {
		return
	}
	m.typed = m.typed[:len(m.typed)-1]
	if m.typed == "" {
		m.action = Copy
	}
}

// Reset forgets the keys typed.
func (m *Matcher) Reset() {
	m.typed, m.action = "", Copy
}

// Typed is the start of a label typed so far (for drawing the labels that
// still match).
func (m *Matcher) Typed() string { return m.typed }
