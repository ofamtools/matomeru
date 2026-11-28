package daemon

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

// One-shot sticky pane titles.
//
// The first worthwhile prompt a pane receives may replace a placeholder title
// (the harness or shell name, an empty title, or an oversized raw prompt) with
// a short summary. A pane is titled at most once: an in-memory claim keeps
// concurrent and repeated prompts from starting a second summarizer, and the
// paneTitleSummarizedField marker on the pane record keeps the decision across
// daemon restarts. A title somebody chose (a rename, an orchestrator's
// "D2 (Fable 5.2): ..." title) is never a placeholder, so it is never touched.
//
// The summary comes from an out-of-process command when one is configured and
// from SummarizePromptTitle otherwise, or when the command fails or times out.

const (
	// EnvTitleSummarizerCmd names a shell command that reads the raw prompt
	// on stdin and prints a concise one-line title on stdout.
	EnvTitleSummarizerCmd = "MTM_TITLE_SUMMARIZER_CMD"
	// EnvTitleSummarizerTimeout overrides the command timeout (a Go duration).
	EnvTitleSummarizerTimeout = "MTM_TITLE_SUMMARIZER_TIMEOUT"
	// EnvDisableTitleSummarizer turns automatic pane titles off when set to
	// anything but "", "0" or "false".
	EnvDisableTitleSummarizer = "MTM_DISABLE_TITLE_SUMMARIZER"

	// PaneTitleMaxChars caps an automatic pane title.
	PaneTitleMaxChars = 52

	paneTitleSummarizedField  = "titleSummarized"
	paneTitleRawPromptChars   = 60
	paneTitleDefaultTimeout   = 5 * time.Second
	paneTitleMaxTimeout       = 30 * time.Second
	paneTitleCmdWaitDelay     = 500 * time.Millisecond
	paneTitleMinWords         = 2
	paneTitleMinChars         = 8
	paneTitleCmdMaxOutputSize = 4096
)

// paneTitlePlaceholders are the titles a pane carries before anybody chose
// one: harness and shell names. The pane's own harness field is checked too.
var paneTitlePlaceholders = map[string]bool{
	"": true, "pane": true, "shell": true, "terminal": true, "agent": true,
	"zsh": true, "bash": true, "sh": true, "fish": true,
	"claude": true, "codex": true, "agy": true, "antigravity": true,
	"omp": true, "pi": true, "opencode": true, "gemini": true,
}

// paneTitleFillers are leading conversational phrases dropped from a prompt
// before it becomes a title. Longer phrases come first so they win.
var paneTitleFillers = []string{
	"i would like you to", "i'd like you to", "i want you to", "i need you to",
	"can you please", "could you please", "would you please",
	"i would like to", "i'd like to", "i want to", "i need to", "we need to",
	"go ahead and", "can you", "could you", "would you", "will you",
	"help me to", "help me", "let's", "lets", "please", "pls", "kindly",
	"hello", "hey", "hi", "okay", "ok", "so", "now", "also", "just", "claude",
}

// paneTitleDangling are words a trimmed title should not end on.
var paneTitleDangling = map[string]bool{
	"a": true, "an": true, "and": true, "the": true, "to": true, "of": true,
	"in": true, "on": true, "for": true, "with": true, "or": true, "at": true,
	"by": true, "from": true, "into": true, "that": true, "so": true,
}

type paneTitleSummarizer struct {
	store    *data.Store
	command  string
	timeout  time.Duration
	disabled bool

	mu      sync.Mutex
	claimed map[string]struct{}
	// inflight counts running summarizer goroutines.
	inflight sync.WaitGroup
}

// newPaneTitleSummarizer reads the summarizer configuration from the daemon's
// environment.
func newPaneTitleSummarizer(store *data.Store) *paneTitleSummarizer {
	timeout := paneTitleDefaultTimeout
	if d, err := time.ParseDuration(strings.TrimSpace(os.Getenv(EnvTitleSummarizerTimeout))); err == nil && d > 0 {
		timeout = min(d, paneTitleMaxTimeout)
	}
	off := strings.ToLower(strings.TrimSpace(os.Getenv(EnvDisableTitleSummarizer)))
	return &paneTitleSummarizer{
		store:    store,
		command:  strings.TrimSpace(os.Getenv(EnvTitleSummarizerCmd)),
		timeout:  timeout,
		disabled: off != "" && off != "0" && off != "false",
		claimed:  map[string]struct{}{},
	}
}

