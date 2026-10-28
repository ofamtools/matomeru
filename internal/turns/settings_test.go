package turns

import (
	"os"
	"path/filepath"
	"testing"
)

// Fixture lines in the shapes of real logs (Claude Code 2.1.312, omp
// 18.13.0, Codex 0.162.1), trimmed to the fields that matter.
const (
	claudeAssistant = `{"type":"assistant","uuid":"a1","timestamp":"2026-10-28T10:00:01Z","sessionId":"s1","effort":"medium","perTurnEffort":"medium","message":{"model":"claude-opus-5-5","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"cache_creation_input_tokens":3094,"cache_read_input_tokens":435702,"output_tokens":476,"speed":"standard"}}}`
	claudeFast      = `{"type":"assistant","uuid":"a2","timestamp":"2026-10-28T10:05:01Z","sessionId":"s1","effort":"high","message":{"model":"claude-opus-5-5","content":[{"type":"text","text":"quick"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"cache_creation_input_tokens":0,"cache_read_input_tokens":440000,"output_tokens":5,"speed":"fast"}}}`
	claudeSynthetic = `{"type":"assistant","uuid":"a3","timestamp":"2026-10-28T10:06:01Z","sessionId":"s1","effort":null,"message":{"model":"<synthetic>","content":[{"type":"text","text":"API Error"}],"usage":{"input_tokens":0,"output_tokens":0}}}`
	claudeSidechain = `{"type":"assistant","uuid":"a4","isSidechain":true,"timestamp":"2026-10-28T10:07:01Z","effort":"low","message":{"model":"claude-haiku-5-5","content":[],"usage":{"input_tokens":9,"speed":"standard"}}}`
	claudePerm      = `{"type":"permission-mode","permissionMode":"bypassPermissions","sessionId":"s1"}`
	claudePlan      = `{"type":"permission-mode","permissionMode":"plan","sessionId":"s1"}`
	claudeCost      = `{"type":"cost-state","sessionId":"s1","totalCostUSD":46.7789,"modelUsage":{}}`
	claudeSetModel  = "{\"type\":\"user\",\"uuid\":\"u9\",\"timestamp\":\"2026-10-28T10:08:00Z\",\"message\":{\"role\":\"user\",\"content\":\"<local-command-stdout>Set model to `Fable 5.1` and saved as your default for new sessions</local-command-stdout>\"}}"
	claudeSetEffort = `{"type":"system","subtype":"local_command","timestamp":"2026-10-28T10:09:00Z","content":"<local-command-stdout>Effort level set to xhigh</local-command-stdout>"}`
	claudeFastOff   = `{"type":"system","subtype":"local_command","timestamp":"2026-10-28T10:10:00Z","content":"<local-command-stdout>Fast mode OFF</local-command-stdout>"}`
)

func feedClaudeLines(lines ...string) Live {
	var p ClaudeParser
	for _, l := range lines {
		p.Feed([]byte(l))
	}
	return p.Settings()
}

func TestClaudeSettingsFromTheTranscript(t *testing.T) {
	got := feedClaudeLines(claudePerm, claudeAssistant)
	if got.Model != "claude-opus-5-5" || got.Effort != "medium" || got.Fast != "off" || got.PermissionMode != "bypassPermissions" {
		t.Fatalf("settings %+v", got)
	}
	if got.ContextTokens != 2+3094+435702 || got.HasCost || got.ContextWindow != 0 {
		t.Fatalf("usage %+v", got)
	}
	// The newest reply wins; the harness's own synthetic reply and a
	// subagent's do not move the session's model, effort or context.
	got = feedClaudeLines(claudePerm, claudeAssistant, claudeFast, claudeSynthetic, claudeSidechain, claudePlan, claudeCost)
	if got.Model != "claude-opus-5-5" || got.Effort != "high" || got.Fast != "on" || got.PermissionMode != "plan" || got.ContextTokens != 440010 {
		t.Fatalf("newest %+v", got)
	}
	if !got.HasCost || got.Cost != 46.7789 {
		t.Fatalf("cost %+v", got)
	}
}

// A slash command's reply states the new value before the next answer.
func TestClaudeSlashCommandRepliesSetTheValue(t *testing.T) {
	got := feedClaudeLines(claudeFast, claudeSetModel, claudeSetEffort, claudeFastOff)
	if got.Model != "Fable 5.1" || got.Effort != "xhigh" || got.Fast != "off" {
		t.Fatalf("after commands %+v", got)
	}
	for text, want := range map[string]string{
		"<local-command-stdout>Set model to Default (Opus 5.5 · Best for everyday, complex tasks)</local-command-stdout>": "Default",
		"<local-command-stdout>Set model to claude-fable-5-1[1m] (claude-fable-5-1)\nSet workflows to on":                 "claude-fable-5-1[1m]",
		"<local-command-stdout>Set model to `Fable 5.1 (default)` and saved as your default</local-command-stdout>":       "Fable 5.1",
	} {
		var l Live
		if !l.localCommand(text) || l.Model != want {
			t.Errorf("%q: %q, want %q", text, l.Model, want)
		}
	}
	var l Live
	if l.localCommand("<local-command-stdout>Kept model as `Fable 5.1`</local-command-stdout>") || l.localCommand("Set model to x") {
		t.Fatal("not a setting reply")
	}
	// The reply is not a prompt: the turns are unchanged by it.
	var p ClaudeParser
	p.Feed([]byte(claudeSetModel))
	if len(p.Turns()) != 0 {
		t.Fatalf("a reply became a turn: %+v", p.Turns())
	}
}

