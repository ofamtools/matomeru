package contextitems

import (
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

// The patterns, lifted from matomeru-old's internal/context/extract.go
// (links, paths, the location suffix) and extended with PR references,
// branches and commits.
var (
	linkPattern    = regexp.MustCompile(`https?://[^\s<>"'` + "`" + `]+`)
	pathPattern    = regexp.MustCompile(`(?:~?/|\./|\.\./|(?:[[:alnum:]_.-]+/)+)[^\s<>"'` + "`" + `|]+|\b[[:alnum:]_.-]+\.[[:alpha:]][[:alnum:]]*(?::[0-9]+(?::[0-9]+)?)?`)
	locationSuffix = regexp.MustCompile(`:[0-9]+(?:[:-][0-9]+)?$`)
	// prRef is owner/repo#123, a GitHub PR or issue named without a URL.
	prRef = regexp.MustCompile(`\b([A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+)#([0-9]+)\b`)
	// branchPatterns name a branch in git's own words (status, switch,
	// push, worktree add, log decorations) or a shell prompt's git:NAME.
	branchName     = `([A-Za-z0-9_][A-Za-z0-9._/-]*)`
	branchPatterns = []*regexp.Regexp{
		regexp.MustCompile(`\bOn branch ` + branchName),
		regexp.MustCompile(`\b[Ss]witched to (?:a new )?branch '([^'\s]+)'`),
		regexp.MustCompile(`\bbranch '([^'\s]+)' set up to track`),
		regexp.MustCompile(`\bgit (?:checkout -[bB]|switch -[cC]) +` + branchName),
		regexp.MustCompile(`\bworktree add +\S+ +-[bB] +` + branchName),
		regexp.MustCompile(`\[new branch\] +` + branchName),
		regexp.MustCompile(`\bHEAD -> ` + branchName),
		regexp.MustCompile(`\bgit:\(?` + branchName),
		regexp.MustCompile(`\bbranch ([A-Za-z0-9_][A-Za-z0-9._-]*/[A-Za-z0-9._/-]+)`),
	}
	// commitPattern is a lowercase hex run; commitAt decides from its
	// neighbours and length whether it is a SHA.
	commitPattern = regexp.MustCompile(`[0-9a-f]{7,40}`)
)

// Join strips terminal controls and joins soft-wrapped lines: a line that
// fills width (the pane's columns) exactly and ends mid-token continues on the
// next, so a wrapped URL or path is one string. Lines that are hard
// newlines stay apart. URLs split by a program's own wrap are repaired as
// matomeru-old did (Unwrap).
func Join(text string, width int) string {
	text = strings.ReplaceAll(ansi.Strip(text), "\r\n", "\n")
	raw := strings.Split(text, "\n")
	lines := make([]mline, len(raw))
	for i, line := range raw {
		lines[i] = mline{s: line}
	}
	joined, _ := joinLines(lines, width, nil)
	return joined
}

// joinLines is Join over lines that may carry the screen cell of every
// byte (mline.at), so ExtractSpans can map a match in the joined text back
// to the screen. wrapped, when given, says which rows continue on the next
// (a terminal's soft-wrap flags) in place of the width rule.
func joinLines(lines []mline, width int, wrapped []bool) (string, []Cell) {
	out := make([]mline, 0, len(lines))
	var cur mline
	joining := false
	for i, line := range lines {
		line = line.trimRight("\r")
		if joining {
			cur = cur.concat(line)
		} else {
			cur = line
		}
		joining = false
		switch {
		case wrapped != nil:
			joining = i+1 < len(lines) && i < len(wrapped) && wrapped[i]
		// Exactly the width: a wider line was written at another width
		// (history from before a resize) and is left alone.
		case width > 0 && i+1 < len(lines) && ansi.StringWidth(line.s) == width:
			next := lines[i+1].s
			joining = next != "" && !strings.HasSuffix(line.s, " ") && !strings.HasPrefix(next, " ")
		}
		if !joining {
			out = append(out, cur)
		}
	}
	out = unwrap(out)
	var b strings.Builder
	var at []Cell
	track := len(lines) > 0 && lines[0].at != nil
	for i, line := range out {
		if i > 0 {
			b.WriteByte('\n')
			if track {
				at = append(at, Cell{Row: -1})
			}
		}
		b.WriteString(line.s)
		if track {
			at = append(at, line.at...)
		}
	}
	return b.String(), at
}

// unwrap repairs URLs split by hard terminal wraps (matomeru-old's Unwrap).
func unwrap(lines []mline) []mline {
	for i := 0; i+1 < len(lines); i++ {
		line := lines[i].trimRight("\r ")
		start := strings.LastIndex(line.s, "https://")
		if start < 0 {
			start = strings.LastIndex(line.s, "http://")
		}
		if start < 0 || strings.ContainsAny(line.s[start:], " \t<>`\"") {
			continue
		}
		next := lines[i+1].trimSpace()
		if next.s == "" || strings.ContainsAny(next.s, " \t<>") || (!strings.HasSuffix(line.s, "/") && !strings.HasSuffix(line.s, "-") && !strings.HasPrefix(next.s, "/") && !strings.HasPrefix(next.s, "?")) {
			continue
		}
		lines[i+1] = line.concat(next)
		lines[i] = mline{}
	}
	return lines
}

// mline is a line of text and, when tracked, the screen cell each of its
// bytes was drawn in (every byte of a rune holds the rune's cell, End one
// past its last column).
type mline struct {
	s  string
	at []Cell
}

func (l mline) trimRight(cutset string) mline {
	n := len(strings.TrimRight(l.s, cutset))
	return l.cut(0, n)
}

func (l mline) trimSpace() mline {
	end := len(strings.TrimRightFunc(l.s, unicode.IsSpace))
	start := end - len(strings.TrimLeftFunc(l.s[:end], unicode.IsSpace))
	return l.cut(start, end)
}

func (l mline) cut(start, end int) mline {
	out := mline{s: l.s[start:end]}
	if l.at != nil {
		out.at = l.at[start:end:end]
	}
	return out
}

func (l mline) concat(o mline) mline {
	out := mline{s: l.s + o.s}
	if l.at != nil || o.at != nil {
		out.at = append(append(make([]Cell, 0, len(out.s)), l.at...), o.at...)
	}
	return out
}

// span is a byte range of the joined text an item already claimed.
type span [2]int

func overlaps(spans []span, at [2]int) bool {
	for _, s := range spans {
		if at[0] < s[1] && s[0] < at[1] {
			return true
		}
	}
	return false
}

// Extract finds the items in text, printed by source: width is the pane's
// columns for joining soft wraps (0: none). Paths resolve against
// source.Cwd and are checked on disk (Exists); each kind and text appears
// once. Owner fields are left for Merge.
func Extract(text string, width int, source Source) []Item {
	text = Join(text, width)
	var result []Item
	seen := map[string]bool{}
	add := func(kind, value string, exists *bool) {
		value = strings.TrimSpace(value)
		k := itemKey(kind, value)
		if value == "" || seen[k] {
			return
		}
		seen[k] = true
		result = append(result, newItem(kind, value, exists, source))
	}
	if kind := source.EntryKind; kind == "prompt" || kind == "command" {
		add(kind, text, nil)
	}
	for _, m := range find(text, source.Cwd, os.Stat) {
		add(m.kind, m.value, m.exists)
	}
	return result
}

func newItem(kind, value string, exists *bool, source Source) Item {
	return Item{Kind: kind, Text: value, Session: source.Session, Window: source.Window, Pane: source.Pane, Created: source.At, Sources: []Source{source}, Exists: exists}
}

// itemKey is what makes two items the same: kind and normalized text.
func itemKey(kind, value string) string {
	return kind + "\x00" + normalize(kind, value)
}

// StatFunc reports on a path as os.Stat does. Extract uses os.Stat;
// ExtractSpans takes one so a caller on the UI loop can pass a cache
// (CachedStat).
type StatFunc func(path string) (fs.FileInfo, error)

// found is one match in the joined text: its kind, the item text (a path
// resolved and cleaned), and the byte range [start, end) of the text it
// was read from.
type found struct {
	kind, value string
	start, end  int
	exists      *bool
}

// find is the one matcher Extract and ExtractSpans share. Matches come in
// pattern order (links, PR references, branches, paths, commits); a later
// pattern never takes text an earlier one claimed.
func find(text, cwd string, stat StatFunc) []found {
	var out []found
	var claimed []span
	for _, at := range linkPattern.FindAllStringIndex(text, -1) {
		value := strings.TrimRight(text[at[0]:at[1]], ".,;:!'")
		for strings.HasSuffix(value, ")") && strings.Count(value, ")") > strings.Count(value, "(") {
			value = strings.TrimSuffix(value, ")")
		}
		value = strings.TrimRight(value, "]}")
		claimed = append(claimed, span{at[0], at[1]})
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" {
			continue
		}
		out = append(out, found{kind: linkKind(parsed), value: value, start: at[0], end: at[0] + len(value)})
	}
	for _, at := range prRef.FindAllStringIndex(text, -1) {
		if overlaps(claimed, [2]int{at[0], at[1]}) {
			continue
		}
		claimed = append(claimed, span{at[0], at[1]})
		out = append(out, found{kind: "pr", value: text[at[0]:at[1]], start: at[0], end: at[1]})
	}
	for _, p := range branchPatterns {
		for _, m := range p.FindAllStringSubmatchIndex(text, -1) {
			if overlaps(claimed, [2]int{m[2], m[3]}) {
				continue
			}
			name := strings.TrimRight(text[m[2]:m[3]], ".,:;)/")
			if name == "" || name == "HEAD" {
				continue
			}
			claimed = append(claimed, span{m[2], m[3]})
			out = append(out, found{kind: "branch", value: name, start: m[2], end: m[2] + len(name)})
		}
	}
	for _, at := range pathPattern.FindAllStringIndex(text, -1) {
		if overlaps(claimed, [2]int{at[0], at[1]}) {
			continue
		}
		raw := trimPath(text[at[0]:at[1]])
		if raw == "" {
			continue
		}
		kind, path, exists, ok := classifyPath(raw, cwd, stat)
		if !ok {
			continue
		}
		claimed = append(claimed, span{at[0], at[1]})
		out = append(out, found{kind: kind, value: path, start: at[0], end: at[0] + len(raw), exists: &exists})
	}
	for _, at := range commitPattern.FindAllStringIndex(text, -1) {
		if commitAt(text, at) && !overlaps(claimed, [2]int{at[0], at[1]}) {
			out = append(out, found{kind: "commit", value: text[at[0]:at[1]], start: at[0], end: at[1]})
		}
	}
	return out
}

