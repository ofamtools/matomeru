// Package contextitems extracts high-signal context items (HTTP/HTTPS URLs,
// OSC-8 terminal hyperlinks, file/document paths with optional :line[:col], and
// GitHub owner/repo#123 PR/issue references) from terminal screens, prompts,
// agent responses, and created file lists.
//
// It intentionally excludes noisy bare commit SHAs and uncontextualized git
// branch names, and provides ExtractVisibleMatches with exact visual screen
// coordinates ready for ochazuke/widgets/hints.
//
// contextitems imports only the standard library.
package contextitems

import (
	"cmp"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Item kinds.
const (
	KindURL  = "url"
	KindOSC8 = "osc8"
	KindFile = "file"
	KindPR   = "pr"
)

// Source tags identifying where a context item was harvested.
const (
	SourceScreen      = "screen"
	SourcePrompt      = "prompt"
	SourceResponse    = "response"
	SourceCreatedFile = "created_file"
)

// Item is one deduplicated high-signal context item.
type Item struct {
	Kind   string `json:"kind"`
	Value  string `json:"value"`
	Path   string `json:"path,omitempty"`
	Line   int    `json:"line,omitempty"`
	Col    int    `json:"col,omitempty"`
	Label  string `json:"label,omitempty"`
	Source string `json:"source,omitempty"`
}

// Match locates an extractable target on a visible terminal screen line in
// 0-based visual columns [StartCol, EndCol) after ANSI stripping.
type Match struct {
	Row      int    `json:"row"`
	StartCol int    `json:"startCol"`
	EndCol   int    `json:"endCol"`
	Kind     string `json:"kind"`
	Value    string `json:"value"`
	Label    string `json:"label,omitempty"`
}

// Options configures extraction behavior.
type Options struct {
	// Source tags every extracted Item (e.g. SourceScreen, SourcePrompt,
	// SourceResponse, SourceCreatedFile).
	Source string
	// WrapWidth, when > 0, is the terminal column width used to detect
	// soft-wrapped lines in addition to URL/path continuation heuristics.
	WrapWidth int
}

var (
	urlRe      = regexp.MustCompile(`https?://[^\s<>"{}|\\^` + "`" + `]+`)
	prRefRe    = regexp.MustCompile(`(?:^|[\s(\[{"',;:])([A-Za-z0-9][A-Za-z0-9_.-]{0,38}/[A-Za-z0-9_.-]{1,100}#[1-9][0-9]*)\b`)
	pathRe     = regexp.MustCompile(`(?:~?/|\./|\.\./|(?:[A-Za-z0-9_.-]+/)+)[A-Za-z0-9_@%+=,.:~/-]+|\b[A-Za-z0-9_.-]+\.[A-Za-z][A-Za-z0-9]{0,9}(?::[0-9]+(?::[0-9]+)?)?`)
	locRe      = regexp.MustCompile(`:([0-9]+)(?::([0-9]+))?$`)
	hexSHARe   = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)
	semverRe   = regexp.MustCompile(`^v?[0-9]+\.[0-9]+(\.[0-9]+)?`)
	ansiCSISeq = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	ansiOSCSeq = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
)

// knownExtensions is the high-signal allowlist for bare filenames without a
// directory separator (e.g. "main.go", "README.md", "harnesses.json") so prose
// words and dotted identifiers like "os.Exit" or "e.g." are ignored.
var knownExtensions = map[string]bool{
	"go": true, "mod": true, "sum": true,
	"md": true, "markdown": true, "txt": true, "rst": true, "org": true, "pdf": true, "docx": true,
	"json": true, "jsonc": true, "jsonl": true, "yaml": true, "yml": true, "toml": true, "ini": true, "cfg": true, "conf": true, "env": true, "xml": true,
	"csv": true, "tsv": true, "sql": true, "db": true, "sqlite": true,
	"ts": true, "tsx": true, "js": true, "jsx": true, "mjs": true, "cjs": true, "css": true, "scss": true, "html": true, "htm": true, "svg": true,
	"py": true, "rb": true, "rs": true, "c": true, "h": true, "cc": true, "cpp": true, "hpp": true, "java": true, "kt": true, "swift": true, "zig": true, "lua": true, "php": true,
	"sh": true, "bash": true, "zsh": true, "fish": true,
	"proto": true, "graphql": true, "gql": true, "tf": true, "hcl": true,
	"log": true, "diff": true, "patch": true, "lock": true,
	"png": true, "jpg": true, "jpeg": true, "gif": true, "webp": true,
}

