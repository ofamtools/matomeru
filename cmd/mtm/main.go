// Command mtm is Matomeru v2: the four-space TUI, and an oden-unified CLI over
// the same store.
//
//	mtm [--db PATH] [--seed-demo]            the TUI
//	mtm --tutorial                           the TUI on a tutorial workspace kept in memory
//	mtm --smoke [--dump]                     a scripted run with no terminal; exits 0 when every check holds
//	mtm about [--socket PATH] [--json]       version, daemon, storage, config, and harnesses overview
//	mtm config export [--jsonl]              the settings and bindings in effect
//	mtm inspect actions|bindings|modes|schemas [--json]
//	mtm record list KIND [--query Q] [--mode substring|fuzzy|regex] [--archived] [--starred]
//	mtm record get KIND ID
//	mtm record create KIND --title "..." [--field k=v ...]
//	mtm record undo | redo
//	mtm mcp tools
//	mtm mcp call TOOL 'JSON'
//	mtm mcp serve                            an MCP server (JSON-RPC 2.0) on stdin and stdout
//	mtm agent hook [harness] [flags]         forward agent hook JSON to the local daemon (fast path)
//	mtm agent state <state> [flags]          send a manual turn state update
//	mtm daemon serve|status|reset|stop       manage the local self-hosted daemon
//	mtm harness list [--json]                list available harnesses and configured launch commands
//	mtm migrate --from V1_DB [--dry-run | --apply]
//
// Record commands print JSON; --format table prints records as a table.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/matomeru/agenthook"
	"ofam.dev/matomeru/app"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/migrate"
	ocli "ofam.dev/ochazuke/cli"
	"ofam.dev/ochazuke/core"
	"ofam.dev/oden"
)

const mtmVersion = "2.0.0-next"

const usage = `mtm [--db PATH] [--seed-demo]            open the TUI
mtm --tutorial                           open the TUI on a tutorial workspace kept in memory:
                                         nothing is read from or written to your data or config
mtm --smoke [--dump]                     scripted run, no terminal needed
                                         (with --tutorial: a scripted walk of the tutorial)
mtm about [--socket PATH] [--json]       show version, daemon, storage, config, and harness status
mtm config export [--jsonl]              print the settings and bindings in effect, as JSON or
                                         as JSON lines (settings first, then bindings)
mtm inspect actions|bindings|modes|schemas [--json]
                                         the registries the app runs on, as a table or as JSON:
                                         every action with its keys, the keymap, the modes, the nouns
mtm binding list [--all] [--json]        list effective key bindings
mtm binding check [--json]               validate bindings and report any errors
mtm <noun> list|get|create|add|edit|set|patch|star|unstar|archive|restore|snooze|unsnooze|delete|relate|unrelate ...
                                         nouns: task, pane, note, document (docs), lane, schedule,
                                         run, signal, directory, extension, rule, question (q),
                                         command (cmd), context (ctx) (plus plurals)
mtm question answer <id> (--answer "..." | <answer>)
                                         answer a question and sync its linked pane ui.page card
mtm extension discover [--dir DIR]       discover installed o-* extensions
mtm extension run <name> [args...]       run an extension command as a recorded run
mtm undo | redo                          undo or redo the last CLI change
mtm record list KIND [--query Q] [--mode substring|fuzzy|regex] [--field k=v ...] [--archived] [--starred]
mtm record get KIND ID
mtm record create KIND --title "..." [--field k=v ...]
mtm record undo | redo
mtm mcp tools
mtm mcp call TOOL 'JSON'
mtm mcp serve                            MCP server on stdin/stdout (JSON-RPC 2.0, one message per line)
mtm agent hook [harness] [flags]         forward agent hook JSON from stdin to the local daemon (fast path)
mtm agent state <state> [flags]          send a manual turn state update (running|needs_you|idle|done)
mtm daemon serve [--socket PATH] [--mcp-addr ADDR] [--db PATH]
                                         run the local daemon (PTY host, HTTP MCP server, scheduler)
mtm daemon status [--socket PATH] [--json]
                                         show local daemon status
mtm daemon reset [--socket PATH] [--json]
                                         drop all live panes held by the daemon
mtm daemon upgrade [--binary PATH] [--socket PATH] [--json]
                                         re-exec the daemon in place without killing live panes or subagents
mtm daemon stop [--socket PATH] [--json] stop the local daemon
mtm harness list [--json]                list available harnesses and configured launch commands
mtm migrate --from V1_DB [--dry-run | --apply]
                                         import a v1 mtm.db; prints a JSON report. The v1
                                         file is only read. Nothing is written without --apply.

--db PATH       the SQLite file (default: $MTM_DB, or the user data directory)
config          settings.json, bindings.json, themes.json, harnesses.json, handlers.json,
                actions.json and drafts.json are read from $MTM_CONFIG_DIR, or ~/.config/matomeru.
                S and B in the TUI change and save the first two; handlers.json says what opens
                which file (o and O).
--format table  print records as a table instead of JSON`

