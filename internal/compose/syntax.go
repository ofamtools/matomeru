package compose

// SkillInvocation is how the harness's prompt names a skill, the text the
// prompt box inserts when a skill is accepted:
//
//   - omp: /skill:<name> (a namespaced skill /skill:<ns>/<name>), which
//     omp reads at the start of the prompt or mid-text
//     (getSkillSlashCommandName and the /skill: token parse in
//     src/extensibility/skills.ts, 18.13.0).
//   - Claude Code: /<name>, a plugin's skill /<plugin>:<name> (2.1.311:
//     a skill is a slash command; its Skill tool names plugin skills
//     plugin:skill).
//   - Codex: $<name> (codex-cli 0.162.1: "explicitly invoked as
//     `$skill-name`").
//
// Any other harness gets Claude's form, the most common.
func SkillInvocation(harness string, s Skill) string {
	switch harness {
	case Omp:
		return "/skill:" + s.Name
	case Codex:
		return "$" + s.Name
	}
	if s.Plugin != "" {
		return "/" + s.Plugin + ":" + s.Name
	}
	return "/" + s.Name
}