var knownBasenames = map[string]bool{
	"makefile": true, "dockerfile": true, "justfile": true, "procfile": true,
	"vagrantfile": true, "readme": true, "license": true, "changelog": true,
	"agents": true, "claude": true, "gemfile": true, "rakefile": true,
}

var branchPrefixes = map[string]bool{
	"feat": true, "feature": true, "fix": true, "bugfix": true, "hotfix": true,
	"chore": true, "refactor": true, "release": true, "wip": true,
	"origin": true, "upstream": true, "refs": true, "heads": true, "remotes": true,
}

type osc8Link struct {
	uri      string
	label    string
	startCol int
	endCol   int
}

// Extract extracts deduplicated, high-signal context items from text.
func Extract(text string, opts Options) []Item {
	if text == "" {
		return nil
	}
	norm := strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(norm, "\n")
	return ExtractLines(lines, opts)
}

// ExtractLines extracts deduplicated, high-signal context items from terminal
// lines, preserving OSC-8 hyperlinks and repairing soft-wrapped URLs/paths.
func ExtractLines(lines []string, opts Options) []Item {
	if len(lines) == 0 {
		return nil
	}

	var out []Item
	seen := map[string]int{} // normalized value -> index in out

	addItem := func(it Item) {
		it.Source = opts.Source
		key := dedupeKey(it)
		if key == "" {
			return
		}
		if idx, exists := seen[key]; exists {
			// Upgrade metadata if the newer sighting has a label or line info.
			if out[idx].Label == "" && it.Label != "" {
				out[idx].Label = it.Label
			}
			if out[idx].Line == 0 && it.Line > 0 {
				out[idx].Line = it.Line
				out[idx].Col = it.Col
				out[idx].Value = it.Value
			}
			return
		}
		seen[key] = len(out)
		out = append(out, it)
	}

	// 1. Parse each line for OSC-8 hyperlinks while producing clean visual lines.
	plainLines := make([]string, len(lines))
	for i, raw := range lines {
		plain, links := parseLineANSI(raw)
		plainLines[i] = plain
		for _, l := range links {
			if uri := cleanOSC8URI(l.uri); uri != "" {
				addItem(Item{
					Kind:  KindOSC8,
					Value: uri,
					Label: strings.TrimSpace(l.label),
				})
			}
		}
	}

	// 2. Join soft-wrapped continuations on plain lines.
	joined := joinSoftWrappedLines(plainLines, opts.WrapWidth)

	// 3. Extract URLs, GitHub PR refs, and file paths without overlapping spans.
	for _, line := range joined {
		for _, m := range scanPlainLine(line, 0) {
			it := Item{
				Kind:  m.Kind,
				Value: m.Value,
				Label: m.Label,
			}
			if m.Kind == KindFile {
				it.Path, it.Line, it.Col = splitFileLocation(m.Value)
			}
			addItem(it)
		}
	}

	return out
}

// Unwrap strips ANSI sequences and joins soft-wrapped URL/path lines while
// leaving ordinary line breaks intact.
func Unwrap(text string, wrapWidth int) string {
	norm := strings.ReplaceAll(text, "\r\n", "\n")
	rawLines := strings.Split(norm, "\n")
	plain := make([]string, len(rawLines))
	for i, l := range rawLines {
		plain[i], _ = parseLineANSI(l)
	}
	return strings.Join(joinSoftWrappedLines(plain, wrapWidth), "\n")
}

