package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/harness"
	"github.com/unixsuperhero/matomeru/internal/history"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// The review page: one row per agent pane (matomeru-old's rail: "the
// review section has one row per pane"), newest first with the panes
// that have unread turns on top. A row reads the pane, how many of its
// finished turns are unread, its last prompt and when it last finished;
// its record is the window's first review (the launch's), or, for a pane
// mtm did not launch, one written the first time a verb needs it. Plain
// terminals never appear: a row needs a launch review or a turn in the
// history journal. Three stops: the list, the opened pane's Turns tree
// (the same accordion as the Panes page's Turns tab: l opens a turn and
// marks it reviewed, so the row's count drops) and the answer area (an
// input of the answer noun). review.open opens the row: the Turns tree
// takes the focus when the pane has turns; it also captures the pane's
// screen off the loop in the "capture" slot scoped to the list, the
// detail of a pane with no turns. The answer area holds the pane's answer,
// enter saves it, esc restores it. review.accept and review.reject set the
// status; neither touches the task: closing or finishing a pane never
// implies the work is done (znbu outline/panes.md, "Pane closure"), so
// marking the task done stays task.done's job. review.saveAnswer asks for
// the answer in the answer area, or takes it as the answer argument.

const (
	reviewsPage  = "reviews"
	reviewListID = "reviews/list"
	answerID     = "reviews/answer"
	reviewTreeID = "reviews/turns"
)

// defineReviews adds the review noun and the answer area's noun.
func (a *App) defineReviews() {
	r := a.r
	r.Define("review").Columns(reviewColumns...).
		Verb("list", a.listReviews, "List reviews in scope; --all widens the list").Fetch().
		Verb("open", a.openReview, "Capture the review's pane into the detail and load its answer").
		Verb("accept", a.acceptReview, "Mark the review accepted (the task is left as it is)").
		Verb("reject", a.rejectReview, "Mark the review rejected").
		Verb("status", a.statusReview, "Set the review's status (pending, accepted, rejected); omit status to choose one").Positional("status").
		Verb("saveAnswer", a.saveAnswer, "Save the review's answer; omit answer to type it in the answer area (autosaved as a draft)").
		Mode("normal").
		Bind("enter", ochazuke.Do("review.open")).
		Bind("x", ochazuke.Do("review.accept")).
		Bind("s", ochazuke.Do("review.status")).
		Bind("i", ochazuke.Do("review.saveAnswer"))
	a.parameters("review.saveAnswer", map[string]ochazuke.Parameter{"answer": {Type: "string", Description: "Omit to type it in the answer area"}})
	a.parameters("review.status", map[string]ochazuke.Parameter{"status": {Type: "string", Enum: reviewStatuses}})
	// The answer area is not insert-pinned: } lands on it in normal mode,
	// so the next command key is never typed into it. Typing is autosaved
	// as a draft; esc leaves insert and keeps the text.
	r.Define("answer").
		Verb("save", a.saveAnswerArea, "Save the answer area as the opened review's answer").
		Verb("leave", a.leaveAnswerArea, "Return to the review list; the typed answer stays as a draft").
		Verb("cancel", a.cancelAnswerArea, "Drop the draft: restore the saved answer and return to the list").
		Mode("insert").
		Bind("enter", ochazuke.Do("answer.save")).
		Mode("normal").
		Bind("enter", ochazuke.Do("answer.save")).
		Bind("esc", ochazuke.Do("answer.leave")).
		Bind("U", ochazuke.Do("answer.cancel"))
}