// cliActor is who the CLI writes as. "mtm record undo" undoes its changes.
var cliActor = core.Actor{Kind: "user", ID: "cli", Label: "mtm CLI"}

var paramFormatJSON = oden.Parameter{
	Name:        "format",
	Type:        "string",
	Default:     "json",
	Enum:        []string{"json", "table"},
	Description: "Output format (json or table)",
}

func recordFormat(p oden.Parsed) string {
	if p.Bool("json") {
		return "json"
	}
	if f := p.String("format"); f != "" {
		return f
	}
	return "json"
}

type globalOpts struct {
	db       string
	seed     bool
	tutorial bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// extractGlobalFlags peels root-level flags (--db, --seed-demo, --tutorial)
// from argv so they work before or after any subcommand while leaving all
// subcommand flags and extension run arguments untouched.
func extractGlobalFlags(argv []string) (globalOpts, []string, error) {
	var g globalOpts
	rest := make([]string, 0, len(argv))
	var words []string
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		// For `mtm extension run <name> [args...]`, pass everything after <name>
		// verbatim to the extension binary so extension flags are not intercepted.
		if len(words) >= 3 && (words[0] == "extension" || words[0] == "extensions") && words[1] == "run" {
			rest = append(rest, argv[i:]...)
			break
		}
		if arg == "--" {
			rest = append(rest, argv[i:]...)
			break
		}
		if arg == "--seed-demo" {
			g.seed = true
			continue
		}
		if arg == "--tutorial" {
			g.tutorial = true
			continue
		}
		if val, ok := strings.CutPrefix(arg, "--db="); ok {
			g.db = val
			continue
		}
		if arg == "--db" {
			if i+1 >= len(argv) {
				return g, nil, errors.New("--db needs a value")
			}
			i++
			g.db = argv[i]
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			words = append(words, arg)
		}
		rest = append(rest, arg)
	}
	return g, rest, nil
}

func withCommands(cmds ...oden.Command) oden.Option {
	return func(a *oden.App) {
		a.Add(cmds...)
	}
}

