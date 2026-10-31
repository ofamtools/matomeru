package work

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"ofam.dev/matomeru/store"
)

// Review is one row of the reviews table: the result of one launched agent,
// tied to its project (empty for a launch with no project), the task it was launched on (empty when launched from
// a project row) and the pane it runs in. Output is the pane's screen as
// last captured; Answer is the text the user saved against it.
type Review struct {
	ID              string `json:"id"`
	ProjectID       string `json:"project_id"`
	TaskID          string `json:"task_id,omitempty"`
	FormerProjectID string `json:"former_project_id,omitempty"`
	FormerTaskID    string `json:"former_task_id,omitempty"`
	Harness         string `json:"harness,omitempty"` // the harness the pane runs (empty: rows from before harnesses)
	Session         string `json:"session"`
	Window          string `json:"window"`
	Pane            string `json:"pane"`
	Status          string `json:"status"`
	Output          string `json:"output,omitempty"`
	CapturedAt      int64  `json:"captured_at,omitempty"`
	Answer          string `json:"answer,omitempty"`
	Created         int64  `json:"created"`
	Updated         int64  `json:"updated"`
	// TurnID and ReviewedAt are kept for rows written before turns came
	// from the history journal (internal/history); nothing sets them now.
	TurnID     string `json:"turn_id,omitempty"`
	ReviewedAt int64  `json:"reviewed_at,omitempty"`
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

// AddReview records a pending review of a launched pane. The project is
// optional: a launch with no project records none.
func (s *Store) AddReview(r Review) (Review, error) {
	if r.Session == "" || r.Window == "" {
		return Review{}, errors.New("a review needs a session and a window")
	}
	now := Now()
	r.ID, r.Status, r.Created, r.Updated = NewID(), ReviewPending, now, now
	err := s.db.Tx([]string{"reviews"}, func(tx *store.Tx) error { return tx.Put("reviews", r.ID, r) })
	return r, err
}

// EnsureReview writes a review record under its own id when there is none
// yet (a pane mtm did not launch gets one the first time it is answered or
// accepted) and returns the stored row.
func (s *Store) EnsureReview(r Review) (Review, error) {
	if r.ID == "" || r.Session == "" || r.Window == "" {
		return Review{}, errors.New("a review needs an id, a session and a window")
	}
	err := s.db.Tx([]string{"reviews"}, func(tx *store.Tx) error {
		var existing Review
		err := tx.Get("reviews", r.ID, &existing)
		if err == nil {
			r = existing
			return nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		now := Now()
		if r.Status == "" {
			r.Status = ReviewPending
		}
		if r.Created == 0 {
			r.Created = now
		}
		r.Updated = now
		return tx.Put("reviews", r.ID, r)
	})
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
