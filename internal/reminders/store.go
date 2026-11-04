// Package reminders keeps local-date checkoffs and dated scheduling rules.
package reminders

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"ofam.dev/matomeru/store"
)

const (
	Records = "reminder_defs"
	Days    = "reminder_days"
	Runs    = "reminder_notify_runs"
)

var Week = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// Rule applies from a local date until the next rule. Edits never change an
// earlier day's schedule, including days without a checkoff record.
type Rule struct {
	From     string   `json:"from"`
	Time     string   `json:"time"`
	Days     []string `json:"days"`
	Enabled  bool     `json:"enabled"`
	Archived bool     `json:"archived"`
}

type Reminder struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Rules []Rule `json:"rules"`
}

func (r Reminder) Current() Rule { return r.Rules[len(r.Rules)-1] }
func (r Reminder) On(date string) Rule {
	var rule Rule
	for _, v := range r.Rules {
		if v.From <= date {
			rule = v
		}
	}
	return rule
}

func (r Rule) Due(day time.Time) bool {
	return r.Enabled && !r.Archived && slices.Contains(r.Days, Week[day.Weekday()])
}

func ParseDays(text string) ([]string, error) {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "every day" || text == "daily" {
		return slices.Clone(Week), nil
	}
	if text == "" || text == "none" {
		return []string{}, nil
	}
	set := map[string]bool{}
	for _, d := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ' ' }) {
		if !slices.Contains(Week, d) {
			return nil, fmt.Errorf("unknown day %q: use sun,mon,tue,wed,thu,fri,sat", d)
		}
		set[d] = true
	}
	out := []string{}
	for _, d := range Week {
		if set[d] {
			out = append(out, d)
		}
	}
	return out, nil
}

func CheckTime(text string) error {
	t, err := time.Parse("15:04", text)
	if err != nil || t.Format("15:04") != text {
		return fmt.Errorf("time %q: use local HH:MM (00:00 through 23:59)", text)
	}
	return nil
}

// Day's actor is an opaque, transport-provided identity, not user input.
type Day struct {
	Reminder   string `json:"reminder"`
	Date       string `json:"date"`
	Done       bool   `json:"done"`
	DoneAt     string `json:"done-at,omitempty"`
	ChangedAt  string `json:"changed-at,omitempty"`
	Actor      any    `json:"actor,omitempty"`
	NotifiedOn string `json:"notified-on,omitempty"`
	NotifiedAt string `json:"notified-at,omitempty"`
	Claim      string `json:"claim,omitempty"`
	ClaimUntil string `json:"claim-until,omitempty"`
}

type HistoryDay struct {
	Day
	State         string `json:"state"`
	ScheduledTime string `json:"time,omitempty"`
}

type Store struct{ DB *store.Store }

func Open(db *store.Store) (*Store, error) {
	for _, name := range []string{Records, Days, Runs} {
		if err := db.Declare(store.Table{Name: name, Owner: "reminders", Key: "id", Version: 1}); err != nil {
			return nil, err
		}
	}
	return &Store{DB: db}, nil
}

