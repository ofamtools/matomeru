package turns

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// omp's (oh-my-pi's) session log: <agent dir>/sessions/<workspace>/
// <timestamp>_<session id>.jsonl, one JSON entry a line, appended as the
// session goes. The file is a tree, not a list: every entry but the
// header carries id and parentId, and a branch (a retry, /tree, a fork in
// place) appends entries whose parent is an older entry. The current
// branch is the path from the leaf back to the root; the leaf is the last
// entry's activeLeafId when it carries one (null: a cleared session), else
// the last entry's id (omp's resolveSessionLeafId,
// src/session/session-entries.ts). Entries off that path are abandoned
// branches and are not the pane's turns.
//
// Entry types (shapes read from real logs, omp 18.13.0, 2026-10-27):
//
//   - title: a fixed-width slot on the first line, rewritten in place; no id.
//   - session: the header {id, cwd, version}; no id of the tree.
//   - message, message.role:
//   - user {content: text and image blocks, steering}: a prompt. steering
//     true is a message the person sent while the agent worked (omp's
//     steer, agent-session.ts): it is folded into the running turn. A
//     follow-up (queued to run after the turn) carries no flag and starts
//     a turn of its own.
//   - assistant {content: thinking {thinking}, text {text}, toolCall {id,
//     name, arguments} blocks, stopReason}: stopReason toolUse while the
//     loop goes on; stop ends the turn; error and aborted end it as an
//     error (aborted is an interrupt).
//   - toolResult {toolCallId, toolName, content, isError}: a call's result.
//   - bashExecution {command, output, exitCode}: a `!command` the person
//     ran; a turn of its own (a step of the running turn while one runs).
//   - developer, custom, fileMention: the harness's, not the person's.
//   - reset_boundary: /clear in place; the next turn draws a separator.
//   - compaction, branch_summary, model_change, thinking_level_change,
//     title_change, custom, custom_message, credential_pin, model_usage,
//     ttsr_injection, label: no turn of their own.

// ompEntry is the part of an entry the parser reads.
type ompEntry struct {
	Type         string          `json:"type"`
	ID           string          `json:"id"`
	ParentID     *string         `json:"parentId"`
	Timestamp    string          `json:"timestamp"`
	ActiveLeafID json.RawMessage `json:"activeLeafId"`
	Message      *ompMessage     `json:"message"`
}

type ompMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Steering   bool            `json:"steering"`
	StopReason string          `json:"stopReason"`
	ToolCallID string          `json:"toolCallId"`
	IsError    bool            `json:"isError"`
	Command    string          `json:"command"`
	Output     string          `json:"output"`
	ExitCode   *int            `json:"exitCode"`
	Cancelled  bool            `json:"cancelled"`
}

type ompBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ompNode is one entry of the tree, decoded once.
type ompNode struct {
	entry  ompEntry
	parent string
	at     time.Time
}

// OmpParser builds turns from an omp session log, a line at a time; the
// turns follow the current branch. The zero value is ready; it is not
// safe for concurrent use (OmpLog locks around it).
type OmpParser struct {
	Session string // the session id the turns carry; else the header's
	nodes   map[string]*ompNode
	leaf    string // "" with no leaf: an empty or cleared session
	dirty   bool
	turns   []Turn
	live    Live // the session's settings as the log states them (settings.go)
}

// Feed reads one line of the log. A line that does not parse (a partial
// write, garbage) is skipped.
func (p *OmpParser) Feed(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return
	}
	var e ompEntry
	if json.Unmarshal(line, &e) != nil {
		return
	}
	p.live.feedOmp(line)
	if e.Type == "session" {
		if p.Session == "" {
			p.Session = e.ID
		}
		return
	}
	if e.ID == "" {
		return // the title slot and anything else outside the tree
	}
	if p.nodes == nil {
		p.nodes = map[string]*ompNode{}
	}
	n := &ompNode{entry: e}
	if e.ParentID != nil {
		n.parent = *e.ParentID
	}
	n.at, _ = time.Parse(time.RFC3339Nano, e.Timestamp)
	p.nodes[e.ID] = n
	p.leaf = e.ID
	if len(e.ActiveLeafID) > 0 {
		var leaf *string
		if json.Unmarshal(e.ActiveLeafID, &leaf) == nil {
			if leaf == nil {
				p.leaf = ""
			} else if _, ok := p.nodes[*leaf]; ok || *leaf == e.ID {
				p.leaf = *leaf
			}
		}
	}
	p.dirty = true
}

