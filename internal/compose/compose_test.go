package compose

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func skill(t *testing.T, dir, name, description string) {
	write(t, filepath.Join(dir, name, "SKILL.md"), "---\nname: "+name+"\ndescription: \""+description+"\"\n---\n# "+name+"\n")
}

// env is a home with Claude's and omp's directories and a project with a
// repository root above the pane's directory.
func env(t *testing.T) (Env, string) {
	t.Helper()
	home := t.TempDir()
	e := Env{Home: home, ClaudeDir: filepath.Join(home, ".claude"), OmpDir: filepath.Join(home, ".omp", "agent")}
	repo := filepath.Join(home, "proj", "app")
	pane := filepath.Join(repo, "cmd", "tool")
	write(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	if err := os.MkdirAll(pane, 0o755); err != nil {
		t.Fatal(err)
	}
	return e, pane
}

func names(skills []Skill) string {
	var out []string
	for _, s := range skills {
		n := s.Name
		if s.Plugin != "" {
			n = s.Plugin + ":" + n
		}
		out = append(out, n+"("+s.Scope+")")
	}
	return strings.Join(out, " ")
}

func TestClaudeSkillsUserProjectAndEnabledPlugins(t *testing.T) {
	e, pane := env(t)
	repo := filepath.Dir(filepath.Dir(pane))
	skill(t, filepath.Join(e.ClaudeDir, "skills"), "deploy", "Ship it")
	skill(t, filepath.Join(repo, ".claude", "skills"), "lint", "Lint the repo")
	skill(t, filepath.Join(pane, ".claude", "skills"), "local", "Nearest")
	// Above the repository root: not the project's.
	skill(t, filepath.Join(e.Home, "proj", ".claude", "skills"), "outside", "no")
	on := filepath.Join(e.ClaudeDir, "plugins", "cache", "mkt", "helper", "1.0.0")
	off := filepath.Join(e.ClaudeDir, "plugins", "cache", "mkt", "quiet", "2.0.0")
	other := filepath.Join(e.ClaudeDir, "plugins", "cache", "mkt", "elsewhere", "1.0.0")
	skill(t, filepath.Join(on, "skills"), "review", "Plugin review")
	skill(t, filepath.Join(off, "skills"), "hidden", "Disabled plugin")
	skill(t, filepath.Join(other, "skills"), "theirs", "Another project's plugin")
	write(t, filepath.Join(e.ClaudeDir, "plugins", "installed_plugins.json"), `{"version":2,"plugins":{
		"helper@mkt":[{"scope":"user","installPath":"`+on+`"}],
		"quiet@mkt":[{"scope":"user","installPath":"`+off+`"}],
		"elsewhere@mkt":[{"scope":"project","projectPath":"/somewhere/else","installPath":"`+other+`"}]}}`)
	write(t, filepath.Join(e.ClaudeDir, "settings.json"), `{"enabledPlugins":{"helper@mkt":true,"quiet@mkt":false,"elsewhere@mkt":true}}`)
	got := NewSources(e).Skills(context.Background(), Claude, pane)
	if want := "deploy(user) lint(project) local(project) helper:review(plugin)"; names(got) != want {
		t.Fatalf("skills %q, want %q", names(got), want)
	}
	if got[0].Description != "Ship it" {
		t.Fatalf("description %q", got[0].Description)
	}
	if SkillInvocation(Claude, got[3]) != "/helper:review" || SkillInvocation(Claude, got[0]) != "/deploy" {
		t.Fatalf("invocations %q %q", SkillInvocation(Claude, got[3]), SkillInvocation(Claude, got[0]))
	}
}

func TestOmpSkillsFollowItsConfig(t *testing.T) {
	e, pane := env(t)
	repo := filepath.Dir(filepath.Dir(pane))
	skill(t, filepath.Join(e.OmpDir, "skills"), "mine", "omp user")
	skill(t, filepath.Join(e.Home, ".agents", "skills"), "shared", "shared user")
	skill(t, filepath.Join(repo, ".omp", "skills"), "proj", "omp project")
	skill(t, filepath.Join(repo, ".claude", "skills"), "claudeproj", "Claude project")
	skill(t, filepath.Join(e.ClaudeDir, "skills"), "claudeuser", "Claude user, opt-in")
	custom := filepath.Join(e.Home, "team", "skills")
	skill(t, custom, "team", "custom directory")
	write(t, filepath.Join(e.OmpDir, "config.yml"), "skills:\n  enabled: true\n  customDirectories:\n    - ~/team/skills\n")
	s := NewSources(e)
	if want := "claudeproj(project) mine(user) proj(project) shared(shared) team(configured)"; names(s.Skills(context.Background(), Omp, pane)) != want {
		t.Fatalf("skills %q", names(s.Skills(context.Background(), Omp, pane)))
	}
	// The Claude user opt-in; the config change is seen (its fingerprint).
	time.Sleep(10 * time.Millisecond)
	write(t, filepath.Join(e.OmpDir, "config.yml"), "skills:\n  enableClaudeUser: true\n  customDirectories: [~/team/skills]\n")
	if got := names(s.Skills(context.Background(), Omp, pane)); !strings.Contains(got, "claudeuser(user)") {
		t.Fatalf("opt-in: %q", got)
	}
	write(t, filepath.Join(e.OmpDir, "config.yml"), "skills:\n  enabled: false\n")
	os.Chtimes(filepath.Join(e.OmpDir, "config.yml"), time.Now().Add(time.Hour), time.Now().Add(time.Hour))
	if got := s.Skills(context.Background(), Omp, pane); len(got) != 0 {
		t.Fatalf("disabled: %q", names(got))
	}
	if SkillInvocation(Omp, Skill{Name: "mine"}) != "/skill:mine" || SkillInvocation(Codex, Skill{Name: "mine"}) != "$mine" {
		t.Fatal("omp and codex invocations")
	}
}

// o-ai answers first when it runs; its rows the harness does not load
// are dropped, and a plugin's name comes from its install path.
func TestSkillsPreferOAI(t *testing.T) {
	e, pane := env(t)
	calls := 0
	e.OAI = func(_ context.Context, harness, dir string) ([]byte, error) {
		calls++
		if harness != Claude || dir != pane {
			t.Fatalf("o-ai asked %s %s", harness, dir)
		}
		return []byte(`[
			{"id":"skill/a","data":{"name":"alpha","description":"A","path":"/h/.claude/skills/alpha/SKILL.md","scope":"user","access":"discovered in harness search path; session loading not verified"}},
			{"id":"skill/b","data":{"name":"gated","path":"/h/.claude/skills/gated/SKILL.md","scope":"Claude user","access":"discovered cross-harness source; requires Claude-user discovery opt-in"}},
			{"id":"skill/c","data":{"name":"broken","path":"/x","scope":"user","access":"invalid frontmatter; source discovered, loading not verified"}},
			{"id":"skill/d","data":{"name":"plug","path":"/h/.claude/plugins/cache/mkt/tools/9f/skills/plug/SKILL.md","scope":"plugin user","access":"enabled plugin; session loading not verified"}},
			{"id":"skill/e","data":{"name":"off","path":"/h/.claude/plugins/cache/mkt/x/1/skills/off/SKILL.md","scope":"plugin user","access":"disabled plugin"}}]`), nil
	}
	s := NewSources(e)
	got := s.Skills(context.Background(), Claude, pane)
	if names(got) != "alpha(user) tools:plug(plugin user)" {
		t.Fatalf("skills %q", names(got))
	}
	s.Skills(context.Background(), Claude, pane)
	if calls != 1 {
		t.Fatalf("o-ai ran %d times; the fingerprint did not change", calls)
	}
}

func TestCommandsBuiltinsUserProjectAndPlugin(t *testing.T) {
	e, pane := env(t)
	repo := filepath.Dir(filepath.Dir(pane))
	write(t, filepath.Join(e.ClaudeDir, "commands", "cr.md"), "---\ndescription: Code review\n---\nreview\n")
	write(t, filepath.Join(repo, ".claude", "commands", "frontend", "component.md"), "make one\n")
	on := filepath.Join(e.ClaudeDir, "plugins", "cache", "mkt", "helper", "1")
	write(t, filepath.Join(on, "commands", "setup.md"), "setup\n")
	write(t, filepath.Join(e.ClaudeDir, "plugins", "installed_plugins.json"), `{"plugins":{"helper@mkt":[{"scope":"user","installPath":"`+on+`"}]}}`)
	write(t, filepath.Join(e.ClaudeDir, "settings.json"), `{"enabledPlugins":{"helper@mkt":true}}`)
	got := map[string]Command{}
	for _, c := range NewSources(e).Commands(context.Background(), Claude, pane) {
		got[c.Name] = c
	}
	for _, want := range []string{"clear", "compact", "cr", "component", "frontend:component", "helper:setup"} {
		if _, ok := got[want]; !ok {
			t.Fatalf("no /%s in %v", want, got)
		}
	}
	if got["cr"].Description != "Code review" || got["cr"].Source != "user" || got["clear"].Source != "builtin" {
		t.Fatalf("cr %+v clear %+v", got["cr"], got["clear"])
	}
	omp := map[string]bool{}
	write(t, filepath.Join(e.OmpDir, "commands", "mine.md"), "x\n")
	for _, c := range NewSources(e).Commands(context.Background(), Omp, pane) {
		omp[c.Name] = true
	}
	if !omp["mine"] || !omp["tree"] || !omp["component"] || omp["cr"] {
		t.Fatalf("omp commands %v", omp)
	}
}

func TestFilesWalkAndRank(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"main.go", "internal/turns/omp.go", "internal/turns/claude.go", "docs/notes.md", ".hidden/x", "node_modules/y.js"} {
		write(t, filepath.Join(dir, p), "x")
	}
	s := NewSources(Env{})
	files := s.Files(context.Background(), dir)
	if strings.Join(files, " ") != "docs/ docs/notes.md internal/ internal/turns/ internal/turns/claude.go internal/turns/omp.go main.go" {
		t.Fatalf("files %q", files)
	}
	var got []string
	for _, m := range Rank("omp", files, 5) {
		got = append(got, m.Text)
	}
	if strings.Join(got, " ") != "internal/turns/omp.go" {
		t.Fatalf("rank omp %q", got)
	}
	got = nil
	for _, m := range Rank("itc", files, 5) {
		got = append(got, m.Text)
	}
	if len(got) != 1 || got[0] != "internal/turns/claude.go" {
		t.Fatalf("scattered %q", got)
	}
	if len(Rank("", files, 3)) != 3 {
		t.Fatal("an empty query keeps the first ones")
	}
}

func TestHarnessNames(t *testing.T) {
	for id, want := range map[string]string{"claude-code": Claude, "omp": Omp, "codex": Codex, "aider": ""} {
		if Harness(id) != want {
			t.Fatalf("%s: %q", id, Harness(id))
		}
	}
}
