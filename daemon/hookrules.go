package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Callback bus events.
const (
	// EventAgentDone is emitted when a pane moves from an active state
	// (running, needs_you) into done or idle.
	EventAgentDone = "agent.done"
	// EventAgentNeedsInput is emitted when a pane enters needs_you (an
	// approval or question waits on a person).
	EventAgentNeedsInput = "agent.needs_input"
	// EventAgentBlocked is emitted when a pane sends a msgKind=blocked
	// mailbox message.
	EventAgentBlocked = "agent.blocked"
	// EventMailReceived is emitted for every mailbox message; its PaneID is
	// the recipient pane.
	EventMailReceived = "mail.received"
)

// AgentEvents lists every event the callback bus emits.
var AgentEvents = []string{EventAgentDone, EventAgentNeedsInput, EventAgentBlocked, EventMailReceived}

// HooksFileName is the declarative callback rules file inside the config dir.
const HooksFileName = "hooks.json"

// DefaultHookTimeout bounds one callback delivery attempt.
const DefaultHookTimeout = 15 * time.Second

// AgentDoneEvent describes one callback bus event: an edge-triggered agent
// completion (agent.done), a needs_you edge (agent.needs_input), or a mailbox
// message (mail.received, agent.blocked) whose Message* fields are set.
type AgentDoneEvent struct {
	Event          string    `json:"event"`
	PaneID         string    `json:"paneId"`
	Harness        string    `json:"harness,omitempty"`
	PrevState      string    `json:"prevState,omitempty"`
	State          string    `json:"state"`
	Summary        string    `json:"summary,omitempty"`
	Response       string    `json:"response,omitempty"`
	SessionID      string    `json:"sessionId,omitempty"`
	TranscriptPath string    `json:"transcriptPath,omitempty"`
	TaskID         string    `json:"taskId,omitempty"`
	LaneID         string    `json:"laneId,omitempty"`
	ProjectID      string    `json:"projectId,omitempty"`
	MessageID      string    `json:"messageId,omitempty"`
	FromPane       string    `json:"fromPane,omitempty"`
	ToPane         string    `json:"toPane,omitempty"`
	MsgKind        string    `json:"msgKind,omitempty"`
	Subject        string    `json:"subject,omitempty"`
	Thread         string    `json:"thread,omitempty"`
	At             time.Time `json:"at"`
}

// HookMatch selects events by field; empty fields match anything.
type HookMatch struct {
	Harness string `json:"harness,omitempty"`
	Pane    string `json:"pane,omitempty"`
	Lane    string `json:"lane,omitempty"`
	Project string `json:"project,omitempty"`
}