// branch is the current branch, root first.
func (p *OmpParser) branch() []*ompNode {
	var path []*ompNode
	seen := map[string]bool{}
	for id := p.leaf; id != "" && !seen[id]; {
		n, ok := p.nodes[id]
		if !ok {
			break
		}
		seen[id] = true
		path = append(path, n)
		id = n.parent
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// ompBuild folds a branch into turns.
type ompBuild struct {
	session  string
	turns    []Turn
	cur      int // the running turn, -1 for none
	tools    map[string][2]int
	last     time.Time
	boundary bool // a /clear came: the next turn starts a new session
}

func (p *OmpParser) build() []Turn {
	b := &ompBuild{session: p.Session, cur: -1, tools: map[string][2]int{}}
	for _, n := range p.branch() {
		b.node(n)
	}
	if b.cur >= 0 {
		t := &b.turns[b.cur]
		if !b.last.IsZero() && !t.StartedAt.IsZero() {
			t.Duration = b.last.Sub(t.StartedAt)
		}
	}
	return b.turns
}

func (b *ompBuild) node(n *ompNode) {
	e := n.entry
	switch e.Type {
	case "reset_boundary":
		if b.cur >= 0 {
			b.finish(StateDone)
		}
		b.boundary = len(b.turns) > 0
		return
	case "message":
	default:
		return
	}
	m := e.Message
	if m == nil {
		return
	}
	switch m.Role {
	case "user":
		text := strings.TrimSpace(ompText(m.Content))
		if text == "" {
			return
		}
		if m.Steering && len(b.turns) > 0 {
			// A steer folds into the turn it was sent to, which runs
			// again even when its agent had said its last word.
			if b.cur < 0 {
				b.cur = len(b.turns) - 1
				b.turns[b.cur].State = StateRunning
			}
			t := &b.turns[b.cur]
			t.Steers = append(t.Steers, Message{Text: text, At: n.at})
			b.touch(n.at)
			return
		}
		if b.cur >= 0 {
			b.finish(StateDone)
		}
		b.open(e.ID, text, n.at)
	case "assistant":
		var blocks []ompBlock
		if json.Unmarshal(m.Content, &blocks) != nil {
			return
		}
		switch {
		case b.cur >= 0:
		case len(b.turns) > 0 && !b.boundary:
			// The agent went on after its turn ended with no prompt in
			// between (a retry after an error, a background job's result
			// it resumed on): the same turn, running again.
			b.cur = len(b.turns) - 1
			b.turns[b.cur].State = StateRunning
		default:
			// No prompt on this branch yet (a resumed or compacted
			// session): a turn of its own.
			b.open(e.ID, "", n.at)
		}
		t := &b.turns[b.cur]
		for _, bl := range blocks {
			switch bl.Type {
			case "thinking":
				if s := strings.TrimSpace(bl.Thinking); s != "" {
					t.Steps = append(t.Steps, Step{Kind: StepThinking, Text: s, At: n.at})
				}
			case "text":
				if s := strings.TrimSpace(bl.Text); s != "" {
					t.Steps = append(t.Steps, Step{Kind: StepText, Text: s, At: n.at})
					t.Final = s
				}
			case "toolCall":
				input := strings.TrimSpace(string(bl.Arguments))
				t.Steps = append(t.Steps, Step{Kind: StepTool, ToolID: bl.ID, Tool: bl.Name, Summary: toolSummary(bl.Name, bl.Arguments), Input: input, At: n.at})
				if bl.ID != "" {
					b.tools[bl.ID] = [2]int{b.cur, len(t.Steps) - 1}
				}
				t.Final = ""
			}
		}
		b.touch(n.at)
		switch m.StopReason {
		case "stop", "length":
			b.finish(StateDone)
		case "error", "aborted":
			b.finish(StateError)
		}
	case "toolResult":
		ref, ok := b.tools[m.ToolCallID]
		if !ok {
			return
		}
		s := &b.turns[ref[0]].Steps[ref[1]]
		s.Result, s.HasResult, s.IsError = ompText(m.Content), true, m.IsError
		b.touch(n.at)
	case "bashExecution":
		failed := m.Cancelled || m.ExitCode != nil && *m.ExitCode != 0
		step := Step{Kind: StepTool, Tool: "bash", Summary: oneLine(m.Command, 120), Input: m.Command, Result: m.Output, HasResult: true, IsError: failed, At: n.at}
		if b.cur >= 0 {
			t := &b.turns[b.cur]
			t.Steps = append(t.Steps, step)
			b.touch(n.at)
			return
		}
		b.open(e.ID, "! "+m.Command, n.at)
		t := &b.turns[b.cur]
		t.Steps = append(t.Steps, step)
		state := StateDone
		if failed {
			state = StateError
		}
		b.finish(state)
	}
}

func (b *ompBuild) touch(at time.Time) {
	if !at.IsZero() {
		b.last = at
	}
}

func (b *ompBuild) open(id, prompt string, at time.Time) {
	b.turns = append(b.turns, Turn{ID: b.session + "/" + id, Source: SourceOmp, Session: b.session, NewSession: b.boundary, Prompt: prompt, StartedAt: at, State: StateRunning})
	b.cur, b.last, b.boundary = len(b.turns)-1, at, false
}

// finish closes the running turn in state (an error stays an error).
func (b *ompBuild) finish(state State) {
	t := &b.turns[b.cur]
	t.State = state
	if !b.last.IsZero() && !t.StartedAt.IsZero() {
		t.Duration = b.last.Sub(t.StartedAt)
	}
	b.cur = -1
}

// ompText is a string, or the text blocks of a content list.
func ompText(raw json.RawMessage) string {
	if s, ok := stringContent(raw); ok {
		return s
	}
	var blocks []ompBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// Turns is a copy of the current branch's turns, oldest first; the last
// may be running.
func (p *OmpParser) Turns() []Turn {
	if p.dirty || p.turns == nil {
		p.turns, p.dirty = p.build(), false
	}
	out := make([]Turn, len(p.turns))
	for i, t := range p.turns {
		out[i] = t.clone()
	}
	return out
}

// OmpLog follows one omp session log (as ClaudeLog follows Claude's).
// Safe for concurrent use.
type OmpLog struct {
	Path    string
	Session string
	mu      sync.Mutex
	tail    tail
	parser  OmpParser
}

// NewOmpLog follows the log at path of session.
func NewOmpLog(path, session string) *OmpLog {
	return &OmpLog{Path: path, Session: session, parser: OmpParser{Session: session}}
}

// SessionID is the session the log is of.
func (l *OmpLog) SessionID() string { return l.Session }

// Read parses the lines appended since the last read and reports whether
// there were any.
func (l *OmpLog) Read() (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.tail.read(l.Path, func() { l.parser = OmpParser{Session: l.Session} }, l.parser.Feed)
}

// Turns are the log's turns so far, on its current branch.
func (l *OmpLog) Turns() []Turn {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.parser.Turns()
}

// OmpSessions is omp's sessions directory: $PI_CODING_AGENT_DIR/sessions,
// else ~/.omp/agent/sessions.
func OmpSessions() string {
	if dir := os.Getenv("PI_CODING_AGENT_DIR"); dir != "" {
		return filepath.Join(dir, "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".omp", "agent", "sessions")
}

// FindOmpLog is the log of session under sessions: omp names it
// <timestamp>_<id>.jsonl in its workspace's directory, so any workspace
// directory is looked in (a pane's cwd may have left the one omp started
// in).
func FindOmpLog(sessions, session string) (string, error) {
	if sessions == "" || session == "" || strings.ContainsAny(session, `/\*?[`) || strings.Contains(session, "..") {
		return "", ErrNoLog
	}
	matches, _ := filepath.Glob(filepath.Join(sessions, "*", "*_"+session+".jsonl"))
	if len(matches) == 0 {
		return "", fmt.Errorf("%w: no omp session %s", ErrNoLog, session)
	}
	return matches[len(matches)-1], nil
}

// IsNoLog reports an error that says no log exists.
func IsNoLog(err error) bool { return errors.Is(err, ErrNoLog) }