// ExtractVisibleMatches scans visible terminal screen lines and returns
// non-overlapping Match spans with 0-based visual Row, StartCol, and EndCol
// coordinates ready to plug directly into ochazuke/widgets/hints.
func ExtractVisibleMatches(lines []string) []Match {
	if len(lines) == 0 {
		return nil
	}

	plainLines := make([]string, len(lines))
	oscByRow := make([][]osc8Link, len(lines))
	for r, raw := range lines {
		plainLines[r], oscByRow[r] = parseLineANSI(raw)
	}

	var all []Match
	for r, plain := range plainLines {
		var rowMatches []Match
		var occupied [][2]int

		// First record OSC-8 hyperlinks using their exact visible label columns.
		for _, l := range oscByRow[r] {
			uri := cleanOSC8URI(l.uri)
			if uri == "" || l.endCol <= l.startCol {
				continue
			}
			rowMatches = append(rowMatches, Match{
				Row:      r,
				StartCol: l.startCol,
				EndCol:   l.endCol,
				Kind:     KindOSC8,
				Value:    uri,
				Label:    strings.TrimSpace(l.label),
			})
			occupied = append(occupied, [2]int{l.startCol, l.endCol})
		}

		// Scan plain text for URLs, PR refs, and file paths.
		for _, m := range scanPlainLine(plain, r) {
			if overlapsCols(occupied, m.StartCol, m.EndCol) {
				continue
			}
			// If a URL wraps cleanly onto the next visible row, enrich Value
			// with the joined target while keeping the hint on this row's span.
			if m.Kind == KindURL && r+1 < len(plainLines) {
				if joinedVal, ok := peekWrappedURLContinuation(plain, m, plainLines[r+1]); ok {
					m.Value = joinedVal
				}
			}
			rowMatches = append(rowMatches, m)
			occupied = append(occupied, [2]int{m.StartCol, m.EndCol})
		}

		slices.SortFunc(rowMatches, func(a, b Match) int {
			return cmp.Compare(a.StartCol, b.StartCol)
		})
		all = append(all, rowMatches...)
	}

	return all
}

// scanPlainLine extracts non-overlapping URL, PR ref, and file path matches
// from a single ANSI-stripped line with visual rune columns.
func scanPlainLine(line string, row int) []Match {
	if strings.TrimSpace(line) == "" {
		return nil
	}

	var matches []Match
	var byteSpans [][2]int

	// 1. HTTP/HTTPS URLs.
	for _, sp := range urlRe.FindAllStringIndex(line, -1) {
		raw := line[sp[0]:sp[1]]
		cleaned := trimURLTrailing(raw)
		if cleaned == "" {
			continue
		}
		u, err := url.Parse(cleaned)
		if err != nil || u.Host == "" {
			continue
		}
		endByte := sp[0] + len(cleaned)
		matches = append(matches, Match{
			Row:      row,
			StartCol: byteOffsetToCol(line, sp[0]),
			EndCol:   byteOffsetToCol(line, endByte),
			Kind:     KindURL,
			Value:    cleaned,
		})
		byteSpans = append(byteSpans, [2]int{sp[0], endByte})
	}

	// 2. GitHub PR / issue refs (owner/repo#123).
	for _, sub := range prRefRe.FindAllStringSubmatchIndex(line, -1) {
		start, end := sub[2], sub[3]
		if overlapsCols(byteSpans, start, end) {
			continue
		}
		val := line[start:end]
		matches = append(matches, Match{
			Row:      row,
			StartCol: byteOffsetToCol(line, start),
			EndCol:   byteOffsetToCol(line, end),
			Kind:     KindPR,
			Value:    val,
		})
		byteSpans = append(byteSpans, [2]int{start, end})
	}

	// 3. File / document paths (with optional :line or :line:col).
	for _, sp := range pathRe.FindAllStringIndex(line, -1) {
		start, end := sp[0], sp[1]
		if overlapsCols(byteSpans, start, end) {
			continue
		}
		// Reject tokens glued onto an identifier prefix (e.g. email or scheme).
		if start > 0 {
			prev, _ := utf8.DecodeLastRuneInString(line[:start])
			if unicode.IsLetter(prev) || unicode.IsDigit(prev) || prev == '@' || prev == '#' {
				continue
			}
		}
		raw := line[start:end]
		cleaned := trimPathTrailing(raw)
		if cleaned == "" {
			continue
		}
		if !isHighSignalPath(cleaned) {
			continue
		}
		endByte := start + len(cleaned)
		matches = append(matches, Match{
			Row:      row,
			StartCol: byteOffsetToCol(line, start),
			EndCol:   byteOffsetToCol(line, endByte),
			Kind:     KindFile,
			Value:    cleaned,
		})
		byteSpans = append(byteSpans, [2]int{start, endByte})
	}

	slices.SortFunc(matches, func(a, b Match) int {
		return cmp.Compare(a.StartCol, b.StartCol)
	})
	return matches
}

