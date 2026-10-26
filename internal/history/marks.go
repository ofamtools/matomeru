package history

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/unixsuperhero/matomeru/internal/store"
)

// MarksTable holds which turns were read, declared exactly as
// matomeru-old's reviews unit declared it (owner reviews, key id, version
// 1), so the turns reviewed there stay reviewed. The turn text is never
// copied here: it stays in the journal.
var MarksTable = store.Table{Name: "review_marks", Owner: "reviews", Key: "id", Version: 1}

// Mark is one review mark (matomeru-old's reviews.ReviewMark): the turn's
// prompt entry id and when it was first opened.
type Mark struct {
	ID         string     `json:"id"`
	ReviewedAt int64      `json:"reviewed_at"`
	Provenance Provenance `json:"provenance"`
}

// Marks is every mark: turn id → reviewed at.
func (j *Journal) Marks() (map[string]int64, error) {
	rows, err := j.db.All(MarksTable.Name)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		var m Mark
		if err := json.Unmarshal(row, &m); err != nil {
			return nil, err
		}
		out[m.ID] = m.ReviewedAt
	}
	return out, nil
}

// MarkReviewed records that the turn was read; a turn already marked
// keeps its first time.
func (j *Journal) MarkReviewed(t PaneTurn) (Mark, error) {
	if strings.TrimSpace(t.ID) == "" {
		return Mark{}, errors.New("review id is required")
	}
	m := Mark{ID: t.ID, ReviewedAt: Now(), Provenance: Provenance{TurnID: t.ID, Session: t.Session, Window: t.Window, TaskID: t.TaskID, ProjectID: t.ProjectID}}
	m.Provenance.At = m.ReviewedAt
	err := j.db.Tx([]string{MarksTable.Name}, func(tx *store.Tx) error {
		var existing Mark
		err := tx.Get(MarksTable.Name, t.ID, &existing)
		if err == nil {
			m = existing
			return nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		return tx.Put(MarksTable.Name, m.ID, m)
	})
	return m, err
}
