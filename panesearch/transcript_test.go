package panesearch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTranscriptFormatsAndMasking(t *testing.T) {
	dir := t.TempDir()

	t.Run("Claude JSONL", func(t *testing.T) {
		p := filepath.Join(dir, "claude.jsonl")
		body := strings.Join([]string{
			`{"type":"user","message":{"role":"user","content":"Fix the auth middleware with OPENAI_API_KEY=sk-proj-secret1234567890"}}`,
			`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Checked Authorization: Bearer supersecrettoken99"},{"type":"tool_use","name":"Edit","input":{"file_path":"app/auth.go"}}]}}`,
			`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"updated app/auth.go successfully"}]}}`,
		}, "\n") + "\n"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		lines, err := ReadTranscript(p, DefaultMaxLogBytes)
		if err != nil {
			t.Fatalf("ReadTranscript: %v", err)
		}
		joined := strings.Join(lines, "\n")
		for _, want := range []string{
			"user: Fix the auth middleware",
			"OPENAI_API_KEY=***",
			"Bearer ***",
			"tool: Edit: app/auth.go",
			"tool: updated app/auth.go successfully",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("Claude lines missing %q:\n%s", want, joined)
			}
		}
		for _, secret := range []string{"sk-proj-secret1234567890", "supersecrettoken99"} {
			if strings.Contains(joined, secret) {
				t.Errorf("Claude lines leaked secret %q:\n%s", secret, joined)
			}
		}
	})

	t.Run("Codex JSONL", func(t *testing.T) {
		p := filepath.Join(dir, "codex.jsonl")
		body := strings.Join([]string{
			`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Investigate flaky sqlite lock"}]}}`,
			`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Found busy_timeout mismatch in data/store.go"}]}}`,
			`{"type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{\"command\":\"go test ./data\"}"}}`,
			`{"type":"event_msg","payload":{"type":"agent_message","message":"All sqlite tests now pass"}}`,
		}, "\n") + "\n"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		lines, err := ReadTranscript(p, DefaultMaxLogBytes)
		if err != nil {
			t.Fatalf("ReadTranscript: %v", err)
		}
		joined := strings.Join(lines, "\n")
		for _, want := range []string{
			"user: Investigate flaky sqlite lock",
			"assistant: Found busy_timeout mismatch in data/store.go",
			"tool: shell:",
			"assistant: All sqlite tests now pass",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("Codex lines missing %q:\n%s", want, joined)
			}
		}
	})

	t.Run("Gemini JSON", func(t *testing.T) {
		p := filepath.Join(dir, "gemini.json")
		body := `{
			"sessionId": "gem-42",
			"messages": [
				{"type": "user", "content": "Explain the TUI focus ring"},
				{"type": "gemini", "content": [{"text": "Focus moves via Core Engine steps"}], "toolCalls": [{"name": "read_file", "args": {"path": "app/focus.go"}}]}
			]
		}`
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		lines, err := ReadTranscript(p, DefaultMaxLogBytes)
		if err != nil {
			t.Fatalf("ReadTranscript: %v", err)
		}
		joined := strings.Join(lines, "\n")
		for _, want := range []string{
			"user: Explain the TUI focus ring",
			"assistant: Focus moves via Core Engine steps",
			"tool: read_file: app/focus.go",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("Gemini lines missing %q:\n%s", want, joined)
			}
		}
	})

	t.Run("Generic JSONL fallback", func(t *testing.T) {
		p := filepath.Join(dir, "generic.jsonl")
		body := `{"prompt":"Run migration","response":"Migration completed cleanly"}` + "\n"
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		lines, err := ReadTranscript(p, DefaultMaxLogBytes)
		if err != nil {
			t.Fatalf("ReadTranscript: %v", err)
		}
		joined := strings.Join(lines, "\n")
		if !strings.Contains(joined, "user: Run migration") || !strings.Contains(joined, "assistant: Migration completed cleanly") {
			t.Fatalf("generic lines = %v", lines)
		}
	})
}

func TestTranscriptTailCapCacheAndRegularFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "big.jsonl")

	var sb strings.Builder
	sb.WriteString(`{"type":"user","message":{"role":"user","content":"ANCIENT_HEAD_MARKER"}}` + "\n")
	for range 80 {
		sb.WriteString(`{"type":"assistant","message":{"role":"assistant","content":"padding line with repeated filler text to grow file past tail cap"}}` + "\n")
	}
	sb.WriteString(`{"type":"assistant","message":{"role":"assistant","content":"RECENT_TAIL_MARKER"}}` + "\n")
	if err := os.WriteFile(p, []byte(sb.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	cache := NewCache()
	lines, err := cache.ReadTranscript(p, 512)
	if err != nil {
		t.Fatalf("ReadTranscript tail cap: %v", err)
	}
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "ANCIENT_HEAD_MARKER") {
		t.Fatalf("tail cap failed to skip head:\n%s", joined)
	}
	if !strings.Contains(joined, "RECENT_TAIL_MARKER") {
		t.Fatalf("tail cap missed recent tail:\n%s", joined)
	}

	// Cache hit returns identical slice header without re-reading.
	lines2, err := cache.ReadTranscript(p, 512)
	if err != nil {
		t.Fatalf("cached ReadTranscript: %v", err)
	}
	if len(lines2) != len(lines) || (len(lines) > 0 && &lines2[0] != &lines[0]) {
		t.Fatalf("expected cache hit to reuse cached slice")
	}

	// Modifying file invalidates cache entry.
	newTime := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(p, []byte(`{"type":"user","message":{"role":"user","content":"UPDATED_MARKER"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(p, newTime, newTime)
	lines3, err := cache.ReadTranscript(p, 512)
	if err != nil {
		t.Fatalf("updated ReadTranscript: %v", err)
	}
	if !strings.Contains(strings.Join(lines3, "\n"), "UPDATED_MARKER") {
		t.Fatalf("cache did not invalidate on file update: %v", lines3)
	}

	// Non-regular files (directory, symlink) are rejected by OpenRegular.
	if _, _, err := OpenRegular(dir); err == nil {
		t.Fatal("OpenRegular accepted directory")
	}
	link := filepath.Join(dir, "link.jsonl")
	if err := os.Symlink(p, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenRegular(link); err == nil {
		t.Fatal("OpenRegular accepted symlink")
	}
}

func TestResolveTranscriptPathDiscovery(t *testing.T) {
	home := t.TempDir()
	cwd := "/Users/dev/proj/demo"
	claudeDir := filepath.Join(home, ".claude", "projects", "-Users-dev-proj-demo")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	claudeFile := filepath.Join(claudeDir, "sess-123.jsonl")
	if err := os.WriteFile(claudeFile, []byte(`{"role":"user","content":"hello"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if got := ResolveTranscriptPath("claude", cwd, "sess-123", "", home); got != claudeFile {
		t.Fatalf("claude discovery = %q, want %q", got, claudeFile)
	}

	// Explicit path takes precedence.
	explicit := filepath.Join(home, "custom.jsonl")
	if err := os.WriteFile(explicit, []byte(`{"role":"user","content":"explicit"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ResolveTranscriptPath("claude", cwd, "sess-123", explicit, home); got != explicit {
		t.Fatalf("explicit path = %q, want %q", got, explicit)
	}

	// Codex discovery by session ID.
	codexDir := filepath.Join(home, ".codex", "sessions", "2026", "11", "24")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	codexFile := filepath.Join(codexDir, "rollout-codex-abc.jsonl")
	if err := os.WriteFile(codexFile, []byte(`{"type":"event_msg","payload":{"type":"user_message","message":"hi"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ResolveTranscriptPath("codex", cwd, "codex-abc", "", home); got != codexFile {
		t.Fatalf("codex discovery = %q, want %q", got, codexFile)
	}

	// Gemini discovery by session ID.
	gemDir := filepath.Join(home, ".gemini", "tmp", "hash1", "chats")
	if err := os.MkdirAll(gemDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gemFile := filepath.Join(gemDir, "session-gem99.json")
	if err := os.WriteFile(gemFile, []byte(`{"sessionId":"gem99","messages":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ResolveTranscriptPath("gemini", cwd, "gem99", "", home); got != gemFile {
		t.Fatalf("gemini discovery = %q, want %q", got, gemFile)
	}
}
