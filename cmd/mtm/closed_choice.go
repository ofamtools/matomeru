package main

import (
	"fmt"
	"strings"
)

// closedChoice is the value a write stores for a prop or field whose
// choices are a closed set its readers switch on (a review's status, a
// schedule's kind, a context item's kind): value when it is one of
// choices (case and surrounding space ignored), else fallback, and say
// tells what was used instead ("kind \"bogus\" is not one of prompt,
// command: KEPT"). Empty text is fallback with nothing said (not given).
// Since ochazuke v0.142.0 a value outside a parameter's or prop's choices
// reaches the handler, so this is where such a value stops: it is never
// stored, and never an error (no-refusals). A configurable list (the task
// statuses) takes a new value instead (admitStatus).
func closedChoice(say func(string), prop, value string, choices []string, fallback, kept string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return fallback
	}
	for _, c := range choices {
		if strings.EqualFold(v, c) {
			return c
		}
	}
	if say != nil {
		say(fmt.Sprintf("%s %q is not one of %s: %s", prop, v, strings.Join(choices, ", "), kept))
	}
	return fallback
}

// say is an info line on the status line (a "status:" line on the CLI),
// for a write that has no Ctx at hand; nothing without a runtime.
func (a *App) say(text string) {
	if a.r != nil {
		a.r.UI().Info(text)
	}
}
