package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"ofam.dev/matomeru/app"
	"ofam.dev/matomeru/data"
	"ofam.dev/ochazuke/core"
)

var nounAliases = map[string]string{
	"task":        data.KindTask,
	"tasks":       data.KindTask,
	"pane":        data.KindPane,
	"panes":       data.KindPane,
	"note":        data.KindNote,
	"notes":       data.KindNote,
	"document":    data.KindDocument,
	"documents":   data.KindDocument,
	"doc":         data.KindDocument,
	"docs":        data.KindDocument,
	"lane":        data.KindLane,
	"lanes":       data.KindLane,
	"schedule":    data.KindSchedule,
	"schedules":   data.KindSchedule,
	"run":         data.KindRun,
	"runs":        data.KindRun,
	"signal":      data.KindSignal,
	"signals":     data.KindSignal,
	"directory":   "directory",
	"directories": "directory",
	"dir":         "directory",
	"dirs":        "directory",
	"extension":   data.KindExtension,
	"extensions":  data.KindExtension,
	"rule":        data.KindTriageRule,
	"rules":       data.KindTriageRule,
	"triage.rule": data.KindTriageRule,
}

func resolveNoun(name string) (string, bool) {
	kind, ok := nounAliases[strings.ToLower(name)]
	return kind, ok
}

func bodyFieldName(schema data.Schema) string {
	for _, name := range []string{"body", "description", "summary"} {
		if _, ok := schema.Field(name); ok {
			return name
		}
	}
	return "body"
}

func resolveBody(o options, stdin io.Reader) (string, bool, error) {
	if !o.bodySet {
		return "", false, nil
	}
	if o.body == "-" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", true, fmt.Errorf("read stdin for --body -: %w", err)
		}
		return string(b), true, nil
	}
	return o.body, true, nil
}

func parseFieldAssignments(schema data.Schema, pairs []string) (map[string]any, error) {
	fields := map[string]any{}
	for _, kv := range pairs {
		name, text, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			return nil, fmt.Errorf("--field %s: write it as name=value", kv)
		}
		v, err := fieldValue(schema, name, text)
		if err != nil {
			return nil, err
		}
		fields[name] = v
	}
	return fields, nil
}

func filterByFields(recs []data.Record, pairs []string) ([]data.Record, error) {
	if len(pairs) == 0 {
		return recs, nil
	}
	type cond struct{ key, val string }
	conds := make([]cond, 0, len(pairs))
	for _, kv := range pairs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--field %s: write it as name=value", kv)
		}
		conds = append(conds, cond{k, v})
	}
	out := make([]data.Record, 0, len(recs))
	for _, rec := range recs {
		ok := true
		for _, c := range conds {
			got := rec.Text(c.key)
			if got == c.val {
				continue
			}
			if ref, isRef := rec.RefField(c.key); isRef && (ref.ID == c.val || data.RefText(ref) == c.val) {
				continue
			}
			ok = false
			break
		}
		if ok {
			out = append(out, rec)
		}
	}
	return out, nil
}

func parseSnoozeWhen(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errors.New("snooze needs a time or duration (e.g. 24h, 2d, 2026-12-01, tomorrow)")
	}
	if strings.EqualFold(s, "tomorrow") {
		y, m, d := now.Add(24 * time.Hour).Date()
		return time.Date(y, m, d, 9, 0, 0, 0, now.Location()), nil
	}
	if strings.HasSuffix(s, "d") && len(s) > 1 {
		if days, err := strconv.Atoi(s[:len(s)-1]); err == nil && days > 0 {
			return now.Add(time.Duration(days) * 24 * time.Hour), nil
		}
	}
	if dur, err := time.ParseDuration(s); err == nil {
		if dur <= 0 {
			return time.Time{}, fmt.Errorf("snooze duration %q must be positive", s)
		}
		return now.Add(dur), nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse snooze time %q (use duration like 2h, 2d, or RFC 3339 / YYYY-MM-DD)", s)
}

func parseRefArg(raw string) (core.Ref, error) {
	kind, id, ok := strings.Cut(raw, ":")
	if !ok || kind == "" || id == "" {
		return core.Ref{}, fmt.Errorf("target %q must be written as kind:id", raw)
	}
	if canonical, found := resolveNoun(kind); found {
		kind = canonical
	}
	return core.Ref{Kind: kind, ID: id}, nil
}

