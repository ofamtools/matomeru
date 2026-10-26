package main

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// Notices are transient here: the framework keeps a notice in the footer
// until notice.clear, so a launch's "Launching claude into …" stayed for
// the whole session and grew until it hid the key hints. mtm drops each
// notice on the first key after it appeared, or noticeLinger after the
// newest one, whichever comes first.

const noticeLinger = 6 * time.Second

// noticeTimer is the expiry of the shown notices.
type noticeTimer struct {
	seen int // how many notices the last update left
	gen  int // bumped when a notice appears; an older expiry is stale
}

// noticeExpired is the linger timer of notices generation gen.
type noticeExpired struct{ gen int }

// expireNotices runs after every message. before is how many notices were
// shown when msg arrived: a key or paste drops those (not the ones it
// added itself), the current linger timer drops all, and a new notice
// sets the timer.
func (a *App) expireNotices(msg tea.Msg, before int) tea.Cmd {
	switch v := msg.(type) {
	case tea.KeyPressMsg, tea.PasteMsg:
		drop := min(before, len(a.m.Notices))
		a.m.Notices = a.m.Notices[drop:]
	case noticeExpired:
		if v.gen == a.notices.gen {
			a.m.Notices = nil
		}
	}
	grew := len(a.m.Notices) > a.notices.seen
	a.notices.seen = len(a.m.Notices)
	if !grew || a.pv.after == nil {
		return nil
	}
	a.notices.gen++
	return a.pv.after(noticeLinger, noticeExpired{gen: a.notices.gen})
}
