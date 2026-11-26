package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/oden"
)

var hookSocketParam = oden.Parameter{
	Name: "socket", Type: "string",
	Description: "Unix domain socket path (default: $MTM_SOCKET or ~/.local/share/matomeru/mtm.sock)",
}

var hookAddParams = []oden.Parameter{
	{Name: "id", Type: "string", Description: "Rule ID (generated when omitted; an existing ID is replaced)"},
	{Name: "on", Type: "string", Default: daemon.EventAgentDone, Description: "Event name to match (* for any)"},
	{Name: "once", Type: "bool", Description: "Remove the rule after it fires once"},
	{Name: "harness", Type: "string", Description: "Match only this harness (claude, codex, ...)"},
	{Name: "pane", Type: "string", Description: "Match only this pane ID"},
	{Name: "lane", Type: "string", Description: "Match only panes claimed by this lane ID"},
	{Name: "project", Type: "string", Description: "Match only panes in this project (root task ID)"},
	{Name: "run", Type: "string", Description: "Shell command run via /bin/sh -c; event JSON on stdin"},
	{Name: "argv", Type: "string", Repeatable: true, Description: "Exact command argv element (repeat in order); event JSON on stdin"},
	{Name: "webhook", Type: "string", Description: "URL that receives the event JSON as an HTTP POST body"},
	{Name: "header", Type: "string", Repeatable: true, Description: "Webhook header Name=Value (${VAR} expands from the daemon environment)"},
	{Name: "timeout-sec", Type: "int", Default: "0", Description: "Per-attempt timeout in seconds (0 = 15s)"},
	{Name: "memory", Type: "bool", Description: "Register in the running daemon only instead of hooks.json"},
	hookSocketParam,
}

var hookTestParams = []oden.Parameter{
	{Name: "pane", Type: "string", Description: "Pane ID for the synthetic event (default $MTM_PANE_ID)"},
	{Name: "harness", Type: "string", Description: "Harness for the synthetic event"},
	hookSocketParam,
}

var hookLogParams = []oden.Parameter{
	{Name: "limit", Type: "int", Default: "20", Description: "Most recent deliveries to show (0 = all)"},
	hookSocketParam,
}

// mtmHookCommand is `mtm hook`: manage declarative agent.done callbacks.
func mtmHookCommand() oden.Command {
	socketOnly := []oden.Parameter{hookSocketParam}
	return oden.Command{
		Name:     "hook",
		Summary:  "manage agent.done callbacks (hooks.json rules and in-daemon one-shots)",
		Category: "Daemon",
		Children: []oden.Command{
			{
				Name: "list", Summary: "list callback rules from hooks.json and the running daemon",
				Usage: "[--json]", Parameters: socketOnly,
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(socketOnly)
					if err != nil {
						return err
					}
					rules, err := listHookRules(c.Context, p.String("socket"))
					if err != nil {
						return err
					}
					if p.Format() == "json" {
						return printJSON(c.Stdout, rules)
					}
					return printHookRules(c.Stdout, rules)
				},
			},
			{
				Name: "add", Summary: "add a callback rule to hooks.json (or the daemon with --memory)",
				Usage:      "[--id ID] [--once] [--harness H] [--pane ID] [--lane ID] [--project ID] (--run CMD | --argv A... | --webhook URL [--header K=V])",
				Parameters: hookAddParams,
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(hookAddParams)
					if err != nil {
						return err
					}
					rule, err := hookRuleFromParams(p)
					if err != nil {
						return err
					}
					if p.Bool("memory") {
						err = daemon.NewClient(daemon.ResolveSocketPath(p.String("socket"))).RegisterHook(c.Context, rule)
						rule.Source = daemon.HookSourceMemory
					} else {
						err = daemon.AddHookRule(daemon.HooksPath(configDir()), rule)
						rule.Source = daemon.HookSourceFile
					}
					if err != nil {
						return err
					}
					return printJSON(c.Stdout, rule)
				},
			},
			{
				Name: "remove", Summary: "remove a callback rule by ID (hooks.json first, then the daemon)",
				Usage: "<id>", Parameters: socketOnly,
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(socketOnly)
					if err != nil {
						return err
					}
					if len(p.Positional()) != 1 {
						return errors.New("usage: mtm hook remove <id>")
					}
					id := p.Positional()[0]
					found, err := daemon.RemoveHookRule(daemon.HooksPath(configDir()), id)
					if err != nil {
						return err
					}
					if !found {
						found, _ = daemon.NewClient(daemon.ResolveSocketPath(p.String("socket"))).UnregisterHook(c.Context, id)
					}
					if !found {
						return fmt.Errorf("hook %q not found", id)
					}
					return printJSON(c.Stdout, map[string]any{"id": id, "removed": true})
				},
			},
			{
				Name: "test", Summary: "deliver a rule now with a synthetic agent.done event",
				Usage: "<id> [--pane ID] [--harness H] [--json]", Parameters: hookTestParams,
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(hookTestParams)
					if err != nil {
						return err
					}
					if len(p.Positional()) != 1 {
						return errors.New("usage: mtm hook test <id> [--pane ID] [--harness H]")
					}
					return runHookTest(c.Context, c.Stdout, p.Positional()[0], p.String("socket"),
						syntheticAgentDone(p.String("pane"), p.String("harness")), p.Format() == "json")
				},
			},
			{
				Name: "log", Summary: "show recent callback deliveries from the running daemon",
				Usage: "[--limit 20] [--json]", Parameters: hookLogParams,
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(hookLogParams)
					if err != nil {
						return err
					}
					log, err := daemon.NewClient(daemon.ResolveSocketPath(p.String("socket"))).HookLog(c.Context, p.Int("limit"))
					if err != nil {
						return err
					}
					if p.Format() == "json" {
						return printJSON(c.Stdout, log)
					}
					return printHookLog(c.Stdout, log)
				},
			},
		},
	}
}

