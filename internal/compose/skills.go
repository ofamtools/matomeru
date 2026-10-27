// Package compose holds what the Turns tab's prompt box completes from:
// the files under a pane's directory (files.go), the agent's slash
// commands (commands.go) and the skills the pane's harness can see
// (this file), and how each harness spells a skill's invocation
// (syntax.go). It knows nothing of the UI; cmd/mtm turns these into
// ochazuke completion suggestions.
//
// Which skills a harness sees is the harness's own search path:
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
// (`o-ai skill list --harness H --project DIR --no-probe --json`) and
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
)

// Harnesses, as compose names them (Harness maps tuios's ids).
const (
	Claude = "claude"
	Omp    = "omp"
	Codex  = "codex"
)

// Harness is compose's name for a tuios harness id ("claude-code" is
// Claude), "" for a harness it does not know.
func Harness(id string) string {
	switch {
	case strings.HasPrefix(id, "claude"):
		return Claude
	case id == "omp":
		return Omp
	case strings.HasPrefix(id, "codex"):
		return Codex
	}
	return ""
}

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
func (s *Sources) Skills(ctx context.Context, harness, dir string) []Skill {
	key := harness + "\x00" + dir
	print := fingerprint(s.skillInputs(harness, dir))
	s.mu.Lock()
	c, ok := s.skills[key]
	s.mu.Unlock()
	if ok && c.print == print {
		return c.value
	}
	var skills []Skill
	if s.Env.OAI != nil && harness != "" {
		if out, err := s.Env.OAI(ctx, harness, dir); err == nil {
			skills, ok = ParseOAISkills(out)
		} else {
			ok = false
		}
	} else {
		ok = false
	}
	if !ok {
		skills = s.scanSkills(harness, dir)
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

// skillRoots are the harness's skill directories seen from dir.
func (s *Sources) skillRoots(harness, dir string) []skillRoot {
	e := s.Env
	up := ancestors(dir, e.Home)
	var roots []skillRoot
	switch harness {
	case Claude:
		roots = append(roots, skillRoot{filepath.Join(e.ClaudeDir, "skills"), "user", ""})
		for _, d := range up {
			roots = append(roots, skillRoot{filepath.Join(d, ".claude", "skills"), "project", ""})
		}
		for _, p := range claudePlugins(e.ClaudeDir, dir) {
			roots = append(roots, skillRoot{filepath.Join(p.root, "skills"), "plugin", p.name})
		}
	case Omp:
		cfg := ompConfig(e.OmpDir)
		if cfg.Skills.Enabled != nil && !*cfg.Skills.Enabled {
			return nil
		}
		roots = append(roots,
			skillRoot{filepath.Join(e.OmpDir, "skills"), "user", ""},
			skillRoot{filepath.Join(e.OmpDir, "managed-skills"), "managed", ""},
			skillRoot{filepath.Join(e.Home, ".agents", "skills"), "shared", ""})
		for _, d := range up {
			roots = append(roots,
				skillRoot{filepath.Join(d, ".omp", "skills"), "project", ""},
				skillRoot{filepath.Join(d, ".agents", "skills"), "project", ""},
				skillRoot{filepath.Join(d, ".claude", "skills"), "project", ""})
		}
		if cfg.Skills.EnableClaudeUser {
			roots = append(roots, skillRoot{filepath.Join(e.ClaudeDir, "skills"), "user", ""})
		}
		for _, c := range cfg.Skills.CustomDirectories {
			roots = append(roots, skillRoot{expand(c, e.Home, dir), "configured", ""})
		}
	case Codex:
		roots = append(roots,
			skillRoot{filepath.Join(e.Home, ".codex", "skills"), "user", ""},
			skillRoot{filepath.Join(e.Home, ".agents", "skills"), "shared", ""})
		for _, d := range up {
			roots = append(roots,
				skillRoot{filepath.Join(d, ".codex", "skills"), "project", ""},
				skillRoot{filepath.Join(d, ".agents", "skills"), "project", ""})
		}
	}
	return roots
}

// skillInputs are the paths whose mtimes say the skills may have changed:
// every root, and the config files that add or drop roots.
func (s *Sources) skillInputs(harness, dir string) []string {
	var paths []string
	for _, r := range s.skillRoots(harness, dir) {
		paths = append(paths, r.dir)
	}
	switch harness {
	case Claude:
		paths = append(paths, filepath.Join(s.Env.ClaudeDir, "settings.json"), filepath.Join(s.Env.ClaudeDir, "plugins", "installed_plugins.json"))
	case Omp:
		paths = append(paths, filepath.Join(s.Env.OmpDir, "config.yml"))
	}
	return paths
}

// scanSkills reads each root's <name>/SKILL.md.
func (s *Sources) scanSkills(harness, dir string) []Skill {
	seen := map[string]bool{}
	var skills []Skill
	for _, r := range s.skillRoots(harness, dir) {
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

// ompSettings is the part of omp's config.yml compose reads.
type ompSettings struct {
	Skills struct {
		Enabled           *bool    `yaml:"enabled"`
		EnableClaudeUser  bool     `yaml:"enableClaudeUser"`
		CustomDirectories []string `yaml:"customDirectories"`
	} `yaml:"skills"`
	Commands struct {
		EnableClaudeUser    bool  `yaml:"enableClaudeUser"`
		EnableClaudeProject *bool `yaml:"enableClaudeProject"`
	} `yaml:"commands"`
}

func ompConfig(ompDir string) ompSettings {
	var cfg ompSettings
	if b, err := os.ReadFile(filepath.Join(ompDir, "config.yml")); err == nil {
		_ = yaml.Unmarshal(b, &cfg)
	}
	return cfg
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
