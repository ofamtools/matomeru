package history

import (
	"path/filepath"
	"testing"

	"ofam.dev/matomeru/store"
)

// TestJournalExpire: the journal's readers with old entries gone. Pane w1
// has an old turn (prompt and answer at 10-11) and a turn that spans the
// cutoff (prompt at 20, answer at 40); w2 has a recent turn. Expiring
// below 30 in another process (another store on the file) leaves the
// recent turns: the cached reader reads the journal again, the turn that
// lost its prompt is not shown (its answer has no turn), a review mark of
// an expired turn is ignored, and a ring read again after expiry (an old
// entry appended again) is left out by the horizon.
func TestJournalExpire(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "mtm.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	j, err := Open(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	turn := func(id, window string, at int64, kind string) Entry {
		return Entry{ID: id, Kind: kind, At: at, Source: "daemon", Session: "s", Window: window, Detail: id}
	}
	old := turn("old-prompt", "w1", 10, "prompt-submitted")
	if _, err := j.Append(old, turn("old-answer", "w1", 11, "response-completed"),
		turn("span-prompt", "w1", 20, "prompt-submitted"), turn("span-answer", "w1", 40, "response-completed"),
		turn("new-prompt", "w2", 50, "prompt-submitted"), turn("new-answer", "w2", 51, "response-completed")); err != nil {
		t.Fatal(err)
	}
	if _, err := j.MarkReviewed(PaneTurn{ID: "old-prompt", Session: "s", Window: "w1"}); err != nil {
		t.Fatal(err)
	}
	if turns, err := j.Turns(""); err != nil || len(turns) != 3 {
		t.Fatalf("three turns before: %+v %v", turns, err)
	}
	other, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	elsewhere, err := Open(other, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := elsewhere.Expire(30, 0, 1, true); err != nil || got.Remove != 3 {
		t.Fatalf("the dry run counts three: %+v %v", got, err)
	}
	if got, err := elsewhere.Expire(30, 0, 1, false); err != nil || got.Remove != 3 {
		t.Fatalf("expire removes three: %+v %v", got, err)
	}
	turns, err := j.Turns("")
	if err != nil || len(turns) != 1 || turns[0].ID != "new-prompt" || !turns[0].Done() {
		t.Fatalf("the cached reader shows only the recent turn: %+v %v", turns, err)
	}
	if w1, err := j.Turns("w1"); err != nil || len(w1) != 0 {
		t.Fatalf("a pane whose turns expired has none (the spanning answer has no prompt): %+v %v", w1, err)
	}
	if _, err := j.Get("old-prompt"); err == nil {
		t.Fatal("an expired entry is not found")
	}
	j.SetHorizon(30)
	if ids, err := j.Append(old); err != nil || len(ids) != 0 {
		t.Fatalf("an old entry read again is left out: %v %v", ids, err)
	}
	if all, err := j.All(); err != nil || len(all) != 3 {
		t.Fatalf("w1's spanning answer and w2's turn are left: %+v %v", all, err)
	}
}