func defaultDB() (string, error) {
	if p := os.Getenv("MTM_DB"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if base := os.Getenv("XDG_DATA_HOME"); base != "" {
		dir, err = base, nil
	} else if home, herr := os.UserHomeDir(); herr == nil {
		dir, err = filepath.Join(home, ".local", "share"), nil
	}
	if err != nil {
		return "", err
	}
	// Its own directory: v2 never opens the data of the mtm it replaces.
	return filepath.Join(dir, "matomeru", "mtm.db"), nil
}

func openStore(path string) (*data.Store, error) {
	if path == "" {
		var err error
		if path, err = defaultDB(); err != nil {
			return nil, err
		}
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}
	return data.Open(path)
}

func openStoreWithSeed(g globalOpts) (*data.Store, error) {
	store, err := openStore(g.db)
	if err != nil {
		return nil, err
	}
	if g.seed {
		if err := app.SeedDemo(store); err != nil {
			store.Close()
			return nil, err
		}
	}
	return store, nil
}

// configDirName is v2's own directory: the bindings.json of the mtm it
// replaces (~/.config/matomeru) is never read.
const configDirName = "matomeru"

func configDir() string {
	if dir := os.Getenv("MTM_CONFIG_DIR"); dir != "" {
		return dir
	}
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(base, configDirName)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", configDirName)
}

func run(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	g, args, err := extractGlobalFlags(argv)
	if err != nil {
		return oden.Report(stderr, "mtm", err)
	}
	isSmoke := len(args) > 0 && (args[0] == "--smoke" || args[0] == "smoke")
	if g.tutorial && !isSmoke && (len(args) != 0 || g.db != "" || g.seed) {
		return oden.Report(stderr, "mtm", errors.New("--tutorial opens its own workspace: it takes no command, --db or --seed-demo"))
	}
	// Fast-path agent hook/state before oden setup or opening SQLite so
	// hook invocations stay under 20ms and never touch the database.
	if len(args) >= 2 && args[0] == "agent" {
		switch args[1] {
		case "hook":
			return agenthook.Run(args[2:], stdin, stdout, stderr)
		case "state":
			return agenthook.Run(args[1:], stdin, stdout, stderr)
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	mtmApp := newCLIApp(g, args, cancel, stdin, stdout, stderr)
	return oden.Report(stderr, "mtm", mtmApp.Run(ctx))
}

func newCLIApp(g globalOpts, args []string, cancel context.CancelFunc, stdin io.Reader, stdout, stderr io.Writer) *oden.App {
	info := ocli.AppInfo{
		Name:     "mtm",
		Version:  mtmVersion,
		Summary:  "Matomeru v2 — four-space TUI and CLI over the unified store",
		HelpText: usage,
		Modes:    app.Modes,
		Bindings: func() []core.Binding {
			v, err := app.Inspect(app.InspectBindings, configDir())
			if err != nil {
				return app.DefaultBindings()
			}
			bs, _ := v.([]core.Binding)
			return bs
		},
		Specs: app.CommandSpecs,
		RunSmoke: func(out io.Writer, dump bool) error {
			if g.tutorial {
				return app.RunTutorialSmoke(out)
			}
			return app.RunSmoke(out, dump)
		},
		RunTUI: func(_ context.Context, rootArgs []string) error {
			if len(rootArgs) > 0 {
				return fmt.Errorf("unknown flag %s", rootArgs[0])
			}
			if g.tutorial {
				cancel() // the TUI reads ctrl+c itself
				return runTutorial()
			}
			dbPath := g.db
			if dbPath == "" {
				dbPath, _ = defaultDB()
			}
			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			cancel() // the TUI reads ctrl+c itself
			reload, err := runTUI(store, dbPath, stderr)
			_ = store.Close()
			if err != nil {
				return err
			}
			if reload {
				return execSelfReload()
			}
			return nil
		},
	}

	a := ocli.NewApp(info, args,
		oden.WithPrefixes("o"),
		oden.WithStdio(stdin, stdout, stderr),
		withCommands(
			mtmBindingCommand(),
			mtmInspectCommand(),
		),
	)

	a.Add(
		mtmAboutCommand(g),
		mtmConfigCommand(),
		mtmRecordCommand(g),
		mtmMCPCommand(g),
		mtmAgentCommand(),
		mtmDaemonCommand(g),
		mtmHarnessCommand(),
		mtmMigrateCommand(g),
		mtmUndoCommand(g),
		mtmRedoCommand(g),
	)
	a.Add(mtmNounCommands(g)...)

	baseClaims := a.Claims
	a.Claims = func(rootArgs []string) bool {
		if len(rootArgs) > 1 && strings.EqualFold(rootArgs[0], "commands") {
			return true
		}
		return baseClaims != nil && baseClaims(rootArgs)
	}

	baseDefault := a.Default
	a.Default = func(ctx context.Context, rootArgs []string) error {
		if len(rootArgs) > 0 && strings.EqualFold(rootArgs[0], data.KindTriageRule) {
			if e := a.Catalog().Lookup("rule"); e != nil {
				return a.RunEntry(ctx, e, rootArgs[1:])
			}
		}
		if len(rootArgs) > 1 && strings.EqualFold(rootArgs[0], "commands") {
			if e := a.Catalog().Lookup("command"); e != nil {
				return a.RunEntry(ctx, e, rootArgs[1:])
			}
		}
		return baseDefault(ctx, rootArgs)
	}

	return a
}

func mtmConfigCommand() oden.Command {
	exportParams := []oden.Parameter{
		{Name: "jsonl", Type: "bool", Description: "Emit configuration as JSON Lines"},
	}
	return oden.Command{
		Name:     "config",
		Summary:  "inspect and export configuration",
		Category: "Configuration",
		Handle: func(c *oden.Ctx) error {
			sub := c.Sub()
			sub.Add(oden.Command{
				Name:       "export",
				Summary:    "export effective settings and bindings as JSON or JSONL",
				Usage:      "[--jsonl]",
				Parameters: exportParams,
				Handle: func(sc *oden.Ctx) error {
					p, err := sc.Parse(exportParams)
					if err != nil {
						return err
					}
					if len(p.Positional()) != 0 {
						return errors.New("usage: mtm config export [--jsonl]")
					}
					out, err := app.ExportConfig(configDir(), p.Bool("jsonl"))
					if err != nil {
						return err
					}
					_, err = sc.Stdout.Write(out)
					return err
				},
			})
			if len(c.Args) == 1 && (c.Args[0] == "-h" || c.Args[0] == "--help") {
				sub.Help(c.Stdout)
				return nil
			}
			sub.Default = func(_ context.Context, _ []string) error {
				return errors.New("usage: mtm config export [--jsonl]")
			}
			return sub.Run(c.Context)
		},
	}
}

func mtmInspectCommand() oden.Command {
	inspectLeaf := func(kind, summary string) oden.Command {
		return oden.Command{
			Name:    kind,
			Summary: summary,
			Usage:   "[--json]",
			Handle: func(sc *oden.Ctx) error {
				p, err := sc.Parse(nil)
				if err != nil {
					return err
				}
				if len(p.Positional()) != 0 {
					return errors.New("usage: mtm inspect actions|bindings|modes|schemas [--json]")
				}
				v, err := app.Inspect(kind, configDir())
				if err != nil {
					return fmt.Errorf("%w\nusage: mtm inspect actions|bindings|modes|schemas [--json]", err)
				}
				if p.Format() == "json" {
					return printJSON(sc.Stdout, v)
				}
				_, err = io.WriteString(sc.Stdout, app.InspectText(v))
				return err
			},
		}
	}
	return oden.Command{
		Name:     "inspect",
		Summary:  "inspect actions, bindings, modes, and schemas",
		Category: "Configuration",
		Handle: func(c *oden.Ctx) error {
			sub := c.Sub()
			sub.Add(
				inspectLeaf(app.InspectActions, "list every action with its bound keys"),
				inspectLeaf(app.InspectBindings, "list all effective key bindings"),
				inspectLeaf(app.InspectModes, "list registered input modes"),
				inspectLeaf(app.InspectSchemas, "list registered record schemas"),
			)
			if len(c.Args) == 1 && (c.Args[0] == "-h" || c.Args[0] == "--help") {
				sub.Help(c.Stdout)
				return nil
			}
			sub.Default = func(_ context.Context, subArgs []string) error {
				if len(subArgs) == 0 {
					return errors.New("usage: mtm inspect actions|bindings|modes|schemas [--json]")
				}
				return fmt.Errorf("inspect %s: not one of %s\nusage: mtm inspect actions|bindings|modes|schemas [--json]", subArgs[0], strings.Join(app.InspectKinds, ", "))
			}
			return sub.Run(c.Context)
		},
	}
}

func mtmRecordCommand(g globalOpts) oden.Command {
	listParams := []oden.Parameter{
		{Name: "query", Type: "string", Description: "Filter query string"},
		{Name: "mode", Type: "string", Default: "substring", Enum: []string{"substring", "fuzzy", "regex"}, Description: "Query match mode"},
		{Name: "field", Type: "string", Repeatable: true, Description: "Filter by field value (name=value)"},
		{Name: "archived", Type: "bool", Description: "Include archived records"},
		{Name: "starred", Type: "bool", Description: "Show only starred records"},
		paramFormatJSON,
	}
	getParams := []oden.Parameter{paramFormatJSON}
	createParams := []oden.Parameter{
		{Name: "title", Type: "string", Description: "Record title"},
		{Name: "field", Type: "string", Repeatable: true, Description: "Field assignment (name=value)"},
		paramFormatJSON,
	}

	return oden.Command{
		Name:     "record",
		Summary:  "low-level record operations across any schema kind",
		Category: "Records",
		Children: []oden.Command{
			{
				Name:       "list",
				Summary:    "list records of a given kind",
				Usage:      "KIND [--query Q] [--mode substring|fuzzy|regex] [--field k=v ...] [--archived] [--starred] [--format json|table]",
				Parameters: listParams,
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(listParams)
					if err != nil {
						return err
					}
					pos := p.Positional()
					if len(pos) != 1 {
						return errors.New("usage: mtm record list KIND [--query Q] [--mode substring|fuzzy|regex] [--archived] [--starred]")
					}
					store, err := openStoreWithSeed(g)
					if err != nil {
						return err
					}
					defer store.Close()
					return recordListParsed(store, pos[0], p, c.Stdout)
				},
			},
			{
				Name:       "get",
				Summary:    "get one record by kind and ID/title",
				Usage:      "KIND [ID] [--format json|table]",
				Parameters: getParams,
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(getParams)
					if err != nil {
						return err
					}
					pos := p.Positional()
					const use = "usage: mtm record get KIND ID"
					if len(pos) < 1 || len(pos) > 2 {
						return errors.New(use)
					}
					query := ""
					if len(pos) == 2 {
						query = pos[1]
					}
					store, err := openStoreWithSeed(g)
					if err != nil {
						return err
					}
					defer store.Close()
					id, err := resolveRecordID(c.Context, store, pos[0], query, c.Stdin, use)
					if err != nil {
						return err
					}
					rec, err := store.Get(core.Ref{Kind: pos[0], ID: id})
					if err != nil {
						return err
					}
					return printRecordOne(c.Stdout, store, pos[0], recordFormat(p), rec)
				},
			},
			{
				Name:       "create",
				Summary:    "create a record of a given kind",
				Usage:      `KIND --title "..." [--field k=v ...] [--format json|table]`,
				Parameters: createParams,
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(createParams)
					if err != nil {
						return err
					}
					pos := p.Positional()
					if len(pos) != 1 {
						return errors.New(`usage: mtm record create KIND --title "..." [--field k=v ...]`)
					}
					store, err := openStoreWithSeed(g)
					if err != nil {
						return err
					}
					defer store.Close()
					return recordCreateParsed(store, pos[0], p, c.Stdout)
				},
			},
			{
				Name:    "undo",
				Summary: "undo the last CLI change",
				Handle: func(c *oden.Ctx) error {
					if _, err := c.Parse(nil); err != nil {
						return err
					}
					store, err := openStoreWithSeed(g)
					if err != nil {
						return err
					}
					defer store.Close()
					cs, err := store.UndoLast(cliActor)
					if err != nil {
						return err
					}
					return printJSON(c.Stdout, cs)
				},
			},
			{
				Name:    "redo",
				Summary: "redo the last undone CLI change",
				Handle: func(c *oden.Ctx) error {
					if _, err := c.Parse(nil); err != nil {
						return err
					}
					store, err := openStoreWithSeed(g)
					if err != nil {
						return err
					}
					defer store.Close()
					cs, err := store.RedoLast(cliActor)
					if err != nil {
						return err
					}
					return printJSON(c.Stdout, cs)
				},
			},
		},
	}
}

