package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"ofam.dev/matomeru/daemon"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/ochazuke/core"
	"ofam.dev/oden"
)

// newCLIToolbox constructs a Toolbox wired with the live daemon PaneRuntime
// and harness launch resolver so both `mtm mcp call`/`serve` and `mtm pane
// spawn|snapshot|send|status|close` share one implementation.
func newCLIToolbox(g globalOpts, store *data.Store) *mcpui.Toolbox {
	box := mcpui.NewToolbox(store, nil)
	if box.Panes != nil {
		sock := daemon.DefaultSocketPath()
		box.Panes.Runtime = daemon.NewClientPaneRuntime(sock, "")
		box.Panes.ResolveLaunch = func(harnessID, mcpURL, paneID, customCmd string) (string, map[string]string, error) {
			return daemon.ResolveHarnessLaunch(configDir(), harnessID, mcpURL, paneID, customCmd)
		}
		box.Panes.ResolveLaunchWithPrompt = func(harnessID, mcpURL, paneID, customCmd, prompt string) (mcpui.PaneLaunchSpec, error) {
			cmd, env, delivered, err := daemon.ResolveHarnessLaunchWithPrompt(configDir(), harnessID, mcpURL, paneID, customCmd, prompt)
			return mcpui.PaneLaunchSpec{Cmd: cmd, Env: env, PromptDeliveredInCmd: delivered}, err
		}
		box.Panes.MCPURL = func(paneID string) string {
			if base := strings.TrimSpace(os.Getenv(daemon.EnvMCPURL)); base != "" {
				if idx := strings.Index(base, "/mcp/"); idx > 0 {
					return base[:idx] + "/mcp/" + url.PathEscape(paneID)
				}
			}
			c := daemon.NewClient(sock)
			if st, err := c.Status(context.Background()); err == nil && st.MCPURL != "" {
				return st.MCPURL + "/mcp/" + url.PathEscape(paneID)
			}
			return ""
		}
	}
	return box
}

