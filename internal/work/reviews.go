package work

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/unixsuperhero/matomeru/internal/store"
)

// Review is one row of the reviews table: the result of one launched agent,
// tied to its project, the task it was launched on (empty when launched from
// a project row) and the pane it runs in. Output is the pane's screen as
// last captured; Answer is the text the user saved against it.
type Review struct {
	ID         string `json:"id"`
	ProjectID  string `json:"project_id"`
	TaskID     string `json:"task_id,omitempty"`
	Session    string `json:"session"`
	Window     string `json:"window"`
	Pane       string `json:"pane"`
	Status     string `json:"status"`
	Output     string `json:"output,omitempty"`
	CapturedAt int64  `json:"captured_at,omitempty"`
	Answer     string `json:"answer,omitempty"`
	Created    int64  `json:"created"`
	Updated    int64  `json:"updated"`
}

// Review statuses.
const (
	ReviewPending  = "pending"
	ReviewAccepted = "accepted"
	ReviewRejected = "rejected"
)

// Reviews returns every review, oldest first.
func (s *Store) Reviews() ([]Review, error) {
	out, err := all[Review](s.db, "reviews")
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b Review) int {
		return cmp.Or(cmp.Compare(a.Created, b.Created), cmp.Compare(a.ID, b.ID))
	})
	return out, nil
}

// Review reads one review row by id.
func (s *Store) Review(id string) (Review, error) {
	var r Review
	err := s.db.Get("reviews", id, &r)
	return r, err
}

// AddReview records a pending review of a launched pane.
func (s *Store) AddReview(r Review) (Review, error) {
	if r.ProjectID == "" || r.Session == "" || r.Window == "" {
		return Review{}, errors.New("a review needs a project, a session and a window")
	}
	now := Now()
	r.ID, r.Status, r.Created, r.Updated = NewID(), ReviewPending, now, now
	err := s.db.Tx([]string{"reviews"}, func(tx *store.Tx) error { return tx.Put("reviews", r.ID, r) })
	return r, err
}

// SetReviewStatus accepts or rejects a review (or sets it back to pending).
func (s *Store) SetReviewStatus(id, status string) (Review, error) {
	switch status {
	case ReviewPending, ReviewAccepted, ReviewRejected:
	default:
		return Review{}, fmt.Errorf("unknown review status %q", status)
	}
	return s.patchReview(id, func(r *Review) { r.Status = status })
}

// SaveOutput stores a fresh capture of the review's pane.
func (s *Store) SaveOutput(id, output string) (Review, error) {
	return s.patchReview(id, func(r *Review) { r.Output, r.CapturedAt = output, Now() })
}

// SaveAnswer stores the answer text of a review.
func (s *Store) SaveAnswer(id, answer string) (Review, error) {
	return s.patchReview(id, func(r *Review) { r.Answer = answer })
}

func (s *Store) patchReview(id string, change func(*Review)) (Review, error) {
	var r Review
	err := s.db.Tx([]string{"reviews"}, func(tx *store.Tx) error {
		if err := tx.Get("reviews", id, &r); err != nil {
			return err
		}
		change(&r)
		r.Updated = Now()
		return tx.Put("reviews", r.ID, r)
	})
	return r, err
}
