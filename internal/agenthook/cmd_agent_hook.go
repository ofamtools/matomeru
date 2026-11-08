// Adapted from tuios (github.com/Gaurav-Gosain/tuios) v0.12.1 cmd/tuios, MIT License,
// Copyright (c) 2025 Gaurav Gosain. See LICENSE-tuios in this directory.

package agenthook

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"ofam.dev/matomeru/internal/daemon"
	"strings"
	"time"
)

// agentHookDeadline bounds a whole hook run: reading the payload, finding the
// pane and reporting. Harnesses run some hooks synchronously, PreToolUse and
// PermissionRequest among them, so a hook that waits on a stuck or restarting
// daemon slows every tool call. Past the deadline the hook gives up and exits
// 0 having printed nothing, which a harness reads as "no opinion".
const agentHookDeadline = 500 * time.Millisecond

// agentHookMaxPayload caps what is read from stdin. A payload is a few
// kilobytes; the cap only guards against a harness piping something else.
const agentHookMaxPayload = 4 << 20

// agentHookHoldMax bounds the wait for an answer from the Inbox. The daemon
// ends every hold within 300 seconds; this only guards against a daemon that
// stops answering, and stays under the 310 second limit the Claude Code
// integration gives the hook, so the hook still exits on its own.
const agentHookHoldMax = 305 * time.Second

// timedCaller is a verbCaller whose call can wait longer than the default.
type timedCaller interface {
	CallWithTimeout(verb string, params any, timeout time.Duration) (json.RawMessage, error)
}

// verbCaller is the one method the hook needs from a verb client, so a test
// can stand in for the daemon.
type verbCaller interface {
	Call(verb string, params any) (json.RawMessage, error)
}

// agentHookOptions are the flags of tuios agent-hook.
type agentHookOptions struct {
	session string
	window  string
	explain bool
	timeout time.Duration
	// integration is the version marker a managed hook entry carries. It is
	// accepted so the command runs, and otherwise ignored.
	integration int
}

// agentHookIO is everything a hook run touches outside itself.
type agentHookIO struct {
	stdin    io.Reader
	stdinTTY bool
	stdout   io.Writer
	stderr   io.Writer
	getenv   func(string) string
	dial     func() (verbCaller, error)
	self     func() (sid int, ancestors []int)
	// harnessPID names the harness process among the ancestors self reports.
	harnessPID func(ancestors []int) int
	// holdMax bounds the wait for an answer from the Inbox. Zero means
	// agentHookHoldMax.
	holdMax time.Duration
	// sendCwd sends the folder the harness reports (agent_cwd). Off by
	// default: the daemon derives a pane's folder from the PTY (the
	// foreground process's folder, OSC 7), and accepts agent_cwd only when it
	// equals that folder.
	sendCwd bool
	// noContext leaves out the session hint a SessionStart adds to the
	// agent's context.
	noContext bool
	// stampDir is where agent-statusline keeps each pane's last values. A
	// turn end sends what it held back (flushStatusLine). Nil skips that.
	stampDir func() (string, error)
	now      func() time.Time
}

// agentHookOutcome is what --explain prints: the decision, the pane it went
// to and how that pane was found, and what the daemon said.
type agentHookOutcome struct {
	Decision
	Session string `json:"session,omitempty"`
	Window  string `json:"window,omitempty"`
	PaneBy  string `json:"pane_by,omitempty"`
	// HarnessPID is the harness process the hook ran under, 0 when unknown.
	HarnessPID int `json:"harness_pid,omitempty"`
	// Unsupported lists the report's fields the daemon does not know, which
	// were left out of the call.
	Unsupported []string `json:"unsupported,omitempty"`
	Applied     *bool    `json:"applied,omitempty"`
	State       string   `json:"state,omitempty"`
	Reason      string   `json:"reason,omitempty"`
	// ActivityRecorded says whether the daemon kept the event's activity,
	// nil when none was sent.
	ActivityRecorded *bool `json:"activity_recorded,omitempty"`
	// TranscriptRefused is why the daemon did not join the pane to the
	// transcript the hook named.
	TranscriptRefused string `json:"transcript_refused,omitempty"`
	// Subagents is how many subagents the pane holds after a
	// report-agent-activity call, nil when none was made.
	Subagents *int `json:"subagents,omitempty"`
	// ActivityError is why the report-agent-activity call that followed a
	// state report failed. The state report stands.
	ActivityError string `json:"activity_error,omitempty"`
	// Updates is how many of the report's subagent updates the daemon
	// answered.
	Updates int    `json:"updates,omitempty"`
	Error   string `json:"error,omitempty"`
	// Hold is what happened to a prompt the Inbox could answer, when the
	// hook asked for one.
	Hold *approvalTrace `json:"hold,omitempty"`
	// StatusLineFlushed says the turn end sent values the pane's status line
	// feed had held back.
	StatusLineFlushed bool `json:"status_line_flushed,omitempty"`
}

