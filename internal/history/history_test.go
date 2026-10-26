package history

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/matomeru/store"
)

func openJournal(t *testing.T, owner Owner) (*Journal, *store.Store) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	j, err := Open(db, owner)
	if err != nil {
		t.Fatal(err)
	}
	return j, db
}

// The tables are matomeru-old's declarations: a database it wrote opens,
// and the journal stays append-only at the database.
func TestTablesMatchMatomeruOld(t *testing.T) {
	old := []store.Table{
		{Name: "history", Owner: "history", Key: "id", Version: 1, AppendOnly: true},
		{Name: "review_marks", Owner: "reviews", Key: "id", Version: 1},
	}
	if Table != old[0] || MarksTable != old[1] {
		t.Fatalf("tables %+v %+v", Table, MarksTable)
	}
	j, db := openJournal(t, nil)
	if _, err := j.Append(Entry{ID: "e1", Kind: "prompt-submitted", Window: "w"}); err != nil {
		t.Fatal(err)
	}
	err := db.Tx([]string{"history"}, func(tx *store.Tx) error { return tx.Put("history", "e1", Entry{ID: "e1"}) })
	if err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("overwrite: %v", err)
	}
}

// A ring entry read live and again from the ring is one row; the
// capture's own summaries are not journaled; nanoseconds become ms.
func TestFromActivity(t *testing.T) {
	j, _ := openJournal(t, func(_, w string) (string, string, error) { return "", "proj-" + w, nil })
	p := Pane{Session: "s", Window: "w", Name: "claude", Harness: "claude-code"}
	e, ok := FromActivity("boot", p, Activity{Seq: 7, At: 1_790_000_000_000_000_000, Kind: "prompt", Text: "fix the build"})
	again, _ := FromActivity("boot", p, Activity{Seq: 7, At: 1_790_000_000_000_000_000, Kind: "prompt", Text: "fix the build"})
	if !ok || e.ID != again.ID || e.Kind != "prompt-submitted" || e.At != 1_790_000_000_000 || e.SourceID != "boot/w/activity:7" {
		t.Fatalf("entry %+v", e)
	}
	if _, ok := FromActivity("boot", p, Activity{Seq: 8, Kind: "turn_end", Target: "mtm-turn:abc"}); ok {
		t.Fatal("a capture's own summary was journaled")
	}
	ids, err := j.Append(e, again)
	if err != nil || len(ids) != 1 {
		t.Fatalf("append %v %v", ids, err)
	}
	if ids, _ := j.Append(e); len(ids) != 0 {
		t.Fatal("a replay wrote again")
	}
	all, _ := j.All()
	if len(all) != 1 || all[0].ProjectID != "proj-w" {
		t.Fatalf("journal %+v", all)
	}
	if seq, _ := j.LastSequence("boot", "w"); seq != 7 {
		t.Fatalf("last seq %d", seq)
	}
}

// A captured turn keeps the owner its prompt was journaled with, even when
// the pane moves before the answer.
func TestCaptureKeepsThePromptTimeOwner(t *testing.T) {
	owner := "matomeru"
	j, _ := openJournal(t, func(string, string) (string, string, error) { return "", owner, nil })
	start := Turn{ID: "t1", Session: "s", Window: "w", Harness: "claude-code", ConversationID: "c", Prompt: "first\nline two", StartedAt: 1000}
	if _, err := j.Capture(start); err != nil {
		t.Fatal(err)
	}
	owner = "ochazuke"
	open, ok, err := j.OpenCapture("s", "w", "claude-code", "c")
	if err != nil || !ok || open.ID != "t1" {
		t.Fatalf("open %+v %v %v", open, ok, err)
	}
	if _, err := j.Complete(open, "the whole\nanswer", 2000); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := j.OpenCapture("s", "w", "claude-code", "c"); ok {
		t.Fatal("a completed turn is still open")
	}
	// Capturing the same completion again is a no-op, different text a conflict.
	if _, err := j.Complete(open, "the whole\nanswer", 2000); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Complete(open, "other", 2000); err == nil {
		t.Fatal("a different answer for the same turn was accepted")
	}
	if _, err := j.Capture(Turn{ID: "t9", Session: "s", Window: "w", Harness: "h", ConversationID: "c", StartedAt: 5, CompletedAt: 6, Response: "x"}); err == nil {
		t.Fatal("a completion without a start was accepted")
	}
	turns, err := j.Turns("w")
	if err != nil || len(turns) != 1 {
		t.Fatalf("turns %+v %v", turns, err)
	}
	if got := turns[0]; got.ProjectID != "matomeru" || got.Prompt != "first\nline two" || got.Response() != "the whole\nanswer" || !got.Done() || !got.Captured {
		t.Fatalf("turn %+v", got)
	}
}