func mtmMCPCommand(g globalOpts) oden.Command {
	return oden.Command{
		Name:     "mcp",
		Summary:  "MCP server and tool calls over the store",
		Category: "MCP",
		Children: []oden.Command{
			{
				Name:    "tools",
				Summary: "list available MCP tool specs as JSON",
				Handle: func(c *oden.Ctx) error {
					if _, err := c.Parse(nil); err != nil {
						return err
					}
					store, err := openStoreWithSeed(g)
					if err != nil {
						return err
					}
					defer store.Close()
					return printJSON(c.Stdout, mcpui.NewToolbox(store, nil).ToolSpecs())
				},
			},
			{
				Name:    "call",
				Summary: "invoke one MCP tool with JSON arguments",
				Usage:   "TOOL ['JSON']",
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(nil)
					if err != nil {
						return err
					}
					pos := p.Positional()
					if len(pos) < 1 || len(pos) > 2 {
						return errors.New("usage: mtm mcp call TOOL 'JSON'")
					}
					raw := "{}"
					if len(pos) == 2 {
						raw = pos[1]
					}
					store, err := openStoreWithSeed(g)
					if err != nil {
						return err
					}
					defer store.Close()
					actor := core.Actor{Kind: "agent", ID: os.Getenv("MTM_PANE_ID"), Label: "mtm mcp call"}
					out, err := mcpui.NewToolbox(store, nil).Call(c.Context, actor, pos[0], json.RawMessage(raw))
					if err != nil {
						return err
					}
					return printJSON(c.Stdout, out)
				},
			},
			{
				Name:    "serve",
				Summary: "run an MCP JSON-RPC 2.0 server on stdin/stdout",
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(nil)
					if err != nil {
						return err
					}
					if len(p.Positional()) != 0 {
						return errors.New("usage: mtm mcp serve")
					}
					store, err := openStoreWithSeed(g)
					if err != nil {
						return err
					}
					defer store.Close()
					err = mcpui.ServeStdio(c.Context, c.Stdin, c.Stdout, store, mcpui.New(store))
					if errors.Is(err, context.Canceled) {
						return nil // interrupted: a normal end for a server
					}
					return err
				},
			},
		},
	}
}