// approvalTrace is the Inbox half of a hook run, for --explain.
type approvalTrace struct {
	RequestID string `json:"request_id,omitempty"`
	Decision  string `json:"decision,omitempty"`
	Reason    string `json:"reason,omitempty"`
	// Printed says the hook printed the harness's decision.
	Printed bool   `json:"printed"`
	Error   string `json:"error,omitempty"`
}

// runAgentHook runs one hook event under the deadline. It returns nothing,
// because nothing it could return may reach the harness as a failure.
func runAgentHook(o agentHookOptions, args []string, hio agentHookIO) {
	harness := args[0]
	if answer := StdoutAnswer(harness); answer != "" {
		// Gemini CLI and Antigravity CLI parse a hook's stdout as JSON. An
		// empty object is the answer that changes nothing, and it goes out
		// first so a deadline cannot leave the hook with no answer at all.
		_, _ = io.WriteString(hio.stdout, answer)
	}
	timeout := o.timeout
	if timeout <= 0 {
		timeout = agentHookDeadline
	}
	done := make(chan agentHookOutcome, 1)
	go func() { done <- agentHook(o, args, hio) }()
	var out agentHookOutcome
	select {
	case out = <-done:
	case <-time.After(timeout):
		out = agentHookOutcome{Decision: Decision{Harness: harness}, Error: "gave up after " + timeout.String()}
	}
	writeContext(out, hio.stdout, hio.noContext)
	if holdable(out) {
		out.Hold = holdForAnswer(out, hio)
	}
	if o.explain {
		line, _ := json.Marshal(out)
		fmt.Fprintln(hio.stderr, string(line))
	}
}

// startHint reports whether a hook run tells the agent where it runs: a
// SessionStart for a harness that reads the hook's output as context, reported
// for a pane the hook found. Outside tuios, or when the report failed, it
// prints nothing. A pane named with --window is a run by hand or by a script,
// and the name it was given is not the pane's id, so that prints nothing too.
func startHint(out agentHookOutcome) bool {
	return out.Event == "SessionStart" && StartsWithContext(out.Harness) &&
		out.Error == "" && (out.Report != nil || out.Context != ContextNone) &&
		out.Window != "" && out.Session != "" && out.PaneBy != "flag"
}

// writeContext prints what the hook adds to the agent's context: where it
// runs, on a SessionStart, in the form the harness reads for the event. The
// session hint is off with noContext. (tuios also added a line about the
// pane's unread mail here; mtm has no mail, so that is not carried over.)
func writeContext(out agentHookOutcome, w io.Writer, noContext bool) {
	if noContext || !startHint(out) {
		return
	}
	text := SessionStartHint(shortWindowID(out.Window), out.Session)
	if text == "" {
		return
	}
	if out.Context == ContextJSON {
		text = ContextJSONOutput(out.Event, strings.TrimSuffix(text, "\n"))
	}
	_, _ = io.WriteString(w, text)
}

