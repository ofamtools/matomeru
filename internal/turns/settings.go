package turns

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Live is a session's settings as its own log says them now, each empty
// (or zero) when the log states none. It is read from the same lines the
// turns are, as they are appended, so it follows a change made inside the
// session (a /model, a shift+tab) as soon as the harness writes it.
//
// What each harness writes (shapes read from real logs, 2026-10-28):
//
//   - Claude Code 2.1.312 (~/.claude/projects/<dir>/<session>.jsonl):
//     every assistant record carries message.model (the model that
//     answered; "<synthetic>" for the harness's own), effort (the effort
//     the request ran at; perTurnEffort beside it), message.usage with
//     input_tokens, cache_creation_input_tokens, cache_read_input_tokens
//     (their sum is the context the request used, as Claude Code's status
//     line counts it) and speed ("fast" with fast mode on, else
//     "standard"). A permission-mode record {permissionMode} is written
//     with the session's metadata as it goes; a cost-state record
//     {totalCostUSD} when the session's cost is saved. A slash command's
//     reply is a <local-command-stdout> text: "Set model to `Opus 5.5`
//     …", "Effort level set to high", "Fast mode ON".
//   - omp 18.13.0: model_change {model: "provider/id"}, thinking_level_change
//     {thinkingLevel, configured}, service_tier_change {serviceTier: null
//     off, else on}, mode_change {mode: "plan" or "none"}; an assistant
//     message carries model, provider and usage {input, cacheRead,
//     cacheWrite, cost.total}, a model_usage entry a side request's cost.
//   - Codex 0.162.1 (~/.codex/sessions/YYYY/MM/DD/rollout-…-<id>.jsonl):
//     turn_context {model, effort, approval_policy, sandbox_policy.type,
//     service_tier}, token_count {info.last_token_usage.total_tokens,
//     info.model_context_window}. No cost.
type Live struct {
	Model          string    `json:"model,omitempty"`
	Effort         string    `json:"effort,omitempty"`
	Fast           string    `json:"fast,omitempty"` // "on" or "off"
	PermissionMode string    `json:"permission_mode,omitempty"`
	Mode           string    `json:"mode,omitempty"`           // omp's plan mode, Codex's collaboration mode
	ContextTokens  int64     `json:"context_tokens,omitempty"` // the context the newest request used
	ContextWindow  int64     `json:"context_window,omitempty"` // as the log states it (Codex); 0 unknown
	Cost           float64   `json:"cost,omitempty"`
	HasCost        bool      `json:"has_cost,omitempty"`
	At             time.Time `json:"at,omitzero"` // the newest record that stated one
}

// Zero reports whether the log stated nothing.
func (l Live) Zero() bool {
	return l.Model == "" && l.Effort == "" && l.Fast == "" && l.PermissionMode == "" && l.Mode == "" && l.ContextTokens == 0 && !l.HasCost
}

func (l *Live) touch(at time.Time) {
	if !at.IsZero() {
		l.At = at
	}
}

// claudeSettings is the part of a Claude record the settings read.
type claudeSettings struct {
	Type           string          `json:"type"`
	Subtype        string          `json:"subtype"`
	Timestamp      string          `json:"timestamp"`
	IsSidechain    bool            `json:"isSidechain"`
	Effort         string          `json:"effort"`
	PerTurnEffort  string          `json:"perTurnEffort"`
	PermissionMode string          `json:"permissionMode"`
	TotalCostUSD   *float64        `json:"totalCostUSD"`
	Content        json.RawMessage `json:"content"`
	Message        *struct {
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   *struct {
			Input         int64  `json:"input_tokens"`
			CacheCreation int64  `json:"cache_creation_input_tokens"`
			CacheRead     int64  `json:"cache_read_input_tokens"`
			Speed         string `json:"speed"`
		} `json:"usage"`
	} `json:"message"`
}

// feedClaude folds one Claude record into l.
func (l *Live) feedClaude(line []byte) {
	if !bytes.Contains(line, []byte(`"assistant"`)) && !bytes.Contains(line, []byte(`"permission-mode"`)) &&
		!bytes.Contains(line, []byte(`"cost-state"`)) && !bytes.Contains(line, []byte("local-command-stdout")) {
		return // most lines state no setting: skip the second decode
	}
	var r claudeSettings
	if json.Unmarshal(line, &r) != nil || r.IsSidechain {
		return
	}
	at, _ := time.Parse(time.RFC3339Nano, r.Timestamp)
	switch r.Type {
	case "permission-mode":
		if r.PermissionMode != "" {
			l.PermissionMode = r.PermissionMode
			l.touch(at)
		}
	case "cost-state":
		if r.TotalCostUSD != nil {
			l.Cost, l.HasCost = *r.TotalCostUSD, true
			l.touch(at)
		}
	case "assistant":
		m := r.Message
		if m == nil || m.Model == "" || m.Model == "<synthetic>" {
			return
		}
		l.Model = m.Model
		if e := r.Effort; e != "" {
			l.Effort = e
		} else if r.PerTurnEffort != "" {
			l.Effort = r.PerTurnEffort
		}
		if u := m.Usage; u != nil {
			switch u.Speed {
			case "fast":
				l.Fast = "on"
			case "standard":
				l.Fast = "off"
			}
			if n := u.Input + u.CacheCreation + u.CacheRead; n > 0 {
				l.ContextTokens = n
			}
		}
		l.touch(at)
	case "user", "system":
		raw := r.Content
		if r.Message != nil {
			raw = r.Message.Content
		}
		if l.localCommand(contentText(raw)) {
			l.touch(at)
		}
	}
}