func mtmPaneOpsCommands(g globalOpts) []oden.Command {
	spawnParams := []oden.Parameter{
		{Name: "id", Type: "string", Description: "Optional explicit pane ID"},
		{Name: "title", Type: "string", Description: "Pane title (defaults to harness or lane title)"},
		{Name: "harness", Type: "string", Default: "zsh", Description: "Agent harness or shell (claude, codex, omp, gemini, zsh, ...)"},
		{Name: "cwd", Type: "string", Description: "Working directory (inherits lane worktree or project defaultDir when omitted)"},
		{Name: "project", Type: "string", Description: "Project task ID to associate with the pane"},
		{Name: "task", Type: "string", Description: "Task ID to associate with the pane"},
		{Name: "lane", Type: "string", Description: "Lane ID to claim this pane (creates lane --claim--> pane relation)"},
		{Name: "dir", Type: "string", Description: "Directory path to relate via directory relation"},
		{Name: "cmd", Type: "string", Description: "Explicit shell command overriding harness command"},
		{Name: "prompt", Type: "string", Description: "Optional initial prompt sent to the live pane after spawn"},
		{Name: "cols", Type: "int", Default: "120", Description: "Initial terminal columns"},
		{Name: "rows", Type: "int", Default: "36", Description: "Initial terminal rows"},
		{Name: "no-launch", Type: "bool", Description: "Create record and relations without spawning a live PTY"},
		{Name: "on-done-run", Type: "string", Description: "One-shot shell command run when the pane first emits agent.done (event JSON on stdin)"},
		{Name: "on-done-argv", Type: "string", Repeatable: true, Description: "One-shot exact argv element (repeat in order) run on agent.done"},
		{Name: "on-done-webhook", Type: "string", Description: "One-shot URL that receives the agent.done event JSON as a POST body"},
		{Name: "on-done-notify", Type: "string", Description: "Pane ID (e.g. your $MTM_PANE_ID) mailed on agent.done/needs_input/blocked and woken only once idle"},
	}
	snapParams := []oden.Parameter{
		{Name: "lines", Type: "int", Default: "0", Description: "Optional tail line limit (0 = all rows)"},
	}
	sendParams := []oden.Parameter{
		{Name: "no-enter", Type: "bool", Description: "Send raw text without trailing carriage return"},
	}

	return []oden.Command{
		{
			Name:       "spawn",
			Summary:    "spawn a live pane/agent in any project and claim it on a lane",
			Usage:      "[--harness claude|codex|omp|gemini|zsh] [--project ID] [--task ID] [--lane ID] [--cwd DIR] [--title T] [--prompt TEXT]",
			Parameters: spawnParams,
			Handle: func(c *oden.Ctx) error {
				p, err := c.Parse(spawnParams)
				if err != nil {
					return err
				}
				store, err := openStoreWithSeed(g)
				if err != nil {
					return err
				}
				defer store.Close()
				box := newCLIToolbox(g, store)
				res, err := box.Panes.Spawn(c.Context, cliActor, mcpui.PaneSpawnArgs{
					ID:        p.String("id"),
					Title:     p.String("title"),
					Harness:   p.String("harness"),
					Cwd:       p.String("cwd"),
					Project:   p.String("project"),
					Task:      p.String("task"),
					Lane:      p.String("lane"),
					Directory: p.String("dir"),
					Cmd:       p.String("cmd"),
					Prompt:    p.String("prompt"),
					Cols:      p.Int("cols"),
					Rows:      p.Int("rows"),
					NoLaunch:  p.Bool("no-launch"),
					OnDone:    paneOnDoneFromParams(p),
				})
				if err != nil {
					return err
				}
				return printJSON(c.Stdout, res)
			},
		},
		{
			Name:       "snapshot",
			Summary:    "capture the current plain-text screen of a live pane",
			Usage:      "<pane-id> [--lines N]",
			Parameters: snapParams,
			Handle: func(c *oden.Ctx) error {
				p, err := c.Parse(snapParams)
				if err != nil {
					return err
				}
				pos := p.Positional()
				if len(pos) != 1 {
					return errors.New("usage: mtm pane snapshot <pane-id> [--lines N]")
				}
				store, err := openStoreWithSeed(g)
				if err != nil {
					return err
				}
				defer store.Close()
				id, err := resolveRecordID(c.Context, store, data.KindPane, pos[0], c.Stdin, "usage: mtm pane snapshot <pane-id>")
				if err != nil {
					return err
				}
				res, err := newCLIToolbox(g, store).Panes.Snapshot(c.Context, mcpui.PaneSnapshotArgs{
					Pane:  id,
					Lines: p.Int("lines"),
				})
				if err != nil {
					return err
				}
				return printJSON(c.Stdout, res)
			},
		},
		{
			Name:       "send",
			Summary:    "send text or keystrokes to a live pane",
			Usage:      "<pane-id> <text> [--no-enter]",
			Parameters: sendParams,
			Handle: func(c *oden.Ctx) error {
				p, err := c.Parse(sendParams)
				if err != nil {
					return err
				}
				pos := p.Positional()
				if len(pos) < 2 {
					return errors.New("usage: mtm pane send <pane-id> <text> [--no-enter]")
				}
				store, err := openStoreWithSeed(g)
				if err != nil {
					return err
				}
				defer store.Close()
				id, err := resolveRecordID(c.Context, store, data.KindPane, pos[0], c.Stdin, "usage: mtm pane send <pane-id> <text>")
				if err != nil {
					return err
				}
				enter := !p.Bool("no-enter")
				res, err := newCLIToolbox(g, store).Panes.Send(c.Context, mcpui.PaneSendArgs{
					Pane:  id,
					Text:  strings.Join(pos[1:], " "),
					Enter: &enter,
				})
				if err != nil {
					return err
				}
				return printJSON(c.Stdout, res)
			},
		},
		{
			Name:    "status",
			Summary: "inspect live PTY status, hook state, linked task, and claiming lanes for a pane",
			Usage:   "<pane-id>",
			Handle: func(c *oden.Ctx) error {
				p, err := c.Parse(nil)
				if err != nil {
					return err
				}
				pos := p.Positional()
				if len(pos) != 1 {
					return errors.New("usage: mtm pane status <pane-id>")
				}
				store, err := openStoreWithSeed(g)
				if err != nil {
					return err
				}
				defer store.Close()
				id, err := resolveRecordID(c.Context, store, data.KindPane, pos[0], c.Stdin, "usage: mtm pane status <pane-id>")
				if err != nil {
					return err
				}
				res, err := newCLIToolbox(g, store).Panes.Status(c.Context, mcpui.PaneStatusArgs{Pane: id})
				if err != nil {
					return err
				}
				return printJSON(c.Stdout, res)
			},
		},
		{
			Name:    "close",
			Summary: "close a live PTY pane and mark its record exited",
			Usage:   "<pane-id>",
			Handle: func(c *oden.Ctx) error {
				p, err := c.Parse(nil)
				if err != nil {
					return err
				}
				pos := p.Positional()
				if len(pos) != 1 {
					return errors.New("usage: mtm pane close <pane-id>")
				}
				store, err := openStoreWithSeed(g)
				if err != nil {
					return err
				}
				defer store.Close()
				id, err := resolveRecordID(c.Context, store, data.KindPane, pos[0], c.Stdin, "usage: mtm pane close <pane-id>")
				if err != nil {
					return err
				}
				res, err := newCLIToolbox(g, store).Panes.Close(c.Context, cliActor, mcpui.PaneCloseArgs{Pane: id})
				if err != nil {
					return err
				}
				return printJSON(c.Stdout, res)
			},
		},
	}
}

