package schedules

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"ofam.dev/matomeru/store"
)

// The one-line timing reads and writes back in one canonical form.
func TestParseWhen(t *testing.T) {
	for in, want := range map[string]string{
		"every 30m":                "every 30m",
		"90m":                      "every 1h30m",
		"daily 9:00":               "daily 09:00",
		"09:00":                    "daily 09:00",
		"Mon,Wed,Friday 17:30,9am": "mon,wed,fri 09:00,17:30",
		"weekdays 8:30":            "weekdays 08:30",
		"sat, sun 10:00":           "weekends 10:00",
		"tuesday":                  "tue 09:00",
		"cron 0 9 * * 1-5":         "cron 0 9 * * 1-5",
		"cron  */15   *  * * *":    "cron */15 * * * *",
		"":                         "never",
		"never":                    "never",
	} {
		w, err := ParseWhen(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if got := w.Text(); got != want {
			t.Errorf("%q reads back %q, want %q", in, got, want)
		}
		again, err := ParseWhen(w.Text())
		if err != nil || again.Text() != want {
			t.Errorf("%q does not round-trip: %q %v", want, again.Text(), err)
		}
	}
	for _, bad := range []string{"every 10s", "fooday 9:00", "25:00", "cron 0 9 * *", "cron 61 * * * *", "mon 9:00 30m"} {
		if _, err := ParseWhen(bad); err == nil {
			t.Errorf("%q was taken", bad)
		}
	}
}

func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04 Mon", s)
	if err != nil {
		panic(err)
	}
	return t
}

// Days and times, and cron, give the next time after a moment.
func TestNextAfter(t *testing.T) {
	cases := []struct {
		when, from, want string
	}{
		// 2026-10-28 is a Wednesday.
		{"mon,fri 09:00", "2026-10-28 12:00 Wed", "2026-10-30 09:00 Fri"},
		{"mon,fri 09:00", "2026-10-30 09:00 Fri", "2026-11-02 09:00 Mon"},
		{"daily 09:00,17:30", "2026-10-28 12:00 Wed", "2026-10-28 17:30 Wed"},
		{"daily 09:00,17:30", "2026-10-28 17:30 Wed", "2026-10-29 09:00 Thu"},
		{"weekdays 08:30", "2026-10-30 09:00 Fri", "2026-11-02 08:30 Mon"},
		{"cron 0 9 * * 1-5", "2026-10-30 09:00 Fri", "2026-11-02 09:00 Mon"},
		{"cron */20 * * * *", "2026-10-28 12:05 Wed", "2026-10-28 12:20 Wed"},
		{"cron 0 0 1 * *", "2026-10-28 12:05 Wed", "2026-11-01 00:00 Sun"},
		{"cron 30 6 * * sun", "2026-10-28 12:05 Wed", "2026-11-01 06:30 Sun"},
	}
	for _, c := range cases {
		w, err := ParseWhen(c.when)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := w.nextAfter(at(c.from), time.UTC)
		if !ok || !got.Equal(at(c.want)) {
			t.Errorf("%s after %s: %s %v, want %s", c.when, c.from, got.Format("2006-01-02 15:04 Mon"), ok, c.want)
		}
	}
}

func calendar(name, when string) Def {
	w, err := ParseWhen(when)
	if err != nil {
		panic(err)
	}
	d := Def{Name: name, Command: []string{"tool"}, Enabled: On}
	d.SetWhen(w)
	return d
}

// A day-and-time schedule counts from its first check: it waits for its
// time, runs once at it, and then waits for the next day it names.
func TestDayAndTimeRunsAtItsTime(t *testing.T) {
	db, _ := openDB(t)
	c, f := &clock{t: at("2026-10-28 08:00 Wed")}, &fake{}
	r := newRunner(t, db, c, f, "a")
	r.Location = time.UTC
	defs := []Def{calendar("standup", "mon,wed 09:00")}
	ctx := context.Background()
	if got := r.Tick(ctx, defs); len(got) != 0 {
		t.Fatalf("ran before its time: %v", got)
	}
	c.add(59 * time.Minute) // 08:59
	if got := r.Tick(ctx, defs); len(got) != 0 {
		t.Fatalf("ran at 08:59: %v", got)
	}
	c.add(time.Minute + 10*time.Second) // 09:00:10
	if got := r.Tick(ctx, defs); !slices.Equal(got, []string{"standup"}) {
		t.Fatalf("did not run at 09:00: %v", got)
	}
	r.Wait()
	c.add(time.Minute)
	if got := r.Tick(ctx, defs); len(got) != 0 {
		t.Fatalf("ran twice: %v", got)
	}
	states, _ := r.States()
	if next := time.UnixMilli(states["standup"].Next).UTC(); !next.Equal(at("2026-11-02 09:00 Mon")) {
		t.Fatalf("next %s, want Monday 09:00", next)
	}
	if f.count() != 1 {
		t.Fatalf("%d runs", f.count())
	}
}

