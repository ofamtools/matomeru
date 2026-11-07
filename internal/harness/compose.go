package harness

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// Compose is what mtm's prompt box completes from in a pane running the
// harness (internal/compose reads it): where the harness finds its skills
// and its Markdown commands, its built-in commands, and how its prompt
// names a skill. Nil: the harness has no completions but files.
//
// A path starts with ~ (the home directory), {claudeDir} (Claude Code's
// config directory: $CLAUDE_CONFIG_DIR, else ~/.claude) or {ompDir}
// (omp's: $PI_CODING_AGENT_DIR, else ~/.omp/agent), or is absolute; a
// path in an up group is relative, taken at the pane's directory and each
// parent up to the repository root. The skill templates take {name} and
// {plugin}.
//
//	{"oai": "codex", "skills": [{"path": "~/.codex/skills", "scope": "user"},
//	 {"up": [{"path": ".codex/skills", "scope": "project"}]}],
//	 "commands": ["model", "new"], "skill": "${name}"}
type Compose struct {
	// OAI is the harness name `o-ai skill list --harness` takes, asked
	// first when o-ai is on PATH; empty: the scan of Skills alone.
	OAI string `json:"oai,omitempty"`
	// Config is a YAML (or JSON) settings file whose keys the
	// directories' If conditions and List read.
	Config string `json:"config,omitempty"`
	// Inputs are more files whose change means the lists may have
	// changed (the directories are watched already).
	Inputs []string `json:"inputs,omitempty"`
	// Skills are the skill directories (each holds <name>/SKILL.md), the
	// nearest first: a skill of a name already seen is left out.
	Skills []Dir `json:"skills,omitempty"`
	// CommandDirs are the Markdown command directories, the same way.
	CommandDirs []Dir `json:"commandDirs,omitempty"`
	// Commands are the harness's own slash commands, without the slash.
	Commands []string `json:"commands,omitempty"`
	// Skill is how the prompt names a skill ("/{name}", the default);
	// PluginSkill a plugin's ("/{plugin}:{name}"; empty: Skill, or with
	// no Skill either the default "/{plugin}:{name}").
	Skill       string `json:"skill,omitempty"`
	PluginSkill string `json:"pluginSkill,omitempty"`
}

// Dir is one entry of a Compose directory list: a Path, an Up group, the
// Plugins of a Claude Code config directory (Path then names the folder
// inside each enabled plugin) or a List of paths read from Config. If are
// Config conditions that must all hold: "key" (the key is true),
// "key=value" or "key!=value" (unset counts as not equal), keys dotted
// ("skills.enabled").
type Dir struct {
	Path    string   `json:"path,omitempty"`
	Scope   string   `json:"scope,omitempty"`
	Up      []Dir    `json:"up,omitempty"`
	Plugins string   `json:"plugins,omitempty"`
	List    string   `json:"list,omitempty"`
	If      []string `json:"if,omitempty"`
}

func (c Compose) clone() *Compose {
	c.Inputs, c.Commands = slices.Clone(c.Inputs), slices.Clone(c.Commands)
	c.Skills, c.CommandDirs = cloneDirs(c.Skills), cloneDirs(c.CommandDirs)
	return &c
}

func cloneDirs(ds []Dir) []Dir {
	out := slices.Clone(ds)
	for i := range out {
		out[i].Up, out[i].If = cloneDirs(out[i].Up), slices.Clone(out[i].If)
	}
	return out
}

func (c Compose) validate(name string) error {
	for _, l := range []struct {
		field string
		dirs  []Dir
	}{{"skills", c.Skills}, {"commandDirs", c.CommandDirs}} {
		for i, d := range l.dirs {
			if err := d.validate(false); err != nil {
				return fmt.Errorf("harness %s: compose.%s[%d]: %w", name, l.field, i, err)
			}
		}
	}
	return nil
}

func (d Dir) validate(inUp bool) error {
	kinds := 0
	for _, set := range []bool{d.Path != "" && d.Plugins == "", len(d.Up) > 0, d.Plugins != "", d.List != ""} {
		if set {
			kinds++
		}
	}
	if kinds != 1 {
		return fmt.Errorf("give one of path, up, plugins (with path) or list")
	}
	for _, c := range d.If {
		if strings.TrimSpace(c) == "" {
			return fmt.Errorf("an empty if condition")
		}
	}
	switch {
	case len(d.Up) > 0:
		if inUp {
			return fmt.Errorf("an up group inside an up group")
		}
		for i, u := range d.Up {
			if err := u.validate(true); err != nil {
				return fmt.Errorf("up[%d]: %w", i, err)
			}
			if u.Path == "" || u.Plugins != "" {
				return fmt.Errorf("up[%d]: an up entry is a relative path", i)
			}
		}
		return nil
	case d.Scope == "":
		return fmt.Errorf("scope is empty")
	case d.Plugins != "" && d.Path == "":
		return fmt.Errorf("plugins needs path, the folder inside each plugin")
	case inUp && rooted(d.Path):
		return fmt.Errorf("path %q in an up group must be relative", d.Path)
	case !inUp && d.Plugins == "" && d.Path != "" && !rooted(d.Path):
		return fmt.Errorf("path %q must start with ~, {claudeDir}, {ompDir} or /; put a relative one in an up group", d.Path)
	}
	return nil
}

// rooted reports whether a Compose path names its own start.
func rooted(p string) bool {
	return p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "{") || filepath.IsAbs(p)
}
