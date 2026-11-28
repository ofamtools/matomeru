package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"ofam.dev/matomeru/setup"
	"ofam.dev/oden"
)

const setupUsage = "usage: mtm setup [machine|agents|skills|hooks|mcp|check|sync|purge|init-shell] [--home DIR] [--source FILE] [--skills-src DIR ...] [--all-skills] [--hook-cmd CMD] [--olane-hook-cmd CMD] [--no-olane-hook] [--mcp-cmd CMD] [--transport stdio|http] [--mcp-url URL] [--dry-run] [--check] [--force] [--quiet] [--json]"

func setupStateDir(homeOverride string) string {
	if h := strings.TrimSpace(homeOverride); h != "" {
		return filepath.Join(filepath.Clean(h), ".local", "state", "matomeru")
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); xdg != "" {
		return filepath.Join(xdg, "matomeru")
	}
	if h, err := os.UserHomeDir(); err == nil && strings.TrimSpace(h) != "" {
		return filepath.Join(h, ".local", "state", "matomeru")
	}
	return ""
}

func autoSyncSetupIfChanged() string {
	if os.Getenv("MTM_NO_AUTO_SETUP") != "" || os.Getenv("MTM_SMOKE") != "" {
		return ""
	}
	if _, _, err := setup.SyncIfChanged(setup.Options{}, setupStateDir(""), false); err != nil {
		return "setup auto-sync: " + err.Error()
	}
	return ""
}

func setupParameters() []oden.Parameter {
	return []oden.Parameter{
		{Name: "home", Type: "string", Description: "Override target home directory (default: current user home)"},
		{Name: "source", Type: "string", Description: "Manage agent instruction targets as symlinks to FILE"},
		{Name: "skills-src", Type: "string", Repeatable: true, Description: "Additional source directory of skill folders to sync (repeatable)"},
		{Name: "all-skills", Type: "bool", Description: "Sync all valid skills in ~/.agents/skills instead of OFAM/Matomeru skills only"},
		{Name: "hook-cmd", Type: "string", Description: "Command invoked by Claude Code hooks (default: mtm-hook claude-code)"},
		{Name: "olane-hook-cmd", Type: "string", Description: "Command invoked by o-lane Claude Code subagent hooks (default: o-lane agent-hook claude-code)"},
		{Name: "no-olane-hook", Type: "bool", Description: "Skip wiring o-lane agent-hook on Claude Code SubagentStart/SubagentStop"},
		{Name: "mcp-cmd", Type: "string", Description: "Command invoked by harness MCP server configs (default: mtm)"},
		{Name: "transport", Type: "string", Default: setup.MCPTransportStdio, Description: "Harness MCP transport: stdio (spawn mtm mcp serve) or http (shared daemon endpoint)"},
		{Name: "mcp-url", Type: "string", Description: "HTTP MCP endpoint for --transport http (default: live daemon's <url>/mcp, else " + setup.DefaultMCPURL + ")"},
		{Name: "dry-run", Type: "bool", Description: "Preview planned changes without writing files"},
		{Name: "check", Type: "bool", Description: "Read-only drift check; exit non-zero when not converged"},
		{Name: "force", Type: "bool", Description: "Replace conflicting symlinks or back up and replace regular files/directories"},
	}
}

func setupOptionsFromParsed(p oden.Parsed) setup.Options {
	return setup.Options{
		Home:          p.String("home"),
		Source:        p.String("source"),
		SkillsSrc:     p.Strings("skills-src"),
		AllSkills:     p.Bool("all-skills"),
		HookCmd:       p.String("hook-cmd"),
		OLaneHookCmd:  p.String("olane-hook-cmd"),
		SkipOLaneHook: p.Bool("no-olane-hook"),
		MCPCmd:        p.String("mcp-cmd"),
		MCPTransport:  p.String("transport"),
		MCPURL:        p.String("mcp-url"),
		DryRun:        p.Bool("dry-run"),
		Check:         p.Bool("check"),
		Force:         p.Bool("force"),
	}
}

