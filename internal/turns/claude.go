package turns

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Claude Code's session log: ~/.claude/projects/<project>/<session>.jsonl,
// one JSON record a line, appended as the session goes. The records a turn
// is built from (shapes read from real logs, 2026-10-27):
//
//   - user, message.content a string (or text blocks): a prompt. Records
//     with isMeta (system reminders, caveats), slash-command echoes
//     (<command-name>, <local-command-stdout>, <bash-input>) and the
//     "[Request interrupted by user]" marker are not prompts; the marker
//     ends the running turn as an error. promptSource is "typed", "queued"
//     (a follow-up that waited for the turn to end) or "system" (a task
//     notification).
//   - user, message.content tool_result blocks {tool_use_id, content (a
//     string or text blocks), is_error}: a tool call's result.
//   - assistant, message.content blocks thinking {thinking}, text {text},
//     tool_use {id, name, input}; one record a block, message.stop_reason
//     "tool_use" while the loop goes on and "end_turn" on the last.
//   - attachment, attachment.type queued_command {prompt}: a message the
//     person sent while the turn ran, delivered into it: a steer.
//   - system, subtype turn_duration {durationMs}: the turn ended.
//
// Records of a subagent (isSidechain) are not the pane's turns. Every
// other record type (api-request, file-history-snapshot, queue-operation,
// ai-title, mode, …) is skipped.

// claudeRecord is the part of a record the parser reads.
type claudeRecord struct {
	Type         string `json:"type"`
	Subtype      string `json:"subtype"`
	UUID         string `json:"uuid"`
	Timestamp    string `json:"timestamp"`
	SessionID    string `json:"sessionId"`
	Cwd          string `json:"cwd"`
	IsSidechain  bool   `json:"isSidechain"`
	IsMeta       bool   `json:"isMeta"`
	IsAPIError   bool   `json:"isApiErrorMessage"`
	PromptSource string `json:"promptSource"`
	DurationMs   int64  `json:"durationMs"`
	Message      *struct {
		Content    json.RawMessage `json:"content"`
		StopReason string          `json:"stop_reason"`
	} `json:"message"`
	Attachment *struct {
		Type   string          `json:"type"`
		Prompt json.RawMessage `json:"prompt"`
	} `json:"attachment"`
}

// claudeBlock is one content block of a message.
type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// ClaudeParser builds turns from a Claude Code session log, a line at a
// time. The zero value is ready; it is not safe for concurrent use
// (ClaudeLog locks around it).
type ClaudeParser struct {
	Session string // the session id the turns carry; else each record's sessionId
	turns   []Turn
	cur     int  // the running turn's index, -1 (or no turns) for none
	ended   bool // the running turn's agent said end_turn: a prompt now starts a new turn
	tools   map[string][2]int
	seen    map[string]bool
	last    time.Time // the newest record's time in the running turn
	started bool
	cwd     string // the newest main-chain record's cwd
	live    Live   // the session's settings as the log states them (settings.go)
}

func (p *ClaudeParser) init() {
	if !p.started {
		p.started, p.cur = true, -1
		p.tools, p.seen = map[string][2]int{}, map[string]bool{}
	}
}

// Feed reads one line of the log. A line that does not parse (a partial
// write, garbage) is skipped.
func (p *ClaudeParser) Feed(line []byte) {
	p.init()
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return
	}
	p.live.feedClaude(line)
	var r claudeRecord
	if json.Unmarshal(line, &r) != nil || r.IsSidechain {
		return
	}
	if r.UUID != "" {
		if p.seen[r.UUID] {
			return
		}
		p.seen[r.UUID] = true
	}
	if r.Cwd != "" {
		p.cwd = r.Cwd
	}
	at, _ := time.Parse(time.RFC3339Nano, r.Timestamp)
	switch r.Type {
	case "user":
		p.user(r, at)
	case "assistant":
		p.assistant(r, at)
	case "attachment":
		if r.Attachment != nil && r.Attachment.Type == "queued_command" && p.running() && !p.ended {
			if text := strings.TrimSpace(contentText(r.Attachment.Prompt)); text != "" {
				t := &p.turns[p.cur]
				t.Steers = append(t.Steers, Message{Text: text, At: at})
				p.touch(at)
			}
		}
	case "system":
		if r.Subtype == "turn_duration" && p.running() {
			t := &p.turns[p.cur]
			if r.DurationMs > 0 {
				t.Duration = time.Duration(r.DurationMs) * time.Millisecond
			}
			p.finish(StateDone)
		}
	}
}

