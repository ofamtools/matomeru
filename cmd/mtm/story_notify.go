//go:build monogatari

package main

import (
	"context"
	"io"

	"ofam.dev/monogatari"
)

func buildInboxNotice(env monogatari.Env) (monogatari.Built, error) {
	built, err := buildEmpty(env)
	if err != nil {
		return built, err
	}
	a := built.Program.(*App)
	a.notify.live = true
	// A prior tick established the baseline. All senders remain inside the story.
	a.inbox.notifySince, err = inboxNotifySince(a.db, a.now().UnixMilli()-1)
	if err == nil {
		err = a.r.Settings().Set(settingNotifyChannel, "os")
	}
	if err != nil {
		built.Close()
		return monogatari.Built{}, err
	}
	a.notify.output = io.Discard
	a.notify.exec = func(context.Context, []string) (string, error) { return "", nil }
	return built, nil
}
