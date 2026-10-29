package harness

import (
	"fmt"
	"slices"
	"strings"
)

// How a pane running the harness opens in mtm's pane detail (View.Open).
const (
	// ViewTurns opens on the Turns tab: the agent's turns read from its
	// session log, the prompt box under them.
	ViewTurns = "turns"
	// ViewTerminal opens on the Screen tab: the pane's live screen.
	ViewTerminal = "terminal"
	// ViewBoth splits the detail: the Turns tab and the live screen side
	// by side, Split percent of the width to the turns.
	ViewBoth = "both"
)

// ViewValues are the values View.Open takes (and the agent.view setting).
var ViewValues = []string{ViewTurns, ViewTerminal, ViewBoth}

// Split bounds: each side of a both view keeps at least this share.
const (
	MinSplit = 10
	MaxSplit = 90
)

// View is how mtm's pane detail opens a pane running the harness:
// {"open": "both", "split": 60}. A field left out takes mtm's agent.view
// and agent.view-split settings; a pane's own override (pane.view) beats
// both. Nil (the built-ins): the settings alone.
type View struct {
	Open  string `json:"open,omitempty"`  // ViewTurns, ViewTerminal or ViewBoth
	Split int    `json:"split,omitempty"` // percent of the width the turns take in a both view; 0: the setting
}

// CheckView reports a value that is no view; "" is fine.
func CheckView(v string) error {
	if v == "" || slices.Contains(ViewValues, v) {
		return nil
	}
	return fmt.Errorf("view %q is not a view; use %s", v, strings.Join(ViewValues, ", "))
}

// CheckSplit reports a split outside MinSplit..MaxSplit; 0 (unset) is fine.
func CheckSplit(n int) error {
	if n == 0 || n >= MinSplit && n <= MaxSplit {
		return nil
	}
	return fmt.Errorf("split %d is outside %d..%d (percent of the width the turns take)", n, MinSplit, MaxSplit)
}

func (v View) validate(name string) error {
	if err := CheckView(v.Open); err != nil {
		return fmt.Errorf("harness %s: view.open: %w", name, err)
	}
	if err := CheckSplit(v.Split); err != nil {
		return fmt.Errorf("harness %s: view.%w", name, err)
	}
	return nil
}
