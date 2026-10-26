package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/unixsuperhero/matomeru/internal/harness"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// TestMain points XDG_CONFIG_HOME and MTM_CONFIG_DIR at an empty directory,
// so no test reads or writes the user's config (dirs.json, harnesses.json)
// even through the default -data, and turns off
// the o-proj source of known directories.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mtm-config")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CONFIG_HOME", dir)
	os.Setenv("MTM_CONFIG_DIR", "")
	// no test reads the user's o proj manifest (known directories)
	os.Setenv("MTM_O_PROJ", "none")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// harnessApp is the seeded app with the harness file file (none when
// empty) and a fake daemon whose panes join the fake listing.
func harnessApp(t *testing.T, file string) (*App, *fakeDaemon) {
	t.Helper()
	opts := Options{Data: seeded(t)}
	if file != "" {
		opts.Harnesses = filepath.Join(t.TempDir(), "harnesses.json")
		if err := os.WriteFile(opts.Harnesses, []byte(file), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	app := openWith(t, opts)
	f := &fakeDaemon{panes: app.panesd.(*fakePanes)}
	app.launcher = f
	return app, f
}

// setHarness stores a harness setting on the matomeru project.
func setHarness(t *testing.T, a *App, name string) {
	t.Helper()
	if err := a.work.PutProject(work.Project{ID: "matomeru", Name: "matomeru", Created: 2, Settings: work.ProjectSettings{Harness: name}}); err != nil {
		t.Fatal(err)
	}
	if err := a.refreshProjects(); err != nil {
		t.Fatal(err)
	}
}

// launchCLI runs one launch through the CLI and returns its only window.
func launchCLI(t *testing.T, a *App, f *fakeDaemon, args ...string) *fakeWindow {
	t.Helper()
	mustCLI(t, a, args...)
	if len(f.launches) != 1 {
		t.Fatalf("launches = %+v", f.launches)
	}
	return f.launches[0]
}

func TestEveryHarnessLaunchesThroughNewWindowThenSendText(t *testing.T) {
	for _, c := range []struct {
		name    string
		args    []string
		argv    string
		harness string
	}{
		{"claude, the default", nil, "claude --model opus", "claude"},
		{"claude with a model", []string{"--model", "haiku"}, "claude --model haiku", "claude"},
		{"omp with any model", []string{"--harness", "omp", "--model", "openai-codex/gpt-6.1-sol"}, "omp --model openai-codex/gpt-6.1-sol", "omp"},
		{"omp with no model", []string{"--harness", "omp"}, "omp", "omp"},
	} {
		t.Run(c.name, func(t *testing.T) {
			app, f := harnessApp(t, "")
			got := launchCLI(t, app, f, append([]string{"agent", "launch", "--target", "t1", "--yes"}, c.args...)...)
			if strings.Join(got.Command, " ") != c.argv || got.Session != "matomeru" || got.Name != "write the brief" {
				t.Fatalf("new-window = %+v", got)
			}
			if len(got.Waits) != 1 || got.Waits[0] != (harness.Ready{State: "idle,done", TimeoutMS: harness.DefaultReadyTimeoutMS}) {
				t.Fatalf("waits = %+v", got.Waits)
			}
			if !reflect.DeepEqual(f.sends, []string{"matomeru/a1: write the brief"}) {
				t.Fatalf("send-text = %q", f.sends)
			}
			reviews, err := app.work.Reviews()
			if err != nil || len(reviews) != 1 || reviews[0].Harness != c.harness {
				t.Fatalf("reviews = %+v, %v", reviews, err)
			}
			if cells := app.reviewCells(paneReview{Review: reviews[0]}, time.Now()); cells[1] != "matomeru/write the brief" || cells[len(cells)-1] != c.harness {
				t.Fatalf("review cells = %q", cells)
			}
		})
	}
}

func TestTheProjectHarnessSettingIsTheDefault(t *testing.T) {
	app, f := harnessApp(t, "")
	setHarness(t, app, "omp")
	if got := launchCLI(t, app, f, "task", "launch", "--target", "t1", "--yes"); strings.Join(got.Command, " ") != "omp" {
		t.Fatalf("new-window = %+v", got)
	}
	// An explicit harness beats the setting.
	mustCLI(t, app, "task", "launch", "--target", "t1", "--harness", "codex", "--yes")
	if got := f.launches[1]; strings.Join(got.Command, " ") != "codex" {
		t.Fatalf("new-window = %+v", got)
	}
}

func TestUnknownHarnessesAndBadModelsFailClearly(t *testing.T) {
	app, f := harnessApp(t, "")
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"agent", "launch", "--target", "t1", "--harness", "aider"}, "harness must be one of claude, omp, codex"},
		{[]string{"agent", "launch", "--target", "t1", "--model", "gpt"}, `model "gpt" is not one of claude's models: opus, sonnet, haiku`},
	} {
		if _, _, err := cli(app, "", c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("mtm %s: %v, want %q", strings.Join(c.args, " "), err, c.want)
		}
	}
	setHarness(t, app, "ghost")
	if _, _, err := cli(app, "", "task", "launch", "--target", "t1", "--yes"); err == nil || !strings.Contains(err.Error(), `unknown harness "ghost"; known: claude, omp, codex`) {
		t.Fatalf("a project naming an unknown harness: %v", err)
	}
	if len(f.launches) != 0 {
		t.Fatalf("launches = %+v", f.launches)
	}
}

