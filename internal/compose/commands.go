package compose

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"ofam.dev/matomeru/internal/harness"
)

// Command is one slash command a harness takes, without its slash.
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source"` // builtin, user, project, plugin
}

// Commands are the slash commands the harness takes from dir: its
// built-ins, then the Markdown commands of its command directories (a
// file in a subdirectory also as dir:name, the way Claude Code and omp
// name it), then (Claude) every enabled plugin's as plugin:name.
func (s *Sources) Commands(_ context.Context, h harness.Harness, dir string) []Command {
	key := h.Name + "\x00" + dir
	c := profile(h)
	roots := s.commandRoots(c, dir)
	print := fingerprint(s.commandInputs(c, dir))
	s.mu.Lock()
	got, ok := s.commands[key]
	s.mu.Unlock()
	if ok && got.print == print {
		return got.value
	}
	seen := map[string]bool{}
	var out []Command
	add := func(c Command) {
		if c.Name == "" || seen[c.Name] {
			return
		}
		seen[c.Name] = true
		out = append(out, c)
	}
	for _, name := range c.Commands {
		add(Command{Name: name, Source: "builtin"})
	}
	for _, r := range roots {
		_ = filepath.WalkDir(r.dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
				return nil
			}
			rel, _ := filepath.Rel(r.dir, path)
			rel = strings.TrimSuffix(filepath.ToSlash(rel), ".md")
			meta, _ := frontmatter(path)
			name := filepath.Base(rel)
			if r.plugin != "" {
				name = r.plugin + ":" + name
			}
			add(Command{Name: name, Description: meta["description"], Source: r.scope})
			if strings.Contains(rel, "/") && r.plugin == "" {
				add(Command{Name: strings.ReplaceAll(rel, "/", ":"), Description: meta["description"], Source: r.scope})
			}
			return nil
		})
	}
	s.mu.Lock()
	s.commands[key] = cached[[]Command]{print: print, at: s.now(), value: out}
	s.mu.Unlock()
	return out
}

// commandInputs are the paths whose mtimes say the commands may have
// changed: every root, and the config files that add or drop roots.
func (s *Sources) commandInputs(c harness.Compose, dir string) []string {
	inputs := []string{}
	for _, r := range s.commandRoots(c, dir) {
		inputs = append(inputs, r.dir)
	}
	for _, in := range c.Inputs {
		inputs = append(inputs, s.path(in))
	}
	return inputs
}

// commandRoots are the harness's command directories seen from dir.
func (s *Sources) commandRoots(c harness.Compose, dir string) []skillRoot {
	return s.roots(c, c.CommandDirs, dir)
}

// runCommand runs a program and returns its standard output.
func runCommand(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = nil
	return cmd.Output()
}