const (
	ompModel    = `{"type":"model_change","id":"m1","parentId":null,"timestamp":"2026-10-28T07:18:46.299Z","model":"openai-codex/gpt-6.1-sol"}`
	ompThinking = `{"type":"thinking_level_change","id":"t1","parentId":"m1","timestamp":"2026-10-28T07:18:46.299Z","thinkingLevel":"medium","configured":null}`
	ompAuto     = `{"type":"thinking_level_change","id":"t2","parentId":"t1","timestamp":"2026-10-28T07:19:00Z","thinkingLevel":"high","configured":"auto"}`
	ompFast     = `{"type":"service_tier_change","id":"f1","parentId":"t2","timestamp":"2026-10-28T07:20:00Z","serviceTier":{"openai":"priority"}}`
	ompFastOff  = `{"type":"service_tier_change","id":"f2","parentId":"f1","timestamp":"2026-10-28T07:21:00Z","serviceTier":null}`
	ompPlan     = `{"type":"mode_change","id":"p1","parentId":"f2","timestamp":"2026-10-28T07:22:00Z","mode":"plan"}`
	ompReply    = `{"type":"message","id":"r1","parentId":"p1","timestamp":"2026-10-28T07:23:00Z","message":{"role":"assistant","model":"gpt-6.1-sol","provider":"openai-codex","content":[{"type":"text","text":"ok"}],"stopReason":"stop","usage":{"input":367,"output":278,"cacheRead":276736,"cacheWrite":0,"cost":{"total":0.0311876}}}}`
	ompSide     = `{"type":"model_usage","id":"u1","parentId":"r1","timestamp":"2026-10-28T07:24:00Z","model":"anthropic.claude-haiku-5-5","usage":{"input":10,"cost":{"total":0.0001}}}`
)

func TestOmpSettingsFromTheLog(t *testing.T) {
	var p OmpParser
	for _, l := range []string{ompModel, ompThinking} {
		p.Feed([]byte(l))
	}
	if got := p.Settings(); got.Model != "openai-codex/gpt-6.1-sol" || got.Effort != "medium" || got.Fast != "" {
		t.Fatalf("start %+v", got)
	}
	for _, l := range []string{ompAuto, ompFast} {
		p.Feed([]byte(l))
	}
	if got := p.Settings(); got.Effort != "auto" || got.Fast != "on" {
		t.Fatalf("auto, fast %+v", got)
	}
	for _, l := range []string{ompFastOff, ompPlan, ompReply, ompSide} {
		p.Feed([]byte(l))
	}
	got := p.Settings()
	if got.Fast != "off" || got.Mode != "plan" || got.Model != "openai-codex/gpt-6.1-sol" || got.ContextTokens != 367+276736 {
		t.Fatalf("end %+v", got)
	}
	if !got.HasCost || got.Cost < 0.03128 || got.Cost > 0.03129 {
		t.Fatalf("cost %+v", got)
	}
}

const (
	codexContext = `{"timestamp":"2026-10-28T06:40:00Z","type":"turn_context","payload":{"model":"gpt-6.1-sol","effort":"low","approval_policy":"never","sandbox_policy":{"type":"danger-full-access"},"service_tier":null,"collaboration_mode":{"mode":"default"}}}`
	codexTokens  = `{"timestamp":"2026-10-28T06:40:05Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":140986},"last_token_usage":{"input_tokens":48502,"output_tokens":55,"total_tokens":48557},"model_context_window":475000}}}`
)

func TestCodexSettingsFromTheRollout(t *testing.T) {
	dir := t.TempDir()
	day := filepath.Join(dir, "2026", "10", "28")
	if err := os.MkdirAll(day, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(day, "rollout-2026-10-28T02-39-47-c1.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"id":"c1"}}`+"\n"+codexContext+"\n"+codexTokens+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	found, err := FindCodexLog(dir, "c1")
	if err != nil || found != path {
		t.Fatalf("find %q %v", found, err)
	}
	if _, err := FindCodexLog(dir, "../c1"); err == nil {
		t.Fatal("a path escaped")
	}
	got := ReadCodexSettings(path)
	if got.Model != "gpt-6.1-sol" || got.Effort != "low" || got.PermissionMode != "never/danger-full-access" || got.Fast != "off" || got.Mode != "" {
		t.Fatalf("codex %+v", got)
	}
	if got.ContextTokens != 48557 || got.ContextWindow != 475000 || got.HasCost {
		t.Fatalf("codex usage %+v", got)
	}
}

// A /clear's new log has stated nothing of its own yet: the session's
// settings carry over from the log before it, its usage does not.
func TestChainSettingsCarryOverAClear(t *testing.T) {
	dir := t.TempDir()
	old, cur := filepath.Join(dir, "s1.jsonl"), filepath.Join(dir, "s2.jsonl")
	if err := os.WriteFile(old, []byte(claudePerm+"\n"+claudeAssistant+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cur, []byte(claudeSetEffort+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var c LogChain
	c.Add("s1", old)
	c.Add("s2", cur)
	if _, err := c.Read(); err != nil {
		t.Fatal(err)
	}
	got := c.Settings()
	if got.Effort != "xhigh" || got.Model != "claude-opus-5-5" || got.PermissionMode != "bypassPermissions" || got.ContextTokens != 0 {
		t.Fatalf("chain %+v", got)
	}
}