func TestAHarnessFileAddsAHarnessAndChangesACommand(t *testing.T) {
	app, f := harnessApp(t, `[
	  {"name": "omp", "command": ["omp-dev", "--no-extensions"]},
	  {"name": "aider", "command": ["aider"], "prompt": "arg"}
	]`)
	if got := app.r.Operations["agent.launch"].Parameters["harness"].Enum; strings.Join(got, " ") != "claude omp codex aider" {
		t.Fatalf("harness enum = %v", got)
	}
	got := launchCLI(t, app, f, "agent", "launch", "--target", "t1", "--harness", "omp", "--yes")
	if strings.Join(got.Command, " ") != "omp-dev --no-extensions" || got.Prompt != "write the brief" || len(got.Waits) != 1 {
		t.Fatalf("omp from the file = %+v", got)
	}
	// aider takes its prompt as an argument: no readiness wait, no send-text.
	mustCLI(t, app, "agent", "launch", "--target", "t1", "--harness", "aider", "--yes")
	if got := f.launches[1]; !reflect.DeepEqual(got.Command, []string{"aider", "write the brief"}) || len(got.Waits) != 0 || got.Prompt != "" || len(f.sends) != 1 {
		t.Fatalf("aider = %+v, sends %q", got, f.sends)
	}
	if _, err := New(Options{Data: seeded(t), Harnesses: writeFile(t, `[{"name": "x", "command": ["x"], "prompt": "stdin"}]`)}); err == nil || !strings.Contains(err.Error(), `prompt "stdin" is not a prompt mode`) {
		t.Fatalf("a bad harness file: %v", err)
	}
}

func writeFile(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "harnesses.json")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAPaneThatIsNotReadyKeepsItsPrompt(t *testing.T) {
	app, f := harnessApp(t, "")
	f.notReady = errors.New("wait-for: timeout")
	got := launchCLI(t, app, f, "agent", "launch", "--target", "t1", "--yes")
	if got.Prompt != "" || len(f.sends) != 0 {
		t.Fatalf("typed into a pane that was not ready: %+v %q", got, f.sends)
	}
	if !strings.Contains(app.landErr, "prompt not sent: claude was not ready: wait-for: timeout") {
		t.Fatalf("landErr = %q", app.landErr)
	}
}

func TestCLIHarnessListPrintsTheRegistry(t *testing.T) {
	app, _ := harnessApp(t, "")
	var list []harness.Harness
	if err := json.Unmarshal([]byte(mustCLI(t, app, "harness", "list")), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].Name != "claude" || list[1].Name != "omp" || list[2].Name != "codex" || list[1].Command[0] != "omp" || list[0].Models[0] != "opus" {
		t.Fatalf("harness list = %+v", list)
	}
}

func TestTheDriverRequests(t *testing.T) {
	wire := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	if got := wire(windowParams(harness.Window{Session: "s", Name: "n", Cwd: "/c", Command: []string{"omp"}}, 2)); got != `{"session":"s","name":"n","workspace":2,"cwd":"/c","focus":false,"command":["omp"]}` {
		t.Fatalf("new-window = %s", got)
	}
	if got := wire(waitParams("s", "w1", harness.Ready{State: "idle,done", TimeoutMS: 9})); got != `{"condition":"agent-state","session":"s","window":"w1","until":"idle,done","timeout":9}` {
		t.Fatalf("wait-for = %s", got)
	}
	if got := wire(waitParams("s", "w1", harness.Ready{Pattern: "> $"})); got != `{"condition":"window-output","session":"s","window":"w1","pattern":"\u003e $"}` {
		t.Fatalf("wait-for = %s", got)
	}
}
