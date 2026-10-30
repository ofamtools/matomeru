package main

import (
	"fmt"

	"github.com/unixsuperhero/ochazuke"
)

// The review noun's record side (standard verbs phase 1, lane M3c): its
// props and its store, so review get, list (--all), set (alias patch) and
// watch are ochazuke's standard verbs over the Review page's rows (one per
// agent pane: its review record, else one made from its journaled turns,
// written the first time a write needs it: record first).
//
// The writable props are the review's own: its status and its answer.
// status is a lasting alias of set of status (review status STATUS); s is
// review.choose-status, the chooser; accept, reject and saveAnswer stay
// named ("do not force generic CRUD onto review history") and run set.
// Everything else (the pane, its owner, the capture, the turns) is the
// pane's, read-only: a review follows its pane (pane.move moves the
// pane), so a review has no move. No create or delete: a launch records a
// review, and a review's history is kept.

// reviewProps are a review's props: the key; the pane (the label) and its
// owner, harness and session, read-only; the status and the answer; and
// the read-only capture, turns and times.
func reviewProps() []ochazuke.PropSpec {
	text := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "string", Description: desc} }
	integer := func(desc string) ochazuke.Parameter { return ochazuke.Parameter{Type: "integer", Description: desc} }
	ro := func(name string, p ochazuke.Parameter) ochazuke.PropSpec {
		return ochazuke.PropSpec{Name: name, ReadOnly: true, Parameter: p}
	}
	pane := ro("pane", text("The pane the review is of, as it reads"))
	pane.Role = ochazuke.RoleLabel
	return []ochazuke.PropSpec{
		{Name: "id", Key: true, Parameter: text("The review's key")},
		pane,
		ro("project", ochazuke.Parameter{Type: "string", Noun: "project", Description: "The project the pane worked for (the pane's; pane.move changes it)"}),
		ro("task", ochazuke.Parameter{Type: "string", Noun: "task", Description: "The task the pane worked on"}),
		ro("harness", text("The harness the pane runs")),
		ro("session", text("The pane's session")),
		ro("window", text("The pane's window id")),
		{Name: "status", Parameter: ochazuke.Parameter{Type: "string", Enum: reviewStatuses, Description: "pending, accepted or rejected (the task is left as it is)"}},
		{Name: "answer", Multiline: true, Parameter: text("The review's answer")},
		ro("prompt", text("The first line of the pane's newest prompt")),
		ro("unread", integer("How many of the pane's finished turns are not reviewed")),
		ro("turns", integer("How many turns the pane has journaled")),
		ro("output", text("The pane's last captured screen")),
		ro("captured", integer("When the screen was captured (Unix ms)")),
		ro("created", integer("When the review was recorded (Unix ms)")),
		ro("updated", integer("When the review last changed (Unix ms)")),
	}
}

// reviewStore is the review noun's store over the rows. history.undo may
// revert its writes (Undo): a status, an answer.
func (a *App) reviewStore() ochazuke.Store {
	return ochazuke.Store{
		Kind: ochazuke.Collection,
		Undo: true,
		ListParams: map[string]ochazuke.Parameter{
			"all": {Type: "boolean", Description: "Every project, whatever the pin"},
		},
		Get: func(_ ochazuke.Ctx, id string) (ochazuke.Thing, error) {
			row, err := a.paneReviewOf(id)
			if err != nil {
				return nil, err
			}
			return reviewThing(row), nil
		},
		List: func(_ ochazuke.Ctx, q ochazuke.ListQuery) ([]ochazuke.Thing, error) {
			previous := a.all
			if all, _ := q.Args["all"].(bool); all {
				a.all = true
			}
			defer func() { a.all = previous }()
			rows, err := a.paneReviews()
			if err != nil {
				return nil, err
			}
			out := []ochazuke.Thing{}
			for _, row := range rows {
				out = append(out, reviewThing(row))
			}
			return out, nil
		},
		Patch: a.patchReview,
	}
}

// reviewThing is a row as its props.
func reviewThing(r paneReview) ochazuke.Thing {
	rv := r.Review
	return ochazuke.Thing{
		"id": rv.ID, "pane": rv.Pane, "project": rv.ProjectID, "task": rv.TaskID, "harness": rv.Harness, "session": rv.Session, "window": rv.Window,
		"status": rv.Status, "answer": rv.Answer, "prompt": r.lastPrompt(), "unread": r.unread(), "turns": len(r.Turns),
		"output": rv.Output, "captured": rv.CapturedAt, "created": rv.Created, "updated": rv.Updated,
	}
}

// paneReviewOf is the row with that id: the page's, else read now (every
// project, the CLI), else a stored record no row shows.
func (a *App) paneReviewOf(id string) (paneReview, error) {
	if row, ok := a.reviewRows[id]; ok {
		return row, nil
	}
	previous := a.all
	a.all = true
	rows, err := a.paneReviews()
	a.all = previous
	if err != nil {
		return paneReview{}, err
	}
	for _, row := range rows {
		if row.Review.ID == id {
			return row, nil
		}
	}
	rv, err := a.work.Review(id)
	if err != nil {
		return paneReview{}, fmt.Errorf("review %s: %w", id, err)
	}
	return paneReview{Review: rv, Stored: true}, nil
}

// patchReview is the store's Patch: the status and the answer, on the
// review record, written first for a pane mtm did not launch (record
// first). A saved answer drops the answer area's draft and, on the opened
// review, shows in the area.
func (a *App) patchReview(_ ochazuke.Ctx, id string, changes ochazuke.Thing) (ochazuke.Thing, error) {
	row, err := a.paneReviewOf(id)
	if err != nil {
		return nil, err
	}
	if !row.Stored {
		if _, err := a.work.EnsureReview(row.Review); err != nil {
			return nil, err
		}
	}
	rv := row.Review
	if v, ok := thingString(changes, "status"); ok {
		if rv, err = a.work.SetReviewStatus(id, v); err != nil {
			return nil, err
		}
	}
	if v, ok := thingString(changes, "answer"); ok {
		if rv, err = a.work.SaveAnswer(id, v); err != nil {
			return nil, err
		}
		if err := a.drafts.Clear("review", id); err != nil {
			return nil, err
		}
		if a.opened == id && a.answer != nil {
			if err := a.setAnswerText(v); err != nil {
				return nil, err
			}
		}
	}
	if err := a.refreshReviews(); err != nil {
		return nil, err
	}
	row.Review, row.Stored = rv, true
	if fresh, ok := a.reviewRows[id]; ok {
		row = fresh
	}
	return reviewThing(row), nil
}