// addReviewPage builds the page: the list first, so the page starts on it,
// then the answer area.
func (a *App) addReviewPage() error {
	a.r.Instance(reviewsPage, ochazuke.Kind("page"))
	list, err := a.r.AddList(reviewListID, nil, ochazuke.Parent(reviewsPage), ochazuke.Columns(reviewColumns...))
	if err != nil {
		return err
	}
	a.reviewList = list
	if a.reviewTurns, err = a.r.AddTree(reviewTreeID, nil, ochazuke.Parent(reviewsPage)); err != nil {
		return err
	}
	// A stop only while it shows an opened pane's turns.
	a.r.Instances[reviewTreeID+"/list"].Unfocusable = true
	a.answer = ochazuke.NewInput(answerID, "", false)
	if err := a.r.AddInput(a.answer, ochazuke.Parent(reviewsPage), ochazuke.Kind("answer")); err != nil {
		return err
	}
	if _, err = a.r.AddPage(reviewsPage, "Review", a.renderReviews, ochazuke.Focus(reviewListID)); err != nil {
		return err
	}
	// The detail is the opened review's turns, else its captured output
	// (a screen: the newest at the bottom).
	a.detail.reviews, err = a.addDetail(reviewsDetailID, reviewsPage, func() string { return a.opened }, func() bool {
		return len(a.reviewRows[a.opened].Turns) == 0
	})
	return err
}

// paneReview is one row: a pane, its review record and its turns in
// scope, oldest first.
type paneReview struct {
	Review work.Review
	Stored bool // the record is in the reviews table (else it is written on first use)
	Turns  []history.PaneTurn
}

// unread is how many finished turns are not reviewed.
func (p paneReview) unread() int {
	n := 0
	for _, t := range p.Turns {
		if t.Done() && t.ReviewedAt == 0 {
			n++
		}
	}
	return n
}

// at is when the pane last finished a turn, else when it was recorded.
func (p paneReview) at() int64 {
	at := p.Review.Created
	for _, t := range p.Turns {
		at = max(at, t.CompletedAt)
	}
	return at
}

// lastPrompt is the first line of the pane's newest prompt.
func (p paneReview) lastPrompt() string {
	if len(p.Turns) == 0 {
		return ""
	}
	return firstLine(p.Turns[len(p.Turns)-1].Prompt)
}

// paneReviewID is the record id of a pane mtm did not launch: stable for
// the pane, and a valid draft id (letters, digits, - and _).
func paneReviewID(session, window string) string {
	sum := sha256.Sum256([]byte(session + "\x00" + window))
	return "pane-review-" + hex.EncodeToString(sum[:8])
}

// refreshReviews reloads the rows, one per pane, and pushes them to the
// list; the opened row's Turns tree follows.
func (a *App) refreshReviews() error {
	reviews, err := a.work.Reviews()
	if err != nil {
		return err
	}
	turns, err := a.hist.Turns("")
	if err != nil {
		return err
	}
	type key struct{ session, window string }
	rows := map[key]*paneReview{}
	var order []key
	for _, rv := range reviews {
		k := key{rv.Session, rv.Window}
		if rows[k] != nil {
			continue // the window's first review is its record
		}
		rows[k] = &paneReview{Review: rv, Stored: true}
		order = append(order, k)
	}
	for _, t := range turns {
		if !a.inScope(t.ProjectID) {
			continue
		}
		k := key{t.Session, t.Window}
		if rows[k] == nil {
			rows[k] = &paneReview{Review: work.Review{ID: paneReviewID(t.Session, t.Window), ProjectID: t.ProjectID, TaskID: t.TaskID,
				Harness: t.Harness, Session: t.Session, Window: t.Window, Pane: a.turnPane(t), Status: work.ReviewPending, Created: t.StartedAt}}
			order = append(order, k)
		}
		rows[k].Turns = append(rows[k].Turns, t)
	}
	var shown []*paneReview
	for _, k := range order {
		if r := rows[k]; len(r.Turns) > 0 || a.inScope(r.Review.ProjectID) {
			shown = append(shown, r)
		}
	}
	// Unread first, then newest first, like every other list.
	slices.SortStableFunc(shown, func(x, y *paneReview) int {
		return cmp.Or(cmp.Compare(min(y.unread(), 1), min(x.unread(), 1)), cmp.Compare(y.at(), x.at()), cmp.Compare(x.Review.ID, y.Review.ID))
	})
	a.reviews, a.reviewRows = nil, map[string]paneReview{}
	now := time.Now()
	items := make([]ochazuke.ListItem, 0, len(shown))
	for _, r := range shown {
		a.reviews = append(a.reviews, r.Review)
		a.reviewRows[r.Review.ID] = *r
		items = append(items, ochazuke.ListItem{ID: r.Review.ID, Label: a.reviewLabel(*r, now), Noun: "review", Cells: a.reviewCells(*r, now)})
	}
	if err := a.reviewList.SetItems(items); err != nil {
		return err
	}
	return a.showReviewTurns(false)
}

