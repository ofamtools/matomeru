package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/ochazuke"
)

// fakeConn is one stream-pane connection: it hands out its frames in
// order, then fails the read with end (a dropped connection).
type fakeConn struct {
	frames chan daemon.Frame
	end    error
	closed chan struct{}
	once   sync.Once
}

func newFakeConn(end error, frames ...daemon.Frame) *fakeConn {
	c := &fakeConn{frames: make(chan daemon.Frame, len(frames)), end: end, closed: make(chan struct{})}
	for _, f := range frames {
		c.frames <- f
	}
	close(c.frames)
	return c
}

func (c *fakeConn) ReadFrame() (daemon.Frame, error) {
	if f, ok := <-c.frames; ok {
		return f, nil
	}
	if c.end != nil {
		return daemon.Frame{}, c.end
	}
	<-c.closed // a live connection with nothing to say
	return daemon.Frame{}, errors.New("use of closed connection")
}
func (c *fakeConn) Lease(uint16, uint16) error { return nil }
func (c *fakeConn) Close() error               { c.once.Do(func() { close(c.closed) }); return nil }

// fakeDialer answers each dial with the next scripted result and records
// the params.
type fakeDialer struct {
	mu      sync.Mutex
	results []dialResult
	params  []daemon.StreamParams
}

type dialResult struct {
	conn *fakeConn
	info daemon.StreamInfo
	err  error
}

func (d *fakeDialer) dial(_ context.Context, p daemon.StreamParams) (paneConn, daemon.StreamInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.params = append(d.params, p)
	if len(d.results) == 0 {
		return nil, daemon.StreamInfo{}, errors.New("connect: no such file or directory")
	}
	r := d.results[0]
	d.results = d.results[1:]
	if r.err != nil {
		return nil, daemon.StreamInfo{}, r.err
	}
	return r.conn, r.info, nil
}

func reconnectStream(d *fakeDialer) *liveStream {
	ctx, cancel := context.WithCancel(context.Background())
	return &liveStream{pane: pane{Session: "work", Pane: daemon.Pane{ID: "w1"}}, ctx: ctx, cancel: cancel, dial: d.dial, backoff: func(int) time.Duration { return 0 }}
}

func snapshot(seq uint64, text string) daemon.Frame {
	return daemon.Frame{Type: 'S', Seq: seq, Cols: 20, Rows: 3, Data: []byte(text)}
}

func output(seq uint64, text string) daemon.Frame {
	return daemon.Frame{Type: 'O', Seq: seq, Data: []byte(text)}
}

func next(t *testing.T, s *liveStream) ochazuke.TerminalFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	f, err := s.Next(ctx)
	if err != nil {
		t.Fatalf("next: %v (state %q)", err, s.connState())
	}
	return f
}

func screenText(f ochazuke.TerminalFrame) string { return strings.Join(f.Lines, "\n") }

// A dropped connection keeps the screen and says reconnecting; the next
// read dials again from the last seq and boot, and output resumes into
// the same screen.
func TestLiveStreamReconnectsFromTheLastSeq(t *testing.T) {
	d := &fakeDialer{results: []dialResult{
		{conn: newFakeConn(io.ErrUnexpectedEOF, snapshot(10, "hello"), output(15, " there")), info: daemon.StreamInfo{Mode: "snapshot", BootID: "b1", Seq: 10}},
		{err: errors.New("connect: connection refused")}, // the daemon is restarting
		{conn: newFakeConn(nil, output(21, " again")), info: daemon.StreamInfo{Mode: "resume", BootID: "b1", Seq: 15, Cols: 20, Rows: 3}},
	}}
	s := reconnectStream(d)
	defer s.Release()
	if f := next(t, s); !strings.Contains(screenText(f), "hello there") {
		t.Fatalf("first screen %q", screenText(f))
	}
	f := next(t, s)
	if s.connState() != streamReconnecting || !strings.Contains(screenText(f), "hello there") {
		t.Fatalf("drop: state %q screen %q", s.connState(), screenText(f))
	}
	f = next(t, s)
	if s.connState() != "" || !strings.Contains(screenText(f), "hello there") {
		t.Fatalf("back: state %q screen %q", s.connState(), screenText(f))
	}
	if f = next(t, s); !strings.Contains(screenText(f), "hello there again") {
		t.Fatalf("resumed screen %q", screenText(f))
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.params) != 3 || d.params[0].FromSeq != 0 || d.params[1].FromSeq != 15 || d.params[2].FromSeq != 15 || d.params[2].BootID != "b1" {
		t.Fatalf("dials %+v", d.params)
	}
}

// A window that is gone when the stream dials again stops the retries:
// the screen stays, the state is pane gone and reads wait quietly.
func TestLiveStreamSaysPaneGone(t *testing.T) {
	d := &fakeDialer{results: []dialResult{
		{conn: newFakeConn(nil, snapshot(5, "last words"), daemon.Frame{Type: 'X', Data: []byte("the daemon stopped")}), info: daemon.StreamInfo{BootID: "b1"}},
		{err: &daemon.Error{Code: "window_not_found", Message: "no window w1"}},
	}}
	s := reconnectStream(d)
	defer s.Release()
	next(t, s)
	if next(t, s); s.connState() != streamReconnecting {
		t.Fatalf("daemon stopped: state %q", s.connState())
	}
	if f := next(t, s); s.connState() != streamGone || !strings.Contains(screenText(f), "last words") {
		t.Fatalf("gone: state %q screen %q", s.connState(), screenText(f))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := s.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a gone pane's read ended with %v, want it to wait", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.params) != 2 {
		t.Fatalf("dialed %d times after the window went", len(d.params))
	}
}

