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
	"ofam.dev/matomeru/mcpui"
	"ofam.dev/ochazuke/core"
	"ofam.dev/oden"
)

type nounSpec struct {
	name    string
	aliases []string
	kind    string
	summary string
}

var nounSpecs = []nounSpec{
	{name: "task", aliases: []string{"tasks"}, kind: data.KindTask, summary: "manage tasks"},
	{name: "pane", aliases: []string{"panes"}, kind: data.KindPane, summary: "manage terminal panes"},
	{name: "note", aliases: []string{"notes"}, kind: data.KindNote, summary: "manage notes"},
	{name: "document", aliases: []string{"documents", "doc", "docs"}, kind: data.KindDocument, summary: "manage documents"},
	{name: "lane", aliases: []string{"lanes"}, kind: data.KindLane, summary: "manage work lanes"},
	{name: "schedule", aliases: []string{"schedules"}, kind: data.KindSchedule, summary: "manage schedules"},
	{name: "run", aliases: []string{"runs"}, kind: data.KindRun, summary: "manage command runs"},
	{name: "signal", aliases: []string{"signals"}, kind: data.KindSignal, summary: "manage inbox signals"},
	{name: "directory", aliases: []string{"directories", "dir", "dirs"}, kind: "directory", summary: "manage directories"},
	{name: "extension", aliases: []string{"extensions"}, kind: data.KindExtension, summary: "manage and run o-* extensions"},
	{name: "rule", aliases: []string{"rules"}, kind: data.KindTriageRule, summary: "manage triage rules"},
	{name: "question", aliases: []string{"questions", "q"}, kind: data.KindQuestion, summary: "manage agent questions and Q&A"},
	{name: "command", aliases: []string{"commands", "cmd"}, kind: data.KindCommand, summary: "manage suggested shell commands"},
	{name: "context", aliases: []string{"contexts", "ctx"}, kind: data.KindContext, summary: "manage context items"},
	{name: "concept", aliases: []string{"concepts"}, kind: data.KindConcept, summary: "manage tracked design concepts"},
	{name: "queue", aliases: []string{"queues"}, kind: data.KindQueue, summary: "manage named agent queues"},
	{name: "repo", aliases: []string{"repos"}, kind: data.KindRepo, summary: "manage tracked git repositories"},
	{name: "capture", aliases: []string{"captures"}, kind: data.KindCapture, summary: "manage captured dictation and composed prompts"},
	{name: "page", aliases: []string{"pages", data.KindPage}, kind: data.KindPage, summary: "manage UI pages"},
	{name: "occurrence", aliases: []string{"occurrences", data.KindOccurrence}, kind: data.KindOccurrence, summary: "manage schedule occurrences"},
}

var nounAliases = map[string]string{
	"task":                data.KindTask,
	"tasks":               data.KindTask,
	"pane":                data.KindPane,
	"panes":               data.KindPane,
	"note":                data.KindNote,
	"notes":               data.KindNote,
	"document":            data.KindDocument,
	"documents":           data.KindDocument,
	"doc":                 data.KindDocument,
	"docs":                data.KindDocument,
	"lane":                data.KindLane,
	"lanes":               data.KindLane,
	"schedule":            data.KindSchedule,
	"schedules":           data.KindSchedule,
	"run":                 data.KindRun,
	"runs":                data.KindRun,
	"signal":              data.KindSignal,
	"signals":             data.KindSignal,
	"directory":           "directory",
	"directories":         "directory",
	"dir":                 "directory",
	"dirs":                "directory",
	"extension":           data.KindExtension,
	"extensions":          data.KindExtension,
	"rule":                data.KindTriageRule,
	"rules":               data.KindTriageRule,
	"triage.rule":         data.KindTriageRule,
	"question":            data.KindQuestion,
	"questions":           data.KindQuestion,
	"q":                   data.KindQuestion,
	"command":             data.KindCommand,
	"commands":            data.KindCommand,
	"cmd":                 data.KindCommand,
	"context":             data.KindContext,
	"contexts":            data.KindContext,
	"ctx":                 data.KindContext,
	"concept":             data.KindConcept,
	"concepts":            data.KindConcept,
	"queue":               data.KindQueue,
	"queues":              data.KindQueue,
	"repo":                data.KindRepo,
	"repos":               data.KindRepo,
	"capture":             data.KindCapture,
	"captures":            data.KindCapture,
	"page":                data.KindPage,
	"pages":               data.KindPage,
	"ui.page":             data.KindPage,
	"occurrence":          data.KindOccurrence,
	"occurrences":         data.KindOccurrence,
	"schedule.occurrence": data.KindOccurrence,
	"tag":                 data.KindTag,
	"tags":                data.KindTag,
	"workspace":           data.KindWorkspace,
	"workspaces":          data.KindWorkspace,
}

