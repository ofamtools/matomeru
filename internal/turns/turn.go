// Package turns is the turn model of a pane's conversation: one Turn per
// prompt (an agent's) or per command (a shell's), read from the agent's
// own session log (claude.go, omp.go) or the shell's OSC 133 command history
// (shell.go). It knows nothing of the UI; cmd/mtm draws the turns as an
// accordion.
//
// A turn holds its prompt, the messages folded into it while it ran
// (Steers), the steps the agent took (thinking, text, tool calls with
// their results) and its final answer. A pane's sessions are chained into
// one list (Chain): the first turn of every session after the first
// carries NewSession, where the view draws a separator.
package turns

import (
	"strings"
	"time"
)

// Source is where a turn was read from.
type Source string

// Sources.
const (
	SourceClaude Source = "claude"
	SourceOmp    Source = "omp"
	SourceShell  Source = "shell"
)

// State is how far a turn got.
type State string

// States.
const (
	StateRunning State = "running"
	StateDone    State = "done"
	StateError   State = "error" // interrupted, an API error, a command that exited non-zero
)

// StepKind is the kind of one step of a turn.
type StepKind string

// Step kinds.
const (
	StepThinking StepKind = "thinking"
	StepText     StepKind = "text"
	StepTool     StepKind = "tool"
)

// Message is a message folded into a turn: a steer sent while it ran.
type Message struct {
	Text string
	At   time.Time
}

// Step is one step of a turn: a thinking block, a text block, or a tool
// call with its result once the result came.
type Step struct {
	Kind StepKind
	Text string // the thinking or the text
	At   time.Time
	// A tool call.
	ToolID    string
	Tool      string
	Summary   string // the call in one line: a command, a path, a pattern
	Input     string // the call's input, as JSON
	Result    string
	HasResult bool
	IsError   bool
	Agent     *Agent // a spawn call's subagent (agent.go)
}

// Command is one shell command of a pane (tuios command-history).
type Command struct {
	Seq        uint64
	Cmdline    string
	Exit       *int
	At         int64 // started, Unix milliseconds
	DurationMS int64
	Cwd        string
	Row        int // the history row of its prompt; -1 once it left the history
	// End is the row of the next command's prompt, where its output ends;
	// -1 reads to the end of the buffer.
	End int
}

// Turn is one prompt and everything that answered it, or one shell command.
type Turn struct {
	ID         string
	Source     Source
	Session    string // the agent session (or the shell pane) it belongs to
	NewSession bool   // the first turn of a chained session after the first: a separator goes above it
	Prompt     string
	Steers     []Message
	Steps      []Step
	Final      string // the last text the agent wrote in the turn
	StartedAt  time.Time
	Duration   time.Duration
	State      State
	Exit       *int     // a shell command's exit status; nil when the shell sent none
	Command    *Command // a shell turn's command, for reading its output
	// Notice: the turn was opened by a task notification (an event, not
	// the person's prompt); Events are notifications that landed while it
	// ran (agent.go).
	Notice *Notice
	Events []Notice
}

// Response is what answered the turn: the final answer, else every text
// step joined. A shell turn's response is its output, read apart.
func (t Turn) Response() string {
	if t.Final != "" {
		return t.Final
	}
	var parts []string
	for _, s := range t.Steps {
		if s.Kind == StepText && strings.TrimSpace(s.Text) != "" {
			parts = append(parts, s.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// Chain joins a pane's sessions, oldest first, into one list of turns:
// the first turn of every session after the first non-empty one carries
// NewSession, as does a turn its log marked (omp's /clear boundary).
func Chain(sessions ...[]Turn) []Turn {
	var out []Turn
	for _, s := range sessions {
		for i, t := range s {
			t.NewSession = len(out) > 0 && (i == 0 || t.NewSession)
			out = append(out, t)
		}
	}
	return out
}

// clone is a deep copy of a turn's slices, so a snapshot handed to the
// loop never shares memory with the parser that goes on filling it.
func (t Turn) clone() Turn {
	t.Steers = append([]Message(nil), t.Steers...)
	t.Steps = append([]Step(nil), t.Steps...)
	for i := range t.Steps {
		t.Steps[i].Agent = t.Steps[i].Agent.clone()
	}
	t.Events = append([]Notice(nil), t.Events...)
	if t.Notice != nil {
		n := *t.Notice
		t.Notice = &n
	}
	if t.Command != nil {
		c := *t.Command
		t.Command = &c
	}
	return t
}