func mtmMigrateCommand(g globalOpts) oden.Command {
	params := []oden.Parameter{
		{Name: "from", Type: "string", Description: "Path to v1 mtm.db"},
		{Name: "dry-run", Type: "bool", Description: "Preview changes without writing"},
		{Name: "apply", Type: "bool", Description: "Apply migration to the v2 store"},
	}
	return oden.Command{
		Name:       "migrate",
		Summary:    "import a v1 mtm.db into the v2 store",
		Category:   "Data",
		Usage:      "--from V1_DB [--dry-run | --apply]",
		Parameters: params,
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(params)
			if err != nil {
				return err
			}
			return runMigrate(c.Context, g, p, c.Stdout)
		},
	}
}

func mtmUndoCommand(g globalOpts) oden.Command {
	return oden.Command{
		Name:     "undo",
		Summary:  "undo the last CLI change",
		Category: "Records",
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(nil)
			if err != nil {
				return err
			}
			if len(p.Positional()) != 0 {
				return errors.New("usage: mtm undo")
			}
			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			defer store.Close()
			cs, err := store.UndoLast(cliActor)
			if err != nil {
				return err
			}
			return printJSON(c.Stdout, cs)
		},
	}
}

func mtmRedoCommand(g globalOpts) oden.Command {
	return oden.Command{
		Name:     "redo",
		Summary:  "redo the last undone CLI change",
		Category: "Records",
		Handle: func(c *oden.Ctx) error {
			p, err := c.Parse(nil)
			if err != nil {
				return err
			}
			if len(p.Positional()) != 0 {
				return errors.New("usage: mtm redo")
			}
			store, err := openStoreWithSeed(g)
			if err != nil {
				return err
			}
			defer store.Close()
			cs, err := store.RedoLast(cliActor)
			if err != nil {
				return err
			}
			return printJSON(c.Stdout, cs)
		},
	}
}