func managedCategoryFor(kind string) string {
	switch kind {
	case data.KindNote:
		return data.CategoryNotes
	case data.KindDocument:
		return data.CategoryDocuments
	default:
		return data.CategoryArtifacts
	}
}

func shouldWriteManaged(o options, kind string, existingPath, body string) bool {
	if body == "" {
		return false
	}
	if o.managed {
		return true
	}
	return kind == data.KindDocument && existingPath == ""
}

func syncManagedCLI(store *data.Store, rec *data.Record, o options, body string) (*data.Record, error) {
	if rec == nil || body == "" {
		return rec, nil
	}
	root := store.FilesRoot()
	if root == "" {
		return rec, nil
	}
	existing := rec.Text("filePath")
	if existing == "" {
		existing = rec.Text("path")
	}
	if existing != "" && !data.IsManagedPath(root, existing) && !o.managed {
		return rec, nil
	}
	if !shouldWriteManaged(o, rec.Kind, existing, body) && !data.IsManagedPath(root, existing) {
		return rec, nil
	}
	p, err := data.WriteManagedFile(root, managedCategoryFor(rec.Kind), rec.ID, rec.Text("title"), ".md", body)
	if err != nil {
		return rec, err
	}
	updated, _, err := store.Patch(data.PatchInput{
		Ref:     rec.Ref(),
		Fields:  map[string]any{"filePath": p, "path": p},
		Summary: "record managed file path",
	}, cliActor)
	if err != nil {
		return rec, err
	}
	return updated, nil
}

