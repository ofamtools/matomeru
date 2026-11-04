package reminders

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"ofam.dev/matomeru/store"
)

func fixture(t *testing.T) *Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mtm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := Open(db)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func add(t *testing.T, s *Store, now time.Time, days string) Reminder {
	t.Helper()
	r, err := s.Write("plants", now, func(r *Reminder, rule *Rule) error {
		r.Title = "water the plants"
		var err error
		rule.Days, err = ParseDays(days)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestReminderLocalHistoryAndRuleEdits(t *testing.T) {
	t.Parallel()
	s := fixture(t)
	now := time.Date(2026, 11, 2, 23, 58, 0, 0, time.UTC) // Monday
	add(t, s, now, "mon,wed")
	if _, err := s.Check("plants", "2026-11-02", "check", now, "owner"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Minute)
	today, err := s.Day("plants", now.Format(time.DateOnly))
	if err != nil || today.Done {
		t.Fatalf("today = %+v, %v", today, err)
	}
	_, err = s.Write("plants", now, func(_ *Reminder, r *Rule) error { r.Time = "18:00"; r.Days = []string{"tue", "wed"}; return nil })
	if err != nil {
		t.Fatal(err)
	}
	now = now.AddDate(0, 0, 2) // Thursday
	hs, err := s.History("plants", now, 0)
	if err != nil {
		t.Fatal(err)
	}
	var states []string
	for _, d := range hs {
		states = append(states, d.Date+" "+d.State+" "+d.ScheduledTime)
	}
	want := []string{"2026-11-05 not scheduled ", "2026-11-04 missed 18:00", "2026-11-03 missed 18:00", "2026-11-02 done 10:30"}
	if !reflect.DeepEqual(states, want) {
		t.Fatalf("history = %q, want %q", states, want)
	}
	d, err := s.Check("plants", "2026-11-03", "check", now, map[string]string{"kind": "agent", "pane": "test"})
	if err != nil || !d.Done || d.DoneAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("backfill %+v, %v", d, err)
	}
	d, err = s.Check("plants", "2026-11-03", "uncheck", now, "owner")
	if err != nil || d.Done || d.DoneAt != "" {
		t.Fatalf("uncheck %+v, %v", d, err)
	}
}

func TestReminderCalendarDaysAcrossDST(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, start := range []time.Time{time.Date(2026, 3, 7, 23, 59, 0, 0, loc), time.Date(2026, 10, 31, 23, 59, 0, 0, loc)} {
		t.Run(start.Format(time.DateOnly), func(t *testing.T) {
			t.Parallel()
			s := fixture(t)
			add(t, s, start, "daily")
			if _, err := s.Check("plants", start.Format(time.DateOnly), "toggle", start, "owner"); err != nil {
				t.Fatal(err)
			}
			now := start.AddDate(0, 0, 2)
			hs, err := s.History("plants", now, 0)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{now.Format(time.DateOnly), start.AddDate(0, 0, 1).Format(time.DateOnly), start.Format(time.DateOnly)}
			var got []string
			for _, d := range hs {
				got = append(got, d.Date)
			}
			if !reflect.DeepEqual(got, want) || hs[0].Done || hs[1].State != "missed" || hs[2].State != "done" {
				t.Fatalf("DST history %+v", hs)
			}
		})
	}
}

func TestReminderDateOnMidnightDSTGap(t *testing.T) {
	t.Parallel()
	loc, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2018, 11, 4, 12, 0, 0, 0, loc)
	s := fixture(t)
	add(t, s, now, "daily")
	d, err := s.Check("plants", "2018-11-04", "check", now, "owner")
	if err != nil || !d.Done || d.Date != "2018-11-04" {
		t.Fatalf("midnight gap %+v %v", d, err)
	}
}