func mtmSetupCommand() oden.Command {
	params := setupParameters()

	runTarget := func(target string, forceCheck bool, exitOnDrift bool) func(*oden.Ctx) error {
		return func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			if len(p.Positional()) != 0 {
				return errors.New(setupUsage)
			}
			opts := setupOptionsFromParsed(p)
			opts.MCPURL = resolveSetupMCPURL(c.Context, opts)
			if forceCheck {
				opts.Check = true
				opts.DryRun = false
			}
			var rep setup.Report
			switch target {
			case "machine":
				rep, err = setup.RunMachine(opts)
			case "agents":
				rep, err = setup.RunAgents(opts)
			case "skills":
				rep, err = setup.RunSkills(opts)
			case "hooks":
				rep, err = setup.RunHooks(opts)
			case "mcp":
				rep, err = setup.RunMCP(opts)
			case "check":
				rep, err = setup.CheckMachine(opts)
			default:
				return errors.New(setupUsage)
			}
			if err != nil {
				return err
			}
			if err := writeSetupOutput(c.Stdout, p.Format(), rep); err != nil {
				return err
			}
			if !rep.OK && (exitOnDrift || opts.Check || !opts.DryRun) {
				return &oden.ExitError{Code: 1}
			}
			return nil
		}
	}

	return oden.Command{
		Name:       "setup",
		Summary:    "configure agent instructions, OFAM skills, harness hooks, and MCP",
		Category:   "Configuration",
		Usage:      "[machine|agents|skills|hooks|mcp|check|sync|purge|init-shell] [--home DIR] [--dry-run] [--check] [--force] [--quiet] [--json]",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			syncParams := append(append([]oden.Parameter(nil), params...),
				oden.Parameter{Name: "quiet", Type: "bool", Description: "Suppress output when up to date or synced cleanly"},
			)
			sub := c.Sub()
			sub.Add(
				oden.Command{
					Name:       "machine",
					Summary:    "configure agent instruction files, OFAM skills, harness hooks, and MCP",
					Usage:      "[--home DIR] [--source FILE] [--skills-src DIR ...] [--all-skills] [--hook-cmd CMD] [--olane-hook-cmd CMD] [--no-olane-hook] [--mcp-cmd CMD] [--transport stdio|http] [--mcp-url URL] [--dry-run] [--check] [--force] [--json]",
					Parameters: params,
					Handle:     runTarget("machine", false, false),
				},
				oden.Command{
					Name:       "agents",
					Summary:    "configure global AGENTS.md / GEMINI.md / CLAUDE.md instruction files",
					Usage:      "[--home DIR] [--source FILE] [--dry-run] [--check] [--force] [--json]",
					Parameters: params,
					Handle:     runTarget("agents", false, false),
				},
				oden.Command{
					Name:       "skills",
					Summary:    "seed embedded skills and sync OFAM skill directories across harnesses",
					Usage:      "[--home DIR] [--skills-src DIR ...] [--all-skills] [--dry-run] [--check] [--force] [--json]",
					Parameters: params,
					Handle:     runTarget("skills", false, false),
				},
				oden.Command{
					Name:       "hooks",
					Summary:    "configure mtm-hook across Claude Code, Codex, Antigravity, Gemini CLI, and OMP",
					Usage:      "[--home DIR] [--hook-cmd CMD] [--olane-hook-cmd CMD] [--no-olane-hook] [--dry-run] [--check] [--force] [--json]",
					Parameters: params,
					Handle:     runTarget("hooks", false, false),
				},
				oden.Command{
					Name:       "mcp",
					Summary:    "configure mtm mcp serve across Claude Code, Codex, Antigravity, and Gemini CLI",
					Usage:      "[--home DIR] [--mcp-cmd CMD] [--transport stdio|http] [--mcp-url URL] [--dry-run] [--check] [--force] [--json]",
					Parameters: params,
					Handle:     runTarget("mcp", false, false),
				},
				oden.Command{
					Name:       "check",
					Summary:    "read-only drift check across agents, skills, hooks, and MCP (exits non-zero on drift)",
					Usage:      "[--home DIR] [--source FILE] [--skills-src DIR ...] [--all-skills] [--hook-cmd CMD] [--olane-hook-cmd CMD] [--no-olane-hook] [--mcp-cmd CMD] [--json]",
					Parameters: params,
					Handle:     runTarget("check", true, true),
				},
				oden.Command{
					Name:       "sync",
					Summary:    "hash-gated machine setup sync (no-op when embedded assets are unchanged)",
					Usage:      "[--quiet] [--force] [--home DIR] [--dry-run] [--json]",
					Parameters: syncParams,
					Handle: func(c *oden.Ctx) error {
						p, err := c.Parse(syncParams)
						if err != nil {
							return err
						}
						if len(p.Positional()) != 0 {
							return errors.New("usage: mtm setup sync [--quiet] [--force] [--home DIR] [--dry-run] [--json]")
						}
						opts := setupOptionsFromParsed(p)
						quiet := p.Bool("quiet")
						rep, ran, err := setup.SyncIfChanged(opts, setupStateDir(opts.Home), opts.Force)
						if err != nil {
							return err
						}
						if p.Format() == "json" {
							if err := printJSON(c.Stdout, map[string]any{
								"ok":     rep.OK,
								"synced": ran,
								"hash":   setup.AssetsHash(),
								"report": rep,
							}); err != nil {
								return err
							}
						} else if !ran {
							if !quiet {
								if _, err := fmt.Fprintf(c.Stdout, "setup sync: up to date (%s)\n", setup.AssetsHash()[:12]); err != nil {
									return err
								}
							}
						} else if !quiet || !rep.OK {
							if err := writeSetupOutput(c.Stdout, p.Format(), rep); err != nil {
								return err
							}
						}
						if !rep.OK && (opts.Check || !opts.DryRun) {
							return &oden.ExitError{Code: 1}
						}
						return nil
					},
				},
				oden.Command{
					Name:       "purge",
					Summary:    "remove only the managed OFAM policy block from global agent instruction files",
					Usage:      "[--home DIR] [--dry-run] [--json]",
					Parameters: params,
					Handle: func(c *oden.Ctx) error {
						p, err := c.Parse(params)
						if err != nil {
							return err
						}
						if len(p.Positional()) != 0 {
							return errors.New("usage: mtm setup purge [--home DIR] [--dry-run] [--json]")
						}
						opts := setupOptionsFromParsed(p)
						rep, err := setup.RunPurgeAgents(opts)
						if err != nil {
							return err
						}
						if !opts.DryRun && rep.OK {
							if stateDir := setupStateDir(opts.Home); stateDir != "" {
								_ = os.Remove(filepath.Join(stateDir, setup.SyncStampFilename))
							}
						}
						if err := writeSetupOutput(c.Stdout, p.Format(), rep); err != nil {
							return err
						}
						if !rep.OK && !opts.DryRun {
							return &oden.ExitError{Code: 1}
						}
						return nil
					},
				},
				oden.Command{
					Name:    "init-shell",
					Summary: "print a lightweight, non-blocking shell startup hook for setup sync",
					Usage:   "",
					Handle: func(c *oden.Ctx) error {
						p, err := c.Parse(nil)
						if err != nil {
							return err
						}
						if len(p.Positional()) != 0 {
							return errors.New("usage: mtm setup init-shell")
						}
						_, err = io.WriteString(c.Stdout, "# Matomeru already auto-syncs on TUI startup and g U.\n# Optional background shell hook:\nif command -v mtm >/dev/null 2>&1; then (mtm setup sync --quiet >/dev/null 2>&1 &); fi\n")
						return err
					},
				},
			)
			if len(c.Args) == 1 && (c.Args[0] == "-h" || c.Args[0] == "--help") {
				sub.Help(c.Stdout)
				return nil
			}
			// Bare `mtm setup [flags]` runs a safe read-only status summary.
			sub.Default = func(_ context.Context, subArgs []string) error {
				p, err := oden.ParseFlags(subArgs, params)
				if err != nil {
					return err
				}
				if pos := p.Positional(); len(pos) != 0 {
					return fmt.Errorf("setup %s: unknown subcommand\n%s", pos[0], setupUsage)
				}
				opts := setupOptionsFromParsed(p)
				explicitCheck := opts.Check
				rep, err := setup.CheckMachine(opts)
				if err != nil {
					return err
				}
				if err := writeSetupOutput(c.Stdout, p.Format(), rep); err != nil {
					return err
				}
				if explicitCheck && !rep.OK {
					return &oden.ExitError{Code: 1}
				}
				return nil
			}
			return sub.Run(c.Context)
		},
	}
}

func writeSetupOutput(w io.Writer, format string, rep setup.Report) error {
	if format == "json" {
		return printJSON(w, rep)
	}
	tw := tabwriter.NewWriter(w, 2, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "COMPONENT\tTARGET\tSTATUS\tPATH\tDETAIL")
	for _, a := range rep.Actions {
		detail := a.Detail
		if a.Backup != "" {
			if detail != "" {
				detail += " "
			}
			detail += fmt.Sprintf("(backup: %s)", a.Backup)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", a.Component, a.Target, a.Status, a.Path, detail)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	mode := "apply"
	switch {
	case rep.Check:
		mode = "check"
	case rep.DryRun:
		mode = "dry-run"
	}
	status := "ok"
	if !rep.OK {
		status = "drift/conflict"
		if rep.Check {
			status = "drift detected (run: mtm setup machine)"
		}
	}
	_, err := fmt.Fprintf(w, "\nsetup (%s): %s (%d actions)\n", mode, status, len(rep.Actions))
	return err
}
