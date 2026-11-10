//go:build monogatari

package main

import (
	"time"

	"ofam.dev/monogatari"
	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/reminders"
)

// Only the story fixture owns this clock control; no shipped verb changes time.
func buildReminderRollover(env monogatari.Env) (monogatari.Built, error) {
	built, err := buildEmpty(env)
	if err != nil {
		return built, err
	}
	a := built.Program.(*App)
	clock, offset := storyClock(env), time.Duration(0)
	a.clock = func() time.Time { return clock().Add(offset) }
	now := a.reminderNow()
	_, err = a.reminders.store.Write("plants", now.AddDate(0, 0, -2), func(r *reminders.Reminder, _ *reminders.Rule) error { r.Title = "water the plants"; return nil })
	if err == nil {
		_, err = a.reminders.store.Check("plants", now.Format(time.DateOnly), "check", now, "owner")
	}
	if err != nil {
		built.Close()
		return monogatari.Built{}, err
	}
	a.r.Define("story-clock").Verb("tomorrow", func(ochazuke.Ctx) ochazuke.Result { offset += 24 * time.Hour; return ochazuke.Done(nil) })
	if err := a.reminders.list.Refill(); err != nil {
		built.Close()
		return monogatari.Built{}, err
	}
	return built, nil
}
