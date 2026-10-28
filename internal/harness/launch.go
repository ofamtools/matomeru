package harness

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Window is a pane to open: the session it goes in, its name, its working
// directory and the argv it runs (none: a shell). Workspace names the
// multiplexer workspace it goes on; 0 lets the driver choose one.
type Window struct {
	Session   string
	Name      string
	Cwd       string
	Command   []string
	Workspace int
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
	// Settings are the session's settings at launch by name (effort,
	// fast, permission-mode): flags in the argv, or commands typed before
	// the prompt (Harness.LaunchSettings).
	Settings map[string]string
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
	// Typed are the setting commands typed before the prompt.
	Typed []string `json:"typed,omitempty"`
}

// Launcher runs launches through a Driver. Delay is the wait before typing
// into a pane whose harness has no readiness probe; Sleep is how it waits
// (tests replace both).
type Launcher struct {
	Driver Driver
	Delay  time.Duration
	Sleep  func(context.Context, time.Duration) error
	// Settle is a pause before typing the text after a setting command
	// (0: none beyond the readiness wait).
	Settle time.Duration
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
	flags, typed, notes, err := h.LaunchSettings(s.Settings)
	if err != nil {
		return Result{}, err
	}
	argv := h.ArgvWith(s.Model, s.Prompt, flags)
	id, err := l.Driver.OpenWindow(ctx, Window{Session: s.Session, Name: s.Name, Cwd: s.Cwd, Command: argv})
	if err != nil {
		return Result{}, err
	}
	out := Result{Session: s.Session, Window: id, Name: s.Name, Harness: h.Name, Argv: argv, Prompt: PromptNone, Note: strings.Join(notes, "; ")}
	prompt := s.Prompt
	if h.Prompt == PromptArg {
		prompt = ""
		if s.Prompt != "" {
			out.Prompt = PromptInArgv
		}
	}
	if prompt == "" && len(typed) == 0 {
		return out, nil
	}
	ready := func() (bool, error) {
		if h.Ready.Zero() {
			return true, l.Sleep(ctx, l.Delay)
		}
		r := h.Ready
		if r.TimeoutMS == 0 {
			r.TimeoutMS = DefaultReadyTimeoutMS
		}
		if err := l.Driver.WaitReady(ctx, s.Session, id, r); err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			note := fmt.Sprintf("%s was not ready: %v", h.Name, err)
			out.Note = strings.TrimPrefix(out.Note+"; "+note, "; ")
			return false, nil
		}
		return true, nil
	}
	// Each setting command is typed once the pane is ready, and the next
	// text waits for it to be ready again: a command the harness is still
	// handling never takes the prompt as its argument.
	for i, text := range append(typed, prompt) {
		if text == "" {
			continue
		}
		ok, err := ready()
		if err != nil {
			return out, err
		}
		if !ok {
			if prompt != "" {
				out.Prompt = PromptNotSent
			}
			return out, nil
		}
		if i > 0 && l.Settle > 0 {
			if err := l.Sleep(ctx, l.Settle); err != nil {
				return out, err
			}
		}
		if err := l.Driver.SendText(ctx, s.Session, id, text); err != nil {
			return out, fmt.Errorf("pane %s is open but %q was not typed: %w", id, text, err)
		}
		if i < len(typed) {
			out.Typed = append(out.Typed, text)
		}
	}
	if prompt != "" {
		out.Prompt = PromptSent
	}
	return out, nil
}