// turnPane is how a turn's pane reads: the live pane's label, else the
// name journaled with it, else the window id.
func (a *App) turnPane(t history.PaneTurn) string {
	for _, p := range a.panes {
		if p.ID == t.Window {
			return p.label()
		}
	}
	return cmp.Or(t.Pane, shortSession(t.Session)+"/"+t.Window)
}

// showReviewTurns fills the Turns tree with the opened row's turns; fresh
// (a newly opened row) collapses them all.
func (a *App) showReviewTurns(fresh bool) error {
	row, ok := a.reviewRows[a.opened]
	tree := a.r.Instances[reviewTreeID+"/list"]
	if !ok {
		tree.Unfocusable = true
		return a.reviewTurns.SetNodes(nil)
	}
	n, err := a.fillTurns(a.reviewTurns, "review-turn:", row.Review.Window, turnRef{Back: row.Review.ID, Review: true}, fresh)
	tree.Unfocusable = n == 0
	return err
}

// reviewColumns are the review noun's table: the status (with "answered"
// when an answer is saved), the pane, its unread turns, its last prompt,
// when it last finished, the project, the task and the harness; narrow,
// the harness drops first, then the task, the project, the time and the
// prompt.
var reviewColumns = []ochazuke.Column{
	{Title: "status", Min: 8},
	{Title: "pane", Min: 12},
	{Title: "new", Align: ochazuke.AlignRight},
	{Title: "last prompt", Min: 12, Priority: 1},
	{Title: "when", Priority: 2},
	{Title: "project", Priority: 3},
	{Title: "task", Priority: 4},
	{Title: "harness", Priority: 5},
}

// reviewCells are a row's cells in reviewColumns order.
func (a *App) reviewCells(r paneReview, now time.Time) []string {
	rv := r.Review
	status := rv.Status
	if rv.Answer != "" {
		status += " · answered"
	}
	if _, ok := a.answerDraftOf(rv.ID); ok {
		status += " · draft"
	}
	return []string{status, rv.Pane, countCell(r.unread()), r.lastPrompt(), sinceUse(r.at(), now), a.ownerLabel(rv.ProjectID), a.taskTitle(rv.TaskID), rv.Harness}
}

// taskTitle is a task's title, else its id; "" for none.
func (a *App) taskTitle(id string) string {
	if id == "" {
		return ""
	}
	if t, err := a.work.Task(id); err == nil {
		return t.Title
	}
	return id
}

// reviewLabel is the row text: [status] pane · project · N unreviewed ·
// "last prompt" · when, and a mark when an answer is saved.
func (a *App) reviewLabel(r paneReview, now time.Time) string {
	rv := r.Review
	project := "(no project)"
	if rv.ProjectID != "" {
		project = a.ownerLabel(rv.ProjectID)
	}
	parts := []string{"[" + rv.Status + "] " + rv.Pane, project}
	if n := r.unread(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d unreviewed", n))
	}
	if prompt := r.lastPrompt(); prompt != "" {
		parts = append(parts, fmt.Sprintf("%q", ansi.Truncate(prompt, 40, "…")))
		parts = append(parts, sinceUse(r.at(), now))
	}
	label := strings.Join(parts, " · ")
	if rv.Answer != "" {
		label += "  (answered)"
	}
	return label
}