func dispatchNoun(ctx context.Context, store *data.Store, kind string, o options, stdin io.Reader, stdout io.Writer) error {
	if len(o.args) < 2 {
		return fmt.Errorf("usage: mtm %s list|get|create|add|edit|set|patch|star|unstar|archive|restore|snooze|unsnooze|delete|relate|unrelate ...", o.args[0])
	}
	verb := o.args[1]
	rest := o.args[2:]

	if kind == data.KindExtension {
		switch verb {
		case "discover":
			if len(rest) != 0 {
				return errors.New("usage: mtm extension discover [--dir DIR]")
			}
			res, err := app.DiscoverExtensions(ctx, store, o.dirs)
			if err != nil {
				return err
			}
			return printJSON(stdout, res)
		case "run":
			if len(rest) < 1 {
				return errors.New("usage: mtm extension run <name> [args...]")
			}
			rec, err := app.RunExtension(ctx, store, rest[0], rest[1:], cliActor)
			if err != nil {
				return err
			}
			if o.format == "table" {
				return printRecords(stdout, store, data.KindRun, o.format, []data.Record{*rec})
			}
			return printJSON(stdout, rec)
		}
	}

	switch verb {
	case "list":
		if len(rest) != 0 {
			return fmt.Errorf("usage: mtm %s list [--query Q] [--mode substring|fuzzy|regex] [--field k=v ...] [--archived] [--starred] [--format json|table]", o.args[0])
		}
		return recordList(store, o, []string{kind}, stdout)
	case "get":
		use := fmt.Sprintf("usage: mtm %s get <id>", o.args[0])
		if len(rest) > 1 {
			return errors.New(use)
		}
		query := ""
		if len(rest) == 1 {
			query = rest[0]
		}
		id, err := resolveRecordID(store, kind, query, stdin, use)
		if err != nil {
			return err
		}
		return recordGet(store, o, []string{kind, id}, stdin, stdout)
	case "create", "add":
		if len(rest) != 0 {
			return fmt.Errorf(`usage: mtm %s %s --title "..." [--body "..."|-] [--field k=v ...]`, o.args[0], verb)
		}
		return nounCreate(store, kind, o, stdin, stdout)
	case "edit", "set", "patch":
		use := fmt.Sprintf(`usage: mtm %s %s <id> [--title "..."] [--body "..."|-] [--field k=v ...]`, o.args[0], verb)
		if len(rest) > 1 {
			return errors.New(use)
		}
		query := ""
		if len(rest) == 1 {
			query = rest[0]
		}
		id, err := resolveRecordID(store, kind, query, stdin, use)
		if err != nil {
			return err
		}
		return nounEdit(store, kind, id, o, stdin, stdout)
	case "star":
		return nounStatusOp(store, kind, rest, o, stdin, stdout, store.Star)
	case "unstar":
		return nounStatusOp(store, kind, rest, o, stdin, stdout, store.Unstar)
	case "archive":
		return nounStatusOp(store, kind, rest, o, stdin, stdout, store.Archive)
	case "restore":
		return nounStatusOp(store, kind, rest, o, stdin, stdout, store.Restore)
	case "unsnooze":
		return nounStatusOp(store, kind, rest, o, stdin, stdout, store.Unsnooze)
	case "delete":
		use := fmt.Sprintf("usage: mtm %s delete <id>", o.args[0])
		if len(rest) > 1 {
			return errors.New(use)
		}
		query := ""
		if len(rest) == 1 {
			query = rest[0]
		}
		id, err := resolveRecordID(store, kind, query, stdin, use)
		if err != nil {
			return err
		}
		cs, err := store.Delete(core.Ref{Kind: kind, ID: id}, cliActor)
		if err != nil {
			return err
		}
		return printJSON(stdout, cs)
	case "snooze":
		use := fmt.Sprintf("usage: mtm %s snooze <id> <when>", o.args[0])
		var query, whenStr string
		switch len(rest) {
		case 2:
			query, whenStr = rest[0], rest[1]
		case 1:
			whenStr = rest[0]
		default:
			return errors.New(use)
		}
		id, err := resolveRecordID(store, kind, query, stdin, use)
		if err != nil {
			return err
		}
		until, err := parseSnoozeWhen(whenStr, time.Now())
		if err != nil {
			return err
		}
		rec, _, err := store.Snooze(core.Ref{Kind: kind, ID: id}, until, cliActor)
		if err != nil {
			return err
		}
		return printRecordOne(stdout, store, kind, o.format, rec)
	case "relate":
		use := fmt.Sprintf("usage: mtm %s relate <id> <rel> <target-kind:target-id>", o.args[0])
		if len(rest) != 3 {
			return errors.New(use)
		}
		fromID, err := resolveRecordID(store, kind, rest[0], stdin, use)
		if err != nil {
			return err
		}
		to, err := resolveRefArg(store, rest[2], stdin)
		if err != nil {
			return err
		}
		rel, _, err := store.Relate(data.RelateInput{From: core.Ref{Kind: kind, ID: fromID}, Kind: rest[1], To: to}, cliActor)
		if err != nil {
			return err
		}
		return printJSON(stdout, rel)
	case "unrelate":
		use := fmt.Sprintf("usage: mtm %s unrelate <id> <rel> <target-kind:target-id>", o.args[0])
		if len(rest) != 3 {
			return errors.New(use)
		}
		fromID, err := resolveRecordID(store, kind, rest[0], stdin, use)
		if err != nil {
			return err
		}
		to, err := resolveRefArg(store, rest[2], stdin)
		if err != nil {
			return err
		}
		cs, err := store.Unrelate(core.Ref{Kind: kind, ID: fromID}, rest[1], to, cliActor)
		if err != nil {
			return err
		}
		return printJSON(stdout, cs)
	default:
		return fmt.Errorf("unknown %s subcommand %q", o.args[0], verb)
	}
}

func printRecordOne(w io.Writer, store *data.Store, kind, format string, rec *data.Record) error {
	if format == "table" {
		return printRecords(w, store, kind, format, []data.Record{*rec})
	}
	return printJSON(w, rec)
}

func nounStatusOp(store *data.Store, kind string, rest []string, o options, stdin io.Reader, w io.Writer, fn func(core.Ref, core.Actor) (*data.Record, *data.ChangeSet, error)) error {
	use := fmt.Sprintf("usage: mtm %s %s <id>", o.args[0], o.args[1])
	if len(rest) > 1 {
		return errors.New(use)
	}
	query := ""
	if len(rest) == 1 {
		query = rest[0]
	}
	id, err := resolveRecordID(store, kind, query, stdin, use)
	if err != nil {
		return err
	}
	rec, _, err := fn(core.Ref{Kind: kind, ID: id}, cliActor)
	if err != nil {
		return err
	}
	return printRecordOne(w, store, kind, o.format, rec)
}

