// Command mtm is Matomeru v2: the four-space TUI, and a small CLI over the
// same store.
//
//	mtm [--db PATH] [--seed-demo]            the TUI
//	mtm --tutorial                           the TUI on a tutorial workspace kept in memory
//	mtm --smoke [--dump]                     a scripted run with no terminal; exits 0 when every check holds
//	mtm config export [--jsonl]              the settings and bindings in effect
//	mtm inspect actions|bindings|modes|schemas [--json]
//	mtm record list KIND [--query Q] [--mode substring|fuzzy|regex] [--archived] [--starred]
//	mtm record get KIND ID
//	mtm record create KIND --title "..." [--field k=v ...]
//	mtm record undo
//	mtm mcp tools
//	mtm mcp call TOOL 'JSON'
//	mtm mcp serve                            an MCP server (JSON-RPC 2.0) on stdin and stdout
//	mtm migrate --from V1_DB [--dry-run | --apply]
//
// The CLI prints JSON; --format table prints records as a table.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/matomeru/app"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/matomeru/migrate"
	"ofam.dev/ochazuke/core"
)

const usage = `mtm [--db PATH] [--seed-demo]            open the TUI
mtm --tutorial                           open the TUI on a tutorial workspace kept in memory:
                                         nothing is read from or written to your data or config
mtm --smoke [--dump]                     scripted run, no terminal needed
                                         (with --tutorial: a scripted walk of the tutorial)
mtm config export [--jsonl]              print the settings and bindings in effect, as JSON or
                                         as JSON lines (settings first, then bindings)
mtm inspect actions|bindings|modes|schemas [--json]
                                         the registries the app runs on, as a table or as JSON:
                                         every action with its keys, the keymap, the modes, the nouns
mtm binding list [--all] [--json]        list effective key bindings
mtm binding check [--json]               validate bindings and report any errors
mtm <noun> list|get|create|add|edit|set|patch|star|unstar|archive|restore|snooze|unsnooze|delete|relate|unrelate ...
                                         nouns: task, pane, note, document (docs), lane, schedule,
                                         run, signal, directory, extension, rule (plus plurals)
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
mtm migrate --from V1_DB [--dry-run | --apply]
                                         import a v1 mtm.db; prints a JSON report. The v1
                                         file is only read. Nothing is written without --apply.

--db PATH       the SQLite file (default: $MTM_DB, or the user data directory)
config          settings.json, bindings.json, themes.json and handlers.json are read from
                $MTM_CONFIG_DIR, or ~/.config/matomeru-next. S and B in the TUI change and
                save the first two; handlers.json says what opens which file (o and O).
--format table  print records as a table instead of JSON
`

// cliActor is who the CLI writes as. "mtm record undo" undoes its changes.
var cliActor = core.Actor{Kind: "user", ID: "cli", Label: "mtm CLI"}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

type options struct {
	db, format, query, mode, title string
	body                           string
	bodySet                        bool
	from                           string
	seed, smoke, dump, help        bool
	tutorial, jsonl, json          bool
	dryRun, apply                  bool
	archived, starred              bool
	all, managed                   bool
	fields                         []string
	dirs                           []string
	args                           []string
}

// parse reads flags from anywhere on the command line; the rest are the
// subcommand and its arguments.
func parse(argv []string) (options, error) {
	o := options{format: "json"}
	text := map[string]*string{"--db": &o.db, "--format": &o.format, "--query": &o.query, "--mode": &o.mode, "--title": &o.title, "--from": &o.from, "--body": &o.body}
	flags := map[string]*bool{"--seed-demo": &o.seed, "--smoke": &o.smoke, "--dump": &o.dump, "--help": &o.help, "-h": &o.help,
		"--archived": &o.archived, "--starred": &o.starred, "--dry-run": &o.dryRun, "--apply": &o.apply,
		"--tutorial": &o.tutorial, "--jsonl": &o.jsonl, "--json": &o.json, "--all": &o.all, "--managed": &o.managed}
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		// For `mtm extension run <name> [args...]`, pass everything after <name>
		// verbatim to the extension binary so extension flags are not intercepted.
		if len(o.args) >= 3 && (o.args[0] == "extension" || o.args[0] == "extensions") && o.args[1] == "run" {
			o.args = append(o.args, argv[i:]...)
			break
		}
		name, value, inline := strings.Cut(arg, "=")
		switch {
		case arg == "--":
			o.args = append(o.args, argv[i+1:]...)
			return o, nil
		case flags[arg] != nil:
			*flags[arg] = true
		case text[name] != nil || name == "--field" || name == "--dir":
			if !inline {
				if i+1 >= len(argv) {
					return o, fmt.Errorf("%s needs a value", name)
				}
				i++
				value = argv[i]
			}
			switch name {
			case "--field":
				o.fields = append(o.fields, value)
			case "--dir":
				o.dirs = append(o.dirs, value)
			case "--body":
				o.body, o.bodySet = value, true
			default:
				*text[name] = value
			}
		case strings.HasPrefix(arg, "--"):
			return o, fmt.Errorf("unknown flag %s", arg)
		default:
			o.args = append(o.args, arg)
		}
	}
	if o.format != "json" && o.format != "table" {
		return o, fmt.Errorf("--format %s is not json or table", o.format)
	}
	return o, nil
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
	return filepath.Join(dir, "matomeru-next", "mtm.db"), nil
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

