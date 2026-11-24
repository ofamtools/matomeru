package panesearch

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// DefaultMaxLogBytes is the default tail-read byte cap for external agent
// session transcripts (2 MiB).
const DefaultMaxLogBytes int64 = 2 << 20

const maxLineLen = 2048

var (
	secretBearerRe = regexp.MustCompile(`(?i)\b(Bearer\s+)[A-Za-z0-9._~+/=-]{8,}`)
	secretTokenRe  = regexp.MustCompile(`\b(?:sk-(?:proj-|ant-)?[A-Za-z0-9_-]{12,}|gh[pousr]_[A-Za-z0-9_]{16,}|github_pat_[A-Za-z0-9_]{20,}|xox[baprs]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{20,}|AKIA[0-9A-Z]{16})\b`)
	secretAssignRe = regexp.MustCompile(`\b((?:[A-Z][A-Z0-9_]*)?(?:KEY|TOKEN|SECRET|PASSWORD|PASSWD|AUTH|CREDENTIAL)[A-Z0-9_]*)=([^\s'",;]+)`)
	secretJSONRe   = regexp.MustCompile(`(?i)("(?:api_?key|access_?token|auth_?token|refresh_?token|secret|password|client_secret)"\s*:\s*")([^"]{8,})(")`)
)

// MaskSecrets redacts common tokens, API keys, Bearer headers, and secret
// assignments from extracted text lines.
func MaskSecrets(s string) string {
	if s == "" {
		return ""
	}
	s = secretBearerRe.ReplaceAllString(s, "${1}***")
	s = secretTokenRe.ReplaceAllString(s, "***")
	s = secretAssignRe.ReplaceAllString(s, "${1}=***")
	s = secretJSONRe.ReplaceAllString(s, "${1}***${3}")
	return s
}

// OpenRegular opens path only if it is a regular file (never a FIFO, socket,
// device, directory, or leaf symlink), verifying the opened descriptor matches
// the lstat metadata.
func OpenRegular(path string) (*os.File, os.FileInfo, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil, errors.New("panesearch: empty transcript path")
	}
	linfo, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !linfo.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("panesearch: %s is not a regular file", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	finfo, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if !finfo.Mode().IsRegular() || !os.SameFile(linfo, finfo) {
		_ = f.Close()
		return nil, nil, fmt.Errorf("panesearch: %s changed or is not a regular file", path)
	}
	return f, finfo, nil
}

type cachedTranscript struct {
	modTime  int64
	size     int64
	maxBytes int64
	lines    []string
}

type cachedFrame struct {
	revision uint64
	lines    []string
}

// Cache holds bounded in-memory caches for parsed session transcripts (keyed by
// path, modTime, size, and maxBytes) and normalized live terminal scrollback
// frames (keyed by paneID and revision) so repeated queries do zero disk I/O.
type Cache struct {
	mu          sync.RWMutex
	transcripts map[string]cachedTranscript
	frames      map[string]cachedFrame
}

// NewCache creates an empty Cache.
func NewCache() *Cache {
	return &Cache{
		transcripts: map[string]cachedTranscript{},
		frames:      map[string]cachedFrame{},
	}
}

// ReadTranscript reads and extracts searchable text lines from path with a
// bounded tail cap, returning cached lines when (path, modTime, size, maxBytes)
// are unchanged.
func (c *Cache) ReadTranscript(path string, maxBytes int64) ([]string, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxLogBytes
	}
	clean := filepath.Clean(path)
	linfo, err := os.Lstat(clean)
	if err != nil {
		return nil, err
	}
	if !linfo.Mode().IsRegular() {
		return nil, fmt.Errorf("panesearch: %s is not a regular file", clean)
	}
	modNano := linfo.ModTime().UnixNano()
	size := linfo.Size()

	if c != nil {
		c.mu.RLock()
		if entry, ok := c.transcripts[clean]; ok && entry.modTime == modNano && entry.size == size && entry.maxBytes == maxBytes {
			lines := entry.lines
			c.mu.RUnlock()
			return lines, nil
		}
		c.mu.RUnlock()
	}

	lines, finfo, err := readTranscriptFile(clean, maxBytes)
	if err != nil {
		return nil, err
	}
	if c != nil {
		c.mu.Lock()
		c.transcripts[clean] = cachedTranscript{
			modTime:  finfo.ModTime().UnixNano(),
			size:     finfo.Size(),
			maxBytes: maxBytes,
			lines:    lines,
		}
		c.mu.Unlock()
	}
	return lines, nil
}

