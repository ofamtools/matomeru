package turns

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// Activities: what a pane's agent has running beside its own turn, for a
// list that shows each pane's live children (mtm's Panes list). Today
// these are the subagents its spawn calls started (agent.go), each with
// its state; a harness whose log names no such calls has none.
//
// ActivityLog follows a session's log for these alone. It shares the
// turn parser's pieces (the spawn call, its result, the task
// notification; agent.go) but keeps no turns, so following every agent
// pane's log costs the calls and not the conversation: a line that names
// no spawn tool, no notification and no call still waiting is not
// decoded at all.

// Activity is one child activity of a pane: a subagent, as far as its
// parent's log and its meta say.
type Activity struct {
	ID         string // the call that started it
	AgentID    string
	Title      string // its description
	Kind       string // its type (subagent_type)
	State      AgentState
	Status     string // the word its end gave (completed, failed, killed, …); "running" while it runs
	Summary    string // the notification's summary: why it failed
	Background bool
	StartedAt  time.Time
	EndedAt    time.Time // zero while it runs
}

// Running reports whether the activity has not ended.
func (a Activity) Running() bool { return a.State == AgentRunning }

// ActivityLog follows one session log's activities: Read parses what was
// appended since the last read (a log that shrank is read again). Safe
// for concurrent use.
type ActivityLog struct {
	Path    string
	Session string
	mu      sync.Mutex
	tail    tail
	p       activityParser
	metaDir metaDir
	metas   AgentMetas
}

// NewActivityLog follows the activities of the log at path of session.
func NewActivityLog(path, session string) *ActivityLog {
	return &ActivityLog{Path: path, Session: session}
}

// Read parses the lines appended since the last read and the session's
// subagent metas, and reports whether either changed.
func (l *ActivityLog) Read() (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	changed, err := l.tail.read(l.Path, func() { l.p = activityParser{} }, l.p.feed)
	if err == nil {
		metas := l.metaDir.read(SubagentDir(l.Path))
		changed = changed || !sameMetas(metas, l.metas)
		l.metas = metas
	}
	return changed, err
}

// Activities are the log's activities so far, oldest first, as their
// metas complete them.
func (l *ActivityLog) Activities() []Activity {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Activity, 0, len(l.p.order))
	for _, id := range l.p.order {
		c := l.p.calls[id]
		ag := *c.agent
		if m, ok := l.metas[id]; ok {
			ag.apply(m)
		}
		status := c.status
		if status == "" {
			status = activityStatus(ag.State)
		}
		out = append(out, Activity{ID: id, AgentID: ag.ID, Title: ag.Description, Kind: ag.Type, State: ag.State, Status: status,
			Summary: ag.Summary, Background: ag.Background, StartedAt: c.at, EndedAt: ag.EndedAt})
	}
	return out
}

// activityStatus is the word of a state no notification worded.
func activityStatus(s AgentState) string {
	switch s {
	case AgentRunning:
		return "running"
	case AgentDone:
		return "completed"
	case AgentFailed:
		return "failed"
	case AgentStopped:
		return "stopped"
	}
	return string(s)
}

// activityParser reads a log's spawn calls, their results and their
// notifications.
type activityParser struct {
	calls    map[string]*activityCall
	order    []string
	agentIDs map[string]string // agent id → call
	pending  [][]byte          // the ids of the calls with no result yet, as bytes
	seen     map[string]bool
}

type activityCall struct {
	agent  *Agent
	at     time.Time
	status string // the notification's status word, "" before one
}

var (
	spawnNames = [][]byte{[]byte(`"name":"Agent"`), []byte(`"name":"Task"`), []byte(`"name": "Agent"`), []byte(`"name": "Task"`)}
	notifyMark = []byte("task-notification") // bare: a log may escape the angle brackets
)

// wanted reports whether a line may matter: one naming a spawn tool, a
// notification, or a call still waiting for its result.
func (p *activityParser) wanted(line []byte) bool {
	if bytes.Contains(line, notifyMark) {
		return true
	}
	for _, n := range spawnNames {
		if bytes.Contains(line, n) {
			return true
		}
	}
	for _, id := range p.pending {
		if bytes.Contains(line, id) {
			return true
		}
	}
	return false
}