// recordReview is a finished launch's review: the project, the task and
// the new pane. A failure is kept for the page's header.
func (a *App) recordReview(projectID, taskID string, started harness.Result) {
	p := pane{Session: started.Session, Pane: daemon.Pane{ID: started.Window, Name: started.Name}}
	_, err := a.work.AddReview(work.Review{ProjectID: projectID, TaskID: taskID, Harness: started.Harness, Session: p.Session, Window: p.ID, Pane: p.label()})
	if err == nil {
		err = a.refreshReviews()
	}
	a.reviewErr = ""
	if err != nil {
		a.reviewErr = "record review: " + err.Error()
	}
}

// review reads the addressed row's review fresh from the store; a pane
// mtm did not launch gets its record written now (record first), unless
// write is false (opening a row writes nothing).
func (a *App) review(ctx ochazuke.Ctx, write ...bool) (work.Review, error) {
	if ctx.Instance == nil || ctx.Instance.Noun != "review" {
		return work.Review{}, errors.New("no review is selected")
	}
	row, ok := a.reviewRows[ctx.Instance.ID]
	if ok && !row.Stored && len(write) > 0 && !write[0] {
		return row.Review, nil // opening reads; it writes nothing
	}
	if ok && !row.Stored {
		if _, err := a.work.EnsureReview(row.Review); err != nil {
			return work.Review{}, err
		}
		row.Stored = true
		a.reviewRows[ctx.Instance.ID] = row
	}
	rv, err := a.work.Review(ctx.Instance.ID)
	if err != nil {
		return work.Review{}, fmt.Errorf("review %s: %w", ctx.Instance.ID, err)
	}
	return rv, nil
}

// setAnswerText puts text in the answer area; it is what the draft holds
// already, so it is not saved again.
func (a *App) setAnswerText(text string) error {
	if err := a.answer.Replace(0, len(a.answer.Text), text); err != nil {
		return err
	}
	a.answer.Cursor = len(a.answer.Text)
	a.answerDraft = text
	return nil
}

// autosaveAnswer writes the answer area as the opened review's draft
// whenever its text changes (internal/draft, noun review); text equal to
// the saved answer clears the draft.
func (a *App) autosaveAnswer() error {
	if a.opened == "" || a.answer == nil {
		return nil
	}
	text := string(a.answer.Text)
	if text == a.answerDraft {
		return nil
	}
	a.answerDraft = text
	if rv, ok := a.openedReview(); ok && text == rv.Answer {
		return a.drafts.Clear("review", rv.ID)
	}
	return a.drafts.Save("review", a.opened, editRecord{Noun: "review", ID: a.opened, Text: text})
}

// answerDraftOf is a review's unsaved answer, if one was typed.
func (a *App) answerDraftOf(id string) (string, bool) {
	var d editRecord
	if err := a.drafts.Load("review", id, &d); err != nil {
		return "", false
	}
	return d.Text, true
}