// FrameLines returns cached sanitized scrollback lines for (paneID, revision),
// populating the cache when revision changed or revision == 0.
func (c *Cache) FrameLines(paneID string, rev uint64, raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	if c != nil && rev > 0 {
		c.mu.RLock()
		if entry, ok := c.frames[paneID]; ok && entry.revision == rev {
			lines := entry.lines
			c.mu.RUnlock()
			return lines
		}
		c.mu.RUnlock()
	}
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		clean := strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(clean) == "" {
			continue
		}
		out = append(out, MaskSecrets(clean))
	}
	if c != nil && rev > 0 {
		c.mu.Lock()
		c.frames[paneID] = cachedFrame{revision: rev, lines: out}
		c.mu.Unlock()
	}
	return out
}

// ReadTranscript reads a transcript file without caching.
func ReadTranscript(path string, maxBytes int64) ([]string, error) {
	lines, _, err := readTranscriptFile(path, maxBytes)
	return lines, err
}

func readTranscriptFile(path string, maxBytes int64) ([]string, os.FileInfo, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxLogBytes
	}
	f, info, err := OpenRegular(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	size := info.Size()
	if size == 0 {
		return nil, info, nil
	}

	truncated := false
	if size > maxBytes {
		if _, err := f.Seek(size-maxBytes, io.SeekStart); err != nil {
			return nil, info, err
		}
		truncated = true
	}

	data, err := io.ReadAll(io.LimitReader(f, maxBytes))
	if err != nil {
		return nil, info, err
	}

	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".json" && !truncated {
		if lines := parseGeminiJSON(data); len(lines) > 0 {
			return lines, info, nil
		}
	}

	if truncated {
		if idx := bytes.IndexByte(data, '\n'); idx >= 0 && idx+1 < len(data) {
			data = data[idx+1:]
		}
	}

	return parseJSONLBytes(data), info, nil
}

// ResolveTranscriptPath resolves a pane's transcript path: explicitPath first
// (when pointing to an existing regular file), then optional safe fallback
// discovery by (harness, cwd, sessionID) under homeDir (or os.UserHomeDir()).
func ResolveTranscriptPath(harness, cwd, sessionID, explicitPath, homeDir string) string {
	home := strings.TrimSpace(homeDir)
	if home == "" {
		home, _ = os.UserHomeDir()
	}

	if p := expandTilde(strings.TrimSpace(explicitPath), home); p != "" {
		if isRegularFile(p) {
			return p
		}
	}

	if home == "" {
		return ""
	}
	h := strings.ToLower(strings.TrimSpace(harness))
	sid := strings.TrimSpace(sessionID)
	cleanCwd := strings.TrimSpace(cwd)
	if cleanCwd != "" {
		cleanCwd = filepath.Clean(expandTilde(cleanCwd, home))
	}

	switch h {
	case "claude", "claude-code", "claudecode":
		return discoverClaudeTranscript(home, cleanCwd, sid)
	case "codex", "openai-codex":
		return discoverCodexTranscript(home, cleanCwd, sid)
	case "gemini", "gemini-cli":
		return discoverGeminiTranscript(home, sid)
	case "":
		if p := discoverClaudeTranscript(home, cleanCwd, sid); p != "" {
			return p
		}
		if p := discoverCodexTranscript(home, cleanCwd, sid); p != "" {
			return p
		}
		return discoverGeminiTranscript(home, sid)
	default:
		return ""
	}
}

func expandTilde(p, home string) string {
	if p == "" || home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

func isRegularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func claudeProjectSlug(cwd string) string {
	if cwd == "" {
		return ""
	}
	slug := strings.ReplaceAll(cwd, string(filepath.Separator), "-")
	if filepath.Separator != '/' {
		slug = strings.ReplaceAll(slug, "/", "-")
	}
	return slug
}

func discoverClaudeTranscript(home, cwd, sessionID string) string {
	if cwd == "" {
		return ""
	}
	projectsRoot := filepath.Join(home, ".claude", "projects")
	candidates := []string{
		filepath.Join(projectsRoot, claudeProjectSlug(cwd)),
		filepath.Join(projectsRoot, strings.NewReplacer("/", "-", "_", "-", ".", "-").Replace(cwd)),
	}
	for _, dir := range candidates {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		if sessionID != "" {
			exact := filepath.Join(dir, sessionID+".jsonl")
			if isRegularFile(exact) {
				return exact
			}
			for _, ent := range entries {
				if !ent.IsDir() && strings.HasSuffix(ent.Name(), ".jsonl") && strings.Contains(ent.Name(), sessionID) {
					p := filepath.Join(dir, ent.Name())
					if isRegularFile(p) {
						return p
					}
				}
			}
		}
		var bestPath string
		var bestMod time.Time
		for _, ent := range entries {
			if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".jsonl") {
				continue
			}
			p := filepath.Join(dir, ent.Name())
			info, err := os.Lstat(p)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if bestPath == "" || info.ModTime().After(bestMod) {
				bestPath = p
				bestMod = info.ModTime()
			}
		}
		if bestPath != "" {
			return bestPath
		}
	}
	return ""
}