func (s *Store) Get(id string) (Reminder, error) {
	var r Reminder
	err := s.DB.Get(Records, id, &r)
	return r, err
}
func (s *Store) All() ([]Reminder, error) {
	rows, err := s.DB.All(Records)
	if err != nil {
		return nil, err
	}
	out := make([]Reminder, 0, len(rows))
	for _, raw := range rows {
		var r Reminder
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

// Write serializes edits and records the current local day's rule. Multiple
// edits today replace today's rule; rules for prior dates remain untouched.
func (s *Store) Write(id string, now time.Time, change func(*Reminder, *Rule) error) (Reminder, error) {
	var r Reminder
	err := s.DB.Tx([]string{Records}, func(tx *store.Tx) error {
		err := tx.Get(Records, id, &r)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		rule := Rule{Time: "10:30", Days: slices.Clone(Week), Enabled: true}
		if err == nil {
			rule = r.Current()
		} else {
			r.ID = id
		}
		if err := change(&r, &rule); err != nil {
			return err
		}
		if err := CheckTime(rule.Time); err != nil {
			return err
		}
		rule.From = now.Format(time.DateOnly)
		// Keep rules sorted if the machine's clock moves backwards.
		at := slices.IndexFunc(r.Rules, func(v Rule) bool { return v.From >= rule.From })
		if at < 0 {
			r.Rules = append(r.Rules, rule)
		} else if r.Rules[at].From == rule.From {
			r.Rules[at] = rule
		} else {
			r.Rules = slices.Insert(r.Rules, at, rule)
		}
		return tx.Put(Records, id, r)
	})
	return r, err
}

func dayKey(id, date string) string { return id + "/" + date }
func readDay(tx *store.Tx, id, date string) (Day, error) {
	d := Day{Reminder: id, Date: date}
	err := tx.Get(Days, dayKey(id, date), &d)
	if errors.Is(err, store.ErrNotFound) {
		err = nil
	}
	return d, err
}

func (s *Store) Day(id, date string) (Day, error) {
	var d Day
	err := s.DB.View([]string{Days}, func(tx *store.Tx) error { var err error; d, err = readDay(tx, id, date); return err })
	return d, err
}

// Summary reads only recorded days; list rendering need not synthesize every
// missed day since creation just to find the last checkoff.
func (s *Store) Summary(id, date string) (Day, string, error) {
	var today Day
	last := ""
	err := s.DB.View([]string{Days}, func(tx *store.Tx) error {
		var err error
		today, err = readDay(tx, id, date)
		if err != nil {
			return err
		}
		rows, err := tx.Entries(Days)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if !strings.HasPrefix(row.ID, id+"/") {
				continue
			}
			var day Day
			if err := json.Unmarshal(row.Row, &day); err != nil {
				return err
			}
			if day.Done && day.Date <= date && day.Date > last {
				last = day.Date
			}
		}
		return nil
	})
	return today, last, err
}

func (s *Store) Check(id, date, verb string, now time.Time, actor any) (Day, error) {
	var d Day
	err := s.DB.Tx([]string{Records, Days}, func(tx *store.Tx) error {
		var r Reminder
		if err := tx.Get(Records, id, &r); err != nil {
			return err
		}
		parsed, err := time.Parse(time.DateOnly, date)
		if err != nil || parsed.Format(time.DateOnly) != date {
			return fmt.Errorf("date %q: use YYYY-MM-DD", date)
		}
		if date > now.Format(time.DateOnly) || date < r.Rules[0].From {
			return errors.New("check date must be between the reminder's first day and today")
		}
		d, err = readDay(tx, id, date)
		if err != nil {
			return err
		}
		switch verb {
		case "check":
			d.Done = true
		case "uncheck":
			d.Done = false
		case "toggle":
			d.Done = !d.Done
		default:
			return fmt.Errorf("unknown check action %q", verb)
		}
		d.DoneAt = ""
		if d.Done {
			d.DoneAt = now.Format(time.RFC3339Nano)
		}
		d.ChangedAt, d.Actor = now.Format(time.RFC3339Nano), actor
		return tx.Put(Days, dayKey(id, date), d)
	})
	return d, err
}

func (s *Store) History(id string, now time.Time, limit int) ([]HistoryDay, error) {
	out := []HistoryDay{}
	err := s.DB.View([]string{Records, Days}, func(tx *store.Tx) error {
		var r Reminder
		if err := tx.Get(Records, id, &r); err != nil {
			return err
		}
		// Noon plus AddDate walks calendar days even across 23/25-hour days.
		day := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
		for date := day.Format(time.DateOnly); date >= r.Rules[0].From; date = day.Format(time.DateOnly) {
			d, err := readDay(tx, id, date)
			if err != nil {
				return err
			}
			rule := r.On(date)
			h := HistoryDay{Day: d, State: "not scheduled"}
			if rule.Due(day) {
				h.State, h.ScheduledTime = "missed", rule.Time
			}
			if d.Done {
				h.State = "done"
			}
			out = append(out, h)
			if limit > 0 && len(out) >= limit {
				break
			}
			day = day.AddDate(0, 0, -1)
		}
		return nil
	})
	return out, err
}

func (s *Store) Delete(id string) error {
	return s.DB.Tx([]string{Records, Days}, func(tx *store.Tx) error {
		entries, err := tx.Entries(Days)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if strings.HasPrefix(e.ID, id+"/") {
				if err := tx.Delete(Days, e.ID); err != nil {
					return err
				}
			}
		}
		return tx.Delete(Records, id)
	})
}