// samePath reports whether a and b name the same file.
func samePath(a, b string) bool {
	if x, err := filepath.Abs(a); err == nil {
		a = x
	}
	if y, err := filepath.Abs(b); err == nil {
		b = y
	}
	if a == b {
		return true
	}
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}

// runMigrate imports a v1 database into the v2 store and prints the report.
// It is a dry run unless --apply is given. A dry run writes nothing: it reads
// the v2 store when there is one (to tell new from unchanged) and does not
// create it.
func runMigrate(ctx context.Context, g globalOpts, p oden.Parsed, stdout io.Writer) error {
	const use = "usage: mtm migrate --from V1_DB [--dry-run | --apply]"
	from := p.String("from")
	dryRun := p.Bool("dry-run")
	apply := p.Bool("apply")
	switch {
	case len(p.Positional()) != 0:
		return errors.New(use)
	case from == "":
		return errors.New("migrate needs --from, the v1 mtm.db\n" + use)
	case dryRun && apply:
		return errors.New("--dry-run and --apply cannot both be given")
	}
	target := g.db
	if target == "" {
		var err error
		if target, err = defaultDB(); err != nil {
			return err
		}
	}
	if samePath(from, target) {
		return fmt.Errorf("--from %s is the v2 store itself; name the v1 mtm.db", from)
	}
	var store *data.Store
	_, statErr := os.Stat(target)
	if apply || statErr == nil || target == ":memory:" || strings.HasPrefix(target, "file:") {
		var err error
		if store, err = openStore(target); err != nil {
			return err
		}
		defer store.Close()
	}
	rep, err := migrate.Import(ctx, store, migrate.Options{From: from, Apply: apply})
	if err != nil {
		return err
	}
	return printJSON(stdout, rep)
}

