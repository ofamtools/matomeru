package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ofam.dev/matomeru/internal/compose"
	"ofam.dev/matomeru/internal/daemon"
	"ofam.dev/matomeru/internal/harness"
)

// claudeWork is a registry with no row named claude: the owner's
// claude-work, a copy of the built-in claude's fields under its own name,
// beside omp and codex.
func claudeWork(t *testing.T) *harness.Registry {
	t.Helper()
	var list []harness.Harness
	for _, h := range harness.Builtins() {
		if h.Name == "claude" {
			h.Name = "claude-work"
		}
		list = append(list, h)
	}
	reg, err := harness.NewRegistry(list)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// A user harness named claude-work that gives claude's fields behaves as
// claude in every capability, with no row named claude: a pane mtm
// launched with it (tuios reporting claude-code, or nothing yet) reads
// the Claude transcript, keeps its sessions, attaches by mention,
// completes Claude's skills as slash commands and gets claude's MCP
// recipe.
func TestUserHarnessWithClaudesFieldsBehavesAsClaude(t *testing.T) {
	t.Parallel()
	reg := claudeWork(t)
	a := &App{harnesses: reg}
	for _, p := range []pane{
		{Pane: daemon.Pane{ID: "w1"}, Harness: "claude-code", LaunchHarness: "claude-work"},
		{Pane: daemon.Pane{ID: "w2"}, LaunchHarness: "claude-work"},
		{Pane: daemon.Pane{ID: "w3"}, Harness: "claude-code"},
	} {
		h, ok := paneHarness(reg, p)
		if !ok || h.Name != "claude-work" {
			t.Fatalf("%s: harness %q %v", p.ID, h.Name, ok)
		}
		if got := paneLog(reg, p); got != harness.LogClaude {
			t.Fatalf("%s: log %q", p.ID, got)
		}
		if f := a.logFormatOf(p); f.turns != turnsClaude || f.from != "Claude transcript" || f.rollout {
			t.Fatalf("%s: format %+v", p.ID, f)
		}
		if h, ok := a.harnessOf(p); !ok || h.Name != "claude-work" {
			t.Fatalf("%s: profile %q", p.ID, h.Name)
		}
		if a.attachMode(p) != harness.AttachMention {
			t.Fatalf("%s: attach %q", p.ID, a.attachMode(p))
		}
		if _, ok := h.Setting(harness.SettingPermission); !ok {
			t.Fatalf("%s: no permission-mode row", p.ID)
		}
	}
	h, _ := reg.Get("claude-work")
	if !compose.SkillsAreCommands(h) || compose.SkillInvocation(h, compose.Skill{Name: "r", Plugin: "p"}) != "/p:r" {
		t.Fatal("claude-work's skills are not Claude's slash commands")
	}
	// Codex and omp stay what their own rows say.
	if f := a.logFormatOf(pane{Harness: "codex"}); !f.rollout || f.from != "codex rollout" {
		t.Fatalf("codex format %+v", f)
	}
	if f := a.logFormatOf(pane{LaunchHarness: "omp"}); f.turns != turnsOmp {
		t.Fatalf("omp format %+v", f)
	}
	if f := a.logFormatOf(pane{Harness: "aider", LaunchHarness: "claude-work"}); f.turns != turnsJournal || f.rollout {
		t.Fatalf("a pane tuios reports as another program: %+v", f)
	}
}

// mtm mcp config claude-work prints claude's recipe under its own name.
func TestMCPConfigFollowsTheHarnessRow(t *testing.T) {
	t.Parallel()
	app, _, _ := mcpApp(t, nil)
	app.harnesses = claudeWork(t)
	var out, errOut bytes.Buffer
	if err := app.commands([]string{"mcp", "config", "claude-work"}, strings.NewReader(""), &out, &errOut, nil).Run(context.Background()); err != nil {
		t.Fatalf("mcp config claude-work: %v\n%s", err, errOut.String())
	}
	var config struct {
		Harness string   `json:"harness"`
		File    string   `json:"file"`
		Args    []string `json:"args"`
	}
	if err := json.Unmarshal(out.Bytes(), &config); err != nil {
		t.Fatal(err)
	}
	if config.Harness != "claude-work" || config.File != "/home/test/.claude.json" || len(config.Args) != 2 || config.Args[0] != "--mcp-config" {
		t.Fatalf("claude-work config:\n%s", out.String())
	}
}