// isHighSignalPath keeps real file/document paths while rejecting bare commit
// SHAs, uncontextualized git branch names, version numbers, and prose.
func isHighSignalPath(raw string) bool {
	if raw == "" || raw == "/" || strings.HasPrefix(raw, "//") {
		return false
	}
	basePath, lineNum, _ := splitFileLocation(raw)
	if basePath == "" || basePath == "/" || basePath == "." || basePath == ".." {
		return false
	}
	// Never treat bare commit SHAs or semver strings as files.
	if hexSHARe.MatchString(basePath) || semverRe.MatchString(basePath) {
		return false
	}
	// Reject trailing slash-only junk like "a/".
	trimmed := strings.TrimRight(basePath, "/")
	if trimmed == "" {
		return false
	}

	hasExplicitPrefix := strings.HasPrefix(basePath, "/") ||
		strings.HasPrefix(basePath, "~/") ||
		strings.HasPrefix(basePath, "./") ||
		strings.HasPrefix(basePath, "../")

	if hasExplicitPrefix {
		// Reject root-level single slash or trivial "/a" unless it has an extension or multiple segments.
		if strings.HasPrefix(basePath, "/") {
			rest := strings.TrimPrefix(trimmed, "/")
			if rest == "" {
				return false
			}
			// Must have at least 2 path segments (e.g. /etc/hosts, /Users/x/y)
			// or a file extension (e.g. /main.go).
			if !strings.Contains(rest, "/") && !hasPlausibleExtension(rest) {
				return false
			}
		}
		return true
	}

	// Relative path with one or more slashes (e.g. "app/model.go", "docs/README.md").
	if strings.Contains(trimmed, "/") {
		parts := strings.Split(trimmed, "/")
		first := strings.ToLower(parts[0])
		last := parts[len(parts)-1]
		if last == "" {
			return false
		}
		// If it has a file extension or a known filename or :line location, it's a file.
		if hasPlausibleExtension(last) || knownBasenames[strings.ToLower(last)] || lineNum > 0 {
			// Still reject git remote/branch refs like "origin/v1.2" where extension is numeric.
			if branchPrefixes[first] && !hasKnownExtension(last) && !knownBasenames[strings.ToLower(last)] && lineNum == 0 {
				return false
			}
			return true
		}
		// Without an extension or :line, slash tokens like "feat/auth",
		// "origin/main", "fix/login", or "foo/bar" are typically branch names
		// or prose ratios — intentionally exclude them!
		return false
	}

	// Bare filename without slash (e.g. "main.go:42", "README.md", "harnesses.json").
	// Must have a recognized code/doc/config extension.
	return hasKnownExtension(trimmed)
}

func hasPlausibleExtension(name string) bool {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	if ext == "" || len(ext) > 10 {
		return false
	}
	// Reject purely numeric extensions (e.g. "v1.2", "foo.123").
	allDigits := true
	for _, r := range ext {
		if !unicode.IsDigit(r) {
			allDigits = false
			break
		}
	}
	if allDigits {
		return false
	}
	// Reject leading-dot-only dotfiles unless they also have an extension or
	// are explicitly prefixed (handled above).
	if strings.HasPrefix(name, ".") && strings.Count(name, ".") == 1 {
		return false
	}
	return true
}