// runTUI opens the live app: commands run, extensions are found, panes have
// real terminals (backed by the local self-hosted daemon unless disabled) and
// the store is watched for others' writes. Everything it started is stopped
// when the program ends.
func runTUI(store *data.Store, dbPath string, stderr io.Writer) (bool, error) {
	opts, warnings := buildTUIOptions(context.Background(), dbPath, configDir(), stderr, nil)
	a, err := app.New(store, opts)
	if err != nil {
		return false, err
	}
	if len(warnings) > 0 {
		a.Warnings = append(a.Warnings, warnings...)
	}
	err = runApp(a)
	return a.WantReload(), err
}

func execSelfReload() error {
	bin := ""
	if len(os.Args) > 0 && os.Args[0] != "" {
		if p, err := exec.LookPath(os.Args[0]); err == nil {
			bin = p
		}
	}
	if bin == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("reload client: resolve binary: %w", err)
		}
		bin = exe
	}
	if abs, err := filepath.Abs(bin); err == nil {
		bin = abs
	}
	return syscall.Exec(bin, os.Args, os.Environ())
}

// runTutorial opens the live app on the tutorial workspace: a store kept in
// memory and no user configuration.
func runTutorial() error {
	a, err := app.NewTutorial(app.Options{Live: true})
	if err != nil {
		return err
	}
	defer a.Store.Close()
	return runApp(a)
}

