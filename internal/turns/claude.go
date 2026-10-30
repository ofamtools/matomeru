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
// Records of a subagent (isSidechain, an older Claude Code's) are not the
// pane's turns: they are the agent's (agent.go), unless the parser reads a
// subagent's own log (Sidechain). A <task-notification> prompt is not the
// person's: it is an event (Notice, agent.go). Every other record type (api-request, file-history-snapshot, queue-operation,
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
	AgentID      string `json:"agentId"`
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
	ToolUseResult json.RawMessage `json:"toolUseResult"`
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
	// Sidechain reads a subagent's own log, whose every record is
	// isSidechain; else such records are the agents' (Agent.Inline).
	Sidechain bool
	turns     []Turn
	cur       int  // the running turn's index, -1 (or no turns) for none
	ended     bool // the running turn's agent said end_turn: a prompt now starts a new turn
	tools     map[string][2]int
	seen      map[string]bool
	last      time.Time // the newest record's time in the running turn
	started   bool
	cwd       string // the newest main-chain record's cwd
	live      Live   // the session's settings as the log states them (settings.go)
	// Subagents: the spawn calls by agent id, an older log's inline
	// subagent records by call, and where each notification's card is
	// (its key: the call's id, else the task's), so a repeat folds in.
	agentIDs map[string]string
	inline   map[string]*ClaudeParser
	notices  map[string]noticeRef
}

// noticeRef is where a notification landed: the turn it opened (event
// -1) or one of a turn's events.
type noticeRef struct{ turn, event int }

