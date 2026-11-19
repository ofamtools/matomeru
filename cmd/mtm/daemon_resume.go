package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"ofam.dev/ochazuke"
)

// Resuming agents after a fresh restart. A fresh restart (daemon.restart or
// daemon.upgrade with fresh, and upgrade's or-restart fallback) stops the
// daemon and starts it again: the daemon restores every session, window,
// layout and folder with a new shell in each pane, and the agents those panes
// ran are gone. Their conversations are not: the daemon keeps each window's
// agent_session_id across the restore. So before the stop mtm notes the panes
// running an agent (a harness attribution in list-agents), and after the start
// it asks the daemon to resume each one (pane.resume, over the daemon's
// resume-agent verb). The resume command is the daemon's harness data: its
// manifest's [resume] argv with the conversation id (mtmd
// internal/harness/manifests). The daemon types it only into a pane whose own
// shell holds the terminal, holds the id to one plain shell word, and closes
// the pane's resume item in the Inbox. A pane with no recorded conversation,
// or whose harness has no resume command, stays a shell and is reported
// skipped.

const (
	// settingResumeAgents is whether a fresh restart resumes the agents that
	// ran before it.
	settingResumeAgents = "daemon.resume-agents"
	// resumeWait bounds how long a fresh restart waits for each restored
	// pane's shell to take its resume command (the daemon answers not_ready
	// until the shell holds the terminal).
	resumeWait = 15 * time.Second
	// resumePoll is the pause between those tries.
	resumePoll = 250 * time.Millisecond
)

// freshAbout is what the fresh parameter does, for both verbs.
const freshAbout = "Stop the daemon and start it on the installed mtm, which ends the programs in its panes (they are restored from saved state with new shells; with daemon.resume-agents on, the agents they ran are resumed on their conversations)"

func resumeSettings() []ochazuke.Setting {
	return []ochazuke.Setting{
		{Name: settingResumeAgents, Type: ochazuke.SettingBoolean, Default: true, Section: "daemon",
			Description: "After a fresh restart (daemon.restart or daemon.upgrade with fresh, or upgrade's or-restart fallback), resume each agent that ran before the stop in its restored pane (pane.resume: the harness's resume command on the conversation the agent reported). A pane with no recorded conversation, or whose harness has no resume command, stays a shell. Off leaves every restored pane a shell (the daemon's own daemon.resume_agents mode still applies)"},
	}
}

// agentResume is what became of one pane's resume.
type agentResume struct {
	Session string `json:"session"`
	Window  string `json:"window"`
	// Label is the pane as the pane list names it (pane.label).
	Label        string `json:"label,omitempty"`
	Harness      string `json:"harness,omitempty"`
	AgentSession string `json:"agent_session_id,omitempty"`
	// Command is the resume command, as typed (or, for a dry run, as it
	// would be).
	Command string `json:"command,omitempty"`
	Warning string `json:"warning,omitempty"`
	Resumed bool   `json:"resumed"`
	// Reason is why the pane was skipped; empty when it was resumed (or a
	// dry run found its command).
	Reason string `json:"reason,omitempty"`
}

// liveAgents are the panes running an agent now (list-agents attributes a
// harness to them). A listing that fails is no agents: the restart goes on.
func (a *App) liveAgents(ctx context.Context) []pane {
	if a.panesd == nil {
		return nil
	}
	panes, err := a.panesd.ListPanes(ctx)
	if err != nil {
		return nil
	}
	stampNames(panes) // the names the pane list gives them (claude-2)
	var live []pane
	for _, p := range panes {
		if p.ID != "" && p.Harness != "" && !p.Exited {
			live = append(live, p)
		}
	}
	return live
}

