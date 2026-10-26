package contextitems

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

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
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	cur, joining := "", false
	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		if joining {
			cur += line
		} else {
			cur = line
		}
		joining = false
		// Exactly the width: a wider line was written at another width
		// (history from before a resize) and is left alone.
		if width > 0 && i+1 < len(lines) && ansi.StringWidth(line) == width {
			next := lines[i+1]
			joining = next != "" && !strings.HasSuffix(line, " ") && !strings.HasPrefix(next, " ")
		}
		if !joining {
			out = append(out, cur)
		}
	}
	return unwrap(strings.Join(out, "\n"))
}

// unwrap repairs URLs split by hard terminal wraps (matomeru-old's Unwrap).
func unwrap(text string) string {
	lines := strings.Split(text, "\n")
	for i := 0; i+1 < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r ")
		start := strings.LastIndex(line, "https://")
		if start < 0 {
			start = strings.LastIndex(line, "http://")
		}
		if start < 0 || strings.ContainsAny(line[start:], " \t<>`\"") {
			continue
		}
		next := strings.TrimSpace(lines[i+1])
		if next == "" || strings.ContainsAny(next, " \t<>") || (!strings.HasSuffix(line, "/") && !strings.HasSuffix(line, "-") && !strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "?")) {
			continue
		}
		lines[i+1] = line + next
		lines[i] = ""
	}
	return strings.Join(lines, "\n")
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
		k := kind + "\x00" + normalize(kind, value)
		if value == "" || seen[k] {
			return
		}
		seen[k] = true
		result = append(result, Item{Kind: kind, Text: value, Session: source.Session, Window: source.Window, Pane: source.Pane, Created: source.At, Sources: []Source{source}, Exists: exists})
	}
	if kind := source.EntryKind; kind == "prompt" || kind == "command" {
		add(kind, text, nil)
	}
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
		add(linkKind(parsed), value, nil)
	}
	for _, at := range prRef.FindAllStringIndex(text, -1) {
		if overlaps(claimed, [2]int{at[0], at[1]}) {
			continue
		}
		claimed = append(claimed, span{at[0], at[1]})
		add("pr", text[at[0]:at[1]], nil)
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
			add("branch", name, nil)
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
		kind, path, exists, ok := classifyPath(raw, source.Cwd)
		if !ok {
			continue
		}
		claimed = append(claimed, span{at[0], at[1]})
		add(kind, path, &exists)
	}
	for _, at := range commitPattern.FindAllStringIndex(text, -1) {
		if commitAt(text, at) && !overlaps(claimed, [2]int{at[0], at[1]}) {
			add("commit", text[at[0]:at[1]], nil)
		}
	}
	return result
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
func classifyPath(raw, cwd string) (kind, path string, exists, ok bool) {
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
	info, err := os.Stat(path)
	exists = err == nil
	if !exists && !explicit {
		return "", "", false, false
	}
	// A missing absolute path whose top folder does not exist either
	// (/eval/browser, /debug/pprof) is a URL path or prose, not a file.
	if !exists && filepath.IsAbs(path) {
		top := "/" + strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)[0]
		if _, err := os.Stat(top); err != nil {
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