// Cwd is the folder the agent is in as its newest record says: Claude Code
// writes the folder it works in on every record, and a Bash cd moves it
// while the process's own folder stays where it started. "" before any.
func (p *ClaudeParser) Cwd() string { return p.cwd }

// running reports whether a turn is open.
func (p *ClaudeParser) running() bool { return p.cur >= 0 && p.cur < len(p.turns) }

// touch keeps the running turn's last activity for its duration.
func (p *ClaudeParser) touch(at time.Time) {
	if !at.IsZero() {
		p.last = at
	}
}

// finish closes the running turn in state (an error stays an error).
func (p *ClaudeParser) finish(state State) {
	t := &p.turns[p.cur]
	if t.State != StateError {
		t.State = state
	}
	if t.Duration == 0 && !p.last.IsZero() && !t.StartedAt.IsZero() {
		t.Duration = p.last.Sub(t.StartedAt)
	}
	p.cur, p.ended = -1, false
}

func (p *ClaudeParser) user(r claudeRecord, at time.Time) {
	if r.Message == nil {
		return
	}
	var text string
	var blocks []claudeBlock
	if s, ok := stringContent(r.Message.Content); ok {
		text = s
	} else if json.Unmarshal(r.Message.Content, &blocks) == nil {
		var parts []string
		for _, b := range blocks {
			switch b.Type {
			case "text":
				parts = append(parts, b.Text)
			case "tool_result":
				p.result(b, at)
			}
		}
		text = strings.Join(parts, "\n")
	}
	text = strings.TrimSpace(text)
	if text == "" || r.IsMeta {
		return
	}
	if strings.HasPrefix(text, "[Request interrupted by user") {
		if p.running() {
			p.turns[p.cur].State = StateError
			p.touch(at)
			p.finish(StateError)
		}
		return
	}
	if notPrompt(text) {
		return
	}
	// A message that arrives while the turn runs, before its agent's last
	// word, is folded into it; a prompt after the turn ended, a queued
	// follow-up included, starts a new one.
	if p.running() && !p.ended && r.PromptSource != "queued" {
		t := &p.turns[p.cur]
		t.Steers = append(t.Steers, Message{Text: text, At: at})
		p.touch(at)
		return
	}
	if p.running() {
		p.finish(StateDone)
	}
	p.open(r, text, at)
}

// open starts a turn.
func (p *ClaudeParser) open(r claudeRecord, prompt string, at time.Time) {
	session := p.Session
	if session == "" {
		session = r.SessionID
	}
	id := r.UUID
	if id == "" {
		id = fmt.Sprintf("%s#%d", session, len(p.turns))
	}
	p.turns = append(p.turns, Turn{ID: id, Source: SourceClaude, Session: session, Prompt: prompt, StartedAt: at, State: StateRunning})
	p.cur, p.ended, p.last = len(p.turns)-1, false, at
}

// notPrompt reports a user text that is the harness's, not the person's:
// a slash command's echo and its output, a shell escape.
func notPrompt(text string) bool {
	for _, prefix := range []string{"<command-name>", "<command-message>", "<command-args>", "<local-command-", "<bash-input>", "<bash-stdout>", "<bash-stderr>", "Caveat: "} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func (p *ClaudeParser) assistant(r claudeRecord, at time.Time) {
	if r.Message == nil {
		return
	}
	var blocks []claudeBlock
	if json.Unmarshal(r.Message.Content, &blocks) != nil {
		return
	}
	if !p.running() {
		// The agent went on without a prompt in this log (a resumed or
		// compacted session): its work is a turn of its own.
		p.open(r, "", at)
	}
	t := &p.turns[p.cur]
	for _, b := range blocks {
		switch b.Type {
		case "thinking":
			if s := strings.TrimSpace(b.Thinking); s != "" {
				t.Steps = append(t.Steps, Step{Kind: StepThinking, Text: s, At: at})
			}
		case "text":
			if s := strings.TrimSpace(b.Text); s != "" {
				t.Steps = append(t.Steps, Step{Kind: StepText, Text: s, At: at})
				t.Final = s
			}
		case "tool_use":
			input := strings.TrimSpace(string(b.Input))
			t.Steps = append(t.Steps, Step{Kind: StepTool, ToolID: b.ID, Tool: b.Name, Summary: toolSummary(b.Name, b.Input), Input: input, At: at})
			if b.ID != "" {
				p.tools[b.ID] = [2]int{p.cur, len(t.Steps) - 1}
			}
			// A tool call after text: that text was not the last word.
			t.Final = ""
		}
	}
	if r.IsAPIError {
		t.State = StateError
	}
	p.touch(at)
	switch r.Message.StopReason {
	case "end_turn", "stop_sequence", "max_tokens", "refusal":
		p.ended = true
	case "":
	default:
		p.ended = false
	}
}

// result pairs a tool result with its call.
func (p *ClaudeParser) result(b claudeBlock, at time.Time) {
	ref, ok := p.tools[b.ToolUseID]
	if !ok {
		return
	}
	s := &p.turns[ref[0]].Steps[ref[1]]
	s.Result, s.HasResult, s.IsError = contentText(b.Content), true, b.IsError
	p.touch(at)
}

// Turns is a copy of the turns so far, oldest first; the last may be
// running.
func (p *ClaudeParser) Turns() []Turn {
	out := make([]Turn, len(p.turns))
	for i, t := range p.turns {
		out[i] = t.clone()
		if i == p.cur && t.Duration == 0 && !p.last.IsZero() {
			out[i].Duration = p.last.Sub(t.StartedAt)
		}
	}
	return out
}

// stringContent is content that is a JSON string.
func stringContent(raw json.RawMessage) (string, bool) {
	var s string
	if len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &s) == nil {
		return s, true
	}
	return "", false
}