// Runs missed while no mtm ran: catch-up none skips to the next time on
// time; once runs one run for all of them, then keeps time.
func TestCatchUp(t *testing.T) {
	for _, mode := range []string{CatchUpNone, CatchUpOnce} {
		t.Run(mode, func(t *testing.T) {
			db, _ := openDB(t)
			c, f := &clock{t: at("2026-10-27 08:00 Tue")}, &fake{}
			r := newRunner(t, db, c, f, "a")
			r.Location = time.UTC
			d := calendar("daily", "daily 09:00")
			d.CatchUp = mode
			ctx := context.Background()
			r.Tick(ctx, []Def{d}) // anchors at Tue 08:00
			// mtm is closed for three days.
			c.add(3*24*time.Hour + 4*time.Hour) // Fri 12:00
			got := r.Tick(ctx, []Def{d})
			r.Wait()
			states, _ := r.States()
			s := states["daily"]
			switch mode {
			case CatchUpNone:
				if len(got) != 0 || f.count() != 0 || s.Missed != 1 {
					t.Fatalf("none ran %v (%d runs, missed %d)", got, f.count(), s.Missed)
				}
			case CatchUpOnce:
				if !slices.Equal(got, []string{"daily"}) || f.count() != 1 {
					t.Fatalf("once ran %v (%d runs)", got, f.count())
				}
				r.Tick(ctx, []Def{d})
				r.Wait()
				if f.count() != 1 {
					t.Fatalf("once caught up more than once: %d", f.count())
				}
			}
			if next := time.UnixMilli(s.Next).UTC(); !next.Equal(at("2026-10-31 09:00 Sat")) {
				t.Fatalf("next %s, want Saturday 09:00", next)
			}
			// On time again, it runs.
			c.add(21 * time.Hour) // Sat 09:00
			if got := r.Tick(ctx, []Def{d}); len(got) != 1 {
				t.Fatalf("did not run on time after catching up: %v", got)
			}
			r.Wait()
		})
	}
}

// An interval counts from a stored row's creation; start and end bound it.
func TestIntervalWithStartAndEnd(t *testing.T) {
	db, _ := openDB(t)
	c, f := &clock{t: at("2026-10-28 12:00 Wed")}, &fake{}
	r := newRunner(t, db, c, f, "a")
	r.Location = time.UTC
	d := Def{Name: "check", Command: []string{"tool"}, Interval: Duration(time.Hour), Enabled: On,
		Start: "2026-10-29", End: "2026-10-29", CatchUp: CatchUpOnce}
	ctx := context.Background()
	if got := r.Tick(ctx, []Def{d}); len(got) != 0 {
		t.Fatalf("ran before its start: %v", got)
	}
	c.add(12 * time.Hour) // Thu 00:00
	if got := r.Tick(ctx, []Def{d}); len(got) != 1 {
		t.Fatalf("did not run at its start: %v", got)
	}
	r.Wait()
	c.add(30 * time.Minute)
	if got := r.Tick(ctx, []Def{d}); len(got) != 0 {
		t.Fatalf("ran inside its interval: %v", got)
	}
	c.add(30 * time.Minute)
	if got := r.Tick(ctx, []Def{d}); len(got) != 1 {
		t.Fatalf("did not run an hour on: %v", got)
	}
	r.Wait()
	c.add(48 * time.Hour) // past its end
	if got := r.Tick(ctx, []Def{d}); len(got) != 0 {
		t.Fatalf("ran past its end: %v", got)
	}
	// A stored row counts its first interval from its creation.
	created := Def{Name: "fresh", Command: []string{"tool"}, Interval: Duration(time.Hour), Enabled: On, Created: c.now().UnixMilli()}
	if got := r.Tick(ctx, []Def{created}); len(got) != 0 {
		t.Fatalf("a new interval row ran at once: %v", got)
	}
}

// A disabled row never fires, whatever its timing.
func TestDisabledNeverFires(t *testing.T) {
	db, _ := openDB(t)
	c, f := &clock{t: at("2026-10-28 08:00 Wed")}, &fake{}
	r := newRunner(t, db, c, f, "a")
	r.Location = time.UTC
	defs := []Def{calendar("cal", "daily 09:00"), {Name: "every", Command: []string{"tool"}, Interval: Duration(time.Minute)}}
	for i := range defs {
		defs[i].Enabled = Off
	}
	for range 48 {
		r.Tick(context.Background(), defs)
		c.add(30 * time.Minute)
	}
	r.Wait()
	if f.count() != 0 {
		t.Fatalf("a disabled schedule ran %d times", f.count())
	}
}