func hasKnownExtension(name string) bool {
	if strings.HasPrefix(name, ".") && strings.Count(name, ".") == 1 {
		return false
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	return knownExtensions[ext]
}

// joinSoftWrappedLines repairs URLs and file paths split across terminal lines.
func joinSoftWrappedLines(lines []string, wrapWidth int) []string {
	if len(lines) <= 1 {
		return slices.Clone(lines)
	}
	out := make([]string, 0, len(lines))
	cur := lines[0]

	for i := 1; i < len(lines); i++ {
		next := lines[i]
		if shouldJoinWrap(cur, next, wrapWidth) {
			// Attach the leading non-whitespace continuation token onto cur;
			// preserve any remainder of next on a new line so trailing prose
			// stays distinct.
			tok, rest := splitFirstToken(next)
			cur += tok
			if rest != "" {
				out = append(out, cur)
				cur = rest
			}
			continue
		}
		out = append(out, cur)
		cur = next
	}
	out = append(out, cur)
	return out
}

func shouldJoinWrap(line, next string, wrapWidth int) bool {
	if line == "" || next == "" {
		return false
	}
	// Continuation of a soft-wrapped token starts at column 0 with non-whitespace.
	firstRune, _ := utf8.DecodeRuneInString(next)
	if unicode.IsSpace(firstRune) {
		return false
	}
	// Do not join if next starts a brand-new URL.
	if strings.HasPrefix(next, "http://") || strings.HasPrefix(next, "https://") {
		return false
	}
	lastRune, _ := utf8.DecodeLastRuneInString(line)
	if unicode.IsSpace(lastRune) {
		return false
	}

	// Trailing token of current line.
	tok := lastToken(line)
	if tok == "" {
		return false
	}

	// Case 1: Explicit terminal wrap width reached on an open URL or path token.
	if wrapWidth > 0 && utf8.RuneCountInString(line) >= wrapWidth {
		if looksLikeURLToken(tok) || looksLikePathToken(tok) {
			return true
		}
	}

	// Case 2: Heuristic URL wrap without explicit wrapWidth.
	if looksLikeURLToken(tok) {
		nextTok, _ := splitFirstToken(next)
		if nextTok != "" && !strings.ContainsAny(nextTok, "<>\"`") {
			if strings.HasSuffix(tok, "/") || strings.HasSuffix(tok, "-") ||
				strings.HasSuffix(tok, "_") || strings.HasSuffix(tok, "?") ||
				strings.HasSuffix(tok, "&") || strings.HasSuffix(tok, "=") ||
				strings.HasPrefix(nextTok, "/") || strings.HasPrefix(nextTok, "?") ||
				strings.HasPrefix(nextTok, "&") || len(tok) >= 32 {
				return true
			}
		}
	}

	// Case 3: Heuristic file path wrap (line ends in '/' inside an explicit path).
	if looksLikePathToken(tok) && strings.HasSuffix(tok, "/") {
		nextTok, _ := splitFirstToken(next)
		if nextTok != "" && !strings.ContainsAny(nextTok, "<>\"`") {
			return true
		}
	}

	return false
}

func peekWrappedURLContinuation(curPlain string, m Match, nextPlain string) (string, bool) {
	if m.EndCol != utf8.RuneCountInString(curPlain) {
		return "", false
	}
	if !shouldJoinWrap(curPlain, nextPlain, 0) {
		return "", false
	}
	tok, _ := splitFirstToken(nextPlain)
	joined := trimURLTrailing(m.Value + tok)
	u, err := url.Parse(joined)
	if err != nil || u.Host == "" {
		return "", false
	}
	return joined, true
}

func lastToken(line string) string {
	idx := strings.LastIndexFunc(line, unicode.IsSpace)
	if idx < 0 {
		return line
	}
	return line[idx+1:]
}

func splitFirstToken(line string) (string, string) {
	idx := strings.IndexFunc(line, unicode.IsSpace)
	if idx < 0 {
		return line, ""
	}
	return line[:idx], strings.TrimLeftFunc(line[idx:], unicode.IsSpace)
}

func looksLikeURLToken(tok string) bool {
	return strings.HasPrefix(tok, "http://") || strings.HasPrefix(tok, "https://")
}

func looksLikePathToken(tok string) bool {
	return strings.HasPrefix(tok, "/") ||
		strings.HasPrefix(tok, "~/") ||
		strings.HasPrefix(tok, "./") ||
		strings.HasPrefix(tok, "../")
}

// parseLineANSI strips ANSI CSI and OSC sequences from a single line while
// capturing OSC-8 hyperlinks with their exact 0-based visual column spans.
func parseLineANSI(raw string) (string, []osc8Link) {
	if !strings.ContainsRune(raw, '\x1b') {
		return raw, nil
	}

	var b strings.Builder
	b.Grow(len(raw))

	var links []osc8Link
	var activeURI string
	var activeStartCol int
	var activeLabel strings.Builder
	col := 0

	flushOSC8 := func() {
		if activeURI != "" {
			links = append(links, osc8Link{
				uri:      activeURI,
				label:    activeLabel.String(),
				startCol: activeStartCol,
				endCol:   col,
			})
			activeURI = ""
			activeLabel.Reset()
		}
	}

	for i := 0; i < len(raw); {
		if raw[i] == '\x1b' && i+1 < len(raw) {
			switch raw[i+1] {
			case ']': // OSC sequence: \x1b] ... (\x07 | \x1b\\)
				end, termLen := findOSCEnd(raw, i+2)
				if end >= 0 {
					body := raw[i+2 : end]
					if rest, ok := strings.CutPrefix(body, "8;"); ok {
						if _, uri, hasSemi := strings.Cut(rest, ";"); hasSemi {
							if uri == "" {
								flushOSC8()
							} else {
								flushOSC8()
								activeURI = uri
								activeStartCol = col
								activeLabel.Reset()
							}
						}
					}
					i = end + termLen
					continue
				}
			case '[': // CSI sequence: \x1b[ ... final byte 0x40..0x7E
				j := i + 2
				for j < len(raw) && !(raw[j] >= 0x40 && raw[j] <= 0x7e) {
					j++
				}
				if j < len(raw) {
					i = j + 1
					continue
				}
			}
		}

		r, size := utf8.DecodeRuneInString(raw[i:])
		b.WriteRune(r)
		if activeURI != "" {
			activeLabel.WriteRune(r)
		}
		col++
		i += size
	}
	flushOSC8()

	// Final safety pass in case any stray ANSI escapes remained.
	plain := b.String()
	if strings.ContainsRune(plain, '\x1b') {
		plain = ansiOSCSeq.ReplaceAllString(plain, "")
		plain = ansiCSISeq.ReplaceAllString(plain, "")
	}
	return plain, links
}

func findOSCEnd(s string, start int) (int, int) {
	for i := start; i < len(s); i++ {
		if s[i] == '\x07' {
			return i, 1
		}
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' {
			return i, 2
		}
	}
	return -1, 0
}

