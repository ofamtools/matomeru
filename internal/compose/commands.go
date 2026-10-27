package compose

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Command is one slash command a harness takes, without its slash.
type Command struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source"` // builtin, user, project, plugin
}

// The harnesses' own commands. Claude Code's are its documented built-ins
// (2.1.x); omp's are read from its slash-command registry
// (src/slash-commands/builtin-*.ts, 18.13.0); Codex's from its TUI.
var builtinCommands = map[string][]string{
	Claude: {"add-dir", "agents", "bug", "clear", "compact", "config", "context", "cost", "doctor", "exit", "export", "help", "hooks", "init", "login", "logout", "mcp", "memory", "model", "permissions", "plugin", "pr-comments", "release-notes", "resume", "review", "rewind", "security-review", "status", "statusline", "todos", "usage", "vim"},
	Omp:    {"add-dir", "advisor", "agents", "branch", "btw", "changelog", "clear", "collab", "compact", "context", "copy", "debug", "delete", "dirs", "dump", "effort", "exit", "export", "extensions", "fast", "fork", "fresh", "git", "goal", "handoff", "hotkeys", "jobs", "login", "logout", "loop", "marketplace", "mcp", "memory", "model", "move", "new", "open", "plan", "plugins", "queue", "quit", "reload-plugins", "rename", "restart", "resume", "retry", "session", "settings", "setup", "share", "skills", "slow", "stats", "switch", "todo", "tools", "trace", "tree", "usage"},
	Codex:  {"approvals", "compact", "diff", "init", "logout", "mcp", "mention", "model", "new", "quit", "review", "status"},
}

// Commands are the slash commands the harness takes from dir: its
// built-ins, then the Markdown commands of its command directories (a
// file in a subdirectory also as dir:name, the way Claude Code and omp
// name it), then (Claude) every enabled plugin's as plugin:name.
func (s *Sources) Commands(_ context.Context, harness, dir string) []Command {
	key := harness + "\x00" + dir
	roots := s.commandRoots(harness, dir)
	inputs := []string{}
	for _, r := range roots {
		inputs = append(inputs, r.dir)
	}
	switch harness {
	case Claude:
		inputs = append(inputs, filepath.Join(s.Env.ClaudeDir, "settings.json"), filepath.Join(s.Env.ClaudeDir, "plugins", "installed_plugins.json"))
	case Omp:
		inputs = append(inputs, filepath.Join(s.Env.OmpDir, "config.yml"))
	}
	print := fingerprint(inputs)
	s.mu.Lock()
	c, ok := s.commands[key]
	s.mu.Unlock()
	if ok && c.print == print {
		return c.value
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
	for _, name := range builtinCommands[harness] {
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

// commandRoots are the harness's command directories seen from dir.
func (s *Sources) commandRoots(harness, dir string) []skillRoot {
	e := s.Env
	up := ancestors(dir, e.Home)
	var roots []skillRoot
	switch harness {
	case Claude:
		for _, d := range up {
			roots = append(roots, skillRoot{filepath.Join(d, ".claude", "commands"), "project", ""})
		}
		roots = append(roots, skillRoot{filepath.Join(e.ClaudeDir, "commands"), "user", ""})
		for _, p := range claudePlugins(e.ClaudeDir, dir) {
			roots = append(roots, skillRoot{filepath.Join(p.root, "commands"), "plugin", p.name})
		}
	case Omp:
		cfg := ompConfig(e.OmpDir)
		for _, d := range up {
			roots = append(roots, skillRoot{filepath.Join(d, ".omp", "commands"), "project", ""})
			if cfg.Commands.EnableClaudeProject == nil || *cfg.Commands.EnableClaudeProject {
				roots = append(roots, skillRoot{filepath.Join(d, ".claude", "commands"), "project", ""})
			}
		}
		roots = append(roots, skillRoot{filepath.Join(e.OmpDir, "commands"), "user", ""})
		if cfg.Commands.EnableClaudeUser {
			roots = append(roots, skillRoot{filepath.Join(e.ClaudeDir, "commands"), "user", ""})
		}
	}
	return roots
}

// runCommand runs a program and returns its standard output.
func runCommand(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = nil
	return cmd.Output()
}