// holdable reports whether a hook run may wait for an answer from the Inbox:
// the event is a prompt the harness takes a decision for, and the pane is now
// on needs_input by this hook's own report. A report that failed or was
// refused, a nested harness's for one, holds nothing. A harness outside the
// pane holds nothing even when the pane's own agent is on needs_input: the
// prompt on the pane is not its prompt.
func holdable(out agentHookOutcome) bool {
	return out.Approval != nil && out.Error == "" && out.Report != nil &&
		out.State == "needs_input" && out.Window != "" && out.Reason != "foreign_pane"
}

// holdForAnswer asks the daemon to hold the pane's prompt for the person and
// prints the harness's decision when one comes back. Whatever goes wrong, it
// prints nothing: a harness that gets no answer asks in its own pane, so
// silence is always safe and an approval can only come from the person.
func holdForAnswer(out agentHookOutcome, hio agentHookIO) *approvalTrace {
	limit := hio.holdMax
	if limit <= 0 {
		limit = agentHookHoldMax
	}
	type answer struct {
		trace    approvalTrace
		decision string
		message  string
	}
	done := make(chan answer, 1)
	go func() {
		var a answer
		a.decision, a.message, a.trace = requestAnswer(out, hio, limit)
		done <- a
	}()
	var a answer
	select {
	case a = <-done:
	case <-time.After(limit):
		// The call may still come back; nothing reads it, so nothing it says
		// is printed.
		return &approvalTrace{Error: "gave up after " + limit.String()}
	}
	if a.decision == "" {
		return &a.trace
	}
	text, ok := out.Approval.Answer(out.Harness, a.decision, a.message)
	if !ok {
		a.trace.Error = "the harness was not offered " + a.decision + ", so nothing was printed"
		return &a.trace
	}
	if _, err := io.WriteString(hio.stdout, text); err != nil {
		a.trace.Error = "writing the decision: " + err.Error()
		return &a.trace
	}
	a.trace.Printed = true
	return &a.trace
}

// requestAnswer is the request-approval call. It returns the decision, empty
// for none, and what happened.
func requestAnswer(out agentHookOutcome, hio agentHookIO, limit time.Duration) (string, string, approvalTrace) {
	var trace approvalTrace
	client, err := hio.dial()
	if err != nil {
		trace.Error = err.Error()
		return "", "", trace
	}
	if c, ok := client.(io.Closer); ok {
		defer func() { _ = c.Close() }()
	}
	params := map[string]any{
		"session": out.Session,
		"window":  out.Window,
		"harness": out.Harness,
		"options": out.Approval.Options,
		// The line the person answers from. The daemon shows it on the
		// held item and refuses a hold whose line it would have to cut.
		"summary": out.Report.Message,
	}
	if len(out.Approval.Scope) > 0 {
		params["always_scope"] = out.Approval.Scope
	}
	// The fields the risk rules, plans and deny reasons need go only to a
	// daemon whose request-approval lists them. An older daemon would ignore
	// them, which for a plan would hold it as a plain approval the person
	// could allow without reading, so a plan is not sent to one at all.
	extra := map[string]any{}
	if a := out.Approval; a.IsPlan() {
		extra["kind"], extra["plan"] = KindPlan, a.Plan
	} else {
		if a.Tool != "" {
			extra["tool"] = a.Tool
		}
		if a.Target != "" {
			extra["target"] = a.Target
		}
		if a.Cwd != "" {
			extra["cwd"] = a.Cwd
		}
	}
	if out.Approval.DenyMessage {
		extra["deny_message"] = true
	}
	if len(extra) > 0 {
		known, err := verbParams(client, "request-approval")
		if err != nil {
			trace.Error = "could not ask the daemon what request-approval takes: " + err.Error()
			return "", "", trace
		}
		if out.Approval.IsPlan() && !known["plan"] {
			trace.Error = "the running daemon predates plans in the Inbox, so the plan was left to the pane"
			return "", "", trace
		}
		for k, v := range extra {
			if known[k] {
				params[k] = v
			}
		}
	}
	var raw json.RawMessage
	if tc, ok := client.(timedCaller); ok {
		raw, err = tc.CallWithTimeout("request-approval", params, limit)
	} else {
		raw, err = client.Call("request-approval", params)
	}
	if err != nil {
		// unknown_verb from a daemon older than approvals lands here too.
		trace.Error = err.Error()
		return "", "", trace
	}
	var res struct {
		RequestID string `json:"request_id"`
		Decision  string `json:"decision"`
		Message   string `json:"message"`
		Reason    string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		trace.Error = "reading the answer: " + err.Error()
		return "", "", trace
	}
	trace.RequestID, trace.Decision, trace.Reason = res.RequestID, res.Decision, res.Reason
	return res.Decision, res.Message, trace
}

