package harness

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// SessionSource says where a harness records the session a running agent
// writes now, so a pane's live session is found from the harness's own
// files rather than from the id tuios first heard (internal/locate reads
// it). A record is a file: one JSON object per running process
// (Claude Code's sessions/<pid>.json), or a text file per terminal
// (omp's terminal-sessions/<tty>). Field names are JSON keys, or line
// numbers from 1 for Format "lines".
//
//	{"agents": ["claude-code"], "files": "~/.claude/sessions/*.json",
//	 "pid": "pid", "start": "procStart", "env": ["TUIOS_WINDOW_ID"],
//	 "session": "sessionId", "cwd": "cwd",
//	 "transcript": "~/.claude/projects/{cwd_slug}/{session}.jsonl"}
type SessionSource struct {
	// Agents are the harness ids tuios reports that this source covers
	// ("claude-code"); empty is the harness's name. A pane tuios reports
	// no harness for takes the harness of a record it holds.
	Agents []string `json:"agents,omitempty"`
	// Files is a glob of the records. ~ is the home directory,
	// ${VAR:-default} an environment variable, and {tty} the pane's
	// terminal name (ttys041): a record named by the pane's terminal is
	// the pane's.
	Files string `json:"files"`
	// Format is "json" (the default) or "lines".
	Format string `json:"format,omitempty"`
	// PID is the field of the process that wrote the record. The record
	// stands only while that process runs and started when Start says
	// (ps's lstart in UTC), and it is the pane's when one of Env in the
	// process's environment is the pane's window id.
	PID   string   `json:"pid,omitempty"`
	Start string   `json:"start,omitempty"`
	Env   []string `json:"env,omitempty"`
	// Process, for a record named by {tty}: the program that must run on
	// the pane's terminal for the record to stand (a word of its command
	// line, by base name, among the first two: "bun …/bin/omp" is omp).
	Process string `json:"process,omitempty"`
	// Session and Cwd are fields: the session id and the folder the
	// agent started in.
	Session string `json:"session,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
	// Path is the field holding the transcript's path; else Transcript
	// makes it from {session}, {cwd} and {cwd_slug} (the folder with
	// every character but letters and digits as "-").
	Path       string `json:"path,omitempty"`
	Transcript string `json:"transcript,omitempty"`
	// SessionPattern finds the session id in the transcript's base name
	// (its first group) when Session is not given.
	SessionPattern string `json:"sessionPattern,omitempty"`
}

// Session record formats.
const (
	SessionJSON  = "json"
	SessionLines = "lines"
)

// Covers reports whether the source is for agents tuios reports as
// harness id (name is the harness's own name).
func (s SessionSource) Covers(name, id string) bool {
	if id == "" {
		return false
	}
	if len(s.Agents) == 0 {
		return id == name
	}
	for _, a := range s.Agents {
		if a == id {
			return true
		}
	}
	return false
}

// validate reports what makes the source unusable.
func (s SessionSource) validate(harness string) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("harness %s: session: "+format, append([]any{harness}, args...)...)
	}
	if strings.TrimSpace(s.Files) == "" {
		return bad("files is empty; give the glob of the records")
	}
	switch s.Format {
	case "", SessionJSON:
	case SessionLines:
		for name, f := range map[string]string{"pid": s.PID, "start": s.Start, "session": s.Session, "cwd": s.Cwd, "path": s.Path} {
			if f == "" {
				continue
			}
			if n, err := strconv.Atoi(f); err != nil || n < 1 {
				return bad("%s %q is not a line number (format lines)", name, f)
			}
		}
	default:
		return bad("format %q; use %q or %q", s.Format, SessionJSON, SessionLines)
	}
	if s.Path == "" && s.Transcript == "" {
		return bad("give path (the field with the transcript) or transcript (its template)")
	}
	if s.Session == "" && s.SessionPattern == "" {
		return bad("give session (the field with the id) or sessionPattern")
	}
	if s.SessionPattern != "" {
		re, err := regexp.Compile(s.SessionPattern)
		if err != nil || re.NumSubexp() < 1 {
			return bad("sessionPattern %q: a regular expression with a group", s.SessionPattern)
		}
	}
	if s.PID != "" && (s.Start == "" || len(s.Env) == 0) {
		return bad("pid needs start (a reused pid is not trusted) and env (what names the pane)")
	}
	if s.PID == "" && !strings.Contains(s.Files, "{tty}") {
		return bad("records need pid (with start and env) or {tty} in files: nothing else ties one to a pane")
	}
	return nil
}

// builtinSession is a built-in harness's session source: Claude Code
// rewrites sessions/<pid>.json when the process moves to another session
// (2.1.312: {"pid", "sessionId", "cwd", "kind", "procStart", …}); omp
// (oh-my-pi 18) writes terminal-sessions/<tty>, the folder on line 1 and
// the session file on line 2, when it sets a session file.
func builtinSession(name string) *SessionSource {
	switch name {
	case "claude":
		home := "${CLAUDE_CONFIG_DIR:-~/.claude}"
		return &SessionSource{
			Agents: []string{"claude-code", "claude"},
			Files:  home + "/sessions/*.json",
			PID:    "pid", Start: "procStart", Env: []string{"TUIOS_WINDOW_ID", "TUIOS_PANE_ID"},
			Session: "sessionId", Cwd: "cwd",
			Transcript: home + "/projects/{cwd_slug}/{session}.jsonl",
		}
	case "omp":
		return &SessionSource{
			Agents:  []string{"omp"},
			Files:   "${PI_CODING_AGENT_DIR:-~/.omp/agent}/terminal-sessions/{tty}",
			Format:  SessionLines,
			Process: "omp",
			Cwd:     "1", Path: "2",
			SessionPattern: `_([0-9A-Za-z-]+)\.jsonl$`,
		}
	}
	return nil
}