// HookRun describes what a callback does: run a command (event JSON on
// stdin), POST the event JSON to a webhook, and/or record a mailbox message
// to NotifyPane and wake it once it is idle.
type HookRun struct {
	Command    []string          `json:"command,omitempty"`
	Webhook    string            `json:"webhook,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	TimeoutSec int               `json:"timeoutSec,omitempty"`
	NotifyPane string            `json:"notifyPane,omitempty"`
}

// HookRule is one declarative callback rule from hooks.json or registered in
// daemon memory (pane.spawn onDone, mtm hook add --memory).
type HookRule struct {
	ID     string    `json:"id"`
	On     string    `json:"on,omitempty"`
	Once   bool      `json:"once,omitempty"`
	Match  HookMatch `json:"match,omitzero"`
	Run    HookRun   `json:"run"`
	Source string    `json:"source,omitempty"`
}

// Hook rule sources reported by list operations.
const (
	HookSourceFile   = "file"
	HookSourceMemory = "memory"
)

// HooksFile is the on-disk shape of hooks.json.
type HooksFile struct {
	Rules []HookRule `json:"rules"`
}

// HooksPath returns <configDir>/hooks.json.
func HooksPath(configDir string) string {
	if configDir == "" {
		return ""
	}
	return filepath.Join(configDir, HooksFileName)
}

// Validate reports whether rule is complete enough to deliver.
func (r HookRule) Validate() error {
	if strings.TrimSpace(r.ID) == "" {
		return errors.New("hook rule: id is required")
	}
	if len(r.Run.Command) == 0 && strings.TrimSpace(r.Run.Webhook) == "" && strings.TrimSpace(r.Run.NotifyPane) == "" {
		return fmt.Errorf("hook rule %q: run.command, run.webhook or run.notifyPane is required", r.ID)
	}
	if r.Run.TimeoutSec < 0 {
		return fmt.Errorf("hook rule %q: run.timeoutSec must be >= 0", r.ID)
	}
	return nil
}

// Timeout returns the per-attempt delivery timeout.
func (r HookRule) Timeout() time.Duration {
	if r.Run.TimeoutSec > 0 {
		return time.Duration(r.Run.TimeoutSec) * time.Second
	}
	return DefaultHookTimeout
}

// Matches reports whether rule fires for ev. On is one event, a
// comma-separated list, or "*" for every event; an empty On means agent.done
// (the only event older rules knew).
func (r HookRule) Matches(ev AgentDoneEvent) bool {
	if !matchesOn(r.On, ev.Event) {
		return false
	}
	return matchField(r.Match.Harness, ev.Harness) &&
		matchField(r.Match.Pane, ev.PaneID) &&
		matchField(r.Match.Lane, ev.LaneID) &&
		matchField(r.Match.Project, ev.ProjectID)
}

func matchesOn(on, event string) bool {
	on = strings.TrimSpace(on)
	if on == "" {
		on = EventAgentDone
	}
	for _, want := range strings.Split(on, ",") {
		if want = strings.TrimSpace(want); want == "*" || want == event {
			return true
		}
	}
	return false
}

func matchField(want, got string) bool {
	want = strings.TrimSpace(want)
	return want == "" || want == "*" || strings.EqualFold(want, got)
}

// hookFile serializes reads and writes of one hooks.json path.
type hookFile struct {
	mu   sync.Mutex
	path string
}

// LoadHookRules reads rules from path. A missing file yields no rules.
func LoadHookRules(path string) ([]HookRule, error) {
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	var f HooksFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for i := range f.Rules {
		f.Rules[i].Source = HookSourceFile
	}
	return f.Rules, nil
}

// SaveHookRules writes rules to path atomically.
func SaveHookRules(path string, rules []HookRule) error {
	if path == "" {
		return errors.New("hooks: no config dir")
	}
	out := HooksFile{Rules: make([]HookRule, len(rules))}
	for i, r := range rules {
		r.Source = ""
		out.Rules[i] = r
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// AddHookRule validates rule and upserts it (by ID) into the rules file at path.
func AddHookRule(path string, rule HookRule) error {
	if err := rule.Validate(); err != nil {
		return err
	}
	rules, err := LoadHookRules(path)
	if err != nil {
		return err
	}
	replaced := false
	for i := range rules {
		if rules[i].ID == rule.ID {
			rules[i] = rule
			replaced = true
		}
	}
	if !replaced {
		rules = append(rules, rule)
	}
	return SaveHookRules(path, rules)
}

// RemoveHookRule deletes rule id from the rules file at path, reporting whether it existed.
func RemoveHookRule(path, id string) (bool, error) {
	rules, err := LoadHookRules(path)
	if err != nil {
		return false, err
	}
	kept := rules[:0]
	found := false
	for _, r := range rules {
		if r.ID == id {
			found = true
			continue
		}
		kept = append(kept, r)
	}
	if !found {
		return false, nil
	}
	return true, SaveHookRules(path, kept)
}

func (f *hookFile) load() []HookRule {
	f.mu.Lock()
	defer f.mu.Unlock()
	rules, _ := LoadHookRules(f.path)
	return rules
}

// consume removes once-rules ids from the file after they fired.
func (f *hookFile) consume(ids []string) {
	if len(ids) == 0 {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range ids {
		_, _ = RemoveHookRule(f.path, id)
	}
}