// linkKind is pr for a GitHub pull request or issue, or a GitLab merge
// request; link for any other URL.
func linkKind(u *url.URL) string {
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if u.Hostname() == "github.com" && len(parts) >= 4 && (parts[2] == "pull" || parts[2] == "issues") {
		return "pr"
	}
	if strings.Contains(u.Path, "/-/merge_requests/") {
		return "pr"
	}
	return "link"
}

// trimPath cuts what follows a path in prose: a fragment or escape (#,
// a backslash), a Markdown link's target, a location (:12, :1-7,565) and trailing punctuation or
// brackets. A truncated (…) or glob path ({a,b}, *) names nothing and is
// dropped ("").
func trimPath(raw string) string {
	if strings.ContainsAny(raw, "…*{") {
		return ""
	}
	if cut := strings.IndexAny(raw, "#\\"); cut >= 0 {
		raw = raw[:cut]
	}
	// A Markdown link, [path](path): the label is the path.
	if cut := strings.Index(raw, "]("); cut >= 0 {
		raw = raw[:cut]
	}
	if slash := strings.LastIndex(raw, "/"); slash >= 0 {
		if colon := strings.Index(raw[slash:], ":"); colon >= 0 {
			raw = raw[:slash+colon]
		}
	} else if colon := strings.Index(raw, ":"); colon >= 0 {
		raw = raw[:colon]
	}
	return strings.TrimRightFunc(raw, func(r rune) bool {
		return !(r < 128 && (r == '/' || r == '_' || r == '-' || r == '~' || r == '+' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'))
	})
}

// classifyPath decides whether raw names a path: an existing file or
// directory, or an explicit path (absolute with two segments or more, ~/,
// ./, ../) that does not exist yet. A bare dotted word counts only when it
// exists, so prose is not a path; a relative word with a slash that does
// not exist (origin/main, owner/repo) is not one either.
func classifyPath(raw, cwd string, stat StatFunc) (kind, path string, exists, ok bool) {
	path = locationSuffix.ReplaceAllString(raw, "")
	if strings.Trim(path, "./~") == "" { // ./..., .., / name no file
		return "", "", false, false
	}
	explicit := strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../") ||
		strings.HasPrefix(path, "/") && strings.Contains(strings.Trim(path, "/"), "/")
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	if !filepath.IsAbs(path) && cwd != "" {
		path = filepath.Join(cwd, path)
	}
	info, err := stat(path)
	exists = err == nil
	if !exists && !explicit {
		return "", "", false, false
	}
	// A missing absolute path whose top folder does not exist either
	// (/eval/browser, /debug/pprof) is a URL path or prose, not a file.
	if !exists && filepath.IsAbs(path) {
		top := "/" + strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)[0]
		if _, err := stat(top); err != nil {
			return "", "", false, false
		}
	}
	kind = "file"
	if strings.HasSuffix(raw, "/") || exists && info.IsDir() {
		kind = "dir"
	}
	return kind, filepath.Clean(path), exists, true
}

// commitAt reports whether the hex run at is a commit SHA: 7 or 9 to 12
// characters (an abbreviation; 8 is left out, it is how TUIOS and mtm
// shorten window and session ids) or 40 (a full SHA), holding a digit and a
// letter, standing alone as a word (not part of a uuid, a path, a file
// name or a longer word).
func commitAt(text string, at []int) bool {
	sha := text[at[0]:at[1]]
	if n := len(sha); n == 8 || n > 12 && n != 40 {
		return false
	}
	if !strings.ContainsAny(sha, "0123456789") || !strings.ContainsAny(sha, "abcdef") {
		return false
	}
	if at[0] > 0 && strings.ContainsRune("0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_./:#@-", rune(text[at[0]-1])) {
		return false
	}
	if at[1] < len(text) {
		next := text[at[1]]
		if strings.ContainsRune("0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ_/@-", rune(next)) {
			return false
		}
		if next == '.' && at[1]+1 < len(text) && text[at[1]+1] != ' ' && text[at[1]+1] != '\n' {
			return false
		}
	}
	return true
}

// Target is what open hands the system opener: the item's text, a PR
// reference as its GitHub URL.
func Target(it Item) string {
	if it.Kind == "pr" {
		if m := prRef.FindStringSubmatch(it.Text); m != nil && m[0] == it.Text {
			return "https://github.com/" + m[1] + "/issues/" + m[2]
		}
	}
	return it.Text
}