// configDirName is v2's own directory: the bindings.json of the mtm it
// replaces (~/.config/matomeru) is never read.
const configDirName = "matomeru-next"

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
	o, err := parse(argv)
	if err == nil {
		err = dispatch(o, stdin, stdout)
	}
	if err != nil {
		fmt.Fprintln(stderr, "mtm:", err)
		return 1
	}
	return 0
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
func runMigrate(ctx context.Context, o options, stdout io.Writer) error {
	const use = "usage: mtm migrate --from V1_DB [--dry-run | --apply]"
	switch {
	case len(o.args) != 1:
		return errors.New(use)
	case o.from == "":
		return errors.New("migrate needs --from, the v1 mtm.db\n" + use)
	case o.dryRun && o.apply:
		return errors.New("--dry-run and --apply cannot both be given")
	}
	target := o.db
	if target == "" {
		var err error
		if target, err = defaultDB(); err != nil {
			return err
		}
	}
	if samePath(o.from, target) {
		return fmt.Errorf("--from %s is the v2 store itself; name the v1 mtm.db", o.from)
	}
	var store *data.Store
	_, statErr := os.Stat(target)
	if o.apply || statErr == nil || target == ":memory:" || strings.HasPrefix(target, "file:") {
		var err error
		if store, err = openStore(target); err != nil {
			return err
		}
		defer store.Close()
	}
	rep, err := migrate.Import(ctx, store, migrate.Options{From: o.from, Apply: o.apply})
	if err != nil {
		return err
	}
	return printJSON(stdout, rep)
}