func runApp(a *app.App) error {
	defer a.Close() // cancels runs, closes every pane session
	p := tea.NewProgram(a.Program())
	stop := a.Start(p.Send)
	_, err := p.Run()
	stop()
	return err
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func schemaOf(store *data.Store, kind string) data.Schema {
	if s, ok := store.Registry().Get(kind); ok {
		return s
	}
	return data.Schema{Kind: kind, Title: kind, Plural: kind}
}

// printRecords prints records as JSON, or as a table of the schema's default
// columns with the ID first.
func printRecords(w io.Writer, store *data.Store, kind, format string, recs []data.Record) error {
	if format != "table" {
		if recs == nil {
			recs = []data.Record{}
		}
		return printJSON(w, recs)
	}
	cols := schemaOf(store, kind).DefaultColumns
	if len(cols) == 0 {
		cols = []string{"title"}
	}
	tw := tabwriter.NewWriter(w, 2, 8, 2, ' ', 0)
	fmt.Fprintln(tw, strings.ToUpper("id\t"+strings.Join(cols, "\t")+"\tstar\tarchive"))
	for _, rec := range recs {
		row := []string{rec.ID}
		for _, c := range cols {
			row = append(row, strings.ReplaceAll(rec.Text(c), "\n", " "))
		}
		fmt.Fprintln(tw, strings.Join(append(row, rec.StarStatus, rec.ArchiveStatus), "\t"))
	}
	return tw.Flush()
}

func recordListParsed(store *data.Store, kind string, p oden.Parsed, w io.Writer) error {
	mode := data.MatchMode(p.String("mode"))
	switch mode {
	case "":
		mode = data.MatchSubstring
	case data.MatchSubstring, data.MatchFuzzy, data.MatchRegex:
	default:
		return fmt.Errorf("--mode %s is not substring, fuzzy or regex", p.String("mode"))
	}
	res, err := store.Query(data.Query{
		Kind:            kind,
		Query:           p.String("query"),
		MatchMode:       mode,
		IncludeArchived: p.Bool("archived"),
		StarredOnly:     p.Bool("starred"),
	})
	if err != nil {
		return err
	}
	if res.FilterErr != "" {
		return fmt.Errorf("--query is not a regular expression: %s", res.FilterErr)
	}
	items, err := filterByFields(res.Items, p.Strings("field"))
	if err != nil {
		return err
	}
	return printRecords(w, store, kind, recordFormat(p), items)
}

// fieldValue reads a --field value as the schema's type for that field: a
// number, a bool, a reference ("kind:id", or a bare ID when the field takes
// one kind), a comma-separated list or JSON. Anything else is text.
func fieldValue(schema data.Schema, name, text string) (any, error) {
	spec, ok := schema.Field(name)
	if !ok {
		return text, nil
	}
	switch spec.Type {
	case core.ParamInt:
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("--field %s=%s: not a whole number", name, text)
		}
		return n, nil
	case core.ParamFloat:
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return nil, fmt.Errorf("--field %s=%s: not a number", name, text)
		}
		return f, nil
	case core.ParamBool:
		b, err := strconv.ParseBool(text)
		if err != nil {
			return nil, fmt.Errorf("--field %s=%s: not true or false", name, text)
		}
		return b, nil
	case core.ParamRef:
		if kind, id, ok := strings.Cut(text, ":"); ok && kind != "" && id != "" {
			return core.Ref{Kind: kind, ID: id}, nil
		}
		if len(spec.RefKinds) == 1 {
			return core.Ref{Kind: spec.RefKinds[0], ID: text}, nil
		}
		return nil, fmt.Errorf("--field %s=%s: write the reference as kind:id", name, text)
	case core.ParamList:
		var out []string
		for _, part := range strings.Split(text, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
		return out, nil
	case core.ParamObject:
		var v any
		if err := json.Unmarshal([]byte(text), &v); err != nil {
			return nil, fmt.Errorf("--field %s: not JSON: %w", name, err)
		}
		return v, nil
	}
	return text, nil
}

func recordCreateParsed(store *data.Store, kind string, p oden.Parsed, w io.Writer) error {
	schema := schemaOf(store, kind)
	fields := map[string]any{}
	if title := p.String("title"); title != "" {
		// The title goes to the noun's summary field: an extension's is its name.
		name := "title"
		if _, ok := schema.Field("title"); !ok && schema.SummaryField != "" {
			name = schema.SummaryField
		}
		fields[name] = title
	}
	for _, kv := range p.Strings("field") {
		name, text, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			return fmt.Errorf("--field %s: write it as name=value", kv)
		}
		v, err := fieldValue(schema, name, text)
		if err != nil {
			return err
		}
		fields[name] = v
	}
	rec, _, err := store.Create(data.CreateInput{Kind: kind, Fields: fields}, cliActor)
	if err != nil {
		return err
	}
	return printRecordOne(w, store, kind, recordFormat(p), rec)
}