// agentHook reads, decides, resolves and reports.
func agentHook(o agentHookOptions, args []string, hio agentHookIO) agentHookOutcome {
	in := Input{Getenv: hio.getenv}
	rest := args[1:]
	// Codex's notify command appends its JSON payload as the last argument.
	if n := len(rest); n > 0 && strings.HasPrefix(strings.TrimSpace(rest[n-1]), "{") {
		in.Payload = []byte(rest[n-1])
		rest = rest[:n-1]
	}
	if len(rest) > 0 {
		in.Event = rest[0]
	}
	if in.Payload == nil && hio.stdin != nil && !hio.stdinTTY {
		data, err := io.ReadAll(io.LimitReader(hio.stdin, agentHookMaxPayload))
		if err != nil {
			return agentHookOutcome{Decision: Decision{Harness: args[0], Event: in.Event}, Error: "reading stdin: " + err.Error()}
		}
		in.Payload = data
	}
	out := agentHookOutcome{Decision: Translate(args[0], in)}
	if out.Report == nil && out.Context == ContextNone {
		return out
	}

	client, err := hio.dial()
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if c, ok := client.(io.Closer); ok {
		defer func() { _ = c.Close() }()
	}
	var sid int
	var ancestors []int
	if hio.self != nil {
		sid, ancestors = hio.self()
	}
	if hio.harnessPID != nil {
		out.HarnessPID = hio.harnessPID(ancestors)
	}
	out.Session, out.Window, out.PaneBy, err = resolveHookPane(o, hio, client, sid, ancestors)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if out.Report != nil {
		out = reportHookEvent(out, client, hio)
	}
	return out
}

// reportHookEvent sends what one hook event reports, once the pane is found.
func reportHookEvent(out agentHookOutcome, client verbCaller, hio agentHookIO) agentHookOutcome {
	if out.Report.SessionOnly {
		res, dropped, err := reportHookSession(client, out.Session, out.Window, out.Harness, out.HarnessPID, *out.Report)
		out.Unsupported = dropped
		if err != nil {
			out.Error = err.Error()
			return out
		}
		out.Applied, out.Reason = &res.Applied, res.Reason
		out.TranscriptRefused = res.TranscriptRefused
		return out
	}
	// An event set-agent-state does not take goes with report-agent-activity:
	// on its own for a report with no state, and after the state report for
	// one with a state, so a new conversation has taken the pane over by
	// the time its session_start is checked against it.
	r := *out.Report
	if !hio.sendCwd {
		r.Cwd = ""
	}
	var alone *Activity
	if r.Activity != nil && !StateActivity(r.Activity.Event) {
		alone, r.Activity = r.Activity, nil
	}
	if r.State != "" {
		res, dropped, err := reportHook(client, out.Session, out.Window, out.Harness, out.HarnessPID, r)
		out.Unsupported = dropped
		switch {
		case errors.Is(err, errConditionUnsupported):
			out.Error = err.Error()
		case err != nil:
			out.Error = err.Error()
			return out
		default:
			out.Applied, out.State, out.Reason = &res.Applied, res.State, res.Reason
			out.ActivityRecorded = res.ActivityRecorded
			out.TranscriptRefused = res.TranscriptRefused
			out.StatusLineFlushed = flushAtTurnEnd(out, res, client, hio)
		}
	}
	if alone != nil {
		res, err := reportHookActivity(client, out.Session, out.Window, out.Harness, out.HarnessPID, r.SessionID, *alone)
		switch {
		case err != nil && r.State == "":
			out.Error = err.Error()
			return out
		case err != nil:
			out.ActivityError = err.Error()
		default:
			out.ActivityRecorded, out.Subagents = &res.Recorded, &res.Subagents
			if r.State == "" {
				out.State, out.Reason = res.State, res.Reason
			}
		}
	}
	// The subagent updates the same event carries, in order. A daemon from
	// before subagent_update refuses each with invalid_params, which changes
	// nothing; the first refusal ends the run, since the rest would be
	// refused alike.
	for _, a := range r.Extra {
		res, err := reportHookActivity(client, out.Session, out.Window, out.Harness, out.HarnessPID, r.SessionID, a)
		if err != nil {
			out.ActivityError = err.Error()
			break
		}
		out.Updates++
		out.Subagents = &res.Subagents
	}
	return out
}