func discoverCodexTranscript(home, cwd, sessionID string) string {
	if sessionID == "" && cwd == "" {
		return ""
	}
	root := filepath.Join(home, ".codex", "sessions")
	if _, err := os.Stat(root); err != nil {
		return ""
	}
	var bestPath string
	var bestMod time.Time
	visited := 0
	const maxVisited = 200
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			rel, rerr := filepath.Rel(root, path)
			if rerr == nil && rel != "." && strings.Count(rel, string(filepath.Separator)) >= 4 {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		visited++
		if visited > maxVisited {
			return filepath.SkipAll
		}
		info, serr := os.Lstat(path)
		if serr != nil || !info.Mode().IsRegular() {
			return nil
		}
		if sessionID != "" {
			if strings.Contains(d.Name(), sessionID) {
				if bestPath == "" || info.ModTime().After(bestMod) {
					bestPath = path
					bestMod = info.ModTime()
				}
			}
			return nil
		}
		if cwd != "" && fileHeaderContains(path, cwd) {
			if bestPath == "" || info.ModTime().After(bestMod) {
				bestPath = path
				bestMod = info.ModTime()
			}
		}
		return nil
	})
	return bestPath
}

func discoverGeminiTranscript(home, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	tmpRoot := filepath.Join(home, ".gemini", "tmp")
	dirs, err := os.ReadDir(tmpRoot)
	if err != nil {
		return ""
	}
	var bestPath string
	var bestMod time.Time
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		chatsDir := filepath.Join(tmpRoot, d.Name(), "chats")
		files, err := os.ReadDir(chatsDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			p := filepath.Join(chatsDir, f.Name())
			info, serr := os.Lstat(p)
			if serr != nil || !info.Mode().IsRegular() {
				continue
			}
			if strings.Contains(f.Name(), sessionID) || fileHeaderContains(p, sessionID) {
				if bestPath == "" || info.ModTime().After(bestMod) {
					bestPath = p
					bestMod = info.ModTime()
				}
			}
		}
	}
	return bestPath
}

func fileHeaderContains(path, needle string) bool {
	if needle == "" {
		return false
	}
	f, _, err := OpenRegular(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 2048)
	n, _ := f.Read(buf)
	return strings.Contains(string(buf[:n]), needle)
}

func parseJSONLBytes(raw []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal(line, &obj); err != nil || obj == nil {
			// Plain text fallback line.
			appendSanitizedLines(&out, "", string(line))
			continue
		}
		extractJSONRecordLines(&out, obj)
	}
	return out
}

func parseGeminiJSON(raw []byte) []string {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil || root == nil {
		return nil
	}
	var out []string
	if msgs, ok := root["messages"].([]any); ok {
		for _, item := range msgs {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			role := normalizeRole(firstString(m, "type", "role", "author"))
			extractContentValue(&out, role, m["content"])
			if parts, ok := m["parts"].([]any); ok {
				extractContentValue(&out, role, parts)
			}
			if calls, ok := m["toolCalls"].([]any); ok {
				for _, tc := range calls {
					if tcm, ok := tc.(map[string]any); ok {
						extractToolCall(&out, tcm)
					}
				}
			}
		}
	}
	if hist, ok := root["history"].([]any); ok {
		for _, item := range hist {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			role := normalizeRole(firstString(m, "role", "type"))
			extractContentValue(&out, role, m["parts"])
			extractContentValue(&out, role, m["content"])
		}
	}
	return out
}

