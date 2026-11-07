// Package compose holds what the Turns tab's prompt box completes from:
// the files under a pane's directory (files.go), the agent's slash
// commands (commands.go) and the skills the pane's harness can see
// (this file), and how each harness spells a skill's invocation
// (syntax.go). It knows nothing of the UI; cmd/mtm turns these into
// ochazuke completion suggestions.
//
// Which skills a harness sees is the harness's own search path, data on
// its harnesses.json row (harness.Compose; the built-ins'):
//
//   - Claude Code: ~/.claude/skills (user), .claude/skills in the pane's
//     directory and each parent up to the repository root (project), and
//     the skills/ of every plugin installed_plugins.json lists that
//     settings.json's enabledPlugins turns on (user scope, or a project
//     scope whose projectPath holds the pane's directory). A plugin's
//     skill is invoked as plugin:name.
//   - omp: ~/.omp/agent/skills, ~/.omp/agent/managed-skills, ~/.agents/
//     skills, .omp/skills, .agents/skills and .claude/skills from the
//     pane's directory up, ~/.claude/skills only with
//     skills.enableClaudeUser, and skills.customDirectories, from
//     ~/.omp/agent/config.yml; none with skills.enabled false.
//   - Codex: ~/.codex/skills, ~/.agents/skills, .codex/skills and
//     .agents/skills from the pane's directory up.
//
// o-ai (~/proj/o-ai) already resolves these per harness, omp's many
// providers included, so when it is on PATH Skills asks it first
// (`o-ai skill list --harness H --project DIR --no-probe --json`, H the
// row's compose.oai) and
// drops the rows it says the harness does not load; the scan above is
// the fallback. Either answer is cached by a fingerprint of the search
// path's directories and config files (their mtimes), so a completion
// asks again only after something there changed.
package compose

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"ofam.dev/matomeru/internal/harness"
)

// Skill is one skill a harness can load.
type Skill struct {
	Name        string `json:"name"`
	Plugin      string `json:"plugin,omitempty"` // a Claude plugin's skill: invoked as plugin:name
	Description string `json:"description,omitempty"`
	Path        string `json:"path"`
	Scope       string `json:"scope"` // user, project, plugin, configured, shared
}

// Env is where the harnesses keep their files; tests point it at
// temporary directories.
type Env struct {
	Home      string
	ClaudeDir string // $CLAUDE_CONFIG_DIR, else ~/.claude
	OmpDir    string // $PI_CODING_AGENT_DIR, else ~/.omp/agent
	// OAI runs `o-ai skill list` for a harness and directory and returns
	// its JSON; nil (or an error) falls back to the scan.
	OAI func(ctx context.Context, harness, dir string) ([]byte, error)
}

// DefaultEnv is the real home's directories, with o-ai when it is on PATH.
func DefaultEnv() Env {
	home, _ := os.UserHomeDir()
	env := Env{Home: home, ClaudeDir: filepath.Join(home, ".claude"), OmpDir: filepath.Join(home, ".omp", "agent")}
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		env.ClaudeDir = dir
	}
	if dir := os.Getenv("PI_CODING_AGENT_DIR"); dir != "" {
		env.OmpDir = dir
	}
	env.OAI = runOAI
	return env
}

// Sources answers the prompt box's completions for a harness and a
// directory, caching each answer until its fingerprint changes. Safe for
// concurrent use (searches run off the loop).
type Sources struct {
	Env Env
	mu  sync.Mutex
	// skills and commands are keyed by harness and directory.
	skills   map[string]cached[[]Skill]
	commands map[string]cached[[]Command]
	files    map[string]cached[[]string]
	now      func() time.Time
}

type cached[T any] struct {
	print string
	at    time.Time
	value T
}

// NewSources answers from env.
func NewSources(env Env) *Sources {
	return &Sources{Env: env, skills: map[string]cached[[]Skill]{}, commands: map[string]cached[[]Command]{}, files: map[string]cached[[]string]{}, now: time.Now}
}

// Skills are the skills the harness sees from dir, sorted by name.
func (s *Sources) Skills(ctx context.Context, h harness.Harness, dir string) []Skill {
	key := h.Name + "\x00" + dir
	c := profile(h)
	print := fingerprint(s.skillInputs(c, dir))
	s.mu.Lock()
	got, ok := s.skills[key]
	s.mu.Unlock()
	if ok && got.print == print {
		return got.value
	}
	var skills []Skill
	if s.Env.OAI != nil && c.OAI != "" {
		if out, err := s.Env.OAI(ctx, c.OAI, dir); err == nil {
			skills, ok = ParseOAISkills(out)
		} else {
			ok = false
		}
	} else {
		ok = false
	}
	if !ok {
		skills = s.scanSkills(c, dir)
	}
	s.mu.Lock()
	s.skills[key] = cached[[]Skill]{print: print, at: s.now(), value: skills}
	s.mu.Unlock()
	return skills
}

