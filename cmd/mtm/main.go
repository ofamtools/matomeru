// Command mtm is Matomeru v2: the four-space TUI, and a small CLI over the
// same store.
//
//	mtm [--db PATH] [--seed-demo]            the TUI
//	mtm --smoke [--dump]                     a scripted run with no terminal; exits 0 when every check holds
//	mtm record list KIND [--query Q] [--mode substring|fuzzy|regex] [--archived] [--starred]
//	mtm record get KIND ID
//	mtm record create KIND --title "..." [--field k=v ...]
//	mtm record undo
//	mtm mcp tools
//	mtm mcp call TOOL 'JSON'
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
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	tea "charm.land/bubbletea/v2"

	"ofam.dev/matomeru/app"
	"ofam.dev/matomeru/data"
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/ochazuke/core"
)

const usage = `mtm [--db PATH] [--seed-demo]            open the TUI
mtm --smoke [--dump]                     scripted run, no terminal needed
mtm record list KIND [--query Q] [--mode substring|fuzzy|regex] [--archived] [--starred]
mtm record get KIND ID
mtm record create KIND --title "..." [--field k=v ...]
mtm record undo
mtm mcp tools
mtm mcp call TOOL 'JSON'

--db PATH       the SQLite file (default: $MTM_DB, or the user data directory)
--format table  print records as a table instead of JSON
`

// cliActor is who the CLI writes as. "mtm record undo" undoes its changes.
var cliActor = core.Actor{Kind: "user", ID: "cli", Label: "mtm CLI"}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type options struct {
	db, format, query, mode, title string
	seed, smoke, dump, help        bool
	archived, starred              bool
	fields                         []string
	args                           []string
}

// parse reads flags from anywhere on the command line; the rest are the
// subcommand and its arguments.
func parse(argv []string) (options, error) {
	o := options{format: "json"}
	text := map[string]*string{"--db": &o.db, "--format": &o.format, "--query": &o.query, "--mode": &o.mode, "--title": &o.title}
	flags := map[string]*bool{"--seed-demo": &o.seed, "--smoke": &o.smoke, "--dump": &o.dump, "--help": &o.help, "-h": &o.help,
		"--archived": &o.archived, "--starred": &o.starred}
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		name, value, inline := strings.Cut(arg, "=")
		switch {
		case arg == "--":
			o.args = append(o.args, argv[i+1:]...)
			return o, nil
		case flags[arg] != nil:
			*flags[arg] = true
		case text[name] != nil || name == "--field":
			if !inline {
				if i+1 >= len(argv) {
					return o, fmt.Errorf("%s needs a value", name)
				}
				i++
				value = argv[i]
			}
			if name == "--field" {
				o.fields = append(o.fields, value)
			} else {
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

func configDir() string {
	if dir := os.Getenv("MTM_CONFIG_DIR"); dir != "" {
		return dir
	}
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(base, "matomeru")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "matomeru")
}

func run(argv []string, stdout, stderr io.Writer) int {
	o, err := parse(argv)
	if err == nil {
		err = dispatch(o, stdout)
	}
	if err != nil {
		fmt.Fprintln(stderr, "mtm:", err)
		return 1
	}
	return 0
}

func dispatch(o options, stdout io.Writer) error {
	switch {
	case o.help:
		fmt.Fprint(stdout, usage)
		return nil
	case o.smoke:
		return app.RunSmoke(stdout, o.dump)
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
		a, err := app.New(store, app.Options{ConfigDir: configDir()})
		if err != nil {
			return err
		}
		_, err = tea.NewProgram(a.Shell).Run()
		return err
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
	case "mcp tools":
		return printJSON(stdout, mcpui.New(store).ToolSpecs())
	case "mcp call":
		if len(rest) < 1 || len(rest) > 2 {
			return errors.New("usage: mtm mcp call TOOL 'JSON'")
		}
		raw := "{}"
		if len(rest) == 2 {
			raw = rest[1]
		}
		actor := core.Actor{Kind: "agent", ID: os.Getenv("MTM_PANE_ID"), Label: "mtm mcp call"}
		out, err := mcpui.New(store).Call(context.Background(), actor, rest[0], json.RawMessage(raw))
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
	return printRecords(w, store, rest[0], o.format, res.Items)
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