func (p *ClaudeParser) init() {
	if !p.started {
		p.started, p.cur = true, -1
		p.tools, p.seen = map[string][2]int{}, map[string]bool{}
		p.agentIDs, p.inline, p.notices = map[string]string{}, map[string]*ClaudeParser{}, map[string]noticeRef{}
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
	if json.Unmarshal(line, &r) != nil {
		return
	}
	if r.IsSidechain && !p.Sidechain {
		p.sidechain(r, line)
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
		if r.Attachment != nil && r.Attachment.Type == "queued_command" {
			text := strings.TrimSpace(contentText(r.Attachment.Prompt))
			if notes, rest := splitNotifications(text); len(notes) > 0 {
				for _, n := range notes {
					p.event(n, at)
				}
				text = rest
			}
			if text != "" && p.running() && !p.ended {
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
				p.result(b, at, r.ToolUseResult)
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
	// A notification is an event, never the person's prompt: it opens a
	// turn of its own (the agent answers it) or lands in the running one,
	// and a message the person wrote with it is the real prompt.
	notes, rest := splitNotifications(text)
	if len(notes) > 0 && rest == "" {
		for _, n := range notes {
			if p.running() {
				p.event(n, at)
				continue
			}
			// A repeat's turn (the agent answers it again) folds into the
			// first card: it keeps RepeatOf, the first card the latest state.
			first, again := p.landed(n)
			p.open(r, n.Summary, at)
			no := p.notice(n, at, p.turns[p.cur].ID)
			if again {
				p.update(first, no)
				repeat := *no
				repeat.RepeatOf = p.turns[first.turn].ID
				p.turns[p.cur].Notice = &repeat
				continue
			}
			p.turns[p.cur].Notice = no
			p.land(n, noticeRef{turn: p.cur, event: -1})
		}
		return
	}
	text = rest
	// The person's message while a notification's turn runs starts the
	// person's own turn.
	if p.running() && p.turns[p.cur].Notice != nil {
		p.finish(StateDone)
	}
	defer func() {
		for _, n := range notes {
			p.event(n, at)
		}
	}()
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
			step := Step{Kind: StepTool, ToolID: b.ID, Tool: b.Name, Summary: toolSummary(b.Name, b.Input), Input: input, At: at}
			if IsSpawnTool(b.Name) {
				step.Agent = newAgent(b.Input)
				if step.Agent.Description != "" {
					step.Summary = oneLine(step.Agent.Description, 120)
				}
			}
			t.Steps = append(t.Steps, step)
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

// result pairs a tool result with its call; a spawn call's result says
// how its agent started or ended (toolUseResult, agent.go).
func (p *ClaudeParser) result(b claudeBlock, at time.Time, raw json.RawMessage) {
	ref, ok := p.tools[b.ToolUseID]
	if !ok {
		return
	}
	s := &p.turns[ref[0]].Steps[ref[1]]
	s.Result, s.HasResult, s.IsError = contentText(b.Content), true, b.IsError
	if s.Agent != nil {
		s.Agent.applyResult(s.Result, b.IsError, raw)
		if !s.Agent.Background {
			s.Agent.EndedAt = at
		}
		if s.Agent.ID != "" {
			p.agentIDs[s.Agent.ID] = b.ToolUseID
		}
	}
	if p.cur == ref[0] {
		p.touch(at)
	}
}

// agentStep is the spawn step a notification names: by its call's id,
// else by the agent's id (a resumed agent's notification names no call).
func (p *ClaudeParser) agentStep(n notification) (*Step, string) {
	id := n.ToolUseID
	if _, ok := p.tools[id]; !ok {
		id = p.agentIDs[n.TaskID]
	}
	ref, ok := p.tools[id]
	if !ok {
		return nil, ""
	}
	s := &p.turns[ref[0]].Steps[ref[1]]
	if s.Agent == nil {
		return nil, ""
	}
	return s, p.turns[ref[0]].ID
}

// notice is a notification as a turn's or an event's Notice (landing in
// the turn with id where), and the spawn step it names updated to the
// state it reports.
func (p *ClaudeParser) notice(n notification, at time.Time, where string) *Notice {
	no := n.asNotice(at)
	if s, turn := p.agentStep(n); s != nil {
		no.Turn, no.ToolUseID = turn, s.ToolID
		s.Agent.applyNotification(n, at, where)
		if no.Title == "" {
			no.Title = s.Agent.Description
		}
	}
	return no
}

// update gives the card a notification already landed in the latest
// state.
func (p *ClaudeParser) update(ref noticeRef, no *Notice) {
	t := &p.turns[ref.turn]
	if ref.event < 0 {
		no.Repeats = t.Notice.Repeats + 1
		t.Notice = no
		return
	}
	no.Repeats = t.Events[ref.event].Repeats + 1
	t.Events[ref.event] = *no
}

// event lands a notification: a repeat updates the card it already has
// (the latest state wins), else it is an event of the running turn, or of
// the last turn when none runs.
func (p *ClaudeParser) event(n notification, at time.Time) {
	i := p.cur
	if !p.running() {
		i = len(p.turns) - 1
	}
	where := ""
	if i >= 0 {
		where = p.turns[i].ID
	}
	first, again := p.landed(n)
	no := p.notice(n, at, where)
	if again {
		p.update(first, no)
		p.land(n, first)
		return
	}
	if i < 0 {
		return
	}
	t := &p.turns[i]
	t.Events = append(t.Events, *no)
	p.land(n, noticeRef{turn: i, event: len(t.Events) - 1})
}

// noticeKeys are the keys a task's notifications fold by: the call that
// started it and its task id, as the notification names them or as its
// spawn step does. One notification of a task can name the call and a
// later one only the task (a resumed agent's): both are one card.
func (p *ClaudeParser) noticeKeys(n notification) []string {
	keys := []string{n.key()}
	if n.TaskID != "" {
		keys = append(keys, "task:"+n.TaskID)
		if id := p.agentIDs[n.TaskID]; id != "" {
			keys = append(keys, id)
		}
	}
	return keys
}

// landed is the card a notification of the same task already landed in.
func (p *ClaudeParser) landed(n notification) (noticeRef, bool) {
	for _, k := range p.noticeKeys(n) {
		if ref, ok := p.notices[k]; ok {
			return ref, true
		}
	}
	return noticeRef{}, false
}

// land records the card n landed in under every key it folds by.
func (p *ClaudeParser) land(n notification, ref noticeRef) {
	for _, k := range p.noticeKeys(n) {
		if _, ok := p.notices[k]; !ok {
			p.notices[k] = ref
		}
	}
}

// sidechain reads an older log's subagent record into its agent's own
// turns: the agent its agentId names, else the newest spawn call still
// waiting for its result. It never touches the pane's turns.
func (p *ClaudeParser) sidechain(r claudeRecord, line []byte) {
	id := p.agentIDs[r.AgentID]
	if id == "" {
		for i := len(p.turns) - 1; i >= 0 && id == ""; i-- {
			for j := len(p.turns[i].Steps) - 1; j >= 0; j-- {
				if s := p.turns[i].Steps[j]; s.Agent != nil && !s.HasResult {
					id = s.ToolID
					break
				}
			}
		}
	}
	if id == "" {
		return
	}
	sub := p.inline[id]
	if sub == nil {
		sub = &ClaudeParser{Session: p.Session, Sidechain: true}
		p.inline[id] = sub
	}
	sub.Feed(line)
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
	for id, sub := range p.inline {
		if ref, ok := p.tools[id]; ok {
			if a := out[ref[0]].Steps[ref[1]].Agent; a != nil {
				a.Inline = sub.Turns()
			}
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
	metaDir metaDir
	metas   AgentMetas // the session's subagents' metas (agent.go), read with the log
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
	changed, err := l.tail.read(l.Path, func() { l.parser = ClaudeParser{Session: l.Session} }, l.parser.Feed)
	if err == nil {
		// The metas are small; each is read again only when it changed.
		metas := l.metaDir.read(SubagentDir(l.Path))
		changed = changed || !sameMetas(metas, l.metas)
		l.metas = metas
	}
	return changed, err
}

// Turns are the log's turns so far, their subagents as the session's
// metas say.
func (l *ClaudeLog) Turns() []Turn {
	l.mu.Lock()
	defer l.mu.Unlock()
	list := l.parser.Turns()
	l.metas.Enrich(list)
	return list
}

// AgentMetas are the session's subagents' metas as last read.
func (l *ClaudeLog) AgentMetas() AgentMetas {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.metas
}

// sameMetas reports two reads of a directory's metas alike.
func sameMetas(a, b AgentMetas) bool {
	if len(a) != len(b) {
		return false
	}
	for k, m := range a {
		if b[k] != m {
			return false
		}
	}
	return true
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
	cut     bool // the read starts mid-file: its first line is cut, dropped
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
		t.offset, t.partial, t.cut = 0, nil, false
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
	if t.cut {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			t.partial = append([]byte(nil), buf...)
			return false, nil
		}
		buf, t.cut = buf[i+1:], false
	}
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
