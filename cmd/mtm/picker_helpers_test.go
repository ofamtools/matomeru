package main

import (
	"ofam.dev/matomeru/internal/work"
	"ofam.dev/ochazuke"
)

// taskPickerRows are the task picker's choices as the chooser's rows.
func (a *App) taskPickerRows(owner work.Assignment) []ochazuke.PickerRow {
	return choiceRows(a.taskPickerChoices(owner))
}