func nounCreate(store *data.Store, kind string, o options, stdin io.Reader, w io.Writer) error {
	schema := schemaOf(store, kind)
	fields, err := parseFieldAssignments(schema, o.fields)
	if err != nil {
		return err
	}
	if o.title != "" {
		name := "title"
		if _, ok := schema.Field("title"); !ok && schema.SummaryField != "" {
			name = schema.SummaryField
		}
		fields[name] = o.title
	}
	body, hasBody, err := resolveBody(o, stdin)
	if err != nil {
		return err
	}
	if hasBody {
		fields[bodyFieldName(schema)] = body
	}
	rec, _, err := store.Create(data.CreateInput{Kind: kind, Fields: fields}, cliActor)
	if err != nil {
		return err
	}
	if hasBody {
		rec, err = syncManagedCLI(store, rec, o, body)
		if err != nil {
			return err
		}
	}
	return printRecordOne(w, store, kind, o.format, rec)
}

func nounEdit(store *data.Store, kind, id string, o options, stdin io.Reader, w io.Writer) error {
	schema := schemaOf(store, kind)
	fields, err := parseFieldAssignments(schema, o.fields)
	if err != nil {
		return err
	}
	if o.title != "" {
		name := "title"
		if _, ok := schema.Field("title"); !ok && schema.SummaryField != "" {
			name = schema.SummaryField
		}
		fields[name] = o.title
	}
	body, hasBody, err := resolveBody(o, stdin)
	if err != nil {
		return err
	}
	if hasBody {
		fields[bodyFieldName(schema)] = body
	}
	if len(fields) == 0 && !o.managed {
		return errors.New("edit needs at least one of --title, --body, --field, or --managed")
	}
	ref := core.Ref{Kind: kind, ID: id}
	var rec *data.Record
	if len(fields) > 0 {
		rec, _, err = store.Patch(data.PatchInput{Ref: ref, Fields: fields}, cliActor)
		if err != nil {
			return err
		}
	} else {
		rec, err = store.Get(ref)
		if err != nil {
			return err
		}
	}
	effectiveBody := body
	if !hasBody {
		effectiveBody = rec.Text(bodyFieldName(schema))
	}
	if hasBody || o.managed {
		rec, err = syncManagedCLI(store, rec, o, effectiveBody)
		if err != nil {
			return err
		}
	}
	return printRecordOne(w, store, kind, o.format, rec)
}

func dispatchBinding(o options, stdout io.Writer) error {
	if len(o.args) < 2 {
		return errors.New("usage: mtm binding list [--all] [--json] | mtm binding check [--json]")
	}
	switch o.args[1] {
	case "list":
		if len(o.args) != 2 {
			return errors.New("usage: mtm binding list [--all] [--json]")
		}
		v, err := app.Inspect(app.InspectBindings, configDir())
		if err != nil {
			return err
		}
		rows, _ := v.([]core.Binding)
		if !o.all {
			filtered := make([]core.Binding, 0, len(rows))
			for _, b := range rows {
				if b.Hidden || b.Class != "" {
					continue
				}
				filtered = append(filtered, b)
			}
			rows = filtered
		}
		if o.json {
			return printJSON(stdout, rows)
		}
		_, err = io.WriteString(stdout, app.InspectText(rows))
		return err
	case "check":
		if len(o.args) != 2 {
			return errors.New("usage: mtm binding check [--json]")
		}
		rep, err := app.CheckBindings(configDir())
		if err != nil {
			return err
		}
		if o.json {
			if err := printJSON(stdout, rep); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(stdout, "bindings: %d total, %d errors, %d warnings\n", rep.Total, len(rep.Errors), len(rep.Warnings))
			for _, e := range rep.Errors {
				fmt.Fprintf(stdout, "ERROR [%s %s %s] %s: %s\n", e.Mode, e.Scope, e.Keys, e.Command, e.Message)
			}
		}
		if len(rep.Errors) > 0 {
			return fmt.Errorf("binding check found %d error(s)", len(rep.Errors))
		}
		return nil
	default:
		return errors.New("usage: mtm binding list [--all] [--json] | mtm binding check [--json]")
	}
}