var (
	setModelReply  = regexp.MustCompile("^Set model to (?:`([^`]+)`|([^\\n(]+?))(?: \\(|\\s+and |$|\\n)")
	setEffortReply = regexp.MustCompile(`^Effort level set to (\S+)`)
)

// localCommand reads a slash command's reply into l, reporting whether it
// was one that states a setting.
func (l *Live) localCommand(text string) bool {
	text, ok := strings.CutPrefix(strings.TrimSpace(text), "<local-command-stdout>")
	if !ok {
		return false
	}
	text = strings.TrimSuffix(text, "</local-command-stdout>")
	switch {
	case setModelReply.MatchString(text):
		m := setModelReply.FindStringSubmatch(text)
		name := strings.TrimSpace(m[1] + m[2])
		l.Model = strings.TrimSuffix(name, " (default)")
		return true
	case setEffortReply.MatchString(text):
		l.Effort = setEffortReply.FindStringSubmatch(text)[1]
		return true
	case strings.HasPrefix(text, "Fast mode ON"):
		l.Fast = "on"
		return true
	case strings.HasPrefix(text, "Fast mode OFF"):
		l.Fast = "off"
		return true
	}
	return false
}

// ompSettings is the part of an omp entry the settings read.
type ompSettings struct {
	Type          string          `json:"type"`
	Timestamp     string          `json:"timestamp"`
	Model         string          `json:"model"`
	ThinkingLevel *string         `json:"thinkingLevel"`
	Configured    *string         `json:"configured"`
	ServiceTier   json.RawMessage `json:"serviceTier"`
	Mode          string          `json:"mode"`
	Usage         *ompUsage       `json:"usage"`
	Message       *struct {
		Role     string    `json:"role"`
		Model    string    `json:"model"`
		Provider string    `json:"provider"`
		Usage    *ompUsage `json:"usage"`
	} `json:"message"`
}

