package contextitems

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Statter answers StatFuncs for extraction: it remembers each path's answer
// for ttl (at most max paths; when full, the expired answers go, else all), so a screen
// extracted again every second costs no system calls for paths it saw a
// moment ago, and it does not ask the system about paths under the skip
// roots (autofs mounts such as /home, where a lookup wakes the automount
// and directory daemons). It is safe for concurrent use.
type Statter struct {
	ttl  time.Duration
	max  int
	skip []string
	now  func() time.Time
	stat StatFunc

	mu      sync.Mutex
	answers map[string]statAnswer
}

type statAnswer struct {
	info fs.FileInfo
	err  error
	at   time.Time
}

// NewStatter returns a Statter keeping up to max answers for ttl; paths
// strictly under a skip root answer ErrNotLocal without a system call (the
// root itself is still asked, so a missing top folder is told apart).
func NewStatter(ttl time.Duration, max int, skipRoots []string) *Statter {
	s := &Statter{ttl: ttl, max: max, now: time.Now, stat: os.Stat, answers: map[string]statAnswer{}}
	for _, root := range skipRoots {
		if root = filepath.Clean(root); filepath.IsAbs(root) && root != "/" {
			s.skip = append(s.skip, root)
		}
	}
	return s
}

// Local is the StatFunc for panes on this machine.
func (s *Statter) Local() StatFunc { return s.lookup }

// Remote is the StatFunc for a pane on another host: no path is looked at
// here (ErrNotLocal), since the same name on this machine is another file.
func Remote() StatFunc {
	return func(string) (fs.FileInfo, error) { return nil, ErrNotLocal }
}

func (s *Statter) lookup(path string) (fs.FileInfo, error) {
	for _, root := range s.skip {
		if strings.HasPrefix(path, root+"/") {
			return nil, ErrNotLocal
		}
	}
	now := s.now()
	s.mu.Lock()
	if a, ok := s.answers[path]; ok && now.Sub(a.at) < s.ttl {
		s.mu.Unlock()
		return a.info, a.err
	}
	s.mu.Unlock()
	info, err := s.stat(path)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.answers) >= s.max {
		for p, a := range s.answers { // drop the expired; else everything
			if now.Sub(a.at) >= s.ttl {
				delete(s.answers, p)
			}
		}
		if len(s.answers) >= s.max {
			clear(s.answers)
		}
	}
	s.answers[path] = statAnswer{info, err, now}
	return info, err
}