// runOAI is `o-ai skill list` for the harness and directory.
func runOAI(ctx context.Context, harness, dir string) ([]byte, error) {
	return runCommand(ctx, 20*time.Second, "o-ai", "skill", "list", "--harness", harness, "--project", dir, "--no-probe", "--json")
}

// ParseOAISkills reads o-ai's skill rows ({id, label, data}), keeping the
// ones the harness loads: a row o-ai marks disabled, an opt-in the
// harness lacks or a broken frontmatter is left out. A Claude plugin's
// skill gets its plugin's name from its install path.
func ParseOAISkills(out []byte) ([]Skill, bool) {
	var rows []struct {
		Data struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Path        string `json:"path"`
			Scope       string `json:"scope"`
			Access      string `json:"access"`
		} `json:"data"`
	}
	if json.Unmarshal(out, &rows) != nil {
		return nil, false
	}
	seen := map[string]bool{}
	var skills []Skill
	for _, r := range rows {
		d := r.Data
		access := strings.ToLower(d.Access)
		if d.Name == "" || strings.Contains(access, "disabled") || strings.Contains(access, "requires") || strings.Contains(access, "invalid") {
			continue
		}
		sk := Skill{Name: d.Name, Description: d.Description, Path: d.Path, Scope: d.Scope}
		if strings.HasPrefix(d.Scope, "plugin") {
			sk.Plugin = pluginOfPath(d.Path)
		}
		key := sk.Plugin + ":" + sk.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		skills = append(skills, sk)
	}
	sortSkills(skills)
	return skills, true
}

// pluginOfPath is the plugin a cached plugin file belongs to:
// …/plugins/cache/<marketplace>/<plugin>/<version>/….
func pluginOfPath(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i := 0; i+3 < len(parts); i++ {
		if parts[i] == "plugins" && parts[i+1] == "cache" {
			return parts[i+3]
		}
	}
	return ""
}

func sortSkills(skills []Skill) {
	sort.SliceStable(skills, func(i, j int) bool {
		if skills[i].Name != skills[j].Name {
			return skills[i].Name < skills[j].Name
		}
		return skills[i].Plugin < skills[j].Plugin
	})
}

// skillRoot is one directory of skill directories.
type skillRoot struct{ dir, scope, plugin string }

// profile is the harness's compose row; an empty one when it has none.
func profile(h harness.Harness) harness.Compose {
	if h.Compose == nil {
		return harness.Compose{}
	}
	return *h.Compose
}

// skillRoots are the harness's skill directories seen from dir.
func (s *Sources) skillRoots(c harness.Compose, dir string) []skillRoot {
	return s.roots(c, c.Skills, dir)
}

// roots are the directories of a Compose list seen from dir, in order:
// an up group takes each of its entries at dir and every parent up to
// the repository root; a plugins entry the folder in every plugin the
// Claude Code config directory enables; a list entry the paths a config
// key holds (relative to dir).
func (s *Sources) roots(c harness.Compose, dirs []harness.Dir, dir string) []skillRoot {
	e := s.Env
	cfg := readConfig(s.path(c.Config))
	var roots []skillRoot
	for _, d := range dirs {
		if !cfg.holds(d.If) {
			continue
		}
		switch {
		case len(d.Up) > 0:
			for _, a := range ancestors(dir, e.Home) {
				for _, u := range d.Up {
					if cfg.holds(u.If) {
						roots = append(roots, skillRoot{filepath.Join(a, u.Path), u.Scope, ""})
					}
				}
			}
		case d.Plugins != "":
			for _, p := range claudePlugins(s.path(d.Plugins), dir) {
				roots = append(roots, skillRoot{filepath.Join(p.root, d.Path), d.Scope, p.name})
			}
		case d.List != "":
			for _, p := range cfg.strings(d.List) {
				roots = append(roots, skillRoot{expand(p, e.Home, dir), d.Scope, ""})
			}
		default:
			roots = append(roots, skillRoot{s.path(d.Path), d.Scope, ""})
		}
	}
	return roots
}

// path is a Compose path with ~, {claudeDir} and {ompDir} replaced by
// the Env's directories.
func (s *Sources) path(p string) string {
	switch {
	case p == "":
		return ""
	case p == "~" || strings.HasPrefix(p, "~/"):
		return filepath.Join(s.Env.Home, p[1:])
	}
	p = strings.ReplaceAll(p, "{claudeDir}", s.Env.ClaudeDir)
	p = strings.ReplaceAll(p, "{ompDir}", s.Env.OmpDir)
	return filepath.Clean(p)
}

// skillInputs are the paths whose mtimes say the skills may have changed:
// every root, and the config files that add or drop roots.
func (s *Sources) skillInputs(c harness.Compose, dir string) []string {
	var paths []string
	for _, r := range s.skillRoots(c, dir) {
		paths = append(paths, r.dir)
	}
	for _, in := range c.Inputs {
		paths = append(paths, s.path(in))
	}
	return paths
}

