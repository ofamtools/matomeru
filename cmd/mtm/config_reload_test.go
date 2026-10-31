package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// reloadConfig runs config.reload and returns its problems.
func reloadConfig(t *testing.T, a *App) []string {
	t.Helper()
	v, err := a.r.Execute(ochazuke.Request{Noun: "config", Verb: "reload"})
	if err != nil {
		t.Fatal(err)
	}
	problems, _ := v.([]string)
	return problems
}

func rewrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAgentLaunchKeepsAppNamespace(t *testing.T) {
	t.Parallel()
	app, _ := harnessApp(t, `[]`)
	check := func() {
		t.Helper()
		for _, verb := range []string{"launch", "start", "choose"} {
			key := "agent." + verb
			canonical := app.r.CanonicalKey(key)
			op, ok := app.r.Operations[canonical]
			if !ok || canonical != key || op.Noun != "agent" || op.Builtin {
				t.Fatalf("%s canonical=%q noun=%q builtin=%v harness=%v", key, canonical, op.Noun, op.Builtin, op.Parameters["harness"].Enum)
			}
		}
		var rights []ochazuke.AgentRight
		if err := json.Unmarshal([]byte(mustCLI(t, app, "ochazuke", "agent", "rights", "--verb", "agent.launch")), &rights); err != nil {
			t.Fatal(err)
		}
		if len(rights) != 1 || rights[0].Verb != "agent.launch" {
			t.Fatalf("framework agent rights lost the app launch: %+v", rights)
		}
	}
	check()
	if p := reloadConfig(t, app); len(p) != 0 {
		t.Fatalf("problems %q", p)
	}
	check()
}

// config.reload reads harnesses.json again: a harness added there launches
// and is a choice of the harness parameters; a bad file is a problem naming
// it and keeps the registry it had.
func TestConfigReloadReadsTheHarnessFileAgain(t *testing.T) {
	t.Parallel()
	app, _ := harnessApp(t, `[{"name": "aider", "command": ["aider"], "prompt": "arg"}]`)
	rewrite(t, app.opts.Harnesses, `[{"name": "aider", "command": ["aider"], "prompt": "arg"}, {"name": "goose", "command": ["goose"], "prompt": "arg"}]`)
	if p := reloadConfig(t, app); len(p) != 0 {
		t.Fatalf("problems %q", p)
	}
	if _, ok := app.harnesses.Get("goose"); !ok {
		t.Fatalf("goose not loaded: %v", app.harnesses.Names())
	}
	for _, key := range harnessOps {
		if got := app.r.Operations[key].Parameters["harness"].Enum; !slices.Contains(got, "goose") {
			t.Fatalf("%s harness enum %v", key, got)
		}
	}
	rewrite(t, app.opts.Harnesses, `[{"name": "x", "command": ["x"], "prompt": "stdin"}]`)
	p := reloadConfig(t, app)
	if len(p) != 1 || !strings.HasPrefix(p[0], "harnesses.json: ") || !strings.Contains(p[0], `prompt "stdin" is not a prompt mode`) || strings.Contains(p[0], app.opts.Harnesses) {
		t.Fatalf("bad file problems %q", p)
	}
	if _, ok := app.harnesses.Get("goose"); !ok {
		t.Fatal("a bad file dropped the registry")
	}
	if got := app.r.Operations["agent.launch"].Parameters["harness"].Enum; !slices.Contains(got, "goose") || slices.Contains(got, "x") {
		t.Fatalf("enum after a bad file %v", got)
	}
}

// config.reload reads dirs.json again: the bases list shows the file's
// bases; a bad file is a problem naming it and the list keeps its rows.
func TestConfigReloadReadsDirsJSONAgain(t *testing.T) {
	t.Parallel()
	a := open(t)
	base := filepath.Join(t.TempDir(), "base")
	data, _ := json.Marshal(directoryConfig{Bases: []projectBase{{Path: base, Depth: 2}}})
	rewrite(t, a.dirsConfigPath(), string(data))
	if p := reloadConfig(t, a); len(p) != 0 {
		t.Fatalf("problems %q", p)
	}
	hasBase := func() bool {
		for _, it := range a.baseList.Items() {
			if it.ID == "base:"+base {
				return true
			}
		}
		return false
	}
	if !hasBase() {
		t.Fatalf("bases list %+v", a.baseList.Items())
	}
	rewrite(t, a.dirsConfigPath(), `{"project_base_dirs": [`)
	p := reloadConfig(t, a)
	if len(p) != 1 || !strings.HasPrefix(p[0], "dirs.json: ") {
		t.Fatalf("bad file problems %q", p)
	}
	if !hasBase() {
		t.Fatalf("a bad file dropped the bases list %+v", a.baseList.Items())
	}
}

// mtm config reload prints the problems as a JSON array: empty when every
// file reads, else one per bad file, each naming it.
func TestCLIConfigReloadReturnsTheProblems(t *testing.T) {
	t.Parallel()
	app, _ := harnessApp(t, `[]`)
	var problems []string
	if err := json.Unmarshal([]byte(mustCLI(t, app, "config", "reload")), &problems); err != nil || len(problems) != 0 {
		t.Fatalf("clean reload %q %v", problems, err)
	}
	rewrite(t, app.opts.Harnesses, `{`)
	rewrite(t, app.dirsConfigPath(), `{`)
	out := mustCLI(t, app, "config", "reload")
	if err := json.Unmarshal([]byte(out), &problems); err != nil {
		t.Fatalf("output %q: %v", out, err)
	}
	if len(problems) != 2 || !strings.HasPrefix(problems[0], "harnesses.json: ") || !strings.HasPrefix(problems[1], "dirs.json: ") {
		t.Fatalf("problems %q", problems)
	}
}