// claim reports whether the caller is the first to ask for paneID's title.
func (p *paneTitleSummarizer) claim(paneID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, taken := p.claimed[paneID]; taken {
		return false
	}
	p.claimed[paneID] = struct{}{}
	return true
}

func (p *paneTitleSummarizer) release(paneID string) {
	p.mu.Lock()
	delete(p.claimed, paneID)
	p.mu.Unlock()
}

// summarizePaneTitle starts the one-shot title summarizer for a prompt hook
// event. It never blocks: the only work on the caller's goroutine is a cheap
// prompt check and the per-pane claim.
func (s *Server) summarizePaneTitle(ev HookEvent) {
	p := s.titles
	if p == nil || p.disabled || p.store == nil {
		return
	}
	paneID := strings.TrimSpace(ev.PaneID)
	if paneID == "" || ev.SubagentID != "" || !paneTitleWorthyPrompt(ev.Prompt) {
		return
	}
	if !p.claim(paneID) {
		return
	}
	parent := s.ctx
	if parent == nil {
		parent = context.Background()
	}
	s.wg.Add(1)
	p.inflight.Add(1)
	go func() {
		defer s.wg.Done()
		defer p.inflight.Done()
		defer func() { _ = recover() }()
		ctx, cancel := context.WithTimeout(parent, p.timeout)
		defer cancel()
		p.run(ctx, paneID, ev.Prompt)
	}()
}

// run titles one claimed pane. The claim is kept whatever happens, except
// when the pane has no record yet, so a pane registered later still gets its
// one title.
func (p *paneTitleSummarizer) run(ctx context.Context, paneID, prompt string) {
	ref := core.Ref{Kind: data.KindPane, ID: paneID}
	pane, err := p.store.Get(ref)
	if err != nil {
		p.release(paneID)
		return
	}
	if !paneTitleEligible(*pane) {
		return
	}
	before := pane.Text("title")
	title := p.summarize(ctx, prompt)
	if title == "" {
		return
	}
	// The command may have run for seconds: leave a title that changed
	// meanwhile (a rename) alone.
	pane, err = p.store.Get(ref)
	if err != nil || pane.Text("title") != before || !paneTitleEligible(*pane) {
		return
	}
	_, _, _ = p.store.Patch(data.PatchInput{
		Ref:     ref,
		Fields:  map[string]any{"title": title, paneTitleSummarizedField: "true"},
		Summary: "summarize pane title from first prompt",
	}, daemonActor)
}

// summarize returns the external command's title, or the deterministic one
// when no command is configured or it fails, times out or prints nothing.
func (p *paneTitleSummarizer) summarize(ctx context.Context, prompt string) string {
	if p.command != "" {
		if title := runTitleSummarizerCommand(ctx, p.command, prompt); title != "" {
			return title
		}
	}
	return SummarizePromptTitle(prompt, PaneTitleMaxChars)
}

// runTitleSummarizerCommand runs command through the shell with the prompt on
// stdin and returns its cleaned first output line, or "" on any failure.
func runTitleSummarizerCommand(ctx context.Context, command, prompt string) string {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Stdin = strings.NewReader(prompt)
	var out bytes.Buffer
	cmd.Stdout = &out
	// A grandchild holding stdout open must not outlive the timeout.
	cmd.WaitDelay = paneTitleCmdWaitDelay
	if err := cmd.Run(); err != nil || ctx.Err() != nil {
		return ""
	}
	raw := out.Bytes()
	if len(raw) > paneTitleCmdMaxOutputSize {
		raw = raw[:paneTitleCmdMaxOutputSize]
	}
	return cleanSummarizerTitle(string(raw), PaneTitleMaxChars)
}

// cleanSummarizerTitle turns command output into a one-line title: the first
// line with text, without wrapping quotes or control characters, capped at
// maxChars on a word boundary.
func cleanSummarizerTitle(out string, maxChars int) string {
	for _, line := range strings.Split(strings.ToValidUTF8(out, ""), "\n") {
		line = strings.Trim(collapseTitleSpace(line), "\"'`*# ")
		if line != "" {
			return trimTitleToWidth(line, maxChars)
		}
	}
	return ""
}

// paneTitleEligible reports whether pane may still receive its one automatic
// title: never summarized before, and still carrying a placeholder title.
func paneTitleEligible(pane data.Record) bool {
	if pane.Text(paneTitleSummarizedField) != "" {
		return false
	}
	return isPlaceholderPaneTitle(pane.Text("title"), pane.Text("harness"))
}

