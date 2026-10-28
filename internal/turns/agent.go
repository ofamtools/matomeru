package turns

import (
	"encoding/json"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Subagents: a Claude Code tool call that starts another agent (Agent, or
// Task before Claude Code 2.1, as tuios's hook names them). Shapes read
// from real logs, 2026-10-28:
//
//   - The call: an assistant tool_use named Agent or Task, input
//     {description, subagent_type, model, run_in_background, prompt}; the
//     prompt is the brief.
//   - Its result: a tool_result, and on the record a toolUseResult. A
//     background launch is {isAsync: true, status: "async_launched",
//     agentId, description, resolvedModel, prompt, outputFile} and its text
//     "Async agent launched successfully. … agentId: <id> …": the agent
//     runs on. A foreground agent's is {status: "completed", agentId,
//     content, totalDurationMs, totalTokens, totalToolUseCount} (the
//     response tuios's hook reads totalToolUseCount from), its text the
//     answer with an "agentId: …" line and a <usage> block after it; an
//     is_error result is a failed agent.
//   - A background agent's end: a later user record with promptSource
//     "system" (origin.kind "task-notification") whose content is
//     <task-notification> with <task-id>, <tool-use-id> (the call),
//     <status> (completed, failed, killed, stopped), <summary> ("Agent
//     "…" finished", or why it failed), <result> and <usage>
//     (<subagent_tokens>, <tool_uses>, <duration_ms>). One task may notify
//     more than once (it was resumed): the last word stands. A
//     notification of a Bash task names a Bash call and is left as it is.
//   - On disk, beside the session's log <session>.jsonl, the directory
//     <session>/subagents holds agent-<id>.jsonl (the subagent's own log,
//     every record isSidechain with its agentId; its first prompt is the
//     brief) and agent-<id>.meta.json {agentType, description, toolUseId,
//     spawnDepth, parentAgentId (a nested agent's spawner), requestShape,
//     model, finished, stoppedByUser}. A nested agent's files sit in the
//     same flat directory, its call in its parent agent's log. A meta
//     without its log happens: the transcript is missing.
//   - Older Claude Code wrote a subagent's records into the parent's own
//     log, isSidechain true: they are the agent's (Agent.Inline), never the
//     pane's turns, and never end or touch the parent's turn (tuios's rule:
//     a sidechain never ends the turn).

// AgentState is how far a subagent got.
type AgentState string

// Agent states.
const (
	AgentRunning AgentState = "running"
	AgentDone    AgentState = "done"
	AgentFailed  AgentState = "failed"
	AgentStopped AgentState = "stopped" // killed or stopped by the person
)

// spawnTools are the tools that start a subagent: Agent, and Task in
// Claude Code before 2.1.
var spawnTools = map[string]bool{"Agent": true, "Task": true}

// IsSpawnTool reports whether a tool call starts a subagent.
func IsSpawnTool(name string) bool { return spawnTools[name] }

// Agent is the subagent a tool call started, as its parent's log, its
// notification and its meta file say.
type Agent struct {
	ID          string // Claude Code's agentId
	Description string
	Type        string // subagent_type (general-purpose, Explore, …)
	Model       string
	Brief       string // the prompt it was given
	Background  bool
	State       AgentState
	Result      string // its last word: the foreground result, or the notification's
	Summary     string // the notification's summary (a failure's reason)
	Tokens      int64
	ToolUses    int
	Duration    time.Duration
	EndedAt     time.Time
	Depth       int    // spawnDepth: 1 for the pane's agent's own, 2 for one of those's
	Parent      string // the agentId of the agent that started it (depth 2 and more)
	Transcript  string // its own log's path, when the session's directory holds it
	Missing     bool   // its transcript is neither on disk nor in the parent's log
	Notice      string // the id of the turn whose notification ended it
	Inline      []Turn // an older log's isSidechain records of it
}

// Notice is a task notification: a turn it opened (Turn.Notice) or one
// that landed in a running turn (Turn.Events). It is an event, never the
// person's prompt; an agent's links back to the call that started it.
type Notice struct {
	ToolUseID string // the call that started the task
	TaskID    string
	Turn      string // the id of the turn whose step started the agent ("" not an agent's, or not in this log)
	Status    string // the notification's: completed, failed, killed, stopped
	State     string // what it says in words: finished, failed, waiting on its own background work, stopped, …
	Title     string // what the task is: the agent's description, else the summary's quoted name
	Summary   string
	Result    string
	Tokens    int64
	ToolUses  int
	Duration  time.Duration
	At        time.Time
	Repeats   int    // notifications of the task before this one folded into its card
	RepeatOf  string // a repeat's own turn: the id of the turn whose card it folds into
}

// key is what folds a task's notifications into one card.
func (n notification) key() string {
	if n.ToolUseID != "" {
		return n.ToolUseID
	}
	return "task:" + n.TaskID
}

var summaryName = regexp.MustCompile(`^(Agent|Background agent|Background command|Monitor|Dynamic workflow|Monitor event:) "(.*)"(.*)$`)

// asNotice is the notification as a Notice: its title and state read
// from the summary (Agent "X" finished → X, finished).
func (n notification) asNotice(at time.Time) *Notice {
	no := &Notice{ToolUseID: n.ToolUseID, TaskID: n.TaskID, Status: n.Status, Summary: n.Summary, Result: n.Result, At: at}
	var u Agent
	u.applyUsage(n.Usage)
	no.Tokens, no.ToolUses, no.Duration = u.Tokens, u.ToolUses, u.Duration
	rest := n.Summary
	if m := summaryName.FindStringSubmatch(n.Summary); m != nil {
		no.Title, rest = m[2], strings.TrimSpace(m[3])
	}
	switch {
	case strings.Contains(rest, "waiting on its own background work"):
		no.State = "waiting on its own background work"
	case n.Status == "failed":
		no.State = "failed"
		if r := strings.TrimSpace(strings.TrimPrefix(rest, "failed")); r != "" {
			no.State = "failed" + r
		}
	case n.Status == "killed" || n.Status == "stopped":
		no.State = "stopped"
	case strings.HasPrefix(rest, "finished"):
		no.State = "finished"
	case rest != "" && no.Title != "":
		no.State = rest
	case n.Status == "completed":
		no.State = "finished"
	default:
		no.State = n.Status
	}
	if no.Title == "" {
		no.Title = n.Summary
	}
	return no
}

var notifyBlock = regexp.MustCompile(`(?s)<task-notification>.*?</task-notification>`)

// splitNotifications takes the <task-notification> blocks out of a
// prompt's text: the notifications, and the text left (the person's own
// message, "" when it was notifications alone).
func splitNotifications(text string) ([]notification, string) {
	if !strings.Contains(text, "<task-notification>") {
		return nil, text
	}
	var out []notification
	for _, b := range notifyBlock.FindAllString(text, -1) {
		if n, ok := parseNotification(b); ok {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil, text
	}
	return out, strings.TrimSpace(notifyBlock.ReplaceAllString(text, ""))
}

func (a *Agent) clone() *Agent {
	if a == nil {
		return nil
	}
	c := *a
	c.Inline = make([]Turn, len(a.Inline))
	for i, t := range a.Inline {
		c.Inline[i] = t.clone()
	}
	return &c
}

// newAgent is the agent a spawn call's input describes, running.
func newAgent(raw json.RawMessage) *Agent {
	var in struct {
		Description string `json:"description"`
		Type        string `json:"subagent_type"`
		Model       string `json:"model"`
		Prompt      string `json:"prompt"`
		Background  bool   `json:"run_in_background"`
	}
	_ = json.Unmarshal(raw, &in)
	return &Agent{Description: in.Description, Type: in.Type, Model: in.Model, Brief: in.Prompt, Background: in.Background, State: AgentRunning, Depth: 1}
}

// agentToolResult is the part of a spawn call's toolUseResult read.
type agentToolResult struct {
	Status   string `json:"status"`
	IsAsync  bool   `json:"isAsync"`
	AgentID  string `json:"agentId"`
	Model    string `json:"resolvedModel"`
	Duration int64  `json:"totalDurationMs"`
	Tokens   int64  `json:"totalTokens"`
	Tools    int    `json:"totalToolUseCount"`
}

var (
	agentIDLine = regexp.MustCompile(`(?m)^agentId: ([A-Za-z0-9_.:@-]+)`)
	usageBlock  = regexp.MustCompile(`(?s)<usage>(.*?)</usage>`)
	usageField  = regexp.MustCompile(`(?m)(subagent_tokens|total_tokens|tool_uses|duration_ms)\s*(?:>|:)\s*([0-9]+)`)
)

// applyResult reads a spawn call's result into its agent: a background
// launch leaves it running, else it ended (failed when the result is an
// error).
func (a *Agent) applyResult(text string, isError bool, raw json.RawMessage) {
	var tr agentToolResult
	if len(raw) > 0 && raw[0] == '{' {
		_ = json.Unmarshal(raw, &tr)
	}
	if tr.AgentID != "" {
		a.ID = tr.AgentID
	} else if m := agentIDLine.FindStringSubmatch(text); m != nil {
		a.ID = m[1]
	}
	if a.Model == "" && tr.Model != "" {
		a.Model = tr.Model
	}
	if tr.IsAsync || tr.Status == "async_launched" || strings.HasPrefix(text, "Async agent launched") {
		a.Background = true
		return
	}
	a.applyUsage(text)
	if tr.Duration > 0 {
		a.Duration = time.Duration(tr.Duration) * time.Millisecond
	}
	if tr.Tokens > 0 {
		a.Tokens = tr.Tokens
	}
	if tr.Tools > 0 {
		a.ToolUses = tr.Tools
	}
	a.Result = agentAnswer(text)
	if isError {
		a.State = AgentFailed
		return
	}
	a.State = AgentDone
}

// applyUsage reads a <usage> block's counts (either spelling).
func (a *Agent) applyUsage(text string) {
	m := usageBlock.FindStringSubmatch(text)
	if m == nil {
		return
	}
	for _, f := range usageField.FindAllStringSubmatch(m[1], -1) {
		n, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil {
			continue
		}
		switch f[1] {
		case "subagent_tokens", "total_tokens":
			a.Tokens = n
		case "tool_uses":
			a.ToolUses = int(n)
		case "duration_ms":
			a.Duration = time.Duration(n) * time.Millisecond
		}
	}
}

// agentAnswer is a foreground result's text without the harness's lines
// after it (the agentId line, the usage block).
func agentAnswer(text string) string {
	text = usageBlock.ReplaceAllString(text, "")
	var keep []string
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "agentId: ") {
			continue
		}
		keep = append(keep, l)
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}

// notification is a <task-notification> prompt.
type notification struct {
	TaskID, ToolUseID, Status, Summary, Result, Usage string
}

var notifyTag = regexp.MustCompile(`(?s)<(task-id|tool-use-id|status|summary|result|usage)>(.*?)</(?:task-id|tool-use-id|status|summary|result|usage)>`)

// parseNotification reads a task notification; false for any other text.
func parseNotification(text string) (notification, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "<task-notification>") {
		return notification{}, false
	}
	var n notification
	for _, m := range notifyTag.FindAllStringSubmatch(text, -1) {
		v := strings.TrimSpace(m[2])
		switch m[1] {
		case "task-id":
			n.TaskID = v
		case "tool-use-id":
			n.ToolUseID = v
		case "status":
			n.Status = v
		case "summary":
			n.Summary = html.UnescapeString(v)
		case "result":
			if n.Result == "" {
				n.Result = v
			}
		case "usage":
			n.Usage = "<usage>" + v + "</usage>"
		}
	}
	return n, n.ToolUseID != "" || n.TaskID != ""
}

// applyNotification ends a background agent as its notification says.
func (a *Agent) applyNotification(n notification, at time.Time, turn string) {
	a.Background = true
	switch n.Status {
	case "completed":
		a.State = AgentDone
	case "failed":
		a.State = AgentFailed
	case "killed", "stopped":
		a.State = AgentStopped
	default:
		a.State = AgentDone
	}
	if n.TaskID != "" && a.ID == "" {
		a.ID = n.TaskID
	}
	a.Summary = n.Summary
	if n.Result != "" {
		a.Result = n.Result
	}
	a.applyUsage(n.Usage)
	a.EndedAt, a.Notice = at, turn
}


// AgentMeta is a subagent's meta file (agent-<id>.meta.json).
type AgentMeta struct {
	ID          string `json:"-"`
	Type        string `json:"agentType"`
	Description string `json:"description"`
	ToolUseID   string `json:"toolUseId"`
	Depth       int    `json:"spawnDepth"`
	Parent      string `json:"parentAgentId"`
	Shape       string `json:"requestShape"`
	Model       string `json:"model"`
	Finished    bool   `json:"finished"`
	Stopped     bool   `json:"stoppedByUser"`
	Transcript  string `json:"-"` // agent-<id>.jsonl, "" when the directory lacks it
}

// apply fills what the parent's log left out from the agent's meta.
func (a *Agent) apply(m AgentMeta) {
	if a.ID == "" {
		a.ID = m.ID
	}
	if a.Type == "" {
		a.Type = m.Type
	}
	if a.Description == "" {
		a.Description = m.Description
	}
	if a.Model == "" {
		a.Model = m.Model
	}
	if m.Depth > 0 {
		a.Depth = m.Depth
	}
	if m.Parent != "" {
		a.Parent = m.Parent
	}
	if m.Shape == "background" {
		a.Background = true
	}
	a.Transcript = m.Transcript
	if a.State == AgentRunning {
		switch {
		case m.Stopped:
			a.State = AgentStopped
		case m.Finished:
			a.State = AgentDone
		}
	}
}

// AgentMetas are a session's subagent metas by the id of the call that
// started each.
type AgentMetas map[string]AgentMeta

// Enrich gives every subagent step of the turns what its meta says and
// whether its transcript is missing. The turns are changed in place.
func (m AgentMetas) Enrich(list []Turn) {
	for i := range list {
		for j := range list[i].Steps {
			s := &list[i].Steps[j]
			if s.Agent == nil {
				continue
			}
			if meta, ok := m[s.ToolID]; ok {
				s.Agent.apply(meta)
			}
			s.Agent.Missing = s.Agent.Transcript == "" && len(s.Agent.Inline) == 0
		}
	}
}

// SubagentDir is the directory of a session log's subagents: beside
// <session>.jsonl, <session>/subagents; a subagent's own log's is the
// directory it is in.
func SubagentDir(log string) string {
	if filepath.Base(filepath.Dir(log)) == "subagents" {
		return filepath.Dir(log)
	}
	return filepath.Join(strings.TrimSuffix(log, ".jsonl"), "subagents")
}

// metaDir reads a subagents directory's metas, each file again only when
// its modification time or size changed. Safe for concurrent use.
type metaDir struct {
	mu    sync.Mutex
	files map[string]metaFile
}

type metaFile struct {
	mod  time.Time
	size int64
	meta AgentMeta
	ok   bool
}

// read is the directory's metas; a directory that does not exist has
// none.
func (d *metaDir) read(dir string) AgentMetas {
	d.mu.Lock()
	defer d.mu.Unlock()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	if d.files == nil {
		d.files = map[string]metaFile{}
	}
	logs := map[string]bool{}
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, ".jsonl") {
			logs[strings.TrimSuffix(name, ".jsonl")] = true
		}
	}
	out := AgentMetas{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".meta.json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		f := d.files[name]
		if !f.ok || !f.mod.Equal(info.ModTime()) || f.size != info.Size() {
			f = metaFile{mod: info.ModTime(), size: info.Size()}
			if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil && json.Unmarshal(data, &f.meta) == nil {
				f.ok = true
			}
			d.files[name] = f
		}
		if !f.ok || f.meta.ToolUseID == "" {
			continue
		}
		base := strings.TrimSuffix(name, ".meta.json")
		m := f.meta
		m.ID = strings.TrimPrefix(base, "agent-")
		if logs[base] {
			m.Transcript = filepath.Join(dir, base+".jsonl")
		}
		out[m.ToolUseID] = m
	}
	return out
}