// hookActivityResult is the part of report-agent-activity's answer the hook
// reads.
type hookActivityResult struct {
	State     string `json:"state"`
	Recorded  bool   `json:"recorded"`
	Reason    string `json:"reason"`
	Subagents int    `json:"subagents"`
}

// reportHookActivity sends one hook event with report-agent-activity. A
// daemon from before the verb answers unknown_verb, which changes nothing, so
// the hook does not ask list-verbs first; the error says why nothing was
// recorded.
func reportHookActivity(client verbCaller, sess, window, harness string, harnessPID int, sessionID string, a Activity) (hookActivityResult, error) {
	params := map[string]any{
		"session":  sess,
		"window":   window,
		"harness":  harness,
		"activity": a,
	}
	if sessionID != "" {
		params["agent_session_id"] = sessionID
		if harnessPID > 1 {
			params["harness_pid"] = harnessPID
		}
	}
	raw, err := client.Call("report-agent-activity", params)
	if verr, ok := errors.AsType[*daemon.Error](err); ok && verr.Code == errUnknownVerb {
		return hookActivityResult{}, errors.New("the running daemon predates report-agent-activity, so the " + a.Event + " was not reported. It works once the daemon restarts")
	}
	if err != nil {
		return hookActivityResult{}, err
	}
	var res hookActivityResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return hookActivityResult{}, err
	}
	return res, nil
}

// flushAtTurnEnd sends what the pane's status line feed held back when this
// report ends a turn, since the status line does not run again until the
// conversation changes. A report the identity guard refused is a nested
// run's, or a harness's that runs outside the pane, and its turn is not the
// pane's.
func flushAtTurnEnd(out agentHookOutcome, res hookReportResult, client verbCaller, hio agentHookIO) bool {
	if hio.stampDir == nil || out.Report == nil {
		return false
	}
	if st := out.Report.State; st != "done" && st != "errored" {
		return false
	}
	if res.Reason == "foreign_session" || res.Reason == "foreign_harness" || res.Reason == "foreign_pane" {
		return false
	}
	dir, err := hio.stampDir()
	if err != nil {
		return false
	}
	now := time.Now
	if hio.now != nil {
		now = hio.now
	}
	sent, _ := flushStatusLine(client, out.Session, out.Window, dir, now())
	return sent
}