// isPlaceholderPaneTitle reports whether title is a default nobody chose: a
// harness or shell name, empty, or a raw prompt too long to be a title.
func isPlaceholderPaneTitle(title, harnessID string) bool {
	t := strings.ToLower(strings.TrimSpace(title))
	if paneTitlePlaceholders[t] {
		return true
	}
	if h := strings.ToLower(strings.TrimSpace(harnessID)); h != "" && t == h {
		return true
	}
	return utf8.RuneCountInString(t) > paneTitleRawPromptChars
}

// paneTitleWorthyPrompt reports whether prompt says enough to title a pane.
// A slash command or a one-word reply ("continue", "y") does not spend the
// pane's one title.
func paneTitleWorthyPrompt(prompt string) bool {
	if strings.HasPrefix(strings.TrimSpace(prompt), "/") {
		return false
	}
	title := SummarizePromptTitle(prompt, PaneTitleMaxChars)
	return len(strings.Fields(title)) >= paneTitleMinWords && utf8.RuneCountInString(title) >= paneTitleMinChars
}

// SummarizePromptTitle derives a short title from a raw prompt without any
// model: the first line with words, leading conversational filler and
// Markdown markers dropped, whitespace collapsed, cut at the first sentence
// end and then at a word boundary so the result is at most maxChars runes.
func SummarizePromptTitle(raw string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	line := firstTitleLine(strings.ToValidUTF8(raw, ""))
	line = stripTitleFillers(line)
	line = firstTitleSentence(line)
	line = trimTitleToWidth(line, maxChars)
	return capitalizeTitle(line)
}

// firstTitleLine returns the first line of raw that holds a letter or digit,
// with whitespace collapsed and leading Markdown markers removed.
func firstTitleLine(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimLeft(collapseTitleSpace(line), "#>-*`~=| ")
		if strings.IndexFunc(line, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
			return line
		}
	}
	return ""
}

func collapseTitleSpace(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// stripTitleFillers drops leading filler phrases, repeatedly, so
// "Hey, can you please fix X" becomes "fix X".
func stripTitleFillers(line string) string {
	for {
		rest, ok := cutTitleFiller(line)
		if !ok {
			return line
		}
		line = rest
	}
}

func cutTitleFiller(line string) (string, bool) {
	lower := strings.ToLower(line)
	for _, filler := range paneTitleFillers {
		if !strings.HasPrefix(lower, filler) {
			continue
		}
		rest := line[len(filler):]
		if rest == "" {
			return line, false // nothing but filler: keep it
		}
		if r, _ := utf8.DecodeRuneInString(rest); unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue // "source" is not "so"
		}
		rest = strings.TrimLeft(rest, " ,.!:;-")
		if rest == "" {
			return line, false
		}
		return rest, true
	}
	return line, false
}

// firstTitleSentence cuts line after its first sentence when that sentence
// is long enough to stand as a title.
func firstTitleSentence(line string) string {
	for i, r := range line {
		if r != '.' && r != '?' && r != '!' {
			continue
		}
		rest := line[i+1:]
		if rest != "" && !strings.HasPrefix(rest, " ") {
			continue // "v1.2", "main.go"
		}
		if head := line[:i]; len(strings.Fields(head)) >= paneTitleMinWords+1 {
			return head
		}
	}
	return line
}

// trimTitleToWidth cuts s to at most maxChars runes on a word boundary and
// drops trailing punctuation and dangling connector words.
func trimTitleToWidth(s string, maxChars int) string {
	s = strings.TrimSpace(s)
	if maxChars <= 0 {
		return ""
	}
	cut := false
	if utf8.RuneCountInString(s) > maxChars {
		cut = true
		runes := []rune(s)
		head := string(runes[:maxChars])
		// Keep whole words: back up to the last space unless the cut landed
		// exactly on a word end.
		if runes[maxChars] != ' ' {
			if i := strings.LastIndex(head, " "); i > 0 {
				head = head[:i]
			}
		}
		s = head
	}
	s = strings.TrimRight(s, " ,.;:!?-—–([{\"'`")
	if cut {
		for {
			i := strings.LastIndex(s, " ")
			if i < 0 || !paneTitleDangling[strings.ToLower(s[i+1:])] {
				break
			}
			s = strings.TrimRight(s[:i], " ,.;:!?-—–")
		}
	}
	return s
}

func capitalizeTitle(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if size == 0 || !unicode.IsLower(r) {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}