// AgentTailBytes is how much of a large subagent log's end AgentLogs
// reads first: the brief comes from the parent's call, so the end, where
// the work and the answer are, is what is shown.
const AgentTailBytes = 1 << 20

// AgentTranscript is a subagent's own turns as its log says.
type AgentTranscript struct {
	Turns     []Turn
	Truncated bool  // the log was read from its last AgentTailBytes: earlier steps are not shown
	Size      int64 // the log's size when read
	Metas     AgentMetas
}

// AgentLogs reads subagents' logs on demand and keeps each: a log whose
// modification time and size did not change is not read again, one that
// grew is read from where the last read stopped, and a large one is read
// from its last AgentTailBytes. Safe for concurrent use; meant to be read
// off the UI loop.
type AgentLogs struct {
	mu    sync.Mutex
	logs  map[string]*agentLog
	metas map[string]*metaDir
	reads atomic.Int64
}

type agentLog struct {
	mu        sync.Mutex
	mod       time.Time
	size      int64
	tail      tail
	parser    ClaudeParser
	truncated bool
	read      bool
}

// Reads is how many times a log's bytes were read (not answered from the
// cache): for tests that check the laziness.
func (c *AgentLogs) Reads() int64 { return c.reads.Load() }

// Read is the subagent log at path's turns, enriched with the metas of
// its directory (its own nested agents').
func (c *AgentLogs) Read(path string) (AgentTranscript, error) {
	c.mu.Lock()
	if c.logs == nil {
		c.logs, c.metas = map[string]*agentLog{}, map[string]*metaDir{}
	}
	l := c.logs[path]
	if l == nil {
		l = &agentLog{parser: ClaudeParser{Sidechain: true}}
		c.logs[path] = l
	}
	dir := SubagentDir(path)
	md := c.metas[dir]
	if md == nil {
		md = &metaDir{}
		c.metas[dir] = md
	}
	c.mu.Unlock()

	l.mu.Lock()
	defer l.mu.Unlock()
	info, err := os.Stat(path)
	if err != nil {
		return AgentTranscript{}, err
	}
	if !l.read || !info.ModTime().Equal(l.mod) || info.Size() != l.size {
		if !l.read && info.Size() > AgentTailBytes {
			l.tail.offset, l.tail.cut, l.truncated = info.Size()-AgentTailBytes, true, true
		}
		c.reads.Add(1)
		if _, err := l.tail.read(path, func() { l.parser, l.truncated = ClaudeParser{Sidechain: true}, false }, l.parser.Feed); err != nil {
			return AgentTranscript{}, err
		}
		l.mod, l.size, l.read = info.ModTime(), info.Size(), true
	}
	metas := md.read(dir)
	list := l.parser.Turns()
	metas.Enrich(list)
	return AgentTranscript{Turns: list, Truncated: l.truncated, Size: l.size, Metas: metas}, nil
}