// contentText is a string, or the text of a list of blocks.
func contentText(raw json.RawMessage) string {
	if s, ok := stringContent(raw); ok {
		return s
	}
	var blocks []claudeBlock
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

// toolSummary is a tool call in one line: the field that says what it
// does (a command, a path, a pattern), else its input cut short.
func toolSummary(tool string, raw json.RawMessage) string {
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil {
		return ""
	}
	for _, key := range []string{"command", "file_path", "path", "pattern", "url", "query", "description", "prompt"} {
		if s, ok := in[key].(string); ok && strings.TrimSpace(s) != "" {
			return oneLine(s, 120)
		}
	}
	return oneLine(string(raw), 120)
}

// oneLine is text on one line, cut at n runes.
func oneLine(text string, n int) string {
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > n {
		return string(r[:n]) + "…"
	}
	return text
}

// ClaudeLog follows one session log: Read parses what was appended since
// the last read. A last line without its newline yet waits for the rest;
// a log that shrank (rewritten) is read again from the start. Safe for
// concurrent use.
type ClaudeLog struct {
	Path    string
	Session string
	mu      sync.Mutex
	tail    tail
	parser  ClaudeParser
}

// NewClaudeLog follows the log at path of session.
func NewClaudeLog(path, session string) *ClaudeLog {
	return &ClaudeLog{Path: path, Session: session, parser: ClaudeParser{Session: session}}
}

// SessionID is the session the log is of.
func (l *ClaudeLog) SessionID() string { return l.Session }

// Read parses the lines appended since the last read and reports whether
// there were any.
func (l *ClaudeLog) Read() (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.tail.read(l.Path, func() { l.parser = ClaudeParser{Session: l.Session} }, l.parser.Feed)
}

// Turns are the log's turns so far.
func (l *ClaudeLog) Turns() []Turn {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.parser.Turns()
}

// Cwd is the agent's folder as the log read so far says (ClaudeParser.Cwd).
func (l *ClaudeLog) Cwd() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.parser.Cwd()
}

// cwdTail is how much of a log's end ClaudeLogCwd reads.
const cwdTail = 256 << 10

// ClaudeLogCwd is the agent's folder from the end of the log at path alone
// (its last cwdTail bytes), for a caller that has not followed the log; ""
// when it cannot be read or no record there names one.
func ClaudeLogCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	start := max(0, info.Size()-cwdTail)
	data, err := io.ReadAll(io.NewSectionReader(f, start, info.Size()-start))
	if err != nil {
		return ""
	}
	if start > 0 {
		// The first line is cut; it starts after the first newline.
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		} else {
			return ""
		}
	}
	var p ClaudeParser
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		p.Feed(line)
	}
	return p.Cwd()
}

// tail reads a growing file a line at a time: from where the last read
// stopped, a last line without its newline held until the rest comes, and
// a file that shrank (rewritten) read again from the start after reset.
type tail struct {
	offset  int64
	partial []byte
}