func hookRuleFromParams(p oden.Parsed) (daemon.HookRule, error) {
	rule := daemon.HookRule{
		ID:   strings.TrimSpace(p.String("id")),
		On:   strings.TrimSpace(p.String("on")),
		Once: p.Bool("once"),
		Match: daemon.HookMatch{
			Harness: strings.TrimSpace(p.String("harness")),
			Pane:    strings.TrimSpace(p.String("pane")),
			Lane:    strings.TrimSpace(p.String("lane")),
			Project: strings.TrimSpace(p.String("project")),
		},
		Run: daemon.HookRun{
			Command:    p.Strings("argv"),
			Webhook:    strings.TrimSpace(p.String("webhook")),
			TimeoutSec: p.Int("timeout-sec"),
		},
	}
	if run := strings.TrimSpace(p.String("run")); run != "" {
		if len(rule.Run.Command) > 0 {
			return rule, errors.New("hook add: use --run or --argv, not both")
		}
		rule.Run.Command = []string{"/bin/sh", "-c", run}
	}
	for _, h := range p.Strings("header") {
		k, v, ok := strings.Cut(h, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return rule, fmt.Errorf("hook add: --header %q must be Name=Value", h)
		}
		if rule.Run.Headers == nil {
			rule.Run.Headers = map[string]string{}
		}
		rule.Run.Headers[strings.TrimSpace(k)] = v
	}
	if rule.ID == "" {
		rule.ID = newHookID()
	}
	return rule, rule.Validate()
}

func newHookID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "hook-" + hex.EncodeToString(b[:])
}

// listHookRules returns hooks.json rules plus the daemon's in-memory rules
// (the daemon is optional: when it is not running only file rules are listed).
func listHookRules(ctx context.Context, socket string) ([]daemon.HookRule, error) {
	rules, err := daemon.LoadHookRules(daemon.HooksPath(configDir()))
	if err != nil {
		return nil, err
	}
	if rules == nil {
		rules = []daemon.HookRule{}
	}
	live, err := daemon.NewClient(daemon.ResolveSocketPath(socket)).HookRules(ctx)
	if err != nil {
		return rules, nil
	}
	for _, r := range live {
		if r.Source == daemon.HookSourceMemory {
			rules = append(rules, r)
		}
	}
	return rules, nil
}

func runHookTest(ctx context.Context, w io.Writer, id, socket string, ev daemon.AgentDoneEvent, asJSON bool) error {
	rules, err := listHookRules(ctx, socket)
	if err != nil {
		return err
	}
	for _, r := range rules {
		if r.ID != id {
			continue
		}
		d := daemon.DeliverHook(ctx, nil, r, ev)
		if asJSON {
			if err := printJSON(w, d); err != nil {
				return err
			}
		} else if err := printHookLog(w, []daemon.HookDelivery{d}); err != nil {
			return err
		}
		if !d.OK {
			return fmt.Errorf("hook %s failed: %s", id, d.Error)
		}
		return nil
	}
	return fmt.Errorf("hook %q not found", id)
}

// syntheticAgentDone builds the event `mtm hook test` delivers.
func syntheticAgentDone(pane, harness string) daemon.AgentDoneEvent {
	if pane = strings.TrimSpace(pane); pane == "" {
		pane = os.Getenv(daemon.EnvPaneID)
	}
	if pane == "" {
		pane = "test-pane"
	}
	return daemon.AgentDoneEvent{
		Event:     daemon.EventAgentDone,
		PaneID:    pane,
		Harness:   strings.TrimSpace(harness),
		PrevState: daemon.StateRunning,
		State:     daemon.StateDone,
		Summary:   "synthetic agent.done from mtm hook test",
		At:        time.Now().UTC(),
	}
}

func hookMatchText(m daemon.HookMatch) string {
	var parts []string
	for _, kv := range [][2]string{{"harness", m.Harness}, {"pane", m.Pane}, {"lane", m.Lane}, {"project", m.Project}} {
		if kv[1] != "" {
			parts = append(parts, kv[0]+"="+kv[1])
		}
	}
	if len(parts) == 0 {
		return "*"
	}
	return strings.Join(parts, ",")
}

func hookTargetText(r daemon.HookRun) string {
	var parts []string
	if len(r.Command) > 0 {
		parts = append(parts, strings.Join(r.Command, " "))
	}
	if r.Webhook != "" {
		parts = append(parts, "POST "+r.Webhook)
	}
	return strings.Join(parts, " + ")
}

func printHookRules(w io.Writer, rules []daemon.HookRule) error {
	if len(rules) == 0 {
		_, err := fmt.Fprintf(w, "no hooks (add one with `mtm hook add`; file: %s)\n", daemon.HooksPath(configDir()))
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSOURCE\tON\tONCE\tMATCH\tRUN")
	for _, r := range rules {
		on := r.On
		if on == "" {
			on = "*"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%t\t%s\t%s\n", r.ID, r.Source, on, r.Once, hookMatchText(r.Match), hookTargetText(r.Run))
	}
	return tw.Flush()
}

func printHookLog(w io.Writer, log []daemon.HookDelivery) error {
	if len(log) == 0 {
		_, err := fmt.Fprintln(w, "no callback deliveries yet")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "AT\tRULE\tPANE\tRESULT\tTRIES\tMS\tTARGET")
	for _, d := range log {
		result := "ok"
		if !d.OK {
			result = "FAIL " + d.Error
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%s\n", d.At.Local().Format("15:04:05"), d.RuleID, d.PaneID, result, d.Attempts, d.DurationMs, d.Target)
	}
	return tw.Flush()
}
