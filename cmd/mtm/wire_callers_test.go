package main

import (
	"strings"
	"testing"

	"ofam.dev/ochazuke"
)

// tuiosCLICommands are the commands of the tuios binary that callers outside
// mtm use (mtmd-5-callers report, section 2) and the mtm verb each switches
// to. A caller's command with no verb here is a gap: the test fails until it
// is mapped, so the tuios binary can go once every row holds.
// agent-hook has no verb: it is the oden command `mtm agent hook`.
var tuiosCLICommands = map[string]string{
	"list-sessions":   "session.list",
	"list-windows":    "session.list-panes",
	"subscribe":       "session.events",
	"list-agents":     "agent.scan",
	"queue":           "agent.enqueue",
	"queue-prompt":    "agent.enqueue",
	"new-window":      "pane.open",
	"focus-window":    "pane.switch",
	"close-window":    "pane.close",
	"get-window":      "pane.show",
	"resize":          "pane.resize",
	"new":             "session.new",
	"new-session":     "session.new",
	"capture-pane":    "pane.capture",
	"send-text":       "pane.send-text",
	"send-keys":       "pane.send-keys",
	"wait-for":        "pane.wait",
	"peek-prompt":     "agent.peek-prompt",
	"ask-agent":       "agent.ask",
	"set-agent-state": "agent.set-status",
	"set-agent-meta":  "agent.set-meta",
	"start-server":    "daemon.start",
	"kill-server":     "daemon.stop",
	"--skill":         "agent.guide",
}

func TestEveryTuiosCLICommandACallerUsesHasAnMtmVerb(t *testing.T) {
	t.Parallel()
	app := open(t)
	for command, verb := range tuiosCLICommands {
		if _, ok := app.r.Operation(verb); !ok {
			t.Errorf("tuios %s -> %s: no such verb", command, verb)
		}
	}
	// What an agent may do: reads and typing run; closing a live window asks.
	for verb, want := range map[string]string{
		"pane.open": ochazuke.AgentAllow, "pane.switch": ochazuke.AgentAllow, "pane.show": ochazuke.AgentAllow,
		"pane.resize": ochazuke.AgentAllow, "session.new": ochazuke.AgentAllow, "agent.enqueue": ochazuke.AgentAllow,
		"agent.guide": ochazuke.AgentAllow, "pane.close": ochazuke.AgentConfirm,
	} {
		if op, ok := app.r.Operation(verb); !ok || op.Agent != want {
			t.Errorf("%s: agent right %q, want %q", verb, op.Agent, want)
		}
	}
}

func TestAgentGuideCarriesTheLicenceAndNamesNoTuiosCommand(t *testing.T) {
	t.Parallel()
	for _, topic := range guideTopics() {
		raw, err := guideFiles.ReadFile("guide/" + topic + ".md")
		if err != nil {
			t.Fatal(err)
		}
		text := string(raw)
		if !strings.Contains(text, "MIT License, Copyright (c) 2025 Gaurav Gosain") {
			t.Errorf("guide %s lacks the tuios licence header", topic)
		}
		for _, banned := range []string{"tuios new-window", "tuios send", "tuios capture", "tuios list", "tuios --skill", "tuios wait-for", "mail"} {
			if strings.Contains(text, banned) {
				t.Errorf("guide %s names %q", topic, banned)
			}
		}
	}
}