// paneOnDoneFromParams maps --on-done-* flags to a pane.spawn onDone callback (nil when unset).
func paneOnDoneFromParams(p oden.Parsed) *mcpui.PaneOnDone {
	od := &mcpui.PaneOnDone{
		Command:    p.Strings("on-done-argv"),
		Webhook:    strings.TrimSpace(p.String("on-done-webhook")),
		NotifyPane: strings.TrimSpace(p.String("on-done-notify")),
	}
	if run := strings.TrimSpace(p.String("on-done-run")); run != "" && len(od.Command) == 0 {
		od.Command = []string{"/bin/sh", "-c", run}
	}
	if od.Empty() {
		return nil
	}
	return od
}

func mtmRelatedVerb(noun, kind string, g globalOpts) oden.Command {
	params := []oden.Parameter{
		{Name: "rel", Type: "string", Description: "Optional relation kind filter (e.g. claim, plan, about, directory)"},
		{Name: "direction", Type: "string", Default: "both", Enum: []string{"out", "in", "both"}, Description: "Relation direction"},
	}
	return oden.Command{
		Name:       "related",
		Summary:    fmt.Sprintf("list relations and linked records for a %s", noun),
		Usage:      "<id> [--rel KIND] [--direction out|in|both]",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			pos := p.Positional()
			use := fmt.Sprintf("usage: mtm %s related <id> [--rel KIND] [--direction out|in|both]", noun)
			if len(pos) != 1 {
				return errors.New(use)
			}
			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			defer store.Close()
			id, err := resolveRecordID(c.Context, store, kind, pos[0], c.Stdin, use)
			if err != nil {
				return err
			}
			res, err := mcpui.NewDataTools(store).Related(mcpui.RelatedArgs{
				Kind:      kind,
				ID:        id,
				Relation:  p.String("rel"),
				Direction: p.String("direction"),
			})
			if err != nil {
				return err
			}
			return printJSON(c.Stdout, res)
		},
	}
}

func mtmSetVerb(noun, kind string, g globalOpts) oden.Command {
	params := []oden.Parameter{
		{Name: "field", Type: "string", Repeatable: true, Description: "Field assignment (name=value)"},
		paramFormatJSON,
	}
	return oden.Command{
		Name:       "set",
		Summary:    fmt.Sprintf("set one or more fields (including refs) on a %s", noun),
		Usage:      "<id> <field=value>... [--field k=v ...]",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			pos := p.Positional()
			use := fmt.Sprintf("usage: mtm %s set <id> <field=value>... [--field k=v ...]", noun)
			if len(pos) < 1 {
				return errors.New(use)
			}
			pairs := append([]string{}, p.Strings("field")...)
			pairs = append(pairs, pos[1:]...)
			if len(pairs) == 0 {
				return errors.New(use)
			}
			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			defer store.Close()
			id, err := resolveRecordID(c.Context, store, kind, pos[0], c.Stdin, use)
			if err != nil {
				return err
			}
			schema := schemaOf(store, kind)
			fields, err := parseFieldAssignments(schema, pairs)
			if err != nil {
				return err
			}
			rec, _, err := store.Patch(data.PatchInput{Ref: core.Ref{Kind: kind, ID: id}, Fields: fields}, cliActor)
			if err != nil {
				return err
			}
			return printRecordOne(c.Stdout, store, kind, recordFormat(p), rec)
		},
	}
}

// dispatchDynamicNoun resolves any registered extension/dynamic schema kind
// (or relation-only kind like tag/workspace) on the fly as a first-class CLI
// noun subcommand without slowing startup or causing oden prefix collisions.
func dispatchDynamicNoun(ctx context.Context, a *oden.App, g globalOpts, rootArgs []string) (bool, error) {
	if len(rootArgs) == 0 || strings.HasPrefix(rootArgs[0], "-") {
		return false, nil
	}
	if strings.EqualFold(rootArgs[0], data.KindTriageRule) {
		if e := a.Catalog().Lookup("rule"); e != nil {
			return true, a.RunEntry(ctx, e, rootArgs[1:])
		}
	}
	if len(rootArgs) > 1 && strings.EqualFold(rootArgs[0], "commands") {
		if e := a.Catalog().Lookup("command"); e != nil {
			return true, a.RunEntry(ctx, e, rootArgs[1:])
		}
	}
	raw := strings.TrimSpace(rootArgs[0])
	kind := raw
	if resolved, ok := resolveNoun(raw); ok {
		kind = resolved
	}
	store, err := openStoreWithSeed(g)
	if err != nil {
		return false, nil
	}
	_, ok := store.Registry().Get(kind)
	store.Close()
	if !ok && kind != data.KindTag && kind != data.KindWorkspace {
		return false, nil
	}
	a.Add(buildNounCommand(nounSpec{name: raw, kind: kind, summary: "manage " + kind + " records"}, g))
	if e := a.Catalog().Lookup(raw); e != nil {
		return true, a.RunEntry(ctx, e, rootArgs[1:])
	}
	return false, nil
}
