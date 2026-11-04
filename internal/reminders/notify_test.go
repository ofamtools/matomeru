package reminders

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ofam.dev/matomeru/store"
)

func TestReminderNotifyDue(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, days, at, done, notified string
		want                           int
	}{
		{name: "not scheduled today", days: "tue", at: "11:00"},
		{name: "before time", days: "daily", at: "10:29"},
		{name: "after time", days: "daily", at: "10:30", want: 1},
		{name: "wake after time", days: "daily", at: "23:59", want: 1},
		{name: "already done", days: "daily", at: "11:00", done: "check"},
		{name: "already notified", days: "daily", at: "11:00", notified: "yes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := fixture(t)
			now, _ := time.Parse("2006-01-02 15:04", "2026-11-02 "+tc.at)
			add(t, s, now, tc.days)
			if tc.done != "" {
				if _, err := s.Check("plants", now.Format(time.DateOnly), tc.done, now, "owner"); err != nil {
					t.Fatal(err)
				}
			}
			if tc.notified != "" {
				if _, err := s.NotifyDue(now, func(Reminder) (bool, error) { return true, nil }); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			n, err := s.NotifyDue(now, func(Reminder) (bool, error) { calls++; return true, nil })
			if err != nil || n != tc.want || calls != tc.want {
				t.Fatalf("sent %d calls %d err %v, want %d", n, calls, err, tc.want)
			}
			if n, err := s.NotifyDue(now, func(Reminder) (bool, error) { t.Error("duplicate notification"); return true, nil }); err != nil || n != 0 {
				t.Fatalf("repeat %d %v", n, err)
			}
		})
	}
}
func TestReminderNotifyFailureRetryAndPartialSuccess(t *testing.T) {
	t.Parallel()
	s := fixture(t)
	now := time.Date(2026, 11, 2, 11, 0, 0, 0, time.UTC)
	add(t, s, now, "daily")
	failure := errors.New("fake sender failed")
	if n, err := s.NotifyDue(now, func(Reminder) (bool, error) { return false, failure }); n != 0 || !errors.Is(err, failure) {
		t.Fatalf("failed send %d %v", n, err)
	}
	d, err := s.Day("plants", "2026-11-02")
	if err != nil || d.Claim != "" || d.NotifiedOn != "" {
		t.Fatalf("failed claim %+v %v", d, err)
	}
	if n, err := s.NotifyDue(now, func(Reminder) (bool, error) { return true, failure }); n != 1 || !errors.Is(err, failure) {
		t.Fatalf("partial send %d %v", n, err)
	}
	if n, err := s.NotifyDue(now, func(Reminder) (bool, error) { t.Error("partial success retried"); return true, nil }); err != nil || n != 0 {
		t.Fatalf("repeat %d %v", n, err)
	}
	status, err := s.Status()
	if err != nil || status.LastSent != now.Format(time.RFC3339Nano) || status.LastError != "reminder \"water the plants\": fake sender failed" {
		t.Fatalf("status %+v %v", status, err)
	}
	now = now.AddDate(0, 0, 1)
	if n, err := s.NotifyDue(now, func(Reminder) (bool, error) { return true, nil }); err != nil || n != 1 {
		t.Fatalf("next day %d %v", n, err)
	}
}

func TestReminderNotifyAtomicAcrossConnections(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "mtm.db")
	db1, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db1.Close()
	s1, err := Open(db1)
	if err != nil {
		t.Fatal(err)
	}
	db2, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	s2, err := Open(db2)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 2, 11, 0, 0, 0, time.UTC)
	add(t, s1, now, "daily")
	start := make(chan struct{})
	var wg sync.WaitGroup
	var calls atomic.Int32
	for _, s := range []*Store{s1, s2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := s.NotifyDue(now, func(Reminder) (bool, error) { calls.Add(1); return true, nil }); err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("two connections sent %d times", got)
	}
}
func TestReminderAbandonedClaimAndConcurrentCheck(t *testing.T) {
	t.Parallel()
	s := fixture(t)
	now := time.Date(2026, 11, 2, 11, 0, 0, 0, time.UTC)
	add(t, s, now, "daily")
	claim, err := s.Claim("plants", now)
	if err != nil || claim == nil {
		t.Fatalf("claim %v %v", claim, err)
	}
	if c, err := s.Claim("plants", now.Add(time.Minute)); err != nil || c != nil {
		t.Fatalf("active claim %v %v", c, err)
	}
	next, err := s.Claim("plants", now.Add(ClaimTTL))
	if err != nil || next == nil {
		t.Fatalf("abandoned claim %v %v", next, err)
	}
	if _, err := s.Check("plants", "2026-11-02", "check", now, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(*next, now, true); err != nil {
		t.Fatal(err)
	}
	d, err := s.Day("plants", "2026-11-02")
	if err != nil || !d.Done || d.NotifiedOn != "2026-11-02" {
		t.Fatalf("concurrent check %+v %v", d, err)
	}
	if err := s.Finish(*claim, now, true); err == nil {
		t.Fatal("old claim accepted")
	}
}

func TestReminderNotifyDSTAndEarlyCheckoff(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, start, repeat, at string }{
		{"spring gap catches up", "2026-03-08T03:00:00-04:00", "2026-03-08T04:00:00-04:00", "02:30"},
		{"fall repeat sends once", "2026-11-01T01:30:00-04:00", "2026-11-01T01:30:00-05:00", "01:30"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := fixture(t)
			start, _ := time.Parse(time.RFC3339, tc.start)
			start = start.In(loc)
			add(t, s, start, "daily")
			if _, err := s.Write("plants", start, func(_ *Reminder, rule *Rule) error { rule.Time = tc.at; return nil }); err != nil {
				t.Fatal(err)
			}
			calls := 0
			send := func(Reminder) (bool, error) { calls++; return true, nil }
			if n, err := s.NotifyDue(start, send); err != nil || n != 1 {
				t.Fatalf("DST first %d %v", n, err)
			}
			repeat, _ := time.Parse(time.RFC3339, tc.repeat)
			if n, err := s.NotifyDue(repeat.In(loc), send); err != nil || n != 0 || calls != 1 {
				t.Fatalf("DST repeat %d %v, calls %d", n, err, calls)
			}
		})
	}
	s := fixture(t)
	early := time.Date(2026, 11, 2, 9, 0, 0, 0, loc)
	add(t, s, early, "daily")
	if _, err := s.Check("plants", early.Format(time.DateOnly), "check", early, "owner"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.NotifyDue(early.Add(3*time.Hour), func(Reminder) (bool, error) { t.Error("checked before time but notified"); return true, nil }); err != nil || n != 0 {
		t.Fatalf("early check %d %v", n, err)
	}
}
