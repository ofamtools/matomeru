package main

import (
	"strings"

	"ofam.dev/ochazuke"
)

// mtm's key map (owner, 2026-10-26): one meaning per key on every page.
// The rows that differ from ochazuke's defaults are moved here, as data.
// ochazuke's defaults record macros with Q and close modals with q.

// macroKey is the key that records (and stops) a macro in mtm.
const macroKey = "Q"

// defineKeyMap sets mtm's outline keys and palette quit behavior.
func (a *App) defineKeyMap() error {
	// The outline adds with Vim's o and O only; a is left unbound in mtm.
	// Rows that run one operation with two arguments say which is which.
	outline := []ochazuke.Binding{
		ochazuke.Do("outline.add", ochazuke.Arguments{"where": "below"}).Hinted("add below"),
		ochazuke.Do("outline.add", ochazuke.Arguments{"where": "above"}).Hinted("add above"),
		ochazuke.Do("outline.move", ochazuke.Arguments{"delta": 1}).Hinted("move down"),
		ochazuke.Do("outline.move", ochazuke.Arguments{"delta": -1}).Hinted("move up"),
		{Unbind: true},
	}
	for i, keys := range []string{"o", "O", "J", "K", "a"} {
		outline[i].Noun, outline[i].Mode, outline[i].Keys = "outline", "normal", keys
	}
	if err := a.r.Bind(outline...); err != nil {
		return err
	}
	// ctrl+c quits from every modal; ochazuke's palette alone binds it to
	// palette.close, so after esc cleared a typed line the palette stayed
	// open and quitting took two ctrl+c (test pass R18).
	paletteQuit := ochazuke.Do("app.quit").Hide()
	paletteQuit.Noun, paletteQuit.Mode, paletteQuit.Keys = "palette", "", "ctrl+c"
	return a.r.Bind(paletteQuit)
}

// keyFor is the key that runs op at focus, as ? help lists it ("" when no
// row answers), so a sentence that names a key cannot drift from its row.
func (a *App) keyFor(focus, op string) string { return a.keyForRequest(focus, op, nil) }

// keyForRequest is keyFor for a request with these arguments (nil: any):
// a help row's Request is the operation it runs, also when the row shows
// its Hint, and its Args the arguments it would run with.
func (a *App) keyForRequest(focus, op string, args ochazuke.Arguments) string {
	for _, row := range ochazuke.BindingHelp(a.r, focus) {
		if row.Request == "" || a.r.CanonicalKey(row.Request) != a.r.CanonicalKey(op) {
			continue
		}
		if args != nil && !sameArgs(row.Args, args) {
			continue
		}
		return row.Key
	}
	return ""
}

// sameArgs reports whether got holds every argument of want, equal.
func sameArgs(got, want ochazuke.Arguments) bool {
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}

// keyAction is one "key does this" phrase of a hint sentence.
type keyAction struct{ op, does string }

// keySentence joins "KEY does" for each action that has a key at focus.
func (a *App) keySentence(focus string, actions ...keyAction) string {
	var parts []string
	for _, act := range actions {
		if k := a.keyFor(focus, act.op); k != "" {
			parts = append(parts, k+" "+act.does)
		}
	}
	return strings.Join(parts, " · ")
}
