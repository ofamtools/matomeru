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
	quit := ochazuke.Do("modal.pop")
	quit.Noun, quit.Mode, quit.Keys = "modal", "normal", "q"
	return a.r.Bind(quit)
}
