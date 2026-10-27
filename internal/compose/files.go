package compose

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// File listing: the files under a directory, relative to it, with every
// directory that holds one (ending in "/"). In a Git work tree the list is
// git ls-files' (tracked and untracked, ignored ones left out); elsewhere
// a walk that skips hidden directories and stops at maxFiles. A listing is
// kept filesTTL and while the directory's (and its Git index's) mtime
// stands, so a completion lists again only after the tree changed.
const (
	maxFiles = 20000
	filesTTL = 30 * time.Second
)

// skipDirs are directories a walk never enters.
var skipDirs = map[string]bool{"node_modules": true, "vendor": true, "target": true, "dist": true, "build": true, "__pycache__": true}

// Files lists dir's files and directories, relative to it.
func (s *Sources) Files(ctx context.Context, dir string) []string {
	if dir == "" {
		return nil
	}
	inputs := []string{dir, filepath.Join(dir, ".git", "index")}
	print := fingerprint(inputs)
	s.mu.Lock()
	c, ok := s.files[dir]
	s.mu.Unlock()
	if ok && c.print == print && s.now().Sub(c.at) < filesTTL {
		return c.value
	}
	list := gitFiles(ctx, dir)
	if list == nil {
		list = walkFiles(dir)
	}
	list = withDirs(list)
	s.mu.Lock()
	s.files[dir] = cached[[]string]{print: print, at: s.now(), value: list}
	s.mu.Unlock()
	return list
}

// gitFiles is git ls-files' listing under dir, nil outside a work tree.
func gitFiles(ctx context.Context, dir string) []string {
	out, err := runCommand(ctx, 10*time.Second, "git", "-C", dir, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil
	}
	var list []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			list = append(list, p)
		}
		if len(list) >= maxFiles {
			break
		}
	}
	if list == nil {
		list = []string{}
	}
	return list
}

func walkFiles(dir string) []string {
	list := []string{}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == dir {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") || skipDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err == nil {
			list = append(list, filepath.ToSlash(rel))
		}
		if len(list) >= maxFiles {
			return fs.SkipAll
		}
		return nil
	})
	return list
}

// withDirs adds each listed file's directories, sorted.
func withDirs(files []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f)
		for d := filepath.Dir(f); d != "." && d != "/" && !seen[d]; d = filepath.Dir(d) {
			seen[d] = true
			out = append(out, d+"/")
		}
	}
	sort.Strings(out)
	return out
}

// Match is one ranked candidate.
type Match struct {
	Text  string
	Index int // the candidate's index in what Rank was given
	score int
}

// Rank orders the candidates query matches, best first, at most limit: a
// candidate matches when the query's runes appear in it in order, case
// folded. A match at the start of the candidate's last path element beats
// one inside it, a whole substring beats scattered runes, and among equals
// the shorter candidate leads. An empty query keeps the given order.
func Rank(query string, candidates []string, limit int) []Match {
	q := []rune(strings.ToLower(query))
	var out []Match
	for i, c := range candidates {
		score, ok := matchScore(q, c)
		if !ok {
			continue
		}
		out = append(out, Match{Text: c, Index: i, score: score})
	}
	if len(q) > 0 {
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].score != out[j].score {
				return out[i].score < out[j].score
			}
			return len(out[i].Text) < len(out[j].Text)
		})
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// matchScore is lower for a better match.
func matchScore(q []rune, candidate string) (int, bool) {
	if len(q) == 0 {
		return 0, true
	}
	lower := strings.ToLower(candidate)
	query := string(q)
	base := strings.ToLower(filepath.Base(strings.TrimSuffix(candidate, "/")))
	switch {
	case strings.HasPrefix(base, query):
		return 0, true
	case strings.HasPrefix(lower, query):
		return 1, true
	case strings.Contains(base, query):
		return 2, true
	case strings.Contains(lower, query):
		return 3, true
	}
	// Scattered: the runes in order; fewer gaps rank higher.
	gaps, k, last := 0, 0, -1
	for i, r := range []rune(lower) {
		if k < len(q) && r == q[k] {
			if last >= 0 && i != last+1 {
				gaps++
			}
			last, k = i, k+1
		}
	}
	if k < len(q) {
		return 0, false
	}
	return 10 + gaps, true
}
