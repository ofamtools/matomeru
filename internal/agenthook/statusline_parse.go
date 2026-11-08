// Adapted from tuios (github.com/Gaurav-Gosain/tuios) v0.12.1, MIT License,
// Copyright (c) 2025 Gaurav Gosain. See LICENSE-tuios in this directory.

package agenthook

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Status line feed: the model, context use and cost a harness shows the
// person, written to the pane's agent metadata.
//
// Claude Code runs one status line command with a JSON object on stdin every
// time the conversation changes (https://code.claude.com/docs/en/statusline).
// tuios can own that one slot, statusLine in the same settings.json the hooks
// live in, with a command that runs "tuios agent-statusline claude-code". It is
// opt in, through integration install --statusline, because the slot is the
// person's: a status line they wrote is never replaced. They can keep it by
// installing with --then and their command, which the wrapper runs with the
// same stdin, printing its output unchanged. Uninstall puts their command back.
//
// The opencode plugin feeds the same wrapper from its message events, since
// opencode has no status line command (see assets/opencode).
//
// Every field of every payload is optional. A field that is missing, null or
// of another type is left out, and a payload with none of them writes nothing.

// StatusLineVersion is the version of the statusLine entry this build writes.
const StatusLineVersion = 1

// statusLineVerb is the subcommand the managed entry runs.
const statusLineVerb = "agent-statusline"

// StatusLineMaxPayload caps what the wrapper reads from stdin (the cap
// agent-deck uses for the same payload).
const StatusLineMaxPayload = 1 << 20

// Meta keys the status line feed writes, and the source it writes them under.
const (
	MetaModel   = "model"
	MetaContext = "context"
	MetaCost    = "cost"
	MetaPlan    = "plan"
	// StatusLineSource is the set-agent-meta source of the wrapper's keys.
	StatusLineSource = "statusline"
)

// StatusLineHarnesses lists the harnesses the wrapper reads a payload for.
func StatusLineHarnesses() []string { return []string{ClaudeCode, OpenCode, Kilo} }

// StatusLineValues are what one payload says, formatted for the rail. An
// empty field is unstated.
type StatusLineValues struct {
	Model   string `json:"model,omitempty"`
	Context string `json:"context,omitempty"`
	Cost    string `json:"cost,omitempty"`
	// Session is the harness's conversation id, when the payload names it.
	// It is not written anywhere; the wrapper uses it to tell a new
	// conversation from the last one.
	Session string `json:"session,omitempty"`
}

// Tokens are the values as set-agent-meta tokens, only the stated ones.
func (v StatusLineValues) Tokens() map[string]string {
	out := map[string]string{}
	for k, val := range map[string]string{MetaModel: v.Model, MetaContext: v.Context, MetaCost: v.Cost} {
		if val != "" {
			out[k] = val
		}
	}
	return out
}

// Empty reports whether the payload stated nothing the rail can show.
func (v StatusLineValues) Empty() bool { return v.Model == "" && v.Context == "" && v.Cost == "" }

// ParseStatusLine reads one status line payload for harness.
//
// Claude Code: model.display_name, else model.id; context_window.used_percentage;
// cost.total_cost_usd; session_id. opencode and Kilo (from tuios's own plugin):
// modelID, cost, session_id.
func ParseStatusLine(harness string, payload []byte) (StatusLineValues, error) {
	id, ok := Canonical(harness)
	if !ok || !slices.Contains(StatusLineHarnesses(), id) {
		return StatusLineValues{}, fmt.Errorf("no status line feed for %s. Available: %s", harness, strings.Join(StatusLineHarnesses(), ", "))
	}
	trimmed := strings.TrimSpace(string(payload))
	if !strings.HasPrefix(trimmed, "{") {
		return StatusLineValues{}, errors.New("payload is not a JSON object")
	}
	payloadMap := map[string]any{}
	if err := json.Unmarshal([]byte(trimmed), &payloadMap); err != nil {
		return StatusLineValues{}, fmt.Errorf("payload is not a JSON object: %w", err)
	}
	p := fields(payloadMap)
	var v StatusLineValues
	v.Session = p.str("session_id")
	switch id {
	case ClaudeCode:
		m := p.obj("model")
		v.Model = m.first("display_name", "id")
		if pct, ok := p.obj("context_window").num("used_percentage"); ok {
			v.Context = FormatPercent(pct)
		}
		if usd, ok := p.obj("cost").num("total_cost_usd"); ok {
			v.Cost = FormatCost(usd, "USD")
		}
	default:
		v.Model = p.str("modelID")
		if usd, ok := p.num("cost"); ok {
			v.Cost = FormatCost(usd, "USD")
		}
	}
	return v, nil
}

// num reads a finite number.
func (f fields) num(key string) (float64, bool) {
	v, ok := f[key].(float64)
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

// FormatPercent is a context use as the rail shows it: whole percent, 0 to
// 100.
func FormatPercent(pct float64) string {
	pct = math.Max(0, math.Min(100, pct))
	return strconv.Itoa(int(math.Round(pct))) + "%"
}

// FormatCost is a cost as the rail shows it: dollars with two decimals, or the
// amount and the currency code for any other currency. A negative cost is
// unstated and gives "".
func FormatCost(amount float64, currency string) string {
	if amount < 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return ""
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" || currency == "USD" {
		return "$" + strconv.FormatFloat(amount, 'f', 2, 64)
	}
	return strconv.FormatFloat(amount, 'f', 2, 64) + " " + currency
}

// StatusLineForeign reports whether TUIOS_AGENT names a harness other than
// harness, the same rule the hooks apply: a status line then belongs to an
// agent nested inside the pane's own, and is not reported.
func StatusLineForeign(harness string, getenv func(string) string) (string, bool) {
	if getenv == nil {
		return "", false
	}
	id, _ := Canonical(harness)
	hint := strings.TrimSpace(getenv(AgentHintEnv))
	if hint == "" {
		return "", false
	}
	owner, known := hintOwner(hint)
	if !known {
		return "", false
	}
	// Kilo is an opencode fork that shares its plugin.
	if owner == id || (owner == OpenCode && id == Kilo) || (owner == Kilo && id == OpenCode) {
		return "", false
	}
	return owner, true
}