// base is a realistic Unix millisecond time: the daemon's nanoseconds are
// recognised by their size.
const base = int64(1_790_000_000_000)

func activity(t *testing.T, j *Journal, p Pane, seq uint64, ms int64, kind, text string) {
	t.Helper()
	e, ok := FromActivity("boot", p, Activity{Seq: seq, At: (base + ms) * 1_000_000, Kind: kind, Text: text})
	if !ok {
		t.Fatal("dropped")
	}
	if _, err := j.Append(e); err != nil {
		t.Fatal(err)
	}
}

// A <task-notification> prompt continues the open turn; a session start
// ends it; the daemon's summary of a captured turn is left out.
func TestTurnsProjection(t *testing.T) {
	j, _ := openJournal(t, nil)
	p := Pane{Session: "s", Window: "w", Harness: "claude-code"}
	activity(t, j, p, 1, 1000, "prompt", "explain the store")
	activity(t, j, p, 2, 2000, "turn_end", "It keeps JSON rows.")
	activity(t, j, p, 3, 3000, "prompt", "<task-notification> the build finished")
	activity(t, j, p, 4, 4000, "turn_end", "The build is green.")
	// A captured turn, and the daemon's echo of it.
	c := Turn{ID: "t1", Session: "s", Window: "w", Harness: "claude-code", ConversationID: "c", Prompt: "write the tests\nall of them", StartedAt: base + 10_000}
	if _, err := j.Capture(c); err != nil {
		t.Fatal(err)
	}
	activity(t, j, p, 5, 10_050, "prompt", "write the tests")
	activity(t, j, p, 6, 12_000, "turn_end", "Done.")
	if _, err := j.Complete(c, "Done.\nAll tests pass.", base+12_010); err != nil {
		t.Fatal(err)
	}
	activity(t, j, p, 7, 20_000, "session_start", "clear")
	activity(t, j, p, 8, 21_000, "turn_end", "an answer with no prompt")
	turns, err := j.Turns("w")
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("turns %+v", turns)
	}
	if got := turns[0]; got.Prompt != "explain the store" || len(got.Responses) != 2 || got.CompletedAt != base+4000 {
		t.Fatalf("notification turn %+v", got)
	}
	if got := turns[1]; !got.Captured || got.Prompt != "write the tests\nall of them" || got.Response() != "Done.\nAll tests pass." {
		t.Fatalf("captured turn %+v", got)
	}
	if _, err := j.MarkReviewed(turns[1]); err != nil {
		t.Fatal(err)
	}
	first, _ := j.MarkReviewed(turns[1])
	turns, _ = j.Turns("")
	if turns[1].ReviewedAt == 0 || turns[1].ReviewedAt != first.ReviewedAt || turns[0].ReviewedAt != 0 {
		t.Fatalf("marks %+v", turns)
	}
}

// A prompt typed while the agent works (no answer yet) joins the turn.
func TestPromptWhileWorkingJoinsTheTurn(t *testing.T) {
	j, _ := openJournal(t, nil)
	p := Pane{Session: "s", Window: "w"}
	activity(t, j, p, 1, 1000, "prompt", "start")
	activity(t, j, p, 2, 1500, "prompt", "and also this")
	activity(t, j, p, 3, 2000, "turn_end", "both done")
	turns, _ := j.Turns("w")
	if len(turns) != 1 || turns[0].Prompt != "start\n\nand also this" {
		t.Fatalf("turns %+v", turns)
	}
}