// openReview is review.open: the review becomes the detail, its answer
// fills the answer area, and its pane is captured off the loop; a newer
// open replaces an older capture.
func (a *App) openReview(ctx ochazuke.Ctx) ochazuke.Result {
	rv, err := a.review(ctx, false)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.autosaveAnswer(); err != nil {
		return ochazuke.Failed(err)
	}
	a.opened, a.captureErr = rv.ID, ""
	answer := rv.Answer
	if draft, ok := a.answerDraftOf(rv.ID); ok {
		answer = draft
	}
	if err := a.setAnswerText(answer); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.showReviewTurns(true); err != nil {
		return ochazuke.Failed(err)
	}
	if !a.r.Instances[reviewTreeID+"/list"].Unfocusable {
		if err := a.reviewTurns.List().Focus(); err != nil {
			return ochazuke.Failed(err)
		}
	}
	d := a.panesd
	version := a.pinVersion
	target := pane{Session: rv.Session, Pane: daemon.Pane{ID: rv.Window}}
	for _, p := range a.panes {
		if p.ID == rv.Window && p.Session == rv.Session {
			target = p
			break
		}
	}
	if !a.inScope(a.paneOwner(target).ProjectID) {
		a.captureErr = "pane outside pin; showing saved output"
		return ochazuke.Done(rv)
	}
	return ochazuke.Pending(func(c context.Context) (any, error) {
		text, err := d.CapturePane(c, target)
		if gone := (*daemon.Error)(nil); errors.As(err, &gone) && (gone.Code == "window_not_found" || gone.Code == "session_not_found") {
			// A killed pane is not a failure: the saved output stays.
			return paneGone{}, nil
		}
		if err != nil {
			return nil, fmt.Errorf("capture %s: %w", rv.Pane, err)
		}
		return text, nil
	}).ReplacingAt(reviewListID, "capture").Then(func(_ ochazuke.Ctx, v any, err error) {
		if version != a.pinVersion || a.opened != rv.ID || !a.inScope(rv.ProjectID) || !a.inScope(a.paneOwner(target).ProjectID) {
			return
		}
		if err != nil {
			a.captureErr = err.Error()
			return
		}
		if _, ok := v.(paneGone); ok {
			a.captureErr = paneGoneNote
			return
		}
		text, _ := v.(string)
		if _, err := a.work.SaveOutput(rv.ID, text); err != nil {
			a.captureErr = err.Error()
			return
		}
		if err := a.refreshReviews(); err != nil {
			a.captureErr = err.Error()
		}
	})
}

// paneGone is a capture of a pane the daemon no longer has.
type paneGone struct{}

// paneGoneNote is the detail's note for a review whose pane was killed.
const paneGoneNote = "pane gone; showing saved output"

func (a *App) acceptReview(ctx ochazuke.Ctx) ochazuke.Result {
	return a.setReviewStatus(ctx, work.ReviewAccepted)
}

func (a *App) rejectReview(ctx ochazuke.Ctx) ochazuke.Result {
	return a.setReviewStatus(ctx, work.ReviewRejected)
}

// reviewStatuses are the statuses s chooses from, in chooser order.
var reviewStatuses = []string{work.ReviewPending, work.ReviewAccepted, work.ReviewRejected}