// scanSkills reads each root's <name>/SKILL.md.
func (s *Sources) scanSkills(c harness.Compose, dir string) []Skill {
	seen := map[string]bool{}
	var skills []Skill
	for _, r := range s.skillRoots(c, dir) {
		entries, err := os.ReadDir(r.dir)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			path := filepath.Join(r.dir, ent.Name(), "SKILL.md")
			meta, err := frontmatter(path)
			if err != nil {
				continue
			}
			name := meta["name"]
			if name == "" {
				name = ent.Name()
			}
			key := r.plugin + ":" + name
			if seen[key] {
				continue // a nearer root's skill of the same name wins
			}
			seen[key] = true
			skills = append(skills, Skill{Name: name, Plugin: r.plugin, Description: meta["description"], Path: path, Scope: r.scope})
		}
	}
	sortSkills(skills)
	return skills
}

// claudePlugin is an enabled plugin's install root.
type claudePlugin struct{ name, root string }

// claudePlugins are the plugins installed_plugins.json lists that
// settings.json enables, for a pane in dir.
func claudePlugins(claudeDir, dir string) []claudePlugin {
	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if b, err := os.ReadFile(filepath.Join(claudeDir, "settings.json")); err == nil {
		_ = json.Unmarshal(b, &settings)
	}
	var installed struct {
		Plugins map[string][]struct {
			Scope       string `json:"scope"`
			InstallPath string `json:"installPath"`
			ProjectPath string `json:"projectPath"`
		} `json:"plugins"`
	}
	b, err := os.ReadFile(filepath.Join(claudeDir, "plugins", "installed_plugins.json"))
	if err != nil || json.Unmarshal(b, &installed) != nil {
		return nil
	}
	var out []claudePlugin
	for key, records := range installed.Plugins {
		if !settings.EnabledPlugins[key] {
			continue
		}
		name, _, _ := strings.Cut(key, "@")
		for _, r := range records {
			if r.InstallPath == "" {
				continue
			}
			if r.ProjectPath != "" && dir != r.ProjectPath && !strings.HasPrefix(dir, r.ProjectPath+string(filepath.Separator)) {
				continue
			}
			out = append(out, claudePlugin{name: name, root: r.InstallPath})
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// config is a Compose row's settings file, parsed (YAML, so JSON too);
// nil when there is none or it does not parse.
type config map[string]any

func readConfig(path string) config {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]any
	if yaml.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

// value is the dotted key's value; false when it is not set.
func (c config) value(key string) (any, bool) {
	var v any = map[string]any(c)
	for _, part := range strings.Split(key, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, false
		}
		if v, ok = m[part]; !ok {
			return nil, false
		}
	}
	return v, v != nil
}

// holds reports whether every condition holds: "key" (the key is true),
// "key=value" or "key!=value" (an unset key equals nothing).
func (c config) holds(conds []string) bool {
	for _, cond := range conds {
		key, want, neg := cond, "", false
		if k, v, ok := strings.Cut(cond, "!="); ok {
			key, want, neg = k, v, true
		} else if k, v, ok := strings.Cut(cond, "="); ok {
			key, want = k, v
		} else {
			want = "true"
		}
		v, set := c.value(strings.TrimSpace(key))
		equal := set && fmt.Sprint(v) == strings.TrimSpace(want)
		if equal == neg {
			return false
		}
	}
	return true
}

// strings are the key's list of strings (other items skipped).
func (c config) strings(key string) []string {
	v, _ := c.value(key)
	list, _ := v.([]any)
	var out []string
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// ancestors are dir and its parents up to the repository root (the first
// holding .git), never home or above it.
func ancestors(dir, home string) []string {
	if dir == "" {
		return nil
	}
	var out []string
	for d := filepath.Clean(dir); d != home && d != filepath.Dir(d); d = filepath.Dir(d) {
		out = append(out, d)
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			break
		}
	}
	return out
}

func expand(path, home, base string) string {
	switch {
	case path == "~":
		return home
	case strings.HasPrefix(path, "~/"):
		return filepath.Join(home, path[2:])
	case filepath.IsAbs(path):
		return path
	}
	return filepath.Join(base, path)
}

// frontmatter is a Markdown file's leading --- block as key: value pairs
// (one line each; quotes dropped). A file without one has none.
func frontmatter(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	meta := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return meta, nil
	}
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		meta[strings.TrimSpace(k)] = v
	}
	return meta, nil
}

// fingerprint is the paths' mtimes and sizes, "-" for a missing one.
func fingerprint(paths []string) string {
	var b strings.Builder
	for _, p := range slices.Compact(slices.Clone(paths)) {
		if info, err := os.Stat(p); err == nil {
			fmt.Fprintf(&b, "%s=%d.%d;", p, info.ModTime().UnixNano(), info.Size())
		} else {
			fmt.Fprintf(&b, "%s=-;", p)
		}
	}
	return b.String()
}
