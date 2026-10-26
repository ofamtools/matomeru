package main

import (
	"fmt"
	"strings"

	"github.com/unixsuperhero/ochazuke"
)

// mtm's key map (owner, 2026-10-26): one meaning per key on every page.
// The rows that differ from ochazuke's defaults are moved here, as data:
// q closes an editor or a modal and never starts a macro, so the app's
// macro rows move from q to Q (ochazuke's default stays q for other apps).

// macroKey is the key that records (and stops) a macro in mtm.
const macroKey = "Q"

// defineKeyMap moves the framework's macro rows from q to Q and lets q
// close a modal in normal mode, as esc does.
func (a *App) defineKeyMap() error {
	app := a.r.Nouns["app"]
	if app == nil {
		return fmt.Errorf("no app noun")
	}
	for _, mode := range []string{"normal", "visual", "macro_recording"} {
		for _, row := range app.Bindings[mode] {
			if row.Keys != "q" {
				continue
			}
			moved := row
			moved.Noun, moved.Mode, moved.Keys = "app", mode, macroKey
			if moved.Help != "" {
				moved.Help = strings.NewReplacer("(qa)", "(Qa)", "q again", "Q again").Replace(moved.Help)
			}
			if err := a.r.Bind(moved, ochazuke.Binding{Noun: "app", Mode: mode, Keys: "q", Unbind: true}); err != nil {
				return err
			}
			break
		}
	}
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
	quit := ochazuke.Do("modal.pop")
	quit.Noun, quit.Mode, quit.Keys = "modal", "normal", "q"
	// ctrl+c quits from every modal; ochazuke's palette alone binds it to
	// palette.close, so after esc cleared a typed line the palette stayed
	// open and quitting took two ctrl+c (test pass R18).
	paletteQuit := ochazuke.Do("app.quit").Hide()
	paletteQuit.Noun, paletteQuit.Mode, paletteQuit.Keys = "palette", "", "ctrl+c"
	return a.r.Bind(quit, paletteQuit)
}

// keyFor is the key that runs op at focus, as ? help lists it ("" when no
// row answers), so a sentence that names a key cannot drift from its row.
func (a *App) keyFor(focus, op string) string {
	for _, row := range ochazuke.BindingHelp(a.r, focus) {
		if row.Operation == op {
			return row.Key
		}
	}
	return ""
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
