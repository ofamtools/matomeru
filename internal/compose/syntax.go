package compose

import (
	"cmp"
	"strings"

	"ofam.dev/matomeru/internal/harness"
)

// SkillInvocation is how the harness's prompt names a skill, the text the
// prompt box inserts when a skill is accepted: its compose row's skill
// template ({name}), a plugin's skill by pluginSkill ({plugin}, {name}).
// A row that gives neither gets Claude Code's form, the most common:
// /name, a plugin's /plugin:name.
func SkillInvocation(h harness.Harness, s Skill) string {
	c := profile(h)
	tmpl := cmp.Or(c.Skill, "/{name}")
	if s.Plugin != "" {
		switch {
		case c.PluginSkill != "":
			tmpl = c.PluginSkill
		case c.Skill == "":
			tmpl = "/{plugin}:{name}"
		}
	}
	return strings.NewReplacer("{plugin}", s.Plugin, "{name}", s.Name).Replace(tmpl)
}

// SkillsAreCommands reports whether the harness's skills are slash
// commands (their invocation starts with /), so the / completion offers
// them beside the commands.
func SkillsAreCommands(h harness.Harness) bool {
	return h.Compose != nil && strings.HasPrefix(SkillInvocation(h, Skill{Name: "x"}), "/")
}