// A kind is an executor by name: the prompt kind runs through the one the
// caller registered, its record kept with what it launched.
func TestKindsRunTheirExecutor(t *testing.T) {
	db, _ := openDB(t)
	c, f := start(), &fake{}
	r := newRunner(t, db, c, f, "a")
	var got []Def
	r.Kinds[KindPrompt] = func(_ context.Context, d Def, rec *Record) error {
		got = append(got, d)
		rec.Launched = &Launched{Target: TargetNew, Window: "w9", Harness: d.Agent.Harness}
		rec.Window = "w9"
		return nil
	}
	d := Def{Name: "ask", Prompt: "summarise the inbox", Agent: Agent{Harness: "codex", Model: "gpt-5"}, Enabled: On}
	if d.KindOf() != KindPrompt {
		t.Fatalf("kind %q", d.KindOf())
	}
	rec, err := r.RunNow(context.Background(), d)
	if err != nil || rec.Outcome != OutcomeOK || rec.Kind != KindPrompt || rec.Launched == nil || rec.Launched.Window != "w9" {
		t.Fatalf("run %+v %v", rec, err)
	}
	if len(got) != 1 || got[0].Agent.Model != "gpt-5" || f.count() != 0 {
		t.Fatalf("executor got %+v; commands ran %d", got, f.count())
	}
	runs, _ := r.History("ask", 0)
	if len(runs) != 1 || runs[0].Launched == nil || runs[0].Launched.Harness != "codex" {
		t.Fatalf("history %+v", runs)
	}
	// A kind nobody registered fails its run, recorded.
	delete(r.Kinds, KindPrompt)
	rec, _ = r.RunNow(context.Background(), d)
	if rec.Outcome != OutcomeFailed {
		t.Fatalf("unregistered kind: %+v", rec)
	}
}

// Stored rows merge over the configured ones by name; deleting a copy
// brings the original back; only stored rows delete.
func TestStoredRows(t *testing.T) {
	db, _ := openDB(t)
	c, f := start(), &fake{}
	r := newRunner(t, db, c, f, "a")
	edited := Defaults()[0]
	edited.Enabled = Off
	if err := r.Save(edited); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(Def{Name: "ask", Prompt: "hi", Enabled: Off}); err != nil {
		t.Fatal(err)
	}
	if err := r.Save(Def{Name: "bad name", Prompt: "hi", Enabled: Off}); err == nil {
		t.Fatal("a bad name was stored")
	}
	if err := r.Save(Def{Name: "bad", Prompt: "hi", Enabled: Off, Cron: "nope"}); err == nil {
		t.Fatal("a bad cron was stored")
	}
	stored, err := r.Stored()
	if err != nil || len(stored) != 2 {
		t.Fatalf("stored %+v %v", stored, err)
	}
	all := Layer(Defaults(), stored)
	if len(all) != 3 || all[0].Enabled != Off || all[0].Source != SourceStored || all[2].Name != "ask" || all[2].Created == 0 {
		t.Fatalf("layered %+v", all)
	}
	if err := r.Delete("gh-signals"); err != nil {
		t.Fatal(err)
	}
	stored, _ = r.Stored()
	if all = Layer(Defaults(), stored); all[0].Enabled != Auto || all[0].Source != SourceBuiltin {
		t.Fatalf("deleting the copy did not bring the built-in back: %+v", all[0])
	}
	if err := r.Delete("slack-signals"); !errors.Is(err, ErrNotStored) {
		t.Fatalf("deleted a built-in: %v", err)
	}
}

// matomeru-old's prompt schedules come over once, off, with their pane.
func TestImportLegacy(t *testing.T) {
	db, _ := openDB(t)
	if err := db.Declare(Table); err != nil {
		t.Fatal(err)
	}
	legacy := map[string]any{"id": "morning", "title": "Morning sweep", "interval": 3600, "enabled": true, "created": 5,
		"run": map[string]any{"kind": "prompt", "text": "sweep the inbox", "target": map[string]any{"window": "w4"}}}
	lookup := map[string]any{"id": "look", "title": "Lookup", "interval": 600, "run": map[string]any{"kind": "lookup"}}
	if err := db.Tx([]string{Table.Name}, func(tx *store.Tx) error {
		if err := tx.Put(Table.Name, "morning", legacy); err != nil {
			return err
		}
		return tx.Put(Table.Name, "look", lookup)
	}); err != nil {
		t.Fatal(err)
	}
	c, f := start(), &fake{}
	r := newRunner(t, db, c, f, "a")
	stored, err := r.Stored()
	if err != nil || len(stored) != 1 {
		t.Fatalf("imported %+v %v", stored, err)
	}
	d := stored[0]
	if d.Name != "morning" || d.Prompt != "sweep the inbox" || d.Enabled != Off || d.Every() != time.Hour ||
		d.Agent.Target != TargetPane || d.Agent.Pane != "w4" || d.KindOf() != KindPrompt {
		t.Fatalf("imported %+v", d)
	}
	// Once: a second runner over the same file imports nothing again.
	if err := r.Delete("morning"); err != nil {
		t.Fatal(err)
	}
	r2 := newRunner(t, db, c, f, "b")
	if stored, _ := r2.Stored(); len(stored) != 0 {
		t.Fatalf("imported twice: %+v", stored)
	}
}