// resolveHookPane finds the pane to report for: the --window flag, then
// TUIOS_PANE_ID, then the daemon's resolve-pane on the process's terminal
// session and its ancestors.
func resolveHookPane(o agentHookOptions, hio agentHookIO, client verbCaller, sid int, ancestors []int) (sess, window, by string, err error) {
	if o.window != "" {
		return cmp.Or(o.session, hio.getenv("TUIOS_SESSION")), o.window, "flag", nil
	}
	if id := hio.getenv("TUIOS_PANE_ID"); id != "" {
		return cmp.Or(o.session, hio.getenv("TUIOS_SESSION")), id, "env", nil
	}
	if sid <= 1 && len(ancestors) == 0 {
		return "", "", "", errors.New("no pane: TUIOS_PANE_ID is unset")
	}
	raw, err := client.Call("resolve-pane", map[string]any{"sid": sid, "pids": ancestors})
	if err != nil {
		return "", "", "", fmt.Errorf("no pane: TUIOS_PANE_ID is unset and %w", err)
	}
	var res struct {
		Session  string `json:"session"`
		WindowID string `json:"window_id"`
		By       string `json:"by"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", "", "", err
	}
	return res.Session, res.WindowID, res.By, nil
}

// hookReportResult is the part of set-agent-state's answer the hook reads.
type hookReportResult struct {
	Applied          bool   `json:"applied"`
	State            string `json:"state"`
	Reason           string `json:"reason"`
	ActivityRecorded *bool  `json:"activity_recorded"`
	// TranscriptRefused is why the daemon did not join the pane to the
	// transcript_path the report named, "" when it did or none was named.
	TranscriptRefused string `json:"transcript_refused"`
}

// hookFields are the set-agent-state params a hook report may carry beyond
// the ones every daemon takes. activity is the hook event for the pane's
// activity ring; a daemon from before it drops it, and the report goes
// without it.
var hookFields = []string{"kind", "agent_session_id", "transcript_path", "transcript_leaf", "if_state", "subagent_id", "if_subagent_id", "harness_pid", "activity", "agent_cwd", "session_start"}

// setAgentStateParams asks the daemon which params its set-agent-state takes.
//
// A daemon decodes params leniently and ignores a name it does not know, so a
// daemon older than the hook fields would not refuse them: it would take the
// report and drop the fields without a word. For most of them that only loses
// information, but dropping if_state turns a conditional report into an
// unconditional one, and Claude Code's idle_prompt would then overwrite done
// about a minute after every turn. So the hook asks first. The daemon keeps
// running across a tuios upgrade, which makes a new hook talking to an older
// daemon the ordinary case right after one.
func setAgentStateParams(client verbCaller) (map[string]bool, error) {
	return verbParams(client, "set-agent-state")
}

// verbParams asks the daemon which params its verb takes, by name.
func verbParams(client verbCaller, verb string) (map[string]bool, error) {
	raw, err := client.Call("list-verbs", map[string]any{"verb": verb})
	if err != nil {
		return nil, err
	}
	var res struct {
		Verbs []struct {
			Verb   string `json:"verb"`
			Params []struct {
				Name string `json:"name"`
			} `json:"params"`
		} `json:"verbs"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, v := range res.Verbs {
		if v.Verb != verb {
			continue
		}
		for _, p := range v.Params {
			known[p.Name] = true
		}
	}
	return known, nil
}

// requireIfState returns an error unless the daemon's set-agent-state takes
// if_state. tuios set-agent-state --if-state calls it before sending.
func requireIfState(client verbCaller) error {
	known, err := setAgentStateParams(client)
	if err != nil {
		return fmt.Errorf("could not ask the daemon whether it supports --if-state: %w", err)
	}
	if !known["if_state"] {
		return errors.New("the running daemon predates --if-state, so the report was not sent. It works once the daemon restarts")
	}
	return nil
}

// errConditionUnsupported is reportHook's error for a report whose condition
// the daemon predates, which is not sent. What else the event reports still
// goes.
var errConditionUnsupported = errors.New("the daemon predates if_subagent_id, so the subagent's report was not sent. It works once the daemon restarts")

// reportHook sends one report. The hook fields go only to a daemon whose
// set-agent-state lists them, and the ones it does not are returned as
// dropped. A report with if_state is not sent at all to a daemon without it,
// since sent without its condition it could turn a finished pane back to
// working.
func reportHook(client verbCaller, sess, window, harness string, harnessPID int, r Report) (hookReportResult, []string, error) {
	params := map[string]any{
		"session": sess,
		"window":  window,
		"state":   r.State,
		"harness": harness,
	}
	if r.Message != "" {
		params["message"] = r.Message
	}
	extra := map[string]any{}
	for k, v := range map[string]string{
		"kind":             r.Kind,
		"agent_session_id": r.SessionID,
		"transcript_path":  r.TranscriptPath,
		"if_state":         r.IfState,
		"subagent_id":      r.SubagentID,
		"if_subagent_id":   r.IfSubagentID,
		"agent_cwd":        r.Cwd,
	} {
		if v != "" {
			extra[k] = v
		}
	}
	if harnessPID > 1 && r.SessionID != "" {
		extra["harness_pid"] = harnessPID
	}
	if r.Activity != nil {
		extra["activity"] = r.Activity
	}
	if r.SessionStart && r.SessionID != "" {
		extra["session_start"] = true
	}
	if r.TranscriptLeaf != nil {
		extra["transcript_leaf"] = *r.TranscriptLeaf
	}
	var dropped []string
	if len(extra) > 0 {
		known, err := setAgentStateParams(client)
		if err != nil {
			return hookReportResult{}, nil, fmt.Errorf("could not ask the daemon what set-agent-state takes: %w", err)
		}
		for _, k := range hookFields {
			if _, ok := extra[k]; !ok {
				continue
			}
			if !known[k] {
				dropped = append(dropped, k)
				continue
			}
			params[k] = extra[k]
		}
		if r.IfState != "" && !known["if_state"] {
			return hookReportResult{}, dropped, errors.New("the daemon predates if_state, so a conditional report was not sent")
		}
		if r.IfSubagentID != "" && !known["if_subagent_id"] {
			// Sent without it, a subagent's report would clear the main
			// agent's prompt as well.
			return hookReportResult{}, dropped, errConditionUnsupported
		}
	}
	raw, err := client.Call("set-agent-state", params)
	if err != nil {
		return hookReportResult{}, dropped, err
	}
	var res hookReportResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return hookReportResult{}, dropped, err
	}
	return res, dropped, nil
}

