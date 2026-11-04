package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/reminders"
)

const (
	settingReminderNotify   = "reminders.notify"
	settingReminderCommands = "reminders.notify-commands"
	settingReminderInApp    = "reminders.notify-in-app"
)

// AppleScript source is constant. Both strings reach it through argv, never
// through interpolation into source, including quotes and embedded newlines.
var reminderOSCommand = []string{"/usr/bin/osascript", "-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run", "--", "{{title}}", "{{body}}"}

type reminderNotification struct {
	after    func(time.Duration, tea.Msg) tea.Cmd
	exec     func(context.Context, []string) (string, error)
	terminal chan reminderTerminal
	output   io.Writer // only the headless shell, never the TUI renderer
	live     bool
	busy     bool
	notices  map[string]ochazuke.NoticeID // ids stay here after dismissal, for this date
}

type reminderDue struct{}
type reminderNotified struct {
	count int
	err   error
}
type reminderTerminal struct {
	text string
	ack  chan error
	ctx  context.Context
}

func reminderNotifySettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingReminderNotify, Section: scheduleSection, Group: "Reminders", Type: ochazuke.SettingString, Default: "terminal", Enum: []string{"terminal", "os", "both"}, Description: "Notify this terminal with OSC 9 (Ghostty), the local OS via osascript, or both; only while mtm runs"},
		{Name: settingReminderCommands, Section: scheduleSection, Group: "Reminders", Type: ochazuke.SettingString, Default: "[]", Description: "JSON text: extra argv arrays, e.g. [[\"notify-tool\",\"{{title}}\",\"{{body}}\"]]. No shell; default no extra senders"},
		{Name: settingReminderInApp, Section: scheduleSection, Group: "Reminders", Type: ochazuke.SettingBoolean, Default: true, Description: "Keep due reminder notices in the chrome until checked off or dismissed, without moving focus"},
	}
}

func newReminderNotifications() *reminderNotification {
	return &reminderNotification{
		terminal: make(chan reminderTerminal), notices: map[string]ochazuke.NoticeID{},
		after: func(d time.Duration, msg tea.Msg) tea.Cmd { return tea.Tick(d, func(time.Time) tea.Msg { return msg }) },
		exec: func(ctx context.Context, argv []string) (string, error) {
			out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
			return string(out), err
		},
	}
}

func terminalReminder(title, body string) string {
	clean := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, s)
	}
	// A fixed nonnumeric prefix avoids OSC 9's ConEmu subcommand syntax.
	return "\x1b]9;mtm: " + clean(title) + " · " + clean(body) + "\x1b\\"
}

func parseReminderCommands(text string) ([][]string, error) {
	var commands [][]string
	if err := json.Unmarshal([]byte(text), &commands); err != nil {
		return nil, fmt.Errorf("%s: want a JSON array of argv arrays: %w", settingReminderCommands, err)
	}
	if commands == nil {
		return nil, fmt.Errorf("%s: use [] for no extra senders, not null", settingReminderCommands)
	}
	for _, argv := range commands {
		if len(argv) == 0 || argv[0] == "" {
			return nil, fmt.Errorf("%s: each sender needs an executable", settingReminderCommands)
		}
		for _, arg := range argv {
			if strings.ContainsRune(arg, 0) {
				return nil, fmt.Errorf("%s: an argument contains NUL", settingReminderCommands)
			}
		}
	}
	return commands, nil
}

// notificationWork snapshots runtime-owned configuration on the loop. The
// returned function performs all database and sender work off the loop.
func (a *App) notificationWork() (func(context.Context) (int, error), error) {
	st := a.reminders.notify
	mode := a.r.Settings().String(settingReminderNotify)
	commands, err := parseReminderCommands(a.r.Settings().String(settingReminderCommands))
	if err != nil {
		return nil, err
	}
	// A bad settings file must not silently replace the owner's senders with
	// defaults. Framework diagnostics stay visible; this check also fails the run.
	for _, p := range a.r.Settings().Problems() {
		if strings.Contains(p, settingReminderCommands) || strings.Contains(p, settingReminderNotify) || strings.Contains(p, "not a JSON object") {
			return nil, errors.New(p)
		}
	}
	if mode == "os" || mode == "both" {
		commands = append([][]string{slices.Clone(reminderOSCommand)}, commands...)
	}
	output, live, terminal, execute := st.output, st.live, st.terminal, st.exec
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
			// The whole group is bounded below the claim's lease, not per command.
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			body := "Due " + r.On(now.Format(time.DateOnly)).Time + " · check off in mtm"
			sent := false
			var failures []error
			if mode == "terminal" || mode == "both" {
				text := terminalReminder(r.Title, body)
				var err error
				if live {
					req := reminderTerminal{text: text, ack: make(chan error, 1), ctx: ctx}
					select {
					case terminal <- req:
						select {
						case err = <-req.ack:
						case <-ctx.Done():
							err = ctx.Err()
						}
					case <-ctx.Done():
						err = ctx.Err()
					}
				} else if output != nil {
					_, err = io.WriteString(output, text)
				} else {
					err = errors.New("no terminal output is attached")
				}
				if err == nil {
					sent = true
				} else {
					failures = append(failures, fmt.Errorf("terminal: %w", err))
				}
			}
			for _, template := range commands {
				argv := make([]string, len(template))
				replace := strings.NewReplacer("{{title}}", r.Title, "{{body}}", body)
				for i, arg := range template {
					argv[i] = replace.Replace(arg)
				}
				out, err := execute(ctx, argv)
				if err == nil {
					sent = true
				} else {
					failures = append(failures, fmt.Errorf("%s: %w: %s", argv[0], err, strings.TrimSpace(out)))
				}
			}
			return sent, errors.Join(failures...)
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
	st.live = true
	if st.after == nil {
		return nil
	} // story clocks deliver events explicitly
	return tea.Batch(st.after(0, reminderDue{}), a.waitReminderTerminal())
}
func (a *App) waitReminderTerminal() tea.Cmd {
	ch, ctx := a.reminders.notify.terminal, a.sched.ctx
	return func() tea.Msg {
		select {
		case req := <-ch:
			return req
		case <-ctx.Done():
			return nil
		}
	}
}
func (a *App) reminderNotificationsUpdate(msg tea.Msg) tea.Cmd {
	st := a.reminders.notify
	switch msg := msg.(type) {
	case reminderTerminal:
		if err := msg.ctx.Err(); err != nil {
			msg.ack <- err
			return a.waitReminderTerminal()
		}
		// Raw goes through Bubble Tea's renderer queue, never os.Stdout. Sequence
		// acknowledges only after the raw message has entered that queue.
		return tea.Sequence(tea.Raw(msg.text), func() tea.Msg { msg.ack <- nil; return nil }, a.waitReminderTerminal())
	case reminderDue:
		if !st.live || st.busy {
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
		if st.live && st.after != nil {
			return st.after(time.Minute, reminderDue{})
		}
	}
	return nil
}

func (a *App) syncReminderNotices() {
	st := a.reminders.notify
	if !st.live || a.cli {
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