// resumeAgents resumes each of live in its restored pane, waiting up to wait
// for each shell to take the command.
func (a *App) resumeAgents(ctx context.Context, live []pane, wait time.Duration) []agentResume {
	var out []agentResume
	for _, p := range live {
		if p.AgentSession == "" {
			out = append(out, agentResume{Session: p.Session, Window: p.ID, Label: p.label(), Harness: p.Harness,
				Reason: "no conversation is recorded for it, so it stays a shell"})
			continue
		}
		r := a.resumeAgent(ctx, p.Session, p.ID, wait, false)
		r.Label = p.label()
		r.Harness = cmp.Or(r.Harness, p.Harness)
		r.AgentSession = cmp.Or(r.AgentSession, p.AgentSession)
		out = append(out, r)
	}
	return out
}

// resumeAgent asks the daemon to resume the conversation recorded for a
// window (resume-agent), or with dryRun only what it would type. While the
// pane's shell does not hold the terminal yet (not_ready) it tries again
// until wait has passed.
func (a *App) resumeAgent(ctx context.Context, session, window string, wait time.Duration, dryRun bool) agentResume {
	res := agentResume{Session: session, Window: window}
	if a.daemon == nil {
		res.Reason = "no daemon client"
		return res
	}
	params := map[string]any{"window": window}
	if session != "" {
		params["session"] = session
	}
	if dryRun {
		params["dry_run"] = true
	}
	sleep := a.sleep
	if sleep == nil {
		sleep = func(c context.Context, d time.Duration) error {
			select {
			case <-c.Done():
				return c.Err()
			case <-time.After(d):
				return nil
			}
		}
	}
	deadline := time.Now().Add(wait)
	for {
		var out struct {
			Harness string `json:"harness"`
			Session string `json:"agent_session_id"`
			Command string `json:"command"`
			Warning string `json:"warning"`
			Typed   bool   `json:"typed"`
		}
		call, cancel := context.WithTimeout(ctx, daemonTimeout)
		err := a.daemon.Call(call, "resume-agent", params, &out)
		cancel()
		if err == nil {
			res.Harness, res.AgentSession, res.Command, res.Warning, res.Resumed = out.Harness, out.Session, out.Command, out.Warning, out.Typed
			return res
		}
		if daemonCode(err) == "not_ready" && time.Now().Before(deadline) {
			if sleep(ctx, resumePoll) == nil {
				continue
			}
		}
		res.Reason = err.Error()
		return res
	}
}

// resumeSummary is the notice a fresh restart gives about its agents.
func resumeSummary(rs []agentResume) string {
	var resumed, skipped []string
	for _, r := range rs {
		if r.Resumed {
			resumed = append(resumed, cmp.Or(r.Label, r.Window))
		} else {
			skipped = append(skipped, cmp.Or(r.Label, r.Window)+": "+r.Reason)
		}
	}
	var parts []string
	if len(resumed) > 0 {
		parts = append(parts, fmt.Sprintf("resumed %d agent(s): %s", len(resumed), strings.Join(resumed, ", ")))
	}
	if len(skipped) > 0 {
		parts = append(parts, fmt.Sprintf("skipped %d: %s", len(skipped), strings.Join(skipped, "; ")))
	}
	return "daemon restart: " + strings.Join(parts, "; ")
}

// resumeAgentVerb is pane.resume (nouns/pane.json): resume the
// conversation recorded for a pane (window, else the pane at the focus) in
// that pane's shell.
func (a *App) resumeAgentVerb(ctx ochazuke.Ctx) ochazuke.Result {
	session, window := ochazuke.StringArg(ctx.Args, "session", ""), ochazuke.StringArg(ctx.Args, "window", "")
	if window == "" {
		p, err := a.paneAt(ctx.Instance)
		if err != nil {
			return ochazuke.Failed(err)
		}
		session, window = p.Session, p.ID
	} else if session == "" {
		for _, p := range a.panes {
			if p.ID == window {
				session = p.Session
				break
			}
		}
	}
	dry := boolArg(ctx.Args, "dry-run", false)
	return ochazuke.Pending(func(c context.Context) (any, error) {
		r := a.resumeAgent(c, session, window, 0, dry)
		if r.Reason != "" {
			return r, errors.New(r.Reason)
		}
		return r, nil
	}).Then(func(_ ochazuke.Ctx, _ any, err error) {
		if err == nil && !dry {
			_ = a.reloadPanes()
		}
	})
}
