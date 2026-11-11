package main

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
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
			op, ok := app.r.Operations.Find(ochazuke.Key(canonical))
			if !ok || canonical != key || op.Noun() != "agent" || op.Builtin {
				t.Fatalf("%s canonical=%q noun=%q builtin=%v harness=%v", key, canonical, op.Noun(), op.Builtin, op.Parameters["harness"].Enum)
			}
		}
		var rights []ochazuke.AgentRight
		if err := json.Unmarshal([]byte(mustCLI(t, app, "ochazuke", "agent", "get", "--prop", "rights")), &rights); err != nil {
			t.Fatal(err)
		}
		rights = slices.DeleteFunc(rights, func(r ochazuke.AgentRight) bool { return r.Verb != "agent.launch" })
		if len(rights) != 1 {
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
		if got := app.r.Operations.At(ochazuke.Key(key)).Parameters["harness"].Enum; !slices.Contains(got, "goose") {
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
	if got := app.r.Operations.At("agent.launch").Parameters["harness"].Enum; !slices.Contains(got, "goose") || slices.Contains(got, "x") {
		t.Fatalf("enum after a bad file %v", got)
	}
}

// Legacy directory files are imports, never a second source of live data.
func TestConfigReloadDoesNotImportDirsJSON(t *testing.T) {
	t.Parallel()
	a := open(t)
	rewrite(t, a.dirsConfigPath(), `{"project_base_dirs":[{"path":"/not-imported","depth":2}]}`)
	if problems := reloadConfig(t, a); len(problems) != 0 {
		t.Fatalf("problems %q", problems)
	}
	rewrite(t, a.dirsConfigPath(), `{`)
	if problems := reloadConfig(t, a); len(problems) != 0 {
		t.Fatalf("legacy file was read again: %q", problems)
	}
	if bases, err := a.projectBases(); err != nil || len(bases) != 0 {
		t.Fatalf("config reload imported bases: %+v %v", bases, err)
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
	if len(problems) != 1 || !strings.HasPrefix(problems[0], "harnesses.json: ") {
		t.Fatalf("problems %q", problems)
	}
}
