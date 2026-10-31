package main

import (
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

const modelHarnesses = `[{"name":"omp","models":["openai/sol","shared"]},{"name":"other","command":["other"],"modelFlag":"--model","models":["shared","other/model"]}]`

func modelIDs(choices []ochazuke.Choice) []string {
	out := make([]string, len(choices))
	for i, choice := range choices {
		out[i] = choice.ID
	}
	return out
}

func TestHarnessModelChoices(t *testing.T) {
	t.Parallel()
	a, _ := harnessApp(t, modelHarnesses)
	for _, tc := range []struct {
		harness string
		want    []string
	}{
		{"omp", []string{"openai/sol", "shared"}},
		{"claude", []string{"opus", "sonnet", "haiku"}},
		{"codex", []string{}},
		{"", []string{"opus", "sonnet", "haiku", "openai/sol", "shared", "other/model"}},
	} {
		var choices []ochazuke.Choice
		cliJSON(t, a, &choices, "harness", "models", "--harness", tc.harness)
		if got := modelIDs(choices); !slices.Equal(got, tc.want) {
			t.Fatalf("harness %q: models %v, want %v", tc.harness, got, tc.want)
		}
		if tc.harness == "" && choices[4].Label != "shared (omp, other)" {
			t.Fatalf("combined list lost shared model labels: %+v", choices)
		}
	}
	if _, _, err := cli(a, "", "harness", "models", "--harness", "missing"); err == nil || !strings.Contains(err.Error(), "unknown harness") {
		t.Fatalf("unknown harness: %v", err)
	}
}

func TestModelSourcesFollowHarness(t *testing.T) {
	t.Parallel()
	a, _ := harnessApp(t, modelHarnesses)
	sh := a.shell(strings.NewReader(""), io.Discard, io.Discard)
	for _, key := range []string{"agent.launch", "agent.start", "project.launch", "task.launch", "schedule.add", "schedule.edit", "schedule.create", "saved.create"} {
		op := a.r.Operations[a.r.CanonicalKey(key)]
		p := op.Parameters["model"]
		if p.Source != "harness.models" || !reflect.DeepEqual(p.SourceArgs, map[string]string{"harness": "harness"}) || !p.Free {
			t.Fatalf("%s model: %+v", key, p)
		}
		for _, h := range []string{"omp", "claude", "codex"} {
			parts := strings.Split(key, ".")
			choices, err := sh.Complete(append(parts, "--harness", h, "--model", ""))
			want := []string{}
			switch h {
			case "omp":
				want = []string{"openai/sol", "shared"}
			case "claude":
				want = []string{"opus", "sonnet", "haiku"}
			}
			if err != nil || !slices.Equal(modelIDs(choices), want) {
				t.Fatalf("%s --harness %s --model <tab>: %+v, %v", key, h, choices, err)
			}
		}
	}
}

func TestScheduleModelFormRederives(t *testing.T) {
	t.Parallel()
	a, _ := harnessApp(t, modelHarnesses)
	mustCLI(t, a, "schedule", "add", "--name", "review", "--prompt", "Review", "--harness", "omp")
	a.cli = false
	toPage(t, a, schedulesPage)
	if err := a.openScheduleForm("review", "Edit review"); err != nil {
		t.Fatal(err)
	}
	press(t, a, "esc")
	if err := a.r.SetFocus(scheduleFormID + "/model"); err != nil {
		t.Fatal(err)
	}
	press(t, a, "enter")
	fr := frame(a)
	if !strings.Contains(fr, "openai/sol") || strings.Contains(fr, "sonnet") || strings.Contains(fr, "other/model") {
		t.Fatalf("omp choices:\n%s", fr)
	}
	press(t, a, "esc", "k", "enter", "k", "enter", "j", "enter")
	fr = frame(a)
	if !strings.Contains(fr, "sonnet") || strings.Contains(fr, "openai/sol") || strings.Contains(fr, "other/model") {
		t.Fatalf("claude choices after changing harness:\n%s", fr)
	}
	d, _ := a.scheduleDef("review")
	if d.Agent.Harness != "claude" {
		t.Fatalf("harness change was not recorded: %+v", d.Agent)
	}
}