// runTUI opens the live app: commands run, extensions are found, panes have
// real terminals and the store is watched for others' writes. Everything it
// started is stopped when the program ends.
func runTUI(store *data.Store) error {
	a, err := app.New(store, app.Options{ConfigDir: configDir(), Live: true})
	if err != nil {
		return err
	}
	return runApp(a)
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

func dispatch(o options, stdin io.Reader, stdout io.Writer) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	switch {
	case o.help:
		fmt.Fprint(stdout, usage)
		return nil
	case o.smoke && o.tutorial:
		return app.RunTutorialSmoke(stdout)
	case o.smoke:
		return app.RunSmoke(stdout, o.dump)
	case o.tutorial:
		if len(o.args) != 0 || o.db != "" || o.seed {
			return errors.New("--tutorial opens its own workspace: it takes no command, --db or --seed-demo")
		}
		cancel() // the TUI reads ctrl+c itself
		return runTutorial()
	case len(o.args) > 0 && o.args[0] == "config":
		if len(o.args) != 2 || o.args[1] != "export" {
			return errors.New("usage: mtm config export [--jsonl]")
		}
		out, err := app.ExportConfig(configDir(), o.jsonl)
		if err != nil {
			return err
		}
		_, err = stdout.Write(out)
		return err
	case len(o.args) > 0 && o.args[0] == "inspect":
		if len(o.args) != 2 {
			return errors.New("usage: mtm inspect actions|bindings|modes|schemas [--json]")
		}
		v, err := app.Inspect(o.args[1], configDir())
		if err != nil {
			return fmt.Errorf("%w\nusage: mtm inspect actions|bindings|modes|schemas [--json]", err)
		}
		if o.json {
			return printJSON(stdout, v)
		}
		_, err = io.WriteString(stdout, app.InspectText(v))
		return err
	case len(o.args) > 0 && (o.args[0] == "binding" || o.args[0] == "bindings"):
		return dispatchBinding(o, stdout)
	case len(o.args) > 0 && o.args[0] == "migrate":
		return runMigrate(ctx, o, stdout)
	case o.from != "" || o.dryRun || o.apply:
		return errors.New("--from, --dry-run and --apply belong to mtm migrate")
	}
	store, err := openStore(o.db)
	if err != nil {
		return err
	}
	defer store.Close()
	if o.seed {
		if err := app.SeedDemo(store); err != nil {
			return err
		}
	}
	if len(o.args) == 0 {
		cancel() // the TUI reads ctrl+c itself
		return runTUI(store)
	}
	if len(o.args) == 1 && o.args[0] == "undo" {
		cs, err := store.UndoLast(cliActor)
		if err != nil {
			return err
		}
		return printJSON(stdout, cs)
	}
	if len(o.args) == 1 && o.args[0] == "redo" {
		cs, err := store.RedoLast(cliActor)
		if err != nil {
			return err
		}
		return printJSON(stdout, cs)
	}
	if kind, ok := resolveNoun(o.args[0]); ok {
		return dispatchNoun(ctx, store, kind, o, stdin, stdout)
	}
	sub := strings.Join(o.args[:min(2, len(o.args))], " ")
	rest := o.args[min(2, len(o.args)):]
	switch sub {
	case "record list":
		return recordList(store, o, rest, stdout)
	case "record get":
		return recordGet(store, o, rest, stdout)
	case "record create":
		return recordCreate(store, o, rest, stdout)
	case "record undo":
		cs, err := store.UndoLast(cliActor)
		if err != nil {
			return err
		}
		return printJSON(stdout, cs)
	case "record redo":
		cs, err := store.RedoLast(cliActor)
		if err != nil {
			return err
		}
		return printJSON(stdout, cs)
	case "mcp tools":
		return printJSON(stdout, mcpui.NewToolbox(store, nil).ToolSpecs())
	case "mcp serve":
		if len(rest) != 0 {
			return errors.New("usage: mtm mcp serve")
		}
		err := mcpui.ServeStdio(ctx, stdin, stdout, store, mcpui.New(store))
		if errors.Is(err, context.Canceled) {
			return nil // interrupted: a normal end for a server
		}
		return err
	case "mcp call":
		if len(rest) < 1 || len(rest) > 2 {
			return errors.New("usage: mtm mcp call TOOL 'JSON'")
		}
		raw := "{}"
		if len(rest) == 2 {
			raw = rest[1]
		}
		actor := core.Actor{Kind: "agent", ID: os.Getenv("MTM_PANE_ID"), Label: "mtm mcp call"}
		out, err := mcpui.NewToolbox(store, nil).Call(ctx, actor, rest[0], json.RawMessage(raw))
		if err != nil {
			return err
		}
		return printJSON(stdout, out)
	}
	return fmt.Errorf("unknown command %q\n\n%s", strings.Join(o.args, " "), usage)
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

func recordList(store *data.Store, o options, rest []string, w io.Writer) error {
	if len(rest) != 1 {
		return errors.New("usage: mtm record list KIND [--query Q] [--mode substring|fuzzy|regex] [--archived] [--starred]")
	}
	mode := data.MatchMode(o.mode)
	switch mode {
	case "":
		mode = data.MatchSubstring
	case data.MatchSubstring, data.MatchFuzzy, data.MatchRegex:
	default:
		return fmt.Errorf("--mode %s is not substring, fuzzy or regex", o.mode)
	}
	res, err := store.Query(data.Query{Kind: rest[0], Query: o.query, MatchMode: mode, IncludeArchived: o.archived, StarredOnly: o.starred})
	if err != nil {
		return err
	}
	if res.FilterErr != "" {
		return fmt.Errorf("--query is not a regular expression: %s", res.FilterErr)
	}
	items, err := filterByFields(res.Items, o.fields)
	if err != nil {
		return err
	}
	return printRecords(w, store, rest[0], o.format, items)
}

func recordGet(store *data.Store, o options, rest []string, w io.Writer) error {
	if len(rest) != 2 {
		return errors.New("usage: mtm record get KIND ID")
	}
	rec, err := store.Get(core.Ref{Kind: rest[0], ID: rest[1]})
	if err != nil {
		return err
	}
	if o.format == "table" {
		return printRecords(w, store, rest[0], o.format, []data.Record{*rec})
	}
	return printJSON(w, rec)
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

func recordCreate(store *data.Store, o options, rest []string, w io.Writer) error {
	if len(rest) != 1 {
		return errors.New(`usage: mtm record create KIND --title "..." [--field k=v ...]`)
	}
	schema := schemaOf(store, rest[0])
	fields := map[string]any{}
	if o.title != "" {
		// The title goes to the noun's summary field: an extension's is its name.
		name := "title"
		if _, ok := schema.Field("title"); !ok && schema.SummaryField != "" {
			name = schema.SummaryField
		}
		fields[name] = o.title
	}
	for _, kv := range o.fields {
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
	rec, _, err := store.Create(data.CreateInput{Kind: rest[0], Fields: fields}, cliActor)
	if err != nil {
		return err
	}
	if o.format == "table" {
		return printRecords(w, store, rest[0], o.format, []data.Record{*rec})
	}
	return printJSON(w, rec)
}