// statusReview is review.status (s): the status argument sets it at once;
// without one a chooser of the three statuses asks (reject lives here).
func (a *App) statusReview(ctx ochazuke.Ctx) ochazuke.Result {
	if status, ok := ctx.Args["status"].(string); ok {
		return a.setReviewStatus(ctx, status)
	}
	rv, err := a.review(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	rows := make([]ochazuke.ChooserRow, 0, len(reviewStatuses))
	for _, s := range reviewStatuses {
		label := s
		if s == rv.Status {
			label += " (current)"
		}
		rows = append(rows, ochazuke.ChooserRow{ID: "review-status:" + s, Label: label, Data: s})
	}
	target := ctx.Instance.ID
	_, err = a.r.OpenChooser(ochazuke.ChooserSpec{
		Kind:  statusChooser,
		Title: "Status · " + ctx.Instance.Label,
		Rows:  rows,
		Filter: func(query string, row ochazuke.ChooserRow) bool {
			return strings.HasPrefix(row.Label, strings.ToLower(strings.TrimSpace(query)))
		},
		Choose: func(row ochazuke.ChooserRow) ochazuke.Result {
			out, err := a.r.Execute(ochazuke.Request{Verb: "status", Noun: "review", Target: target, Args: ochazuke.Arguments{"status": row.Data}})
			if err != nil {
				return ochazuke.Failed(err)
			}
			return ochazuke.Done(out)
		},
	})
	return resultError(err)
}

// setReviewStatus writes the status and pushes the rows again.
func (a *App) setReviewStatus(ctx ochazuke.Ctx, status string) ochazuke.Result {
	rv, err := a.review(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if rv, err = a.work.SetReviewStatus(rv.ID, status); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshReviews(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(rv)
}

// saveAnswer is review.saveAnswer: the answer argument saves at once;
// without one (the i key) the review opens and the answer area takes the
// typing in insert mode, its draft or saved answer in it.
func (a *App) saveAnswer(ctx ochazuke.Ctx) ochazuke.Result {
	rv, err := a.review(ctx)
	if err != nil {
		return ochazuke.Failed(err)
	}
	if answer := ochazuke.StringArg(ctx.Args, "answer", ""); answer != "" {
		if err := a.answerCommit(rv)(answer); err != nil {
			return ochazuke.Failed(err)
		}
		return ochazuke.Done(answer)
	}
	result := ochazuke.Done(rv)
	if a.opened != rv.ID {
		result = a.openReview(ctx)
	}
	if err := a.r.SetFocus(answerID); err != nil {
		return ochazuke.Failed(err)
	}
	a.answer.SetMode("insert")
	a.answer.Cursor = len(a.answer.Text) // s appends, like A
	return result
}

// answerCommit saves an answer for a review (an unchanged one writes
// nothing), keeps the answer area in step when it is the opened review,
// and pushes the rows again.
func (a *App) answerCommit(rv work.Review) func(string) error {
	return func(text string) error {
		if text == rv.Answer {
			return a.drafts.Clear("review", rv.ID)
		}
		if _, err := a.work.SaveAnswer(rv.ID, text); err != nil {
			return err
		}
		if err := a.drafts.Clear("review", rv.ID); err != nil {
			return err
		}
		if a.opened == rv.ID {
			if err := a.setAnswerText(text); err != nil {
				return err
			}
		}
		return a.refreshReviews()
	}
}

// openedReview is the review in the detail.
func (a *App) openedReview() (work.Review, bool) {
	for _, rv := range a.reviews {
		if rv.ID == a.opened {
			return rv, true
		}
	}
	return work.Review{}, false
}

// backToReview returns focus to the opened review's row (else the list).
func (a *App) backToReview() error {
	if _, ok := a.openedReview(); ok {
		if err := a.reviewList.Select(a.opened); err != nil {
			return err
		}
	}
	return a.reviewList.Focus()
}

// saveAnswerArea is answer.save: the area's text becomes the opened
// review's answer.
func (a *App) saveAnswerArea(ochazuke.Ctx) ochazuke.Result {
	rv, ok := a.openedReview()
	if !ok {
		return ochazuke.Failed(errors.New("open a review first (enter on a review row)"))
	}
	text := string(a.answer.Text)
	if err := a.answerCommit(rv)(text); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.backToReview(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(text)
}

// leaveAnswerArea is answer.leave (esc in normal mode): back to the list;
// the text stays in the area and in its draft.
func (a *App) leaveAnswerArea(ochazuke.Ctx) ochazuke.Result {
	if err := a.autosaveAnswer(); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.refreshReviews(); err != nil { // the row's draft mark
		return ochazuke.Failed(err)
	}
	return resultError(a.backToReview())
}

// cancelAnswerArea is answer.cancel (U): the draft is dropped, the area
// goes back to the saved answer and focus to the list.
func (a *App) cancelAnswerArea(ochazuke.Ctx) ochazuke.Result {
	rv, _ := a.openedReview()
	if err := a.drafts.Clear("review", rv.ID); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.setAnswerText(rv.Answer); err != nil {
		return ochazuke.Failed(err)
	}
	if err := a.backToReview(); err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Done(nil)
}

func (a *App) renderReviews(_ *ochazuke.Runtime, width, height int) ochazuke.Block {
	s := ochazuke.NewStyles(*a.m.Appearance)
	top := max(1, (height-5)/3)
	head := fmt.Sprintf("reviews (%d)", len(a.reviews))
	list := a.view(a.reviewList, width, top)
	switch {
	case a.reviewErr != "":
		head += ": " + a.reviewErr
	case len(a.reviews) == 0:
		list = a.fill("no reviews; a launch records one when it finishes", top)
	}
	rows := max(1, height-4-top)
	detail, body, answer := "detail: enter captures the review's pane", a.fill("", rows), "answer: s on a review types one"
	rv, opened := a.openedReview()
	if !opened {
		a.detail.reviews.hide()
	}
	if opened {
		detail = "output: " + rv.Pane + " [" + rv.Status + "]"
		owner, err := a.work.PaneOwner(rv.Window, "")
		if err == nil {
			for _, p := range a.panes {
				if p.ID == rv.Window {
					owner = a.paneOwner(p)
					break
				}
			}
			detail += " · current owner: " + a.ownerLabel(owner.ProjectID)
		}
		switch {
		case a.captureErr != "":
			detail += " · " + a.captureErr
		case rv.CapturedAt != 0:
			detail += " · captured " + time.UnixMilli(rv.CapturedAt).Format("15:04:05")
		}
		body = a.detail.reviews.lines(trimBlankTail(rv.Output), 0, width, rows)
		if row := a.reviewRows[rv.ID]; len(row.Turns) > 0 {
			// The pane's turns are the detail; its screen only without them.
			detail = fmt.Sprintf("turns: %s · %s", rv.Pane, plural(row.unread(), "unreviewed turn"))
			if owner, err := a.work.PaneOwner(rv.Window, ""); err == nil {
				detail += " · current owner: " + a.ownerLabel(owner.ProjectID)
			}
			if a.captureErr == paneGoneNote {
				detail += " · pane gone"
			}
			a.wrapTurns(a.reviewTurns, width)
			if list := a.reviewTurns.List(); list.Focused() {
				// In the tree the selection moves and the tree keeps it
				// in view; J and K scroll it only from the review list.
				a.detail.reviews.hide()
				body = a.fill(a.reviewTurns.Block(width, rows).Text, rows)
			} else {
				body = a.detail.reviews.lines(a.reviewTurns.Block(width, max(1, list.Len())).Text, 0, width, rows)
			}
		}
		// The keys that edit, save and drop the answer are on the footer.
		answer = "answer:"
		if _, ok := a.answerDraftOf(rv.ID); ok {
			answer = "answer · draft, not saved yet:"
		}
	}
	lines := []string{s.Muted.Render(ansi.Truncate(head, width, "…")), list, s.Muted.Render(ansi.Truncate(detail, width, "…")), body, s.Muted.Render(ansi.Truncate(answer, width, "…"))}
	before := strings.Count(strings.Join(lines, "\n"), "\n") + 1
	// The area is drawn, focused or not, so it is the stop after the
	// list: a stop the frame does not place is skipped by } and spills.
	a.r.Placed(answerID)
	field := ochazuke.Text(ansi.Truncate(string(a.answer.Text), width, "…"))
	if a.r.Focus == answerID {
		field = ochazuke.InputBlock(a.answer, max(1, width), 1).Offset(0, before)
	}
	text := strings.Join(append(lines, field.Text), "\n")
	return field.WithText(text)
}

func (a *App) listReviews(ctx ochazuke.Ctx) ochazuke.Result {
	rows, err := a.work.Reviews()
	if err != nil {
		return ochazuke.Failed(err)
	}
	scope := *a
	if all, _ := ctx.Args["all"].(bool); all {
		scope.all = true
	}
	out := []work.Review{}
	for _, rv := range rows {
		if scope.inScope(rv.ProjectID) {
			out = append(out, rv)
		}
	}
	return ochazuke.Done(out)
}
