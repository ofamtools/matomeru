package main

import (
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/unixsuperhero/ochazuke"
)

// Notices are transient here: the framework keeps a notice in the footer
// until notice.clear, so a launch's "Launching claude into …" stayed for
// the whole session and grew until it hid the key hints. mtm drops each
// notice on the first key after it appeared, or noticeLinger after the
// newest one, whichever comes first.

const noticeLinger = 6 * time.Second

// noticeTimer is the expiry of the shown notices.
type noticeTimer struct {
	seen int // how many notices had been posted when the last update ended
	gen  int // bumped when a notice appears; an older expiry is stale
}

// noticeExpired is the linger timer of notices generation gen.
type noticeExpired struct{ gen int }

// noticeSink sits between the UI and the Model's sink: it forwards every
// message and keeps the ids of the notices still shown, so expiry takes
// them back with ClearNotice instead of editing Model.Notices.
var _ ochazuke.UISink = (*noticeSink)(nil)

type noticeSink struct {
	next   ochazuke.UISink
	shown  []ochazuke.NoticeID
	posted int
}

// trackNotices puts a noticeSink in front of the sink attached now (the
// Model's).
func (a *App) trackNotices() {
	a.noticeSink = &noticeSink{next: a.r.UI().Sink()}
	a.r.UI().Attach(a.noticeSink)
}

func (s *noticeSink) Notify(id ochazuke.NoticeID, text string) {
	s.shown = append(s.shown, id)
	s.posted++
	if s.next != nil {
		s.next.Notify(id, text)
	}
}

func (s *noticeSink) ClearNotice(id ochazuke.NoticeID) {
	s.shown = slices.DeleteFunc(s.shown, func(v ochazuke.NoticeID) bool { return v == id })
	if s.next != nil {
		s.next.ClearNotice(id)
	}
}

func (s *noticeSink) Status(text string) {
	if s.next != nil {
		s.next.Status(text)
	}
}

// Info forwards the status line's plain tone to the same tone of the next
// sink (UIMessage.Apply), never to its error tone.
func (s *noticeSink) Info(text string) {
	if s.next != nil {
		ochazuke.UIMessage{Kind: ochazuke.UIInfo, Text: text}.Apply(s.next)
	}
}

// shownNotices is a copy of the ids shown now, taken before a message.
func (a *App) shownNotices() []ochazuke.NoticeID {
	if a.noticeSink == nil {
		return nil
	}
	return slices.Clone(a.noticeSink.shown)
}

// expireNotices runs after every message. before is the notices shown
// when msg arrived: a key or paste takes those back (not the ones it
// added itself), the current linger timer takes back all, and a new
// notice sets the timer.
func (a *App) expireNotices(msg tea.Msg, before []ochazuke.NoticeID) tea.Cmd {
	if a.noticeSink == nil {
		return nil
	}
	switch v := msg.(type) {
	case tea.KeyPressMsg, tea.PasteMsg:
		for _, id := range before {
			a.r.UI().ClearNotice(id)
		}
	case noticeExpired:
		if v.gen == a.notices.gen {
			for _, id := range slices.Clone(a.noticeSink.shown) {
				a.r.UI().ClearNotice(id)
			}
		}
	}
	grew := a.noticeSink.posted > a.notices.seen
	a.notices.seen = a.noticeSink.posted
	if !grew || a.pv.after == nil {
		return nil
	}
	a.notices.gen++
	return a.pv.after(noticeLinger, noticeExpired{gen: a.notices.gen})
}
