package reminders

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"ofam.dev/matomeru/store"
)

// A claim survives a crashed process for at most two minutes. Delivery has a
// shorter, caller-enforced timeout. No transaction stays open during external
// I/O, so checkoffs and the UI never wait for a sender.
const ClaimTTL = 2 * time.Minute

type Claim struct {
	Reminder Reminder
	Date     string
	Token    string
}

func (s *Store) Claim(id string, now time.Time) (*Claim, error) {
	var claim *Claim
	err := s.DB.Tx([]string{Records, Days}, func(tx *store.Tx) error {
		var r Reminder
		if err := tx.Get(Records, id, &r); err != nil {
			return err
		}
		date := now.Format(time.DateOnly)
		rule := r.On(date)
		if !rule.Due(now) || now.Format("15:04") < rule.Time {
			return nil
		}
		d, err := readDay(tx, id, date)
		if err != nil {
			return err
		}
		if d.Done || d.NotifiedOn == date {
			return nil
		}
		if until, err := time.Parse(time.RFC3339Nano, d.ClaimUntil); err == nil && until.After(now) {
			return nil
		}
		var token [16]byte
		if _, err := rand.Read(token[:]); err != nil {
			return err
		}
		d.Claim, d.ClaimUntil = hex.EncodeToString(token[:]), now.Add(ClaimTTL).Format(time.RFC3339Nano)
		if err := tx.Put(Days, dayKey(id, date), d); err != nil {
			return err
		}
		claim = &Claim{Reminder: r, Date: date, Token: d.Claim}
		return nil
	})
	return claim, err
}

// Finish re-reads the day so a concurrent checkoff is never overwritten. A
// failed send releases the claim immediately; one successful sender is enough
// to mark the date notified, even if another sender failed.
func (s *Store) Finish(c Claim, now time.Time, sent bool) error {
	return s.DB.Tx([]string{Days}, func(tx *store.Tx) error {
		d, err := readDay(tx, c.Reminder.ID, c.Date)
		if err != nil {
			return err
		}
		if d.Claim != c.Token {
			return errors.New("notification claim expired or was replaced")
		}
		d.Claim, d.ClaimUntil = "", ""
		if sent {
			d.NotifiedOn, d.NotifiedAt = c.Date, now.Format(time.RFC3339Nano)
		}
		return tx.Put(Days, dayKey(c.Reminder.ID, c.Date), d)
	})
}

type NotifyStatus struct {
	LastRun     string `json:"last-run,omitempty"`
	LastSent    string `json:"last-sent,omitempty"`
	LastTitle   string `json:"last-title,omitempty"`
	LastFailure string `json:"last-failure,omitempty"`
	LastError   string `json:"last-error,omitempty"`
}

func (s *Store) Status() (NotifyStatus, error) {
	var v NotifyStatus
	err := s.DB.Get(Runs, "last", &v)
	if errors.Is(err, store.ErrNotFound) {
		err = nil
	}
	return v, err
}

func (s *Store) RecordRun(now time.Time, title string, sent bool, failure error) error {
	return s.DB.Tx([]string{Runs}, func(tx *store.Tx) error {
		var v NotifyStatus
		if err := tx.Get(Runs, "last", &v); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		v.LastRun = now.Format(time.RFC3339Nano)
		if sent {
			v.LastSent, v.LastTitle = v.LastRun, title
		}
		if failure != nil {
			v.LastFailure, v.LastError = v.LastRun, failure.Error()
		}
		return tx.Put(Runs, "last", v)
	})
}

type Send func(Reminder) (bool, error)

// NotifyDue is shared by the running TUI and the manual headless verb. Send
// reports whether at least one sender succeeded, separately from failures.
func (s *Store) NotifyDue(now time.Time, send Send) (int, error) {
	rows, err := s.All()
	if err != nil {
		return 0, err
	}
	count := 0
	var failures []error
	for _, r := range rows {
		c, err := s.Claim(r.ID, now)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if c == nil {
			continue
		}
		sent, err := send(c.Reminder)
		if !sent && err == nil {
			err = errors.New("no notification sender succeeded")
		}
		finishErr := s.Finish(*c, now, sent)
		if sent {
			count++
		}
		failure := errors.Join(err, finishErr)
		if failure != nil {
			failure = fmt.Errorf("reminder %q: %w", r.Title, failure)
			failures = append(failures, failure)
		}
		if err := s.RecordRun(now, r.Title, sent, failure); err != nil {
			failures = append(failures, err)
		}
	}
	if err := s.RecordRun(now, "", false, errors.Join(failures...)); err != nil {
		failures = append(failures, err)
	}
	return count, errors.Join(failures...)
}