func cleanOSC8URI(uri string) string {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return ""
	}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme == "" {
		return ""
	}
	return uri
}

func trimURLTrailing(raw string) string {
	s := strings.TrimRight(raw, ".,;:!'\"`")
	for strings.HasSuffix(s, ")") && strings.Count(s, ")") > strings.Count(s, "(") {
		s = strings.TrimSuffix(s, ")")
	}
	for strings.HasSuffix(s, "]") && strings.Count(s, "]") > strings.Count(s, "[") {
		s = strings.TrimSuffix(s, "]")
	}
	for strings.HasSuffix(s, "}") && strings.Count(s, "}") > strings.Count(s, "{") {
		s = strings.TrimSuffix(s, "}")
	}
	return strings.TrimRight(s, ".,;:!'\"`")
}

func trimPathTrailing(raw string) string {
	return strings.TrimRight(raw, ".,;:!'\")]}>`")
}

func splitFileLocation(raw string) (path string, line, col int) {
	loc := locRe.FindStringSubmatchIndex(raw)
	if loc == nil {
		return raw, 0, 0
	}
	path = raw[:loc[0]]
	line, _ = strconv.Atoi(raw[loc[2]:loc[3]])
	if loc[4] >= 0 && loc[5] >= 0 {
		col, _ = strconv.Atoi(raw[loc[4]:loc[5]])
	}
	return path, line, col
}

func byteOffsetToCol(s string, byteIdx int) int {
	return utf8.RuneCountInString(s[:byteIdx])
}

func overlapsCols(spans [][2]int, start, end int) bool {
	for _, sp := range spans {
		if start < sp[1] && end > sp[0] {
			return true
		}
	}
	return false
}

func dedupeKey(it Item) string {
	val := strings.TrimSpace(it.Value)
	if val == "" {
		return ""
	}
	switch it.Kind {
	case KindURL, KindOSC8:
		// Deduplicate OSC-8 and plain URL when they point to the exact same URI.
		return "uri\x00" + val
	case KindFile:
		p, _, _ := splitFileLocation(val)
		return "file\x00" + p
	default:
		return it.Kind + "\x00" + val
	}
}
