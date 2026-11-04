package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/actionitems"
	"ofam.dev/matomeru/store"
)

const (
	settingInboxNotify  = "inbox.notify"
	settingInboxSources = "inbox.notify-sources"
	settingInboxInApp   = "inbox.notify-in-app"
)

type inboxNotified struct {
	sent  int
	since int64
	err   error
}

func inboxNotifySettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingInboxNotify, Type: ochazuke.SettingString, Default: "new", Enum: []string{"off", "new"}, Description: "Notify new action items while any mtm TUI runs; off disables inbox delivery and notices"},
		{Name: settingInboxSources, Type: ochazuke.SettingList, Default: []string{"slack", "gh"}, Description: "Sources that notify; action-item add --notify bypasses this filter, not inbox.notify off"},
		{Name: settingInboxInApp, Type: ochazuke.SettingBoolean, Default: true, Description: "Hold new unread action item notices in the existing chrome without moving focus"},
	}
}

// inboxNotifySince uses the existing app-state row, preserving its other fields.
// Every process gets the same baseline, including a first run with delivery off.
func inboxNotifySince(db *store.Store, now int64) (int64, error) {
	var since int64
	err := db.Tx([]string{"active"}, func(tx *store.Tx) error {
		row := map[string]json.RawMessage{}
		if err := tx.Get("active", "singleton", &row); err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if value, ok := row["notify-since"]; ok {
			return json.Unmarshal(value, &since)
		}
		since = now
		raw, err := json.Marshal(since)
		if err != nil {
			return err
		}
		row["notify-since"] = raw
		return tx.Put("active", "singleton", row)
	})
	return since, err
}

func (a *App) inboxNotifyOptions() actionitems.NotifyOptions {
	s := a.r.Settings()
	return actionitems.NotifyOptions{Enabled: s.String(settingInboxNotify) == "new", Sources: s.Strings(settingInboxSources), Since: a.inbox.notifySince}
}

func (a *App) inboxNotificationWork() (func(context.Context) inboxNotified, error) {
	opts := a.inboxNotifyOptions()
	sender, err := a.notificationSender("", "")
	if err != nil && opts.Enabled {
		return nil, err
	}
	items, db, clock := a.inbox.items, a.db, a.clock
	if clock == nil {
		clock = time.Now
	}
	failureLimit := a.r.Settings().Int(settingNotifyFailures)
	return func(ctx context.Context) inboxNotified {
		// Bound the entire batch, not each item, below the claim lease.
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		now := clock().UnixMilli()
		since, err := inboxNotifySince(db, now)
		if err != nil {
			return inboxNotified{err: err}
		}
		opts.Since = since
		if now <= since {
			return inboxNotified{since: since}
		}
		if err := syncFailingSchedules(db, failureLimit, now); err != nil {
			return inboxNotified{since: since, err: err}
		}
		n, err := items.NotifyDue(now, opts, func(title, body string) (bool, error) { return sender.Send(ctx, title, body) })
		return inboxNotified{sent: n, since: since, err: err}
	}, nil
}

func (a *App) notifyActionItems(ochazuke.Ctx) ochazuke.Result {
	work, err := a.inboxNotificationWork()
	if err != nil {
		return ochazuke.Failed(err)
	}
	return ochazuke.Pending(func(ctx context.Context) (any, error) {
		result := work(ctx)
		return map[string]any{"sent": result.sent, "notify-since": result.since}, result.err
	}).Then(func(_ ochazuke.Ctx, value any, _ error) {
		if answer, ok := value.(map[string]any); ok {
			a.inbox.notifySince, _ = answer["notify-since"].(int64)
			if err := a.reloadInbox(); err != nil {
				a.r.UI().Error(err.Error())
			}
			a.syncInboxNotices()
		}
	})
}

func (a *App) inboxNotificationsUpdate(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case inboxDue:
		if a.inbox.notifyBusy {
			return nil
		}
		work, err := a.inboxNotificationWork()
		if err != nil {
			a.r.UI().Error("inbox notifications: " + err.Error())
			return nil
		}
		a.inbox.notifyBusy = true
		ctx := a.sched.ctx
		return func() tea.Msg { return work(ctx) }
	case inboxNotified:
		a.inbox.notifyBusy, a.inbox.notifySince = false, msg.since
		if msg.err != nil {
			a.r.UI().Error("inbox notifications: " + msg.err.Error())
		}
		if err := a.reloadInbox(); err != nil {
			a.r.UI().Error(err.Error())
		}
		a.syncInboxNotices()
	}
	return nil
}

func (a *App) startInboxNotifications() tea.Cmd {
	if a.inbox.after == nil {
		return nil
	}
	return a.inbox.after(0, inboxDue{})
}

func (a *App) syncInboxNotices() {
	if !a.notify.live || a.cli {
		return
	}
	st := a.inbox
	opts := a.inboxNotifyOptions()
	wanted := map[string]string{}
	if a.r.Settings().Bool(settingInboxInApp) && st.notifySince != 0 {
		var items []actionitems.Item
		for _, it := range st.all {
			if opts.Wants(it, a.now().UnixMilli()) {
				items = append(items, it)
			}
		}
		if len(items) > 5 {
			ids := make([]string, len(items))
			for i, it := range items {
				ids[i] = it.ID
			}
			slices.Sort(ids)
			wanted[strings.Join(ids, "/")] = fmt.Sprintf("Inbox: %d new action items · notice clear dismisses", len(items))
		} else {
			for _, it := range items {
				wanted[it.ID] = "Inbox: " + it.Source + " · " + strings.Join(strings.Fields(it.Label()), " ") + " · notice clear dismisses"
			}
		}
	}
	for key, id := range st.notices {
		if _, ok := wanted[key]; !ok {
			a.r.UI().ClearNotice(id)
			delete(st.notices, key)
		}
	}
	if st.notices == nil {
		st.notices = map[string]ochazuke.NoticeID{}
	}
	for key, text := range wanted {
		if _, exists := st.notices[key]; exists {
			continue
		} // includes dismissed notices
		id := a.r.UI().Notify(text)
		st.notices[key] = id
		a.noticeSink.keep(id)
	}
}
