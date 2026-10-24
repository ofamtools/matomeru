package harness

import (
	"context"
	"fmt"
	"time"
)

// Window is a pane to open: the session it goes in, its name, its working
// directory and the argv it runs (none: a shell).
type Window struct {
	Session string
	Name    string
	Cwd     string
	Command []string
}

// Driver is what Launch needs from a terminal multiplexer: open a pane,
// wait until a pane is ready, type text into a pane and submit it.
type Driver interface {
	OpenWindow(ctx context.Context, w Window) (id string, err error)
	WaitReady(ctx context.Context, session, window string, r Ready) error
	SendText(ctx context.Context, session, window, text string) error
}

// Spec is one launch: where, under which name, with which model and first
// prompt.
type Spec struct {
	Session string
	Name    string
	Cwd     string
	Model   string // checked with Harness.Model first
	Prompt  string
}

// Prompt statuses of a Result.
const (
	PromptNone    = "none"     // no prompt was given
	PromptSent    = "sent"     // typed into the ready pane
	PromptInArgv  = "argv"     // passed as the last argument
	PromptNotSent = "not sent" // the pane did not become ready; see Result.Note
)

// Result is a finished launch: the pane, the argv it runs and what became
// of the prompt.
type Result struct {
	Session string   `json:"session"`
	Window  string   `json:"window"`
	Name    string   `json:"name"`
	Harness string   `json:"harness,omitempty"`
	Argv    []string `json:"argv,omitempty"`
	Prompt  string   `json:"prompt"`
	Note    string   `json:"note,omitempty"`
}

// Launcher runs launches through a Driver. Delay is the wait before typing
// into a pane whose harness has no readiness probe; Sleep is how it waits
// (tests replace both).
type Launcher struct {
	Driver Driver
	Delay  time.Duration
	Sleep  func(context.Context, time.Duration) error
}

// DefaultDelay is the fixed wait for a harness without a readiness probe.
const DefaultDelay = 1500 * time.Millisecond

// NewLauncher is a Launcher over d with the default delay.
func NewLauncher(d Driver) *Launcher {
	return &Launcher{Driver: d, Delay: DefaultDelay, Sleep: Sleep}
}

// Sleep waits d or until ctx ends: a Launcher's default Sleep.
func Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Launch is the one flow for every harness and for a plain shell (the zero
// Harness, no prompt): open a pane in s.Session running h's argv in s.Cwd,
// named s.Name; then, when there is a prompt the argv did not carry, wait
// until the pane is ready (h.Ready, else the fixed delay) and type the
// prompt into it, submitted. A pane that does not become ready keeps its
// prompt unsent (typing it could answer a question the program is
// asking); the Result says so and the pane stays open.
func (l *Launcher) Launch(ctx context.Context, h Harness, s Spec) (Result, error) {
	argv := h.Argv(s.Model, s.Prompt)
	id, err := l.Driver.OpenWindow(ctx, Window{Session: s.Session, Name: s.Name, Cwd: s.Cwd, Command: argv})
	if err != nil {
		return Result{}, err
	}
	out := Result{Session: s.Session, Window: id, Name: s.Name, Harness: h.Name, Argv: argv, Prompt: PromptNone}
	switch {
	case s.Prompt == "":
		return out, nil
	case h.Prompt == PromptArg:
		out.Prompt = PromptInArgv
		return out, nil
	}
	if h.Ready.Zero() {
		if err := l.Sleep(ctx, l.Delay); err != nil {
			return out, err
		}
	} else {
		r := h.Ready
		if r.TimeoutMS == 0 {
			r.TimeoutMS = DefaultReadyTimeoutMS
		}
		if err := l.Driver.WaitReady(ctx, s.Session, id, r); err != nil {
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			out.Prompt, out.Note = PromptNotSent, fmt.Sprintf("%s was not ready: %v", h.Name, err)
			return out, nil
		}
	}
	if err := l.Driver.SendText(ctx, s.Session, id, s.Prompt); err != nil {
		return out, fmt.Errorf("pane %s is open but its prompt was not typed: %w", id, err)
	}
	out.Prompt = PromptSent
	return out, nil
}