func (t *tail) read(path string, reset func(), feed func([]byte)) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	if info.Size() < t.offset {
		t.offset, t.partial = 0, nil
		reset()
	}
	if info.Size() == t.offset {
		return false, nil
	}
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return false, err
	}
	data, err := io.ReadAll(io.LimitReader(f, info.Size()-t.offset))
	if err != nil {
		return false, err
	}
	t.offset += int64(len(data))
	buf := append(t.partial, data...)
	changed := false
	for {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			break
		}
		feed(buf[:i])
		buf, changed = buf[i+1:], true
	}
	t.partial = append([]byte(nil), buf...)
	return changed, nil
}

// Log is one agent session's log a chain follows.
type Log interface {
	SessionID() string
	Read() (bool, error)
	Turns() []Turn
}

// LogChain is a pane's sessions, oldest first, read as one list of turns:
// a /clear or /new starts a new session (a new log), which AddLog appends.
// Safe for concurrent use.
type LogChain struct {
	mu   sync.Mutex
	logs []Log
}

// AddLog follows a session's log after the ones the chain has, once per
// session.
func (c *LogChain) AddLog(l Log) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, have := range c.logs {
		if have.SessionID() == l.SessionID() {
			return
		}
	}
	c.logs = append(c.logs, l)
}

// Add follows a Claude session's log (ClaudeChain's spelling).
func (c *LogChain) Add(session, path string) { c.AddLog(NewClaudeLog(path, session)) }

// Sessions are the chained session ids, oldest first.
func (c *LogChain) Sessions() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.logs))
	for i, l := range c.logs {
		out[i] = l.SessionID()
	}
	return out
}

// Read reads every log; a log that is gone is skipped (its turns read so
// far stay), and the error says which.
func (c *LogChain) Read() (bool, error) {
	c.mu.Lock()
	logs := append([]Log(nil), c.logs...)
	c.mu.Unlock()
	changed := false
	var errs []error
	for _, l := range logs {
		ch, err := l.Read()
		changed = changed || ch
		if err != nil {
			errs = append(errs, fmt.Errorf("session %s: %w", l.SessionID(), err))
		}
	}
	return changed, errors.Join(errs...)
}

// Cwd is the agent's folder as the newest log that records one says, ""
// when none does (a harness whose log has no folder, or nothing read yet).
func (c *LogChain) Cwd() string {
	c.mu.Lock()
	logs := append([]Log(nil), c.logs...)
	c.mu.Unlock()
	for i := len(logs) - 1; i >= 0; i-- {
		if l, ok := logs[i].(interface{ Cwd() string }); ok {
			if cwd := l.Cwd(); cwd != "" {
				return cwd
			}
		}
	}
	return ""
}

// Turns are every session's turns, chained.
func (c *LogChain) Turns() []Turn {
	c.mu.Lock()
	logs := append([]Log(nil), c.logs...)
	c.mu.Unlock()
	sessions := make([][]Turn, len(logs))
	for i, l := range logs {
		sessions[i] = l.Turns()
	}
	return Chain(sessions...)
}

// ClaudeChain is a pane's Claude sessions (a LogChain of Claude logs).
type ClaudeChain = LogChain

// ClaudeProjects is Claude Code's projects directory: $CLAUDE_CONFIG_DIR
// /projects, else ~/.claude/projects.
func ClaudeProjects() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "projects")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

var notWordChar = regexp.MustCompile(`[^A-Za-z0-9]`)

// ClaudeProjectDir is the directory name Claude Code keeps a working
// directory's sessions under: every character but letters and digits
// becomes "-" (/Users/me/proj → -Users-me-proj).
func ClaudeProjectDir(cwd string) string { return notWordChar.ReplaceAllString(cwd, "-") }

// ErrNoLog is FindClaudeLog's answer when no log of the session exists.
var ErrNoLog = errors.New("no session log")

// FindClaudeLog is the log of session under projects: in cwd's project
// directory first, else in any (a session keeps the directory Claude
// started in, which a pane's cwd may have left).
func FindClaudeLog(projects, cwd, session string) (string, error) {
	if projects == "" || session == "" || strings.ContainsAny(session, `/\`) || strings.Contains(session, "..") {
		return "", ErrNoLog
	}
	name := session + ".jsonl"
	if cwd != "" {
		path := filepath.Join(projects, ClaudeProjectDir(cwd), name)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	matches, _ := filepath.Glob(filepath.Join(projects, "*", name))
	if len(matches) == 0 {
		return "", ErrNoLog
	}
	return matches[0], nil
}
