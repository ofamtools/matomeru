package main

import (
	"testing"
	"time"

	"ofam.dev/matomeru/internal/history"
)

func TestEventLoadHistoryVisibilityAndDebounce(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	e := history.Entry{ID: "prompt", Kind: "prompt-submitted", At: 10, Window: "w1", Session: "matomeru", Detail: "new request"}
	if _, err := a.hist.Append(e); err != nil {
		t.Fatal(err)
	}
	a.historyChanged()
	if len(a.reviewList.Items()) != 0 {
		t.Fatal("hidden reviews refreshed")
	}
	if err := a.r.OpenPage(reviewsPage); err != nil {
		t.Fatal(err)
	}
	if len(a.reviewList.Items()) != 1 {
		t.Fatal("opening reviews did not refresh")
	}
	// Coalesce history bursts; the visible review remains stale until due.
	e.ID, e.At, e.Window = "second", 20, "w2"
	if _, err := a.hist.Append(e); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		a.journalUpdate(historyChanged{})
	}
	if !a.feed.pending || len(a.reviewList.Items()) != 1 {
		t.Fatal("history burst refreshed eagerly")
	}
	a.journalUpdate(historyDue{})
	if a.feed.pending || len(a.reviewList.Items()) != 2 {
		t.Fatal("history deadline did not refresh")
	}
	if err := a.r.OpenPage(panesPage); err != nil {
		t.Fatal(err)
	}
	setting(t, a, settingHistoryVisible, false)
	e.ID, e.Window = "third", "w3"
	if _, err := a.hist.Append(e); err != nil {
		t.Fatal(err)
	}
	a.historyChanged()
	if len(a.reviewList.Items()) != 3 {
		t.Fatal("visibility opt-out ignored")
	}
}

func TestEventLoadHistoryImmediateOptOut(t *testing.T) {
	t.Parallel()
	a, _ := panesApp(t)
	setting(t, a, settingHistoryCoalesce, time.Duration(0))
	setting(t, a, settingHistoryVisible, false)
	if _, err := a.hist.Append(history.Entry{ID: "immediate", Kind: "prompt-submitted", Window: "w", Session: "s", At: 1, Detail: "hello"}); err != nil {
		t.Fatal(err)
	}
	a.journalUpdate(historyChanged{})
	if a.feed.pending || len(a.reviewList.Items()) != 1 {
		t.Fatal("zero interval did not refresh immediately")
	}
}