func extractJSONRecordLines(out *[]string, obj map[string]any) {
	recType := strings.ToLower(firstString(obj, "type"))

	// Codex rollout format: {"type": "response_item"|"event_msg", "payload": {...}}
	if payload, ok := obj["payload"].(map[string]any); ok {
		pType := strings.ToLower(firstString(payload, "type"))
		switch pType {
		case "message":
			role := normalizeRole(firstString(payload, "role"))
			extractContentValue(out, role, payload["content"])
			return
		case "user_message":
			appendSanitizedLines(out, "user", firstString(payload, "message", "text"))
			return
		case "agent_message", "assistant_message":
			appendSanitizedLines(out, "assistant", firstString(payload, "message", "text"))
			return
		case "function_call", "tool_call":
			extractToolCall(out, payload)
			return
		case "function_call_output", "tool_result":
			appendSanitizedLines(out, "tool", firstString(payload, "output", "content"))
			return
		}
	}

	// Claude JSONL format: {"type": "user"|"assistant", "message": {"role": ..., "content": ...}}
	if msg, ok := obj["message"].(map[string]any); ok {
		role := normalizeRole(firstString(msg, "role"))
		if role == "" {
			role = normalizeRole(recType)
		}
		extractContentValue(out, role, msg["content"])
		return
	}

	// Generic / direct JSONL record.
	role := normalizeRole(firstString(obj, "role"))
	if role == "" {
		role = normalizeRole(recType)
	}
	before := len(*out)
	if content, has := obj["content"]; has {
		extractContentValue(out, role, content)
	}
	for _, k := range []string{"prompt", "response", "text", "summary", "message"} {
		if s, ok := obj[k].(string); ok && strings.TrimSpace(s) != "" {
			r := role
			if r == "" {
				if k == "prompt" {
					r = "user"
				} else if k == "response" {
					r = "assistant"
				}
			}
			appendSanitizedLines(out, r, s)
		}
	}
	if len(*out) == before {
		if name := firstString(obj, "tool_name", "tool", "name"); name != "" {
			extractToolCall(out, obj)
		}
	}
}

func extractContentValue(out *[]string, role string, val any) {
	switch v := val.(type) {
	case string:
		appendSanitizedLines(out, role, v)
	case []any:
		for _, item := range v {
			switch block := item.(type) {
			case string:
				appendSanitizedLines(out, role, block)
			case map[string]any:
				bType := strings.ToLower(firstString(block, "type"))
				switch bType {
				case "tool_use", "tool_call", "function_call":
					extractToolCall(out, block)
				case "tool_result", "function_call_output":
					extractContentValue(out, "tool", block["content"])
					if s := firstString(block, "output"); s != "" {
						appendSanitizedLines(out, "tool", s)
					}
				case "thinking":
					if s := firstString(block, "thinking", "text"); s != "" {
						appendSanitizedLines(out, role, s)
					}
				default:
					if s := firstString(block, "text", "content", "value"); s != "" {
						appendSanitizedLines(out, role, s)
					}
				}
			}
		}
	}
}

func extractToolCall(out *[]string, m map[string]any) {
	name := firstString(m, "name", "tool_name", "tool")
	argSummary := ""
	for _, key := range []string{"input", "args", "arguments"} {
		v, ok := m[key]
		if !ok || v == nil {
			continue
		}
		switch t := v.(type) {
		case string:
			argSummary = strings.TrimSpace(t)
		case map[string]any:
			argSummary = firstString(t, "command", "file_path", "path", "pattern", "query", "url")
			if argSummary == "" {
				if b, err := json.Marshal(t); err == nil {
					argSummary = string(b)
				}
			}
		}
		if argSummary != "" {
			break
		}
	}
	switch {
	case name != "" && argSummary != "":
		appendSanitizedLines(out, "tool", name+": "+argSummary)
	case name != "":
		appendSanitizedLines(out, "tool", name)
	case argSummary != "":
		appendSanitizedLines(out, "tool", argSummary)
	}
}

func appendSanitizedLines(out *[]string, role, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(rawLine, "\r"))
		if line == "" {
			continue
		}
		line = MaskSecrets(line)
		if len(line) > maxLineLen {
			line = line[:maxLineLen]
		}
		if role != "" {
			*out = append(*out, role+": "+line)
		} else {
			*out = append(*out, line)
		}
	}
}

func normalizeRole(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "user", "human":
		return "user"
	case "assistant", "model", "gemini", "agent":
		return "assistant"
	case "tool", "function":
		return "tool"
	default:
		return ""
	}
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}
