package main

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"unicode"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/contextitems"
	"github.com/unixsuperhero/matomeru/internal/history"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// Context from turns. When the journal takes a prompt or an answer (the
// daemon's activity, which the journal reader follows in the background,
// or a capture hook through the CLI), the same extractor runs over the
// turn's text and merges what it finds with the same de-duplication as a
// pane's output. Items belong to the turn's owner when its prompt went
// in (history.PaneTurn keeps it), not to the pane's owner now. The turn's
// prompt itself is not an item: the picker lists prompts from the journal
// (context_picker.go).
//
// Which text: a prompt that starts a turn gives the turn's prompt; a
// prompt that continues one (typed while the agent worked, a
// task-notification) gives its own text and the turn's owner; the
// daemon's summary of a captured prompt gives nothing (the capture holds
// the whole prompt); an answer gives its own text when the projection
// keeps it in a turn, nothing when a captured answer replaces it. The
// daemon cuts its texts (about 100 characters, then "..."); the cut word
// is left out.

// turnContextSource is the source kind of an item found in a turn.
const turnContextSource = "turn"

// turnContext extracts context items from the turns of journaled entries
// and returns how many were new. It reads the journal and writes the
// context table only, so it runs anywhere off the loop.
func turnContext(j *history.Journal, store *contextitems.Store, entries []history.Entry) (int, error) {
	byWindow := map[string][]history.Entry{}
	for _, e := range entries {
		if e.Window != "" && (e.Kind == "prompt-submitted" || e.Kind == "response-completed") {
			byWindow[e.Window] = append(byWindow[e.Window], e)
		}
	}
	windows := make([]string, 0, len(byWindow))
	for w := range byWindow {
		windows = append(windows, w)
	}
	slices.Sort(windows)
	added := 0
	var errs []error
	now := work.Now()
	for _, w := range windows {
		turns, err := j.Turns(w)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, e := range byWindow[w] {
			t, text, ok := turnText(turns, e)
			if !ok {
				continue
			}
			if e.Turn == nil {
				text = untruncated(text)
			}
			src := contextitems.Source{Kind: turnContextSource, EntryKind: e.Kind, Session: t.Session, Window: t.Window, Pane: t.Pane, At: e.At, Cwd: e.Cwd}
			items := contextitems.Extract(text, 0, src)
			for i := range items {
				items[i].Provenance.TurnID = t.ID
			}
			_, n, err := store.Merge(items, contextitems.Owner{ProjectID: t.ProjectID, TaskID: t.TaskID}, now)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			added += n
		}
	}
	return added, errors.Join(errs...)
}

// turnText is the turn an entry belongs to and the text of it to read.
func turnText(turns []history.PaneTurn, e history.Entry) (history.PaneTurn, string, bool) {
	if e.Kind == "response-completed" {
		for _, t := range turns {
			for _, r := range t.Responses {
				if r.ID == e.ID {
					return t, r.Text, true
				}
			}
		}
		return history.PaneTurn{}, "", false
	}
	if i := slices.IndexFunc(turns, func(t history.PaneTurn) bool { return t.ID == e.ID }); i >= 0 {
		return turns[i], turns[i].Prompt, true
	}
	var open *history.PaneTurn
	for i := range turns {
		t := &turns[i]
		// The daemon's summary of a captured prompt.
		if e.Turn == nil && t.Captured && abs64(t.StartedAt-e.At) <= 5_000 {
			return history.PaneTurn{}, "", false
		}
		if t.StartedAt <= e.At {
			open = t
		}
	}
	text := cmp.Or(e.Detail, e.Summary)
	if open == nil || text == "" {
		return history.PaneTurn{}, "", false
	}
	return *open, text, true
}

// untruncated is a daemon text without the word its cut (a trailing
// "..." or "…", about 100 characters in) left half: a half path or URL
// names nothing.
func untruncated(text string) string {
	trimmed := strings.TrimRightFunc(text, unicode.IsSpace)
	if !strings.HasSuffix(trimmed, "...") && !strings.HasSuffix(trimmed, "…") {
		return text
	}
	if cut := strings.LastIndexFunc(trimmed, unicode.IsSpace); cut >= 0 {
		return trimmed[:cut]
	}
	return ""
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// turnsExtracted is what context-item.turns wrote.
type turnsExtracted struct {
	Entries int `json:"entries"`
	New     int `json:"new"`
}

// extractTurns is context-item.turns: every journaled turn at once, off
// the loop, for the turns journaled before extraction from turns existed.
// Running it again adds nothing.
func (a *App) extractTurns(ctx ochazuke.Ctx) ochazuke.Result {
	j, store := a.hist, a.contexts
	return ochazuke.Pending(func(context.Context) (any, error) {
		entries, err := j.All()
		if err != nil {
			return nil, err
		}
		n, err := turnContext(j, store, entries)
		return turnsExtracted{Entries: len(entries), New: n}, err
	}).Then(func(ctx ochazuke.Ctx, _ any, err error) {
		if err != nil {
			return
		}
		if err := a.reloadContext(); err != nil {
			ctx.Error(err.Error())
		}
		a.contextChanged()
	})
}

// capturedContext is a capture verb's tail: the entry it journaled goes
// through turnContext off the loop, and the capture's own result is the
// command's. A failed extraction never fails the capture.
func (a *App) capturedContext(id string, result any) ochazuke.Result {
	j, store := a.hist, a.contexts
	return ochazuke.Pending(func(context.Context) (any, error) {
		if e, err := j.Get(id); err == nil {
			_, _ = turnContext(j, store, []history.Entry{e})
		}
		return result, nil
	}).Then(func(ochazuke.Ctx, any, error) {
		if err := a.reloadContext(); err != nil {
			a.r.UI().Error(err.Error())
		}
		a.contextChanged()
	})
}

// transcriptContext returns the extraction for a transcript read of the
// pane at inst, safe off the loop: the owner is read now, on the loop.
// New items reach the views with the journal's next burst.
func (a *App) transcriptContext(inst *ochazuke.Instance) func(p pane, text string) {
	var owner contextitems.Owner
	if p, err := a.paneAt(inst); err == nil {
		owner = a.contextOwner(p)
	}
	store, feed := a.contexts, a.feed
	return func(p pane, text string) {
		items := contextitems.Extract(text, 0, contextSource(p, "transcript"))
		if _, n, err := store.Merge(items, owner, work.Now()); err == nil && n > 0 && feed != nil {
			feed.contextAdded.Store(true)
			feed.signal()
		}
	}
}