// A program that fails or a refusal is still an error, not a reconnect;
// a stream released while it waits does not dial again.
func TestLiveStreamEndingsThatAreNotDrops(t *testing.T) {
	for _, reason := range []string{"exited 1", "killed 9", "refused: no read grant"} {
		d := &fakeDialer{results: []dialResult{{conn: newFakeConn(nil, snapshot(1, "x"), daemon.Frame{Type: 'X', Data: []byte(reason)})}}}
		s := reconnectStream(d)
		next(t, s)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := s.Next(ctx)
		cancel()
		if err == nil || !strings.Contains(err.Error(), reason) || s.connState() != "" {
			t.Fatalf("%s: err %v state %q", reason, err, s.connState())
		}
		s.Release()
	}
	d := &fakeDialer{results: []dialResult{{conn: newFakeConn(io.EOF, snapshot(1, "x"))}}}
	s := reconnectStream(d)
	next(t, s)
	next(t, s) // the drop
	s.Release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := s.Next(ctx); err == nil {
		t.Fatal("a released stream reconnected")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.params) != 1 {
		t.Fatalf("released stream dialed %d times", len(d.params))
	}
}

// The attached pane's title says when its connection is not live; the
// last screen stays under it.
func TestAttachedPaneTitleSaysReconnectingAndGone(t *testing.T) {
	app, f := panesApp(t)
	cmd := key(t, app, "enter")
	f.streams[0].frames <- ochazuke.TerminalFrame{Lines: []string{"$ last screen"}}
	runOnce(app, cmd)
	app.m.Width, app.m.Height = 120, 30
	view := func() string { return ansi.Strip(app.m.View().Content) }
	if v := view(); strings.Contains(v, streamReconnecting) || !strings.Contains(v, "$ last screen") {
		t.Fatalf("live:\n%s", v)
	}
	f.streams[0].state = streamReconnecting
	if v := view(); !strings.Contains(v, app.attached.label()+" · "+streamReconnecting) || !strings.Contains(v, "$ last screen") {
		t.Fatalf("reconnecting:\n%s", v)
	}
	f.streams[0].state = streamGone
	if v := view(); !strings.Contains(v, " · "+streamGone) || !strings.Contains(v, "$ last screen") {
		t.Fatalf("gone:\n%s", v)
	}
}

// An owned stream asks for its sizes off the loop, one at a time, and
// ends at the newest even when sizes come faster than the daemon answers;
// a released stream asks for nothing more.
func TestOwnedStreamAsksForTheNewestSize(t *testing.T) {
	s := reconnectStream(&fakeDialer{})
	gate := make(chan struct{})
	asked := make(chan [2]int, 8)
	s.own(func(_ context.Context, cols, rows int) error {
		asked <- [2]int{cols, rows}
		<-gate
		return nil
	})
	next := func() [2]int {
		t.Helper()
		select {
		case size := <-asked:
			return size
		case <-time.After(2 * time.Second):
			t.Fatal("no ask")
			return [2]int{}
		}
	}
	if err := s.Resize(80, 24); err != nil {
		t.Fatal(err)
	}
	if got := next(); got != [2]int{80, 24} {
		t.Fatalf("first ask %v", got)
	}
	// While that ask is out, two more sizes collapse into the newest.
	for _, size := range [][2]int{{100, 30}, {120, 40}} {
		if err := s.Resize(size[0], size[1]); err != nil {
			t.Fatal(err)
		}
	}
	close(gate)
	if got := next(); got != [2]int{120, 40} {
		t.Fatalf("second ask %v, want the newest 120x40", got)
	}
	select {
	case size := <-asked:
		t.Fatalf("asked again for %v", size)
	case <-time.After(50 * time.Millisecond):
	}
	// The same size again is no ask; a released stream asks for nothing.
	if err := s.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	if err := s.Release(); err != nil {
		t.Fatal(err)
	}
	if err := s.Resize(60, 20); err != nil {
		t.Fatal(err)
	}
	select {
	case size := <-asked:
		t.Fatalf("asked for %v after the same size or a release", size)
	case <-time.After(50 * time.Millisecond):
	}
}

// A held stream parked by Release asks for no size afterwards: its window
// stays at the last size the daemon took.
func TestParkedStreamAsksForNothing(t *testing.T) {
	s := reconnectStream(&fakeDialer{})
	asked := make(chan [2]int, 8)
	s.own(func(_ context.Context, cols, rows int) error {
		asked <- [2]int{cols, rows}
		return nil
	})
	s.hold()
	if err := s.Resize(80, 24); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-asked:
		if got != [2]int{80, 24} {
			t.Fatalf("ask %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no ask")
	}
	if err := s.Release(); err != nil { // held: parks
		t.Fatal(err)
	}
	if err := s.Resize(120, 40); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-asked:
		t.Fatalf("a parked stream asked for %v", got)
	case <-time.After(50 * time.Millisecond):
	}
	if err := s.drop(); err != nil {
		t.Fatal(err)
	}
}
