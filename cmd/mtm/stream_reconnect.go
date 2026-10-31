package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/ochazuke"
)

// Reconnecting a live pane. When the daemon connection under an attached
// pane drops (a read error, the daemon stopping, a busy snapshot), and not
// because its program ended, the terminal keeps the last screen and its
// title reads "reconnecting…"; the stream dials stream-pane again with
// backoff, resuming from the last seq written into the screen (from_seq,
// boot_id), so the daemon replays only what was missed, or sends a fresh
// snapshot when it cannot. When the window no longer exists the title says
// "pane gone" and the stream stops trying.

// The connection states a title shows (connState).
const (
	streamReconnecting = "reconnecting…"
	streamGone         = "pane gone"
)

// paneConn is one stream-pane connection (*daemon.PaneStream).
type paneConn interface {
	ReadFrame() (daemon.Frame, error)
	Lease(cols, rows uint16) error
	Close() error
}

// connStater is a stream that can say its connection is not live.
type connStater interface{ connState() string }

// dialDaemon opens stream-pane on the daemon.
func dialDaemon(c *daemon.Client) func(context.Context, daemon.StreamParams) (paneConn, daemon.StreamInfo, error) {
	return func(ctx context.Context, params daemon.StreamParams) (paneConn, daemon.StreamInfo, error) {
		conn, err := c.StreamPane(ctx, params)
		if err != nil {
			return nil, daemon.StreamInfo{}, err
		}
		return conn, conn.Info, nil
	}
}

// reconnectBackoff waits 250ms, doubling to at most 5s.
func reconnectBackoff(attempt int) time.Duration {
	d := 250 * time.Millisecond << min(attempt, 5)
	return min(d, 5*time.Second)
}

// connState is "" while the stream is live, else streamReconnecting or
// streamGone.
func (s *liveStream) connState() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// applyFrame writes a frame into the screen and moves the resume point.
func (s *liveStream) applyFrame(f daemon.Frame) {
	s.screen.apply(f)
	switch f.Type {
	case 'S', 'O', 'R':
		s.mu.Lock()
		s.seq = f.Seq
		s.mu.Unlock()
	}
}

// dropped reports a frame that ends the connection rather than the
// pane: a read error on a stream nobody released, the daemon stopping, or
// a busy daemon asking for a reconnect.
func (s *liveStream) dropped(ctx context.Context, item streamFrame) bool {
	s.mu.Lock()
	released := s.released
	s.mu.Unlock()
	if released || ctx.Err() != nil || s.ctx != nil && s.ctx.Err() != nil {
		return false
	}
	if item.err != nil {
		return true
	}
	if item.frame.Type != 'X' {
		return false
	}
	reason := string(item.frame.Data)
	return reason == "the daemon stopped" || strings.HasPrefix(reason, "busy:")
}

// lose drops the dead connection and keeps the screen: the next read
// reconnects.
func (s *liveStream) lose() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		close(s.connDone)
		s.conn.Close()
	}
	s.conn, s.frames, s.held = nil, nil, nil
	s.state = streamReconnecting
}

// reconnect dials again with backoff until the stream is back (state ""),
// the window is gone (streamGone) or ctx ends.
func (s *liveStream) reconnect(ctx context.Context) error {
	for attempt := 0; ; attempt++ {
		wait := time.Duration(0)
		if s.backoff != nil {
			wait = s.backoff(attempt)
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		_, err := s.open(ctx, true)
		s.mu.Lock()
		switch {
		case err == nil:
			s.state = ""
		case windowGone(err):
			s.state = streamGone
		case s.released:
			s.mu.Unlock()
			return context.Canceled
		}
		done := s.state != streamReconnecting
		s.mu.Unlock()
		if done {
			return nil
		}
	}
}

// windowGone reports a dial refused because the window or its session no
// longer exists.
func windowGone(err error) bool {
	var remote *daemon.Error
	return errors.As(err, &remote) && (remote.Code == "window_not_found" || remote.Code == "session_not_found")
}

// lastFrame is the screen as it stands, for a read that only changes the
// title.
func (s *liveStream) lastFrame(ctx context.Context) (ochazuke.TerminalFrame, error) {
	if frame, ok := s.screen.frame(); ok {
		return frame, nil
	}
	if ctx.Err() != nil {
		return ochazuke.TerminalFrame{}, ctx.Err()
	}
	return ochazuke.TerminalFrame{Hidden: true}, nil
}

// trackStream remembers the stream behind terminal id, for its title.
func (a *App) trackStream(id string, stream paneStream) {
	if a.termStreams == nil {
		a.termStreams = map[string]paneStream{}
	}
	a.termStreams[id] = stream
}

// streamTitle is a terminal's title with its connection state while that
// is not live: "… · reconnecting…" or "… · pane gone".
func (a *App) streamTitle(id, title string) string {
	if a.r.Terminal(id) == nil {
		return title
	}
	if st, ok := a.termStreams[id].(connStater); ok {
		if state := st.connState(); state != "" {
			return title + " · " + state
		}
	}
	return title
}