func (p *activityParser) feed(line []byte) {
	if p.calls == nil {
		p.calls, p.agentIDs, p.seen = map[string]*activityCall{}, map[string]string{}, map[string]bool{}
	}
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' || !p.wanted(line) {
		return
	}
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
	at, _ := time.Parse(time.RFC3339Nano, r.Timestamp)
	switch {
	case r.Type == "assistant" && r.Message != nil:
		var blocks []claudeBlock
		if json.Unmarshal(r.Message.Content, &blocks) != nil {
			return
		}
		for _, b := range blocks {
			if b.Type == "tool_use" && IsSpawnTool(b.Name) && b.ID != "" && p.calls[b.ID] == nil {
				p.calls[b.ID] = &activityCall{agent: newAgent(b.Input), at: at}
				p.order = append(p.order, b.ID)
				p.pending = append(p.pending, []byte(b.ID))
			}
		}
	case r.Type == "user" && r.Message != nil:
		var text string
		var blocks []claudeBlock
		if s, ok := stringContent(r.Message.Content); ok {
			text = s
		} else if json.Unmarshal(r.Message.Content, &blocks) == nil {
			for _, b := range blocks {
				switch b.Type {
				case "text":
					text += b.Text + "\n"
				case "tool_result":
					p.result(b, at, r.ToolUseResult)
				}
			}
		}
		p.notifications(text, at)
	case r.Type == "attachment" && r.Attachment != nil && r.Attachment.Type == "queued_command":
		p.notifications(contentText(r.Attachment.Prompt), at)
	}
}

// result is a spawn call's result: a background launch leaves its agent
// running, anything else ends it.
func (p *activityParser) result(b claudeBlock, at time.Time, raw json.RawMessage) {
	c := p.calls[b.ToolUseID]
	if c == nil {
		return
	}
	c.agent.applyResult(contentText(b.Content), b.IsError, raw)
	if !c.agent.Background {
		c.agent.EndedAt = at
	}
	if c.agent.ID != "" {
		p.agentIDs[c.agent.ID] = b.ToolUseID
	}
	p.pending = slicesDeleteBytes(p.pending, b.ToolUseID)
}

// notifications end the background agents the text's notifications name
// (the last word stands: a resumed agent notifies again).
func (p *activityParser) notifications(text string, at time.Time) {
	if !strings.Contains(text, "<task-notification>") {
		return
	}
	notes, _ := splitNotifications(text)
	for _, n := range notes {
		id := n.ToolUseID
		if p.calls[id] == nil {
			id = p.agentIDs[n.TaskID]
		}
		c := p.calls[id]
		if c == nil {
			continue
		}
		switch {
		case strings.Contains(n.Summary, "waiting on its own background work"), n.Status == "" && strings.Contains(n.Summary, "was resumed"):
			// Not an end: it waits on its own agents, or it runs again.
			c.agent.State, c.agent.EndedAt, c.status = AgentRunning, time.Time{}, "waiting on its own work"
			if n.Status == "" {
				c.status = "resumed"
			}
			continue
		case n.Status == "":
			continue // a notice that says no end (a monitor's event)
		}
		c.agent.applyNotification(n, at, "")
		c.status = n.Status
		p.pending = slicesDeleteBytes(p.pending, id)
	}
}

func slicesDeleteBytes(list [][]byte, id string) [][]byte {
	out := list[:0]
	for _, b := range list {
		if string(b) != id {
			out = append(out, b)
		}
	}
	return out
}

// ActivitySource is what a pane's activities are read from.
type ActivitySource interface {
	Read() (bool, error)
	Activities() []Activity
}

// ActivitySourceOf is the activity source of a session's log: a Claude
// log's subagents; nil for a log whose harness names none (omp's today).
func ActivitySourceOf(l Log) ActivitySource {
	if c, ok := l.(*ClaudeLog); ok {
		return NewActivityLog(c.Path, c.Session)
	}
	return nil
}
