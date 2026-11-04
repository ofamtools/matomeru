package main

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/reminders"
)

const (
	settingReminderNotify   = "reminders.notify"
	settingReminderCommands = "reminders.notify-commands"
	settingReminderInApp    = "reminders.notify-in-app"
)

type reminderNotification struct {
	after   func(time.Duration, tea.Msg) tea.Cmd
	busy    bool
	notices map[string]ochazuke.NoticeID // includes dismissed notices for this date
}

type reminderDue struct{}
type reminderNotified struct {
	count int
	err   error
}

func reminderNotifySettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingReminderNotify, Section: scheduleSection, Group: "Reminders", Type: ochazuke.SettingString, Default: "", Enum: []string{"", "terminal", "os", "both"}, Description: "Override notify.channel for reminders; empty follows the shared channel"},
		{Name: settingReminderCommands, Section: scheduleSection, Group: "Reminders", Type: ochazuke.SettingString, Default: "[]", Description: "When explicitly set, override notify.commands for reminders with JSON argv arrays; [] disables extra reminder senders"},
		{Name: settingReminderInApp, Section: scheduleSection, Group: "Reminders", Type: ochazuke.SettingBoolean, Default: true, Description: "Keep due reminder notices in the chrome until checked off or dismissed, without moving focus"},
	}
}

func newReminderNotifications() *reminderNotification {
	return &reminderNotification{
		notices: map[string]ochazuke.NoticeID{},
		after:   func(d time.Duration, msg tea.Msg) tea.Cmd { return tea.Tick(d, func(time.Time) tea.Msg { return msg }) },
	}
}

// notificationWork snapshots runtime-owned configuration on the loop. The
// returned function performs all database and sender work off the loop.
func (a *App) notificationWork() (func(context.Context) (int, error), error) {
	sender, err := a.notificationSender(settingReminderNotify, settingReminderCommands)
	if err != nil {
		return nil, err
	}
	db, clock, loc := a.reminders.store, a.clock, a.location()
	if clock == nil {
		clock = time.Now
	}
	return func(ctx context.Context) (int, error) {
		now := clock().In(loc)
		return db.NotifyDue(now, func(r reminders.Reminder) (bool, error) {
			if clock().In(loc).Format(time.DateOnly) != now.Format(time.DateOnly) {
				return false, errors.New("local date changed before delivery; retry on the next tick")
			}
			body := "Due " + r.On(now.Format(time.DateOnly)).Time + " · check off in mtm"
			return sender.Send(ctx, r.Title, body)
		})
	}, nil
}

func (a *App) defineReminderNotifications() {
	a.r.Define(reminderNoun).
		Verb("notify-due", a.notifyReminders, "Manually send due reminders once per local date; normal delivery happens only in a running mtm TUI").
		Verb("notify-status", a.reminderNotifyStatus, "Last run, last notification sent, last failure and next due reminder").Fetch()
}
func (a *App) notifyReminders(c ochazuke.Ctx) ochazuke.Result {
	work, err := a.notificationWork()
	if err != nil {
		return ochazuke.Failed(errors.Join(err, a.reminders.store.RecordRun(a.reminderNow(), "", false, err)))
	}
	return ochazuke.Pending(func(ctx context.Context) (any, error) { n, err := work(ctx); return map[string]int{"sent": n}, err }).Then(func(c ochazuke.Ctx, _ any, _ error) {
		if err := a.refreshReminders(); err != nil {
			c.Error(err.Error())
		}
		a.syncReminderNotices()
	})
}
func (a *App) reminderNotifyStatus(ochazuke.Ctx) ochazuke.Result {
	s, now := a.reminders.store, a.reminderNow()
	return ochazuke.Pending(func(context.Context) (any, error) {
		v, err := s.Status()
		if err != nil {
			return nil, err
		}
		rs, err := s.All()
		if err != nil {
			return nil, err
		}
		next, title := "", ""
		for _, r := range rs {
			rule := r.Current()
			if !rule.Enabled || rule.Archived {
				continue
			}
			for i := range 8 {
				day := now.AddDate(0, 0, i)
				if !rule.Due(day) {
					continue
				}
				date := day.Format(time.DateOnly)
				d, err := s.Day(r.ID, date)
				if err != nil {
					return nil, err
				}
				if d.Done || d.NotifiedOn == date {
					continue
				}
				at := date + " " + rule.Time
				if next == "" || at < next {
					next, title = at, r.Title
				}
				break
			}
		}
		return map[string]any{"last-run": v.LastRun, "last-sent": v.LastSent, "last-title": v.LastTitle, "last-failure": v.LastFailure, "last-error": v.LastError, "next-due": next, "next-title": title}, nil
	})
}

func (a *App) startReminderNotifications() tea.Cmd {
	st := a.reminders.notify
	if st.after == nil {
		return nil
	} // story clocks deliver events explicitly
	return st.after(0, reminderDue{})
}
func (a *App) reminderNotificationsUpdate(msg tea.Msg) tea.Cmd {
	st := a.reminders.notify
	switch msg := msg.(type) {
	case reminderDue:
		if !a.notify.live || st.busy {
			return nil
		}
		st.busy = true
		a.syncReminderNotices()
		work, err := a.notificationWork()
		if err != nil {
			s, now := a.reminders.store, a.reminderNow()
			return func() tea.Msg { return reminderNotified{err: errors.Join(err, s.RecordRun(now, "", false, err))} }
		}
		ctx := a.sched.ctx
		return func() tea.Msg { n, err := work(ctx); return reminderNotified{count: n, err: err} }
	case reminderNotified:
		st.busy = false
		if msg.err != nil {
			a.r.UI().Error("reminder notifications: " + msg.err.Error())
		}
		if err := a.refreshReminders(); err != nil {
			a.r.UI().Error(err.Error())
		}
		a.syncReminderNotices()
		if a.notify.live && st.after != nil {
			return st.after(time.Minute, reminderDue{})
		}
	}
	return nil
}

func (a *App) syncReminderNotices() {
	st := a.reminders.notify
	if !a.notify.live || a.cli {
		return
	}
	due := map[string]reminders.Reminder{}
	now := a.reminderNow()
	if a.r.Settings().Bool(settingReminderInApp) {
		rs, err := a.reminders.store.All()
		if err != nil {
			a.r.UI().Error(err.Error())
			return
		}
		for _, r := range rs {
			rule := r.On(now.Format(time.DateOnly))
			if !rule.Due(now) || now.Format("15:04") < rule.Time {
				continue
			}
			d, err := a.reminders.store.Day(r.ID, now.Format(time.DateOnly))
			if err != nil {
				a.r.UI().Error(err.Error())
				return
			}
			if !d.Done {
				due[r.ID+"/"+now.Format(time.DateOnly)] = r
			}
		}
	}
	for key, id := range st.notices {
		if _, ok := due[key]; !ok {
			a.r.UI().ClearNotice(id)
			delete(st.notices, key)
		}
	}
	for key, r := range due {
		if _, exists := st.notices[key]; exists {
			continue
		} // includes explicitly dismissed notices
		hint := "reminder toggle " + r.ID
		for _, stop := range a.r.StopBindings() {
			if stop.Stop == reminderListID {
				for _, row := range stop.Bindings {
					if row.Request.Key() == reminderNoun+".toggle" && row.Keys != "" {
						hint = "Schedules: " + row.Keys + " check/uncheck"
						break
					}
				}
			}
		}
		id := a.r.UI().Notify(hint + " · Reminder: " + strings.Join(strings.Fields(r.Title), " ") + " · notice clear dismisses")
		st.notices[key] = id
		a.noticeSink.keep(id)
	}
}
