package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/unixsuperhero/matomeru/internal/mcp"
	"github.com/unixsuperhero/ochazuke"
	catalogmcp "github.com/unixsuperhero/ochazuke/cli/mcp"
	"github.com/unixsuperhero/oden"
)

// catalogMCP binds the pane at startup and keeps one headless runtime so history
// and exact undo survive tool calls. Assignments are re-read for domain writes.
func (a *App) catalogMCP(stdin io.Reader, stdout, stderr io.Writer) (*catalogmcp.Server, error) {
	id, err := a.identifyPane(mcp.IdentityFromEnv(a.getenv))
	if err != nil {
		return nil, err
	}
	if id.Harness == "" {
		id.Harness = a.harnesses.AgentFromEnv(a.getenv)
	}
	if a.mcpPrepare != nil {
		a.mcpPrepare(a)
	}
	a.setActor(mcpActor(id))
	a.actorSet = true
	actor := ochazuke.Actor{Kind: ochazuke.ActorAgent, Harness: id.Harness, Pane: id.Window, Session: id.Session, Model: id.Model}
	if actor.Pane == "" {
		actor.Name = "mtm-mcp"
	}
	sh := a.shell(stdin, stdout, stderr)
	sh.Actor = &actor
	sh.Version = mtmVersion()
	return sh.NewMCPServer()
}

// defineMCPCommand mounts mtm mcp on the oden app.
func (a *App) defineMCPCommand(app *oden.App, stdin io.Reader, stdout, stderr io.Writer) {
	server := func() (*catalogmcp.Server, error) { return a.catalogMCP(stdin, stdout, stderr) }
	printJSON := func(v any) error {
		raw, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "%s\n", raw)
		return err
	}
	noArgs := func(name string, args []string) error {
		if len(args) > 0 {
			return fmt.Errorf("usage: mtm mcp %s (unexpected %q)", name, strings.Join(args, " "))
		}
		return nil
	}
	app.Add(oden.Command{Name: "mcp", Summary: "Family catalog MCP gateway: serve, tools, instructions, whoami, config", Source: "inline", Children: []oden.Command{
		{Name: "serve", Summary: "Speak MCP (JSON-RPC 2.0) on stdin and stdout as the pane this runs in", Run: func(ctx context.Context, args []string) error {
			if err := noArgs("serve", args); err != nil {
				return err
			}
			s, err := server()
			if err != nil {
				return err
			}
			a.r.EnsureHistory()
			defer a.r.Config().Flush()
			return s.Serve(ctx, stdin, stdout)
		}},
		{Name: "tools", Summary: "List the catalog tools and compatibility aliases", Run: func(_ context.Context, args []string) error {
			if err := noArgs("tools", args); err != nil {
				return err
			}
			s, err := server()
			if err != nil {
				return err
			}
			return printJSON(s.Tools())
		}},
		{Name: "whoami", Summary: "Print what the whoami tool answers for the pane this runs in", Run: func(ctx context.Context, args []string) error {
			if err := noArgs("whoami", args); err != nil {
				return err
			}
			s, err := server()
			if err != nil {
				return err
			}
			result, err := s.Call(ctx, "whoami", nil)
			if err != nil {
				return err
			}
			if result, ok := result.(map[string]any); ok {
				if result["status"] != "done" {
					return fmt.Errorf("%v", result["reason"])
				}
				return printJSON(result["result"])
			}
			return printJSON(result)
		}},
		{Name: "instructions", Summary: "Print the generated catalog guidance", Run: func(_ context.Context, args []string) error {
			if err := noArgs("instructions", args); err != nil {
				return err
			}
			s, err := server()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(stdout, s.Instructions())
			return err
		}},
		{Name: "config", Usage: "[HARNESS]", Summary: "Print the MCP server entry for a harness (" + strings.Join(mcp.Harnesses(), ", ") + "); writes nothing", Run: func(_ context.Context, args []string) error {
			if len(args) > 1 {
				return fmt.Errorf("usage: mtm mcp config [HARNESS]")
			}
			cmd := a.mcpServerCommand(stderr)
			if len(args) == 0 {
				out := mcp.Stdio(cmd)
				out["harnesses"] = mcp.Harnesses()
				return printJSON(out)
			}
			home, _ := a.home()
			config, err := mcp.Config(cmd, args[0], home, a.getenv)
			if err != nil {
				return err
			}
			return printJSON(config)
		}},
	}})
}

// mcpServerCommand is how a harness starts this server: this binary with
// the database and daemon of this invocation, absolute.
func (a *App) mcpServerCommand(stderr io.Writer) mcp.Command {
	exe, err := os.Executable()
	if err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
	}
	if strings.Contains(exe, string(filepath.Separator)+"go-build") {
		fmt.Fprintln(stderr, "warning: this mtm is a go run build in a temporary directory; install mtm and run mtm mcp config from the installed binary")
	}
	abs := func(p string) string {
		if out, err := filepath.Abs(p); err == nil {
			return out
		}
		return p
	}
	args := []string{"--data", abs(a.opts.Data)}
	switch {
	case a.opts.Socket != "":
		args = append(args, "--socket", abs(a.opts.Socket))
	case a.opts.Server != "":
		args = append(args, "--server", a.opts.Server)
	default:
		args = append(args, "--server", DefaultDaemon)
	}
	if a.opts.Config != "" {
		args = append(args, "--config", abs(a.opts.Config))
	}
	return mcp.Command{Executable: exe, Args: append(args, "mcp", "serve")}
}

// mtmVersion is the main module's version, for initialize's serverInfo.
func mtmVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "devel"
}

// Identify is whoami's lookup, the journal's rule (journalOwner): the
// pane's assignment, else its session's owner by name; then the recipe
// the pane was launched with for its cwd, harness and model. A pane mtm
// did not launch has no recipe: its cwd is the server's, which the
// harness started in its own directory.
func (a *App) identifyPane(id mcp.Identity) (mcp.Identity, error) {
	task, project, err := journalOwner(a.work)(id.Session, id.Window)
	if err != nil {
		return id, err
	}
	id.TaskID, id.ProjectID = task, project
	if r, err := a.work.PaneRecipe(id.Window); err == nil {
		id.Cwd, id.Harness, id.Model = r.Cwd, r.Harness, r.Model
	}
	if id.Cwd == "" {
		id.Cwd, _ = os.Getwd()
	}
	if task != "" {
		if t, err := a.work.Task(task); err == nil {
			id.TaskTitle, id.TaskStatus, id.TaskVersion = t.Title, t.Status, t.Updated
		}
	}
	if project != "" {
		if p, err := a.work.Project(project); err == nil {
			id.ProjectName, id.ProjectRepo = p.Name, p.Repo
		}
	}
	return id, nil
}
