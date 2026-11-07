package compose

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuiltinCompletionsAreGolden fills every directory the built-in
// harnesses search with a skill and a command, under each omp config
// variant, and pins what each harness completes (skills in order with
// their invocation, commands in order, the fingerprint's inputs) in
// testdata/completions.golden.json. GOLDEN_UPDATE=1 rewrites it.
func TestBuiltinCompletionsAreGolden(t *testing.T) {
	type answer struct {
		Harness  string   `json:"harness"`
		Config   string   `json:"config"`
		Skills   []string `json:"skills"`
		Commands []string `json:"commands"`
		Inputs   []string `json:"inputs"`
		CmdIn    []string `json:"commandInputs"`
	}
	configs := []string{
		"",
		"skills:\n  enableClaudeUser: true\n  customDirectories: [team, ~/shared-skills]\ncommands:\n  enableClaudeUser: true\n",
		"skills:\n  enabled: false\ncommands:\n  enableClaudeProject: false\n",
	}
	var out []answer
	for ci, cfg := range configs {
		e, pane := env(t)
		home := e.Home
		repo := filepath.Dir(filepath.Dir(pane))
		mid := filepath.Dir(pane)
		if cfg != "" {
			write(t, filepath.Join(e.OmpDir, "config.yml"), cfg)
		}
		on := filepath.Join(e.ClaudeDir, "plugins", "cache", "mkt", "helper", "1")
		write(t, filepath.Join(e.ClaudeDir, "plugins", "installed_plugins.json"), `{"plugins":{"helper@mkt":[{"scope":"user","installPath":"`+on+`"}]}}`)
		write(t, filepath.Join(e.ClaudeDir, "settings.json"), `{"enabledPlugins":{"helper@mkt":true}}`)
		dirs := map[string]string{
			"cu": filepath.Join(e.ClaudeDir, "%s"), "pl": filepath.Join(on, "%s"),
			"ou": filepath.Join(e.OmpDir, "%s"), "om": filepath.Join(e.OmpDir, "managed-skills"),
			"sh": filepath.Join(home, ".agents", "skills"), "xu": filepath.Join(home, ".codex", "skills"),
			"team": filepath.Join(pane, "team"), "hs": filepath.Join(home, "shared-skills"),
		}
		for _, d := range []string{pane, mid, repo} {
			tag := strings.ReplaceAll(strings.TrimPrefix(d, home), "/", "_")
			for _, sub := range []string{".claude", ".omp", ".agents", ".codex"} {
				skill(t, filepath.Join(d, sub, "skills"), "s"+sub[1:]+tag, sub)
				skill(t, filepath.Join(d, sub, "skills"), "same", sub+tag)
				write(t, filepath.Join(d, sub, "commands", "c"+sub[1:]+tag+".md"), "---\ndescription: "+sub+"\n---\n")
				write(t, filepath.Join(d, sub, "commands", "same.md"), "---\ndescription: "+sub+tag+"\n---\n")
			}
		}
		for k, d := range dirs {
			sk := d
			if strings.Contains(d, "%s") {
				sk = strings.Replace(d, "%s", "skills", 1)
				write(t, filepath.Join(strings.Replace(d, "%s", "commands", 1), "c"+k+".md"), "---\ndescription: "+k+"\n---\n")
				write(t, filepath.Join(strings.Replace(d, "%s", "commands", 1), "same.md"), "---\ndescription: "+k+"\n---\n")
			}
			skill(t, sk, "s"+k, k)
			skill(t, sk, "same", k)
		}
		rel := func(p string) string { return strings.TrimPrefix(p, home) }
		for _, h := range []string{Claude, Omp, Codex} {
			s := NewSources(e)
			a := answer{Harness: h, Config: []string{"none", "opt-ins", "disabled"}[ci]}
			for _, sk := range s.Skills(context.Background(), h, pane) {
				a.Skills = append(a.Skills, sk.Plugin+":"+sk.Name+" "+sk.Scope+" "+rel(sk.Path)+" "+sk.Description+" → "+SkillInvocation(h, sk))
			}
			for _, c := range s.Commands(context.Background(), h, pane) {
				if c.Source == "builtin" {
					a.Commands = append(a.Commands, c.Name)
					continue
				}
				a.Commands = append(a.Commands, c.Name+" "+c.Source+" "+c.Description)
			}
			for _, p := range s.skillInputs(h, pane) {
				a.Inputs = append(a.Inputs, rel(p))
			}
			for _, p := range s.commandInputs(h, pane) {
				a.CmdIn = append(a.CmdIn, rel(p))
			}
			out = append(out, a)
		}
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	b = append(b, '\n')
	path := filepath.Join("testdata", "completions.golden.json")
	if os.Getenv("GOLDEN_UPDATE") != "" {
		_ = os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, want) {
		t.Fatalf("%s differs:\n%s", path, b)
	}
}