type ompUsage struct {
	Input      int64 `json:"input"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Cost       *struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

// feedOmp folds one omp entry into l. Every entry counts, an abandoned
// branch's too: a setting changed there was changed in the session.
func (l *Live) feedOmp(line []byte) {
	var e ompSettings
	if json.Unmarshal(line, &e) != nil {
		return
	}
	at, _ := time.Parse(time.RFC3339Nano, e.Timestamp)
	switch e.Type {
	case "model_change":
		if e.Model != "" {
			l.Model = e.Model
			l.touch(at)
		}
	case "thinking_level_change":
		switch {
		case e.Configured != nil && *e.Configured != "":
			l.Effort = *e.Configured
		case e.ThinkingLevel != nil && *e.ThinkingLevel != "":
			l.Effort = *e.ThinkingLevel
		default:
			l.Effort = "off"
		}
		l.touch(at)
	case "service_tier_change":
		if t := bytes.TrimSpace(e.ServiceTier); len(t) == 0 || string(t) == "null" || string(t) == "{}" {
			l.Fast = "off"
		} else {
			l.Fast = "on"
		}
		l.touch(at)
	case "mode_change":
		l.Mode = e.Mode
		if l.Mode == "none" {
			l.Mode = ""
		}
		l.touch(at)
	case "model_usage":
		if e.Usage != nil && e.Usage.Cost != nil {
			l.Cost += e.Usage.Cost.Total
			l.HasCost = true
		}
	case "message":
		m := e.Message
		if m == nil || m.Role != "assistant" {
			return
		}
		if m.Model != "" {
			l.Model = m.Model
			if m.Provider != "" && !strings.Contains(m.Model, "/") {
				l.Model = m.Provider + "/" + m.Model
			}
		}
		if u := m.Usage; u != nil {
			if n := u.Input + u.CacheRead + u.CacheWrite; n > 0 {
				l.ContextTokens = n
			}
			if u.Cost != nil {
				l.Cost += u.Cost.Total
				l.HasCost = true
			}
		}
		l.touch(at)
	}
}

// codexRecord is the part of a Codex rollout line the settings read.
type codexRecord struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Payload   struct {
		Type           string `json:"type"`
		Model          string `json:"model"`
		Effort         string `json:"effort"`
		ApprovalPolicy string `json:"approval_policy"`
		SandboxPolicy  *struct {
			Type string `json:"type"`
		} `json:"sandbox_policy"`
		ServiceTier       *string `json:"service_tier"`
		CollaborationMode *struct {
			Mode string `json:"mode"`
		} `json:"collaboration_mode"`
		Info *struct {
			Last *struct {
				Total int64 `json:"total_tokens"`
			} `json:"last_token_usage"`
			Window int64 `json:"model_context_window"`
		} `json:"info"`
	} `json:"payload"`
}

// FeedCodex folds one Codex rollout line into l.
func (l *Live) FeedCodex(line []byte) {
	var r codexRecord
	if json.Unmarshal(bytes.TrimSpace(line), &r) != nil {
		return
	}
	at, _ := time.Parse(time.RFC3339Nano, r.Timestamp)
	p := r.Payload
	switch {
	case r.Type == "turn_context":
		if p.Model != "" {
			l.Model = p.Model
		}
		if p.Effort != "" {
			l.Effort = p.Effort
		}
		if p.ApprovalPolicy != "" {
			l.PermissionMode = p.ApprovalPolicy
			if p.SandboxPolicy != nil && p.SandboxPolicy.Type != "" {
				l.PermissionMode += "/" + p.SandboxPolicy.Type
			}
		}
		switch {
		case p.ServiceTier == nil, *p.ServiceTier == "", *p.ServiceTier == "default":
			l.Fast = "off"
		default:
			l.Fast = "on"
		}
		if c := p.CollaborationMode; c != nil {
			l.Mode = c.Mode
			if l.Mode == "default" {
				l.Mode = ""
			}
		}
		l.touch(at)
	case p.Type == "token_count" && p.Info != nil:
		if p.Info.Last != nil && p.Info.Last.Total > 0 {
			l.ContextTokens = p.Info.Last.Total
		}
		if p.Info.Window > 0 {
			l.ContextWindow = p.Info.Window
		}
		l.touch(at)
	}
}

// Settings is what the log read so far says of the session's settings.
func (p *ClaudeParser) Settings() Live { return p.live }

// Settings is what the log read so far says of the session's settings.
func (p *OmpParser) Settings() Live { return p.live }

// Settings is the session's settings as the log read so far says them.
func (l *ClaudeLog) Settings() Live {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.parser.Settings()
}

// Settings is the session's settings as the log read so far says them.
func (l *OmpLog) Settings() Live {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.parser.Settings()
}

// Settings is the current session's settings: the newest chained log's
// that states any ("" fields when none does). A /clear starts a log that
// has stated nothing yet; the session's settings carry over, so the
// fields it lacks come from the log before it.
func (c *LogChain) Settings() Live {
	c.mu.Lock()
	logs := append([]Log(nil), c.logs...)
	c.mu.Unlock()
	var out Live
	for _, l := range logs {
		s, ok := l.(interface{ Settings() Live })
		if !ok {
			continue
		}
		live := s.Settings()
		if live.Zero() {
			continue
		}
		prev := out
		out = live
		// Fields the newer log has not stated yet carry over; usage and
		// cost are the session's own and do not.
		if out.Model == "" {
			out.Model = prev.Model
		}
		if out.Effort == "" {
			out.Effort = prev.Effort
		}
		if out.Fast == "" {
			out.Fast = prev.Fast
		}
		if out.PermissionMode == "" {
			out.PermissionMode = prev.PermissionMode
		}
	}
	return out
}

// codexTail is how much of a rollout's end ReadCodexSettings reads.
const codexTail = 512 << 10

// ReadCodexSettings is a Codex session's settings from the end of its
// rollout at path (its last codexTail bytes); the zero Live when it
// cannot be read.
func ReadCodexSettings(path string) Live {
	var l Live
	f, err := os.Open(path)
	if err != nil {
		return l
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return l
	}
	start := max(0, info.Size()-codexTail)
	data, err := io.ReadAll(io.NewSectionReader(f, start, info.Size()-start))
	if err != nil {
		return l
	}
	if start > 0 {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		l.FeedCodex(line)
	}
	return l
}

// CodexSessions is Codex's sessions directory: $CODEX_HOME/sessions, else
// ~/.codex/sessions.
func CodexSessions() string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return filepath.Join(dir, "sessions")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "sessions")
}

// FindCodexLog is the rollout of session under sessions
// (YYYY/MM/DD/rollout-<time>-<session>.jsonl), the newest when several.
func FindCodexLog(sessions, session string) (string, error) {
	if sessions == "" || session == "" || strings.ContainsAny(session, `/\*?[`) || strings.Contains(session, "..") {
		return "", ErrNoLog
	}
	matches, _ := filepath.Glob(filepath.Join(sessions, "*", "*", "*", "rollout-*-"+session+".jsonl"))
	if len(matches) == 0 {
		return "", ErrNoLog
	}
	return matches[len(matches)-1], nil
}