// reportHookSession sends an identity-only report with set-agent-session. A
// daemon older than the verb would answer unknown_verb, which costs nothing,
// but the hook asks list-verbs first anyway so --explain can say why nothing
// was stored.
func reportHookSession(client verbCaller, sess, window, harness string, harnessPID int, r Report) (hookReportResult, []string, error) {
	raw, err := client.Call("list-verbs", map[string]any{"verb": "set-agent-session"})
	if err != nil {
		return hookReportResult{}, nil, fmt.Errorf("could not ask the daemon whether it has set-agent-session: %w", err)
	}
	var verbs struct {
		Verbs []struct {
			Verb   string `json:"verb"`
			Params []struct {
				Name string `json:"name"`
			} `json:"params"`
		} `json:"verbs"`
	}
	if err := json.Unmarshal(raw, &verbs); err != nil {
		return hookReportResult{}, nil, err
	}
	known := false
	fields := map[string]bool{}
	for _, v := range verbs.Verbs {
		known = known || v.Verb == "set-agent-session"
		if v.Verb == "set-agent-session" {
			for _, p := range v.Params {
				fields[p.Name] = true
			}
		}
	}
	if !known {
		return hookReportResult{}, nil, errors.New("the running daemon predates set-agent-session, so the session was not reported. It works once the daemon restarts")
	}
	params := map[string]any{
		"session":          sess,
		"window":           window,
		"harness":          harness,
		"agent_session_id": r.SessionID,
	}
	if harnessPID > 1 {
		params["harness_pid"] = harnessPID
	}
	var dropped []string
	for _, key := range []string{"transcript_path", "transcript_leaf"} {
		var value any
		if key == "transcript_path" && r.TranscriptPath != "" {
			value = r.TranscriptPath
		}
		if key == "transcript_leaf" && r.TranscriptLeaf != nil {
			value = *r.TranscriptLeaf
		}
		if value == nil {
			continue
		}
		if fields[key] {
			params[key] = value
		} else {
			dropped = append(dropped, key)
		}
	}
	raw, err = client.Call("set-agent-session", params)
	if err != nil {
		return hookReportResult{}, dropped, err
	}
	var res hookReportResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return hookReportResult{}, dropped, err
	}
	return res, dropped, nil
}
