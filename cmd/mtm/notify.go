package main

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"ofam.dev/ochazuke"

	"ofam.dev/matomeru/internal/notify"
)

const (
	settingNotifyChannel  = "notify.channel"
	settingNotifyCommands = "notify.commands"
	settingNotifyFailures = "notify.failures"
)

type notification struct {
	exec     func(context.Context, []string) (string, error)
	terminal chan notificationTerminal
	output   io.Writer // only the headless shell, never the TUI renderer
	live     bool
}

type notificationTerminal struct {
	text string
	ack  chan error
	ctx  context.Context
}

func notifySettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingNotifyChannel, Section: ochazuke.SettingsGeneralSection, Group: "Notifications", Type: ochazuke.SettingString, Default: "terminal", Enum: []string{"terminal", "os", "both"}, Description: "Notification delivery: OSC 9 to this terminal, osascript on this machine, or both"},
		{Name: settingNotifyCommands, Section: ochazuke.SettingsGeneralSection, Group: "Notifications", Type: ochazuke.SettingString, Default: "[]", Description: "JSON text of extra argv arrays; {{title}} and {{body}} are text arguments, never shell source"},
		{Name: settingNotifyFailures, Section: ochazuke.SettingsGeneralSection, Group: "Notifications", Type: ochazuke.SettingInteger, Default: 3, Description: "Consecutive schedule failures before raising a notifying inbox item; 0 disables failure items"},
	}
}

func newNotifications() *notification {
	return &notification{terminal: make(chan notificationTerminal), exec: func(ctx context.Context, argv []string) (string, error) {
		out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
		return string(out), err
	}}
}

// notificationSender snapshots all runtime-owned settings and endpoints on the
// loop. Both reminder and inbox workers use the same delivery implementation.
func (a *App) notificationSender(channelOverride, commandOverride string) (notify.Sender, error) {
	settings := a.r.Settings()
	mode, commandKey := settings.String(settingNotifyChannel), settingNotifyCommands
	keys := []string{settingNotifyChannel, settingNotifyCommands}
	if channelOverride != "" {
		keys = append(keys, channelOverride)
		if value := settings.String(channelOverride); value != "" {
			mode = value
		}
	}
	if commandOverride != "" {
		keys = append(keys, commandOverride)
		value, err := settings.Get(commandOverride)
		if err != nil {
			return notify.Sender{}, err
		}
		if value.Set {
			commandKey = commandOverride
		}
	}
	for _, problem := range settings.Problems() {
		if strings.Contains(problem, "not a JSON object") {
			return notify.Sender{}, errors.New(problem)
		}
		for _, key := range keys {
			if strings.Contains(problem, key) {
				return notify.Sender{}, errors.New(problem)
			}
		}
	}
	commands, err := notify.Commands(commandKey, settings.String(commandKey))
	if err != nil {
		return notify.Sender{}, err
	}
	if mode == "os" || mode == "both" {
		commands = append([][]string{slices.Clone(notify.OSCommand)}, commands...)
	}
	st := a.notify
	output, live, terminal := st.output, st.live, st.terminal
	return notify.Sender{Channel: mode, Commands: commands, Exec: st.exec, Terminal: func(ctx context.Context, text string) error {
		if !live {
			if output == nil {
				return errors.New("no terminal output is attached")
			}
			_, err := io.WriteString(output, text)
			return err
		}
		req := notificationTerminal{text: text, ack: make(chan error, 1), ctx: ctx}
		select {
		case terminal <- req:
			select {
			case err := <-req.ack:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}}, nil
}
func (a *App) startNotifications() tea.Cmd {
	a.notify.live = true
	if a.inbox.after == nil && a.reminders.notify.after == nil {
		return nil // story clocks deliver events explicitly
	}
	return a.waitNotificationTerminal()
}

func (a *App) waitNotificationTerminal() tea.Cmd {
	ch, ctx := a.notify.terminal, a.sched.ctx
	return func() tea.Msg {
		select {
		case req := <-ch:
			return req
		case <-ctx.Done():
			return nil
		}
	}
}

func (a *App) notificationsUpdate(msg tea.Msg) tea.Cmd {
	req, ok := msg.(notificationTerminal)
	if !ok {
		return nil
	}
	if err := req.ctx.Err(); err != nil {
		req.ack <- err
		return a.waitNotificationTerminal()
	}
	// Acknowledge after Raw has entered Bubble Tea's renderer queue.
	return tea.Sequence(tea.Raw(req.text), func() tea.Msg { req.ack <- nil; return nil }, a.waitNotificationTerminal())
}
