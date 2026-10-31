//go:build monogatari

// The tester's fakes (make story-bin): the daemon, its streams, the
// renamer and the system actions a story session runs against, so a
// story never reaches the owner's daemon, clipboard or browser. Copied
// and adapted from the test fakes (panes_test.go fakePanes and
// fakeStream, rename_test.go fakeRenamer, context_test.go fakeSystem),
// which stay in their _test.go files: the tagged test build sees both, so
// these carry story names.

package main

import (
	"context"
	"errors"
	"sync"

	"github.com/unixsuperhero/ochazuke"

	"github.com/unixsuperhero/matomeru/internal/daemon"
	"github.com/unixsuperhero/matomeru/internal/harness"
)

// errStoryDaemon is every call the story daemon cannot answer.
var errStoryDaemon = errors.New("story daemon: not supported in a story session")

// storyPanes is a story's TUIOS daemon: the fixture's panes, a screen per
// window, no real process behind any of them.
type storyPanes struct {
	mu      sync.Mutex
	panes   []pane
	screens map[string]string
}

func newStoryPanes(panes []pane, screens map[string]string) *storyPanes {
	if screens == nil {
		screens = map[string]string{}
	}
	return &storyPanes{panes: panes, screens: screens}
}

func (f *storyPanes) ListPanes(context.Context) ([]pane, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]pane(nil), f.panes...), nil
}

func (f *storyPanes) CapturePane(_ context.Context, p pane) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.screens[p.ID], nil
}

func (f *storyPanes) KillPane(_ context.Context, p pane) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, q := range f.panes {
		if q.ID == p.ID {
			f.panes = append(f.panes[:i:i], f.panes[i+1:]...)
			break
		}
	}
	return nil
}

func (f *storyPanes) KillSession(_ context.Context, session string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var keep []pane
	for _, q := range f.panes {
		if q.Session != session {
			keep = append(keep, q)
		}
	}
	f.panes = keep
	return nil
}

func (f *storyPanes) Activity(context.Context, pane) (string, error) {
	return "12:00:00  tool  Read  main.go", nil
}

func (f *storyPanes) Transcript(context.Context, pane) (paneTranscript, error) {
	return paneTranscript{Text: "user\nfix the tests\n\nassistant\ndone"}, nil
}

func (f *storyPanes) OpenStream(p pane) paneStream {
	return &storyStream{pane: p, frames: make(chan ochazuke.TerminalFrame, 4)}
}

// PasteText, FitWindow and Exited answer as a daemon that took the call.
func (f *storyPanes) PasteText(context.Context, pane, string) error    { return nil }
func (f *storyPanes) FitWindow(context.Context, pane, int, int) error  { return nil }
func (f *storyPanes) Exited(context.Context, pane) (bool, error)       { return false, nil }
func (f *storyPanes) PromptSubmit(context.Context, pane, string) error { return nil }
func (f *storyPanes) PromptPaste(context.Context, pane, string) error  { return nil }
func (f *storyPanes) PromptInterrupt(context.Context, pane) error      { return nil }
func (f *storyPanes) RenameSession(_ context.Context, s, name string) error {
	return f.rename(s, "", name)
}
func (f *storyPanes) RenameWindow(_ context.Context, s, w, name string) error {
	return f.rename(s, w, name)
}

func (f *storyPanes) PromptQueue(context.Context, pane, string) (daemon.QueuePromptResult, error) {
	return daemon.QueuePromptResult{}, errStoryDaemon
}

// rename is the renamer: a session (window "") or one window, in place.
func (f *storyPanes) rename(session, window, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.panes {
		switch {
		case f.panes[i].Session != session:
		case window == "":
			f.panes[i].Session = name
		case f.panes[i].ID == window:
			f.panes[i].Name = name
		}
	}
	return nil
}

// storyStream is a live pane that never draws: keys and pastes are
// taken, the size lease kept, Next waits for the session to end.
type storyStream struct {
	pane     pane
	frames   chan ochazuke.TerminalFrame
	fit      func(ctx context.Context, cols, rows int) error
	keep     bool
	parked   bool
	released bool
}

func (s *storyStream) own(fit func(ctx context.Context, cols, rows int) error) { s.fit = fit }
func (s *storyStream) connState() string                                       { return "" }
func (s *storyStream) reportUnsendable(func(key string))                       {}
func (s *storyStream) Send(...string) error                                    { return nil }
func (s *storyStream) Paste(string) error                                      { return nil }
func (s *storyStream) hold()                                                   { s.keep = true }
func (s *storyStream) drop() error                                             { s.released = true; return nil }

func (s *storyStream) Next(ctx context.Context) (ochazuke.TerminalFrame, error) {
	select {
	case f := <-s.frames:
		return f, nil
	case <-ctx.Done():
		return ochazuke.TerminalFrame{}, ctx.Err()
	}
}

func (s *storyStream) Resize(cols, rows int) error {
	if s.fit != nil && !s.parked && !s.released {
		return s.fit(context.Background(), cols, rows)
	}
	return nil
}

func (s *storyStream) Release() error {
	if s.keep {
		s.parked = true
		return nil
	}
	return s.drop()
}

// storySystem is the clipboard, the opener and the file manager of a
// story: every call is taken and nothing leaves the session.
type storySystem struct{}

func (storySystem) Copy(context.Context, string) (bool, error) { return false, nil }
func (storySystem) Open(context.Context, string) error         { return nil }
func (storySystem) Reveal(context.Context, string) error       { return nil }

// storyClipboard is an empty clipboard.
type storyClipboard struct{}

func (storyClipboard) Read(context.Context, string) (clipboardContent, error) {
	return clipboardContent{}, nil
}

// storyLauncher is the harness driver of a story: it opens no window, so
// a launch reports that a story has no daemon.
type storyLauncher struct{}

func (storyLauncher) OpenWindow(context.Context, harness.Window) (string, error) {
	return "", errStoryDaemon
}
func (storyLauncher) WaitReady(context.Context, string, string, harness.Ready) error {
	return errStoryDaemon
}
func (storyLauncher) SendText(context.Context, string, string, string) error { return errStoryDaemon }
