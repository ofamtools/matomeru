package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/unixsuperhero/oden"

	"github.com/unixsuperhero/matomeru/internal/mcp"
	"github.com/unixsuperhero/matomeru/internal/work"
)

// mtm mcp is the MCP shell (internal/mcp), ported from matomeru-old:
//
//   - mcp serve speaks MCP over stdio as the pane it runs in. Each tool
//     call runs mtm operations the way mtm NOUN VERB --json '{...}' does:
//     a fresh App from the same command line (--data, --server, --socket,
//     --config), its CLI shell (cli.go) in process, the result read from
//     the shell's JSON output. So a tool does what the command does, and
//     like the command it never sees a running TUI's memory, only the
//     database and the daemon.
//   - mcp tools lists the tools; mcp whoami prints what whoami answers.
//   - mcp config [HARNESS] prints the entry a harness needs to start the
//     server: this binary (os.Executable) with this invocation's --data
//     and --server or --socket. It writes nothing.

// defineMCPCommand mounts mtm mcp on the oden app.
func (a *App) defineMCPCommand(app *oden.App, stdin io.Reader, stdout, stderr io.Writer) {
	backend := &mcpBackend{opts: a.opts, work: a.work, prepare: a.mcpPrepare}
	server := func() (*mcp.Server, error) { return mcp.New(backend, mtmVersion()) }
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
	app.Add(oden.Command{Name: "mcp", Summary: "MCP server for agents in TUIOS panes: serve, tools, whoami, config", Source: "inline", Children: []oden.Command{
		{Name: "serve", Summary: "Speak MCP (JSON-RPC 2.0) on stdin and stdout as the pane this runs in", Run: func(ctx context.Context, args []string) error {
			if err := noArgs("serve", args); err != nil {
				return err
			}
			s, err := server()
			if err != nil {
				return err
			}
			return s.Serve(ctx, mcp.IdentityFromEnv(a.getenv), stdin, stdout)
		}},
		{Name: "tools", Summary: "List the tools serve offers, with the operations each runs", Run: func(_ context.Context, args []string) error {
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
			result, err := s.Call(ctx, mcp.IdentityFromEnv(a.getenv), "whoami", nil)
			if err != nil {
				return err
			}
			return printJSON(result)
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

// mcpBackend is mtm for the MCP tools.
type mcpBackend struct {
	opts Options
	work *work.Store // the serve process's own store, read by Identify
	// prepare, when set, adjusts each per-call app before it runs (the
	// serve app's test doubles: the daemon, the environment).
	prepare func(*App)
}

// Run is one mtm NOUN VERB --json ARGS in this process: a fresh App, so no
// state carries from one call to the next, as between two CLI commands.
func (b *mcpBackend) Run(ctx context.Context, noun, verb string, args map[string]any) (json.RawMessage, error) {
	if args == nil {
		args = map[string]any{}
	}
	payload, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	app, err := New(b.opts)
	if err != nil {
		return nil, err
	}
	defer app.Close()
	if b.prepare != nil {
		b.prepare(app)
	}
	// Every tool call runs as the agent of the pane the server serves
	// (the call's Identity), whatever the environment says.
	id, _ := mcp.IdentityFrom(ctx)
	app.setActor(mcpActor(id))
	app.actorSet = true
	var out, errOut bytes.Buffer
	if err := app.shell(strings.NewReader(""), &out, &errOut).Run(ctx, []string{noun, verb, "--json", string(payload)}); err != nil {
		return nil, err
	}
	return json.RawMessage(bytes.TrimSpace(out.Bytes())), nil
}

// Identify is whoami's lookup, the journal's rule (journalOwner): the
// pane's assignment, else its session's owner by name; then the recipe
// the pane was launched with for its cwd, harness and model. A pane mtm
// did not launch has no recipe: its cwd is the server's, which the
// harness started in its own directory.
func (b *mcpBackend) Identify(_ context.Context, id mcp.Identity) (mcp.Identity, error) {
	task, project, err := journalOwner(b.work)(id.Session, id.Window)
	if err != nil {
		return id, err
	}
	id.TaskID, id.ProjectID = task, project
	if r, err := b.work.PaneRecipe(id.Window); err == nil {
		id.Cwd, id.Harness, id.Model = r.Cwd, r.Harness, r.Model
	}
	if id.Cwd == "" {
		id.Cwd, _ = os.Getwd()
	}
	if task != "" {
		if t, err := b.work.Task(task); err == nil {
			id.TaskTitle, id.TaskStatus, id.TaskVersion = t.Title, t.Status, t.Updated
		}
	}
	if project != "" {
		if p, err := b.work.Project(project); err == nil {
			id.ProjectName, id.ProjectRepo = p.Name, p.Repo
		}
	}
	return id, nil
}