func resolveNoun(name string) (string, bool) {
	kind, ok := nounAliases[strings.ToLower(name)]
	return kind, ok
}

func bodyFieldName(schema data.Schema) string {
	for _, name := range []string{"body", "description", "summary", "details", "notes"} {
		if _, ok := schema.Field(name); ok {
			return name
		}
	}
	return "body"
}

func resolveBody(bodyVal string, bodySet bool, stdin io.Reader) (string, bool, error) {
	if !bodySet {
		return "", false, nil
	}
	if bodyVal == "-" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", true, fmt.Errorf("read stdin for --body -: %w", err)
		}
		return string(b), true, nil
	}
	return bodyVal, true, nil
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

func shouldWriteManaged(managed bool, kind string, existingPath, body string) bool {
	if body == "" {
		return false
	}
	if managed {
		return true
	}
	return kind == data.KindDocument && existingPath == ""
}

func syncManagedCLI(store *data.Store, rec *data.Record, managed bool, body string) (*data.Record, error) {
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
	if existing != "" && !data.IsManagedPath(root, existing) && !managed {
		return rec, nil
	}
	if !shouldWriteManaged(managed, rec.Kind, existing, body) && !data.IsManagedPath(root, existing) {
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

func mtmNounCommands(g globalOpts) []oden.Command {
	cmds := make([]oden.Command, 0, len(nounSpecs))
	for _, spec := range nounSpecs {
		cmds = append(cmds, buildNounCommand(spec, g))
	}
	return cmds
}

func buildNounCommand(spec nounSpec, g globalOpts) oden.Command {
	noun := spec.name
	kind := spec.kind

	listParams := []oden.Parameter{
		{Name: "query", Type: "string", Description: "Filter query string"},
		{Name: "mode", Type: "string", Default: "substring", Enum: []string{"substring", "fuzzy", "regex"}, Description: "Query match mode"},
		{Name: "field", Type: "string", Repeatable: true, Description: "Filter by field value (name=value)"},
		{Name: "archived", Type: "bool", Description: "Include archived records"},
		{Name: "starred", Type: "bool", Description: "Show only starred records"},
		paramFormatJSON,
	}
	formatOnly := []oden.Parameter{paramFormatJSON}
	createParams := []oden.Parameter{
		{Name: "title", Type: "string", Description: "Record title"},
		{Name: "body", Type: "string", Description: "Body text (or - to read stdin)"},
		{Name: "field", Type: "string", Repeatable: true, Description: "Field assignment (name=value)"},
		{Name: "managed", Type: "bool", Description: "Write body to a managed file"},
		paramFormatJSON,
	}

	statusVerb := func(name, summary string, fn func(*data.Store) func(core.Ref, core.Actor) (*data.Record, *data.ChangeSet, error)) oden.Command {
		return oden.Command{
			Name:       name,
			Summary:    summary,
			Usage:      "[<id>] [--format json|table]",
			Parameters: formatOnly,
			Handle: func(c *oden.Ctx) error {
				p, err := c.Parse(formatOnly)
				if err != nil {
					return err
				}
				pos := p.Positional()
				use := fmt.Sprintf("usage: mtm %s %s <id>", noun, name)
				if len(pos) > 1 {
					return errors.New(use)
				}
				query := ""
				if len(pos) == 1 {
					query = pos[0]
				}
				store, err := openStoreWithSeed(g)
				if err != nil {
					return err
				}
				defer store.Close()
				id, err := resolveRecordID(c.Context, store, kind, query, c.Stdin, use)
				if err != nil {
					return err
				}
				rec, _, err := fn(store)(core.Ref{Kind: kind, ID: id}, cliActor)
				if err != nil {
					return err
				}
				return printRecordOne(c.Stdout, store, kind, recordFormat(p), rec)
			},
		}
	}

	children := []oden.Command{
		{
			Name:       "list",
			Summary:    fmt.Sprintf("list %s records", noun),
			Usage:      "[--query Q] [--mode substring|fuzzy|regex] [--field k=v ...] [--archived] [--starred] [--format json|table]",
			Parameters: listParams,
			Handle: func(c *oden.Ctx) error {
				p, err := c.Parse(listParams)
				if err != nil {
					return err
				}
				if len(p.Positional()) != 0 {
					return fmt.Errorf("usage: mtm %s list [--query Q] [--mode substring|fuzzy|regex] [--field k=v ...] [--archived] [--starred] [--format json|table]", noun)
				}
				store, err := openStoreWithSeed(g)
				if err != nil {
					return err
				}
				defer store.Close()
				return recordListParsed(store, kind, p, c.Stdout)
			},
		},
		{
			Name:       "get",
			Summary:    fmt.Sprintf("get one %s by ID or title", noun),
			Usage:      "[<id>] [--format json|table]",
			Parameters: formatOnly,
			Handle: func(c *oden.Ctx) error {
				p, err := c.Parse(formatOnly)
				if err != nil {
					return err
				}
				pos := p.Positional()
				use := fmt.Sprintf("usage: mtm %s get <id>", noun)
				if len(pos) > 1 {
					return errors.New(use)
				}
				query := ""
				if len(pos) == 1 {
					query = pos[0]
				}
				store, err := openStoreWithSeed(g)
				if err != nil {
					return err
				}
				defer store.Close()
				id, err := resolveRecordID(c.Context, store, kind, query, c.Stdin, use)
				if err != nil {
					return err
				}
				rec, err := store.Get(core.Ref{Kind: kind, ID: id})
				if err != nil {
					return err
				}
				return printRecordOne(c.Stdout, store, kind, recordFormat(p), rec)
			},
		},
		{
			Name:       "create",
			Aliases:    []string{"add"},
			Summary:    fmt.Sprintf("create a new %s", noun),
			Usage:      `--title "..." [--body "..."|-] [--field k=v ...] [--managed] [--format json|table]`,
			Parameters: createParams,
			Handle: func(c *oden.Ctx) error {
				p, err := c.Parse(createParams)
				if err != nil {
					return err
				}
				if len(p.Positional()) != 0 {
					return fmt.Errorf(`usage: mtm %s create --title "..." [--body "..."|-] [--field k=v ...]`, noun)
				}
				store, err := openStoreWithSeed(g)
				if err != nil {
					return err
				}
				defer store.Close()
				return nounCreate(store, kind, p, c.Stdin, c.Stdout)
			},
		},
		{
			Name:       "edit",
			Aliases:    []string{"set", "patch"},
			Summary:    fmt.Sprintf("edit an existing %s", noun),
			Usage:      `[<id>] [--title "..."] [--body "..."|-] [--field k=v ...] [--managed] [--format json|table]`,
			Parameters: createParams,
			Handle: func(c *oden.Ctx) error {
				p, err := c.Parse(createParams)
				if err != nil {
					return err
				}
				pos := p.Positional()
				use := fmt.Sprintf(`usage: mtm %s edit <id> [--title "..."] [--body "..."|-] [--field k=v ...]`, noun)
				if len(pos) > 1 {
					return errors.New(use)
				}
				query := ""
				if len(pos) == 1 {
					query = pos[0]
				}
				store, err := openStoreWithSeed(g)
				if err != nil {
					return err
				}
				defer store.Close()
				id, err := resolveRecordID(c.Context, store, kind, query, c.Stdin, use)
				if err != nil {
					return err
				}
				return nounEdit(store, kind, id, p, c.Stdin, c.Stdout)
			},
		},
		statusVerb("star", fmt.Sprintf("star a %s", noun), func(s *data.Store) func(core.Ref, core.Actor) (*data.Record, *data.ChangeSet, error) {
			return s.Star
		}),
		statusVerb("unstar", fmt.Sprintf("unstar a %s", noun), func(s *data.Store) func(core.Ref, core.Actor) (*data.Record, *data.ChangeSet, error) {
			return s.Unstar
		}),
		statusVerb("archive", fmt.Sprintf("archive a %s", noun), func(s *data.Store) func(core.Ref, core.Actor) (*data.Record, *data.ChangeSet, error) {
			return s.Archive
		}),
		statusVerb("restore", fmt.Sprintf("restore an archived %s", noun), func(s *data.Store) func(core.Ref, core.Actor) (*data.Record, *data.ChangeSet, error) {
			return s.Restore
		}),
		statusVerb("unsnooze", fmt.Sprintf("clear snooze on a %s", noun), func(s *data.Store) func(core.Ref, core.Actor) (*data.Record, *data.ChangeSet, error) {
			return s.Unsnooze
		}),
		{
			Name:       "snooze",
			Summary:    fmt.Sprintf("snooze a %s until a time or duration", noun),
			Usage:      "[<id>] <when> [--format json|table]",
			Parameters: formatOnly,
			Handle: func(c *oden.Ctx) error {
				p, err := c.Parse(formatOnly)
				if err != nil {
					return err
				}
				pos := p.Positional()
				use := fmt.Sprintf("usage: mtm %s snooze <id> <when>", noun)
				var query, whenStr string
				switch len(pos) {
				case 2:
					query, whenStr = pos[0], pos[1]
				case 1:
					whenStr = pos[0]
				default:
					return errors.New(use)
				}
				store, err := openStoreWithSeed(g)
				if err != nil {
					return err
				}
				defer store.Close()
				id, err := resolveRecordID(c.Context, store, kind, query, c.Stdin, use)
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
				return printRecordOne(c.Stdout, store, kind, recordFormat(p), rec)
			},
		},
		{
			Name:    "delete",
			Summary: fmt.Sprintf("delete a %s", noun),
			Usage:   "[<id>]",
			Handle: func(c *oden.Ctx) error {
				p, err := c.Parse(nil)
				if err != nil {
					return err
				}
				pos := p.Positional()
				use := fmt.Sprintf("usage: mtm %s delete <id>", noun)
				if len(pos) > 1 {
					return errors.New(use)
				}
				query := ""
				if len(pos) == 1 {
					query = pos[0]
				}
				store, err := openStoreWithSeed(g)
				if err != nil {
					return err
				}
				defer store.Close()
				id, err := resolveRecordID(c.Context, store, kind, query, c.Stdin, use)
				if err != nil {
					return err
				}
				cs, err := store.Delete(core.Ref{Kind: kind, ID: id}, cliActor)
				if err != nil {
					return err
				}
				return printJSON(c.Stdout, cs)
			},
		},
		{
			Name:    "relate",
			Summary: fmt.Sprintf("relate a %s to another record", noun),
			Usage:   "<id> <rel> <target-kind:target-id>",
			Handle: func(c *oden.Ctx) error {
				p, err := c.Parse(nil)
				if err != nil {
					return err
				}
				pos := p.Positional()
				use := fmt.Sprintf("usage: mtm %s relate <id> <rel> <target-kind:target-id>", noun)
				if len(pos) != 3 {
					return errors.New(use)
				}
				store, err := openStoreWithSeed(g)
				if err != nil {
					return err
				}
				defer store.Close()
				fromID, err := resolveRecordID(c.Context, store, kind, pos[0], c.Stdin, use)
				if err != nil {
					return err
				}
				to, err := resolveRefArg(c.Context, store, pos[2], c.Stdin)
				if err != nil {
					return err
				}
				rel, _, err := store.Relate(data.RelateInput{From: core.Ref{Kind: kind, ID: fromID}, Kind: pos[1], To: to}, cliActor)
				if err != nil {
					return err
				}
				return printJSON(c.Stdout, rel)
			},
		},
		{
			Name:    "unrelate",
			Summary: fmt.Sprintf("remove a relation from a %s", noun),
			Usage:   "<id> <rel> <target-kind:target-id>",
			Handle: func(c *oden.Ctx) error {
				p, err := c.Parse(nil)
				if err != nil {
					return err
				}
				pos := p.Positional()
				use := fmt.Sprintf("usage: mtm %s unrelate <id> <rel> <target-kind:target-id>", noun)
				if len(pos) != 3 {
					return errors.New(use)
				}
				store, err := openStoreWithSeed(g)
				if err != nil {
					return err
				}
				defer store.Close()
				fromID, err := resolveRecordID(c.Context, store, kind, pos[0], c.Stdin, use)
				if err != nil {
					return err
				}
				to, err := resolveRefArg(c.Context, store, pos[2], c.Stdin)
				if err != nil {
					return err
				}
				cs, err := store.Unrelate(core.Ref{Kind: kind, ID: fromID}, pos[1], to, cliActor)
				if err != nil {
					return err
				}
				return printJSON(c.Stdout, cs)
			},
		},
		mtmSetVerb(noun, kind, g),
		mtmRelatedVerb(noun, kind, g),
	}

	if kind == data.KindTask {
		children = append(children, mtmTaskQueueCommands(g)...)
		children = append(children, mtmTaskLineageCommands(g)...)
	}

	if kind == data.KindSchedule {
		children = append(children, mtmSchedulePresetCommand(g))
	}

	if kind == data.KindContext {
		children = append(children, mtmContextProjectCommands(g)...)
	}

	if kind == data.KindPane {
		children = append(children, mtmPaneSearchCommand(g))
		children = append(children, mtmPaneOpsCommands(g)...)
	}

	if kind == data.KindRepo {
		children = append(children, mtmRepoCommands(g)...)
	}

	if kind == data.KindQuestion {
		answerParams := []oden.Parameter{
			{Name: "answer", Type: "string", Description: "Answer text (or pass as a positional argument)"},
			paramFormatJSON,
		}
		children = append(children, oden.Command{
			Name:       "answer",
			Summary:    "answer a question and sync its linked pane ui.page card",
			Usage:      `[<id>] (--answer "..." | <answer>) [--format json|table]`,
			Parameters: answerParams,
			Handle: func(c *oden.Ctx) error {
				p, err := c.Parse(answerParams)
				if err != nil {
					return err
				}
				pos := p.Positional()
				const use = `usage: mtm question answer <id> (--answer "..." | <answer>)`
				var query, answer string
				if p.Has("answer") {
					if len(pos) > 1 {
						return errors.New(use)
					}
					if len(pos) == 1 {
						query = pos[0]
					}
					answer = p.String("answer")
				} else {
					switch len(pos) {
					case 2:
						query, answer = pos[0], pos[1]
					case 1:
						answer = pos[0]
					default:
						return errors.New(use)
					}
				}
				if strings.TrimSpace(answer) == "" {
					return errors.New("answer is required\n" + use)
				}
				store, err := openStoreWithSeed(g)
				if err != nil {
					return err
				}
				defer store.Close()
				id, err := resolveRecordID(c.Context, store, kind, query, c.Stdin, use)
				if err != nil {
					return err
				}
				rec, err := mcpui.New(store).AnswerQuestion(id, answer, cliActor)
				if err != nil {
					return err
				}
				return printRecordOne(c.Stdout, store, kind, recordFormat(p), rec)
			},
		})
	}

	if kind == data.KindExtension {
		discoverParams := []oden.Parameter{
			{Name: "dir", Type: "string", Repeatable: true, Description: "Directory to search before PATH"},
			{Name: "catalog", Type: "bool", Description: "Include uninstalled OFAM catalog tools as available"},
		}
		children = append(children,
			oden.Command{
				Name:       "discover",
				Summary:    "discover installed o-* extensions",
				Usage:      "[--dir DIR] [--catalog]",
				Parameters: discoverParams,
				Handle: func(c *oden.Ctx) error {
					p, err := c.Parse(discoverParams)
					if err != nil {
						return err
					}
					if len(p.Positional()) != 0 {
						return errors.New("usage: mtm extension discover [--dir DIR] [--catalog]")
					}
					store, err := openStoreWithSeed(g)
					if err != nil {
						return err
					}
					defer store.Close()
					res, err := app.DiscoverExtensionsWithCatalog(c.Context, store, p.Strings("dir"), p.Bool("catalog"))
					if err != nil {
						return err
					}
					return printJSON(c.Stdout, res)
				},
			},
			oden.Command{
				Name:    "run",
				Summary: "run an extension command as a recorded run",
				Usage:   "[--format json|table] <name> [args...]",
				Handle: func(c *oden.Ctx) error {
					format := "json"
					rest := c.Args
					for len(rest) > 0 && strings.HasPrefix(rest[0], "-") {
						arg := rest[0]
						if arg == "--" {
							rest = rest[1:]
							break
						}
						if arg == "--json" {
							format = "json"
							rest = rest[1:]
							continue
						}
						if val, ok := strings.CutPrefix(arg, "--format="); ok {
							if val != "json" && val != "table" {
								return fmt.Errorf("--format %s is not json or table", val)
							}
							format = val
							rest = rest[1:]
							continue
						}
						if arg == "--format" && len(rest) >= 2 {
							val := rest[1]
							if val != "json" && val != "table" {
								return fmt.Errorf("--format %s is not json or table", val)
							}
							format = val
							rest = rest[2:]
							continue
						}
						break
					}
					if len(rest) < 1 {
						return errors.New("usage: mtm extension run <name> [args...]")
					}
					store, err := openStoreWithSeed(g)
					if err != nil {
						return err
					}
					defer store.Close()
					rec, err := app.RunExtension(c.Context, store, rest[0], rest[1:], cliActor)
					if err != nil {
						return err
					}
					return printRecordOne(c.Stdout, store, data.KindRun, format, rec)
				},
			},
			mtmExtensionCatalogCommand(g),
			mtmExtensionInstallCommand(g),
		)
	}

	return oden.Command{
		Name:     spec.name,
		Aliases:  spec.aliases,
		Summary:  spec.summary,
		Category: "Records",
		Audience: oden.AudienceHuman,
		Children: children,
	}
}

func printRecordOne(w io.Writer, store *data.Store, kind, format string, rec *data.Record) error {
	if format == "table" {
		return printRecords(w, store, kind, format, []data.Record{*rec})
	}
	return printJSON(w, rec)
}

func nounCreate(store *data.Store, kind string, p oden.Parsed, stdin io.Reader, w io.Writer) error {
	schema := schemaOf(store, kind)
	fields, err := parseFieldAssignments(schema, p.Strings("field"))
	if err != nil {
		return err
	}
	if title := p.String("title"); title != "" {
		name := "title"
		if _, ok := schema.Field("title"); !ok && schema.SummaryField != "" {
			name = schema.SummaryField
		}
		fields[name] = title
	}
	body, hasBody, err := resolveBody(p.String("body"), p.Has("body"), stdin)
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
		rec, err = syncManagedCLI(store, rec, p.Bool("managed"), body)
		if err != nil {
			return err
		}
	}
	return printRecordOne(w, store, kind, recordFormat(p), rec)
}

func nounEdit(store *data.Store, kind, id string, p oden.Parsed, stdin io.Reader, w io.Writer) error {
	schema := schemaOf(store, kind)
	fields, err := parseFieldAssignments(schema, p.Strings("field"))
	if err != nil {
		return err
	}
	if title := p.String("title"); title != "" {
		name := "title"
		if _, ok := schema.Field("title"); !ok && schema.SummaryField != "" {
			name = schema.SummaryField
		}
		fields[name] = title
	}
	body, hasBody, err := resolveBody(p.String("body"), p.Has("body"), stdin)
	if err != nil {
		return err
	}
	if hasBody {
		fields[bodyFieldName(schema)] = body
	}
	managed := p.Bool("managed")
	if len(fields) == 0 && !managed {
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
	if hasBody || managed {
		rec, err = syncManagedCLI(store, rec, managed, effectiveBody)
		if err != nil {
			return err
		}
	}
	return printRecordOne(w, store, kind, recordFormat(p), rec)
}

func mtmBindingCommand() oden.Command {
	listParams := []oden.Parameter{
		{Name: "all", Type: "bool", Description: "Include hidden and class bindings"},
	}
	return oden.Command{
		Name:     "binding",
		Aliases:  []string{"bindings"},
		Summary:  "list and check key bindings",
		Category: "Configuration",
		Handle: func(c *oden.Ctx) error {
			sub := c.Sub()
			sub.Add(
				oden.Command{
					Name:       "list",
					Summary:    "list effective key bindings",
					Usage:      "[--all] [--json]",
					Parameters: listParams,
					Handle: func(sc *oden.Ctx) error {
						p, err := sc.Parse(listParams)
						if err != nil {
							return err
						}
						if len(p.Positional()) != 0 {
							return errors.New("usage: mtm binding list [--all] [--json]")
						}
						v, err := app.Inspect(app.InspectBindings, configDir())
						if err != nil {
							return err
						}
						rows, _ := v.([]core.Binding)
						if !p.Bool("all") {
							filtered := make([]core.Binding, 0, len(rows))
							for _, b := range rows {
								if b.Hidden || b.Class != "" {
									continue
								}
								filtered = append(filtered, b)
							}
							rows = filtered
						}
						if p.Format() == "json" {
							return printJSON(sc.Stdout, rows)
						}
						_, err = io.WriteString(sc.Stdout, app.InspectText(rows))
						return err
					},
				},
				oden.Command{
					Name:    "check",
					Summary: "validate key bindings and report errors",
					Usage:   "[--json]",
					Handle: func(sc *oden.Ctx) error {
						p, err := sc.Parse(nil)
						if err != nil {
							return err
						}
						if len(p.Positional()) != 0 {
							return errors.New("usage: mtm binding check [--json]")
						}
						rep, err := app.CheckBindings(configDir())
						if err != nil {
							return err
						}
						if p.Format() == "json" {
							if err := printJSON(sc.Stdout, rep); err != nil {
								return err
							}
						} else {
							fmt.Fprintf(sc.Stdout, "bindings: %d total, %d errors, %d warnings\n", rep.Total, len(rep.Errors), len(rep.Warnings))
							for _, e := range rep.Errors {
								fmt.Fprintf(sc.Stdout, "ERROR [%s %s %s] %s: %s\n", e.Mode, e.Scope, e.Keys, e.Command, e.Message)
							}
						}
						if len(rep.Errors) > 0 {
							return fmt.Errorf("binding check found %d error(s)", len(rep.Errors))
						}
						return nil
					},
				},
			)
			if len(c.Args) == 1 && (c.Args[0] == "-h" || c.Args[0] == "--help") {
				sub.Help(c.Stdout)
				return nil
			}
			sub.Default = func(_ context.Context, _ []string) error {
				return errors.New("usage: mtm binding list [--all] [--json] | mtm binding check [--json]")
			}
			return sub.Run(c.Context)
		},
	}
}
